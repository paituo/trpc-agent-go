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
	"sort"
	"strings"
	"sync"

	"github.com/DavidBelicza/TextRank/v2"
	"github.com/DavidBelicza/TextRank/v2/convert"
	"github.com/DavidBelicza/TextRank/v2/parse"
	"github.com/go-ego/gse"
	lua "github.com/yuin/gopher-lua"
)

// gseSegmenter 是模块级 gse 分词器单例，lazy init。
var (
	gseSegmenter *gse.Segmenter
	gseOnce      sync.Once
	gseInitErr   error
)

// initGSESegmenter 初始化 gse 分词器（纯 Go 模式）。
func initGSESegmenter() error {
	gseOnce.Do(func() {
		var seg gse.Segmenter
		gseInitErr = seg.LoadDict()
		if gseInitErr != nil {
			return
		}
		gseSegmenter = &seg
	})
	return gseInitErr
}

// chineseRule 实现 parse.Rule 接口，支持中文标点作为句子分隔符。
var _ parse.Rule = (*chineseRule)(nil)

type chineseRule struct{}

func (r *chineseRule) IsWordSeparator(rn rune) bool {
	chr := string(rn)
	wordSeps := []string{" ", ",", "，", "、", "'", "'", "\"", ")", "(", "[", "]", "{", "}", "\"", ";", "；", "\n", ">", "<", "%", "@", "&", "=", "#", "：", "："}
	for _, s := range wordSeps {
		if chr == s {
			return true
		}
	}
	return r.IsSentenceSeparator(rn)
}

func (r *chineseRule) IsSentenceSeparator(rn rune) bool {
	chr := string(rn)
	sentSeps := []string{"!", ".", "?", "！", "。", "？"}
	for _, s := range sentSeps {
		if chr == s {
			return true
		}
	}
	return false
}

// splitChineseSentences 对中文文本进行简单句子分割。
// 使用 。，；\n 作为分隔符，过滤空句子和过短句子（<5字符）。
func splitChineseSentences(text string) []string {
	// 先用各种分隔符统一替换
	replaced := text
	seps := []string{"。", "；", "；", "\n"}
	for _, sep := range seps {
		replaced = strings.ReplaceAll(replaced, sep, "\x00")
	}

	parts := strings.Split(replaced, "\x00")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len([]rune(p)) >= 5 {
			result = append(result, p)
		}
	}
	return result
}

// truncateText 截断文本到前 maxRunes 个字符。
func truncateText(text string, maxRunes int) string {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes])
}

// extractSummary 使用 TextRank + gse 生成抽取式摘要。
func extractSummary(text string, sentenceCount int) string {
	// 如果文本太短（少于3个句子），直接返回原文截断。
	sentences := splitChineseSentences(text)
	if len(sentences) < 3 {
		return truncateText(text, 200)
	}

	// 初始化 gse 分词器。
	if err := initGSESegmenter(); err != nil {
		// gse 初始化失败，回退到简单截断。
		return truncateText(text, 200)
	}

	// 对每个句子用 gse 分词，然后用空格连接分词结果，
	// 使 TextRank 的默认 Rule 可以正确识别单词边界。
	var tokenizedSentences []string
	for _, s := range sentences {
		segments := gseSegmenter.Cut(s, true)
		tokenized := strings.Join(segments, " ")
		if tokenized == "" {
			continue
		}
		tokenizedSentences = append(tokenizedSentences, tokenized)
	}

	if len(tokenizedSentences) < 3 {
		return truncateText(text, 200)
	}

	// 将分词后的句子用换行符连接，传给 TextRank。
	tokenizedText := strings.Join(tokenizedSentences, "\n")

	// 创建 TextRank 实例并计算排名。
	tr := textrank.NewTextRank()
	rule := &chineseRule{}
	lang := convert.NewLanguage()
	lang.SetActiveLanguage("zh")

	tr.Populate(tokenizedText, lang, rule)
	tr.Ranking(textrank.NewDefaultAlgorithm())

	// 获取权重最高的 sentenceCount 个句子。
	ranked := textrank.FindSentencesByRelationWeight(tr, sentenceCount)
	if len(ranked) == 0 {
		return truncateText(text, 200)
	}

	// ranked 中的 Value 是分词后的文本，需要映射回原始句子。
	// 由于 TextRank 内部按 ID 排列句子，我们可以用 SentenceMap 中的 ID
	// 来对应 tokenizedSentences 的索引，从而找到原始句子。
	rankData := tr.GetRankData()

	// 构建 ID -> 原始句子的映射。
	// TextRank 的 SentenceMap 中 ID 对应 tokenizedSentences 的索引。
	type idSentence struct {
		id       int
		original string
	}
	var idSentences []idSentence
	for id := range rankData.SentenceMap {
		if id >= 0 && id < len(sentences) {
			idSentences = append(idSentences, idSentence{id: id, original: sentences[id]})
		}
	}

	// 从 ranked 结果中收集原始句子，按 ID 排序以保持原文顺序。
	var selectedIDs []int
	for _, s := range ranked {
		selectedIDs = append(selectedIDs, s.ID)
	}
	sort.Ints(selectedIDs)

	var resultParts []string
	for _, id := range selectedIDs {
		if id >= 0 && id < len(sentences) {
			resultParts = append(resultParts, sentences[id])
		}
	}

	if len(resultParts) == 0 {
		return truncateText(text, 200)
	}

	return strings.Join(resultParts, "。") + "。"
}

// registerSummarizeBridge 注册 summarize 模块到 Lua VM。
func registerSummarizeBridge(L *lua.LState) {
	mod := L.NewTable()
	L.SetField(mod, "textrank", L.NewFunction(bridgeSummarizeTextRank))
	L.SetGlobal("summarize", mod)
}

// bridgeSummarizeTextRank 实现 summarize.textrank(text, sentence_count)。
// 使用 TextRank + gse 生成抽取式摘要，返回摘要字符串。
func bridgeSummarizeTextRank(L *lua.LState) int {
	text := L.CheckString(1)
	sentenceCount := L.OptInt(2, 3)

	if sentenceCount < 1 {
		sentenceCount = 1
	}

	result := extractSummary(text, sentenceCount)
	L.Push(lua.LString(result))
	return 1
}
