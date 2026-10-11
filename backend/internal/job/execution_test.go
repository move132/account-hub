package job

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"account-hub/internal/audit"
	"account-hub/internal/idempotency"
	platformsqlite "account-hub/internal/platform/sqlite"
)

func testManager(t *testing.T) (*Manager, *sql.DB) {
	t.Helper()
	db, err := platformsqlite.Open(context.Background(), filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	seedAdmin(t, db)
	return NewManager(db, slog.New(slog.NewTextHandler(io.Discard, nil))), db
}
func createTestJob(t *testing.T, m *Manager, action string, ids ...string) Job {
	t.Helper()
	targets := make([]Target, 0, len(ids))
	for _, id := range ids {
		targets = append(targets, Target{ID: id, Label: id})
	}
	result, _, err := m.Create(context.Background(), "token", action, "admin_test", "/jobs", "", "", targets)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func noopExecutor(context.Context, string, json.RawMessage) error { return nil }

func TestCancellationRecoveryLeavesNoRunnableOrUndeletableItems(t *testing.T) {
	m, _ := testManager(t)
	m.Register("token", "check", noopExecutor, nil)
	created := createTestJob(t, m, "check", "running", "waiting")
	_, _, _, found, err := m.claim(context.Background(), "token")
	if err != nil || !found {
		t.Fatalf("claim found=%v err=%v", found, err)
	}
	cancelling, err := m.Cancel(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !cancelling.CancelRequested || cancelling.State != "running" || cancelling.Running != 1 || cancelling.Cancelled != 1 || cancelling.CompletedAt != nil {
		t.Fatalf("cancellation must wait for running outcome: %+v", cancelling)
	}
	if err := m.recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := m.Get(context.Background(), created.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "cancelled" || result.Running != 0 || result.Pending != 0 || result.Cancelled != 2 {
		t.Fatalf("recovered %+v", result)
	}
	if _, _, _, found, err := m.claim(context.Background(), "token"); err != nil || found {
		t.Fatalf("cancelled claim found=%v err=%v", found, err)
	}
	if err := m.Delete(context.Background(), created.ID); err != nil {
		t.Fatalf("delete recovered cancellation: %v", err)
	}
}

func TestLegacyCancelledRunningJobIsReconciled(t *testing.T) {
	m, db := testManager(t)
	m.Register("token", "check", noopExecutor, nil)
	created := createTestJob(t, m, "check", "legacy")
	if _, err := db.Exec(`UPDATE job_items SET state='running' WHERE job_id=?`, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE jobs SET state='cancelled',pending=0,running=1 WHERE id=?`, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := m.Get(context.Background(), created.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Running != 0 || result.Items[0].State != "cancelled" {
		t.Fatalf("legacy recovery: %+v", result)
	}
}

func TestShutdownAcknowledgesWithIndependentContext(t *testing.T) {
	m, _ := testManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	m.Register("token", "check", func(ctx context.Context, _ string, _ json.RawMessage) error {
		execution, ok := ExecutionFromContext(ctx)
		if !ok || execution.ItemID == "" || execution.Attempt != 1 {
			t.Errorf("execution metadata: %+v found=%v", execution, ok)
		}
		cancel()
		return ctx.Err()
	}, nil)
	created := createTestJob(t, m, "check", "interrupted")
	if worked, err := m.processOne(ctx, "token"); err != nil || !worked {
		t.Fatalf("process worked=%v err=%v", worked, err)
	}
	result, err := m.Get(context.Background(), created.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Running != 0 || result.Pending != 1 || result.Items[0].State != "queued" {
		t.Fatalf("shutdown outcome: %+v", result)
	}
	m.Register("token", "check", func(ctx context.Context, _ string, _ json.RawMessage) error {
		execution, _ := ExecutionFromContext(ctx)
		if execution.ItemID != result.Items[0].ID || execution.Attempt != 2 {
			t.Errorf("replay lost operation identity: %+v", execution)
		}
		return nil
	}, nil)
	if _, err := m.processOne(context.Background(), "token"); err != nil {
		t.Fatal(err)
	}
	result, err = m.Get(context.Background(), created.ID, true)
	if err != nil || result.Succeeded != 1 {
		t.Fatalf("retry result=%+v err=%v", result, err)
	}
}

func TestFinishRetriesWithoutRepeatingExecutor(t *testing.T) {
	m, db := testManager(t)
	var calls atomic.Int32
	m.Register("token", "check", func(context.Context, string, json.RawMessage) error { calls.Add(1); return nil }, nil)
	created := createTestJob(t, m, "check", "one")
	if _, err := db.Exec(`CREATE TRIGGER fail_finish BEFORE UPDATE OF state ON job_items WHEN NEW.state='succeeded' BEGIN SELECT RAISE(FAIL,'injected result write failure'); END`); err != nil {
		t.Fatal(err)
	}
	dropped := make(chan error, 1)
	go func() {
		time.Sleep(75 * time.Millisecond)
		_, err := db.Exec(`DROP TRIGGER fail_finish`)
		dropped <- err
	}()
	if _, err := m.processOne(context.Background(), "token"); err != nil {
		t.Fatal(err)
	}
	if err := <-dropped; err != nil {
		t.Fatal(err)
	}
	result, err := m.Get(context.Background(), created.ID, true)
	if err != nil || result.Succeeded != 1 || result.Running != 0 || calls.Load() != 1 {
		t.Fatalf("finish result=%+v calls=%d err=%v", result, calls.Load(), err)
	}
}

func TestPersistentFinishFailureIsBoundedAndRecoverable(t *testing.T) {
	m, db := testManager(t)
	m.completionTimeout = 75 * time.Millisecond
	m.Register("token", "check", noopExecutor, nil)
	created := createTestJob(t, m, "check", "one")
	if _, err := db.Exec(`CREATE TRIGGER fail_finish BEFORE UPDATE OF state ON job_items WHEN NEW.state='succeeded' BEGIN SELECT RAISE(FAIL,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := m.processOne(context.Background(), "token"); err == nil {
		t.Fatal("expected bounded persistence failure")
	}
	if time.Since(started) > time.Second {
		t.Fatal("result persistence was not bounded")
	}
	if _, err := db.Exec(`DROP TRIGGER fail_finish`); err != nil {
		t.Fatal(err)
	}
	if err := m.recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.processOne(context.Background(), "token"); err != nil {
		t.Fatal(err)
	}
	result, err := m.Get(context.Background(), created.ID, true)
	if err != nil || result.Succeeded != 1 || result.Running != 0 || result.Items[0].Attempts != 2 {
		t.Fatalf("recovery result=%+v err=%v", result, err)
	}
}

func TestRefreshCooldownDoesNotBlockOtherActions(t *testing.T) {
	m, _ := testManager(t)
	m.Register("token", "refresh", noopExecutor, func(context.Context) time.Duration { return time.Hour })
	m.Register("token", "disable", noopExecutor, nil)
	refresh := createTestJob(t, m, "refresh", "one", "two")
	done := make(chan error, 1)
	go func() { _, err := m.processOne(context.Background(), "token"); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker slept through cooldown")
	}
	disable := createTestJob(t, m, "disable", "three")
	if worked, err := m.processOne(context.Background(), "token"); err != nil || !worked {
		t.Fatalf("management job worked=%v err=%v", worked, err)
	}
	result, err := m.Get(context.Background(), disable.ID, false)
	if err != nil || result.State != "succeeded" {
		t.Fatalf("disable result=%+v err=%v", result, err)
	}
	pending, err := m.Get(context.Background(), refresh.ID, false)
	if err != nil || pending.Pending != 1 {
		t.Fatalf("cooldown violated %+v err=%v", pending, err)
	}
	if worked, err := m.processOne(context.Background(), "token"); err != nil || worked {
		t.Fatalf("refresh ran before available time worked=%v err=%v", worked, err)
	}
}

func TestJobsShareLaneFairlyAndDelayedActionIsExclusive(t *testing.T) {
	t.Run("round robin", func(t *testing.T) {
		m, _ := testManager(t)
		m.Register("token", "check", noopExecutor, nil)
		first := createTestJob(t, m, "check", "a", "b", "c")
		second := createTestJob(t, m, "check", "d")
		for range 2 {
			if _, err := m.processOne(context.Background(), "token"); err != nil {
				t.Fatal(err)
			}
		}
		a, _ := m.Get(context.Background(), first.ID, false)
		b, _ := m.Get(context.Background(), second.ID, false)
		if a.Succeeded != 1 || b.Succeeded != 1 {
			t.Fatalf("unfair dispatch first=%+v second=%+v", a, b)
		}
	})
	t.Run("shared limit", func(t *testing.T) {
		m, _ := testManager(t)
		m.Register("token", "refresh", noopExecutor, func(context.Context) time.Duration { return time.Second })
		createTestJob(t, m, "refresh", "a", "b")
		if _, _, _, found, err := m.claim(context.Background(), "token"); err != nil || !found {
			t.Fatalf("first claim %v %v", found, err)
		}
		if _, _, _, found, err := m.claim(context.Background(), "token"); err != nil || found {
			t.Fatalf("concurrent worker bypassed shared action limit %v %v", found, err)
		}
	})
}

func TestConcurrentIdempotentCreationReturnsSameJob(t *testing.T) {
	m, db := testManager(t)
	m.Register("token", "check", noopExecutor, nil)
	var workers sync.WaitGroup
	ids := make(chan string, 8)
	failures := make(chan error, 8)
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			result, _, err := m.Create(context.Background(), "token", "check", "admin_test", "/jobs", "same", "hash", []Target{{ID: "one", Label: "one"}})
			if err != nil {
				failures <- err
			} else {
				ids <- result.ID
			}
		}()
	}
	workers.Wait()
	close(ids)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Errorf("duplicate job %s vs %s", id, first)
		}
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM jobs").Scan(&count); err != nil || count != 1 {
		t.Fatalf("job count=%d err=%v", count, err)
	}
}

func TestDeleteJobReplayPrecedesTargetResolution(t *testing.T) {
	m, db := testManager(t)
	exists := true
	m.Register("token", "delete", func(context.Context, string, json.RawMessage) error { exists = false; return nil }, nil)
	resolverCalls := 0
	handler := NewHandler(m, audit.NewRepository(db)).WithTargetResolver("token", func(context.Context, []string) ([]Target, error) {
		resolverCalls++
		if !exists {
			return nil, errors.New("target no longer exists")
		}
		return []Target{{ID: "one", Label: "one"}}, nil
	})
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/token-batch-jobs", bytes.NewBufferString(`{"action":"delete","ids":["one"]}`))
		r.Header.Set("Idempotency-Key", "delete-command")
		w := httptest.NewRecorder()
		handler.Create("token")(w, r)
		return w
	}
	first := request()
	if first.Code != http.StatusAccepted {
		t.Fatal(first.Body.String())
	}
	if _, err := m.processOne(context.Background(), "token"); err != nil {
		t.Fatal(err)
	}
	replay := request()
	if replay.Code != http.StatusAccepted || resolverCalls != 1 {
		t.Fatalf("replay status=%d body=%s resolverCalls=%d", replay.Code, replay.Body.String(), resolverCalls)
	}
}

func TestImportLinksIdempotencyWithinJobTransaction(t *testing.T) {
	m, db := testManager(t)
	m.Register("token", "import", noopExecutor, nil)
	store := idempotency.NewStore(db)
	request := idempotency.Request{ActorID: "admin_test", Route: "POST /imports", Key: "import", Hash: "content"}
	record, created, err := store.Reserve(context.Background(), request)
	if err != nil || !created {
		t.Fatalf("reserve created=%v err=%v", created, err)
	}
	ctx := idempotency.WithRecord(context.Background(), record)
	result, _, err := m.Create(ctx, "token", "import", "admin_test", "/imports", "", "", []Target{{Label: "one", Payload: map[string]string{"value": "test"}}})
	if err != nil {
		t.Fatal(err)
	}
	saved, found, err := store.Lookup(context.Background(), request)
	if err != nil || !found || saved.State != "completed" || saved.ResourceID != result.ID || saved.Status != 202 || !json.Valid([]byte(saved.Body)) {
		t.Fatalf("atomic operation reference %+v found=%v err=%v", saved, found, err)
	}
	if _, err := m.processOne(context.Background(), "token"); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(context.Background(), result.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Lookup(context.Background(), request); err != nil || found {
		t.Fatalf("deleted resource retained key found=%v err=%v", found, err)
	}
}

// listBeforeRefactor models the previous count + id query + per-job Get path.
func listBeforeRefactor(ctx context.Context, m *Manager, limit int) ([]Job, int, error) {
	var count int
	if err := m.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs").Scan(&count); err != nil {
		return nil, 0, err
	}
	rows, err := m.db.QueryContext(ctx, "SELECT id FROM jobs ORDER BY created_at DESC LIMIT ?", limit)
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
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, err
	}
	rows.Close()
	result := make([]Job, 0, len(ids))
	for _, id := range ids {
		item, err := m.Get(ctx, id, false)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, item)
	}
	return result, count, nil
}
func BenchmarkJobList(b *testing.B) {
	for _, size := range []int{100, 500} {
		b.Run(fmt.Sprintf("rows_%d", size), func(b *testing.B) {
			ctx := context.Background()
			db, err := platformsqlite.Open(ctx, filepath.Join(b.TempDir(), "benchmark.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			tx, err := db.Begin()
			if err != nil {
				b.Fatal(err)
			}
			for i := range size {
				if _, err := tx.Exec(`INSERT INTO jobs(id,target_kind,action,state,total,succeeded,created_at) VALUES(?,'token','check','succeeded',1,1,?)`, fmt.Sprintf("job_%d", i), i); err != nil {
					b.Fatal(err)
				}
			}
			if err := tx.Commit(); err != nil {
				b.Fatal(err)
			}
			m := NewManager(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
			b.Run("before_n_plus_one", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					items, count, err := listBeforeRefactor(ctx, m, size)
					if err != nil || len(items) != size || count != size {
						b.Fatalf("rows=%d total=%d err=%v", len(items), count, err)
					}
				}
			})
			b.Run("after_direct_query", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					items, count, err := m.List(ctx, 1, size)
					if err != nil || len(items) != size || count != size {
						b.Fatalf("rows=%d total=%d err=%v", len(items), count, err)
					}
				}
			})
		})
	}
}

