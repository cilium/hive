// Copyright 2026 The Cilium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package script

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWhenAndUnlessTrueValues(t *testing.T) {
	for _, value := range []string{"1", "t", "T", "true", "TRUE"} {
		t.Run(value, func(t *testing.T) {
			whenCalled := false
			err := When().Run(nil, []string{value}, func(map[string]string) error {
				whenCalled = true
				return nil
			})
			require.NoError(t, err)
			require.True(t, whenCalled)

			unlessCalled := false
			err = Unless().Run(nil, []string{value}, func(map[string]string) error {
				unlessCalled = true
				return nil
			})
			require.NoError(t, err)
			require.False(t, unlessCalled)
		})
	}

	for _, value := range []string{"", "0", "f", "false", "yes"} {
		t.Run("false/"+value, func(t *testing.T) {
			whenCalled := false
			err := When().Run(nil, []string{value}, func(map[string]string) error {
				whenCalled = true
				return nil
			})
			require.NoError(t, err)
			require.False(t, whenCalled)

			unlessCalled := false
			err = Unless().Run(nil, []string{value}, func(map[string]string) error {
				unlessCalled = true
				return nil
			})
			require.NoError(t, err)
			require.True(t, unlessCalled)
		})
	}
}

func TestRetryPrefixesOnBlockCommands(t *testing.T) {
	for _, prefix := range []string{"*", "!*"} {
		t.Run(prefix, func(t *testing.T) {
			s, err := NewState(context.Background(), t.TempDir(), nil)
			require.NoError(t, err)
			defer s.CloseAndWait(io.Discard)

			err = NewEngine().Execute(s, "test", bufio.NewReader(strings.NewReader(prefix+" foreach x y {\n}\n")), io.Discard)
			require.ErrorContains(t, err, "retry prefixes cannot be used on block commands; use the retry block command instead")
		})
	}
}

func TestForeachEmptyValues(t *testing.T) {
	called := false
	err := Foreach().Run(nil, []string{"item"}, func(map[string]string) error { called = true; return nil })
	require.NoError(t, err)
	require.False(t, called)
}
