//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/query"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	coherereranker "trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/cohere"
	infinityreranker "trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/infinity"
	topkreranker "trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/topk"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	dirknowledge "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
	urlknowledge "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	vectors "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/elasticsearch"
	inmemoryvs "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	vectorpg "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
	vectorsqlite "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/sqlitevec"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

const (
	genericKnowledgeToolName = "knowledge_search"
	knowledgeToolNameSuffix  = "_knowledge_search"
)

// knowledgeEntry is the parsed intermediate representation of a knowledge
// provider configuration.
type knowledgeEntry struct {
	Type        string
	Name        string
	Description string
	MaxResults  int
	Config      *yaml.Node
}

type knowledgeToolsBundle struct {
	tools []tool.Tool
}

type rawKnowledgeComponent struct {
	Node *yaml.Node
}

func (r *rawKnowledgeComponent) UnmarshalYAML(node *yaml.Node) error {
	r.Node = node
	return nil
}

// builtinKnowledgeConfig is the config schema for the "builtin" knowledge
// provider (embedder + vector_store + reranker + sources + query_enhancer +
// agentic_filter_info).
type builtinKnowledgeConfig struct {
	Embedder          *rawKnowledgeComponent   `yaml:"embedder,omitempty"`
	VectorStore       *rawKnowledgeComponent   `yaml:"vector_store,omitempty"`
	Reranker          *rawKnowledgeComponent   `yaml:"reranker,omitempty"`
	Sources           []*rawKnowledgeComponent `yaml:"sources,omitempty"`
	QueryEnhancer     *rawKnowledgeComponent   `yaml:"query_enhancer,omitempty"`
	AgenticFilterInfo map[string][]any         `yaml:"agentic_filter_info,omitempty"`
}

func newBuiltinKnowledge(
	_ registry.KnowledgeProviderDeps,
	spec registry.PluginSpec,
) (knowledge.Knowledge, map[string][]any, error) {
	var cfg builtinKnowledgeConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, nil, fmt.Errorf("decode failed: %w", err)
	}
	emb, err := buildKnowledgeEmbedder(cfg.Embedder)
	if err != nil {
		return nil, nil, err
	}
	store, err := buildKnowledgeVectorStore(cfg.VectorStore, emb)
	if err != nil {
		return nil, nil, err
	}

	kOpts := []knowledge.Option{
		knowledge.WithEmbedder(emb),
		knowledge.WithVectorStore(store),
	}

	// Build reranker if configured
	if cfg.Reranker != nil && cfg.Reranker.Node != nil {
		reranker, err := buildKnowledgeReranker(cfg.Reranker.Node)
		if err != nil {
			return nil, nil, fmt.Errorf("reranker config invalid: %w", err)
		}
		kOpts = append(kOpts, knowledge.WithReranker(reranker))
	}

	// Build sources if configured
	if len(cfg.Sources) > 0 {
		sources, err := buildKnowledgeSources(cfg.Sources)
		if err != nil {
			return nil, nil, fmt.Errorf("sources config invalid: %w", err)
		}
		kOpts = append(kOpts, knowledge.WithSources(sources))
	}

	// Build query enhancer if configured
	if cfg.QueryEnhancer != nil && cfg.QueryEnhancer.Node != nil {
		enhancer, err := buildKnowledgeQueryEnhancer(cfg.QueryEnhancer.Node)
		if err != nil {
			return nil, nil, fmt.Errorf("query_enhancer config invalid: %w", err)
		}
		kOpts = append(kOpts, knowledge.WithQueryEnhancer(enhancer))
	}

	kb := knowledge.New(kOpts...)

	// Load sources into the vector store when the store is empty.
	// This ensures documents from configured sources are indexed at startup
	// without re-indexing on every restart.
	if len(cfg.Sources) > 0 {
		ctx := context.Background()
		count, err := store.Count(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to count vector store: %w", err)
		}
		if count == 0 {
			if err := kb.Load(ctx, knowledge.WithSourceConcurrency(1)); err != nil {
				return nil, nil, fmt.Errorf("failed to load knowledge sources: %w", err)
			}
		}
	}

	return kb, cfg.AgenticFilterInfo, nil
}

