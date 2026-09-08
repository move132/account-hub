package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenMigratesFreshDatabase(t *testing.T) {
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	for _, table := range []string{"admins", "tokens", "dreamina_accounts", "jobs", "settings", "audit_logs", "idempotency_keys"} {
		var found string
		if err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&found); err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
	var foreignKeys int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign_keys pragma: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys=%d, want 1", foreignKeys)
	}
}

func TestSessionIdentityMigrationPreservesAccountsAndHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer func() {
		if db != nil {
			db.Close()
		}
	}()
	initial, err := migrationFiles.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"+string(initial)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
        CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL);
        INSERT INTO schema_migrations VALUES ('001_init.sql', 1);
        INSERT INTO dreamina_accounts VALUES
            ('account', 'user@example.com', 'preserved-password', 'preserved-session', '2030-01-01',
             'disabled', 'error', '88', 'preserved-note', 101, 102, 103, 'ERROR', 'preserved-error', 9, 100, 104);
        INSERT INTO dreamina_refresh_attempts VALUES
            ('linked-attempt', 'account', 'user@example.com', 'refresh', 'failed', 'ERROR', 'failed-refresh', 10, 11),
            ('orphan-attempt', NULL, 'deleted@example.com', 'check', 'succeeded', NULL, NULL, 12, 13);
    `); err != nil {
		t.Fatal(err)
	}
	accountQuery := `SELECT json_array(id, email, password, session_id, session_expires_at, status, check_state,
        credit_balance, note, next_refresh_at, last_checked_at, last_refreshed_at, last_error_code,
        last_error_message, version, created_at, updated_at) FROM dreamina_accounts WHERE id = 'account'`
	historyQuery := `SELECT json_group_array(json_array(id, account_id, target_label, operation, status,
        error_code, error_message, started_at, completed_at)) FROM (SELECT * FROM dreamina_refresh_attempts ORDER BY id)`
	var accountBefore, historyBefore string
	if err := db.QueryRow(accountQuery).Scan(&accountBefore); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(historyQuery).Scan(&historyBefore); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var accountAfter, historyAfter string
	if err := db.QueryRow(accountQuery).Scan(&accountAfter); err != nil || accountBefore != accountAfter {
		t.Fatalf("migration changed account data: err=%v", err)
	}
	if err := db.QueryRow(historyQuery).Scan(&historyAfter); err != nil || historyBefore != historyAfter {
		t.Fatalf("migration changed history or its account links: before=%s after=%s err=%v", historyBefore, historyAfter, err)
	}
	if _, err := db.Exec(`INSERT INTO dreamina_accounts (id, email, password, session_id, created_at, updated_at)
        VALUES ('another', 'user@example.com', 'another-password', 'another-session', 200, 200)`); err != nil {
		t.Fatalf("same email with a different Session must be allowed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO dreamina_accounts (id, email, password, session_id, created_at, updated_at)
        VALUES ('duplicate', 'other@example.com', 'other-password', 'preserved-session', 200, 200)`); err == nil {
		t.Fatal("duplicate Session ID must still be rejected")
	}
	violations, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer violations.Close()
	if violations.Next() {
		t.Fatal("foreign key violation after migration")
	}
	if err := violations.Err(); err != nil {
		t.Fatal(err)
	}
	violations.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("reopening a migrated database failed: %v", err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil || count != 2 {
		t.Fatalf("migration was not recorded exactly once: count=%d err=%v", count, err)
	}
}
