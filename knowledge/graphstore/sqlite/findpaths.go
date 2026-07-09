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

	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
)

func (s *Store) FindPaths(ctx context.Context, query *graph.PathQuery) (*graph.PathResult, error) {
	if query == nil {
		return nil, errors.New("sqlitegraph: path query is required")
	}
	if query.FromID == "" || query.ToID == "" {
		return nil, errors.New("sqlitegraph: from_id and to_id are required")
	}

	depth := query.MaxDepth
	if depth <= 0 {
		depth = 5
	}
	maxPaths := query.MaxPaths
	if maxPaths <= 0 {
		maxPaths = 10
	}
	direction := query.Direction
	if direction == "" {
		direction = graph.DirectionOut
	}
	if direction != graph.DirectionOut && direction != graph.DirectionIn && direction != graph.DirectionBoth {
		return nil, fmt.Errorf("sqlitegraph: unsupported direction %q", direction)
	}

	edgeTypes := normalizeEdgeTypes(query.EdgeTypes)

	var paths []*graph.Path
	truncated := false

	if len(edgeTypes) <= 1 {
		edgeType := ""
		if len(edgeTypes) == 1 {
			edgeType = edgeTypes[0]
		}

		remaining := maxPaths
		rawPaths, err := s.findPathsSingleType(ctx, query.FromID, query.ToID, direction, edgeType, depth, remaining+1)
		if err != nil {
			return nil, err
		}
		if len(rawPaths) > remaining {
			truncated = true
			rawPaths = rawPaths[:remaining]
		}

		for _, rp := range rawPaths {
			fullNodes, err := s.queryNodesByIDs(ctx, rp.nodeIDs)
			if err != nil {
				return nil, err
			}
			paths = append(paths, &graph.Path{
				Nodes: fullNodes,
				Edges: pathEdges(rp),
			})
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

		for i, edgeType := range edgeTypes {
			if len(paths) >= maxPaths {
				hasMore, err := s.hasAnyPath(ctx, query.FromID, query.ToID, direction, edgeType, depth)
				if err != nil {
					return nil, err
				}
				if hasMore {
					truncated = true
				}
				continue
			}

			remaining := maxPaths - len(paths)
			rawPaths, err := s.findPathsSingleType(ctx, query.FromID, query.ToID, direction, edgeType, depth, remaining+1)
			if err != nil {
				return nil, err
			}
			if len(rawPaths) > remaining {
				truncated = true
				rawPaths = rawPaths[:remaining]
			}

			for _, rp := range rawPaths {
				fullNodes, err := s.queryNodesByIDs(ctx, rp.nodeIDs)
				if err != nil {
					return nil, err
				}
				paths = append(paths, &graph.Path{
					Nodes: fullNodes,
					Edges: pathEdges(rp),
				})
			}

			if len(paths) >= maxPaths && !truncated && i+1 < len(edgeTypes) {
				for _, et := range edgeTypes[i+1:] {
					hasMore, err := s.hasAnyPath(ctx, query.FromID, query.ToID, direction, et, depth)
					if err != nil {
						return nil, err
					}
					if hasMore {
						truncated = true
						break
					}
				}
			}
		}
	}

	return &graph.PathResult{Paths: paths, Truncated: truncated}, nil
}

func (s *Store) findPathsSingleType(ctx context.Context, fromID, toID string, direction graph.Direction, edgeType string, depth int, limit int) ([]*rawPath, error) {
	edgeJoin, nextIDExpr, visitIDExpr := s.buildPathDirectionClauses(direction)

	edgeTypeFilter := ""
	var edgeTypeArgs []any
	if edgeType != "" {
		edgeTypeFilter = "AND e.type = ?"
		edgeTypeArgs = []any{edgeType}
	}

	query := fmt.Sprintf(`WITH RECURSIVE paths AS (
    SELECT id AS last_id, 0 AS depth,
           ',' || id || ',' AS visited,
           id AS node_ids,
           '' AS edge_ids,
           '' AS from_ids,
           '' AS to_ids,
           '' AS edge_types
    FROM %s WHERE id = ?

    UNION

    SELECT %s, p.depth + 1,
           p.visited || %s || ',',
           p.node_ids || ',' || %s,
           p.edge_ids || ',' || e.id,
           p.from_ids || ',' || e.from_id,
           p.to_ids || ',' || e.to_id,
           p.edge_types || ',' || e.type
    FROM paths p
    JOIN %s e ON %s
    WHERE p.depth < ?
      AND instr(p.visited, ',' || %s || ',') = 0
      %s
)
SELECT node_ids, edge_ids, from_ids, to_ids, edge_types
FROM paths
WHERE last_id = ?
ORDER BY depth ASC
LIMIT ?`,
		s.opts.nodeTableName,
		nextIDExpr,
		visitIDExpr,
		nextIDExpr,
		s.opts.edgeTableName, edgeJoin,
		visitIDExpr,
		edgeTypeFilter,
	)

	args := []any{fromID, depth}
	args = append(args, edgeTypeArgs...)
	args = append(args, toID, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlitegraph: find paths: %w", err)
	}
	defer rows.Close()

	var result []*rawPath
	for rows.Next() {
		var nodeIDs, edgeIDs, fromIDs, toIDs, edgeTypes string
		if err := rows.Scan(&nodeIDs, &edgeIDs, &fromIDs, &toIDs, &edgeTypes); err != nil {
			return nil, fmt.Errorf("sqlitegraph: scan path: %w", err)
		}
		result = append(result, &rawPath{
			nodeIDs:   splitCommaList(nodeIDs),
			edgeIDs:   splitCommaList(edgeIDs),
			fromIDs:   splitCommaList(fromIDs),
			toIDs:     splitCommaList(toIDs),
			edgeTypes: splitCommaList(edgeTypes),
		})
	}
	return result, rows.Err()
}

func (s *Store) hasAnyPath(ctx context.Context, fromID, toID string, direction graph.Direction, edgeType string, depth int) (bool, error) {
	rawPaths, err := s.findPathsSingleType(ctx, fromID, toID, direction, edgeType, depth, 1)
	if err != nil {
		return false, err
	}
	return len(rawPaths) > 0, nil
}
