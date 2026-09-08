package scheduler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"account-hub/internal/dreamina"
	"account-hub/internal/job"
	platformsqlite "account-hub/internal/platform/sqlite"
	"account-hub/internal/settings"
	"account-hub/internal/token"
)

type tokenClient struct{}

func (tokenClient) Check(context.Context, string) (token.Profile, error) { return token.Profile{}, nil }
func (tokenClient) Refresh(context.Context, string) (token.RefreshResult, error) {
	return token.RefreshResult{}, nil
}

type dreaminaClient struct{}

func (dreaminaClient) Login(context.Context, string, string) (dreamina.LoginResult, error) {
	return dreamina.LoginResult{}, nil
}
func (dreaminaClient) Check(context.Context, string) (dreamina.CreditResult, error) {
	return dreamina.CreditResult{}, nil
}
func (dreaminaClient) ClaimCredit(context.Context, string) (any, error)      { return nil, nil }
func (dreaminaClient) Assets(context.Context, string, int, int) (any, error) { return nil, nil }

func TestSchedulerCreatesPersistentRefreshJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	settingRepository := settings.NewRepository(db)
	if err := settingRepository.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := settingRepository.Update(ctx, map[string]any{"token_auto_refresh_enabled": true}); err != nil {
		t.Fatal(err)
	}
	tokenRepository := token.NewRepository(db)
	tokenService := token.NewService(tokenRepository, tokenClient{}, settingRepository)
	if _, err := tokenService.Create(ctx, token.CreateInput{Name: "due", AccessToken: "access", SessionToken: "session"}); err != nil {
		t.Fatal(err)
	}
	dreaminaRepository := dreamina.NewRepository(db)
	dreaminaService := dreamina.NewService(dreaminaRepository, dreaminaClient{}, settingRepository)
	manager := job.NewManager(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	manager.Register("token", "refresh", func(context.Context, string, json.RawMessage) error { return nil }, nil)
	scheduler := New(manager, settingRepository, tokenService, dreaminaService, slog.New(slog.NewTextHandler(io.Discard, nil)))
	go scheduler.Start(ctx, 10*time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := db.QueryRow("SELECT COUNT(1) FROM jobs WHERE target_kind='token' AND action='refresh'").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("scheduler did not create refresh job")
}
