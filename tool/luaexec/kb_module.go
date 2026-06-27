//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package luaexec

import (
	"context"
	"fmt"
	"strings"
	"sync"

	lua "github.com/yuin/gopher-lua"

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
)

// KBModuleConfig configures the knowledge base module for Lua scripts.
type KBModuleConfig struct {
	EmbedderType    string // "openai"
	EmbedderModel   string // "bge-m3"
	EmbedderBaseURL string // 必填
	EmbedderAPIKey  string // 必填
	Dimensions      int    // 默认 1024
	MaxChunkSize    int    // 默认 2000
	ChunkOverlap    int    // 默认 200
}

// kbStore holds a BuiltinKnowledge, its associated VectorStore, and a per-store
// RWMutex for concurrent access control.
type kbStore struct {
	kb   *knowledge.BuiltinKnowledge
	vs   vectorstore.VectorStore
	mu   sync.RWMutex // per-store 读写锁
	name string       // path/filename 唯一标识
}

// KBModule provides knowledge base operations to Lua scripts.
// It is a global singleton shared by all Lua VMs.
type KBModule struct {
	mu          sync.RWMutex
	stores      map[string]*kbStore // handle → store
	namedStores map[string]*kbStore // "path/filename" → store
	embedder    embedder.Embedder
	config      *KBModuleConfig
}

// NewKBModule creates a new KBModule with the given configuration.
func NewKBModule(cfg *KBModuleConfig) (*KBModule, error) {
	if cfg.EmbedderBaseURL == "" {
		return nil, fmt.Errorf("kb module: EmbedderBaseURL is required")
	}
	if cfg.EmbedderAPIKey == "" {
		return nil, fmt.Errorf("kb module: EmbedderAPIKey is required")
	}

	// Apply defaults.
	if cfg.Dimensions <= 0 {
		cfg.Dimensions = 1024
	}
	if cfg.MaxChunkSize <= 0 {
		cfg.MaxChunkSize = 2000
	}
	if cfg.ChunkOverlap <= 0 {
		cfg.ChunkOverlap = 200
	}

	// Create embedder.
	model := cfg.EmbedderModel
	if model == "" {
		model = "bge-m3"
	}
	emb := openaiembedder.New(
		openaiembedder.WithModel(model),
		openaiembedder.WithBaseURL(cfg.EmbedderBaseURL),
		openaiembedder.WithAPIKey(cfg.EmbedderAPIKey),
		openaiembedder.WithDimensions(cfg.Dimensions),
	)

	return &KBModule{
		stores:      make(map[string]*kbStore),
		namedStores: make(map[string]*kbStore),
		embedder:    emb,
		config:      cfg,
	}, nil
}

// Register registers the kb module functions into the Lua state.
func (m *KBModule) Register(L *lua.LState) {
	tbl := L.NewTable()
	L.SetField(tbl, "create_store", L.NewFunction(m.luaCreateStore))
	L.SetField(tbl, "embed", L.NewFunction(m.luaEmbed))
	L.SetField(tbl, "add_docs", L.NewFunction(m.luaAddDocs))
	L.SetField(tbl, "search", L.NewFunction(m.luaSearch))
	L.SetField(tbl, "close_store", L.NewFunction(m.luaCloseStore))
	L.SetField(tbl, "store_info", L.NewFunction(m.luaStoreInfo))
	L.SetGlobal("kb", tbl)
}

