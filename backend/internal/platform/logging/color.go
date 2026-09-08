package logging

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ansiReset  = "\x1b[0m"
	ansiCyan   = "\x1b[36m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiRed    = "\x1b[31m"
)

type ColorHandler struct {
	writer io.Writer
	level  slog.Leveler
	attrs  []slog.Attr
	groups []string
	mu     *sync.Mutex
}

func NewColorHandler(writer io.Writer, opts *slog.HandlerOptions) *ColorHandler {
	level := slog.Leveler(slog.LevelInfo)
	if opts != nil && opts.Level != nil {
		level = opts.Level
	}
	return &ColorHandler{writer: writer, level: level, mu: &sync.Mutex{}}
}

func (h *ColorHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *ColorHandler) Handle(_ context.Context, record slog.Record) error {
	var line bytes.Buffer
	timestamp := record.Time
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	line.WriteString(timestamp.Format(time.RFC3339))
	line.WriteByte(' ')
	line.WriteString(colorize(levelLabel(record.Level), levelColor(record.Level)))
	line.WriteByte(' ')
	line.WriteString(colorize(record.Message, messageColor(record.Level)))
	for _, attr := range h.attrs {
		writeAttr(&line, h.groups, attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		writeAttr(&line, h.groups, attr)
		return true
	})
	line.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.writer.Write(line.Bytes())
	return err
}

func (h *ColorHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := h.clone()
	next.attrs = append(next.attrs, attrs...)
	return next
}

func (h *ColorHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := h.clone()
	next.groups = append(next.groups, name)
	return next
}

func (h *ColorHandler) clone() *ColorHandler {
	next := *h
	next.attrs = append([]slog.Attr(nil), h.attrs...)
	next.groups = append([]string(nil), h.groups...)
	return &next
}

func writeAttr(line *bytes.Buffer, groups []string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}
	if attr.Value.Kind() == slog.KindGroup {
		children := attr.Value.Group()
		if len(children) == 0 {
			return
		}
		nextGroups := groups
		if attr.Key != "" {
			nextGroups = append(append([]string(nil), groups...), attr.Key)
		}
		for _, child := range children {
			writeAttr(line, nextGroups, child)
		}
		return
	}
	key := attr.Key
	if len(groups) > 0 {
		key = strings.Join(append(append([]string(nil), groups...), key), ".")
	}
	line.WriteByte(' ')
	line.WriteString(key)
	line.WriteByte('=')
	line.WriteString(formatValue(attr.Value))
}

func formatValue(value slog.Value) string {
	switch value.Kind() {
	case slog.KindString:
		return quoteIfNeeded(value.String())
	case slog.KindTime:
		return value.Time().Format(time.RFC3339)
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindAny:
		if err, ok := value.Any().(error); ok {
			return quoteIfNeeded(err.Error())
		}
		return quoteIfNeeded(fmt.Sprint(value.Any()))
	default:
		return quoteIfNeeded(value.String())
	}
}

func quoteIfNeeded(value string) string {
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\r\n\"=") {
		return strconv.Quote(value)
	}
	return value
}

func levelLabel(level slog.Level) string {
	switch {
	case level <= slog.LevelDebug:
		return "DEBUG"
	case level < slog.LevelWarn:
		return "INFO "
	case level < slog.LevelError:
		return "WARN "
	default:
		return "ERROR"
	}
}

func levelColor(level slog.Level) string {
	switch {
	case level <= slog.LevelDebug:
		return ansiCyan
	case level < slog.LevelWarn:
		return ansiGreen
	case level < slog.LevelError:
		return ansiYellow
	default:
		return ansiRed
	}
}

func messageColor(level slog.Level) string {
	if level >= slog.LevelError {
		return ansiRed
	}
	if level >= slog.LevelWarn {
		return ansiYellow
	}
	return ""
}

func colorize(value, color string) string {
	if color == "" {
		return value
	}
	return color + value + ansiReset
}
