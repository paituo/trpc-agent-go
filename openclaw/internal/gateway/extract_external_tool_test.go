package gateway

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// toolCallEvent builds a minimal runner event carrying a single tool call of
// the given name, suitable for exercising extractExternalToolCalls.
func toolCallEvent(name, args string) *event.Event {
	return &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolCalls: []model.ToolCall{
							{
								ID:       "call_1",
								Function: model.FunctionDefinitionParam{Name: name, Arguments: []byte(args)},
							},
						},
					},
				},
			},
		},
	}
}

// TestExtractExternalToolCalls verifies that the gateway emits a
// permission.request for every declared caller-executed external tool
// (e.g. AskUserQuestion / 媒体生成 / MCP 桥接), not just confirm_card.
// Regression test for the "扩展消息没有响应" bug where non-confirm_card
// external tools were rendered as raw JSON cards instead of interactive cards.
func TestExtractExternalToolCalls(t *testing.T) {
	ext := map[string]struct{}{
		"askuserquestion":         {},
		"lobsterai_image_generate": {},
	}

	// 外部工具 AskUserQuestion 应被识别为需下发 permission.request。
	calls := extractExternalToolCalls(toolCallEvent("AskUserQuestion", "{}"), ext)
	if len(calls) != 1 || calls[0].Function.Name != "AskUserQuestion" {
		t.Fatalf("expected AskUserQuestion matched, got %+v", calls)
	}

	// confirm_card 始终应被识别，即使不在 externalToolNames 集合中。
	calls = extractExternalToolCalls(toolCallEvent("confirm_card", "{}"), map[string]struct{}{})
	if len(calls) != 1 || calls[0].Function.Name != "confirm_card" {
		t.Fatalf("expected confirm_card matched, got %+v", calls)
	}

	// 普通（服务端执行）工具不应被识别为外部工具。
	calls = extractExternalToolCalls(toolCallEvent("web_search", "{}"), ext)
	if len(calls) != 0 {
		t.Fatalf("expected web_search NOT matched, got %+v", calls)
	}

	// 未声明的外部工具名不应被识别（避免误把普通工具当外部工具）。
	calls = extractExternalToolCalls(toolCallEvent("AskUserQuestion", "{}"), map[string]struct{}{})
	if len(calls) != 0 {
		t.Fatalf("expected AskUserQuestion NOT matched when not declared, got %+v", calls)
	}

	// isExternalToolName 判定一致性检查。
	if !isExternalToolName("confirm_card", ext) {
		t.Fatal("confirm_card should always be external")
	}
	if !isExternalToolName("AskUserQuestion", ext) {
		t.Fatal("AskUserQuestion should be external when declared")
	}
	if !isExternalToolName("lobsterai_image_generate", ext) {
		t.Fatal("lobsterai_image_generate should be external when declared")
	}
	if isExternalToolName("web_search", ext) {
		t.Fatal("web_search should not be external")
	}
}
