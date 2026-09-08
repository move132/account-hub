package dreamina

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	platformsqlite "account-hub/internal/platform/sqlite"
)

func TestParseImportLegacyFormat(t *testing.T) {
	content := "\ufeff\r\n  \r\n User@Example.com , first-password , first-session \r\n" +
		"user@example.com,\"second,password\",second-session,\"带逗号,的描述\"\r\n" +
		"other@example.com,third-password,first-session,重复\r\n"
	rows, err := parseImport(strings.NewReader(content))
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	first, second, third := rows[0], rows[1], rows[2]
	if first.Line != 3 || first.Input.Email != "user@example.com" || first.Input.Password != "first-password" || first.Input.SessionID != "first-session" || first.Input.Note != "批量导入" || first.Input.Status != "enabled" {
		t.Fatalf("incorrect legacy column mapping: %+v", first)
	}
	if second.Input.Password != "second,password" || second.Input.SessionID != "second-session" || second.Input.Note != "带逗号,的描述" || second.Duplicate || len(second.Errors) > 0 {
		t.Fatalf("same email with a different session must be accepted: %+v", second)
	}
	if !third.Duplicate || third.DuplicateReason != "与第 3 行 Session ID 重复" || len(third.Errors) > 0 {
		t.Fatalf("duplicate session should be skipped without an error: %+v", third)
	}
	preview, err := json.Marshal(importPreview(rows))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"first-password", "first-session", "second,password", "second-session"} {
		if strings.Contains(string(preview), secret) {
			t.Fatal("preview exposed an unmasked credential")
		}
	}
}

func TestParseImportHeaderCompatibility(t *testing.T) {
	content := "\ufeff\"note\",status,session_expires_at,session_id,password,email\n" +
		"描述,disabled,2030-01-01T00:00:00Z,session-value,password-value,user@example.com\n"
	rows, err := parseImport(strings.NewReader(content))
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	want := CreateInput{Email: "user@example.com", Password: "password-value", SessionID: "session-value", SessionExpiresAt: "2030-01-01T00:00:00Z", Status: "disabled", Note: "描述"}
	if rows[0].Input != want || len(rows[0].Errors) > 0 || rows[0].Line != 2 {
		t.Fatalf("header format changed: %+v", rows[0])
	}
}

func TestParseImportValidation(t *testing.T) {
	for _, content := range []string{"", "\ufeff\n \r\n", "email,password,session_id\n", "email,password\n", "user@example.com,\"unclosed,session"} {
		if _, err := parseImport(strings.NewReader(content)); err == nil {
			t.Errorf("expected an error for %q", content)
		}
	}
	for _, content := range []string{
		"user@example.com,password", "user@example.com,password,", ",password,session", "user@example.com,,session",
		"user@example.com,password,session,note,extra", "email,password,session_id,status\nuser@example.com,password,session,unknown",
	} {
		rows, err := parseImport(strings.NewReader(content))
		if err != nil || len(rows) != 1 || len(rows[0].Errors) == 0 {
			t.Errorf("expected a row error: rows=%+v err=%v", rows, err)
		}
	}
	rows, err := parseImport(strings.NewReader("user@example.com,,session\nuser@example.com,password,session"))
	if err != nil || len(rows) != 2 || rows[1].Duplicate || len(rows[1].Errors) > 0 {
		t.Fatalf("invalid rows should not suppress a valid session: %+v err=%v", rows, err)
	}
	if _, err := parseImport(strings.NewReader(strings.Repeat("user@example.com,password,session\n", 10001))); err == nil {
		t.Fatal("row limit was not enforced")
	}
}

func TestImportSkipsExistingAndConcurrentSessions(t *testing.T) {
	ctx := context.Background()
	db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repository := NewRepository(db)
	service := NewService(repository, nil, nil)
	created, err := service.Create(ctx, CreateInput{Email: "user@example.com", Password: "original-password", SessionID: "existing-session", Note: "original-note"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := parseImport(strings.NewReader("user@example.com,new-password,new-session\nother@example.com,changed-password,existing-session\nuser@example.com,third-password,third-session"))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.markImportDuplicates(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if rows[0].Duplicate || !rows[1].Duplicate || rows[2].Duplicate {
		t.Fatalf("database deduplication must only use Session ID: %+v", rows)
	}
	// Repeat the new row to model overlapping queued imports or a resumed job.
	for _, row := range append(rows, rows[0]) {
		if err := service.Import(ctx, row.Input); err != nil {
			t.Fatalf("Import: %v", err)
		}
	}
	items, total, err := service.List(ctx, ListFilter{})
	if err != nil || total != 3 || len(items) != 3 {
		t.Fatalf("expected three different Sessions for one email: total=%d err=%v", total, err)
	}
	record, err := repository.Get(ctx, created.ID)
	if err != nil || record.Email != "user@example.com" || record.Password != "original-password" || record.Note != "original-note" {
		t.Fatalf("duplicate import changed existing account: %+v err=%v", record, err)
	}
}
