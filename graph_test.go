// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package hive_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/cilium/hive"
	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
	"github.com/stretchr/testify/require"
)

type graphA struct{}
type graphB struct{}

func TestInspectGraphEdges(t *testing.T) {
	h := hive.New(
		cell.Provide(func() *graphA { return &graphA{} }),
		cell.Provide(func(a *graphA) *graphB { return &graphB{} }),
		cell.Invoke(func(b *graphB) {}),
	)

	graph, err := h.InspectGraph(hivetest.Logger(t))
	require.NoError(t, err)

	typeA := reflect.TypeOf((*graphA)(nil)).String()
	typeB := reflect.TypeOf((*graphB)(nil)).String()

	objAID := findObjectByType(t, graph, typeA)
	objBID := findObjectByType(t, graph, typeB)

	ctorAID := findCtorByOutputType(t, graph, typeA)
	ctorBID := findCtorByOutputType(t, graph, typeB)
	invokerID := findInvokerByInputType(t, graph, typeB)

	require.Contains(t, graph.Objects[objAID].ProvidedBy, ctorAID)
	require.Contains(t, graph.Objects[objAID].ConsumedBy, ctorBID)
	require.Contains(t, graph.Objects[objBID].ProvidedBy, ctorBID)
	require.Contains(t, graph.Objects[objBID].ConsumedBy, invokerID)

	require.True(t, hasEdge(graph, ctorAID, objAID, "provides"))
	require.True(t, hasEdge(graph, objAID, ctorBID, "depends"))
	require.True(t, hasEdge(graph, ctorBID, objBID, "provides"))
	require.True(t, hasEdge(graph, objBID, invokerID, "invokes"))
}

func TestInspectGraphStableIDs(t *testing.T) {
	h := hive.New(
		cell.Provide(func() *graphA { return &graphA{} }),
		cell.Invoke(func(*graphA) {}),
	)

	graph1, err := h.InspectGraph(hivetest.Logger(t))
	require.NoError(t, err)
	graph2, err := h.InspectGraph(hivetest.Logger(t))
	require.NoError(t, err)

	require.Equal(t, sortedKeys(graph1.Objects), sortedKeys(graph2.Objects))
	require.Equal(t, sortedKeys(graph1.Constructors), sortedKeys(graph2.Constructors))
	require.Equal(t, sortedKeys(graph1.Invokers), sortedKeys(graph2.Invokers))
}

func findObjectByType(t *testing.T, graph *hive.Graph, typeName string) string {
	t.Helper()
	for id, obj := range graph.Objects {
		if obj.Type == typeName {
			return id
		}
	}
	t.Fatalf("object with type %q not found", typeName)
	return ""
}

func findCtorByOutputType(t *testing.T, graph *hive.Graph, typeName string) string {
	t.Helper()
	for id, ctor := range graph.Constructors {
		for _, out := range ctor.Outputs {
			if out.Type == typeName {
				return id
			}
		}
	}
	t.Fatalf("constructor output for type %q not found", typeName)
	return ""
}

func findInvokerByInputType(t *testing.T, graph *hive.Graph, typeName string) string {
	t.Helper()
	for id, inv := range graph.Invokers {
		for _, in := range inv.Inputs {
			if in.Type == typeName {
				return id
			}
		}
	}
	t.Fatalf("invoker input for type %q not found", typeName)
	return ""
}

func hasEdge(graph *hive.Graph, from, to, kind string) bool {
	for _, edge := range graph.Edges {
		if edge.From == from && edge.To == to && edge.Kind == kind {
			return true
		}
	}
	return false
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
