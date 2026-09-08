package upstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"account-hub/internal/platform/httpserver"
	"account-hub/internal/settings"

	xproxy "golang.org/x/net/proxy"
)

const (
	InvalidCredentials = "invalid_credentials"
	Forbidden          = "upstream_forbidden"
	ChallengeRequired  = "upstream_challenge"
	RateLimited        = "rate_limited"
	Timeout            = "upstream_timeout"
	Unavailable        = "upstream_unavailable"
	NetworkError       = "network_error"
	InvalidResponse    = "invalid_response"
	Unknown            = "unknown"
)

type Error struct {
	Code       string
	HTTPStatus int
	Message    string
	Cause      error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

func (e *Error) Unwrap() error     { return e.Cause }
func (e *Error) ErrorCode() string { return e.Code }

type Client struct {
	settings *settings.Repository
	timeout  time.Duration
	retries  int
	logger   *slog.Logger
	mu       sync.Mutex
	cacheKey string
	client   *http.Client
	browser  bool
}

func NewClient(repository *settings.Repository, timeout time.Duration, retries int) *Client {
	return &Client{settings: repository, timeout: timeout, retries: retries, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// NewBrowserClient uses a Chrome TLS/HTTP fingerprint for outgoing requests.
// Credentials remain explicit on each request; no shared cookie jar is used.
func NewBrowserClient(repository *settings.Repository, timeout time.Duration, retries int) *Client {
	client := NewClient(repository, timeout, retries)
	client.browser = true
	return client
}

func (c *Client) WithLogger(logger *slog.Logger) *Client {
	if logger != nil {
		c.logger = logger
	}
	return c
}

type RequestFactory func(client *http.Client) (*http.Request, error)

func (c *Client) Do(ctx context.Context, factory RequestFactory) (*http.Response, error) {
	return c.do(ctx, factory, c.retries, true)
}

func (c *Client) DoOnce(ctx context.Context, factory RequestFactory) (*http.Response, error) {
	return c.do(ctx, factory, 0, true)
}

func (c *Client) DoWithoutRedirects(ctx context.Context, factory RequestFactory) (*http.Response, error) {
	return c.do(ctx, factory, c.retries, false)
}

func (c *Client) DoOnceWithoutRedirects(ctx context.Context, factory RequestFactory) (*http.Response, error) {
	return c.do(ctx, factory, 0, false)
}

func (c *Client) do(ctx context.Context, factory RequestFactory, retries int, followRedirects bool) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	httpClient, err := c.httpClient(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	if !followRedirects {
		copy := *httpClient
		copy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
		httpClient = &copy
	}
	var last error
	for attempt := 0; attempt <= retries; attempt++ {
		request, err := factory(httpClient)
		if err != nil {
			cancel()
			c.logger.Warn("build upstream request failed", "attempt", attempt+1, "max_attempts", retries+1, "error", err)
			return nil, err
		}
		request = request.WithContext(ctx)
		attemptStarted := time.Now()
		response, err := httpClient.Do(request)
		c.logUpstreamResult(request, response, err, attempt+1, retries+1, time.Since(attemptStarted))
		if err == nil {
			if rejection := classifyAccessError(response); rejection != nil {
				response.Body.Close()
				cancel()
				return nil, rejection
			}
		}
		if err == nil && response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests {
			if response.StatusCode >= 400 {
				response.Body.Close()
				cancel()
				return nil, &Error{Code: InvalidResponse, HTTPStatus: response.StatusCode, Message: fmt.Sprintf("上游返回 HTTP %d", response.StatusCode)}
			}
			response.Body = &cancelOnCloseReadCloser{ReadCloser: response.Body, cancel: cancel}
			return response, nil
		}
		if response != nil {
			io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			response.Body.Close()
			if response.StatusCode == http.StatusTooManyRequests {
				last = &Error{Code: RateLimited, HTTPStatus: response.StatusCode, Message: "上游请求受限流"}
			} else {
				last = &Error{Code: Unavailable, HTTPStatus: response.StatusCode, Message: "上游服务暂时不可用"}
			}
		} else {
			last = classifyNetworkError(err)
		}
		if attempt < retries {
			delay := time.Duration(1<<attempt)*250*time.Millisecond + time.Duration(rand.IntN(200))*time.Millisecond
			c.logger.Info("retry upstream request", "method", request.Method, "url", safeURL(request.URL), "attempt", attempt+1, "next_attempt", attempt+2, "max_attempts", retries+1, "delay_ms", delay.Milliseconds(), "error_code", Code(last), "error", last)
			select {
			case <-ctx.Done():
				cancel()
				return nil, classifyNetworkError(ctx.Err())
			case <-time.After(delay):
			}
		}
	}
	cancel()
	return nil, last
}

type cancelOnCloseReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r *cancelOnCloseReadCloser) Close() error {
	err := r.ReadCloser.Close()
	r.cancel()
	return err
}

func (c *Client) logUpstreamResult(request *http.Request, response *http.Response, err error, attempt, maxAttempts int, elapsed time.Duration) {
	fields := []any{"method", request.Method, "url", safeURL(request.URL), "attempt", attempt, "max_attempts", maxAttempts, "elapsed_ms", elapsed.Milliseconds()}
	if requestID := httpserver.RequestID(request.Context()); requestID != "" {
		fields = append(fields, "request_id", requestID)
	}
	if response != nil {
		fields = append(fields, "status", response.StatusCode)
	}
	if err != nil {
		upstreamErr := classifyNetworkError(err)
		c.logger.Warn("upstream request failed", append(fields, "error_code", Code(upstreamErr), "error", upstreamErr)...)
		return
	}
	if response == nil {
		c.logger.Warn("upstream request failed", append(fields, "error_code", Unknown, "error", "missing response")...)
		return
	}
	if rejection := classifyAccessError(response); rejection != nil {
		c.logger.Warn("upstream request rejected", append(fields, "error_code", rejection.Code, "error", rejection)...)
		return
	}
	switch {
	case response.StatusCode == http.StatusTooManyRequests:
		c.logger.Warn("upstream request rate limited", append(fields, "error_code", RateLimited)...)
	case response.StatusCode >= 500:
		c.logger.Warn("upstream request unavailable", append(fields, "error_code", Unavailable)...)
	case response.StatusCode >= 400:
		c.logger.Warn("upstream request rejected", append(fields, "error_code", InvalidResponse)...)
	case elapsed >= time.Second:
		c.logger.Info("slow upstream request", fields...)
	default:
		c.logger.Debug("upstream request succeeded", fields...)
	}
}

func classifyAccessError(response *http.Response) *Error {
	if strings.EqualFold(strings.TrimSpace(response.Header.Get("Cf-Mitigated")), "challenge") {
		return &Error{Code: ChallengeRequired, HTTPStatus: response.StatusCode, Message: "上游触发 Cloudflare 安全验证，请检查服务端网络和代理设置"}
	}
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return &Error{Code: InvalidCredentials, HTTPStatus: response.StatusCode, Message: "上游拒绝认证（HTTP 401），凭据可能已过期或失效"}
	case http.StatusForbidden:
		return &Error{Code: Forbidden, HTTPStatus: response.StatusCode, Message: "上游拒绝访问（HTTP 403），请检查账号权限或网络访问限制"}
	default:
		return nil
	}
}

func safeURL(value *url.URL) string {
	if value == nil {
		return ""
	}
	copy := *value
	copy.User = nil
	copy.RawQuery = ""
	copy.Fragment = ""
	return copy.String()
}

func (c *Client) httpClient(ctx context.Context) (*http.Client, error) {
	values, err := c.settings.Values(ctx)
	if err != nil {
		return nil, fmt.Errorf("load proxy settings: %w", err)
	}
	enabled, _ := values["proxy_enabled"].(bool)
	raw, _ := values["proxy_url"].(string)
	cacheKey := fmt.Sprintf("%t|%s", enabled, raw)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client != nil && c.cacheKey == cacheKey {
		return c.client, nil
	}
	var transport http.RoundTripper
	if c.browser {
		proxyURL := ""
		if enabled {
			proxyURL = raw
		}
		transport, err = newBrowserTransport(c.timeout, proxyURL)
	} else {
		transport, err = newStandardTransport(enabled, raw)
	}
	if err != nil {
		return nil, err
	}
	if c.client != nil {
		c.client.CloseIdleConnections()
	}
	c.cacheKey = cacheKey
	c.client = &http.Client{Transport: transport, Timeout: c.timeout}
	return c.client, nil
}

func (c *Client) CloseIdleConnections() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client != nil {
		c.client.CloseIdleConnections()
	}
}

func newStandardTransport(enabled bool, raw string) (*http.Transport, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if enabled {
		parsed, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("parse proxy URL: %w", err)
		}
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https":
			transport.Proxy = http.ProxyURL(parsed)
		case "socks5":
			var auth *xproxy.Auth
			if parsed.User != nil {
				password, _ := parsed.User.Password()
				auth = &xproxy.Auth{User: parsed.User.Username(), Password: password}
			}
			dialer, err := xproxy.SOCKS5("tcp", parsed.Host, auth, &net.Dialer{Timeout: 10 * time.Second})
			if err != nil {
				return nil, fmt.Errorf("create SOCKS5 proxy: %w", err)
			}
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return dialer.Dial(network, address)
			}
		default:
			return nil, errors.New("unsupported proxy scheme")
		}
	}
	return transport, nil
}

func classifyNetworkError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Code: Timeout, Message: "上游请求超时", Cause: err}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &Error{Code: Timeout, Message: "上游请求超时", Cause: err}
	}
	return &Error{Code: NetworkError, Message: "无法连接上游服务", Cause: err}
}

func Code(err error) string {
	var upstreamError *Error
	if errors.As(err, &upstreamError) {
		return upstreamError.Code
	}
	return Unknown
}
