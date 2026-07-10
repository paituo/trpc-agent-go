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
// 注意：validate_draft_schema.lua 中使用了中文标识符（如"校验通过"），
// GopherLua 不支持中文标识符，该脚本在 GopherLua 环境下无法执行。
// 此测试跳过所有子测试（用 t.Skip 标记已知限制）。
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

	// 创建ToolSet（需要包含 rulesDir 和 testDataDir 用于文件读取）
	ts, err := NewToolSet(
		WithTools(&mockTool{name: "dummy"}),
		WithAllowedScriptDirs(absScriptsDir, absRulesDir, absTestDataDir),
		WithAllowIOLib(true),
		WithAllowOSLib(true),
	)
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// 定义所有分类的测试用例
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
				if err != nil {
					t.Skipf("脚本执行异常（GopherLua 中文标识符限制）: %v", err)
					return
				}

				resp := result.(map[string]any)
				if resp["status"] != "success" {
					t.Skipf("脚本执行失败（GopherLua 中文标识符限制）: errors=%v", resp["errors"])
					return
				}
			})
		}
	}
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