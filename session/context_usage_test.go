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
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestComputeContextUsage_Basic(t *testing.T) {
	sess := &Session{
		Events: []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.NewSystemMessage("You are a helpful assistant."),
						},
					},
				},
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.NewUserMessage("Hello, how are you?"),
						},
					},
				},
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleAssistant,
								Content: "I'm doing well, thank you!",
							},
						},
					},
				},
			},
		},
	}

	usage, err := ComputeContextUsage(context.Background(), sess, "gpt-4o", nil, DefaultContextUsageConfig())
	if err != nil {
		t.Fatalf("ComputeContextUsage() error = %v", err)
	}

	if usage.ModelName != "gpt-4o" {
		t.Errorf("ModelName = %q, want %q", usage.ModelName, "gpt-4o")
	}
	if usage.ContextWindow != 128000 {
		t.Errorf("ContextWindow = %d, want 128000", usage.ContextWindow)
	}
	if usage.UsedTokens <= 0 {
		t.Errorf("UsedTokens = %d, want > 0", usage.UsedTokens)
	}
	if usage.RemainingTokens <= 0 {
		t.Errorf("RemainingTokens = %d, want > 0", usage.RemainingTokens)
	}

	// Check breakdown has expected categories.
	catMap := make(map[string]CategoryUsage)
	for _, b := range usage.Breakdown {
		catMap[b.Category] = b
	}

	if _, ok := catMap["system"]; !ok {
		t.Error("breakdown missing 'system' category")
	}
	if _, ok := catMap["user"]; !ok {
		t.Error("breakdown missing 'user' category")
	}
	if _, ok := catMap["assistant"]; !ok {
		t.Error("breakdown missing 'assistant' category")
	}

	// Verify counts.
	if catMap["system"].Count != 1 {
		t.Errorf("system count = %d, want 1", catMap["system"].Count)
	}
	if catMap["user"].Count != 1 {
		t.Errorf("user count = %d, want 1", catMap["user"].Count)
	}
	if catMap["assistant"].Count != 1 {
		t.Errorf("assistant count = %d, want 1", catMap["assistant"].Count)
	}
}

func TestComputeContextUsage_NilSession(t *testing.T) {
	_, err := ComputeContextUsage(context.Background(), nil, "gpt-4o", nil, DefaultContextUsageConfig())
	if err == nil {
		t.Error("expected error for nil session")
	}
}

func TestComputeContextUsage_EmptySession(t *testing.T) {
	sess := &Session{Events: []event.Event{}}
	usage, err := ComputeContextUsage(context.Background(), sess, "gpt-4o", nil, DefaultContextUsageConfig())
	if err != nil {
		t.Fatalf("ComputeContextUsage() error = %v", err)
	}
	if usage.UsedTokens != 0 {
		t.Errorf("UsedTokens = %d, want 0 for empty session", usage.UsedTokens)
	}
	if len(usage.Breakdown) != 0 {
		t.Errorf("Breakdown length = %d, want 0 for empty session", len(usage.Breakdown))
	}
}

func TestComputeContextUsage_WithReasoning(t *testing.T) {
	sess := &Session{
		Events: []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:             model.RoleAssistant,
								Content:          "The answer is 42.",
								ReasoningContent: "Let me think about this step by step...",
							},
						},
					},
				},
			},
		},
	}

	usage, err := ComputeContextUsage(context.Background(), sess, "gpt-4o", nil, DefaultContextUsageConfig())
	if err != nil {
		t.Fatalf("ComputeContextUsage() error = %v", err)
	}

	catMap := make(map[string]CategoryUsage)
	for _, b := range usage.Breakdown {
		catMap[b.Category] = b
	}

	if _, ok := catMap["reasoning"]; !ok {
		t.Error("breakdown missing 'reasoning' category for message with ReasoningContent")
	}
	if catMap["reasoning"].Tokens <= 0 {
		t.Errorf("reasoning tokens = %d, want > 0", catMap["reasoning"].Tokens)
	}
}

