//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package fragment provides a graph source that parses Markdown documents into
// a four-layer graph structure: skeleton → document → chapter → fragment.
//
// # 切片逻辑说明
//
// 本包实现了基于 Lua 脚本 batch_extract_outline_v2.lua 的文档切片逻辑，
// 将 Markdown 文档按 1500 字符限制切割为多个 fragment，同时保留表格和图片的完整性。
//
// ## 核心流程
//
// 1. **标题识别**：扫描文档中的 Markdown 标题行（# 开头），记录行号、层级和标题文本
// 2. **行块划分**：将文档行划分为 lineBlock 列表，识别以下类型：
//   - Markdown 表格（| 开头的行）
//   - HTML 表格（<table> 标签）
//   - 普通文本行（含图片引用 ![alt](url)）
//
// 3. **按字符限制切割**：使用 splitBlocksByCharLimit 函数，按 1500 字符限制切割：
//   - 保证不拆行：切割点只在行边界
//   - 保证不拆表：表格整体归属一个 fragment
//   - 保证不拆图：图片整体归属一个 fragment
//   - 如果单个块超过限制，单独作为一个片段
//
// 4. **关联表格/图片**：每个 fragment 记录其包含的表格（tableInfo）和图片（imageInfo）
//
// ## 表格识别
//
// - Markdown 表格：识别 | 开头的行，解析表头和数据行
// - HTML 表格：识别 <table>...</table> 标签对
// - 表格信息包括：起始行、结束行、行数、列数、预览文本
//
// ## 图片识别
//
// - 匹配 ![alt](url) 格式的图片引用
// - 记录图片的 alt 文本和 URL
//
// ## 与 Lua 脚本的对应关系
//
// | Lua 脚本 | Go 实现 |
// |---------|--------|
// | pass1_candidate_scan | buildLineBlocks |
// | build_fragments_from_outline | splitBlocksByCharLimit |
// | slim_table | tableInfo |
// | extract_images | extractImagesFromLine |
package fragment

