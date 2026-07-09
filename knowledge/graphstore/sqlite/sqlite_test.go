//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite

import (
	"context"
	"sort"
	"testing"

	"fmt"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New()
	require.NoError(t, err, "New() should succeed with defaults")
	t.Cleanup(func() { s.Close() })
	return s
}

func newTestStoreWithDSN(t *testing.T, dsn string) *Store {
	t.Helper()
	s, err := New(WithDSN(dsn))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func addTestNodes(t *testing.T, ctx context.Context, s *Store, nodes []*graph.Node) {
	t.Helper()
	err := s.AddNodes(ctx, nodes)
	require.NoError(t, err)
}

func addTestEdges(t *testing.T, ctx context.Context, s *Store, edges []*graph.Edge) {
	t.Helper()
	err := s.AddEdges(ctx, edges)
	require.NoError(t, err)
}

func nodeIDs(nodes []*graph.Node) []string {
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n != nil {
			ids = append(ids, n.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func edgeKeys(edges []*graph.Edge) []string {
	keys := make([]string, 0, len(edges))
	for _, e := range edges {
		if e != nil {
			keys = append(keys, e.FromID+":"+e.Type+":"+e.ToID)
		}
	}
	sort.Strings(keys)
	return keys
}

func pathNodeIDs(paths []*graph.Path) [][]string {
	result := make([][]string, len(paths))
	for i, p := range paths {
		result[i] = nodeIDs(p.Nodes)
	}
	return result
}

// ---- New / Close ----

func TestNew_DefaultOptions(t *testing.T) {
	s, err := New()
	assert.NoError(t, err)
	assert.NotNil(t, s)
	assert.Equal(t, ":memory:", s.opts.dsn)
	assert.Equal(t, "sqlite3", s.opts.driverName)
	assert.Equal(t, "graph_nodes", s.opts.nodeTableName)
	assert.Equal(t, "graph_edges", s.opts.edgeTableName)
	s.Close()
}

func TestNew_CustomOptions(t *testing.T) {
	s, err := New(
		WithNodeTableName("custom_nodes"),
		WithEdgeTableName("custom_edges"),
	)
	assert.NoError(t, err)
	assert.NotNil(t, s)
	assert.Equal(t, "custom_nodes", s.opts.nodeTableName)
	assert.Equal(t, "custom_edges", s.opts.edgeTableName)
	s.Close()
}

func TestNew_InvalidNodeTableName(t *testing.T) {
	assert.Panics(t, func() {
		New(WithNodeTableName("invalid-table-name"))
	})
}

func TestNew_InvalidEdgeTableName(t *testing.T) {
	assert.Panics(t, func() {
		New(WithEdgeTableName("123bad"))
	})
}

func TestNew_SkipDBInit(t *testing.T) {
	s, err := New(WithSkipDBInit(true))
	assert.NoError(t, err)
	assert.True(t, s.opts.skipDBInit)
	s.Close()
}

func TestNew_EmptyDSNPreservesDefault(t *testing.T) {
	s, err := New(WithDSN(""))
	assert.NoError(t, err)
	assert.Equal(t, ":memory:", s.opts.dsn)
	s.Close()
}

func TestClose_NilDB(t *testing.T) {
	s := &Store{}
	assert.NoError(t, s.Close())
}

// ---- AddNodes ----

func TestAddNodes_EmptySlice(t *testing.T) {
	s := newTestStore(t)
	err := s.AddNodes(context.Background(), []*graph.Node{})
	assert.NoError(t, err)
}

func TestAddNodes_NilSlice(t *testing.T) {
	s := newTestStore(t)
	err := s.AddNodes(context.Background(), nil)
	assert.NoError(t, err)
}

func TestAddNodes_NilElement(t *testing.T) {
	s := newTestStore(t)
	err := s.AddNodes(context.Background(), []*graph.Node{nil})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node at index 0 is nil")
}

func TestAddNodes_EmptyID(t *testing.T) {
	s := newTestStore(t)
	err := s.AddNodes(context.Background(), []*graph.Node{{ID: ""}})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node at index 0 has empty id")
}

func TestAddNodes_Success(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	err := s.AddNodes(ctx, []*graph.Node{
		{ID: "a", Name: "Node A", Content: "content-a", Metadata: map[string]any{"kind": "test"}},
		{ID: "b", Name: "Node B"},
	})
	assert.NoError(t, err)

	nodes, err := s.queryNodesByIDs(ctx, []string{"a", "b"})
	assert.NoError(t, err)
	assert.Len(t, nodes, 2)
}

func TestAddNodes_Upsert(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	addTestNodes(t, ctx, s, []*graph.Node{{ID: "a", Name: "Original"}})

	err := s.AddNodes(ctx, []*graph.Node{{ID: "a", Name: "Updated"}})
	assert.NoError(t, err)

	nodes, err := s.queryNodesByIDs(ctx, []string{"a"})
	assert.NoError(t, err)
	assert.Len(t, nodes, 1)
	assert.Equal(t, "Updated", nodes[0].Name)
}

func TestAddNodes_WithMetadata(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	addTestNodes(t, ctx, s, []*graph.Node{
		{ID: "x", Metadata: map[string]any{"key1": "val1", "num": float64(42)}},
	})

	nodes, err := s.queryNodesByIDs(ctx, []string{"x"})
	assert.NoError(t, err)
	assert.Len(t, nodes, 1)
	assert.Equal(t, "val1", nodes[0].Metadata["key1"])
}

func TestAddNodes_NilMetadata(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	addTestNodes(t, ctx, s, []*graph.Node{{ID: "y"}})

	nodes, err := s.queryNodesByIDs(ctx, []string{"y"})
	assert.NoError(t, err)
	assert.Len(t, nodes, 1)
	// nil metadata may come back as nil or empty depending on unmarshalMetadata
}

// ---- AddEdges ----

func TestAddEdges_EmptySlice(t *testing.T) {
	s := newTestStore(t)
	err := s.AddEdges(context.Background(), []*graph.Edge{})
	assert.NoError(t, err)
}

func TestAddEdges_NilSlice(t *testing.T) {
	s := newTestStore(t)
	err := s.AddEdges(context.Background(), nil)
	assert.NoError(t, err)
}

func TestAddEdges_NilElement(t *testing.T) {
	s := newTestStore(t)
	err := s.AddEdges(context.Background(), []*graph.Edge{nil})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "edge at index 0 is nil")
}

func TestAddEdges_EmptyEndpoint(t *testing.T) {
	s := newTestStore(t)
	err := s.AddEdges(context.Background(), []*graph.Edge{
		{FromID: "", ToID: "b", Type: "CALLS"},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty endpoint")
}

func TestAddEdges_EmptyType(t *testing.T) {
	s := newTestStore(t)
	err := s.AddEdges(context.Background(), []*graph.Edge{
		{FromID: "a", ToID: "b", Type: ""},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty type")
}

func TestAddEdges_Success(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{{ID: "a"}, {ID: "b"}})

	err := s.AddEdges(ctx, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS", Metadata: map[string]any{"weight": float64(1)}},
	})
	assert.NoError(t, err)
}

func TestAddEdges_WithOptionalID(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{{ID: "a"}, {ID: "b"}})

	err := s.AddEdges(ctx, []*graph.Edge{
		{ID: "edge-1", FromID: "a", ToID: "b", Type: "CALLS"},
	})
	assert.NoError(t, err)
}

func TestAddEdges_Upsert(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{{ID: "a"}, {ID: "b"}})

	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS", Metadata: map[string]any{"v": "1"}},
	})

	err := s.AddEdges(ctx, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS", Metadata: map[string]any{"v": "2"}},
	})
	assert.NoError(t, err)

	edges, err := s.queryEdgesBySQL(ctx,
		fmt.Sprintf("SELECT e.id, e.from_id, e.to_id, e.type, e.metadata FROM %s e", s.opts.edgeTableName),
	)
	assert.NoError(t, err)
	assert.Len(t, edges, 1)
	assert.Equal(t, "2", edges[0].Metadata["v"])
}

