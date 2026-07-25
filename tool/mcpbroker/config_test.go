//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mcpbroker

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

func TestNormalizeConnectionConfig_StdioForwardsEnv(t *testing.T) {
	in := mcp.ConnectionConfig{
		Command: "python",
		Args:    []string{"-m", "mcp_server"},
		Env: map[string]string{
			"EVERYTHING_SDK_PATH": "path/to/Everything64.dll",
		},
	}

	normalized, kind, err := normalizeConnectionConfig(in, false)
	require.NoError(t, err)
	require.Equal(t, transportStdio, kind)
	require.Equal(t, in.Env, normalized.Env)
	require.Len(t, normalized.Env, 1)
}

func TestNormalizeConnectionConfig_HTTPEnvRejected(t *testing.T) {
	in := mcp.ConnectionConfig{
		ServerURL: "https://example.com/mcp",
		Transport: "streamable_http",
		Env:       map[string]string{"FOO": "bar"},
	}

	_, _, err := normalizeConnectionConfig(in, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "HTTP MCP cannot specify env")
}

func TestCloneConnectionConfig_EnvIsDeepCopy(t *testing.T) {
	in := mcp.ConnectionConfig{
		Command: "python",
		Env:     map[string]string{"FOO": "bar"},
	}

	clone := cloneConnectionConfig(in)
	require.Equal(t, in.Env, clone.Env)

	// Mutating the clone must not affect the source.
	clone.Env["FOO"] = "changed"
	require.Equal(t, "bar", in.Env["FOO"])
	require.Equal(t, "changed", clone.Env["FOO"])
}
