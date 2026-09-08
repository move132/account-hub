package dreamina

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

func parseImport(reader io.Reader) ([]importRow, error) {
	buffered := bufio.NewReader(reader)
	if prefix, _ := buffered.Peek(3); string(prefix) == "\ufeff" {
		_, _ = buffered.Discard(3)
	}
	csvReader := csv.NewReader(buffered)
	csvReader.FieldsPerRecord = -1
	csvReader.TrimLeadingSpace = true
	rows, seen := make([]importRow, 0), map[string]int{}
	var indexes map[string]int
	firstRecord := true
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
		if firstRecord {
			firstRecord = false
			header := make(map[string]int, len(values))
			for index, column := range values {
				header[strings.ToLower(strings.TrimSpace(column))] = index
			}
			if _, found := header["email"]; found {
				for _, column := range []string{"email", "password", "session_id"} {
					if _, found := header[column]; !found {
						return nil, fmt.Errorf("CSV 缺少列: %s", column)
					}
				}
				indexes = header
				continue
			}
		}
		valueAt := func(index int) string {
			if index >= len(values) {
				return ""
			}
			return strings.TrimSpace(values[index])
		}
		input := CreateInput{}
		if indexes == nil {
			input.Email, input.Password, input.SessionID, input.Note = valueAt(0), valueAt(1), valueAt(2), valueAt(3)
		} else {
			value := func(column string) string {
				if index, found := indexes[column]; found {
					return valueAt(index)
				}
				return ""
			}
			input = CreateInput{Email: value("email"), Password: value("password"), SessionID: value("session_id"), SessionExpiresAt: value("session_expires_at"), Status: value("status"), Note: value("note")}
		}
		input.Email = strings.ToLower(input.Email)
		if input.Status == "" {
			input.Status = "enabled"
		}
		if input.Note == "" {
			input.Note = "批量导入"
		}
		row := importRow{Line: line, Input: input, Errors: []string{}}
		if indexes == nil && (len(values) < 3 || len(values) > 4) {
			row.Errors = append(row.Errors, "每行格式：账户邮箱,账户密码,Session ID,描述(可选)；字段包含逗号时请用双引号包裹")
		} else if indexes != nil && len(values) > len(indexes) {
			row.Errors = append(row.Errors, "字段数量超出 CSV 表头")
		}
		if input.Email == "" || input.Password == "" {
			row.Errors = append(row.Errors, "账户邮箱和账户密码不能为空")
		}
		if indexes == nil && input.SessionID == "" {
			row.Errors = append(row.Errors, "Session ID 不能为空")
		}
		if input.Status != "enabled" && input.Status != "disabled" {
			row.Errors = append(row.Errors, "status 只能是 enabled 或 disabled")
		}
		if err := validateLengths(input.Email, input.Password, input.SessionID, input.Note); err != nil {
			row.Errors = append(row.Errors, err.Error())
		}
		if len(row.Errors) == 0 && input.SessionID != "" {
			if first, exists := seen[input.SessionID]; exists {
				row.Duplicate = true
				row.DuplicateReason = fmt.Sprintf("与第 %d 行 Session ID 重复", first)
			} else {
				seen[input.SessionID] = line
			}
		}
		rows = append(rows, row)
		if len(rows) > 10000 {
			return nil, errors.New("每次最多允许导入 10000 行")
		}
	}
	if len(rows) == 0 {
		return nil, errors.New("请粘贴 Session 内容或上传包含 Session 的 TXT/CSV 文件")
	}
	return rows, nil
}

func (s *Service) markImportDuplicates(ctx context.Context, rows []importRow) error {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row.Errors) == 0 && !row.Duplicate && row.Input.SessionID != "" {
			ids = append(ids, row.Input.SessionID)
		}
	}
	existing, err := s.repository.ExistingSessionIDs(ctx, ids)
	if err != nil {
		return err
	}
	for index := range rows {
		if _, found := existing[rows[index].Input.SessionID]; found && !rows[index].Duplicate {
			rows[index].Duplicate = true
			rows[index].DuplicateReason = "Session ID 已存在"
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
			"line": row.Line, "email": row.Input.Email, "status": row.Input.Status, "note": row.Input.Note,
			"password_masked": security.MaskSecret(row.Input.Password), "session_id_masked": security.MaskSecret(row.Input.SessionID),
			"result_status": status, "errors": row.Errors, "duplicate_reason": row.DuplicateReason,
		})
	}
	return result
}

// Import also handles a Session inserted after preview or by another queued import.
func (s *Service) Import(ctx context.Context, input CreateInput) error {
	_, err := s.Create(ctx, input)
	if err == nil || !IsUniqueError(err) || strings.TrimSpace(input.SessionID) == "" {
		return err
	}
	existing, lookupErr := s.repository.ExistingSessionIDs(ctx, []string{strings.TrimSpace(input.SessionID)})
	if lookupErr != nil {
		return lookupErr
	}
	if len(existing) > 0 {
		return nil
	}
	return err
}
