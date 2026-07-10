package luaexec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// getLuaExecResult 从 lua_exec 返回结果中提取完整响应
func getLuaExecResult(t *testing.T, result any) map[string]any {
	t.Helper()
	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	return resultMap
}

// getScriptResult 从 lua_exec 返回结果中提取脚本 return 值（内层 result）
func getScriptResult(t *testing.T, result any) map[string]any {
	t.Helper()
	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	outer, ok := resultMap["result"].(map[string]any)
	require.True(t, ok, "外层 result 应为 map")
	return outer
}

// setupWriteObjTest 初始化 write_obj.lua 测试环境
func setupWriteObjTest(t *testing.T) (ct interface {
	Call(ctx context.Context, jsonArgs []byte) (any, error)
}, scriptsDir string, outputDir string, rulesDir string) {
	t.Helper()

	scriptsDir = filepath.Join("..", "..", ".trae", "skills", "single-extractor", "scripts")
	absScriptsDir, err := filepath.Abs(scriptsDir)
	require.NoError(t, err)

	rulesDir = filepath.Join("..", "..", ".trae", "skills", "single-extractor", "references", "field_rules")
	absRulesDir, err := filepath.Abs(rulesDir)
	require.NoError(t, err)

	outputDir = t.TempDir()

	ts, err := NewToolSet(
		WithTools(&mockTool{name: "dummy"}),
		WithAllowedScriptDirs(absScriptsDir, outputDir, absRulesDir),
		WithAllowIOLib(true),
		WithAllowOSLib(true),
		WithAllowFSLib(true),
	)
	require.NoError(t, err)
	t.Cleanup(func() { ts.Close() })

	ct = ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})
	return ct, absScriptsDir, outputDir, absRulesDir
}

// callWriteObj 调用 write_obj.lua 并返回脚本 return 值（内层 result）
func callWriteObj(t *testing.T, ct interface {
	Call(ctx context.Context, jsonArgs []byte) (any, error)
}, scriptPath string, args map[string]any) map[string]any {
	t.Helper()
	argsJSON, err := json.Marshal(map[string]any{
		"script_path": scriptPath,
		"args":        args,
	})
	require.NoError(t, err)

	result, err := ct.Call(context.Background(), argsJSON)
	require.NoError(t, err)

	resp := getLuaExecResult(t, result)
	require.Equal(t, "success", resp["status"], "lua_exec 执行失败: %v", resp["errors"])

	inner := getScriptResult(t, result)
	return inner
}

// assertSuccess 断言脚本返回成功（code=0）
func assertSuccess(t *testing.T, inner map[string]any) {
	t.Helper()
	assert.Equal(t, float64(0), inner["code"])
	assert.Equal(t, "success", inner["message"])
}

// assertError 断言脚本返回失败（code=1）
func assertError(t *testing.T, inner map[string]any) {
	t.Helper()
	assert.Equal(t, float64(1), inner["code"])
}

// getRuleFilePath 获取规则文件路径
func getRuleFilePath(t *testing.T, rulesDir, filename string) string {
	t.Helper()
	return filepath.Join(rulesDir, filename)
}

// createTestFieldRule 创建测试用的 field_rule YAML 文件
// 返回 field_rule_path 文件路径
// 注意：不包含 field_definitions，因此不会触发字段校验（多余字段检查）。
// 需要字段校验的测试应自行创建包含 field_definitions 的 YAML。
func createTestFieldRule(t *testing.T, outputDir, rulesDir, category, subcategory, mdFilename string, keyFields []string) string {
	t.Helper()
	frData := map[string]any{
		"_meta": map[string]any{
			"category":    category,
			"subcategory": subcategory,
			"rules_path":  filepath.Join(rulesDir, mdFilename),
		},
		"subcategory": map[string]any{
			"name":         subcategory,
			"project_name": "测试工程",
			"key_fields":   keyFields,
			"field_count":  0,
		},
	}
	frFilePath := filepath.Join(outputDir, subcategory+"_field_rule.yaml")
	frBytes, err := yaml.Marshal(frData)
	require.NoError(t, err)
	err = os.WriteFile(frFilePath, frBytes, 0644)
	require.NoError(t, err)
	return frFilePath
}

