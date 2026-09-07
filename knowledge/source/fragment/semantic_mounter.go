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
	"encoding/json"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
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
	kwScore     float64
	rrScore     float64
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
	keywordsByID   map[string][]string
	docCatalogByID map[string][]string
	// mutexBand: two accepted candidates whose normalized scores are
	// within this distance of the top candidate are "near-ties"; when the
	// fragment's heading path disambiguates them, only the heading-
	// matching candidates are kept.
	mutexBand float64

	// keywordVecs maps skeleton node ID -> keyword text -> embedding vector.
	// Built lazily by ensureIndex so every keyword is available for
	// independent cosine matching against a fragment embedding.
	keywordVecs map[string]map[string][]float64
	// keywordMatchThreshold is the per-keyword cosine floor used to
	// decide whether a skeleton node is a candidate. Each keyword of a
	// node is matched independently; the node's final score is the
	// maximum keyword cosine. Only nodes whose max keyword cosine clears
	// this threshold enter the candidate pool.
	keywordMatchThreshold float64
	rerankMatchThreshold  float64
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
	keywordMatchThreshold float64,
	rerankMatchThreshold float64,
) *semanticMounter {
	m := &semanticMounter{
		embedder:              emb,
		reranker:              rk,
		store:                 inmemory.New(),
		docs:                  make(map[string]*document.Document),
		unknownID:             unknownID,
		partialThreshold:      0.20,
		directThreshold:       0.60,
		multiNodeDelta:        0.12,
		parentByID:            make(map[string]string),
		keywordsByID:          make(map[string][]string),
		docCatalogByID:        make(map[string][]string),
		mutexBand:             0.15,
		keywordVecs:           make(map[string]map[string][]float64),
		keywordMatchThreshold: keywordMatchThreshold,
		rerankMatchThreshold:  rerankMatchThreshold,
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
		m.docCatalogByID[sk.ID] = sk.SourceCatalog
	}
	return m
}

// ensureIndex embeds every skeleton node once and adds it to the vector
// store. It also embeds every keyword for each skeleton node independently
// so that Mount can do per-keyword cosine matching. It is idempotent and
// uses the request context for the embedder.
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
			delete(m.keywordVecs, id)
			continue
		}
		if err := m.store.Add(ctx, doc, vec); err != nil {
			log.Printf("[fragment-mount] add skeleton %s failed: %v", id, err)
			delete(m.docs, id)
			delete(m.keywordVecs, id)
		}
	}
	// Embed each keyword independently.
	for id, kws := range m.keywordsByID {
		m.keywordVecs[id] = make(map[string][]float64, len(kws))
		for _, kw := range kws {
			if kw == "" {
				continue
			}
			vec, err := m.embedder.GetEmbedding(ctx, kw)
			if err != nil || len(vec) == 0 {
				log.Printf("[fragment-mount] keyword embed failed for %s/%q: %v", id, kw, err)
				continue
			}
			m.keywordVecs[id][kw] = vec
		}
	}
	return nil
}

