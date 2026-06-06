//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gwproto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageJSONIncludesZeroCounters(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(struct {
		Usage *Usage `json:"usage,omitempty"`
	}{
		Usage: &Usage{},
	})
	require.NoError(t, err)
	require.JSONEq(
		t,
		`{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
		string(payload),
	)
}

func TestStreamToolCallJSON(t *testing.T) {
	t.Parallel()

	// With arguments
	tc := StreamToolCall{
		ID:        "tc1",
		Name:      "read_file",
		Arguments: json.RawMessage(`{"path":"/tmp/a"}`),
	}
	data, err := json.Marshal(tc)
	require.NoError(t, err)
	require.Contains(t, string(data), `"id":"tc1"`)
	require.Contains(t, string(data), `"name":"read_file"`)
	require.Contains(t, string(data), `"arguments":`)

	// Without arguments (omitempty)
	tc2 := StreamToolCall{ID: "tc2", Name: "write_file"}
	data2, err := json.Marshal(tc2)
	require.NoError(t, err)
	require.NotContains(t, string(data2), `"arguments"`)
}

func TestStreamEventToolCallsOmitted(t *testing.T) {
	t.Parallel()

	evt := StreamEvent{Type: StreamEventTypeRunProgress}
	data, err := json.Marshal(evt)
	require.NoError(t, err)
	require.NotContains(t, string(data), `"tool_calls"`)
}

func TestStreamEventMessageID(t *testing.T) {
	t.Parallel()

	evt := StreamEvent{Type: StreamEventTypeMessageDelta, MessageID: "msg-123"}
	data, err := json.Marshal(evt)
	require.NoError(t, err)
	require.Contains(t, string(data), `"message_id":"msg-123"`)

	// Empty MessageID is omitted
	evt2 := StreamEvent{Type: StreamEventTypeMessageDelta}
	data2, err := json.Marshal(evt2)
	require.NoError(t, err)
	require.NotContains(t, string(data2), `"message_id"`)
}

func TestMessageStreamOptionsNoTruncateTools(t *testing.T) {
	t.Parallel()

	opts := MessageStreamOptions{
		ProgressAfterTextDelta: true,
		NoTruncateTools:        []string{"todo_write", "plan_search"},
	}
	data, err := json.Marshal(opts)
	require.NoError(t, err)
	require.Contains(t, string(data), `"no_truncate_tools"`)
	require.Contains(t, string(data), `"todo_write"`)

	// Empty NoTruncateTools is omitted
	opts2 := MessageStreamOptions{ProgressAfterTextDelta: true}
	data2, err := json.Marshal(opts2)
	require.NoError(t, err)
	require.NotContains(t, string(data2), `"no_truncate_tools"`)
}