func buildKnowledgeTools(
	entries []knowledgeEntry,
	enableAgenticFilter bool,
) (*knowledgeToolsBundle, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	type resolvedKnowledge struct {
		kb               knowledge.Knowledge
		description      string
		maxResults       int
		agenticFilterInfo map[string][]any
	}
	knowledges := make(map[string]*resolvedKnowledge, len(entries))
	for _, entry := range entries {
		f, ok := registry.LookupKnowledgeProvider(entry.Type)
		if !ok {
			return nil, fmt.Errorf(
				"unsupported knowledge provider type: %s",
				entry.Type,
			)
		}
		kb, agenticFilterInfo, err := f(
			registry.KnowledgeProviderDeps{},
			registry.PluginSpec{
				Type:   entry.Type,
				Name:   entry.Name,
				Config: entry.Config,
			},
		)
		if err != nil {
			return nil, fmt.Errorf(
				"knowledge %q config invalid: %w",
				entry.Name,
				err,
			)
		}
		knowledges[entry.Name] = &resolvedKnowledge{
			kb:               kb,
			description:      entry.Description,
			maxResults:       entry.MaxResults,
			agenticFilterInfo: agenticFilterInfo,
		}
	}

	names := make([]string, 0, len(knowledges))
	for name := range knowledges {
		names = append(names, name)
	}
	sort.Strings(names)

	if len(names) == 1 {
		entry := knowledges[names[0]]
		desc := entry.description
		if desc == "" {
			desc = "Search for relevant information in the knowledge base."
		}
		toolOpts := []knowledgetool.Option{
			knowledgetool.WithToolName(genericKnowledgeToolName),
			knowledgetool.WithToolDescription(desc),
		}
		if entry.maxResults > 0 {
			toolOpts = append(
				toolOpts,
				knowledgetool.WithMaxResults(entry.maxResults),
			)
		}
		var searchTool tool.Tool
		if enableAgenticFilter {
			searchTool = knowledgetool.NewAgenticFilterSearchTool(
				entry.kb,
				entry.agenticFilterInfo,
				toolOpts...,
			)
		} else {
			searchTool = knowledgetool.NewKnowledgeSearchTool(
				entry.kb,
				toolOpts...,
			)
		}
		return &knowledgeToolsBundle{
			tools: []tool.Tool{
				newKnowledgeIndexTool(names[0], searchTool),
			},
		}, nil
	}

	tools := make([]tool.Tool, 0, len(names))
	seenToolNames := make(map[string]string, len(names))
	for _, name := range names {
		toolName := knowledgeToolName(name)
		if existing, ok := seenToolNames[toolName]; ok {
			return nil, fmt.Errorf(
				"knowledge tool name collision: %q and %q both map to %q",
				existing,
				name,
				toolName,
			)
		}
		seenToolNames[toolName] = name
		entry := knowledges[name]
		desc := entry.description
		if desc == "" {
			desc = fmt.Sprintf(
				"Search for relevant information in the %q knowledge base.",
				name,
			)
		}
		toolOpts := []knowledgetool.Option{
			knowledgetool.WithToolName(toolName),
			knowledgetool.WithToolDescription(desc),
		}
		if entry.maxResults > 0 {
			toolOpts = append(
				toolOpts,
				knowledgetool.WithMaxResults(entry.maxResults),
			)
		}
		var searchTool tool.Tool
		if enableAgenticFilter {
			searchTool = knowledgetool.NewAgenticFilterSearchTool(
				entry.kb,
				entry.agenticFilterInfo,
				toolOpts...,
			)
		} else {
			searchTool = knowledgetool.NewKnowledgeSearchTool(
				entry.kb,
				toolOpts...,
			)
		}
		tools = append(tools, newKnowledgeIndexTool(name, searchTool))
	}

	return &knowledgeToolsBundle{tools: tools}, nil
}

