// Copyright 2026 The Cilium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package script

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
	"golang.org/x/term"
)

func TestReentrantExecutionMetadata(t *testing.T) {
	for _, outerLine := range []bool{false, true} {
		for _, innerLine := range []bool{false, true} {
			for _, retry := range []bool{false, true} {
				t.Run(fmt.Sprintf("outerLine=%v/innerLine=%v/retry=%v", outerLine, innerLine, retry), func(t *testing.T) {
					e, s := NewEngine(), executionState(t)
					e.MaxRetries = 1
					e.RetryInterval = time.Microsecond
					e.MaxRetryInterval = time.Microsecond
					calls := 0
					failure := errors.New("outer failed")
					e.Cmds["delegate"] = Command(CmdUsage{Flags: func(fs *pflag.FlagSet) { fs.String("label", "outer", "label") }}, func(s *State, _ ...string) (WaitFunc, error) {
						calls++
						flags, count, requested := s.Flags, s.RetryCount, s.retriesRequested
						// Exercise both directions: an inner success must neither enable nor
						// disable retries in its caller.
						source := "* env NESTED=persisted"
						if retry {
							source = "env NESTED=persisted"
						}
						var err error
						if innerLine {
							err = e.ExecuteLine(s, source, io.Discard)
						} else {
							err = e.Execute(s, "inner", bufio.NewReader(strings.NewReader(source)), io.Discard)
						}
						require.NoError(t, err)
						require.Same(t, flags, s.Flags)
						require.Equal(t, count, s.RetryCount)
						require.Equal(t, requested, s.retriesRequested)
						label, err := s.Flags.GetString("label")
						require.NoError(t, err)
						require.Equal(t, "outer", label)
						if calls == 1 {
							return nil, failure
						}
						return nil, nil
					})
					source := "delegate"
					if retry {
						source = "* delegate"
					}
					var err error
					if outerLine {
						err = e.ExecuteLine(s, source, io.Discard)
					} else {
						err = e.Execute(s, "outer", bufio.NewReader(strings.NewReader(source)), io.Discard)
					}
					if retry {
						require.NoError(t, err)
						require.Equal(t, 2, calls)
					} else {
						require.ErrorIs(t, err, failure)
						require.Equal(t, 1, calls)
					}
					value, _ := s.LookupEnv("NESTED")
					require.Equal(t, "persisted", value)
				})
			}
		}
	}
}

func TestCompletionPreservesPrefixes(t *testing.T) {
	e, s := NewEngine(), executionState(t)
	e.Cmds["custom"] = Command(CmdUsage{
		Flags: func(fs *pflag.FlagSet) { fs.String("label", "", "label") },
		AutocompleteArgs: func(_ *State, before []string, cur string) []string {
			require.Empty(t, before)
			require.Equal(t, "pre", cur)
			return []string{"prefix"}
		},
	}, func(*State, ...string) (WaitFunc, error) { t.Fatal("completion executed command"); return nil, nil })
	for _, tc := range []struct{ input, want string }{
		{"! custom pre", "! custom prefix"},
		{"? custom pre", "? custom prefix"},
		{"* custom pre", "* custom prefix"},
		{"!* custom pre", "!* custom prefix"},
		{"[yes] custom pre", "[yes] custom prefix"},
		{"[custom:tag] ! custom pre", "[custom:tag] ! custom prefix"},
		{"  [custom]  !  custom\t pre", "  [custom]  !  custom\t prefix"},
		{"[custom] custom --lab", "[custom] custom --label"},
		{"! cu", "! custom"},
		{"[custom] cu", "[custom] custom"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			completed, pos, ok := e.Autocomplete(term.NewTerminal(new(bytes.Buffer), ""), s)(tc.input, len(tc.input), '\t')
			require.True(t, ok)
			require.Equal(t, tc.want, completed)
			require.Equal(t, len(tc.want), pos)
		})
	}
	// The remote protocol still returns a full command without local prefixes.
	require.NoError(t, e.ExecuteLine(s, "custom --autocomplete='pre'", io.Discard))
	require.Equal(t, "custom prefix\n", s.Stdout())
}

func TestSetupErrorsSurviveDelegation(t *testing.T) {
	for _, inner := range []string{"missing", "[missing] echo ok", "[root:invalid] echo ok", "[GOOS] echo ok", "[broken] echo ok", "configured --unknown", "configured --count=bad", "configured --count", "echo ok &"} {
		for _, prefix := range []string{"! ", "? ", "* ", "!* "} {
			t.Run(prefix+inner, func(t *testing.T) {
				e, s := NewEngine(), executionState(t)
				e.MaxRetries = 1
				e.Cmds["configured"] = Command(CmdUsage{Flags: func(fs *pflag.FlagSet) { fs.Int("count", 0, "count") }}, func(*State, ...string) (WaitFunc, error) { t.Fatal("invalid command executed"); return nil, nil })
				conditionFailure := errors.New("condition failed")
				e.Conds["broken"] = Condition("broken", func(*State) (bool, error) { return false, conditionFailure })
				calls := 0
				e.Cmds["delegate"] = Command(CmdUsage{}, func(s *State, _ ...string) (WaitFunc, error) {
					calls++
					return nil, e.ExecuteLine(s, inner, io.Discard)
				})
				err := e.ExecuteLine(s, prefix+"delegate", io.Discard)
				var setup *setupError
				require.ErrorAs(t, err, &setup)
				require.Equal(t, 1, calls)
				if strings.Contains(inner, "broken") {
					require.ErrorIs(t, err, conditionFailure)
				}
			})
		}
	}
}
