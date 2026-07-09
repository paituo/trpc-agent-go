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
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
)

const (
	defaultDriverName    = "sqlite3"
	defaultDSN           = ":memory:"
	defaultNodeTableName = "graph_nodes"
	defaultEdgeTableName = "graph_edges"
)

type options struct {
	dsn           string
	driverName    string
	nodeTableName string
	edgeTableName string
	skipDBInit    bool
}

var defaultOptions = options{
	dsn:           defaultDSN,
	driverName:    defaultDriverName,
	nodeTableName: defaultNodeTableName,
	edgeTableName: defaultEdgeTableName,
}

type Option func(*options)

func WithDSN(dsn string) Option {
	return func(o *options) {
		if dsn != "" {
			o.dsn = dsn
		}
	}
}

func WithDriverName(name string) Option {
	return func(o *options) {
		if name != "" {
			o.driverName = name
		}
	}
}

func WithNodeTableName(name string) Option {
	if err := sqldb.ValidateTableName(name); err != nil {
		panic(fmt.Sprintf("sqlitegraph: invalid node table name: %v", err))
	}
	return func(o *options) {
		o.nodeTableName = name
	}
}

func WithEdgeTableName(name string) Option {
	if err := sqldb.ValidateTableName(name); err != nil {
		panic(fmt.Sprintf("sqlitegraph: invalid edge table name: %v", err))
	}
	return func(o *options) {
		o.edgeTableName = name
	}
}

func WithSkipDBInit(skip bool) Option {
	return func(o *options) {
		o.skipDBInit = skip
	}
}
