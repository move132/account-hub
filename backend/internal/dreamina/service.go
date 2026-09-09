package dreamina

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

func (s *Service) Create(ctx context.Context, input CreateInput) (View, error) {
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.Password = strings.TrimSpace(input.Password)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.Note = strings.TrimSpace(input.Note)
	if input.Email == "" || input.Password == "" {
		return View{}, errors.New("email 和 password 不能为空")
	}
	if err := validateLengths(input.Email, input.Password, input.SessionID, input.Note); err != nil {
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

func (s *Service) ActiveSessionIDs(ctx context.Context) ([]string, error) {
	return s.repository.ActiveSessionIDs(ctx)
}

func (s *Service) Update(ctx context.Context, id string, input UpdateInput) (View, error) {
	if input.Email != nil {
		normalized := strings.ToLower(strings.TrimSpace(*input.Email))
		if normalized == "" {
			return View{}, errors.New("email 不能为空")
		}
		input.Email = &normalized
	}
	if input.Password != nil && strings.TrimSpace(*input.Password) == "" {
		return View{}, errors.New("password 不能为空")
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

func validateLengths(email, password, sessionID, note string) error {
	switch {
	case len([]rune(email)) > 320:
		return errors.New("email 不能超过 320 个字符")
	case len(password) > 4096:
		return errors.New("password 不能超过 4096 字节")
	case len(sessionID) > 256*1024:
		return errors.New("session_id 不能超过 256 KB")
	case len([]rune(note)) > 2000:
		return errors.New("note 不能超过 2000 个字符")
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
	if record.SessionID == "" {
		s.logger.Warn("dreamina check skipped", "account_id", id, "email", record.Email, "reason", "missing_session_id")
		return View{}, CreditResult{}, errors.New("该账号没有 session_id")
	}
	startedAt := time.Now()
	s.logger.Info("dreamina check started", "account_id", id, "email", record.Email, "status", record.Status)
	started := time.Now().UTC().UnixMilli()
	credit, operationErr := s.client.Check(ctx, record.SessionID)
	completed := time.Now().UTC().UnixMilli()
	s.recordAttempt(ctx, record, "check", started, completed, operationErr)
	if operationErr != nil {
		state := "error"
		if upstream.Code(operationErr) == upstream.InvalidCredentials {
			state = "invalid"
		}
		_ = s.repository.UpdateCheck(ctx, id, state, "", upstream.Code(operationErr), operationErr.Error())
		s.logger.Warn("dreamina check failed", "account_id", id, "email", record.Email, "state", state, "error_code", upstream.Code(operationErr), "error", operationErr, "elapsed_ms", time.Since(startedAt).Milliseconds())
		return View{}, CreditResult{}, operationErr
	}
	if err := s.repository.UpdateCheck(ctx, id, "valid", credit.Balance, "", ""); err != nil {
		s.logger.Error("dreamina check database update failed", "account_id", id, "email", record.Email, "error", err, "elapsed_ms", time.Since(startedAt).Milliseconds())
		return View{}, CreditResult{}, err
	}
	view, err := s.Get(ctx, id)
	if err != nil {
		s.logger.Error("dreamina check reload failed", "account_id", id, "email", record.Email, "error", err, "elapsed_ms", time.Since(startedAt).Milliseconds())
		return View{}, CreditResult{}, err
	}
	s.logger.Info("dreamina check succeeded", "account_id", id, "email", record.Email, "credit_balance", credit.Balance, "elapsed_ms", time.Since(startedAt).Milliseconds())
	return view, credit, err
}

func (s *Service) Refresh(ctx context.Context, id string) (View, error) {
	record, err := s.repository.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	startedAt := time.Now()
	s.logger.Info("dreamina refresh started", "account_id", id, "email", record.Email, "status", record.Status)
	started := time.Now().UTC().UnixMilli()
	login, operationErr := s.client.Login(ctx, record.Email, record.Password)
	completed := time.Now().UTC().UnixMilli()
	s.recordAttempt(ctx, record, "refresh", started, completed, operationErr)
	intervalDays := s.intSetting(ctx, "dreamina_refresh_interval_days", 7)
	next := time.Now().UTC().Add(time.Duration(intervalDays) * 24 * time.Hour).UnixMilli()
	if operationErr != nil {
		_ = s.repository.SetNextRefresh(ctx, id, next, upstream.Code(operationErr), operationErr.Error())
		s.logger.Warn("dreamina refresh failed", "account_id", id, "email", record.Email, "error_code", upstream.Code(operationErr), "error", operationErr, "next_refresh_at", next, "elapsed_ms", time.Since(startedAt).Milliseconds())
		return View{}, operationErr
	}
	if err := s.repository.UpdateRefresh(ctx, id, login, next); err != nil {
		s.logger.Error("dreamina refresh database update failed", "account_id", id, "email", record.Email, "error", err, "elapsed_ms", time.Since(startedAt).Milliseconds())
		return View{}, err
	}
	s.logger.Info("dreamina refresh succeeded", "account_id", id, "email", record.Email, "session_expires_at", login.SessionExpiresAt, "next_refresh_at", next, "elapsed_ms", time.Since(startedAt).Milliseconds())
	return s.Get(ctx, id)
}

func (s *Service) Credit(ctx context.Context, id string) (CreditResult, error) {
	_, credit, err := s.Check(ctx, id)
	return credit, err
}

func (s *Service) ClaimCredit(ctx context.Context, id string) (any, error) {
	record, err := s.repository.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if record.SessionID == "" {
		s.logger.Warn("dreamina credit claim skipped", "account_id", id, "email", record.Email, "reason", "missing_session_id")
		return nil, errors.New("该账号没有 session_id")
	}
	startedAt := time.Now()
	s.logger.Info("dreamina credit claim started", "account_id", id, "email", record.Email, "status", record.Status)
	started := time.Now().UTC().UnixMilli()
	result, operationErr := s.client.ClaimCredit(ctx, record.SessionID)
	completed := time.Now().UTC().UnixMilli()
	s.recordAttempt(ctx, record, "credit_claim", started, completed, operationErr)
	if operationErr != nil {
		s.logger.Warn("dreamina credit claim failed", "account_id", id, "email", record.Email, "error_code", upstream.Code(operationErr), "error", operationErr, "elapsed_ms", time.Since(startedAt).Milliseconds())
		return nil, operationErr
	}
	credit, checkErr := s.client.Check(ctx, record.SessionID)
	if checkErr != nil {
		state := "error"
		if upstream.Code(checkErr) == upstream.InvalidCredentials {
			state = "invalid"
		}
		_ = s.repository.UpdateCheck(ctx, id, state, "", upstream.Code(checkErr), checkErr.Error())
		s.logger.Warn("dreamina credit balance refresh after claim failed", "account_id", id, "email", record.Email, "error_code", upstream.Code(checkErr), "error", checkErr)
		return nil, checkErr
	}
	if err := s.repository.UpdateCheck(ctx, id, "valid", credit.Balance, "", ""); err != nil {
		s.logger.Error("dreamina credit balance update after claim failed", "account_id", id, "email", record.Email, "error", err)
		return nil, err
	}
	s.logger.Info("dreamina credit balance refreshed after claim", "account_id", id, "email", record.Email, "credit_balance", credit.Balance)
	s.logger.Info("dreamina credit claim succeeded", "account_id", id, "email", record.Email, "elapsed_ms", time.Since(startedAt).Milliseconds())
	return result, nil
}

func (s *Service) Assets(ctx context.Context, id string, offset, count int) (any, error) {
	record, err := s.repository.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if record.SessionID == "" {
		s.logger.Warn("dreamina assets skipped", "account_id", id, "email", record.Email, "reason", "missing_session_id", "offset", offset, "count", count)
		return nil, errors.New("该账号没有 session_id")
	}
	startedAt := time.Now()
	s.logger.Info("dreamina assets started", "account_id", id, "email", record.Email, "offset", offset, "count", count)
	result, err := s.client.Assets(ctx, record.SessionID, offset, count)
	if err != nil {
		s.logger.Warn("dreamina assets failed", "account_id", id, "email", record.Email, "offset", offset, "count", count, "error_code", upstream.Code(err), "error", err, "elapsed_ms", time.Since(startedAt).Milliseconds())
		return nil, err
	}
	s.logger.Info("dreamina assets succeeded", "account_id", id, "email", record.Email, "offset", offset, "count", count, "elapsed_ms", time.Since(startedAt).Milliseconds())
	return result, nil
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

func (s *Service) recordAttempt(ctx context.Context, record Record, operation string, started, completed int64, operationErr error) {
	id, _ := security.RandomID("da")
	attempt := Attempt{ID: id, AccountID: record.ID, TargetLabel: record.Email, Operation: operation, Status: "succeeded", StartedAt: started, CompletedAt: &completed}
	if operationErr != nil {
		attempt.Status, attempt.ErrorCode, attempt.ErrorMessage = "failed", upstream.Code(operationErr), operationErr.Error()
	}
	_ = s.repository.RecordAttempt(ctx, attempt)
}

func (s *Service) intSetting(ctx context.Context, key string, fallback int) int {
	values, err := s.settings.Values(ctx)
	if err == nil {
		if value, ok := values[key].(int); ok {
			return value
		}
	}
	return fallback
}

func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
func ErrorCode(err error) string {
	if errors.Is(err, ErrVersionConflict) {
		return "VERSION_CONFLICT"
	}
	if IsNotFound(err) {
		return "DREAMINA_NOT_FOUND"
	}
	return upstream.Code(err)
}
