// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cell

import (
	"fmt"
	"strings"

	"github.com/cilium/hive/internal"
	"go.uber.org/dig"
)

type Inspection struct {
	Modules      []InspectModule
	Constructors []InspectConstructor
	Invokers     []InspectInvoker
	Decorators   []InspectDecorator
}

type InspectModule struct {
	ID          string
	Description string
	Path        string
	Parent      string
}

type InspectConstructor struct {
	ID         string
	Name       string
	ModulePath string
	Exported   bool
	Inputs     []InspectParam
	Outputs    []InspectParam
}

type InspectInvoker struct {
	ID         string
	Name       string
	ModulePath string
	Inputs     []InspectParam
}

type InspectDecorator struct {
	ID         string
	Name       string
	ModulePath string
	Inputs     []InspectParam
	Outputs    []InspectParam
	Global     bool
}

type InspectParam struct {
	Type     string
	Name     string
	Group    string
	Optional bool
}

// Inspect walks the cell tree and returns structured metadata for graph export.
func Inspect(cells ...Cell) Inspection {
	inspection := Inspection{}
	inspectCells(&inspection, cells, "")
	return inspection
}

type inspector interface {
	inspect(*Inspection, string)
}

func inspectCells(inspection *Inspection, cells []Cell, modulePath string) {
	for _, c := range cells {
		if ins, ok := c.(inspector); ok {
			ins.inspect(inspection, modulePath)
		}
	}
}

func (m *module) inspect(inspection *Inspection, parent string) {
	path := joinModulePath(parent, m.id)
	inspection.Modules = append(inspection.Modules, InspectModule{
		ID:          m.id,
		Description: m.description,
		Path:        path,
		Parent:      parent,
	})
	inspectCells(inspection, m.cells, path)
}

func (g group) inspect(inspection *Inspection, modulePath string) {
	inspectCells(inspection, g, modulePath)
}

func (p *provider) inspect(inspection *Inspection, modulePath string) {
	p.infosMu.Lock()
	defer p.infosMu.Unlock()

	for i, ctor := range p.ctors {
		var info *dig.ProvideInfo
		if i < len(p.infos) {
			info = &p.infos[i]
		}
		name := internal.FuncNameAndLocation(ctor)
		id := provideID(info, name, modulePath, i)
		var inputs []*dig.Input
		var outputs []*dig.Output
		if info != nil {
			inputs = info.Inputs
			outputs = info.Outputs
		}
		inspection.Constructors = append(inspection.Constructors, InspectConstructor{
			ID:         id,
			Name:       name,
			ModulePath: modulePath,
			Exported:   p.export,
			Inputs:     parseInputs(inputs),
			Outputs:    parseOutputs(outputs),
		})
	}
}

func (inv *invoker) inspect(inspection *Inspection, modulePath string) {
	for i := range inv.funcs {
		namedFunc := &inv.funcs[i]
		namedFunc.infoMu.Lock()
		info := namedFunc.info
		namedFunc.infoMu.Unlock()

		name := namedFunc.name
		id := invokeID(name, modulePath, i)
		var inputs []*dig.Input
		if info != nil {
			inputs = info.Inputs
		}
		inspection.Invokers = append(inspection.Invokers, InspectInvoker{
			ID:         id,
			Name:       name,
			ModulePath: modulePath,
			Inputs:     parseInputs(inputs),
		})
	}
}

func (d *decorator) inspect(inspection *Inspection, modulePath string) {
	d.infoMu.Lock()
	info := d.info
	d.infoMu.Unlock()

	name := internal.FuncNameAndLocation(d.decorator)
	id := decorateID(info, name, modulePath, false)
	var inputs []*dig.Input
	var outputs []*dig.Output
	if info != nil {
		inputs = info.Inputs
		outputs = info.Outputs
	}
	inspection.Decorators = append(inspection.Decorators, InspectDecorator{
		ID:         id,
		Name:       name,
		ModulePath: modulePath,
		Inputs:     parseInputs(inputs),
		Outputs:    parseOutputs(outputs),
		Global:     false,
	})
	inspectCells(inspection, d.cells, modulePath)
}

