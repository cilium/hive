// Copyright 2026 The Cilium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package script

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestRetryAttempts(t *testing.T) {
	failure := errors.New("body failed")
	cases := []struct {
		name                               string
		flags                              []string
		succeedAfter, failAfter, wantCalls int
		wantErr                            error
		errorText                          string
	}{
		{name: "immediate success", wantCalls: 1},
		{name: "eventual success", flags: []string{"--max-retries=2"}, succeedAfter: 2, wantCalls: 3},
		{name: "exhausted", flags: []string{"--max-retries=2"}, succeedAfter: 3, wantCalls: 3, wantErr: failure},
		{name: "until failure", flags: []string{"--until-failure", "--max-retries=2"}, failAfter: 2, wantCalls: 3},
		{name: "until failure exhausted", flags: []string{"--until-failure", "--max-retries=2"}, wantCalls: 3, errorText: "block did not fail after 2 retries"},
		{name: "parse error", flags: []string{"--until-failure"}, wantCalls: 1, wantErr: ParseError},
		{name: "cancellation error", flags: []string{"--until-failure"}, wantCalls: 1, wantErr: context.Canceled},
		{name: "deadline error", wantCalls: 1, wantErr: context.DeadlineExceeded},
		{name: "stop", flags: []string{"--until-failure"}, wantCalls: 1, wantErr: stopError{}},
		{name: "negative count", flags: []string{"--max-retries=-1"}, errorText: "must be non-negative"},
		{name: "negative interval", flags: []string{"--interval=-1s"}, errorText: "must be non-negative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := NewState(context.Background(), t.TempDir(), nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, s.CloseAndWait(io.Discard)) })
			s.engine = &Engine{RetryInterval: time.Microsecond, MaxRetryInterval: time.Microsecond}
			retry := Retry()
			s.Flags = pflag.NewFlagSet("retry", pflag.ContinueOnError)
			retry.Usage().Flags(s.Flags)
			require.NoError(t, s.Flags.Parse(append([]string{"--interval=1us"}, tc.flags...)))
			calls := 0
			err = retry.Run(s, nil, func(map[string]string) error {
				calls++
				if tc.wantErr != nil && tc.wantErr != failure {
					return tc.wantErr
				}
				if calls <= tc.succeedAfter || (tc.failAfter > 0 && calls > tc.failAfter) {
					return failure
				}
				return nil
			})
			require.Equal(t, tc.wantCalls, calls)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else if tc.errorText != "" {
				require.ErrorContains(t, err, tc.errorText)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestRetryCancellationDuringWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := NewState(ctx, t.TempDir(), nil)
	require.NoError(t, err)
	defer s.CloseAndWait(io.Discard)
	retry := Retry()
	s.Flags = pflag.NewFlagSet("retry", pflag.ContinueOnError)
	retry.Usage().Flags(s.Flags)
	require.NoError(t, s.Flags.Parse([]string{"--interval=1h"}))
	attempted := make(chan struct{})
	go func() { <-attempted; cancel() }()
	calls := 0
	err = retry.Run(s, nil, func(map[string]string) error { calls++; close(attempted); return errors.New("failed") })
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls)
}
