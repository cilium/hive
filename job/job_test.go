// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package job

import (
	"context"
	"log/slog"
	"runtime"
	"runtime/pprof"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

	if r1.(*registry).groups[0] != g1 {
		t.Fail()
	}
	if r1.(*registry).groups[1] != g2 {
		t.Fail()
	}
}

// This test asserts that jobs are queued, until the hive has been started
func TestGroup_JobQueue(t *testing.T) {
	t.Parallel()

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s)
		g.Add(
			OneShot("queued1", func(ctx context.Context, health cell.Health) error { return nil }),
			OneShot("queued2", func(ctx context.Context, health cell.Health) error { return nil }),
		)
		g.Add(
			OneShot("queued3", func(ctx context.Context, health cell.Health) error { return nil }),
			OneShot("queued4", func(ctx context.Context, health cell.Health) error { return nil }),
		)
		if len(g.(*group).queuedJobs) != 4 {
			t.Fatal()
		}
		l.Append(g)
	})

	h.Populate(hivetest.Logger(t))
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
		l.Append(g)
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
			g := r.NewGroup(h,
				WithLogger(l),
				WithPprofLabels(pprof.Labels("module", modID.String())))
			lc.Append(g)
			return g
		},
	}
	callCount := 0
	fn := func(g Group) {
		g.Add(OneShot("test", func(ctx context.Context, health cell.Health) error {
			callCount++
			return nil
		}))
	}
	h := hive.NewWithOptions(
		opts,
		cell.SimpleHealthCell,
		Cell,
		cell.Module("test", "test module",
			cell.Invoke(fn),
			cell.Module("nested", "nested module",
				cell.Invoke(fn),
			),
		),
	)

	log := slog.Default()
	assert.NoError(t, h.Start(log, context.Background()))
	assert.NoError(t, h.Stop(log, context.Background()))
	assert.Equal(t, 2, callCount, "expected OneShot function to be called twice")
}

func TestOneShot_ValidateName(t *testing.T) {
	testCases := []struct {
		name        string
		jbName      string
		expectError bool
	}{
		{
			name:        "valid",
			jbName:      "valid_name",
			expectError: false,
		},
		{
			name:        "invalid name",
			jbName:      "$%^&",
			expectError: true,
		},
		{
			name:        "allow upper",
			jbName:      "AABB00",
			expectError: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateName(tc.jbName)
			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