func (d *allDecorator) inspect(inspection *Inspection, modulePath string) {
	d.infoMu.Lock()
	info := d.info
	d.infoMu.Unlock()

	name := internal.FuncNameAndLocation(d.decorator)
	id := decorateID(info, name, modulePath, true)
	var inputs []*dig.Input
	var outputs []*dig.Output
	if info != nil {
		inputs = info.Inputs
		outputs = info.Outputs
	}
	inspection.Decorators = append(inspection.Decorators, InspectDecorator{
		ID:         id,
		Name:       name,
		ModulePath: modulePath,
		Inputs:     parseInputs(inputs),
		Outputs:    parseOutputs(outputs),
		Global:     true,
	})
}

func (c *config[Cfg]) inspect(inspection *Inspection, modulePath string) {
	c.infoMu.Lock()
	info := c.info
	ctor := c.ctor
	c.infoMu.Unlock()
	if info == nil || ctor == nil {
		return
	}

	name := internal.FuncNameAndLocation(ctor)
	id := provideID(info, name, modulePath, 0)
	inspection.Constructors = append(inspection.Constructors, InspectConstructor{
		ID:         id,
		Name:       name,
		ModulePath: modulePath,
		Exported:   true,
		Inputs:     parseInputs(info.Inputs),
		Outputs:    parseOutputs(info.Outputs),
	})
}

func joinModulePath(parent, id string) string {
	if parent == "" {
		return id
	}
	return parent + "." + id
}

func provideID(info *dig.ProvideInfo, name, modulePath string, index int) string {
	if info != nil && info.ID != 0 {
		return fmt.Sprintf("ctor:%d", info.ID)
	}
	return fmt.Sprintf("ctor:%s:%d:%s", modulePath, index, name)
}

func decorateID(info *dig.DecorateInfo, name, modulePath string, global bool) string {
	if info != nil && info.ID != 0 {
		return fmt.Sprintf("decorator:%d", info.ID)
	}
	prefix := "decorator"
	if global {
		prefix = "decorator-global"
	}
	return fmt.Sprintf("%s:%s:%s", prefix, modulePath, name)
}

func invokeID(name, modulePath string, index int) string {
	return fmt.Sprintf("invoke:%s:%d:%s", modulePath, index, name)
}

func parseInputs(inputs []*dig.Input) []InspectParam {
	if len(inputs) == 0 {
		return nil
	}
	out := make([]InspectParam, 0, len(inputs))
	for _, input := range inputs {
		out = append(out, parseDigParam(input.String()))
	}
	return out
}

func parseOutputs(outputs []*dig.Output) []InspectParam {
	if len(outputs) == 0 {
		return nil
	}
	out := make([]InspectParam, 0, len(outputs))
	for _, output := range outputs {
		out = append(out, parseDigParam(output.String()))
	}
	return out
}

func parseDigParam(value string) InspectParam {
	param := InspectParam{Type: strings.TrimSpace(value)}
	idx := strings.LastIndex(value, "[")
	if idx < 0 || !strings.HasSuffix(value, "]") {
		return param
	}
	tags := value[idx+1 : len(value)-1]
	if !strings.Contains(tags, "optional") &&
		!strings.Contains(tags, "name = ") &&
		!strings.Contains(tags, "group = ") {
		return param
	}
	param.Type = strings.TrimSpace(value[:idx])
	for _, token := range strings.Split(tags, ",") {
		token = strings.TrimSpace(token)
		switch {
		case token == "optional":
			param.Optional = true
		case strings.HasPrefix(token, "name = "):
			param.Name = strings.Trim(strings.TrimPrefix(token, "name = "), "\"")
		case strings.HasPrefix(token, "group = "):
			param.Group = strings.Trim(strings.TrimPrefix(token, "group = "), "\"")
		}
	}
	return param
}
