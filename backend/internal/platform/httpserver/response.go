package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

type Response struct {
	Code    int    `json:"code"`
	Success bool   `json:"success"`
	Data    any    `json:"data"`
	Msg     string `json:"msg"`
	TS      int64  `json:"ts"`
}

func Write(w http.ResponseWriter, status int, data any, message string) {
	if data == nil {
		data = map[string]any{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Response{
		Code: status, Success: status < 400, Data: data, Msg: message, TS: time.Now().UTC().UnixMilli(),
	})
}

func Error(w http.ResponseWriter, r *http.Request, status int, code, message string, details any) {
	if details == nil {
		details = map[string]any{}
	}
	Write(w, status, map[string]any{
		"error_code": code,
		"request_id": RequestID(r.Context()),
		"details":    details,
	}, message)
}

func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			Error(w, r, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "请求内容过大", nil)
		} else {
			Error(w, r, http.StatusBadRequest, "INVALID_JSON", "请求格式错误", map[string]any{"reason": err.Error()})
		}
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		Error(w, r, http.StatusBadRequest, "INVALID_JSON", "请求只能包含一个 JSON 对象", nil)
		return false
	}
	return true
}