func TestWriteObj_基本写入场景(t *testing.T) {
	ct, scriptsDir, outputDir, rulesDir := setupWriteObjTest(t)
	scriptPath := filepath.Join(scriptsDir, "write_obj.lua")
	objDraftsPath := filepath.Join(outputDir, "test_output.yaml")
	frFilePath := createTestFieldRule(t, outputDir, rulesDir, "杆塔组件", "铁塔", "tower_component.md", []string{"铁塔型号", "呼高(m)"})

	// ==================== 场景1：首次写入（新增对象） ====================
	t.Run("首次写入-新增对象", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":      1,
				"铁塔型号":    "1E5-SZ1",
				"呼高(m)":   24,
				"基数":      2,
				"塔全高(m)":  32.5,
				"每基塔重(t)": 8.5,
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePath,
		})

		assertSuccess(t, inner)
		r := inner["data"].(map[string]any)
		assert.Equal(t, "added", r["action"])
		assert.Equal(t, float64(1), r["round"])
		assert.Equal(t, "1E5-SZ1|24", r["object_key"])
		assert.Equal(t, true, r["supports_multiple"])
		assert.Equal(t, true, r["has_material"])
		t.Logf("  action=%s, key=%s, supports_multiple=%v, has_material=%v",
			r["action"], r["object_key"], r["supports_multiple"], r["has_material"])

		_, err := os.Stat(objDraftsPath)
		require.NoError(t, err, "文件应存在")
	})

	// ==================== 场景2：追加写入（不同key的新对象） ====================
	t.Run("追加写入-新对象", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":      2,
				"铁塔型号":    "1E5-SZ1",
				"呼高(m)":   27,
				"基数":      3,
				"塔全高(m)":  35.5,
				"每基塔重(t)": 9.2,
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePath,
		})

		assertSuccess(t, inner)
		r := inner["data"].(map[string]any)
		assert.Equal(t, "added", r["action"])
		assert.Equal(t, "1E5-SZ1|27", r["object_key"])
		t.Logf("  action=%s, key=%s", r["action"], r["object_key"])
	})

	// ==================== 场景3：覆盖更新（相同key的已有对象） ====================
	t.Run("覆盖更新-已有对象", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":      1,
				"铁塔型号":    "1E5-SZ1",
				"呼高(m)":   24,
				"基数":      5,
				"塔全高(m)":  32.5,
				"每基塔重(t)": 8.5,
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePath,
		})

		assertSuccess(t, inner)
		r := inner["data"].(map[string]any)
		assert.Equal(t, "updated", r["action"])
		assert.Equal(t, "1E5-SZ1|24", r["object_key"])
		t.Logf("  action=%s, key=%s", r["action"], r["object_key"])
	})

	// ==================== 场景4：验证最终文件结构 ====================
	t.Run("验证最终文件结构", func(t *testing.T) {
		data, err := os.ReadFile(objDraftsPath)
		require.NoError(t, err)
		content := string(data)
		t.Logf("文件内容:\n%s", content)

		assert.Contains(t, content, "铁塔:")
		assert.Contains(t, content, "序号: 1")
		assert.Contains(t, content, "序号: 2")
	})
}

func TestWriteObj_参数校验场景(t *testing.T) {
	ct, scriptsDir, outputDir, rulesDir := setupWriteObjTest(t)
	scriptPath := filepath.Join(scriptsDir, "write_obj.lua")
	objDraftsPath := filepath.Join(outputDir, "param_test.yaml")
	basicFrFilePath := createTestFieldRule(t, outputDir, rulesDir, "基本信息", "新建信息", "basic_info.md", []string{})
	towerFrFilePath := createTestFieldRule(t, outputDir, rulesDir, "杆塔组件", "铁塔", "tower_component.md", []string{"铁塔型号"})

	// ==================== 场景5：缺少必填参数 ====================
	t.Run("缺少必填参数", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"category":    "杆塔组件",
			"subcategory": "铁塔",
		})

		assertError(t, inner)
		assert.Contains(t, inner["message"].(string), "缺少必填参数")
		t.Logf("  错误信息: %s", inner["message"])
	})

	// ==================== 场景6：key_fields 为空（单条记录模式） ====================
	t.Run("key_fields为空-单条记录模式", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":   1,
				"工程名称": "测试工程",
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": basicFrFilePath,
		})

		assertSuccess(t, inner)
		r := inner["data"].(map[string]any)
		assert.Equal(t, false, r["supports_multiple"])
		t.Logf("  单条记录模式: supports_multiple=%v", r["supports_multiple"])
	})

	// ==================== 场景7：obj_data 类型错误 ====================
	t.Run("obj_data类型错误", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data":        12345,
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": towerFrFilePath,
		})

		assertError(t, inner)
		assert.Contains(t, inner["message"].(string), "obj_data 类型不支持")
		t.Logf("  错误信息: %s", inner["message"])
	})
}

