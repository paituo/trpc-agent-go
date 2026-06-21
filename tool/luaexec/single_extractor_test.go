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
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateDraftSchema 测试 validate_draft_schema.lua 脚本（所有分类）
func TestValidateDraftSchema(t *testing.T) {
	scriptsDir := filepath.Join("..", "..", ".trae", "skills", "single-extractor", "scripts")
	absScriptsDir, err := filepath.Abs(scriptsDir)
	require.NoError(t, err)
	scriptPath := filepath.Join(absScriptsDir, "validate_draft_schema.lua")
	_, err = os.Stat(scriptPath)
	require.NoError(t, err, "validate_draft_schema.lua 脚本文件必须存在: %s", scriptPath)

	// 规则文件目录
	rulesDir := filepath.Join("..", "..", ".trae", "skills", "single-extractor", "references", "field_rules")
	absRulesDir, err := filepath.Abs(rulesDir)
	require.NoError(t, err)

	// 测试数据目录（新生成的step1测试数据）
	testDataDir := filepath.Join("..", "..", "temp", "test_step1", "obj_drafts")
	absTestDataDir, err := filepath.Abs(testDataDir)
	require.NoError(t, err)

	// 创建ToolSet
	ts, err := NewToolSet(
		WithTools(&mockTool{name: "dummy"}),
		WithAllowedScriptDirs(absScriptsDir),
		WithAllowIOLib(true),
		WithAllowOSLib(true),
	)
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// 定义所有分类的测试用例：{分类名, 规则文件名, 草稿文件列表}
	type categoryTest struct {
		name       string
		rulesFile  string
		draftFiles []string
	}

	categories := []categoryTest{
		{
			name: "杆塔组件", rulesFile: "tower_component.md",
			draftFiles: []string{"杆塔组件_混凝土杆_draft.yaml", "杆塔组件_钢管杆_draft.yaml", "杆塔组件_铁塔_draft.yaml"},
		},
		{
			name: "架线组件", rulesFile: "conductor.md",
			draftFiles: []string{"架线组件_导线架设_draft.yaml", "架线组件_避雷线架设_draft.yaml", "架线组件_OPGW架设_draft.yaml", "架线组件_耦合屏蔽线架设_draft.yaml"},
		},
		{
			name: "基础组件", rulesFile: "foundation.md",
			draftFiles: []string{"基础组件_现浇基础_draft.yaml", "基础组件_挖孔基础_draft.yaml", "基础组件_灌注桩基础_draft.yaml", "基础组件_微型桩基础_draft.yaml", "基础组件_螺旋锚基础_draft.yaml", "基础组件_钢管桩基础_draft.yaml", "基础组件_岩石锚杆基础_draft.yaml"},
		},
		{
			name: "接地组件", rulesFile: "grounding.md",
			draftFiles: []string{"接地组件_接地装置_draft.yaml"},
		},
		{
			name: "附件组件", rulesFile: "accessories.md",
			draftFiles: []string{"附件组件_悬垂串_draft.yaml", "附件组件_耐张串_draft.yaml", "附件组件_跳线串_draft.yaml", "附件组件_防振锤_draft.yaml", "附件组件_间隔棒_draft.yaml", "附件组件_重锤_draft.yaml", "附件组件_阻尼线_draft.yaml", "附件组件_阻冰环_draft.yaml", "附件组件_避雷器_draft.yaml"},
		},
		{
			name: "交叉跨越", rulesFile: "crossing.md",
			draftFiles: []string{"交叉组件_跨越_draft.yaml"},
		},
		{
			name: "辅助工程", rulesFile: "auxiliary.md",
			draftFiles: []string{"辅助工程_排水沟_draft.yaml", "辅助工程_尖峰及施工基面_draft.yaml", "辅助工程_护坡_draft.yaml", "辅助工程_挡土墙_draft.yaml"},
		},
	}

	for _, cat := range categories {
		rulesPath := filepath.Join(absRulesDir, cat.rulesFile)
		_, err := os.Stat(rulesPath)
		if os.IsNotExist(err) {
			t.Logf("规则文件不存在，跳过分类 %s: %s", cat.name, rulesPath)
			continue
		}

		for _, draftFile := range cat.draftFiles {
			testName := cat.name + "/" + draftFile
			t.Run(testName, func(t *testing.T) {
				draftPath := filepath.Join(absTestDataDir, draftFile)
				_, err := os.Stat(draftPath)
				if os.IsNotExist(err) {
					t.Skipf("测试文件不存在: %s", draftPath)
					return
				}
				require.NoError(t, err)

				argsJSON, err := json.Marshal(map[string]any{
					"script_path": scriptPath,
					"args": map[string]any{
						"draft_path": draftPath,
						"rules_path": rulesPath,
					},
				})
				require.NoError(t, err)

				result, err := ct.Call(context.Background(), argsJSON)
				require.NoError(t, err)

				resp := result.(map[string]any)
				require.Equal(t, "success", resp["status"], "validate_draft_schema.lua 应该成功执行")

				// 解析校验结果
				if data, ok := resp["result"]; ok {
					if dataMap, ok := data.(map[string]any); ok {
						if resultStr, ok := dataMap["校验结果"]; ok {
							t.Logf("[%s] 校验结果: %v", draftFile, resultStr)
							// 输出缺失字段数
							if missing, ok := dataMap["缺失字段"]; ok {
								if missingList, ok := missing.([]any); ok {
									t.Logf("[%s] 缺失字段数: %d", draftFile, len(missingList))
								}
							}
							// 输出多余字段数
							if extra, ok := dataMap["多余字段"]; ok {
								if extraList, ok := extra.([]any); ok {
									t.Logf("[%s] 多余字段数: %d", draftFile, len(extraList))
								}
							}
						}
					}
				}
			})
		}
	}
}