// ---- Traverse ----

func TestTraverse_NilQuery(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Traverse(context.Background(), nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "traverse query is required")
}

func TestTraverse_EmptyStartIDs(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Traverse(context.Background(), &graph.TraverseQuery{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "start_ids cannot be empty")
}

func TestTraverse_UnsupportedDirection(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Traverse(context.Background(), &graph.TraverseQuery{
		StartIDs:  []string{"a"},
		Direction: graph.Direction("sideways"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported direction")
}

func TestTraverse_Defaults(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{{ID: "a"}, {ID: "b"}})
	addTestEdges(t, ctx, s, []*graph.Edge{{FromID: "a", ToID: "b", Type: "CALLS"}})

	result, err := s.Traverse(ctx, &graph.TraverseQuery{
		StartIDs: []string{"a"},
	})
	assert.NoError(t, err)
	assert.NotNil(t, result)
	// Default MaxDepth=1, MaxNodes=100, Direction=out
	assert.False(t, result.Truncated)
}

func TestTraverse_Outgoing_SingleHop(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		{FromID: "a", ToID: "c", Type: "CALLS"},
	})

	result, err := s.Traverse(ctx, &graph.TraverseQuery{
		StartIDs:  []string{"a"},
		Direction: graph.DirectionOut,
		MaxDepth:  1,
	})
	assert.NoError(t, err)
	assert.NotNil(t, result)
	// Start node "a" + neighbors "b", "c"
	assert.ElementsMatch(t, []string{"a", "b", "c"}, nodeIDs(result.Nodes))
	assert.NotEmpty(t, result.Edges)
}

func TestTraverse_Outgoing_MultiHop(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		{FromID: "b", ToID: "c", Type: "CALLS"},
	})

	result, err := s.Traverse(ctx, &graph.TraverseQuery{
		StartIDs:  []string{"a"},
		Direction: graph.DirectionOut,
		MaxDepth:  2,
	})
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b", "c"}, nodeIDs(result.Nodes))
}

