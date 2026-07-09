//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package telemetry provides metric context for agent runtime observability.
package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/metric/histogram"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

var (
	ContextMeter metric.Meter = MeterProvider.Meter(metrics.MeterNameContext)

	ContextMetricInputTokens         *histogram.DynamicInt64Histogram
	ContextMetricWindowSize          *histogram.DynamicInt64Histogram
	ContextMetricUsageRatio          *histogram.DynamicFloat64Histogram
	ContextMetricInitialTokens       *histogram.DynamicInt64Histogram
	ContextMetricInitialMessageCount *histogram.DynamicInt64Histogram
	ContextMetricTailoredTokens      *histogram.DynamicInt64Histogram
	ContextMetricTailoredMessages    *histogram.DynamicInt64Histogram
	ContextMetricCompactedTokens     *histogram.DynamicInt64Histogram
	ContextMetricMessageCount        *histogram.DynamicInt64Histogram

	ContextMetricCompletionTokens     *histogram.DynamicInt64Histogram
	ContextMetricTotalTokens          *histogram.DynamicInt64Histogram
	ContextMetricCachedTokens         *histogram.DynamicInt64Histogram
	ContextMetricReasoningTokens      *histogram.DynamicInt64Histogram
	ContextMetricToolDefinitionTokens *histogram.DynamicInt64Histogram
	ContextMetricUsageRatioByInitial  *histogram.DynamicFloat64Histogram

	ContextMetricCompactionTrigger          metric.Int64Counter
	ContextMetricTailoringTrigger           metric.Int64Counter
	ContextMetricSummaryTrigger             metric.Int64Counter
	ContextMetricToolCompactionTrigger      metric.Int64Counter
	ContextMetricOversizedTruncationTrigger metric.Int64Counter
	ContextMetricHistoryTrimTrigger         metric.Int64Counter
)

// ContextConfigSnapshot captures the context control configuration at the time of an LLM call.
type ContextConfigSnapshot struct {
	EnableCompaction             bool
	EnableTokenTailoring         bool
	SyncSummary                  bool
	SummaryInjectionMode         string
	AddSummary                   bool
	TailoringStrategy            string
	MessageFilterMode            string
	ReasoningContentMode         string
	CompactionThresholdRatio     float64
	ToolResultMaxTokens          int
	OversizedToolResultMaxTokens int
	MaxHistoryRuns               int
	KeepRecentRequests           int
	EnableDetailedMetrics        bool
	ProtocolOverheadTokens       int
	ReserveOutputTokens          int
	InputTokensFloor             int
	SafetyMarginRatio            float64
	MaxInputTokensRatio          float64
}

// ContextMetricsTracker tracks context control metrics for a single LLM call lifecycle.
type ContextMetricsTracker struct {
	ctx context.Context

	initialMessageCount int
	initialTokens       int

	historyTrimTriggered      bool
	historyTrimMessagesBefore int
	historyTrimMessagesAfter  int

	summaryTriggered bool

	compactionTriggered  bool
	preCompactionTokens  int
	postCompactionTokens int

	toolCompactionTriggered   bool
	toolCompactionTokensSaved int

	oversizedTruncationTriggered   bool
	oversizedTruncationTokensSaved int

	tailoringTriggered    bool
	tailoringStrategy     string
	preTailoringTokens    int
	postTailoringTokens   int
	preTailoringMessages  int
	postTailoringMessages int
	maxInputTokens        int

	contextWindow      int
	actualPromptTokens int

	actualCompletionTokens int
	actualTotalTokens      int
	cachedTokens           int
	cacheCreationTokens    int
	cacheReadTokens        int
	reasoningTokens        int
	toolDefinitionTokens   int
	protocolOverheadTokens int

	config ContextConfigSnapshot

	invocation *agent.Invocation
}

type contextMetricsTrackerKey struct{}

// WithContextMetricsTracker stores the tracker in context.
func WithContextMetricsTracker(ctx context.Context, tracker *ContextMetricsTracker) context.Context {
	return context.WithValue(ctx, contextMetricsTrackerKey{}, tracker)
}

