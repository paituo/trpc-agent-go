//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

//go:build cgo

package luaexec

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/sqlitevec"
)

// newSQLiteVecStore 创建 sqlitevec vector store。
// 仅在 cgo 可用时编译。
func newSQLiteVecStore(dsn string, dimension int) (vectorstore.VectorStore, string, bool, error) {
	sqliteVs, err := sqlitevec.New(
		sqlitevec.WithDSN(dsn),
		sqlitevec.WithIndexDimension(dimension),
		sqlitevec.WithEnableFTS(true),
	)
	if err != nil {
		return nil, "", false, fmt.Errorf("create sqlite vector store: %w", err)
	}
	return sqliteVs, "sqlite", true, nil
}
