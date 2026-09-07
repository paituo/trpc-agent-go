//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package fragment

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// defaultDocClassifyPrompt is the default prompt used for LLM-based document
// classification. It instructs the model to classify a list of document
// paths into predefined categories. Callers can override it via
// WithDocClassifyPrompt.
//
// The placeholder "[在此处粘贴你的文件列表]" is replaced with the actual
// docPaths at runtime.
const defaultDocClassifyPrompt = `## 角色设定

你是工程设计资料分类专家。请仅根据文档的路径和文件名，将全部文档归入以下五个类别之一：

1. 设计说明书
2. 设备材料清册
3. 专题报告
4. 设计图纸
5. 工程依据及支撑性文件

不得读取或推测文档正文，不得输出上述五类以外的类别。

## 分类原则

1. 每个文档只能归入一个类别。
2. 优先判断文件名表达的文档主体，文件路径仅作为辅助。
3. 不得仅凭单个关键词直接分类，应结合文件名整体语义判断。
4. 当文件名与路径表达不一致时，以文件名为准。
5. “设计说明书”和“设备材料清册”各只能归入一个文档。
6. 未明显命中规则的文档，也必须根据名称和路径选择最可能的类别。

## 分类规则

### 1. 设计说明书

文件名整体表示工程设计说明文件，例如：

* 初步设计说明书
* 初设说明书
* 施工图设计说明书
* 施设说明书
* 设计说明书
* 工程设计说明书

“设备说明书”“产品说明书”“使用说明”“编制说明”等，不属于设计说明书。

若存在多个候选文件，选择名称最完整、最能代表工程总体设计说明的一个。

### 2. 设备材料清册

文件名整体表示工程设备或材料汇总清单，例如：

* 设备材料清册
* 主要设备材料清册
* 主要设备材料表
* 设备清册
* 材料清册
* 设备材料表
* 主要材料表

“材料价格表”“材料报审表”“材料检验报告”“材料计算书”等，不属于设备材料清册。

若存在多个候选文件，选择最能代表工程总体设备材料汇总的一个。

### 3. 专题报告

文件名或路径表示针对某一专业事项形成的专项研究、勘察或评价文件，例如：

* 专题报告
* 专项报告
* 地勘报告
* 水保报告
* 环评报告
* 压覆矿报告
* 文物调查报告
* 洪评报告
* 林勘报告
* 机械化施工报告

### 4. 设计图纸

文件名整体表示图纸、图册或设计图，例如：

* 总图
* 路径图
* 平断面图
* 杆塔明细图
* 杆塔一览图
* 基础施工图
* 基础一览图
* 接地施工图
* 附件图
* 组装图
* 图纸目录
* 施工图
* 设计图

不得仅因文件名中出现单独的“图”字就直接归类，应判断其是否确实表示设计图纸。

### 5. 工程依据及支撑性文件

文件名整体表示工程立项、审批、审查、协议或外部支撑材料，例如：

* 批复
* 核准
* 可研
* 协议
* 会议纪要
* 审查意见
* 收资资料
* 依据文件
* 支撑材料
* 评审意见
* 复函
* 通知

## 无法判定

当文档确实无法根据名称与路径归入上述任何一类时，标注“待人工复核”，交由人工处理。

## 冲突处理

当一个文档同时符合多个类别时，按以下顺序判断：

1. 先判断文件名整体表达的文档主体。
2. 再参考直接上级目录。
3. 完整文档名称优先于普通关键词。
4. 专项研究类文件优先归入“专题报告”。
5. 图纸或图册类文件优先归入“设计图纸”。
6. 批复、协议、审查意见等优先归入“工程依据及支撑性文件”。

输出格式（只输出这部分内容）：
文件名（或路径） -> 归入的类别

待处理数据如下：
[在此处粘贴你的文件列表]`

// docClassifyPlaceholder is the placeholder in the prompt that will be
// replaced with the actual file list.
const docClassifyPlaceholder = "[在此处粘贴你的文件列表]"

// categoryLineRegexp matches a single classification result line, e.g.
//
//	/path/to/file.md -> 设计说明书
var categoryLineRegexp = regexp.MustCompile(`^(.+?)\s*->\s*(.+)$`)

// classifyDocPaths sends the docPaths (as relative paths based on sourceDir)
// to the LLM for batch classification and returns a map of docPath → category.
// On error it logs a warning and returns an empty map so the caller can
// degrade gracefully.
func (s *Source) classifyDocPaths(ctx context.Context) (map[string]string, error) {
	if s.llm == nil {
		return nil, nil
	}

	// Build display paths: use sourceDir-relative paths when sourceDir is set,
	// otherwise fall back to the original docPaths.
	displayPaths := make([]string, len(s.docPaths))
	for i, p := range s.docPaths {
		if s.sourceDir != "" {
			if rel, err := filepath.Rel(s.sourceDir, p); err == nil {
				displayPaths[i] = rel
			} else {
				displayPaths[i] = p
			}
		} else {
			displayPaths[i] = p
		}
	}

	prompt := s.resolveClassifyPrompt()
	userText := strings.Join(displayPaths, "\n")
	// Replace placeholder; if not found, append the file list.
	if strings.Contains(prompt, docClassifyPlaceholder) {
		prompt = strings.ReplaceAll(prompt, docClassifyPlaceholder, userText)
	} else {
		prompt = prompt + "\n\n" + userText
	}
	s.logf(prompt)
	messages := []model.Message{
		model.NewSystemMessage(prompt),
		model.NewUserMessage("请对以上文件列表进行分类。"),
	}

	ch, err := s.llm.GenerateContent(ctx, &model.Request{
		Messages:         messages,
		GenerationConfig: model.GenerationConfig{Stream: true},
	})
	if err != nil {
		s.logf("classifyDocPaths: LLM call failed: %v", err)
		return nil, fmt.Errorf("classifyDocPaths: LLM call failed: %w", err)
	}

	var result strings.Builder
	for resp := range ch {
		if resp.Error != nil {
			s.logf("classifyDocPaths: LLM response error: %v", resp.Error)
			return nil, fmt.Errorf("classifyDocPaths: response error: %v", resp.Error)
		}
		for _, choice := range resp.Choices {
			if choice.Message.Content != "" {
				fmt.Print(choice.Message.Content)
				result.WriteString(choice.Message.Content)
			}
			if choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
				//result.WriteString(choice.Delta.Content)
			}
		}
	}

	// parseClassificationResults returns relative-path → category.
	// Map the keys back to the original docPaths so callers can look up
	// categories by the original path.
	raw := parseClassificationResults(result.String())
	catMap := make(map[string]string, len(raw))
	for i, dp := range displayPaths {
		if cat, ok := raw[dp]; ok {
			catMap[s.docPaths[i]] = cat
			s.logf(s.docPaths[i] + " -> " + cat)
		}
	}
	return catMap, nil
}

// resolveClassifyPrompt returns the configured prompt or the default.
func (s *Source) resolveClassifyPrompt() string {
	if s.docClassifyPrompt != "" {
		return s.docClassifyPrompt
	}
	return defaultDocClassifyPrompt
}

// parseClassificationResults parses the LLM classification output and returns
// a map of docPath → category. Lines that don't match the expected format
// are silently ignored.
func parseClassificationResults(output string) map[string]string {
	results := make(map[string]string)
	if output == "" {
		return results
	}

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := categoryLineRegexp.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		docPath := strings.TrimSpace(m[1])
		category := strings.TrimSpace(m[2])
		if docPath != "" && category != "" {
			results[docPath] = category
		}
	}
	return results
}
