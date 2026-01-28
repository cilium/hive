// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cell

import (
	"fmt"
	"sync"

	"github.com/cilium/hive/internal"
	"go.uber.org/dig"
)

// Decorate takes a decorator function and a set of cells and returns
// a decorator cell.
//
// A decorator function is a function that takes as arguments objects
// in the hive and returns one or more augmented objects. The cells wrapped
// with a decorator will be provided the returned augmented objects.
//
// Example:
//
//	cell.Decorate(
//		func(e Example) Example {
//			return e.WithMoreMagic()
//		},
//		cell.Invoke(func(e Example) {
//			// e now has more magic
//		},
//	)
func Decorate(dtor any, cells ...Cell) Cell {
	return &decorator{
		decorator: dtor,
		cells:     cells,
	}
}

type decorator struct {
	decorator any
	cells     []Cell

	infoMu sync.Mutex
	info   *dig.DecorateInfo
}

func (d *decorator) Apply(c container, rc rootContainer) error {
	scope := c.Scope(fmt.Sprintf("(decorate %s)", internal.PrettyType(d.decorator)))

	d.infoMu.Lock()
	opts := []dig.DecorateOption{}
	if d.info == nil {
		d.info = &dig.DecorateInfo{}
		opts = append(opts, dig.FillDecorateInfo(d.info))
	}
	if err := scope.Decorate(d.decorator, opts...); err != nil {
		d.infoMu.Unlock()
		return err
	}
	d.infoMu.Unlock()

	for _, cell := range d.cells {
		if err := cell.Apply(scope, rc); err != nil {
			return err
		}
	}

	return nil
}

func (d *decorator) Info(c container) Info {
	n := NewInfoNode(fmt.Sprintf("🔀 %s: %s", internal.FuncNameAndLocation(d.decorator), internal.PrettyType(d.decorator)))
	for _, cell := range d.cells {
		n.Add(cell.Info(c))
	}
	return n
}

// DecorateAll takes a decorator function and applies the decoration globally.
//
// Example:
//
//		cell.Module(
//		  "my-app",
//		  "My application",
//		    foo.Cell, // provides foo.Foo
//		    bar.Cell,
//
//	       // Wrap 'foo.Foo' everywhere, including inside foo.Cell.
//		   cell.DecorateAll(
//		     func(f foo.Foo) foo.Foo {
//		       return myFooWrapper{f}
//		     },
//		   ),
//		)
func DecorateAll(dtor any) Cell {
	return &allDecorator{decorator: dtor}
}

type allDecorator struct {
	decorator any

	infoMu sync.Mutex
	info   *dig.DecorateInfo
}

func (d *allDecorator) Apply(_ container, rc rootContainer) error {
	d.infoMu.Lock()
	opts := []dig.DecorateOption{}
	if d.info == nil {
		d.info = &dig.DecorateInfo{}
		opts = append(opts, dig.FillDecorateInfo(d.info))
	}
	err := rc.Decorate(d.decorator, opts...)
	d.infoMu.Unlock()
	return err
}

func (d *allDecorator) Info(_ container) Info {
	n := NewInfoNode(fmt.Sprintf("🔀* %s: %s", internal.FuncNameAndLocation(d.decorator), internal.PrettyType(d.decorator)))
	return n
}
