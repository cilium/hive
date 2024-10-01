// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package hive_test

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"testing"

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
hive start
example1
example2
hive stop
`
	bio := bufio.NewReader(bytes.NewBufferString(script))
	var stdout bytes.Buffer
	err = e.Execute(s, "", bio, &stdout)
	require.NoError(t, err, "Execute")

	expected := `> hive start.*> example1.*hello1.*> example2.*hello2.*> hive stop`
	require.Regexp(t, expected, strings.ReplaceAll(stdout.String(), "\n", " "))
}
