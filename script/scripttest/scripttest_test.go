// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package scripttest_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilium/hive/script"
	"github.com/cilium/hive/script/scripttest"
	"github.com/spf13/pflag"
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

		engine.Cmds["retrytest"] = script.Command(
			// This is a simple test command to verify that the flags are not
			// misplaced when retrying a command.
			script.CmdUsage{
				Flags: func(fs *pflag.FlagSet) {
					fs.String("not-empty", "", "this should not be empty")
				},
			},
			func(s *script.State, args ...string) (script.WaitFunc, error) {
				notEmpty, err := s.Flags.GetString("not-empty")
				if err != nil {
					return nil, err
				}
				if len(notEmpty) == 0 {
					return nil, errors.New("not-empty is empty")
				}
				if len(args) != 1 || args[0] != "abc" {
					return nil, errors.New("expected one arg 'abc'")
				}

				// Check if the file was already created (this command ran already)
				// otherwise create it so we succeed second time.
				_, err = os.Stat(s.Path("retrytest"))
				if err == nil {
					return nil, nil
				}
				os.WriteFile(s.Path("retrytest"), []byte("xxx"), 0644)
				return nil, errors.New("retrytest")
			},
		)
		return engine
	}, env, "testdata/*.txt")
}
