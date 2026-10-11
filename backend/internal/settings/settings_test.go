package settings

import (
	"context"
	"errors"
	"sync"

	"account-hub/internal/audit"
	"account-hub/internal/platform/apperror"
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
	items, err := repository.List(ctx)
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
	maintenance := NewMaintenance(db)
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
        (actor_id, route, key, request_hash, status_code, response_body, expires_at, created_at, resource_type, resource_id)
        VALUES('admin', '/jobs', 'old-done', 'hash', 202, '', ?, ?, 'job', 'job_old_done')`, recent, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO audit_logs
        (id, action, target_kind, summary, request_id, remote_addr, created_at) VALUES
        ('audit_old', 'token_check', 'token', 'old', 'req_old', '127.0.0.1', ?),
        ('audit_recent', 'token_check', 'token', 'recent', 'req_recent', '127.0.0.1', ?)`, old, recent); err != nil {
		t.Fatal(err)
	}
	result, err := maintenance.CleanupHistory(ctx, cutoff)
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

func TestUpdateRollsBackConfigurationAndEffects(t *testing.T) {
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
	before, err := repository.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("effect unavailable")
	service := NewService(repository, func(ctx context.Context, tx platformsqlite.DBTX, change Change) error {
		if !change.Keys["proxy_url"] || change.After.Proxy.URL != "http://localhost:8080" {
			t.Fatal("change does not describe the request")
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_logs(id, action, target_kind, summary, request_id, remote_addr, created_at) VALUES('effect', 'test', 'settings', '', '', '', 1)`)
		if err != nil {
			return err
		}
		return failure
	})
	if _, err := service.Update(ctx, map[string]any{"proxy_url": "http://localhost:8080"}); !errors.Is(err, failure) {
		t.Fatalf("err=%v", err)
	}
	after, err := repository.Snapshot(ctx)
	if err != nil || after != before {
		t.Fatalf("failed update changed snapshot: %+v %v", after, err)
	}
	values, err := repository.Values(ctx)
	if err != nil || values["proxy_url"] != "" {
		t.Fatalf("failed update changed database: %v %v", values, err)
	}
	if countRows(t, db, "audit_logs") != 0 {
		t.Fatal("effect committed despite failed configuration update")
	}
}

func TestConcurrentUpdatesPreserveProxyInvariant(t *testing.T) {
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
	for attempt := 0; attempt < 30; attempt++ {
		if _, err := repository.Update(ctx, map[string]any{"proxy_enabled": false, "proxy_url": "http://localhost:8080"}); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		failures := make(chan error, 2)
		var wg sync.WaitGroup
		for _, update := range []map[string]any{{"proxy_enabled": true}, {"proxy_url": ""}} {
			wg.Add(1)
			go func(update map[string]any) {
				defer wg.Done()
				<-start
				_, err := repository.Update(ctx, update)
				failures <- err
			}(update)
		}
		close(start)
		wg.Wait()
		close(failures)
		invalid := 0
		for err := range failures {
			if err != nil {
				if apperror.KindOf(err) != apperror.Validation {
					t.Fatal(err)
				}
				invalid++
			}
		}
		if invalid != 1 {
			t.Fatalf("wanted one rejected conflicting patch, got %d", invalid)
		}
		values, err := repository.Values(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if values["proxy_enabled"] == true && values["proxy_url"] == "" {
			t.Fatal("concurrent updates broke proxy invariant")
		}
		snapshot, err := repository.Snapshot(ctx)
		if err != nil || snapshot.Proxy.Enabled != values["proxy_enabled"].(bool) || snapshot.Proxy.URL != values["proxy_url"].(string) {
			t.Fatalf("snapshot is stale: %+v %v", snapshot, err)
		}
	}
}

func TestRuntimeSnapshotDoesNotQueryDatabaseAfterPublication(t *testing.T) {
	ctx := context.Background()
	db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(db)
	if err := repository.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Update(ctx, map[string]any{"proxy_enabled": true, "proxy_url": "http://localhost:8080", "token_refresh_interval_hours": 24}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	snapshot, err := repository.Snapshot(ctx)
	if err != nil || snapshot.Token.Interval != 24*time.Hour || !snapshot.Proxy.Enabled {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
}

func TestAuditFailureRollsBackSettings(t *testing.T) {
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
	service := NewService(repository).WithAudit(audit.NewRepository(db))
	if _, err := db.Exec(`CREATE TRIGGER reject_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, map[string]any{"token_refresh_interval_hours": 24}); err == nil {
		t.Fatal("update succeeded without required audit record")
	}
	values, err := repository.Values(ctx)
	if err != nil || values["token_refresh_interval_hours"] != 168 {
		t.Fatalf("settings changed without audit: %v %v", values, err)
	}
	if _, err := db.Exec("DROP TRIGGER reject_audit"); err != nil {
		t.Fatal(err)
	}
	ctx = audit.WithEntry(ctx, audit.Entry{Action: "settings_update", TargetKind: "settings", RequestID: "req_update"})
	if _, err := service.Update(ctx, map[string]any{"token_refresh_interval_hours": 24}); err != nil {
		t.Fatal(err)
	}
	var requestID string
	if err := db.QueryRow("SELECT request_id FROM audit_logs").Scan(&requestID); err != nil || requestID != "req_update" {
		t.Fatalf("audit metadata missing: %q %v", requestID, err)
	}
}

func TestCleanupPreservesOperationMarkerUntilJobIsRemoved(t *testing.T) {
	ctx := context.Background()
	db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UnixMilli()
	if _, err := db.Exec(`INSERT INTO jobs(id, target_kind, action, state, total, running, created_at) VALUES('job_active','token','refresh','running',1,1,?);
 INSERT INTO job_items(id, job_id, state) VALUES('item_active','job_active','running');
 INSERT INTO token_refresh_attempts(id, target_label, operation, status, started_at, completed_at, operation_key) VALUES('committed','account','refresh','succeeded',1,2,'item_active')`, now-1000); err != nil {
		t.Fatal(err)
	}
	maintenance := NewMaintenance(db)
	result, err := maintenance.CleanupHistory(ctx, now)
	if err != nil || result.TokenAttempts != 0 || result.Jobs != 0 {
		t.Fatalf("cleanup removed recovery marker: %+v %v", result, err)
	}
	if _, err := db.Exec("UPDATE jobs SET state='succeeded', running=0 WHERE id='job_active'; UPDATE job_items SET state='succeeded' WHERE id='item_active'"); err != nil {
		t.Fatal(err)
	}
	result, err = maintenance.CleanupHistory(ctx, now)
	if err != nil || result.TokenAttempts != 1 || result.Jobs != 1 {
		t.Fatalf("completed job and marker not removed: %+v %v", result, err)
	}
}
