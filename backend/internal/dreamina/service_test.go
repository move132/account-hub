package dreamina

import (
	"context"
	"path/filepath"
	"testing"

	platformsqlite "account-hub/internal/platform/sqlite"
	"account-hub/internal/settings"
)

type fakeClient struct{}

func (fakeClient) Login(context.Context, string, string) (LoginResult, error) {
	return LoginResult{SessionID: "session-new", SessionExpiresAt: "2030-01-01T00:00:00Z"}, nil
}
func (fakeClient) Check(context.Context, string) (CreditResult, error) {
	return CreditResult{Balance: "88", Raw: map[string]any{"ret": 0}}, nil
}
func (fakeClient) ClaimCredit(context.Context, string) (any, error) {
	return map[string]any{"received": true}, nil
}
func (fakeClient) Assets(context.Context, string, int, int) (any, error) {
	return map[string]any{"asset_list": []any{}}, nil
}

func TestServiceAccountFlows(t *testing.T) {
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
	created, err := service.Create(ctx, CreateInput{Email: "User@Example.com", Password: "plain-password", SessionID: "session-old"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Email != "user@example.com" || created.PasswordMasked == "plain-password" {
		t.Fatalf("unexpected created view: %+v", created)
	}
	edit, err := service.GetForEdit(ctx, created.ID)
	if err != nil || edit.Password != "plain-password" || edit.SessionID != "session-old" {
		t.Fatalf("GetForEdit: %+v err=%v", edit, err)
	}
	checked, credit, err := service.Check(ctx, created.ID)
	if err != nil || checked.CheckState != "valid" || credit.Balance != "88" {
		t.Fatalf("Check: %+v %+v err=%v", checked, credit, err)
	}
	refreshed, err := service.Refresh(ctx, created.ID)
	if err != nil || refreshed.SessionIDMasked == "session-new" || refreshed.LastRefreshedAt == nil {
		t.Fatalf("Refresh: %+v err=%v", refreshed, err)
	}
	if _, err := service.ClaimCredit(ctx, created.ID); err != nil {
		t.Fatalf("ClaimCredit: %v", err)
	}
	if _, err := service.Assets(ctx, created.ID, 0, 50); err != nil {
		t.Fatalf("Assets: %v", err)
	}
	record, err := repository.Get(ctx, created.ID)
	if err != nil || record.Password != "plain-password" || record.SessionID != "session-new" {
		t.Fatalf("stored record: %+v err=%v", record, err)
	}
}
