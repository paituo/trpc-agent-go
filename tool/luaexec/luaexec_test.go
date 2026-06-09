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
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// osWriteFile is an alias for os.WriteFile to avoid name collision.
var osWriteFile = os.WriteFile

func TestNewToolSet(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	require.NotNil(t, ts)

	assert.Equal(t, "luaexec", ts.Name())

	tools := ts.Tools(context.Background())
	require.Len(t, tools, 1)

	decl := tools[0].Declaration()
	assert.Equal(t, "lua_exec", decl.Name)
	assert.Contains(t, decl.Description, "Lua 5.1")

	require.NoError(t, ts.Close())
}

func TestNewToolSet_NoTools(t *testing.T) {
	_, err := NewToolSet()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no tool source provided")
}

func TestNewToolSet_ToolDenied(t *testing.T) {
	// When tool module is denied, empty Tools is OK.
	ts, err := NewToolSet(WithDeniedModules("tool"))
	require.NoError(t, err)
	require.NotNil(t, ts)
	require.NoError(t, ts.Close())
}

func TestLuaExec_SimpleReturn(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": "return 1 + 1",
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, float64(2), resp["result"])
}

func TestLuaExec_StringReturn(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `return "hello world"`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "hello world", resp["result"])
}

func TestLuaExec_TableReturn(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `local t = {name = "test", value = 42}; return t`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	tbl := resp["result"].(map[string]any)
	assert.Equal(t, "test", tbl["name"])
	assert.Equal(t, float64(42), tbl["value"])
}

