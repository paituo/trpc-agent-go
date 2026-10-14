//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import "trpc.group/trpc-go/trpc-agent-go/openclaw/registry"

func init() {
	must(registry.RegisterModel(modeMock, newMockModel))
	must(registry.RegisterModel(modeOpenAI, newOpenAIModel))

	must(registry.RegisterSessionBackend(
		sessionBackendInMemory,
		newInMemorySessionBackend,
	))
	must(registry.RegisterSessionBackend(
		sessionBackendSQLite,
		newSQLiteSessionBackend,
	))

	must(registry.RegisterMemoryBackend(
		memoryBackendFile,
		newDisabledMemoryBackend,
	))
	must(registry.RegisterMemoryBackend(
		memoryBackendInMemory,
		newInMemoryMemoryBackend,
	))
	must(registry.RegisterMemoryBackend(
		memoryBackendSQLite,
		newSQLiteMemoryBackend,
	))
	must(registry.RegisterMemoryBackend(
		memoryBackendSQLiteVec,
		newSQLiteVecMemoryBackend,
	))

	must(registry.RegisterKnowledgeProvider(
		"builtin",
		newBuiltinKnowledge,
	))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
