package hivetest

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestLoggerLateWrites(t *testing.T) {
	var late bytes.Buffer
	var log *slog.Logger
	t.Run("sub", func(t *testing.T) {
		// Registered before the logger, hence runs after it is finished.
		t.Cleanup(func() { log.Info("in early cleanup") })

		log = newLogger(t, &late)
		t.Cleanup(func() { log.Info("in late cleanup") })

		log.Info("during test")
	})
	log.Info("after test")

	lines := strings.Split(strings.TrimSuffix(late.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 late lines, got %q", lines)
	}
	for i, msg := range []string{`"in early cleanup"`, `"after test"`} {
		prefix := "hivetest: log after " + t.Name() + "/sub completed: "
		if !strings.HasPrefix(lines[i], prefix) || !strings.Contains(lines[i], "msg="+msg) {
			t.Errorf("line %d = %q, want prefix %q and msg=%s", i, lines[i], prefix, msg)
		}
	}
}
