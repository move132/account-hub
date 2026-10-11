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
type DueSource interface {
	DueIDs(context.Context, int) ([]string, error)
}

// Schedule belongs to application wiring; adding a provider does not add a
// dependency on its domain service to the scheduling loop.
type Schedule struct {
	Policy     func(context.Context) (enabled bool, batchSize int, err error)
	TargetKind string
	Source     DueSource
}
type Scheduler struct {
	manager   Submitter
	schedules []Schedule
	logger    *slog.Logger
}

func New(manager Submitter, logger *slog.Logger, schedules ...Schedule) *Scheduler {
	return &Scheduler{manager: manager, schedules: append([]Schedule(nil), schedules...), logger: logger}
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
	for _, schedule := range s.schedules {
		enabled, batchSize, err := schedule.Policy(ctx)
		if err != nil {
			s.logger.Error("load schedule policy", "target_kind", schedule.TargetKind, "error", err)
			continue
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
