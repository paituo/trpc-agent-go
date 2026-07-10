package luaexec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractRawDraftsSingleFile(t *testing.T) {
	baseDir := filepath.Join("..", "..", "bowei-openclaw")

	// 检查数据目录是否存在
	skeletonRelPath := filepath.Join(baseDir, "userData", "源文件", "temp", "skeleton_index.yaml")
	absSkeletonPath, err := filepath.Abs(skeletonRelPath)
	require.NoError(t, err)
	if _, err := os.Stat(absSkeletonPath); os.IsNotExist(err) {
		t.Skipf("骨架索引文件不存在，跳过测试: %s", absSkeletonPath)
	}

	scriptsDir := filepath.Join(baseDir, "skills", "single-extractor", "scripts")
	absScriptsDir, err := filepath.Abs(scriptsDir)
	require.NoError(t, err)
	scriptPath := filepath.Join(absScriptsDir, "extract_raw_drafts.lua")
	_, err = os.Stat(scriptPath)
	require.NoError(t, err)

	rulesPath := filepath.Join(baseDir, "skills", "single-extractor", "references", "field_rules", "basic_info.md")
	absRulesPath, err := filepath.Abs(rulesPath)
	require.NoError(t, err)

	absSourceDir, err := filepath.Abs(baseDir)
	require.NoError(t, err)

	outputDir := filepath.Join(baseDir, "userData", "源文件", "temp", "output")
	absOutputDir, err := filepath.Abs(outputDir)
	require.NoError(t, err)
	os.MkdirAll(absOutputDir, 0755)

	absBaseDir, err := filepath.Abs(baseDir)
	require.NoError(t, err)

	ts, err := NewToolSet(
		WithTools(&mockTool{name: "dummy"}),
		WithAllowedScriptDirs(absScriptsDir, absOutputDir, absBaseDir),
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

	t.Logf("执行结果: %+v", result)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "success", resultMap["status"])

	// 验证临时文件目录：{output_dir}/{category}/
	categoryDir := filepath.Join(absOutputDir, "基本信息")
	_, err = os.Stat(categoryDir)
	require.NoError(t, err, "临时文件目录应存在")

	// 验证 raw_drafts：每个类别一个文件（YAML格式）
	rawFile := "基本信息_raw_drafts.yaml"
	rawPath := filepath.Join(categoryDir, rawFile)
	data, err := os.ReadFile(rawPath)
	if err == nil {
		content := string(data)
		t.Logf("✅ %s: %d 字节", rawFile, len(content))
		assert.False(t, strings.Contains(content, "_content:"), "不应包含 YAML _content 包装")
		assert.False(t, strings.Contains(content, "\\n"), "不应包含转义的 \\n")
		assert.Contains(t, content, "_meta:", "应包含 _meta 元数据")
		assert.Contains(t, content, "subcategories:", "应包含 subcategories 数组")
		assert.Contains(t, content, "fragment_count:", "应包含 fragment_count")
		assert.Contains(t, content, "category: 基本信息", "应有类别信息")
		// 当有片段时验证以下字段（total_fragments > 0 时）
		if strings.Contains(content, "total_fragments: 0") {
			t.Logf("⚠ total_fragments 为 0，跳过片段级字段验证")
		} else {
			assert.Contains(t, content, "length:", "应包含 length（分段长度）")
			assert.Contains(t, content, "source_document:", "应包含 source_document")
			assert.Contains(t, content, "content:", "应包含 content")
		}
	} else {
		t.Logf("❌ %s: %v", rawFile, err)
	}

	// 验证 field_rule：每个子类别独立一个文件（YAML格式）
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
			// 验证新字段
			assert.Contains(t, content, "约束值:", "%s 应包含 约束值", ff)
			assert.Contains(t, content, "说明:", "%s 应包含 说明", ff)
			assert.Contains(t, content, "key_fields:", "%s 应包含 key_fields", ff)
			assert.Contains(t, content, "supplement_notes:", "%s 应包含 supplement_notes", ff)
		} else {
			t.Logf("❌ %s: %v", ff, err)
		}
	}
}
