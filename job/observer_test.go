// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package job

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
	"github.com/cilium/stream"
	"github.com/stretchr/testify/assert"
)

// This test asserts that an observer job will stop after a stream has been completed.
func TestObserver_ShortStream(t *testing.T) {
	t.Parallel()

	var (
		g Group
		i atomic.Int32
	)

	streamSlice := []string{"a", "b", "c"}

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g = r.NewGroup(s, l)

		g.Add(
			Observer("retry-fail", func(ctx context.Context, event string) error {
				i.Add(1)
				return nil
			}, stream.FromSlice(streamSlice)),
		)
	})

	log := hivetest.Logger(t)
	if err := h.Start(log, context.Background()); err != nil {
		t.Fatal(err)
	}

	assert.Eventually(t,
		func() bool {
			return i.Load() == int32(len(streamSlice))
		},
		timeout,
		tick)

	if err := h.Stop(log, context.Background()); err != nil {
		t.Fatal(err)
	}
}

// This test asserts that the observer will stop without errors when the lifecycle ends, even if the stream has not
// gone away.
func TestObserver_LongStream(t *testing.T) {
	t.Parallel()

	var (
		g Group
		i atomic.Int32
	)

	inChan := make(chan struct{})

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g = r.NewGroup(s, l)

		g.Add(
			Observer("retry-fail", func(ctx context.Context, _ struct{}) error {
				i.Add(1)
				return nil
			}, stream.FromChannel(inChan)),
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
		t.Fatal()
	}
}

// This test asserts that the context given to the observer fn is closed when the lifecycle ends and the observer
// stops even if there are still pending items in the stream.
func TestObserver_CtxClose(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	var i atomic.Int32
	streamSlice := []string{"a", "b", "c"}

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s, l)

		g.Add(
			Observer("retry-fail", func(ctx context.Context, event string) error {
				if i.Load() == 0 {
					close(started)
					i.Add(1)
				}
				<-ctx.Done()
				return nil
			}, stream.FromSlice(streamSlice)),
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
}
