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

// TestExtractOutlineWithRealFile 使用实际源文件测试extract_outline.lua脚本
func TestExtractOutlineWithRealFile(t *testing.T) {
	// 找到技能脚本路径
	scriptsDir := filepath.Join("..", "..", ".trae", "skills", "skeleton-mounter", "scripts")
	absScriptsDir, err := filepath.Abs(scriptsDir)
	require.NoError(t, err)
	scriptPath := filepath.Join(absScriptsDir, "extract_outline.lua")
	_, err = os.Stat(scriptPath)
	require.NoError(t, err, "extract_outline.lua 脚本文件必须存在: %s", scriptPath)

	// 源文件路径
	sourceDir := filepath.Join("..", "..", "源文件")
	absSourceDir, err := filepath.Abs(sourceDir)
	require.NoError(t, err)

	// 选择一个较小的源文件进行测试
	docName := "规划协议.md"
	docPath := filepath.Join(absSourceDir, "02.协议", "01-规划、国土协议", docName)
	_, err = os.Stat(docPath)
	require.NoError(t, err, "源文件必须存在: %s", docPath)

	// 临时输出文件
	outputDir := t.TempDir()
	outputPath := filepath.Join(outputDir, "output.yaml")

	// 创建ToolSet（需要IOLib以支持文件读写，需要OSLib以支持os.time()）
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

	// 调用extract_outline.lua脚本
	argsJSON, err := json.Marshal(map[string]any{
		"script_path": scriptPath,
		"args": map[string]any{
			"doc_path":          docPath,
			"doc_name":          docName,
			"output_path":       outputPath,
			"encoding":          "auto",
			"max_output_level":  2,
			"max_scan_level":    3,
			"include_tables":    true,
			"include_images":    true,
			"preview_table_rows": 3,
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
		if diagnostics, ok := resp["diagnostics"]; ok {
			t.Logf("诊断: %+v", diagnostics)
		}
	}

	// 检查执行状态
	require.Equal(t, "success", resp["status"], "extract_outline.lua 应该成功执行")

	// 验证输出文件存在
	_, err = os.Stat(outputPath)
	require.NoError(t, err, "输出YAML文件应该存在: %s", outputPath)

	// 验证输出文件内容
	data, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	content := string(data)
	t.Logf("输出YAML大小: %d 字节", len(content))
	require.NotEmpty(t, content, "输出YAML内容不应为空")

	// 验证输出包含关键信息
	assert.Contains(t, content, "doc_name")
	assert.Contains(t, content, docName)
	assert.Contains(t, content, "fragments")
	assert.Contains(t, content, "stats")

	// 验证有片段被正确提取
	assert.Contains(t, content, "heading_count")
	assert.NotContains(t, content, "invalid UTF-8", "输出不应包含UTF-8错误")

	t.Logf("✓ extract_outline.lua 使用实际源文件测试通过!")
	t.Logf("输出文件: %s", outputPath)
}

// TestBatchExtractOutlineWithRealFiles 使用多个实际源文件测试batch_extract_outline.lua脚本
func TestBatchExtractOutlineWithRealFiles(t *testing.T) {
	scriptsDir := filepath.Join("..", "..", ".trae", "skills", "skeleton-mounter", "scripts")
	absScriptsDir, err := filepath.Abs(scriptsDir)
	require.NoError(t, err)
	scriptPath := filepath.Join(absScriptsDir, "batch_extract_outline.lua")
	_, err = os.Stat(scriptPath)
	require.NoError(t, err, "batch_extract_outline.lua 脚本文件必须存在")

	// 源文件路径
	sourceDir := filepath.Join("..", "..", "源文件")
	absSourceDir, err := filepath.Abs(sourceDir)
	require.NoError(t, err)

	// 选择5个源文件进行批量测试
	docPaths := []map[string]any{
		{
			"doc_path": filepath.Join(absSourceDir, "02.协议", "01-规划、国土协议", "规划协议.md"),
			"doc_name": "规划协议.md",
		},
		{
			"doc_path": filepath.Join(absSourceDir, "02.协议", "01-规划、国土协议", "国土协议.md"),
			"doc_name": "国土协议.md",
		},
		{
			"doc_path": filepath.Join(absSourceDir, "03.专题部分", "01-勘测任务书", "地勘任务书.md"),
			"doc_name": "地勘任务书.md",
		},
		{
			"doc_path": filepath.Join(absSourceDir, "03.专题部分", "01-勘测任务书", "测量任务书.md"),
			"doc_name": "测量任务书.md",
		},
		{
			"doc_path": filepath.Join(absSourceDir, "OPPC光缆.md"),
			"doc_name": "OPPC光缆.md",
		},
	}

	// 确保所有源文件存在
	for _, entry := range docPaths {
		path := entry["doc_path"].(string)
		_, err := os.Stat(path)
		require.NoError(t, err, "源文件必须存在: %s", path)
	}

	outputDir := t.TempDir()

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
			"doc_paths":         docPaths,
			"batch_output_dir":  outputDir,
			"encoding":          "auto",
			"max_output_level":  2,
			"max_scan_level":    3,
			"include_tables":    true,
			"include_images":    false,
			"preview_table_rows": 3,
		},
	})
	require.NoError(t, err)

	result, err := ct.Call(context.Background(), argsJSON)
	require.NoError(t, err)

	resp := result.(map[string]any)
	status, _ := resp["status"].(string)
	t.Logf("批量执行结果: status=%v", status)

	if status == "failed" {
		if errors, ok := resp["errors"]; ok {
			t.Logf("错误: %+v", errors)
		}
		if diagnostics, ok := resp["diagnostics"]; ok {
			t.Logf("诊断: %+v", diagnostics)
		}
	}

	// 批量处理允许部分成功（partial）
	require.NotEqual(t, "failed", status, "批量执行不应完全失败")

	// 验证输出文件存在
	for _, entry := range docPaths {
		name := entry["doc_name"].(string)
		safeName := sanitizeFileName(name)
		outputPath := filepath.Join(outputDir, safeName+"_fragments.yaml")
		_, err := os.Stat(outputPath)
		if err == nil {
			data, _ := os.ReadFile(outputPath)
			assert.NotEmpty(t, data, "输出文件不应为空: %s", outputPath)
			assert.NotContains(t, string(data), "invalid UTF-8", "输出不应包含UTF-8错误: %s", outputPath)
			t.Logf("✓ %s 处理成功", name)
		} else {
			t.Logf("⚠ %s 输出文件未生成（可能该文件无有效内容）: %s", name, outputPath)
		}
	}

	t.Logf("✓ batch_extract_outline.lua 批量测试通过！输出目录: %s", outputDir)
}

