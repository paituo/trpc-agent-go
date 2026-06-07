//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// mockTokenCounter is a test TokenCounter that returns a fixed value.
type mockTokenCounter struct {
	count int
	err   error
}

func (m *mockTokenCounter) CountTokens(_ context.Context, _ Message) (int, error) {
	return m.count, m.err
}

func (m *mockTokenCounter) CountTokensRange(_ context.Context, _ []Message, _, _ int) (int, error) {
	return m.count, m.err
}

func (m *mockTokenCounter) RecordEstimate(_ context.Context, _ []Message) (int, error) {
	return m.count, m.err
}

func TestCalibratingTokenCounter_CountTokens(t *testing.T) {
	inner := &mockTokenCounter{count: 100}
	cc := NewCalibratingTokenCounter(inner)

	// Before calibration, factor is 1.0 so result equals inner count.
	n, err := cc.CountTokens(context.Background(), Message{Content: "hello"})
	require.NoError(t, err)
	require.Equal(t, 100, n)
	require.Equal(t, 1.0, cc.Factor())
	require.False(t, cc.Calibrated())
}

func TestCalibratingTokenCounter_Calibrate(t *testing.T) {
	inner := &mockTokenCounter{count: 100}
	cc := NewCalibratingTokenCounter(inner)

	// First calibration: factor = 200/100 = 2.0
	cc.Calibrate(100, 200)
	require.True(t, cc.Calibrated())
	require.Equal(t, 2.0, cc.Factor())

	// After calibration, CountTokens returns corrected value.
	n, err := cc.CountTokens(context.Background(), Message{Content: "hello"})
	require.NoError(t, err)
	require.Equal(t, 200, n)

	// Second calibration: EMA with alpha=0.5
	// newFactor = 150/100 = 1.5
	// factor = 0.5*2.0 + 0.5*1.5 = 1.75
	cc.Calibrate(100, 150)
	require.Equal(t, 1.75, cc.Factor())
}

func TestCalibratingTokenCounter_Calibrate_SkipsInvalid(t *testing.T) {
	inner := &mockTokenCounter{count: 100}
	cc := NewCalibratingTokenCounter(inner)

	// Zero estimated tokens → skip
	cc.Calibrate(0, 200)
	require.False(t, cc.Calibrated())

	// Zero actual tokens → skip
	cc.Calibrate(100, 0)
	require.False(t, cc.Calibrated())

	// Negative → skip
	cc.Calibrate(-1, 200)
	require.False(t, cc.Calibrated())
}

func TestCalibratingTokenCounter_CalibrateFromActual(t *testing.T) {
	inner := &mockTokenCounter{count: 100}
	cc := NewCalibratingTokenCounter(inner)

	msgs := []Message{{Content: "hello"}, {Content: "world"}}

	// CountTokens does NOT accumulate; RecordEstimate does.
	_, _ = cc.CountTokens(context.Background(), Message{Content: "hello"})
	_, _ = cc.CountTokens(context.Background(), Message{Content: "world"})
	// accumulatedRaw should still be 0 (CountTokens does not accumulate).

	// RecordEstimate accumulates: 100 tokens per call.
	_, _ = cc.RecordEstimate(context.Background(), msgs)
	// accumulatedRaw = 100

	// Calibrate with actual = 150 → factor = 150/100 = 1.5
	cc.CalibrateFromActual(150)
	require.True(t, cc.Calibrated())
	require.Equal(t, 1.5, cc.Factor())

	// After calibration, accumulator is reset.
	// RecordEstimate again → accumulatedRaw = 100
	_, _ = cc.RecordEstimate(context.Background(), msgs)

	// Second calibration: newFactor = 250/100 = 2.5
	// factor = 0.5*1.5 + 0.5*2.5 = 2.0
	cc.CalibrateFromActual(250)
	require.Equal(t, 2.0, cc.Factor())
}

func TestCalibratingTokenCounter_CalibrateFromActual_SkipsInvalid(t *testing.T) {
	inner := &mockTokenCounter{count: 100}
	cc := NewCalibratingTokenCounter(inner)

	// No accumulated raw → skip
	cc.CalibrateFromActual(200)
	require.False(t, cc.Calibrated())

	// Accumulate then calibrate with zero actual → skip
	_, _ = cc.RecordEstimate(context.Background(), []Message{{Content: "hello"}})
	cc.CalibrateFromActual(0)
	require.False(t, cc.Calibrated())
}

func TestCalibratingTokenCounter_NilInner(t *testing.T) {
	cc := NewCalibratingTokenCounter(nil)
	// Should fall back to SimpleTokenCounter
	require.NotNil(t, cc)
	n, err := cc.CountTokens(context.Background(), Message{Content: "hello"})
	require.NoError(t, err)
	require.Greater(t, n, 0)
}

