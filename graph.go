// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package hive

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"

	"github.com/cilium/hive/cell"
)

const GraphVersion = "v1"
const GraphRootModule = "__root__"

type Graph struct {
	Version      string                   `json:"version"`
	RootModules  []string                 `json:"rootModules,omitempty"`
	Modules      map[string]*GraphModule  `json:"modules"`
	Constructors map[string]*GraphCtor    `json:"constructors"`
	Objects      map[string]*GraphObject  `json:"objects"`
	Invokers     map[string]*GraphInvoker `json:"invokers"`
	Decorators   map[string]*GraphDecor   `json:"decorators"`
	Edges        []GraphEdge              `json:"edges"`
}

type GraphModule struct {
	ID           string   `json:"id"`
	Description  string   `json:"description"`
	Path         string   `json:"path"`
	Parent       string   `json:"parent,omitempty"`
	Children     []string `json:"children,omitempty"`
	Constructors []string `json:"constructors,omitempty"`
	Objects      []string `json:"objects,omitempty"`
	Invokers     []string `json:"invokers,omitempty"`
	Decorators   []string `json:"decorators,omitempty"`
}

type GraphCtor struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	ModulePath string           `json:"modulePath,omitempty"`
	Exported   bool             `json:"exported"`
	Inputs     []GraphObjectRef `json:"inputs,omitempty"`
	Outputs    []GraphObjectRef `json:"outputs,omitempty"`
}

type GraphObject struct {
	ID         string   `json:"id"`
	Type       string   `json:"type"`
	Name       string   `json:"name,omitempty"`
	Group      string   `json:"group,omitempty"`
	ModulePath string   `json:"modulePath,omitempty"`
	Exported   bool     `json:"exported"`
	ProvidedBy []string `json:"providedBy,omitempty"`
	ConsumedBy []string `json:"consumedBy,omitempty"`
}

type GraphInvoker struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	ModulePath string           `json:"modulePath,omitempty"`
	Inputs     []GraphObjectRef `json:"inputs,omitempty"`
}

type GraphDecor struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	ModulePath string           `json:"modulePath,omitempty"`
	Global     bool             `json:"global"`
	Inputs     []GraphObjectRef `json:"inputs,omitempty"`
	Outputs    []GraphObjectRef `json:"outputs,omitempty"`
}

type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

type GraphObjectRef struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Name     string `json:"name,omitempty"`
	Group    string `json:"group,omitempty"`
	Optional bool   `json:"optional,omitempty"`
}

// InspectGraph extracts a structured dependency graph from the hive.
func (h *Hive) InspectGraph(log *slog.Logger) (*Graph, error) {
	if err := h.Populate(log); err != nil {
		return nil, err
	}
	inspection := cell.Inspect(h.cells...)
	builder := newGraphBuilder()
	return builder.Build(inspection), nil
}

// PrintGraphJSON writes the dependency graph as JSON to the given writer.
func (h *Hive) PrintGraphJSON(w io.Writer, log *slog.Logger) error {
	graph, err := h.InspectGraph(log)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(graph)
}

type graphBuilder struct {
	graph              *Graph
	objectByKey        map[string]string
	objectsBySignature map[string][]string
	edgeSet            map[string]struct{}
	idCounts           map[string]int
}

func newGraphBuilder() *graphBuilder {
	return &graphBuilder{
		graph: &Graph{
			Version:      GraphVersion,
			Modules:      map[string]*GraphModule{},
			Constructors: map[string]*GraphCtor{},
			Objects:      map[string]*GraphObject{},
			Invokers:     map[string]*GraphInvoker{},
			Decorators:   map[string]*GraphDecor{},
		},
		objectByKey:        map[string]string{},
		objectsBySignature: map[string][]string{},
		edgeSet:            map[string]struct{}{},
		idCounts:           map[string]int{},
	}
}

func (b *graphBuilder) Build(inspection cell.Inspection) *Graph {
	b.addModules(inspection.Modules)

	ctorIDs := b.addConstructors(inspection.Constructors)
	decorIDs := b.addDecorators(inspection.Decorators)
	invokerIDs := b.addInvokers(inspection.Invokers)

	b.addConstructorOutputs(inspection.Constructors, ctorIDs)
	b.addDecoratorOutputs(inspection.Decorators, decorIDs)

	b.addConstructorInputs(inspection.Constructors, ctorIDs)
	b.addDecoratorInputs(inspection.Decorators, decorIDs)
	b.addInvokerInputs(inspection.Invokers, invokerIDs)

	b.normalize()
	return b.graph
}