type knowledgeTypeConfig struct {
	Type string `yaml:"type,omitempty"`
}

type openAIKnowledgeEmbedderConfig struct {
	Type       string `yaml:"type,omitempty"`
	Model      string `yaml:"model,omitempty"`
	BaseURL    string `yaml:"base_url,omitempty"`
	APIKey     string `yaml:"api_key,omitempty"`
	Dimensions *int   `yaml:"dimensions,omitempty"`
}

type inmemoryKnowledgeVectorStoreConfig struct {
	Type       string `yaml:"type,omitempty"`
	MaxResults *int   `yaml:"max_results,omitempty"`
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

type sqlitevecKnowledgeVectorStoreConfig struct {
	Type           string `yaml:"type,omitempty"`
	DSN            string `yaml:"dsn,omitempty"`
	TableName      string `yaml:"table_name,omitempty"`
	MetaTableName  string `yaml:"metadata_table_name,omitempty"`
	IndexDimension *int   `yaml:"index_dimension,omitempty"`
	MaxResults     *int   `yaml:"max_results,omitempty"`
	SkipDBInit     *bool  `yaml:"skip_db_init,omitempty"`
}

type knowledgeVectorStoreBuildContext struct {
	embedder embedder.Embedder
}

type knowledgeEmbedderBuilder func(
	node *yaml.Node,
) (embedder.Embedder, error)

type knowledgeVectorStoreBuilder func(
	node *yaml.Node,
	ctx knowledgeVectorStoreBuildContext,
) (vectorstore.VectorStore, error)

var knowledgeEmbedderBuilders = map[string]knowledgeEmbedderBuilder{
	"":       buildOpenAIKnowledgeEmbedder,
	"openai": buildOpenAIKnowledgeEmbedder,
}

var knowledgeVectorStoreBuilders = map[string]knowledgeVectorStoreBuilder{
	"inmemory":      buildInMemoryKnowledgeVectorStore,
	"pgvector":      buildPGVectorKnowledgeVectorStore,
	"elasticsearch": buildElasticsearchKnowledgeVectorStore,
	"sqlitevec":     buildSQLiteVecKnowledgeVectorStore,
}

func buildKnowledgeEmbedder(
	cfg *rawKnowledgeComponent,
) (embedder.Embedder, error) {
	if cfg == nil || cfg.Node == nil {
		return buildOpenAIKnowledgeEmbedder(nil)
	}

	typeName, err := knowledgeComponentType(cfg.Node)
	if err != nil {
		return nil, fmt.Errorf("embedder type invalid: %w", err)
	}
	builder, ok := knowledgeEmbedderBuilders[typeName]
	if !ok {
		return nil, fmt.Errorf(
			"unsupported knowledge embedder type: %s",
			typeName,
		)
	}
	return builder(cfg.Node)
}

func buildKnowledgeVectorStore(
	cfg *rawKnowledgeComponent,
	emb embedder.Embedder,
) (vectorstore.VectorStore, error) {
	if cfg == nil || cfg.Node == nil {
		return nil, fmt.Errorf("vector_store is required")
	}

	typeName, err := knowledgeComponentType(cfg.Node)
	if err != nil {
		return nil, fmt.Errorf("vector_store type invalid: %w", err)
	}
	if typeName == "" {
		return nil, fmt.Errorf("vector_store.type is required")
	}
	builder, ok := knowledgeVectorStoreBuilders[typeName]
	if !ok {
		return nil, fmt.Errorf(
			"unsupported vector_store.type: %s",
			typeName,
		)
	}
	return builder(cfg.Node, knowledgeVectorStoreBuildContext{
		embedder: emb,
	})
}

func knowledgeComponentType(node *yaml.Node) (string, error) {
	if node == nil {
		return "", nil
	}

	var cfg knowledgeTypeConfig
	if err := node.Decode(&cfg); err != nil {
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(cfg.Type)), nil
}

func buildOpenAIKnowledgeEmbedder(
	node *yaml.Node,
) (embedder.Embedder, error) {
	var cfg openAIKnowledgeEmbedderConfig
	if err := registry.DecodeStrict(node, &cfg); err != nil {
		return nil, err
	}

	opts := make([]openaiembedder.Option, 0, 4)
	if v := strings.TrimSpace(cfg.Model); v != "" {
		opts = append(opts, openaiembedder.WithModel(v))
	}
	if v := strings.TrimSpace(cfg.BaseURL); v != "" {
		opts = append(opts, openaiembedder.WithBaseURL(v))
	}
	if v := strings.TrimSpace(cfg.APIKey); v != "" {
		opts = append(opts, openaiembedder.WithAPIKey(v))
	}
	if cfg.Dimensions != nil && *cfg.Dimensions > 0 {
		opts = append(opts, openaiembedder.WithDimensions(*cfg.Dimensions))
	}
	return openaiembedder.New(opts...), nil
}

func buildInMemoryKnowledgeVectorStore(
	node *yaml.Node,
	_ knowledgeVectorStoreBuildContext,
) (vectorstore.VectorStore, error) {
	var cfg inmemoryKnowledgeVectorStoreConfig
	if err := registry.DecodeStrict(node, &cfg); err != nil {
		return nil, err
	}

	opts := make([]inmemoryvs.Option, 0, 1)
	if cfg.MaxResults != nil && *cfg.MaxResults > 0 {
		opts = append(opts, inmemoryvs.WithMaxResults(*cfg.MaxResults))
	}
	return inmemoryvs.New(opts...), nil
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

func buildSQLiteVecKnowledgeVectorStore(
	node *yaml.Node,
	ctx knowledgeVectorStoreBuildContext,
) (vectorstore.VectorStore, error) {
	var cfg sqlitevecKnowledgeVectorStoreConfig
	if err := registry.DecodeStrict(node, &cfg); err != nil {
		return nil, err
	}

	opts := make([]vectorsqlite.Option, 0, 6)

	dsn := strings.TrimSpace(cfg.DSN)
	if dsn != "" {
		// Resolve relative paths in the DSN to absolute paths.
		resolved, err := resolveSQLiteVecDSN(dsn)
		if err != nil {
			return nil, fmt.Errorf("resolve dsn path: %w", err)
		}
		opts = append(opts, vectorsqlite.WithDSN(resolved))
	}

	if v := strings.TrimSpace(cfg.TableName); v != "" {
		opts = append(opts, vectorsqlite.WithTableName(v))
	}
	if v := strings.TrimSpace(cfg.MetaTableName); v != "" {
		opts = append(opts, vectorsqlite.WithMetadataTableName(v))
	}
	if cfg.IndexDimension != nil && *cfg.IndexDimension > 0 {
		opts = append(opts, vectorsqlite.WithIndexDimension(*cfg.IndexDimension))
	} else if dims := knowledgeEmbedderDimensions(ctx.embedder); dims > 0 {
		opts = append(opts, vectorsqlite.WithIndexDimension(dims))
	}
	if cfg.MaxResults != nil && *cfg.MaxResults > 0 {
		opts = append(opts, vectorsqlite.WithMaxResults(*cfg.MaxResults))
	}
	if cfg.SkipDBInit != nil {
		opts = append(opts, vectorsqlite.WithSkipDBInit(*cfg.SkipDBInit))
	}

	return vectorsqlite.New(opts...)
}

// resolveSQLiteVecDSN resolves relative file paths in a SQLite DSN to absolute
// paths. It supports both plain paths (e.g. "data/db.db") and SQLite file: URI
// schemes (e.g. "file:data/db.db?_busy_timeout=5000"). In-memory databases
// (":memory:") are returned as-is.
func resolveSQLiteVecDSN(dsn string) (string, error) {
	if dsn == ":memory:" {
		return dsn, nil
	}

	// Check for the SQLite file: URI scheme.
	var path string
	var query string
	rest := dsn
	if strings.HasPrefix(rest, "file:") {
		rest = rest[len("file:"):]
	}

	// Split off query parameters (everything after the first '?').
	if idx := strings.Index(rest, "?"); idx >= 0 {
		path = rest[:idx]
		query = rest[idx:]
	} else {
		path = rest
	}

	if path == "" {
		return dsn, nil
	}

	// If the path is already absolute, return as-is.
	if filepath.IsAbs(path) {
		return dsn, nil
	}

	// Resolve the relative path to an absolute path.
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to resolve relative path %q: %w", path, err)
	}

	// Reconstruct the DSN. If the original had a "file:" prefix, preserve it.
	if strings.HasPrefix(dsn, "file:") {
		return "file:" + absPath + query, nil
	}
	return absPath + query, nil
}

// ---------- Reranker support ----------

type knowledgeRerankerConfig struct {
	Type     string `yaml:"type,omitempty"`
	K        *int   `yaml:"k,omitempty"`
	Model    string `yaml:"model,omitempty"`
	APIKey   string `yaml:"api_key,omitempty"`
	Endpoint string `yaml:"endpoint,omitempty"`
	URL      string `yaml:"url,omitempty"`
	TopN     *int   `yaml:"top_n,omitempty"`
}

func buildKnowledgeReranker(node *yaml.Node) (reranker.Reranker, error) {
	var cfg knowledgeRerankerConfig
	if err := registry.DecodeStrict(node, &cfg); err != nil {
		return nil, err
	}

	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "topk", "":
		opts := make([]topkreranker.Option, 0, 1)
		if cfg.K != nil && *cfg.K > 0 {
			opts = append(opts, topkreranker.WithK(*cfg.K))
		}
		return topkreranker.New(opts...), nil

	case "infinity":
		opts := make([]infinityreranker.Option, 0, 4)
		if v := strings.TrimSpace(cfg.Model); v != "" {
			opts = append(opts, infinityreranker.WithModel(v))
		}
		if v := strings.TrimSpace(cfg.APIKey); v != "" {
			opts = append(opts, infinityreranker.WithAPIKey(v))
		}
		endpoint := strings.TrimSpace(cfg.Endpoint)
		if endpoint == "" {
			endpoint = strings.TrimSpace(cfg.URL)
		}
		if endpoint != "" {
			opts = append(opts, infinityreranker.WithEndpoint(endpoint))
		}
		if cfg.TopN != nil && *cfg.TopN > 0 {
			opts = append(opts, infinityreranker.WithTopN(*cfg.TopN))
		}
		return infinityreranker.New(opts...)

	case "cohere":
		opts := make([]coherereranker.Option, 0, 4)
		if v := strings.TrimSpace(cfg.Model); v != "" {
			opts = append(opts, coherereranker.WithModel(v))
		}
		if v := strings.TrimSpace(cfg.APIKey); v != "" {
			opts = append(opts, coherereranker.WithAPIKey(v))
		}
		if v := strings.TrimSpace(cfg.Endpoint); v != "" {
			opts = append(opts, coherereranker.WithEndpoint(v))
		}
		if cfg.TopN != nil && *cfg.TopN > 0 {
			opts = append(opts, coherereranker.WithTopN(*cfg.TopN))
		}
		return coherereranker.New(opts...)

	default:
		return nil, fmt.Errorf("unsupported reranker type: %s", cfg.Type)
	}
}