func TestTraverse_Incoming(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "c", Type: "CALLS"},
		{FromID: "b", ToID: "c", Type: "CALLS"},
	})

	result, err := s.Traverse(ctx, &graph.TraverseQuery{
		StartIDs:  []string{"c"},
		Direction: graph.DirectionIn,
		MaxDepth:  1,
	})
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"c", "a", "b"}, nodeIDs(result.Nodes))
}

func TestTraverse_BothDirections(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		{FromID: "c", ToID: "b", Type: "CALLS"},
	})

	result, err := s.Traverse(ctx, &graph.TraverseQuery{
		StartIDs:  []string{"b"},
		Direction: graph.DirectionBoth,
		MaxDepth:  1,
	})
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b", "c"}, nodeIDs(result.Nodes))
}

func TestTraverse_MaxDepth1_NoDeepNodes(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		{FromID: "b", ToID: "c", Type: "CALLS"},
	})

	result, err := s.Traverse(ctx, &graph.TraverseQuery{
		StartIDs:  []string{"a"},
		Direction: graph.DirectionOut,
		MaxDepth:  1,
	})
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b"}, nodeIDs(result.Nodes))
	assert.NotContains(t, nodeIDs(result.Nodes), "c")
}

func TestTraverse_MaxNodes_Truncated(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	nodes := make([]*graph.Node, 20)
	edges := make([]*graph.Edge, 19)
	for i := 0; i < 20; i++ {
		nodes[i] = &graph.Node{ID: string(rune('a' + i))}
		if i > 0 {
			edges[i-1] = &graph.Edge{FromID: string(rune('a' + i - 1)), ToID: string(rune('a' + i)), Type: "NEXT"}
		}
	}
	addTestNodes(t, ctx, s, nodes)
	addTestEdges(t, ctx, s, edges)

	result, err := s.Traverse(ctx, &graph.TraverseQuery{
		StartIDs:  []string{"a"},
		Direction: graph.DirectionOut,
		MaxDepth:  20,
		MaxNodes:  5,
	})
	assert.NoError(t, err)
	assert.True(t, result.Truncated)
	assert.LessOrEqual(t, len(result.Nodes), 5)
}

