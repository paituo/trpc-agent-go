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

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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
//
// MaxContextWindow caps the effective context window. Default (-1) means
// using the model's reported context window. When >= 0 and smaller than
// the model's context window, it overrides as the maximum.
type ContextBudgetPlugin struct {
	MaxContextWindow int
}

// Name implements plugin.Plugin.
func (p ContextBudgetPlugin) Name() string { return contextBudgetPluginName }

// Register implements plugin.Plugin.
func (p ContextBudgetPlugin) Register(r *plugin.Registry) {
	if r == nil {
		return
	}
	r.BeforeModel(p.injectContextBudgetContent)
}

// contextBudgetGuidance is a static system-level prompt injected once at
// the beginning of the conversation. It primes the LLM to plan tasks with
// context window limits in mind, without requiring user-side configuration.
const contextBudgetGuidance = `<context_budget_guidance>
系统已启用上下文压缩（context compaction）和会话摘要（session summary），自动管理上下文窗口占用。
预算提醒仅在占比超过 50% 时注入，仅为参考信息。请继续执行任务，无需主动调整策略来节省上下文。

操作原则：
1. 优先精确操作：使用 search_content、grep 等精确搜索，避免批量读取大文件。
2. 善用子任务拆分：复杂任务使用 task_run 拆分为独立子任务，隔离上下文占用。
3. 控制操作粒度：单次工具调用尽量精确，避免一次性请求过多数据。
4. 结果压缩：已完成的操作结果可能被压缩，勿依赖历史工具结果中的完整数据。
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
		ratio:   0.85,
		message: "上下文已使用约 %.0f%%，请立即停止批量操作。优先使用 task_run 处理后续任务。",
	},
}

// selectReminder picks the highest applicable reminder for the given ratio.
// Returns empty string when below 50% to avoid triggering premature
// self-intervention by the LLM.
func selectReminder(ratio float64) string {
	// 低于 50% 不注入提醒，避免 LLM 过早自我干预执行策略
	if ratio < 0.50 {
		return ""
	}
	pct := ratio * 100
	var msg string
	for _, t := range budgetThresholds {
		if ratio >= t.ratio {
			msg = t.message
		}
	}
	msg = fmt.Sprintf(msg, pct)
	return msg
}

// resolveContextWindow resolves the context window from invocation model info
// or falls back to the model name registry, then to a default.
// When maxWindow >= 0 and smaller than the resolved value, it caps the result.
func resolveContextWindow(inv *agent.Invocation, maxWindow int) int {
	var window int
	if inv != nil && inv.Model != nil {
		info := inv.Model.Info()
		if info.ContextWindow > 0 {
			window = info.ContextWindow
		} else if w, ok := model.LookupModelContextWindow(info.Name); ok {
			window = w
		}
	}
	if window <= 0 {
		window = 128000
	}
	if maxWindow > 0 && maxWindow < window {
		window = maxWindow
	}
	return window
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
func (p ContextBudgetPlugin) injectContextBudgetContent(
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

	contextWindow := resolveContextWindow(inv, p.MaxContextWindow)
	ratio := float64(tokens) / float64(contextWindow)

	// Record context budget metrics on the current OTel span for Langfuse.
	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		span.SetAttributes(
			attribute.Int("context_budget.tokens", tokens),
			attribute.Int("context_budget.window", contextWindow),
			attribute.Float64("context_budget.ratio", ratio),
		)
	}

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
