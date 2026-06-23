package luaexec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
}, scriptsDir string, outputDir string) {
	t.Helper()

	// 脚本目录：使用实际技能目录
	scriptsDir = filepath.Join("..", "..", ".trae", "skills", "single-extractor", "scripts")
	absScriptsDir, err := filepath.Abs(scriptsDir)
	require.NoError(t, err)

	// 规则文件目录
	rulesDir := filepath.Join("..", "..", ".trae", "skills", "single-extractor", "references", "field_rules")
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
	return ct, absScriptsDir, outputDir
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

	// 先检查 lua_exec 顶层 status
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

func TestWriteObj_基本写入场景(t *testing.T) {
	ct, scriptsDir, outputDir := setupWriteObjTest(t)
	scriptPath := filepath.Join(scriptsDir, "write_obj.lua")
	objDraftsPath := filepath.Join(outputDir, "test_output.yaml")

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
				"物料":      []any{},
			},
			"category":        "杆塔组件",
			"subcategory":     "铁塔",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"铁塔型号", "呼高(m)"},
		})

		assertSuccess(t, inner)
		r := inner["data"].(map[string]any)
		assert.Equal(t, "added", r["action"])
		assert.Equal(t, float64(1), r["round"])
		assert.Equal(t, "1E5-SZ1|24", r["object_key"])
		t.Logf("  action=%s, key=%s", r["action"], r["object_key"])

		// 验证文件已创建
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
				"物料":      []any{},
			},
			"category":        "杆塔组件",
			"subcategory":     "铁塔",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"铁塔型号", "呼高(m)"},
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
				"基数":      5, // 修改基数
				"塔全高(m)":  32.5,
				"每基塔重(t)": 8.5,
				"物料":      []any{},
			},
			"category":        "杆塔组件",
			"subcategory":     "铁塔",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"铁塔型号", "呼高(m)"},
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

		assert.Contains(t, content, "_meta:")
		assert.Contains(t, content, "_validation_report:")
		assert.Contains(t, content, "杆塔组件:")
		assert.Contains(t, content, "铁塔:")
		assert.Contains(t, content, "objects:")
		assert.Contains(t, content, "object_count: 2") // 2个对象
	})
}

func TestWriteObj_参数校验场景(t *testing.T) {
	ct, scriptsDir, outputDir := setupWriteObjTest(t)
	scriptPath := filepath.Join(scriptsDir, "write_obj.lua")
	objDraftsPath := filepath.Join(outputDir, "param_test.yaml")

	// ==================== 场景5：缺少必填参数 ====================
	t.Run("缺少必填参数", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"category":    "杆塔组件",
			"subcategory": "铁塔",
			// 缺少 obj_data, obj_drafts_path, key_fields
		})

		assertError(t, inner)
		assert.Contains(t, inner["message"].(string), "缺少必填参数")
		t.Logf("  错误信息: %s", inner["message"])
	})

	// ==================== 场景6：key_fields 为空 ====================
	t.Run("key_fields为空", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":   1,
				"铁塔型号": "1E5-SZ1",
			},
			"category":        "杆塔组件",
			"subcategory":     "铁塔",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{},
		})

		assertError(t, inner)
		assert.Contains(t, inner["message"].(string), "key_fields 必须为非空数组")
		t.Logf("  错误信息: %s", inner["message"])
	})

	// ==================== 场景7：obj_data 类型错误 ====================
	t.Run("obj_data类型错误", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data":        12345, // 非法类型
			"category":        "杆塔组件",
			"subcategory":     "铁塔",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"铁塔型号"},
		})

		assertError(t, inner)
		assert.Contains(t, inner["message"].(string), "obj_data 类型不支持")
		t.Logf("  错误信息: %s", inner["message"])
	})
}

