// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package fragment provides a graph source that parses Markdown documents into
// a four-layer graph structure: skeleton → document → chapter → fragment.
package fragment

import (
	"context"
	"log"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
)

// mountTarget is a single (fragment → skeleton node) mounting decision.
type mountTarget struct {
	// SkeletonID is the target skeleton node ID (may be defaultUnknownSkeletonID).
	SkeletonID string
	// MountType is "direct" (fully covers a node), "partial" (partial
	// coverage), or "unknown" (no confident match).
	MountType string
	// MatchSource records which strategy produced the decision:
	// "deterministic" (heading-path matcher), "semantic:vector+rerank"
	// (vector retrieval + reranker), or "semantic:vector-only"
	// (reranker unavailable, cosine fallback).
	MatchSource string
}

// semanticMounter mounts fragments onto skeleton nodes using a vector model
// (embedding) for encoding, an in-memory vector store for top-K candidate
// retrieval, and a reranker for final ranking. It follows the
// skeleton-mounter v3 design: deterministic-first (L1), then semantic.
//
// Why an in-memory vector store (no CGO): the skeleton is tiny (≈10
// nodes), so a pure-Go cosine index is sufficient and keeps this package
// free of the CGO/sqlite-vec build dependency.
type semanticMounter struct {
	embedder  embedder.Embedder
	reranker  reranker.Reranker
	store     *inmemory.VectorStore
	docs      map[string]*document.Document // skeleton node ID -> doc
	unknownID string
	built     bool

	// topK is the number of skeleton candidates retrieved per fragment.
	topK int
	// minCosine is the absolute cosine gate: a candidate is only
	// considered when its vector similarity clears this floor, so clearly
	// unrelated fragments never mount even if the reranker scale is odd.
	minCosine float64
	// partialThreshold / directThreshold are applied to the reranker
	// score normalized to [0,1] across the K candidates.
	partialThreshold float64
	directThreshold  float64
	// multiNodeDelta allows a second/third candidate (within this distance
	// of the top normalized score) to also be mounted (single fragment
	// covering multiple business domains → multiple skeleton nodes).
	multiNodeDelta float64

	// parentByID maps a skeleton node ID to its parent node ID ("" =
	// root). Used by refine for ancestor suppression (keep the most
	// specific node when both an ancestor and a descendant are accepted).
	parentByID map[string]string
	// keywordsByID holds heading-match business keywords (the node Name
	// plus Chinese terms parsed from its description) used to
	// disambiguate near-tie candidates via the fragment's parent
	// heading path.
	keywordsByID map[string][]string
	// mutexBand: two accepted candidates whose normalized scores are
	// within this distance of the top candidate are "near-ties"; when the
	// fragment's heading path disambiguates them, only the heading-
	// matching candidates are kept.
	mutexBand float64
}

// newSemanticMounter builds a mounter from the user skeleton nodes
// (the auto "unknown data" node is intentionally excluded so it never
// becomes a retrieval candidate). It does NOT embed anything yet;
// indexing is deferred to ensureIndex so it can use the request context.
func newSemanticMounter(
	emb embedder.Embedder,
	rk reranker.Reranker,
	unknownID string,
	skeletons []SkeletonNode,
) *semanticMounter {
	m := &semanticMounter{
		embedder:         emb,
		reranker:         rk,
		store:            inmemory.New(),
		docs:             make(map[string]*document.Document),
		unknownID:        unknownID,
		topK:             5,
		minCosine:        0.10,
		partialThreshold: 0.20,
		directThreshold:  0.60,
		multiNodeDelta:   0.12,
		parentByID:       make(map[string]string),
		keywordsByID:     make(map[string][]string),
		mutexBand:        0.15,
	}
	for _, sk := range skeletons {
		if sk.ID == "" || sk.ID == unknownID {
			continue
		}
		// 用「英文节点名 + 中文业务描述」作为向量文本。业务概念
		// （导地线选型、杆塔规划、基础型式…）集中在 description 中，
		// 它是与中文文档 fragment 对齐的关键。
		text := sk.Name
		if sk.Content != "" {
			text = sk.Name + "：" + sk.Content
		}
		m.docs[sk.ID] = &document.Document{
			ID:      sk.ID,
			Name:    sk.Name,
			Content: text,
		}
		if sk.ParentID != "" {
			m.parentByID[sk.ID] = sk.ParentID
		}
		m.keywordsByID[sk.ID] = skeletonKeywords(sk)
	}
	return m
}

