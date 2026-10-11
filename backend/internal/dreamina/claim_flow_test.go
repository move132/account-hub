package dreamina

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"account-hub/internal/platform/upstream"
	"account-hub/internal/settings"
)

type claimFlowClient struct {
	controlledClient
	claims, checks       int
	raw                  any
	claimErr, balanceErr error
}

func (c *claimFlowClient) ClaimCredit(context.Context, string) (any, error) {
	c.claims++
	return c.raw, c.claimErr
}
func (c *claimFlowClient) Check(ctx context.Context, _ string) (CreditResult, error) {
	c.checks++
	if c.beforeCheck != nil {
		if err := c.beforeCheck(ctx); err != nil {
			return CreditResult{}, err
		}
	}
	return CreditResult{Balance: "42"}, c.balanceErr
}

func TestClaimFlowReportsProviderDecisionAndRefreshesBalance(t *testing.T) {
	cases := []struct {
		name       string
		raw        any
		status     ClaimStatus
		httpStatus int
		code       string
	}{
		{"received_string", map[string]any{"data": map[string]any{"received": "true"}}, ClaimConfirmed, http.StatusOK, ""},
		{"code_envelope", map[string]any{"code": "0", "data": map[string]any{"received": 1}}, ClaimConfirmed, http.StatusOK, ""},
		{"already_received", map[string]any{"ret": 3001, "data": map[string]any{"received": false}}, ClaimAlreadyReceived, http.StatusOK, ""},
		{"provider_rejection", map[string]any{"ret": 9999, "message": "private-provider-detail", "session_id": "private-session"}, ClaimRejected, http.StatusUnprocessableEntity, "CLAIM_REJECTED"},
		{"unknown_response", map[string]any{"message": "private-provider-detail"}, ClaimUnconfirmed, http.StatusBadGateway, "CLAIM_OUTCOME_UNCONFIRMED"},
		{"contradiction", map[string]any{"ret": 0, "data": map[string]any{"received": false}}, ClaimUnconfirmed, http.StatusBadGateway, "CLAIM_OUTCOME_UNCONFIRMED"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client := &claimFlowClient{raw: test.raw}
			_, repository, service := operationFixture(t, client)
			var logs bytes.Buffer
			service.WithLogger(slog.New(slog.NewJSONHandler(&logs, nil)))
			account, err := service.Create(context.Background(), CreateInput{Email: "claim@example.com", Password: "private-password", SessionID: "private-session"})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/credit-claims", nil)
			request.SetPathValue("id", account.ID)
			response := httptest.NewRecorder()
			NewHandler(service, nil, nil).ClaimCredit(response, request)
			var envelope struct {
				Success bool   `json:"success"`
				Msg     string `json:"msg"`
				Data    struct {
					ErrorCode string      `json:"error_code"`
					Operation ClaimResult `json:"operation"`
					Details   struct {
						Operation ClaimResult `json:"operation"`
					} `json:"details"`
				} `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			result := envelope.Data.Operation
			if test.httpStatus != http.StatusOK {
				result = envelope.Data.Details.Operation
			}
			if response.Code != test.httpStatus || envelope.Success != (test.httpStatus == http.StatusOK) || result.Status != test.status || envelope.Data.ErrorCode != test.code {
				t.Fatalf("unexpected claim response: %d %s", response.Code, response.Body.String())
			}
			if client.claims != 1 || client.checks != 1 || !result.BalanceRefreshed || result.Balance != "42" || !result.Persisted {
				t.Fatalf("unsafe/incomplete claim flow: claims=%d checks=%d result=%+v", client.claims, client.checks, result)
			}
			if result.Claimed != (test.status == ClaimConfirmed) {
				t.Fatalf("balance read incorrectly confirmed claim: %+v", result)
			}
			record, err := repository.Get(context.Background(), account.ID)
			if err != nil || record.CreditBalance != "42" || record.CheckState != "valid" {
				t.Fatalf("balance not persisted: %+v %v", record, err)
			}
			attempts, err := repository.Attempts(context.Background(), account.ID, 10)
			wantStatus := "succeeded"
			if test.code != "" {
				wantStatus = "failed"
			}
			if err != nil || len(attempts) != 1 || attempts[0].Status != wantStatus || attempts[0].ErrorCode != test.code {
				t.Fatalf("claim history incorrect: %+v %v", attempts, err)
			}
			if test.status == ClaimRejected && (result.ProviderCode != "9999" || !strings.Contains(envelope.Msg, "9999")) {
				t.Fatalf("provider rejection lost diagnostics: %s", response.Body.String())
			}
			for _, secret := range []string{"private-provider-detail", "private-session", "private-password"} {
				if strings.Contains(logs.String(), secret) || (test.code != "" && strings.Contains(response.Body.String(), secret)) {
					t.Fatalf("claim diagnostics exposed provider data: %s", secret)
				}
			}
		})
	}
}

func TestUnconfirmedClaimBalanceFailureDoesNotRepeatClaim(t *testing.T) {
	client := &claimFlowClient{raw: map[string]any{}, balanceErr: &upstream.Error{Code: upstream.Timeout, Message: "private-balance-error"}}
	_, repository, service := operationFixture(t, client)
	account, err := service.Create(context.Background(), CreateInput{Email: "claim@example.com", Password: "pass", SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ClaimCredit(context.Background(), account.ID)
	if err == nil || result.Status != ClaimUnconfirmed || result.Claimed || result.BalanceRefreshed || result.Balance != "" || result.Warning == nil || result.Warning.Stage != "balance_refresh" || !result.Persisted || client.claims != 1 || client.checks != 1 {
		t.Fatalf("unexpected result: %+v %v calls=%d/%d", result, err, client.claims, client.checks)
	}
	if strings.Contains(result.Warning.Message, "private-balance-error") {
		t.Fatal("raw balance error exposed")
	}
	record, err := repository.Get(context.Background(), account.ID)
	if err != nil || record.CreditBalance != "" {
		t.Fatalf("failed balance query persisted unverified value: %+v %v", record, err)
	}
}

func TestClaimTransportFailureDoesNotMasqueradeAsBusinessRejection(t *testing.T) {
	transportErr := &upstream.Error{Code: upstream.Timeout, Message: "request timed out"}
	client := &claimFlowClient{claimErr: transportErr}
	_, _, service := operationFixture(t, client)
	account, err := service.Create(context.Background(), CreateInput{Email: "claim@example.com", Password: "pass", SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ClaimCredit(context.Background(), account.ID)
	if !errors.Is(err, transportErr) || result.RemoteCompleted || result.Status != ClaimUnconfirmed || result.Claimed || client.claims != 1 || client.checks != 0 {
		t.Fatalf("transport error misclassified: %+v %v", result, err)
	}
}

func TestClaimClientPostsOnceAndQueriesBalanceThroughRealAdapter(t *testing.T) {
	var claims, checks atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/commerce/v1/benefits/credit_receive":
			claims.Add(1)
			_, _ = w.Write([]byte(`{"ret":"0","data":{"received":"true"}}`))
		case "/commerce/v1/benefits/user_credit":
			checks.Add(1)
			_, _ = w.Write([]byte(`{"ret":0,"data":{"credit":{"gift_credit":42}}}`))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	db, _, service := operationFixture(t, controlledClient{})
	httpClient := upstream.NewClient(settings.NewRepository(db), time.Second, 2)
	defer httpClient.CloseIdleConnections()
	service.client = NewClientWithBases(httpClient, server.URL, server.URL, server.URL, "Asia/Shanghai")
	account, err := service.Create(context.Background(), CreateInput{Email: "claim@example.com", Password: "pass", SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ClaimCredit(context.Background(), account.ID)
	if err != nil || !result.Claimed || !result.BalanceRefreshed || claims.Load() != 1 || checks.Load() != 1 {
		t.Fatalf("unexpected adapter flow: %+v %v posts=%d reads=%d", result, err, claims.Load(), checks.Load())
	}
}

func TestClaimPrimaryOutcomeSurvivesPersistenceFailure(t *testing.T) {
	for _, status := range []ClaimStatus{ClaimRejected, ClaimUnconfirmed} {
		for _, failure := range []string{"attempt_insert", "credential_conflict"} {
			t.Run(string(status)+"/"+failure, func(t *testing.T) {
				raw := map[string]any{}
				if status == ClaimRejected {
					raw["ret"] = 9999
				}
				client := &claimFlowClient{raw: raw}
				db, repository, service := operationFixture(t, client)
				account, err := service.Create(context.Background(), CreateInput{Email: "claim@example.com", Password: "pass", SessionID: "session"})
				if err != nil {
					t.Fatal(err)
				}
				if failure == "attempt_insert" {
					if _, err := db.Exec(`CREATE TRIGGER fail_claim_history BEFORE INSERT ON dreamina_refresh_attempts BEGIN SELECT RAISE(ABORT,'private write failure'); END`); err != nil {
						t.Fatal(err)
					}
				} else {
					client.beforeCheck = func(ctx context.Context) error {
						record, err := repository.Get(ctx, account.ID)
						if err != nil {
							return err
						}
						replacement := "replacement-session"
						_, err = repository.Update(ctx, account.ID, UpdateInput{SessionID: &replacement, Version: record.Version})
						return err
					}
				}
				result, err := service.ClaimCredit(context.Background(), account.ID)
				wantCode := "CLAIM_OUTCOME_UNCONFIRMED"
				if status == ClaimRejected {
					wantCode = "CLAIM_REJECTED"
				}
				if err == nil || upstream.Code(err) != wantCode || result.Status != status || result.Claimed || result.Persisted || result.Warning == nil || result.Warning.Stage != "persistence" || !result.BalanceRefreshed || client.claims != 1 || client.checks != 1 {
					t.Fatalf("persistence failure replaced primary outcome: %+v %v", result, err)
				}
				record, readErr := repository.Get(context.Background(), account.ID)
				if readErr != nil || record.CreditBalance != "" {
					t.Fatalf("uncommitted balance was persisted: %+v %v", record, readErr)
				}
				if failure == "credential_conflict" && record.SessionID != "replacement-session" {
					t.Fatal("claim overwrote new credentials")
				}
				attempts, readErr := repository.Attempts(context.Background(), account.ID, 10)
				if readErr != nil || len(attempts) != 0 {
					t.Fatalf("outcome partially committed: %+v %v", attempts, readErr)
				}
			})
		}
	}
}

func TestClaimWriteIsNotRetriedOnRateLimit(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/commerce/v1/benefits/credit_receive" {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	db, _, service := operationFixture(t, controlledClient{})
	httpClient := upstream.NewClient(settings.NewRepository(db), time.Second, 2)
	defer httpClient.CloseIdleConnections()
	service.client = NewClientWithBases(httpClient, server.URL, server.URL, server.URL, "Asia/Shanghai")
	account, err := service.Create(context.Background(), CreateInput{Email: "claim@example.com", Password: "pass", SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ClaimCredit(context.Background(), account.ID)
	if upstream.Code(err) != upstream.RateLimited || result.RemoteCompleted || result.Claimed || calls.Load() != 1 {
		t.Fatalf("claim was retried or misclassified: %+v %v calls=%d", result, err, calls.Load())
	}
}
