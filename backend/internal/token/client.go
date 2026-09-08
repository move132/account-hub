package token

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"account-hub/internal/platform/upstream"
)

type Profile struct {
	Email  string
	UserID string
}

type RefreshResult struct {
	AccessToken  string
	SessionToken string
	Expires      string
}

type UpstreamClient interface {
	Check(context.Context, string) (Profile, error)
	Refresh(context.Context, string) (RefreshResult, error)
}

type Client struct {
	http        *upstream.Client
	chatGPTBase string
	soraBase    string
}

func NewClient(client *upstream.Client) *Client {
	return &Client{http: client, chatGPTBase: "https://chatgpt.com", soraBase: "https://sora.chatgpt.com"}
}

func NewClientWithBases(client *upstream.Client, chatGPTBase, soraBase string) *Client {
	return &Client{http: client, chatGPTBase: strings.TrimRight(chatGPTBase, "/"), soraBase: strings.TrimRight(soraBase, "/")}
}

func (c *Client) Check(ctx context.Context, accessToken string) (Profile, error) {
	response, err := c.http.Do(ctx, func(_ *http.Client) (*http.Request, error) {
		request, err := http.NewRequest(http.MethodGet, c.chatGPTBase+"/backend-api/me", nil)
		if err == nil {
			request.Header.Set("Authorization", bearer(accessToken))
			request.Header.Set("Accept", "application/json")
		}
		return request, err
	})
	if err != nil {
		return Profile{}, err
	}
	defer response.Body.Close()
	var payload struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil || payload.ID == "" {
		return Profile{}, &upstream.Error{Code: upstream.InvalidResponse, Message: "上游账户响应格式错误", Cause: err}
	}
	return Profile{Email: payload.Email, UserID: payload.ID}, nil
}

func (c *Client) Refresh(ctx context.Context, sessionToken string) (RefreshResult, error) {
	response, err := c.http.Do(ctx, func(_ *http.Client) (*http.Request, error) {
		request, err := http.NewRequest(http.MethodGet, c.soraBase+"/api/auth/session", nil)
		if err == nil {
			request.Header.Set("Cookie", "__Secure-next-auth.session-token="+sessionToken)
			request.Header.Set("Accept", "application/json")
		}
		return request, err
	})
	if err != nil {
		return RefreshResult{}, err
	}
	defer response.Body.Close()
	var payload struct {
		AccessToken string `json:"accessToken"`
		Expires     string `json:"expires"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil || payload.AccessToken == "" {
		return RefreshResult{}, &upstream.Error{Code: upstream.InvalidResponse, Message: "刷新响应缺少 accessToken", Cause: err}
	}
	newSession := sessionCookie(response.Cookies())
	if newSession == "" {
		newSession = sessionToken
	}
	return RefreshResult{AccessToken: payload.AccessToken, SessionToken: newSession, Expires: payload.Expires}, nil
}

func bearer(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "bearer ") {
		return value
	}
	return "Bearer " + value
}

func sessionCookie(cookies []*http.Cookie) string {
	parts := make(map[int]string)
	plain := ""
	for _, cookie := range cookies {
		switch cookie.Name {
		case "__Secure-next-auth.session-token":
			plain = cookie.Value
		case "__Secure-next-auth.session-token.0", "__Secure-next-auth.session-token.1", "__Secure-next-auth.session-token.2":
			var index int
			if _, err := fmt.Sscanf(cookie.Name, "__Secure-next-auth.session-token.%d", &index); err == nil {
				parts[index] = cookie.Value
			}
		}
	}
	if len(parts) == 0 {
		return plain
	}
	indexes := make([]int, 0, len(parts))
	for index := range parts {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	var builder strings.Builder
	for _, index := range indexes {
		builder.WriteString(parts[index])
	}
	return builder.String()
}

var _ UpstreamClient = (*Client)(nil)
