-- SQLite graph store schema for knowledge/graphstore/sqlite.
--
-- Replace:
--   {{NODE_TABLE_NAME}}  with the node table name (default: graph_nodes)
--   {{EDGE_TABLE_NAME}}  with the edge table name (default: graph_edges)

CREATE TABLE IF NOT EXISTS {{NODE_TABLE_NAME}} (
    id       TEXT PRIMARY KEY,
    name     TEXT NOT NULL DEFAULT '',
    content  TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS {{EDGE_TABLE_NAME}} (
    id       TEXT NOT NULL DEFAULT '',
    from_id  TEXT NOT NULL,
    to_id    TEXT NOT NULL,
    type     TEXT NOT NULL,
    metadata TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (from_id, to_id, type)
);

CREATE INDEX IF NOT EXISTS idx_edges_from_id   ON {{EDGE_TABLE_NAME}}(from_id);
CREATE INDEX IF NOT EXISTS idx_edges_to_id     ON {{EDGE_TABLE_NAME}}(to_id);
CREATE INDEX IF NOT EXISTS idx_edges_type      ON {{EDGE_TABLE_NAME}}(type);
CREATE INDEX IF NOT EXISTS idx_edges_from_type ON {{EDGE_TABLE_NAME}}(from_id, type);
CREATE INDEX IF NOT EXISTS idx_edges_to_type   ON {{EDGE_TABLE_NAME}}(to_id, type);
