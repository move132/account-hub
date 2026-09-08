package settings

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	platformsqlite "account-hub/internal/platform/sqlite"
)

func TestDefaultsAndValidation(t *testing.T) {
	ctx := context.Background()
	db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repository := NewRepository(db)
	if err := repository.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := repository.List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 10 {
		t.Fatalf("got %d settings, want 10", len(items))
	}
	if _, err := repository.Update(ctx, map[string]any{"unknown": true}); err == nil {
		t.Fatal("unknown setting was accepted")
	}
	if _, err := repository.Update(ctx, map[string]any{"token_refresh_interval_hours": float64(217)}); err == nil {
		t.Fatal("out-of-range setting was accepted")
	}
	updated, err := repository.Update(ctx, map[string]any{"token_refresh_interval_hours": float64(24)})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range updated {
		if item.Key == "token_refresh_interval_hours" {
			found = item.Value == 24
		}
	}
	if !found {
		t.Fatal("updated value not returned")
	}
}

func TestCleanupHistory(t *testing.T) {
	ctx := context.Background()
	db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repository := NewRepository(db)
	now := time.Now().UTC().UnixMilli()
	old, cutoff, recent := now-3_600_000, now-1_800_000, now
	if _, err := db.ExecContext(ctx, `INSERT INTO token_refresh_attempts
        (id, target_label, operation, status, started_at) VALUES
        ('ta_old', 'old', 'check', 'succeeded', ?),
        ('ta_recent', 'recent', 'check', 'succeeded', ?)`, old, recent); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO dreamina_refresh_attempts
        (id, target_label, operation, status, started_at) VALUES
        ('da_old', 'old', 'check', 'succeeded', ?),
        ('da_recent', 'recent', 'check', 'succeeded', ?)`, old, recent); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO jobs
        (id, target_kind, action, state, total, pending, running, created_at) VALUES
        ('job_old_done', 'token', 'check', 'succeeded', 1, 0, 0, ?),
        ('job_old_active', 'token', 'check', 'running', 1, 0, 1, ?),
        ('job_recent_done', 'token', 'check', 'succeeded', 1, 0, 0, ?)`, old, old, recent); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO job_items
        (id, job_id, target_label, state) VALUES
        ('ji_old_done', 'job_old_done', 'old', 'succeeded'),
        ('ji_old_active', 'job_old_active', 'active', 'running'),
        ('ji_recent_done', 'job_recent_done', 'recent', 'succeeded')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO idempotency_keys
        (actor_id, route, key, request_hash, status_code, response_body, expires_at, created_at)
        VALUES('admin', '/jobs', 'old-done', 'hash', 202, 'job_old_done', ?, ?)`, recent, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO audit_logs
        (id, action, target_kind, summary, request_id, remote_addr, created_at) VALUES
        ('audit_old', 'token_check', 'token', 'old', 'req_old', '127.0.0.1', ?),
        ('audit_recent', 'token_check', 'token', 'recent', 'req_recent', '127.0.0.1', ?)`, old, recent); err != nil {
		t.Fatal(err)
	}
	result, err := repository.CleanupHistory(ctx, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if result.AuditLogs != 1 || result.TokenAttempts != 1 || result.DreaminaAttempts != 1 || result.Jobs != 1 {
		t.Fatalf("unexpected cleanup result: %+v", result)
	}
	for table, want := range map[string]int{
		"token_refresh_attempts":    1,
		"dreamina_refresh_attempts": 1,
		"jobs":                      2,
		"job_items":                 2,
		"idempotency_keys":          0,
		"audit_logs":                1,
	} {
		if got := countRows(t, db, table); got != want {
			t.Fatalf("%s rows=%d want %d", table, got, want)
		}
	}
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(1) FROM "+table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