// luaCreateStore implements kb.create_store({path, filename}) -> {store_handle, ...}
//
// 路径即名称：path+filename 作为 store 的唯一标识。
// 如果同名 store 已存在，直接返回已有 handle（复用）。
// 检查+创建在同一个写锁临界区内完成，避免 TOCTOU 竞态条件。
func (m *KBModule) luaCreateStore(L *lua.LState) int {
	tbl := L.CheckTable(1)
	path := luaTableGetString(tbl, "path")
	filename := luaTableGetString(tbl, "filename")

	if path == "" || filename == "" {
		pushGoValue(L, map[string]any{"error": "create_store: path and filename are required"})
		return 1
	}

	storeName := path + "/" + filename

	// 检查+创建在同一个写锁临界区内
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.namedStores[storeName]; ok {
		result := map[string]any{
			"store_handle": storeName,
			"store_type":   "inmemory",
			"dimension":    m.config.Dimensions,
			"has_fts":      false,
			"reused":       true,
		}
		pushGoValue(L, result)
		return 1
	}

	// Create in-memory vector store.
	vs := inmemory.New()

	// Create BuiltinKnowledge.
	bk := knowledge.New(
		knowledge.WithEmbedder(m.embedder),
		knowledge.WithVectorStore(vs),
	)

	entry := &kbStore{kb: bk, vs: vs, name: storeName}
	m.stores[storeName] = entry
	m.namedStores[storeName] = entry

	result := map[string]any{
		"store_handle": storeName,
		"store_type":   "inmemory",
		"dimension":    m.config.Dimensions,
		"has_fts":      false,
		"reused":       false,
	}
	pushGoValue(L, result)
	return 1
}

// luaEmbed implements kb.embed({texts}) -> {embeddings, api_calls}
func (m *KBModule) luaEmbed(L *lua.LState) int {
	tbl := L.CheckTable(1)
	textsRaw := luaTableGetArray(tbl, "texts")

	if len(textsRaw) == 0 {
		pushGoValue(L, map[string]any{"error": "embed: texts array is required"})
		return 1
	}

	ctx := L.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	embeddings := make([][]float64, 0, len(textsRaw))
	apiCalls := 0

	for _, text := range textsRaw {
		textStr, ok := text.(string)
		if !ok {
			pushGoValue(L, map[string]any{"error": "embed: each text must be a string"})
			return 1
		}
		emb, err := m.embedder.GetEmbedding(ctx, textStr)
		if err != nil {
			pushGoValue(L, map[string]any{"error": fmt.Sprintf("embed: failed to get embedding: %v", err)})
			return 1
		}
		embeddings = append(embeddings, emb)
		apiCalls++
	}

	result := map[string]any{
		"embeddings": embeddings,
		"api_calls":  apiCalls,
	}
	pushGoValue(L, result)
	return 1
}

