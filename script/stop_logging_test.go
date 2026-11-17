// Copyright 2026 The Cilium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package script

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStopSectionLogging(t *testing.T) {
	for _, quiet := range []bool{false, true} {
		t.Run(map[bool]string{false: "verbose", true: "quiet"}[quiet], func(t *testing.T) {
			e, s := NewEngine(), executionState(t)
			e.Quiet = quiet
			var log bytes.Buffer
			err := e.Execute(s, "test", bufio.NewReader(strings.NewReader("# section\necho before\nstop done\necho after\n")), &log)
			require.NoError(t, err)
			require.Regexp(t, `^# section \([0-9.]+s\)\n`, log.String())
			require.Equal(t, 1, strings.Count(log.String(), "stop: done\n"))
			require.NotContains(t, log.String(), "echo after")
			if quiet {
				require.Regexp(t, `^# section \([0-9.]+s\)\nstop: done\n$`, log.String())
			} else {
				require.Contains(t, log.String(), "> echo before\n[stdout]\nbefore\n> stop done\nstop: done\n")
			}
		})
	}
}

type failLogWriter struct {
	writes  int
	failure error
}

func (w *failLogWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == 2 {
		return 0, w.failure
	}
	return len(p), nil
}

func TestStopPreservesLogWriteFailure(t *testing.T) {
	e, s := NewEngine(), executionState(t)
	failure := errors.New("log write failed")
	out := &failLogWriter{failure: failure}
	require.ErrorIs(t, e.Execute(s, "test", bufio.NewReader(strings.NewReader("# section\nstop done\n")), out), failure)
}
