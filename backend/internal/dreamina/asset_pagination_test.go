package dreamina

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"account-hub/internal/platform/upstream"
	"account-hub/internal/settings"
)

func TestAssetPaginationAcceptsProviderRepresentations(t *testing.T) {
	cases := []struct {
		name     string
		offset   int
		next     any
		more     any
		wantNext int
		wantMore bool
	}{
		{"numeric", 10, float64(12), true, 12, true},
		{"large_number", 0, float64(1_000_000), true, 1_000_000, true},
		{"timestamp_cursor", 0, float64(1_777_777_777_777), float64(1), 1_777_777_777_777, true},
		{"descending_cursor", 200, float64(100), true, 100, true},
		{"number_string", 0, "1000000", "true", 1_000_000, true},
		{"json_number", 0, json.Number("1000000"), json.Number("1"), 1_000_000, true},
		{"null", 10, nil, nil, 11, false},
		{"empty", 10, "", "", 11, false},
		{"false_string", 10, "12", "false", 12, false},
		{"zero_string", 10, "12", "0", 12, false},
		{"one_string", 10, "12", "1", 12, true},
		{"whitespace", 10, " 12 ", " true ", 12, true},
		{"integer", 10, 12, 1, 12, true},
		{"terminal_sentinel", 10, float64(-1), false, 11, false},
		{"fractional_optional_offset", 10, 12.5, false, 11, false},
		{"unknown_optional_metadata", 10, []any{}, map[string]any{}, 11, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			entries := []any{map[string]any{"id": "a1", "provider_field": "preserved"}}
			raw := map[string]any{"asset_list": entries, "next_offset": test.next, "has_more": test.more, "extra": "preserved"}
			original := map[string]any{"asset_list": entries, "next_offset": test.next, "has_more": test.more, "extra": "preserved"}
			page, err := normalizeAssetPage(raw, test.offset)
			if err != nil {
				t.Fatalf("valid assets rejected: %v", err)
			}
			if page.NextOffset != test.wantNext || page.HasMore != test.wantMore || len(page.Items) != 1 {
				t.Fatalf("page=%+v want next=%d more=%t", page, test.wantNext, test.wantMore)
			}
			legacy, ok := page.Legacy.(map[string]any)
			if !ok || legacy["next_offset"] != test.wantNext || legacy["has_more"] != test.wantMore || !reflect.DeepEqual(legacy["asset_list"], entries) || legacy["extra"] != "preserved" {
				t.Fatalf("legacy pagination does not match normalized page: %#v", page.Legacy)
			}
			if !reflect.DeepEqual(raw, original) {
				t.Fatal("normalizing a page mutated the provider payload")
			}
		})
	}
}

func TestAssetPaginationMissingMetadataAndInvalidContent(t *testing.T) {
	page, err := normalizeAssetPage(map[string]any{"asset_list": []any{}}, 20)
	if err != nil || page.NextOffset != 20 || page.HasMore {
		t.Fatalf("empty page: %+v %v", page, err)
	}
	for _, raw := range []any{
		nil,
		map[string]any{"error": "failed"},
		map[string]any{"asset_list": "invalid"},
		map[string]any{"asset_list": []any{"invalid"}},
	} {
		if _, err := normalizeAssetPage(raw, 0); err == nil {
			t.Fatalf("invalid content accepted: %#v", raw)
		}
	}
}

func TestAssetsHTTPNormalizesActualJSONPagination(t *testing.T) {
	cases := []struct {
		name, body string
		wantNext   int
		wantMore   bool
	}{
		{"large_numeric_cursor", `{"data":{"asset_list":[{"id":"a1","image":{"prompt":"preserved"}}],"next_offset":1777777777777,"has_more":1}}`, 1_777_777_777_777, true},
		{"nullable_terminal_cursor", `{"data":{"asset_list":[{"id":"a1","image":{"prompt":"preserved"}}],"next_offset":null,"has_more":"false"}}`, 1, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/mweb/v1/get_asset_list" {
					t.Errorf("unexpected provider path %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.body))
			}))
			defer upstreamServer.Close()
			db, _, service := operationFixture(t, controlledClient{})
			client := upstream.NewClient(settings.NewRepository(db), time.Second, 0)
			defer client.CloseIdleConnections()
			service.client = NewClientWithBases(client, upstreamServer.URL, upstreamServer.URL, upstreamServer.URL, "Asia/Shanghai")
			account, err := service.Create(context.Background(), CreateInput{Email: "assets@example.com", Password: "test-password", SessionID: "test-session"})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/assets?offset=0&count=50", nil)
			request.SetPathValue("id", account.ID)
			response := httptest.NewRecorder()
			NewHandler(service, nil, nil).Assets(response, request)
			var envelope struct {
				Success bool `json:"success"`
				Data    struct {
					Assets map[string]any `json:"assets"`
					Page   AssetPage      `json:"page"`
				} `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || !envelope.Success {
				t.Fatalf("valid provider page failed: %d %s", response.Code, response.Body.String())
			}
			if envelope.Data.Page.NextOffset != test.wantNext || envelope.Data.Page.HasMore != test.wantMore || envelope.Data.Assets["next_offset"] != float64(test.wantNext) || envelope.Data.Assets["has_more"] != test.wantMore {
				t.Fatalf("pagination contracts disagree: %s", response.Body.String())
			}
			entries, ok := envelope.Data.Assets["asset_list"].([]any)
			if !ok || len(entries) != 1 || entries[0].(map[string]any)["image"].(map[string]any)["prompt"] != "preserved" {
				t.Fatalf("legacy asset content lost: %s", response.Body.String())
			}
		})
	}
}