// ContextMetricsTrackerFromContext extracts the tracker from context.
func ContextMetricsTrackerFromContext(ctx context.Context) *ContextMetricsTracker {
	tracker, _ := ctx.Value(contextMetricsTrackerKey{}).(*ContextMetricsTracker)
	return tracker
}

// NewContextMetricsTracker creates a new ContextMetricsTracker.
func NewContextMetricsTracker(ctx context.Context, invocation *agent.Invocation, config ContextConfigSnapshot) *ContextMetricsTracker {
	return &ContextMetricsTracker{
		ctx:        ctx,
		config:     config,
		invocation: invocation,
	}
}

// RecordInitialState records the initial message count and token estimate after preprocess.
func (t *ContextMetricsTracker) RecordInitialState(messages []model.Message, tokenCounter model.TokenCounter) {
	if t == nil {
		return
	}
	t.initialMessageCount = len(messages)
	if len(messages) > 0 {
		counter := tokenCounter
		if counter == nil {
			counter = model.NewTokenCounter("") // TODO: pass model.TokenCounter from caller to avoid fallback to SimpleTokenCounter(4.0).
		}
		tokens, err := counter.CountTokensRange(t.ctx, messages, 0, len(messages))
		if err == nil {
			t.initialTokens = tokens
		}
	}
}

// RecordHistoryTrim records the history trim effect.
func (t *ContextMetricsTracker) RecordHistoryTrim(triggered bool, messagesBefore, messagesAfter int) {
	if t == nil {
		return
	}
	t.historyTrimTriggered = triggered
	t.historyTrimMessagesBefore = messagesBefore
	t.historyTrimMessagesAfter = messagesAfter
}

// RecordSummaryTriggered records that session summary was triggered.
func (t *ContextMetricsTracker) RecordSummaryTriggered(triggered bool) {
	if t == nil {
		return
	}
	t.summaryTriggered = triggered
}

// RecordPreCompaction records the token count before context compaction.
func (t *ContextMetricsTracker) RecordPreCompaction(tokens int) {
	if t == nil {
		return
	}
	t.preCompactionTokens = tokens
}

// RecordPostCompaction records the token count after context compaction.
func (t *ContextMetricsTracker) RecordPostCompaction(tokens int, triggered bool) {
	if t == nil {
		return
	}
	t.postCompactionTokens = tokens
	t.compactionTriggered = triggered
}

// RecordToolCompaction records the tool result compaction effect.
func (t *ContextMetricsTracker) RecordToolCompaction(triggered bool, tokensSaved int) {
	if t == nil {
		return
	}
	t.toolCompactionTriggered = triggered
	t.toolCompactionTokensSaved = tokensSaved
}

// RecordOversizedTruncation records the oversized tool result truncation effect.
func (t *ContextMetricsTracker) RecordOversizedTruncation(triggered bool, tokensSaved int) {
	if t == nil {
		return
	}
	t.oversizedTruncationTriggered = triggered
	t.oversizedTruncationTokensSaved = tokensSaved
}

// RecordPreTailoring records the state before token tailoring.
func (t *ContextMetricsTracker) RecordPreTailoring(messages []model.Message, maxInputTokens int, strategy string, tokenCounter model.TokenCounter) {
	if t == nil {
		return
	}
	t.preTailoringMessages = len(messages)
	t.maxInputTokens = maxInputTokens
	t.tailoringStrategy = strategy
	if tokenCounter != nil && len(messages) > 0 {
		tokens, err := tokenCounter.CountTokensRange(t.ctx, messages, 0, len(messages))
		if err == nil {
			t.preTailoringTokens = tokens
		}
	}
}

// RecordPostTailoring records the state after token tailoring.
func (t *ContextMetricsTracker) RecordPostTailoring(messages []model.Message, tokenCounter model.TokenCounter) {
	if t == nil {
		return
	}
	t.postTailoringMessages = len(messages)
	if tokenCounter != nil && len(messages) > 0 {
		tokens, err := tokenCounter.CountTokensRange(t.ctx, messages, 0, len(messages))
		if err == nil {
			t.postTailoringTokens = tokens
		}
	}
	t.tailoringTriggered = t.preTailoringMessages > 0 && t.postTailoringMessages < t.preTailoringMessages
}

