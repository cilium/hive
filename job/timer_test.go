// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package job

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
)

// This test ensures that the timer function is called repeatedly.
// Not testing the timer interval is intentional, as there are no guarantees for test execution
// timeliness in the CI or even locally. This makes assertions of test timing inherently flaky,
// leading to a need of large tolerances that diminish value of such assertions.
func TestTimer_OnInterval(t *testing.T) {
	t.Parallel()

	stop := make(chan struct{})
	var i atomic.Int32

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s, l)

		g.Add(
			Timer("on-interval", func(ctx context.Context) error {
				// Close the stop channel after 5 invocations.
				i.Add(1)
				if i.Load() == 5 {
					close(stop)
				}
				return nil
			}, 100*time.Millisecond),
		)
	})

	log := hivetest.Logger(t)
	if err := h.Start(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	<-stop

	if err := h.Stop(log, context.Background()); err != nil {
		t.Fatal(err)
	}
}

// This test asserts that a timer will run when triggered, even when its interval has not yet expired
func TestTimer_Trigger(t *testing.T) {
	t.Parallel()

	ran := make(chan struct{})

	var i atomic.Int32

	trigger := NewTrigger()

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s, l)

		g.Add(
			Timer("on-interval", func(ctx context.Context) error {
				defer func() { ran <- struct{}{} }()

				i.Add(1)

				return nil
			}, 1*time.Hour, WithTrigger(trigger)),
		)
	})

	log := hivetest.Logger(t)
	if err := h.Start(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	trigger.Trigger()
	<-ran

	trigger.Trigger()
	<-ran

	trigger.Trigger()
	<-ran

	if err := h.Stop(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	if i.Load() != 3 {
		t.Fail()
	}
}

// This test asserts that, if a trigger is called multiple times before a job is finished, that the events will coalesce
func TestTimer_DoubleTrigger(t *testing.T) {
	t.Parallel()

	ran := make(chan struct{})

	var i atomic.Int32

	trigger := NewTrigger()

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s, l)

		g.Add(
			Timer("on-interval", func(ctx context.Context) error {
				defer func(iter int32) {
					if iter == 0 {
						close(ran)
					}
				}(i.Load())

				i.Add(1)

				return nil
			}, 1*time.Hour, WithTrigger(trigger)),
		)
	})

	// Trigger a few times before we start consuming events, these should coalesce
	trigger.Trigger()
	trigger.Trigger()
	trigger.Trigger()

	log := hivetest.Logger(t)
	if err := h.Start(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	<-ran

	if err := h.Stop(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	if i.Load() != 1 {
		t.Fail()
	}
}

// This test asserts that, if a trigger is called multiple times over the debounce interval, the events will coalesce
func TestTimer_TriggerDebounce(t *testing.T) {
	t.Parallel()

	ran := make(chan struct{})

	var i atomic.Int32

	trigger := NewTrigger(WithDebounce(time.Hour))

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s, l)

		g.Add(
			Timer("on-interval", func(ctx context.Context) error {
				defer func(iter int32) {
					if iter == 0 {
						close(ran)
					}
				}(i.Load())

				i.Add(1)

				return nil
			}, 1*time.Hour, WithTrigger(trigger)),
		)
	})

	// Trigger once before we start consuming events, to run the job immediately after start
	trigger.Trigger()

	log := hivetest.Logger(t)
	if err := h.Start(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	<-ran

	// Trigger a few times after the initial run, these should coalesce
	trigger.Trigger()
	trigger.Trigger()
	trigger.Trigger()

	if err := h.Stop(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	if i.Load() != 1 {
		t.Fail()
	}
}

// This test asserts that a timer with a zero interval will run its job only when triggered
func TestTimer_TriggerOnly(t *testing.T) {
	t.Parallel()

	ran := make(chan struct{})

	var i atomic.Int32

	trigger := NewTrigger()

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s, l)

		g.Add(
			Timer("on-interval", func(ctx context.Context) error {
				defer func() { ran <- struct{}{} }()

				i.Add(1)

				return nil
			}, 0, WithTrigger(trigger)),
		)
	})

	log := hivetest.Logger(t)
	if err := h.Start(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	trigger.Trigger()
	<-ran

	trigger.Trigger()
	<-ran

	trigger.Trigger()
	<-ran

	if err := h.Stop(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	if i.Load() != 3 {
		t.Fail()
	}
}

// This test asserts that the timer will stop as soon as the lifecycle has stopped, when waiting for an interval pulse
func TestTimer_ExitOnClose(t *testing.T) {
	t.Parallel()

	var i atomic.Int32
	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s, l)

		g.Add(
			Timer("on-interval", func(ctx context.Context) error {
				i.Add(1)
				return nil
			}, 1*time.Hour),
		)
	})

	log := hivetest.Logger(t)
	if err := h.Start(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := h.Stop(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	if i.Load() != 0 {
		t.Fail()
	}
}

// This test asserts that the context given to the timer closes when the lifecycle ends, and that the timer stops
// after the fn return.
func TestTimer_ExitOnCloseFnCtx(t *testing.T) {
	t.Parallel()

	var i atomic.Int32
	started := make(chan struct{})
	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s, l)

		g.Add(
			Timer("on-interval", func(ctx context.Context) error {
				i.Add(1)
				if started != nil {
					close(started)
				}
				<-ctx.Done()
				return nil
			}, 1*time.Millisecond),
		)
	})

	log := hivetest.Logger(t)
	if err := h.Start(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	<-started

	if err := h.Stop(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	if i.Load() != 1 {
		t.Fail()
	}
}