// Mount decides which skeleton node(s) a leaf fragment mounts to.
// It embeds the fragment once and then performs per-keyword cosine
// matching against every keyword of every skeleton node. The maximum
// keyword cosine per skeleton node is the node's score; nodes that
// clear keywordMatchThreshold enter the candidate pool and are then
// optionally refined by a reranker.
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

	// Per-keyword matching: each skeleton node's score is the max cosine
	// across its keywords.
	accepted := m.matchByKeywords(fragVec, f.docCategory)

	if len(accepted) == 0 {
		return m.unknown()
	}

	// Optional: send the keyword-matched candidates through the reranker
	// for a second round of ordering when the reranker is configured.
	if m.reranker != nil {
		accepted = m.rerankKeywordCandidates(ctx, queryText, accepted)
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

// rerankKeywordCandidates sends the keyword-matched candidates through
// the reranker for a second ordering pass. It returns the reranked
// scoredTargets on success or the original keyword scores when the
// reranker is unavailable or fails.
func (m *semanticMounter) rerankKeywordCandidates(
	ctx context.Context,
	queryText string,
	keywordMatched []scoredTarget,
) []scoredTarget {
	if len(keywordMatched) == 0 {
		return keywordMatched
	}

	rerankIn := make([]*reranker.Result, len(keywordMatched))
	for i, st := range keywordMatched {
		d := m.docs[st.id]
		content := ""
		if d != nil {
			content = d.Content
		}
		rerankIn[i] = &reranker.Result{
			Document: &document.Document{ID: st.id, Name: st.id, Content: content},
			Score:    st.kwScore,
		}
	}
	q := &reranker.Query{Text: queryText, FinalQuery: queryText}

	reranked, rerr := m.reranker.Rerank(ctx, q, rerankIn)
	if rerr != nil || len(reranked) == 0 {
		log.Printf("[fragment-mount] keyword rerank unavailable (err=%v, rerankedLen=%d); using keyword scores", rerr, len(reranked))
		m.dumpRerankJSON(queryText, rerankIn, reranked)
		return keywordMatched
	}

	// Dump rerank input/output JSON to disk for debugging.
	m.dumpRerankJSON(queryText, rerankIn, reranked)

	// Convert reranker output back to scoredTarget, preserving the
	// original cosine from keyword matching for direct/partial gating.
	cosineByID := make(map[string]float64, len(keywordMatched))
	kwNameByID := make(map[string]string, len(keywordMatched))
	for _, st := range keywordMatched {
		cosineByID[st.id] = st.kwScore
		kwNameByID[st.id] = st.src
	}

	scores := make([]float64, len(reranked))
	for i, r := range reranked {
		scores[i] = r.Score
	}
	//minS, maxS := minMax(scores)
	// norm := func(v float64) float64 {
	// 	if maxS == minS {
	// 		return 0.5
	// 	}
	// 	return (v - minS) / (maxS - minS)
	// }

	var out []scoredTarget
	for _, r := range reranked {
		if r.Document == nil {
			continue
		}
		id := r.Document.ID
		cos := cosineByID[id]
		if r.Score < m.rerankMatchThreshold {
			continue
		}
		//n := norm(r.Score)
		mt := "partial"
		// 用 keyword cosine（而非 reranker 归一化分）判定 direct/partial：
		// reranker 只负责排序，唯一候选时 min-max 归一化恒为 0.5，
		// 会错误地把 keyword cosine=1.0 的强匹配标成 partial。
		if cos >= m.directThreshold {
			mt = "direct"
		}
		out = append(out, scoredTarget{
			id:      id,
			kwScore: cos,
			rrScore: r.Score,
			mt:      mt,
			src:     "semantic:keyword+rerank",
		})
	}
	if len(out) == 0 {
		return keywordMatched
	}
	return out
}

// dumpRerankJSON writes the rerank request (query + input docs) and
// response (reranked docs with scores) to a timestamped JSON file under
// .fragment-debug/ in the current working directory for offline inspection.
func (m *semanticMounter) dumpRerankJSON(
	queryText string,
	input []*reranker.Result,
	output []*reranker.Result,
) {
	type debugDoc struct {
		ID      string  `json:"id"`
		Name    string  `json:"name,omitempty"`
		Score   float64 `json:"score,omitempty"`
		Content string  `json:"content,omitempty"`
	}
	type debugEntry struct {
		InputDocs  []debugDoc `json:"input_docs"`
		OutputDocs []debugDoc `json:"output_docs"`
	}

	entry := debugEntry{}
	for _, r := range input {
		d := debugDoc{Score: r.Score}
		if r.Document != nil {
			d.ID = r.Document.ID
			d.Name = r.Document.Name
			d.Content = r.Document.Content
		}
		entry.InputDocs = append(entry.InputDocs, d)
	}
	for _, r := range output {
		d := debugDoc{Score: r.Score}
		if r.Document != nil {
			d.ID = r.Document.ID
			d.Name = r.Document.Name
			d.Content = r.Document.Content
		}
		entry.OutputDocs = append(entry.OutputDocs, d)
	}

	payload := map[string]any{
		"query":    queryText,
		"rerank":   entry,
		"datetime": time.Now().Format(time.RFC3339),
	}
	b, _ := json.MarshalIndent(payload, "", "  ")

	dir := filepath.Join(".", ".fragment-debug")
	_ = os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, "rerank.jsonl")
	line := append(b, '\n')
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("[fragment-mount] write rerank debug json failed: %v", err)
	} else {
		_, _ = f.Write(line)
		_ = f.Close()
	}
}

