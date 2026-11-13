// Copyright 2026 The Cilium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package scripttest_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cilium/hive/script"
	"github.com/cilium/hive/script/scripttest"
	"github.com/stretchr/testify/require"
)

func TestParallelSharedEngine(t *testing.T) {
	engine := &script.Engine{Cmds: scripttest.DefaultCmds()}
	t.Run("parallel", func(t *testing.T) {
		for i := range 16 {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				s, err := script.NewState(context.Background(), t.TempDir(), nil)
				require.NoError(t, err)
				t.Parallel()
				scripttest.Run(t, engine, s, "shared", strings.NewReader("echo ok"))
			})
		}
	})
	require.Nil(t, engine.BlockCmds, "Run must not modify the shared engine configuration")
}
