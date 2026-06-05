//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package model

import (
	"sort"
	"strings"
	"sync"
)

// TokenCounterRegistry manages model-prefix → counter mappings for model construction.
// It is used during model creation to resolve the appropriate TokenCounter.
// Runtime code should use model.Info().TokenCounter or TokenCounterForModel() instead.
type TokenCounterRegistry struct {
	mu      sync.RWMutex
	entries []registryEntry
}

type registryEntry struct {
	prefix  string
	counter TokenCounter
}

var globalRegistry = &TokenCounterRegistry{}

// RegisterRegistryEntry adds a model-prefix → counter mapping to the global registry.
func RegisterRegistryEntry(prefix string, counter TokenCounter) {
	globalRegistry.Register(prefix, counter)
}

func (r *TokenCounterRegistry) Register(prefix string, counter TokenCounter) {
	if prefix == "" || counter == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, registryEntry{prefix: prefix, counter: counter})
	sort.SliceStable(r.entries, func(i, j int) bool {
		return len(r.entries[i].prefix) > len(r.entries[j].prefix)
	})
}

// Lookup returns the TokenCounter for the given model name by longest prefix match.
// Returns nil if no prefix matches.
func (r *TokenCounterRegistry) Lookup(modelName string) TokenCounter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, entry := range r.entries {
		if strings.HasPrefix(modelName, entry.prefix) {
			return entry.counter
		}
	}
	return nil
}
