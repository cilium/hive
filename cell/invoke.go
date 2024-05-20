// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cell

import (
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/dig"

	"github.com/cilium/hive/internal"
)

type invoker struct {
	// probe denotes if this invoke is a "cell.Probe" which is run before
	// the lifecycle phase, but not during
	probe bool
	//funcs []namedFunc[dig.InvokeInfo]
	funcs []invokerNamedFunc
}

type namedFunc[InfoType any] struct {
	name string
	fn   any

	infoMu sync.Mutex
	info   *InfoType
}

type invokerNamedFunc = namedFunc[dig.InvokeInfo]

type InvokerList interface {
	AppendInvoke(fn func(*slog.Logger, time.Duration) error)
}

type ProberList interface {
	AppendProbe(fn func(*slog.Logger, time.Duration) error)
}

func (inv *invoker) invoke(log *slog.Logger, cont container, logThreshold time.Duration) error {
	for i := range inv.funcs {
		nf := &inv.funcs[i]
		log.Debug("Invoking", "function", nf.name)
		t0 := time.Now()

		var opts []dig.InvokeOption
		nf.infoMu.Lock()
		if nf.info == nil {
			nf.info = &dig.InvokeInfo{}
			opts = []dig.InvokeOption{
				dig.FillInvokeInfo(nf.info),
			}
		}
		defer inv.funcs[i].infoMu.Unlock()

		if err := cont.Invoke(nf.fn, opts...); err != nil {
			log.Error("Invoke failed", "error", err, "function", nf.name)
			return err
		}
		d := time.Since(t0)
		if d > logThreshold {
			log.Info("Invoked", "duration", d, "function", nf.name)
		} else {
			log.Debug("Invoked", "duration", d, "function", nf.name)
		}
	}
	return nil
}

func (inv *invoker) Apply(c container) error {
	// Remember the scope in which we need to invoke.
	// This func is just a wrapper for some logging stuff, by doing inv.invoke we're really running
	// container.Invoke(inv.fn, ...)
	// This means that when this function is called, we give the hive a Invoke to do with our fn.
	// For probe, we'll want to do something similar, but we'll instead do a Provide.
	invoker := func(log *slog.Logger, logThreshold time.Duration) error { return inv.invoke(log, c, logThreshold) }

	// Append the invoker to the list of invoke functions. These are invoked
	// prior to start to build up the objects. They are not invoked directly
	// here as first the configuration flags need to be registered. This allows
	// using hives in a command-line application with many commands and where
	// we don't yet know which command to run, but we still need to register
	// all the flags.
	return c.Invoke(func(l InvokerList) {
		l.AppendInvoke(invoker)
	})
}

func (inv *invoker) Info(container) Info {
	n := NewInfoNode("")
	for i := range inv.funcs {
		namedFunc := &inv.funcs[i]
		namedFunc.infoMu.Lock()
		defer namedFunc.infoMu.Unlock()

		invNode := NewInfoNode(fmt.Sprintf("🛠️ %s", namedFunc.name))
		invNode.condensed = true

		var ins []string
		for _, input := range namedFunc.info.Inputs {
			ins = append(ins, input.String())
		}
		sort.Strings(ins)
		invNode.AddLeaf("⇨ %s", strings.Join(ins, ", "))
		n.Add(invNode)
	}
	return n
}

// Invoke constructs a cell for invoke functions. The invoke functions are executed
// when the hive is started to instantiate all objects via the constructors.
func Invoke(funcs ...any) Cell {
	return invoke(false, funcs...)
}

func invoke(probe bool, funcs ...any) Cell {
	namedFuncs := []invokerNamedFunc{}
	for _, fn := range funcs {
		namedFuncs = append(
			namedFuncs,
			invokerNamedFunc{name: internal.FuncNameAndLocation(fn), fn: fn})
	}
	return &invoker{funcs: namedFuncs, probe: probe}
}

func Probe(funcs ...any) Cell {
	return invokeProbe(funcs...)
}

