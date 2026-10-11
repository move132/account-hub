package scheduler

import (
	"context"
	"log/slog"
	"time"

	"account-hub/internal/job"
)

type Submitter interface {
	Submit(context.Context, job.Submission) (job.Job, bool, error)
}
type Settings interface {
	Values(context.Context) (map[string]any, error)
}
type DueSource interface {
	DueIDs(context.Context, int) ([]string, error)
}

// Schedule belongs to application wiring; adding a provider does not add a
// dependency on its domain service to the scheduling loop.
type Schedule struct {
	Policy           func(context.Context) (enabled bool, batchSize int, err error)
	TargetKind       string
	EnabledSetting   string
	BatchSizeSetting string
	Source           DueSource
}
type Scheduler struct {
	manager   Submitter
	settings  Settings
	schedules []Schedule
	logger    *slog.Logger
}

func New(manager Submitter, settings Settings, tokens DueSource, dreamina DueSource, logger *slog.Logger) *Scheduler {
	return NewWithSchedules(manager, settings, logger,
		Schedule{TargetKind: "token", EnabledSetting: "token_auto_refresh_enabled", BatchSizeSetting: "token_refresh_batch_size", Source: tokens},
		Schedule{TargetKind: "dreamina", EnabledSetting: "dreamina_auto_refresh_enabled", BatchSizeSetting: "dreamina_refresh_batch_size", Source: dreamina})
}
func NewWithSchedules(manager Submitter, settings Settings, logger *slog.Logger, schedules ...Schedule) *Scheduler {
	return &Scheduler{manager: manager, settings: settings, logger: logger, schedules: append([]Schedule(nil), schedules...)}
}
func (s *Scheduler) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		s.logger.Error("scheduler interval must be positive")
		return
	}
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
	if ctx.Err() != nil {
		return
	}
	var values map[string]any
	for _, schedule := range s.schedules {
		if schedule.Policy != nil {
			continue
		}
		if s.settings == nil {
			s.logger.Error("scheduler settings source missing")
			return
		}
		var err error
		values, err = s.settings.Values(ctx)
		if err != nil {
			s.logger.Error("load scheduler settings", "error", err)
			return
		}
		break
	}
	for _, schedule := range s.schedules {
		var enabled bool
		var batchSize int
		if schedule.Policy != nil {
			var err error
			enabled, batchSize, err = schedule.Policy(ctx)
			if err != nil {
				s.logger.Error("load schedule policy", "target_kind", schedule.TargetKind, "error", err)
				continue
			}
		} else {
			enabled, _ = values[schedule.EnabledSetting].(bool)
			batchSize = intValue(values, schedule.BatchSizeSetting, 100)
		}
		if !enabled {
			continue
		}
		if batchSize <= 0 {
			batchSize = 100
		}
		ids, err := schedule.Source.DueIDs(ctx, batchSize)
		if err != nil {
			s.logger.Error("load due accounts", "target_kind", schedule.TargetKind, "error", err)
			continue
		}
		if len(ids) == 0 {
			continue
		}
		_, _, err = s.manager.Submit(ctx, job.Submission{TargetKind: schedule.TargetKind, Action: "refresh", Route: "scheduler/" + schedule.TargetKind, Targets: targets(ids)})
		if err != nil {
			s.logger.Debug("schedule account refresh", "target_kind", schedule.TargetKind, "error", err)
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
	if value, ok := values[key].(int); ok && value > 0 {
		return value
	}
	return fallback
}
