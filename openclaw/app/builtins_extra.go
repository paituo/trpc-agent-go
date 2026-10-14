//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build !minimal

package app

import "trpc.group/trpc-go/trpc-agent-go/openclaw/registry"

// init registers the optional heavy backends (Redis / MySQL / Postgres /
// ClickHouse / pgvector). These are excluded when building with the
// "minimal" build tag, which keeps only the SQLite + in-memory backends.
func init() {
	must(registry.RegisterSessionBackend(
		sessionBackendRedis,
		newRedisSessionBackend,
	))
	must(registry.RegisterSessionBackend(
		sessionBackendMySQL,
		newMySQLSessionBackend,
	))
	must(registry.RegisterSessionBackend(
		sessionBackendPostgres,
		newPostgresSessionBackend,
	))
	must(registry.RegisterSessionBackend(
		sessionBackendClickHouse,
		newClickHouseSessionBackend,
	))

	must(registry.RegisterMemoryBackend(
		memoryBackendRedis,
		newRedisMemoryBackend,
	))
	must(registry.RegisterMemoryBackend(
		memoryBackendMySQL,
		newMySQLMemoryBackend,
	))
	must(registry.RegisterMemoryBackend(
		memoryBackendPostgres,
		newPostgresMemoryBackend,
	))
	must(registry.RegisterMemoryBackend(
		memoryBackendPGVector,
		newPGVectorMemoryBackend,
	))
}