type provideNamedFunc namedFunc[dig.ProvideInfo]

type probe struct {
	funcs []provideNamedFunc
}

func invokeProbe(funcs ...any) Cell {
	namedFuncs := []provideNamedFunc{}
	for _, fn := range funcs {
		namedFuncs = append(
			namedFuncs,
			provideNamedFunc{name: internal.FuncNameAndLocation(fn), fn: fn})
	}
	return &probe{funcs: namedFuncs}
}

// Apply the prober to the list of probers
func (p *probe) Apply(c container) error {
	// Remember the scope in which we need to invoke.
	prober := func(log *slog.Logger, logThreshold time.Duration) error { return p.apply(log, c, logThreshold) }

	// Append the invoker to the list of invoke functions. These are invoked
	// prior to start to build up the objects. They are not invoked directly
	// here as first the configuration flags need to be registered. This allows
	// using hives in a command-line application with many commands and where
	// we don't yet know which command to run, but we still need to register
	// all the flags.

	// Appends to a list of probes, these are provided after config has been created
	// but before invoke, when running hive.
	// TODO: Need to have some way to only run this on probe.
	// By invoking this, we're adding this to a list of things to invoke later (in the hive).
	return c.Invoke(func(l ProberList) {
		l.AppendProbe(prober)
	})
}

func (p *probe) apply(log *slog.Logger, cont container, logThreshold time.Duration) error {
	for i := range p.funcs {
		nf := &p.funcs[i]
		log.Debug("Providing Config Probe", "function", nf.name)

		v := reflect.ValueOf(nf.fn)
		// Check that the config override is of type func(*cfg) and
		// 'cfg' implements Flagger.
		t := v.Type()
		if t.Kind() != reflect.Func || t.NumIn() != 1 {
			return fmt.Errorf("config override has invalid type %T, expected func(*T)", nf.fn)
		}
		flaggerType := reflect.TypeOf((*Flagger)(nil)).Elem()
		if !t.In(0).Implements(flaggerType) {
			return fmt.Errorf("config override function parameter (%T) does not implement Flagger", nf.fn)
		}

		// Construct the provider function: 'func() func(*cfg)'. This is
		// picked up by the config cell and called to mutate the config
		// after it has been parsed.
		providerFunc := func(in []reflect.Value) []reflect.Value {
			return []reflect.Value{v}
		}
		providerFuncType := reflect.FuncOf(nil, []reflect.Type{t}, false)
		pfv := reflect.MakeFunc(providerFuncType, providerFunc)
		if err := cont.Provide(pfv.Interface()); err != nil {
			return fmt.Errorf("providing config override failed: %w", err)
		}

		t0 := time.Now()

		var opts []dig.ProvideOption
		nf.infoMu.Lock()
		if nf.info == nil {
			nf.info = &dig.ProvideInfo{}
			opts = []dig.ProvideOption{
				dig.FillProvideInfo(nf.info)}
		}
		defer p.funcs[i].infoMu.Unlock()

		if err := cont.Provide(nf.fn, opts...); err != nil {
			log.Error("Failed to provide config probe", "error", err, "function", nf.name)
			return err
		}
		d := time.Since(t0)
		if d > logThreshold {
			log.Info("Probed", "duration", d, "function", nf.name)
		} else {
			log.Debug("Probed", "duration", d, "function", nf.name)
		}

	}
	return nil
}

func (p *probe) Info(container) Info {
	n := NewInfoNode("")
	for i := range p.funcs {
		namedFunc := &p.funcs[i]
		namedFunc.infoMu.Lock()
		defer namedFunc.infoMu.Unlock()

		invNode := NewInfoNode(fmt.Sprintf("🛰️ %s", namedFunc.name))
		invNode.condensed = true

		var ins []string
		for _, input := range namedFunc.info.Inputs {
			ins = append(ins, input.String())
		}
		sort.Strings(ins)
		invNode.AddLeaf("⇨ %s", strings.Join(ins, ", "))
		n.Add(invNode)
	}
	return n
}
