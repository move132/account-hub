package apikey

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"account-hub/internal/platform/apperror"
)

type unavailableAuthenticator struct{}

func (unavailableAuthenticator) Authenticate(context.Context, string) (string, error) {
	return "", apperror.Wrap(apperror.Unavailable, "unavailable", errors.New("private database error"))
}

func TestAuthenticationStorageFailureDoesNotAuthorizeOrFallBack(t *testing.T) {
	deny := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("authentication failure reached a handler") })
	handler := NewMiddleware(unavailableAuthenticator{}).Allow(deny, deny)
	request := httptest.NewRequest("GET", "/api/v1/tokens", nil)
	request.Header.Set("Authorization", "Bearer supplied-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "private database error") {
		t.Fatalf("storage failure: %d %s", response.Code, response.Body)
	}
}
