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
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Option represents a functional option for configuring fragment sources.
type Option func(*Source)

// WithName sets the source name.
func WithName(name string) Option {
	return func(s *Source) {
		s.name = name
	}
}

// WithMetadata sets custom metadata for the source.
func WithMetadata(metadata map[string]any) Option {
	return func(s *Source) {
		for k, v := range metadata {
			s.metadata[k] = v
		}
	}
}

// WithMetadataValue adds a single metadata key-value pair.
func WithMetadataValue(key string, value any) Option {
	return func(s *Source) {
		if s.metadata == nil {
			s.metadata = make(map[string]any)
		}
		s.metadata[key] = value
	}
}

// WithLogging enables or disables internal log output for the fragment source.
// Default is true (enabled). Set to false to suppress logging.
func WithLogging(enabled bool) Option {
	return func(s *Source) {
		s.logging = enabled
	}
}

// WithEmbedder sets the embedder used for semantic skeleton mounting.
// When both WithEmbedder and WithReranker are set, ReadGraph mounts
// fragments onto skeleton nodes via vector retrieval + reranking instead
// of the deterministic heading-path matcher.
func WithEmbedder(e embedder.Embedder) Option {
	return func(s *Source) {
		s.embedder = e
	}
}

// WithReranker sets the reranker used for semantic skeleton mounting.
// See WithEmbedder for how the two combine.
func WithReranker(r reranker.Reranker) Option {
	return func(s *Source) {
		s.reranker = r
	}
}

// WithKeywordMatchThreshold sets the per-keyword cosine floor used by the
// keyword-based semantic mounter. Each fragment is matched against the
// keywords of each skeleton node independently; a node is a candidate
// when any of its keyword cosines reaches this threshold. Default is 0.30.
func WithKeywordMatchThreshold(threshold float64) Option {
	return func(s *Source) {
		s.keywordMatchThreshold = threshold
	}
}

func WithRerankMatchThreshold(threshold float64) Option {
	return func(s *Source) {
		s.rerankMatchThreshold = threshold
	}
}

func WithRerankTopN(n int) Option {
	return func(s *Source) {
		s.rerankTopN = n
	}
}

// WithModel sets the LLM used for batch document classification.
// When set, ReadGraph will call the LLM to classify all docPaths into
// predefined categories and attach the result as trpc_ast_doc_category
// metadata on each document node.
//
// If not set, no classification is performed.
func WithModel(llm model.Model) Option {
	return func(s *Source) {
		s.llm = llm
	}
}

// WithDocClassifyPrompt overrides the default document classification prompt.
// The placeholder "[在此处粘贴你的文件列表]" will be replaced with the actual
// docPaths at runtime. If empty, the built-in default prompt is used.
func WithDocClassifyPrompt(prompt string) Option {
	return func(s *Source) {
		s.docClassifyPrompt = prompt
	}
}

func WithSourceDir(dir string) Option {
	return func(s *Source) {
		s.sourceDir = dir
	}
}
