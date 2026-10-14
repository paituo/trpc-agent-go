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
	"fmt"
	"strings"

	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
)

// openAIEmbedderConfig is shared by the SQLite and SQL memory backends.
type openAIEmbedderConfig struct {
	Type         string `yaml:"type,omitempty"`
	Model        string `yaml:"model,omitempty"`
	Dimensions   int    `yaml:"dimensions,omitempty"`
	APIKey       string `yaml:"api_key,omitempty"`
	Organization string `yaml:"organization,omitempty"`
	BaseURL      string `yaml:"base_url,omitempty"`
}

func newOpenAIEmbedder(
	cfg *openAIEmbedderConfig,
) (*openaiembedder.Embedder, error) {
	if cfg == nil {
		return openaiembedder.New(), nil
	}

	t := strings.ToLower(strings.TrimSpace(cfg.Type))
	if t == "" {
		t = "openai"
	}
	if t != "openai" {
		return nil, fmt.Errorf("unsupported embedder type: %s", t)
	}

	opts := make([]openaiembedder.Option, 0, 5)
	if model := strings.TrimSpace(cfg.Model); model != "" {
		opts = append(opts, openaiembedder.WithModel(model))
	}
	if cfg.Dimensions > 0 {
		opts = append(opts, openaiembedder.WithDimensions(cfg.Dimensions))
	}
	if key := strings.TrimSpace(cfg.APIKey); key != "" {
		opts = append(opts, openaiembedder.WithAPIKey(key))
	}
	if org := strings.TrimSpace(cfg.Organization); org != "" {
		opts = append(opts, openaiembedder.WithOrganization(org))
	}
	if baseURL := strings.TrimSpace(cfg.BaseURL); baseURL != "" {
		opts = append(opts, openaiembedder.WithBaseURL(baseURL))
	}

	return openaiembedder.New(opts...), nil
}

func safeOption[O any](mk func(string) O, value string) (opt O, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("invalid value %q: %v", value, r)
		}
	}()
	return mk(value), nil
}