func TestWriteObj_source_fields场景(t *testing.T) {
	ct, scriptsDir, outputDir, rulesDir := setupWriteObjTest(t)
	scriptPath := filepath.Join(scriptsDir, "write_obj.lua")
	objDraftsPath := filepath.Join(outputDir, "source_fields_test.yaml")
	frFilePath := createTestFieldRule(t, outputDir, rulesDir, "基本信息", "新建信息", "basic_info.md", []string{"工程名称"})

	// ==================== 场景8：source_fields 自动设置来源位置 ====================
	t.Run("source_fields自动设置来源位置", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":     1,
				"工程名称":   "测试工程",
				"电压等级":   "110kV",
				"工程性质":   "新建",
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePath,
			"source_fields": map[string]any{
				"序号":    "初设说明书.md::工程概况|100-250→1",
				"工程名称":  "初设说明书.md::工程概况|100-250→测试工程",
				"电压等级":  "初设说明书.md::工程概况|100-250→110kV",
				"工程性质":  "初设说明书.md::工程概况|100-250→新建",
			},
		})

		assertSuccess(t, inner)
		t.Logf("  source_fields 写入成功")

		data, err := os.ReadFile(objDraftsPath)
		require.NoError(t, err)
		content := string(data)
		assert.Contains(t, content, "来源位置:")
		assert.Contains(t, content, "初设说明书.md::工程概况|100-250→测试工程")
		t.Logf("  文件包含来源位置信息")
	})

	// ==================== 场景9：source_fields 合并到已有来源位置 ====================
	t.Run("source_fields合并到已有来源位置", func(t *testing.T) {
		inner1 := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":    2,
				"工程名称":  "测试工程2",
				"电压等级":  "220kV",
				"来源位置": map[string]any{
					"工程名称": "旧文档.md::章节|1-50→测试工程2",
				},
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePath,
			"source_fields": map[string]any{
				"电压等级": "新文档.md::章节|1-50→220kV",
			},
		})

		assertSuccess(t, inner1)

		data, err := os.ReadFile(objDraftsPath)
		require.NoError(t, err)
		content := string(data)
		assert.Contains(t, content, "旧文档.md::章节|1-50→测试工程2")
		assert.Contains(t, content, "新文档.md::章节|1-50→220kV")
		t.Logf("  来源位置合并成功")
	})
}

func TestWriteObj_write_mode场景(t *testing.T) {
	ct, scriptsDir, outputDir, rulesDir := setupWriteObjTest(t)
	scriptPath := filepath.Join(scriptsDir, "write_obj.lua")
	objDraftsPath := filepath.Join(outputDir, "write_mode_test.yaml")
	frFilePath := createTestFieldRule(t, outputDir, rulesDir, "基本信息", "新建信息", "basic_info.md", []string{"工程名称"})

	_ = callWriteObj(t, ct, scriptPath, map[string]any{
		"obj_data": map[string]any{
			"序号":     1,
			"工程名称":   "测试工程",
			"电压等级":   "110kV",
			"工程性质":   "新建",
			"地区类型":   "Ⅱ类",
			"来源位置": map[string]any{
				"工程名称": "doc.md::章节|1-50→测试工程",
				"电压等级": "doc.md::章节|1-50→110kV",
			},
		},
		"obj_drafts_path": objDraftsPath,
		"field_rule_path": frFilePath,
	})

	// ==================== 场景10：overwrite 模式（默认） ====================
	t.Run("overwrite模式-整体替换", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":    1,
				"工程名称":  "测试工程",
				"电压等级":  "220kV",
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePath,
			"write_mode":      "overwrite",
		})

		assertSuccess(t, inner)
		r := inner["data"].(map[string]any)
		assert.Equal(t, "updated", r["action"])

		data, err := os.ReadFile(objDraftsPath)
		require.NoError(t, err)
		content := string(data)
		assert.NotContains(t, content, "工程性质: 新建", "overwrite 应移除旧字段")
		assert.NotContains(t, content, "地区类型:", "overwrite 应移除旧字段")
		assert.NotContains(t, content, "来源位置:", "overwrite 应移除旧字段")
		t.Logf("  overwrite 模式：旧字段已被移除")
	})

	// ==================== 场景11：merge 模式（增量合并） ====================
	t.Run("merge模式-增量合并", func(t *testing.T) {
		_ = callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":    2,
				"工程名称":  "测试工程-merge",
				"电压等级":  "110kV",
				"工程性质":  "新建",
				"来源位置": map[string]any{
					"工程名称": "doc.md::章节|1-50→测试工程-merge",
				},
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePath,
		})

		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":    2,
				"工程名称":  "测试工程-merge",
				"电压等级":  "220kV",
				"来源位置": map[string]any{
					"电压等级": "new_doc.md::章节|1-50→220kV",
				},
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePath,
			"write_mode":      "merge",
		})

		assertSuccess(t, inner)
		r := inner["data"].(map[string]any)
		assert.Equal(t, "updated", r["action"])

		data, err := os.ReadFile(objDraftsPath)
		require.NoError(t, err)
		content := string(data)
		assert.Contains(t, content, "工程性质: 新建", "merge 应保留旧字段")
		assert.Contains(t, content, "电压等级: 220kV", "merge 应更新新字段")
		assert.Contains(t, content, "new_doc.md::章节|1-50→220kV", "merge 应合并来源位置")
		t.Logf("  merge 模式：旧字段保留，新字段更新")
	})
}