import (
	"context"
	//nolint:gosec // Used only for stable graph IDs, not cryptographic security.
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const defaultSourceName = "Fragment Source"

// trpcAstMetaPrefix is the prefix for all metadata keys written by fragment source,
// consistent with codeast.TrpcAstMetaPrefix used by repo source.
const trpcAstMetaPrefix = "trpc_ast_"
const max_output_level = 2 // 最大输出层级，超过该层级的不拆分
// SkeletonNode describes a skeleton node from an engineering skeleton definition.
type SkeletonNode struct {
	ID            string
	Name          string
	Content       string
	SourceCatalog []string
	ParentID      string
	Metadata      map[string]any
}

type fragment struct {
	id            string
	name          string
	content       string
	headingLevel  int
	headingPath   string
	startLine     int
	endLine       int
	children      []fragment
	documentID    string
	docCategory   string
	fragmentIndex int
	// tables 记录该 fragment 关联的表格信息
	tables []tableInfo
	// images 记录该 fragment 关联的图片信息
	images []imageInfo
}

// tableInfo 描述一个表格的位置和基本信息
type tableInfo struct {
	title     string
	startLine int
	endLine   int
	rowCount  int
	colCount  int
	preview   string
}

// imageInfo 描述一个图片的位置和信息
type imageInfo struct {
	alt     string
	url     string
	lineNum int
}

type relation struct {
	fromID string
	toID   string
	typ    string
}

// Source implements source.GraphSource for fragment-based graph data.
type Source struct {
	name      string
	skeletons []SkeletonNode
	docPaths  []string
	metadata  map[string]any
	logging   bool

	// skeleton index (lazily built) for matching fragments to skeleton nodes.
	skeletonRoots []*skeletonTreeNode
	skeletonBuilt bool
	// skeletonView caches the effective skeleton list (user skeletons plus the
	// auto "unknown data" node) used for both indexing and graph emission.
	skeletonView []SkeletonNode

	// embedder / reranker enable semantic skeleton mounting. When both
	// are set, a semanticMounter is built in New and assignSkeleton
	// delegates to it; otherwise the deterministic heading-path matcher is used.
	embedder embedder.Embedder
	reranker reranker.Reranker
	mounter  *semanticMounter

	// keywordMatchThreshold is the per-keyword cosine floor used by the
	// semantic mounter's keyword matching path. When non-zero it is
	// forwarded to the mounter; the mounter default (0.30) is used
	// otherwise.
	keywordMatchThreshold float64
	rerankMatchThreshold  float64
	rerankTopN            int

	// llm is the optional LLM used for batch document classification.
	// When nil, classification is skipped and no doc_category metadata
	// is written.
	llm model.Model
	// docClassifyPrompt overrides the default classification prompt.
	// An empty value means the default prompt is used.
	docClassifyPrompt string
	// docCategories maps docPath → category after LLM classification.
	// Populated lazily by classifyDocPaths.
	docCategories map[string]string
	sourceDir     string // optional source directory for resolving relative docPaths
}

// skeletonTreeNode is a node in the in-memory skeleton tree built from
// Source.skeletons (keyed by ParentID).
type skeletonTreeNode struct {
	node     SkeletonNode
	children []*skeletonTreeNode
}

// New creates a FragmentGraphSource with the given skeleton nodes and document paths.
func New(skeletons []SkeletonNode, docPaths []string, opts ...Option) *Source {
	s := &Source{
		name:      defaultSourceName,
		skeletons: skeletons,
		docPaths:  docPaths,
		metadata:  make(map[string]any),
		logging:   true,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.embedder != nil && s.reranker != nil {
		m := newSemanticMounter(s.embedder, s.reranker, defaultUnknownSkeletonID, skeletons, s.keywordMatchThreshold, s.rerankMatchThreshold)
		s.mounter = m
	}
	return s
}

func (s *Source) logf(format string, args ...any) {
	if s.logging {
		log.Printf("[fragment] "+format, args...)
	}
}

// ReadGraph reads all documents and skeleton definitions, returning a graph.Data
// containing skeleton, document, chapter, and fragment nodes with their edges.
func (s *Source) ReadGraph(ctx context.Context, opts ...source.ReadGraphOption) (*graph.Data, error) {
	s.logf("ReadGraph start: skeletons=%d docPaths=%d", len(s.skeletons), len(s.docPaths))
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	nodeMap := make(map[string]*graph.Node)
	edgeMap := make(map[string]*graph.Edge)

	// LLM-based batch classification of docPaths.
	// If LLM is configured and classification succeeds, the result is stored
	// and attached as metadata to each document node.
	if s.llm != nil {
		cats, err := s.classifyDocPaths(ctx)
		if err != nil {
			s.logf("classifyDocPaths failed (non-fatal): %v", err)
		} else {
			s.docCategories = cats
			s.logf("classifyDocPaths done: %d docs classified", len(cats))
		}
	}

	var allLeaves []fragment

	// 先解析骨架文件：构建 skeleton 树（含自动的“未知数据”节点），
	// 以便后续文档 fragment 在解析时就能挂载到正确的骨架节点。
	s.ensureSkeletonIndex()
	for _, sk := range s.effectiveSkeletons() {
		if sk.ID == "" {
			s.logf("Skipping skeleton node with empty ID, name=%q", sk.Name)
			continue
		}
		metadata := cloneMap(sk.Metadata)
		if metadata == nil {
			metadata = make(map[string]any)
		}
		metadata[trpcAstMetaPrefix+"type"] = "skeleton"
		metadata[trpcAstMetaPrefix+"scope"] = "skeleton"

		nodeMap[sk.ID] = &graph.Node{
			ID:       sk.ID,
			Name:     sk.Name,
			Content:  sk.Content,
			Metadata: metadata,
		}
		if sk.ParentID != "" {
			edgeKey := sk.ParentID + "::HAS_CHILD::" + sk.ID
			edgeMap[edgeKey] = &graph.Edge{
				ID:     edgeKey,
				FromID: sk.ParentID,
				ToID:   sk.ID,
				Type:   "HAS_CHILD",
				Metadata: map[string]any{
					"builder": "fragment_graph_source",
				},
			}
		}
	}

	for _, docPath := range s.docPaths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		frags, err := s.parseMDDocument(docPath)
		if err != nil {
			return nil, err
		}
		docID := "doc:" + docPath
		leaves, chapters, relations := flattenFragments(frags, docID)
		allLeaves = append(allLeaves, leaves...)

		docCat := ""
		if s.docCategories != nil {
			if cat, ok := s.docCategories[docPath]; ok {
				docCat = cat
			}
		}
		// 计算当前文档最大片段内容大小
		maxFragSize := 0
		var maxFragName string
		for _, leaf := range leaves {
			size := len(leaf.content)
			if size > maxFragSize {
				maxFragSize = size
				maxFragName = leaf.name
			}
		}
		s.logf("Parsed doc %s: headings→tree ok, leaves=%d chapters=%d relations=%d maxFragmentSize=%d maxFragmentName=%q",
			filepath.Base(docPath), len(leaves), len(chapters), len(relations), maxFragSize, maxFragName)

		// Build document node metadata; include LLM classification if available.
		docMeta := map[string]any{
			trpcAstMetaPrefix + "type":         "document",
			trpcAstMetaPrefix + "scope":        "document",
			trpcAstMetaPrefix + "file_path":    docPath,
			trpcAstMetaPrefix + "doc_category": docCat,
		}

		nodeMap[docID] = &graph.Node{
			ID:       docID,
			Name:     docPath,
			Content:  "",
			Metadata: docMeta,
		}

		for _, ch := range chapters {
			chNodeID := generateNodeID("fragment:" + ch.id)
			chMetadata := map[string]any{
				trpcAstMetaPrefix + "type":          "chapter",
				trpcAstMetaPrefix + "scope":         "chapter",
				trpcAstMetaPrefix + "heading_level": ch.headingLevel,
				trpcAstMetaPrefix + "heading_path":  ch.headingPath,
				trpcAstMetaPrefix + "document_id":   docID,
				trpcAstMetaPrefix + "doc_category":  docCat,
			}
			nodeMap[chNodeID] = &graph.Node{
				ID:       chNodeID,
				Name:     ch.name,
				Content:  ch.content,
				Metadata: chMetadata,
			}
		}

		for _, rel := range relations {
			if rel.typ == "CONTAINS_DOC" {
				edgeKey := docID + "::CONTAINS::" + generateNodeID("fragment:"+rel.toID)
				edgeMap[edgeKey] = &graph.Edge{
					ID:     edgeKey,
					FromID: docID,
					ToID:   generateNodeID("fragment:" + rel.toID),
					Type:   "CONTAINS",
					Metadata: map[string]any{
						"builder": "fragment_graph_source",
					},
				}
			} else {
				fromID := generateNodeID("fragment:" + rel.fromID)
				toID := generateNodeID("fragment:" + rel.toID)
				edgeKey := fromID + "::" + rel.typ + "::" + toID
				edgeMap[edgeKey] = &graph.Edge{
					ID:     edgeKey,
					FromID: fromID,
					ToID:   toID,
					Type:   rel.typ,
					Metadata: map[string]any{
						"builder": "fragment_graph_source",
					},
				}
			}
		}
	}

	for index, leaf := range allLeaves {
		if leaf.id == "" {
			continue
		}
		nodeID := generateNodeID("fragment:" + leaf.id)
		metadata := map[string]any{
			trpcAstMetaPrefix + "type":           "fragment",
			trpcAstMetaPrefix + "scope":          "fragment",
			trpcAstMetaPrefix + "fragment_id":    leaf.id,
			trpcAstMetaPrefix + "fragment_type":  "text",
			trpcAstMetaPrefix + "document_id":    leaf.documentID,
			trpcAstMetaPrefix + "heading_path":   leaf.headingPath,
			trpcAstMetaPrefix + "heading_level":  leaf.headingLevel,
			trpcAstMetaPrefix + "start_line":     leaf.startLine,
			trpcAstMetaPrefix + "end_line":       leaf.endLine,
			trpcAstMetaPrefix + "fragment_index": leaf.fragmentIndex,
			trpcAstMetaPrefix + "chunk_index":    index,
			trpcAstMetaPrefix + "chunk_size":     len(leaf.content),
			trpcAstMetaPrefix + "doc_category":   leaf.docCategory,
		}

		// 语义挂接：配置了 embedder+reranker 时用向量召回+重排，
		// 否则退回确定性标题路径匹配。多节点会返回多条挂接。
		targets := s.mountFragment(ctx, leaf)
		if len(targets) > 0 {
			metadata[trpcAstMetaPrefix+"skeleton_node"] = targets[0].SkeletonID
			metadata[trpcAstMetaPrefix+"mount_type"] = targets[0].MountType
			metadata[trpcAstMetaPrefix+"match_source"] = targets[0].MatchSource
		}
		nodeMap[nodeID] = &graph.Node{
			ID:       nodeID,
			Name:     leaf.name,
			Content:  leaf.content,
			Metadata: metadata,
		}

		for _, t := range targets {
			edgeKey := nodeID + "::MOUNTS_TO::" + t.SkeletonID
			edgeMap[edgeKey] = &graph.Edge{
				ID:     edgeKey,
				FromID: nodeID,
				ToID:   t.SkeletonID,
				Type:   "MOUNTS_TO",
				Metadata: map[string]any{
					"builder":      "fragment_graph_source",
					"mount_type":   t.MountType,
					"match_source": t.MatchSource,
				},
			}
		}
	}

	nodes := make([]*graph.Node, 0, len(nodeMap))
	for _, node := range nodeMap {
		nodes = append(nodes, node)
	}
	edges := make([]*graph.Edge, 0, len(edgeMap))
	for _, edge := range edgeMap {
		edges = append(edges, edge)
	}

	typeCounts := make(map[string]int)
	for _, n := range nodes {
		if t, ok := n.Metadata[trpcAstMetaPrefix+"type"].(string); ok {
			typeCounts[t]++
		}
	}
	edgeTypeCounts := make(map[string]int)
	for _, e := range edges {
		edgeTypeCounts[e.Type]++
	}
	s.logf("ReadGraph done: nodes=%d edges=%d nodeTypes=%v edgeTypes=%v",
		len(nodes), len(edges), typeCounts, edgeTypeCounts)

	return &graph.Data{Nodes: nodes, Edges: edges}, nil
}

func (s *Source) parseMDDocument(docPath string) ([]fragment, error) {
	data, err := os.ReadFile(docPath)
	if err != nil {
		return nil, fmt.Errorf("fragment: read file %s: %w", docPath, err)
	}
	docCat := ""
	if s.docCategories != nil {
		if cat, ok := s.docCategories[docPath]; ok {
			docCat = cat
		}
	}
	content := string(data)
	lines := strings.Split(content, "\n")
	s.logf("parseMDDocument %s: %d lines", filepath.Base(docPath), len(lines))

	type heading struct {
		lineNum int
		level   int
		title   string
	}
	var headings []heading
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		level := 0
		for _, ch := range trimmed {
			if ch == '#' {
				level++
			} else {
				break
			}
		}
		if level <= 0 || level > max_output_level {
			continue
		}
		title := strings.TrimSpace(trimmed[level:])
		if title == "" {
			continue
		}
		headings = append(headings, heading{
			lineNum: i + 1,
			level:   level,
			title:   title,
		})
	}

	// 如果第一个非空行不是标题，添加一个虚拟标题，避免前面的内容丢失
	if len(headings) > 0 {
		firstNonEmptyLine := -1
		for i, line := range lines {
			if strings.TrimSpace(line) != "" {
				firstNonEmptyLine = i + 1 // 1-based
				break
			}
		}
		if firstNonEmptyLine > 0 && headings[0].lineNum > firstNonEmptyLine {
			docName := defaultChapterName(docPath)
			headings = append([]heading{{
				lineNum: firstNonEmptyLine,
				level:   1,
				title:   docName,
			}}, headings...)
		}
	}

	s.logf("parseMDDocument %s: found %d headings, building tree", filepath.Base(docPath), len(headings))

	var buildTree func(start, end int, parentLevel int, parentPath string) []fragment
	buildTree = func(start, end int, parentLevel int, parentPath string) []fragment {
		var result []fragment
		i := start
		for i < end {
			h := headings[i]
			if h.level <= parentLevel && i > start {
				break
			}
			j := i + 1
			for j < end && headings[j].level > h.level {
				j++
			}

			headingPath := h.title
			if parentPath != "" {
				headingPath = parentPath + " > " + h.title
			}

			children := buildTree(i+1, j, h.level, headingPath)

			contentStart := h.lineNum
			contentEnd := len(lines)
			if i+1 < len(headings) {
				contentEnd = headings[i+1].lineNum - 1
			}

			// 按1500字符限制切割正文内容，保证不拆行、不拆表、不拆图
			var bodyLines []string
			for k := contentStart; k <= contentEnd && k-1 < len(lines); k++ {
				if k-1 >= 0 && k-1 < len(lines) {
					bodyLines = append(bodyLines, lines[k-1])
				}
			}

			// 使用 splitBodyByCharLimit 按1500字符切割
			bodySegments := splitBodyByCharLimit(bodyLines, contentStart, maxFragmentChars)

			// 5.4 修复（口径 B）：顶层、且自身没有子标题的标题，
			// 建模为 chapter，并将其正文作为一个 fragment 挂到该 chapter 下，
			// 避免它沦为 document 级 fragment（与真正的 chapter 类型不一致）。
			if parentLevel == 0 || len(children) > 0 {
				chapterID := fmt.Sprintf("h_%d_%d", h.lineNum, h.level)
				var bodyFragments []fragment
				for segIdx, seg := range bodySegments {
					bodyFragID := fmt.Sprintf("%s_body_%d", chapterID, segIdx)
					bodyFragments = append(bodyFragments, fragment{
						id:           bodyFragID,
						name:         h.title,
						content:      seg.content,
						headingLevel: h.level,
						headingPath:  headingPath,
						startLine:    seg.startLine,
						endLine:      seg.endLine,
						tables:       seg.tables,
						images:       seg.images,
						docCategory:  docCat,
					})
				}
				children = append(bodyFragments, children...)
				result = append(result, fragment{
					id:           chapterID,
					name:         h.title,
					content:      strings.TrimSpace(lines[h.lineNum-1]), // 仅标题行，作为章节头
					headingLevel: h.level,
					headingPath:  headingPath,
					startLine:    contentStart,
					endLine:      contentEnd,
					children:     children,
					docCategory:  docCat,
				})
			} else {
				for segIdx, seg := range bodySegments {
					result = append(result, fragment{
						id:           fmt.Sprintf("h_%d_%d_seg_%d", h.lineNum, h.level, segIdx),
						name:         h.title,
						content:      seg.content,
						headingLevel: h.level,
						headingPath:  headingPath,
						startLine:    contentStart,
						endLine:      contentEnd,
						children:     children,
						docCategory:  docCat,
					})
				}
			}

			i = j
		}
		return result
	}

	// 构建文档根节点，将所有顶层 fragment 挂载到该根节点下
	result := buildTree(0, len(headings), 0, "")
	return result, nil
}

