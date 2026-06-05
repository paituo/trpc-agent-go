//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package model

// NewTestInfo returns a model.Info suitable for test mocks.
// It automatically sets TokenCounter based on the model name.
func NewTestInfo(name string) Info {
	return Info{
		Name:         name,
		TokenCounter: NewTokenCounter(name),
	}
}

// NewTestInfoWithWindow returns a model.Info with a specific context window.
func NewTestInfoWithWindow(name string, window int) Info {
	return Info{
		Name:          name,
		ContextWindow: window,
		TokenCounter:  NewTokenCounter(name),
	}
}
