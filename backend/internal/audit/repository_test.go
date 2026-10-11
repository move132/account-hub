package audit

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	platformsqlite "account-hub/internal/platform/sqlite"
)

func TestAuditFailureIsObservableAndTransactionCanRollBack(t *testing.T) {
	ctx := context.Background()
	db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	repository := NewRepository(db).WithLogger(slog.New(slog.NewJSONHandler(&logs, nil)))
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.LogTx(ctx, tx, Entry{Action: "settings_update", TargetKind: "settings"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM audit_logs").Scan(&count); err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	db.Close()
	if err := repository.Log(ctx, Entry{Action: "password_change", TargetKind: "auth", RequestID: "req_test"}); err == nil {
		t.Fatal("expected storage error")
	}
	if !strings.Contains(logs.String(), "audit record failed") || !strings.Contains(logs.String(), "password_change") || !strings.Contains(logs.String(), "req_test") {
		t.Fatalf("missing audit diagnostic: %s", logs.String())
	}
}
