//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/metric/histogram"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
)

// saveAndNilContextMetrics saves all context metric globals and sets them to nil.
// Returns a restore function for use in t.Cleanup.
func saveAndNilContextMetrics() func() {
	origProvider := MeterProvider
	origMeter := ContextMeter
	origInputTokens := ContextMetricInputTokens
	origWindowSize := ContextMetricWindowSize
	origUsageRatio := ContextMetricUsageRatio
	origInitialTokens := ContextMetricInitialTokens
	origInitialMessageCount := ContextMetricInitialMessageCount
	origTailoredTokens := ContextMetricTailoredTokens
	origTailoredMessages := ContextMetricTailoredMessages
	origCompactedTokens := ContextMetricCompactedTokens
	origMessageCount := ContextMetricMessageCount
	origCompactionTrigger := ContextMetricCompactionTrigger
	origTailoringTrigger := ContextMetricTailoringTrigger
	origSummaryTrigger := ContextMetricSummaryTrigger
	origToolCompactionTrigger := ContextMetricToolCompactionTrigger
	origOversizedTruncationTrigger := ContextMetricOversizedTruncationTrigger
	origHistoryTrimTrigger := ContextMetricHistoryTrimTrigger

	MeterProvider = nil
	ContextMeter = nil
	ContextMetricInputTokens = nil
	ContextMetricWindowSize = nil
	ContextMetricUsageRatio = nil
	ContextMetricInitialTokens = nil
	ContextMetricInitialMessageCount = nil
	ContextMetricTailoredTokens = nil
	ContextMetricTailoredMessages = nil
	ContextMetricCompactedTokens = nil
	ContextMetricMessageCount = nil
	ContextMetricCompactionTrigger = nil
	ContextMetricTailoringTrigger = nil
	ContextMetricSummaryTrigger = nil
	ContextMetricToolCompactionTrigger = nil
	ContextMetricOversizedTruncationTrigger = nil
	ContextMetricHistoryTrimTrigger = nil

	return func() {
		MeterProvider = origProvider
		ContextMeter = origMeter
		ContextMetricInputTokens = origInputTokens
		ContextMetricWindowSize = origWindowSize
		ContextMetricUsageRatio = origUsageRatio
		ContextMetricInitialTokens = origInitialTokens
		ContextMetricInitialMessageCount = origInitialMessageCount
		ContextMetricTailoredTokens = origTailoredTokens
		ContextMetricTailoredMessages = origTailoredMessages
		ContextMetricCompactedTokens = origCompactedTokens
		ContextMetricMessageCount = origMessageCount
		ContextMetricCompactionTrigger = origCompactionTrigger
		ContextMetricTailoringTrigger = origTailoringTrigger
		ContextMetricSummaryTrigger = origSummaryTrigger
		ContextMetricToolCompactionTrigger = origToolCompactionTrigger
		ContextMetricOversizedTruncationTrigger = origOversizedTruncationTrigger
		ContextMetricHistoryTrimTrigger = origHistoryTrimTrigger
	}
}

// registerAllContextMetrics creates a ManualReader + MeterProvider and registers
// all context histogram and counter metrics. Returns the reader for collecting data.
func registerAllContextMetrics(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	MeterProvider = provider
	ContextMeter = provider.Meter(metrics.MeterNameContext)

	var err error
	ContextMetricInputTokens, err = histogram.NewDynamicInt64Histogram(provider, metrics.MeterNameContext, metrics.MetricContextInputTokens)
	require.NoError(t, err)
	ContextMetricWindowSize, err = histogram.NewDynamicInt64Histogram(provider, metrics.MeterNameContext, metrics.MetricContextWindowSize)
	require.NoError(t, err)
	ContextMetricUsageRatio, err = histogram.NewDynamicFloat64Histogram(provider, metrics.MeterNameContext, metrics.MetricContextUsageRatio)
	require.NoError(t, err)
	ContextMetricInitialTokens, err = histogram.NewDynamicInt64Histogram(provider, metrics.MeterNameContext, metrics.MetricContextInitialTokens)
	require.NoError(t, err)
	ContextMetricInitialMessageCount, err = histogram.NewDynamicInt64Histogram(provider, metrics.MeterNameContext, metrics.MetricContextInitialMessageCount)
	require.NoError(t, err)
	ContextMetricTailoredTokens, err = histogram.NewDynamicInt64Histogram(provider, metrics.MeterNameContext, metrics.MetricContextTailoredTokens)
	require.NoError(t, err)
	ContextMetricTailoredMessages, err = histogram.NewDynamicInt64Histogram(provider, metrics.MeterNameContext, metrics.MetricContextTailoredMessages)
	require.NoError(t, err)
	ContextMetricCompactedTokens, err = histogram.NewDynamicInt64Histogram(provider, metrics.MeterNameContext, metrics.MetricContextCompactedTokens)
	require.NoError(t, err)
	ContextMetricMessageCount, err = histogram.NewDynamicInt64Histogram(provider, metrics.MeterNameContext, metrics.MetricContextMessageCount)
	require.NoError(t, err)

	ContextMetricCompactionTrigger, err = ContextMeter.Int64Counter(metrics.MetricContextCompactionTrigger)
	require.NoError(t, err)
	ContextMetricTailoringTrigger, err = ContextMeter.Int64Counter(metrics.MetricContextTailoringTrigger)
	require.NoError(t, err)
	ContextMetricSummaryTrigger, err = ContextMeter.Int64Counter(metrics.MetricContextSummaryTrigger)
	require.NoError(t, err)
	ContextMetricToolCompactionTrigger, err = ContextMeter.Int64Counter(metrics.MetricContextToolCompactionTrigger)
	require.NoError(t, err)
	ContextMetricOversizedTruncationTrigger, err = ContextMeter.Int64Counter(metrics.MetricContextOversizedTruncationTrigger)
	require.NoError(t, err)
	ContextMetricHistoryTrimTrigger, err = ContextMeter.Int64Counter(metrics.MetricContextHistoryTrimTrigger)
	require.NoError(t, err)

	return reader
}

