package job

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	platformsqlite "account-hub/internal/platform/sqlite"
)

func TestManagerExecutesAndReplaysIdempotentJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedAdmin(t, db)
	manager := NewManager(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var calls atomic.Int32
	manager.Register("token", "check", func(context.Context, string, json.RawMessage) error {
		calls.Add(1)
		return nil
	}, nil)
	go manager.Start(ctx, 10*time.Millisecond, 1)

	targets := []Target{{ID: "one", Label: "one"}, {ID: "two", Label: "two"}}
	created, replay, err := manager.Create(ctx, "token", "check", "admin_test", "/jobs", "same-key", RequestHash(targets), targets)
	if err != nil || replay {
		t.Fatalf("Create: replay=%v err=%v", replay, err)
	}
	replayed, replay, err := manager.Create(ctx, "token", "check", "admin_test", "/jobs", "same-key", RequestHash(targets), targets)
	if err != nil || !replay || replayed.ID != created.ID {
		t.Fatalf("idempotent replay: job=%s replay=%v err=%v", replayed.ID, replay, err)
	}
	if _, _, err := manager.Create(ctx, "token", "check", "admin_test", "/jobs", "same-key", "different", targets); err != ErrIdempotencyConflict {
		t.Fatalf("different request with same key returned %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, err := manager.Get(ctx, created.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		if current.State == "succeeded" {
			if current.Succeeded != 2 || calls.Load() != 2 {
				t.Fatalf("unexpected completion: %+v calls=%d", current, calls.Load())
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not complete")
}

func TestManagerRecoversRunningItemAfterRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedAdmin(t, db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	creator := NewManager(db, logger)
	creator.Register("token", "check", func(context.Context, string, json.RawMessage) error { return nil }, nil)
	created, _, err := creator.Create(ctx, "token", "check", "admin_test", "/jobs", "", "", []Target{{ID: "recover-me", Label: "recover-me"}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	if _, err := db.Exec("UPDATE job_items SET state='running', started_at=? WHERE job_id=?", now, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE jobs SET state='running', pending=0, running=1, started_at=? WHERE id=?", now, created.ID); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	restarted := NewManager(db, logger)
	restarted.Register("token", "check", func(context.Context, string, json.RawMessage) error { calls.Add(1); return nil }, nil)
	go restarted.Start(ctx, 10*time.Millisecond, 1)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, err := restarted.Get(ctx, created.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		if current.State == "succeeded" {
			if calls.Load() != 1 || current.Succeeded != 1 {
				t.Fatalf("unexpected recovered job: %+v calls=%d", current, calls.Load())
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("recovered job did not complete")
}

func TestManagerDeletesOnlyFinishedJobs(t *testing.T) {
	ctx := context.Background()
	db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedAdmin(t, db)
	manager := NewManager(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	manager.Register("token", "check", func(context.Context, string, json.RawMessage) error { return nil }, nil)
	targets := []Target{{ID: "one", Label: "one"}}
	created, _, err := manager.Create(ctx, "token", "check", "admin_test", "/jobs", "delete-key", RequestHash(targets), targets)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(ctx, created.ID); !errors.Is(err, ErrActive) {
		t.Fatalf("Delete active job: %v", err)
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := db.ExecContext(ctx, "UPDATE job_items SET state = 'running', started_at = ? WHERE job_id = ?", now, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE jobs SET state = 'cancelled', pending = 0, running = 1, completed_at = ? WHERE id = ?", now, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(ctx, created.ID); !errors.Is(err, ErrActive) {
		t.Fatalf("Delete cancelled job with running item: %v", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE job_items SET state = 'succeeded', completed_at = ? WHERE job_id = ?", now, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE jobs SET state = 'succeeded', pending = 0, running = 0, succeeded = total, completed_at = ? WHERE id = ?", now, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Get(ctx, created.ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get deleted job: %v", err)
	}
	for table, query := range map[string]string{
		"job items":        "SELECT COUNT(1) FROM job_items WHERE job_id = ?",
		"idempotency keys": "SELECT COUNT(1) FROM idempotency_keys WHERE response_body = ?",
	} {
		var count int
		if err := db.QueryRowContext(ctx, query, created.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s remain after deletion: %d", table, count)
		}
	}
}

func TestManagerDeleteManyIsAtomic(t *testing.T) {
	ctx := context.Background()
	db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedAdmin(t, db)
	manager := NewManager(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	manager.Register("token", "check", func(context.Context, string, json.RawMessage) error { return nil }, nil)
	first, _, err := manager.Create(ctx, "token", "check", "admin_test", "/jobs", "", "", []Target{{ID: "one", Label: "one"}})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := manager.Create(ctx, "token", "check", "admin_test", "/jobs", "", "", []Target{{ID: "two", Label: "two"}})
	if err != nil {
		t.Fatal(err)
	}
	finishJob(t, db, first.ID)
	if deleted, err := manager.DeleteMany(ctx, []string{first.ID, second.ID}); deleted != 0 || !errors.Is(err, ErrActive) {
		t.Fatalf("DeleteMany with active job: deleted=%d err=%v", deleted, err)
	}
	if _, err := manager.Get(ctx, first.ID, false); err != nil {
		t.Fatalf("finished job was partially deleted: %v", err)
	}
	finishJob(t, db, second.ID)
	deleted, err := manager.DeleteMany(ctx, []string{first.ID, second.ID, first.ID, ""})
	if err != nil || deleted != 2 {
		t.Fatalf("DeleteMany: deleted=%d err=%v", deleted, err)
	}
	for _, id := range []string{first.ID, second.ID} {
		if _, err := manager.Get(ctx, id, false); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get deleted job %s: %v", id, err)
		}
	}
}

func finishJob(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	now := time.Now().UTC().UnixMilli()
	if _, err := db.Exec("UPDATE job_items SET state = 'succeeded', completed_at = ? WHERE job_id = ?", now, id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE jobs SET state = 'succeeded', pending = 0, running = 0, succeeded = total, completed_at = ? WHERE id = ?", now, id); err != nil {
		t.Fatal(err)
	}
}

func seedAdmin(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Now().UnixMilli()
	if _, err := db.Exec(`INSERT INTO admins(id, username, password_hash, created_at, updated_at)
        VALUES('admin_test', 'admin', 'unused', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
}
