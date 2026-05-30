package telemetry

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestRecordCompactionTrigger(t *testing.T) {
	before := GetCompactionTriggerCount()
	RecordCompactionTrigger(context.Background())
	after := GetCompactionTriggerCount()
	if after != before+1 {
		t.Fatalf("expected %d, got %d", before+1, after)
	}
}

func TestRecordTokenUsageRatio(t *testing.T) {
	RecordTokenUsageRatio(context.Background(), 0.75)
	got := GetTokenUsageRatio()
	if got != 0.75 {
		t.Fatalf("expected 0.75, got %f", got)
	}
}

func TestGetTokenUsageRatio_Default(t *testing.T) {
	old := ContextTokenUsageRatio
	ContextTokenUsageRatio = atomic.Value{}
	got := GetTokenUsageRatio()
	if got != 0 {
		t.Fatalf("expected 0, got %f", got)
	}
	ContextTokenUsageRatio = old
}