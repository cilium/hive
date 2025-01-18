// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cell_test

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"go.uber.org/dig"

	"github.com/cilium/hive/cell"
)

type testConfig1 struct {
	OverlappingFlag string
}

func (cfg testConfig1) Flags(flags *pflag.FlagSet) {
	flags.String("overlapping-flag", "", "Flag that conflicts with another cell")
}

type testConfig2 struct {
	testConfig1
}

func TestDuplicateFlags(t *testing.T) {
	orderedCells := []cell.Cell{
		cell.Config(testConfig1{}),
		cell.Config(testConfig2{}),
	}
	expErrMsgs := []error{
		nil,                   // testConfig1 is fine
		cell.ErrDuplicateFlag, // Adding testConfig2 creates duplicate flag
	}

	container := dig.New(dig.DeferAcyclicVerification())
	container.Provide(func() *pflag.FlagSet {
		return pflag.NewFlagSet("", pflag.ContinueOnError)
	})

	for i, cell := range orderedCells {
		err := cell.Apply(container, container)
		assert.ErrorIs(t, err, expErrMsgs[i])
	}

}
