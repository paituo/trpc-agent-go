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
	"errors"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
)

const maxRelationshipPatternCombinations = 256

func (s *Store) Traverse(ctx context.Context, query *graph.TraverseQuery) (*graph.TraverseResult, error) {
	if query == nil {
		return nil, errors.New("sqlitegraph: traverse query is required")
	}
	if len(query.StartIDs) == 0 {
		return nil, errors.New("sqlitegraph: start_ids cannot be empty")
	}

	depth := query.MaxDepth
	if depth <= 0 {
		depth = 1
	}
	maxNodes := query.MaxNodes
	if maxNodes <= 0 {
		maxNodes = 100
	}
	direction := query.Direction
	if direction == "" {
		direction = graph.DirectionOut
	}
	if direction != graph.DirectionOut && direction != graph.DirectionIn && direction != graph.DirectionBoth {
		return nil, fmt.Errorf("sqlitegraph: unsupported direction %q", direction)
	}

	startNodes, err := s.queryNodesByIDs(ctx, query.StartIDs)
	if err != nil {
		return nil, err
	}

	allNodes := make([]*graph.Node, len(startNodes))
	copy(allNodes, startNodes)

	allEdges := make([]*graph.Edge, 0)
	queryTruncated := false

	edgeTypes := normalizeEdgeTypes(query.EdgeTypes)

	if len(edgeTypes) <= 1 {
		edgeType := ""
		if len(edgeTypes) == 1 {
			edgeType = edgeTypes[0]
		}

		for _, startID := range query.StartIDs {
			resultNodes, truncated, err := s.traverseNodesSingleType(ctx, startID, direction, edgeType, depth, maxNodes, query.StartIDs)
			if err != nil {
				return nil, err
			}
			if truncated {
				queryTruncated = true
			}
			allNodes = append(allNodes, resultNodes...)

			allNodeIDs := append(query.StartIDs, nodeIDsFrom(resultNodes)...)
			resultEdges, err := s.traverseEdges(ctx, allNodeIDs, direction, edgeType, maxNodes)
			if err != nil {
				return nil, err
			}
			allEdges = append(allEdges, resultEdges...)
		}
	} else {
		totalCombinations := 0
		for pathLen := 1; pathLen <= depth; pathLen++ {
			combinations := powInt(len(edgeTypes), pathLen)
			totalCombinations += combinations
			if totalCombinations > maxRelationshipPatternCombinations {
				return nil, fmt.Errorf(
					"sqlitegraph: too many edge type combinations: %d edge type(s) with max_depth %d exceeds %d",
					len(edgeTypes), depth, maxRelationshipPatternCombinations,
				)
			}
		}

		for _, startID := range query.StartIDs {
			for _, edgeType := range edgeTypes {
				resultNodes, truncated, err := s.traverseNodesSingleType(ctx, startID, direction, edgeType, depth, maxNodes, query.StartIDs)
				if err != nil {
					return nil, err
				}
				if truncated {
					queryTruncated = true
				}
				allNodes = append(allNodes, resultNodes...)

				allNodeIDs := append(query.StartIDs, nodeIDsFrom(resultNodes)...)
				resultEdges, err := s.traverseEdges(ctx, allNodeIDs, direction, edgeType, maxNodes)
				if err != nil {
					return nil, err
				}
				allEdges = append(allEdges, resultEdges...)
			}
		}
	}

	resultNodes := uniqueNodes(allNodes)
	limitedNodes := limitNodes(resultNodes, maxNodes)
	truncated := queryTruncated || len(resultNodes) > len(limitedNodes)

	return &graph.TraverseResult{
		Nodes:     limitedNodes,
		Edges:     filterEdgesByNodes(uniqueEdges(allEdges), limitedNodes),
		Truncated: truncated,
	}, nil
}