func (b *graphBuilder) addModules(modules []cell.InspectModule) {
	for _, mod := range modules {
		b.graph.Modules[mod.Path] = &GraphModule{
			ID:          mod.ID,
			Description: mod.Description,
			Path:        mod.Path,
			Parent:      mod.Parent,
		}
	}
	for path, mod := range b.graph.Modules {
		if mod.Parent == "" {
			b.graph.RootModules = append(b.graph.RootModules, path)
			continue
		}
		parent, ok := b.graph.Modules[mod.Parent]
		if !ok {
			continue
		}
		parent.Children = appendUnique(parent.Children, path)
	}
}

func (b *graphBuilder) addConstructors(constructors []cell.InspectConstructor) []string {
	ids := make([]string, len(constructors))
	for i, ctor := range constructors {
		base := ctor.ID
		if base == "" {
			base = ctor.Name
		}
		id := b.uniqueID("ctor:" + base)
		ids[i] = id
		b.graph.Constructors[id] = &GraphCtor{
			ID:         id,
			Name:       ctor.Name,
			ModulePath: ctor.ModulePath,
			Exported:   ctor.Exported,
		}
		b.addModuleConstructor(ctor.ModulePath, id)
	}
	return ids
}

func (b *graphBuilder) addInvokers(invokers []cell.InspectInvoker) []string {
	ids := make([]string, len(invokers))
	for i, inv := range invokers {
		base := inv.ID
		if base == "" {
			base = inv.Name
		}
		id := b.uniqueID("invoke:" + base)
		ids[i] = id
		b.graph.Invokers[id] = &GraphInvoker{
			ID:         id,
			Name:       inv.Name,
			ModulePath: inv.ModulePath,
		}
		b.addModuleInvoker(inv.ModulePath, id)
	}
	return ids
}

func (b *graphBuilder) addDecorators(decorators []cell.InspectDecorator) []string {
	ids := make([]string, len(decorators))
	for i, decor := range decorators {
		base := decor.ID
		if base == "" {
			base = decor.Name
		}
		id := b.uniqueID("decorator:" + base)
		ids[i] = id
		b.graph.Decorators[id] = &GraphDecor{
			ID:         id,
			Name:       decor.Name,
			ModulePath: decor.ModulePath,
			Global:     decor.Global,
		}
		b.addModuleDecorator(decor.ModulePath, id)
	}
	return ids
}

func (b *graphBuilder) addConstructorOutputs(constructors []cell.InspectConstructor, ids []string) {
	for i, ctor := range constructors {
		node := b.graph.Constructors[ids[i]]
		for _, output := range ctor.Outputs {
			ref := b.toObjectRef(output)
			objID := b.ensureProvidedObject(ref, ctor.ModulePath)
			ref.ID = objID
			node.Outputs = append(node.Outputs, ref)
			b.addEdge(ids[i], objID, "provides")
			b.appendProvidedBy(objID, ids[i])
		}
	}
}

func (b *graphBuilder) addDecoratorOutputs(decorators []cell.InspectDecorator, ids []string) {
	for i, decor := range decorators {
		node := b.graph.Decorators[ids[i]]
		for _, output := range decor.Outputs {
			ref := b.toObjectRef(output)
			objID := b.ensureProvidedObject(ref, decor.ModulePath)
			ref.ID = objID
			node.Outputs = append(node.Outputs, ref)
			b.addEdge(ids[i], objID, "provides")
			b.appendProvidedBy(objID, ids[i])
		}
	}
}

func (b *graphBuilder) addConstructorInputs(constructors []cell.InspectConstructor, ids []string) {
	for i, ctor := range constructors {
		if isConfigConstructor(ctor) {
			continue
		}
		node := b.graph.Constructors[ids[i]]
		for _, input := range ctor.Inputs {
			ref := b.toObjectRef(input)
			objID := b.resolveOrCreateObject(ref, ctor.ModulePath)
			ref.ID = objID
			node.Inputs = append(node.Inputs, ref)
			b.addEdge(objID, ids[i], "depends")
			b.appendConsumedBy(objID, ids[i])
		}
	}
}

