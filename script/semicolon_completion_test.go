// Copyright 2026 The Cilium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package script

import (
	"bytes"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
	"golang.org/x/term"
)

func TestCompletionAfterSemicolons(t *testing.T) {
	e, s := NewEngine(), executionState(t)
	e.Cmds["custom"] = Command(CmdUsage{
		Flags: func(fs *pflag.FlagSet) { fs.String("label", "", "label") },
		AutocompleteArgs: func(_ *State, before []string, cur string) []string {
			require.Equal(t, []string{"'a;#{}'"}, before)
			require.Equal(t, "pre", cur)
			return []string{"prefix"}
		},
	}, func(*State, ...string) (WaitFunc, error) { t.Fatal("completion executed command"); return nil, nil })
	for _, tc := range []struct{ input, want string }{
		{"echo first; cu", "echo first; custom"},
		{"echo 'a;#{}'; cu", "echo 'a;#{}'; custom"},
		{"echo first; [condition] ! cu", "echo first; [condition] ! custom"},
		{"echo first; custom --lab", "echo first; custom --label"},
		{"echo first; custom 'a;#{}' pre", "echo first; custom 'a;#{}' prefix"},
		{"echo first;  ", "echo first;  break"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, pos, ok := e.Autocomplete(term.NewTerminal(new(bytes.Buffer), ""), s)(tc.input, len(tc.input), '\t')
			require.True(t, ok)
			require.Equal(t, tc.want, got)
			require.Equal(t, len(tc.want), pos)
		})
	}
	input := "echo first; cu; echo last"
	cursor := len("echo first; cu")
	got, pos, ok := e.Autocomplete(term.NewTerminal(new(bytes.Buffer), ""), s)(input, cursor, '\t')
	require.True(t, ok)
	require.Equal(t, "echo first; custom; echo last", got)
	require.Equal(t, len("echo first; custom"), pos)
}
