// Copyright 2026 The Cilium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package script

import (
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRetryBlockExecution(t *testing.T) {
	e, s := NewEngine(), executionState(t)
	e.RetryInterval = time.Microsecond
	e.MaxRetryInterval = time.Microsecond
	attempts := 0
	e.Cmds["flaky"] = Command(CmdUsage{}, func(*State, ...string) (WaitFunc, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("retry")
		}
		return nil, nil
	})
	require.NoError(t, e.ExecuteLine(s, "retry --max-retries=1 { flaky; env RESULT=$NAME }", io.Discard))
	require.Equal(t, 2, attempts)
	value, _ := s.LookupEnv("RESULT")
	require.Equal(t, "value", value)
	require.ErrorIs(t, e.ExecuteLine(s, "! retry {\necho '\n}", io.Discard), ParseError)
}

func TestRetryRejectsSetupErrors(t *testing.T) {
	for _, body := range []string{"missing", "missing {}", "[missing] echo ok", "echo ok &", "retry --unknown {}", "retry --max-retries=invalid {}", "retry"} {
		for _, mode := range []string{"", "--until-failure "} {
			t.Run(mode+body, func(t *testing.T) {
				e, s := NewEngine(), executionState(t)
				attempts := 0
				e.Cmds["attempt"] = Command(CmdUsage{}, func(*State, ...string) (WaitFunc, error) { attempts++; return nil, nil })
				err := e.ExecuteLine(s, "retry "+mode+"--max-retries=1 { attempt; "+body+" }", io.Discard)
				var setup *setupError
				require.ErrorAs(t, err, &setup)
				require.Equal(t, 1, attempts)
			})
		}
	}
}
