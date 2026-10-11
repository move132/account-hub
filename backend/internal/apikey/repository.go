package apikey

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"account-hub/internal/audit"
	"account-hub/internal/platform/apperror"
)

type Repository struct {
	db    *sql.DB
	audit *audit.Repository
}

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

func (r *Repository) WithAudit(repository *audit.Repository) *Repository {
	r.audit = repository
	return r
}

func (r *Repository) List(ctx context.Context) ([]View, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, key_masked, created_at, last_used_at FROM api_keys
        WHERE deleted_at IS NULL ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]View, 0)
	for rows.Next() {
		var item View
		if err := rows.Scan(&item.ID, &item.Name, &item.KeyMasked, &item.CreatedAt, &item.LastUsedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) Create(ctx context.Context, item View, hash string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	entry, _ := audit.EntryFromContext(ctx)
	if _, err := tx.ExecContext(ctx, `INSERT INTO api_keys(id,name,key_hash,key_masked,created_by,created_at)
        VALUES(?,?,?,?,NULLIF(?,''),?)`, item.ID, item.Name, hash, item.KeyMasked, entry.AdminID, item.CreatedAt); err != nil {
		return err
	}
	if err := r.log(ctx, tx, entry, "api_key_create", item.ID, "新增 API Key："+item.Name); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) Delete(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var name string
	err = tx.QueryRowContext(ctx, `SELECT name FROM api_keys WHERE id=? AND deleted_at IS NULL`, id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return apperror.New(apperror.NotFound, "API Key 不存在或已删除")
	}
	if err != nil {
		return err
	}
	// Keep the identity for historical audit records; authentication only reads active rows.
	if _, err := tx.ExecContext(ctx, `UPDATE api_keys SET deleted_at=? WHERE id=? AND deleted_at IS NULL`, time.Now().UTC().UnixMilli(), id); err != nil {
		return err
	}
	entry, _ := audit.EntryFromContext(ctx)
	if err := r.log(ctx, tx, entry, "api_key_delete", id, "删除 API Key："+name); err != nil {
		return err
	}
	return tx.Commit()
}

// UseActive validates and records use atomically, without touching deleted keys.
func (r *Repository) UseActive(ctx context.Context, hash string) (string, error) {
	var id string
	err := r.db.QueryRowContext(ctx, `UPDATE api_keys
        SET last_used_at=MAX(COALESCE(last_used_at, 0), ?)
        WHERE key_hash=? AND deleted_at IS NULL RETURNING id`, time.Now().UTC().UnixMilli(), hash).Scan(&id)
	return id, err
}

func (r *Repository) log(ctx context.Context, tx *sql.Tx, entry audit.Entry, action, id, summary string) error {
	if r.audit == nil {
		return nil
	}
	entry.Action, entry.TargetKind, entry.TargetID, entry.Summary = action, "api_key", id, summary
	return r.audit.LogTx(ctx, tx, entry)
}