func flattenFragments(frags []fragment, docID string) ([]fragment, []fragment, []relation) {
	var leaves []fragment
	var chapters []fragment
	var relations []relation

	// walk 在每一层独立维护 prevLeafID / prevChapterID 与 index：
	//   - index 为“当前父节点（document 或 chapter）下”的局部自增序号，
	//     每个父节点独立从 1 开始计数，使 fragmentIndex 表示“在父 chapter 下的顺序”；
	//   - 递归进入子 chapter 时，不把子树末端 leaf 回写到本层 prevLeafID，
	//     从而避免不同 chapter 的 fragment 之间产生跨章 NEXT 边；
	//   - 仅在同一父 chapter 下的直接 leaf 兄弟之间生成 NEXT（保持原文顺序）；
	//   - 兄弟 chapter 之间生成 NEXT。
	var walk func(parents []fragment, frags []fragment)
	walk = func(parents []fragment, frags []fragment) {
		var prevLeafID string
		var prevChapterID string
		index := 0 // 每个父节点独立计数：fragmentIndex 表示该 leaf 在父节点下的局部序号
		for i := range frags {
			frags[i].documentID = docID
			hasChildren := len(frags[i].children) > 0

			// 与父节点（document 或 chapter）的 CONTAINS 关系
			if len(parents) == 0 {
				relations = append(relations, relation{
					fromID: "",
					toID:   frags[i].id,
					typ:    "CONTAINS_DOC",
				})
			} else {
				relations = append(relations, relation{
					fromID: parents[len(parents)-1].id,
					toID:   frags[i].id,
					typ:    "CONTAINS",
				})
			}

			if hasChildren {
				chapters = append(chapters, frags[i])
				// 兄弟 chapter 之间建立 NEXT 关系
				if prevChapterID != "" {
					relations = append(relations, relation{
						fromID: prevChapterID,
						toID:   frags[i].id,
						typ:    "NEXT",
					})
				}
				prevChapterID = frags[i].id
				// 递归处理子节点；不把子树末端 leaf 回写到本层 prevLeafID
				parentsCopy := make([]fragment, len(parents), len(parents)+1)
				copy(parentsCopy, parents)
				walk(append(parentsCopy, frags[i]), frags[i].children)
			} else {
				index++
				frags[i].fragmentIndex = index
				leaves = append(leaves, frags[i])
				// 仅同一父 chapter 下的直接 leaf 兄弟之间建立 NEXT，保持原文顺序
				if prevLeafID != "" {
					relations = append(relations, relation{
						fromID: prevLeafID,
						toID:   frags[i].id,
						typ:    "NEXT",
					})
				}
				prevLeafID = frags[i].id
			}
		}
	}
	walk(nil, frags)
	return leaves, chapters, relations
}

