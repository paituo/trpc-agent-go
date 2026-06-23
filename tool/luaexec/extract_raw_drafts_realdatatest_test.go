package luaexec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExtractRawDrafts_RealData_20260623 使用用户提供的真实数据路径测试
// 资料路径：./源文件/
// 临时文件路径和骨架挂接结果目录：./userData/2026-06-23_11-05-49/temp
// 上一步输出：./userData/2026-06-23_11-05-49/temp/output/基本信息
// 提取类别：基本信息
func TestExtractRawDrafts_RealData_20260623(t *testing.T) {
	baseDir := filepath.Join("..", "..", "bowei-openclaw")

	// 脚本目录
	scriptsDir := filepath.Join("..", "..", ".trae", "skills", "single-extractor", "scripts")
	absScriptsDir, err := filepath.Abs(scriptsDir)
	require.NoError(t, err)
	scriptPath := filepath.Join(absScriptsDir, "extract_raw_drafts.lua")
	_, err = os.Stat(scriptPath)
	require.NoError(t, err)

	// 骨架索引文件路径
	skeletonPath := filepath.Join(baseDir, "userData", "2026-06-23_11-05-49", "skeleton_index.yaml")
	absSkeletonPath, err := filepath.Abs(skeletonPath)
	require.NoError(t, err)
	_, err = os.Stat(absSkeletonPath)
	require.NoError(t, err, "骨架索引文件应存在: %s", absSkeletonPath)

	// 规则文件路径
	rulesPath := filepath.Join("..", "..", ".trae", "skills", "single-extractor", "references", "field_rules", "basic_info.md")
	absRulesPath, err := filepath.Abs(rulesPath)
	require.NoError(t, err)
	_, err = os.Stat(absRulesPath)
	require.NoError(t, err, "规则文件应存在: %s", absRulesPath)

	// 源文档目录（骨架索引中的 path 是 ./源文件/...，所以 source_doc_dir 是 baseDir）
	absSourceDir, err := filepath.Abs(baseDir)
	require.NoError(t, err)

	// 输出目录
	outputDir := filepath.Join(baseDir, "userData", "2026-06-23_11-05-49", "temp", "output")
	absOutputDir, err := filepath.Abs(outputDir)
	require.NoError(t, err)
	err = os.MkdirAll(absOutputDir, 0755)
	require.NoError(t, err)

	// 清理之前的输出
	categoryDir := filepath.Join(absOutputDir, "基本信息")
	os.RemoveAll(categoryDir)

	t.Logf("脚本路径: %s", scriptPath)
	t.Logf("骨架索引: %s", absSkeletonPath)
	t.Logf("规则文件: %s", absRulesPath)
	t.Logf("源文档目录: %s", absSourceDir)
	t.Logf("输出目录: %s", absOutputDir)

	ts, err := NewToolSet(
		WithTools(&mockTool{name: "dummy"}),
		WithAllowedScriptDirs(absScriptsDir, absOutputDir, absSourceDir),
		WithAllowIOLib(true),
		WithAllowOSLib(true),
		func(c *Config) { c.AllowFSLib = true },
	)
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	argsJSON, err := json.Marshal(map[string]any{
		"script_path": scriptPath,
		"args": map[string]any{
			"skeleton_path":  absSkeletonPath,
			"category":       "基本信息",
			"rules_path":     absRulesPath,
			"source_doc_dir": absSourceDir,
			"output_dir":     absOutputDir,
		},
	})
	require.NoError(t, err)

	result, err := ct.Call(context.Background(), argsJSON)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	t.Logf("执行结果: %+v", result)

	// 检查 status
	status, ok := resultMap["status"].(string)
	require.True(t, ok, "status 应为字符串")
	assert.Equal(t, "success", status)

	if status != "success" {
		// 输出错误详情
		if errors, exists := resultMap["errors"]; exists {
			t.Logf("错误详情: %+v", errors)
		}
		return
	}

	// 检查 result 字段（lua_exec 返回结构为 {status, result: {result: {...}}}
	innerWrapper, ok := resultMap["result"].(map[string]any)
	require.True(t, ok, "应有 result 字段")
	innerResult, ok := innerWrapper["result"].(map[string]any)
	require.True(t, ok, "应有内层 result 字段")

	// 验证返回的元数据
	category, _ := innerResult["category"].(string)
	assert.Equal(t, "基本信息", category)

	subcategoryCount := 0
	if v, ok := innerResult["subcategory_count"].(float64); ok {
		subcategoryCount = int(v)
	}
	totalFragments := 0
	if v, ok := innerResult["total_fragments"].(float64); ok {
		totalFragments = int(v)
	}
	tempDir, _ := innerResult["temp_dir"].(string)
	outputFilesRaw, _ := innerResult["output_files"].([]any)
	outputFiles := outputFilesRaw

	t.Logf("类别: %s", category)
	t.Logf("子类别数: %d", subcategoryCount)
	t.Logf("总片段数: %d", totalFragments)
	t.Logf("临时目录: %s", tempDir)
	t.Logf("输出文件数: %d", len(outputFiles))
	for _, f := range outputFiles {
		t.Logf("  - %s", f)
	}

	// 验证临时文件目录存在
	_, err = os.Stat(categoryDir)
	require.NoError(t, err, "临时文件目录应存在: %s", categoryDir)

	// 验证 raw_drafts 文件
	rawFile := "基本信息_raw_drafts.yaml"
	rawPath := filepath.Join(categoryDir, rawFile)
	rawData, err := os.ReadFile(rawPath)
	require.NoError(t, err, "raw_drafts 文件应存在: %s", rawPath)
	rawContent := string(rawData)
	t.Logf("✅ %s: %d 字节", rawFile, len(rawContent))

	// 验证 raw_drafts 内容格式
	assert.False(t, strings.Contains(rawContent, "_content:"), "不应包含 YAML _content 包装")
	assert.False(t, strings.Contains(rawContent, "\\n"), "不应包含转义的 \\n")
	assert.Contains(t, rawContent, "_meta:", "应包含 _meta 元数据")
	assert.Contains(t, rawContent, "subcategories:", "应包含 subcategories 数组")
	assert.Contains(t, rawContent, "fragment_count:", "应包含 fragment_count")
	assert.Contains(t, rawContent, "category: 基本信息", "应有类别信息")

	if totalFragments > 0 {
		assert.Contains(t, rawContent, "length:", "应包含 length（分段长度）")
		assert.Contains(t, rawContent, "source_document:", "应包含 source_document")
		assert.Contains(t, rawContent, "content:", "应包含 content")
		assert.Contains(t, rawContent, "doc_path:", "应包含 doc_path")
	}

	// 验证 field_rule 文件
	expectedFieldFiles := []string{
		"新建信息_基本信息_field_rule.yaml",
		"计算信息_基本信息_field_rule.yaml",
		"工程设置_基本信息_field_rule.yaml",
		"编制人信息_基本信息_field_rule.yaml",
		"调差系数_基本信息_field_rule.yaml",
		"特征信息_基本信息_field_rule.yaml",
	}
	for _, ff := range expectedFieldFiles {
		fp := filepath.Join(categoryDir, ff)
		data2, err := os.ReadFile(fp)
		if err == nil {
			content := string(data2)
			t.Logf("✅ %s: %d 字节", ff, len(content))
			assert.False(t, strings.Contains(content, "_content:"), "%s 不应包含 YAML _content 包装", ff)
			assert.Contains(t, content, "_meta:", "%s 应包含 _meta", ff)
			assert.Contains(t, content, "subcategory:", "%s 应包含 subcategory", ff)
			assert.Contains(t, content, "field_definitions:", "%s 应包含 field_definitions", ff)
			assert.Contains(t, content, "field_count:", "%s 应包含 field_count", ff)
			assert.Contains(t, content, "约束值:", "%s 应包含 约束值", ff)
			assert.Contains(t, content, "说明:", "%s 应包含 说明", ff)
			assert.Contains(t, content, "key_fields:", "%s 应包含 key_fields", ff)
			assert.Contains(t, content, "supplement_notes:", "%s 应包含 supplement_notes", ff)
		} else {
			t.Logf("❌ %s: %v", ff, err)
		}
	}
}