func TestTraverse_EdgeTypeFilter(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		{FromID: "a", ToID: "c", Type: "CONTAINS"},
	})

	result, err := s.Traverse(ctx, &graph.TraverseQuery{
		StartIDs:  []string{"a"},
		Direction: graph.DirectionOut,
		MaxDepth:  1,
		EdgeTypes: []string{"CALLS"},
	})
	assert.NoError(t, err)
	assert.Contains(t, nodeIDs(result.Nodes), "b")
	assert.NotContains(t, nodeIDs(result.Nodes), "c")
}

func TestTraverse_MultipleEdgeTypes(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		{FromID: "a", ToID: "c", Type: "CONTAINS"},
	})

	result, err := s.Traverse(ctx, &graph.TraverseQuery{
		StartIDs:  []string{"a"},
		Direction: graph.DirectionOut,
		MaxDepth:  1,
		EdgeTypes: []string{"CALLS", "CONTAINS"},
	})
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b", "c"}, nodeIDs(result.Nodes))
}

func TestTraverse_MultipleStartIDs(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{
		{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"},
	})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "c", Type: "CALLS"},
		{FromID: "b", ToID: "d", Type: "CALLS"},
	})

	result, err := s.Traverse(ctx, &graph.TraverseQuery{
		StartIDs:  []string{"a", "b"},
		Direction: graph.DirectionOut,
		MaxDepth:  1,
	})
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b", "c", "d"}, nodeIDs(result.Nodes))
}

func TestTraverse_CycleDetection(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "NEXT"},
		{FromID: "b", ToID: "c", Type: "NEXT"},
		{FromID: "c", ToID: "a", Type: "NEXT"},
	})

	result, err := s.Traverse(ctx, &graph.TraverseQuery{
		StartIDs:  []string{"a"},
		Direction: graph.DirectionOut,
		MaxDepth:  10,
	})
	assert.NoError(t, err)
	assert.Len(t, nodeIDs(result.Nodes), 3)
	assert.False(t, result.Truncated)
}

func TestTraverse_NoResults(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{{ID: "a"}, {ID: "b"}})

	result, err := s.Traverse(ctx, &graph.TraverseQuery{
		StartIDs:  []string{"a"},
		Direction: graph.DirectionOut,
		MaxDepth:  1,
	})
	assert.NoError(t, err)
	assert.Len(t, result.Nodes, 1) // just the start node
	assert.Empty(t, result.Edges)
}

func TestTraverse_EdgesReturned(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{{ID: "a"}, {ID: "b"}})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{ID: "e1", FromID: "a", ToID: "b", Type: "CALLS"},
	})

	result, err := s.Traverse(ctx, &graph.TraverseQuery{
		StartIDs:  []string{"a"},
		Direction: graph.DirectionOut,
		MaxDepth:  1,
	})
	assert.NoError(t, err)
	assert.NotEmpty(t, result.Edges)
	assert.Equal(t, []string{"a:CALLS:b"}, edgeKeys(result.Edges))
}

func TestTraverse_FilterEdgesByNodes(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		{FromID: "b", ToID: "c", Type: "CALLS"},
	})

	result, err := s.Traverse(ctx, &graph.TraverseQuery{
		StartIDs:  []string{"a"},
		Direction: graph.DirectionOut,
		MaxDepth:  1,
		MaxNodes:  2,
	})
	assert.NoError(t, err)
	// With MaxNodes=2, only "a" and "b" are in result; edge a->b is kept, b->c is filtered
	for _, e := range result.Edges {
		assert.Equal(t, "a", e.FromID)
		assert.Equal(t, "b", e.ToID)
	}
}

