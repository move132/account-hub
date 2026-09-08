package token

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"account-hub/internal/platform/httpserver"
	platformsqlite "account-hub/internal/platform/sqlite"
	"account-hub/internal/platform/upstream"
	"account-hub/internal/settings"
)

func contentClientFixture(t *testing.T, handler http.HandlerFunc) (*Client, *ContentHandler, Record) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	ctx := context.Background()
	db, err := platformsqlite.Open(ctx, filepath.Join(t.TempDir(), "content.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	settingRepository := settings.NewRepository(db)
	if err := settingRepository.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	upstreamClient := upstream.NewBrowserClient(settingRepository, 2*time.Second, 2)
	t.Cleanup(upstreamClient.CloseIdleConnections)
	client := NewClientWithBases(upstreamClient, server.URL, server.URL)
	repository := NewRepository(db)
	record, err := repository.Create(ctx, CreateInput{AccessToken: "private-access", SessionToken: "private-session"})
	if err != nil {
		t.Fatal(err)
	}
	return client, NewContentHandler(repository, client, nil), record
}

func TestContentClientStorageListsAndCredentials(t *testing.T) {
	client, handler, record := contentClientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-access" || r.Header.Get("Origin") == "" || r.Header.Get("X-Openai-Target-Path") != r.URL.Path {
			t.Error("missing account authorization or ChatGPT request headers")
		}
		switch r.URL.Path {
		case "/backend-api/files/library/storage/usage":
			if !strings.HasSuffix(r.Referer(), "/library") {
				t.Error("storage referer missing")
			}
			_ = json.NewEncoder(w).Encode(` {"used_bytes":2048,"allowed_bytes":1024,"breakdown_by_file_type":[{"file_type":"image","count":2,"used_bytes":2048}],"access_token":"private-access","session_token":"private-session"} `)
		case "/backend-api/files/library/nodes":
			if r.URL.Query().Get("categories") != "image" || r.URL.Query().Get("cursor") != "opaque+/= cursor" {
				t.Error("library cursor was changed")
			}
			_, _ = io.WriteString(w, `{"items":[{"id":"libfile_1","file_id":"file_1","name":"图片.png","parent_directory_id":"root","file_size_bytes":2048}],"cursor":null}`)
		case "/backend-api/conversations":
			if r.URL.Query().Get("offset") != "30" || r.URL.Query().Get("limit") != "30" || r.URL.Query().Get("is_archived") != "false" || r.URL.Query().Get("order") != "updated" {
				t.Error("conversation pagination changed")
			}
			_, _ = io.WriteString(w, `{"conversations":[{"id":"conversation-1","title":"问候","create_time":1700000000}],"total_count":"31"}`)
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	usage, err := client.StorageUsage(ctx, record.AccessToken)
	if err != nil || usage.UsedBytes != 2048 || usage.RemainingBytes != 0 || !usage.IsOverLimit || len(usage.BreakdownByFileType) != 1 {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
	files, err := client.LibraryFiles(ctx, record.AccessToken, "opaque+/= cursor")
	if err != nil || len(files.Items) != 1 || files.Cursor != "" {
		t.Fatalf("files=%+v err=%v", files, err)
	}
	conversations, err := client.Conversations(ctx, record.AccessToken, 30, 30)
	if err != nil || conversations.Total == nil || *conversations.Total != 31 || conversations.NextOffset != 31 || conversations.HasMore || conversations.Items[0].CreateTime != "2023-11-14T22:13:20Z" {
		t.Fatalf("conversations=%+v err=%v", conversations, err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tokens/"+record.ID+"/storage", nil)
	request.SetPathValue("id", record.ID)
	response := httptest.NewRecorder()
	httpserver.Middleware(slog.New(slog.NewTextHandler(io.Discard, nil)), http.HandlerFunc(handler.Storage)).ServeHTTP(response, request)
	if response.Code != 200 || strings.Contains(response.Body.String(), "private-") || !strings.Contains(response.Body.String(), `"used_bytes":2048`) {
		t.Fatalf("unsafe or invalid storage response: %s", response.Body.String())
	}
}

func TestConversationCurrentBranchAndFallbackOrdering(t *testing.T) {
	if text, assets := normalizeMessageContent(""); text != "" || len(assets) != 0 {
		t.Fatal("empty text became a visible chat message")
	}
	var payload map[string]any
	err := json.Unmarshal([]byte(`{
  "title":"对话", "current_node":"answer", "mapping": {
    "root":{"parent":null,"message":{"content":{"parts":["hidden"]},"metadata":{"is_visually_hidden_from_conversation":true}}},
    "question":{"parent":"root","message":{"id":"q","author":{"role":"user"},"create_time":1700000000,"content":{"parts":["看看图片",{"content_type":"image_asset_pointer","asset_pointer":"sediment://file_abc"},{"content_type":"image_asset_pointer","asset_pointer":"sediment://file_abc"}]}}},
    "old":{"parent":"question","message":{"id":"old","author":{"role":"assistant"},"content":"wrong branch"}},
    "answer":{"parent":"question","message":{"id":"a","author":{"role":"assistant"},"create_time":1700000000.25,"content":{"parts":[{"text":"<script>literal text</script>"}]}}}
  }}
`), &payload)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := normalizeConversation(payload, "conversation-1")
	if err != nil || len(detail.Messages) != 2 || detail.Messages[0].ID != "q" || detail.Messages[1].ID != "a" || len(detail.Messages[0].Assets) != 1 || detail.Messages[1].Content != "<script>literal text</script>" {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	delete(payload, "current_node")
	delete(contentObject(payload["mapping"]), "old")
	detail, err = normalizeConversation(payload, "conversation-1")
	if err != nil || len(detail.Messages) != 2 || detail.Messages[0].ID != "q" || detail.Messages[1].ID != "a" {
		t.Fatalf("fallback ordering failed: %+v err=%v", detail, err)
	}
	payload["current_node"] = "answer"
	contentObject(contentObject(payload["mapping"])["root"])["parent"] = "answer"
	detail, err = normalizeConversation(payload, "conversation-1")
	if err != nil || len(detail.Messages) != 2 {
		t.Fatalf("cycle handling failed: %+v err=%v", detail, err)
	}
}

func TestContentClientConversationHeaders(t *testing.T) {
	client, _, record := contentClientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/conversation/conversation-1" || r.Header.Get("X-Openai-Target-Route") != "/backend-api/conversation/{conversation_id}" {
			t.Error("wrong conversation route")
		}
		_, _ = io.WriteString(w, `{"title":"空会话","mapping":{}}`)
	})
	detail, err := client.Conversation(context.Background(), record.AccessToken, "conversation-1")
	if err != nil || detail.ID != "conversation-1" || detail.Title != "空会话" || detail.Messages == nil {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	if _, err := client.Conversation(context.Background(), record.AccessToken, "../me"); err == nil {
		t.Fatal("accepted invalid conversation ID")
	}
}

func TestLibraryDeletionPartialFailureAndValidation(t *testing.T) {
	var calls atomic.Int32
	client, _, record := contentClientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Query().Get("soft_delete") != "true" || r.URL.Query().Get("parent_directory_id") != "directory" || r.Header.Get("Authorization") != "Bearer private-access" {
			t.Error("invalid deletion request")
		}
		switch {
		case strings.Contains(r.URL.Path, "libfile_ok/"):
			if r.URL.Query().Get("file_name") != "a & b.png" {
				t.Error("file name was not encoded correctly")
			}
			_, _ = io.WriteString(w, "data: {\"status\":\"success\"}\n\n")
		case strings.Contains(r.URL.Path, "libfile_stream/"):
			_, _ = io.WriteString(w, "data: {\"success\":false,\"error\":\"private-access\"}\n\n")
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})
	files := []DeleteLibraryFile{{"libfile_ok", "file_1", "directory", "a & b.png"}, {"libfile_stream", "file_2", "directory", "stream.png"}, {"libfile_fail", "file_3", "directory", "failed.png"}}
	result, err := client.DeleteLibraryFiles(context.Background(), record.AccessToken, files)
	if err != nil || result.DeletedCount != 1 || result.FailureCount != 2 || calls.Load() != 3 || result.DeletedLibraryFileIDs[0] != "libfile_ok" {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls.Load(), err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "private-access") {
		t.Fatal("deletion response leaked upstream content")
	}
	for _, invalid := range [][]DeleteLibraryFile{nil, {files[0], files[0]}, {{"../other", "file_1", "directory", "bad.png"}}, make([]DeleteLibraryFile, 101)} {
		if _, err := client.DeleteLibraryFiles(context.Background(), record.AccessToken, invalid); err == nil {
			t.Fatal("accepted invalid deletion input")
		}
	}
	if calls.Load() != 3 {
		t.Fatal("invalid input issued upstream mutations")
	}
}

func TestContentImageAndRedirectProtection(t *testing.T) {
	for _, mode := range []string{"image", "foreign_url", "userinfo", "wrong_path", "redirect", "svg"} {
		t.Run(mode, func(t *testing.T) {
			var unexpected atomic.Int32
			foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { unexpected.Add(1); w.WriteHeader(200) }))
			defer foreign.Close()
			client, _, record := contentClientFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer private-access" {
					t.Error("missing image authorization")
				}
				if strings.HasPrefix(r.URL.Path, "/backend-api/files/download/") {
					target := "http://" + r.Host + "/backend-api/estuary/content?sig=private-signature"
					switch mode {
					case "foreign_url":
						target = foreign.URL + "/backend-api/estuary/content"
					case "userinfo":
						target = strings.Replace(target, "http://", "http://user:secret@", 1)
					case "wrong_path":
						target = "http://" + r.Host + "/backend-api/estuary/content-other"
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"download_url": target})
					return
				}
				if mode == "redirect" {
					http.Redirect(w, r, foreign.URL, http.StatusFound)
					return
				}
				if mode == "svg" {
					w.Header().Set("Content-Type", "image/svg+xml")
					_, _ = io.WriteString(w, "<svg/>")
					return
				}
				if r.URL.Query().Get("sig") != "private-signature" || !strings.Contains(r.Header.Get("Accept"), "image/") {
					t.Error("image query or headers lost")
				}
				w.Header().Set("Content-Type", "image/png")
				_, _ = w.Write([]byte{137, 80, 78, 71})
			})
			image, err := client.ContentImage(context.Background(), record.AccessToken, "file_abc")
			if mode == "image" {
				if err != nil || image.ContentType != "image/png" || image.DataURL != "data:image/png;base64,iVBORw==" {
					t.Fatalf("image=%+v err=%v", image, err)
				}
			} else if err == nil {
				t.Fatalf("accepted unsafe image mode %s", mode)
			}
			if unexpected.Load() != 0 {
				t.Fatal("followed an untrusted image URL or redirect")
			}
		})
	}
}

