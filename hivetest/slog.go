package hivetest

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
)

// Logger returns a logger which forwards structured logs to t.
//
// Logs emitted after t has completed are written to stderr instead, prefixed
// with the name of the test. These usually indicate a goroutine outliving its
// test. Note that this includes logs from cleanup functions registered before
// Logger was called, since cleanups run in last-in-first-out order.
func Logger(t testing.TB, opts ...LogOption) *slog.Logger {
	return newLogger(t, os.Stderr, opts...)
}

func newLogger(t testing.TB, late io.Writer, opts ...LogOption) *slog.Logger {
	o := logOptions{level: slog.LevelInfo}
	for _, opt := range opts {
		opt(&o)
	}

	w := &testWriter{name: t.Name(), w: t.Output(), late: late}
	// Cleanups run before the testing package marks t as done. Once this
	// cleanup has run, no write to t is in flight and none will follow.
	t.Cleanup(w.finish)

	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		AddSource: true,
		Level:     o.level,
	}))
}

type logOptions struct {
	level slog.Leveler
}

type LogOption func(*logOptions)

func LogLevel(l slog.Level) LogOption {
	return func(o *logOptions) {
		o.level = l
	}
}

// testWriter guards writes to a test's output against the test completing.
//
// Writing to t after it has completed panics. Merely checking for completion
// beforehand isn't sufficient: the testing package marks the test as done
// without synchronization (deliberately, so that the race detector flags
// unsynchronized goroutines), so a concurrent write races with that. Holding
// mu across the write and in finish orders all writes before the test
// completes. Afterwards, writes go to late instead.
type testWriter struct {
	mu   sync.Mutex
	done bool
	name string
	w    io.Writer
	late io.Writer
}

func (tw *testWriter) finish() {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	tw.done = true
}

// Write is called once per log record by slog.TextHandler, with p being a
// single newline-terminated line.
func (tw *testWriter) Write(p []byte) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.done {
		if _, err := fmt.Fprintf(tw.late, "hivetest: log after %s completed: %s", tw.name, p); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	return tw.w.Write(p)
}
