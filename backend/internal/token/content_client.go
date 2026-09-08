package token

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"account-hub/internal/platform/upstream"
)

const maxContentJSON = 16 << 20
const maxContentImage = 10 << 20

var fileIDPattern = regexp.MustCompile(`^file_[a-zA-Z0-9]+$`)
var libraryIDPattern = regexp.MustCompile(`^libfile_[a-zA-Z0-9_-]+$`)
var conversationIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,200}$`)

func invalidContent(message string) error {
	return &upstream.Error{Code: upstream.InvalidResponse, Message: message}
}

func (c *Client) contentRequest(ctx context.Context, accessToken, method, path string, query url.Values, referer string) (*http.Response, error) {
	factory := func(_ *http.Client) (*http.Request, error) {
		target := c.chatGPTBase + path
		if len(query) > 0 {
			target += "?" + query.Encode()
		}
		request, err := http.NewRequest(method, target, nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", bearer(accessToken))
		request.Header.Set("Accept", "*/*")
		if path == "/backend-api/estuary/content" {
			request.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,image/gif")
		}
		request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
		request.Header.Set("Cache-Control", "no-cache")
		request.Header.Set("Pragma", "no-cache")
		request.Header.Set("Origin", c.chatGPTBase)
		request.Header.Set("Referer", c.chatGPTBase+referer)
		request.Header.Set("Oai-Language", "zh-CN")
		request.Header.Set("X-Openai-Target-Path", path)
		route := path
		if strings.HasPrefix(path, "/backend-api/conversation/") {
			route = "/backend-api/conversation/{conversation_id}"
		}
		request.Header.Set("X-Openai-Target-Route", route)
		return request, nil
	}
	var response *http.Response
	var err error
	if method == http.MethodGet {
		response, err = c.http.DoWithoutRedirects(ctx, factory)
	} else {
		response, err = c.http.DoOnceWithoutRedirects(ctx, factory)
	}
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, &upstream.Error{Code: upstream.InvalidResponse, HTTPStatus: response.StatusCode, Message: "上游返回了非成功响应"}
	}
	return response, nil
}

func readContentJSON(response *http.Response, target any) error {
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxContentJSON+1))
	if err != nil {
		return invalidContent("读取上游内容失败")
	}
	if len(body) > maxContentJSON {
		return invalidContent("上游内容超过大小限制")
	}
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '"' {
		var decoded string
		if json.Unmarshal(body, &decoded) != nil {
			return invalidContent("上游返回的 JSON 格式错误")
		}
		body = bytes.TrimSpace([]byte(decoded))
	}
	if len(body) == 0 || (body[0] != '{' && body[0] != '[') || json.Unmarshal(body, target) != nil {
		return invalidContent("上游返回的内容格式错误")
	}
	return nil
}

func (c *Client) StorageUsage(ctx context.Context, accessToken string) (StorageUsage, error) {
	response, err := c.contentRequest(ctx, accessToken, http.MethodGet, "/backend-api/files/library/storage/usage", nil, "/library")
	if err != nil {
		return StorageUsage{}, err
	}
	var payload struct {
		StorageUsage
		Used    *int64 `json:"used_bytes"`
		Allowed *int64 `json:"allowed_bytes"`
	}
	if err := readContentJSON(response, &payload); err != nil {
		return StorageUsage{}, err
	}
	if payload.Used == nil || payload.Allowed == nil || *payload.Used < 0 || *payload.Allowed < 0 {
		return StorageUsage{}, invalidContent("存储用量响应缺少有效的容量信息")
	}
	result := payload.StorageUsage
	result.UsedBytes, result.AllowedBytes = *payload.Used, *payload.Allowed
	result.RemainingBytes = max(0, result.AllowedBytes-result.UsedBytes)
	result.IsOverLimit = result.IsOverLimit || result.UsedBytes > result.AllowedBytes
	if result.BreakdownByFileType == nil {
		result.BreakdownByFileType = []StorageBreakdown{}
	}
	return result, nil
}

func (c *Client) LibraryFiles(ctx context.Context, accessToken, cursor string) (LibraryPage, error) {
	query := url.Values{"categories": {"image"}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	response, err := c.contentRequest(ctx, accessToken, http.MethodGet, "/backend-api/files/library/nodes", query, "/library?tab=images")
	if err != nil {
		return LibraryPage{}, err
	}
	var result LibraryPage
	if err := readContentJSON(response, &result); err != nil {
		return LibraryPage{}, err
	}
	if result.Items == nil {
		return LibraryPage{}, invalidContent("图片列表响应缺少 items 列表")
	}
	if result.Cursor == cursor {
		result.Cursor = ""
	}
	return result, nil
}

func validateLibraryDeletion(files []DeleteLibraryFile) error {
	if len(files) == 0 || len(files) > 100 {
		return errors.New("请选择 1 至 100 张图片")
	}
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		if !libraryIDPattern.MatchString(file.LibraryFileID) || len(file.LibraryFileID) > 256 || !fileIDPattern.MatchString(file.FileID) || len(file.FileID) > 256 || strings.TrimSpace(file.ParentDirectoryID) == "" || len(file.ParentDirectoryID) > 256 || strings.TrimSpace(file.FileName) == "" || len(file.FileName) > 4096 {
			return errors.New("图片删除参数不完整或格式错误")
		}
		if seen[file.LibraryFileID] {
			return errors.New("图片删除列表包含重复项")
		}
		seen[file.LibraryFileID] = true
	}
	return nil
}

func (c *Client) DeleteLibraryFiles(ctx context.Context, accessToken string, files []DeleteLibraryFile) (LibraryDeleteResult, error) {
	if err := validateLibraryDeletion(files); err != nil {
		return LibraryDeleteResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	errorsByIndex := make([]error, len(files))
	indices := make(chan int, len(files))
	for index := range files {
		indices <- index
	}
	close(indices)
	var workers sync.WaitGroup
	for range min(5, len(files)) {
		workers.Go(func() {
			for index := range indices {
				if ctx.Err() != nil {
					errorsByIndex[index] = ctx.Err()
					continue
				}
				file := files[index]
				query := url.Values{"file_id": {file.FileID}, "parent_directory_id": {file.ParentDirectoryID}, "file_name": {file.FileName}, "soft_delete": {"true"}}
				response, err := c.contentRequest(ctx, accessToken, http.MethodPost, "/backend-api/files/library/files/"+url.PathEscape(file.LibraryFileID)+"/delete_stream", query, "/library?tab=images")
				if err == nil {
					var body []byte
					body, err = io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
					response.Body.Close()
					if len(body) > 1<<20 {
						err = invalidContent("删除响应超过大小限制")
					}
					if err == nil && deletionReportedError(body) {
						err = invalidContent("上游未确认文件删除")
					}
				}
				errorsByIndex[index] = err
			}
		})
	}
	workers.Wait()
	result := LibraryDeleteResult{DeletedLibraryFileIDs: []string{}, Failures: []LibraryDeleteFailure{}}
	for index, err := range errorsByIndex {
		if err == nil {
			result.DeletedLibraryFileIDs = append(result.DeletedLibraryFileIDs, files[index].LibraryFileID)
			continue
		}
		message := "删除结果未确认，请刷新列表后核实"
		var upstreamErr *upstream.Error
		if errors.As(err, &upstreamErr) {
			message = upstreamErr.Message
		}
		result.Failures = append(result.Failures, LibraryDeleteFailure{DeleteLibraryFile: files[index], ErrorCode: upstream.Code(err), Message: message})
	}
	result.DeletedCount, result.FailureCount = len(result.DeletedLibraryFileIDs), len(result.Failures)
	return result, nil
}

func deletionReportedError(body []byte) bool {
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("<")) {
		return true
	}
	reportedError := func(event map[string]any) bool {
		if success, ok := event["success"].(bool); ok && !success {
			return true
		}
		if failure := event["error"]; failure != nil && failure != false && failure != "" {
			return true
		}
		return event["status"] == "error" || event["status"] == "failed"
	}
	var complete map[string]any
	if json.Unmarshal(body, &complete) == nil && reportedError(complete) {
		return true
	}
	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(line), []byte("data:")))
		var event map[string]any
		if json.Unmarshal(line, &event) == nil && reportedError(event) {
			return true
		}
	}
	return false
}

func (c *Client) Conversations(ctx context.Context, accessToken string, offset, limit int) (ConversationPage, error) {
	query := url.Values{"offset": {fmt.Sprint(offset)}, "limit": {fmt.Sprint(limit)}, "order": {"updated"}, "is_archived": {"false"}, "is_starred": {"false"}}
	response, err := c.contentRequest(ctx, accessToken, http.MethodGet, "/backend-api/conversations", query, "/")
	if err != nil {
		return ConversationPage{}, err
	}
	var payload any
	if err := readContentJSON(response, &payload); err != nil {
		return ConversationPage{}, err
	}
	return normalizeConversations(payload, offset, limit)
}

func (c *Client) Conversation(ctx context.Context, accessToken, id string) (ConversationDetail, error) {
	if !conversationIDPattern.MatchString(id) {
		return ConversationDetail{}, errors.New("会话 ID 格式错误")
	}
	response, err := c.contentRequest(ctx, accessToken, http.MethodGet, "/backend-api/conversation/"+url.PathEscape(id), nil, "/")
	if err != nil {
		return ConversationDetail{}, err
	}
	var payload map[string]any
	if err := readContentJSON(response, &payload); err != nil {
		return ConversationDetail{}, err
	}
	return normalizeConversation(payload, id)
}

func (c *Client) ContentImage(ctx context.Context, accessToken, fileID string) (ContentImage, error) {
	if !fileIDPattern.MatchString(fileID) || len(fileID) > 256 {
		return ContentImage{}, errors.New("文件 ID 格式错误")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	response, err := c.contentRequest(ctx, accessToken, http.MethodGet, "/backend-api/files/download/"+fileID, url.Values{"inline": {"false"}, "download_intent": {"false"}, "post_id": {""}}, "/")
	if err != nil {
		return ContentImage{}, err
	}
	var payload struct {
		DownloadURL string `json:"download_url"`
		URL         string `json:"url"`
	}
	if err := readContentJSON(response, &payload); err != nil {
		return ContentImage{}, err
	}
	target := payload.DownloadURL
	if target == "" {
		target = payload.URL
	}
	base, baseErr := url.Parse(c.chatGPTBase)
	parsed, err := url.Parse(target)
	if err != nil || baseErr != nil || parsed.User != nil || parsed.Scheme != base.Scheme || parsed.Host != base.Host || parsed.Path != "/backend-api/estuary/content" || parsed.Fragment != "" {
		return ContentImage{}, invalidContent("上游返回了不受支持的图片地址")
	}
	response, err = c.contentRequest(ctx, accessToken, http.MethodGet, parsed.Path, parsed.Query(), "/")
	if err != nil {
		return ContentImage{}, err
	}
	defer response.Body.Close()
	contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	switch contentType {
	case "image/png", "image/jpeg", "image/webp", "image/gif", "image/avif":
	default:
		return ContentImage{}, invalidContent("上游没有返回可预览的图片")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxContentImage+1))
	if err != nil || len(body) == 0 || len(body) > maxContentImage {
		return ContentImage{}, invalidContent("图片读取失败或超过 10 MB 预览限制")
	}
	return ContentImage{ContentType: contentType, DataURL: "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(body)}, nil
}
