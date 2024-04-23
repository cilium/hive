// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package job

import (
	"context"
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
	i := 0

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s)

		g.Add(
			Timer("on-interval", func(ctx context.Context) error {
				// Close the stop channel after 5 invocations.
				i++
				if i == 5 {
					close(stop)
				}
				return nil
			}, 100*time.Millisecond),
		)

		l.Append(g)
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

	var i int

	trigger := NewTrigger()

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s)

		g.Add(
			Timer("on-interval", func(ctx context.Context) error {
				defer func() { ran <- struct{}{} }()

				i++

				return nil
			}, 1*time.Hour, WithTrigger(trigger)),
		)

		l.Append(g)
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

	if i != 3 {
		t.Fail()
	}
}

// This test asserts that, if a trigger is called multiple times before a job is finished, that the events will coalesce
func TestTimer_DoubleTrigger(t *testing.T) {
	t.Parallel()

	ran := make(chan struct{})

	var i int

	trigger := NewTrigger()

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s)

		g.Add(
			Timer("on-interval", func(ctx context.Context) error {
				defer func(iter int) {
					if iter == 0 {
						close(ran)
					}
				}(i)

				i++

				return nil
			}, 1*time.Hour, WithTrigger(trigger)),
		)

		l.Append(g)
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

	if i != 1 {
		t.Fail()
	}
}

// This test asserts that the timer will stop as soon as the lifecycle has stopped, when waiting for an interval pulse
func TestTimer_ExitOnClose(t *testing.T) {
	t.Parallel()

	var i int
	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s)

		g.Add(
			Timer("on-interval", func(ctx context.Context) error {
				i++
				return nil
			}, 1*time.Hour),
		)

		l.Append(g)
	})

	log := hivetest.Logger(t)
	if err := h.Start(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := h.Stop(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	if i != 0 {
		t.Fail()
	}
}

// This test asserts that the context given to the timer closes when the lifecycle ends, and that the timer stops
// after the fn return.
func TestTimer_ExitOnCloseFnCtx(t *testing.T) {
	t.Parallel()

	var i int
	started := make(chan struct{})
	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s)

		g.Add(
			Timer("on-interval", func(ctx context.Context) error {
				i++
				if started != nil {
					close(started)
				}
				<-ctx.Done()
				return nil
			}, 1*time.Millisecond),
		)

		l.Append(g)
	})

	log := hivetest.Logger(t)
	if err := h.Start(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	<-started

	if err := h.Stop(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	if i != 1 {
		t.Fail()
	}
}
