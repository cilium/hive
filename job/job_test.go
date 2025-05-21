// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package job

import (
	"context"
	"log/slog"
	"runtime"
	"runtime/pprof"
	"strings"
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
		g1 = r.NewGroup(s, l)
		g2 = r.NewGroup(s, l)
	})
	h.Populate(hivetest.Logger(t))

	if r1.(*registry).groups[0] != g1 {
		t.Fail()
	}
	if r1.(*registry).groups[1] != g2 {
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
		g := r.NewGroup(s, l)
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
		g = r.NewGroup(s, l)
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

func TestModuleDecoratedGroup(t *testing.T) {
	opts := hive.DefaultOptions()
	opts.ModulePrivateProviders = cell.ModulePrivateProviders{
		func(r Registry, h cell.Health, modID cell.FullModuleID, l *slog.Logger, lc cell.Lifecycle) Group {
			g := r.NewGroup(h, lc,
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