func TestToolsProvider_ReAndJsonAvailable(t *testing.T) {
	// Simulate OpenClaw integration path: ToolsProvider instead of static Tools.
	provider := func(ctx context.Context) []tool.Tool {
		return []tool.Tool{&mockTool{name: "some_tool"}}
	}
	ts, err := NewToolSet(WithToolsProvider(provider))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// Test re module availability.
	args, _ := json.Marshal(map[string]any{
		"script": `return re.match("hello 123", "\\d+")`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	matches := resp["result"].([]any)
	assert.Equal(t, "123", matches[0])

	// Test json module availability.
	args2, _ := json.Marshal(map[string]any{
		"script": `return json.decode('{"key":"value"}')`,
	})
	result2, err := ct.Call(context.Background(), args2)
	require.NoError(t, err)
	resp2 := result2.(map[string]any)
	assert.Equal(t, "success", resp2["status"])
	tbl := resp2["result"].(map[string]any)
	assert.Equal(t, "value", tbl["key"])
}

func TestNewToolSet_WithToolsProvider(t *testing.T) {
	provider := func(ctx context.Context) []tool.Tool {
		return []tool.Tool{&mockTool{name: "dynamic_tool"}}
	}
	ts, err := NewToolSet(WithToolsProvider(provider))
	require.NoError(t, err)
	require.NotNil(t, ts)
	require.NoError(t, ts.Close())
}

func TestNewToolSet_ToolsProviderSufficient(t *testing.T) {
	// ToolsProvider alone is sufficient; no need for static Tools.
	provider := func(ctx context.Context) []tool.Tool {
		return []tool.Tool{&mockTool{name: "dynamic_tool"}}
	}
	ts, err := NewToolSet(WithToolsProvider(provider))
	require.NoError(t, err)
	require.NotNil(t, ts)
	require.NoError(t, ts.Close())
}

func TestToolsProvider_ResolvesInCall(t *testing.T) {
	resolved := false
	provider := func(ctx context.Context) []tool.Tool {
		resolved = true
		return []tool.Tool{&mockTool{name: "resolved_tool"}}
	}
	ts, err := NewToolSet(WithToolsProvider(provider))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `local names = tool.list(); return names`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	assert.True(t, resolved, "ToolsProvider should be resolved during Call()")

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	names := resp["result"].([]any)
	assert.Contains(t, names, "resolved_tool")
}

func TestToolsProvider_NilReturn(t *testing.T) {
	provider := func(ctx context.Context) []tool.Tool {
		return nil
	}
	ts, err := NewToolSet(WithToolsProvider(provider))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// When ToolsProvider returns nil, tool module is not registered.
	// type(tool) returns "nil" string in Lua.
	args, _ := json.Marshal(map[string]any{
		"script": `return type(tool)`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	// Lua type() returns "nil" string for nil values
	assert.Equal(t, "nil", resp["result"])
}

func TestToolsProvider_PreferOverStaticTools(t *testing.T) {
	staticTool := &mockTool{name: "static_tool"}
	dynamicTool := &mockTool{name: "dynamic_tool"}
	provider := func(ctx context.Context) []tool.Tool {
		return []tool.Tool{dynamicTool}
	}
	ts, err := NewToolSet(
		WithTools(staticTool),
		WithToolsProvider(provider),
	)
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `local names = tool.list(); return names`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	names := resp["result"].([]any)
	// ToolsProvider result replaces static Tools
	assert.Contains(t, names, "dynamic_tool")
	assert.NotContains(t, names, "static_tool")
}

func TestWithMaxOutputLen(t *testing.T) {
	ts, err := NewToolSet(
		WithTools(&mockTool{name: "test_tool"}),
		WithMaxOutputLen(10),
	)
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `print("hello world this is a long string"); return nil`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	stdout := resp["stdout"].(string)
	assert.LessOrEqual(t, len(stdout), 13) // 10 + "..."\n
}

func TestWithMaxErrorLen(t *testing.T) {
	ts, err := NewToolSet(
		WithTools(&mockTool{name: "test_tool"}),
		WithMaxErrorLen(10),
	)
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `error("this is a very long error message that should be truncated")`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	errors := resp["errors"].([]LuaError)
	require.Len(t, errors, 1)
	assert.LessOrEqual(t, len(errors[0].Message), 13) // 10 + "..."
}

func TestRe_Match(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `local m = re.match("hello 123 world 456", "\\d+"); return m`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	matches := resp["result"].([]any)
	assert.Equal(t, "123", matches[0])
	assert.Equal(t, "456", matches[1])
}

func TestRe_Gsub(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `local s = re.gsub("hello 123", "\\d+", "NUM"); return s`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "hello NUM", resp["result"])
}

func TestRe_Matches(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `return #re.matches("hello 123 world 456", "\\d+")`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, float64(2), resp["result"])
}

func TestDeniedModules_Yaml(t *testing.T) {
	ts, err := NewToolSet(
		WithTools(&mockTool{name: "test_tool"}),
		WithDeniedModules("yaml"),
	)
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `yaml.decode("name: test")`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "error", resp["status"])
}

func TestAllowIOLib_True(t *testing.T) {
	ts, err := NewToolSet(
		WithTools(&mockTool{name: "test_tool"}),
		WithAllowIOLib(true),
	)
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// io.open should work.
	args, _ := json.Marshal(map[string]any{
		"script": `return type(io)`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "table", resp["result"])
}

func TestAllowIOLib_False(t *testing.T) {
	ts, err := NewToolSet(
		WithTools(&mockTool{name: "test_tool"}),
		WithAllowIOLib(false),
	)
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// io should not be available.
	args, _ := json.Marshal(map[string]any{
		"script": `return io`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, nil, resp["result"])
}

func TestAllowOSLib_True(t *testing.T) {
	ts, err := NewToolSet(
		WithTools(&mockTool{name: "test_tool"}),
		WithAllowOSLib(true),
	)
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// os.time should work.
	args, _ := json.Marshal(map[string]any{
		"script": `return type(os.time)`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "function", resp["result"])
}

func TestAllowOSLib_DangerousFunctionsRemoved(t *testing.T) {
	ts, err := NewToolSet(
		WithTools(&mockTool{name: "test_tool"}),
		WithAllowOSLib(true),
	)
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// os.execute should be nil.
	args, _ := json.Marshal(map[string]any{
		"script": `return os.execute`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, nil, resp["result"])
}

func TestYamlReadFile_AllowIO(t *testing.T) {
	// Create a temp YAML file.
	tmpFile := t.TempDir() + "\\test.yaml"
	require.NoError(t, osWriteFile(tmpFile, []byte("name: test\nvalue: 42\n"), 0644))

	ts, err := NewToolSet(
		WithTools(&mockTool{name: "test_tool"}),
		WithAllowIOLib(true),
	)
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": fmt.Sprintf(`local data = yaml.read_file("%s"); return data`, strings.ReplaceAll(tmpFile, "\\", "\\\\")),
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	tbl := resp["result"].(map[string]any)
	assert.Equal(t, "test", tbl["name"])
}

func TestYamlReadFile_DenyIO(t *testing.T) {
	ts, err := NewToolSet(
		WithTools(&mockTool{name: "test_tool"}),
		WithAllowIOLib(false),
	)
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// yaml.read_file should not exist when AllowIOLib=false.
	args, _ := json.Marshal(map[string]any{
		"script": `return yaml.read_file`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, nil, resp["result"])
}

func TestLuaExec_PrintOutput(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `print("hello"); return nil`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Contains(t, resp["stdout"], "hello")
}

func TestLuaExec_SyntaxError(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `if true then`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "error", resp["status"])
	errors := resp["errors"].([]LuaError)
	require.Len(t, errors, 1)
	assert.Equal(t, ErrTypeSyntax, errors[0].Type)
}

func TestLuaExec_RuntimeError(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `error("something went wrong")`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "error", resp["status"])
	errors := resp["errors"].([]LuaError)
	require.Len(t, errors, 1)
	assert.Equal(t, ErrTypeRuntime, errors[0].Type)
	assert.Contains(t, errors[0].Message, "something went wrong")
}

func TestLuaExec_Timeout(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}), WithDefaultTimeout(1))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `while true do end`,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := ct.Call(ctx, args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "error", resp["status"])
	errors := resp["errors"].([]LuaError)
	require.Len(t, errors, 1)
	assert.Equal(t, ErrTypeTimeout, errors[0].Type)
}

func TestLuaExec_EmptyScript(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": "",
	})

	_, err = ct.Call(context.Background(), args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "script is required")
}

