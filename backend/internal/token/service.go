package token

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"account-hub/internal/platform/security"
	"account-hub/internal/platform/upstream"
	"account-hub/internal/settings"
)

type Service struct {
	repository *Repository
	client     UpstreamClient
	settings   *settings.Repository
	logger     *slog.Logger
}

const (
	refreshStageSession = "session_refresh"
	refreshStageProfile = "profile_check"
)

type refreshError struct {
	stage string
	cause error
}

func (e *refreshError) Error() string {
	message := "使用 Session Token 获取 Access Token 失败"
	if e.stage == refreshStageProfile {
		message = "已获取新 Access Token，但账号校验失败"
	}
	return message + ": " + e.cause.Error()
}

func (e *refreshError) Unwrap() error { return e.cause }

func NewService(repository *Repository, client UpstreamClient, settingsRepository *settings.Repository) *Service {
	return &Service{repository: repository, client: client, settings: settingsRepository, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func (s *Service) WithLogger(logger *slog.Logger) *Service {
	if logger != nil {
		s.logger = logger
	}
	return s
}

func ToView(record Record) View {
	return View{
		ID: record.ID, Name: record.Name, Email: record.Email,
		AccessTokenMasked: security.MaskSecret(record.AccessToken), SessionTokenMasked: security.MaskSecret(record.SessionToken),
		HasSessionToken: record.SessionToken != "", AccessExpiresAt: record.AccessExpiresAt,
		Status: record.Status, CheckState: record.CheckState,
		Note: record.Note, NextRefreshAt: record.NextRefreshAt,
		LastCheckedAt: record.LastCheckedAt, LastRefreshedAt: record.LastRefreshedAt,
		LastErrorCode: record.LastErrorCode, LastErrorMessage: record.LastErrorMessage,
		Version: record.Version, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

func ToEditView(record Record) EditView {
	return EditView{View: ToView(record), AccessToken: record.AccessToken, SessionToken: record.SessionToken}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (View, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.AccessToken = strings.TrimSpace(strings.TrimPrefix(input.AccessToken, "Bearer "))
	input.SessionToken = strings.TrimSpace(input.SessionToken)
	input.Note = strings.TrimSpace(input.Note)
	if input.AccessToken == "" {
		return View{}, errors.New("access_token 不能为空")
	}
	if err := validateLengths(input.Name, input.Email, input.AccessToken, input.SessionToken, input.Note); err != nil {
		return View{}, err
	}
	if input.Status != "" && input.Status != "enabled" && input.Status != "disabled" {
		return View{}, errors.New("status 只能是 enabled 或 disabled")
	}
	record, err := s.repository.Create(ctx, input)
	return ToView(record), err
}

func (s *Service) Get(ctx context.Context, id string) (View, error) {
	record, err := s.repository.Get(ctx, id)
	return ToView(record), err
}

func (s *Service) GetForEdit(ctx context.Context, id string) (EditView, error) {
	record, err := s.repository.Get(ctx, id)
	return ToEditView(record), err
}

func (s *Service) List(ctx context.Context, filter ListFilter) ([]View, int, error) {
	records, total, err := s.repository.List(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	views := make([]View, 0, len(records))
	for _, record := range records {
		views = append(views, ToView(record))
	}
	return views, total, nil
}

func (s *Service) Update(ctx context.Context, id string, input UpdateInput) (View, error) {
	if input.AccessToken != nil {
		trimmed := strings.TrimSpace(strings.TrimPrefix(*input.AccessToken, "Bearer "))
		if trimmed == "" {
			return View{}, errors.New("access_token 不能为空")
		}
		input.AccessToken = &trimmed
	}
	if input.Email != nil {
		normalized := strings.ToLower(strings.TrimSpace(*input.Email))
		input.Email = &normalized
	}
	if input.Status != nil && *input.Status != "enabled" && *input.Status != "disabled" {
		return View{}, errors.New("status 只能是 enabled 或 disabled")
	}
	if err := validateOptionalLengths(input); err != nil {
		return View{}, err
	}
	record, err := s.repository.Update(ctx, id, input)
	return ToView(record), err
}

func validateLengths(name, email, accessToken, sessionToken, note string) error {
	switch {
	case len([]rune(name)) > 200:
		return errors.New("name 不能超过 200 个字符")
	case len([]rune(email)) > 320:
		return errors.New("email 不能超过 320 个字符")
	case len(accessToken) > 256*1024:
		return errors.New("access_token 不能超过 256 KB")
	case len(sessionToken) > 256*1024:
		return errors.New("session_token 不能超过 256 KB")
	case len([]rune(note)) > 2000:
		return errors.New("note 不能超过 2000 个字符")
	}
	return nil
}

func validateOptionalLengths(input UpdateInput) error {
	values := struct{ name, email, access, session, note string }{}
	if input.Name != nil {
		values.name = *input.Name
	}
	if input.Email != nil {
		values.email = *input.Email
	}
	if input.AccessToken != nil {
		values.access = *input.AccessToken
	}
	if input.SessionToken != nil {
		values.session = *input.SessionToken
	}
	if input.Note != nil {
		values.note = *input.Note
	}
	return validateLengths(values.name, values.email, values.access, values.session, values.note)
}

func (s *Service) Delete(ctx context.Context, id string) error { return s.repository.Delete(ctx, id) }

func (s *Service) Check(ctx context.Context, id string) (View, error) {
	record, err := s.repository.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	startedAt := time.Now()
	s.logger.Info("token check started", "token_id", id, "target", label(record), "status", record.Status, "has_session_token", record.SessionToken != "")
	started := time.Now().UTC().UnixMilli()
	profile, checkErr := s.client.Check(ctx, record.AccessToken)
	completed := time.Now().UTC().UnixMilli()
	attempt := Attempt{TokenID: id, TargetLabel: label(record), Operation: "check", StartedAt: started, CompletedAt: &completed}
	attempt.ID, _ = security.RandomID("ta")
	if checkErr != nil {
		code := upstream.Code(checkErr)
		state := "error"
		if code == upstream.InvalidCredentials {
			state = "invalid"
		}
		_ = s.repository.UpdateCheck(ctx, id, state, "", code, checkErr.Error())
		attempt.Status, attempt.ErrorCode, attempt.ErrorMessage = "failed", code, checkErr.Error()
		_ = s.repository.RecordAttempt(ctx, attempt)
		s.logger.Warn("token check failed", "token_id", id, "target", label(record), "state", state, "error_code", code, "error", checkErr, "elapsed_ms", time.Since(startedAt).Milliseconds())
		return View{}, checkErr
	}
	if err := s.repository.UpdateCheck(ctx, id, "valid", profile.Email, "", ""); err != nil {
		s.logger.Error("token check database update failed", "token_id", id, "target", label(record), "error", err, "elapsed_ms", time.Since(startedAt).Milliseconds())
		return View{}, err
	}
	attempt.Status = "succeeded"
	_ = s.repository.RecordAttempt(ctx, attempt)
	s.logger.Info("token check succeeded", "token_id", id, "target", label(record), "email", profile.Email, "user_id", profile.UserID, "elapsed_ms", time.Since(startedAt).Milliseconds())
	return s.Get(ctx, id)
}

func (s *Service) Refresh(ctx context.Context, id string) (View, error) {
	record, err := s.repository.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	if record.SessionToken == "" {
		s.logger.Warn("token refresh skipped", "token_id", id, "target", label(record), "reason", "missing_session_token")
		return View{}, errors.New("该记录没有 session_token")
	}
	startedAt := time.Now()
	s.logger.Info("token refresh started", "token_id", id, "target", label(record), "status", record.Status)
	started := time.Now().UTC().UnixMilli()
	result, refreshErr := s.client.Refresh(ctx, record.SessionToken)
	var profile Profile
	if refreshErr != nil {
		refreshErr = &refreshError{stage: refreshStageSession, cause: refreshErr}
	} else {
		s.logger.Info("token refresh received access token, validating profile", "token_id", id, "target", label(record), "expires", result.Expires, "session_rotated", result.SessionToken != record.SessionToken)
		profile, refreshErr = s.client.Check(ctx, result.AccessToken)
		if refreshErr != nil {
			refreshErr = &refreshError{stage: refreshStageProfile, cause: refreshErr}
		}
	}
	completed := time.Now().UTC().UnixMilli()
	attempt := Attempt{TokenID: id, TargetLabel: label(record), Operation: "refresh", StartedAt: started, CompletedAt: &completed}
	attempt.ID, _ = security.RandomID("ta")
	interval := s.intSetting(ctx, "token_refresh_interval_hours", 168)
	next := time.Now().UTC().Add(time.Duration(interval) * time.Hour).UnixMilli()
	if refreshErr != nil {
		code := upstream.Code(refreshErr)
		_ = s.repository.SetNextRefresh(ctx, id, next, code, refreshErr.Error())
		attempt.Status, attempt.ErrorCode, attempt.ErrorMessage = "failed", code, refreshErr.Error()
		_ = s.repository.RecordAttempt(ctx, attempt)
		s.logger.Warn("token refresh failed", "token_id", id, "target", label(record), "error_code", code, "error", refreshErr, "next_refresh_at", next, "elapsed_ms", time.Since(startedAt).Milliseconds())
		return View{}, refreshErr
	}
	if err := s.repository.UpdateRefresh(ctx, id, result, profile, next); err != nil {
		s.logger.Error("token refresh database update failed", "token_id", id, "target", label(record), "error", err, "elapsed_ms", time.Since(startedAt).Milliseconds())
		return View{}, err
	}
	attempt.Status = "succeeded"
	_ = s.repository.RecordAttempt(ctx, attempt)
	s.logger.Info("token refresh succeeded", "token_id", id, "target", label(record), "email", profile.Email, "expires", result.Expires, "next_refresh_at", next, "elapsed_ms", time.Since(startedAt).Milliseconds())
	return s.Get(ctx, id)
}

func (s *Service) Attempts(ctx context.Context, id string, limit int) ([]Attempt, error) {
	if _, err := s.repository.Get(ctx, id); err != nil {
		return nil, err
	}
	return s.repository.Attempts(ctx, id, limit)
}

func (s *Service) SetStatus(ctx context.Context, id, status string) error {
	return s.repository.SetStatus(ctx, id, status)
}

func (s *Service) DueIDs(ctx context.Context, limit int) ([]string, error) {
	return s.repository.DueIDs(ctx, time.Now().UTC().UnixMilli(), limit)
}

func (s *Service) intSetting(ctx context.Context, key string, fallback int) int {
	values, err := s.settings.Values(ctx)
	if err != nil {
		return fallback
	}
	if value, ok := values[key].(int); ok {
		return value
	}
	return fallback
}

func label(record Record) string {
	if record.Email != "" {
		return record.Email
	}
	if record.Name != "" {
		return record.Name
	}
	return record.ID
}

func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
func ErrorCode(err error) string {
	if errors.Is(err, ErrVersionConflict) {
		return "VERSION_CONFLICT"
	}
	if IsNotFound(err) {
		return "TOKEN_NOT_FOUND"
	}
	return upstream.Code(err)
}
