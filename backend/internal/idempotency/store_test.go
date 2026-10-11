package idempotency

import (
	"account-hub/internal/audit"
	"account-hub/internal/auth"
	"account-hub/internal/platform/security"
	platformsqlite "account-hub/internal/platform/sqlite"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db, err := platformsqlite.Open(context.Background(), filepath.Join(t.TempDir(), "idempotency.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE test_commands(id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	return NewStore(db), db
}
func TestLocalCommandAndReplayCommitTogether(t *testing.T) {
	store, db := testStore(t)
	request := Request{ActorID: "admin", Route: "create", Key: "one", Hash: "hash"}
	failed := errors.New("injected failure after local mutation")
	if _, _, err := store.InTransaction(context.Background(), request, func(ctx context.Context, tx *sql.Tx) (Result, error) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO test_commands(id) VALUES('one')`); err != nil {
			return Result{}, err
		}
		return Result{}, failed
	}); !errors.Is(err, failed) {
		t.Fatalf("expected rollback failure, got %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM test_commands`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial mutation count=%d err=%v", count, err)
	}
	if _, found, err := store.Lookup(context.Background(), request); err != nil || found {
		t.Fatalf("partial replay found=%v err=%v", found, err)
	}
	calls := 0
	apply := func(ctx context.Context, tx *sql.Tx) (Result, error) {
		calls++
		_, err := tx.ExecContext(ctx, `INSERT INTO test_commands(id) VALUES('one')`)
		return Result{Status: 201, Body: `{"id":"one"}`, ResourceType: "test", ResourceID: "one"}, err
	}
	first, replay, err := store.InTransaction(context.Background(), request, apply)
	if err != nil || replay {
		t.Fatalf("create replay=%v err=%v", replay, err)
	}
	second, replay, err := store.InTransaction(context.Background(), request, apply)
	if err != nil || !replay || calls != 1 || first.Body != second.Body {
		t.Fatalf("replay=%v calls=%d result=%+v err=%v", replay, calls, second, err)
	}
}
func TestUnknownReservationNeverGrantsAnotherExecution(t *testing.T) {
	store, db := testStore(t)
	request := Request{ActorID: "admin", Route: "refresh", Key: "one", Hash: "hash"}
	first, created, err := store.Reserve(context.Background(), request)
	if err != nil || !created {
		t.Fatalf("reserve created=%v err=%v", created, err)
	}
	if _, err := db.Exec(`UPDATE idempotency_keys SET lease_expires_at=0 WHERE key='one'`); err != nil {
		t.Fatal(err)
	}
	existing, created, err := store.Reserve(context.Background(), request)
	if err != nil || created || existing.State != "uncertain" || existing.OwnerToken != first.OwnerToken {
		t.Fatalf("uncertain reservation=%+v created=%v err=%v", existing, created, err)
	}
	different := request
	different.Hash = "other"
	if _, _, err := store.Reserve(context.Background(), different); !errors.Is(err, ErrConflict) {
		t.Fatalf("hash collision %v", err)
	}
}
func TestExpiredOwnerCannotCommitBusinessOrReplay(t *testing.T) {
	store, db := testStore(t)
	request := Request{ActorID: "admin", Route: "create", Key: "one", Hash: "hash"}
	old, _, err := store.Reserve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE idempotency_keys SET expires_at=0 WHERE key='one'`); err != nil {
		t.Fatal(err)
	}
	current, created, err := store.Reserve(context.Background(), request)
	if err != nil || !created || current.OwnerToken == old.OwnerToken {
		t.Fatalf("new owner=%+v created=%v err=%v", current, created, err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO test_commands(id) VALUES('old')`); err != nil {
		t.Fatal(err)
	}
	if err := CompleteResultTx(WithRecord(context.Background(), old), tx, Result{Status: 201, Body: `{}`}); !errors.Is(err, ErrUncertain) {
		t.Fatalf("stale completion %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM test_commands`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale business commit count=%d err=%v", count, err)
	}
}
func authenticatedMiddleware(t *testing.T, db *sql.DB, next http.Handler) http.Handler {
	t.Helper()
	repository := auth.NewRepository(db)
	now := time.Now().UTC().UnixMilli()
	if err := repository.CreateAdmin(context.Background(), auth.Admin{ID: "admin", Username: "admin", PasswordHash: "unused", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateSession(context.Background(), auth.Session{IDHash: security.HashToken("session"), AdminID: "admin", CSRFHash: "unused", CreatedAt: now, LastSeenAt: now, ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	return auth.NewHandler(auth.NewService(repository, time.Hour), audit.NewRepository(db)).Require(New(db).Wrap(next))
}
func invoke(t *testing.T, handler http.Handler, ctx context.Context) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/operation", strings.NewReader(`{"value":1}`)).WithContext(ctx)
	request.Header.Set("Idempotency-Key", "operation-key")
	request.AddCookie(&http.Cookie{Name: "account_session", Value: "session"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
func TestMiddlewareFinalizesAfterRequestCancellationAndReplaysHeaders(t *testing.T) {
	_, db := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	handler := authenticatedMiddleware(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if _, err := db.ExecContext(r.Context(), `INSERT INTO test_commands(id) VALUES('one')`); err != nil {
			t.Error(err)
		}
		cancel()
		w.Header().Set("X-Operation", "one")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"one"}`))
	}))
	first := invoke(t, handler, ctx)
	replay := invoke(t, handler, context.Background())
	if first.Code != 201 || replay.Code != 201 || replay.Body.String() != first.Body.String() || calls != 1 || replay.Header().Get("X-Operation") != "one" || replay.Header().Get("Idempotent-Replay") != "true" {
		t.Fatalf("first=%d replay=%d body=%s calls=%d headers=%v", first.Code, replay.Code, replay.Body.String(), calls, replay.Header())
	}
}
func TestMiddlewarePreservesAtomicLocalResultAfterPanic(t *testing.T) {
	_, db := testStore(t)
	calls := 0
	handler := authenticatedMiddleware(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO test_commands(id) VALUES('one')`); err != nil {
			t.Fatal(err)
		}
		if err := CompleteResultTx(r.Context(), tx, Result{Status: 201, Body: `{"id":"one"}`, Headers: http.Header{"Content-Type": []string{"application/json"}}, ResourceType: "test", ResourceID: "one"}); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		panic("injected crash after business commit")
	}))
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected injected panic")
			}
		}()
		invoke(t, handler, context.Background())
	}()
	replay := invoke(t, handler, context.Background())
	if replay.Code != 201 || calls != 1 || replay.Body.String() != `{"id":"one"}` {
		t.Fatalf("committed replay=%d %s calls=%d", replay.Code, replay.Body.String(), calls)
	}
}
func TestMiddlewareDoesNotRepeatUnknownBusinessOutcome(t *testing.T) {
	_, db := testStore(t)
	if _, err := db.Exec(`CREATE TRIGGER fail_replay_save BEFORE UPDATE ON idempotency_keys WHEN NEW.state='completed' BEGIN SELECT RAISE(FAIL,'injected save failure'); END`); err != nil {
		t.Fatal(err)
	}
	calls := 0
	handler := authenticatedMiddleware(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if _, err := db.ExecContext(r.Context(), `INSERT INTO test_commands(id) VALUES('one')`); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	first := invoke(t, handler, context.Background())
	replay := invoke(t, handler, context.Background())
	if first.Code != 500 || replay.Code != 409 || calls != 1 || !strings.Contains(replay.Body.String(), "IDEMPOTENCY_OUTCOME_UNKNOWN") {
		t.Fatalf("unknown outcome first=%d replay=%d %s calls=%d", first.Code, replay.Code, replay.Body.String(), calls)
	}
}

func TestMiddlewareDoesNotDowngradeCommittedSuccessToHandlerFailure(t *testing.T) {
	_, db := testStore(t)
	calls := 0
	handler := authenticatedMiddleware(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO test_commands(id) VALUES('one')`); err != nil {
			t.Fatal(err)
		}
		if err := CompleteResultTx(r.Context(), tx, Result{Status: 201, Body: `{"id":"one"}`, Headers: http.Header{"Content-Type": []string{"application/json"}}, ResourceType: "test", ResourceID: "one"}); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		http.Error(w, "injected reload failure after commit", http.StatusInternalServerError)
	}))
	first := invoke(t, handler, context.Background())
	replay := invoke(t, handler, context.Background())
	if first.Code != 201 || replay.Code != 201 || first.Body.String() != `{"id":"one"}` || replay.Body.String() != first.Body.String() || calls != 1 {
		t.Fatalf("first=%d %s replay=%d %s calls=%d", first.Code, first.Body.String(), replay.Code, replay.Body.String(), calls)
	}
}
func TestMigrationPreservesLegacyReplayAndAssociatesImportJobs(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, name := range []string{"001_init.sql", "002_dreamina_session_identity.sql"} {
		oldSchema, err := os.ReadFile(filepath.Join("../platform/sqlite/migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(oldSchema)); err != nil {
			t.Fatal(err)
		}
	}
	oldImport := `{"code":202,"success":true,"data":{"job":{"id":"job_import"},"accepted":5}}`
	records := []struct {
		key, body string
		status    int
	}{{"batch", "job_batch", 202}, {"import", oldImport, 202}, {"create", `{"data":{"token":{"id":"tok_1"}}}`, 201}, {"interrupted", "", 0}}
	for _, record := range records {
		if _, err := db.Exec(`INSERT INTO idempotency_keys(actor_id,route,key,request_hash,status_code,response_body,expires_at,created_at) VALUES('admin','route',?,'hash',?,?,?,0)`, record.key, record.status, record.body, time.Now().Add(time.Hour).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	migration, err := os.ReadFile("../platform/sqlite/migrations/003_account_operations.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	for _, input := range records {
		record, found, err := store.Lookup(context.Background(), Request{ActorID: "admin", Route: "route", Key: input.key, Hash: "hash"})
		if err != nil || !found {
			t.Fatalf("migration key=%s found=%v err=%v", input.key, found, err)
		}
		if record.Headers.Get("Content-Type") != "application/json; charset=utf-8" {
			t.Fatalf("missing legacy content type %+v", record)
		}
		switch input.key {
		case "batch":
			if record.ResourceType != "job" || record.ResourceID != "job_batch" || record.Body != "" {
				t.Fatalf("batch %+v", record)
			}
		case "import":
			if record.ResourceType != "job" || record.ResourceID != "job_import" || record.Body != oldImport {
				t.Fatalf("import %+v", record)
			}
		case "interrupted":
			if record.State != "uncertain" {
				t.Fatalf("interrupted %+v", record)
			}
		}
	}
	if _, err := db.Exec(`DELETE FROM idempotency_keys WHERE resource_type='job' AND resource_id='job_import'`); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Lookup(context.Background(), Request{ActorID: "admin", Route: "route", Key: "import", Hash: "hash"}); err != nil || found {
		t.Fatalf("cleanup legacy import found=%v err=%v", found, err)
	}
}

func TestPruneExpiredRemovesOnlyExpiredKeys(t *testing.T) {
	store, db := testStore(t)
	expired := Request{ActorID: "admin", Route: "create", Key: "expired", Hash: "hash"}
	live := Request{ActorID: "admin", Route: "create", Key: "live", Hash: "hash"}
	for _, request := range []Request{expired, live} {
		if _, created, err := store.Reserve(context.Background(), request); err != nil || !created {
			t.Fatalf("reserve key=%s created=%v err=%v", request.Key, created, err)
		}
	}
	if _, err := db.Exec(`UPDATE idempotency_keys SET expires_at=0 WHERE key='expired'`); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.PruneExpired(context.Background())
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM idempotency_keys WHERE key='expired'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expired rows=%d err=%v", count, err)
	}
	if _, found, err := store.Lookup(context.Background(), live); err != nil || !found {
		t.Fatalf("live key found=%v err=%v", found, err)
	}
	if deleted, err := store.PruneExpired(context.Background()); err != nil || deleted != 0 {
		t.Fatalf("repeated prune deleted=%d err=%v", deleted, err)
	}
}
