package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"account-hub/internal/platform/security"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrPasswordManaged    = errors.New("password managed by environment")
)

type Service struct {
	repository      *Repository
	passwordManaged bool
	sessionTTL      time.Duration
}

func NewService(repository *Repository, passwordManaged bool, sessionTTL time.Duration) *Service {
	return &Service{repository: repository, passwordManaged: passwordManaged, sessionTTL: sessionTTL}
}

func Bootstrap(ctx context.Context, repository *Repository, username, environmentPassword string, managed bool) error {
	count, err := repository.AdminCount(ctx)
	if err != nil {
		return fmt.Errorf("count admins: %w", err)
	}
	if count == 0 {
		if !managed || environmentPassword == "" {
			return errors.New("empty database requires ADMIN_PASSWORD")
		}
		hash, err := security.HashPassword(environmentPassword)
		if err != nil {
			return fmt.Errorf("invalid ADMIN_PASSWORD: %w", err)
		}
		id, err := security.RandomID("admin")
		if err != nil {
			return err
		}
		now := time.Now().UTC().UnixMilli()
		return repository.CreateAdmin(ctx, Admin{ID: id, Username: username, PasswordHash: hash, CreatedAt: now, UpdatedAt: now})
	}
	return nil
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
	return s.repository.Session(ctx, security.HashToken(sessionToken))
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
		return errors.New("两次输入的新密码不一致")
	}
	admin, err := s.repository.AdminByUsername(ctx, session.Username)
	if err != nil {
		return err
	}
	if !security.VerifyPassword(current, admin.PasswordHash) {
		return ErrInvalidCredentials
	}
	hash, err := security.HashPassword(next)
	if err != nil {
		return err
	}
	return s.repository.UpdatePassword(ctx, admin.ID, hash, session.IDHash)
}

func (s *Service) PasswordManaged() bool { return s.passwordManaged }