// RecordFinalUsage records the final usage from the LLM response.
func (t *ContextMetricsTracker) RecordFinalUsage(usage *model.Usage, contextWindow int) {
	if t == nil {
		return
	}
	if usage != nil {
		t.actualPromptTokens = usage.PromptTokens
		if t.config.EnableDetailedMetrics {
			t.actualCompletionTokens = usage.CompletionTokens
			t.actualTotalTokens = usage.TotalTokens
			t.cachedTokens = usage.PromptTokensDetails.CachedTokens
			t.cacheCreationTokens = usage.PromptTokensDetails.CacheCreationTokens
			t.cacheReadTokens = usage.PromptTokensDetails.CacheReadTokens
			t.reasoningTokens = usage.CompletionTokensDetails.ReasoningTokens
		}
	}
	t.contextWindow = contextWindow
}

// SetModelTailoringConfig updates the tracker with the model's token tailoring configuration.
func (t *ContextMetricsTracker) SetModelTailoringConfig(strategy string, enabled bool) {
	if t == nil {
		return
	}
	t.config.TailoringStrategy = strategy
	t.config.EnableTokenTailoring = enabled
}

// RecordToolDefinitionTokens records the estimated token count of tool/function definitions.
func (t *ContextMetricsTracker) RecordToolDefinitionTokens(tokens int) {
	if t == nil {
		return
	}
	if !t.config.EnableDetailedMetrics {
		return
	}
	t.toolDefinitionTokens = tokens
}

// SetTokenTailoringBudget updates the tracker with the model's token tailoring budget parameters.
func (t *ContextMetricsTracker) SetTokenTailoringBudget(protocolOverhead, reserveOutput int) {
	if t == nil {
		return
	}
	t.protocolOverheadTokens = protocolOverhead
	if t.config.EnableDetailedMetrics {
		t.config.ProtocolOverheadTokens = protocolOverhead
		t.config.ReserveOutputTokens = reserveOutput
	}
}

// SetTokenTailoringRatios updates the tracker with the model's token tailoring ratio parameters.
func (t *ContextMetricsTracker) SetTokenTailoringRatios(inputTokensFloor int, safetyMarginRatio, maxInputTokensRatio float64) {
	if t == nil {
		return
	}
	if t.config.EnableDetailedMetrics {
		t.config.InputTokensFloor = inputTokensFloor
		t.config.SafetyMarginRatio = safetyMarginRatio
		t.config.MaxInputTokensRatio = maxInputTokensRatio
	}
}

// contextAttributes holds the attributes for context metrics.
type contextAttributes struct {
	RequestModelName string
	AgentName        string
	AppName          string
	UserID           string
	SessionID        string

	EnableCompaction     bool
	SyncSummary          bool
	SummaryInjectionMode string
	AddSummary           bool
	TailoringStrategy    string
	MessageFilterMode    string
	ReasoningContentMode string
}

func (a contextAttributes) toAttributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(semconvtrace.KeyGenAIOperationName, OperationChat),
		attribute.Bool("context.enable_compaction", a.EnableCompaction),
		attribute.Bool("context.sync_summary", a.SyncSummary),
		attribute.String("context.summary_injection_mode", a.SummaryInjectionMode),
		attribute.Bool("context.add_summary", a.AddSummary),
		attribute.String("context.tailoring_strategy", a.TailoringStrategy),
		attribute.String("context.message_filter_mode", a.MessageFilterMode),
		attribute.String("context.reasoning_content_mode", a.ReasoningContentMode),
	}
	if a.RequestModelName != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyGenAIRequestModel, a.RequestModelName))
		attrs = append(attrs, attribute.String(semconvtrace.KeyGenAISystem, a.RequestModelName))
	}
	if a.AgentName != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyGenAIAgentName, a.AgentName))
	}
	if a.AppName != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyTRPCAgentGoAppName, a.AppName))
	}
	if a.UserID != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyTRPCAgentGoUserID, a.UserID))
	}
	if a.SessionID != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyGenAIConversationID, a.SessionID))
	}
	return attrs
}