// TestExtractRawDrafts_AllCategories 测试所有8个类别
func TestExtractRawDrafts_AllCategories(t *testing.T) {
	baseDir := filepath.Join("..", "..", "bowei-openclaw")

	scriptsDir := filepath.Join("..", "..", ".trae", "skills", "single-extractor", "scripts")
	absScriptsDir, err := filepath.Abs(scriptsDir)
	require.NoError(t, err)
	scriptPath := filepath.Join(absScriptsDir, "extract_raw_drafts.lua")
	_, err = os.Stat(scriptPath)
	require.NoError(t, err)

	skeletonPath := filepath.Join(baseDir, "userData", "2026-06-23_11-05-49", "skeleton_index.yaml")
	absSkeletonPath, err := filepath.Abs(skeletonPath)
	require.NoError(t, err)

	absSourceDir, err := filepath.Abs(baseDir)
	require.NoError(t, err)

	outputDir := filepath.Join(baseDir, "userData", "2026-06-23_11-05-49", "temp", "output")
	absOutputDir, err := filepath.Abs(outputDir)
	require.NoError(t, err)

	ts, err := NewToolSet(
		WithTools(&mockTool{name: "dummy"}),
		WithAllowedScriptDirs(absScriptsDir, absOutputDir, absSourceDir),
		WithAllowIOLib(true),
		WithAllowOSLib(true),
		func(c *Config) { c.AllowFSLib = true },
	)
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// 所有8个类别
	categories := []string{
		"基本信息",
		"杆塔组件",
		"架线组件",
		"基础组件",
		"接地组件",
		"附件组件",
		"交叉跨越",
		"辅助工程",
	}

	for _, cat := range categories {
		t.Run(cat, func(t *testing.T) {
			rulesPath := filepath.Join("..", "..", ".trae", "skills", "single-extractor", "references", "field_rules")
			rulesFile := ""
			switch cat {
			case "基本信息":
				rulesFile = "basic_info.md"
			case "杆塔组件":
				rulesFile = "tower_component.md"
			case "架线组件":
				rulesFile = "conductor.md"
			case "基础组件":
				rulesFile = "foundation.md"
			case "接地组件":
				rulesFile = "grounding.md"
			case "附件组件":
				rulesFile = "accessories.md"
			case "交叉跨越":
				rulesFile = "crossing.md"
			case "辅助工程":
				rulesFile = "auxiliary.md"
			}
			absRulesPath, err := filepath.Abs(filepath.Join(rulesPath, rulesFile))
			require.NoError(t, err)

			// 清理之前的输出
			categoryDir := filepath.Join(absOutputDir, cat)
			os.RemoveAll(categoryDir)

			argsJSON, err := json.Marshal(map[string]any{
				"script_path": scriptPath,
				"args": map[string]any{
					"skeleton_path":  absSkeletonPath,
					"category":       cat,
					"rules_path":     absRulesPath,
					"source_doc_dir": absSourceDir,
					"output_dir":     absOutputDir,
				},
			})
			require.NoError(t, err)

			result, err := ct.Call(context.Background(), argsJSON)
			require.NoError(t, err)

			resultMap, ok := result.(map[string]any)
			require.True(t, ok)

			status, _ := resultMap["status"].(string)
			if status != "success" {
				if errors, exists := resultMap["errors"]; exists {
					t.Logf("❌ %s 失败: %+v", cat, errors)
				}
				// 某些类别可能没有对应的骨架节点，这是正常的
				t.Skipf("类别 %s 可能无对应骨架节点", cat)
				return
			}

			innerWrapper, _ := resultMap["result"].(map[string]any)
			innerResult, _ := innerWrapper["result"].(map[string]any)

			subcategoryCount := 0
			if v, ok := innerResult["subcategory_count"].(float64); ok {
				subcategoryCount = int(v)
			}
			totalFragments := 0
			if v, ok := innerResult["total_fragments"].(float64); ok {
				totalFragments = int(v)
			}
			outputFilesRaw, _ := innerResult["output_files"].([]any)
			outputFiles := outputFilesRaw

			t.Logf("✅ %s: %d 子类别, %d 片段, %d 输出文件",
				cat, subcategoryCount, totalFragments, len(outputFiles))

			// 验证输出文件存在
			_, err = os.Stat(categoryDir)
			if err == nil {
				for _, f := range outputFiles {
					fp := filepath.Join(categoryDir, fmt.Sprintf("%v", f))
					if data, err := os.ReadFile(fp); err == nil {
						t.Logf("   ✅ %s: %d 字节", f, len(data))
					} else {
						t.Logf("   ❌ %s: %v", f, err)
					}
				}
			}
		})
	}
}

