// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package hive_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
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

func TestRunReplBlockCommands(t *testing.T) {
	in, err := os.CreateTemp(t.TempDir(), "repl-input")
	require.NoError(t, err)
	defer in.Close()

	_, err = in.WriteString(strings.Join([]string{
		"foreach item first second {",
		"echo repl-${item}-result",
		"}",
		"foreach foo A B {",
		"foreach bar 1 2 {",
		"echo nested-${foo}-${bar}",
		"}",
		"}",
		"foreach foo C D { foreach bar 3 4 { echo nested-${foo}-${bar} } }",
		"echo repl-${/}-one; echo repl-${/}-two",
		"foreach foo E F { echo loop-${foo}; echo block-${foo}-done }",
		"retry --int\t 1ms --max-retries=1 { echo retry-${/}-ok }",
		"echo completion-${/}-before; fore\t item A { ech\t completion-${item}-inline }",
		"foreach item B { when true { ech\t completion-${item}-nested } }",
		"foreach item C { retry --int\t 1ms --max-retries=1 { echo completion-${item}-flag } }",
		"",
	}, "\r"))
	require.NoError(t, err)
	_, err = in.Seek(0, io.SeekStart)
	require.NoError(t, err)

	out, err := os.CreateTemp(t.TempDir(), "repl-output")
	require.NoError(t, err)
	defer out.Close()

	hive.RunRepl(hive.New(), in, out, "test> ")

	_, err = out.Seek(0, io.SeekStart)
	require.NoError(t, err)
	output, err := io.ReadAll(out)
	require.NoError(t, err)

	normalized := strings.ReplaceAll(string(output), "\r\n", "\n")
	for _, value := range []string{
		"repl-first-result", "repl-second-result",
		"nested-A-1", "nested-A-2", "nested-B-1", "nested-B-2",
		"nested-C-3", "nested-C-4", "nested-D-3", "nested-D-4",
		"completion-/-before", "completion-A-inline", "completion-B-nested", "completion-C-flag",
		"repl-/-one", "repl-/-two", "loop-E", "loop-F", "block-E-done", "block-F-done", "retry-/-ok",
	} {
		require.Contains(t, normalized, "\n"+value+"\n")
	}
	require.NotContains(t, normalized, "unknown command")
	require.NotContains(t, normalized, "parse error")
	require.Contains(t, normalized, "\n> echo nested-A-1\n")
	require.NotContains(t, normalized, "> echo nested-${foo}-${bar}\n")
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
