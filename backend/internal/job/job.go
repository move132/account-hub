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

	"account-hub/internal/platform/security"
)

var (
	ErrNotFound            = errors.New("job not found")
	ErrActive              = errors.New("active job cannot be deleted")
	ErrNoSelection         = errors.New("no jobs selected")
	ErrIdempotencyConflict = errors.New("idempotency key reused with different request")
)

type Job struct {
	ID          string `json:"id"`
	TargetKind  string `json:"target_kind"`
	Action      string `json:"action"`
	State       string `json:"state"`
	Total       int    `json:"total"`
	Pending     int    `json:"pending"`
	Running     int    `json:"running"`
	Succeeded   int    `json:"succeeded"`
	Failed      int    `json:"failed"`
	Cancelled   int    `json:"cancelled"`
	CreatedBy   string `json:"created_by,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	StartedAt   *int64 `json:"started_at,omitempty"`
	CompletedAt *int64 `json:"completed_at,omitempty"`
	Items       []Item `json:"items,omitempty"`
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
	db        *sql.DB
	logger    *slog.Logger
	mu        sync.RWMutex
	executors map[string]registeredExecutor
	wake      map[string]chan struct{}
}

func NewManager(db *sql.DB, logger *slog.Logger) *Manager {
	return &Manager{db: db, logger: logger, executors: make(map[string]registeredExecutor), wake: map[string]chan struct{}{
		"token": make(chan struct{}, 1), "dreamina": make(chan struct{}, 1),
	}}
}

func (m *Manager) Register(targetKind, action string, executor Executor, delay Delay) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.executors[targetKind+":"+action] = registeredExecutor{execute: executor, delay: delay}
}

func RequestHash(value any) string {
	body, _ := json.Marshal(value)
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}

func (m *Manager) Create(ctx context.Context, targetKind, action, actorID, route, idempotencyKey, requestHash string, targets []Target) (Job, bool, error) {
	if len(targets) == 0 {
		return Job{}, false, errors.New("至少选择一个目标")
	}
	if _, found := m.executor(targetKind, action); !found {
		return Job{}, false, fmt.Errorf("不支持的批量动作: %s", action)
	}
	if idempotencyKey != "" {
		_, _ = m.db.ExecContext(ctx, "DELETE FROM idempotency_keys WHERE expires_at <= ?", time.Now().UTC().UnixMilli())
		var existingHash, jobID string
		err := m.db.QueryRowContext(ctx, `SELECT request_hash, response_body FROM idempotency_keys
            WHERE actor_id = ? AND route = ? AND key = ? AND expires_at > ?`, actorID, route, idempotencyKey, time.Now().UTC().UnixMilli()).Scan(&existingHash, &jobID)
		if err == nil {
			if existingHash != requestHash {
				return Job{}, false, ErrIdempotencyConflict
			}
			job, err := m.Get(ctx, jobID, true)
			return job, true, err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return Job{}, false, err
		}
	}

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
			_ = tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM job_items i JOIN jobs j ON j.id = i.job_id
                WHERE i.target_id = ? AND j.target_kind = ? AND j.action = ?
                AND i.state IN ('queued', 'running') AND j.state IN ('queued', 'running')`, target.ID, targetKind, action).Scan(&active)
		}
		if active > 0 {
			continue
		}
		itemID, err := security.RandomID("item")
		if err != nil {
			return Job{}, false, err
		}
		payload, _ := json.Marshal(target.Payload)
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
		return Job{}, false, errors.New("所选目标已有相同的执行中任务")
	}
	if created != len(targets) {
		if _, err := tx.ExecContext(ctx, "UPDATE jobs SET total = ?, pending = ? WHERE id = ?", created, created, jobID); err != nil {
			return Job{}, false, err
		}
	}
	if idempotencyKey != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO idempotency_keys
            (actor_id, route, key, request_hash, status_code, response_body, expires_at, created_at)
            VALUES(?, ?, ?, ?, 202, ?, ?, ?)`, actorID, route, idempotencyKey, requestHash, jobID,
			time.Now().UTC().Add(24*time.Hour).UnixMilli(), now); err != nil {
			return Job{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Job{}, false, err
	}
	m.signal(targetKind)
	job, err := m.Get(ctx, jobID, true)
	return job, false, err
}

func (m *Manager) Start(ctx context.Context, scanInterval time.Duration, concurrency int) {
	_, _ = m.db.ExecContext(ctx, "UPDATE job_items SET state = 'queued', started_at = NULL WHERE state = 'running'")
	_, _ = m.db.ExecContext(ctx, "UPDATE jobs SET state = 'queued', running = 0, pending = total - succeeded - failed - cancelled WHERE state = 'running'")
	if concurrency < 1 {
		concurrency = 1
	}
	var workers sync.WaitGroup
	for _, targetKind := range []string{"token", "dreamina"} {
		for range concurrency {
			workers.Add(1)
			go func() {
				defer workers.Done()
				m.runLane(ctx, scanInterval, targetKind)
			}()
		}
	}
	workers.Wait()
}

func (m *Manager) runLane(ctx context.Context, scanInterval time.Duration, targetKind string) {
	ticker := time.NewTicker(scanInterval)
	defer ticker.Stop()
	for {
		worked := m.processOne(ctx, targetKind)
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-m.wake[targetKind]:
		}
	}
}

func (m *Manager) processOne(ctx context.Context, lane string) bool {
	item, targetKind, action, found, err := m.claim(ctx, lane)
	if err != nil {
		m.logger.Error("claim job item", "error", err)
		return false
	}
	if !found {
		return false
	}
	registered, found := m.executor(targetKind, action)
	var executeErr error
	started := time.Now()
	m.logger.Info("job item started", "job_id", item.JobID, "item_id", item.ID, "target_kind", targetKind, "action", action, "target_id", item.TargetID, "target_label", item.TargetLabel, "attempt", item.Attempts+1)
	if !found {
		executeErr = errors.New("job executor unavailable")
	} else {
		executeErr = registered.execute(ctx, item.TargetID, json.RawMessage(item.PayloadJSON))
	}
	if executeErr != nil {
		code := "EXECUTION_FAILED"
		var coded interface{ ErrorCode() string }
		if errors.As(executeErr, &coded) && coded.ErrorCode() != "" {
			code = coded.ErrorCode()
		}
		m.logger.Warn("job item failed", "job_id", item.JobID, "item_id", item.ID, "target_kind", targetKind, "action", action, "target_id", item.TargetID, "target_label", item.TargetLabel, "error_code", code, "error", executeErr, "elapsed_ms", time.Since(started).Milliseconds())
	} else {
		m.logger.Info("job item succeeded", "job_id", item.JobID, "item_id", item.ID, "target_kind", targetKind, "action", action, "target_id", item.TargetID, "target_label", item.TargetLabel, "elapsed_ms", time.Since(started).Milliseconds())
	}
	if err := m.finishItem(ctx, item, executeErr); err != nil {
		m.logger.Error("finish job item", "job_id", item.JobID, "item_id", item.ID, "error", err)
	}
	if found && registered.delay != nil {
		delay := registered.delay(ctx)
		if delay > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(delay):
			}
		}
	}
	return true
}

func (m *Manager) claim(ctx context.Context, targetKindFilter string) (Item, string, string, bool, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, "", "", false, err
	}
	defer tx.Rollback()
	var item Item
	var targetID sql.NullString
	var targetKind, action string
	err = tx.QueryRowContext(ctx, `SELECT i.id, i.job_id, i.target_id, i.target_label, i.payload_json,
        i.state, i.attempts, j.target_kind, j.action FROM job_items i JOIN jobs j ON j.id = i.job_id
		WHERE i.state = 'queued' AND j.state IN ('queued', 'running') AND j.target_kind = ?
		ORDER BY j.created_at, i.rowid LIMIT 1`, targetKindFilter).
		Scan(&item.ID, &item.JobID, &targetID, &item.TargetLabel, &item.PayloadJSON, &item.State, &item.Attempts, &targetKind, &action)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, "", "", false, nil
	}
	if err != nil {
		return Item{}, "", "", false, err
	}
	item.TargetID = targetID.String
	now := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, "UPDATE job_items SET state = 'running', attempts = attempts + 1, started_at = ? WHERE id = ? AND state = 'queued'", now, item.ID); err != nil {
		return Item{}, "", "", false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state = 'running', started_at = COALESCE(started_at, ?),
        pending = pending - 1, running = running + 1 WHERE id = ?`, now, item.JobID); err != nil {
		return Item{}, "", "", false, err
	}
	if err := tx.Commit(); err != nil {
		return Item{}, "", "", false, err
	}
	return item, targetKind, action, true, nil
}

