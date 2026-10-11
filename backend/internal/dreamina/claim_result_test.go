package dreamina

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"account-hub/internal/platform/upstream"
)

func TestClaimSuccessKeepsBalanceFailureSeparate(t *testing.T) {
	_, repository, service := operationFixture(t, controlledClient{beforeCheck: func(context.Context) error {
		return &upstream.Error{Code: upstream.Timeout, Message: "balance unavailable"}
	}})
	created, err := service.Create(context.Background(), CreateInput{Email: "claim@example.com", Password: "pass", SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ClaimCredit(context.Background(), created.ID)
	if err != nil || !result.Claimed || !result.RemoteCompleted || !result.Persisted || result.Warning == nil || result.Warning.Stage != "balance_refresh" {
		t.Fatalf("claim success was lost after balance failure: %+v err=%v", result, err)
	}
	attempts, err := repository.Attempts(context.Background(), created.ID, 10)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "succeeded" || attempts[0].Operation != "credit_claim" {
		t.Fatalf("misleading claim history: %+v err=%v", attempts, err)
	}
}

func TestClaimSuccessKeepsPersistenceFailureSeparate(t *testing.T) {
	db, _, service := operationFixture(t, controlledClient{})
	created, err := service.Create(context.Background(), CreateInput{Email: "claim@example.com", Password: "pass", SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_claim BEFORE INSERT ON dreamina_refresh_attempts BEGIN SELECT RAISE(ABORT,'injected'); END;`); err != nil {
		t.Fatal(err)
	}
	result, err := service.ClaimCredit(context.Background(), created.ID)
	if err != nil || !result.Claimed || result.Persisted || result.Warning == nil || result.Warning.Stage != "persistence" {
		t.Fatalf("unsafe retry signal: %+v %v", result, err)
	}
}

func TestClaimUnknownResponseRefreshesBalanceWithoutAssumingSuccess(t *testing.T) {
	checks := 0
	_, _, service := operationFixture(t, controlledClient{
		beforeCheck: func(context.Context) error { checks++; return nil },
		claim: func(context.Context) (any, error) {
			return map[string]any{"description": "private-provider-detail"}, nil
		},
	})
	created, err := service.Create(context.Background(), CreateInput{Email: "claim@example.com", Password: "pass", SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ClaimCredit(context.Background(), created.ID)
	if err == nil || result.Claimed || result.Status != ClaimUnconfirmed || checks != 1 || !result.BalanceRefreshed || result.Balance != "42" {
		t.Fatalf("unconfirmed claim treated as success: %+v err=%v checks=%d", result, err, checks)
	}
	handler := NewHandler(service, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/credit-claims", strings.NewReader("{}"))
	request.SetPathValue("id", created.ID)
	response := httptest.NewRecorder()
	handler.ClaimCredit(response, request)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "CLAIM_OUTCOME_UNCONFIRMED") || strings.Contains(response.Body.String(), "private-provider-detail") {
		t.Fatalf("unsafe unknown-outcome response: %d %s", response.Code, response.Body.String())
	}
}

func TestClaimHTTPPreservesLegacyDataAndAddsTypedOutcome(t *testing.T) {
	_, _, service := operationFixture(t, controlledClient{claim: func(context.Context) (any, error) {
		return map[string]any{"ret": 3001, "message": "already received", "data": map[string]any{"received": false}}, nil
	}})
	created, err := service.Create(context.Background(), CreateInput{Email: "claim@example.com", Password: "pass", SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/credit-claims", strings.NewReader("{}"))
	request.SetPathValue("id", created.ID)
	response := httptest.NewRecorder()
	NewHandler(service, nil, nil).ClaimCredit(response, request)
	var envelope struct {
		Data struct {
			Upstream  map[string]any `json:"upstream"`
			Operation ClaimResult    `json:"operation"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || envelope.Data.Upstream["ret"] != float64(3001) || envelope.Data.Operation.Status != ClaimAlreadyReceived || envelope.Data.Operation.Claimed {
		t.Fatalf("contract mismatch: %s", response.Body.String())
	}
}

func TestAssetPageHasStablePagination(t *testing.T) {
	raw := map[string]any{"asset_list": []any{map[string]any{"id": "a1", "provider_field": "kept"}}, "has_more": true, "next_offset": float64(12)}
	page, err := normalizeAssetPage(raw, 10)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "a1" || page.NextOffset != 12 || !page.HasMore || page.Legacy == nil {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if _, err := normalizeAssetPage(map[string]any{"error": "bad"}, 0); err == nil {
		t.Fatal("accepted malformed asset response")
	}
	if _, err := normalizeAssetPage(errors.New("not a page"), 0); err == nil {
		t.Fatal("accepted non-object page")
	}
}