func (t *ContextMetricsTracker) buildAttributes() contextAttributes {
	attrs := contextAttributes{
		EnableCompaction:     t.config.EnableCompaction,
		SyncSummary:          t.config.SyncSummary,
		SummaryInjectionMode: t.config.SummaryInjectionMode,
		AddSummary:           t.config.AddSummary,
		TailoringStrategy:    t.config.TailoringStrategy,
		MessageFilterMode:    t.config.MessageFilterMode,
		ReasoningContentMode: t.config.ReasoningContentMode,
	}
	if t.invocation != nil {
		if t.invocation.Model != nil {
			attrs.RequestModelName = t.invocation.Model.Info().Name
		}
		attrs.AgentName = t.invocation.AgentName
		if t.invocation.Session != nil {
			attrs.SessionID = t.invocation.Session.ID
			attrs.UserID = t.invocation.Session.UserID
			attrs.AppName = t.invocation.Session.AppName
		}
	}
	return attrs
}

func contextMetricsEnabled() bool {
	return ContextMetricInputTokens != nil ||
		ContextMetricWindowSize != nil ||
		ContextMetricUsageRatio != nil ||
		ContextMetricInitialTokens != nil ||
		ContextMetricInitialMessageCount != nil ||
		ContextMetricTailoredTokens != nil ||
		ContextMetricTailoredMessages != nil ||
		ContextMetricCompactedTokens != nil ||
		ContextMetricMessageCount != nil ||
		ContextMetricCompletionTokens != nil ||
		ContextMetricTotalTokens != nil ||
		ContextMetricCachedTokens != nil ||
		ContextMetricReasoningTokens != nil ||
		ContextMetricToolDefinitionTokens != nil ||
		ContextMetricUsageRatioByInitial != nil ||
		ContextMetricCompactionTrigger != nil ||
		ContextMetricTailoringTrigger != nil ||
		ContextMetricSummaryTrigger != nil ||
		ContextMetricToolCompactionTrigger != nil ||
		ContextMetricOversizedTruncationTrigger != nil ||
		ContextMetricHistoryTrimTrigger != nil
}