func TestCreateHandlerClassifiesDatabaseFailureWithoutLeakingSQL(t *testing.T) {
	m, db := testManager(t)
	m.Register("token", "check", noopExecutor, nil)
	if _, err := db.Exec(`DROP TABLE job_items`); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(`{"action":"check","ids":["one"]}`))
	response := httptest.NewRecorder()
	NewHandler(m, audit.NewRepository(db)).Create("token")(response, request)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "no such table") || strings.Contains(response.Body.String(), "SQL") {
		t.Fatalf("unexpected failure status=%d body=%s", response.Code, response.Body.String())
	}
}

type internalExecutionError struct{ cause error }

func (e internalExecutionError) Error() string     { return e.cause.Error() }
func (e internalExecutionError) Unwrap() error     { return e.cause }
func (e internalExecutionError) ErrorCode() string { return "INTERNAL_ERROR" }
func TestInternalExecutionFailureDoesNotExposeCauseInJobItems(t *testing.T) {
	m, _ := testManager(t)
	m.Register("token", "check", func(context.Context, string, json.RawMessage) error {
		return internalExecutionError{cause: errors.New("SQL failure: no such table sensitive_internal_table")}
	}, nil)
	created := createTestJob(t, m, "check", "one")
	if worked, err := m.processOne(context.Background(), "token"); err != nil || !worked {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
	result, err := m.Get(context.Background(), created.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "failed" || len(result.Items) != 1 || result.Items[0].ErrorCode != "INTERNAL_ERROR" || result.Items[0].ErrorMessage != "任务执行失败，请稍后重试" {
		t.Fatalf("internal error contract %+v", result)
	}
}