// ---------- Source support ----------

type knowledgeSourceConfig struct {
	Name           string         `yaml:"name,omitempty"`
	Type           string         `yaml:"type,omitempty"`
	Paths          []string       `yaml:"paths,omitempty"`
	URLs           []string       `yaml:"urls,omitempty"`
	Recursive      *bool          `yaml:"recursive,omitempty"`
	FileExtensions []string       `yaml:"file_extensions,omitempty"`
	FileReaderType string         `yaml:"file_reader_type,omitempty"`
	Metadata       map[string]any `yaml:"metadata,omitempty"`
}

func buildKnowledgeSources(nodes []*rawKnowledgeComponent) ([]source.Source, error) {
	sources := make([]source.Source, 0, len(nodes))
	for i, comp := range nodes {
		if comp == nil || comp.Node == nil {
			continue
		}
		src, err := buildKnowledgeSource(comp.Node)
		if err != nil {
			return nil, fmt.Errorf("sources[%d] invalid: %w", i, err)
		}
		sources = append(sources, src)
	}
	return sources, nil
}

func buildKnowledgeSource(node *yaml.Node) (source.Source, error) {
	var cfg knowledgeSourceConfig
	if err := registry.DecodeStrict(node, &cfg); err != nil {
		return nil, err
	}

	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "dir", "":
		if len(cfg.Paths) == 0 {
			return nil, fmt.Errorf("dir source requires at least one path")
		}
		opts := make([]dirknowledge.Option, 0, 4)
		if v := strings.TrimSpace(cfg.Name); v != "" {
			opts = append(opts, dirknowledge.WithName(v))
		}
		if cfg.Recursive != nil {
			opts = append(opts, dirknowledge.WithRecursive(*cfg.Recursive))
		}
		if len(cfg.FileExtensions) > 0 {
			opts = append(opts, dirknowledge.WithFileExtensions(cfg.FileExtensions))
		}
		if len(cfg.Metadata) > 0 {
			opts = append(opts, dirknowledge.WithMetadata(cfg.Metadata))
		}
		return dirknowledge.New(cfg.Paths, opts...), nil

	case "url":
		if len(cfg.URLs) == 0 {
			return nil, fmt.Errorf("url source requires at least one url in 'urls' field")
		}
		urlOpts := make([]urlknowledge.Option, 0, 2)
		if v := strings.TrimSpace(cfg.Name); v != "" {
			urlOpts = append(urlOpts, urlknowledge.WithName(v))
		}
		if v := strings.TrimSpace(cfg.FileReaderType); v != "" {
			urlOpts = append(urlOpts, urlknowledge.WithFileReaderType(source.FileReaderType(v)))
		}
		if len(cfg.Metadata) > 0 {
			urlOpts = append(urlOpts, urlknowledge.WithMetadata(cfg.Metadata))
		}
		return urlknowledge.New(cfg.URLs, urlOpts...), nil

	default:
		return nil, fmt.Errorf("unsupported source type: %s", cfg.Type)
	}
}

