// Copyright 2026 The Cilium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package script

import (
	"context"
	"errors"
)

// TerminationError ends script execution regardless of command status prefixes
// or retry blocks. Commands such as stop and the testing harness's skip use it
// to propagate control flow through nested block invocations.
type TerminationError interface {
	error
	ScriptTermination()
}

// setupError distinguishes invalid engine requests from command failures. A
// surrounding status assertion or retry must not hide lookup or flag errors.
type setupError struct{ error }

func (e *setupError) Unwrap() error { return e.error }

func unrecoverableError(err error) bool {
	var termination TerminationError
	var setup *setupError
	return errors.As(err, &termination) || errors.As(err, &setup) || errors.Is(err, ParseError) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
