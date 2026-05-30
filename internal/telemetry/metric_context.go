//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package telemetry provides metric context for agent runtime observability.
package telemetry

import (
	"context"
	"sync/atomic"
)

// ContextCompactionTriggered returns a counter of compaction trigger events.
var ContextCompactionTriggered atomic.Int64

// ContextTokenUsageRatio stores the last observed token usage ratio (0.0-1.0).
var ContextTokenUsageRatio atomic.Value // float64

func init() {
	ContextTokenUsageRatio.Store(float64(0))
}

// RecordCompactionTrigger increments the compaction trigger counter.
func RecordCompactionTrigger(ctx context.Context) {
	ContextCompactionTriggered.Add(1)
}

// RecordTokenUsageRatio records the last token budget usage ratio.
func RecordTokenUsageRatio(ctx context.Context, ratio float64) {
	ContextTokenUsageRatio.Store(ratio)
}

// GetCompactionTriggerCount returns the current compaction trigger count.
func GetCompactionTriggerCount() int64 {
	return ContextCompactionTriggered.Load()
}

// GetTokenUsageRatio returns the last recorded token usage ratio.
func GetTokenUsageRatio() float64 {
	v := ContextTokenUsageRatio.Load()
	if v == nil {
		return 0
	}
	return v.(float64)
}