func TestTruncateMessage(t *testing.T) {
	assert.Equal(t, "hello", truncateMessage("hello", 10))
	assert.Equal(t, "hel...", truncateMessage("hello world", 6))
	assert.Equal(t, "hello world", truncateMessage("hello world", 11))
}

// mockTool is a simple mock for testing.
type mockTool struct {
	name string
}

func (m *mockTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        m.name,
		Description: "mock tool",
		InputSchema: &tool.Schema{Type: "object"},
	}
}

func (m *mockTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	return map[string]any{"status": "ok"}, nil
}

func TestToolCall_Basic(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `local result, err = tool.call("test_tool", {}); return result`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	resultMap := resp["result"].(map[string]any)
	assert.Equal(t, "ok", resultMap["status"])
}

func TestToolCall_List(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `local names = tool.list(); return names`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	names := resp["result"].([]any)
	assert.Contains(t, names, "test_tool")
	// lua_exec and agent tools should not be in the list.
	for _, n := range names {
		assert.NotEqual(t, "lua_exec", n)
		assert.NotContains(t, n.(string), "agent")
	}
}

func TestToolCall_DeniedSelf(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `local result, err = tool.call("lua_exec", {script="return 1"}); return err`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	errMap := resp["result"].(map[string]any)
	assert.Equal(t, ErrTypeToolCall, errMap["type"])
}

