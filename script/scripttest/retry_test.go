// Copyright 2026 The Cilium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package scripttest_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/cilium/hive/script"
	"github.com/cilium/hive/script/scripttest"
	"github.com/stretchr/testify/require"
)

func TestRetrySkipSignal(t *testing.T) {
	for _, source := range []string{"retry { skip reason }", "retry --until-failure { skip reason }", "! retry { skip reason }"} {
		t.Run(source, func(t *testing.T) {
			e := script.NewEngine()
			_, want := scripttest.Skip().Run(nil, "reason")
			calls := 0
			e.Cmds["skip"] = script.Command(script.CmdUsage{}, func(*script.State, ...string) (script.WaitFunc, error) { calls++; return nil, want })
			s, err := script.NewState(context.Background(), t.TempDir(), nil)
			require.NoError(t, err)
			defer s.CloseAndWait(io.Discard)
			err = e.ExecuteLine(s, source, io.Discard)
			require.True(t, errors.Is(err, want))
			require.Equal(t, 1, calls)
		})
	}
}
