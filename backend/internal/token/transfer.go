package token

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"account-hub/internal/platform/security"
)

const maxImportRows = 10000

type importRow struct {
	Line            int         `json:"line"`
	Input           CreateInput `json:"input"`
	Errors          []string    `json:"errors"`
	Duplicate       bool        `json:"duplicate"`
	DuplicateReason string      `json:"duplicate_reason"`
}

func readImport(r *http.Request) ([]importRow, error) {
	file, _, err := r.FormFile("file")
	if err == nil {
		defer file.Close()
		return parseImport(file)
	}
	if !errors.Is(err, http.ErrMissingFile) {
		return nil, errors.New("无法读取上传文件")
	}
	return parseImport(strings.NewReader(r.FormValue("content")))
}

// parseImport follows sora2-video's Token import formats:
//   - one Access Token per line
//   - name,access_token,session_token,note(optional)
func parseImport(reader io.Reader) ([]importRow, error) {
	buffered := bufio.NewReader(reader)
	if prefix, _ := buffered.Peek(3); string(prefix) == "\ufeff" {
		_, _ = buffered.Discard(3)
	}
	csvReader := csv.NewReader(buffered)
	csvReader.FieldsPerRecord = -1
	csvReader.TrimLeadingSpace = true
	rows := make([]importRow, 0)
	seen := make(map[string]int)
	for {
		values, err := csvReader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var parseError *csv.ParseError
			if errors.As(err, &parseError) {
				return nil, fmt.Errorf("第 %d 行无法解析，请检查逗号和引号", parseError.Line)
			}
			return nil, errors.New("无法读取导入内容")
		}
		line, _ := csvReader.FieldPos(0)
		if len(values) == 1 && strings.TrimSpace(values[0]) == "" {
			continue
		}
		valueAt := func(index int) string {
			if index >= len(values) {
				return ""
			}
			return strings.TrimSpace(values[index])
		}
		input := CreateInput{Status: "enabled", Note: "批量导入"}
		if len(values) == 1 {
			input.Name = fmt.Sprintf("Token_%d", line)
			input.AccessToken = valueAt(0)
		} else {
			input.Name = valueAt(0)
			if input.Name == "" {
				input.Name = fmt.Sprintf("Token_%d", line)
			}
			input.AccessToken = valueAt(1)
			input.SessionToken = valueAt(2)
			if note := valueAt(3); note != "" {
				input.Note = note
			}
		}
		input.AccessToken = strings.TrimSpace(strings.TrimPrefix(input.AccessToken, "Bearer "))
		row := importRow{Line: line, Input: input, Errors: []string{}}
		if len(values) > 4 {
			row.Errors = append(row.Errors, "每行格式：名称,Access Token,Session Token,描述(可选)；字段包含逗号时请用双引号包裹")
		}
		if input.AccessToken == "" {
			row.Errors = append(row.Errors, "Access Token 不能为空")
		}
		if err := validateLengths(input.Name, "", input.AccessToken, input.SessionToken, input.Note); err != nil {
			row.Errors = append(row.Errors, err.Error())
		}
		if len(row.Errors) == 0 {
			if first, found := seen[input.AccessToken]; found {
				row.Duplicate = true
				row.DuplicateReason = fmt.Sprintf("与第 %d 行 Access Token 重复", first)
			} else {
				seen[input.AccessToken] = line
			}
		}
		rows = append(rows, row)
		if len(rows) > maxImportRows {
			return nil, fmt.Errorf("每次最多允许导入 %d 行", maxImportRows)
		}
	}
	if len(rows) == 0 {
		return nil, errors.New("请粘贴 Access Token 或上传包含 Token 的 TXT/CSV 文件")
	}
	return rows, nil
}

func (s *Service) markImportDuplicates(ctx context.Context, rows []importRow) error {
	tokens := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row.Errors) == 0 && !row.Duplicate {
			tokens = append(tokens, row.Input.AccessToken)
		}
	}
	existing, err := s.repository.ExistingAccessTokens(ctx, tokens)
	if err != nil {
		return err
	}
	for index := range rows {
		if _, found := existing[rows[index].Input.AccessToken]; found && !rows[index].Duplicate {
			rows[index].Duplicate = true
			rows[index].DuplicateReason = "Access Token 已存在"
		}
	}
	return nil
}

func importPreview(rows []importRow) []map[string]any {
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		status := "new"
		if len(row.Errors) > 0 {
			status = "invalid"
		} else if row.Duplicate {
			status = "duplicate"
		}
		result = append(result, map[string]any{
			"line": row.Line, "name": row.Input.Name, "note": row.Input.Note,
			"access_token_masked":  security.MaskSecret(row.Input.AccessToken),
			"session_token_masked": security.MaskSecret(row.Input.SessionToken),
			"result_status":        status, "errors": row.Errors, "duplicate_reason": row.DuplicateReason,
		})
	}
	return result
}

// Import also handles an Access Token inserted after preview or by another
// queued import.
func (s *Service) Import(ctx context.Context, input CreateInput) error {
	_, err := s.Create(ctx, input)
	if err == nil || !IsUniqueError(err) || strings.TrimSpace(input.AccessToken) == "" {
		return err
	}
	existing, lookupErr := s.repository.ExistingAccessTokens(ctx, []string{strings.TrimSpace(strings.TrimPrefix(input.AccessToken, "Bearer "))})
	if lookupErr != nil {
		return lookupErr
	}
	if len(existing) > 0 {
		return nil
	}
	return err
}