func (b *graphBuilder) addDecoratorInputs(decorators []cell.InspectDecorator, ids []string) {
	for i, decor := range decorators {
		node := b.graph.Decorators[ids[i]]
		for _, input := range decor.Inputs {
			ref := b.toObjectRef(input)
			objID := b.resolveOrCreateObject(ref, decor.ModulePath)
			ref.ID = objID
			node.Inputs = append(node.Inputs, ref)
			b.addEdge(objID, ids[i], "depends")
			b.appendConsumedBy(objID, ids[i])
		}
	}
}

func (b *graphBuilder) addInvokerInputs(invokers []cell.InspectInvoker, ids []string) {
	for i, inv := range invokers {
		node := b.graph.Invokers[ids[i]]
		for _, input := range inv.Inputs {
			ref := b.toObjectRef(input)
			objID := b.resolveOrCreateObject(ref, inv.ModulePath)
			ref.ID = objID
			node.Inputs = append(node.Inputs, ref)
			b.addEdge(objID, ids[i], "invokes")
			b.appendConsumedBy(objID, ids[i])
		}
	}
}

func (b *graphBuilder) ensureProvidedObject(ref GraphObjectRef, modulePath string) string {
	signature := objectSignature(ref)
	key := objectKey(signature, modulePath)
	if id, ok := b.objectByKey[key]; ok {
		return id
	}
	objID := key
	b.graph.Objects[objID] = &GraphObject{
		ID:         objID,
		Type:       ref.Type,
		Name:       ref.Name,
		Group:      ref.Group,
		ModulePath: modulePath,
	}
	b.objectByKey[key] = objID
	b.objectsBySignature[signature] = append(b.objectsBySignature[signature], objID)
	b.addModuleObject(modulePath, objID)
	return objID
}

func (b *graphBuilder) resolveOrCreateObject(ref GraphObjectRef, modulePath string) string {
	signature := objectSignature(ref)
	altSignature, altType := normalizeGroupSliceSignature(ref)
	if ids, ok := b.objectsBySignature[signature]; ok && len(ids) > 0 {
		if len(ids) == 1 {
			return ids[0]
		}
		best := b.selectBestObject(ids, modulePath)
		if best != "" {
			return best
		}
		return ids[0]
	}
	if altSignature != "" {
		if ids, ok := b.objectsBySignature[altSignature]; ok && len(ids) > 0 {
			if len(ids) == 1 {
				return ids[0]
			}
			best := b.selectBestObject(ids, modulePath)
			if best != "" {
				return best
			}
			return ids[0]
		}
	}
	objSignature := signature
	objType := ref.Type
	if altSignature != "" {
		objSignature = altSignature
		objType = altType
	}
	objID := objectKey(objSignature, GraphRootModule)
	b.graph.Objects[objID] = &GraphObject{
		ID:         objID,
		Type:       objType,
		Name:       ref.Name,
		Group:      ref.Group,
		ModulePath: GraphRootModule,
	}
	b.objectByKey[objID] = objID
	b.objectsBySignature[objSignature] = append(b.objectsBySignature[objSignature], objID)
	b.addModuleObject(GraphRootModule, objID)
	return objID
}

func (b *graphBuilder) selectBestObject(ids []string, modulePath string) string {
	best := ""
	bestLen := -1
	for _, id := range ids {
		obj := b.graph.Objects[id]
		if !isModulePrefix(obj.ModulePath, modulePath) {
			continue
		}
		if len(obj.ModulePath) > bestLen {
			best = id
			bestLen = len(obj.ModulePath)
		}
	}
	return best
}

func (b *graphBuilder) toObjectRef(param cell.InspectParam) GraphObjectRef {
	return GraphObjectRef{
		Type:     param.Type,
		Name:     param.Name,
		Group:    param.Group,
		Optional: param.Optional,
	}
}

func isConfigConstructor(ctor cell.InspectConstructor) bool {
	return strings.Contains(ctor.Name, "cell/config.go") ||
		strings.Contains(ctor.Name, "cell.(*config[")
}

func objectSignature(ref GraphObjectRef) string {
	return fmt.Sprintf("%s|%s|%s", ref.Type, ref.Name, ref.Group)
}