func TestWriteObj_source_fields场景(t *testing.T) {
	ct, scriptsDir, outputDir := setupWriteObjTest(t)
	scriptPath := filepath.Join(scriptsDir, "write_obj.lua")
	objDraftsPath := filepath.Join(outputDir, "source_fields_test.yaml")

	// ==================== 场景8：source_fields 自动设置来源位置 ====================
	t.Run("source_fields自动设置来源位置", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":      1,
				"工程名称":    "测试工程",
				"电压等级":    "110kV",
				"工程性质":    "新建",
				"物料":      []any{},
			},
			"category":        "基本信息",
			"subcategory":     "新建信息",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"工程名称"},
			"source_fields": map[string]any{
				"序号":    "初设说明书.md::工程概况|100-250→1",
				"工程名称":  "初设说明书.md::工程概况|100-250→测试工程",
				"电压等级":  "初设说明书.md::工程概况|100-250→110kV",
				"工程性质":  "初设说明书.md::工程概况|100-250→新建",
			},
		})

		assertSuccess(t, inner)
		t.Logf("  source_fields 写入成功")

		// 验证文件中包含来源位置
		data, err := os.ReadFile(objDraftsPath)
		require.NoError(t, err)
		content := string(data)
		assert.Contains(t, content, "来源位置:")
		assert.Contains(t, content, "初设说明书.md::工程概况|100-250→测试工程")
		t.Logf("  文件包含来源位置信息")
	})

	// ==================== 场景9：source_fields 合并到已有来源位置 ====================
	t.Run("source_fields合并到已有来源位置", func(t *testing.T) {
		// 先写入一个带部分来源位置的对象
		inner1 := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":    2,
				"工程名称":  "测试工程2",
				"电压等级":  "220kV",
				"物料":    []any{},
				"来源位置": map[string]any{
					"工程名称": "旧文档.md::章节|1-50→测试工程2",
				},
			},
			"category":        "基本信息",
			"subcategory":     "新建信息",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"工程名称"},
			"source_fields": map[string]any{
				"电压等级": "新文档.md::章节|1-50→220kV",
			},
		})

		assertSuccess(t, inner1)

		// 验证来源位置合并
		data, err := os.ReadFile(objDraftsPath)
		require.NoError(t, err)
		content := string(data)
		assert.Contains(t, content, "旧文档.md::章节|1-50→测试工程2")
		assert.Contains(t, content, "新文档.md::章节|1-50→220kV")
		t.Logf("  来源位置合并成功")
	})
}

