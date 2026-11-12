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

func TestCompletionInsideBlocks(t *testing.T) {
	e, s := registeredBlockEngine(), executionState(t)
	e.Cmds["custom"] = Command(CmdUsage{Flags: func(fs *pflag.FlagSet) { fs.String("label", "", "label") }}, func(*State, ...string) (WaitFunc, error) { t.Fatal("completion executed command"); return nil, nil })
	for _, tc := range []struct{ input, want string }{
		{"echo first; ea", "echo first; each"},
		{"each x A { ech", "each x A { echo"},
		{"each x A { block { ech", "each x A { block { echo"},
		{"each x A { echo ${x}; ech", "each x A { echo ${x}; echo"},
		{"each x A { echo 'a;#{}'; ech", "each x A { echo 'a;#{}'; echo"},
		{"each x A { echo a }; ech", "each x A { echo a }; echo"},
		{"block { [condition] ! cus", "block { [condition] ! custom"},
		{"block { custom --lab", "block { custom --label"},
		{"block {\n# comment {\nech", "block {\n# comment {\necho"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, pos, ok := e.Autocomplete(term.NewTerminal(new(bytes.Buffer), ""), s)(tc.input, len(tc.input), '\t')
			require.True(t, ok)
			require.Equal(t, tc.want, got)
			require.Equal(t, len(tc.want), pos)
		})
	}
	input := "each x A { ech }"
	got, pos, ok := e.Autocomplete(term.NewTerminal(new(bytes.Buffer), ""), s)(input, len(input)-2, '\t')
	require.True(t, ok)
	require.Equal(t, "each x A { echo }", got)
	require.Equal(t, len("each x A { echo"), pos)
}