func TestWriteObj_field_rule_path场景(t *testing.T) {
	ct, scriptsDir, outputDir, rulesDir := setupWriteObjTest(t)
	scriptPath := filepath.Join(scriptsDir, "write_obj.lua")
	objDraftsPath := filepath.Join(outputDir, "field_rule_test.yaml")
	frFilePath := createTestFieldRule(t, outputDir, rulesDir, "基本信息", "新建信息", "basic_info.md", []string{"工程名称"})

	// 为"规则文件不存在"场景创建特殊的 field_rule（rules_path 指向不存在的文件）
	notExistRulePath := filepath.Join(outputDir, "not_exist.md")
	frNotExistData := map[string]any{
		"_meta": map[string]any{
			"category":    "基本信息",
			"subcategory": "新建信息",
			"rules_path":  notExistRulePath,
		},
		"subcategory": map[string]any{
			"name":           "新建信息",
			"project_name":   "测试工程",
			"key_fields":     []string{"工程名称"},
			"field_count":    0,
			"field_definitions": []map[string]string{
				{"字段": "工程名称", "来源": "直提", "约束值": "必须存在", "说明": "工程名称"},
			},
		},
	}
	frNotExistFilePath := filepath.Join(outputDir, "field_rule_not_exist.yaml")
	frNotExistBytes, err := yaml.Marshal(frNotExistData)
	require.NoError(t, err)
	err = os.WriteFile(frNotExistFilePath, frNotExistBytes, 0644)
	require.NoError(t, err)

	// 为"字段校验失败-多余字段"场景创建包含 field_definitions 的 field_rule
	frWithDefsData := map[string]any{
		"_meta": map[string]any{
			"category":    "基本信息",
			"subcategory": "新建信息",
			"rules_path":  filepath.Join(rulesDir, "basic_info.md"),
		},
		"subcategory": map[string]any{
			"name":         "新建信息",
			"project_name": "测试工程",
			"key_fields":   []string{"工程名称"},
			"field_count":  14,
			"field_definitions": []map[string]string{
				{"字段": "序号", "来源": "直提", "约束值": "数值", "说明": "唯一编号"},
				{"字段": "工程名称", "来源": "直提", "约束值": "必须存在", "说明": "工程名称"},
				{"字段": "项目划分", "来源": "直提", "约束值": "架空输电线路工程等", "说明": "项目划分"},
				{"字段": "工程阶段", "来源": "直提", "约束值": "可行性研究估算等", "说明": "工程阶段"},
				{"字段": "定额规范", "来源": "直提", "约束值": "电网预规2025年版", "说明": "定额规范"},
				{"字段": "地区规范", "来源": "直提", "约束值": "预规等", "说明": "地区规范"},
				{"字段": "电压等级", "来源": "直提", "约束值": "35kV~1100kV", "说明": "电压等级"},
				{"字段": "工程性质", "来源": "直提", "约束值": "新建/扩建", "说明": "工程性质"},
				{"字段": "地区类型", "来源": "直提", "约束值": "Ⅰ类~Ⅴ类", "说明": "地区类型"},
				{"字段": "架线类型", "来源": "直提", "约束值": "一般线路/大跨越", "说明": "架线类型"},
				{"字段": "编制模式", "来源": "直提", "约束值": "统计工程量", "说明": "编制模式"},
				{"字段": "阶段类型", "来源": "直提", "约束值": "概预算/建安预算", "说明": "阶段类型"},
				{"字段": "项目类型", "来源": "直提", "约束值": "变电/线路", "说明": "项目类型"},
				{"字段": "特殊地区", "来源": "直提", "约束值": "常规地区等", "说明": "特殊地区"},
			},
		},
	}
	frWithDefsBytes, err := yaml.Marshal(frWithDefsData)
	require.NoError(t, err)
	frFilePathWithDefs := filepath.Join(outputDir, "field_rule_with_defs.yaml")
	err = os.WriteFile(frFilePathWithDefs, frWithDefsBytes, 0644)
	require.NoError(t, err)

	// ==================== 场景12：字段校验通过 ====================
	t.Run("字段校验通过", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":     1,
				"工程名称":   "测试工程",
				"项目划分":   "架空输电线路工程",
				"工程阶段":   "初步设计概算",
				"定额规范":   "电网预规2025年版",
				"地区规范":   "预规",
				"电压等级":   "110kV",
				"工程性质":   "新建",
				"地区类型":   "Ⅱ类",
				"架线类型":   "一般线路",
				"编制模式":   "统计工程量",
				"阶段类型":   "概预算",
				"项目类型":   "线路",
				"特殊地区":   "常规地区",
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePath,
		})

		assertSuccess(t, inner)
		t.Logf("  字段校验通过")
	})

	// ==================== 场景13：缺少字段（不再校验，应成功） ====================
	t.Run("缺少字段-应成功通过", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":    2,
				"工程名称":  "测试工程2",
				"电压等级":  "110kV",
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePath,
		})

		assertSuccess(t, inner)
		t.Logf("  缺少字段场景：已通过（不再校验缺少字段）")
	})

	// ==================== 场景14：字段校验失败-多余字段 ====================
	t.Run("字段校验失败-多余字段", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":     3,
				"工程名称":   "测试工程3",
				"项目划分":   "架空输电线路工程",
				"工程阶段":   "初步设计概算",
				"定额规范":   "电网预规2025年版",
				"地区规范":   "预规",
				"电压等级":   "110kV",
				"工程性质":   "新建",
				"地区类型":   "Ⅱ类",
				"架线类型":   "一般线路",
				"编制模式":   "统计工程量",
				"阶段类型":   "概预算",
				"项目类型":   "线路",
				"特殊地区":   "常规地区",
				"多余字段1":  "不应该存在",
				"多余字段2":  "也不应该存在",
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePathWithDefs,
		})

		assertError(t, inner)
		assert.Contains(t, inner["message"].(string), "字段校验失败")
		errs := inner["errors"].([]any)
		hasExtra := false
		for _, e := range errs {
			if str, ok := e.(string); ok {
				if contains(str, "多余字段") {
					hasExtra = true
					break
				}
			}
		}
		assert.True(t, hasExtra, "应包含多余字段错误")
		t.Logf("  多余字段检测成功，错误数: %d", len(errs))
		for _, e := range errs {
			t.Logf("    - %v", e)
		}
	})

	// ==================== 场景15：规则文件不存在 ====================
	t.Run("规则文件不存在", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":   4,
				"工程名称": "测试工程4",
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frNotExistFilePath,
		})

		assertError(t, inner)
		assert.Contains(t, inner["message"].(string), "解析规则文件失败")
		t.Logf("  规则文件不存在检测成功: %s", inner["message"])
	})
}

