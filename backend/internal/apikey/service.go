package apikey

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"account-hub/internal/platform/apperror"
	"account-hub/internal/platform/security"
)

const keyPrefix = "ahk_"

type View struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	KeyMasked  string `json:"key_masked"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt *int64 `json:"last_used_at"`
}

// SecretView is returned only by an administrator-authenticated create command.
type SecretView struct {
	APIKey View   `json:"api_key"`
	Secret string `json:"secret"`
}

type Service struct {
	repository *Repository
}

func NewService(repository *Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) List(ctx context.Context) ([]View, error) { return s.repository.List(ctx) }

func (s *Service) Create(ctx context.Context, name string) (SecretView, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 15 {
		return SecretView{}, apperror.New(apperror.Validation, "名称需为 1–15 个字符")
	}
	random, err := security.RandomToken(32)
	if err != nil {
		return SecretView{}, err
	}
	id, err := security.RandomID("key")
	if err != nil {
		return SecretView{}, err
	}
	secret := keyPrefix + random
	view := View{ID: id, Name: name, KeyMasked: secret[:8] + "…" + secret[len(secret)-4:], CreatedAt: time.Now().UTC().UnixMilli()}
	if err := s.repository.Create(ctx, view, security.HashToken(secret)); err != nil {
		return SecretView{}, err
	}
	return SecretView{APIKey: view, Secret: secret}, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return apperror.New(apperror.Validation, "请选择要删除的 Key")
	}
	return s.repository.Delete(ctx, id)
}

func (s *Service) Authenticate(ctx context.Context, secret string) (string, error) {
	invalid := apperror.New(apperror.Unauthenticated, "API Key 无效或已删除")
	if len(secret) != len(keyPrefix)+43 || !strings.HasPrefix(secret, keyPrefix) {
		return "", invalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(secret, keyPrefix))
	if err != nil || len(decoded) != 32 {
		return "", invalid
	}
	id, err := s.repository.UseActive(ctx, security.HashToken(secret))
	if errors.Is(err, sql.ErrNoRows) {
		return "", invalid
	}
	if err != nil {
		return "", apperror.Wrap(apperror.Unavailable, "暂时无法验证 API Key", err)
	}
	return id, nil
}
