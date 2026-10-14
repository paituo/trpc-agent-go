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

import (
	"errors"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionredis "trpc.group/trpc-go/trpc-agent-go/session/redis"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

func newRedisSessionBackend(
	deps registry.SessionDeps,
	spec registry.SessionBackendSpec,
) (session.Service, error) {
	sessionURL := strings.TrimSpace(spec.Redis.URL)
	instance := strings.TrimSpace(spec.Redis.Instance)
	if sessionURL == "" && instance == "" {
		return nil, errors.New(
			"session redis backend requires url or instance",
		)
	}

	serviceOpts := make([]sessionredis.ServiceOpt, 0, 3)
	if sessionURL != "" {
		serviceOpts = append(
			serviceOpts,
			sessionredis.WithRedisClientURL(sessionURL),
		)
	}
	if instance != "" {
		serviceOpts = append(
			serviceOpts,
			sessionredis.WithRedisInstance(instance),
		)
	}
	keyPref := strings.TrimSpace(spec.Redis.KeyPrefix)
	if keyPref != "" {
		serviceOpts = append(
			serviceOpts,
			sessionredis.WithKeyPrefix(keyPref),
		)
	}
	if deps.Summarizer != nil {
		serviceOpts = append(
			serviceOpts,
			sessionredis.WithSummarizer(deps.Summarizer),
		)
	}
	return sessionredis.NewService(serviceOpts...)
}

func newRedisMemoryBackend(
	deps registry.MemoryDeps,
	spec registry.MemoryBackendSpec,
) (memory.Service, error) {
	memURL := strings.TrimSpace(spec.Redis.URL)
	instance := strings.TrimSpace(spec.Redis.Instance)
	if memURL == "" && instance == "" {
		return nil, errors.New(
			"memory redis backend requires url or instance",
		)
	}

	serviceOpts := make([]memredis.ServiceOpt, 0, 4)
	if memURL != "" {
		serviceOpts = append(
			serviceOpts,
			memredis.WithRedisClientURL(memURL),
		)
	}
	if instance != "" {
		serviceOpts = append(
			serviceOpts,
			memredis.WithRedisInstance(instance),
		)
	}
	keyPref := strings.TrimSpace(spec.Redis.KeyPrefix)
	if keyPref != "" {
		serviceOpts = append(
			serviceOpts,
			memredis.WithKeyPrefix(keyPref),
		)
	}
	if spec.Limit > 0 {
		serviceOpts = append(
			serviceOpts,
			memredis.WithMemoryLimit(spec.Limit),
		)
	}
	if deps.Extractor != nil {
		serviceOpts = append(
			serviceOpts,
			memredis.WithExtractor(deps.Extractor),
		)
	}
	return memredis.NewService(serviceOpts...)
}
