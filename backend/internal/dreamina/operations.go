package dreamina

import (
	"context"
	"time"

	platformsqlite "account-hub/internal/platform/sqlite"
)

// completeOperation commits the account outcome and its history together. Network
// calls finish before this transaction begins. CredentialVersion fences stale
// responses without rejecting concurrent edits to names and notes.
func (r *Repository) completeOperation(ctx context.Context, record Record, attempt Attempt, assignments string, args ...any) (Record, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, err
	}
	defer tx.Rollback()
	args = append(args, record.ID, record.CredentialVersion)
	result, err := tx.ExecContext(ctx, "UPDATE dreamina_accounts SET "+assignments+" WHERE id = ? AND credential_version = ?", args...)
	if err != nil {
		return Record{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return Record{}, err
	}
	if changed == 0 {
		var exists int
		if err := tx.QueryRowContext(ctx, "SELECT 1 FROM dreamina_accounts WHERE id = ?", record.ID).Scan(&exists); err != nil {
			return Record{}, err
		}
		return Record{}, ErrVersionConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO dreamina_refresh_attempts
 (id, account_id, target_label, operation, status, error_code, error_message, started_at, completed_at, operation_key)
 VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?)`,
		attempt.ID, record.ID, attempt.TargetLabel, attempt.Operation, attempt.Status, attempt.ErrorCode, attempt.ErrorMessage, attempt.StartedAt, attempt.CompletedAt, attempt.OperationKey)
	if err != nil {
		return Record{}, err
	}
	updated, err := scanRecord(tx.QueryRowContext(ctx, "SELECT "+accountColumns+" FROM dreamina_accounts WHERE id = ?", record.ID))
	if err != nil {
		return Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return Record{}, err
	}
	return updated, nil
}

func (r *Repository) CompleteCheck(ctx context.Context, record Record, state, value, code, message string, attempt Attempt) (Record, error) {
	now := *attempt.CompletedAt
	return r.completeOperation(ctx, record, attempt, `check_state = ?,
 credit_balance = CASE WHEN ? <> '' THEN ? ELSE credit_balance END,
 last_checked_at = ?, last_error_code = NULLIF(?, ''), last_error_message = NULLIF(?, ''),
 version = version + 1, updated_at = ?`, state, value, value, now, code, message, now)
}

func (r *Repository) CompleteRefresh(ctx context.Context, record Record, login LoginResult, next int64, attempt Attempt) (Record, error) {
	now := *attempt.CompletedAt
	if attempt.Status == "failed" {
		return r.completeOperation(ctx, record, attempt, `next_refresh_at = ?, last_error_code = NULLIF(?, ''),
  last_error_message = NULLIF(?, ''), refresh_failures = refresh_failures + 1, version = version + 1, updated_at = ?`,
			next, attempt.ErrorCode, attempt.ErrorMessage, now)
	}
	return r.completeOperation(ctx, record, attempt, `session_id = ?, session_expires_at = NULLIF(?, ''),
 check_state = 'valid', last_refreshed_at = ?, last_checked_at = ?, next_refresh_at = ?,
 last_error_code = NULL, last_error_message = NULL, refresh_failures = 0, credential_version = credential_version + 1,
 version = version + 1, updated_at = ?`, login.SessionID, login.SessionExpiresAt, now, now, next, now)
}

// Reschedule participates in the settings transaction without transferring table
// ownership to the settings package.
func (r *Repository) Reschedule(ctx context.Context, db platformsqlite.DBTX, intervalMillis int64) error {
	_, err := db.ExecContext(ctx, `UPDATE dreamina_accounts SET next_refresh_at = CASE
 WHEN last_refreshed_at IS NULL THEN 0 ELSE last_refreshed_at + ? END WHERE status = 'enabled'`, intervalMillis)
	return err
}

// Once an upstream operation has finished, client disconnection must not prevent
// its local outcome from being recorded. The detached write is strictly bounded.
func completionContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}

// WithOperationKey associates a stable application command ID with its committed
// outcome. This is deliberately independent of the job transport.
type operationKeyContext struct{}

func WithOperationKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, operationKeyContext{}, key)
}

func operationKey(ctx context.Context) string {
	key, _ := ctx.Value(operationKeyContext{}).(string)
	return key
}

func (r *Repository) operationCompleted(ctx context.Context, id, operation string) (bool, error) {
	key := operationKey(ctx)
	if key == "" {
		return false, nil
	}
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(1) FROM dreamina_refresh_attempts WHERE operation_key = ? AND account_id = ? AND operation = ? AND status = 'succeeded'", key, id, operation).Scan(&count)
	return count > 0, err
}
