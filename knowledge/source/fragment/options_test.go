//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package fragment

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWithName(t *testing.T) {
	s := New(nil, nil, WithName("my-source"))
	require.Equal(t, "my-source", s.name)
}

func TestWithMetadata(t *testing.T) {
	s := New(nil, nil, WithMetadata(map[string]any{"key": "value"}))
	require.Equal(t, "value", s.metadata["key"])
}

func TestWithMetadataMerges(t *testing.T) {
	s := New(nil, nil,
		WithMetadata(map[string]any{"a": 1}),
		WithMetadata(map[string]any{"b": 2}),
	)
	require.Equal(t, 1, s.metadata["a"])
	require.Equal(t, 2, s.metadata["b"])
}

func TestWithMetadataValue(t *testing.T) {
	s := New(nil, nil, WithMetadataValue("single", "entry"))
	require.Equal(t, "entry", s.metadata["single"])
}

func TestWithMetadataValueOnNilMap(t *testing.T) {
	s := &Source{}
	WithMetadataValue("key", "val")(s)
	require.Equal(t, "val", s.metadata["key"])
}

func TestDefaultName(t *testing.T) {
	s := New(nil, nil)
	require.Equal(t, defaultSourceName, s.name)
}
