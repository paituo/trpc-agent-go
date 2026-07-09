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
	"fmt"
)

func (s *Store) initDB(ctx context.Context) error {
	stmts := s.buildCreateTableSQL()
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("sqlitegraph: init schema: %w", err)
		}
	}
	return nil
}

func (s *Store) buildCreateTableSQL() []string {
	nodeTable := s.opts.nodeTableName
	edgeTable := s.opts.edgeTableName

	createNodes := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
    id       TEXT PRIMARY KEY,
    name     TEXT NOT NULL DEFAULT '',
    content  TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}'
);`, nodeTable)

	createEdges := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
    id       TEXT NOT NULL DEFAULT '',
    from_id  TEXT NOT NULL,
    to_id    TEXT NOT NULL,
    type     TEXT NOT NULL,
    metadata TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (from_id, to_id, type)
);`, edgeTable)

	idxFromID := fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS idx_%s_from_id ON %s(from_id);`,
		edgeTable, edgeTable,
	)
	idxToID := fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS idx_%s_to_id ON %s(to_id);`,
		edgeTable, edgeTable,
	)
	idxType := fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS idx_%s_type ON %s(type);`,
		edgeTable, edgeTable,
	)
	idxFromType := fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS idx_%s_from_type ON %s(from_id, type);`,
		edgeTable, edgeTable,
	)
	idxToType := fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS idx_%s_to_type ON %s(to_id, type);`,
		edgeTable, edgeTable,
	)

	return []string{createNodes, createEdges, idxFromID, idxToID, idxType, idxFromType, idxToType}
}