func TestTraverse_CombinationExplosion(t *testing.T) {
	s := newTestStore(t)
	types := make([]string, 5)
	for i := range types {
		types[i] = string(rune('A' + i))
	}
	_, err := s.Traverse(context.Background(), &graph.TraverseQuery{
		StartIDs:  []string{"x"},
		EdgeTypes: types,
		MaxDepth:  5,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too many edge type combinations")
}

// ---- FindPaths ----

func TestFindPaths_NilQuery(t *testing.T) {
	s := newTestStore(t)
	_, err := s.FindPaths(context.Background(), nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path query is required")
}

func TestFindPaths_EmptyEndpoints(t *testing.T) {
	s := newTestStore(t)
	_, err := s.FindPaths(context.Background(), &graph.PathQuery{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "from_id and to_id are required")
}

func TestFindPaths_SimplePath(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{{ID: "a"}, {ID: "b"}})
	addTestEdges(t, ctx, s, []*graph.Edge{{FromID: "a", ToID: "b", Type: "CALLS"}})

	result, err := s.FindPaths(ctx, &graph.PathQuery{
		FromID: "a",
		ToID:   "b",
	})
	assert.NoError(t, err)
	assert.Len(t, result.Paths, 1)
	assert.Equal(t, []string{"a", "b"}, nodeIDs(result.Paths[0].Nodes))
	assert.Len(t, result.Paths[0].Edges, 1)
	assert.Equal(t, "a", result.Paths[0].Edges[0].FromID)
	assert.Equal(t, "b", result.Paths[0].Edges[0].ToID)
	assert.Equal(t, "CALLS", result.Paths[0].Edges[0].Type)
	assert.False(t, result.Truncated)
}

func TestFindPaths_MultiHopPath(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		{FromID: "b", ToID: "c", Type: "CALLS"},
	})

	result, err := s.FindPaths(ctx, &graph.PathQuery{
		FromID:   "a",
		ToID:     "c",
		MaxDepth: 5,
	})
	assert.NoError(t, err)
	assert.Len(t, result.Paths, 1)
	assert.Equal(t, []string{"a", "b", "c"}, nodeIDs(result.Paths[0].Nodes))
	assert.Len(t, result.Paths[0].Edges, 2)
}

func TestFindPaths_MultiplePaths(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{
		{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"},
	})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		{FromID: "b", ToID: "d", Type: "CALLS"},
		{FromID: "a", ToID: "c", Type: "CONTAINS"},
		{FromID: "c", ToID: "d", Type: "CONTAINS"},
	})

	result, err := s.FindPaths(ctx, &graph.PathQuery{
		FromID:   "a",
		ToID:     "d",
		MaxDepth: 5,
	})
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(result.Paths), 2)
}

func TestFindPaths_NoPath(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{{ID: "a"}, {ID: "b"}})

	result, err := s.FindPaths(ctx, &graph.PathQuery{
		FromID: "a",
		ToID:   "b",
	})
	assert.NoError(t, err)
	assert.Empty(t, result.Paths)
}

func TestFindPaths_MaxDepth(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		{FromID: "b", ToID: "c", Type: "CALLS"},
	})

	result, err := s.FindPaths(ctx, &graph.PathQuery{
		FromID:   "a",
		ToID:     "c",
		MaxDepth: 1,
	})
	assert.NoError(t, err)
	assert.Empty(t, result.Paths, "a->c requires depth 2, MaxDepth=1 should find no path")
}

func TestFindPaths_MaxPaths_Truncated(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{
		{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"},
	})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		{FromID: "b", ToID: "d", Type: "CALLS"},
		{FromID: "a", ToID: "c", Type: "CONTAINS"},
		{FromID: "c", ToID: "d", Type: "CONTAINS"},
	})

	result, err := s.FindPaths(ctx, &graph.PathQuery{
		FromID:   "a",
		ToID:     "d",
		MaxDepth: 5,
		MaxPaths: 1,
	})
	assert.NoError(t, err)
	assert.LessOrEqual(t, len(result.Paths), 1)
	assert.True(t, result.Truncated)
}

