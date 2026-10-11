package token

import (
	"context"
	"errors"
	"testing"
)

type credentialReaderStub struct {
	record Record
	err    error
}

func (s credentialReaderStub) Get(context.Context, string) (Record, error) { return s.record, s.err }

type contentGatewayStub struct {
	ContentGateway
	credential string
	calls      int
}

func (s *contentGatewayStub) StorageUsage(_ context.Context, credential string) (StorageUsage, error) {
	s.credential = credential
	s.calls++
	return StorageUsage{UsedBytes: 5}, nil
}

func TestContentServiceOwnsCredentialResolution(t *testing.T) {
	gateway := &contentGatewayStub{}
	service := NewContentService(credentialReaderStub{record: Record{AccessToken: "private-access"}}, gateway)
	result, err := service.Storage(context.Background(), "account")
	if err != nil || result.UsedBytes != 5 || gateway.credential != "private-access" || gateway.calls != 1 {
		t.Fatalf("content use case failed: %+v %v", result, err)
	}
	service = NewContentService(credentialReaderStub{record: Record{}}, gateway)
	if _, err := service.Storage(context.Background(), "empty"); !errors.Is(err, ErrMissingAccessToken) {
		t.Fatalf("missing credential: %v", err)
	}
	if gateway.calls != 1 {
		t.Fatal("provider called without credentials")
	}
}

func TestContentServiceValidatesDeletionBeforeProvider(t *testing.T) {
	// Only StorageUsage is implemented; reaching another provider method is a bug.
	gateway := &contentGatewayStub{}
	service := NewContentService(credentialReaderStub{record: Record{AccessToken: "access"}}, gateway)
	if _, err := service.DeleteFiles(context.Background(), "account", nil); err == nil {
		t.Fatal("accepted empty deletion")
	}
	if _, err := service.Conversations(context.Background(), "account", -1, 30); err == nil {
		t.Fatal("accepted negative offset")
	}
}