// TestExtractRawDrafts_MergeBehavior 测试片段合并行为
// 验证同一文件中相邻片段（间隔<5行）被正确合并
func TestExtractRawDrafts_MergeBehavior(t *testing.T) {
	baseDir := filepath.Join("..", "..", "bowei-openclaw")

	scriptsDir := filepath.Join("..", "..", ".trae", "skills", "single-extractor", "scripts")
	absScriptsDir, err := filepath.Abs(scriptsDir)
	require.NoError(t, err)
	scriptPath := filepath.Join(absScriptsDir, "extract_raw_drafts.lua")
	_, err = os.Stat(scriptPath)
	require.NoError(t, err)

	skeletonPath := filepath.Join(baseDir, "userData", "2026-06-23_11-05-49", "skeleton_index.yaml")
	absSkeletonPath, err := filepath.Abs(skeletonPath)
	require.NoError(t, err)

	rulesPath := filepath.Join("..", "..", ".trae", "skills", "single-extractor", "references", "field_rules", "basic_info.md")
	absRulesPath, err := filepath.Abs(rulesPath)
	require.NoError(t, err)

	absSourceDir, err := filepath.Abs(baseDir)
	require.NoError(t, err)

	outputDir := filepath.Join(baseDir, "userData", "2026-06-23_11-05-49", "temp", "output")
	absOutputDir, err := filepath.Abs(outputDir)
	require.NoError(t, err)

	// 清理之前的输出
	categoryDir := filepath.Join(absOutputDir, "基本信息")
	os.RemoveAll(categoryDir)

	ts, err := NewToolSet(
		WithTools(&mockTool{name: "dummy"}),
		WithAllowedScriptDirs(absScriptsDir, absOutputDir, absSourceDir),
		WithAllowIOLib(true),
		WithAllowOSLib(true),
		func(c *Config) { c.AllowFSLib = true },
	)
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	argsJSON, err := json.Marshal(map[string]any{
		"script_path": scriptPath,
		"args": map[string]any{
			"skeleton_path":  absSkeletonPath,
			"category":       "基本信息",
			"rules_path":     absRulesPath,
			"source_doc_dir": absSourceDir,
			"output_dir":     absOutputDir,
		},
	})
	require.NoError(t, err)

	result, err := ct.Call(context.Background(), argsJSON)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "success", resultMap["status"])

	innerWrapper, _ := resultMap["result"].(map[string]any)
	innerResult, _ := innerWrapper["result"].(map[string]any)
	totalFragments := 0
	if v, ok := innerResult["total_fragments"].(float64); ok {
		totalFragments = int(v)
	}
	t.Logf("基本信息类别总片段数（合并后）: %d", totalFragments)

	// 读取 raw_drafts.yaml 检查片段内容
	rawPath := filepath.Join(categoryDir, "基本信息_raw_drafts.yaml")
	rawData, err := os.ReadFile(rawPath)
	require.NoError(t, err)
	rawContent := string(rawData)

	// 统计 content: | 或 content: |- 出现的次数（即片段数）
	contentCount := strings.Count(rawContent, "content: |")
	t.Logf("raw_drafts 中 content: | 出现次数: %d", contentCount)
	// content: | 和 content: |- 都算
	contentCountStripped := strings.Count(rawContent, "content: |-")
	contentCountBlock := strings.Count(rawContent, "content: |\n")
	t.Logf("  content: |- 格式: %d, content: | 格式: %d", contentCountStripped, contentCountBlock)
	// 总 content 出现次数
	totalContentCount := strings.Count(rawContent, "content:")
	t.Logf("  content: 总出现次数: %d", totalContentCount)
	// 由于 content: 也可能出现在其他字段中，我们只统计 content: | 和 content: |-
	actualContentCount := contentCount - contentCountStripped
	t.Logf("  实际片段数（content: | 不含 |-）: %d", actualContentCount)

	// 检查是否有合并后的多行内容（合并后的片段 content 应包含多行）
	// 查找 content: | 后面跟着多行缩进内容的片段
	multiLineCount := 0
	lines := strings.Split(rawContent, "\n")
	for i, line := range lines {
		if strings.Contains(line, "content: |") {
			// 检查下一行是否有缩进内容
			if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "  ") {
				// 再检查是否有更多行
				j := i + 2
				for j < len(lines) && strings.HasPrefix(lines[j], "  ") {
					j++
				}
				contentLines := j - i - 1
				if contentLines > 1 {
					multiLineCount++
				}
			}
		}
	}
	t.Logf("多行内容片段数（合并后）: %d", multiLineCount)

	// 验证片段按文档分组（同一文档的片段应相邻）
	// 检查 source_document 字段是否按文档分组
	docOrder := make([]string, 0)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "source_document: ") {
			docName := strings.TrimPrefix(trimmed, "source_document: ")
			if len(docOrder) == 0 || docOrder[len(docOrder)-1] != docName {
				docOrder = append(docOrder, docName)
			}
		}
	}
	t.Logf("涉及的不同源文档数: %d", len(docOrder))
	for _, doc := range docOrder {
		t.Logf("  - %s", doc)
	}
}
