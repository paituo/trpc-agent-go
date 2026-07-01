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
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	lua "github.com/yuin/gopher-lua"

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	infinityreranker "trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/infinity"
	topkreranker "trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/topk"
	filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
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

	// Reranker 配置（可选），与 openclaw knowledge_tools 中的配置格式一致。
	// 当配置了 RerankerType 时，kb.search 将通过 BuiltinKnowledge.Search
	// 走完整 RAG 流水线（含 reranker 重排），与 Agent 级 knowledge_search 工具行为一致。
	RerankerType   string // "infinity" | "topk" | ""（空=topk）
	RerankerModel  string // 模型名，如 "bge-reranker-v2-m3"
	RerankerURL    string // infinity 服务地址
	RerankerAPIKey string // API Key
	RerankerTopN   int    // 返回 top N 条，默认 -1（全部）
}

// kbStore holds a BuiltinKnowledge, its associated VectorStore, and a per-store
// RWMutex for concurrent access control.
type kbStore struct {
	kb       *knowledge.BuiltinKnowledge
	vs       vectorstore.VectorStore
	mu       sync.RWMutex // per-store 读写锁
	name     string       // path/filename 唯一标识
	refCount int32        // 引用计数，用于安全关闭
}

// KBModule provides knowledge base operations to Lua scripts.
// It is a global singleton shared by all Lua VMs.
type KBModule struct {
	mu          sync.RWMutex
	stores      map[string]*kbStore // handle → store
	namedStores map[string]*kbStore // "path/filename" → store
	embedder    embedder.Embedder
	reranker    reranker.Reranker
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

	// Create reranker based on config.
	var rerank reranker.Reranker
	switch strings.ToLower(strings.TrimSpace(cfg.RerankerType)) {
	case "infinity":
		var ropts []infinityreranker.Option
		if cfg.RerankerModel != "" {
			ropts = append(ropts, infinityreranker.WithModel(cfg.RerankerModel))
		}
		if cfg.RerankerURL != "" {
			ropts = append(ropts, infinityreranker.WithEndpoint(cfg.RerankerURL))
		}
		if cfg.RerankerAPIKey != "" {
			ropts = append(ropts, infinityreranker.WithAPIKey(cfg.RerankerAPIKey))
		}
		if cfg.RerankerTopN > 0 {
			ropts = append(ropts, infinityreranker.WithTopN(cfg.RerankerTopN))
		}
		r, err := infinityreranker.New(ropts...)
		if err != nil {
			return nil, fmt.Errorf("kb module: failed to create infinity reranker: %w", err)
		}
		rerank = r
	default:
		// topk 为默认 reranker，与 knowledge.New 默认行为一致
		var ropts []topkreranker.Option
		if cfg.RerankerTopN > 0 {
			ropts = append(ropts, topkreranker.WithK(cfg.RerankerTopN))
		}
		rerank = topkreranker.New(ropts...)
	}

	return &KBModule{
		stores:      make(map[string]*kbStore),
		namedStores: make(map[string]*kbStore),
		embedder:    emb,
		reranker:    rerank,
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
	L.SetField(tbl, "add_source", L.NewFunction(m.luaAddSource))
	L.SetField(tbl, "remove_source", L.NewFunction(m.luaRemoveSource))
	L.SetField(tbl, "query_docs", L.NewFunction(m.luaQueryDocs))
	L.SetField(tbl, "close_store", L.NewFunction(m.luaCloseStore))
	L.SetField(tbl, "store_info", L.NewFunction(m.luaStoreInfo))
	L.SetGlobal("kb", tbl)
}

// luaCreateStore implements kb.create_store({path, filename, db_path}) -> {store_handle, ...}
//
// 路径即名称：path+filename 作为 store 的唯一标识。
// 如果同名 store 已存在，直接返回已有 handle（复用）并递增引用计数。
// db_path 为必填参数，指定 SQLite 数据库文件路径（如 "file:/path/to/kb.db"），
// 仅支持 sqlitevec 磁盘持久化方式，不支持内存模式。
// 检查+创建在同一个写锁临界区内完成，避免 TOCTOU 竞态条件。
//
// 路径去重：如果 path 已以 filename 结尾，则不再重复拼接 filename，
// 避免出现 "knowledge.db/knowledge.db" 这样的重复路径。
func (m *KBModule) luaCreateStore(L *lua.LState) int {
	tbl := L.CheckTable(1)
	path := luaTableGetString(tbl, "path")
	filename := luaTableGetString(tbl, "filename")
	dbPath := luaTableGetString(tbl, "db_path")

	if path == "" || filename == "" {
		L.Push(lua.LNil)
		L.Push(lua.LString("create_store: path and filename are required"))
		return 2
	}
	if dbPath == "" {
		L.Push(lua.LNil)
		L.Push(lua.LString("create_store: db_path is required, sqlitevec disk mode only"))
		return 2
	}

	// 路径去重：如果 path 已以 filename 结尾，直接使用 path 作为 storeName
	storeName := path + "/" + filename
	if strings.HasSuffix(path, filename) {
		storeName = path
	}

	// 检查+创建在同一个写锁临界区内
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry, ok := m.namedStores[storeName]; ok {
		atomic.AddInt32(&entry.refCount, 1)
		result := map[string]any{
			"store_handle": storeName,
			"store_type":   "sqlite",
			"dimension":    m.config.Dimensions,
			"has_fts":      true,
			"reused":       true,
		}
		pushGoValue(L, result)
		return 1
	}

	// 创建 sqlitevec vector store（仅支持磁盘持久化方式）
	vs, storeType, hasFTS, err := newSQLiteVecStore(dbPath, m.config.Dimensions)
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(fmt.Sprintf("create_store: failed to create vector store: %v", err)))
		return 2
	}

	// Create BuiltinKnowledge with reranker support.
	// 传入 reranker 使 kb.search 走完整 RAG 流水线（含重排），
	// 与 Agent 级 knowledge_search 工具行为一致。
	bk := knowledge.New(
		knowledge.WithEmbedder(m.embedder),
		knowledge.WithVectorStore(vs),
		knowledge.WithReranker(m.reranker),
	)

	entry := &kbStore{kb: bk, vs: vs, name: storeName, refCount: 1}
	m.stores[storeName] = entry
	m.namedStores[storeName] = entry
	result := map[string]any{
		"store_handle": storeName,
		"store_type":   storeType,
		"dimension":    m.config.Dimensions,
		"has_fts":      hasFTS,
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
		L.Push(lua.LNil)
		L.Push(lua.LString("embed: texts array is required"))
		return 2
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
			L.Push(lua.LNil)
			L.Push(lua.LString("embed: each text must be a string"))
			return 2
		}
		emb, err := m.embedder.GetEmbedding(ctx, textStr)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(fmt.Sprintf("embed: failed to get embedding: %v", err)))
			return 2
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
		L.Push(lua.LNil)
		L.Push(lua.LString("add_docs: store_handle is required"))
		return 2
	}
	if len(docsRaw) == 0 {
		L.Push(lua.LNil)
		L.Push(lua.LString("add_docs: documents array is required"))
		return 2
	}

	m.mu.RLock()
	entry, ok := m.stores[storeHandle]
	m.mu.RUnlock()
	if !ok {
		L.Push(lua.LNil)
		L.Push(lua.LString(fmt.Sprintf("add_docs: store %q not found", storeHandle)))
		return 2
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
			L.Push(lua.LNil)
			L.Push(lua.LString("add_docs: each document must be an object"))
			return 2
		}

		content, _ := docMap["content"].(string)
		if content == "" {
			continue
		}

		id, _ := docMap["id"].(string)
		name, _ := docMap["name"].(string)
		meta, _ := docMap["metadata"].(map[string]any)

		doc := &document.Document{
			ID:       id,
			Name:     name,
			Content:  content,
			Metadata: meta,
		}

		// Generate embedding.
		embedding, err := m.embedder.GetEmbedding(ctx, content)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(fmt.Sprintf("add_docs: failed to get embedding: %v", err)))
			return 2
		}

		// Add to vector store directly.
		// 如果 Add 失败（如 sqlitevec 的 UNIQUE 约束），尝试 Update。
		if err := entry.vs.Add(ctx, doc, embedding); err != nil {
			if updateErr := entry.vs.Update(ctx, doc, embedding); updateErr != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(fmt.Sprintf("add_docs: failed to add document (add: %v, update: %v)", err, updateErr)))
				return 2
			}
		}
		added++
	}

	pushGoValue(L, map[string]any{"added": added})
	return 1
}

