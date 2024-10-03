// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cell_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/cilium/hive/cell"
)

var (
	errLifecycle = errors.New("nope")
	badStartHook = cell.Hook{
		OnStart: func(cell.HookContext) error {
			return errLifecycle
		},
	}

	nilHook = cell.Hook{OnStart: nil, OnStop: nil}
)

func TestNewDefaultLifecycle(t *testing.T) {
	var started, stopped int
	goodHook := cell.Hook{
		OnStart: func(cell.HookContext) error { started++; return nil },
		OnStop:  func(cell.HookContext) error { stopped++; return nil },
	}

	log := slog.Default()
	lc := cell.NewDefaultLifecycle([]cell.HookInterface{goodHook}, 0, time.Second)

	err := lc.Start(log, context.TODO())
	assert.NoError(t, err, "expected Start to succeed")
	err = lc.Stop(log, context.TODO())
	assert.NoError(t, err, "expected Stop to succeed")

	assert.Equal(t, 1, started)
	assert.Equal(t, 1, stopped)

	// Construct already started hooks
	started = 0
	stopped = 0

	lc = cell.NewDefaultLifecycle([]cell.HookInterface{goodHook}, 1, time.Second)
	err = lc.Stop(log, context.TODO())
	assert.NoError(t, err, "expected Stop to succeed")

	assert.Equal(t, 0, started)
	assert.Equal(t, 1, stopped)
}

func TestLifecycle(t *testing.T) {
	var started, stopped int
	goodHook := cell.Hook{
		OnStart: func(cell.HookContext) error { started++; return nil },
		OnStop:  func(cell.HookContext) error { stopped++; return nil },
	}
	badStopHook := cell.Hook{
		OnStart: func(cell.HookContext) error { started++; return nil },
		OnStop:  func(cell.HookContext) error { return errLifecycle },
	}
	log := slog.Default()
	var lc cell.DefaultLifecycle

	// Test without any hooks
	lc = cell.DefaultLifecycle{}
	err := lc.Start(log, context.TODO())
	assert.NoError(t, err, "expected Start to succeed")
	err = lc.Stop(log, context.TODO())
	assert.NoError(t, err, "expected Stop to succeed")

	// Test with 3 good, 1 nil hook, all successful.
	lc = cell.DefaultLifecycle{}
	lc.Append(goodHook)
	lc.Append(goodHook)
	lc.Append(goodHook)
	lc.Append(nilHook)

	err = lc.Start(log, context.TODO())
	assert.NoError(t, err, "expected Start to succeed")
	err = lc.Stop(log, context.TODO())
	assert.NoError(t, err, "expected Stop to succeed")

	assert.Equal(t, 3, started)
	assert.Equal(t, 3, stopped)
	started = 0
	stopped = 0

	// Test with 2 good, 1 bad start. Should see
	// the good ones stopped.
	lc = cell.DefaultLifecycle{}
	lc.Append(goodHook)
	lc.Append(goodHook)
	lc.Append(badStartHook)

	err = lc.Start(log, context.TODO())
	assert.ErrorIs(t, err, errLifecycle, "expected Start to fail")

	assert.Equal(t, 2, started)
	started = 0
	stopped = 0

	// Test with 2 good, 1 bad stop. Stop should return the error.
	lc = cell.DefaultLifecycle{}
	lc.Append(goodHook)
	lc.Append(goodHook)
	lc.Append(badStopHook)

	err = lc.Start(log, context.TODO())
	assert.NoError(t, err, "expected Start to succeed")
	assert.Equal(t, 3, started)
	assert.Equal(t, 0, stopped)

	err = lc.Stop(log, context.TODO())
	assert.ErrorIs(t, err, errLifecycle, "expected Stop to fail")
	assert.Equal(t, 2, stopped)
	started = 0
	stopped = 0

	// Test that one can have hook with a stop and no start.
	lc = cell.DefaultLifecycle{}
	lc.Append(cell.Hook{
		OnStop: func(cell.HookContext) error { stopped++; return nil },
	})
	err = lc.Start(log, context.TODO())
	assert.NoError(t, err, "expected Start to succeed")
	err = lc.Stop(log, context.TODO())
	assert.NoError(t, err, "expected Stop to succeed")
	assert.Equal(t, 1, stopped)

	started = 0
	stopped = 0
}

func TestLifecycleCancel(t *testing.T) {
	log := slog.Default()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Test cancellation in start hook
	lc := cell.DefaultLifecycle{}
	lc.Append(cell.Hook{
		OnStart: func(ctx cell.HookContext) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})
	err := lc.Start(log, ctx)
	assert.ErrorIs(t, err, context.Canceled)

	// Test cancellation in stop hook
	expectedErr := errors.New("stop cancelled")
	ctx, cancel = context.WithCancel(context.Background())
	inStop := make(chan struct{})
	lc = cell.DefaultLifecycle{}
	lc.Append(cell.Hook{
		OnStop: func(ctx cell.HookContext) error {
			close(inStop)
			<-ctx.Done()
			assert.ErrorIs(t, ctx.Err(), context.Canceled)
			return expectedErr
		},
	})

	// Only cancel once we're inside stop as cell.Stop() short-circuits
	// when context is cancelled.
	go func() {
		<-inStop
		cancel()
	}()

	err = lc.Start(log, ctx)
	assert.NoError(t, err)

	err = lc.Stop(log, ctx)
	assert.ErrorIs(t, err, expectedErr)
}