// RecordMetrics records all context control metrics.
func (t *ContextMetricsTracker) RecordMetrics() {
	if t == nil || !contextMetricsEnabled() {
		return
	}

	attrs := t.buildAttributes()
	otelAttrs := attrs.toAttributes()

	if ContextMetricInputTokens != nil {
		ContextMetricInputTokens.Record(t.ctx, int64(t.actualPromptTokens),
			metric.WithAttributes(otelAttrs...))
	}
	if ContextMetricWindowSize != nil {
		ContextMetricWindowSize.Record(t.ctx, int64(t.contextWindow),
			metric.WithAttributes(otelAttrs...))
	}
	if ContextMetricUsageRatio != nil && t.contextWindow > 0 {
		ContextMetricUsageRatio.Record(t.ctx, float64(t.actualPromptTokens)/float64(t.contextWindow),
			metric.WithAttributes(otelAttrs...))
	}
	if ContextMetricInitialTokens != nil {
		ContextMetricInitialTokens.Record(t.ctx, int64(t.initialTokens),
			metric.WithAttributes(otelAttrs...))
	}
	if ContextMetricInitialMessageCount != nil {
		ContextMetricInitialMessageCount.Record(t.ctx, int64(t.initialMessageCount),
			metric.WithAttributes(otelAttrs...))
	}
	if ContextMetricTailoredTokens != nil {
		tailoredTokens := t.preTailoringTokens - t.postTailoringTokens
		if tailoredTokens < 0 {
			tailoredTokens = 0
		}
		ContextMetricTailoredTokens.Record(t.ctx, int64(tailoredTokens),
			metric.WithAttributes(otelAttrs...))
	}
	if ContextMetricTailoredMessages != nil {
		tailoredMessages := t.preTailoringMessages - t.postTailoringMessages
		if tailoredMessages < 0 {
			tailoredMessages = 0
		}
		ContextMetricTailoredMessages.Record(t.ctx, int64(tailoredMessages),
			metric.WithAttributes(otelAttrs...))
	}
	if ContextMetricCompactedTokens != nil {
		compactedTokens := t.preCompactionTokens - t.postCompactionTokens
		if compactedTokens < 0 {
			compactedTokens = 0
		}
		ContextMetricCompactedTokens.Record(t.ctx, int64(compactedTokens),
			metric.WithAttributes(otelAttrs...))
	}
	if ContextMetricMessageCount != nil {
		ContextMetricMessageCount.Record(t.ctx, int64(t.postTailoringMessages),
			metric.WithAttributes(otelAttrs...))
	}

	if t.config.EnableDetailedMetrics {
		if ContextMetricCompletionTokens != nil {
			ContextMetricCompletionTokens.Record(t.ctx, int64(t.actualCompletionTokens),
				metric.WithAttributes(otelAttrs...))
		}
		if ContextMetricTotalTokens != nil {
			ContextMetricTotalTokens.Record(t.ctx, int64(t.actualTotalTokens),
				metric.WithAttributes(otelAttrs...))
		}
		if ContextMetricCachedTokens != nil {
			ContextMetricCachedTokens.Record(t.ctx, int64(t.cachedTokens),
				metric.WithAttributes(otelAttrs...))
		}
		if ContextMetricReasoningTokens != nil {
			ContextMetricReasoningTokens.Record(t.ctx, int64(t.reasoningTokens),
				metric.WithAttributes(otelAttrs...))
		}
		if ContextMetricToolDefinitionTokens != nil {
			ContextMetricToolDefinitionTokens.Record(t.ctx, int64(t.toolDefinitionTokens),
				metric.WithAttributes(otelAttrs...))
		}
		if ContextMetricUsageRatioByInitial != nil && t.contextWindow > 0 && t.initialTokens > 0 {
			ContextMetricUsageRatioByInitial.Record(t.ctx,
				float64(t.initialTokens)/float64(t.contextWindow),
				metric.WithAttributes(otelAttrs...))
		}
	}

	if t.compactionTriggered && ContextMetricCompactionTrigger != nil {
		compactionAttrs := append(otelAttrs,
			attribute.String("context.compaction_threshold_ratio",
				fmt.Sprintf("%.2f", t.config.CompactionThresholdRatio)))
		ContextMetricCompactionTrigger.Add(t.ctx, 1,
			metric.WithAttributes(compactionAttrs...))
	}
	if t.tailoringTriggered && ContextMetricTailoringTrigger != nil {
		ContextMetricTailoringTrigger.Add(t.ctx, 1,
			metric.WithAttributes(otelAttrs...))
	}
	if t.summaryTriggered && ContextMetricSummaryTrigger != nil {
		ContextMetricSummaryTrigger.Add(t.ctx, 1,
			metric.WithAttributes(otelAttrs...))
	}
	if t.toolCompactionTriggered && ContextMetricToolCompactionTrigger != nil {
		toolCompactionAttrs := append(otelAttrs,
			attribute.Int("context.tool_result_max_tokens", t.config.ToolResultMaxTokens))
		ContextMetricToolCompactionTrigger.Add(t.ctx, 1,
			metric.WithAttributes(toolCompactionAttrs...))
	}
	if t.oversizedTruncationTriggered && ContextMetricOversizedTruncationTrigger != nil {
		oversizedAttrs := append(otelAttrs,
			attribute.Int("context.oversized_tool_result_max_tokens", t.config.OversizedToolResultMaxTokens))
		ContextMetricOversizedTruncationTrigger.Add(t.ctx, 1,
			metric.WithAttributes(oversizedAttrs...))
	}
	if t.historyTrimTriggered && ContextMetricHistoryTrimTrigger != nil {
		historyTrimAttrs := append(otelAttrs,
			attribute.Int("context.max_history_runs", t.config.MaxHistoryRuns))
		ContextMetricHistoryTrimTrigger.Add(t.ctx, 1,
			metric.WithAttributes(historyTrimAttrs...))
	}
}

