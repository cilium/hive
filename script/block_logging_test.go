// Copyright 2026 The Cilium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package script

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBlockSectionLogging(t *testing.T) {
	for _, quiet := range []bool{false, true} {
		for _, outcome := range []string{"success", "failure", "stop", "retry"} {
			t.Run(fmt.Sprintf("quiet=%v/%s", quiet, outcome), func(t *testing.T) {
				e, s := registeredBlockEngine(), executionState(t)
				e.Quiet = quiet
				e.RetryInterval = time.Microsecond
				e.MaxRetryInterval = time.Microsecond
				attempts := 0
				e.Cmds["flaky"] = Command(CmdUsage{}, func(*State, ...string) (WaitFunc, error) {
					attempts++
					if attempts == 1 {
						return nil, errors.New("try again")
					}
					return nil, nil
				})
				tail := map[string]string{"success": "echo done", "failure": "cat missing", "stop": "stop done", "retry": "* flaky"}[outcome]
				source := "# section\necho before\nblock {\n# body comment\neach x A B { echo $x }\n" + tail + "\n}\necho after\n"
				var log bytes.Buffer
				err := e.Execute(s, "test", bufio.NewReader(strings.NewReader(source)), &log)
				if outcome == "failure" {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				text := log.String()
				require.Regexp(t, `^# section \([0-9.]+s\)\n`, text)
				require.NotContains(t, text, "# body comment")
				if quiet && outcome != "failure" {
					require.NotContains(t, text, "> ")
					require.NotContains(t, text, "[stdout]")
					require.NotContains(t, text, "retrying in")
				} else {
					require.Contains(t, text, "> echo before\n")
					require.Contains(t, text, "> echo A\n[stdout]\nA\n")
					require.Contains(t, text, "> echo B\n[stdout]\nB\n")
				}
				if outcome == "stop" {
					require.True(t, strings.HasSuffix(text, "stop: done\n"))
				}
				if outcome == "retry" {
					require.Equal(t, 2, attempts)
				}
			})
		}
	}
}
