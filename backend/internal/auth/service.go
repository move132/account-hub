package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"account-hub/internal/platform/apperror"
	"account-hub/internal/platform/security"
)

var (
	ErrInvalidCredentials = apperror.New(apperror.Unauthenticated, "invalid credentials")
	ErrSessionInvalid     = apperror.New(apperror.Unauthenticated, "登录已失效")
)

type Store interface {
	AdminByUsername(context.Context, string) (Admin, error)
	CreateSession(context.Context, Session) error
	Session(context.Context, string) (Session, error)
	DeleteSession(context.Context, string) error
	UpdatePassword(context.Context, string, string, string) error
}

type Service struct {
	repository Store
	sessionTTL time.Duration
}

func NewService(repository Store, sessionTTL time.Duration) *Service {
	return &Service{repository: repository, sessionTTL: sessionTTL}
}

// Bootstrap creates the first administrator and returns the generated password.
// Existing administrators are left unchanged and return an empty password.
func Bootstrap(ctx context.Context, repository *Repository, username string) (string, error) {
	count, err := repository.AdminCount(ctx)
	if err != nil {
		return "", fmt.Errorf("count admins: %w", err)
	}
	if count == 0 {
		password, err := security.RandomPassword(10)
		if err != nil {
			return "", fmt.Errorf("generate admin password: %w", err)
		}
		hash, err := security.HashPassword(password)
		if err != nil {
			return "", fmt.Errorf("hash admin password: %w", err)
		}
		id, err := security.RandomID("admin")
		if err != nil {
			return "", err
		}
		now := time.Now().UTC().UnixMilli()
		if err := repository.CreateAdmin(ctx, Admin{ID: id, Username: username, PasswordHash: hash, CreatedAt: now, UpdatedAt: now}); err != nil {
			return "", err
		}
		return password, nil
	}
	return "", nil
}

func (s *Service) Login(ctx context.Context, username, password string) (string, string, Session, error) {
	admin, err := s.repository.AdminByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", Session{}, ErrInvalidCredentials
		}
		return "", "", Session{}, err
	}
	if !security.VerifyPassword(password, admin.PasswordHash) {
		return "", "", Session{}, ErrInvalidCredentials
	}
	sessionToken, err := security.RandomToken(32)
	if err != nil {
		return "", "", Session{}, err
	}
	csrfToken, err := security.RandomToken(24)
	if err != nil {
		return "", "", Session{}, err
	}
	now := time.Now().UTC()
	session := Session{
		IDHash: security.HashToken(sessionToken), AdminID: admin.ID, Username: admin.Username,
		CSRFHash: security.HashToken(csrfToken), ExpiresAt: now.Add(s.sessionTTL).UnixMilli(),
		CreatedAt: now.UnixMilli(), LastSeenAt: now.UnixMilli(),
	}
	if err := s.repository.CreateSession(ctx, session); err != nil {
		return "", "", Session{}, err
	}
	return sessionToken, csrfToken, session, nil
}

func (s *Service) Authenticate(ctx context.Context, sessionToken string) (Session, error) {
	if sessionToken == "" {
		return Session{}, ErrInvalidCredentials
	}
	session, err := s.repository.Session(ctx, security.HashToken(sessionToken))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrSessionInvalid
	}
	return session, err
}

func (s *Service) VerifyCSRF(session Session, cookieValue, headerValue string) bool {
	if cookieValue == "" || headerValue == "" || len(cookieValue) != len(headerValue) {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(cookieValue), []byte(headerValue)) != 1 {
		return false
	}
	want := security.HashToken(headerValue)
	return subtle.ConstantTimeCompare([]byte(want), []byte(session.CSRFHash)) == 1
}

func (s *Service) Logout(ctx context.Context, sessionHash string) error {
	return s.repository.DeleteSession(ctx, sessionHash)
}

func (s *Service) ChangePassword(ctx context.Context, session Session, current, next, confirmation string) error {
	if next != confirmation {
		return apperror.New(apperror.Validation, "两次输入的新密码不一致")
	}
	admin, err := s.repository.AdminByUsername(ctx, session.Username)
	if err != nil {
		return err
	}
	if !security.VerifyPassword(current, admin.PasswordHash) {
		return ErrInvalidCredentials
	}
	if err := security.ValidatePassword(next); err != nil {
		return apperror.Wrap(apperror.Validation, err.Error(), err)
	}
	hash, err := security.HashPassword(next)
	if err != nil {
		return err
	}
	return s.repository.UpdatePassword(ctx, admin.ID, hash, session.IDHash)
}