func TestToolCall_AgentDenied(t *testing.T) {
	agentMock := &mockTool{name: "dynamic_agent"}
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}, agentMock))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// dynamic_agent should not appear in tool.list()
	args, _ := json.Marshal(map[string]any{
		"script": `local names = tool.list(); return names`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	names := resp["result"].([]any)
	for _, n := range names {
		assert.NotContains(t, n.(string), "agent")
	}
}

func TestYAML_DecodeEncode(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `
local data = yaml.decode("name: test\nvalue: 42")
return data
`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	tbl := resp["result"].(map[string]any)
	assert.Equal(t, "test", tbl["name"])
}

func TestYAML_ComplexDraftParsing(t *testing.T) {
	// Simulate a typical extraction draft YAML with nested objects and arrays.
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}), WithRegistrySize(4096))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	draftYAML := `对象:
  - 序号: 1
    基础类型: 现浇基础
    基础型号: DD21GS-J4-18
    来源位置:
      基础类型: ["设计总说明书::基础类型列表"]
      基础型号: ["设计总说明书::基础型号列表→推理：J4呼高对应DD21GS-J4-18"]
  - 序号: 2
    基础类型: 灌注桩
    基础型号: null
    来源位置:
      基础类型: ["设计总说明书::基础类型列表"]
`

	script := `
local data = yaml.decode(ARGS.yaml)
local count = 0
local types = {}
for _, obj in ipairs(data["对象"]) do
  count = count + 1
  table.insert(types, obj["基础类型"])
end
return {count = count, types = types}
`
	argsJSON, _ := json.Marshal(map[string]any{
		"script": script,
		"args":   map[string]any{"yaml": draftYAML},
	})

	result, err := ct.Call(context.Background(), argsJSON)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	r := resp["result"].(map[string]any)
	assert.Equal(t, float64(2), r["count"])
	types := r["types"].([]any)
	assert.Equal(t, "现浇基础", types[0])
	assert.Equal(t, "灌注桩", types[1])
}

func TestJSON_DecodeEncode(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `
local data = json.decode('{"name":"test","value":42}')
return data
`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	tbl := resp["result"].(map[string]any)
	assert.Equal(t, "test", tbl["name"])
	assert.Equal(t, float64(42), tbl["value"])
}

// structTool is a mock tool that returns a Go struct (not map[string]any).
// Used to test the JSON round-trip conversion in pushGoValue.
type structTool struct {
	name string
}

type testReadFileResponse struct {
	BaseDirectory string `json:"base_directory"`
	FileName      string `json:"file_name"`
	Contents      string `json:"contents"`
	Message       string `json:"message"`
}

func (s *structTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        s.name,
		Description: "struct-returning mock tool",
		InputSchema: &tool.Schema{Type: "object"},
	}
}

func (s *structTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	return &testReadFileResponse{
		BaseDirectory: ".",
		FileName:      "test.yaml",
		Contents:      "name: hello",
		Message:       "",
	}, nil
}

// structWithMarshalerTool returns a struct that implements json.Marshaler
// to test that marshalStructFull bypasses custom MarshalJSON.
type structWithMarshalerTool struct {
	name string
}

type testMCPResult struct {
	Content []map[string]string `json:"content"`
	Meta    map[string]string   `json:"meta"`
	IsError bool                `json:"is_error"`
}

// MarshalJSON simulates MCP's custom MarshalJSON that only serializes Content.
func (r *testMCPResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.Content)
}

func (s *structWithMarshalerTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        s.name,
		Description: "mcp-like mock tool with custom MarshalJSON",
		InputSchema: &tool.Schema{Type: "object"},
	}
}

func (s *structWithMarshalerTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	return &testMCPResult{
		Content: []map[string]string{{"type": "text", "text": "hello"}},
		Meta:    map[string]string{"source": "mcp"},
		IsError: false,
	}, nil
}

