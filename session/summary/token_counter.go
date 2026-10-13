//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summary

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/internal/summarytoken"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// modelTokenCounter returns the TokenCounter owned by m. It falls back to the
// process-wide shared default when m is nil or does not expose a counter, so
// callers never have to special-case an unset model.
func modelTokenCounter(m model.Model) model.TokenCounter {
	if m != nil {
		if tc := m.Info().TokenCounter; tc != nil {
			return tc
		}
	}
	return summarytoken.Get()
}

// summaryTokenCounterKey carries the summarizer's own model counter through ctx
// so that budget helpers, which only receive ctx, count against the model that
// actually performs the summarization.
type summaryTokenCounterKey struct{}

// withSummaryTokenCounter attaches the counter of the model that performs
// summarization to ctx. The budget helpers resolve it via
// resolveSummaryTokenCounter.
func withSummaryTokenCounter(
	ctx context.Context,
	m model.Model,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, summaryTokenCounterKey{}, modelTokenCounter(m))
}

// resolveSummaryTokenCounter resolves the counter for the model that performs
// summarization. It prefers the value attached by withSummaryTokenCounter and
// falls back to the shared default, so summary-request budgets are estimated
// with the summarization model rather than the agent's model.
func resolveSummaryTokenCounter(ctx context.Context) model.TokenCounter {
	if ctx != nil {
		if tc, ok := ctx.Value(summaryTokenCounterKey{}).(model.TokenCounter); ok &&
			tc != nil {
			return tc
		}
	}
	return summarytoken.Get()
}