func TestWriteObj_读取模式场景(t *testing.T) {
	ct, scriptsDir, outputDir, rulesDir := setupWriteObjTest(t)
	scriptPath := filepath.Join(scriptsDir, "write_obj.lua")
	objDraftsPath := filepath.Join(outputDir, "read_test.yaml")
	frFilePath := createTestFieldRule(t, outputDir, rulesDir, "基本信息", "新建信息", "basic_info.md", []string{"工程名称"})

	_ = callWriteObj(t, ct, scriptPath, map[string]any{
		"obj_data": map[string]any{
			"序号":     1,
			"工程名称":   "测试工程",
			"电压等级":   "110kV",
			"工程性质":   "新建",
			"来源位置": map[string]any{
				"工程名称": "doc.md::章节|1-50→测试工程",
			},
		},
		"obj_drafts_path": objDraftsPath,
		"field_rule_path": frFilePath,
	})

	// ==================== 场景17：读取模式-找到对象 ====================
	t.Run("读取模式-找到对象", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"工程名称": "测试工程",
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePath,
			"action":          "read",
		})

		assertSuccess(t, inner)
		r := inner["data"].(map[string]any)
		assert.NotNil(t, r["object"])
		assert.Equal(t, "测试工程", r["object_key"])
		t.Logf("  读取成功: key=%s", r["object_key"])
	})

	// ==================== 场景18：读取模式-未找到对象 ====================
	t.Run("读取模式-未找到对象", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"工程名称": "不存在的工程",
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePath,
			"action":          "read",
		})

		assertError(t, inner)
		assert.Contains(t, inner["message"].(string), "未找到匹配的对象")
		t.Logf("  未找到对象检测成功: %s", inner["message"])
	})

	// ==================== 场景19：读取模式-文件不存在 ====================
	t.Run("读取模式-文件不存在", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"工程名称": "测试工程",
			},
			"obj_drafts_path": filepath.Join(outputDir, "not_exist.yaml"),
			"field_rule_path": frFilePath,
			"action":          "read",
		})

		assertError(t, inner)
		assert.Contains(t, inner["message"].(string), "读取文件失败")
		t.Logf("  文件不存在检测成功: %s", inner["message"])
	})
}

