package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"account-hub/internal/audit"
	platformsqlite "account-hub/internal/platform/sqlite"
)

func TestSessionTouchIsThrottledAndExpiredSessionsArePruned(t *testing.T) {
	ctx := context.Background()
	db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repository := NewRepository(db)
	now := time.Now().UnixMilli()
	if err := repository.CreateAdmin(ctx, Admin{ID: "admin", Username: "admin", PasswordHash: "unused", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, session := range []Session{
		{IDHash: "active", AdminID: "admin", CSRFHash: "csrf", ExpiresAt: now + 3600000, CreatedAt: now - 300000, LastSeenAt: now - 120000},
		{IDHash: "expired", AdminID: "admin", CSRFHash: "csrf", ExpiresAt: now - 1, CreatedAt: now - 300000, LastSeenAt: now - 120000},
	} {
		if err := repository.CreateSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repository.Session(ctx, "active"); err != nil {
		t.Fatal(err)
	}
	var first int64
	if err := db.QueryRow("SELECT last_seen_at FROM admin_sessions WHERE id_hash = 'active'").Scan(&first); err != nil {
		t.Fatal(err)
	}
	if first < now {
		t.Fatalf("old session not touched: %d", first)
	}
	// A trigger makes an unnecessary second write observable without timing assumptions.
	if _, err := db.Exec(`CREATE TABLE touches(id INTEGER); CREATE TRIGGER record_active_touch AFTER UPDATE ON admin_sessions BEGIN INSERT INTO touches VALUES(1); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Session(ctx, "active"); err != nil {
		t.Fatal(err)
	}
	var second int64
	if err := db.QueryRow("SELECT last_seen_at FROM admin_sessions WHERE id_hash = 'active'").Scan(&second); err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatal("last-seen written again inside throttle window")
	}
	var touches int
	if err := db.QueryRow("SELECT COUNT(*) FROM touches").Scan(&touches); err != nil || touches != 0 {
		t.Fatalf("unexpected session writes: %d %v", touches, err)
	}
	if err := repository.DeleteExpiredSessions(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM admin_sessions").Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := repository.DeleteSession(ctx, "missing"); err != nil {
		t.Fatalf("repeat logout must be idempotent: %v", err)
	}
}

func TestPasswordChangeAndSessionRevocationShareAuditTransaction(t *testing.T) {
	ctx := context.Background()
	db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repository := NewRepository(db).WithAudit(audit.NewRepository(db))
	now := time.Now().UnixMilli()
	if err := repository.CreateAdmin(ctx, Admin{ID: "admin", Username: "admin", PasswordHash: "old", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"keep", "revoke"} {
		if err := repository.CreateSession(ctx, Session{IDHash: id, AdminID: "admin", CSRFHash: "csrf", ExpiresAt: now + 3600000, CreatedAt: now, LastSeenAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdatePassword(ctx, "admin", "new", "keep"); err == nil {
		t.Fatal("password changed without audit")
	}
	admin, err := repository.AdminByUsername(ctx, "admin")
	if err != nil || admin.PasswordHash != "old" {
		t.Fatalf("password mutation did not roll back: %+v %v", admin, err)
	}
	var sessions int
	if err := db.QueryRow("SELECT COUNT(*) FROM admin_sessions").Scan(&sessions); err != nil || sessions != 2 {
		t.Fatalf("revocation did not roll back: %d %v", sessions, err)
	}
	if _, err := db.Exec("DROP TRIGGER reject_audit"); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdatePassword(ctx, "admin", "new", "keep"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM admin_sessions").Scan(&sessions); err != nil || sessions != 1 {
		t.Fatalf("other session not revoked: %d %v", sessions, err)
	}
	var action string
	if err := db.QueryRow("SELECT action FROM audit_logs").Scan(&action); err != nil || action != "password_change" {
		t.Fatalf("audit missing: %q %v", action, err)
	}
}
