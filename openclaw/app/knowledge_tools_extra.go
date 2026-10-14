//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build !minimal

package app

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	vectors "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/elasticsearch"
	vectorpg "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

// init registers the optional heavy vector stores (pgvector / elasticsearch).
// These are excluded when building with the "minimal" build tag, which keeps
// only the in-memory and sqlitevec vector stores.
func init() {
	knowledgeVectorStoreBuilders["pgvector"] = buildPGVectorKnowledgeVectorStore
	knowledgeVectorStoreBuilders["elasticsearch"] = buildElasticsearchKnowledgeVectorStore
}

type pgvectorKnowledgeVectorStoreConfig struct {
	Type           string `yaml:"type,omitempty"`
	URL            string `yaml:"url,omitempty"`
	Table          string `yaml:"table,omitempty"`
	EnableTSVector *bool  `yaml:"enable_tsvector,omitempty"`
	IndexDimension *int   `yaml:"index_dimension,omitempty"`
	MaxResults     *int   `yaml:"max_results,omitempty"`
}

type elasticsearchKnowledgeVectorStoreConfig struct {
	Type            string   `yaml:"type,omitempty"`
	Addresses       []string `yaml:"addresses,omitempty"`
	Username        string   `yaml:"username,omitempty"`
	Password        string   `yaml:"password,omitempty"`
	APIKey          string   `yaml:"api_key,omitempty"`
	IndexName       string   `yaml:"index_name,omitempty"`
	VectorDimension *int     `yaml:"vector_dimension,omitempty"`
	MaxResults      *int     `yaml:"max_results,omitempty"`
}

func buildPGVectorKnowledgeVectorStore(
	node *yaml.Node,
	ctx knowledgeVectorStoreBuildContext,
) (vectorstore.VectorStore, error) {
	var cfg pgvectorKnowledgeVectorStoreConfig
	if err := registry.DecodeStrict(node, &cfg); err != nil {
		return nil, err
	}

	if strings.TrimSpace(cfg.URL) == "" {
		return nil, fmt.Errorf("pgvector requires vector_store.url")
	}

	opts := []vectorpg.Option{
		vectorpg.WithPGVectorClientDSN(strings.TrimSpace(cfg.URL)),
	}
	if v := strings.TrimSpace(cfg.Table); v != "" {
		opts = append(opts, vectorpg.WithTable(v))
	}
	if cfg.EnableTSVector != nil {
		opts = append(opts, vectorpg.WithEnableTSVector(*cfg.EnableTSVector))
	}
	if cfg.IndexDimension != nil && *cfg.IndexDimension > 0 {
		opts = append(opts, vectorpg.WithIndexDimension(*cfg.IndexDimension))
	} else if dims := knowledgeEmbedderDimensions(ctx.embedder); dims > 0 {
		opts = append(opts, vectorpg.WithIndexDimension(dims))
	}
	if cfg.MaxResults != nil && *cfg.MaxResults > 0 {
		opts = append(opts, vectorpg.WithMaxResults(*cfg.MaxResults))
	}
	return vectorpg.New(opts...)
}

func buildElasticsearchKnowledgeVectorStore(
	node *yaml.Node,
	ctx knowledgeVectorStoreBuildContext,
) (vectorstore.VectorStore, error) {
	var cfg elasticsearchKnowledgeVectorStoreConfig
	if err := registry.DecodeStrict(node, &cfg); err != nil {
		return nil, err
	}

	if len(cfg.Addresses) == 0 {
		return nil, fmt.Errorf("elasticsearch requires vector_store.addresses")
	}

	opts := []vectors.Option{vectors.WithAddresses(cfg.Addresses)}
	if v := strings.TrimSpace(cfg.Username); v != "" {
		opts = append(opts, vectors.WithUsername(v))
	}
	if v := strings.TrimSpace(cfg.Password); v != "" {
		opts = append(opts, vectors.WithPassword(v))
	}
	if v := strings.TrimSpace(cfg.APIKey); v != "" {
		opts = append(opts, vectors.WithAPIKey(v))
	}
	if v := strings.TrimSpace(cfg.IndexName); v != "" {
		opts = append(opts, vectors.WithIndexName(v))
	}
	if cfg.VectorDimension != nil && *cfg.VectorDimension > 0 {
		opts = append(opts, vectors.WithVectorDimension(*cfg.VectorDimension))
	} else if dims := knowledgeEmbedderDimensions(ctx.embedder); dims > 0 {
		opts = append(opts, vectors.WithVectorDimension(dims))
	}
	if cfg.MaxResults != nil && *cfg.MaxResults > 0 {
		opts = append(opts, vectors.WithMaxResults(*cfg.MaxResults))
	}
	return vectors.New(opts...)
}
