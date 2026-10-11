package token

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"account-hub/internal/platform/apperror"
)

type identifierGatewayStub struct {
	ContentGateway
	calls  int
	lastID string
	err    error
}

func (g *identifierGatewayStub) Conversation(_ context.Context, _ string, id string) (ConversationDetail, error) {
	g.calls++
	g.lastID = id
	return ConversationDetail{}, g.err
}
func (g *identifierGatewayStub) ContentImage(_ context.Context, _ string, id string) (ContentImage, error) {
	g.calls++
	g.lastID = id
	return ContentImage{}, g.err
}

func TestContentIdentifiersValidatedBeforeGateway(t *testing.T) {
	cases := []struct {
		name  string
		image bool
		id    string
		valid bool
	}{
		{"conversation_punctuation", false, "bad!", false},
		{"conversation_empty", false, "", false},
		{"conversation_traversal", false, "../other", false},
		{"conversation_too_long", false, strings.Repeat("a", 201), false},
		{"conversation_valid", false, "conversation_123-ABC", true},
		{"conversation_max_length", false, strings.Repeat("a", 200), true},
		{"image_missing_prefix", true, "bad", false},
		{"image_empty", true, "", false},
		{"image_empty_suffix", true, "file_", false},
		{"image_punctuation", true, "file_bad!", false},
		{"image_too_long", true, "file_" + strings.Repeat("a", 252), false},
		{"image_valid", true, "file_abc123ABC", true},
		{"image_max_length", true, "file_" + strings.Repeat("a", 251), true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			gateway := &identifierGatewayStub{}
			service := NewContentService(credentialReaderStub{record: Record{AccessToken: "test-access"}}, gateway)
			var err error
			if test.image {
				_, err = service.Image(context.Background(), "account", test.id)
			} else {
				_, err = service.Conversation(context.Background(), "account", test.id)
			}
			if test.valid {
				if err != nil || gateway.calls != 1 || gateway.lastID != test.id {
					t.Fatalf("valid identifier was changed/rejected: calls=%d id=%q err=%v", gateway.calls, gateway.lastID, err)
				}
			} else {
				if err == nil || apperror.KindOf(err) != apperror.Validation || gateway.calls != 0 {
					t.Fatalf("invalid identifier reached gateway or was misclassified: calls=%d err=%v", gateway.calls, err)
				}
			}
		})
	}
}

func TestContentHandlerMalformedIdentifiersReturnBadRequest(t *testing.T) {
	var calls atomic.Int32
	_, handler, record := contentClientFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	for _, test := range []struct {
		name, pathKey, id, message string
		serve                      http.HandlerFunc
	}{
		{"conversation", "conversation_id", "bad!", "会话 ID 格式错误", handler.Conversation},
		{"long_conversation", "conversation_id", strings.Repeat("a", 201), "会话 ID 格式错误", handler.Conversation},
		{"image", "file_id", "bad", "文件 ID 格式错误", handler.Image},
		{"long_image", "file_id", "file_" + strings.Repeat("a", 252), "文件 ID 格式错误", handler.Image},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/content", nil)
			request.SetPathValue("id", record.ID)
			request.SetPathValue(test.pathKey, test.id)
			response := httptest.NewRecorder()
			test.serve(response, request)
			var envelope struct {
				Code    int    `json:"code"`
				Success bool   `json:"success"`
				Msg     string `json:"msg"`
				Data    struct {
					ErrorCode string `json:"error_code"`
				} `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusBadRequest || envelope.Code != http.StatusBadRequest || envelope.Success || envelope.Data.ErrorCode != "INVALID_CONTENT" || envelope.Msg != test.message {
				t.Fatalf("malformed identifier has wrong HTTP error: %d %s", response.Code, response.Body.String())
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid identifiers caused %d upstream requests", calls.Load())
	}
}

func TestContentIdentifierValidationDoesNotMaskInternalFailures(t *testing.T) {
	gateway := &identifierGatewayStub{err: errors.New("private internal cause")}
	service := NewContentService(credentialReaderStub{record: Record{AccessToken: "test-access"}}, gateway)
	handler := NewContentHandlerWithService(service, nil)
	for _, test := range []struct {
		pathKey, id string
		serve       http.HandlerFunc
	}{
		{"conversation_id", "valid-conversation", handler.Conversation},
		{"file_id", "file_abc", handler.Image},
	} {
		request := httptest.NewRequest(http.MethodGet, "/content", nil)
		request.SetPathValue("id", "account")
		request.SetPathValue(test.pathKey, test.id)
		response := httptest.NewRecorder()
		test.serve(response, request)
		if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"error_code":"INTERNAL_ERROR"`) || strings.Contains(response.Body.String(), "private internal cause") {
			t.Fatalf("internal failure misclassified or exposed: %d %s", response.Code, response.Body.String())
		}
	}
	if gateway.calls != 2 {
		t.Fatalf("valid requests did not reach gateway: %d", gateway.calls)
	}
}
