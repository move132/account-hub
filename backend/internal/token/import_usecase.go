package token

import (
	"context"
	"io"

	"account-hub/internal/job"
	"account-hub/internal/platform/apperror"
)

type ImportJobCreator interface {
	Create(context.Context, string, string, string, string, string, string, []job.Target) (job.Job, bool, error)
}

type ImportReport struct {
	Rows       []map[string]any `json:"rows"`
	Total      int              `json:"total"`
	Valid      int              `json:"valid"`
	Invalid    int              `json:"invalid"`
	Duplicates int              `json:"duplicates"`
}

type ImportPlan struct {
	Report ImportReport
	rows   []importRow
}

type ImportSubmission struct {
	Job      *job.Job `json:"job"`
	Accepted int      `json:"accepted"`
	Skipped  int      `json:"skipped"`
}

type ImportUseCase struct {
	service *Service
	jobs    ImportJobCreator
}

func NewImportUseCase(service *Service, jobs ImportJobCreator) *ImportUseCase {
	return &ImportUseCase{service: service, jobs: jobs}
}

func (u *ImportUseCase) Preview(ctx context.Context, reader io.Reader) (ImportPlan, error) {
	rows, err := parseImport(reader)
	if err != nil {
		return ImportPlan{}, apperror.Wrap(apperror.Validation, err.Error(), err)
	}
	if err := u.service.markImportDuplicates(ctx, rows); err != nil {
		return ImportPlan{}, err
	}
	report := ImportReport{Rows: importPreview(rows), Total: len(rows)}
	for _, row := range rows {
		if len(row.Errors) > 0 {
			report.Invalid++
		} else if row.Duplicate {
			report.Duplicates++
		} else {
			report.Valid++
		}
	}
	return ImportPlan{Report: report, rows: rows}, nil
}

func (u *ImportUseCase) Submit(ctx context.Context, plan ImportPlan, actorID, route string) (ImportSubmission, error) {
	result := ImportSubmission{Accepted: plan.Report.Valid, Skipped: plan.Report.Duplicates}
	if plan.Report.Invalid > 0 {
		return result, apperror.New(apperror.Validation, "导入内容存在错误，请先修正")
	}
	if plan.Report.Valid == 0 {
		return result, nil
	}
	targets := make([]job.Target, 0, plan.Report.Valid)
	for _, row := range plan.rows {
		if !row.Duplicate && len(row.Errors) == 0 {
			targets = append(targets, job.Target{Label: row.Input.Name, Payload: row.Input})
		}
	}
	created, _, err := u.jobs.Create(ctx, "token", "import", actorID, route, "", job.RequestHash(plan.rows), targets)
	if err != nil {
		return ImportSubmission{}, err
	}
	result.Job = &created
	return result, nil
}