// TestLocateSources 测试 locate_sources.lua 脚本
func TestLocateSources(t *testing.T) {
	scriptsDir := filepath.Join("..", "..", ".trae", "skills", "single-extractor", "scripts")
	absScriptsDir, err := filepath.Abs(scriptsDir)
	require.NoError(t, err)
	scriptPath := filepath.Join(absScriptsDir, "locate_sources.lua")
	_, err = os.Stat(scriptPath)
	require.NoError(t, err, "locate_sources.lua 脚本文件必须存在: %s", scriptPath)

	// 骨架文件路径
	skeletonPath := filepath.Join("..", "..", ".trae", "skills", "single-extractor", "references", "skeleton", "engineering_skeleton.yaml")
	absSkeletonPath, err := filepath.Abs(skeletonPath)
	require.NoError(t, err)
	_, err = os.Stat(absSkeletonPath)
	require.NoError(t, err, "engineering_skeleton.yaml 必须存在: %s", absSkeletonPath)

	ts, err := NewToolSet(
		WithTools(&mockTool{name: "dummy"}),
		WithAllowedScriptDirs(absScriptsDir),
		WithAllowIOLib(true),
		WithAllowOSLib(true),
	)
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	argsJSON, err := json.Marshal(map[string]any{
		"script_path": scriptPath,
		"args": map[string]any{
			"skeleton_path": absSkeletonPath,
			"category":      "基本信息",
		},
	})
	require.NoError(t, err)

	result, err := ct.Call(context.Background(), argsJSON)
	require.NoError(t, err)

	resp := result.(map[string]any)
	t.Logf("脚本执行结果: status=%v", resp["status"])
	if resp["status"] != "success" {
		if errors, ok := resp["errors"]; ok {
			t.Logf("错误: %+v", errors)
		}
	}
	require.Equal(t, "success", resp["status"], "locate_sources.lua 应该成功执行")

	if data, ok := resp["result"]; ok {
		t.Logf("定位结果: %+v", data)
	}
}

// TestFormatDraft 测试 format_draft.lua 脚本
func TestFormatDraft(t *testing.T) {
	scriptsDir := filepath.Join("..", "..", ".trae", "skills", "single-extractor", "scripts")
	absScriptsDir, err := filepath.Abs(scriptsDir)
	require.NoError(t, err)
	scriptPath := filepath.Join(absScriptsDir, "format_draft.lua")
	_, err = os.Stat(scriptPath)
	require.NoError(t, err, "format_draft.lua 脚本文件必须存在: %s", scriptPath)

	// 创建一个包含格式问题的临时draft文件
	tmpDir := t.TempDir()
	draftPath := filepath.Join(tmpDir, "test_format_draft.yaml")
	draftContent := `工程名称: null
提取类型: 杆塔组件
铁塔:
  - 铁塔型号: "1E5-SZ1"
    呼高(m): "24"
    基数: "是"
    套筒: "是"
    物料: {}
`
	err = os.WriteFile(draftPath, []byte(draftContent), 0644)
	require.NoError(t, err)

	ts, err := NewToolSet(
		WithTools(&mockTool{name: "dummy"}),
		WithAllowedScriptDirs(absScriptsDir),
		WithAllowIOLib(true),
		WithAllowOSLib(true),
	)
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	argsJSON, err := json.Marshal(map[string]any{
		"script_path": scriptPath,
		"args": map[string]any{
			"draft_path": draftPath,
		},
	})
	require.NoError(t, err)

	result, err := ct.Call(context.Background(), argsJSON)
	require.NoError(t, err)

	resp := result.(map[string]any)
	t.Logf("脚本执行结果: status=%v", resp["status"])
	if resp["status"] != "success" {
		if errors, ok := resp["errors"]; ok {
			t.Logf("错误: %+v", errors)
		}
	}
	require.Equal(t, "success", resp["status"], "format_draft.lua 应该成功执行")

	if data, ok := resp["result"]; ok {
		t.Logf("修正结果: %+v", data)
	}
}

