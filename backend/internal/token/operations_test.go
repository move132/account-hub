package token

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"account-hub/internal/idempotency"
	platformsqlite "account-hub/internal/platform/sqlite"
	"account-hub/internal/platform/upstream"
	"account-hub/internal/settings"
)

type controlledClient struct {
	beforeCheck   func(context.Context) error
	beforeRefresh func(context.Context) error
}

func (c controlledClient) Check(ctx context.Context, _ string) (Profile, error) {
	if c.beforeCheck != nil {
		if err := c.beforeCheck(ctx); err != nil {
			return Profile{}, err
		}
	}
	return Profile{Email: "verified@example.com"}, nil
}
func (c controlledClient) Refresh(ctx context.Context, _ string) (RefreshResult, error) {
	if c.beforeRefresh != nil {
		if err := c.beforeRefresh(ctx); err != nil {
			return RefreshResult{}, err
		}
	}
	return RefreshResult{AccessToken: "refreshed-access", SessionToken: "refreshed-session"}, nil
}
func operationFixture(t *testing.T, client UpstreamClient) (*sql.DB, *Repository, *Service) {
	t.Helper()
	db, err := platformsqlite.Open(context.Background(), filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	config := settings.NewRepository(db)
	if err := config.EnsureDefaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(db)
	return db, repository, NewService(repository, client, config)
}
func awaitOperation(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("operation did not reach provider")
	}
}
func awaitResult(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("operation did not finish")
		return nil
	}
}
func TestOperationCannotOverwriteNewCredentials(t *testing.T) {
	for _, operation := range []string{"check", "refresh"} {
		t.Run(operation, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			block := func(context.Context) error { close(entered); <-release; return nil }
			client := controlledClient{}
			if operation == "check" {
				client.beforeCheck = block
			} else {
				client.beforeRefresh = block
			}
			_, repository, service := operationFixture(t, client)
			created, err := service.Create(context.Background(), CreateInput{AccessToken: "old-access", SessionToken: "old-session"})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				var err error
				if operation == "check" {
					_, err = service.Check(context.Background(), created.ID)
				} else {
					_, err = service.Refresh(context.Background(), created.ID)
				}
				done <- err
			}()
			awaitOperation(t, entered)
			replacement := "edited-session"
			edited, editErr := repository.Update(context.Background(), created.ID, UpdateInput{Version: created.Version, SessionToken: &replacement})
			close(release)
			if editErr != nil {
				t.Fatal(editErr)
			}
			if err := awaitResult(t, done); !errors.Is(err, ErrVersionConflict) {
				t.Fatalf("want stale credential conflict, got %v", err)
			}
			stored, err := repository.Get(context.Background(), created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.SessionToken != replacement || stored.CredentialVersion != edited.CredentialVersion || stored.CheckState != "unchecked" {
				t.Fatalf("stale operation changed edited account: %+v", stored)
			}
			attempts, err := repository.Attempts(context.Background(), created.ID, 10)
			if err != nil || len(attempts) != 0 {
				t.Fatalf("stale result was recorded as committed: %v %v", attempts, err)
			}
		})
	}
}
func TestMetadataEditDoesNotInvalidateRefresh(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	_, repository, service := operationFixture(t, controlledClient{beforeRefresh: func(context.Context) error { close(entered); <-release; return nil }})
	created, err := service.Create(context.Background(), CreateInput{AccessToken: "old-access", SessionToken: "old-session"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := service.Refresh(context.Background(), created.ID); done <- err }()
	awaitOperation(t, entered)
	note := "edited while refreshing"
	unchangedSession := "old-session"
	_, editErr := repository.Update(context.Background(), created.ID, UpdateInput{Version: created.Version, Note: &note, SessionToken: &unchangedSession})
	close(release)
	if editErr != nil {
		t.Fatal(editErr)
	}
	if err := awaitResult(t, done); err != nil {
		t.Fatal(err)
	}
	record, err := repository.Get(context.Background(), created.ID)
	if err != nil || record.Note != note || record.SessionToken != "refreshed-session" {
		t.Fatalf("metadata/refresh lost: %+v %v", record, err)
	}
}
func TestAccountAndAttemptCommitTogether(t *testing.T) {
	db, repository, service := operationFixture(t, controlledClient{})
	created, err := service.Create(context.Background(), CreateInput{AccessToken: "old-access", SessionToken: "old-session"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TRIGGER reject_attempt BEFORE INSERT ON token_refresh_attempts BEGIN SELECT RAISE(ABORT,'injected history failure'); END;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background(), created.ID); err == nil {
		t.Fatal("accepted failed history write")
	}
	record, err := repository.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.SessionToken != "old-session" || record.Version != created.Version || record.LastRefreshedAt != nil {
		t.Fatalf("partial operation commit: %+v", record)
	}
	attempts, err := repository.Attempts(context.Background(), created.ID, 10)
	if err != nil || len(attempts) != 0 {
		t.Fatalf("unexpected attempts: %v %v", attempts, err)
	}
}
func TestCommittedOperationReplayDoesNotCallProvider(t *testing.T) {
	calls := 0
	_, repository, service := operationFixture(t, controlledClient{beforeRefresh: func(context.Context) error { calls++; return nil }})
	created, err := service.Create(context.Background(), CreateInput{AccessToken: "old-access", SessionToken: "old-session"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithOperationKey(context.Background(), "job-item-stable")
	for i := 0; i < 2; i++ {
		if _, err := service.Refresh(ctx, created.ID); err != nil {
			t.Fatal(err)
		}
	}
	attempts, err := repository.Attempts(ctx, created.ID, 10)
	if err != nil || calls != 1 || len(attempts) != 1 {
		t.Fatalf("replayed committed side effect: calls=%d attempts=%d err=%v", calls, len(attempts), err)
	}
}
func TestCancelledRefreshDoesNotWaitNormalInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, repository, service := operationFixture(t, controlledClient{beforeRefresh: func(context.Context) error { cancel(); return context.Canceled }})
	created, err := service.Create(ctx, CreateInput{AccessToken: "old-access", SessionToken: "old-session"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(ctx, created.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
	record, err := repository.Get(context.Background(), created.ID)
	if err != nil || record.NextRefreshAt == nil || *record.NextRefreshAt != 0 {
		t.Fatalf("cancelled refresh was deferred: %+v %v", record, err)
	}
	attempts, err := repository.Attempts(context.Background(), created.ID, 10)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "failed" {
		t.Fatalf("cancellation outcome missing: %+v %v", attempts, err)
	}
}
func TestCreateAndIdempotencyCommitTogether(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "rollback"
		}
		t.Run(name, func(t *testing.T) {
			db, _, service := operationFixture(t, controlledClient{})
			store := idempotency.NewStore(db)
			request := idempotency.Request{ActorID: "admin", Route: "/accounts", Key: "create-1", Hash: "request-hash"}
			record, owned, err := store.Reserve(context.Background(), request)
			if err != nil || !owned {
				t.Fatalf("reserve: %v owned=%v", err, owned)
			}
			if fail {
				_, err = db.Exec(`CREATE TRIGGER reject_idem BEFORE UPDATE OF state ON idempotency_keys WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT,'injected completion failure'); END;`)
				if err != nil {
					t.Fatal(err)
				}
			}
			view, createErr := NewHandler(service, nil, nil).create(idempotency.WithRecord(context.Background(), record), CreateInput{AccessToken: "old-access", SessionToken: "old-session"})
			var count int
			if err := db.QueryRow("SELECT COUNT(1) FROM tokens").Scan(&count); err != nil {
				t.Fatal(err)
			}
			completed, found, err := store.Lookup(context.Background(), request)
			if err != nil || !found {
				t.Fatalf("lookup: %v found=%v", err, found)
			}
			if fail {
				if createErr == nil || count != 0 || completed.State != "processing" {
					t.Fatalf("partial create: count=%d state=%s err=%v", count, completed.State, createErr)
				}
			} else {
				if createErr != nil || count != 1 || completed.State != "completed" || completed.ResourceID != view.ID || !strings.Contains(completed.Body, view.ID) {
					t.Fatalf("missing atomic replay: %+v err=%v", completed, createErr)
				}
				if strings.Contains(completed.Body, "old-session") || strings.Contains(completed.Body, "old-access") {
					t.Fatal("idempotency body contains clear credentials")
				}
			}
		})
	}
}

func TestCommittedReplaySurvivesCredentialRemoval(t *testing.T) {
	calls := 0
	client := controlledClient{beforeRefresh: func(context.Context) error { calls++; return nil }}
	_, repository, service := operationFixture(t, client)
	created, err := service.Create(context.Background(), CreateInput{AccessToken: "old-access", SessionToken: "old-session"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithOperationKey(context.Background(), "completed-before-clear")
	_, err = service.Refresh(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := repository.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	empty := ""
	if _, err := repository.Update(context.Background(), created.ID, UpdateInput{Version: current.Version, SessionToken: &empty}); err != nil {
		t.Fatal(err)
	}
	_, err = service.Refresh(ctx, created.ID)
	if err != nil || calls != 1 {
		t.Fatalf("committed operation was revalidated or replayed: calls=%d err=%v", calls, err)
	}
}

func TestRefreshBackoffSurvivesServiceRecreation(t *testing.T) {
	db, repository, service := operationFixture(t, controlledClient{beforeRefresh: func(context.Context) error { return &upstream.Error{Code: upstream.Timeout, Message: "temporary"} }})
	now := time.Unix(1700000000, 0)
	service.now = func() time.Time { return now }
	created, err := service.Create(context.Background(), CreateInput{AccessToken: "old-access", SessionToken: "old-session"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background(), created.ID); err == nil {
		t.Fatal("expected provider timeout")
	}
	first, err := repository.Get(context.Background(), created.ID)
	if err != nil || first.RefreshFailures != 1 || first.NextRefreshAt == nil || *first.NextRefreshAt != now.Add(5*time.Minute).UnixMilli() {
		t.Fatalf("first backoff: %+v err=%v", first, err)
	}
	restarted := NewService(repository, controlledClient{beforeRefresh: func(context.Context) error { return &upstream.Error{Code: upstream.Timeout, Message: "temporary"} }}, settings.NewRepository(db))
	restarted.now = func() time.Time { return now }
	if _, err := restarted.Refresh(context.Background(), created.ID); err == nil {
		t.Fatal("expected provider timeout")
	}
	second, err := repository.Get(context.Background(), created.ID)
	if err != nil || second.RefreshFailures != 2 || second.NextRefreshAt == nil || *second.NextRefreshAt != now.Add(10*time.Minute).UnixMilli() {
		t.Fatalf("restarted backoff: %+v err=%v", second, err)
	}
	restarted.client = controlledClient{}
	if _, err := restarted.Refresh(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := repository.Get(context.Background(), created.ID)
	if err != nil || recovered.RefreshFailures != 0 {
		t.Fatalf("failure count was not reset: %+v %v", recovered, err)
	}
}
