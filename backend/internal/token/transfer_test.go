package token

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	platformsqlite "account-hub/internal/platform/sqlite"
	"account-hub/internal/settings"
)

func TestParseImportSupportsSora2VideoFormats(t *testing.T) {
	content := "access-only\n\"Named account\",access-two,session-two,\"note,with comma\"\n"
	rows, err := parseImport(strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%d, want 2", len(rows))
	}
	if rows[0].Input.Name != "Token_1" || rows[0].Input.AccessToken != "access-only" || rows[0].Input.Note != "批量导入" {
		t.Fatalf("plain Access Token row parsed incorrectly: %+v", rows[0])
	}
	second := rows[1].Input
	if second.Name != "Named account" || second.AccessToken != "access-two" || second.SessionToken != "session-two" || second.Note != "note,with comma" {
		t.Fatalf("CSV row parsed incorrectly: %+v", second)
	}
}

func TestParseImportMarksDuplicateAccessTokensAndMasksPreview(t *testing.T) {
	rows, err := parseImport(strings.NewReader("First,access-secret,session-secret\nSecond,access-secret,other-session\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Duplicate || !rows[1].Duplicate || rows[1].DuplicateReason == "" {
		t.Fatalf("duplicate Access Token was not marked: %+v", rows)
	}
	preview, err := json.Marshal(importPreview(rows))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(preview, []byte("access-secret")) || bytes.Contains(preview, []byte("session-secret")) {
		t.Fatalf("preview leaked credentials: %s", preview)
	}
}

func TestImportSkipsTokensAlreadyInDatabase(t *testing.T) {
	ctx := context.Background()
	db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	settingRepository := settings.NewRepository(db)
	if err := settingRepository.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(db)
	service := NewService(repository, fakeClient{}, settingRepository)
	existing, err := service.Create(ctx, CreateInput{Name: "Existing", AccessToken: "access-existing"})
	if err != nil {
		t.Fatal(err)
	}

	rows, err := parseImport(strings.NewReader("access-existing\nNew,access-new,session-new\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.markImportDuplicates(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if !rows[0].Duplicate || rows[1].Duplicate {
		t.Fatalf("database duplicate status is wrong: %+v", rows)
	}
	if err := service.Import(ctx, CreateInput{Name: "Duplicate", AccessToken: "access-existing"}); err != nil {
		t.Fatalf("racing duplicate should be skipped: %v", err)
	}

	tokens, err := repository.AccessTokensByIDs(ctx, []string{existing.ID})
	if err != nil || tokens[existing.ID] != "access-existing" {
		t.Fatalf("selected export lookup failed: tokens=%v err=%v", tokens, err)
	}
}