func (s *Store) traverseNodesSingleType(ctx context.Context, startID string, direction graph.Direction, edgeType string, depth int, maxNodes int, excludeIDs []string) ([]*graph.Node, bool, error) {
	edgeJoin, nodeJoin := s.buildDirectionClauses(direction)

	edgeTypeFilter := ""
	if edgeType != "" {
		edgeTypeFilter = "AND e.type = ?"
	}

	excludeClause := ""
	excludeArgs := make([]any, 0)
	if len(excludeIDs) > 0 {
		placeholders := strings.Repeat("?,", len(excludeIDs))
		placeholders = placeholders[:len(placeholders)-1]
		excludeClause = fmt.Sprintf("WHERE id NOT IN (%s)", placeholders)
		for _, id := range excludeIDs {
			excludeArgs = append(excludeArgs, id)
		}
	}

	query := fmt.Sprintf(`WITH RECURSIVE traverse_nodes AS (
    SELECT id, name, content, metadata, 0 AS depth,
           ',' || id || ',' AS visited
    FROM %s WHERE id = ?

    UNION

    SELECT n.id, n.name, n.content, n.metadata, t.depth + 1,
           t.visited || n.id || ','
    FROM traverse_nodes t
    JOIN %s e ON %s
    JOIN %s n ON %s
    WHERE t.depth < ?
      AND instr(t.visited, ',' || n.id || ',') = 0
      %s
)
SELECT DISTINCT id, name, content, metadata
FROM traverse_nodes
%s
LIMIT ?`,
		s.opts.nodeTableName,
		s.opts.edgeTableName, edgeJoin,
		s.opts.nodeTableName, nodeJoin,
		edgeTypeFilter,
		excludeClause,
	)

	args := []any{startID, depth}
	if edgeType != "" {
		args = append(args, edgeType)
	}
	args = append(args, excludeArgs...)
	args = append(args, maxNodes+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("sqlitegraph: traverse nodes: %w", err)
	}
	defer rows.Close()

	var nodes []*graph.Node
	for rows.Next() {
		var id, name, content, metadataStr string
		if err := rows.Scan(&id, &name, &content, &metadataStr); err != nil {
			return nil, false, fmt.Errorf("sqlitegraph: scan traverse node: %w", err)
		}
		metadata, _ := unmarshalMetadata(metadataStr)
		nodes = append(nodes, &graph.Node{
			ID:       id,
			Name:     name,
			Content:  content,
			Metadata: metadata,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("sqlitegraph: traverse nodes rows: %w", err)
	}

	truncated := false
	if len(nodes) > maxNodes {
		truncated = true
		nodes = nodes[:maxNodes]
	}

	return nodes, truncated, nil
}

func (s *Store) traverseEdges(ctx context.Context, nodeIDs []string, direction graph.Direction, edgeType string, maxNodes int) ([]*graph.Edge, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}

	placeholders := strings.Repeat("?,", len(nodeIDs))
	placeholders = placeholders[:len(placeholders)-1]
	nodeIDArgs := make([]any, len(nodeIDs))
	for i, id := range nodeIDs {
		nodeIDArgs[i] = id
	}

	var directionClause string
	var directionArgs []any
	switch direction {
	case graph.DirectionOut, "":
		directionClause = fmt.Sprintf("e.from_id IN (%s)", placeholders)
		directionArgs = nodeIDArgs
	case graph.DirectionIn:
		directionClause = fmt.Sprintf("e.to_id IN (%s)", placeholders)
		directionArgs = nodeIDArgs
	case graph.DirectionBoth:
		directionClause = fmt.Sprintf("(e.from_id IN (%s) OR e.to_id IN (%s))", placeholders, placeholders)
		directionArgs = append(nodeIDArgs, nodeIDArgs...)
	}

	edgeTypeClause := ""
	var edgeTypeArgs []any
	if edgeType != "" {
		edgeTypeClause = "AND e.type = ?"
		edgeTypeArgs = []any{edgeType}
	}

	query := fmt.Sprintf(
		"SELECT DISTINCT e.id, e.from_id, e.to_id, e.type, e.metadata FROM %s e WHERE (%s) %s LIMIT ?",
		s.opts.edgeTableName, directionClause, edgeTypeClause,
	)

	args := append(directionArgs, edgeTypeArgs...)
	args = append(args, maxNodes)

	return s.queryEdgesBySQL(ctx, query, args...)
}

func nodeIDsFrom(nodes []*graph.Node) []string {
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n != nil && n.ID != "" {
			ids = append(ids, n.ID)
		}
	}
	return ids
}

func normalizeEdgeTypes(types []string) []string {
	if len(types) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(types))
	result := make([]string, 0, len(types))
	for _, t := range types {
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		result = append(result, t)
	}
	sort.Strings(result)
	return result
}

func powInt(base, exp int) int {
	result := 1
	for i := 0; i < exp; i++ {
		result *= base
	}
	return result
}
