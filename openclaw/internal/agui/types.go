//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package agui implements the AG-UI protocol event types and emitter
// for bridging OpenClaw gateway events to AG-UI compatible frontends.
package agui

import "encoding/json"

// EventType identifies one AG-UI protocol event.
type EventType string

const (
	EventTypeRunStarted       EventType = "RUN_STARTED"
	EventTypeRunFinished      EventType = "RUN_FINISHED"
	EventTypeTextMessageStart EventType = "TEXT_MESSAGE_START"
	EventTypeTextMessageContent EventType = "TEXT_MESSAGE_CONTENT"
	EventTypeTextMessageEnd   EventType = "TEXT_MESSAGE_END"
	EventTypeToolCallStart    EventType = "TOOL_CALL_START"
	EventTypeToolCallArgs     EventType = "TOOL_CALL_ARGS"
	EventTypeToolCallEnd      EventType = "TOOL_CALL_END"
	EventTypeStateSnapshot    EventType = "STATE_SNAPSHOT"
	EventTypeStateDelta       EventType = "STATE_DELTA"
	EventTypeStepStarted      EventType = "STEP_STARTED"
	EventTypeStepFinished     EventType = "STEP_FINISHED"
	EventTypeCustom           EventType = "CUSTOM"
)

// Event is one AG-UI protocol event.
type Event struct {
	Type EventType        `json:"type"`
	Data *json.RawMessage `json:"data,omitempty"`
}

// RunStartedData is the data payload for RUN_STARTED events.
type RunStartedData struct {
	ThreadID string `json:"threadId,omitempty"`
	RunID    string `json:"runId,omitempty"`
}

// RunFinishedData is the data payload for RUN_FINISHED events.
type RunFinishedData struct {
	ThreadID string `json:"threadId,omitempty"`
	RunID    string `json:"runId,omitempty"`
}

// TextMessageStartData is the data payload for TEXT_MESSAGE_START events.
type TextMessageStartData struct {
	MessageID string `json:"messageId,omitempty"`
	Role      string `json:"role,omitempty"`
}

// TextMessageContentData is the data payload for TEXT_MESSAGE_CONTENT events.
type TextMessageContentData struct {
	MessageID string `json:"messageId,omitempty"`
	Delta     string `json:"delta"`
}

// TextMessageEndData is the data payload for TEXT_MESSAGE_END events.
type TextMessageEndData struct {
	MessageID string `json:"messageId,omitempty"`
}

// ToolCallStartData is the data payload for TOOL_CALL_START events.
type ToolCallStartData struct {
	ToolCallID string `json:"toolCallId,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
}

// ToolCallArgsData is the data payload for TOOL_CALL_ARGS events.
type ToolCallArgsData struct {
	ToolCallID string `json:"toolCallId,omitempty"`
	Delta      string `json:"delta"`
}

// ToolCallEndData is the data payload for TOOL_CALL_END events.
type ToolCallEndData struct {
	ToolCallID string `json:"toolCallId,omitempty"`
}

// StateSnapshotData is the data payload for STATE_SNAPSHOT events.
type StateSnapshotData struct {
	State map[string]json.RawMessage `json:"state,omitempty"`
}

// StateDeltaData is the data payload for STATE_DELTA events.
type StateDeltaData struct {
	Delta map[string]json.RawMessage `json:"delta,omitempty"`
}

// StepStartedData is the data payload for STEP_STARTED events.
type StepStartedData struct {
	StepName string `json:"stepName,omitempty"`
}

// StepFinishedData is the data payload for STEP_FINISHED events.
type StepFinishedData struct {
	StepName string `json:"stepName,omitempty"`
}

// CustomData is the data payload for CUSTOM events.
type CustomData struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value,omitempty"`
}
