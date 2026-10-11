package token

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"

	"account-hub/internal/idempotency"
	"account-hub/internal/platform/httpserver"
)

// create is the HTTP application command. The repository receives a transaction
// callback and remains independent of response envelopes and idempotency records.
func (h *Handler) create(ctx context.Context, input CreateInput) (View, error) {
	if _, ok := idempotency.RecordFromContext(ctx); !ok {
		return h.service.Create(ctx, input)
	}
	return h.service.createWithCommit(ctx, input, func(ctx context.Context, tx *sql.Tx, view View) error {
		body, err := json.Marshal(httpserver.Response{Code: http.StatusCreated, Success: true, Data: map[string]any{"token": view}, Msg: "创建成功", TS: h.service.now().UTC().UnixMilli()})
		if err != nil {
			return err
		}
		return idempotency.CompleteResultTx(ctx, tx, idempotency.Result{
			Status: http.StatusCreated, Body: string(body) + "\n", Headers: http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
			ResourceType: "token", ResourceID: view.ID,
		})
	})
}