func TestFindPaths_DirectionIn(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "c", ToID: "a", Type: "CALLS"},
		{FromID: "a", ToID: "b", Type: "CALLS"},
	})

	result, err := s.FindPaths(ctx, &graph.PathQuery{
		FromID:    "b",
		ToID:      "c",
		Direction: graph.DirectionIn,
		MaxDepth:  5,
	})
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(result.Paths), 1)
}

func TestFindPaths_DirectionBoth(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		{FromID: "c", ToID: "b", Type: "CALLS"},
	})

	result, err := s.FindPaths(ctx, &graph.PathQuery{
		FromID:    "a",
		ToID:      "c",
		Direction: graph.DirectionBoth,
		MaxDepth:  5,
	})
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(result.Paths), 1, "bidirectional should find path through b")
}

func TestFindPaths_EdgeTypeFilter(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		{FromID: "b", ToID: "c", Type: "CONTAINS"},
	})

	result, err := s.FindPaths(ctx, &graph.PathQuery{
		FromID:    "a",
		ToID:      "c",
		MaxDepth:  5,
		EdgeTypes: []string{"CALLS"},
	})
	assert.NoError(t, err)
	assert.Empty(t, result.Paths, "only CALLS edges, but a->b->c needs CONTAINS")
}

func TestFindPaths_CycleDetection(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "NEXT"},
		{FromID: "b", ToID: "c", Type: "NEXT"},
		{FromID: "c", ToID: "a", Type: "NEXT"},
	})

	result, err := s.FindPaths(ctx, &graph.PathQuery{
		FromID:   "a",
		ToID:     "c",
		MaxDepth: 10,
	})
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(result.Paths), 1)
}

func TestFindPaths_EdgeWithID(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{{ID: "a"}, {ID: "b"}})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{ID: "my-edge", FromID: "a", ToID: "b", Type: "CALLS"},
	})

	result, err := s.FindPaths(ctx, &graph.PathQuery{
		FromID: "a",
		ToID:   "b",
	})
	assert.NoError(t, err)
	assert.Len(t, result.Paths, 1)
	assert.Equal(t, "my-edge", result.Paths[0].Edges[0].ID)
}

func TestFindPaths_EdgeWithoutID_GetsCompositeID(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{{ID: "a"}, {ID: "b"}})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
	})

	result, err := s.FindPaths(ctx, &graph.PathQuery{
		FromID: "a",
		ToID:   "b",
	})
	assert.NoError(t, err)
	assert.Len(t, result.Paths, 1)
	assert.Equal(t, "a:CALLS:b", result.Paths[0].Edges[0].ID)
}

func TestFindPaths_EdgeMetadata(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{{ID: "a"}, {ID: "b"}})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS", Metadata: map[string]any{"weight": float64(5)}},
	})

	result, err := s.FindPaths(ctx, &graph.PathQuery{
		FromID: "a",
		ToID:   "b",
	})
	assert.NoError(t, err)
	assert.Len(t, result.Paths, 1)
	// Path edges don't carry metadata (matching AGE behavior)
	assert.Nil(t, result.Paths[0].Edges[0].Metadata)
}

func TestFindPaths_Defaults(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addTestNodes(t, ctx, s, []*graph.Node{{ID: "a"}, {ID: "b"}, {ID: "c"}})
	addTestEdges(t, ctx, s, []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		{FromID: "b", ToID: "c", Type: "CALLS"},
	})

	result, err := s.FindPaths(ctx, &graph.PathQuery{
		FromID: "a",
		ToID:   "c",
	})
	assert.NoError(t, err)
	// Default MaxDepth=5, MaxPaths=10
	assert.GreaterOrEqual(t, len(result.Paths), 1)
}

// ---- Helper function tests ----