func TestWriteObj_真实数据场景(t *testing.T) {
	ct, scriptsDir, outputDir, rulesDir := setupWriteObjTest(t)
	scriptPath := filepath.Join(scriptsDir, "write_obj.lua")
	objDraftsPath := filepath.Join(outputDir, "real_data_test.yaml")

	// ==================== 场景20：使用真实 output_draft.yaml 中的新建信息数据 ====================
	t.Run("真实数据-新建信息", func(t *testing.T) {
		frFilePath := createTestFieldRule(t, outputDir, rulesDir, "基本信息", "新建信息", "basic_info.md", []string{"工程名称"})
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":     1,
				"工程名称":   "梅花~邵屯T接城东变电站110kV线路工程",
				"项目名称":   "梅花~邵屯T接城东变电站110kV线路工程",
				"建设单位":   "国网邢台供电公司",
				"设计单位":   "邢台电力勘测设计院有限责任公司",
				"建设性质":   "新建",
				"电压等级":   "110kV",
				"回路数":    "单回路",
				"线路长度":   "5.43km（架空5.39km，电缆0.04km）",
				"导线型号":   "JL3/G1A-300/25",
				"地线型号":   "两根OPGW-48",
				"地形":     "平地100%",
				"起止点":    "线路起自110kV梅邵线64#大号侧，止于新建南和城东110kV变电站",
				"批准概算":   "架空工程静态投资552万元，动态投资560万元；电缆工程静态投资93万元，动态投资94万元",
				"建设周期":   "",
				"资金来源":   "",
				"项目代码":   "1183-202207C-S02-01",
				"建设依据":   "可研批复、设计中标通知书等",
				"备注":     "",
				"来源位置": map[string]any{
					"工程名称": "初设说明书.md::工程概况|100-250→梅花~邵屯T接城东变电站110kV线路工程",
					"项目名称": "初设说明书.md::工程概况|100-250→梅花~邵屯T接城东变电站110kV线路工程",
					"建设单位": "初设说明书.md::工程概况|100-250→国网邢台供电公司",
					"设计单位": "初设说明书.md::工程概况|100-250→邢台电力勘测设计院有限责任公司",
					"建设性质": "初设说明书.md::工程概况|100-250→新建",
					"电压等级": "初设说明书.md::工程概况|100-250→110kV",
					"回路数":  "初设说明书.md::工程概况|100-250→单回路",
					"线路长度": "初设说明书.md::工程概况|100-250→5.43km",
					"导线型号": "初设说明书.md::工程概况|100-250→JL3/G1A-300/25",
					"地线型号": "初设说明书.md::工程概况|100-250→两根OPGW-48",
					"地形":   "初设说明书.md::工程概况|100-250→平地100%",
					"起止点":  "初设说明书.md::工程概况|100-250→线路起自110kV梅邵线64#大号侧，止于新建南和城东110kV变电站",
					"批准概算": "初设说明书.md::工程概况|250-300→架空工程静态投资552万元，动态投资560万元；电缆工程静态投资93万元，动态投资94万元",
					"建设依据": "初设说明书.md::工程概况|100-250→可研批复、设计中标通知书等",
				},
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePath,
		})

		assertSuccess(t, inner)
		t.Logf("  真实数据写入成功")
	})

	// ==================== 场景21：使用真实 output_draft.yaml 中的编制人信息数据 ====================
	t.Run("真实数据-编制人信息", func(t *testing.T) {
		frFilePath := createTestFieldRule(t, outputDir, rulesDir, "基本信息", "编制人信息", "basic_info.md", []string{"序号"})
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":     1,
				"编制人":    "",
				"编制单位":   "邢台电力勘测设计院有限责任公司",
				"资质证书":   "工程设计(乙级)：A213006018；工程勘察(乙级)：B213012838；工程咨询(乙级)：032022010104",
				"工程地址":   "邢台市南和县",
				"编制时间":   "二〇二四年六月",
				"来源位置": map[string]any{
					"编制单位": "施工组织设计大纲.md::1.2各专业设计简介|1-50→邢台电力勘测设计院有限责任公司",
					"工程地址": "施工组织设计大纲.md::1.2各专业设计简介|1-50→邢台市南和县",
					"资质证书": "初设说明书.md::封面|1-10→工程设计(乙级)：A213006018；工程勘察(乙级)：B213012838；工程咨询(乙级)：032022010104",
					"编制时间": "初设说明书.md::封面|1-10→二〇二四年六月",
				},
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePath,
		})

		assertSuccess(t, inner)
		t.Logf("  编制人信息写入成功")
	})

	// ==================== 场景22：验证最终文件包含所有子类别 ====================
	t.Run("验证最终文件包含所有子类别", func(t *testing.T) {
		data, err := os.ReadFile(objDraftsPath)
		require.NoError(t, err)
		content := string(data)
		t.Logf("最终文件内容:\n%s", content)

		assert.Contains(t, content, "新建信息:")
		assert.Contains(t, content, "编制人信息:")
		assert.Contains(t, content, "梅花~邵屯T接城东变电站110kV线路工程")
		assert.Contains(t, content, "邢台电力勘测设计院有限责任公司")
	})
}