// ensureIndex embeds every skeleton node once and adds it to the vector
// store. It is idempotent and uses the request context for the embedder.
func (m *semanticMounter) ensureIndex(ctx context.Context) error {
	if m.built {
		return nil
	}
	m.built = true
	for id, doc := range m.docs {
		vec, err := m.embedder.GetEmbedding(ctx, doc.Content)
		if err != nil || len(vec) == 0 {
			log.Printf("[fragment-mount] skip skeleton %s (embed failed: %v)", id, err)
			delete(m.docs, id)
			continue
		}
		if err := m.store.Add(ctx, doc, vec); err != nil {
			log.Printf("[fragment-mount] add skeleton %s failed: %v", id, err)
			delete(m.docs, id)
		}
	}
	return nil
}

// Mount decides which skeleton node(s) a leaf fragment mounts to.
func (m *semanticMounter) Mount(ctx context.Context, f fragment) []mountTarget {
	if err := m.ensureIndex(ctx); err != nil {
		return m.unknown()
	}

	queryText := buildMountQuery(f)
	fragVec, err := m.embedder.GetEmbedding(ctx, queryText)
	if err != nil || len(fragVec) == 0 {
		log.Printf("[fragment-mount] embed fragment %q failed: %v", f.name, err)
		return m.unknown()
	}

	res, err := m.store.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeVector,
		Vector:     fragVec,
		Limit:      m.topK,
		MinScore:   0,
	})
	if err != nil || res == nil || len(res.Results) == 0 {
		return m.unknown()
	}

	cands := make([]candidate, 0, len(res.Results))
	cosineByID := make(map[string]float64)
	for _, sd := range res.Results {
		if sd.Document == nil {
			continue
		}
		cands = append(cands, candidate{id: sd.Document.ID, cosine: sd.Score})
		cosineByID[sd.Document.ID] = sd.Score
	}
	if len(cands) == 0 {
		return m.unknown()
	}

	// 送重排模型，得到 relevance 排序。
	rerankIn := make([]*reranker.Result, len(cands))
	for i, c := range cands {
		d := m.docs[c.id]
		content := ""
		if d != nil {
			content = d.Content
		}
		rerankIn[i] = &reranker.Result{
			Document: &document.Document{ID: c.id, Name: c.id, Content: content},
			Score:    c.cosine,
		}
	}
	q := &reranker.Query{Text: queryText, FinalQuery: queryText}

	var accepted []scoredTarget
	if m.reranker == nil {
		// 未配置重排模型 -> 直接按余弦排序兜底（仍用向量模型+向量库）。
		accepted = m.acceptByCosine(cands)
	} else {
		reranked, rerr := m.reranker.Rerank(ctx, q, rerankIn)
		if rerr != nil || len(reranked) == 0 {
			// 重排不可用 -> 退回按余弦排序的兜底逻辑（仍用向量模型+向量库）。
			log.Printf("[fragment-mount] reranker unavailable (%v); falling back to cosine ordering", rerr)
			accepted = m.acceptByCosine(cands)
		} else {
			accepted = m.acceptByRerank(reranked, cosineByID)
		}
	}

	if len(accepted) == 0 {
		return m.unknown()
	}
	// 向量召回 + 重排后的候选，再经「特异性 / 父章节消歧」精炼。
	refined := m.refine(accepted, f)
	if len(refined) == 0 {
		return m.unknown()
	}
	return refined
}

// unknown returns a single "unknown data" mounting so unmatched content
// stays grouped under the auto skeleton node (preserves coverage).
func (m *semanticMounter) unknown() []mountTarget {
	return []mountTarget{{
		SkeletonID:  m.unknownID,
		MountType:   "unknown",
		MatchSource: "semantic",
	}}
}

type candidate struct {
	id     string
	cosine float64
}

