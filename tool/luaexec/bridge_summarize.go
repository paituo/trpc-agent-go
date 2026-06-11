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
	"math"
	"sort"
	"strings"
	"sync"

	textrank "github.com/DavidBelicza/TextRank/v2"
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

// tfidfSentence 记录句子及其 TF-IDF 得分和原始索引。
type tfidfSentence struct {
	index   int
	score   float64
	sentence string
}

// computeTFIDFScores 计算每个句子的 TF-IDF 得分。
// 将每个句子视为一个"文档"，所有句子构成语料库，
// 对每个词计算 TF-IDF 权重，句子得分为其所有词的 TF-IDF 之和。
func computeTFIDFScores(sentences []string, tokenized [][]string) []tfidfSentence {
	N := float64(len(tokenized))

	// 统计每个词出现在多少个句子（文档）中。
	df := make(map[string]int)
	for _, tokens := range tokenized {
		seen := make(map[string]bool)
		for _, t := range tokens {
			if !seen[t] {
				df[t]++
				seen[t] = true
			}
		}
	}

	results := make([]tfidfSentence, 0, len(tokenized))
	for i, tokens := range tokenized {
		if len(tokens) == 0 {
			results = append(results, tfidfSentence{index: i, score: 0, sentence: sentences[i]})
			continue
		}

		// 计算词频 TF。
		tf := make(map[string]float64)
		for _, t := range tokens {
			tf[t]++
		}
		for t := range tf {
			tf[t] /= float64(len(tokens))
		}

		// 句子得分 = 所有用词的 TF-IDF 之和。
		var score float64
		for t, freq := range tf {
			idf := math.Log(N / float64(df[t]))
			score += freq * idf
		}

		results = append(results, tfidfSentence{index: i, score: score, sentence: sentences[i]})
	}

	return results
}

// extractTFIDFSummary 使用 TF-IDF + gse 生成抽取式摘要。
// 将文本分割为句子后，每个句子视为一个文档，计算 TF-IDF 得分，
// 选取得分最高的 N 个句子按原文顺序拼接作为摘要。
func extractTFIDFSummary(text string, sentenceCount int) string {
	sentences := splitChineseSentences(text)
	if len(sentences) < 3 {
		return truncateText(text, 200)
	}

	if err := initGSESegmenter(); err != nil {
		return truncateText(text, 200)
	}

	// 对每个句子用 gse 分词。
	var tokenized [][]string
	var validSentences []string
	for _, s := range sentences {
		segments := gseSegmenter.Cut(s, true)
		// 过滤停用词和单字符词。
		var filtered []string
		for _, seg := range segments {
			seg = strings.TrimSpace(seg)
			if len(seg) <= 1 {
				continue
			}
			filtered = append(filtered, seg)
		}
		if len(filtered) == 0 {
			continue
		}
		tokenized = append(tokenized, filtered)
		validSentences = append(validSentences, s)
	}

	if len(tokenized) < 3 {
		return truncateText(text, 200)
	}

	// 计算 TF-IDF 得分。
	scored := computeTFIDFScores(validSentences, tokenized)

	// 按得分降序排序，取得分最高的 sentenceCount 个。
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	topN := sentenceCount
	if topN > len(scored) {
		topN = len(scored)
	}

	// 收集选中句子的原始索引，按原文顺序排列。
	var selected []int
	for i := 0; i < topN; i++ {
		selected = append(selected, scored[i].index)
	}
	sort.Ints(selected)

	var resultParts []string
	for _, idx := range selected {
		if idx >= 0 && idx < len(validSentences) {
			resultParts = append(resultParts, validSentences[idx])
		}
	}

	if len(resultParts) == 0 {
		return truncateText(text, 200)
	}

	return strings.Join(resultParts, "。") + "。"
}

// keywordScore 记录关键词及其 TF-IDF 得分。
type keywordScore struct {
	word  string
	score float64
}

// extractKeywords 使用 TF-IDF + gse 提取关键词。
// 将文本分割为句子后，每个句子视为一个文档，计算全局 TF-IDF，
// 返回得分最高的 count 个关键词。
func extractKeywords(text string, count int) []string {
	sentences := splitChineseSentences(text)
	if len(sentences) < 1 {
		return nil
	}

	if err := initGSESegmenter(); err != nil {
		return nil
	}

	// 对每个句子用 gse 分词，过滤停用词和单字符词。
	var allTokenized [][]string
	for _, s := range sentences {
		segments := gseSegmenter.Cut(s, true)
		var filtered []string
		for _, seg := range segments {
			seg = strings.TrimSpace(seg)
			if len(seg) <= 1 {
				continue
			}
			filtered = append(filtered, seg)
		}
		allTokenized = append(allTokenized, filtered)
	}

	N := float64(len(allTokenized))

	// 统计文档频率。
	df := make(map[string]int)
	for _, tokens := range allTokenized {
		seen := make(map[string]bool)
		for _, t := range tokens {
			if !seen[t] {
				df[t]++
				seen[t] = true
			}
		}
	}

	// 统计全局词频。
	totalWords := 0
	globalTF := make(map[string]int)
	for _, tokens := range allTokenized {
		for _, t := range tokens {
			globalTF[t]++
			totalWords++
		}
	}

	if totalWords == 0 {
		return nil
	}

	// 计算每个词的 TF-IDF 得分。
	var keywords []keywordScore
	for word, freq := range globalTF {
		tf := float64(freq) / float64(totalWords)
		idf := math.Log(N / float64(df[word]))
		keywords = append(keywords, keywordScore{word: word, score: tf * idf})
	}

	// 按得分降序排序。
	sort.SliceStable(keywords, func(i, j int) bool {
		return keywords[i].score > keywords[j].score
	})

	if count > len(keywords) {
		count = len(keywords)
	}

	result := make([]string, 0, count)
	for i := 0; i < count; i++ {
		result = append(result, keywords[i].word)
	}

	return result
}

// registerSummarizeBridge 注册 summarize 模块到 Lua VM。
func registerSummarizeBridge(L *lua.LState) {
	mod := L.NewTable()
	L.SetField(mod, "textrank", L.NewFunction(bridgeSummarizeTextRank))
	L.SetField(mod, "tfidf", L.NewFunction(bridgeSummarizeTFIDF))
	L.SetField(mod, "keywords", L.NewFunction(bridgeSummarizeKeywords))
	L.SetGlobal("summarize", mod)
}

// bridgeSummarizeTFIDF 实现 summarize.tfidf(text, sentence_count)。
// 使用 TF-IDF + gse 生成抽取式摘要，返回摘要字符串。
func bridgeSummarizeTFIDF(L *lua.LState) int {
	text := L.CheckString(1)
	sentenceCount := L.OptInt(2, 3)

	if sentenceCount < 1 {
		sentenceCount = 1
	}

	result := extractTFIDFSummary(text, sentenceCount)
	L.Push(lua.LString(result))
	return 1
}

// bridgeSummarizeKeywords 实现 summarize.keywords(text, count)。
// 使用 TF-IDF + gse 提取关键词，返回关键词列表。
func bridgeSummarizeKeywords(L *lua.LState) int {
	text := L.CheckString(1)
	count := L.OptInt(2, 10)

	if count < 1 {
		count = 1
	}

	keywords := extractKeywords(text, count)

	// 返回 Lua 表（即使 keywords 为 nil 也返回空表）。
	tbl := L.NewTable()
	for _, kw := range keywords {
		L.RawSetInt(tbl, tbl.Len()+1, lua.LString(kw))
	}

	L.Push(tbl)
	return 1
}