// headingInfo 描述文档中的一个标题行
type headingInfo struct {
	lineNum int
	level   int
	title   string
}

// textBlock 是正文切分后的一个段落块。
type textBlock struct {
	text      string
	startLine int
	endLine   int
}

// defaultChapterName 用文档基名（去扩展名）作为默认 chapter 名。
func defaultChapterName(docPath string) string {
	base := filepath.Base(docPath)
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	if base == "" {
		return "正文"
	}
	return base
}

func generateNodeID(key string) string {
	sum := sha1.Sum([]byte(key))
	return "node:" + hex.EncodeToString(sum[:12])
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	cloned := make(map[string]any, len(m))
	for k, v := range m {
		cloned[k] = v
	}
	return cloned
}

// assignSkeleton maps a fragment to the most specific skeleton node whose
// heading path matches the fragment's heading path. It walks the skeleton tree
// level by level against the fragment's heading path components (joined by " > "),
// advancing as long as the normalized component matches a child of the current
// skeleton node. If no root-level component matches, it falls back to the first
// skeleton root so the fragment still mounts somewhere (conservative default).
//
// This replaces the previous behaviour that ignored the fragment entirely and
// always mounted every fragment onto the first skeleton root, which made the
// skeleton layer useless for hierarchical GraphRAG retrieval.
// defaultUnknownSkeletonID is the auto-generated skeleton node that collects
// every fragment whose heading path matches no real skeleton node.
const defaultUnknownSkeletonID = "skeleton:unknown"

