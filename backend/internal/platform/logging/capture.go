package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type captureHandler struct {
	next   slog.Handler
	buffer *Buffer
	mu     *sync.Mutex
	fields string
	groups []string
}

// WithBuffer preserves the original terminal handler and its level filtering.
// Only the web representation is sanitized, including attributes bound by With.
func WithBuffer(logger *slog.Logger, buffer *Buffer) *slog.Logger {
	return slog.New(&captureHandler{next: logger.Handler(), buffer: buffer, mu: &sync.Mutex{}})
}

func (h *captureHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *captureHandler) Handle(ctx context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	err := h.next.Handle(ctx, record.Clone())
	var fields bytes.Buffer
	fields.WriteString(h.fields)
	record.Attrs(func(attr slog.Attr) bool {
		writeAttr(&fields, h.groups, h.sanitize(attr))
		return fields.Len() < maxRecordBytes
	})
	timestamp := record.Time
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	h.buffer.append(Entry{
		Time: timestamp.UnixMilli(), Level: strings.TrimSpace(levelLabel(record.Level)),
		Message: redactText(record.Message), Fields: strings.TrimSpace(fields.String()),
	})
	return err
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.next = h.next.WithAttrs(attrs)
	var fields bytes.Buffer
	fields.WriteString(h.fields)
	for _, attr := range attrs {
		if fields.Len() >= maxRecordBytes {
			break
		}
		writeAttr(&fields, h.groups, h.sanitize(attr))
	}
	next.fields = limitText(fields.String(), maxRecordBytes)
	return &next
}

func (h *captureHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := *h
	next.next = h.next.WithGroup(name)
	next.groups = append(append([]string(nil), h.groups...), name)
	return &next
}

func (h *captureHandler) sanitize(attr slog.Attr) slog.Attr {
	for _, group := range h.groups {
		if sensitiveKey(group) {
			return slog.String(attr.Key, "[REDACTED]")
		}
	}
	return sanitizedAttr(attr, 0)
}

func sanitizedAttr(attr slog.Attr, depth int) slog.Attr {
	if attr.Equal(slog.Attr{}) {
		return attr
	}
	attr.Value = attr.Value.Resolve()
	if sensitiveKey(attr.Key) {
		return slog.String(attr.Key, "[REDACTED]")
	}
	if depth >= 8 {
		return slog.String(attr.Key, "[truncated]")
	}
	switch attr.Value.Kind() {
	case slog.KindGroup:
		attrs := attr.Value.Group()
		safe := make([]slog.Attr, 0, len(attrs))
		for _, child := range attrs {
			safe = append(safe, sanitizedAttr(child, depth+1))
		}
		return slog.Attr{Key: attr.Key, Value: slog.GroupValue(safe...)}
	case slog.KindString:
		return slog.String(attr.Key, limitText(redactText(attr.Value.String()), maxRecordBytes))
	case slog.KindAny:
		value := attr.Value.Any()
		var text string
		if err, ok := value.(error); ok {
			text = err.Error()
		} else if encoded, err := json.Marshal(value); err == nil {
			text = string(encoded)
		} else {
			text = fmt.Sprint(value)
		}
		return slog.String(attr.Key, limitText(redactText(text), maxRecordBytes))
	default:
		return attr
	}
}