func TestContextMetricsTracker_RecordMetrics_AllHistograms(t *testing.T) {
	restore := saveAndNilContextMetrics()
	t.Cleanup(restore)
	reader := registerAllContextMetrics(t)

	tracker := NewContextMetricsTracker(context.Background(), nil, ContextConfigSnapshot{})
	// Set all state directly (same package access).
	tracker.initialMessageCount = 10
	tracker.initialTokens = 500
	tracker.contextWindow = 8192
	tracker.actualPromptTokens = 4000
	tracker.postTailoringMessages = 8
	tracker.preTailoringTokens = 5000
	tracker.postTailoringTokens = 4000
	tracker.preTailoringMessages = 10
	tracker.preCompactionTokens = 6000
	tracker.postCompactionTokens = 5000

	tracker.RecordMetrics()

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	metricNames := collectMetricNames(rm)
	require.Contains(t, metricNames, metrics.MetricContextInputTokens)
	require.Contains(t, metricNames, metrics.MetricContextWindowSize)
	require.Contains(t, metricNames, metrics.MetricContextUsageRatio)
	require.Contains(t, metricNames, metrics.MetricContextInitialTokens)
	require.Contains(t, metricNames, metrics.MetricContextInitialMessageCount)
	require.Contains(t, metricNames, metrics.MetricContextTailoredTokens)
	require.Contains(t, metricNames, metrics.MetricContextTailoredMessages)
	require.Contains(t, metricNames, metrics.MetricContextCompactedTokens)
	require.Contains(t, metricNames, metrics.MetricContextMessageCount)
}

func TestContextMetricsTracker_RecordMetrics_CompactionTrigger(t *testing.T) {
	restore := saveAndNilContextMetrics()
	t.Cleanup(restore)
	reader := registerAllContextMetrics(t)

	tracker := NewContextMetricsTracker(context.Background(), nil, ContextConfigSnapshot{
		CompactionThresholdRatio: 0.7,
	})
	tracker.compactionTriggered = true

	tracker.RecordMetrics()

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	metricNames := collectMetricNames(rm)
	require.Contains(t, metricNames, metrics.MetricContextCompactionTrigger)
}

func TestContextMetricsTracker_RecordMetrics_TailoringTrigger(t *testing.T) {
	restore := saveAndNilContextMetrics()
	t.Cleanup(restore)
	reader := registerAllContextMetrics(t)

	tracker := NewContextMetricsTracker(context.Background(), nil, ContextConfigSnapshot{})
	tracker.preTailoringMessages = 10
	tracker.postTailoringMessages = 8
	tracker.tailoringTriggered = true

	tracker.RecordMetrics()

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	metricNames := collectMetricNames(rm)
	require.Contains(t, metricNames, metrics.MetricContextTailoringTrigger)
}

func TestContextMetricsTracker_RecordMetrics_NoTrigger(t *testing.T) {
	restore := saveAndNilContextMetrics()
	t.Cleanup(restore)
	reader := registerAllContextMetrics(t)

	tracker := NewContextMetricsTracker(context.Background(), nil, ContextConfigSnapshot{})
	// No triggers set — all counters should be absent.

	tracker.RecordMetrics()

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	metricNames := collectMetricNames(rm)
	require.NotContains(t, metricNames, metrics.MetricContextCompactionTrigger)
	require.NotContains(t, metricNames, metrics.MetricContextTailoringTrigger)
	require.NotContains(t, metricNames, metrics.MetricContextSummaryTrigger)
	require.NotContains(t, metricNames, metrics.MetricContextToolCompactionTrigger)
	require.NotContains(t, metricNames, metrics.MetricContextOversizedTruncationTrigger)
	require.NotContains(t, metricNames, metrics.MetricContextHistoryTrimTrigger)
}