func TestPushGoValue_StructConversion(t *testing.T) {
	ts, err := NewToolSet(WithTools(&structTool{name: "fs_read_file"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// tool.call should return a Lua table with accessible fields.
	args, _ := json.Marshal(map[string]any{
		"script": `local r, err = tool.call("fs_read_file", {}); return r`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	resultMap := resp["result"].(map[string]any)
	assert.Equal(t, ".", resultMap["base_directory"])
	assert.Equal(t, "test.yaml", resultMap["file_name"])
	assert.Equal(t, "name: hello", resultMap["contents"])
}

func TestPushGoValue_MCPFullStruct(t *testing.T) {
	ts, err := NewToolSet(WithTools(&structWithMarshalerTool{name: "mcp_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// MCP tool should return full structure including Meta and IsError,
	// not just Content (which is what MarshalJSON would produce).
	args, _ := json.Marshal(map[string]any{
		"script": `local r, err = tool.call("mcp_tool", {}); return r`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	resultMap := resp["result"].(map[string]any)

	// Content should be accessible.
	content := resultMap["content"].([]any)
	require.Len(t, content, 1)
	contentItem := content[0].(map[string]any)
	assert.Equal(t, "text", contentItem["type"])
	assert.Equal(t, "hello", contentItem["text"])

	// Meta should be accessible (would be lost with standard json.Marshal).
	meta := resultMap["meta"].(map[string]any)
	assert.Equal(t, "mcp", meta["source"])

	// IsError should be accessible (would be lost with standard json.Marshal).
	assert.Equal(t, false, resultMap["is_error"])
}

func TestToolProxyCall_Basic(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// tool.test_tool({...}) proxy call should work.
	args, _ := json.Marshal(map[string]any{
		"script": `local r, err = tool.test_tool({}); return r`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	resultMap := resp["result"].(map[string]any)
	assert.Equal(t, "ok", resultMap["status"])
}

func TestToolProxyCall_StructReturn(t *testing.T) {
	ts, err := NewToolSet(WithTools(&structTool{name: "fs_read_file"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// Proxy call should also convert struct to table.
	args, _ := json.Marshal(map[string]any{
		"script": `local r = tool.fs_read_file({}); return r.file_name`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "test.yaml", resp["result"])
}

func TestToolProxyCall_NoArgs(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// tool.test_tool() without args should not panic.
	args, _ := json.Marshal(map[string]any{
		"script": `local r, err = tool.test_tool(); return r`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	resultMap := resp["result"].(map[string]any)
	assert.Equal(t, "ok", resultMap["status"])
}

func TestToolProxyCall_ReservedName(t *testing.T) {
	// A tool named "call" should not override the built-in tool.call function.
	ts, err := NewToolSet(WithTools(&mockTool{name: "call"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// tool.call should still be the built-in function, not the tool proxy.
	args, _ := json.Marshal(map[string]any{
		"script": `local r, err = tool.call("call", {}); return r`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	resultMap := resp["result"].(map[string]any)
	assert.Equal(t, "ok", resultMap["status"])
}

func TestSessionsDenied(t *testing.T) {
	sessionsMock := &mockTool{name: "sessions_spawn"}
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}, sessionsMock))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// sessions_spawn should not appear in tool.list().
	args, _ := json.Marshal(map[string]any{
		"script": `local names = tool.list(); return names`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	names := resp["result"].([]any)
	for _, n := range names {
		assert.NotEqual(t, "sessions_spawn", n)
	}
}

func TestDetailedToolError_NotFound(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `local r, err = tool.call("nonexistent", {}); return err`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	errMap := resp["result"].(map[string]any)
	assert.Equal(t, ErrTypeToolCall, errMap["type"])
	assert.Equal(t, "nonexistent", errMap["tool"])
	assert.Equal(t, "not_found", errMap["phase"])
}

func TestDetailedToolError_ArgsType(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// Pass a string instead of table as second arg.
	args, _ := json.Marshal(map[string]any{
		"script": `local r, err = tool.call("test_tool", "not_a_table"); return err`,
	})

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	errMap := resp["result"].(map[string]any)
	assert.Equal(t, ErrTypeToolCall, errMap["type"])
	assert.Equal(t, "test_tool", errMap["tool"])
	assert.Equal(t, "args_type", errMap["phase"])
}

func TestDeniedModules_Html(t *testing.T) {
	ts, err := NewToolSet(
		WithTools(&mockTool{name: "test_tool"}),
		WithDeniedModules("html"),
	)
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `return type(html)`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "nil", resp["result"])
}

func TestDeniedModules_Md(t *testing.T) {
	ts, err := NewToolSet(
		WithTools(&mockTool{name: "test_tool"}),
		WithDeniedModules("md"),
	)
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `return type(md)`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "nil", resp["result"])
}

func TestToolNameToLuaIdent(t *testing.T) {
	assert.Equal(t, "fs_read_file", toolNameToLuaIdent("fs_read_file"))
	assert.Equal(t, "hostexec_exec_command", toolNameToLuaIdent("hostexec.exec.command"))
	assert.Equal(t, "luaexec_lua_exec", toolNameToLuaIdent("luaexec_lua_exec"))
}

func TestBuildDescription_ContainsProxyCall(t *testing.T) {
	cfg := Config{
		Tools: []tool.Tool{&mockTool{name: "test_tool"}},
	}
	desc := buildDescription(cfg)
	assert.Contains(t, desc, "tool.工具名")
	assert.Contains(t, desc, "禁止require")
	assert.Contains(t, desc, "sessions_spawn")
	assert.Contains(t, desc, "常见错误")
	assert.Contains(t, desc, "参数传递")
	assert.Contains(t, desc, "ARGS")
}

func TestArgsInjection_Table(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}), WithAllowIOLib(true))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	tests := []struct {
		name   string
		script string
		args   map[string]any
		want   any
	}{
		{
			name:   "simple_string",
			script: `return ARGS.name`,
			args:   map[string]any{"name": "基础组件"},
			want:   "基础组件",
		},
		{
			name:   "nested_table",
			script: `return ARGS.config.threshold`,
			args:   map[string]any{"config": map[string]any{"threshold": 0.8}},
			want:   0.8,
		},
		{
			name:   "number_arg",
			script: `return ARGS.count + 1`,
			args:   map[string]any{"count": float64(5)},
			want:   float64(6),
		},
		{
			name:   "array_arg",
			script: `return #ARGS.items`,
			args:   map[string]any{"items": []any{"a", "b", "c"}},
			want:   float64(3),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			argsJSON, _ := json.Marshal(map[string]any{
				"script": tt.script,
				"args":   tt.args,
			})
			result, err := ct.Call(context.Background(), argsJSON)
			require.NoError(t, err)
			m := result.(map[string]any)
			assert.Equal(t, "success", m["status"])
			assert.Equal(t, tt.want, m["result"])
		})
	}
}

func TestArgsInjection_NilArgs(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// Without args, ARGS should be nil
	argsJSON, _ := json.Marshal(map[string]any{
		"script": `return ARGS`,
	})
	result, err := ct.Call(context.Background(), argsJSON)
	require.NoError(t, err)
	m := result.(map[string]any)
	assert.Equal(t, "success", m["status"])
	assert.Nil(t, m["result"])
}

func TestConfig_RegistrySizeAndCallStackSize(t *testing.T) {
	ts, err := NewToolSet(
		WithTools(&mockTool{name: "test_tool"}),
		WithCallStackSize(512),
		WithRegistrySize(4096),
		WithAllowIOLib(true),
	)
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	argsJSON, _ := json.Marshal(map[string]any{
		"script": `return "ok"`,
	})
	result, err := ct.Call(context.Background(), argsJSON)
	require.NoError(t, err)
	m := result.(map[string]any)
	assert.Equal(t, "success", m["status"])
	assert.Equal(t, "ok", m["result"])
}
