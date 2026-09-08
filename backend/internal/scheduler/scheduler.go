package scheduler

import (
	"context"
	"log/slog"
	"time"

	"account-hub/internal/dreamina"
	"account-hub/internal/job"
	"account-hub/internal/settings"
	"account-hub/internal/token"
)

type Scheduler struct {
	manager  *job.Manager
	settings *settings.Repository
	tokens   *token.Service
	dreamina *dreamina.Service
	logger   *slog.Logger
}

func New(manager *job.Manager, settingRepository *settings.Repository, tokens *token.Service, dreaminaService *dreamina.Service, logger *slog.Logger) *Scheduler {
	return &Scheduler{manager: manager, settings: settingRepository, tokens: tokens, dreamina: dreaminaService, logger: logger}
}

func (s *Scheduler) Start(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	s.scan(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scan(ctx)
		}
	}
}

func (s *Scheduler) scan(ctx context.Context) {
	values, err := s.settings.Values(ctx)
	if err != nil {
		s.logger.Error("load scheduler settings", "error", err)
		return
	}
	if enabled, _ := values["token_auto_refresh_enabled"].(bool); enabled {
		limit := intValue(values, "token_refresh_batch_size", 100)
		ids, err := s.tokens.DueIDs(ctx, limit)
		if err != nil {
			s.logger.Error("load due tokens", "error", err)
		} else if len(ids) > 0 {
			if _, _, err := s.manager.Create(ctx, "token", "refresh", "", "scheduler/token", "", "", targets(ids)); err != nil {
				s.logger.Debug("schedule token refresh", "error", err)
			}
		}
	}
	if enabled, _ := values["dreamina_auto_refresh_enabled"].(bool); enabled {
		limit := intValue(values, "dreamina_refresh_batch_size", 100)
		ids, err := s.dreamina.DueIDs(ctx, limit)
		if err != nil {
			s.logger.Error("load due dreamina accounts", "error", err)
		} else if len(ids) > 0 {
			if _, _, err := s.manager.Create(ctx, "dreamina", "refresh", "", "scheduler/dreamina", "", "", targets(ids)); err != nil {
				s.logger.Debug("schedule dreamina refresh", "error", err)
			}
		}
	}
}

func targets(ids []string) []job.Target {
	result := make([]job.Target, 0, len(ids))
	for _, id := range ids {
		result = append(result, job.Target{ID: id, Label: id})
	}
	return result
}

func intValue(values map[string]any, key string, fallback int) int {
	if value, ok := values[key].(int); ok {
		return value
	}
	return fallback
}
