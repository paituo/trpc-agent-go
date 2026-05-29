//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package subagentrun

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

func TestSpawnRequestContainsParentInvocationID(t *testing.T) {
	req := SpawnRequest{
		OwnerUserID:        "user-a",
		ParentSessionID:    "sess-b",
		Task:               "do something",
		TimeoutSeconds:     300,
		ParentInvocationID: "inv-123",
	}
	require.Equal(t, "inv-123", req.ParentInvocationID)
}

func TestParentInvocationIDFromContext(t *testing.T) {
	t.Run("returns empty when no invocation in context", func(t *testing.T) {
		ctx := context.Background()
		result := parentInvocationIDFromContext(ctx)
		require.Empty(t, result)
	})

	t.Run("returns invocation ID when present", func(t *testing.T) {
		inv := agent.NewInvocation()
		ctx := agent.NewInvocationContext(context.Background(), inv)
		result := parentInvocationIDFromContext(ctx)
		require.Equal(t, inv.InvocationID, result)
	})
}

func TestRunWithParentInvocationID(t *testing.T) {
	t.Run("WithParentInvocationID sets the RunOption field", func(t *testing.T) {
		opts := agent.NewRunOptions(agent.WithParentInvocationID("parent-inv-456"))
		require.Equal(t, "parent-inv-456", opts.ParentInvocationID)
	})

	t.Run("WithInvocationParentInvocationID sets the Invocation field", func(t *testing.T) {
		inv := agent.NewInvocation(
			agent.WithInvocationParentInvocationID("parent-inv-789"),
		)
		require.Equal(t, "parent-inv-789", inv.GetParentInvocationID())
		require.Nil(t, inv.GetParentInvocation())
	})

	t.Run("RunOptions with ParentInvocationID and ExecutionTrace", func(t *testing.T) {
		opts := agent.NewRunOptions(
			agent.WithExecutionTraceEnabled(true),
			agent.WithParentInvocationID("parent-123"),
		)
		require.True(t, opts.ExecutionTraceEnabled)
		require.Equal(t, "parent-123", opts.ParentInvocationID)
	})
}
