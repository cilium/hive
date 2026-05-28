// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package job

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
)

func TestJobLifecycleInsertAndRemove(t *testing.T) {
	t.Parallel()

	r := &registry{logger: lifecycleTestLogger()}
	first := newBlockingLifecycleJob(t, r, "first", nil)
	second := newBlockingLifecycleJob(t, r, "second", nil)
	third := newBlockingLifecycleJob(t, r, "third", nil)
	defer stopLifecycleJobs(t, first, second, third)

	insertRuntimeLifecycleJobs(r, first, second, third)
	waitForLifecycleJobs(t, first, second, third)

	requireRuntimeLifecycleList(t, r, third, second, first)

	r.runtimeLifecycle.remove(second)
	requireRuntimeLifecycleList(t, r, third, first)
	requireRuntimeLifecycleJobUnlinked(t, r, second)

	r.runtimeLifecycle.remove(third)
	requireRuntimeLifecycleList(t, r, first)
	requireRuntimeLifecycleJobUnlinked(t, r, third)

	r.runtimeLifecycle.remove(first)
	requireRuntimeLifecycleList(t, r)
	requireRuntimeLifecycleJobUnlinked(t, r, first)
}

func TestJobLifecycleRemovesCompletedJobs(t *testing.T) {
	t.Parallel()

	r := &registry{logger: lifecycleTestLogger()}
	qj := &queuedJob{
		registry: r,
		job:      &completingLifecycleJob{started: make(chan struct{})},
	}

	insertRuntimeLifecycleJobs(r, qj)
	<-qj.job.(*completingLifecycleJob).started

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		r.runtimeLifecycle.mu.Lock()
		defer r.runtimeLifecycle.mu.Unlock()

		assert.Nil(c, r.runtimeLifecycle.jobs)
		assert.Nil(c, qj.prev)
		assert.Nil(c, qj.next)
	}, timeout, tick)
}

func TestJobLifecycleStopStopsJobsInReverseStartOrder(t *testing.T) {
	t.Parallel()

	var (
		mu      sync.Mutex
		stopped []string
	)
	recordStop := func(name string) {
		mu.Lock()
		defer mu.Unlock()
		stopped = append(stopped, name)
	}

	r := &registry{logger: lifecycleTestLogger()}
	first := newBlockingLifecycleJob(t, r, "first", recordStop)
	second := newBlockingLifecycleJob(t, r, "second", recordStop)
	third := newBlockingLifecycleJob(t, r, "third", recordStop)

	insertRuntimeLifecycleJobs(r, first, second, third)
	waitForLifecycleJobs(t, first, second, third)

	require.NoError(t, r.runtimeLifecycle.stop(context.Background(), hivetest.Logger(t)))

	requireRuntimeLifecycleList(t, r)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"third", "second", "first"}, stopped)
}

func TestJobLifecycleStopReturnsContextError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var lifecycle jobLifecycle
	assert.ErrorIs(t, lifecycle.stop(ctx, hivetest.Logger(t)), context.Canceled)
}

func TestJobLifecycleJobNotStartedAfterStop(t *testing.T) {
	t.Parallel()

	// Create a stopped registry
	r := &registry{logger: lifecycleTestLogger()}
	require.NoError(t, r.Start(context.Background()))
	require.NoError(t, r.Stop(context.Background()))

	// Adding jobs now won't start them
	qj := &queuedJob{
		registry: r,
		job:      &completingLifecycleJob{started: make(chan struct{})},
	}
	r.runtimeLifecycle.insertAndStart(qj)

	// The job list should be empty
	require.Nil(t, r.runtimeLifecycle.jobs)

	// Wait 50ms and verify it did not start.
	select {
	case <-qj.job.(*completingLifecycleJob).started:
		t.Fatalf("job should not start after registry was stopped")
	case <-time.After(50 * time.Millisecond):
	}

}

type blockingLifecycleJob struct {
	name      string
	started   chan struct{}
	startOnce sync.Once
	onStop    func(string)
}

func newBlockingLifecycleJob(t *testing.T, r *registry, name string, onStop func(string)) *queuedJob {
	t.Helper()

	return &queuedJob{
		registry: r,
		job: &blockingLifecycleJob{
			name:    name,
			started: make(chan struct{}),
			onStop:  onStop,
		},
	}
}

func lifecycleTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func (j *blockingLifecycleJob) start(ctx context.Context, _ cell.Health, _ options) {
	j.startOnce.Do(func() {
		close(j.started)
	})
	<-ctx.Done()
	if j.onStop != nil {
		j.onStop(j.name)
	}
}

func (j *blockingLifecycleJob) info() string {
	return j.name
}

type completingLifecycleJob struct {
	started chan struct{}
}

func (j *completingLifecycleJob) start(context.Context, cell.Health, options) {
	close(j.started)
}

func (j *completingLifecycleJob) info() string {
	return "completed"
}

func waitForLifecycleJobs(t *testing.T, jobs ...*queuedJob) {
	t.Helper()

	for _, qj := range jobs {
		<-qj.job.(*blockingLifecycleJob).started
	}
}

func stopLifecycleJobs(t *testing.T, jobs ...*queuedJob) {
	t.Helper()

	for _, qj := range jobs {
		if qj.cancel == nil {
			continue
		}
		require.NoError(t, qj.Stop(context.Background()))
	}
}

func insertRuntimeLifecycleJobs(r *registry, jobs ...*queuedJob) {
	for _, qj := range jobs {
		r.runtimeLifecycle.insertAndStart(qj)
	}
}

func requireRuntimeLifecycleList(t *testing.T, r *registry, want ...*queuedJob) {
	t.Helper()

	r.runtimeLifecycle.mu.Lock()
	defer r.runtimeLifecycle.mu.Unlock()

	requireLifecycleList(t, r.runtimeLifecycle.jobs, want...)
}

func requireRuntimeLifecycleJobUnlinked(t *testing.T, r *registry, qj *queuedJob) {
	t.Helper()

	r.runtimeLifecycle.mu.Lock()
	defer r.runtimeLifecycle.mu.Unlock()

	assert.Nil(t, qj.prev)
	assert.Nil(t, qj.next)
}

func requireLifecycleList(t *testing.T, head *queuedJob, want ...*queuedJob) {
	t.Helper()

	var got []*queuedJob
	for qj := head; qj != nil; qj = qj.next {
		got = append(got, qj)
	}

	require.Len(t, got, len(want))
	for i := range want {
		require.Same(t, want[i], got[i])
		if i == 0 {
			assert.Nil(t, got[i].prev)
		} else {
			assert.Same(t, got[i-1], got[i].prev)
		}
		if i == len(want)-1 {
			assert.Nil(t, got[i].next)
		} else {
			assert.Same(t, got[i+1], got[i].next)
		}
	}
}
