package app

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"account-hub/internal/apikey"
	"account-hub/internal/audit"
	"account-hub/internal/auth"
	"account-hub/internal/dreamina"
	"account-hub/internal/idempotency"
	"account-hub/internal/job"
	"account-hub/internal/overview"
	"account-hub/internal/platform/httpserver"
	"account-hub/internal/platform/logging"
	platformsqlite "account-hub/internal/platform/sqlite"
	"account-hub/internal/platform/upstream"
	"account-hub/internal/runtimelog"
	"account-hub/internal/scheduler"
	"account-hub/internal/settings"
	"account-hub/internal/token"
	"account-hub/internal/web"
)

type Application struct {
	config               Config
	initialAdminPassword string
	logger               *slog.Logger
	runtimeLogs          *logging.Buffer
	db                   *sql.DB
	server               *http.Server
	cancel               context.CancelFunc
	jobs                 *job.Manager
	scheduler            *scheduler.Scheduler
	workers              sync.WaitGroup
	upstreams            []*upstream.Client
	maintenance          func(context.Context) error
	lifecycleMu          sync.Mutex
	started              bool
	stopping             bool
	backgroundErr        error
	runErrors            chan error
	shutdownOnce         sync.Once
	shutdownDone         chan struct{}
	shutdownErr          error
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Application, error) {
	runtimeLogs := logging.NewBuffer()
	logger = logging.WithBuffer(logger, runtimeLogs)
	db, err := platformsqlite.Open(ctx, config.DatabasePath)
	if err != nil {
		return nil, err
	}
	settingRepository := settings.NewRepository(db)
	if err := settingRepository.EnsureDefaults(ctx); err != nil {
		db.Close()
		return nil, err
	}
	auditRepository := audit.NewRepository(db).WithLogger(logger.With("component", "audit"))
	apiKeyService := apikey.NewService(apikey.NewRepository(db).WithAudit(auditRepository))
	authRepository := auth.NewRepository(db).WithLogger(logger.With("component", "auth")).WithAudit(auditRepository)
	initialAdminPassword, err := auth.Bootstrap(ctx, authRepository, config.AdminUsername)
	if err != nil {
		db.Close()
		return nil, err
	}
	if initialAdminPassword != "" {
		logger.Info("generated initial admin password", "username", config.AdminUsername, "password", initialAdminPassword)
	}
	authService := auth.NewService(authRepository, config.AdminSessionTTL)
	authHandler := auth.NewHandler(authService, auditRepository)
	apiKeyMiddleware := apikey.NewMiddleware(apiKeyService)

	upstreamClient := upstream.NewClient(settingRepository, config.UpstreamRequestTimeout, config.UpstreamRetryCount).WithLogger(logger.With("component", "upstream"))
	tokenUpstreamClient := upstream.NewBrowserClient(settingRepository, config.UpstreamRequestTimeout, config.UpstreamRetryCount).WithLogger(logger.With("component", "token_upstream"))
	tokenRepository := token.NewRepository(db)
	tokenClient := token.NewClient(tokenUpstreamClient)
	tokenService := token.NewService(tokenRepository, tokenClient, settingRepository).WithLogger(logger.With("component", "token_service"))
	tokenContentService := token.NewContentService(tokenRepository, tokenClient)
	tokenContentHandler := token.NewContentHandlerWithService(tokenContentService, auditRepository)
	dreaminaRepository := dreamina.NewRepository(db)
	dreaminaService := dreamina.NewService(dreaminaRepository, dreamina.NewClient(upstreamClient, config.DisplayTimezone), settingRepository).WithLogger(logger.With("component", "dreamina_service"))

	jobManager := job.NewManager(db, logger)
	registerExecutors(jobManager, settingRepository, tokenService, dreaminaService)
	tokenHandler := token.NewHandler(tokenService, auditRepository, jobManager)
	dreaminaHandler := dreamina.NewHandler(dreaminaService, auditRepository, jobManager)
	jobHandler := job.NewHandler(jobManager, auditRepository).
		WithTargetResolver("token", func(ctx context.Context, ids []string) ([]job.Target, error) {
			labels, err := tokenService.TargetLabels(ctx, ids)
			if err != nil {
				return nil, err
			}
			return targetsFromLabels(ids, labels)
		}).
		WithTargetResolver("dreamina", func(ctx context.Context, ids []string) ([]job.Target, error) {
			labels, err := dreaminaService.TargetLabels(ctx, ids)
			if err != nil {
				return nil, err
			}
			return targetsFromLabels(ids, labels)
		})
	jobScheduler := scheduler.New(jobManager, logger,
		scheduler.Schedule{TargetKind: "token", Source: tokenService, Policy: func(ctx context.Context) (bool, int, error) {
			snapshot, err := settingRepository.Snapshot(ctx)
			return snapshot.Token.Enabled, snapshot.Token.BatchSize, err
		}},
		scheduler.Schedule{TargetKind: "dreamina", Source: dreaminaService, Policy: func(ctx context.Context) (bool, int, error) {
			snapshot, err := settingRepository.Snapshot(ctx)
			return snapshot.Dreamina.Enabled, snapshot.Dreamina.BatchSize, err
		}},
	)
	settingService := settings.NewService(settingRepository, func(ctx context.Context, db platformsqlite.DBTX, change settings.Change) error {
		if change.After.Token.Enabled && (change.Keys["token_refresh_interval_hours"] || change.Keys["token_auto_refresh_enabled"]) {
			if err := tokenRepository.Reschedule(ctx, db, change.After.Token.Interval.Milliseconds()); err != nil {
				return err
			}
		}
		if change.After.Dreamina.Enabled && (change.Keys["dreamina_refresh_interval_days"] || change.Keys["dreamina_auto_refresh_enabled"]) {
			if err := dreaminaRepository.Reschedule(ctx, db, change.After.Dreamina.Interval.Milliseconds()); err != nil {
				return err
			}
		}
		return nil
	}).WithAudit(auditRepository)
	settingHandler := settings.NewHandler(settingService, settings.NewMaintenance(db).WithAudit(auditRepository)).WithAPIKeys(apiKeyService)
	auditHandler := audit.NewHandler(auditRepository)
	runtimeLogHandler := runtimelog.NewHandler(runtimeLogs, authRepository)
	idempotencyStore := idempotency.NewStore(db)
	idempotencyMiddleware := idempotency.New(db)
	mutation := func(handler http.Handler) http.Handler {
		return authHandler.RequireMutation(idempotencyMiddleware.Wrap(handler))
	}
	tokenRead := func(handler http.Handler) http.Handler {
		return apiKeyMiddleware.Allow(handler, authHandler.Require(handler))
	}
	tokenMutation := func(handler http.Handler) http.Handler {
		return apiKeyMiddleware.Allow(handler, authHandler.RequireMutation(handler))
	}
	tokenCommand := func(handler http.Handler) http.Handler {
		return tokenMutation(idempotencyMiddleware.Wrap(handler))
	}

	application := &Application{
		config: config, initialAdminPassword: initialAdminPassword, logger: logger, runtimeLogs: runtimeLogs,
		db: db, jobs: jobManager, scheduler: jobScheduler,
		upstreams: []*upstream.Client{upstreamClient, tokenUpstreamClient},
		runErrors: make(chan error, 1), shutdownDone: make(chan struct{}),
		maintenance: func(ctx context.Context) error {
			sessionErr := authRepository.DeleteExpiredSessions(ctx)
			deleted, keyErr := idempotencyStore.PruneExpired(ctx)
			if deleted > 0 {
				logger.Info("expired idempotency records removed", "count", deleted)
			}
			return errors.Join(sessionErr, keyErr)
		},
	}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/overview", authHandler.Require(http.HandlerFunc(overview.NewHandler(db).Get)))
	mux.HandleFunc("POST /api/v1/auth/login", authHandler.Login)
	mux.Handle("POST /api/v1/auth/logout", authHandler.RequireMutation(http.HandlerFunc(authHandler.Logout)))
	mux.Handle("GET /api/v1/auth/session", authHandler.Require(http.HandlerFunc(authHandler.CurrentSession)))
	mux.Handle("PATCH /api/v1/auth/password", authHandler.RequireMutation(http.HandlerFunc(authHandler.ChangePassword)))

	mux.Handle("GET /api/v1/tokens", tokenRead(http.HandlerFunc(tokenHandler.List)))
	mux.Handle("POST /api/v1/tokens", tokenCommand(http.HandlerFunc(tokenHandler.Create)))
	mux.Handle("GET /api/v1/tokens/{id}", tokenRead(http.HandlerFunc(tokenHandler.Get)))
	mux.Handle("GET /api/v1/tokens/{id}/edit", authHandler.Require(http.HandlerFunc(tokenHandler.GetForEdit)))
	mux.Handle("PATCH /api/v1/tokens/{id}", tokenMutation(http.HandlerFunc(tokenHandler.Update)))
	mux.Handle("DELETE /api/v1/tokens/{id}", tokenMutation(http.HandlerFunc(tokenHandler.Delete)))
	mux.Handle("POST /api/v1/tokens/{id}/checks", tokenCommand(http.HandlerFunc(tokenHandler.Check)))
	mux.Handle("POST /api/v1/tokens/{id}/refreshes", tokenCommand(http.HandlerFunc(tokenHandler.Refresh)))
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
	mux.Handle("GET /api/v1/runtime-logs/stream", authHandler.Require(http.HandlerFunc(runtimeLogHandler.Stream)))

	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		httpserver.Write(w, http.StatusOK, map[string]any{"status": "live"}, "服务正常")
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if err := application.ready(r.Context()); err != nil {
			httpserver.Error(w, r, http.StatusServiceUnavailable, "SERVICE_NOT_READY", "服务尚未就绪", nil)
			return
		}
		httpserver.Write(w, http.StatusOK, map[string]any{"status": "ready"}, "服务就绪")
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		httpserver.Error(w, r, http.StatusNotFound, "ROUTE_NOT_FOUND", "接口不存在", nil)
	})
	mux.Handle("/", web.Handler())

	application.server = &http.Server{
		Addr: config.ListenAddress, Handler: httpserver.Middleware(logger, mux),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 35 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: 60 * time.Second,
	}
	return application, nil
}

func (a *Application) Handler() http.Handler { return a.server.Handler }

func (a *Application) DB() *sql.DB { return a.db }
