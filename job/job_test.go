// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package job

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/cilium/hive"
	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
)

// Configure a generous timeout to prevent flakes when running in a noisy CI environment.
var (
	tick    = 10 * time.Millisecond
	timeout = 5 * time.Second
)

func TestMain(m *testing.M) {
	cleanup := func(exitCode int) {
		// Force garbage-collection to force finalizers to run and catch
		// missing Event.Done() calls.
		runtime.GC()
	}
	goleak.VerifyTestMain(m, goleak.Cleanup(cleanup))
}

func fixture(fn func(Registry, cell.Health, cell.Lifecycle)) *hive.Hive {
	return hive.New(
		cell.SimpleHealthCell,
		Cell,
		cell.Module("test", "test module", cell.Invoke(fn)),
	)
}

// This test asserts that the test registry hold on to references to its groups
func TestRegistry(t *testing.T) {
	t.Parallel()

	var (
		r1 Registry
		g1 Group
		g2 Group
	)

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		r1 = r
		g1 = r.NewGroup(s)
		g2 = r.NewGroup(s)
	})
	h.Populate(hivetest.Logger(t))

	if r1.(*registry).groups[0] != g1.(*group) {
		t.Fail()
	}
	if r1.(*registry).groups[1] != g2.(*group) {
		t.Fail()
	}
}

// This test asserts that jobs are added to lifecycle before the hive has been started
func TestGroup_JobPreStart(t *testing.T) {
	t.Parallel()

	var startCount atomic.Int32
	incFunc := func(ctx context.Context, health cell.Health) error {
		startCount.Add(1)
		return nil
	}

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s)
		g.Add(
			OneShot("queued1", incFunc),
			OneShot("queued2", incFunc),
		)
		g.Add(
			OneShot("queued3", incFunc),
			OneShot("queued4", incFunc),
		)
	})
	log := hivetest.Logger(t)
	ctx := context.TODO()
	require.NoError(t, h.Start(log, ctx))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(4), startCount.Load())
	}, timeout, tick)
	require.NoError(t, h.Stop(log, ctx))
}

// This test asserts that jobs can be added at runtime.
func TestGroup_JobRuntime(t *testing.T) {
	t.Parallel()

	var (
		g Group
		i atomic.Int32
	)

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g = r.NewGroup(s)
	})

	h.Start(slog.Default(), context.Background())

	done := make(chan struct{})
	g.Add(OneShot("runtime", func(ctx context.Context, health cell.Health) error {
		i.Add(1)
		close(done)
		return nil
	}))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(1), i.Load())
	}, timeout, tick)

	h.Stop(slog.Default(), context.Background())
}

func TestJobLifecycleOrderingAcrossGroups(t *testing.T) {
	t.Parallel()

	var (
		g1 Group
		g2 Group
	)

	staticStarts := []string{"g1-first", "g1-second", "g2-first", "g1-third"}
	expectStarts := append(append([]string{}, staticStarts...), "g2-dynamic", "g1-dynamic")
	expectStops := []string{"g1-third", "g2-first", "g1-second", "g1-first", "g1-dynamic", "g2-dynamic"}

	addJob := func(g Group, name string) {
		g.Add(&orderingJob{name: name})
	}

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g1 = r.NewGroup(s)
		g2 = r.NewGroup(s)

		addJob(g1, "g1-first")
		addJob(g1, "g1-second")
		addJob(g2, "g2-first")
		addJob(g1, "g1-third")
	})

	log, logs := newRecordingLogger()
	ctx := context.Background()
	require.NoError(t, h.Start(log, ctx))

	waitForDetails(t, logs, len(staticStarts))
	startDetails := logs.attrs("detail")
	assert.Equal(t, staticStarts, startDetails)

	// Add jobs dynamically after the lifecycle has already started.
	addJob(g2, "g2-dynamic")
	addJob(g1, "g1-dynamic")

	waitForDetails(t, logs, len(expectStarts))
	startDetails = logs.attrs("detail")
	assert.Equal(t, expectStarts, startDetails)
	logs.clear()

	require.NoError(t, h.Stop(log, ctx))

	waitForDetails(t, logs, len(expectStops))
	stopDetails := logs.attrs("detail")
	assert.Equal(t, expectStops, stopDetails)
}

