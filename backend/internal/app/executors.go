package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"account-hub/internal/dreamina"
	"account-hub/internal/job"
	"account-hub/internal/settings"
	"account-hub/internal/token"
)

func registerExecutors(manager *job.Manager, settingRepository *settings.Repository, tokens *token.Service, dreaminaService *dreamina.Service) {
	register := func(kind, action string, execute job.Executor, delay job.Delay) {
		manager.Register(kind, action, func(ctx context.Context, id string, payload json.RawMessage) error {
			err := execute(ctx, id, payload)
			if err == nil {
				return nil
			}
			code := token.ErrorCode(err)
			if kind == "dreamina" {
				code = dreamina.ErrorCode(err)
			}
			return &executionError{cause: err, code: code}
		}, delay)
	}
	register("token", "import", func(ctx context.Context, _ string, payload json.RawMessage) error {
		var input token.CreateInput
		if err := json.Unmarshal(payload, &input); err != nil {
			return err
		}
		return tokens.Import(ctx, input)
	}, nil)
	register("token", "check", func(ctx context.Context, id string, _ json.RawMessage) error {
		_, err := tokens.Check(token.WithOperationKey(ctx, jobOperationKey(ctx)), id)
		return err
	}, nil)
	register("token", "refresh", func(ctx context.Context, id string, _ json.RawMessage) error {
		_, err := tokens.Refresh(token.WithOperationKey(ctx, jobOperationKey(ctx)), id)
		return err
	}, func(ctx context.Context) time.Duration {
		policy, err := settingRepository.Snapshot(ctx)
		if err != nil {
			return 6 * time.Second
		}
		return policy.Token.AccountInterval
	})
	register("token", "enable", func(ctx context.Context, id string, _ json.RawMessage) error {
		return tokens.SetStatus(ctx, id, "enabled")
	}, nil)
	register("token", "disable", func(ctx context.Context, id string, _ json.RawMessage) error {
		return tokens.SetStatus(ctx, id, "disabled")
	}, nil)
	register("token", "delete", func(ctx context.Context, id string, _ json.RawMessage) error {
		err := tokens.Delete(ctx, id)
		if token.IsNotFound(err) {
			return nil
		}
		return err
	}, nil)
	register("dreamina", "import", func(ctx context.Context, _ string, payload json.RawMessage) error {
		var input dreamina.CreateInput
		if err := json.Unmarshal(payload, &input); err != nil {
			return err
		}
		return dreaminaService.Import(ctx, input)
	}, nil)
	register("dreamina", "check", func(ctx context.Context, id string, _ json.RawMessage) error {
		_, _, err := dreaminaService.Check(dreamina.WithOperationKey(ctx, jobOperationKey(ctx)), id)
		return err
	}, nil)
	register("dreamina", "refresh", func(ctx context.Context, id string, _ json.RawMessage) error {
		_, err := dreaminaService.Refresh(dreamina.WithOperationKey(ctx, jobOperationKey(ctx)), id)
		return err
	}, func(ctx context.Context) time.Duration {
		policy, err := settingRepository.Snapshot(ctx)
		if err != nil {
			return 2 * time.Minute
		}
		return policy.Dreamina.AccountInterval
	})
	register("dreamina", "enable", func(ctx context.Context, id string, _ json.RawMessage) error {
		return dreaminaService.SetStatus(ctx, id, "enabled")
	}, nil)
	register("dreamina", "disable", func(ctx context.Context, id string, _ json.RawMessage) error {
		return dreaminaService.SetStatus(ctx, id, "disabled")
	}, nil)
	register("dreamina", "delete", func(ctx context.Context, id string, _ json.RawMessage) error {
		err := dreaminaService.Delete(ctx, id)
		if dreamina.IsNotFound(err) {
			return nil
		}
		return err
	}, nil)
}

func jobOperationKey(ctx context.Context) string {
	execution, ok := job.ExecutionFromContext(ctx)
	if !ok {
		return ""
	}
	return execution.ItemID
}

func targetsFromLabels(ids []string, labels map[string]string) ([]job.Target, error) {
	targets := make([]job.Target, 0, len(ids))
	for _, id := range ids {
		label, found := labels[id]
		if !found {
			return nil, sql.ErrNoRows
		}
		targets = append(targets, job.Target{ID: id, Label: label})
	}
	return targets, nil
}

// The job adapter uses the same application error code as the HTTP adapter while
// preserving the cause for retry/cancellation classification and internal logs.
type executionError struct {
	cause error
	code  string
}

func (e *executionError) Error() string     { return e.cause.Error() }
func (e *executionError) Unwrap() error     { return e.cause }
func (e *executionError) ErrorCode() string { return e.code }