func normalizeGroupSliceSignature(ref GraphObjectRef) (string, string) {
	if ref.Group == "" || !strings.HasPrefix(ref.Type, "[]") {
		return "", ""
	}
	elemType := strings.TrimPrefix(ref.Type, "[]")
	return fmt.Sprintf("%s|%s|%s", elemType, ref.Name, ref.Group), elemType
}

func objectKey(signature, modulePath string) string {
	return signature + "|" + modulePath
}

func isModulePrefix(prefix, path string) bool {
	if prefix == "" {
		return true
	}
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+".")
}

func (b *graphBuilder) addEdge(from, to, kind string) {
	key := kind + "|" + from + "->" + to
	if _, ok := b.edgeSet[key]; ok {
		return
	}
	b.edgeSet[key] = struct{}{}
	b.graph.Edges = append(b.graph.Edges, GraphEdge{
		From: from,
		To:   to,
		Kind: kind,
	})
}

func (b *graphBuilder) addModuleConstructor(modulePath, id string) {
	if modulePath == "" {
		return
	}
	mod, ok := b.graph.Modules[modulePath]
	if !ok {
		return
	}
	mod.Constructors = appendUnique(mod.Constructors, id)
}

func (b *graphBuilder) addModuleInvoker(modulePath, id string) {
	if modulePath == "" {
		return
	}
	mod, ok := b.graph.Modules[modulePath]
	if !ok {
		return
	}
	mod.Invokers = appendUnique(mod.Invokers, id)
}

func (b *graphBuilder) addModuleDecorator(modulePath, id string) {
	if modulePath == "" {
		return
	}
	mod, ok := b.graph.Modules[modulePath]
	if !ok {
		return
	}
	mod.Decorators = appendUnique(mod.Decorators, id)
}

func (b *graphBuilder) addModuleObject(modulePath, id string) {
	if modulePath == "" {
		return
	}
	mod, ok := b.graph.Modules[modulePath]
	if !ok {
		return
	}
	mod.Objects = appendUnique(mod.Objects, id)
}

func (b *graphBuilder) appendProvidedBy(objectID, providerID string) {
	obj := b.graph.Objects[objectID]
	obj.ProvidedBy = appendUnique(obj.ProvidedBy, providerID)
}

func (b *graphBuilder) appendConsumedBy(objectID, consumerID string) {
	obj := b.graph.Objects[objectID]
	obj.ConsumedBy = appendUnique(obj.ConsumedBy, consumerID)
}

func (b *graphBuilder) uniqueID(base string) string {
	count := b.idCounts[base]
	if count == 0 {
		b.idCounts[base] = 1
		return base
	}
	count++
	b.idCounts[base] = count
	return fmt.Sprintf("%s#%d", base, count)
}

func (b *graphBuilder) normalize() {
	slices.Sort(b.graph.RootModules)
	for _, mod := range b.graph.Modules {
		slices.Sort(mod.Children)
		slices.Sort(mod.Constructors)
		slices.Sort(mod.Objects)
		slices.Sort(mod.Invokers)
		slices.Sort(mod.Decorators)
	}
	for _, obj := range b.graph.Objects {
		obj.Exported = b.objectExported(obj)
		slices.Sort(obj.ProvidedBy)
		slices.Sort(obj.ConsumedBy)
	}
	slices.SortFunc(b.graph.Edges, func(a, b GraphEdge) int {
		if a.Kind != b.Kind {
			return strings.Compare(a.Kind, b.Kind)
		}
		if a.From != b.From {
			return strings.Compare(a.From, b.From)
		}
		return strings.Compare(a.To, b.To)
	})
}

func (b *graphBuilder) objectExported(obj *GraphObject) bool {
	hasCtor := false
	for _, provider := range obj.ProvidedBy {
		ctor, ok := b.graph.Constructors[provider]
		if !ok {
			continue
		}
		hasCtor = true
		if ctor.Exported {
			return true
		}
	}
	if hasCtor {
		return false
	}
	// If we can't attribute the object to a constructor, treat it as exported.
	return true
}

func appendUnique(list []string, value string) []string {
	for _, existing := range list {
		if existing == value {
			return list
		}
	}
	return append(list, value)
}
