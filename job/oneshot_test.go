// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package job

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
)

// This test asserts that a OneShot jobs is started and completes. This test will timeout on failure
func TestOneShot_ShortRun(t *testing.T) {
	t.Parallel()

	stop := make(chan struct{})

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s)

		g.Add(
			OneShot("short", func(ctx context.Context, health cell.Health) error {
				defer close(stop)
				return nil
			}),
		)
	})

	log := hivetest.Logger(t)
	if assert.NoError(t, h.Start(log, context.Background())) {
		<-stop
		assert.NoError(t, h.Stop(log, context.Background()))
	}
}

// This test asserts that the context given to a one shot job cancels when the lifecycle of the group ends.
func TestOneShot_LongRun(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	stopped := make(chan struct{})

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s)

		g.Add(
			OneShot("long", func(ctx context.Context, health cell.Health) error {
				close(started)
				<-ctx.Done()
				defer close(stopped)
				return nil
			}),
		)
	})

	log := hivetest.Logger(t)
	if assert.NoError(t, h.Start(log, context.Background())) {
		<-started
		assert.NoError(t, h.Stop(log, context.Background()))
		<-stopped
	}
}

// This test asserts that we will stop retrying after the retry limit
func TestOneShot_RetryFail(t *testing.T) {
	t.Parallel()

	var (
		g Group
		i atomic.Int32
	)

	const retries = 3
	rateLimiter := &ExponentialBackoff{Min: 10 * time.Millisecond, Max: 20 * time.Millisecond}

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g = r.NewGroup(s)

		g.Add(
			OneShot("retry-fail", func(ctx context.Context, health cell.Health) error {
				defer func() { i.Add(1) }()
				return errors.New("Always error")
			}, WithRetry(retries, rateLimiter)),
		)
	})

	log := hivetest.Logger(t)
	if err := h.Start(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	assert.Eventually(t,
		func() bool {
			// 1 for the initial run, and 3 retries
			return i.Load() == retries+1
		},
		timeout, tick)
	assert.EqualValues(t, i.Load(), retries+1, "Retries = %d, Ran = %d", retries, i.Load())

	if err := h.Stop(log, context.Background()); err != nil {
		t.Fatal(err)
	}

}

// Run the actual test multiple times, as long as 1 out of 5 is good, we accept it, only fail if we are consistently
// broken. This is due to the time based nature of the test which is unreliable in certain CI environments.
func TestOneShot_RetryBackoff(t *testing.T) {
	t.Parallel()

	for i := 0; i < 5; i++ {
		failed, err := testOneShot_RetryBackoff(t)
		if err != nil {
			t.Fatal(err)
		}
		if !failed {
			return
		}
	}

	t.Fatal("0/5 retry backoff tests succeeded")
}

// This test asserts that the one shot jobs have a delay equal to the expected behavior of the passed in ratelimiter.
func testOneShot_RetryBackoff(t *testing.T) (bool, error) {
	var (
		g     Group
		times []time.Time
	)

	failed := false

	const (
		retries  = 6
		retryMin = 5 * time.Millisecond
		retryMax = retryMin * (1 << retries)
	)
	rateLimiter := &ExponentialBackoff{Min: retryMin, Max: retryMax}

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g = r.NewGroup(s)

		g.Add(
			OneShot("retry-backoff", func(ctx context.Context, health cell.Health) error {
				times = append(times, time.Now())
				return errors.New("Always error")
			}, WithRetry(retries, rateLimiter)),
		)
	})

	log := hivetest.Logger(t)
	if err := h.Start(log, context.Background()); err != nil {
		return true, err
	}

	require.Eventually(t, func() bool {
		return len(times) == retries+1
	}, timeout, tick)

	if err := h.Stop(log, context.Background()); err != nil {
		return true, err
	}

	var last time.Duration
	for i := 1; i < len(times); i++ {
		diff := times[i].Sub(times[i-1])
		if i > 2 {
			// Test that the rate of change is 2x (+- 50%, the 50% to account for CI time dilation).
			// The 10 factor is to add avoid integer rounding.
			fract := diff * 10 / last
			if fract < 15 || fract > 25 {
				// The retry backoff wait time was less than 1.5x or more than 2.5x than
				// the previous attempt.
				failed = true
			}
		}
		last = diff
	}

	return failed, nil
}