func TestContextMetricsTracker_ConfigSnapshot(t *testing.T) {
	restore := saveAndNilContextMetrics()
	t.Cleanup(restore)
	reader := registerAllContextMetrics(t)

	invocation := agent.NewInvocation(
		agent.WithInvocationModel(&telemetryTestModel{}),
		agent.WithInvocationSession(&session.Session{
			ID:      "sess-cfg",
			UserID:  "user-cfg",
			AppName: "app-cfg",
		}),
	)
	invocation.AgentName = "agent-cfg"

	config := ContextConfigSnapshot{
		EnableCompaction:     true,
		SyncSummary:          false,
		SummaryInjectionMode: "system",
		AddSummary:           true,
		TailoringStrategy:    "middle_out",
		MessageFilterMode:    "all",
		ReasoningContentMode: "strip",
	}

	tracker := NewContextMetricsTracker(context.Background(), invocation, config)
	tracker.contextWindow = 4096
	tracker.actualPromptTokens = 2000

	tracker.RecordMetrics()

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	require.NotEmpty(t, rm.ScopeMetrics, "expected at least one scope metric")

	// Verify that at least one metric was recorded with config state attributes.
	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == metrics.MetricContextWindowSize {
				found = true
				// Inspect the first data point's attributes.
				switch data := m.Data.(type) {
				case metricdata.Histogram[int64]:
					require.NotEmpty(t, data.DataPoints)
					attrSlice := data.DataPoints[0].Attributes.ToSlice()
					attrMap := make(map[string]any)
					for _, a := range attrSlice {
						attrMap[string(a.Key)] = a.Value.AsInterface()
					}
					require.Equal(t, true, attrMap["context.enable_compaction"])
					require.Equal(t, false, attrMap["context.sync_summary"])
					require.Equal(t, "system", attrMap["context.summary_injection_mode"])
					require.Equal(t, true, attrMap["context.add_summary"])
					require.Equal(t, "middle_out", attrMap["context.tailoring_strategy"])
					require.Equal(t, "all", attrMap["context.message_filter_mode"])
					require.Equal(t, "strip", attrMap["context.reasoning_content_mode"])
				}
			}
		}
	}
	require.True(t, found, "expected context.window_size metric to be recorded")
}

func TestContextMetricsTracker_UsageRatio(t *testing.T) {
	restore := saveAndNilContextMetrics()
	t.Cleanup(restore)
	reader := registerAllContextMetrics(t)

	tracker := NewContextMetricsTracker(context.Background(), nil, ContextConfigSnapshot{})
	tracker.contextWindow = 8192
	tracker.actualPromptTokens = 4096

	tracker.RecordMetrics()

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	metricNames := collectMetricNames(rm)
	require.Contains(t, metricNames, metrics.MetricContextUsageRatio)
}

func TestContextMetricsTracker_Disabled(t *testing.T) {
	restore := saveAndNilContextMetrics()
	t.Cleanup(restore)
	// Do NOT register any metrics — all globals remain nil.

	tracker := NewContextMetricsTracker(context.Background(), nil, ContextConfigSnapshot{})
	require.NotPanics(t, tracker.RecordMetrics)
}

func TestContextMetricsTracker_NilReceiver(t *testing.T) {
	var tracker *ContextMetricsTracker = nil
	require.NotPanics(t, func() {
		tracker.RecordInitialState(nil, nil)
	})
	require.NotPanics(t, func() {
		tracker.RecordHistoryTrim(false, 0, 0)
	})
	require.NotPanics(t, func() {
		tracker.RecordSummaryTriggered(false)
	})
	require.NotPanics(t, func() {
		tracker.RecordPreCompaction(0)
	})
	require.NotPanics(t, func() {
		tracker.RecordPostCompaction(0, false)
	})
	require.NotPanics(t, func() {
		tracker.RecordToolCompaction(false, 0)
	})
	require.NotPanics(t, func() {
		tracker.RecordOversizedTruncation(false, 0)
	})
	require.NotPanics(t, func() {
		tracker.RecordPreTailoring(nil, 0, "", nil)
	})
	require.NotPanics(t, func() {
		tracker.RecordPostTailoring(nil, nil)
	})
	require.NotPanics(t, func() {
		tracker.RecordFinalUsage(nil, 0)
	})
	require.NotPanics(t, func() {
		tracker.RecordMetrics()
	})
}
