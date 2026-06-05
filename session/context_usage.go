//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package session

import (
	"context"
	"fmt"
	"sort"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// ---------------------------------------------------------------------------
// Dimension 1: Token usage overview (for bar chart rendering)
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Dimension 2: Context content inventory (for listing what's in the context)
// ---------------------------------------------------------------------------

// FileSummary holds a brief description of a file attached to a message.
type FileSummary struct {
	// Name is the filename.
	Name string `json:"name"`
	// MimeType is the MIME type (e.g. "application/pdf").
	MimeType string `json:"mimeType,omitempty"`
	// Source indicates where the file comes from: "url", "data", or "file_id".
	Source string `json:"source"`
	// SizeHint is a human-readable size string (e.g. "2.3KB"), only available for data-backed files.
	SizeHint string `json:"sizeHint,omitempty"`
}

// ImageSummary holds a brief description of an image attached to a message.
type ImageSummary struct {
	// URL is the image URL if available.
	URL string `json:"url,omitempty"`
	// Format is the image format (e.g. "png", "jpeg").
	Format string `json:"format,omitempty"`
	// Detail is the detail level: "low", "high", "auto".
	Detail string `json:"detail,omitempty"`
}

// AudioSummary holds a brief description of audio attached to a message.
type AudioSummary struct {
	// Format is the audio format (e.g. "wav", "mp3").
	Format string `json:"format"`
}

// ToolCallSummary holds a brief description of a tool call in a message.
type ToolCallSummary struct {
	// ID is the tool call ID.
	ID string `json:"id"`
	// Name is the tool function name.
	Name string `json:"name"`
	// ArgsPreview is the first 100 characters of the JSON-encoded arguments.
	ArgsPreview string `json:"argsPreview,omitempty"`
}

// ToolResultSummary holds a brief description of a tool result message.
type ToolResultSummary struct {
	// ToolID is the ID of the corresponding tool call.
	ToolID string `json:"toolId"`
	// ToolName is the name of the tool.
	ToolName string `json:"toolName,omitempty"`
	// ContentPreview is the first 100 characters of the tool result content.
	ContentPreview string `json:"contentPreview,omitempty"`
}

// ContextContentItem represents the content summary of a single message in the context.
type ContextContentItem struct {
	// Index is the position of this message in the context (0-based).
	Index int `json:"index"`
	// Role is the message role: "system", "user", "assistant", "tool".
	Role string `json:"role"`
	// Tokens is the estimated token count for this message.
	Tokens int `json:"tokens"`
	// TextPreview is the first 100 characters of the text content.
	TextPreview string `json:"textPreview,omitempty"`
	// Files lists file attachments in this message.
	Files []FileSummary `json:"files,omitempty"`
	// Images lists image attachments in this message.
	Images []ImageSummary `json:"images,omitempty"`
	// Audio lists audio attachments in this message.
	Audio []AudioSummary `json:"audio,omitempty"`
	// ToolCalls lists tool calls in this message.
	ToolCalls []ToolCallSummary `json:"toolCalls,omitempty"`
	// ToolResult describes the tool result if this is a tool message.
	ToolResult *ToolResultSummary `json:"toolResult,omitempty"`
	// HasReasoning indicates whether this message contains reasoning/thinking content.
	HasReasoning bool `json:"hasReasoning"`
}

// ContextContents represents the full content inventory of the current context.
type ContextContents struct {
	// ModelName is the name of the model.
	ModelName string `json:"modelName"`
	// TotalItems is the total number of content items.
	TotalItems int `json:"totalItems"`
	// Items is the list of content summaries for each message.
	Items []ContextContentItem `json:"items"`
	// UpdatedAt is the time when this inventory was computed.
	UpdatedAt time.Time `json:"updatedAt"`
}

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Core computation functions
// ---------------------------------------------------------------------------

const (
	// previewLen is the maximum number of characters included in text/args previews.
	previewLen = 100
	// defaultContextWindow is the fallback context window size when the model is unknown.
	defaultContextWindow = 128000
)

// ComputeContextUsage computes the context usage overview for a session.
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

	contextWindow := resolveContextWindow(modelName)

	categoryMap := make(map[string]*categoryAccumulator)
	var totalTokens int

	messages := extractMessagesFromSession(sess)
	for i := range messages {
		msg := &messages[i]
		role := string(msg.Role)
		if role == "" {
			continue
		}

		tokens, err := tokenCounter.CountTokens(ctx, *msg)
		if err != nil {
			continue
		}

		acc := getOrCreateAccumulator(categoryMap, role)
		acc.tokens += tokens
		acc.count++
		totalTokens += tokens

		// Track reasoning tokens separately.
		if msg.ReasoningContent != "" {
			reasoningMsg := model.Message{
				Role:    model.RoleAssistant,
				Content: msg.ReasoningContent,
			}
			rt, err := tokenCounter.CountTokens(ctx, reasoningMsg)
			if err == nil {
				racc := getOrCreateAccumulator(categoryMap, "reasoning")
				racc.tokens += rt
				racc.count++
				totalTokens += rt
			}
		}
	}

	if config.ProtocolOverheadTokens > 0 {
		oacc := getOrCreateAccumulator(categoryMap, "overhead")
		oacc.tokens += config.ProtocolOverheadTokens
		oacc.count = 1
		totalTokens += config.ProtocolOverheadTokens
	}

	breakdown := make([]CategoryUsage, 0, len(categoryMap))
	for cat, acc := range categoryMap {
		breakdown = append(breakdown, CategoryUsage{
			Category: cat,
			Tokens:   acc.tokens,
			Count:    acc.count,
		})
	}
	sort.Slice(breakdown, func(i, j int) bool {
		return breakdown[i].Category < breakdown[j].Category
	})

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

// ComputeContextContents computes the content inventory for a session.
// It iterates over the session events and extracts a summary of each message,
// including file names, image/audio info, tool call names, and text previews.
func ComputeContextContents(
	ctx context.Context,
	sess *Session,
	modelName string,
	tokenCounter model.TokenCounter,
) (*ContextContents, error) {
	if sess == nil {
		return nil, fmt.Errorf("session is nil")
	}
	if tokenCounter == nil {
		tokenCounter = model.NewTokenCounter(modelName)
	}

	messages := extractMessagesFromSession(sess)
	items := make([]ContextContentItem, 0, len(messages))

	for i := range messages {
		msg := &messages[i]
		role := string(msg.Role)
		if role == "" {
			continue
		}

		tokens, _ := tokenCounter.CountTokens(ctx, *msg)

		item := ContextContentItem{
			Index:  len(items),
			Role:   role,
			Tokens: tokens,
		}

		// Text preview.
		if msg.Content != "" {
			item.TextPreview = truncateString(msg.Content, previewLen)
		}

		// Extract attachments from ContentParts.
		for _, cp := range msg.ContentParts {
			switch cp.Type {
			case model.ContentTypeFile:
				if cp.File != nil {
					item.Files = append(item.Files, summarizeFile(cp.File))
				}
			case model.ContentTypeImage:
				if cp.Image != nil {
					item.Images = append(item.Images, summarizeImage(cp.Image))
				}
			case model.ContentTypeAudio:
				if cp.Audio != nil {
					item.Audio = append(item.Audio, summarizeAudio(cp.Audio))
				}
			case model.ContentTypeText:
				// Text parts are already captured in Content; skip.
			}
		}

		// Tool calls.
		for _, tc := range msg.ToolCalls {
			item.ToolCalls = append(item.ToolCalls, summarizeToolCall(&tc))
		}

		// Tool result.
		if msg.ToolID != "" {
			item.ToolResult = &ToolResultSummary{
				ToolID:         msg.ToolID,
				ToolName:       msg.ToolName,
				ContentPreview: truncateString(msg.Content, previewLen),
			}
		}

		// Reasoning.
		item.HasReasoning = msg.ReasoningContent != ""

		items = append(items, item)
	}

	return &ContextContents{
		ModelName:  modelName,
		TotalItems: len(items),
		Items:      items,
		UpdatedAt:  time.Now(),
	}, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// categoryAccumulator accumulates token counts for a category.
type categoryAccumulator struct {
	tokens int
	count  int
}

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
	return defaultContextWindow
}

// extractMessagesFromSession reads events from the session and extracts
// the Message from each event's Response.Choices.
func extractMessagesFromSession(sess *Session) []model.Message {
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	var messages []model.Message
	for i := range sess.Events {
		evt := &sess.Events[i]
		if evt.Response == nil {
			continue
		}
		for _, choice := range evt.Response.Choices {
			msg := choice.Message
			if msg.Role == "" && msg.Content == "" && len(msg.ToolCalls) == 0 && msg.ReasoningContent == "" && msg.ToolID == "" {
				continue
			}
			messages = append(messages, msg)
		}
	}
	return messages
}

// truncateString returns the first n characters of s, appending "..." if truncated.
func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// summarizeFile creates a FileSummary from a model.File.
func summarizeFile(f *model.File) FileSummary {
	fs := FileSummary{
		Name:     f.Name,
		MimeType: f.MimeType,
	}
	switch {
	case f.URL != "":
		fs.Source = "url"
	case len(f.Data) > 0:
		fs.Source = "data"
		fs.SizeHint = formatByteSize(len(f.Data))
	case f.FileID != "":
		fs.Source = "file_id"
	}
	return fs
}

// summarizeImage creates an ImageSummary from a model.Image.
func summarizeImage(img *model.Image) ImageSummary {
	return ImageSummary{
		URL:    img.URL,
		Format: img.Format,
		Detail: img.Detail,
	}
}

// summarizeAudio creates an AudioSummary from a model.Audio.
func summarizeAudio(a *model.Audio) AudioSummary {
	return AudioSummary{
		Format: a.Format,
	}
}

// summarizeToolCall creates a ToolCallSummary from a model.ToolCall.
func summarizeToolCall(tc *model.ToolCall) ToolCallSummary {
	return ToolCallSummary{
		ID:          tc.ID,
		Name:        tc.Function.Name,
		ArgsPreview: truncateString(string(tc.Function.Arguments), previewLen),
	}
}

// formatByteSize returns a human-readable byte size string.
func formatByteSize(bytes int) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1fGB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1fMB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1fKB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
