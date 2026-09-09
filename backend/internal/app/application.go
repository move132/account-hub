package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"account-hub/internal/audit"
	"account-hub/internal/auth"
	"account-hub/internal/dreamina"
	"account-hub/internal/idempotency"
	"account-hub/internal/job"
	"account-hub/internal/platform/httpserver"
	platformsqlite "account-hub/internal/platform/sqlite"
	"account-hub/internal/platform/upstream"
	"account-hub/internal/scheduler"
	"account-hub/internal/settings"
	"account-hub/internal/token"
	"account-hub/internal/web"
)

type Application struct {
	config    Config
	logger    *slog.Logger
	db        *sql.DB
	server    *http.Server
	cancel    context.CancelFunc
	jobs      *job.Manager
	scheduler *scheduler.Scheduler
	workers   sync.WaitGroup
	upstreams []*upstream.Client
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Application, error) {
	db, err := platformsqlite.Open(ctx, config.DatabasePath)
	if err != nil {
		return nil, err
	}
	settingRepository := settings.NewRepository(db)
	if err := settingRepository.EnsureDefaults(ctx); err != nil {
		db.Close()
		return nil, err
	}
	auditRepository := audit.NewRepository(db)
	authRepository := auth.NewRepository(db)
	if err := auth.Bootstrap(ctx, authRepository, config.AdminUsername, config.AdminPassword, config.AdminPasswordManaged); err != nil {
		db.Close()
		return nil, err
	}
	config.AdminPassword = ""
	config.AdminPasswordManaged = false
	authService := auth.NewService(authRepository, config.AdminPasswordManaged, config.AdminSessionTTL)
	authHandler := auth.NewHandler(authService, auditRepository)

	upstreamClient := upstream.NewClient(settingRepository, config.UpstreamRequestTimeout, config.UpstreamRetryCount).WithLogger(logger.With("component", "upstream"))
	tokenUpstreamClient := upstream.NewBrowserClient(settingRepository, config.UpstreamRequestTimeout, config.UpstreamRetryCount).WithLogger(logger.With("component", "token_upstream"))
	tokenRepository := token.NewRepository(db)
	tokenClient := token.NewClient(tokenUpstreamClient)
	tokenService := token.NewService(tokenRepository, tokenClient, settingRepository).WithLogger(logger.With("component", "token_service"))
	tokenContentHandler := token.NewContentHandler(tokenRepository, tokenClient, auditRepository)
	dreaminaRepository := dreamina.NewRepository(db)
	dreaminaService := dreamina.NewService(dreaminaRepository, dreamina.NewClient(upstreamClient, config.DisplayTimezone), settingRepository).WithLogger(logger.With("component", "dreamina_service"))

	jobManager := job.NewManager(db, logger)
	registerExecutors(jobManager, settingRepository, tokenService, dreaminaService)
	tokenHandler := token.NewHandler(tokenService, auditRepository, jobManager)
	dreaminaHandler := dreamina.NewHandler(dreaminaService, auditRepository, jobManager)
	jobHandler := job.NewHandler(jobManager, auditRepository).
		WithTargetResolver("token", func(ctx context.Context, ids []string) ([]job.Target, error) {
			targets := make([]job.Target, 0, len(ids))
			for _, id := range ids {
				view, err := tokenService.Get(ctx, id)
				if err != nil {
					return nil, err
				}
				label := view.Email
				if label == "" {
					label = view.Name
				}
				if label == "" {
					label = id
				}
				targets = append(targets, job.Target{ID: id, Label: label})
			}
			return targets, nil
		}).
		WithTargetResolver("dreamina", func(ctx context.Context, ids []string) ([]job.Target, error) {
			targets := make([]job.Target, 0, len(ids))
			for _, id := range ids {
				view, err := dreaminaService.Get(ctx, id)
				if err != nil {
					return nil, err
				}
				label := view.Email
				if label == "" {
					label = id
				}
				targets = append(targets, job.Target{ID: id, Label: label})
			}
			return targets, nil
		})
	jobScheduler := scheduler.New(jobManager, settingRepository, tokenService, dreaminaService, logger)
	settingHandler := settings.NewHandler(settingRepository, auditRepository)
	auditHandler := audit.NewHandler(auditRepository)
	idempotencyMiddleware := idempotency.New(db)
	mutation := func(handler http.Handler) http.Handler {
		return authHandler.RequireMutation(idempotencyMiddleware.Wrap(handler))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", authHandler.Login)
	mux.Handle("POST /api/v1/auth/logout", authHandler.RequireMutation(http.HandlerFunc(authHandler.Logout)))
	mux.Handle("GET /api/v1/auth/session", authHandler.Require(http.HandlerFunc(authHandler.CurrentSession)))
	mux.Handle("PATCH /api/v1/auth/password", authHandler.RequireMutation(http.HandlerFunc(authHandler.ChangePassword)))

	mux.Handle("GET /api/v1/tokens", authHandler.Require(http.HandlerFunc(tokenHandler.List)))
	mux.Handle("POST /api/v1/tokens", mutation(http.HandlerFunc(tokenHandler.Create)))
	mux.Handle("GET /api/v1/tokens/{id}", authHandler.Require(http.HandlerFunc(tokenHandler.Get)))
	mux.Handle("GET /api/v1/tokens/{id}/edit", authHandler.Require(http.HandlerFunc(tokenHandler.GetForEdit)))
	mux.Handle("PATCH /api/v1/tokens/{id}", authHandler.RequireMutation(http.HandlerFunc(tokenHandler.Update)))
	mux.Handle("DELETE /api/v1/tokens/{id}", authHandler.RequireMutation(http.HandlerFunc(tokenHandler.Delete)))
	mux.Handle("POST /api/v1/tokens/{id}/checks", mutation(http.HandlerFunc(tokenHandler.Check)))
	mux.Handle("POST /api/v1/tokens/{id}/refreshes", mutation(http.HandlerFunc(tokenHandler.Refresh)))
	mux.Handle("GET /api/v1/tokens/{id}/attempts", authHandler.Require(http.HandlerFunc(tokenHandler.Attempts)))
	mux.Handle("GET /api/v1/tokens/{id}/storage", authHandler.Require(http.HandlerFunc(tokenContentHandler.Storage)))
	mux.Handle("GET /api/v1/tokens/{id}/library/files", authHandler.Require(http.HandlerFunc(tokenContentHandler.Files)))
	mux.Handle("POST /api/v1/tokens/{id}/library/deletions", mutation(http.HandlerFunc(tokenContentHandler.DeleteFiles)))
	mux.Handle("GET /api/v1/tokens/{id}/conversations", authHandler.Require(http.HandlerFunc(tokenContentHandler.Conversations)))
	mux.Handle("GET /api/v1/tokens/{id}/conversations/{conversation_id}", authHandler.Require(http.HandlerFunc(tokenContentHandler.Conversation)))
	mux.Handle("GET /api/v1/tokens/{id}/files/{file_id}/image", authHandler.Require(http.HandlerFunc(tokenContentHandler.Image)))
	mux.Handle("POST /api/v1/token-imports", mutation(http.HandlerFunc(tokenHandler.Import)))
	mux.Handle("POST /api/v1/token-exports", authHandler.RequireMutation(http.HandlerFunc(tokenHandler.Export)))
	mux.Handle("POST /api/v1/token-batch-jobs", authHandler.RequireMutation(jobHandler.Create("token")))

	mux.Handle("GET /api/v1/dreamina/accounts", authHandler.Require(http.HandlerFunc(dreaminaHandler.List)))
	mux.Handle("POST /api/v1/dreamina/accounts", mutation(http.HandlerFunc(dreaminaHandler.Create)))
	mux.Handle("GET /api/v1/dreamina/accounts/{id}", authHandler.Require(http.HandlerFunc(dreaminaHandler.Get)))
	mux.Handle("GET /api/v1/dreamina/accounts/{id}/edit", authHandler.Require(http.HandlerFunc(dreaminaHandler.GetForEdit)))
	mux.Handle("PATCH /api/v1/dreamina/accounts/{id}", authHandler.RequireMutation(http.HandlerFunc(dreaminaHandler.Update)))
	mux.Handle("DELETE /api/v1/dreamina/accounts/{id}", authHandler.RequireMutation(http.HandlerFunc(dreaminaHandler.Delete)))
	mux.Handle("POST /api/v1/dreamina/accounts/{id}/checks", mutation(http.HandlerFunc(dreaminaHandler.Check)))
	mux.Handle("POST /api/v1/dreamina/accounts/{id}/refreshes", mutation(http.HandlerFunc(dreaminaHandler.Refresh)))
	mux.Handle("GET /api/v1/dreamina/accounts/{id}/attempts", authHandler.Require(http.HandlerFunc(dreaminaHandler.Attempts)))
	mux.Handle("GET /api/v1/dreamina/accounts/{id}/credit", authHandler.Require(http.HandlerFunc(dreaminaHandler.Credit)))
	mux.Handle("POST /api/v1/dreamina/accounts/{id}/credit-claims", mutation(http.HandlerFunc(dreaminaHandler.ClaimCredit)))
	mux.Handle("GET /api/v1/dreamina/accounts/{id}/assets", authHandler.Require(http.HandlerFunc(dreaminaHandler.Assets)))
	mux.Handle("POST /api/v1/dreamina-imports", mutation(http.HandlerFunc(dreaminaHandler.Import)))
	mux.Handle("POST /api/v1/dreamina-exports", authHandler.RequireMutation(http.HandlerFunc(dreaminaHandler.Export)))
	mux.Handle("POST /api/v1/dreamina-batch-jobs", authHandler.RequireMutation(jobHandler.Create("dreamina")))

	mux.Handle("GET /api/v1/jobs", authHandler.Require(http.HandlerFunc(jobHandler.List)))
	mux.Handle("DELETE /api/v1/jobs", authHandler.RequireMutation(http.HandlerFunc(jobHandler.DeleteBatch)))
	mux.Handle("GET /api/v1/jobs/{id}", authHandler.Require(http.HandlerFunc(jobHandler.Get)))
	mux.Handle("DELETE /api/v1/jobs/{id}", authHandler.RequireMutation(http.HandlerFunc(jobHandler.Delete)))
	mux.Handle("POST /api/v1/jobs/{id}/cancellations", authHandler.RequireMutation(http.HandlerFunc(jobHandler.Cancel)))
	mux.Handle("GET /api/v1/settings", authHandler.Require(http.HandlerFunc(settingHandler.Get)))
	mux.Handle("PATCH /api/v1/settings", authHandler.RequireMutation(http.HandlerFunc(settingHandler.Patch)))
	mux.Handle("POST /api/v1/history-log-cleanups", authHandler.RequireMutation(http.HandlerFunc(settingHandler.CleanupHistory)))
	mux.Handle("GET /api/v1/audit-logs", authHandler.Require(http.HandlerFunc(auditHandler.List)))

	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		httpserver.Write(w, http.StatusOK, map[string]any{"status": "live"}, "服务正常")
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if err := db.PingContext(r.Context()); err != nil {
			httpserver.Error(w, r, http.StatusServiceUnavailable, "DATABASE_UNAVAILABLE", "数据库不可用", nil)
			return
		}
		httpserver.Write(w, http.StatusOK, map[string]any{"status": "ready"}, "服务就绪")
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		httpserver.Error(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND", "接口不存在", nil)
	})
	mux.Handle("/", web.Handler())

	workerContext, cancel := context.WithCancel(context.Background())
	application := &Application{config: config, logger: logger, db: db, cancel: cancel, jobs: jobManager, scheduler: jobScheduler, upstreams: []*upstream.Client{upstreamClient, tokenUpstreamClient}}
	application.server = &http.Server{
		Addr: config.ListenAddress, Handler: httpserver.Middleware(logger, mux),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 35 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: 60 * time.Second,
	}
	application.workers.Add(2)
	go func() {
		defer application.workers.Done()
		jobManager.Start(workerContext, config.JobScanInterval, config.RefreshWorkerConcurrency)
	}()
	go func() {
		defer application.workers.Done()
		jobScheduler.Start(workerContext, config.JobScanInterval)
	}()
	return application, nil
}