// This test asserts that we do not keep retrying after the job function has recovered
func TestOneShot_RetryRecover(t *testing.T) {
	t.Parallel()

	var (
		g Group
		i atomic.Int32
	)

	const retries = 3

	rateLimiter := &ExponentialBackoff{Min: 10 * time.Millisecond, Max: 20 * time.Millisecond}

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g = r.NewGroup(s)

		g.Add(
			OneShot("retry-recover", func(ctx context.Context, health cell.Health) error {
				defer func() { i.Add(1) }()
				if i.Load() == 0 {
					return errors.New("Sometimes error")
				}

				return nil
			}, WithRetry(retries, rateLimiter)),
		)
	})

	log := hivetest.Logger(t)
	if err := h.Start(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	assert.Eventually(t,
		func() bool {
			return i.Load() == 2
		},
		timeout, tick,
		"One shot was invoked after the recovery")

	if err := h.Stop(log, context.Background()); err != nil {
		t.Fatal(err)
	}

}

// This tests asserts that returning an error on a one shot job with the WithShutdown option will shutdown the hive.
func TestOneShot_Shutdown(t *testing.T) {
	t.Parallel()

	targetErr := errors.New("Always error")
	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s)

		g.Add(
			OneShot("shutdown", func(ctx context.Context, health cell.Health) error {
				return targetErr
			}, WithShutdown()),
		)
	})

	err := h.Run(hivetest.Logger(t))
	if !errors.Is(err, targetErr) {
		t.Fail()
	}
}

// This test asserts that when the retry and shutdown options are used, the hive is only shutdown after all retries
// failed
func TestOneShot_RetryFailShutdown(t *testing.T) {
	t.Parallel()

	var i int

	const retries = 3
	rateLimiter := &ExponentialBackoff{Min: 10 * time.Millisecond, Max: 20 * time.Millisecond}

	targetErr := errors.New("Always error")
	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s)

		g.Add(
			OneShot("retry-fail-shutdown", func(ctx context.Context, health cell.Health) error {
				defer func() { i++ }()
				return targetErr
			}, WithRetry(retries, rateLimiter), WithShutdown()),
		)
	})

	err := h.Run(hivetest.Logger(t))
	if !errors.Is(err, targetErr) {
		t.Fail()
	}

	if i != retries+1 {
		t.Fail()
	}
}

// This test asserts that when both the WithRetry and WithShutdown options are used, and the one shot function recovers
// that the hive does not shutdown.
func TestOneShot_RetryRecoverNoShutdown(t *testing.T) {
	t.Parallel()

	var (
		g Group
		i atomic.Int32
	)

	started := make(chan struct{})

	const retries = 5

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g = r.NewGroup(s)
	})

	log := hivetest.Logger(t)
	ctx := context.TODO()
	require.NoError(t, h.Start(log, ctx))

	// Add the job dynamically.
	jobDone := make(chan struct{})

	g.Add(
		OneShot("retry-recover-no-shutdown", func(ctx context.Context, health cell.Health) error {
			defer func() {
				if i.Add(1) == 2 {
					close(jobDone)
				}
			}()

			if i.Load() == 0 {
				close(started)
				return errors.New("First try error")
			}

			return nil
		}, WithRetry(retries, ConstantBackoff(time.Millisecond)), WithShutdown()),
	)

	shutdown := make(chan struct{})

	// Manually trigger a shutdown after the group has no more running jobs, will exit the hive with a nil
	go func() {
		<-started
		<-jobDone
		h.Shutdown()
		close(shutdown)
	}()

	<-shutdown
	require.Equal(t, int32(2), i.Load())
}

// This test asserts that when the WithRetry option is used, retries are not
// attempted after that the hive shutdown process has started.
func TestOneShot_RetryWhileShuttingDown(t *testing.T) {
	t.Parallel()

	var (
		g    Group
		runs atomic.Int32
	)

	const retries = 5
	rateLimiter := &ExponentialBackoff{Min: 10 * time.Millisecond, Max: 50 * time.Millisecond}
	started := make(chan struct{})
	shutdown := make(chan struct{})

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g = r.NewGroup(s)

		g.Add(
			OneShot("retry-context-closed", func(ctx context.Context, health cell.Health) error {
				if runs.Load() == 0 {
					close(started)
				}

				runs.Add(1)
				<-ctx.Done()
				return ctx.Err()
			}, WithRetry(retries, rateLimiter)),
		)
	})

	go func() {
		// Wait until the job function has started, and then immediately stop the hive
		<-started
		h.Shutdown()
		close(shutdown)
	}()

	assert.NoError(t, h.Run(hivetest.Logger(t)))
	assert.EqualValues(t, 1, runs.Load(), "The job function should not have been retried after that the context expired")

	<-shutdown
}
