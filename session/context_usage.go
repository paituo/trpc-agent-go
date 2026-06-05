//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package session

import (
	"context"
	"fmt"
	"sort"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// CategoryUsage represents the token usage for a specific category of content.
type CategoryUsage struct {
	// Category identifies the content category.
	// Possible values: "system", "user", "assistant", "tool", "reasoning", "overhead".
	Category string `json:"category"`
	// Tokens is the estimated number of tokens used by this category.
	Tokens int `json:"tokens"`
	// Count is the number of messages in this category.
	Count int `json:"count"`
}

// ContextUsage represents the context window usage details for a session.
type ContextUsage struct {
	// ModelName is the name of the model used for the session.
	ModelName string `json:"modelName"`
	// ContextWindow is the total context window size in tokens.
	ContextWindow int `json:"contextWindow"`
	// UsedTokens is the total number of tokens currently used.
	UsedTokens int `json:"usedTokens"`
	// RemainingTokens is the estimated number of tokens still available.
	RemainingTokens int `json:"remainingTokens"`
	// Breakdown contains per-category token usage details.
	Breakdown []CategoryUsage `json:"breakdown"`
	// UpdatedAt is the time when this usage was computed.
	UpdatedAt time.Time `json:"updatedAt"`
}

// ContextUsageConfig holds configuration for context usage computation.
type ContextUsageConfig struct {
	// ProtocolOverheadTokens is the number of tokens reserved for protocol overhead.
	ProtocolOverheadTokens int
	// ReserveOutputTokens is the number of tokens reserved for output generation.
	ReserveOutputTokens int
	// SafetyMarginRatio is the safety margin ratio for token counting inaccuracies.
	SafetyMarginRatio float64
	// MaxInputTokensRatio is the maximum input tokens ratio of the context window.
	MaxInputTokensRatio float64
}

// DefaultContextUsageConfig returns a ContextUsageConfig with sensible defaults.
func DefaultContextUsageConfig() ContextUsageConfig {
	return ContextUsageConfig{
		ProtocolOverheadTokens: 0,
		ReserveOutputTokens:    0,
		SafetyMarginRatio:      0.0,
		MaxInputTokensRatio:    1.0,
	}
}

// EffectiveMaxInputTokens computes the effective maximum input tokens given
// the context window and configuration.
func (c ContextUsageConfig) EffectiveMaxInputTokens(contextWindow int) int {
	maxInput := int(float64(contextWindow) * c.MaxInputTokensRatio)
	maxInput -= c.ProtocolOverheadTokens
	maxInput -= c.ReserveOutputTokens
	if c.SafetyMarginRatio > 0 {
		maxInput = int(float64(maxInput) * (1 - c.SafetyMarginRatio))
	}
	if maxInput < 0 {
		maxInput = 0
	}
	return maxInput
}

// ComputeContextUsage computes the context usage for a session.
// It iterates over the session events, extracts messages, and counts tokens
// by category using the provided token counter.
func ComputeContextUsage(
	ctx context.Context,
	sess *Session,
	modelName string,
	tokenCounter model.TokenCounter,
	config ContextUsageConfig,
) (*ContextUsage, error) {
	if sess == nil {
		return nil, fmt.Errorf("session is nil")
	}
	if tokenCounter == nil {
		tokenCounter = model.NewTokenCounter(modelName)
	}

	// Resolve context window.
	contextWindow := resolveContextWindow(modelName)

	// Extract messages from events and count tokens by category.
	categoryMap := make(map[string]*categoryAccumulator)
	var totalTokens int

	sess.EventMu.RLock()
	events := make([]event.Event, len(sess.Events))
	copy(events, sess.Events)
	sess.EventMu.RUnlock()

	for i := range events {
		evt := &events[i]
		if evt.Response == nil {
			continue
		}
		for _, choice := range evt.Response.Choices {
			msg := choice.Message
			if msg.Role == "" && msg.Content == "" && len(msg.ToolCalls) == 0 && msg.ReasoningContent == "" {
				continue
			}

			// Count tokens for the main message content.
			role := string(msg.Role)
			if role == "" {
				continue
			}

			tokens, err := tokenCounter.CountTokens(ctx, msg)
			if err != nil {
				// Skip messages that fail to count.
				continue
			}

			// Handle reasoning content separately if present.
			reasoningTokens := 0
			if msg.ReasoningContent != "" {
				// Create a temporary message with only reasoning content to count its tokens.
				reasoningMsg := model.Message{
					Role:    model.RoleAssistant,
					Content: msg.ReasoningContent,
				}
				rt, err := tokenCounter.CountTokens(ctx, reasoningMsg)
				if err == nil {
					reasoningTokens = rt
				}
			}

			// Add to the appropriate category.
			acc := getOrCreateAccumulator(categoryMap, role)
			acc.tokens += tokens
			acc.count++

			// If there's reasoning content, track it separately.
			if reasoningTokens > 0 {
				reasoningAcc := getOrCreateAccumulator(categoryMap, "reasoning")
				reasoningAcc.tokens += reasoningTokens
				reasoningAcc.count++
			}

			totalTokens += tokens
		}
	}

	// Add overhead if configured.
	if config.ProtocolOverheadTokens > 0 {
		overheadAcc := getOrCreateAccumulator(categoryMap, "overhead")
		overheadAcc.tokens += config.ProtocolOverheadTokens
		overheadAcc.count = 1
		totalTokens += config.ProtocolOverheadTokens
	}

	// Build breakdown.
	breakdown := make([]CategoryUsage, 0, len(categoryMap))
	for cat, acc := range categoryMap {
		breakdown = append(breakdown, CategoryUsage{
			Category: cat,
			Tokens:   acc.tokens,
			Count:    acc.count,
		})
	}
	// Sort by category name for deterministic output.
	sort.Slice(breakdown, func(i, j int) bool {
		return breakdown[i].Category < breakdown[j].Category
	})

	// Compute remaining tokens.
	effectiveMax := config.EffectiveMaxInputTokens(contextWindow)
	remaining := effectiveMax - totalTokens
	if remaining < 0 {
		remaining = 0
	}

	return &ContextUsage{
		ModelName:       modelName,
		ContextWindow:   contextWindow,
		UsedTokens:      totalTokens,
		RemainingTokens: remaining,
		Breakdown:       breakdown,
		UpdatedAt:       time.Now(),
	}, nil
}

// categoryAccumulator accumulates token counts for a category.
type categoryAccumulator struct {
	tokens int
	count  int
}

// getOrCreateAccumulator returns the accumulator for the given category,
// creating one if it doesn't exist.
func getOrCreateAccumulator(m map[string]*categoryAccumulator, category string) *categoryAccumulator {
	acc, ok := m[category]
	if !ok {
		acc = &categoryAccumulator{}
		m[category] = acc
	}
	return acc
}

// resolveContextWindow resolves the context window size for a model name.
func resolveContextWindow(modelName string) int {
	if w, ok := model.LookupModelContextWindow(modelName); ok {
		return w
	}
	return 128000 // default fallback
}
