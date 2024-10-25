// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package scripttest_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilium/hive/script"
	"github.com/cilium/hive/script/scripttest"
)

func TestAll(t *testing.T) {
	ctx := context.Background()
	env := os.Environ()
	scripttest.Test(t, ctx, func(t testing.TB, scriptArgs []string) *script.Engine {
		engine := &script.Engine{
			Conds:         scripttest.DefaultConds(),
			Cmds:          scripttest.DefaultCmds(),
			Quiet:         !testing.Verbose(),
			RetryInterval: 10 * time.Millisecond,
		}
		engine.Cmds["args"] = script.Command(
			script.CmdUsage{},
			func(s *script.State, args ...string) (script.WaitFunc, error) {
				return func(s *script.State) (stdout string, stderr string, err error) {
					stdout = strings.Join(scriptArgs, ":") + "\n"
					return
				}, nil
			})
		return engine
	}, env, "testdata/*.txt")
}
