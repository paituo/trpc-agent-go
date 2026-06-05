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
	"sync"
)

// CalibratingTokenCounter wraps a TokenCounter and adjusts its estimates
// based on actual token counts reported by the model API.
//
// When the API returns usage.prompt_tokens, calling CalibrateFromActual
// with that value allows the counter to compute a correction factor:
//
//	factor = actualTokens / accumulatedRawEstimated
//
// The counter tracks the sum of raw (inner counter) estimates from
// CountTokens/CountTokensRange calls. When CalibrateFromActual is called,
// it computes the factor from the accumulated raw estimate and the actual
// token count, then resets the accumulator for the next request cycle.
//
// Subsequent calls to CountTokens/CountTokensRange multiply the inner
// counter's estimate by this factor. This compensates for tokenizer
// mismatches caused by API gateways that use a different tokenizer than
// the model name suggests.
//
// CalibratingTokenCounter is safe for concurrent use.
type CalibratingTokenCounter struct {
	inner TokenCounter

	mu               sync.RWMutex
	factor           float64 // correction factor, 1.0 = no correction
	calibrated       bool
	accumulatedRaw   int // sum of raw (inner) estimates since last calibration
}

// NewCalibratingTokenCounter creates a CalibratingTokenCounter wrapping
// the given inner counter. The inner counter must not be nil.
func NewCalibratingTokenCounter(inner TokenCounter) *CalibratingTokenCounter {
	if inner == nil {
		inner = NewSimpleTokenCounter()
	}
	return &CalibratingTokenCounter{
		inner:  inner,
		factor: 1.0,
	}
}

// Calibrate adjusts the correction factor based on an actual token count
// from the API response (usage.prompt_tokens).
//
// estimatedTokens is the sum of CountTokens calls for the messages that
// were sent in the request. actualTokens is the prompt_tokens value from
// the API response.
//
// The correction factor is computed as:
//
//	factor = actualTokens / estimatedTokens
//
// If estimatedTokens is 0, the calibration is skipped.
// Multiple calibrations are averaged using an exponential moving average
// with alpha=0.5 to smooth out per-request variance.
func (c *CalibratingTokenCounter) Calibrate(estimatedTokens, actualTokens int) {
	if estimatedTokens <= 0 || actualTokens <= 0 {
		return
	}

	newFactor := float64(actualTokens) / float64(estimatedTokens)

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.calibrated {
		c.factor = newFactor
		c.calibrated = true
	} else {
		// Exponential moving average with alpha=0.5
		c.factor = 0.5*c.factor + 0.5*newFactor
	}
}

// CalibrateFromActual calibrates the correction factor using the
// accumulated raw (inner counter) estimates and the actual token count
// from the API response. After calibration, the accumulator is reset.
//
// This is the preferred method for automatic calibration: the counter
// tracks raw estimates from CountTokens/CountTokensRange calls, and
// CalibrateFromActual uses them to compute the correction factor without
// requiring the caller to track estimated tokens separately.
//
// If no raw estimates have been accumulated, the calibration is skipped.
func (c *CalibratingTokenCounter) CalibrateFromActual(actualTokens int) {
	if actualTokens <= 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.accumulatedRaw <= 0 {
		return
	}

	newFactor := float64(actualTokens) / float64(c.accumulatedRaw)
	c.accumulatedRaw = 0

	if !c.calibrated {
		c.factor = newFactor
		c.calibrated = true
	} else {
		c.factor = 0.5*c.factor + 0.5*newFactor
	}
}

// Calibrated returns whether the counter has been calibrated at least once.
func (c *CalibratingTokenCounter) Calibrated() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.calibrated
}

// Factor returns the current correction factor.
// Returns 1.0 if not yet calibrated.
func (c *CalibratingTokenCounter) Factor() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.factor
}

// CountTokens estimates tokens for a single message, applying the
// correction factor if calibrated. The raw (inner counter) estimate
// is accumulated for automatic calibration via CalibrateFromActual.
func (c *CalibratingTokenCounter) CountTokens(ctx context.Context, message Message) (int, error) {
	raw, err := c.inner.CountTokens(ctx, message)
	if err != nil {
		return raw, err
	}

	c.mu.Lock()
	c.accumulatedRaw += raw
	f := c.factor
	c.mu.Unlock()

	n := raw
	if f != 1.0 && n > 0 {
		n = max(1, int(float64(n)*f))
	}
	return n, nil
}

// CountTokensRange estimates tokens for a range of messages, applying the
// correction factor if calibrated. The raw (inner counter) estimate
// is accumulated for automatic calibration via CalibrateFromActual.
func (c *CalibratingTokenCounter) CountTokensRange(ctx context.Context, messages []Message, start, end int) (int, error) {
	raw, err := c.inner.CountTokensRange(ctx, messages, start, end)
	if err != nil {
		return raw, err
	}

	c.mu.Lock()
	c.accumulatedRaw += raw
	f := c.factor
	c.mu.Unlock()

	n := raw
	if f != 1.0 && n > 0 {
		n = max(1, int(float64(n)*f))
	}
	return n, nil
}
