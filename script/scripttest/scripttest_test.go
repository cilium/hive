// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package scripttest_test

import (
	"context"
	"errors"
	"io"
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
			Conds:            scripttest.DefaultConds(),
			Cmds:             scripttest.DefaultCmds(),
			BlockCmds:        scripttest.DefaultBlockCmds(),
			Quiet:            !testing.Verbose(),
			RetryInterval:    10 * time.Millisecond,
			MaxRetryInterval: 100 * time.Millisecond,
		}
		engine.Conds["testtrue"] = script.BoolCondition("test condition that is true", true)
		engine.Conds["testfalse"] = script.BoolCondition("test condition that is false", false)
		engine.Cmds["line"] = script.Command(
			script.CmdUsage{Args: "command"},
			func(s *script.State, args ...string) (script.WaitFunc, error) {
				if len(args) != 1 {
					return nil, script.ErrUsage
				}
				return nil, engine.ExecuteLine(s, args[0], io.Discard)
			},
		)
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
				s.Logf("retrytest called")
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

func TestSkipThroughBlocks(t *testing.T) {
	for _, source := range []string{
		"retry --max-retries=1 { skip reason }",
		"retry --until-failure --max-retries=1 { skip reason }",
		"! foreach x A B { skip reason }",
		"? foreach x A B { skip reason }",
		"when true { foreach x A B { skip reason } }",
		"* skip reason",
	} {
		t.Run(source, func(t *testing.T) {
			e := script.NewEngine()
			_, wantErr := scripttest.Skip().Run(nil, "reason")
			calls := 0
			e.Cmds["skip"] = script.Command(script.CmdUsage{}, func(*script.State, ...string) (script.WaitFunc, error) { calls++; return nil, wantErr })
			s, err := script.NewState(context.Background(), t.TempDir(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer s.CloseAndWait(io.Discard)
			err = e.ExecuteLine(s, source, io.Discard)
			if !errors.Is(err, wantErr) {
				t.Fatalf("expected skip signal, got %v", err)
			}
			if calls != 1 {
				t.Fatalf("skip called %d times", calls)
			}
		})
	}
}
