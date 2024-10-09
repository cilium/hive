// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cell_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cilium/hive"
	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
	"github.com/cilium/hive/script"
	"github.com/stretchr/testify/require"
)

func TestSimpleHealthCmd(t *testing.T) {
	h := hive.New(
		cell.SimpleHealthCell,
		cell.Provide(func(sh *cell.SimpleHealth) hive.ScriptCmdOut {
			return hive.NewScriptCmd("health", cell.SimpleHealthCmd(sh))
		}),
		cell.Invoke(
			func(h cell.Health) {
				h.NewScope("test1").OK("all OK")
				h.NewScope("test2").Degraded("degraded", errors.New("broken"))
			},
		),
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
health
health 'test1.*level=OK.*message=all OK'
health 'test2.*level=Degraded.*message=degraded error=broken'
hive stop
`
	bio := bufio.NewReader(bytes.NewBufferString(script))
	var stdout bytes.Buffer
	err = e.Execute(s, "", bio, &stdout)
	require.NoError(t, err, "Execute")

	expected := `health 'test1.*matched: test1.*health 'test2.*matched: test2`
	require.Regexp(t, expected, strings.ReplaceAll(stdout.String(), "\n", " "))
}