// acceptByRerank turns the reranker ordering into intermediate scored
// targets. The reranker score is min-max normalized across the K
// candidates to make the decision invariant to the backend's raw score
// scale, then gated by partialThreshold (with a cosine safety floor)
// and used for multi-node. Specificity / heading-context disambiguation
// is applied later in refine.
func (m *semanticMounter) acceptByRerank(
	reranked []*reranker.Result,
	cosineByID map[string]float64,
) []scoredTarget {
	// 单候选：唯一匹配即视为 direct。否则 min-max 归一化在
	// max==min 时会退化为恒 0.5，被 directThreshold 误判为 partial，
	// 丢失「唯一强匹配 = 直接覆盖」的语义。
	if len(reranked) == 1 {
		r := reranked[0]
		if r.Document == nil {
			return nil
		}
		id := r.Document.ID
		if cosineByID[id] < m.minCosine {
			return nil
		}
		return []scoredTarget{{
			id:     id,
			norm:   1.0,
			cosine: cosineByID[id],
			mt:     "direct",
			src:    "semantic:vector+rerank",
		}}
	}

	scores := make([]float64, len(reranked))
	for i, r := range reranked {
		scores[i] = r.Score
	}
	minS, maxS := minMax(scores)
	norm := func(v float64) float64 {
		if maxS == minS {
			return 0.5
		}
		return (v - minS) / (maxS - minS)
	}

	var out []scoredTarget
	firstNorm := -1.0
	for i, r := range reranked {
		if r.Document == nil {
			continue
		}
		id := r.Document.ID
		cos := cosineByID[id]
		n := norm(r.Score)
		// reranked 已按分降序；首个不达标即可停止。
		if n < m.partialThreshold || cos < m.minCosine {
			break
		}
		// 各自按归一化分判定 direct/partial，而非盲目把第二个标 partial。
		mt := "partial"
		if n >= m.directThreshold {
			mt = "direct"
		}
		if firstNorm < 0 {
			firstNorm = n
		}
		// 多节点：与首个归一化分距超过 delta 的额外候选停止计入。
		if i > 0 && (firstNorm-n) > m.multiNodeDelta {
			break
		}
		out = append(out, scoredTarget{
			id:     id,
			norm:   n,
			cosine: cos,
			mt:     mt,
			src:    "semantic:vector+rerank",
		})
	}
	return out
}

// scoredTarget is an intermediate mount decision carrying the scores
// refine needs for specificity / heading-context disambiguation.
type scoredTarget struct {
	id     string
	norm   float64 // reranker score min-max normalized to [0,1]
	cosine float64
	mt     string // "direct" | "partial"
	src    string // match source label
}

// refine applies two specificity rules on top of the reranker/cosine
// decision:
//  1. ancestor suppression — drop a node when a descendant of it is also
//     accepted (keep the most specific node);
//  2. heading-context mutex — when several near-ties (within mutexBand
//     of the top candidate) exist, keep only the candidates whose
//     business keywords appear in the fragment's parent heading path.
//     This is what stops a "杆塔" section from being co-mounted to
//     导/塔/金具/接地 just because those domains co-occur in text.
func (m *semanticMounter) refine(in []scoredTarget, f fragment) []mountTarget {
	kept := append([]scoredTarget(nil), in...)
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].norm > kept[j].norm })

	// 1) ancestor suppression: drop any ancestor whose descendant is kept.
	n := 0
	for _, a := range kept {
		suppressed := false
		for _, b := range kept {
			if a.id == b.id {
				continue
			}
			if m.isAncestor(a.id, b.id) {
				suppressed = true
				break
			}
		}
		if !suppressed {
			kept[n] = a
			n++
		}
	}
	kept = kept[:n]

	// 2) heading-context mutex among near-ties (within mutexBand of top).
	if len(kept) >= 2 {
		top := kept[0].norm
		tie := make(map[string]bool)
		for _, k := range kept {
			if top-k.norm <= m.mutexBand {
				tie[k.id] = true
			}
		}
		if len(tie) >= 2 {
			anyMatch := false
			for id := range tie {
				if m.headingMatch(id, f.headingPath) {
					anyMatch = true
					break
				}
			}
			if anyMatch {
				n2 := 0
				for _, k := range kept {
					if tie[k.id] && !m.headingMatch(k.id, f.headingPath) {
						continue
					}
					kept[n2] = k
					n2++
				}
				kept = kept[:n2]
			}
		}
	}

	out := make([]mountTarget, 0, len(kept))
	for _, k := range kept {
		out = append(out, mountTarget{SkeletonID: k.id, MountType: k.mt, MatchSource: k.src})
	}
	return out
}

// isAncestor reports whether aID is an ancestor of bID in the skeleton
// tree (following ParentID upward from b).
func (m *semanticMounter) isAncestor(aID, bID string) bool {
	cur := m.parentByID[bID]
	for cur != "" {
		if cur == aID {
			return true
		}
		cur = m.parentByID[cur]
	}
	return false
}