func TestContentHandlerValidationAndErrors(t *testing.T) {
	var calls atomic.Int32
	_, handler, record := contentClientFixture(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusForbidden) })
	for _, query := range []string{"offset=-1", "offset=nope", "limit=101"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/tokens/"+record.ID+"/conversations?"+query, nil)
		request.SetPathValue("id", record.ID)
		response := httptest.NewRecorder()
		handler.Conversations(response, request)
		if response.Code != 400 {
			t.Fatalf("query=%s status=%d", query, response.Code)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid query called upstream")
	}
	request := httptest.NewRequest(http.MethodGet, "/storage", nil)
	request.SetPathValue("id", record.ID)
	response := httptest.NewRecorder()
	handler.Storage(response, request)
	if response.Code != 502 || !strings.Contains(response.Body.String(), "upstream_forbidden") {
		t.Fatalf("error mapping: %s", response.Body.String())
	}
	request.SetPathValue("id", "missing")
	response = httptest.NewRecorder()
	handler.Storage(response, request)
	if response.Code != 404 {
		t.Fatalf("missing record status=%d", response.Code)
	}
}

func TestContentRejectsMalformedResponses(t *testing.T) {
	for _, body := range []string{`null`, `"oops"`, `{"items":{}}`, `<html>challenge</html>`} {
		t.Run(fmt.Sprintf("body=%s", body), func(t *testing.T) {
			client, _, record := contentClientFixture(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) })
			if _, err := client.Conversations(context.Background(), record.AccessToken, 0, 30); upstream.Code(err) != upstream.InvalidResponse {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestDeletionResponseMustNotAcceptErrorPages(t *testing.T) {
	for _, body := range []string{`<html>Login required</html>`, "{\n  \"success\": false\n}", "data: {\"error\":{\"message\":\"failed\"}}\n\n"} {
		if !deletionReportedError([]byte(body)) {
			t.Fatalf("accepted deletion response: %s", body)
		}
	}
	if deletionReportedError([]byte(`{"success":true,"error":false}`)) {
		t.Fatal("rejected a successful deletion response")
	}
}