func TestComputeContextUsage_WithOverhead(t *testing.T) {
	sess := &Session{
		Events: []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.NewUserMessage("Hello"),
						},
					},
				},
			},
		},
	}

	config := ContextUsageConfig{
		ProtocolOverheadTokens: 500,
		MaxInputTokensRatio:    1.0,
	}

	usage, err := ComputeContextUsage(context.Background(), sess, "gpt-4o", nil, config)
	if err != nil {
		t.Fatalf("ComputeContextUsage() error = %v", err)
	}

	catMap := make(map[string]CategoryUsage)
	for _, b := range usage.Breakdown {
		catMap[b.Category] = b
	}

	if _, ok := catMap["overhead"]; !ok {
		t.Error("breakdown missing 'overhead' category")
	}
	if catMap["overhead"].Tokens != 500 {
		t.Errorf("overhead tokens = %d, want 500", catMap["overhead"].Tokens)
	}
}

func TestComputeContextContents_Basic(t *testing.T) {
	sess := &Session{
		Events: []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.NewSystemMessage("You are a helpful assistant."),
						},
					},
				},
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.NewUserMessage("Please analyze this file."),
						},
					},
				},
			},
		},
	}

	contents, err := ComputeContextContents(context.Background(), sess, "gpt-4o", nil)
	if err != nil {
		t.Fatalf("ComputeContextContents() error = %v", err)
	}

	if contents.TotalItems != 2 {
		t.Errorf("TotalItems = %d, want 2", contents.TotalItems)
	}
	if len(contents.Items) != 2 {
		t.Errorf("Items length = %d, want 2", len(contents.Items))
	}

	// Check first item is system message.
	if contents.Items[0].Role != "system" {
		t.Errorf("Items[0].Role = %q, want %q", contents.Items[0].Role, "system")
	}
	if contents.Items[0].TextPreview == "" {
		t.Error("Items[0].TextPreview is empty")
	}

	// Check second item is user message.
	if contents.Items[1].Role != "user" {
		t.Errorf("Items[1].Role = %q, want %q", contents.Items[1].Role, "user")
	}
}

func TestComputeContextContents_WithFiles(t *testing.T) {
	msg := model.NewUserMessage("Here is a file.")
	msg.AddFileData("report.pdf", []byte("fake pdf content"), "application/pdf")
	msg.AddFileURL("data.csv", "https://example.com/data.csv", "text/csv")

	sess := &Session{
		Events: []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: msg},
					},
				},
			},
		},
	}

	contents, err := ComputeContextContents(context.Background(), sess, "gpt-4o", nil)
	if err != nil {
		t.Fatalf("ComputeContextContents() error = %v", err)
	}

	if len(contents.Items) != 1 {
		t.Fatalf("Items length = %d, want 1", len(contents.Items))
	}

	item := contents.Items[0]
	if len(item.Files) != 2 {
		t.Fatalf("Files length = %d, want 2", len(item.Files))
	}

	// Check data-backed file.
	if item.Files[0].Name != "report.pdf" {
		t.Errorf("Files[0].Name = %q, want %q", item.Files[0].Name, "report.pdf")
	}
	if item.Files[0].Source != "data" {
		t.Errorf("Files[0].Source = %q, want %q", item.Files[0].Source, "data")
	}
	if item.Files[0].SizeHint == "" {
		t.Error("Files[0].SizeHint is empty for data-backed file")
	}

	// Check URL-backed file.
	if item.Files[1].Name != "data.csv" {
		t.Errorf("Files[1].Name = %q, want %q", item.Files[1].Name, "data.csv")
	}
	if item.Files[1].Source != "url" {
		t.Errorf("Files[1].Source = %q, want %q", item.Files[1].Source, "url")
	}
}

func TestComputeContextContents_WithToolCalls(t *testing.T) {
	msg := model.Message{
		Role:    model.RoleAssistant,
		Content: "Let me search for that.",
		ToolCalls: []model.ToolCall{
			{
				ID:   "call_abc123",
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      "search_web",
					Arguments: []byte(`{"query": "test query"}`),
				},
			},
		},
	}

	sess := &Session{
		Events: []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: msg},
					},
				},
			},
		},
	}

	contents, err := ComputeContextContents(context.Background(), sess, "gpt-4o", nil)
	if err != nil {
		t.Fatalf("ComputeContextContents() error = %v", err)
	}

	if len(contents.Items) != 1 {
		t.Fatalf("Items length = %d, want 1", len(contents.Items))
	}

	item := contents.Items[0]
	if len(item.ToolCalls) != 1 {
		t.Fatalf("ToolCalls length = %d, want 1", len(item.ToolCalls))
	}

	if item.ToolCalls[0].ID != "call_abc123" {
		t.Errorf("ToolCalls[0].ID = %q, want %q", item.ToolCalls[0].ID, "call_abc123")
	}
	if item.ToolCalls[0].Name != "search_web" {
		t.Errorf("ToolCalls[0].Name = %q, want %q", item.ToolCalls[0].Name, "search_web")
	}
	if item.ToolCalls[0].ArgsPreview == "" {
		t.Error("ToolCalls[0].ArgsPreview is empty")
	}
}