// assignSkeleton maps a fragment to the most specific skeleton node whose
// heading path matches the fragment's heading path. If the fragment matches
// no skeleton node at all, it is mounted to the auto "unknown data" node so
// that unmatched content is still grouped under a single, queryable node.
func (s *Source) assignSkeleton(f fragment) string {
	if len(s.skeletons) == 0 {
		return ""
	}
	s.ensureSkeletonIndex()
	return s.matchSkeletonNode(f.headingPath)
}

// effectiveSkeletons returns the skeleton list used for indexing and graph
// emission: the user-provided skeletons plus an auto "unknown data" node
// (added only when at least one skeleton is provided). The result is cached
// in s.skeletonView.
func (s *Source) effectiveSkeletons() []SkeletonNode {
	if s.skeletonView != nil {
		return s.skeletonView
	}
	if len(s.skeletons) == 0 {
		return nil
	}
	// Reuse the user's node if they already defined an unknown node.
	for _, sk := range s.skeletons {
		if sk.ID == defaultUnknownSkeletonID {
			s.skeletonView = s.skeletons
			return s.skeletonView
		}
	}
	view := make([]SkeletonNode, 0, len(s.skeletons)+1)
	view = append(view, s.skeletons...)
	view = append(view, SkeletonNode{
		ID:       defaultUnknownSkeletonID,
		Name:     "未知数据",
		Content:  "未匹配到骨架定义的文档内容",
		ParentID: "",
		Metadata: map[string]any{trpcAstMetaPrefix + "auto": "unknown"},
	})
	s.skeletonView = view
	return view
}

