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
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graphstore"
)

var _ graphstore.Store = (*Store)(nil)

type Store struct {
	db   *sql.DB
	opts options
	mu   sync.Mutex
}

func New(opts ...Option) (*Store, error) {
	o := defaultOptions
	for _, opt := range opts {
		opt(&o)
	}

	db, err := sql.Open(o.driverName, o.dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlitegraph: open database: %w", err)
	}

	if isSQLiteMemoryDSN(o.dsn) {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	}

	s := &Store{db: db, opts: o}

	if !o.skipDBInit {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.initDB(ctx); err != nil {
			db.Close()
			return nil, fmt.Errorf("sqlitegraph: init database: %w", err)
		}
	}

	return s, nil
}

func isSQLiteMemoryDSN(dsn string) bool {
	normalized := strings.ToLower(strings.TrimSpace(dsn))
	switch {
	case normalized == ":memory:":
		return true
	case strings.HasPrefix(normalized, "file::memory:"):
		return true
	case strings.HasPrefix(normalized, "file:") &&
		strings.Contains(normalized, "mode=memory"):
		return true
	default:
		return false
	}
}

func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Store) AddNodes(ctx context.Context, nodes []*graph.Node) error {
	if len(nodes) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitegraph: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, fmt.Sprintf(
		"INSERT OR REPLACE INTO %s (id, name, content, metadata) VALUES (?, ?, ?, ?)",
		s.opts.nodeTableName,
	))
	if err != nil {
		return fmt.Errorf("sqlitegraph: prepare: %w", err)
	}
	defer stmt.Close()

	for i, n := range nodes {
		if n == nil {
			return fmt.Errorf("sqlitegraph: node at index %d is nil", i)
		}
		if n.ID == "" {
			return fmt.Errorf("sqlitegraph: node at index %d has empty id", i)
		}
		metaJSON, _ := json.Marshal(n.Metadata)
		if _, err := stmt.ExecContext(ctx, n.ID, n.Name, n.Content, string(metaJSON)); err != nil {
			return fmt.Errorf("sqlitegraph: add node %s: %w", n.ID, err)
		}
	}

	return tx.Commit()
}

func (s *Store) AddEdges(ctx context.Context, edges []*graph.Edge) error {
	if len(edges) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitegraph: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, fmt.Sprintf(
		"INSERT OR REPLACE INTO %s (id, from_id, to_id, type, metadata) VALUES (?, ?, ?, ?, ?)",
		s.opts.edgeTableName,
	))
	if err != nil {
		return fmt.Errorf("sqlitegraph: prepare: %w", err)
	}
	defer stmt.Close()

	for i, e := range edges {
		if e == nil {
			return fmt.Errorf("sqlitegraph: edge at index %d is nil", i)
		}
		if e.FromID == "" || e.ToID == "" {
			return fmt.Errorf("sqlitegraph: edge at index %d has empty endpoint", i)
		}
		if e.Type == "" {
			return fmt.Errorf("sqlitegraph: edge at index %d has empty type", i)
		}
		metaJSON, _ := json.Marshal(e.Metadata)
		if _, err := stmt.ExecContext(ctx, e.ID, e.FromID, e.ToID, e.Type, string(metaJSON)); err != nil {
			return fmt.Errorf("sqlitegraph: add edge %s->%s:%s: %w", e.FromID, e.ToID, e.Type, err)
		}
	}

	return tx.Commit()
}

func (s *Store) queryNodesByIDs(ctx context.Context, ids []string) ([]*graph.Node, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	query := fmt.Sprintf(
		"SELECT id, name, content, metadata FROM %s WHERE id IN (%s)",
		s.opts.nodeTableName, placeholders,
	)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlitegraph: query nodes: %w", err)
	}
	defer rows.Close()

	var nodes []*graph.Node
	for rows.Next() {
		var id, name, content, metadataStr string
		if err := rows.Scan(&id, &name, &content, &metadataStr); err != nil {
			return nil, fmt.Errorf("sqlitegraph: scan node: %w", err)
		}
		metadata, _ := unmarshalMetadata(metadataStr)
		nodes = append(nodes, &graph.Node{
			ID:       id,
			Name:     name,
			Content:  content,
			Metadata: metadata,
		})
	}
	return nodes, rows.Err()
}

func (s *Store) queryEdgesBySQL(ctx context.Context, query string, args ...any) ([]*graph.Edge, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlitegraph: query edges: %w", err)
	}
	defer rows.Close()

	var edges []*graph.Edge
	for rows.Next() {
		var id, fromID, toID, edgeType, metadataStr string
		if err := rows.Scan(&id, &fromID, &toID, &edgeType, &metadataStr); err != nil {
			return nil, fmt.Errorf("sqlitegraph: scan edge: %w", err)
		}
		metadata, _ := unmarshalMetadata(metadataStr)
		edges = append(edges, &graph.Edge{
			ID:       id,
			FromID:   fromID,
			ToID:     toID,
			Type:     edgeType,
			Metadata: metadata,
		})
	}
	return edges, rows.Err()
}

func (s *Store) buildDirectionClauses(direction graph.Direction) (edgeJoin, nodeJoin string) {
	switch direction {
	case graph.DirectionOut, "":
		return "e.from_id = t.id", "n.id = e.to_id"
	case graph.DirectionIn:
		return "e.to_id = t.id", "n.id = e.from_id"
	case graph.DirectionBoth:
		return "(e.from_id = t.id OR e.to_id = t.id)",
			"((e.from_id = t.id AND n.id = e.to_id) OR (e.to_id = t.id AND n.id = e.from_id))"
	default:
		return "e.from_id = t.id", "n.id = e.to_id"
	}
}

func (s *Store) buildPathDirectionClauses(direction graph.Direction) (edgeJoin, nextIDExpr, visitIDExpr string) {
	switch direction {
	case graph.DirectionOut, "":
		return "e.from_id = p.last_id", "e.to_id", "e.to_id"
	case graph.DirectionIn:
		return "e.to_id = p.last_id", "e.from_id", "e.from_id"
	case graph.DirectionBoth:
		return "(e.from_id = p.last_id OR e.to_id = p.last_id)",
			"CASE WHEN e.from_id = p.last_id THEN e.to_id ELSE e.from_id END",
			"CASE WHEN e.from_id = p.last_id THEN e.to_id ELSE e.from_id END"
	default:
		return "e.from_id = p.last_id", "e.to_id", "e.to_id"
	}
}