// unknown returns a single "unknown data" mounting so unmatched content
// stays grouped under the auto skeleton node (preserves coverage).
func (m *semanticMounter) unknown() []mountTarget {
	return []mountTarget{{
		SkeletonID:  m.unknownID,
		MountType:   "unknown",
		MatchSource: "semantic",
		kwScore:     0,
		rrScore:     0,
	}}
}

// matchByKeywords performs per-keyword cosine matching between the
// fragment embedding and every keyword vector in keywordVecs. For each
// skeleton node the maximum keyword cosine is taken as the node's score;
// nodes whose max cosine clears keywordMatchThreshold are returned as
// scoredTargets, sorted by cosine descending.
func (m *semanticMounter) matchByKeywords(fragVec []float64, docCategory string) []scoredTarget {
	if len(fragVec) == 0 {
		return nil
	}

	type nodeScore struct {
		id     string
		maxCos float64
		bestKw string // keyword that achieved maxCos
	}
	var ranked []nodeScore

	for id, kwVecs := range m.keywordVecs {
		best := 0.0
		bestKw := ""
		if m.docCatalogByID[id] != nil && docCategory != "" {
			if !slices.Contains(m.docCatalogByID[id], docCategory) {
				//log.Printf("[排除][id]: %s [docCategory]: %s)", id, docCategory)
				continue
			}
		}

		for kw, vec := range kwVecs {
			c := cosineSimilarity(fragVec, vec)
			if c > best {
				best = c
				bestKw = kw
			}
		}
		if best >= m.keywordMatchThreshold {
			ranked = append(ranked, nodeScore{id: id, maxCos: best, bestKw: bestKw})
		}
	}
	if len(ranked) == 0 {
		return nil
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		return ranked[i].maxCos > ranked[j].maxCos
	})

	out := make([]scoredTarget, len(ranked))
	for i, ns := range ranked {
		mt := "partial"
		if ns.maxCos >= m.directThreshold {
			mt = "direct"
		}
		out[i] = scoredTarget{
			id:      ns.id,
			kwScore: ns.maxCos, // keyword-only path: reranker not used, carry cosine as rrScore
			rrScore: 0,
			mt:      mt,
			src:     "semantic:keyword",
		}
	}
	return out
}

// cosineSimilarity returns the cosine similarity between two vectors.
// Returns 0 when either vector is empty.
func cosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// scoredTarget is an intermediate mount decision carrying the scores
// refine needs for specificity / heading-context disambiguation.
type scoredTarget struct {
	id      string
	kwScore float64 // keyword vector cosine similarity (direct/partial gating)
	rrScore float64 // reranker score min-max normalized to [0,1]
	mt      string  // "direct" | "partial"
	src     string  // match source label
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
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].rrScore > kept[j].rrScore })

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
		top := kept[0].rrScore
		tie := make(map[string]bool)
		for _, k := range kept {
			if top-k.rrScore <= m.mutexBand {
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
		out = append(out, mountTarget{SkeletonID: k.id, MountType: k.mt, MatchSource: k.src, kwScore: k.kwScore, rrScore: k.rrScore})
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
