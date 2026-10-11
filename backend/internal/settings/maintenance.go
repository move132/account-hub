package settings

import (
	"context"
	"database/sql"
	"time"

	"account-hub/internal/audit"
	"account-hub/internal/platform/apperror"
)

type CleanupResult struct {
	AuditLogs        int `json:"audit_logs"`
	TokenAttempts    int `json:"token_attempts"`
	DreaminaAttempts int `json:"dreamina_attempts"`
	Jobs             int `json:"jobs"`
}

// Maintenance owns cross-table retention independently of configuration storage.
type Maintenance struct {
	db    *sql.DB
	audit *audit.Repository
}

func NewMaintenance(db *sql.DB) *Maintenance { return &Maintenance{db: db} }

func (m *Maintenance) WithAudit(repository *audit.Repository) *Maintenance {
	m.audit = repository
	return m
}

func (m *Maintenance) CleanupHistory(ctx context.Context, before int64) (CleanupResult, error) {
	if before <= 0 {
		return CleanupResult{}, apperror.New(apperror.Validation, "before 必须是有效时间戳")
	}
	if before > time.Now().UTC().UnixMilli() {
		return CleanupResult{}, apperror.New(apperror.Validation, "清理时间不能晚于当前时间")
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return CleanupResult{}, err
	}
	defer tx.Rollback()
	var result CleanupResult
	const finishedJobs = "SELECT id FROM jobs WHERE created_at < ? AND state NOT IN ('queued', 'running') AND running = 0"
	if _, err := tx.ExecContext(ctx, "DELETE FROM idempotency_keys WHERE resource_type = 'job' AND resource_id IN ("+finishedJobs+")", before); err != nil {
		return CleanupResult{}, err
	}
	if result.Jobs, err = deleteWhere(ctx, tx, "DELETE FROM jobs WHERE id IN ("+finishedJobs+")", before); err != nil {
		return CleanupResult{}, err
	}
	// Successful attempt markers must survive while their job item can be replayed.
	if result.TokenAttempts, err = deleteWhere(ctx, tx, `DELETE FROM token_refresh_attempts WHERE started_at < ? AND status <> 'running'
  AND NOT EXISTS (SELECT 1 FROM job_items WHERE job_items.id = token_refresh_attempts.operation_key)`, before); err != nil {
		return CleanupResult{}, err
	}
	if result.DreaminaAttempts, err = deleteWhere(ctx, tx, `DELETE FROM dreamina_refresh_attempts WHERE started_at < ? AND status <> 'running'
  AND NOT EXISTS (SELECT 1 FROM job_items WHERE job_items.id = dreamina_refresh_attempts.operation_key)`, before); err != nil {
		return CleanupResult{}, err
	}
	if result.AuditLogs, err = deleteWhere(ctx, tx, "DELETE FROM audit_logs WHERE created_at < ?", before); err != nil {
		return CleanupResult{}, err
	}
	if m.audit != nil {
		entry, ok := audit.EntryFromContext(ctx)
		if !ok {
			entry = audit.Entry{Action: "history_cleanup", TargetKind: "settings", Summary: "清理历史日志"}
		}
		if err := m.audit.LogTx(ctx, tx, entry); err != nil {
			return CleanupResult{}, err
		}
	}
	return result, tx.Commit()
}

func deleteWhere(ctx context.Context, tx *sql.Tx, query string, args ...any) (int, error) {
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	changed, err := result.RowsAffected()
	return int(changed), err
}