func TestCalibratingTokenCounter_CountTokensRange(t *testing.T) {
	inner := &mockTokenCounter{count: 50}
	cc := NewCalibratingTokenCounter(inner)

	msgs := []Message{
		{Content: "hello"},
		{Content: "world"},
	}

	// Before calibration
	n, err := cc.CountTokensRange(context.Background(), msgs, 0, 2)
	require.NoError(t, err)
	require.Equal(t, 50, n)

	// CountTokensRange does NOT accumulate; use RecordEstimate.
	_, _ = cc.RecordEstimate(context.Background(), msgs)
	// accumulatedRaw = 50

	// Calibrate: factor = 100/50 = 2.0
	cc.CalibrateFromActual(100)
	require.Equal(t, 2.0, cc.Factor())

	// After calibration
	n, err = cc.CountTokensRange(context.Background(), msgs, 0, 2)
	require.NoError(t, err)
	require.Equal(t, 100, n)
}

func TestCalibratingTokenCounter_CalibrateFromActual_ResetsAccumulator(t *testing.T) {
	inner := &mockTokenCounter{count: 100}
	cc := NewCalibratingTokenCounter(inner)

	msgs := []Message{{Content: "a"}, {Content: "b"}, {Content: "c"}}

	// RecordEstimate accumulates: 100 tokens
	_, _ = cc.RecordEstimate(context.Background(), msgs)

	// Calibrate: factor = 300/100 = 3.0, accumulator reset to 0
	cc.CalibrateFromActual(300)
	require.Equal(t, 3.0, cc.Factor())

	// Now RecordEstimate again → accumulatedRaw = 100
	_, _ = cc.RecordEstimate(context.Background(), []Message{{Content: "d"}})

	// Second calibration: newFactor = 150/100 = 1.5
	// factor = 0.5*3.0 + 0.5*1.5 = 2.25
	cc.CalibrateFromActual(150)
	require.Equal(t, 2.25, cc.Factor())
}

func TestCalibratingTokenCounter_MinimumOneToken(t *testing.T) {
	inner := &mockTokenCounter{count: 1}
	cc := NewCalibratingTokenCounter(inner)

	// Calibrate with a very small factor: factor = 0.1/1 = 0.1
	cc.Calibrate(1, 0) // This is skipped because actual <= 0
	cc.Calibrate(10, 1) // factor = 0.1

	// CountTokens should return at least 1
	n, err := cc.CountTokens(context.Background(), Message{Content: "hello"})
	require.NoError(t, err)
	require.Equal(t, 1, n) // max(1, int(1*0.1)) = max(1, 0) = 1
}

func TestCalibratingTokenCounter_CountTokensDoesNotAccumulate(t *testing.T) {
	inner := &mockTokenCounter{count: 100}
	cc := NewCalibratingTokenCounter(inner)

	// Call CountTokens multiple times — should NOT accumulate.
	_, _ = cc.CountTokens(context.Background(), Message{Content: "a"})
	_, _ = cc.CountTokens(context.Background(), Message{Content: "b"})
	_, _ = cc.CountTokens(context.Background(), Message{Content: "c"})

	// Call CountTokensRange — should NOT accumulate.
	_, _ = cc.CountTokensRange(context.Background(), []Message{{Content: "d"}}, 0, 1)

	// No accumulation → CalibrateFromActual should skip.
	cc.CalibrateFromActual(300)
	require.False(t, cc.Calibrated()) // Not calibrated because accumulatedRaw = 0
	require.Equal(t, 1.0, cc.Factor())
}

func TestCalibratingTokenCounter_RecordEstimateAccumulates(t *testing.T) {
	inner := &mockTokenCounter{count: 100}
	cc := NewCalibratingTokenCounter(inner)

	msgs := []Message{{Content: "hello"}, {Content: "world"}}

	// RecordEstimate accumulates.
	_, _ = cc.RecordEstimate(context.Background(), msgs)
	// accumulatedRaw = 100

	// Calibrate: factor = 200/100 = 2.0
	cc.CalibrateFromActual(200)
	require.True(t, cc.Calibrated())
	require.Equal(t, 2.0, cc.Factor())

	// After calibration, RecordEstimate returns corrected value.
	n, err := cc.RecordEstimate(context.Background(), msgs)
	require.NoError(t, err)
	require.Equal(t, 200, n) // 100 * 2.0 = 200

	// And accumulates the raw (uncorrected) estimate for next calibration.
	// accumulatedRaw = 100 (raw from inner counter)
	cc.CalibrateFromActual(150)
	// newFactor = 150/100 = 1.5
	// factor = 0.5*2.0 + 0.5*1.5 = 1.75
	require.Equal(t, 1.75, cc.Factor())
}

func TestCalibratingTokenCounter_ConcurrentRecordEstimate(t *testing.T) {
	inner := &mockTokenCounter{count: 100}
	cc := NewCalibratingTokenCounter(inner)

	msgs := []Message{{Content: "hello"}}

	// Simulate concurrent RecordEstimate calls (safe without SetAccumulateEnabled).
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			_, _ = cc.RecordEstimate(context.Background(), msgs)
		}
		close(done)
	}()
	for i := 0; i < 100; i++ {
		_, _ = cc.RecordEstimate(context.Background(), msgs)
	}
	<-done

	// accumulatedRaw should be 200 * 100 = 20000
	cc.CalibrateFromActual(40000)
	require.True(t, cc.Calibrated())
	require.Equal(t, 2.0, cc.Factor()) // 40000/20000 = 2.0
}
