//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

//go:build !cgo

package luaexec

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// newSQLiteVecStore 在无 cgo 时返回错误（sqlitevec 需要 cgo）。
func newSQLiteVecStore(dsn string, dimension int) (vectorstore.VectorStore, string, bool, error) {
	return nil, "", false, fmt.Errorf("sqlitevec requires cgo (CGO_ENABLED=1)")
}
