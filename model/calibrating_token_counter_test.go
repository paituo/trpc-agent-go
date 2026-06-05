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

	// Call CountTokens a few times to accumulate raw estimates.
	_, _ = cc.CountTokens(context.Background(), Message{Content: "hello"})
	_, _ = cc.CountTokens(context.Background(), Message{Content: "world"})
	// accumulatedRaw = 100 + 100 = 200

	// Calibrate with actual = 300 → factor = 300/200 = 1.5
	cc.CalibrateFromActual(300)
	require.True(t, cc.Calibrated())
	require.Equal(t, 1.5, cc.Factor())

	// After calibration, accumulator is reset.
	// Call CountTokens once → accumulatedRaw = 100
	_, _ = cc.CountTokens(context.Background(), Message{Content: "test"})

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
	_, _ = cc.CountTokens(context.Background(), Message{Content: "hello"})
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

	// Calibrate: factor = 100/50 = 2.0
	// (accumulatedRaw = 50 from the CountTokensRange call)
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

	// Accumulate 3 calls → accumulatedRaw = 300
	_, _ = cc.CountTokens(context.Background(), Message{Content: "a"})
	_, _ = cc.CountTokens(context.Background(), Message{Content: "b"})
	_, _ = cc.CountTokens(context.Background(), Message{Content: "c"})

	// Calibrate: factor = 600/300 = 2.0, accumulator reset to 0
	cc.CalibrateFromActual(600)
	require.Equal(t, 2.0, cc.Factor())

	// Now accumulate 1 call → accumulatedRaw = 100
	_, _ = cc.CountTokens(context.Background(), Message{Content: "d"})

	// Second calibration: newFactor = 150/100 = 1.5
	// factor = 0.5*2.0 + 0.5*1.5 = 1.75
	cc.CalibrateFromActual(150)
	require.Equal(t, 1.75, cc.Factor())
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
