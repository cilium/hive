// Copyright 2026 The Cilium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package script

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
	"golang.org/x/term"
)

func executionState(t *testing.T) *State {
	t.Helper()
	s, err := NewState(context.Background(), t.TempDir(), []string{"NAME=value"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.CloseAndWait(io.Discard)) })
	return s
}

func TestSharedExecution(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{"echo ${NAME}", "value\n"},
		{"echo 'a # b'", "a # b\n"},
		{"echo 'it''s'", "it's\n"},
		{"echo '${NAME}'", "${NAME}\n"},
		{"echo ok # ignored", "ok\n"},
		{"echo ''", "\n"},
	} {
		for _, line := range []bool{false, true} {
			t.Run(tc.source+map[bool]string{false: "/script", true: "/line"}[line], func(t *testing.T) {
				e, s := NewEngine(), executionState(t)
				var err error
				if line {
					err = e.ExecuteLine(s, tc.source, io.Discard)
				} else {
					err = e.Execute(s, "test", bufio.NewReader(strings.NewReader(tc.source)), io.Discard)
				}
				require.NoError(t, err)
				require.Equal(t, tc.want, s.Stdout())
			})
		}
	}
}

func TestSharedPreparation(t *testing.T) {
	e, s := NewEngine(), executionState(t)
	calls := 0
	e.Cmds["custom"] = Command(CmdUsage{
		Summary: "custom command help",
		Flags:   func(fs *pflag.FlagSet) { fs.String("label", "", "label") },
		AutocompleteArgs: func(_ *State, before []string, prefix string) []string {
			require.Equal(t, []string{"one"}, before)
			require.Equal(t, "pre", prefix)
			return []string{"prefix"}
		},
	}, func(s *State, args ...string) (WaitFunc, error) {
		calls++
		label, err := s.Flags.GetString("label")
		require.NoError(t, err)
		require.Equal(t, "value", label)
		require.Equal(t, []string{"argument"}, args)
		return nil, nil
	})
	require.NoError(t, e.ExecuteLine(s, "custom --label=$NAME argument", io.Discard))
	require.Equal(t, 1, calls)
	require.NoError(t, e.ExecuteLine(s, "custom --help", io.Discard))
	require.Contains(t, s.Stdout(), "custom command help")
	require.NoError(t, e.ExecuteLine(s, "custom --autocomplete='one pre'", io.Discard))
	require.Equal(t, "custom one prefix\n", s.Stdout())
	terminal := term.NewTerminal(new(bytes.Buffer), "")
	input := "custom --lab"
	complete, _, ok := e.Autocomplete(terminal, s)(input, len(input), '\t')
	require.True(t, ok)
	require.Equal(t, "custom --label", complete)
	require.Equal(t, 1, calls)
}

type testTermination struct{}

func (testTermination) Error() string      { return "terminate" }
func (testTermination) ScriptTermination() {}

func TestExecutionTermination(t *testing.T) {
	for _, prefix := range []string{"", "! ", "? ", "* ", "!* "} {
		t.Run(prefix, func(t *testing.T) {
			e, s := NewEngine(), executionState(t)
			calls := 0
			e.Cmds["terminate"] = Command(CmdUsage{}, func(*State, ...string) (WaitFunc, error) { calls++; return nil, testTermination{} })
			err := e.ExecuteLine(s, prefix+"terminate", io.Discard)
			require.ErrorIs(t, err, testTermination{})
			require.Equal(t, 1, calls)
		})
	}
	for _, line := range []bool{false, true} {
		e, s := NewEngine(), executionState(t)
		source := "stop done\nenv AFTER=wrong"
		if line {
			err := e.ExecuteLine(s, source, io.Discard)
			var stop stopError
			require.ErrorAs(t, err, &stop)
		} else {
			require.NoError(t, e.Execute(s, "test", bufio.NewReader(strings.NewReader(source)), io.Discard))
		}
		_, ok := s.LookupEnv("AFTER")
		require.False(t, ok)
	}
}

func TestExecutionErrors(t *testing.T) {
	e, s := NewEngine(), executionState(t)
	err := e.Execute(s, "test", bufio.NewReader(strings.NewReader("# section\n\necho '\n")), io.Discard)
	require.ErrorIs(t, err, ParseError)
	require.ErrorContains(t, err, "test:3:")
	require.True(t, errors.Is(err, ParseError))
}
