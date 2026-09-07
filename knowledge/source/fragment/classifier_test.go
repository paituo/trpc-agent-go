//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package fragment

import (
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// TestParseClassificationResults verifies that the LLM output is parsed
// into a docPath → category map correctly.
func TestParseClassificationResults(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  map[string]string
	}{
		{
			name:  "empty input",
			input: "",
			want:  map[string]string{},
		},
		{
			name:  "single line",
			input: "/docs/design.md -> 设计说明书",
			want:  map[string]string{"/docs/design.md": "设计说明书"},
		},
		{
			name: "multiple lines with noise",
			input: strings.Join([]string{
				"以下是分类结果：",
				"",
				"/path/to/说明书.md -> 设计说明书",
				"/path/to/材料清册.xlsx -> 设备材料清册",
				"some random text",
				"/path/to/report.md -> 专题报告",
			}, "\n"),
			want: map[string]string{
				"/path/to/说明书.md":    "设计说明书",
				"/path/to/材料清册.xlsx": "设备材料清册",
				"/path/to/report.md": "专题报告",
			},
		},
		{
			name:  "ignore lines without arrow",
			input: "无匹配行\n也没有匹配行",
			want:  map[string]string{},
		},
		{
			name:  "category with extra whitespace",
			input: "/docs/a.md   ->   设计图纸  ",
			want:  map[string]string{"/docs/a.md": "设计图纸"},
		},
		{
			name:  "待人工复核 category",
			input: "/docs/unknown.txt -> 待人工复核",
			want:  map[string]string{"/docs/unknown.txt": "待人工复核"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseClassificationResults(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d entries, want %d: got=%v want=%v", len(got), len(tt.want), got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("key %q: got %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

// TestResolveClassifyPrompt verifies that the default prompt is used when
// no override is configured, and that the override takes precedence.
func TestResolveClassifyPrompt(t *testing.T) {
	// Default prompt (no override).
	s := &Source{}
	got := s.resolveClassifyPrompt()
	if got != defaultDocClassifyPrompt {
		t.Errorf("default prompt mismatch: got %d chars, want %d chars", len(got), len(defaultDocClassifyPrompt))
	}
	if !strings.Contains(got, docClassifyPlaceholder) {
		t.Error("default prompt should contain the placeholder")
	}

	// Override.
	custom := "custom prompt: {{FILES}}"
	s.docClassifyPrompt = custom
	got = s.resolveClassifyPrompt()
	if got != custom {
		t.Errorf("override mismatch: got %q, want %q", got, custom)
	}
}

// mockModel is a minimal model.Model implementation for testing.
type mockModel struct {
	responses []string
	index     int
}

func (m *mockModel) GenerateContent(_ context.Context, _ *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	if m.index < len(m.responses) {
		content := m.responses[m.index]
		m.index++
		go func() {
			ch <- &model.Response{
				Choices: []model.Choice{
					{Message: model.Message{Content: content}},
				},
			}
			close(ch)
		}()
	} else {
		close(ch)
	}
	return ch, nil
}

func (m *mockModel) Info() model.Info {
	return model.Info{Name: "mock"}
}

// TestClassifyDocPathsEndToEnd tests the full classifyDocPaths flow with a
// mock model to verify prompt substitution and result parsing.
func TestClassifyDocPathsEndToEnd(t *testing.T) {
	llmOutput := "/a.md -> 设计说明书\n/b.md -> 设备材料清册\n/c.pdf -> 待人工复核"

	s := &Source{
		llm: &mockModel{
			responses: []string{llmOutput},
		},
		docPaths: []string{"/a.md", "/b.md", "/c.pdf"},
	}

	cats, err := s.classifyDocPaths(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string]string{
		"/a.md":  "设计说明书",
		"/b.md":  "设备材料清册",
		"/c.pdf": "待人工复核",
	}
	if len(cats) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(cats), len(want), cats)
	}
	for k, v := range want {
		if cats[k] != v {
			t.Errorf("key %q: got %q, want %q", k, cats[k], v)
		}
	}
}

// TestClassifyDocPathsNilLLM verifies that nil LLM returns nil, nil
// without error.
func TestClassifyDocPathsNilLLM(t *testing.T) {
	s := &Source{}
	cats, err := s.classifyDocPaths(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cats != nil {
		t.Errorf("expected nil map, got %v", cats)
	}
}

// TestDefaultDocClassifyPromptShape verifies that the default prompt
// contains the expected placeholder and category keywords.
func TestDefaultDocClassifyPromptShape(t *testing.T) {
	p := defaultDocClassifyPrompt
	for _, kw := range []string{
		"设计说明书",
		"设备材料清册",
		"专题报告",
		"设计图纸",
		"工程依据及支撑性文件",
		"待人工复核",
		docClassifyPlaceholder,
	} {
		if !strings.Contains(p, kw) {
			t.Errorf("default prompt missing keyword: %q", kw)
		}
	}
}

// TestWithDocClassifyPromptOption verifies that the option correctly sets
// the prompt on the Source.
func TestWithDocClassifyPromptOption(t *testing.T) {
	custom := "my custom prompt"
	opt := WithDocClassifyPrompt(custom)
	s := &Source{}
	opt(s)
	if s.docClassifyPrompt != custom {
		t.Errorf("got %q, want %q", s.docClassifyPrompt, custom)
	}
}

// TestWithModelOption verifies that the WithModel option sets the LLM.
func TestWithModelOption(t *testing.T) {
	m := &mockModel{responses: []string{}}
	opt := WithModel(m)
	s := &Source{}
	opt(s)
	if s.llm == nil {
		t.Fatal("expected LLM to be set")
	}
	if s.llm.Info().Name != "mock" {
		t.Errorf("got model name %q, want 'mock'", s.llm.Info().Name)
	}
}