func TestWriteObj_write_mode场景(t *testing.T) {
	ct, scriptsDir, outputDir := setupWriteObjTest(t)
	scriptPath := filepath.Join(scriptsDir, "write_obj.lua")
	objDraftsPath := filepath.Join(outputDir, "write_mode_test.yaml")

	// 先写入一个初始对象
	_ = callWriteObj(t, ct, scriptPath, map[string]any{
		"obj_data": map[string]any{
			"序号":     1,
			"工程名称":   "测试工程",
			"电压等级":   "110kV",
			"工程性质":   "新建",
			"地区类型":   "Ⅱ类",
			"物料":     []any{},
			"来源位置": map[string]any{
				"工程名称": "doc.md::章节|1-50→测试工程",
				"电压等级": "doc.md::章节|1-50→110kV",
			},
		},
		"category":        "基本信息",
		"subcategory":     "新建信息",
		"obj_drafts_path": objDraftsPath,
		"key_fields":      []string{"工程名称"},
	})

	// ==================== 场景10：overwrite 模式（默认） ====================
	t.Run("overwrite模式-整体替换", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":    1,
				"工程名称":  "测试工程",
				"电压等级":  "220kV", // 修改电压等级
				"物料":    []any{},
				// 注意：没有工程性质、地区类型、来源位置
			},
			"category":        "基本信息",
			"subcategory":     "新建信息",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"工程名称"},
			"write_mode":      "overwrite",
		})

		assertSuccess(t, inner)
		r := inner["data"].(map[string]any)
		assert.Equal(t, "updated", r["action"])

		// 验证：overwrite 模式下，旧字段（工程性质、地区类型）应被移除
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
		// 先写入一个初始对象
		_ = callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":    2,
				"工程名称":  "测试工程-merge",
				"电压等级":  "110kV",
				"工程性质":  "新建",
				"物料":    []any{},
				"来源位置": map[string]any{
					"工程名称": "doc.md::章节|1-50→测试工程-merge",
				},
			},
			"category":        "基本信息",
			"subcategory":     "新建信息",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"工程名称"},
		})

		// merge 模式：只更新部分字段
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":    2,
				"工程名称":  "测试工程-merge",
				"电压等级":  "220kV", // 更新电压等级
				// 没有工程性质
				"物料": []any{},
				"来源位置": map[string]any{
					"电压等级": "new_doc.md::章节|1-50→220kV",
				},
			},
			"category":        "基本信息",
			"subcategory":     "新建信息",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"工程名称"},
			"write_mode":      "merge",
		})

		assertSuccess(t, inner)
		r := inner["data"].(map[string]any)
		assert.Equal(t, "updated", r["action"])

		// 验证：merge 模式下，旧字段（工程性质）应保留
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
	ct, scriptsDir, outputDir := setupWriteObjTest(t)
	scriptPath := filepath.Join(scriptsDir, "write_obj.lua")

	// 规则文件路径
	rulesDir := filepath.Join("..", "..", ".trae", "skills", "single-extractor", "references", "field_rules")
	absRulesDir, err := filepath.Abs(rulesDir)
	require.NoError(t, err)
	basicInfoRulePath := filepath.Join(absRulesDir, "basic_info.md")

	objDraftsPath := filepath.Join(outputDir, "field_rule_test.yaml")

	// ==================== 场景12：字段校验通过 ====================
	t.Run("字段校验通过", func(t *testing.T) {
		// 新建信息规则定义14个字段：序号、工程名称、项目划分、工程阶段、定额规范、地区规范、
		// 电压等级、工程性质、地区类型、架线类型、编制模式、阶段类型、项目类型、特殊地区
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
				"物料":     []any{},
			},
			"category":        "基本信息",
			"subcategory":     "新建信息",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"工程名称"},
			"field_rule_path": basicInfoRulePath,
		})

		assertSuccess(t, inner)
		t.Logf("  字段校验通过")
	})

	// ==================== 场景13：字段校验失败-缺少字段 ====================
	t.Run("字段校验失败-缺少字段", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":    2,
				"工程名称":  "测试工程2",
				"电压等级":  "110kV",
				"物料":    []any{},
				// 缺少：项目划分、工程阶段、定额规范、地区规范、工程性质等
			},
			"category":        "基本信息",
			"subcategory":     "新建信息",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"工程名称"},
			"field_rule_path": basicInfoRulePath,
		})

		assertError(t, inner)
		assert.Contains(t, inner["message"].(string), "字段校验失败")
		errs := inner["errors"].([]any)
		assert.GreaterOrEqual(t, len(errs), 1)
		hasMissing := false
		for _, e := range errs {
			if str, ok := e.(string); ok {
				if contains(str, "缺少字段") {
					hasMissing = true
					break
				}
			}
		}
		assert.True(t, hasMissing, "应包含缺少字段错误")
		t.Logf("  字段校验失败，错误数: %d", len(errs))
		for _, e := range errs {
			t.Logf("    - %v", e)
		}
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
				"物料":     []any{},
			},
			"category":        "基本信息",
			"subcategory":     "新建信息",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"工程名称"},
			"field_rule_path": basicInfoRulePath,
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
				"物料":   []any{},
			},
			"category":        "基本信息",
			"subcategory":     "新建信息",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"工程名称"},
			"field_rule_path": filepath.Join(outputDir, "not_exist.md"),
		})

		assertError(t, inner)
		assert.Contains(t, inner["message"].(string), "解析规则文件失败")
		t.Logf("  规则文件不存在检测成功: %s", inner["message"])
	})

	// ==================== 场景16：子类别在规则文件中不存在 ====================
	t.Run("子类别不存在于规则文件", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":   5,
				"工程名称": "测试工程5",
				"物料":   []any{},
			},
			"category":        "基本信息",
			"subcategory":     "不存在的子类别",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"工程名称"},
			"field_rule_path": basicInfoRulePath,
		})

		assertError(t, inner)
		assert.Contains(t, inner["message"].(string), "解析规则文件失败")
		t.Logf("  子类别不存在检测成功: %s", inner["message"])
	})
}

func TestWriteObj_读取模式场景(t *testing.T) {
	ct, scriptsDir, outputDir := setupWriteObjTest(t)
	scriptPath := filepath.Join(scriptsDir, "write_obj.lua")
	objDraftsPath := filepath.Join(outputDir, "read_test.yaml")

	// 先写入一个对象用于读取测试
	_ = callWriteObj(t, ct, scriptPath, map[string]any{
		"obj_data": map[string]any{
			"序号":     1,
			"工程名称":   "测试工程",
			"电压等级":   "110kV",
			"工程性质":   "新建",
			"物料":     []any{},
			"来源位置": map[string]any{
				"工程名称": "doc.md::章节|1-50→测试工程",
			},
		},
		"category":        "基本信息",
		"subcategory":     "新建信息",
		"obj_drafts_path": objDraftsPath,
		"key_fields":      []string{"工程名称"},
	})

	// ==================== 场景17：读取模式-找到对象 ====================
	t.Run("读取模式-找到对象", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"工程名称": "测试工程",
			},
			"category":        "基本信息",
			"subcategory":     "新建信息",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"工程名称"},
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
			"category":        "基本信息",
			"subcategory":     "新建信息",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"工程名称"},
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
			"category":        "基本信息",
			"subcategory":     "新建信息",
			"obj_drafts_path": filepath.Join(outputDir, "not_exist.yaml"),
			"key_fields":      []string{"工程名称"},
			"action":          "read",
		})

		assertError(t, inner)
		assert.Contains(t, inner["message"].(string), "读取 obj_drafts.yaml 失败")
		t.Logf("  文件不存在检测成功: %s", inner["message"])
	})
}

