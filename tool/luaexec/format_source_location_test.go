package luaexec

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatSourceLocation(t *testing.T) {
	baseDir := filepath.Join("..", "..", "bowei-openclaw")
	scriptsDir := filepath.Join(baseDir, "skills", "single-extractor", "scripts")
	absScriptsDir, err := filepath.Abs(scriptsDir)
	require.NoError(t, err)
	scriptPath := filepath.Join(absScriptsDir, "format_source_location.lua")

	ts, err := NewToolSet(
		WithTools(&mockTool{name: "dummy"}),
		WithAllowedScriptDirs(absScriptsDir),
		WithAllowIOLib(true),
		WithAllowOSLib(true),
		func(c *Config) { c.AllowFSLib = true },
	)
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	tests := []struct {
		name        string
		args        map[string]any
		expected    string
		expectError bool
	}{
		{
			name: "direct-直提格式",
			args: map[string]any{
				"source_type":     "direct",
				"doc_name":        "初设说明书",
				"chapter":         "10.1",
				"line_range":      "L100",
				"extracted_value": "35kV",
			},
			expected: "初设说明书::10.1|L100→35kV",
		},
		{
			name: "direct-无提取值",
			args: map[string]any{
				"source_type": "direct",
				"doc_name":    "初设说明书",
				"chapter":     "10.1",
				"line_range":  "L100-L120",
			},
			expected: "初设说明书::10.1|L100-L120",
		},
		{
			name: "inference-推理格式",
			args: map[string]any{
				"source_type": "inference",
				"reason":      "根据杆塔型号推断塔型",
			},
			expected: "推理→根据杆塔型号推断塔型",
		},
		{
			name: "inference-无推理说明用提取值",
			args: map[string]any{
				"source_type":     "inference",
				"extracted_value": "直线塔",
			},
			expected: "推理→直线塔",
		},
		{
			name: "default-默认格式",
			args: map[string]any{
				"source_type": "default",
				"reason":      "默认为false",
			},
			expected: "默认→默认为false",
		},
		{
			name: "calculation-计算格式",
			args: map[string]any{
				"source_type": "calculation",
				"reason":      "基数×每基塔重",
			},
			expected: "计算→基数×每基塔重",
		},
		{
			name: "not_provided-未提供格式",
			args: map[string]any{
				"source_type": "not_provided",
				"reason":      "骨架节点状态为not_found",
			},
			expected: "未提供→骨架节点状态为not_found",
		},
		{
			name: "参数缺失-无source_type",
			args: map[string]any{
				"doc_name": "初设说明书",
			},
			expectError: true,
		},
		{
			name: "无效source_type",
			args: map[string]any{
				"source_type": "invalid_type",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			argsJSON, err := json.Marshal(map[string]any{
				"script_path": scriptPath,
				"args":        tt.args,
			})
			require.NoError(t, err)

			result, err := ct.Call(context.Background(), argsJSON)
			require.NoError(t, err)

			resultMap, ok := result.(map[string]any)
			require.True(t, ok)

			// WEB API 格式: { code, data, message, errors }
			inner, ok := resultMap["result"].(map[string]any)
			require.True(t, ok, "result 应为 map")

			if tt.expectError {
				code, _ := inner["code"].(float64)
				assert.Equal(t, float64(1), code, "应返回 code=1")
				t.Logf("✅ %s: 正确返回 error (code=1)", tt.name)
			} else {
				code, _ := inner["code"].(float64)
				assert.Equal(t, float64(0), code, "应返回 code=0")
				data, ok := inner["data"].(map[string]any)
				require.True(t, ok, "data 应为 map")
				location, ok := data["location"].(string)
				require.True(t, ok, "location 应为 string")
				assert.Equal(t, tt.expected, location, "来源位置格式应匹配")
				t.Logf("✅ %s: %s", tt.name, location)
			}
		})
	}
}
