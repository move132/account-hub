package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"
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

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

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
	if session.ExpiresAt <= time.Now().UTC().UnixMilli() {
		_, _ = r.db.ExecContext(ctx, "DELETE FROM admin_sessions WHERE id_hash = ?", idHash)
		return Session{}, sql.ErrNoRows
	}
	_, _ = r.db.ExecContext(ctx, "UPDATE admin_sessions SET last_seen_at = ? WHERE id_hash = ?", time.Now().UTC().UnixMilli(), idHash)
	return session, nil
}

func (r *Repository) DeleteSession(ctx context.Context, idHash string) error {
	result, err := r.db.ExecContext(ctx, "DELETE FROM admin_sessions WHERE id_hash = ?", idHash)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return errors.New("session not found")
	}
	return nil
}

func (r *Repository) DeleteExpiredSessions(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM admin_sessions WHERE expires_at <= ?", time.Now().UTC().UnixMilli())
	return err
}
