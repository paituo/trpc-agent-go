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
)

func TestParentInvocationID_PropagatedToSpawnRequest(t *testing.T) {
	parentID := "parent-inv-12345"
	req := SpawnRequest{
		Task:               "test task",
		ParentInvocationID: parentID,
	}

	if req.ParentInvocationID != parentID {
		t.Fatalf("ParentInvocationID mismatch: got %q, want %q",
			req.ParentInvocationID, parentID)
	}

	_ = context.Background()
}