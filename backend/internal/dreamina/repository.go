package dreamina

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"account-hub/internal/platform/security"
)

var ErrVersionConflict = errors.New("version conflict")

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

const accountColumns = `id, email, password, session_id, COALESCE(session_expires_at, ''), status,
    check_state, credit_balance, note, next_refresh_at, last_checked_at, last_refreshed_at,
    COALESCE(last_error_code, ''), COALESCE(last_error_message, ''), version, created_at, updated_at`

type scanner interface{ Scan(...any) error }

func scanRecord(row scanner) (Record, error) {
	var record Record
	var next, checked, refreshed sql.NullInt64
	err := row.Scan(&record.ID, &record.Email, &record.Password, &record.SessionID, &record.SessionExpiresAt,
		&record.Status, &record.CheckState, &record.CreditBalance, &record.Note, &next, &checked, &refreshed,
		&record.LastErrorCode, &record.LastErrorMessage, &record.Version, &record.CreatedAt, &record.UpdatedAt)
	if next.Valid {
		record.NextRefreshAt = &next.Int64
	}
	if checked.Valid {
		record.LastCheckedAt = &checked.Int64
	}
	if refreshed.Valid {
		record.LastRefreshedAt = &refreshed.Int64
	}
	return record, err
}

func (r *Repository) Create(ctx context.Context, input CreateInput) (Record, error) {
	id, err := security.RandomID("dream")
	if err != nil {
		return Record{}, err
	}
	now := time.Now().UTC().UnixMilli()
	status := input.Status
	if status == "" {
		status = "enabled"
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO dreamina_accounts
        (id, email, password, session_id, session_expires_at, status, note, created_at, updated_at)
        VALUES(?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?)`, id, input.Email, input.Password, input.SessionID,
		input.SessionExpiresAt, status, input.Note, now, now)
	if err != nil {
		return Record{}, err
	}
	return r.Get(ctx, id)
}

func (r *Repository) Get(ctx context.Context, id string) (Record, error) {
	return scanRecord(r.db.QueryRowContext(ctx, "SELECT "+accountColumns+" FROM dreamina_accounts WHERE id = ?", id))
}

func (r *Repository) ExistingSessionIDs(ctx context.Context, ids []string) (map[string]struct{}, error) {
	existing := make(map[string]struct{})
	for start := 0; start < len(ids); start += 400 {
		batch := ids[start:min(start+400, len(ids))]
		args := make([]any, len(batch))
		for index, id := range batch {
			args[index] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		rows, err := r.db.QueryContext(ctx, "SELECT session_id FROM dreamina_accounts WHERE session_id <> '' AND session_id IN ("+placeholders+")", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			existing[id] = struct{}{}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return existing, nil
}

func (r *Repository) List(ctx context.Context, filter ListFilter) ([]Record, int, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 || filter.PageSize > 500 {
		filter.PageSize = 20
	}
	where := []string{"1=1"}
	args := []any{}
	if filter.Search != "" {
		where = append(where, "(email LIKE ? OR note LIKE ?)")
		search := "%" + filter.Search + "%"
		args = append(args, search, search)
	}
	if filter.Status == "enabled" || filter.Status == "disabled" {
		where = append(where, "status = ?")
		args = append(args, filter.Status)
	}
	clause := strings.Join(where, " AND ")
	var total int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(1) FROM dreamina_accounts WHERE "+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	sortColumn := map[string]string{"email": "email", "status": "status", "updated_at": "updated_at", "created_at": "created_at"}[filter.Sort]
	if sortColumn == "" {
		sortColumn = "created_at"
	}
	order := "DESC"
	if strings.EqualFold(filter.Order, "asc") {
		order = "ASC"
	}
	queryArgs := append(append([]any{}, args...), filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := r.db.QueryContext(ctx, "SELECT "+accountColumns+" FROM dreamina_accounts WHERE "+clause+" ORDER BY "+sortColumn+" "+order+" LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]Record, 0, filter.PageSize)
	for rows.Next() {
		item, err := scanRecord(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (r *Repository) Update(ctx context.Context, id string, input UpdateInput) (Record, error) {
	if input.Version < 1 {
		return Record{}, ErrVersionConflict
	}
	sets, args := []string{}, []any{}
	add := func(column string, value any) { sets, args = append(sets, column+" = ?"), append(args, value) }
	if input.Email != nil {
		add("email", *input.Email)
	}
	if input.Password != nil {
		add("password", *input.Password)
	}
	if input.SessionID != nil {
		add("session_id", *input.SessionID)
	}
	if input.SessionExpiresAt != nil {
		sets, args = append(sets, "session_expires_at = NULLIF(?, '')"), append(args, *input.SessionExpiresAt)
	}
	if input.Status != nil {
		add("status", *input.Status)
	}
	if input.Note != nil {
		add("note", *input.Note)
	}
	if len(sets) == 0 {
		return r.Get(ctx, id)
	}
	sets = append(sets, "version = version + 1", "updated_at = ?")
	args = append(args, time.Now().UTC().UnixMilli(), id, input.Version)
	result, err := r.db.ExecContext(ctx, "UPDATE dreamina_accounts SET "+strings.Join(sets, ", ")+" WHERE id = ? AND version = ?", args...)
	if err != nil {
		return Record{}, err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return Record{}, ErrVersionConflict
	}
	return r.Get(ctx, id)
}

func (r *Repository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, "DELETE FROM dreamina_accounts WHERE id = ?", id)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *Repository) UpdateCheck(ctx context.Context, id, state, credit, code, message string) error {
	now := time.Now().UTC().UnixMilli()
	_, err := r.db.ExecContext(ctx, `UPDATE dreamina_accounts SET check_state = ?,
        credit_balance = CASE WHEN ? <> '' THEN ? ELSE credit_balance END, last_checked_at = ?,
        last_error_code = NULLIF(?, ''), last_error_message = NULLIF(?, ''), version = version + 1,
        updated_at = ? WHERE id = ?`, state, credit, credit, now, code, message, now, id)
	return err
}

func (r *Repository) UpdateRefresh(ctx context.Context, id string, login LoginResult, next int64) error {
	now := time.Now().UTC().UnixMilli()
	_, err := r.db.ExecContext(ctx, `UPDATE dreamina_accounts SET session_id = ?, session_expires_at = NULLIF(?, ''),
        check_state = 'valid', last_refreshed_at = ?, last_checked_at = ?, next_refresh_at = ?,
        last_error_code = NULL, last_error_message = NULL, version = version + 1, updated_at = ? WHERE id = ?`,
		login.SessionID, login.SessionExpiresAt, now, now, next, now, id)
	return err
}

func (r *Repository) SetCredit(ctx context.Context, id, balance string) error {
	_, err := r.db.ExecContext(ctx, "UPDATE dreamina_accounts SET credit_balance = ?, updated_at = ? WHERE id = ?", balance, time.Now().UTC().UnixMilli(), id)
	return err
}

func (r *Repository) RecordAttempt(ctx context.Context, attempt Attempt) error {
	var accountID any
	if attempt.AccountID != "" {
		accountID = attempt.AccountID
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO dreamina_refresh_attempts
        (id, account_id, target_label, operation, status, error_code, error_message, started_at, completed_at)
        VALUES(?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?)`, attempt.ID, accountID,
		attempt.TargetLabel, attempt.Operation, attempt.Status, attempt.ErrorCode, attempt.ErrorMessage,
		attempt.StartedAt, attempt.CompletedAt)
	return err
}