// luaAddSource implements kb.add_source({store_handle, source_name, file_paths, chunk_size?, chunk_overlap?})
// -> {source_name, file_count, doc_count}
//
// 使用知识库自带的 file.Source + Markdown 感知分块，直接将源文件导入知识库。
// 无需 Lua 侧手动分块和构建 metadata。
func (m *KBModule) luaAddSource(L *lua.LState) int {
	tbl := L.CheckTable(1)
	storeHandle := luaTableGetString(tbl, "store_handle")
	sourceName := luaTableGetString(tbl, "source_name")
	filePathsRaw := luaTableGetArray(tbl, "file_paths")
	chunkSize := luaTableGetInt(tbl, "chunk_size", 0)
	chunkOverlap := luaTableGetInt(tbl, "chunk_overlap", 0)

	if storeHandle == "" {
		L.Push(lua.LNil)
		L.Push(lua.LString("add_source: store_handle is required"))
		return 2
	}
	if sourceName == "" {
		L.Push(lua.LNil)
		L.Push(lua.LString("add_source: source_name is required"))
		return 2
	}
	if len(filePathsRaw) == 0 {
		L.Push(lua.LNil)
		L.Push(lua.LString("add_source: file_paths array is required"))
		return 2
	}

	m.mu.RLock()
	entry, ok := m.stores[storeHandle]
	m.mu.RUnlock()
	if !ok {
		L.Push(lua.LNil)
		L.Push(lua.LString(fmt.Sprintf("add_source: store %q not found", storeHandle)))
		return 2
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	ctx := L.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// 转换 Lua 字符串数组为 Go string 切片
	filePaths := make([]string, 0, len(filePathsRaw))
	for _, fp := range filePathsRaw {
		if s, ok := fp.(string); ok && s != "" {
			filePaths = append(filePaths, s)
		}
	}
	if len(filePaths) == 0 {
		L.Push(lua.LNil)
		L.Push(lua.LString("add_source: no valid file paths found"))
		return 2
	}

	// 构建 file.Source 选项
	var opts []filesource.Option
	opts = append(opts, filesource.WithName(sourceName))
	if chunkSize > 0 {
		opts = append(opts, filesource.WithChunkSize(chunkSize))
	}
	if chunkOverlap > 0 {
		opts = append(opts, filesource.WithChunkOverlap(chunkOverlap))
	}

	// 创建 file.Source 并逐个读取文档后串行入库
	// 避免 AddSource 内部并发写入 SQLite 导致 "database is locked"
	fileSrc := filesource.New(filePaths, opts...)
	docs, readErr := fileSrc.ReadDocuments(ctx)
	if readErr != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(fmt.Sprintf("add_source: failed to read documents: %v", readErr)))
		return 2
	}

	// 串行添加每个文档（直接使用 vs.Add 避免并发写入问题）
	for _, doc := range docs {
		embedding, embErr := m.embedder.GetEmbedding(ctx, doc.Content)
		if embErr != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(fmt.Sprintf("add_source: failed to embed document %q: %v", doc.Name, embErr)))
			return 2
		}
		if err := entry.vs.Add(ctx, doc, embedding); err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(fmt.Sprintf("add_source: failed to add document %q: %v", doc.Name, err)))
			return 2
		}
	}

	// 获取导入后的文档统计
	var docCount int
	count, err := entry.vs.Count(ctx)
	if err == nil {
		docCount = count
	}

	result := map[string]any{
		"source_name": sourceName,
		"file_count":  len(filePaths),
		"doc_count":   docCount,
	}
	pushGoValue(L, result)
	return 1
}