// ---------- Query Enhancer support ----------

type llmQueryEnhancerConfig struct {
	Type         string `yaml:"type,omitempty"`
	Model        string `yaml:"model,omitempty"`
	BaseURL      string `yaml:"base_url,omitempty"`
	APIKey       string `yaml:"api_key,omitempty"`
	SystemPrompt string `yaml:"system_prompt,omitempty"`
}

func buildKnowledgeQueryEnhancer(node *yaml.Node) (query.Enhancer, error) {
	var cfg llmQueryEnhancerConfig
	if err := node.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode query_enhancer config: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "llm":
		if strings.TrimSpace(cfg.Model) == "" {
			return nil, fmt.Errorf(
				"query_enhancer type 'llm' requires 'model' field",
			)
		}
		m, err := buildQueryEnhancerModel(&cfg)
		if err != nil {
			return nil, err
		}
		opts := []query.LLMEnhancerOption{}
		if cfg.SystemPrompt != "" {
			opts = append(opts, query.WithSystemPrompt(cfg.SystemPrompt))
		}
		return query.NewLLMEnhancer(m, opts...), nil

	case "passthrough", "":
		return query.NewPassthroughEnhancer(), nil

	default:
		return nil, fmt.Errorf(
			"unsupported query_enhancer type: %s", cfg.Type,
		)
	}
}

