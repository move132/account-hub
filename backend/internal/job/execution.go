package job

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Execution is stable across recovery of the same item. Executors must tolerate
// at-least-once delivery; ItemID is the operation key and Attempt starts at one.
// The SQLite runner has one owner per database. Multi-process workers are not
// supported: startup recovery requires that the previous process has stopped.
type Execution struct {
	ItemID  string
	Attempt int
}
type executionContextKey struct{}

func ExecutionFromContext(ctx context.Context) (Execution, bool) {
	value, ok := ctx.Value(executionContextKey{}).(Execution)
	return value, ok
}
func (m *Manager) Ready() bool { m.runMu.Lock(); defer m.runMu.Unlock(); return m.ready }

func (m *Manager) Start(ctx context.Context, scanInterval time.Duration, concurrency int) error {
	m.runMu.Lock()
	if m.running {
		m.runMu.Unlock()
		return errors.New("job manager is already running")
	}
	m.running = true
	m.runMu.Unlock()
	defer func() { m.runMu.Lock(); m.running = false; m.ready = false; m.runMu.Unlock() }()
	if scanInterval <= 0 {
		return errors.New("job scan interval must be positive")
	}
	if err := m.recover(ctx); err != nil {
		return fmt.Errorf("recover jobs: %w", err)
	}
	m.runMu.Lock()
	m.ready = true
	m.runMu.Unlock()
	if concurrency < 1 {
		concurrency = 1
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.mu.RLock()
	lanes := make(map[string]<-chan struct{}, len(m.wake))
	for lane, wake := range m.wake {
		lanes[lane] = wake
	}
	m.mu.RUnlock()
	failures := make(chan error, len(lanes)*concurrency)
	var workers sync.WaitGroup
	for lane, wake := range lanes {
		for range concurrency {
			workers.Add(1)
			go func() {
				defer workers.Done()
				if err := m.runLane(ctx, scanInterval, lane, wake); err != nil {
					failures <- err
					cancel()
				}
			}()
		}
	}
	workers.Wait()
	close(failures)
	var result error
	for err := range failures {
		result = errors.Join(result, err)
	}
	return result
}

// recover reconciles items and aggregate counters atomically. A cancellation
// request survives restart and never turns an interrupted item into runnable work.
func (m *Manager) recover(ctx context.Context) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM jobs WHERE state IN ('queued','running') OR running>0 OR (state='cancelled' AND EXISTS (SELECT 1 FROM job_items WHERE job_id=jobs.id AND state IN ('queued','running')))`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	now := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET cancel_requested=1 WHERE state='cancelled'`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE job_items SET state='cancelled',completed_at=?
        WHERE state IN ('queued','running') AND job_id IN (SELECT id FROM jobs WHERE cancel_requested=1)`, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE job_items SET state='queued',started_at=NULL
        WHERE state='running'`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE job_action_limits SET in_flight=0`); err != nil {
		return err
	}
	for _, id := range ids {
		if err := reconcileJob(ctx, tx, id, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *Manager) runLane(ctx context.Context, interval time.Duration, lane string, wake <-chan struct{}) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		worked, err := m.processOne(ctx, lane)
		if err != nil {
			return err
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		case <-wake:
		}
	}
}

func (m *Manager) processOne(ctx context.Context, lane string) (bool, error) {
	item, kind, action, found, err := m.claim(ctx, lane)
	if err != nil {
		if ctx.Err() != nil {
			return false, nil
		}
		return false, fmt.Errorf("claim job item: %w", err)
	}
	if !found {
		return false, nil
	}
	registered, found := m.executor(kind, action)
	started := time.Now()
	m.logger.Info("job item started", "job_id", item.JobID, "item_id", item.ID, "target_kind", kind, "action", action, "attempt", item.Attempts)
	executionCtx := context.WithValue(ctx, executionContextKey{}, Execution{ItemID: item.ID, Attempt: item.Attempts})
	var executeErr error
	if !found {
		executeErr = errors.New("job executor unavailable")
	} else {
		executeErr = executeSafely(executionCtx, registered.execute, item.TargetID, json.RawMessage(item.PayloadJSON))
	}
	// Request cancellation must not cancel the durable acknowledgement too.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), m.completionTimeout)
	defer cancel()
	interrupted := ctx.Err() != nil && errors.Is(executeErr, context.Canceled)
	if err := m.finishWithRetry(cleanupCtx, item, executeErr, interrupted); err != nil {
		return true, fmt.Errorf("persist result for item %s: %w", item.ID, err)
	}
	if executeErr != nil {
		m.logger.Warn("job item finished with error", "job_id", item.JobID, "item_id", item.ID, "error", executeErr, "interrupted", interrupted, "elapsed_ms", time.Since(started).Milliseconds())
	} else {
		m.logger.Info("job item succeeded", "job_id", item.JobID, "item_id", item.ID, "elapsed_ms", time.Since(started).Milliseconds())
	}
	m.signal(kind)
	return true, nil
}
func executeSafely(ctx context.Context, executor Executor, id string, payload json.RawMessage) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("job executor panic: %v", recovered)
		}
	}()
	return executor(ctx, id, payload)
}
func (m *Manager) finishWithRetry(ctx context.Context, item Item, executeErr error, interrupted bool) error {
	var lastErr error
	for {
		if err := m.finishItem(ctx, item, executeErr, interrupted); err == nil {
			return nil
		} else {
			lastErr = err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(lastErr, ctx.Err())
		case <-timer.C:
		}
	}
}

func (m *Manager) claim(ctx context.Context, lane string) (Item, string, string, bool, error) {
	// Delay policies may read settings through the same one-connection DB, so
	// evaluate them before acquiring the claim transaction.
	m.mu.RLock()
	policies := make(map[string]Delay)
	for key, registered := range m.executors {
		if strings.HasPrefix(key, lane+":") && registered.delay != nil {
			policies[strings.TrimPrefix(key, lane+":")] = registered.delay
		}
	}
	m.mu.RUnlock()
	delays := make(map[string]time.Duration, len(policies))
	for action, policy := range policies {
		delays[action] = max(policy(ctx), 0)
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, "", "", false, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().UnixMilli()
	var item Item
	var target sql.NullString
	var kind, action string
	err = tx.QueryRowContext(ctx, `SELECT i.id,i.job_id,i.target_id,i.target_label,i.payload_json,i.state,i.attempts,j.target_kind,j.action
        FROM job_items i JOIN jobs j ON j.id=i.job_id
        LEFT JOIN job_action_limits l ON l.target_kind=j.target_kind AND l.action=j.action
        WHERE i.state='queued' AND i.available_at<=? AND j.state IN ('queued','running')
        AND j.cancel_requested=0 AND j.target_kind=? AND COALESCE(l.available_at,0)<=? AND COALESCE(l.in_flight,0)=0
        ORDER BY CASE WHEN j.created_by IS NULL THEN 1 ELSE 0 END,j.last_dispatched_at,j.created_at,i.rowid LIMIT 1`, now, lane, now).
		Scan(&item.ID, &item.JobID, &target, &item.TargetLabel, &item.PayloadJSON, &item.State, &item.Attempts, &kind, &action)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, "", "", false, nil
	}
	if err != nil {
		return Item{}, "", "", false, err
	}
	item.TargetID = target.String
	item.Attempts++
	item.cooldown = delays[action]
	item.targetKind = kind
	item.action = action
	changed, err := tx.ExecContext(ctx, `UPDATE job_items SET state='running',attempts=attempts+1,started_at=?,completed_at=NULL
        WHERE id=? AND state='queued'`, now, item.ID)
	if err != nil {
		return Item{}, "", "", false, err
	}
	count, err := changed.RowsAffected()
	if err != nil {
		return Item{}, "", "", false, err
	}
	if count != 1 {
		return Item{}, "", "", false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state='running',started_at=COALESCE(started_at,?),completed_at=NULL,
        pending=pending-1,running=running+1,last_dispatched_at=? WHERE id=?`, now, time.Now().UnixNano(), item.JobID); err != nil {
		return Item{}, "", "", false, err
	}
	if item.cooldown > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO job_action_limits(target_kind,action,available_at,in_flight) VALUES(?,?,?,1)
            ON CONFLICT(target_kind,action) DO UPDATE SET available_at=excluded.available_at,in_flight=1`, kind, action, now); err != nil {
			return Item{}, "", "", false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Item{}, "", "", false, err
	}
	return item, kind, action, true, nil
}

func (m *Manager) finishItem(ctx context.Context, item Item, executeErr error, interrupted bool) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var itemState string
	var attempt int
	var cancelRequested bool
	if err := tx.QueryRowContext(ctx, `SELECT i.state,i.attempts,j.cancel_requested FROM job_items i JOIN jobs j ON j.id=i.job_id WHERE i.id=?`, item.ID).
		Scan(&itemState, &attempt, &cancelRequested); err != nil {
		return err
	}
	// A retry after an uncertain COMMIT must not apply counters a second time.
	if itemState != "running" {
		return nil
	}
	if attempt != item.Attempts {
		return errors.New("job execution attempt no longer owns item")
	}
	state, code, message := "succeeded", "", ""
	if executeErr != nil {
		state, code, message = "failed", "EXECUTION_FAILED", executeErr.Error()
		var coded interface{ ErrorCode() string }
		if errors.As(executeErr, &coded) && coded.ErrorCode() != "" {
			code = coded.ErrorCode()
		}
		if code == "INTERNAL_ERROR" {
			message = "任务执行失败，请稍后重试"
		}
	}
	now := time.Now().UTC().UnixMilli()
	var completed any = now
	if interrupted {
		state, code, message = "queued", "", ""
		completed = nil
		if cancelRequested {
			state = "cancelled"
			completed = now
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE job_items SET state=?,error_code=NULLIF(?,''),error_message=NULLIF(?,''),completed_at=?,available_at=?,
        started_at=CASE WHEN ?='queued' THEN NULL ELSE started_at END WHERE id=? AND state='running' AND attempts=?`, state, code, message, completed, now, state, item.ID, item.Attempts); err != nil {
		return err
	}
	if item.cooldown > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE job_action_limits SET in_flight=0,available_at=? WHERE target_kind=? AND action=?`,
			time.Now().Add(item.cooldown).UnixMilli(), item.targetKind, item.action); err != nil {
			return err
		}
	}
	pending, succeeded, failed, cancelled := 0, 0, 0, 0
	switch state {
	case "queued":
		pending = 1
	case "succeeded":
		succeeded = 1
	case "failed":
		failed = 1
	case "cancelled":
		cancelled = 1
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET running=running-1,pending=pending+?,succeeded=succeeded+?,failed=failed+?,cancelled=cancelled+? WHERE id=?`, pending, succeeded, failed, cancelled, item.JobID); err != nil {
		return err
	}
	if err := settleJob(ctx, tx, item.JobID, now); err != nil {
		return err
	}
	return tx.Commit()
}