func TestUniqueNodes(t *testing.T) {
	nodes := []*graph.Node{
		{ID: "a", Name: "A"},
		{ID: "b", Name: "B"},
		{ID: "a", Name: "A-dup"},
		nil,
		{ID: "", Name: "empty"},
		{ID: "c", Name: "C"},
	}
	got := uniqueNodes(nodes)
	assert.Len(t, got, 3)
	assert.Equal(t, "A", got[0].Name, "should keep first occurrence")
}

func TestUniqueNodes_Empty(t *testing.T) {
	got := uniqueNodes(nil)
	assert.Len(t, got, 0)
}

func TestLimitNodes(t *testing.T) {
	nodes := []*graph.Node{{ID: "a"}, {ID: "b"}, {ID: "c"}}

	assert.Len(t, limitNodes(nodes, 2), 2)
	assert.Len(t, limitNodes(nodes, 5), 3)
	assert.Len(t, limitNodes(nodes, 3), 3)
	assert.Len(t, limitNodes(nodes, 0), 3)
	assert.Len(t, limitNodes(nodes, -1), 3)
}

func TestUniqueEdges(t *testing.T) {
	edges := []*graph.Edge{
		{ID: "e1", FromID: "a", ToID: "b", Type: "CALLS"},
		{ID: "e1", FromID: "a", ToID: "b", Type: "CALLS"},
		nil,
		{ID: "", FromID: "a", ToID: "b", Type: "CALLS"},
		{ID: "", FromID: "a", ToID: "b", Type: "CALLS"},
		{ID: "e2", FromID: "b", ToID: "c", Type: "METHOD"},
	}
	got := uniqueEdges(edges)
	assert.Len(t, got, 3)
}

func TestUniqueEdges_Empty(t *testing.T) {
	got := uniqueEdges(nil)
	assert.Len(t, got, 0)
}

func TestFilterEdgesByNodes_DropsDangling(t *testing.T) {
	edges := []*graph.Edge{
		{ID: "a-b", FromID: "a", ToID: "b", Type: "CALLS"},
		{ID: "b-c", FromID: "b", ToID: "c", Type: "CALLS"},
	}
	nodes := []*graph.Node{{ID: "a"}, {ID: "b"}}

	filtered := filterEdgesByNodes(edges, nodes)
	assert.Len(t, filtered, 1)
	assert.Equal(t, "a-b", filtered[0].ID)
}

func TestFilterEdgesByNodes_EmptyEdges(t *testing.T) {
	got := filterEdgesByNodes(nil, []*graph.Node{{ID: "a"}})
	assert.Nil(t, got)
}

func TestFilterEdgesByNodes_EmptyNodes(t *testing.T) {
	got := filterEdgesByNodes([]*graph.Edge{{ID: "e1"}}, nil)
	assert.Nil(t, got)
}

func TestPathEdges_Basic(t *testing.T) {
	edges := pathEdges(&rawPath{
		edgeIDs:   []string{"edge-1", ""},
		fromIDs:   []string{"a", "b"},
		toIDs:     []string{"b", "c"},
		edgeTypes: []string{"CALLS", "CONTAINS"},
	})
	assert.Len(t, edges, 2)
	assert.Equal(t, "edge-1", edges[0].ID)
	assert.Equal(t, "a", edges[0].FromID)
	assert.Equal(t, "b", edges[0].ToID)
	assert.Equal(t, "CALLS", edges[0].Type)
	assert.Equal(t, "b:CONTAINS:c", edges[1].ID)
	assert.Equal(t, "b", edges[1].FromID)
	assert.Equal(t, "c", edges[1].ToID)
	assert.Equal(t, "CONTAINS", edges[1].Type)
}

func TestPathEdges_NilPath(t *testing.T) {
	assert.Nil(t, pathEdges(nil))
}

