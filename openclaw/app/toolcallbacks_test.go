package app

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestMergeToolCallbacks(t *testing.T) {
	dst := tool.NewCallbacks()
	dst.RegisterToolResultMessages(func(
		ctx context.Context,
		in *tool.ToolResultMessagesInput,
	) (any, error) {
		return "framework-msg", nil
	})

	src := tool.NewCallbacks()
	src.RegisterAfterTool(func(
		ctx context.Context,
		args *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		return nil, nil
	})
	src.RegisterBeforeTool(func(
		ctx context.Context,
		args *tool.BeforeToolArgs,
	) (*tool.BeforeToolResult, error) {
		return nil, nil
	})

	mergeToolCallbacks(dst, src)

	if len(dst.BeforeTool) != 1 {
		t.Fatalf("expected 1 before-tool callback, got %d", len(dst.BeforeTool))
	}
	if len(dst.AfterTool) != 1 {
		t.Fatalf("expected 1 after-tool callback, got %d", len(dst.AfterTool))
	}
	// Framework-owned ToolResultMessages must be preserved.
	if dst.ToolResultMessages == nil {
		t.Fatal("framework ToolResultMessages was overwritten by merge")
	}
}

func TestMergeToolCallbacks_NilSafe(t *testing.T) {
	dst := tool.NewCallbacks()
	mergeToolCallbacks(dst, nil)
	mergeToolCallbacks(nil, tool.NewCallbacks())
}

func TestMergeRegisteredToolCallbacks(t *testing.T) {
	// No registered contributors: base must be left untouched.
	base := tool.NewCallbacks()
	mergeRegisteredToolCallbacks(base)
	if len(base.BeforeTool) != 0 || len(base.AfterTool) != 0 {
		t.Fatal("mergeRegisteredToolCallbacks modified base with no contributors")
	}
	// Nil base must not panic.
	mergeRegisteredToolCallbacks(nil)
}