// ensureSkeletonIndex builds the in-memory skeleton tree once, keyed by
// ParentID, from the effective skeleton list (which includes the auto
// "unknown data" node).
func (s *Source) ensureSkeletonIndex() {
	if s.skeletonBuilt {
		return
	}
	s.skeletonBuilt = true

	skeletons := s.effectiveSkeletons()
	byParent := make(map[string][]*skeletonTreeNode)
	all := make([]*skeletonTreeNode, 0, len(skeletons))
	for i := range skeletons {
		n := &skeletonTreeNode{node: skeletons[i]}
		byParent[skeletons[i].ParentID] = append(byParent[skeletons[i].ParentID], n)
		all = append(all, n)
	}
	for _, n := range all {
		n.children = byParent[n.node.ID]
	}
	s.skeletonRoots = byParent[""]
}

// matchSkeletonNode walks the skeleton tree against the fragment's heading
// path and returns the deepest skeleton node ID that matches. When nothing
// matches (including the top level), it returns the auto "unknown data" node
// ID so unmatched content is collected there.
func (s *Source) matchSkeletonNode(path string) string {
	components := strings.Split(path, " > ")
	current := s.skeletonRoots
	matched := ""
	for _, comp := range components {
		next := matchChildByKey(current, skeletonNormKey(comp))
		if next == nil {
			break
		}
		matched = next.node.ID
		current = next.children
	}
	if matched == "" {
		return defaultUnknownSkeletonID
	}
	return matched
}

// matchChildByKey returns the child whose skeletonNormKey matches key, or nil.
func matchChildByKey(children []*skeletonTreeNode, key string) *skeletonTreeNode {
	if key == "" {
		return nil
	}
	for _, c := range children {
		if skeletonNormKey(c.node.Name) == key {
			return c
		}
	}
	return nil
}

// skeletonNormKey normalizes a heading/skeleton name so that fragments and
// skeleton nodes written with different numbering styles still match. It strips
// leading numbering (both "1.1 " and "第一章 "/ "第1条 " forms) and collapses
// whitespace. For example "1.1 设计依据" and "设计依据" both normalize to
// "设计依据", and "第一章 总则" matches "1 总则".
func skeletonNormKey(name string) string {
	s := strings.TrimSpace(name)
	s = reLeadCNAndNumber.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	s = reCollapseSpace.ReplaceAllString(s, " ")
	return strings.ToLower(s)
}

var (
	// Strips a leading numbering prefix in either form:
	//   - "第一章 " / "第1条 " / "第(一)节 "
	//   - "1.1 " / "3) " / "2． "
	// Literal Unicode punctuation (． and 、) is used because Go's regexp
	// (RE2) does not support \uXXXX escapes.
	reLeadCNAndNumber = regexp.MustCompile(
		`^(第[一二三四五六七八九十百千0-9()]+[章节条款项段部分]?|[0-9]+([.．]\d+)*[.．\s、)]*)`,
	)
	reCollapseSpace = regexp.MustCompile(`\s+`)
)

// ============================================================
// 表格/图片识别 + 按字符限制切割的辅助函数
// ============================================================

const (
	// maxFragmentChars 每个 fragment 的最大字符数（按行边界切割，不拆表/图/行）
	maxFragmentChars = 1500
)

// reMDTableSeparator 匹配 Markdown 表格分隔行，如 "| --- | --- |"
var reMDTableSeparator = regexp.MustCompile(`^\s*\|[\s\-:|]+\|\s*$`)

// reImageRef 匹配 Markdown 图片引用 ![alt](url)
var reImageRef = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)

// reHTMLTableStart 匹配 HTML 表格开始标签
var reHTMLTableStart = regexp.MustCompile(`(?i)<table[\s>]`)

// reHTMLTableEnd 匹配 HTML 表格结束标签
var reHTMLTableEnd = regexp.MustCompile(`(?i)</table>`)

// isMDTableSeparator 判断一行是否为 Markdown 表格分隔行
func isMDTableSeparator(line string) bool {
	return reMDTableSeparator.MatchString(strings.TrimSpace(line))
}

// isMDTableRow 判断一行是否为 Markdown 表格数据行（以 | 开头）
func isMDTableRow(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "|")
}
func is3LevelTocRow(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "### ")
}

// extractImagesFromLine 从一行中提取所有图片引用
func extractImagesFromLine(line string) []imageInfo {
	var images []imageInfo
	matches := reImageRef.FindAllStringSubmatch(line, -1)
	for _, m := range matches {
		images = append(images, imageInfo{
			alt: m[1],
			url: m[2],
		})
	}
	return images
}
func parse3LevelTocBlock(lines []string, startIdx int) (lineBlock, int) {
	var block lineBlock
	block.startLine = startIdx + 1 // 转为 1-based

	i := startIdx + 1
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		if is3LevelTocRow(trimmed) {
			break
		}
		i++
	}
	block.endLine = i - 1
	return block, i - 1
}

// mdTableBlock 描述一个 Markdown 表格块
type mdTableBlock struct {
	startLine int // 1-based
	endLine   int // 1-based
	headers   []string
	rows      [][]string
	preview   string
	rowCount  int
	colCount  int
}