func TestPathEdges_MismatchedLengths(t *testing.T) {
	edges := pathEdges(&rawPath{
		edgeIDs:   []string{"e1"},
		fromIDs:   []string{"a", "b", "c"},
		toIDs:     []string{"b"},
		edgeTypes: []string{"CALLS", "METHOD"},
	})
	assert.Len(t, edges, 1, "min of fromIDs/toIDs/edgeTypes constrained")
}

func TestPathEdges_NoMetadata(t *testing.T) {
	edges := pathEdges(&rawPath{
		fromIDs:   []string{"a"},
		toIDs:     []string{"b"},
		edgeTypes: []string{"CALLS"},
	})
	assert.Len(t, edges, 1)
	assert.Nil(t, edges[0].Metadata, "pathEdges should not set metadata")
}

func TestMarshalMetadata(t *testing.T) {
	assert.Equal(t, "{}", marshalMetadata(nil))
	assert.Equal(t, "{}", marshalMetadata(map[string]any{}))
	assert.Contains(t, marshalMetadata(map[string]any{"k": "v"}), "k")
}

func TestUnmarshalMetadata(t *testing.T) {
	m, err := unmarshalMetadata("")
	assert.NoError(t, err)
	assert.Nil(t, m)

	m, err = unmarshalMetadata("{}")
	assert.NoError(t, err)
	assert.Nil(t, m)

	m, err = unmarshalMetadata(`{"key":"val"}`)
	assert.NoError(t, err)
	assert.Equal(t, "val", m["key"])
}

func TestNormalizeEdgeTypes(t *testing.T) {
	assert.Nil(t, normalizeEdgeTypes(nil))
	assert.Nil(t, normalizeEdgeTypes([]string{}))
	assert.Equal(t, []string{"A"}, normalizeEdgeTypes([]string{"A"}))
	assert.Equal(t, []string{"A", "B"}, normalizeEdgeTypes([]string{"B", "A", "B"}))
}

func TestPowInt(t *testing.T) {
	assert.Equal(t, 1, powInt(2, 0))
	assert.Equal(t, 2, powInt(2, 1))
	assert.Equal(t, 8, powInt(2, 3))
	assert.Equal(t, 9, powInt(3, 2))
	assert.Equal(t, 125, powInt(5, 3))
}

func TestSplitCommaList(t *testing.T) {
	assert.Nil(t, splitCommaList(""))
	assert.Equal(t, []string{"a", "b", "c"}, splitCommaList("a,b,c"))
	assert.Equal(t, []string{"a", "b"}, splitCommaList(",a,,b,"))
}

func TestIsSQLiteMemoryDSN(t *testing.T) {
	assert.True(t, isSQLiteMemoryDSN(":memory:"))
	assert.True(t, isSQLiteMemoryDSN("file::memory:"))
	assert.True(t, isSQLiteMemoryDSN("file:test.db?mode=memory"))
	assert.False(t, isSQLiteMemoryDSN("test.db"))
	assert.False(t, isSQLiteMemoryDSN("file:test.db"))
}

func TestCustomTableNames(t *testing.T) {
	s, err := New(
		WithNodeTableName("my_nodes"),
		WithEdgeTableName("my_edges"),
	)
	require.NoError(t, err)
	defer s.Close()

	assert.Equal(t, "my_nodes", s.opts.nodeTableName)
	assert.Equal(t, "my_edges", s.opts.edgeTableName)

	ctx := context.Background()
	err = s.AddNodes(ctx, []*graph.Node{{ID: "x"}})
	assert.NoError(t, err)

	nodes, err := s.queryNodesByIDs(ctx, []string{"x"})
	assert.NoError(t, err)
	assert.Len(t, nodes, 1)
}

func TestInterfaceCompliance(t *testing.T) {
	var _ interface {
		AddNodes(context.Context, []*graph.Node) error
		AddEdges(context.Context, []*graph.Edge) error
		Traverse(context.Context, *graph.TraverseQuery) (*graph.TraverseResult, error)
		FindPaths(context.Context, *graph.PathQuery) (*graph.PathResult, error)
		Close() error
	} = (*Store)(nil)
}
