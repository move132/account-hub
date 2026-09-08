package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestColorHandlerFormatsLevelAndAttrs(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(NewColorHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))

	logger.Info("started", "component", "test")
	logger.Warn("slow request", "elapsed_ms", 1200)
	logger.Error("failed", "error", context.Canceled)

	got := out.String()
	for _, want := range []string{
		ansiGreen + "INFO " + ansiReset,
		ansiYellow + "WARN " + ansiReset,
		ansiRed + "ERROR" + ansiReset,
		"component=test",
		"elapsed_ms=1200",
		`error="context canceled"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}
