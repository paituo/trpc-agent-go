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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRequireBridgeModules verifies that all bridge modules can be loaded via require().
func TestRequireBridgeModules(t *testing.T) {
	tests := []struct {
		name     string
		module   string
		script   string
		expected any
	}{
		{
			name:     "fs via require",
			module:   "fs",
			script:   `local fs = require("fs"); return type(fs.mkdir)`,
			expected: "function",
		},
		{
			name:     "yaml via require",
			module:   "yaml",
			script:   `local yaml = require("yaml"); return type(yaml.decode)`,
			expected: "function",
		},
		{
			name:     "json via require",
			module:   "json",
			script:   `local json = require("json"); return type(json.decode)`,
			expected: "function",
		},
		{
			name:     "html via require",
			module:   "html",
			script:   `local html = require("html"); return type(html.parse)`,
			expected: "function",
		},
		{
			name:     "md via require",
			module:   "md",
			script:   `local md = require("md"); return type(md.parse)`,
			expected: "function",
		},
		{
			name:     "htmltable via require",
			module:   "htmltable",
			script:   `local htmltable = require("htmltable"); return type(htmltable.parse_html)`,
			expected: "function",
		},
		{
			name:     "summarize via require",
			module:   "summarize",
			script:   `local summarize = require("summarize"); return type(summarize.textrank)`,
			expected: "function",
		},
		{
			name:     "utf8 via require",
			module:   "utf8",
			script:   `local utf8 = require("utf8"); return type(utf8.len)`,
			expected: "function",
		},
		{
			name:     "re via require",
			module:   "re",
			script:   `local re = require("re"); return type(re.find)`,
			expected: "function",
		},
		{
			name:     "log via require",
			module:   "log",
			script:   `local log = require("log"); return type(log.info)`,
			expected: "function",
		},
		{
			name:     "tool via require",
			module:   "tool",
			script:   `local tool = require("tool"); return type(tool.list)`,
			expected: "function",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
			require.NoError(t, err)
			defer ts.Close()

			tools := ts.Tools(context.Background())
			ct := tools[0].(interface {
				Call(ctx context.Context, jsonArgs []byte) (any, error)
			})

			args, err := json.Marshal(map[string]any{
				"script": tt.script,
			})
			require.NoError(t, err)

			result, err := ct.Call(context.Background(), args)
			require.NoError(t, err)

			resp := result.(map[string]any)
			assert.Equal(t, "success", resp["status"], "require(%q) should succeed", tt.module)
			assert.Equal(t, tt.expected, resp["result"], "require(%q) should return expected type", tt.module)
		})
	}
}

// TestRequireFSModuleUsage verifies that fs module loaded via require() works correctly.
func TestRequireFSModuleUsage(t *testing.T) {
	ts, err := NewToolSet(
		WithTools(&mockTool{name: "test_tool"}),
		WithAllowFSLib(true),
		WithAllowedScriptDirs(t.TempDir()),
	)
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	script := `
		local fs = require("fs")
		local ok, err = fs.mkdir("` + t.TempDir() + `")
		if not ok then
			return {status = "error", message = err and err.message or "unknown"}
		end
		return {status = "ok"}
	`

	args, err := json.Marshal(map[string]any{
		"script": script,
	})
	require.NoError(t, err)

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
}

// TestRequireAndGlobalConsistency verifies that require() returns the same table as the global.
func TestRequireAndGlobalConsistency(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// Verify that require("yaml") returns the same table as the global yaml.
	script := `
		local required = require("yaml")
		return required == yaml
	`

	args, err := json.Marshal(map[string]any{
		"script": script,
	})
	require.NoError(t, err)

	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)

	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, true, resp["result"], "require() should return the same table as the global")
}
