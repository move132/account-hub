package job

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"account-hub/internal/idempotency"
	"account-hub/internal/platform/apperror"
	"account-hub/internal/platform/security"
)

var (
	ErrNotFound            = apperror.New(apperror.NotFound, "任务不存在")
	ErrActive              = apperror.New(apperror.Conflict, "任务状态不允许此操作")
	ErrNoSelection         = apperror.New(apperror.Validation, "至少选择一个任务")
	ErrIdempotencyConflict = idempotency.ErrConflict
)

type Job struct {
	ID              string `json:"id"`
	TargetKind      string `json:"target_kind"`
	Action          string `json:"action"`
	State           string `json:"state"`
	CancelRequested bool   `json:"cancel_requested,omitempty"`
	Total           int    `json:"total"`
	Pending         int    `json:"pending"`
	Running         int    `json:"running"`
	Succeeded       int    `json:"succeeded"`
	Failed          int    `json:"failed"`
	Cancelled       int    `json:"cancelled"`
	CreatedBy       string `json:"created_by,omitempty"`
	CreatedAt       int64  `json:"created_at"`
	StartedAt       *int64 `json:"started_at,omitempty"`
	CompletedAt     *int64 `json:"completed_at,omitempty"`
	Items           []Item `json:"items,omitempty"`
}

