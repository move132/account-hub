package token

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"account-hub/internal/platform/apperror"
	"account-hub/internal/platform/refreshpolicy"
	"account-hub/internal/platform/security"
	"account-hub/internal/platform/upstream"
	"account-hub/internal/settings"
)

type SettingsReader interface {
	Snapshot(context.Context) (settings.Snapshot, error)
}

type Service struct {
	repository *Repository
	client     UpstreamClient
	settings   SettingsReader
	now        func() time.Time
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

func NewService(repository *Repository, client UpstreamClient, settingsRepository SettingsReader) *Service {
	return &Service{repository: repository, client: client, settings: settingsRepository, now: time.Now, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
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

func normalizeCreate(input CreateInput) (CreateInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.AccessToken = strings.TrimSpace(strings.TrimPrefix(input.AccessToken, "Bearer "))
	input.SessionToken = strings.TrimSpace(input.SessionToken)
	input.Note = strings.TrimSpace(input.Note)
	if input.AccessToken == "" {
		return CreateInput{}, apperror.New(apperror.Validation, "access_token 不能为空")
	}
	if err := validateLengths(input.Name, input.Email, input.AccessToken, input.SessionToken, input.Note); err != nil {
		return CreateInput{}, err
	}
	if input.Status != "" && input.Status != "enabled" && input.Status != "disabled" {
		return CreateInput{}, apperror.New(apperror.Validation, "status 只能是 enabled 或 disabled")
	}

	return input, nil
}

func (s *Service) Create(ctx context.Context, input CreateInput) (View, error) {
	normalized, err := normalizeCreate(input)
	if err != nil {
		return View{}, err
	}
	record, err := s.repository.Create(ctx, normalized)
	return ToView(record), err
}

func (s *Service) createWithCommit(ctx context.Context, input CreateInput, after func(context.Context, *sql.Tx, View) error) (View, error) {
	normalized, err := normalizeCreate(input)
	if err != nil {
		return View{}, err
	}
	record, err := s.repository.CreateWithCommit(ctx, normalized, func(ctx context.Context, tx *sql.Tx, record Record) error { return after(ctx, tx, ToView(record)) })
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

// TargetLabels reads only non-secret display fields in bounded batches.
func (s *Service) TargetLabels(ctx context.Context, ids []string) (map[string]string, error) {
	return s.repository.TargetLabels(ctx, ids)
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

func (s *Service) AccessTokensByIDs(ctx context.Context, ids []string) (map[string]string, error) {
	return s.repository.AccessTokensByIDs(ctx, ids)
}

func (s *Service) Update(ctx context.Context, id string, input UpdateInput) (View, error) {
	if input.AccessToken != nil {
		trimmed := strings.TrimSpace(strings.TrimPrefix(*input.AccessToken, "Bearer "))
		if trimmed == "" {
			return View{}, apperror.New(apperror.Validation, "access_token 不能为空")
		}
		input.AccessToken = &trimmed
	}
	if input.Email != nil {
		normalized := strings.ToLower(strings.TrimSpace(*input.Email))
		input.Email = &normalized
	}
	if input.Status != nil && *input.Status != "enabled" && *input.Status != "disabled" {
		return View{}, apperror.New(apperror.Validation, "status 只能是 enabled 或 disabled")
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
		return apperror.New(apperror.Validation, "name 不能超过 200 个字符")
	case len([]rune(email)) > 320:
		return apperror.New(apperror.Validation, "email 不能超过 320 个字符")
	case len(accessToken) > 256*1024:
		return apperror.New(apperror.Validation, "access_token 不能超过 256 KB")
	case len(sessionToken) > 256*1024:
		return apperror.New(apperror.Validation, "session_token 不能超过 256 KB")
	case len([]rune(note)) > 2000:
		return apperror.New(apperror.Validation, "note 不能超过 2000 个字符")
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
	if completed, err := s.repository.operationCompleted(ctx, id, "check"); err != nil {
		return View{}, err
	} else if completed {
		return ToView(record), nil
	}
	attempt, err := s.newAttempt(ctx, record, "check")
	if err != nil {
		return View{}, err
	}
	profile, operationErr := s.client.Check(ctx, record.AccessToken)
	s.finishAttempt(&attempt, operationErr)
	state := "valid"
	if operationErr != nil {
		state = "error"
		if upstream.Code(operationErr) == upstream.InvalidCredentials {
			state = "invalid"
		}
	}
	writeCtx, cancel := completionContext(ctx)
	defer cancel()
	updated, err := s.repository.CompleteCheck(writeCtx, record, state, profile.Email, attempt.ErrorCode, attempt.ErrorMessage, attempt)
	if err != nil {
		s.logger.Error("token check outcome could not be committed", "token_id", id, "error", err)
		return View{}, err
	}
	if operationErr != nil {
		return ToView(updated), operationErr
	}
	return ToView(updated), nil
}

func (s *Service) Refresh(ctx context.Context, id string) (View, error) {
	record, err := s.repository.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	if completed, err := s.repository.operationCompleted(ctx, id, "refresh"); err != nil {
		return View{}, err
	} else if completed {
		return ToView(record), nil
	}
	if record.SessionToken == "" {
		return View{}, apperror.New(apperror.Validation, "该记录没有 session_token")
	}
	attempt, err := s.newAttempt(ctx, record, "refresh")
	if err != nil {
		return View{}, err
	}
	result, operationErr := s.client.Refresh(ctx, record.SessionToken)
	var profile Profile
	if operationErr != nil {
		operationErr = &refreshError{stage: refreshStageSession, cause: operationErr}
	} else {
		profile, operationErr = s.client.Check(ctx, result.AccessToken)
		if operationErr != nil {
			operationErr = &refreshError{stage: refreshStageProfile, cause: operationErr}
		}
	}
	s.finishAttempt(&attempt, operationErr)
	writeCtx, cancel := completionContext(ctx)
	defer cancel()
	next := refreshpolicy.NextDeadline(ctx, record.NextRefreshAt, record.RefreshFailures, s.now().UTC(), s.refreshInterval(writeCtx), operationErr)
	updated, err := s.repository.CompleteRefresh(writeCtx, record, result, profile, next, attempt)
	if err != nil {
		s.logger.Error("token refresh outcome could not be committed", "token_id", id, "error", err, "upstream_succeeded", operationErr == nil)
		return View{}, err
	}
	if operationErr != nil {
		return ToView(updated), operationErr
	}
	s.logger.Info("token refresh succeeded", "token_id", id, "next_refresh_at", next)
	return ToView(updated), nil
}

func (s *Service) newAttempt(ctx context.Context, record Record, operation string) (Attempt, error) {
	id, err := security.RandomID("ta")
	if err != nil {
		return Attempt{}, err
	}
	return Attempt{ID: id, OperationKey: operationKey(ctx), TargetLabel: label(record), Operation: operation, StartedAt: s.now().UTC().UnixMilli()}, nil
}

func (s *Service) finishAttempt(attempt *Attempt, operationErr error) {
	completed := s.now().UTC().UnixMilli()
	attempt.CompletedAt, attempt.Status = &completed, "succeeded"
	if operationErr != nil {
		attempt.OperationKey = ""
		attempt.Status, attempt.ErrorCode, attempt.ErrorMessage = "failed", upstream.Code(operationErr), operationErr.Error()
	}
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
	return s.repository.DueIDs(ctx, s.now().UTC().UnixMilli(), limit)
}

func (s *Service) refreshInterval(ctx context.Context) time.Duration {
	snapshot, err := s.settings.Snapshot(ctx)
	if err != nil {
		s.logger.Warn("read refresh policy", "error", err)
		return 7 * 24 * time.Hour
	}
	return snapshot.Token.Interval
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

func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows) }
func ErrorCode(err error) string {
	if errors.Is(err, ErrVersionConflict) {
		return "VERSION_CONFLICT"
	}
	if IsNotFound(err) {
		return "TOKEN_NOT_FOUND"
	}
	var providerErr *upstream.Error
	if errors.As(err, &providerErr) {
		return providerErr.Code
	}
	switch apperror.KindOf(err) {
	case apperror.Validation:
		return "VALIDATION_FAILED"
	case apperror.Conflict:
		return "VERSION_CONFLICT"
	default:
		return "INTERNAL_ERROR"
	}
}
