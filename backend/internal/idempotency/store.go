package idempotency

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"account-hub/internal/platform/security"
	platformsqlite "account-hub/internal/platform/sqlite"
)

var (
	ErrConflict   = errors.New("idempotency key reused with different request")
	ErrUncertain  = errors.New("previous operation outcome requires reconciliation")
	ErrInvalidKey = errors.New("idempotency key exceeds 200 characters")
)

// Request identifies an application command, independently of HTTP response capture.
type Request struct{ ActorID, Route, Key, Hash string }
type Record struct {
	Request
	State, OwnerToken, ResourceType, ResourceID string
	Status                                      int
	Body                                        string
	Headers                                     http.Header
	LeaseExpiresAt                              int64
}
type Result struct {
	Status                   int
	Body                     string
	Headers                  http.Header
	ResourceType, ResourceID string
}
type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func NormalizeKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if len(key) > 200 {
		return "", ErrInvalidKey
	}
	return key, nil
}

func lookup(ctx context.Context, db platformsqlite.DBTX, request Request) (Record, bool, error) {
	record := Record{Request: request}
	var hash, headers string
	err := db.QueryRowContext(ctx, `SELECT request_hash, state, owner_token, status_code,
        response_body, response_headers, resource_type, resource_id, lease_expires_at
        FROM idempotency_keys WHERE actor_id=? AND route=? AND key=? AND expires_at>?`,
		request.ActorID, request.Route, request.Key, time.Now().UTC().UnixMilli()).Scan(
		&hash, &record.State, &record.OwnerToken, &record.Status, &record.Body, &headers,
		&record.ResourceType, &record.ResourceID, &record.LeaseExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	if hash != request.Hash {
		return Record{}, true, ErrConflict
	}
	if err := json.Unmarshal([]byte(headers), &record.Headers); err != nil {
		return Record{}, true, err
	}
	if record.State == "processing" && record.LeaseExpiresAt <= time.Now().UTC().UnixMilli() {
		record.State = "uncertain"
	}
	return record, true, nil
}
func (s *Store) Lookup(ctx context.Context, request Request) (Record, bool, error) {
	return lookup(ctx, s.db, request)
}
func (s *Store) LookupTx(ctx context.Context, tx *sql.Tx, request Request) (Record, bool, error) {
	return lookup(ctx, tx, request)
}

// Reserve never retries an operation with an unknown outcome. The lease describes
// liveness, not permission to repeat side effects after a process crash.
func (s *Store) Reserve(ctx context.Context, request Request) (Record, bool, error) {
	key, err := NormalizeKey(request.Key)
	if err != nil {
		return Record{}, false, err
	}
	request.Key = key
	owner, err := security.RandomID("idem")
	if err != nil {
		return Record{}, false, err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, false, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE actor_id=? AND route=? AND key=? AND expires_at<=?`,
		request.ActorID, request.Route, request.Key, now.UnixMilli()); err != nil {
		return Record{}, false, err
	}
	record, found, err := lookup(ctx, tx, request)
	if err != nil {
		return Record{}, false, err
	}
	if found {
		return record, false, nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO idempotency_keys
        (actor_id,route,key,request_hash,status_code,response_body,expires_at,created_at,state,lease_expires_at,owner_token)
        VALUES(?,?,?,?,0,'',?,?,'processing',?,?)`, request.ActorID, request.Route, request.Key, request.Hash,
		now.Add(24*time.Hour).UnixMilli(), now.UnixMilli(), now.Add(2*time.Minute).UnixMilli(), owner)
	if err != nil {
		return Record{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return Record{}, false, err
	}
	return Record{Request: request, State: "processing", OwnerToken: owner, LeaseExpiresAt: now.Add(2 * time.Minute).UnixMilli()}, true, nil
}

// PutTx records a completed command in the same transaction as its business write.
func (s *Store) PutTx(ctx context.Context, tx *sql.Tx, request Request, result Result) error {
	headers, err := json.Marshal(result.Headers)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, `INSERT INTO idempotency_keys
        (actor_id,route,key,request_hash,status_code,response_body,expires_at,created_at,state,resource_type,resource_id,response_headers)
        VALUES(?,?,?,?,?,?,?,?,'completed',?,?,?)`, request.ActorID, request.Route, request.Key, request.Hash, result.Status, result.Body,
		now.Add(24*time.Hour).UnixMilli(), now.UnixMilli(), result.ResourceType, result.ResourceID, string(headers))
	return err
}
func complete(ctx context.Context, db platformsqlite.DBTX, record Record, result Result) error {
	headers, err := json.Marshal(result.Headers)
	if err != nil {
		return err
	}
	changed, err := db.ExecContext(ctx, `UPDATE idempotency_keys SET state='completed',status_code=?,response_body=?,response_headers=?,
        resource_type=CASE WHEN ?<>'' THEN ? ELSE resource_type END,
        resource_id=CASE WHEN ?<>'' THEN ? ELSE resource_id END,lease_expires_at=0
        WHERE actor_id=? AND route=? AND key=? AND owner_token=? AND state IN ('processing','completed')
        AND NOT (state='completed' AND status_code>=200 AND status_code<400 AND ?>=400)`, result.Status, result.Body, string(headers),
		result.ResourceType, result.ResourceType, result.ResourceID, result.ResourceID, record.ActorID, record.Route, record.Key, record.OwnerToken, result.Status)
	if err != nil {
		return err
	}
	count, err := changed.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		saved, found, err := lookup(ctx, db, record.Request)
		if err != nil {
			return err
		}
		if found && saved.OwnerToken == record.OwnerToken && saved.State == "completed" && saved.Status >= 200 && saved.Status < 400 && result.Status >= 400 {
			return nil // A response-rendering failure cannot undo a committed command.
		}
		return ErrUncertain
	}
	return nil
}
func (s *Store) Complete(ctx context.Context, record Record, result Result) error {
	return complete(ctx, s.db, record, result)
}
func (s *Store) MarkUncertain(ctx context.Context, record Record) error {
	_, err := s.db.ExecContext(ctx, `UPDATE idempotency_keys SET state='uncertain',lease_expires_at=0
        WHERE actor_id=? AND route=? AND key=? AND owner_token=? AND state='processing'`,
		record.ActorID, record.Route, record.Key, record.OwnerToken)
	return err
}