type Item struct {
	ID           string `json:"id"`
	JobID        string `json:"job_id"`
	TargetID     string `json:"target_id,omitempty"`
	TargetLabel  string `json:"target_label"`
	PayloadJSON  string `json:"-"`
	State        string `json:"state"`
	Attempts     int    `json:"attempts"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	StartedAt    *int64 `json:"started_at,omitempty"`
	CompletedAt  *int64 `json:"completed_at,omitempty"`
	cooldown     time.Duration
	targetKind   string
	action       string
}

// Submission is the application-facing command shared by HTTP and scheduling.
type Submission struct {
	TargetKind, Action, ActorID, Route, IdempotencyKey, RequestHash string
	Targets                                                         []Target
}

func (m *Manager) Submit(ctx context.Context, command Submission) (Job, bool, error) {
	return m.Create(ctx, command.TargetKind, command.Action, command.ActorID, command.Route,
		command.IdempotencyKey, command.RequestHash, command.Targets)
}

type Target struct {
	ID      string
	Label   string
	Payload any
}

type Executor func(context.Context, string, json.RawMessage) error
type Delay func(context.Context) time.Duration

type registeredExecutor struct {
	execute Executor
	delay   Delay
}

type Manager struct {
	db                *sql.DB
	logger            *slog.Logger
	mu                sync.RWMutex
	executors         map[string]registeredExecutor
	wake              map[string]chan struct{}
	runMu             sync.Mutex
	running           bool
	ready             bool
	completionTimeout time.Duration
}

func NewManager(db *sql.DB, logger *slog.Logger) *Manager {
	return &Manager{db: db, logger: logger, executors: make(map[string]registeredExecutor), wake: make(map[string]chan struct{}), completionTimeout: 5 * time.Second}
}

func (m *Manager) Register(targetKind, action string, executor Executor, delay Delay) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.executors[targetKind+":"+action] = registeredExecutor{execute: executor, delay: delay}
	if _, ok := m.wake[targetKind]; !ok {
		m.wake[targetKind] = make(chan struct{}, 1)
	}
}

func RequestHash(value any) string {
	body, _ := json.Marshal(value)
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}

func (m *Manager) Create(ctx context.Context, targetKind, action, actorID, route, idempotencyKey, requestHash string, targets []Target) (Job, bool, error) {
	if len(targets) == 0 {
		return Job{}, false, apperror.New(apperror.Validation, "至少选择一个目标")
	}
	if _, found := m.executor(targetKind, action); !found {
		return Job{}, false, apperror.New(apperror.Validation, fmt.Sprintf("不支持的批量动作: %s", action))
	}
	key, err := idempotency.NormalizeKey(idempotencyKey)
	if err != nil {
		return Job{}, false, err
	}
	idempotencyKey = key
	request := idempotency.Request{ActorID: actorID, Route: route, Key: key, Hash: requestHash}

	jobID, err := security.RandomID("job")
	if err != nil {
		return Job{}, false, err
	}
	now := time.Now().UTC().UnixMilli()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, false, err
	}
	defer tx.Rollback()
	store := idempotency.NewStore(m.db)
	if idempotencyKey != "" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE actor_id=? AND route=? AND key=? AND expires_at<=?`, actorID, route, key, now); err != nil {
			return Job{}, false, err
		}
		existing, found, err := store.LookupTx(ctx, tx, request)
		if err != nil {
			return Job{}, false, err
		}
		if found {
			if existing.ResourceType != "job" || existing.ResourceID == "" {
				return Job{}, false, idempotency.ErrUncertain
			}
			// Release the single SQLite connection before reading the committed job.
			if err := tx.Rollback(); err != nil {
				return Job{}, false, err
			}
			result, err := m.Get(ctx, existing.ResourceID, true)
			return result, true, err
		}
	}
	var actor any
	if actorID != "" {
		actor = actorID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO jobs
        (id, target_kind, action, state, total, pending, created_by, created_at)
        VALUES(?, ?, ?, 'queued', ?, ?, ?, ?)`, jobID, targetKind, action, len(targets), len(targets), actor, now); err != nil {
		return Job{}, false, err
	}
	created := 0
	for _, target := range targets {
		var active int
		if target.ID != "" {
			err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM job_items i JOIN jobs j ON j.id = i.job_id
                WHERE i.target_id = ? AND j.target_kind = ? AND j.action = ?
                AND i.state IN ('queued', 'running') AND j.state IN ('queued', 'running')`, target.ID, targetKind, action).Scan(&active)
			if err != nil {
				return Job{}, false, err
			}
		}
		if active > 0 {
			continue
		}
		itemID, err := security.RandomID("item")
		if err != nil {
			return Job{}, false, err
		}
		payload, err := json.Marshal(target.Payload)
		if err != nil {
			return Job{}, false, fmt.Errorf("encode job payload: %w", err)
		}
		if target.Payload == nil {
			payload = []byte("{}")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO job_items
            (id, job_id, target_id, target_label, payload_json, state) VALUES(?, ?, NULLIF(?, ''), ?, ?, 'queued')`,
			itemID, jobID, target.ID, target.Label, string(payload)); err != nil {
			return Job{}, false, err
		}
		created++
	}
	if created == 0 {
		return Job{}, false, apperror.New(apperror.Conflict, "所选目标已有相同的执行中任务")
	}
	if created != len(targets) {
		if _, err := tx.ExecContext(ctx, "UPDATE jobs SET total = ?, pending = ? WHERE id = ?", created, created, jobID); err != nil {
			return Job{}, false, err
		}
	}
	if idempotencyKey != "" {
		if err := store.PutTx(ctx, tx, request, idempotency.Result{Status: 202, ResourceType: "job", ResourceID: jobID}); err != nil {
			return Job{}, false, err
		}
	}
	// Imports enter through HTTP idempotency. Persist their operation reference
	// with the job, before the response middleware can be interrupted.
	recoveryBody, err := json.Marshal(map[string]any{"code": 202, "success": true, "data": map[string]any{
		"job":      Job{ID: jobID, TargetKind: targetKind, Action: action, State: "queued", Total: created, Pending: created, CreatedBy: actorID, CreatedAt: now},
		"accepted": created, "idempotent_replay": true}, "msg": "任务已创建", "ts": now})
	if err != nil {
		return Job{}, false, err
	}
	if err := idempotency.AttachResourceTx(ctx, tx, "job", jobID, string(recoveryBody)); err != nil {
		return Job{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return Job{}, false, err
	}
	m.signal(targetKind)
	job, err := m.Get(ctx, jobID, true)
	return job, false, err
}

const jobColumns = `id, target_kind, action, state, total, pending, running,
    succeeded, failed, cancelled, created_by, created_at, started_at, completed_at, cancel_requested`

type rowScanner interface{ Scan(...any) error }

func scanJob(row rowScanner) (Job, error) {
	var result Job
	var createdBy sql.NullString
	var started, completed sql.NullInt64
	err := row.Scan(&result.ID, &result.TargetKind, &result.Action, &result.State, &result.Total, &result.Pending, &result.Running,
		&result.Succeeded, &result.Failed, &result.Cancelled, &createdBy, &result.CreatedAt, &started, &completed, &result.CancelRequested)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	result.CreatedBy = createdBy.String
	if started.Valid {
		result.StartedAt = &started.Int64
	}
	if completed.Valid {
		result.CompletedAt = &completed.Int64
	}
	return result, nil
}
func (m *Manager) Get(ctx context.Context, id string, withItems bool) (Job, error) {
	result, err := scanJob(m.db.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE id=?", id))
	if err != nil {
		return Job{}, err
	}
	if withItems {
		result.Items, err = m.items(ctx, id)
		if err != nil {
			return Job{}, err
		}
	}
	return result, nil
}
func (m *Manager) List(ctx context.Context, page, pageSize int) ([]Job, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 500 {
		pageSize = 20
	}
	var total int
	if err := m.db.QueryRowContext(ctx, "SELECT COUNT(1) FROM jobs").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := m.db.QueryContext(ctx, "SELECT "+jobColumns+" FROM jobs ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?", pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := make([]Job, 0, pageSize)
	for rows.Next() {
		item, err := scanJob(rows)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, item)
	}
	return result, total, rows.Err()
}

// Replay is intentionally called before resolving current target state. A
// completed delete command remains replayable after its targets cease to exist.
func (m *Manager) Replay(ctx context.Context, actor, route, key, hash string) (Job, bool, error) {
	key, err := idempotency.NormalizeKey(key)
	if err != nil {
		return Job{}, false, err
	}
	if key == "" {
		return Job{}, false, nil
	}
	record, found, err := idempotency.NewStore(m.db).Lookup(ctx, idempotency.Request{ActorID: actor, Route: route, Key: key, Hash: hash})
	if err != nil || !found {
		return Job{}, false, err
	}
	if record.ResourceType != "job" || record.ResourceID == "" {
		return Job{}, false, idempotency.ErrUncertain
	}
	result, err := m.Get(ctx, record.ResourceID, true)
	return result, true, err
}

func (m *Manager) items(ctx context.Context, jobID string) ([]Item, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT id, job_id, COALESCE(target_id, ''), target_label, payload_json,
        state, attempts, COALESCE(error_code, ''), COALESCE(error_message, ''), started_at, completed_at
        FROM job_items WHERE job_id = ? ORDER BY rowid`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		var item Item
		var started, completed sql.NullInt64
		if err := rows.Scan(&item.ID, &item.JobID, &item.TargetID, &item.TargetLabel, &item.PayloadJSON,
			&item.State, &item.Attempts, &item.ErrorCode, &item.ErrorMessage, &started, &completed); err != nil {
			return nil, err
		}
		if started.Valid {
			item.StartedAt = &started.Int64
		}
		if completed.Valid {
			item.CompletedAt = &completed.Int64
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	_, err := m.DeleteMany(ctx, []string{id})
	return err
}

func (m *Manager) DeleteMany(ctx context.Context, ids []string) (int, error) {
	unique := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return 0, ErrNoSelection
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, id := range unique {
		var state string
		var running int
		if err := tx.QueryRowContext(ctx, "SELECT state, running FROM jobs WHERE id = ?", id).Scan(&state, &running); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return 0, ErrNotFound
			}
			return 0, err
		}
		if state == "queued" || state == "running" || running > 0 {
			return 0, ErrActive
		}
	}
	for _, id := range unique {
		if _, err := tx.ExecContext(ctx, "DELETE FROM idempotency_keys WHERE resource_type = 'job' AND resource_id = ?", id); err != nil {
			return 0, err
		}
		result, err := tx.ExecContext(ctx, "DELETE FROM jobs WHERE id = ?", id)
		if err != nil {
			return 0, err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		if changed == 0 {
			return 0, ErrNotFound
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(unique), nil
}

func (m *Manager) executor(kind, action string) (registeredExecutor, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	executor, found := m.executors[kind+":"+action]
	return executor, found
}

func (m *Manager) signal(targetKind string) {
	m.mu.RLock()
	wake, found := m.wake[targetKind]
	m.mu.RUnlock()
	if !found {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}
