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

func TestExtractRawDraftsRealEnv(t *testing.T) {
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

	ts, err := NewToolSet(
		WithTools(&mockTool{name: "dummy"}),
		WithAllowedScriptDirs(absScriptsDir, absOutputDir),
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

	// 检查输出文件
	expectedSubcategories := []string{"新建信息", "计算信息", "工程设置", "编制人信息", "调差系数", "特征信息"}
	foundFiles := 0
	for _, sc := range expectedSubcategories {
		rawFile := sc + "_基本信息_raw_drafts.md"
		rawPath := filepath.Join(absOutputDir, rawFile)
		if data, err := os.ReadFile(rawPath); err == nil {
			foundFiles++
			content := string(data)
			t.Logf("✅ %s: %d 字节", rawFile, len(content))
			assert.False(t, strings.Contains(content, "_content:"), "%s 不应包含 YAML _content 包装", rawFile)
			assert.False(t, strings.Contains(content, "\\n"), "%s 不应包含转义的 \\n", rawFile)
			assert.True(t, strings.HasPrefix(content, "===RAW_DRAFT==="), "%s 应以 ===RAW_DRAFT=== 开头", rawFile)
		} else {
			t.Logf("❌ %s: %v", rawFile, err)
		}

		fieldFile := sc + "_基本信息_field_rule.md"
		fieldPath := filepath.Join(absOutputDir, fieldFile)
		if data, err := os.ReadFile(fieldPath); err == nil {
			foundFiles++
			content := string(data)
			t.Logf("✅ %s: %d 字节", fieldFile, len(content))
			assert.False(t, strings.Contains(content, "_content:"), "%s 不应包含 YAML _content 包装", fieldFile)
			assert.Contains(t, content, "===CATEGORY_RULES===", "%s 应包含 CATEGORY_RULES", fieldFile)
		} else {
			t.Logf("❌ %s: %v", fieldFile, err)
		}
	}
	t.Logf("共找到 %d 个输出文件", foundFiles)
	assert.GreaterOrEqual(t, foundFiles, 1, "至少应生成1个输出文件")
}
