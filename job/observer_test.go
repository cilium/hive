// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package job

import (
	"context"
	"testing"

	"github.com/cilium/hive/cell"
	"github.com/cilium/stream"
)

// This test asserts that an observer job will stop after a stream has been completed.
func TestObserver_ShortStream(t *testing.T) {
	t.Parallel()

	var (
		g Group
		i int
	)

	streamSlice := []string{"a", "b", "c"}

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g = r.NewGroup(s)

		g.Add(
			Observer("retry-fail", func(ctx context.Context, event string) error {
				i++
				return nil
			}, stream.FromSlice(streamSlice)),
		)

		l.Append(g)
	})

	if err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Continue as soon as all jobs stopped
	g.(*group).wg.Wait()

	if err := h.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	if i != len(streamSlice) {
		t.Fatal()
	}
}

// This test asserts that the observer will stop without errors when the lifecycle ends, even if the stream has not
// gone away.
func TestObserver_LongStream(t *testing.T) {
	t.Parallel()

	var (
		g Group
		i int
	)

	inChan := make(chan struct{})

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g = r.NewGroup(s)

		g.Add(
			Observer("retry-fail", func(ctx context.Context, _ struct{}) error {
				i++
				return nil
			}, stream.FromChannel(inChan)),
		)

		l.Append(g)
	})

	if err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := h.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	if i != 0 {
		t.Fatal()
	}
}

// This test asserts that the context given to the observer fn is closed when the lifecycle ends and the observer
// stops even if there are still pending items in the stream.
func TestObserver_CtxClose(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	i := 0
	streamSlice := []string{"a", "b", "c"}

	h := fixture(func(r Registry, s cell.Health, l cell.Lifecycle) {
		g := r.NewGroup(s)

		g.Add(
			Observer("retry-fail", func(ctx context.Context, event string) error {
				if i == 0 {
					close(started)
					i++
				}
				<-ctx.Done()
				return nil
			}, stream.FromSlice(streamSlice)),
		)

		l.Append(g)
	})

	if err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	<-started

	if err := h.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