// ToTraceChatContextMetrics converts the tracker state to TraceChatContextMetrics
// for inclusion in trace span attributes (used by Langfuse).
func (t *ContextMetricsTracker) ToTraceChatContextMetrics() *TraceChatContextMetrics {
	if t == nil {
		return nil
	}
	tailoredTokens := t.preTailoringTokens - t.postTailoringTokens
	if tailoredTokens < 0 {
		tailoredTokens = 0
	}
	tailoredMessages := t.preTailoringMessages - t.postTailoringMessages
	if tailoredMessages < 0 {
		tailoredMessages = 0
	}
	compactedTokens := t.preCompactionTokens - t.postCompactionTokens
	if compactedTokens < 0 {
		compactedTokens = 0
	}
	var usageRatio float64
	if t.contextWindow > 0 {
		usageRatio = float64(t.actualPromptTokens) / float64(t.contextWindow)
	}
	var usageRatioByInitial float64
	if t.config.EnableDetailedMetrics && t.contextWindow > 0 && t.initialTokens > 0 {
		usageRatioByInitial = float64(t.initialTokens) / float64(t.contextWindow)
	}
	return &TraceChatContextMetrics{
		InputTokens:              t.actualPromptTokens,
		WindowSize:               t.contextWindow,
		UsageRatio:               usageRatio,
		InitialTokens:            t.initialTokens,
		InitialMessageCount:      t.initialMessageCount,
		TailoredTokens:           tailoredTokens,
		TailoredMessages:         tailoredMessages,
		CompactedTokens:          compactedTokens,
		MessageCount:             t.postTailoringMessages,
		CompactionTriggered:      t.compactionTriggered,
		TailoringTriggered:       t.tailoringTriggered,
		SummaryTriggered:         t.summaryTriggered,
		ToolCompactionTriggered:  t.toolCompactionTriggered,
		OversizedTruncTriggered:  t.oversizedTruncationTriggered,
		HistoryTrimTriggered:     t.historyTrimTriggered,
		EnableCompaction:         t.config.EnableCompaction,
		EnableTokenTailoring:     t.config.EnableTokenTailoring,
		SyncSummary:              t.config.SyncSummary,
		SummaryInjectionMode:     t.config.SummaryInjectionMode,
		AddSummary:               t.config.AddSummary,
		TailoringStrategy:        t.config.TailoringStrategy,
		MessageFilterMode:        t.config.MessageFilterMode,
		ReasoningContentMode:     t.config.ReasoningContentMode,
		CompactionThresholdRatio: t.config.CompactionThresholdRatio,
		ToolResultMaxTokens:      t.config.ToolResultMaxTokens,
		OversizedToolMaxTokens:   t.config.OversizedToolResultMaxTokens,
		MaxHistoryRuns:           t.config.MaxHistoryRuns,
		KeepRecentRequests:       t.config.KeepRecentRequests,
		CompletionTokens:       t.actualCompletionTokens,
		TotalTokens:            t.actualTotalTokens,
		CachedTokens:           t.cachedTokens,
		CacheCreationTokens:    t.cacheCreationTokens,
		CacheReadTokens:        t.cacheReadTokens,
		ReasoningTokens:        t.reasoningTokens,
		ToolDefinitionTokens:   t.toolDefinitionTokens,
		ProtocolOverheadTokens: t.protocolOverheadTokens,
		UsageRatioByInitial:    usageRatioByInitial,
		EnableDetailedMetrics:  t.config.EnableDetailedMetrics,
		ReserveOutputTokens:    t.config.ReserveOutputTokens,
		InputTokensFloor:       t.config.InputTokensFloor,
		SafetyMarginRatio:      t.config.SafetyMarginRatio,
		MaxInputTokensRatio:    t.config.MaxInputTokensRatio,
	}
}