func registerExecutors(manager *job.Manager, settingRepository *settings.Repository, tokens *token.Service, dreaminaService *dreamina.Service) {
	manager.Register("token", "import", func(ctx context.Context, _ string, payload json.RawMessage) error {
		var input token.CreateInput
		if err := json.Unmarshal(payload, &input); err != nil {
			return err
		}
		return tokens.Import(ctx, input)
	}, nil)
	manager.Register("token", "check", func(ctx context.Context, id string, _ json.RawMessage) error {
		_, err := tokens.Check(ctx, id)
		return err
	}, nil)
	manager.Register("token", "refresh", func(ctx context.Context, id string, _ json.RawMessage) error {
		_, err := tokens.Refresh(ctx, id)
		return err
	}, func(ctx context.Context) time.Duration {
		return time.Duration(settingInt(ctx, settingRepository, "token_refresh_account_interval_seconds", 6)) * time.Second
	})
	manager.Register("token", "enable", func(ctx context.Context, id string, _ json.RawMessage) error {
		return tokens.SetStatus(ctx, id, "enabled")
	}, nil)
	manager.Register("token", "disable", func(ctx context.Context, id string, _ json.RawMessage) error {
		return tokens.SetStatus(ctx, id, "disabled")
	}, nil)
	manager.Register("token", "delete", func(ctx context.Context, id string, _ json.RawMessage) error { return tokens.Delete(ctx, id) }, nil)
	manager.Register("dreamina", "import", func(ctx context.Context, _ string, payload json.RawMessage) error {
		var input dreamina.CreateInput
		if err := json.Unmarshal(payload, &input); err != nil {
			return err
		}
		return dreaminaService.Import(ctx, input)
	}, nil)
	manager.Register("dreamina", "check", func(ctx context.Context, id string, _ json.RawMessage) error {
		_, _, err := dreaminaService.Check(ctx, id)
		return err
	}, nil)
	manager.Register("dreamina", "refresh", func(ctx context.Context, id string, _ json.RawMessage) error {
		_, err := dreaminaService.Refresh(ctx, id)
		return err
	}, func(ctx context.Context) time.Duration {
		return time.Duration(settingInt(ctx, settingRepository, "dreamina_refresh_account_interval_minutes", 2)) * time.Minute
	})
	manager.Register("dreamina", "enable", func(ctx context.Context, id string, _ json.RawMessage) error {
		return dreaminaService.SetStatus(ctx, id, "enabled")
	}, nil)
	manager.Register("dreamina", "disable", func(ctx context.Context, id string, _ json.RawMessage) error {
		return dreaminaService.SetStatus(ctx, id, "disabled")
	}, nil)
	manager.Register("dreamina", "delete", func(ctx context.Context, id string, _ json.RawMessage) error { return dreaminaService.Delete(ctx, id) }, nil)
}

func settingInt(ctx context.Context, repository *settings.Repository, key string, fallback int) int {
	values, err := repository.Values(ctx)
	if err == nil {
		if value, ok := values[key].(int); ok {
			return value
		}
	}
	return fallback
}

func (a *Application) Run(listener net.Listener) error {
	a.logger.Info("account-hub starting", "address", a.config.ListenAddress)
	err := a.server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (a *Application) Shutdown(ctx context.Context) error {
	a.cancel()
	serverErr := a.server.Shutdown(ctx)
	done := make(chan struct{})
	go func() {
		a.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return errors.Join(serverErr, ctx.Err())
	}
	for _, client := range a.upstreams {
		client.CloseIdleConnections()
	}
	dbErr := a.db.Close()
	return errors.Join(serverErr, dbErr)
}

func (a *Application) Handler() http.Handler { return a.server.Handler }

func (a *Application) DB() *sql.DB { return a.db }
