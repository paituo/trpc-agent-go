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
	"math"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
)

// vocab is a fixed business-term vocabulary used to build deterministic,
// network-free embeddings for the semantic mounter tests.
var vocab = []string{"杆塔", "基础", "导地线", "接地", "绝缘子", "跨越", "辅助", "概况", "其他费用"}

// oneHotEmbedder encodes text as a one-hot vector over vocab (presence of a
// business term -> 1). It implements embedder.Embedder without any network.
type oneHotEmbedder struct {
	vocab []string
}

func (e *oneHotEmbedder) vector(text string) []float64 {
	v := make([]float64, len(e.vocab))
	for i, tok := range e.vocab {
		if strings.Contains(text, tok) {
			v[i] = 1
		}
	}
	return v
}

func (e *oneHotEmbedder) GetEmbedding(_ context.Context, text string) ([]float64, error) {
	return e.vector(text), nil
}

func (e *oneHotEmbedder) GetEmbeddingWithUsage(_ context.Context, text string) ([]float64, map[string]any, error) {
	return e.vector(text), nil, nil
}

func (e *oneHotEmbedder) GetDimensions() int { return len(e.vocab) }

func cosineOneHot(vocab []string, a, b string) float64 {
	var dot, na, nb float64
	for i := range vocab {
		av, bv := 0.0, 0.0
		if strings.Contains(a, vocab[i]) {
			av = 1
		}
		if strings.Contains(b, vocab[i]) {
			bv = 1
		}
		dot += av * bv
		na += av * av
		nb += bv * bv
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// oneHotReranker reranks candidates by their one-hot cosine to the query.
// It implements reranker.Reranker without any network.
type oneHotReranker struct {
	vocab []string
}

func (r *oneHotReranker) Rerank(_ context.Context, q *reranker.Query, results []*reranker.Result) ([]*reranker.Result, error) {
	type scored struct {
		res   *reranker.Result
		score float64
	}
	scoredList := make([]scored, 0, len(results))
	for _, res := range results {
		s := 0.0
		if res.Document != nil {
			s = cosineOneHot(r.vocab, q.FinalQuery, res.Document.Content)
		}
		scoredList = append(scoredList, scored{res, s})
	}
	sort.Slice(scoredList, func(i, j int) bool {
		return scoredList[i].score > scoredList[j].score
	})
	out := make([]*reranker.Result, len(scoredList))
	for i, s := range scoredList {
		cp := *s.res
		cp.Score = s.score
		out[i] = &cp
	}
	return out, nil
}

func testSkeletons() []SkeletonNode {
	return []SkeletonNode{
		{ID: "EngineeringOverview", Name: "EngineeringOverview", Content: "概况 线路路径 电压等级"},
		{ID: "FoundationDesign", Name: "FoundationDesign", Content: "基础 挖孔基础 灌注桩 混凝土"},
		{ID: "TowerDesign", Name: "TowerDesign", Content: "杆塔 铁塔 杆塔型式 塔重 根开"},
		{ID: "ConductorAndGroundWire", Name: "ConductorAndGroundWire", Content: "导地线 导线选型 地线选型 OPGW"},
	}
}

func TestSemanticMounter_SingleNode(t *testing.T) {
	emb := &oneHotEmbedder{vocab: vocab}
	rk := &oneHotReranker{vocab: vocab}
	m := newSemanticMounter(emb, rk, defaultUnknownSkeletonID, testSkeletons(), 0.30, 0.30)

	cases := []struct {
		name     string
		fragText string
		want     string
	}{
		{"tower", "1.1 杆塔型式 本工程采用角钢塔，呼高与根开见下表", "TowerDesign"},
		{"foundation", "2.3 基础型式 采用挖孔基础与灌注桩", "FoundationDesign"},
		{"conductor", "3 导地线选型 导线型号JLHA1/G1A，地线采用OPGW", "ConductorAndGroundWire"},
		{"overview", "前言 工程概况 线路路径长度与电压等级", "EngineeringOverview"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			targets := m.Mount(context.Background(), fragment{name: "章节", content: c.fragText})
			require.Len(t, targets, 1, "should mount to exactly one skeleton node")
			require.Equal(t, c.want, targets[0].SkeletonID)
			require.Equal(t, "direct", targets[0].MountType)
			require.Equal(t, "semantic:keyword+rerank", targets[0].MatchSource)
		})
	}
}

func TestSemanticMounter_MultiNode(t *testing.T) {
	emb := &oneHotEmbedder{vocab: vocab}
	rk := &oneHotReranker{vocab: vocab}
	m := newSemanticMounter(emb, rk, defaultUnknownSkeletonID, testSkeletons(), 0.30, 0.30)

	// 同时含「杆塔」与「基础」业务概念 -> 应挂接到两个骨架节点。
	text := "4 杆塔与基础 本工程杆塔采用角钢塔，基础采用挖孔基础"
	targets := m.Mount(context.Background(), fragment{name: "章节", content: text})

	ids := map[string]string{}
	for _, tg := range targets {
		ids[tg.SkeletonID] = tg.MountType
	}
	require.Contains(t, ids, "TowerDesign", "should mount to TowerDesign")
	require.Contains(t, ids, "FoundationDesign", "should mount to FoundationDesign")
	// 两者业务概念并列（余弦分相同），各按自身分判定为 direct。
	require.Equal(t, "direct", ids["TowerDesign"])
	require.Equal(t, "direct", ids["FoundationDesign"])
}

func TestSemanticMounter_UnmatchedToUnknown(t *testing.T) {
	emb := &oneHotEmbedder{vocab: vocab}
	rk := &oneHotReranker{vocab: vocab}
	m := newSemanticMounter(emb, rk, defaultUnknownSkeletonID, testSkeletons(), 0.30, 0.30)

	// 不含任何业务词表词 -> 无法匹配 -> 回退 unknown 节点。
	text := "气象条件 最大设计风速与覆冰厚度统计如下"
	targets := m.Mount(context.Background(), fragment{name: "气象", content: text})
	require.Len(t, targets, 1)
	require.Equal(t, defaultUnknownSkeletonID, targets[0].SkeletonID)
	require.Equal(t, "unknown", targets[0].MountType)
}

func TestSemanticMounter_RerankerUnavailable(t *testing.T) {
	emb := &oneHotEmbedder{vocab: vocab}
	// 传 nil reranker 走余弦兜底路径：Mount 内会触发 rerank 错误 -> 退回
	// acceptByCosine。这里直接用 nil 调 newSemanticMounter 验证兜底仍可命中。
	m := newSemanticMounter(emb, nil, defaultUnknownSkeletonID, testSkeletons(), 0.30, 0.30)

	text := "1.1 杆塔型式 本工程采用角钢塔"
	targets := m.Mount(context.Background(), fragment{name: "章节", content: text})
	require.Len(t, targets, 1)
	require.Equal(t, "TowerDesign", targets[0].SkeletonID)
	require.Equal(t, "direct", targets[0].MountType)
	require.Equal(t, "semantic:keyword", targets[0].MatchSource)
}

func TestSource_MountFragmentDeterministicFallback(t *testing.T) {
	// 未配置 embedder/reranker 时，mountFragment 退回确定性标题匹配。
	// 无骨架：不挂接任何节点（与 ReadGraph「无 skeleton 无 MOUNTS_TO」一致）。
	src := newTestSource(nil, nil)
	targets := src.mountFragment(context.Background(), fragment{headingPath: "X > Y"})
	require.Empty(t, targets)

	// 有骨架但不匹配：回退到 unknown 节点，match_source 为 deterministic。
	src2 := newTestSource([]SkeletonNode{
		{ID: "Root", Name: "Root"},
		{ID: "Child", Name: "Child", ParentID: "Root"},
	}, nil)
	targets2 := src2.mountFragment(context.Background(), fragment{headingPath: "X > Y"})
	require.Len(t, targets2, 1)
	require.Equal(t, defaultUnknownSkeletonID, targets2[0].SkeletonID)
	require.Equal(t, "unknown", targets2[0].MountType)
	require.Equal(t, "deterministic", targets2[0].MatchSource)
}

func TestSemanticMounter_AncestorSuppression(t *testing.T) {
	emb := &oneHotEmbedder{vocab: vocab}
	rk := &oneHotReranker{vocab: vocab}
	// 根节点 Proj，子节点 Tower（ParentID=Proj）。fragment 同时含
	// 「杆塔」→ 与两者都向量匹配；精炼应只保留更具体的 Tower，
	// 丢弃宽泛的根节点 Proj。
	sk := []SkeletonNode{
		{ID: "Proj", Name: "Proj", Content: "杆塔 整体工程", ParentID: ""},
		{ID: "Tower", Name: "Tower", Content: "杆塔 铁塔 杆塔型式", ParentID: "Proj"},
	}
	m := newSemanticMounter(emb, rk, defaultUnknownSkeletonID, sk, 0.30, 0.30)
	targets := m.Mount(context.Background(), fragment{
		name:        "章节",
		content:     "杆塔型式 本工程杆塔采用角钢塔",
		headingPath: "1 工程概况 > 杆塔",
	})
	require.Len(t, targets, 1, "ancestor should be suppressed, keep only the child")
	require.Equal(t, "Tower", targets[0].SkeletonID)
}

func TestSemanticMounter_HeadingContextMutex(t *testing.T) {
	emb := &oneHotEmbedder{vocab: vocab}
	rk := &oneHotReranker{vocab: vocab}
	// 两个兄弟节点 CTower / DCond，fragment 正文只含共享词「跨越」
	// （向量上与两者都匹配），但父章节标题是「杆塔设计」，只有 CTower
	// 的业务词命中标题 → 应只保留 CTower，丢弃 DCond。
	sk := []SkeletonNode{
		{ID: "CTower", Name: "CTower", Content: "杆塔 铁塔 杆塔型式 跨越", ParentID: ""},
		{ID: "DCond", Name: "DCond", Content: "导地线 导线选型 跨越", ParentID: ""},
	}
	m := newSemanticMounter(emb, rk, defaultUnknownSkeletonID, sk, 0.30, 0.30)
	targets := m.Mount(context.Background(), fragment{
		name:        "章节",
		content:     "本段论述跨越与导地线相关内容",
		headingPath: "12 杆塔设计 > 杆塔型式",
	})
	require.Len(t, targets, 1, "heading context should disambiguate the near-tie")
	require.Equal(t, "CTower", targets[0].SkeletonID)
}

var _ embedder.Embedder = (*oneHotEmbedder)(nil)
var _ reranker.Reranker = (*oneHotReranker)(nil)
