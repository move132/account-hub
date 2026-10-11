package token

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"account-hub/internal/job"
	"account-hub/internal/platform/apperror"
)

type importJobsStub struct {
	calls   int
	targets []job.Target
}

func (s *importJobsStub) Create(_ context.Context, kind, action, actor, route, key, hash string, targets []job.Target) (job.Job, bool, error) {
	s.calls++
	s.targets = targets
	return job.Job{ID: "job-import", TargetKind: kind, Action: action, Total: len(targets)}, false, nil
}
func TestImportUseCaseOwnsPreviewAndSubmissionPolicy(t *testing.T) {
	_, _, service := operationFixture(t, controlledClient{})
	if _, err := service.Create(context.Background(), CreateInput{AccessToken: "existing-access"}); err != nil {
		t.Fatal(err)
	}
	jobs := &importJobsStub{}
	usecase := NewImportUseCase(service, jobs)
	plan, err := usecase.Preview(context.Background(), strings.NewReader("existing-access\nnew-access\nnew-access\n"))
	if err != nil || plan.Report.Total != 3 || plan.Report.Valid != 1 || plan.Report.Duplicates != 2 {
		t.Fatalf("preview=%+v err=%v", plan.Report, err)
	}
	encoded, err := json.Marshal(plan.Report)
	if err != nil || strings.Contains(string(encoded), "new-access") {
		t.Fatalf("unsafe preview: %s err=%v", encoded, err)
	}
	result, err := usecase.Submit(context.Background(), plan, "admin", "/imports")
	if err != nil || result.Accepted != 1 || result.Skipped != 2 || result.Job == nil || jobs.calls != 1 || len(jobs.targets) != 1 {
		t.Fatalf("submission=%+v targets=%+v err=%v", result, jobs.targets, err)
	}
	invalid, err := usecase.Preview(context.Background(), strings.NewReader("name,,session"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := usecase.Submit(context.Background(), invalid, "admin", "/imports"); apperror.KindOf(err) != apperror.Validation || jobs.calls != 1 {
		t.Fatalf("invalid import reached queue: %v calls=%d", err, jobs.calls)
	}
}

func TestHTTPMapsLocalStorageAndConflictErrors(t *testing.T) {
	for _, test := range []struct {
		err  error
		want int
	}{
		{ErrVersionConflict, http.StatusConflict},
		{apperror.New(apperror.Validation, "invalid field"), http.StatusBadRequest},
		{apperror.Wrap(apperror.Internal, "write failed", apperror.New(apperror.Internal, "private-database-detail")), http.StatusInternalServerError},
	} {
		request := httptest.NewRequest(http.MethodPost, "/refresh", nil)
		response := httptest.NewRecorder()
		handleUpstreamError(response, request, test.err, "操作失败")
		if response.Code != test.want || strings.Contains(response.Body.String(), "private-database-detail") {
			t.Fatalf("error status=%d body=%s", response.Code, response.Body.String())
		}
	}
}
