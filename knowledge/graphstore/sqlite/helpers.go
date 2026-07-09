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
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
)

func marshalMetadata(metadata map[string]any) string {
	if len(metadata) == 0 {
		return "{}"
	}
	data, _ := json.Marshal(metadata)
	return string(data)
}

func unmarshalMetadata(s string) (map[string]any, error) {
	if s == "" || s == "{}" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return m, nil
}

func uniqueNodes(nodes []*graph.Node) []*graph.Node {
	seen := make(map[string]struct{}, len(nodes))
	result := make([]*graph.Node, 0, len(nodes))
	for _, n := range nodes {
		if n == nil || n.ID == "" {
			continue
		}
		if _, ok := seen[n.ID]; ok {
			continue
		}
		seen[n.ID] = struct{}{}
		result = append(result, n)
	}
	return result
}

func limitNodes(nodes []*graph.Node, max int) []*graph.Node {
	if max <= 0 || len(nodes) <= max {
		return nodes
	}
	return nodes[:max]
}

func uniqueEdges(edges []*graph.Edge) []*graph.Edge {
	seen := make(map[string]bool, len(edges))
	result := make([]*graph.Edge, 0, len(edges))
	for _, e := range edges {
		if e == nil {
			continue
		}
		key := e.ID
		if key == "" {
			key = e.FromID + ":" + e.Type + ":" + e.ToID
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, e)
	}
	return result
}

func filterEdgesByNodes(edges []*graph.Edge, nodes []*graph.Node) []*graph.Edge {
	if len(edges) == 0 || len(nodes) == 0 {
		return nil
	}
	nodeIDs := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		if n == nil || n.ID == "" {
			continue
		}
		nodeIDs[n.ID] = struct{}{}
	}
	filtered := make([]*graph.Edge, 0, len(edges))
	for _, e := range edges {
		if e == nil {
			continue
		}
		if _, ok := nodeIDs[e.FromID]; !ok {
			continue
		}
		if _, ok := nodeIDs[e.ToID]; !ok {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
}

type rawPath struct {
	nodeIDs   []string
	edgeIDs   []string
	fromIDs   []string
	toIDs     []string
	edgeTypes []string
}

func pathEdges(p *rawPath) []*graph.Edge {
	if p == nil {
		return nil
	}
	n := len(p.edgeTypes)
	if len(p.fromIDs) < n {
		n = len(p.fromIDs)
	}
	if len(p.toIDs) < n {
		n = len(p.toIDs)
	}
	edges := make([]*graph.Edge, 0, n)
	for i := 0; i < n; i++ {
		edge := &graph.Edge{
			FromID: p.fromIDs[i],
			ToID:   p.toIDs[i],
			Type:   p.edgeTypes[i],
		}
		if i < len(p.edgeIDs) && p.edgeIDs[i] != "" {
			edge.ID = p.edgeIDs[i]
		}
		if edge.ID == "" {
			edge.ID = edge.FromID + ":" + edge.Type + ":" + edge.ToID
		}
		edges = append(edges, edge)
	}
	return edges
}

func splitCommaList(s string) []string {
	if s == "" {
		return nil
	}
	parts := make([]string, 0, 8)
	for _, p := range splitString(s, ",") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func splitString(s, sep string) []string {
	if s == "" {
		return nil
	}
	result := make([]string, 0, 8)
	rest := s
	for {
		idx := indexOf(rest, sep)
		if idx < 0 {
			result = append(result, rest)
			break
		}
		result = append(result, rest[:idx])
		rest = rest[idx+len(sep):]
	}
	return result
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