type recordContextKey struct{}

func WithRecord(ctx context.Context, record Record) context.Context {
	return context.WithValue(ctx, recordContextKey{}, record)
}
func RecordFromContext(ctx context.Context) (Record, bool) {
	record, ok := ctx.Value(recordContextKey{}).(Record)
	return record, ok
}

// AttachResourceTx lets an asynchronous application command atomically establish
// its durable operation reference before HTTP response rendering can fail.
func AttachResourceTx(ctx context.Context, tx *sql.Tx, kind, id string, body string) error {
	record, ok := RecordFromContext(ctx)
	if !ok {
		return nil
	}
	if kind == "" || id == "" {
		return fmt.Errorf("idempotency resource reference is required")
	}
	return complete(ctx, tx, record, Result{Status: http.StatusAccepted, Body: body,
		Headers: http.Header{"Content-Type": []string{"application/json; charset=utf-8"}}, ResourceType: kind, ResourceID: id})
}

// CompleteResultTx records a middleware-reserved local command inside the domain
// write transaction. A stale owner cannot finalize another request's reservation.
// Response serialization belongs to the application use case, not its repository.
func CompleteResultTx(ctx context.Context, tx *sql.Tx, result Result) error {
	record, ok := RecordFromContext(ctx)
	if !ok {
		return nil
	}
	return complete(ctx, tx, record, result)
}

// PruneExpired is called by periodic application maintenance. Request-time
// cleanup only touches the current key to avoid a global write on every request.
func (s *Store) PruneExpired(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE expires_at<=?`, time.Now().UTC().UnixMilli())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