// parseMDTableBlock 从 lines 中解析一个 Markdown 表格块
// startIdx 是表格第一行的索引（0-based），返回表格块和结束行索引
func parseMDTableBlock(lines []string, startIdx int) (mdTableBlock, int) {
	var block mdTableBlock
	block.startLine = startIdx + 1 // 转为 1-based

	var tableLines []string
	i := startIdx
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || !isMDTableRow(trimmed) {
			break
		}
		tableLines = append(tableLines, trimmed)
		i++
	}

	if len(tableLines) == 0 {
		return block, startIdx
	}

	// 解析表头
	headerLine := tableLines[0]
	block.headers = parseMDTableCells(headerLine)

	// 跳过分隔行
	sepIdx := 1
	if sepIdx < len(tableLines) && isMDTableSeparator(tableLines[sepIdx]) {
		sepIdx++
	}

	// 解析数据行
	for r := sepIdx; r < len(tableLines); r++ {
		cells := parseMDTableCells(tableLines[r])
		block.rows = append(block.rows, cells)
	}

	block.endLine = startIdx + len(tableLines) // 1-based
	block.rowCount = len(block.rows)
	block.colCount = len(block.headers)

	// 生成 preview
	var parts []string
	if len(block.headers) > 0 {
		parts = append(parts, "表头: "+strings.Join(block.headers, " | "))
	}
	previewRows := 3
	if len(block.rows) < previewRows {
		previewRows = len(block.rows)
	}
	for r := 0; r < previewRows; r++ {
		parts = append(parts, strings.Join(block.rows[r], " | "))
	}
	block.preview = strings.Join(parts, "\n")

	return block, i - 1
}

// parseMDTableCells 解析一行 Markdown 表格的单元格
func parseMDTableCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	var cells []string
	for _, p := range parts {
		cells = append(cells, strings.TrimSpace(p))
	}
	return cells
}

// htmlTableBlock 描述一个 HTML 表格块
type htmlTableBlock struct {
	startLine int // 1-based
	endLine   int // 1-based
	preview   string
}

// parseHTMLTableBlock 从 lines 中解析一个 HTML 表格块
// startIdx 是 <table> 行的索引（0-based），返回表格块和结束行索引
func parseHTMLTableBlock(lines []string, startIdx int) (htmlTableBlock, int) {
	var block htmlTableBlock
	block.startLine = startIdx + 1 // 转为 1-based

	i := startIdx
	for i < len(lines) {
		if reHTMLTableEnd.MatchString(lines[i]) {
			block.endLine = i + 1 // 1-based
			// 简单 preview：取表格前几行
			var previewLines []string
			for j := startIdx; j <= i && j < len(lines); j++ {
				previewLines = append(previewLines, strings.TrimSpace(lines[j]))
			}
			preview := strings.Join(previewLines, " ")
			if len(preview) > 200 {
				preview = preview[:200]
			}
			block.preview = preview
			return block, i
		}
		i++
	}

	// 未找到结束标签，表格延伸到文档末尾
	block.endLine = len(lines)
	block.preview = "(HTML表格未闭合)"
	return block, len(lines) - 1
}

// lineBlock 描述文档中一个连续的行块（用于切割）
type lineBlock struct {
	startLine int    // 1-based
	endLine   int    // 1-based
	charCount int    // 该块的字符数
	blockType string // "text", "md_table", "html_table", "image_line","3level_toc"
	tables    []tableInfo
	images    []imageInfo
}

// buildLineBlocks 将文档行划分为 lineBlock 列表
// 识别 Markdown 表格、HTML 表格、图片行，其余为普通文本行
// buildLineBlocks 将文档行划分为 lineBlock 列表
// 识别 Markdown 表格、HTML 表格、图片行，其余为普通文本行
// detect3Level 控制是否检测 ### 层级目录块，递归拆分时设为 false 避免死循环
func buildLineBlocks(lines []string) []lineBlock {
	return buildLineBlocksInternal(lines, true)
}