func TestWriteObj_多轮次场景(t *testing.T) {
	ct, scriptsDir, outputDir := setupWriteObjTest(t)
	scriptPath := filepath.Join(scriptsDir, "write_obj.lua")
	objDraftsPath := filepath.Join(outputDir, "round_test.yaml")

	// ==================== 场景20：round=1 首次写入 ====================
	t.Run("round=1首次写入", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":    1,
				"工程名称":  "测试工程",
				"电压等级":  "110kV",
				"物料":    []any{},
			},
			"category":        "基本信息",
			"subcategory":     "新建信息",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"工程名称"},
			"round":           1,
		})

		assertSuccess(t, inner)
		t.Logf("  round=1 写入成功")
	})

	// ==================== 场景21：round=2 第二轮写入 ====================
	t.Run("round=2第二轮写入", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":    1,
				"工程名称":  "测试工程",
				"电压等级":  "220kV",
				"物料":    []any{},
			},
			"category":        "基本信息",
			"subcategory":     "新建信息",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"工程名称"},
			"round":           2,
		})

		assertSuccess(t, inner)
		t.Logf("  round=2 写入成功")
	})

	// ==================== 场景22：round=2 重复写入（当前 validation_history 未写入，所以会成功） ====================
	t.Run("round=2重复写入-当前允许", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":    1,
				"工程名称":  "测试工程",
				"电压等级":  "220kV",
				"物料":    []any{},
			},
			"category":        "基本信息",
			"subcategory":     "新建信息",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"工程名称"},
			"round":           2,
		})

		// 注意：当前脚本中 validation_history 未被写入，所以 round 检测不会触发
		// 此处验证脚本不会报错，后续如需严格限制需补充 validation_history 写入逻辑
		assertSuccess(t, inner)
		t.Logf("  round=2 重复写入（当前允许，因 validation_history 未写入）")
	})
}

func TestWriteObj_真实数据场景(t *testing.T) {
	ct, scriptsDir, outputDir := setupWriteObjTest(t)
	scriptPath := filepath.Join(scriptsDir, "write_obj.lua")
	objDraftsPath := filepath.Join(outputDir, "real_data_test.yaml")

	// ==================== 场景23：使用真实 output_draft.yaml 中的新建信息数据 ====================
	t.Run("真实数据-新建信息", func(t *testing.T) {
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
				"物料":     []any{},
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
			"category":        "基本信息",
			"subcategory":     "新建信息",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"工程名称"},
		})

		assertSuccess(t, inner)
		t.Logf("  真实数据写入成功")
	})

	// ==================== 场景24：使用真实 output_draft.yaml 中的编制人信息数据 ====================
	t.Run("真实数据-编制人信息", func(t *testing.T) {
		inner := callWriteObj(t, ct, scriptPath, map[string]any{
			"obj_data": map[string]any{
				"序号":     1,
				"编制人":    "",
				"编制单位":   "邢台电力勘测设计院有限责任公司",
				"资质证书":   "工程设计(乙级)：A213006018；工程勘察(乙级)：B213012838；工程咨询(乙级)：032022010104",
				"工程地址":   "邢台市南和县",
				"编制时间":   "二〇二四年六月",
				"物料":     []any{},
				"来源位置": map[string]any{
					"编制单位": "施工组织设计大纲.md::1.2各专业设计简介|1-50→邢台电力勘测设计院有限责任公司",
					"工程地址": "施工组织设计大纲.md::1.2各专业设计简介|1-50→邢台市南和县",
					"资质证书": "初设说明书.md::封面|1-10→工程设计(乙级)：A213006018；工程勘察(乙级)：B213012838；工程咨询(乙级)：032022010104",
					"编制时间": "初设说明书.md::封面|1-10→二〇二四年六月",
				},
			},
			"category":        "基本信息",
			"subcategory":     "编制人信息",
			"obj_drafts_path": objDraftsPath,
			"key_fields":      []string{"序号"},
		})

		assertSuccess(t, inner)
		t.Logf("  编制人信息写入成功")
	})

	// ==================== 场景25：验证最终文件包含所有子类别 ====================
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

// contains 辅助函数，判断字符串是否包含子串
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
