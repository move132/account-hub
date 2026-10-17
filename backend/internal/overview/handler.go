package overview

import (
	"database/sql"
	"net/http"
	"time"

	"account-hub/internal/platform/httpserver"
)

type counts struct {
	Total    int `json:"total"`
	Enabled  int `json:"enabled"`
	Disabled int `json:"disabled"`
}

type Handler struct {
	db  *sql.DB
	now func() time.Time
}

func NewHandler(db *sql.DB) *Handler { return &Handler{db: db, now: time.Now} }

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	now := h.now().UTC().UnixMilli()
	// Both provider summaries share one read snapshot and one clock value.
	tx, err := h.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		httpserver.ReportError(w, r, err, "OVERVIEW_FAILED", "读取账号总览失败")
		return
	}
	defer tx.Rollback()
	var tokens, dreamina counts
	for _, source := range []struct {
		table  string
		result *counts
	}{{"tokens", &tokens}, {"dreamina_accounts", &dreamina}} {
		query := "SELECT COUNT(*), COALESCE(SUM(status = 'enabled'), 0), COALESCE(SUM(status = 'disabled'), 0) FROM " + source.table
		if err := tx.QueryRowContext(r.Context(), query).Scan(&source.result.Total, &source.result.Enabled, &source.result.Disabled); err != nil {
			httpserver.ReportError(w, r, err, "OVERVIEW_FAILED", "读取账号总览失败")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		httpserver.ReportError(w, r, err, "OVERVIEW_FAILED", "读取账号总览失败")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpserver.Write(w, http.StatusOK, map[string]any{"tokens": tokens, "dreamina": dreamina, "as_of": now}, "查询成功")
}