// luaRemoveSource implements kb.remove_source({store_handle, source_name}) -> {ok}
func (m *KBModule) luaRemoveSource(L *lua.LState) int {
	tbl := L.CheckTable(1)
	storeHandle := luaTableGetString(tbl, "store_handle")
	sourceName := luaTableGetString(tbl, "source_name")

	if storeHandle == "" {
		L.Push(lua.LNil)
		L.Push(lua.LString("remove_source: store_handle is required"))
		return 2
	}
	if sourceName == "" {
		L.Push(lua.LNil)
		L.Push(lua.LString("remove_source: source_name is required"))
		return 2
	}

	m.mu.RLock()
	entry, ok := m.stores[storeHandle]
	m.mu.RUnlock()
	if !ok {
		L.Push(lua.LNil)
		L.Push(lua.LString(fmt.Sprintf("remove_source: store %q not found", storeHandle)))
		return 2
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	ctx := L.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	if err := entry.kb.RemoveSource(ctx, sourceName); err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(fmt.Sprintf("remove_source: failed to remove source %q: %v", sourceName, err)))
		return 2
	}

	pushGoValue(L, map[string]any{"ok": true})
	return 1
}

// luaQueryDocs implements kb.query_docs({store_handle, metadata_filter?, limit?})
// -> {docs: [{id, name, content, metadata}]}
//
// 从知识库中查询文档及其元数据。支持按 metadata 过滤。
// 用于获取导入后的分片列表，按章节目录组织输出。
func (m *KBModule) luaQueryDocs(L *lua.LState) int {
	tbl := L.CheckTable(1)
	storeHandle := luaTableGetString(tbl, "store_handle")
	limit := luaTableGetInt(tbl, "limit", 1000)

	if storeHandle == "" {
		L.Push(lua.LNil)
		L.Push(lua.LString("query_docs: store_handle is required"))
		return 2
	}

	m.mu.RLock()
	entry, ok := m.stores[storeHandle]
	m.mu.RUnlock()
	if !ok {
		L.Push(lua.LNil)
		L.Push(lua.LString(fmt.Sprintf("query_docs: store %q not found", storeHandle)))
		return 2
	}

	entry.mu.RLock()
	vs := entry.vs
	entry.mu.RUnlock()

	ctx := L.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// 构建 metadata 过滤条件
	metadataFilter := luaTableGetMap(tbl, "metadata_filter")
	var getOpts []vectorstore.GetMetadataOption
	if len(metadataFilter) > 0 {
		getOpts = append(getOpts, vectorstore.WithGetMetadataFilter(metadataFilter))
	}
	if limit > 0 {
		getOpts = append(getOpts, vectorstore.WithGetMetadataLimit(limit))
	}

	// 获取匹配的文档元数据
	metaMap, err := vs.GetMetadata(ctx, getOpts...)
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(fmt.Sprintf("query_docs: GetMetadata failed: %v", err)))
		return 2
	}

	// 逐 ID 获取文档完整内容
	docs := make([]map[string]any, 0, len(metaMap))
	for id := range metaMap {
		doc, _, getErr := vs.Get(ctx, id)
		if getErr != nil {
			continue
		}
		if doc == nil {
			continue
		}
		docs = append(docs, map[string]any{
			"id":       doc.ID,
			"name":     doc.Name,
			"content":  doc.Content,
			"metadata": doc.Metadata,
		})
	}

	// 按 chunk_index 排序
	sort.Slice(docs, func(i, j int) bool {
		ci := getChunkIndexFromMeta(docs[i]["metadata"])
		cj := getChunkIndexFromMeta(docs[j]["metadata"])
		if ci != cj {
			return ci < cj
		}
		// 同 chunk_index 按 name 排序
		ni, _ := docs[i]["name"].(string)
		nj, _ := docs[j]["name"].(string)
		return ni < nj
	})

	pushGoValue(L, map[string]any{
		"docs":  docs,
		"count": len(docs),
	})
	return 1
}

