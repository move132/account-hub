package audit

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"account-hub/internal/platform/principal"
	"account-hub/internal/platform/security"
	platformsqlite "account-hub/internal/platform/sqlite"
)

type Entry struct {
	ID         string `json:"id"`
	AdminID    string `json:"admin_id,omitempty"`
	APIKeyID   string `json:"api_key_id,omitempty"`
	Action     string `json:"action"`
	TargetKind string `json:"target_kind"`
	TargetID   string `json:"target_id,omitempty"`
	Summary    string `json:"summary"`
	RequestID  string `json:"request_id"`
	RemoteAddr string `json:"remote_addr"`
	CreatedAt  int64  `json:"created_at"`
}

type Repository struct {
	db     *sql.DB
	logger *slog.Logger
}

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db, logger: slog.Default()} }

func (r *Repository) WithLogger(logger *slog.Logger) *Repository {
	if logger != nil {
		r.logger = logger
	}
	return r
}

func (r *Repository) Log(ctx context.Context, entry Entry) error { return r.LogTx(ctx, r.db, entry) }

// LogTx can join the same transaction as the audited local change.
func (r *Repository) LogTx(ctx context.Context, db platformsqlite.DBTX, entry Entry) (err error) {
	if caller, ok := principal.FromContext(ctx); ok {
		switch caller.Kind {
		case principal.APIKey:
			entry.AdminID, entry.APIKeyID = "", caller.ID
		case principal.Admin:
			if entry.AdminID == "" {
				entry.AdminID = caller.ID
			}
		}
	}
	defer func() {
		if err != nil {
			r.logger.Error("audit record failed", "action", entry.Action, "target_kind", entry.TargetKind, "target_id", entry.TargetID, "request_id", entry.RequestID, "error", err)
		}
	}()
	if entry.ID == "" {
		id, err := security.RandomID("audit")
		if err != nil {
			return err
		}
		entry.ID = id
	}
	if entry.CreatedAt == 0 {
		entry.CreatedAt = time.Now().UTC().UnixMilli()
	}
	var adminID any
	if entry.AdminID != "" {
		adminID = entry.AdminID
	}
	var targetID any
	if entry.TargetID != "" {
		targetID = entry.TargetID
	}
	_, err = db.ExecContext(ctx, `INSERT INTO audit_logs
        (id, admin_id, action, target_kind, target_id, summary, request_id, remote_addr, created_at, api_key_id)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?,''))`,
		entry.ID, adminID, entry.Action, entry.TargetKind, targetID, entry.Summary, entry.RequestID, entry.RemoteAddr, entry.CreatedAt, entry.APIKeyID)
	return err
}

func (r *Repository) List(ctx context.Context, page, pageSize int) ([]Entry, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 500 {
		pageSize = 20
	}
	var total int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(1) FROM audit_logs").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, COALESCE(admin_id, ''), action, target_kind,
        COALESCE(target_id, ''), summary, request_id, remote_addr, created_at, COALESCE(api_key_id, '')
        FROM audit_logs ORDER BY created_at DESC LIMIT ? OFFSET ?`, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	entries := make([]Entry, 0, pageSize)
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.ID, &entry.AdminID, &entry.Action, &entry.TargetKind, &entry.TargetID, &entry.Summary, &entry.RequestID, &entry.RemoteAddr, &entry.CreatedAt, &entry.APIKeyID); err != nil {
			return nil, 0, fmt.Errorf("scan audit entry: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, total, rows.Err()
}