// Full aggregation is limited to recovery/cancellation, not every item finish.
func reconcileJob(ctx context.Context, tx *sql.Tx, id string, now int64) error {
	var pending, running, succeeded, failed, cancelled, total int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(state='queued'),0),COALESCE(SUM(state='running'),0),
        COALESCE(SUM(state='succeeded'),0),COALESCE(SUM(state='failed'),0),COALESCE(SUM(state='cancelled'),0)
        FROM job_items WHERE job_id=?`, id).Scan(&total, &pending, &running, &succeeded, &failed, &cancelled); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET total=?,pending=?,running=?,succeeded=?,failed=?,cancelled=? WHERE id=?`, total, pending, running, succeeded, failed, cancelled, id); err != nil {
		return err
	}
	return settleJob(ctx, tx, id, now)
}
func settleJob(ctx context.Context, tx *sql.Tx, id string, now int64) error {
	var pending, running, succeeded, failed, cancelled, total int
	var cancelRequested bool
	if err := tx.QueryRowContext(ctx, `SELECT total,pending,running,succeeded,failed,cancelled,cancel_requested FROM jobs WHERE id=?`, id).
		Scan(&total, &pending, &running, &succeeded, &failed, &cancelled, &cancelRequested); err != nil {
		return err
	}
	state := "queued"
	var completed any
	if running > 0 {
		state = "running"
	} else if pending == 0 {
		completed = now
		switch {
		case cancelRequested || cancelled == total:
			state = "cancelled"
		case failed == total:
			state = "failed"
		case failed > 0 || cancelled > 0:
			state = "partially_succeeded"
		default:
			state = "succeeded"
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE jobs SET state=?,completed_at=? WHERE id=?`, state, completed, id)
	return err
}

// Cancel stops queued work. Already-running external operations are allowed to
// finish; the job remains running until their durable outcomes are recorded.
func (m *Manager) Cancel(ctx context.Context, id string) (Job, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	var state string
	var requested bool
	if err := tx.QueryRowContext(ctx, `SELECT state,cancel_requested FROM jobs WHERE id=?`, id).Scan(&state, &requested); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Job{}, ErrNotFound
		}
		return Job{}, err
	}
	if requested && state == "cancelled" {
		if err := tx.Rollback(); err != nil {
			return Job{}, err
		}
		return m.Get(ctx, id, true)
	}
	if !requested && state != "queued" && state != "running" {
		return Job{}, ErrActive
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET cancel_requested=1 WHERE id=?`, id); err != nil {
		return Job{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE job_items SET state='cancelled',completed_at=? WHERE job_id=? AND state='queued'`, now, id); err != nil {
		return Job{}, err
	}
	if err := reconcileJob(ctx, tx, id, now); err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return m.Get(ctx, id, true)
}