// getChunkIndexFromMeta 从 metadata 中提取 chunk_index
func getChunkIndexFromMeta(metaVal any) int {
	meta, ok := metaVal.(map[string]any)
	if !ok {
		return 0
	}
	if v, ok := meta["trpc_agent_go_chunk_index"]; ok {
		switch val := v.(type) {
		case float64:
			return int(val)
		case int:
			return val
		case int64:
			return int(val)
		}
	}
	return 0
}

// luaSearch implements kb.search({store_handle, query_text, ...}) -> {results}
// 通过 BuiltinKnowledge.Search 走完整 RAG 流水线（含 reranker 重排），
// 与 Agent 级 knowledge_search 工具行为一致。
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
		L.Push(lua.LNil)
		L.Push(lua.LString("search: store_handle is required"))
		return 2
	}
	if queryText == "" {
		L.Push(lua.LNil)
		L.Push(lua.LString("search: query_text is required"))
		return 2
	}

	m.mu.RLock()
	entry, ok := m.stores[storeHandle]
	m.mu.RUnlock()
	if !ok {
		L.Push(lua.LNil)
		L.Push(lua.LString(fmt.Sprintf("search: store %q not found", storeHandle)))
		return 2
	}

	entry.mu.RLock()
	defer entry.mu.RUnlock()

	ctx := L.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// Parse search mode string to SearchMode int.
	searchMode := parseSearchMode(modeStr)

	// Build search request for BuiltinKnowledge.Search (完整 RAG 流水线，含 reranker).
	searchReq := &knowledge.SearchRequest{
		Query:     queryText,
		MaxResults: limit,
		MinScore:  minScore,
		SearchMode: searchMode,
	}
	if len(metadataFilter) > 0 {
		searchReq.SearchFilter = &knowledge.SearchFilter{
			Metadata: metadataFilter,
		}
	}

	result, err := entry.kb.Search(ctx, searchReq)
	if err != nil {
		// "no relevant documents found" 时返回空数组，不报错
		if strings.Contains(err.Error(), "no relevant documents found") ||
			strings.Contains(err.Error(), "no relevant information found") {
			pushGoValue(L, map[string]any{"results": []any{}})
			return 1
		}
		L.Push(lua.LNil)
		L.Push(lua.LString(fmt.Sprintf("search: %v", err)))
		return 2
	}

	// Convert results to Lua-friendly format.
	// 保持与现有 LUA 脚本兼容的格式：{results: [{id, name, content, score, metadata}]}
	var results []any
	for _, doc := range result.Documents {
		if doc.Document == nil {
			continue
		}
		docMap := map[string]any{
			"id":      doc.Document.ID,
			"name":    doc.Document.Name,
			"content": doc.Document.Content,
			"score":   doc.Score,
		}
		if doc.Document.Metadata != nil {
			docMap["metadata"] = doc.Document.Metadata
		}
		results = append(results, docMap)
	}

	pushGoValue(L, map[string]any{"results": results})
	return 1
}

