package token

import (
	"context"
	"path/filepath"
	"testing"

	platformsqlite "account-hub/internal/platform/sqlite"
	"account-hub/internal/settings"
)

type fakeClient struct {
	profile Profile
	refresh RefreshResult
	err     error
}

func (f fakeClient) Check(context.Context, string) (Profile, error)         { return f.profile, f.err }
func (f fakeClient) Refresh(context.Context, string) (RefreshResult, error) { return f.refresh, f.err }

func TestServiceCreateCheckAndRefresh(t *testing.T) {
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
	client := fakeClient{profile: Profile{Email: "verified@example.com", UserID: "user_1"}, refresh: RefreshResult{AccessToken: "access-new", SessionToken: "session-new", Expires: "2030-01-01T00:00:00Z"}}
	service := NewService(repository, client, settingRepository)
	created, err := service.Create(ctx, CreateInput{Name: "Account", AccessToken: "access-old", SessionToken: "session-old"})
	if err != nil {
		t.Fatal(err)
	}
	if created.AccessTokenMasked == "access-old" || created.AccessTokenMasked == "" {
		t.Fatalf("secret was not masked: %q", created.AccessTokenMasked)
	}
	edit, err := service.GetForEdit(ctx, created.ID)
	if err != nil || edit.AccessToken != "access-old" || edit.SessionToken != "session-old" {
		t.Fatalf("GetForEdit: %+v err=%v", edit, err)
	}
	checked, err := service.Check(ctx, created.ID)
	if err != nil || checked.CheckState != "valid" || checked.Email != "verified@example.com" {
		t.Fatalf("Check: %+v err=%v", checked, err)
	}
	refreshed, err := service.Refresh(ctx, created.ID)
	if err != nil || refreshed.SessionTokenMasked == "session-new" || refreshed.LastRefreshedAt == nil {
		t.Fatalf("Refresh: %+v err=%v", refreshed, err)
	}
	record, err := repository.Get(ctx, created.ID)
	if err != nil || record.AccessToken != "access-new" || record.SessionToken != "session-new" {
		t.Fatalf("stored refresh: %+v err=%v", record, err)
	}
	attempts, err := service.Attempts(ctx, created.ID, 10)
	if err != nil || len(attempts) != 2 {
		t.Fatalf("attempts=%d err=%v", len(attempts), err)
	}
}