// ==================== 新增：规则解析和物料写入测试 ====================

func TestWriteObj_规则解析场景(t *testing.T) {
	ct, scriptsDir, outputDir, rulesDir := setupWriteObjTest(t)
	scriptPath := filepath.Join(scriptsDir, "write_obj.lua")
	objDraftsPath := filepath.Join(outputDir, "rule_parse_test.yaml")

	// ==================== 场景23：基础组件-灌注桩基础（多条+物料） ====================
	t.Run("基础组件-灌注桩基础-多条+物料", func(t *testing.T) {
		frFilePath := createTestFieldRule(t, outputDir, rulesDir, "基础组件", "灌注桩基础", "foundation.md", []string{"特征段", "灌注桩基础型号"})
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":          1,
				"特征段":         "特征段1",
				"灌注桩基础型号":     "110-DD21GS-J4-18",
				"基数":          1,
				"每基孔数":        1,
				"基础类型":        "灌注桩",
				"孔径(m)":       2.2,
				"桩长(m)":       11.8,
				"桩入土长(m)":    11.5,
				"累计深度":        11.8,
				"基础.砼量(m3)":  47.64,
				"一般钢筋.钢筋量(t)": 2.47,
				"地脚螺栓(t)":    3.44,
				"保护帽":         true,
				"商品砼":         true,
				"土质类型":        "Ⅰ、Ⅱ类土",
				"挖土方式":        "机械坑上挖土",
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePath,
		})

		assertSuccess(t, inner)
		r := inner["data"].(map[string]any)
		assert.Equal(t, true, r["supports_multiple"], "灌注桩基础应支持多条")
		assert.Equal(t, true, r["has_material"], "灌注桩基础应有物料")
		t.Logf("  灌注桩基础: supports_multiple=%v, has_material=%v", r["supports_multiple"], r["has_material"])
	})

	// ==================== 场景24：基本信息-新建信息（单条+无物料） ====================
	t.Run("基本信息-新建信息-单条+无物料", func(t *testing.T) {
		frFilePath := createTestFieldRule(t, outputDir, rulesDir, "基本信息", "新建信息", "basic_info.md", []string{})
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":    1,
				"工程名称":  "测试工程",
				"电压等级":  "110kV",
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePath,
		})

		assertSuccess(t, inner)
		r := inner["data"].(map[string]any)
		assert.Equal(t, false, r["supports_multiple"], "新建信息应为单条")
		assert.Equal(t, false, r["has_material"], "新建信息应无物料")
		t.Logf("  新建信息: supports_multiple=%v, has_material=%v", r["supports_multiple"], r["has_material"])
	})

	// ==================== 场景25：杆塔组件-铁塔（多条+items-ref物料） ====================
	t.Run("杆塔组件-铁塔-多条+items-ref物料", func(t *testing.T) {
		frFilePath := createTestFieldRule(t, outputDir, rulesDir, "杆塔组件", "铁塔", "tower_component.md", []string{"铁塔型号", "呼高(m)"})
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":      1,
				"铁塔型号":    "1E5-SZ1",
				"呼高(m)":   24,
				"基数":      2,
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePath,
		})

		assertSuccess(t, inner)
		r := inner["data"].(map[string]any)
		assert.Equal(t, true, r["supports_multiple"], "铁塔应支持多条")
		assert.Equal(t, true, r["has_material"], "铁塔应有物料（items-ref）")
		t.Logf("  铁塔: supports_multiple=%v, has_material=%v", r["supports_multiple"], r["has_material"])
	})
}