// luaAddDocs implements kb.add_docs({store_handle, documents}) -> {added}
// 使用 per-store 写锁保证并发安全。
func (m *KBModule) luaAddDocs(L *lua.LState) int {
	tbl := L.CheckTable(1)
	storeHandle := luaTableGetString(tbl, "store_handle")
	docsRaw := luaTableGetArray(tbl, "documents")

	if storeHandle == "" {
		pushGoValue(L, map[string]any{"error": "add_docs: store_handle is required"})
		return 1
	}
	if len(docsRaw) == 0 {
		pushGoValue(L, map[string]any{"error": "add_docs: documents array is required"})
		return 1
	}

	m.mu.RLock()
	entry, ok := m.stores[storeHandle]
	m.mu.RUnlock()
	if !ok {
		pushGoValue(L, map[string]any{"error": fmt.Sprintf("add_docs: store %q not found", storeHandle)})
		return 1
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	ctx := L.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	added := 0
	for _, docRaw := range docsRaw {
		docMap, ok := docRaw.(map[string]any)
		if !ok {
			pushGoValue(L, map[string]any{"error": "add_docs: each document must be an object"})
			return 1
		}

		content, _ := docMap["content"].(string)
		if content == "" {
			continue
		}

		name, _ := docMap["name"].(string)
		meta, _ := docMap["metadata"].(map[string]any)

		doc := &document.Document{
			Name:     name,
			Content:  content,
			Metadata: meta,
		}

		// Generate embedding.
		embedding, err := m.embedder.GetEmbedding(ctx, content)
		if err != nil {
			pushGoValue(L, map[string]any{"error": fmt.Sprintf("add_docs: failed to get embedding: %v", err)})
			return 1
		}

		// Add to vector store directly.
		if err := entry.vs.Add(ctx, doc, embedding); err != nil {
			pushGoValue(L, map[string]any{"error": fmt.Sprintf("add_docs: failed to add document: %v", err)})
			return 1
		}
		added++
	}

	pushGoValue(L, map[string]any{"added": added})
	return 1
}

// luaSearch implements kb.search({store_handle, query_text, ...}) -> {results}
// 使用 per-store 读锁，多个子任务可同时搜索同一个 store。
func (m *KBModule) luaSearch(L *lua.LState) int {
	tbl := L.CheckTable(1)
	storeHandle := luaTableGetString(tbl, "store_handle")
	queryText := luaTableGetString(tbl, "query_text")
	modeStr := luaTableGetString(tbl, "mode")
	limit := luaTableGetInt(tbl, "limit", 10)
	minScore := luaTableGetFloat(tbl, "min_score", 0.0)
	metadataFilter := luaTableGetMap(tbl, "metadata_filter")

	if storeHandle == "" {
		pushGoValue(L, map[string]any{"error": "search: store_handle is required"})
		return 1
	}
	if queryText == "" {
		pushGoValue(L, map[string]any{"error": "search: query_text is required"})
		return 1
	}

	m.mu.RLock()
	entry, ok := m.stores[storeHandle]
	m.mu.RUnlock()
	if !ok {
		pushGoValue(L, map[string]any{"error": fmt.Sprintf("search: store %q not found", storeHandle)})
		return 1
	}

	entry.mu.RLock()
	defer entry.mu.RUnlock()

	ctx := L.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// Get embedding for query text.
	embedding, err := m.embedder.GetEmbedding(ctx, queryText)
	if err != nil {
		pushGoValue(L, map[string]any{"error": fmt.Sprintf("search: failed to get embedding: %v", err)})
		return 1
	}

	// Parse search mode string to SearchMode int.
	searchMode := parseSearchMode(modeStr)

	// Build search filter from metadata_filter.
	var searchFilter *vectorstore.SearchFilter
	if len(metadataFilter) > 0 {
		searchFilter = &vectorstore.SearchFilter{
			Metadata: metadataFilter,
		}
	}

	// Build search query.
	searchQuery := &vectorstore.SearchQuery{
		Query:      queryText,
		Vector:     embedding,
		Limit:      limit,
		MinScore:   minScore,
		Filter:     searchFilter,
		SearchMode: searchMode,
	}

	result, err := entry.vs.Search(ctx, searchQuery)
	if err != nil {
		pushGoValue(L, map[string]any{"error": fmt.Sprintf("search: search failed: %v", err)})
		return 1
	}

	// Convert results to Lua-friendly format.
	var results []any
	for _, sd := range result.Results {
		if sd.Document == nil {
			continue
		}
		docMap := map[string]any{
			"id":      sd.Document.ID,
			"name":    sd.Document.Name,
			"content": sd.Document.Content,
			"score":   sd.Score,
		}
		if sd.Document.Metadata != nil {
			docMap["metadata"] = sd.Document.Metadata
		}
		results = append(results, docMap)
	}

	pushGoValue(L, map[string]any{"results": results})
	return 1
}

// luaCloseStore implements kb.close_store({store_handle})
// 从 stores 和 namedStores 中同时移除。
func (m *KBModule) luaCloseStore(L *lua.LState) int {
	tbl := L.CheckTable(1)
	storeHandle := luaTableGetString(tbl, "store_handle")

	if storeHandle == "" {
		pushGoValue(L, map[string]any{"error": "close_store: store_handle is required"})
		return 1
	}

	m.mu.Lock()
	entry, ok := m.stores[storeHandle]
	if ok {
		_ = entry.kb.Close()
		delete(m.stores, storeHandle)
		delete(m.namedStores, entry.name)
	}
	m.mu.Unlock()

	pushGoValue(L, map[string]any{"ok": true})
	return 1
}

// luaStoreInfo implements kb.store_info({store_handle}) -> {doc_count, ...}
func (m *KBModule) luaStoreInfo(L *lua.LState) int {
	tbl := L.CheckTable(1)
	storeHandle := luaTableGetString(tbl, "store_handle")

	if storeHandle == "" {
		pushGoValue(L, map[string]any{"error": "store_info: store_handle is required"})
		return 1
	}

	m.mu.RLock()
	entry, ok := m.stores[storeHandle]
	m.mu.RUnlock()
	if !ok {
		pushGoValue(L, map[string]any{"error": fmt.Sprintf("store_info: store %q not found", storeHandle)})
		return 1
	}

	entry.mu.RLock()
	defer entry.mu.RUnlock()

	ctx := L.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	var docCount int
	count, err := entry.vs.Count(ctx)
	if err == nil {
		docCount = count
	}

	result := map[string]any{
		"doc_count":      docCount,
		"dimension":      m.config.Dimensions,
		"has_fts":        true,
		"max_chunk_size": m.config.MaxChunkSize,
		"chunk_overlap":  m.config.ChunkOverlap,
	}
	pushGoValue(L, result)
	return 1
}

// ---------- helpers ----------

// parseSearchMode converts a Lua mode string to vectorstore.SearchMode.
// Default is SearchModeHybrid (0).
func parseSearchMode(modeStr string) vectorstore.SearchMode {
	switch strings.ToLower(strings.TrimSpace(modeStr)) {
	case "vector":
		return vectorstore.SearchModeVector
	case "keyword":
		return vectorstore.SearchModeKeyword
	case "filter":
		return vectorstore.SearchModeFilter
	case "hybrid", "":
		return vectorstore.SearchModeHybrid
	default:
		return vectorstore.SearchModeHybrid
	}
}

// luaTableGetString extracts a string field from a Lua table.
func luaTableGetString(tbl *lua.LTable, key string) string {
	v := tbl.RawGetString(key)
	if v.Type() == lua.LTString {
		return string(v.(lua.LString))
	}
	return ""
}

// luaTableGetInt extracts an int field from a Lua table with a default value.
func luaTableGetInt(tbl *lua.LTable, key string, defaultVal int) int {
	v := tbl.RawGetString(key)
	switch val := v.(type) {
	case lua.LNumber:
		return int(val)
	case lua.LString:
		n, err := fmtSscanf(string(val), "%d")
		if err == nil {
			return n
		}
	}
	return defaultVal
}

// luaTableGetFloat extracts a float64 field from a Lua table with a default value.
func luaTableGetFloat(tbl *lua.LTable, key string, defaultVal float64) float64 {
	v := tbl.RawGetString(key)
	switch val := v.(type) {
	case lua.LNumber:
		return float64(val)
	case lua.LString:
		var f float64
		if _, err := fmt.Sscanf(string(val), "%f", &f); err == nil {
			return f
		}
	}
	return defaultVal
}

// luaTableGetArray extracts an array field from a Lua table.
func luaTableGetArray(tbl *lua.LTable, key string) []any {
	v := tbl.RawGetString(key)
	if tbl2, ok := v.(*lua.LTable); ok {
		return lTableToSlice(tbl2)
	}
	return nil
}

// luaTableGetMap extracts a map field from a Lua table.
func luaTableGetMap(tbl *lua.LTable, key string) map[string]any {
	v := tbl.RawGetString(key)
	if tbl2, ok := v.(*lua.LTable); ok {
		return luaTableToMap(tbl2)
	}
	return nil
}

// fmtSscanf is a wrapper for fmt.Sscanf used by luaTableGetInt.
func fmtSscanf(str string, format string) (int, error) {
	var n int
	_, err := fmt.Sscanf(str, format, &n)
	return n, err
}
