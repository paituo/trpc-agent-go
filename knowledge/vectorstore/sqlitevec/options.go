//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package sqlitevec provides a sqlite-vec-backed implementation of the
// knowledge vector store.
package sqlitevec

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
)

const (
	defaultDriverName        = "sqlite3"
	defaultDSN               = ":memory:"
	defaultTableName         = "knowledge_documents"
	defaultMetadataTableName = "knowledge_document_meta"
	defaultIndexDimension    = 1536
	defaultMaxResults        = 10
)

type options struct {
	dsn               string
	driverName        string
	tableName         string
	metadataTableName string
	indexDimension    int
	maxResults        int
	skipDBInit        bool

	// 混合检索配置
	enableFTS          bool    // 是否启用全文搜索（默认 false，向后兼容）
	ftsTableName       string  // FTS5 表名
	rrfK               int     // RRF 常数 k（默认 60）
	rrfCandidateRatio  int     // RRF 候选比例（默认 3）
	sparseNormConstant float64 // 文本分数归一化常数（默认 0.1）
}

var defaultOptions = options{
	dsn:               defaultDSN,
	driverName:        defaultDriverName,
	tableName:         defaultTableName,
	metadataTableName: defaultMetadataTableName,
	indexDimension:    defaultIndexDimension,
	maxResults:        defaultMaxResults,
	ftsTableName:      "knowledge_documents_fts",
	rrfK:              60,
	rrfCandidateRatio: 3,
	sparseNormConstant: 0.1,
}

// Option configures the sqlitevec vector store.
type Option func(*options)

// WithDSN sets the SQLite DSN used when opening a database internally.
// Common mattn/go-sqlite3 DSN examples:
//   - ":memory:" for an in-memory database
//   - "file:/tmp/knowledge.db?_busy_timeout=5000" for a local file
//   - "file::memory:?cache=shared" for a shared in-memory database
func WithDSN(dsn string) Option {
	return func(o *options) {
		if dsn != "" {
			o.dsn = dsn
		}
	}
}

// WithDriverName sets the SQL driver name used with WithDSN.
func WithDriverName(driverName string) Option {
	return func(o *options) {
		if driverName != "" {
			o.driverName = driverName
		}
	}
}

// WithTableName sets the vec0 table name.
func WithTableName(tableName string) Option {
	return func(o *options) {
		if err := sqldb.ValidateTableName(tableName); err != nil {
			panic(fmt.Sprintf("invalid table name: %v", err))
		}
		o.tableName = tableName
	}
}

// WithMetadataTableName sets the metadata index table name.
func WithMetadataTableName(tableName string) Option {
	return func(o *options) {
		if err := sqldb.ValidateTableName(tableName); err != nil {
			panic(fmt.Sprintf("invalid metadata table name: %v", err))
		}
		o.metadataTableName = tableName
	}
}

// WithIndexDimension sets the embedding dimension.
func WithIndexDimension(dimension int) Option {
	return func(o *options) {
		if dimension > 0 {
			o.indexDimension = dimension
		}
	}
}

// WithMaxResults sets the default search result limit.
func WithMaxResults(maxResults int) Option {
	return func(o *options) {
		if maxResults > 0 {
			o.maxResults = maxResults
		}
	}
}

// WithSkipDBInit skips schema initialization.
func WithSkipDBInit(skip bool) Option {
	return func(o *options) {
		o.skipDBInit = skip
	}
}

// WithEnableFTS enables full-text search using FTS5 and gse segmentation.
// When disabled (default), SearchModeHybrid falls back to vector search and
// SearchModeKeyword returns an error.
// Note: requires building with -tags=sqlite_fts5 to enable FTS5 in the
// SQLite driver.
func WithEnableFTS(enable bool) Option {
	return func(o *options) {
		o.enableFTS = enable
	}
}

// WithFTSTableName sets the FTS5 virtual table name.
func WithFTSTableName(name string) Option {
	return func(o *options) {
		if err := sqldb.ValidateTableName(name); err != nil {
			panic(fmt.Sprintf("invalid fts table name: %v", err))
		}
		o.ftsTableName = name
	}
}

// WithRRFParams sets the parameters for Reciprocal Rank Fusion.
// k: RRF constant (default 60, must be > 0).
// candidateRatio: fetch limit * candidateRatio candidates from each
// sub-search (default 3, must be > 0).
func WithRRFParams(k, candidateRatio int) Option {
	return func(o *options) {
		if k > 0 {
			o.rrfK = k
		}
		if candidateRatio > 0 {
			o.rrfCandidateRatio = candidateRatio
		}
	}
}