// buildLineBlocksInternal 内部实现，detect3Level 控制是否检测 ### 层级目录
func buildLineBlocksInternal(lines []string, detect3Level bool) []lineBlock {
	var blocks []lineBlock
	i := 0
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		if detect3Level && is3LevelTocRow(trimmed) {
			block, endIdx := parse3LevelTocBlock(lines, i)
			charCount := 0
			for j := i; j <= endIdx && j < len(lines); j++ {
				charCount += len([]rune(lines[j]))
			}

			// 如果 ### 块内容超过 maxFragmentChars，递归拆分
			if charCount > maxFragmentChars {
				// 提取该块的内容行（从 ### 行到下一个 ### 行之前）
				subLines := make([]string, 0, endIdx-i+1)
				for j := i; j <= endIdx && j < len(lines); j++ {
					subLines = append(subLines, lines[j])
				}
				// 递归调用，detect3Level=false 避免死循环
				subBlocks := buildLineBlocksInternal(subLines, false)
				// 将递归结果合并到当前 blocks 列表中
				blocks = append(blocks, subBlocks...)
			} else {
				blocks = append(blocks, lineBlock{
					startLine: block.startLine,
					endLine:   block.endLine,
					charCount: charCount,
					blockType: "3level_toc",
					tables:    []tableInfo{},
				})
			}
			i = endIdx + 1
			continue
		}
		// 检测 Markdown 表格
		if isMDTableRow(trimmed) {
			block, endIdx := parseMDTableBlock(lines, i)
			charCount := 0
			for j := i; j <= endIdx && j < len(lines); j++ {
				charCount += len([]rune(lines[j]))
			}
			blocks = append(blocks, lineBlock{
				startLine: block.startLine,
				endLine:   block.endLine,
				charCount: charCount,
				blockType: "md_table",
				tables: []tableInfo{{
					title:     "",
					startLine: block.startLine,
					endLine:   block.endLine,
					rowCount:  block.rowCount,
					colCount:  block.colCount,
					preview:   block.preview,
				}},
			})
			i = endIdx + 1
			continue
		}

		// 检测 HTML 表格
		if reHTMLTableStart.MatchString(trimmed) {
			block, endIdx := parseHTMLTableBlock(lines, i)
			charCount := 0
			for j := i; j <= endIdx && j < len(lines); j++ {
				charCount += len([]rune(lines[j]))
			}
			blocks = append(blocks, lineBlock{
				startLine: block.startLine,
				endLine:   block.endLine,
				charCount: charCount,
				blockType: "html_table",
				tables: []tableInfo{{
					title:     "",
					startLine: block.startLine,
					endLine:   block.endLine,
					preview:   block.preview,
				}},
			})
			i = endIdx + 1
			continue
		}

		// 普通文本行（含可能的图片）
		images := extractImagesFromLine(lines[i])
		blocks = append(blocks, lineBlock{
			startLine: i + 1, // 1-based
			endLine:   i + 1,
			charCount: len([]rune(lines[i])),
			blockType: "text",
			images:    images,
		})
		i++
	}
	return blocks
}

// splitBlocksByCharLimit 将 lineBlock 列表按字符限制切割为多个片段
// 每个片段不超过 maxChars 字符，保证不拆行、不拆表、不拆图
func splitBlocksByCharLimit(blocks []lineBlock, maxChars int) [][]lineBlock {
	var segments [][]lineBlock
	var currentSegment []lineBlock
	currentChars := 0

	for _, block := range blocks {
		// 如果当前块本身超过限制，单独作为一个片段
		if block.charCount > maxChars {
			// 先保存当前片段
			if len(currentSegment) > 0 {
				segments = append(segments, currentSegment)
				currentSegment = nil
				currentChars = 0
			}
			// 大块单独成段
			segments = append(segments, []lineBlock{block})
			continue
		}

		// 如果加入当前块会超过限制，先保存当前片段
		if currentChars+block.charCount > maxChars && len(currentSegment) > 0 {
			segments = append(segments, currentSegment)
			currentSegment = nil
			currentChars = 0
		}

		currentSegment = append(currentSegment, block)
		currentChars += block.charCount
	}

	// 保存最后一个片段
	if len(currentSegment) > 0 {
		segments = append(segments, currentSegment)
	}

	return segments
}

// collectTablesAndImages 从 lineBlock 列表中收集表格和图片信息
func collectTablesAndImages(blocks []lineBlock) ([]tableInfo, []imageInfo) {
	var tables []tableInfo
	var images []imageInfo
	for _, b := range blocks {
		tables = append(tables, b.tables...)
		images = append(images, b.images...)
	}
	return tables, images
}

// bodySegment 描述正文切割后的一个片段
type bodySegment struct {
	content   string
	startLine int // 1-based
	endLine   int // 1-based
	tables    []tableInfo
	images    []imageInfo
}

// splitBodyByCharLimit 将正文内容按字符限制切割为多个片段
// 保证不拆行、不拆表、不拆图
func splitBodyByCharLimit(bodyLines []string, contentStart int, maxChars int) []bodySegment {
	// 先将 bodyLines 划分为 lineBlock 列表
	blocks := buildLineBlocks(bodyLines)

	// 按字符限制切割
	segments := splitBlocksByCharLimit(blocks, maxChars)

	// 转换为 bodySegment 列表
	var result []bodySegment
	for _, segBlocks := range segments {
		tables, images := collectTablesAndImages(segBlocks)

		// 计算行号范围（转换为 1-based 绝对行号）
		startLine := segBlocks[0].startLine + contentStart - 1
		endLine := segBlocks[len(segBlocks)-1].endLine + contentStart - 1

		// 构建内容
		var allLines []string
		for _, b := range segBlocks {
			for i := b.startLine - 1; i < b.endLine && i < len(bodyLines); i++ {
				allLines = append(allLines, bodyLines[i])
			}
		}
		content := strings.TrimSpace(strings.Join(allLines, "\n"))

		result = append(result, bodySegment{
			content:   content,
			startLine: startLine,
			endLine:   endLine,
			tables:    tables,
			images:    images,
		})
	}

	return result
}

// buildContentFromBlocks 从 lineBlock 列表构建文本内容
func buildContentFromBlocks(blocks []lineBlock, lines []string) string {
	var allLines []string
	for _, b := range blocks {
		for i := b.startLine - 1; i < b.endLine && i < len(lines); i++ {
			allLines = append(allLines, lines[i])
		}
	}
	return strings.TrimSpace(strings.Join(allLines, "\n"))
}