func TestComputeContextContents_ToolResult(t *testing.T) {
	msg := model.NewToolMessage("call_abc123", "search_web", "Found 10 results for your query.")

	sess := &Session{
		Events: []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: msg},
					},
				},
			},
		},
	}

	contents, err := ComputeContextContents(context.Background(), sess, "gpt-4o", nil)
	if err != nil {
		t.Fatalf("ComputeContextContents() error = %v", err)
	}

	if len(contents.Items) != 1 {
		t.Fatalf("Items length = %d, want 1", len(contents.Items))
	}

	item := contents.Items[0]
	if item.ToolResult == nil {
		t.Fatal("ToolResult is nil")
	}
	if item.ToolResult.ToolID != "call_abc123" {
		t.Errorf("ToolResult.ToolID = %q, want %q", item.ToolResult.ToolID, "call_abc123")
	}
	if item.ToolResult.ToolName != "search_web" {
		t.Errorf("ToolResult.ToolName = %q, want %q", item.ToolResult.ToolName, "search_web")
	}
	if item.ToolResult.ContentPreview == "" {
		t.Error("ToolResult.ContentPreview is empty")
	}
}

func TestComputeContextContents_WithImages(t *testing.T) {
	msg := model.NewUserMessage("Look at this image.")
	msg.AddImageURL("https://example.com/photo.png", "high")

	sess := &Session{
		Events: []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: msg},
					},
				},
			},
		},
	}

	contents, err := ComputeContextContents(context.Background(), sess, "gpt-4o", nil)
	if err != nil {
		t.Fatalf("ComputeContextContents() error = %v", err)
	}

	if len(contents.Items) != 1 {
		t.Fatalf("Items length = %d, want 1", len(contents.Items))
	}

	item := contents.Items[0]
	if len(item.Images) != 1 {
		t.Fatalf("Images length = %d, want 1", len(item.Images))
	}
	if item.Images[0].URL != "https://example.com/photo.png" {
		t.Errorf("Images[0].URL = %q, want %q", item.Images[0].URL, "https://example.com/photo.png")
	}
	if item.Images[0].Detail != "high" {
		t.Errorf("Images[0].Detail = %q, want %q", item.Images[0].Detail, "high")
	}
}

func TestComputeContextContents_NilSession(t *testing.T) {
	_, err := ComputeContextContents(context.Background(), nil, "gpt-4o", nil)
	if err == nil {
		t.Error("expected error for nil session")
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		input string
		n     int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"short", 5, "short"},
		{"", 5, ""},
	}

	for _, tt := range tests {
		got := truncateString(tt.input, tt.n)
		if got != tt.want {
			t.Errorf("truncateString(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
		}
	}
}

func TestFormatByteSize(t *testing.T) {
	tests := []struct {
		bytes int
		want  string
	}{
		{500, "500B"},
		{1024, "1.0KB"},
		{1536, "1.5KB"},
		{1048576, "1.0MB"},
		{1073741824, "1.0GB"},
	}

	for _, tt := range tests {
		got := formatByteSize(tt.bytes)
		if got != tt.want {
			t.Errorf("formatByteSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestContextUsageConfig_EffectiveMaxInputTokens(t *testing.T) {
	tests := []struct {
		name          string
		config        ContextUsageConfig
		contextWindow int
		want          int
	}{
		{
			name:          "default config",
			config:        DefaultContextUsageConfig(),
			contextWindow: 128000,
			want:          128000,
		},
		{
			name: "with overhead and reserve",
			config: ContextUsageConfig{
				ProtocolOverheadTokens: 100,
				ReserveOutputTokens:    4096,
				MaxInputTokensRatio:    1.0,
			},
			contextWindow: 128000,
			want:          123804,
		},
		{
			name: "with safety margin",
			config: ContextUsageConfig{
				SafetyMarginRatio:   0.1,
				MaxInputTokensRatio: 1.0,
			},
			contextWindow: 100000,
			want:          90000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.EffectiveMaxInputTokens(tt.contextWindow)
			if got != tt.want {
				t.Errorf("EffectiveMaxInputTokens() = %d, want %d", got, tt.want)
			}
		})
	}
}