func (r *Repository) Attempts(ctx context.Context, accountID string, limit int) ([]Attempt, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, COALESCE(account_id, ''), target_label, operation, status,
        COALESCE(error_code, ''), COALESCE(error_message, ''), started_at, completed_at
        FROM dreamina_refresh_attempts WHERE account_id = ? ORDER BY started_at DESC LIMIT ?`, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Attempt, 0, limit)
	for rows.Next() {
		var item Attempt
		var completed sql.NullInt64
		if err := rows.Scan(&item.ID, &item.AccountID, &item.TargetLabel, &item.Operation, &item.Status,
			&item.ErrorCode, &item.ErrorMessage, &item.StartedAt, &completed); err != nil {
			return nil, err
		}
		if completed.Valid {
			item.CompletedAt = &completed.Int64
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) DueIDs(ctx context.Context, now int64, limit int) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM dreamina_accounts WHERE status = 'enabled'
        AND COALESCE(next_refresh_at, 0) <= ? ORDER BY COALESCE(next_refresh_at, 0), created_at LIMIT ?`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *Repository) SetNextRefresh(ctx context.Context, id string, next int64, code, message string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE dreamina_accounts SET next_refresh_at = ?, last_error_code = NULLIF(?, ''),
        last_error_message = NULLIF(?, ''), updated_at = ? WHERE id = ?`, next, code, message, time.Now().UTC().UnixMilli(), id)
	return err
}

func (r *Repository) SetStatus(ctx context.Context, id, status string) error {
	if status != "enabled" && status != "disabled" {
		return errors.New("invalid status")
	}
	result, err := r.db.ExecContext(ctx, "UPDATE dreamina_accounts SET status = ?, version = version + 1, updated_at = ? WHERE id = ?", status, time.Now().UTC().UnixMilli(), id)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func IsUniqueError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
