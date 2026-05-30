package telemetry

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNewContextMetricsTracker(t *testing.T) {
	config := ContextConfigSnapshot{
		EnableCompaction: true,
	}
	tracker := NewContextMetricsTracker(context.Background(), nil, config)
	if tracker == nil {
		t.Fatal("expected non-nil tracker")
	}
}

func TestRecordInitialState(t *testing.T) {
	config := ContextConfigSnapshot{}
	tracker := NewContextMetricsTracker(context.Background(), nil, config)
	messages := []model.Message{
		{Role: model.RoleUser, Content: "hello"},
	}
	tracker.RecordInitialState(messages, model.NewSimpleTokenCounter())
	if tracker.initialMessageCount != 1 {
		t.Fatalf("expected 1, got %d", tracker.initialMessageCount)
	}
}

func TestRecordHistoryTrim(t *testing.T) {
	config := ContextConfigSnapshot{}
	tracker := NewContextMetricsTracker(context.Background(), nil, config)
	tracker.RecordHistoryTrim(true, 10, 5)
	if !tracker.historyTrimTriggered {
		t.Fatal("expected history trim triggered")
	}
}

func TestRecordSummaryTriggered(t *testing.T) {
	config := ContextConfigSnapshot{}
	tracker := NewContextMetricsTracker(context.Background(), nil, config)
	tracker.RecordSummaryTriggered(true)
	if !tracker.summaryTriggered {
		t.Fatal("expected summary triggered")
	}
}

func TestRecordCompaction(t *testing.T) {
	config := ContextConfigSnapshot{}
	tracker := NewContextMetricsTracker(context.Background(), nil, config)
	tracker.RecordPreCompaction(1000)
	tracker.RecordPostCompaction(500, true)
	if !tracker.compactionTriggered {
		t.Fatal("expected compaction triggered")
	}
	if tracker.preCompactionTokens != 1000 {
		t.Fatalf("expected 1000, got %d", tracker.preCompactionTokens)
	}
	if tracker.postCompactionTokens != 500 {
		t.Fatalf("expected 500, got %d", tracker.postCompactionTokens)
	}
}

func TestToTraceChatContextMetrics_Nil(t *testing.T) {
	var tracker *ContextMetricsTracker
	result := tracker.ToTraceChatContextMetrics()
	if result != nil {
		t.Fatal("expected nil")
	}
}

func TestWithContextMetricsTracker(t *testing.T) {
	config := ContextConfigSnapshot{}
	tracker := NewContextMetricsTracker(context.Background(), nil, config)
	ctx := WithContextMetricsTracker(context.Background(), tracker)
	got := ContextMetricsTrackerFromContext(ctx)
	if got != tracker {
		t.Fatal("expected same tracker from context")
	}
}

func TestContextMetricsTrackerFromContext_NotFound(t *testing.T) {
	got := ContextMetricsTrackerFromContext(context.Background())
	if got != nil {
		t.Fatal("expected nil")
	}
}