func TestModuleDecoratedGroup(t *testing.T) {
	opts := hive.DefaultOptions()
	opts.ModulePrivateProviders = cell.ModulePrivateProviders{
		func(r Registry, h cell.Health, modID cell.FullModuleID, l *slog.Logger, lc cell.Lifecycle) Group {
			g := r.NewGroup(h,
				WithLogger(l),
				WithPprofLabels(pprof.Labels("module", modID.String())))
			return g
		},
	}
	var callCount atomic.Int32
	fn := func(g Group) {
		g.Add(OneShot("test", func(ctx context.Context, health cell.Health) error {
			callCount.Add(1)
			return nil
		}))
	}
	startOrder := []string{}
	type X uint32
	h := hive.NewWithOptions(
		opts,
		cell.SimpleHealthCell,
		Cell,
		cell.Module("test", "test module",
			cell.Provide(func(lc cell.Lifecycle) X {
				lc.Append(cell.Hook{
					OnStart: func(cell.HookContext) error {
						// Give the job time to start in case it'll start in the wrong
						// order.
						time.Sleep(20 * time.Millisecond)
						startOrder = append(startOrder, "ctor")
						return nil
					},
				})
				return 123
			}),
			cell.Invoke(fn),
			cell.Module("nested", "nested module",
				cell.Invoke(fn),
				cell.Invoke(func(x X, g Group) {
					g.Add(OneShot("useX", func(ctx context.Context, health cell.Health) error {
						startOrder = append(startOrder, "job")
						return nil
					}))
				}),
			),
		),
	)

	log := slog.Default()
	assert.NoError(t, h.Start(log, context.Background()))
	assert.NoError(t, h.Stop(log, context.Background()))
	assert.EqualValues(t, 2, callCount.Load(), "expected OneShot function to be called twice")

	// As jobs are appended to the lifecycle when group has not started, the
	// start hook of the 'X' constructor runs before the job.
	assert.Equal(t, []string{"ctor", "job"}, startOrder)
}

func Test_sanitizeName(t *testing.T) {
	testCases := []struct {
		name    string
		in, out string
	}{
		{
			name: "valid",
			in:   "valid_name-123ABC",
			out:  "valid_name-123ABC",
		},
		{
			name: "allow upper",
			in:   "AABB00",
			out:  "AABB00",
		},
		{
			name: "invalid name",
			in:   "X$%^&Y",
			out:  "XY-4af3ba2ac8",
		},
		{
			name: "unicode chars",
			in:   "smile😀",
			out:  "smile-aacf34c444",
		},
		{
			name: "long name",
			in:   strings.Repeat("a", maxNameLength*2),
			out:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-c2a908d98f",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			out := sanitizeName(tc.in)
			assert.Equal(t, tc.out, out, "name mismatch")
		})
	}
}

type orderingJob struct {
	name string
}

func (oj *orderingJob) start(ctx context.Context, _ cell.Health, _ options) {
	<-ctx.Done()
}

func (oj *orderingJob) info() string {
	return oj.name
}

type logRecord struct {
	msg   string
	attrs map[string]string
}

type logCollector struct {
	mu      sync.Mutex
	records []logRecord
}

func (lc *logCollector) add(rec logRecord) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.records = append(lc.records, rec)
}

func (lc *logCollector) clear() {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.records = nil
}

func (lc *logCollector) attrs(attr string) []string {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	var out []string
	for _, rec := range lc.records {
		value, ok := rec.attrs[attr]
		if !ok {
			continue
		}
		out = append(out, value)
	}
	return out
}

type recordingHandler struct {
	collector *logCollector
	attrs     []slog.Attr
}

func newRecordingLogger() (*slog.Logger, *logCollector) {
	collector := &logCollector{}
	return slog.New(&recordingHandler{collector: collector}), collector
}

func (rh *recordingHandler) Enabled(_ context.Context, lvl slog.Level) bool {
	return lvl >= slog.LevelInfo
}

func (rh *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	rec := logRecord{
		msg:   r.Message,
		attrs: make(map[string]string),
	}
	for _, attr := range rh.attrs {
		rec.attrs[attr.Key] = fmt.Sprint(attr.Value)
	}
	r.Attrs(func(a slog.Attr) bool {
		rec.attrs[a.Key] = fmt.Sprint(a.Value)
		return true
	})
	rh.collector.add(rec)
	return nil
}

func (rh *recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	nrh := &recordingHandler{
		collector: rh.collector,
		attrs:     append(append([]slog.Attr{}, rh.attrs...), attrs...),
	}
	return nrh
}

func (rh *recordingHandler) WithGroup(string) slog.Handler {
	return &recordingHandler{
		collector: rh.collector,
		attrs:     append([]slog.Attr{}, rh.attrs...),
	}
}

func waitForDetails(t *testing.T, logs *logCollector, expectedLen int) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(logs.attrs("detail")) >= expectedLen {
			return
		}
		time.Sleep(tick)
	}
	t.Fatalf("timeout waiting for logs (expected %d); have %v", expectedLen, logs.attrs("detail"))
}