func buildQueryEnhancerModel(cfg *llmQueryEnhancerConfig) (*openaimodel.Model, error) {
	opts := []openaimodel.Option{}
	if strings.TrimSpace(cfg.BaseURL) != "" {
		opts = append(opts, openaimodel.WithBaseURL(cfg.BaseURL))
	}
	if strings.TrimSpace(cfg.APIKey) != "" {
		opts = append(opts, openaimodel.WithAPIKey(cfg.APIKey))
	}
	return openaimodel.New(cfg.Model, opts...), nil
}

func knowledgeEmbedderDimensions(e embedder.Embedder) int {
	if e == nil {
		return 0
	}
	return e.GetDimensions()
}

func knowledgeToolName(name string) string {
	base := sanitizeKnowledgeToolSegment(name)
	if base == "" {
		base = "knowledge"
	}
	return base + knowledgeToolNameSuffix
}

type knowledgeIndexBaseTool struct {
	original  tool.Tool
	indexName string
}

func newKnowledgeIndexTool(indexName string, original tool.Tool) tool.Tool {
	if original == nil {
		return nil
	}
	base := &knowledgeIndexBaseTool{
		original:  original,
		indexName: strings.TrimSpace(indexName),
	}
	_, callable := original.(tool.CallableTool)
	_, streamable := original.(tool.StreamableTool)
	switch {
	case callable && streamable:
		return &knowledgeIndexCallableStreamableTool{
			knowledgeIndexBaseTool: base,
		}
	case streamable:
		return &knowledgeIndexStreamableTool{
			knowledgeIndexBaseTool: base,
		}
	case callable:
		return &knowledgeIndexCallableTool{
			knowledgeIndexBaseTool: base,
		}
	default:
		return base
	}
}

