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
)

func registeredBlockEngine() *Engine {
	e := NewEngine()
	e.BlockCmds["block"] = BlockCommand(CmdUsage{Summary: "execute a registered block"}, func(_ *State, _ []string, body CallBlock) error { return body(nil) })
	e.BlockCmds["each"] = BlockCommand(CmdUsage{}, func(_ *State, args []string, body CallBlock) error {
		for _, value := range args[1:] {
			if err := body(map[string]string{args[0]: value}); err != nil {
				return err
			}
		}
		return nil
	})
	return e
}

func TestRegisteredBlockParsing(t *testing.T) {
	for _, line := range []bool{false, true} {
		t.Run(map[bool]string{false: "script", true: "line"}[line], func(t *testing.T) {
			e, s := registeredBlockEngine(), executionState(t)
			var got []string
			e.Cmds["record"] = Command(CmdUsage{}, func(_ *State, args ...string) (WaitFunc, error) { got = append(got, args...); return nil, nil })
			source := "record before; each x A B { # open\n each y 1 2 { record ${x}${y}; record 'a;#{}' } # closed\n}; record after"
			var err error
			if line {
				err = e.ExecuteLine(s, source, io.Discard)
			} else {
				err = e.Execute(s, "test", bufio.NewReader(strings.NewReader(source)), io.Discard)
			}
			require.NoError(t, err)
			require.Equal(t, []string{"before", "A1", "a;#{}", "A2", "a;#{}", "B1", "a;#{}", "B2", "a;#{}", "after"}, got)
		})
	}
}

func TestRegisteredBlockStatus(t *testing.T) {
	for _, source := range []string{"! block { fail }", "! { block { fail } }", "? block { fail }"} {
		e, s := registeredBlockEngine(), executionState(t)
		e.Cmds["fail"] = Command(CmdUsage{}, func(*State, ...string) (WaitFunc, error) { return nil, errors.New("failed") })
		require.NoError(t, e.ExecuteLine(s, source, io.Discard))
	}
	for _, source := range []string{"! block {\necho '\n}", "? block {\necho '\n}", "* block {}", "!* block {}", "block {", "}"} {
		e, s := registeredBlockEngine(), executionState(t)
		require.ErrorIs(t, e.ExecuteLine(s, source, io.Discard), ParseError)
	}
	e, s := registeredBlockEngine(), executionState(t)
	require.NoError(t, e.Execute(s, "test", bufio.NewReader(strings.NewReader("block { each x A B { stop done }; env AFTER=wrong }; env AFTER=wrong")), io.Discard))
	_, ok := s.LookupEnv("AFTER")
	require.False(t, ok)
}

func TestRegisteredBlockInvocation(t *testing.T) {
	e, s := registeredBlockEngine(), executionState(t)
	e.BlockCmds["scope"] = BlockCommand(CmdUsage{Flags: func(fs *pflag.FlagSet) { fs.String("label", "", "label") }}, func(s *State, args []string, body CallBlock) error {
		flags := s.Flags
		for range 2 {
			if err := body(map[string]string{"NAME": "temporary", "NEW": "temporary"}); err != nil {
				return err
			}
			require.Same(t, flags, s.Flags)
			label, err := s.Flags.GetString("label")
			require.NoError(t, err)
			require.Equal(t, "outer", label)
		}
		return nil
	})
	require.NoError(t, e.ExecuteLine(s, "scope --label=outer { each x A B { echo $x }; env NAME=changed NEW=changed OTHER=persisted }", io.Discard))
	value, _ := s.LookupEnv("NAME")
	require.Equal(t, "value", value)
	value, _ = s.LookupEnv("OTHER")
	require.Equal(t, "persisted", value)
	_, ok := s.LookupEnv("NEW")
	require.False(t, ok)
}

func TestRegisteredBlockMetadata(t *testing.T) {
	e, s := registeredBlockEngine(), executionState(t)
	calls := 0
	e.BlockCmds["custom"] = BlockCommand(CmdUsage{
		Summary: "registered block help",
		AutocompleteArgs: func(_ *State, before []string, prefix string) []string {
			require.Equal(t, "pre", prefix)
			return []string{"prefix"}
		},
	}, func(*State, []string, CallBlock) error { calls++; return nil })
	for _, source := range []string{"custom --help", "custom --help {}"} {
		require.NoError(t, e.ExecuteLine(s, source, io.Discard))
		require.Contains(t, s.Stdout(), "registered block help")
	}
	for _, source := range []string{"custom --autocomplete='pre'", "custom --autocomplete='pre' {}"} {
		require.NoError(t, e.ExecuteLine(s, source, io.Discard))
		require.Equal(t, "custom prefix\n", s.Stdout())
	}
	require.ErrorContains(t, e.ExecuteLine(s, "custom", io.Discard), "requires a body")
	require.Zero(t, calls)
}

func TestRegisteredBlockLog(t *testing.T) {
	e, s := registeredBlockEngine(), executionState(t)
	var log bytes.Buffer
	require.NoError(t, e.ExecuteLine(s, "each tbl backends frontends { echo $tbl }", &log))
	require.Contains(t, log.String(), "> echo backends\n")
	require.Contains(t, log.String(), "> echo frontends\n")
	require.NotContains(t, log.String(), "> echo $tbl")
}

func TestRegisteredBlockConditions(t *testing.T) {
	e, s := registeredBlockEngine(), executionState(t)
	e.Conds["no"] = BoolCondition("false", false)
	require.NoError(t, e.ExecuteLine(s, "[no] block { unknown }; [no] { unknown }; block { env RESULT=$NAME }", io.Discard))
	value, _ := s.LookupEnv("RESULT")
	require.Equal(t, "value", value)
}

func TestRegisteredBlockTermination(t *testing.T) {
	e, s := registeredBlockEngine(), executionState(t)
	e.Cmds["terminate"] = Command(CmdUsage{}, func(*State, ...string) (WaitFunc, error) { return nil, testTermination{} })
	err := e.ExecuteLine(s, "! block { terminate }", io.Discard)
	require.ErrorIs(t, err, testTermination{})
	require.NotErrorIs(t, err, context.Canceled)
}

func TestRegisteredBlockSetupErrors(t *testing.T) {
	for _, body := range []string{"missing", "missing {}", "[missing] echo ok", "echo ok &", "block & {}", "block", "configured --unknown"} {
		for _, prefix := range []string{"! ", "? "} {
			t.Run(prefix+body, func(t *testing.T) {
				e, s := registeredBlockEngine(), executionState(t)
				e.Cmds["configured"] = Command(CmdUsage{Flags: func(fs *pflag.FlagSet) { fs.Bool("known", false, "known") }}, func(*State, ...string) (WaitFunc, error) { t.Fatal("invalid command executed"); return nil, nil })
				err := e.ExecuteLine(s, prefix+"block { "+body+" }", io.Discard)
				var setup *setupError
				require.ErrorAs(t, err, &setup)
			})
		}
	}
}