// luaCloseStore implements kb.close_store({store_handle})
//
// 使用引用计数机制：每次调用递减引用计数，只有最后一个引用者
// （refCount 减到 0）才真正关闭底层数据库连接并从全局 map 中移除。
// 这解决了多个 Lua VM 并发访问同一 store 时，一个调用者关闭
// 导致其他调用者拿到 "database is closed" 错误的问题。
func (m *KBModule) luaCloseStore(L *lua.LState) int {
	tbl := L.CheckTable(1)
	storeHandle := luaTableGetString(tbl, "store_handle")

	if storeHandle == "" {
		L.Push(lua.LNil)
		L.Push(lua.LString("close_store: store_handle is required"))
		return 2
	}

	m.mu.Lock()
	entry, ok := m.stores[storeHandle]
	if ok {
		newRefCount := atomic.AddInt32(&entry.refCount, -1)
		if newRefCount <= 0 {
			_ = entry.kb.Close()
			delete(m.stores, storeHandle)
			delete(m.namedStores, entry.name)
		}
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
		L.Push(lua.LNil)
		L.Push(lua.LString("store_info: store_handle is required"))
		return 2
	}

	m.mu.RLock()
	entry, ok := m.stores[storeHandle]
	m.mu.RUnlock()
	if !ok {
		L.Push(lua.LNil)
		L.Push(lua.LString(fmt.Sprintf("store_info: store %q not found", storeHandle)))
		return 2
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