func (t *knowledgeIndexBaseTool) Declaration() *tool.Declaration {
	return t.original.Declaration()
}

func (t *knowledgeIndexBaseTool) KnowledgeIndexName() string {
	return t.indexName
}

func (t *knowledgeIndexBaseTool) SkipSummarization() bool {
	type skipper interface{ SkipSummarization() bool }
	if s, ok := t.original.(skipper); ok {
		return s.SkipSummarization()
	}
	return false
}

type knowledgeIndexCallableTool struct {
	*knowledgeIndexBaseTool
}

func (t *knowledgeIndexCallableTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (any, error) {
	return t.original.(tool.CallableTool).Call(ctx, jsonArgs)
}

type knowledgeIndexStreamableTool struct {
	*knowledgeIndexBaseTool
}

func (t *knowledgeIndexStreamableTool) StreamableCall(
	ctx context.Context,
	jsonArgs []byte,
) (*tool.StreamReader, error) {
	return t.original.(tool.StreamableTool).StreamableCall(ctx, jsonArgs)
}

type knowledgeIndexCallableStreamableTool struct {
	*knowledgeIndexBaseTool
}

func (t *knowledgeIndexCallableStreamableTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (any, error) {
	return t.original.(tool.CallableTool).Call(ctx, jsonArgs)
}

func (t *knowledgeIndexCallableStreamableTool) StreamableCall(
	ctx context.Context,
	jsonArgs []byte,
) (*tool.StreamReader, error) {
	return t.original.(tool.StreamableTool).StreamableCall(ctx, jsonArgs)
}

func sanitizeKnowledgeToolSegment(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}

	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		isAlpha := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		if isAlpha || isDigit {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}

	out := strings.Trim(b.String(), "_")
	if out == "" {
		return ""
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "kb_" + out
	}
	const maxBaseLen = 40
	if len(out) > maxBaseLen {
		out = strings.Trim(out[:maxBaseLen], "_")
	}
	return out
}
