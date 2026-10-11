package auth

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"account-hub/internal/audit"
)

type Admin struct {
	ID           string
	Username     string
	PasswordHash string
	CreatedAt    int64
	UpdatedAt    int64
}

type Session struct {
	IDHash     string
	AdminID    string
	Username   string
	CSRFHash   string
	ExpiresAt  int64
	CreatedAt  int64
	LastSeenAt int64
}

type Repository struct {
	db     *sql.DB
	logger *slog.Logger
	audit  *audit.Repository
}

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db, logger: slog.Default()} }

func (r *Repository) WithLogger(logger *slog.Logger) *Repository {
	if logger != nil {
		r.logger = logger
	}
	return r
}

func (r *Repository) WithAudit(repository *audit.Repository) *Repository {
	r.audit = repository
	return r
}

func (r *Repository) AdminByUsername(ctx context.Context, username string) (Admin, error) {
	var admin Admin
	err := r.db.QueryRowContext(ctx, `SELECT id, username, password_hash, created_at, updated_at
        FROM admins WHERE username = ?`, username).Scan(&admin.ID, &admin.Username, &admin.PasswordHash, &admin.CreatedAt, &admin.UpdatedAt)
	return admin, err
}

func (r *Repository) AdminCount(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(1) FROM admins").Scan(&count)
	return count, err
}

func (r *Repository) CreateAdmin(ctx context.Context, admin Admin) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO admins(id, username, password_hash, created_at, updated_at)
        VALUES(?, ?, ?, ?, ?)`, admin.ID, admin.Username, admin.PasswordHash, admin.CreatedAt, admin.UpdatedAt)
	return err
}

func (r *Repository) UpdatePassword(ctx context.Context, adminID, passwordHash, keepSessionHash string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE admins SET password_hash = ?, updated_at = ? WHERE id = ?", passwordHash, time.Now().UTC().UnixMilli(), adminID); err != nil {
		return err
	}
	query := "DELETE FROM admin_sessions WHERE admin_id = ?"
	args := []any{adminID}
	if keepSessionHash != "" {
		query += " AND id_hash <> ?"
		args = append(args, keepSessionHash)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	if r.audit != nil {
		entry, ok := audit.EntryFromContext(ctx)
		if !ok {
			entry = audit.Entry{AdminID: adminID, Action: "password_change", TargetKind: "auth", Summary: "修改管理员密码"}
		}
		if err := r.audit.LogTx(ctx, tx, entry); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Repository) CreateSession(ctx context.Context, session Session) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO admin_sessions
        (id_hash, admin_id, csrf_hash, expires_at, created_at, last_seen_at) VALUES(?, ?, ?, ?, ?, ?)`,
		session.IDHash, session.AdminID, session.CSRFHash, session.ExpiresAt, session.CreatedAt, session.LastSeenAt)
	return err
}

func (r *Repository) Session(ctx context.Context, idHash string) (Session, error) {
	var session Session
	err := r.db.QueryRowContext(ctx, `SELECT s.id_hash, s.admin_id, a.username, s.csrf_hash,
        s.expires_at, s.created_at, s.last_seen_at
        FROM admin_sessions s JOIN admins a ON a.id = s.admin_id WHERE s.id_hash = ?`, idHash).
		Scan(&session.IDHash, &session.AdminID, &session.Username, &session.CSRFHash, &session.ExpiresAt, &session.CreatedAt, &session.LastSeenAt)
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC().UnixMilli()
	if session.ExpiresAt <= now {
		return Session{}, sql.ErrNoRows
	}
	// Last-seen is diagnostic metadata, not sliding expiration. Avoid writing on every authenticated read.
	const touchInterval = int64(time.Minute / time.Millisecond)
	if now-session.LastSeenAt >= touchInterval {
		if _, err := r.db.ExecContext(ctx, "UPDATE admin_sessions SET last_seen_at = ? WHERE id_hash = ? AND last_seen_at <= ?", now, idHash, now-touchInterval); err != nil {
			r.logger.Warn("session last-seen update failed", "error", err)
		}
	}

	return session, nil
}

func (r *Repository) DeleteSession(ctx context.Context, idHash string) error {
	// Deleting an already revoked session is idempotent; persistence failures are not.
	_, err := r.db.ExecContext(ctx, "DELETE FROM admin_sessions WHERE id_hash = ?", idHash)
	return err
}

func (r *Repository) DeleteExpiredSessions(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM admin_sessions WHERE expires_at <= ?", time.Now().UTC().UnixMilli())
	return err
}
