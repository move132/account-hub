package dreamina

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"account-hub/internal/platform/upstream"
)

type UpstreamClient interface {
	Login(context.Context, string, string) (LoginResult, error)
	Check(context.Context, string) (CreditResult, error)
	ClaimCredit(context.Context, string) (any, error)
	Assets(context.Context, string, int, int) (any, error)
}

type Client struct {
	http         *upstream.Client
	loginBase    string
	commerceBase string
	assetsBase   string
	timezone     string
}

func NewClient(client *upstream.Client, timezone string) *Client {
	return &Client{
		http: client, 
		loginBase: "https://login.us.capcut.com",
		commerceBase: "https://commerce.us.capcut.com", 
		assetsBase: "https://dreamina-api-us-ttp2.us.capcut.com",
		timezone: timezone,
	}
}

func NewClientWithBases(client *upstream.Client, loginBase, commerceBase, assetsBase, timezone string) *Client {
	return &Client{http: client, loginBase: strings.TrimRight(loginBase, "/"), commerceBase: strings.TrimRight(commerceBase, "/"), assetsBase: strings.TrimRight(assetsBase, "/"), timezone: timezone}
}

func (c *Client) Login(ctx context.Context, email, password string) (LoginResult, error) {
	form := url.Values{}
	form.Set("mix_mode", "1")
	form.Set("fixed_mix_mode", "1")
	form.Set("email", encodeValue(email))
	form.Set("password", encodeValue(password))
	body := form.Encode()
	response, err := c.http.Do(ctx, func(_ *http.Client) (*http.Request, error) {
		request, err := http.NewRequest(http.MethodPost, c.loginBase+"/passport/web/email/login/?aid=513641", strings.NewReader(body))
		if err == nil {
			applyHeaders(request, "application/x-www-form-urlencoded")
		}
		return request, err
	})
	if err != nil {
		return LoginResult{}, err
	}
	defer response.Body.Close()
	var payload struct {
		Message string `json:"message"`
		Data    struct {
			Description string `json:"description"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return LoginResult{}, &upstream.Error{Code: upstream.InvalidResponse, Message: "即梦登录响应格式错误", Cause: err}
	}
	if payload.Message != "success" {
		message := payload.Data.Description
		if message == "" {
			message = "即梦账号或密码无效"
		}
		return LoginResult{}, &upstream.Error{Code: upstream.InvalidCredentials, Message: message}
	}
	result := LoginResult{}
	for _, cookie := range response.Cookies() {
		if cookie.Name == "sessionid" {
			result.SessionID = cookie.Value
			if !cookie.Expires.IsZero() {
				result.SessionExpiresAt = cookie.Expires.UTC().Format(time.RFC3339)
			} else if cookie.MaxAge > 0 {
				result.SessionExpiresAt = time.Now().Add(time.Duration(cookie.MaxAge) * time.Second).UTC().Format(time.RFC3339)
			}
			break
		}
	}
	if result.SessionID == "" {
		return LoginResult{}, &upstream.Error{Code: upstream.InvalidResponse, Message: "即梦登录响应缺少 sessionid"}
	}
	return result, nil
}

func (c *Client) Check(ctx context.Context, sessionID string) (CreditResult, error) {
	result, err := c.commerce(ctx, "/commerce/v1/benefits/user_credit", sessionID, map[string]any{}, false, true)
	if err != nil {
		return CreditResult{}, err
	}
	return CreditResult{Balance: findBalance(result), Raw: result}, nil
}

func (c *Client) ClaimCredit(ctx context.Context, sessionID string) (any, error) {
	return c.commerce(ctx, "/commerce/v1/benefits/credit_receive", sessionID, map[string]any{"time_zone": c.timezone}, true, false)
}

func (c *Client) Assets(ctx context.Context, sessionID string, offset, count int) (any, error) {
	if offset < 0 {
		offset = 0
	}
	if count < 1 || count > 100 {
		count = 50
	}
	payload := map[string]any{
		"count": count, "direction": 1, "mode": "workbench", "channel": "asset_page",
		"asset_type_list": []int{1, 2, 5, 6, 7, 8, 9, 10},
	}
	if offset > 0 {
		payload["offset"] = offset
	}
	body, _ := json.Marshal(payload)
	response, err := c.http.Do(ctx, func(_ *http.Client) (*http.Request, error) {
		request, err := http.NewRequest(http.MethodPost, c.assetsBase+"/mweb/v1/get_asset_list", bytes.NewReader(body))
		if err == nil {
			applyHeaders(request, "application/json")
			request.Header.Set("Cookie", "sessionid="+sessionID+"; sessionid_ss="+sessionID+";")
			request.Header.Set("app-sdk-version", "48.0.0")
			request.Header.Set("appvr", "8.4.0")
			request.Header.Set("Cache-Control", "no-cache")
			request.Header.Set("device-time", strconv.FormatInt(time.Now().Unix(), 10))
			request.Header.Set("did", "7625984387352331789")
		}
		return request, err
	})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var result map[string]any
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, responseReadError("即梦历史响应读取失败", response, err)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, invalidJSONError("即梦历史响应格式错误", response, raw, err)
	}
	if data, found := result["data"]; found {
		return data, nil
	}
	return result, nil
}

func (c *Client) commerce(ctx context.Context, path, sessionID string, payload any, noRetry, requireSuccess bool) (map[string]any, error) {
	body, _ := json.Marshal(payload)
	factory := func(_ *http.Client) (*http.Request, error) {
		request, err := http.NewRequest(http.MethodPost, c.commerceBase+path, bytes.NewReader(body))
		if err == nil {
			applyHeaders(request, "application/json")
			request.Header.Set("Cookie", "sessionid="+sessionID+";")
		}
		return request, err
	}
	var response *http.Response
	var err error
	if noRetry {
		response, err = c.http.DoOnce(ctx, factory)
	} else {
		response, err = c.http.Do(ctx, factory)
	}
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var result map[string]any
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, responseReadError("即梦积分响应读取失败", response, err)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, invalidJSONError("即梦积分响应格式错误", response, raw, err)
	}
	ret, exists := result["ret"]
	if requireSuccess && !exists {
		return nil, &upstream.Error{Code: upstream.InvalidResponse, Message: "即梦积分响应缺少 ret 字段"}
	}
	if requireSuccess && fmt.Sprint(ret) != "0" {
		return nil, &upstream.Error{Code: upstream.InvalidCredentials, Message: "即梦 Session 已失效"}
	}
	return result, nil
}

func encodeValue(value string) string {
	var builder strings.Builder
	for _, item := range []byte(value) {
		builder.WriteString(strconv.FormatUint(uint64(item^5), 16))
	}
	return builder.String()
}

func applyHeaders(request *http.Request, contentType string) {
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Accept-Language", "zh")
	request.Header.Set("Appid", "513641")
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Origin", "https://dreamina.capcut.com")
	request.Header.Set("Priority", "u=1, i")
	request.Header.Set("Referer", "https://dreamina.capcut.com/")
	request.Header.Set("Sec-Ch-Ua", `"Chromium";v="142", "Google Chrome";v="142", "Not_A Brand";v="99"`)
	request.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	request.Header.Set("Sec-Ch-Ua-Platform", `"macOS"`)
	request.Header.Set("Sec-Fetch-Dest", "empty")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Site", "same-site")
	request.Header.Set("Store-Country-Code", "us")
	request.Header.Set("Store-Country-Code-Src", "uid")
	request.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36")
}

func findBalance(result map[string]any) string {
	for _, key := range []string{"gift_credit", "total_credit", "balance", "available_credit"} {
		if value, found := result[key]; found {
			if balance := scalarString(value); balance != "" {
				return balance
			}
		}
	}
	for _, key := range []string{"data", "credit"} {
		if child, ok := result[key].(map[string]any); ok {
			if balance := findBalance(child); balance != "" {
				return balance
			}
		}
	}
	return ""
}

func scalarString(value any) string {
	switch typed := value.(type) {
	case nil, map[string]any, []any:
		return ""
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func responseReadError(message string, response *http.Response, err error) error {
	code := upstream.InvalidResponse
	if errors.Is(err, context.DeadlineExceeded) {
		code = upstream.Timeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		code = upstream.Timeout
	}
	return &upstream.Error{
		Code:       code,
		HTTPStatus: response.StatusCode,
		Message:    fmt.Sprintf("%s (HTTP %d, Content-Type: %s, Content-Length: %d): %v", message, response.StatusCode, responseContentType(response), response.ContentLength, err),
		Cause:      err,
	}
}

func responseContentType(response *http.Response) string {
	contentType := response.Header.Get("Content-Type")
	if contentType == "" {
		return "unknown"
	}
	return contentType
}

func invalidJSONError(message string, response *http.Response, raw []byte, err error) error {
	snippet := strings.TrimSpace(string(raw))
	if len(snippet) > 200 {
		snippet = snippet[:200]
	}
	if snippet == "" {
		snippet = "empty body"
	}
	return &upstream.Error{
		Code:       upstream.InvalidResponse,
		HTTPStatus: response.StatusCode,
		Message:    fmt.Sprintf("%s (HTTP %d, Content-Type: %s, Content-Length: %d)", message, response.StatusCode, responseContentType(response), response.ContentLength),
		Cause:      fmt.Errorf("%w: %s", err, snippet),
	}
}

var _ UpstreamClient = (*Client)(nil)