// headingMatch reports whether any business keyword of skeleton node id
// appears in the fragment's parent heading path.
func (m *semanticMounter) headingMatch(id, headingPath string) bool {
	if headingPath == "" {
		return false
	}
	hp := strings.ToLower(headingPath)
	for _, kw := range m.keywordsByID[id] {
		if kw == "" {
			continue
		}
		if strings.Contains(hp, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// skeletonKeywords extracts heading-match keywords from a skeleton node:
// its (English) Name plus the Chinese business terms in its description.
func skeletonKeywords(sk SkeletonNode) []string {
	kws := make([]string, 0, 8)
	if sk.Name != "" {
		kws = append(kws, sk.Name)
	}
	kws = append(kws, splitKeywords(sk.Content)...)
	return kws
}

// splitKeywords splits a description into business-term tokens on common
// Chinese/English delimiters, keeping multi-char Chinese terms (e.g.
// "杆塔", "接地电阻") which is what parent heading paths contain.
func splitKeywords(s string) []string {
	f := func(r rune) bool {
		switch r {
		case '、', '，', '；', '：', ':', ',', ' ', '/', '(', ')', '（', '）', ';':
			return true
		}
		return false
	}
	parts := strings.FieldsFunc(s, f)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// acceptByCosine is the fallback used when the reranker is unavailable.
// It trusts the in-memory vector store's cosine ordering: the top candidate
// (if it clears the cosine floor) is mounted as "direct".
func (m *semanticMounter) acceptByCosine(cands []candidate) []scoredTarget {
	if len(cands) == 0 || cands[0].cosine < m.minCosine {
		return nil
	}
	return []scoredTarget{{
		id:     cands[0].id,
		norm:   cands[0].cosine,
		cosine: cands[0].cosine,
		mt:     "direct",
		src:    "semantic:vector-only",
	}}
}

// buildMountQuery builds the fragment representation from the three fields
// the skeleton-mounter design matches on, by increasing information
// density: title (heading) → preview (body summary / tables) → and, as
// extra context, the parent heading path (the section the fragment lives
// under). In the Go source we have heading title, body content, and
// headingPath; title = the heading, preview = up to ~900 chars of body
// (the leading heading line, which duplicates the title, is skipped — this
// now also captures table text), and the ancestor headings from
// headingPath supply the domain signal a thinly-titled fragment would
// otherwise lack.
func buildMountQuery(f fragment) string {
	title := strings.TrimSpace(f.name)
	ancestors := ancestorContext(f.headingPath, title)
	preview := strings.TrimSpace(f.content)
	if title != "" {
		if idx := strings.Index(preview, "\n"); idx >= 0 {
			firstLine := strings.TrimSpace(preview[:idx])
			if firstLine == title || strings.Contains(firstLine, title) {
				preview = strings.TrimSpace(preview[idx+1:])
			}
		}
	}
	const maxPreview = 900
	runes := []rune(preview)
	if len(runes) > maxPreview {
		preview = string(runes[:maxPreview])
	}
	var b strings.Builder
	b.Grow(len(ancestors) + len(title) + len(preview) + 8)
	if ancestors != "" {
		b.WriteString(ancestors)
		b.WriteString("\n")
	}
	if title != "" {
		b.WriteString(title)
		b.WriteString("\n")
	}
	b.WriteString(preview)
	return strings.TrimSpace(b.String())
}

// ancestorContext returns the fragment's parent heading components
// (everything except its own title) joined for use as skeleton-matching
// context. It gives a thinly-titled fragment the domain signal of the
// section it lives under (e.g. a "塔型规划" fragment under "12 杆塔设计"
// inherits "杆塔").
func ancestorContext(headingPath, title string) string {
	if headingPath == "" {
		return ""
	}
	parts := strings.Split(headingPath, " > ")
	// drop the trailing component if it is the fragment's own title
	if len(parts) > 0 {
		last := strings.TrimSpace(parts[len(parts)-1])
		if last == strings.TrimSpace(title) {
			parts = parts[:len(parts)-1]
		}
	}
	cleaned := parts[:0]
	for _, p := range parts {
		if !strings.Contains(p, title) {
			cleaned = append(cleaned, strings.TrimSpace(p))
		}
	}
	return strings.Join(cleaned, " / ")
}

func minMax(vs []float64) (float64, float64) {
	if len(vs) == 0 {
		return 0, 0
	}
	mn, mx := vs[0], vs[0]
	for _, v := range vs[1:] {
		if v < mn {
			mn = v
		}
		if v > mx {
			mx = v
		}
	}
	return mn, mx
}

// mountFragment decides which skeleton node(s) a leaf fragment mounts to.
// When a semantic mounter is configured (embedder + reranker), it
// uses vector retrieval + reranking; otherwise it falls back to the
// deterministic heading-path matcher, preserving the old behaviour.
func (s *Source) mountFragment(ctx context.Context, f fragment) []mountTarget {
	if s.mounter != nil {
		return s.mounter.Mount(ctx, f)
	}
	// Deterministic fallback (no embedder/reranker configured).
	id := s.assignSkeleton(f)
	if id == "" {
		return nil
	}
	mt := "deterministic"
	if id == defaultUnknownSkeletonID {
		mt = "unknown"
	}
	return []mountTarget{{SkeletonID: id, MountType: mt, MatchSource: "deterministic"}}
}
