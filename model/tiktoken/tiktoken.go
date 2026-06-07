//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package tiktoken provides a tiktoken-go based token counter implementation
// that is compatible with the root model.TokenCounter interface.
package tiktoken

import (
	"context"
	"fmt"
	"strings"

	"github.com/tiktoken-go/tokenizer"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Counter implements a tiktoken-based token counter compatible with model.TokenCounter.
// It uses a tokenizer.Codec to encode message text and counts tokens as the
// length of the returned token slice.
type Counter struct {
	encoding tokenizer.Codec
}

// New creates a tiktoken-based counter.
//
// Parameters:
//   - modelName: OpenAI model name (e.g., "gpt-4o"). The tokenizer is chosen with tokenizer.ForModel.
//     If the model is not supported, falls back to cl100k_base.
//
// Returns:
// - *Counter on success; error if codec initialization fails.
func New(modelName string) (*Counter, error) {
	enc, err := tokenizer.ForModel(tokenizer.Model(modelName))
	if err != nil {
		// Fallback to cl100k_base for broad compatibility.
		enc, err = tokenizer.Get(tokenizer.Cl100kBase)
		if err != nil {
			return nil, fmt.Errorf("failed to get fallback tokenizer: %w", err)
		}
	}
	return &Counter{encoding: enc}, nil
}

// init auto-registers the tiktoken-based token counter factory with the root model package.
// When this package is imported (even as a blank import), model.NewTokenCounter
// will use tiktoken-go for qwen/deepseek and other known models.
func init() {
	model.SetTokenCounterFromModel(NewTokenCounter)
	registerKnownModels()
}

func registerKnownModels() {
	// Qwen/Qwq → o200k_base (vocab 151,936, tiktoken-compatible BPE)
	if c, err := newWithEncoding(tokenizer.O200kBase); err == nil {
		model.RegisterRegistryEntry("qwen", c)
		model.RegisterRegistryEntry("qwq", c)
		// GLM-5 uses Qwen tokenizer (vocab 151,936)
		model.RegisterRegistryEntry("glm-5", c)
		model.RegisterRegistryEntry("glm5", c)
		// Doubao (豆包) uses Qwen-compatible tokenizer
		model.RegisterRegistryEntry("doubao", c)
		// Hunyuan (混元) uses Qwen-compatible tokenizer
		model.RegisterRegistryEntry("hunyuan", c)
		// MiniMax uses Qwen-compatible tokenizer
		model.RegisterRegistryEntry("minimax", c)
		// Claude → o200k_base (close approximation)
		model.RegisterRegistryEntry("claude", c)
		// GPT-4o / GPT-4.1 → o200k_base (exact match)
		model.RegisterRegistryEntry("gpt-4o", c)
		model.RegisterRegistryEntry("gpt-4.1", c)
	} else {
		log.WarnfContext(context.Background(),
			"tiktoken: failed to load o200k_base encoding: %v", err)
	}

	// DeepSeek-V4 → o200k_base (better approximation for V4's custom tokenizer)
	// Must be registered BEFORE the generic "deepseek" entry so that
	// longest-prefix-match prefers "deepseek-v4" over "deepseek".
	if c, err := newWithEncoding(tokenizer.O200kBase); err == nil {
		model.RegisterRegistryEntry("deepseek-v4", c)
	}

	// DeepSeek (pre-V4) → cl100k_base (vocab 129,024, close approximation for direct API)
	if c, err := newWithEncoding(tokenizer.Cl100kBase); err == nil {
		model.RegisterRegistryEntry("deepseek", c)
		// Llama 3/4 → cl100k_base (vocab 128,256, close approximation)
		model.RegisterRegistryEntry("llama", c)
		// Yi (零一万物) → cl100k_base (vocab ~64,000, close approximation)
		model.RegisterRegistryEntry("yi-", c)
		// GPT-4 / GPT-3.5 → cl100k_base (exact match)
		model.RegisterRegistryEntry("gpt-4", c)
		model.RegisterRegistryEntry("gpt-3.5", c)
	} else {
		log.WarnfContext(context.Background(),
			"tiktoken: failed to load cl100k_base encoding for deepseek: %v", err)
	}

	// GLM-4 and earlier → SimpleTokenCounter(1.8) (Zhipu official ratio)
	// Note: glm-5 is registered above with o200k_base; this catches glm-4, glm-3, etc.
	model.RegisterRegistryEntry("glm",
		model.NewSimpleTokenCounter(model.WithApproxRunesPerToken(1.8)))
}

// CountTokens returns the token count for a single message using tiktoken-go.
// It encodes Message.Content, Message.ReasoningContent, text ContentParts, and ToolCalls.
func (c *Counter) CountTokens(_ context.Context, message model.Message) (int, error) {
	total := 0

	if message.Content != "" {
		toks, _, err := c.encoding.Encode(message.Content)
		if err != nil {
			return 0, fmt.Errorf("encode content failed: %w", err)
		}
		total += len(toks)
	}

	if message.ReasoningContent != "" {
		toks, _, err := c.encoding.Encode(message.ReasoningContent)
		if err != nil {
			return 0, fmt.Errorf("encode reasoning failed: %w", err)
		}
		total += len(toks)
	}

	for _, part := range message.ContentParts {
		if part.Text != nil {
			toks, _, err := c.encoding.Encode(*part.Text)
			if err != nil {
				return 0, fmt.Errorf("encode text part failed: %w", err)
			}
			total += len(toks)
		}
	}

	// Count tokens for tool calls.
	for _, toolCall := range message.ToolCalls {
		toolCallTokens, err := c.countToolCallTokens(toolCall)
		if err != nil {
			return 0, fmt.Errorf("encode tool call failed: %w", err)
		}
		total += toolCallTokens
	}

	return total, nil
}

// NewTokenCounter creates a model-aware TokenCounter based on the model name.
//
// Routing (longest-prefix-match):
//   - gpt-4o*, gpt-4.1*                → o200k_base (exact match)
//   - gpt-4*, gpt-3.5*                 → cl100k_base (exact match)
//   - qwen*, qwq*                      → o200k_base (vocab 151,936, tiktoken-compatible BPE)
//   - glm-5*, glm5*                    → o200k_base (GLM-5 uses Qwen tokenizer)
//   - doubao*                           → o200k_base (Qwen-compatible)
//   - hunyuan*                          → o200k_base (Qwen-compatible)
//   - minimax*                          → o200k_base (Qwen-compatible)
//   - claude*                           → o200k_base (close approximation)
//   - deepseek-v4*                      → o200k_base (better approximation for V4's tokenizer)
//   - deepseek*                         → cl100k_base (vocab 129,024, close approximation)
//   - llama*                            → cl100k_base (vocab 128,256, close approximation)
//   - yi-*                              → cl100k_base (vocab ~64,000, close approximation)
//   - glm*                              → SimpleTokenCounter(runes/1.8) (Zhipu official ratio)
//   - others                            → tiktoken by model name, fallback to SimpleTokenCounter
//
// This function should be registered at application startup via
// model.SetTokenCounterFromModel(tiktoken.NewTokenCounter) to enable
// tiktoken-based counters for all injection points.
func NewTokenCounter(modelName string) model.TokenCounter {
	name := strings.ToLower(strings.TrimSpace(modelName))

	switch {
	case strings.HasPrefix(name, "gpt-4o"), strings.HasPrefix(name, "gpt-4.1"):
		if c, err := newWithEncoding(tokenizer.O200kBase); err == nil {
			return c
		}
	case strings.HasPrefix(name, "gpt-4"), strings.HasPrefix(name, "gpt-3.5"):
		if c, err := newWithEncoding(tokenizer.Cl100kBase); err == nil {
			return c
		}
	case strings.HasPrefix(name, "qwen"), strings.HasPrefix(name, "qwq"):
		if c, err := newWithEncoding(tokenizer.O200kBase); err == nil {
			return c
		}
	case strings.HasPrefix(name, "glm-5"), strings.HasPrefix(name, "glm5"):
		if c, err := newWithEncoding(tokenizer.O200kBase); err == nil {
			return c
		}
	case strings.HasPrefix(name, "doubao"):
		if c, err := newWithEncoding(tokenizer.O200kBase); err == nil {
			return c
		}
	case strings.HasPrefix(name, "hunyuan"):
		if c, err := newWithEncoding(tokenizer.O200kBase); err == nil {
			return c
		}
	case strings.HasPrefix(name, "minimax"):
		if c, err := newWithEncoding(tokenizer.O200kBase); err == nil {
			return c
		}
	case strings.HasPrefix(name, "claude"):
		if c, err := newWithEncoding(tokenizer.O200kBase); err == nil {
			return c
		}
	case strings.HasPrefix(name, "deepseek-v4"):
		if c, err := newWithEncoding(tokenizer.O200kBase); err == nil {
			return c
		}
	case strings.HasPrefix(name, "deepseek"):
		if c, err := newWithEncoding(tokenizer.Cl100kBase); err == nil {
			return c
		}
	case strings.HasPrefix(name, "llama"):
		if c, err := newWithEncoding(tokenizer.Cl100kBase); err == nil {
			return c
		}
	case strings.HasPrefix(name, "yi-"):
		if c, err := newWithEncoding(tokenizer.Cl100kBase); err == nil {
			return c
		}
	case strings.HasPrefix(name, "glm"):
		return model.NewSimpleTokenCounter(model.WithApproxRunesPerToken(1.8))
	}

	if c, err := New(modelName); err == nil {
		return c
	}

	return model.NewSimpleTokenCounter()
}

func newWithEncoding(encoding tokenizer.Encoding) (*Counter, error) {
	enc, err := tokenizer.Get(encoding)
	if err != nil {
		return nil, fmt.Errorf("failed to get %s encoding: %w", encoding, err)
	}
	return &Counter{encoding: enc}, nil
}

// countToolCallTokens calculates the token count for a single tool call.
// It encodes the tool call's type, ID, function name, description, and arguments.
func (c *Counter) countToolCallTokens(toolCall model.ToolCall) (int, error) {
	total := 0

	// Count tokens for tool call type (e.g., "function").
	if toolCall.Type != "" {
		toks, _, err := c.encoding.Encode(toolCall.Type)
		if err != nil {
			return 0, fmt.Errorf("encode tool call type failed: %w", err)
		}
		total += len(toks)
	}

	// Count tokens for tool call ID.
	if toolCall.ID != "" {
		toks, _, err := c.encoding.Encode(toolCall.ID)
		if err != nil {
			return 0, fmt.Errorf("encode tool call ID failed: %w", err)
		}
		total += len(toks)
	}

	// Count tokens for function name.
	if toolCall.Function.Name != "" {
		toks, _, err := c.encoding.Encode(toolCall.Function.Name)
		if err != nil {
			return 0, fmt.Errorf("encode function name failed: %w", err)
		}
		total += len(toks)
	}

	// Count tokens for function description.
	if toolCall.Function.Description != "" {
		toks, _, err := c.encoding.Encode(toolCall.Function.Description)
		if err != nil {
			return 0, fmt.Errorf("encode function description failed: %w", err)
		}
		total += len(toks)
	}

	// Count tokens for function arguments (JSON string).
	if len(toolCall.Function.Arguments) > 0 {
		toks, _, err := c.encoding.Encode(string(toolCall.Function.Arguments))
		if err != nil {
			return 0, fmt.Errorf("encode function arguments failed: %w", err)
		}
		total += len(toks)
	}

	return total, nil
}

// CountTokensRange returns the token count for a range of messages using tiktoken-go.
// This is more efficient than calling CountTokens multiple times.
func (c *Counter) CountTokensRange(ctx context.Context, messages []model.Message, start, end int) (int, error) {
	if start < 0 || end > len(messages) || start >= end {
		return 0, fmt.Errorf("invalid range: start=%d, end=%d, len=%d", start, end, len(messages))
	}

	total := 0
	for i := start; i < end; i++ {
		tokens, err := c.CountTokens(ctx, messages[i])
		if err != nil {
			return 0, fmt.Errorf("count tokens for message %d failed: %w", i, err)
		}
		total += tokens
	}
	return total, nil
}

// RecordEstimate counts tokens for the given messages. tiktoken Counter
// does not support calibration, so this simply delegates to CountTokensRange.
func (c *Counter) RecordEstimate(ctx context.Context, messages []model.Message) (int, error) {
	return c.CountTokensRange(ctx, messages, 0, len(messages))
}
