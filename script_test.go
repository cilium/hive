// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package hive_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cilium/hive"
	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
	"github.com/cilium/hive/script"
	"github.com/stretchr/testify/require"
)

func exampleCmd() hive.ScriptCmdOut {
	return hive.NewScriptCmd(
		"example1",
		script.Command(
			script.CmdUsage{
				Summary: "Example command",
			},
			func(s *script.State, args ...string) (script.WaitFunc, error) {
				s.Logf("hello1")
				return nil, nil
			},
		),
	)
}

func example2Cmd() hive.ScriptCmdsOut {
	return hive.NewScriptCmds(
		map[string]script.Cmd{
			"example2": script.Command(
				script.CmdUsage{
					Summary: "Second example command",
				},
				func(s *script.State, args ...string) (script.WaitFunc, error) {
					s.Logf("hello2")
					return nil, nil
				},
			),
		},
	)
}

func TestScriptCommands(t *testing.T) {
	h := hive.New(
		cell.Provide(exampleCmd, example2Cmd),
	)
	cmds, err := h.ScriptCommands(hivetest.Logger(t))
	require.NoError(t, err, "ScriptCommands")
	e := script.Engine{
		Cmds: cmds,
	}
	s, err := script.NewState(context.TODO(), "/tmp", nil)
	require.NoError(t, err, "NewState")
	script := `
hive/start
example1
example2
hive/stop
`
	bio := bufio.NewReader(bytes.NewBufferString(script))
	var stdout bytes.Buffer
	err = e.Execute(s, "", bio, &stdout)
	require.NoError(t, err, "Execute")

	expected := `> hive/start.*> example1.*hello1.*> example2.*hello2.*> hive/stop`
	require.Regexp(t, expected, strings.ReplaceAll(stdout.String(), "\n", " "))
}

func TestScriptCommandRetries(t *testing.T) {
	var tests = []struct {
		name         string
		succeedAfter uint
		cancelAfter  uint
		maxRetries   uint
		assert       require.ErrorAssertionFunc
	}{
		{
			name:         "max two retries, succeed after two",
			succeedAfter: 2,
			maxRetries:   2,
			assert:       require.NoError,
		},
		{
			name:         "max two retries, succeed after three",
			succeedAfter: 3,
			maxRetries:   2,
			assert: func(tt require.TestingT, err error, args ...any) {
				require.ErrorContains(t, err, "expected to succeed after 3 times, current: 2", args...)
			},
		},
		{
			name:         "no limit, context cancellation only",
			succeedAfter: 1000,
			cancelAfter:  10,
			assert: func(tt require.TestingT, err error, args ...any) {
				require.ErrorContains(t, err, "expected to succeed after 1000 times, current: 10", args...)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			var (
				counter uint
				engine  = script.Engine{
					Cmds: map[string]script.Cmd{
						"test": script.Command(
							script.CmdUsage{},
							func(s *script.State, args ...string) (script.WaitFunc, error) {
								defer func() { counter++ }()

								s.Logf("test command called %d times", counter)
								if tt.cancelAfter != 0 && counter == tt.cancelAfter {
									cancel()
								}

								if counter != tt.succeedAfter {
									return nil, fmt.Errorf("expected to succeed after %d times, current: %d", tt.succeedAfter, counter)
								}

								return nil, nil
							},
						),
					},

					RetryInterval:    10 * time.Millisecond,
					MaxRetryInterval: 10 * time.Millisecond,
					MaxRetries:       tt.maxRetries,
				}
			)

			s, err := script.NewState(ctx, t.TempDir(), nil)
			require.NoError(t, err, "NewState")

			var stdout bytes.Buffer
			err = engine.Execute(s, "", bufio.NewReader(strings.NewReader("* test")), &stdout)
			tt.assert(t, err)
		})
	}
}