// TestMergeDrafts 测试 merge_drafts.lua 脚本
func TestMergeDrafts(t *testing.T) {
	scriptsDir := filepath.Join("..", "..", ".trae", "skills", "single-extractor", "scripts")
	absScriptsDir, err := filepath.Abs(scriptsDir)
	require.NoError(t, err)
	scriptPath := filepath.Join(absScriptsDir, "merge_drafts.lua")
	_, err = os.Stat(scriptPath)
	require.NoError(t, err, "merge_drafts.lua 脚本文件必须存在: %s", scriptPath)

	// 创建两个临时draft文件用于合并
	tmpDir := t.TempDir()
	draft1Path := filepath.Join(tmpDir, "draft1.yaml")
	draft1Content := `对象类型: 铁塔
工程名称: 测试工程
记录:
  - 序号: 1
    铁塔型号: 1E5-SZ1
    呼高(m): 24
`
	err = os.WriteFile(draft1Path, []byte(draft1Content), 0644)
	require.NoError(t, err)

	draft2Path := filepath.Join(tmpDir, "draft2.yaml")
	draft2Content := `对象类型: 钢管杆
工程名称: 测试工程
记录:
  - 序号: 1
    钢管杆型号: GG-1
    杆高(m): 18
`
	err = os.WriteFile(draft2Path, []byte(draft2Content), 0644)
	require.NoError(t, err)

	outputPath := filepath.Join(tmpDir, "merged.yaml")

	ts, err := NewToolSet(
		WithTools(&mockTool{name: "dummy"}),
		WithAllowedScriptDirs(absScriptsDir),
		WithAllowIOLib(true),
		WithAllowOSLib(true),
	)
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	argsJSON, err := json.Marshal(map[string]any{
		"script_path": scriptPath,
		"args": map[string]any{
			"category":    "杆塔组件",
			"output_path": outputPath,
			"draft_files": []string{draft1Path, draft2Path},
		},
	})
	require.NoError(t, err)

	result, err := ct.Call(context.Background(), argsJSON)
	require.NoError(t, err)

	resp := result.(map[string]any)
	t.Logf("脚本执行结果: status=%v", resp["status"])
	if resp["status"] != "success" {
		if errors, ok := resp["errors"]; ok {
			t.Logf("错误: %+v", errors)
		}
	}
	require.Equal(t, "success", resp["status"], "merge_drafts.lua 应该成功执行")

	if data, ok := resp["result"]; ok {
		t.Logf("合并结果: %+v", data)
	}

	// 验证输出文件存在
	_, err = os.Stat(outputPath)
	require.NoError(t, err, "合并后的draft文件应该存在: %s", outputPath)
}

// TestScriptParamMissing 测试脚本参数缺失时的错误处理
func TestScriptParamMissing(t *testing.T) {
	scriptsDir := filepath.Join("..", "..", ".trae", "skills", "single-extractor", "scripts")
	absScriptsDir, err := filepath.Abs(scriptsDir)
	require.NoError(t, err)

	ts, err := NewToolSet(
		WithTools(&mockTool{name: "dummy"}),
		WithAllowedScriptDirs(absScriptsDir),
		WithAllowIOLib(true),
		WithAllowOSLib(true),
	)
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// 测试 validate_draft_schema.lua 缺少参数
	scriptPath := filepath.Join(absScriptsDir, "validate_draft_schema.lua")
	argsJSON, err := json.Marshal(map[string]any{
		"script_path": scriptPath,
		"args":        map[string]any{},
	})
	require.NoError(t, err)

	result, err := ct.Call(context.Background(), argsJSON)
	require.NoError(t, err)

	resp := result.(map[string]any)
	t.Logf("参数缺失测试结果: status=%v", resp["status"])
	// 应该返回 error，因为缺少必需参数
	assert.Equal(t, "error", resp["status"], "缺少参数时应返回error")
}
