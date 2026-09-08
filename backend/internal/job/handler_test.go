package job

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"account-hub/internal/audit"
	platformsqlite "account-hub/internal/platform/sqlite"
)

func TestHandlerCreateUsesTargetResolverLabels(t *testing.T) {
	ctx := context.Background()
	db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := NewManager(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	manager.Register("dreamina", "check", func(context.Context, string, json.RawMessage) error { return nil }, nil)
	handler := NewHandler(manager, audit.NewRepository(db)).WithTargetResolver("dreamina", func(_ context.Context, ids []string) ([]Target, error) {
		targets := make([]Target, 0, len(ids))
		for _, id := range ids {
			targets = append(targets, Target{ID: id, Label: "user@example.com"})
		}
		return targets, nil
	})
	body := bytes.NewBufferString(`{"action":"check","ids":["dream_123"]}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/dreamina-batch-jobs", body)
	response := httptest.NewRecorder()
	handler.Create("dreamina").ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var label string
	if err := db.QueryRowContext(ctx, "SELECT target_label FROM job_items").Scan(&label); err != nil {
		t.Fatal(err)
	}
	if label != "user@example.com" {
		t.Fatalf("target_label=%q", label)
	}
}

func TestHandlerDeleteRejectsActiveAndAuditsFinishedJob(t *testing.T) {
	ctx := context.Background()
	db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedAdmin(t, db)
	manager := NewManager(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	manager.Register("dreamina", "check", func(context.Context, string, json.RawMessage) error { return nil }, nil)
	created, _, err := manager.Create(ctx, "dreamina", "check", "admin_test", "/jobs", "", "", []Target{{ID: "dream_123", Label: "user@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(manager, audit.NewRepository(db))
	activeRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/jobs/"+created.ID, nil)
	activeRequest.SetPathValue("id", created.ID)
	activeResponse := httptest.NewRecorder()
	handler.Delete(activeResponse, activeRequest)
	if activeResponse.Code != http.StatusConflict {
		t.Fatalf("active status=%d body=%s", activeResponse.Code, activeResponse.Body.String())
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := db.ExecContext(ctx, "UPDATE job_items SET state = 'succeeded', completed_at = ? WHERE job_id = ?", now, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE jobs SET state = 'succeeded', pending = 0, succeeded = total, completed_at = ? WHERE id = ?", now, created.ID); err != nil {
		t.Fatal(err)
	}
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/jobs/"+created.ID, nil)
	deleteRequest.SetPathValue("id", created.ID)
	deleteResponse := httptest.NewRecorder()
	handler.Delete(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	var audits int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(1) FROM audit_logs WHERE action = 'job_delete' AND target_id = ?", created.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("job_delete audits=%d", audits)
	}
}

func TestHandlerDeleteBatchReturnsCountAndWritesAudit(t *testing.T) {
	ctx := context.Background()
	db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedAdmin(t, db)
	manager := NewManager(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	manager.Register("token", "check", func(context.Context, string, json.RawMessage) error { return nil }, nil)
	first, _, err := manager.Create(ctx, "token", "check", "admin_test", "/jobs", "", "", []Target{{ID: "one", Label: "one"}})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := manager.Create(ctx, "token", "check", "admin_test", "/jobs", "", "", []Target{{ID: "two", Label: "two"}})
	if err != nil {
		t.Fatal(err)
	}
	finishJob(t, db, first.ID)
	finishJob(t, db, second.ID)
	body, err := json.Marshal(DeleteRequest{IDs: []string{first.ID, second.ID}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/jobs", bytes.NewReader(body))
	response := httptest.NewRecorder()
	NewHandler(manager, audit.NewRepository(db)).DeleteBatch(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Data struct {
			Deleted int `json:"deleted"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Data.Deleted != 2 {
		t.Fatalf("deleted=%d", result.Data.Deleted)
	}
	var jobs, audits int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(1) FROM jobs").Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(1) FROM audit_logs WHERE action = 'job_batch_delete'").Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 || audits != 1 {
		t.Fatalf("jobs=%d audits=%d", jobs, audits)
	}
}