func TestWriteObj_物料写入场景(t *testing.T) {
	ct, scriptsDir, outputDir, rulesDir := setupWriteObjTest(t)
	scriptPath := filepath.Join(scriptsDir, "write_obj.lua")
	objDraftsPath := filepath.Join(outputDir, "material_test.yaml")
	frFilePath := createTestFieldRule(t, outputDir, rulesDir, "基础组件", "灌注桩基础", "foundation.md", []string{"特征段", "灌注桩基础型号"})

	// 先写入主对象
	_ = callWriteObj(t, ct, scriptPath, map[string]any{
		"obj_data": map[string]any{
			"序号":          1,
			"特征段":         "特征段1",
			"灌注桩基础型号":     "110-DD21GS-J4-18",
			"基数":          1,
			"每基孔数":        1,
			"基础类型":        "灌注桩",
			"孔径(m)":       2.2,
			"桩长(m)":       11.8,
			"累计深度":        11.8,
			"基础.砼量(m3)":  47.64,
			"一般钢筋.钢筋量(t)": 2.47,
			"地脚螺栓(t)":    3.44,
			"保护帽":         true,
			"商品砼":         true,
		},
		"obj_drafts_path": objDraftsPath,
		"field_rule_path": frFilePath,
	})

	// ==================== 场景26：写入物料-混凝土 ====================
	t.Run("写入物料-混凝土", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"项目名称": "混凝土",
				"规格型号": "C30",
				"单位":   "m3",
				"数量":   47.64,
				"来源位置": map[string]any{
					"项目名称": "设备材料清册.md::基础明细|L1→C30",
					"规格型号": "设备材料清册.md::基础明细|L1→C30",
					"单位":   "设备材料清册.md::基础明细|L1→m3",
					"数量":   "设备材料清册.md::基础明细|L1→47.64",
				},
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePath,
			"write_type":      "material",
			"material_key":    []string{"特征段1", "110-DD21GS-J4-18"},
		})

		assertSuccess(t, inner)
		r := inner["data"].(map[string]any)
		assert.Equal(t, "material_added", r["action"])
		assert.Equal(t, float64(1), r["material_index"])
		t.Logf("  物料写入成功: material_index=%v", r["material_index"])
	})

	// ==================== 场景27：写入物料-地脚螺栓 ====================
	t.Run("写入物料-地脚螺栓", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"项目名称": "地脚螺栓",
				"规格型号": "5.6级",
				"单位":   "t",
				"数量":   3.44,
				"来源位置": map[string]any{
					"项目名称": "设备材料清册.md::结构架空部分材料表|L1→地脚螺栓",
					"规格型号": "设备材料清册.md::结构架空部分材料表|L1→5.6级",
					"单位":   "设备材料清册.md::结构架空部分材料表|L1→t",
					"数量":   "设备材料清册.md::基础明细|L1→3440.97kg",
				},
			},
			"obj_drafts_path": objDraftsPath,
			"field_rule_path": frFilePath,
			"write_type":      "material",
			"material_key":    []string{"特征段1", "110-DD21GS-J4-18"},
		})

		assertSuccess(t, inner)
		r := inner["data"].(map[string]any)
		assert.Equal(t, "material_added", r["action"])
		assert.Equal(t, float64(2), r["material_index"])
		t.Logf("  物料写入成功: material_index=%v", r["material_index"])
	})

	// ==================== 场景28：验证物料嵌套在对象内部 ====================
	t.Run("验证物料嵌套结构", func(t *testing.T) {
		data, err := os.ReadFile(objDraftsPath)
		require.NoError(t, err)
		content := string(data)
		t.Logf("物料文件内容:\n%s", content)

		assert.Contains(t, content, "物料:")
		assert.Contains(t, content, "C30")
		assert.Contains(t, content, "5.6级")
		assert.Contains(t, content, "3440.97kg")
	})
}

// contains 辅助函数
func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsImpl(s, substr)
}

func containsImpl(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
