//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package trace provides invocation-aware tracing helpers.
package trace

import (
	"context"

	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	telemetrytrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

// RootSpanContextKey is the key used to store the root OpenTelemetry span
// context in the invocation's RunOptions.RuntimeState. This allows the
// runner's event-loop to propagate the agent's root span context to async
// workers (e.g. session summary) so that all LLM calls within a single
// request share the same Langfuse trace.
const RootSpanContextKey = "_root_span_ctx"

// StartSpan returns a no-op span when tracing is disabled for the invocation.
func StartSpan(ctx context.Context, invocation *agent.Invocation, spanName string) (context.Context, oteltrace.Span, bool) {
	if invocation != nil && invocation.RunOptions.DisableTracing {
		return ctx, noop.Span{}, false
	}
	ctx, span := telemetrytrace.Tracer.Start(ctx, spanName)
	// Store the first valid span context into the invocation so that the
	// runner's event-loop can later inject it into async worker contexts.
	if invocation != nil && span.SpanContext().IsValid() {
		opts := &invocation.RunOptions
		if opts.RuntimeState == nil {
			opts.RuntimeState = make(map[string]any, 1)
		}
		if _, ok := opts.RuntimeState[RootSpanContextKey]; !ok {
			opts.RuntimeState[RootSpanContextKey] = span.SpanContext()
		}
	}
	return ctx, span, true
}