// sanitizeFileName 模拟Lua中的文件名安全处理
func sanitizeFileName(name string) string {
	result := make([]byte, 0, len(name))
	for _, b := range []byte(name) {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
			(b >= '0' && b <= '9') || b == '_' || b == '-' || b == '.' {
			result = append(result, b)
		} else {
			result = append(result, '_')
		}
	}
	return string(result)
}

// TestScripts_EnsureNoReModule 测试所有技能脚本不再使用re模块
func TestScripts_EnsureNoReModule(t *testing.T) {
	skillDirs := []string{
		filepath.Join("..", "..", ".trae", "skills", "skeleton-mounter", "scripts"),
		filepath.Join("..", "..", ".trae", "skills", "single-extractor", "scripts"),
	}

	for _, dir := range skillDirs {
		absDir, err := filepath.Abs(dir)
		require.NoError(t, err)

		// 使用Go读取目录
		entries, err := os.ReadDir(absDir)
		if err != nil {
			t.Skipf("跳过的目录: %s (%v)", absDir, err)
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".lua" {
				continue
			}
			path := filepath.Join(absDir, entry.Name())
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			content := string(data)

			// 检查是否还在使用re模块
			assert.NotContains(t, content, "require(\"re\")", "脚本不应再require re模块: %s", entry.Name())
		}
	}
}