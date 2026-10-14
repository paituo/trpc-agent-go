//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memextractor "trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

func yamlNode(t *testing.T, raw string) *yaml.Node {
	t.Helper()

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(raw), &node))
	return &node
}

func builtinKnowledgeEntry(t *testing.T, name, configYAML string) knowledgeEntry {
	t.Helper()
	return knowledgeEntry{
		Type:   "builtin",
		Name:   name,
		Config: yamlNode(t, configYAML),
	}
}

type stubSummarizer struct{}

func (stubSummarizer) ShouldSummarize(*session.Session) bool {
	return false
}

func (stubSummarizer) Summarize(
	_ context.Context,
	_ *session.Session,
) (string, error) {
	return "", nil
}

func (stubSummarizer) SetPrompt(string) {}

func (stubSummarizer) SetModel(model.Model) {}

func (stubSummarizer) Metadata() map[string]any { return nil }

var _ summary.SessionSummarizer = (*stubSummarizer)(nil)

type stubExtractor struct{}

func (stubExtractor) Extract(
	_ context.Context,
	_ []model.Message,
	_ []*memory.Entry,
) ([]*memextractor.Operation, error) {
	return nil, nil
}

func (stubExtractor) ShouldExtract(*memextractor.ExtractionContext) bool {
	return false
}

func (stubExtractor) SetPrompt(string) {}

func (stubExtractor) SetModel(model.Model) {}

func (stubExtractor) Metadata() map[string]any { return nil }

var _ memextractor.MemoryExtractor = (*stubExtractor)(nil)
