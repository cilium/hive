// Copyright 2026 The Cilium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package script

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestInputDelimiters(t *testing.T) {
	for _, tc := range []struct {
		input       string
		depth       int
		uncommented string
	}{
		{"foreach x A {", 1, "foreach x A {"},
		{"foreach x A { # ignored }", 1, "foreach x A { "},
		{"echo ${NAME}", 0, "echo ${NAME}"},
		{"foreach x A { echo ${x}; echo 'it''s { # }' }", 0, "foreach x A { echo ${x}; echo 'it''s { # }' }"},
		{"when true { foreach x A {", 2, "when true { foreach x A {"},
		{"echo a{b}c # comment {", 0, "echo a{b}c "},
		{"}", -1, "}"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			require.Equal(t, tc.depth, BlockDepth(tc.input))
			require.Equal(t, tc.uncommented, StripComment(tc.input))
		})
	}
}
