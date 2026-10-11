package token

import (
	"context"
	"errors"
	"strings"

	"account-hub/internal/platform/apperror"
)

var ErrMissingAccessToken = apperror.New(apperror.Validation, "该账号没有 Access Token")

// ContentGateway is the provider boundary for account content. The HTTP handler
// never retrieves or handles the credentials required by these operations.
type ContentGateway interface {
	StorageUsage(context.Context, string) (StorageUsage, error)
	LibraryFiles(context.Context, string, string) (LibraryPage, error)
	DeleteLibraryFiles(context.Context, string, []DeleteLibraryFile) (LibraryDeleteResult, error)
	Conversations(context.Context, string, int, int) (ConversationPage, error)
	Conversation(context.Context, string, string) (ConversationDetail, error)
	ContentImage(context.Context, string, string) (ContentImage, error)
}

type CredentialReader interface {
	Get(context.Context, string) (Record, error)
}

type ContentService struct {
	accounts CredentialReader
	gateway  ContentGateway
}

func NewContentService(accounts CredentialReader, gateway ContentGateway) *ContentService {
	return &ContentService{accounts: accounts, gateway: gateway}
}

func (s *ContentService) credential(ctx context.Context, id string) (string, error) {
	record, err := s.accounts.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(record.AccessToken) == "" {
		return "", ErrMissingAccessToken
	}
	return record.AccessToken, nil
}

func (s *ContentService) Storage(ctx context.Context, id string) (StorageUsage, error) {
	credential, err := s.credential(ctx, id)
	if err != nil {
		return StorageUsage{}, err
	}
	return s.gateway.StorageUsage(ctx, credential)
}

func (s *ContentService) Files(ctx context.Context, id, cursor string) (LibraryPage, error) {
	cursor = strings.TrimSpace(cursor)
	if len(cursor) > 8192 {
		return LibraryPage{}, apperror.New(apperror.Validation, "分页游标过长")
	}
	credential, err := s.credential(ctx, id)
	if err != nil {
		return LibraryPage{}, err
	}
	return s.gateway.LibraryFiles(ctx, credential, cursor)
}

func (s *ContentService) DeleteFiles(ctx context.Context, id string, files []DeleteLibraryFile) (LibraryDeleteResult, error) {
	if err := validateLibraryDeletion(files); err != nil {
		return LibraryDeleteResult{}, apperror.Wrap(apperror.Validation, err.Error(), err)
	}
	credential, err := s.credential(ctx, id)
	if err != nil {
		return LibraryDeleteResult{}, err
	}
	return s.gateway.DeleteLibraryFiles(ctx, credential, files)
}

func (s *ContentService) Conversations(ctx context.Context, id string, offset, limit int) (ConversationPage, error) {
	if offset < 0 || offset > 1000000 || limit < 1 || limit > 100 {
		return ConversationPage{}, apperror.New(apperror.Validation, "分页参数超出允许范围")
	}
	credential, err := s.credential(ctx, id)
	if err != nil {
		return ConversationPage{}, err
	}
	return s.gateway.Conversations(ctx, credential, offset, limit)
}

func (s *ContentService) Conversation(ctx context.Context, id, conversationID string) (ConversationDetail, error) {
	if !conversationIDPattern.MatchString(conversationID) {
		return ConversationDetail{}, apperror.New(apperror.Validation, "会话 ID 格式错误")
	}
	credential, err := s.credential(ctx, id)
	if err != nil {
		return ConversationDetail{}, err
	}
	return s.gateway.Conversation(ctx, credential, conversationID)
}

func (s *ContentService) Image(ctx context.Context, id, fileID string) (ContentImage, error) {
	if !fileIDPattern.MatchString(fileID) || len(fileID) > 256 {
		return ContentImage{}, apperror.New(apperror.Validation, "文件 ID 格式错误")
	}
	credential, err := s.credential(ctx, id)
	if err != nil {
		return ContentImage{}, err
	}
	return s.gateway.ContentImage(ctx, credential, fileID)
}

func isMissingAccessToken(err error) bool { return errors.Is(err, ErrMissingAccessToken) }
