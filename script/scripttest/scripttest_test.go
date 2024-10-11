// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package scripttest_test

import (
	"context"
	"os"
	"testing"

	"github.com/cilium/hive/script"
	"github.com/cilium/hive/script/scripttest"
)

func TestAll(t *testing.T) {
	ctx := context.Background()
	engine := &script.Engine{
		Conds: scripttest.DefaultConds(),
		Cmds:  scripttest.DefaultCmds(),
		Quiet: !testing.Verbose(),
	}
	env := os.Environ()
	scripttest.Test(t, ctx, func(t testing.TB) *script.Engine { return engine }, env, "testdata/*.txt")
}
