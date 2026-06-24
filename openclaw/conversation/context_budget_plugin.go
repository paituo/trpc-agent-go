//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package conversation

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

const contextBudgetPluginName = "openclaw_context_budget"

// ContextBudgetPlugin monitors conversation context usage and injects
// budget reminders before each LLM call. It works through the BeforeModel
// callback mechanism so it doesn't modify core framework code.
//
// The plugin handles two concerns:
//  1. Static guidance — a one-time system-level prompt that tells the LLM
//     to be mindful of context window limits from the start.
//  2. Dynamic reminders — threshold-based budget alerts injected as user
//     messages when context usage exceeds 50%/70%/85%.
//
// Users do not need to add any context-management prompts to their config.
type ContextBudgetPlugin struct{}

// Name implements plugin.Plugin.
func (ContextBudgetPlugin) Name() string { return contextBudgetPluginName }

// Register implements plugin.Plugin.
func (ContextBudgetPlugin) Register(r *plugin.Registry) {
	if r == nil {
		return
	}
	r.BeforeModel(injectContextBudgetContent)
}

// contextBudgetGuidance is a static system-level prompt injected once at
// the beginning of the conversation. It primes the LLM to plan tasks with
// context window limits in mind, without requiring user-side configuration.
const contextBudgetGuidance = `<context_budget_guidance>
在规划任务和执行操作时，请注意上下文窗口（Context Window）的占用情况，避免单次操作消耗过多上下文导致溢出。

请遵循以下原则：
1. 优先精确操作：优先使用 search_content、grep 等精确搜索，避免批量读取大文件或执行全量列表操作。
2. 善用子任务拆分：对于复杂任务，优先使用 task_run 拆分为独立子任务，隔离上下文占用。
3. 控制操作粒度：单次工具调用应尽量精确，避免一次性请求过多数据。
4. 结果压缩：已  完成的操作结果在后续轮次中可能被压缩，不要依赖历史工具结果中的完整数据。
</context_budget_guidance>`

// threshold defines a context usage threshold and its corresponding reminder text.
type threshold struct {
	ratio   float64
	message string
}

var budgetThresholds = []threshold{
	{
		ratio: 0.50,
		message: "上下文已使用约 %.0f%%，建议后续操作优先使用 search_content/grep 等精确搜索，" +
			"避免批量读取大文件。",
	},
	{
		ratio: 0.70,
		message: "上下文已使用约 %.0f%%，请主动考虑：1) 使用 task_run 隔离复杂子任务；" +
			"2) 已完成的操作结果可被压缩；3) 优先使用 search_content/grep 而非 fs_read_file。",
	},
	{
		ratio: 0.85,
		message: "上下文已使用约 %.0f%%，请立即停止批量操作。优先使用 task_run 处理后续任务。",
	},
}

// selectReminder picks the highest applicable reminder for the given ratio.
// Always returns a non-empty message — when below all thresholds it falls
// back to a simple percentage report.
func selectReminder(ratio float64) string {
	pct := ratio * 100
	var msg string
	for _, t := range budgetThresholds {
		if ratio >= t.ratio {
			msg = t.message
		}
	}
	if msg == "" {
		msg = fmt.Sprintf("当前上下文已使用约 %.0f%%。", pct)
	} else {
		msg = fmt.Sprintf(msg, pct)
	}
	return msg
}

// resolveContextWindow resolves the context window from invocation model info
// or falls back to the model name registry, then to a default.
func resolveContextWindow(inv *agent.Invocation) int {
	if inv != nil && inv.Model != nil {
		info := inv.Model.Info()
		if info.ContextWindow > 0 {
			return info.ContextWindow
		}
		if w, ok := model.LookupModelContextWindow(info.Name); ok {
			return w
		}
	}
	return 128000
}

// resolveTokenCounter returns a token counter from the invocation's model or a default.
func resolveTokenCounter(inv *agent.Invocation) model.TokenCounter {
	if inv != nil && inv.Model != nil {
		if tc := inv.Model.Info().TokenCounter; tc != nil {
			return tc
		}
	}
	return model.NewTokenCounter("")
}

// injectContextBudgetContent is the BeforeModel callback that:
//  1. Injects static context budget guidance as a system message on the first call.
//  2. Injects dynamic budget reminders when context usage exceeds thresholds.
func injectContextBudgetContent(
	ctx context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	if args == nil || args.Request == nil || len(args.Request.Messages) == 0 {
		return &model.BeforeModelResult{}, nil
	}

	req := args.Request
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return &model.BeforeModelResult{}, nil
	}

	// Step 1: Inject static guidance into the first existing system message.
	// We avoid adding a new SYSTEM message because some models don't
	// support multiple system messages.
	hasGuidance := false
	for _, msg := range req.Messages {
		if strings.Contains(msg.Content, "<context_budget_guidance>") {
			hasGuidance = true
			break
		}
	}
	if !hasGuidance {
		injected := false
		for i, msg := range req.Messages {
			if msg.Role == model.RoleSystem {
				req.Messages[i].Content = msg.Content + "\n\n" + contextBudgetGuidance
				injected = true
				break
			}
		}
		if !injected {
			// Fallback: no system message found, prepend as a new message.
			guidanceMsg := model.Message{
				Role:    model.RoleSystem,
				Content: contextBudgetGuidance,
			}
			req.Messages = append([]model.Message{guidanceMsg}, req.Messages...)
		}
	}

	// Step 2: Count tokens and inject dynamic budget reminders.
	tc := resolveTokenCounter(inv)
	tokens, err := tc.CountTokensRange(ctx, req.Messages, 0, len(req.Messages))
	if err != nil || tokens <= 0 {
		return &model.BeforeModelResult{}, nil
	}

	contextWindow := resolveContextWindow(inv)
	ratio := float64(tokens) / float64(contextWindow)
	reminder := selectReminder(ratio)
	if reminder == "" {
		return &model.BeforeModelResult{}, nil
	}

	// Avoid duplicate injection — skip if the last message already has a
	// context_budget tag.
	lastMsg := req.Messages[len(req.Messages)-1]
	if strings.Contains(lastMsg.Content, "<context_budget>") {
		return &model.BeforeModelResult{}, nil
	}

	// Append a user-role reminder message with structured XML tags.
	// Using a new message (not modifying system prompt prefix) preserves prompt caching.
	reminderMsg := model.Message{
		Role: model.RoleUser,
		Content: fmt.Sprintf(
			"<context_budget usage=\"%d\" window=\"%d\" ratio=\"%.0f%%\">\n%s\n</context_budget>",
			tokens, contextWindow, ratio*100, reminder,
		),
	}
	req.Messages = append(req.Messages, reminderMsg)

	return &model.BeforeModelResult{}, nil
}