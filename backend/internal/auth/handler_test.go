package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type failingStore struct {
	sessionErr error
	revokeErr  error
}

func (s failingStore) AdminByUsername(context.Context, string) (Admin, error) {
	return Admin{}, errors.New("database unavailable")
}
func (s failingStore) CreateSession(context.Context, Session) error { return nil }
func (s failingStore) Session(context.Context, string) (Session, error) {
	return Session{IDHash: "session", AdminID: "admin"}, s.sessionErr
}
func (s failingStore) DeleteSession(context.Context, string) error                  { return s.revokeErr }
func (s failingStore) UpdatePassword(context.Context, string, string, string) error { return nil }

func TestRequirePreservesCookiesOnInfrastructureFailure(t *testing.T) {
	handler := NewHandler(NewService(failingStore{sessionErr: errors.New("private database failure")}, time.Hour), nil)
	request := httptest.NewRequest(http.MethodGet, "/api/private", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "valid-token"})
	response := httptest.NewRecorder()
	handler.Require(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("called protected handler") })).ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatal("storage failure cleared valid session cookies")
	}
	if strings.Contains(response.Body.String(), "private database failure") {
		t.Fatal("internal failure exposed")
	}
}

func TestRequireClearsInvalidSession(t *testing.T) {
	handler := NewHandler(NewService(failingStore{sessionErr: sql.ErrNoRows}, time.Hour), nil)
	request := httptest.NewRequest(http.MethodGet, "/api/private", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "expired-token"})
	response := httptest.NewRecorder()
	handler.Require(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("called protected handler") })).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || len(response.Result().Cookies()) != 2 {
		t.Fatalf("status=%d cookies=%v", response.Code, response.Result().Cookies())
	}
}

func TestLogoutReportsRevokeFailure(t *testing.T) {
	handler := NewHandler(NewService(failingStore{revokeErr: errors.New("private database failure")}, time.Hour), nil)
	request := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	request = request.WithContext(context.WithValue(request.Context(), sessionKey, Session{IDHash: "session"}))
	response := httptest.NewRecorder()
	handler.Logout(response, request)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "LOGOUT_FAILED") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if len(response.Result().Cookies()) != 2 {
		t.Fatal("client cookies were not cleared")
	}
	if strings.Contains(response.Body.String(), "private database failure") {
		t.Fatal("internal failure exposed")
	}
}