func (m *Manager) finishItem(ctx context.Context, item Item, executeErr error) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().UnixMilli()
	state, code, message := "succeeded", "", ""
	if executeErr != nil {
		state, code, message = "failed", "EXECUTION_FAILED", executeErr.Error()
		var coded interface{ ErrorCode() string }
		if errors.As(executeErr, &coded) && coded.ErrorCode() != "" {
			code = coded.ErrorCode()
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE job_items SET state = ?, error_code = NULLIF(?, ''),
        error_message = NULLIF(?, ''), completed_at = ? WHERE id = ?`, state, code, message, now, item.ID); err != nil {
		return err
	}
	if state == "succeeded" {
		_, err = tx.ExecContext(ctx, "UPDATE jobs SET running = running - 1, succeeded = succeeded + 1 WHERE id = ?", item.JobID)
	} else {
		_, err = tx.ExecContext(ctx, "UPDATE jobs SET running = running - 1, failed = failed + 1 WHERE id = ?", item.JobID)
	}
	if err != nil {
		return err
	}
	var currentState string
	var pending, running, succeeded, failed, cancelled, total int
	if err := tx.QueryRowContext(ctx, `SELECT state, pending, running, succeeded, failed, cancelled, total FROM jobs WHERE id = ?`, item.JobID).
		Scan(&currentState, &pending, &running, &succeeded, &failed, &cancelled, &total); err != nil {
		return err
	}
	if pending == 0 && running == 0 {
		finalState := "succeeded"
		switch {
		case currentState == "cancelled" || cancelled == total:
			finalState = "cancelled"
		case failed == total:
			finalState = "failed"
		case failed > 0 || cancelled > 0:
			finalState = "partially_succeeded"
		}
		if _, err := tx.ExecContext(ctx, "UPDATE jobs SET state = ?, completed_at = ? WHERE id = ?", finalState, now, item.JobID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *Manager) Get(ctx context.Context, id string, withItems bool) (Job, error) {
	var result Job
	var createdBy sql.NullString
	var started, completed sql.NullInt64
	err := m.db.QueryRowContext(ctx, `SELECT id, target_kind, action, state, total, pending, running,
        succeeded, failed, cancelled, created_by, created_at, started_at, completed_at FROM jobs WHERE id = ?`, id).
		Scan(&result.ID, &result.TargetKind, &result.Action, &result.State, &result.Total, &result.Pending,
			&result.Running, &result.Succeeded, &result.Failed, &result.Cancelled, &createdBy, &result.CreatedAt, &started, &completed)
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
	if withItems {
		items, err := m.items(ctx, id)
		if err != nil {
			return Job{}, err
		}
		result.Items = items
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
	rows, err := m.db.QueryContext(ctx, `SELECT id FROM jobs ORDER BY created_at DESC LIMIT ? OFFSET ?`, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	items := make([]Job, 0, len(ids))
	for _, id := range ids {
		item, err := m.Get(ctx, id, false)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, nil
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
		if _, err := tx.ExecContext(ctx, "DELETE FROM idempotency_keys WHERE response_body = ?", id); err != nil {
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

func (m *Manager) Cancel(ctx context.Context, id string) (Job, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET state = 'cancelled', completed_at = ?
        WHERE id = ? AND state IN ('queued', 'running')`, time.Now().UTC().UnixMilli(), id)
	if err != nil {
		return Job{}, err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return Job{}, ErrNotFound
	}
	var count int
	_ = tx.QueryRowContext(ctx, "SELECT COUNT(1) FROM job_items WHERE job_id = ? AND state = 'queued'", id).Scan(&count)
	_, err = tx.ExecContext(ctx, "UPDATE job_items SET state = 'cancelled', completed_at = ? WHERE job_id = ? AND state = 'queued'", time.Now().UTC().UnixMilli(), id)
	if err != nil {
		return Job{}, err
	}
	_, err = tx.ExecContext(ctx, "UPDATE jobs SET pending = pending - ?, cancelled = cancelled + ? WHERE id = ?", count, count, id)
	if err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return m.Get(ctx, id, true)
}

func (m *Manager) executor(kind, action string) (registeredExecutor, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	executor, found := m.executors[kind+":"+action]
	return executor, found
}

func (m *Manager) signal(targetKind string) {
	wake, found := m.wake[targetKind]
	if !found {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}
