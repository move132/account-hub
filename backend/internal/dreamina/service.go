package dreamina

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"account-hub/internal/platform/apperror"
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
		ID: record.ID, Email: record.Email, PasswordMasked: security.MaskSecret(record.Password),
		SessionIDMasked: security.MaskSecret(record.SessionID), HasSessionID: record.SessionID != "",
		SessionExpiresAt: record.SessionExpiresAt, Status: record.Status, CheckState: record.CheckState,
		CreditBalance: record.CreditBalance, Note: record.Note, NextRefreshAt: record.NextRefreshAt,
		LastCheckedAt: record.LastCheckedAt, LastRefreshedAt: record.LastRefreshedAt,
		LastErrorCode: record.LastErrorCode, LastErrorMessage: record.LastErrorMessage,
		Version: record.Version, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

func ToEditView(record Record) EditView {
	return EditView{View: ToView(record), Password: record.Password, SessionID: record.SessionID}
}

func normalizeCreate(input CreateInput) (CreateInput, error) {
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.Password = strings.TrimSpace(input.Password)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.Note = strings.TrimSpace(input.Note)
	if input.Email == "" || input.Password == "" {
		return CreateInput{}, apperror.New(apperror.Validation, "email 和 password 不能为空")
	}
	if err := validateLengths(input.Email, input.Password, input.SessionID, input.Note); err != nil {
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

func (s *Service) ActiveSessionIDs(ctx context.Context) ([]string, error) {
	return s.repository.ActiveSessionIDs(ctx)
}

func (s *Service) Update(ctx context.Context, id string, input UpdateInput) (View, error) {
	if input.Email != nil {
		normalized := strings.ToLower(strings.TrimSpace(*input.Email))
		if normalized == "" {
			return View{}, apperror.New(apperror.Validation, "email 不能为空")
		}
		input.Email = &normalized
	}
	if input.Password != nil && strings.TrimSpace(*input.Password) == "" {
		return View{}, apperror.New(apperror.Validation, "password 不能为空")
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

func validateLengths(email, password, sessionID, note string) error {
	switch {
	case len([]rune(email)) > 320:
		return apperror.New(apperror.Validation, "email 不能超过 320 个字符")
	case len(password) > 4096:
		return apperror.New(apperror.Validation, "password 不能超过 4096 字节")
	case len(sessionID) > 256*1024:
		return apperror.New(apperror.Validation, "session_id 不能超过 256 KB")
	case len([]rune(note)) > 2000:
		return apperror.New(apperror.Validation, "note 不能超过 2000 个字符")
	}
	return nil
}

func validateOptionalLengths(input UpdateInput) error {
	values := struct{ email, password, session, note string }{}
	if input.Email != nil {
		values.email = *input.Email
	}
	if input.Password != nil {
		values.password = *input.Password
	}
	if input.SessionID != nil {
		values.session = *input.SessionID
	}
	if input.Note != nil {
		values.note = *input.Note
	}
	return validateLengths(values.email, values.password, values.session, values.note)
}

func (s *Service) Delete(ctx context.Context, id string) error { return s.repository.Delete(ctx, id) }

func (s *Service) Check(ctx context.Context, id string) (View, CreditResult, error) {
	record, err := s.repository.Get(ctx, id)
	if err != nil {
		return View{}, CreditResult{}, err
	}
	if completed, err := s.repository.operationCompleted(ctx, id, "check"); err != nil {
		return View{}, CreditResult{}, err
	} else if completed {
		return ToView(record), CreditResult{Balance: record.CreditBalance}, nil
	}
	if record.SessionID == "" {
		return View{}, CreditResult{}, apperror.New(apperror.Validation, "该账号没有 session_id")
	}
	attempt, err := s.newAttempt(ctx, record, "check")
	if err != nil {
		return View{}, CreditResult{}, err
	}
	credit, operationErr := s.client.Check(ctx, record.SessionID)
	s.finishAttempt(&attempt, operationErr)
	writeCtx, cancel := completionContext(ctx)
	defer cancel()
	updated, err := s.repository.CompleteCheck(writeCtx, record, checkState(operationErr), credit.Balance, attempt.ErrorCode, attempt.ErrorMessage, attempt)
	if err != nil {
		return View{}, CreditResult{}, err
	}
	return ToView(updated), credit, operationErr
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
	attempt, err := s.newAttempt(ctx, record, "refresh")
	if err != nil {
		return View{}, err
	}
	login, operationErr := s.client.Login(ctx, record.Email, record.Password)
	s.finishAttempt(&attempt, operationErr)
	writeCtx, cancel := completionContext(ctx)
	defer cancel()
	next := refreshDeadline(ctx, record, s.now().UTC(), s.refreshInterval(writeCtx), operationErr)
	updated, err := s.repository.CompleteRefresh(writeCtx, record, login, next, attempt)
	if err != nil {
		s.logger.Error("dreamina refresh outcome could not be committed", "account_id", id, "error", err, "upstream_succeeded", operationErr == nil)
		return View{}, err
	}
	return ToView(updated), operationErr
}

func (s *Service) Credit(ctx context.Context, id string) (CreditResult, error) {
	_, credit, err := s.Check(ctx, id)
	return credit, err
}

func checkState(err error) string {
	if err == nil {
		return "valid"
	}
	if upstream.Code(err) == upstream.InvalidCredentials {
		return "invalid"
	}
	return "error"
}

func (s *Service) newAttempt(ctx context.Context, record Record, operation string) (Attempt, error) {
	id, err := security.RandomID("da")
	if err != nil {
		return Attempt{}, err
	}
	return Attempt{ID: id, OperationKey: operationKey(ctx), AccountID: record.ID, TargetLabel: record.Email, Operation: operation, StartedAt: s.now().UTC().UnixMilli()}, nil
}

func (s *Service) finishAttempt(attempt *Attempt, operationErr error) {
	completed := s.now().UTC().UnixMilli()
	attempt.CompletedAt, attempt.Status = &completed, "succeeded"
	if operationErr != nil {
		attempt.OperationKey = ""
		attempt.Status, attempt.ErrorCode, attempt.ErrorMessage = "failed", upstream.Code(operationErr), operationErr.Error()
	}
}

func (s *Service) Assets(ctx context.Context, id string, offset, count int) (AssetPage, error) {
	if offset < 0 {
		offset = 0
	}
	if count < 1 || count > 100 {
		count = 50
	}
	record, err := s.repository.Get(ctx, id)
	if err != nil {
		return AssetPage{}, err
	}
	if record.SessionID == "" {
		s.logger.Warn("dreamina assets skipped", "account_id", id, "email", record.Email, "reason", "missing_session_id", "offset", offset, "count", count)
		return AssetPage{}, apperror.New(apperror.Validation, "该账号没有 session_id")
	}
	startedAt := time.Now()
	s.logger.Info("dreamina assets started", "account_id", id, "email", record.Email, "offset", offset, "count", count)
	result, err := s.client.Assets(ctx, record.SessionID, offset, count)
	if err != nil {
		s.logger.Warn("dreamina assets failed", "account_id", id, "email", record.Email, "offset", offset, "count", count, "error_code", upstream.Code(err), "error", err, "elapsed_ms", time.Since(startedAt).Milliseconds())
		return AssetPage{}, err
	}
	page, err := normalizeAssetPage(result, offset)
	if err != nil {
		s.logger.Warn("dreamina assets normalization failed", "account_id", id, "offset", offset, "count", count, "error_code", upstream.Code(err), "error", err)
		return AssetPage{}, err
	}
	s.logger.Info("dreamina assets succeeded", "account_id", id, "email", record.Email, "offset", offset, "count", count, "elapsed_ms", time.Since(startedAt).Milliseconds())
	return page, nil
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
	return snapshot.Dreamina.Interval
}

func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows) }
func ErrorCode(err error) string {
	if errors.Is(err, ErrVersionConflict) {
		return "VERSION_CONFLICT"
	}
	if IsNotFound(err) {
		return "DREAMINA_NOT_FOUND"
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
