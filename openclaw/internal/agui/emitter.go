//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agui

import (
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
)

// Emitter converts OpenClaw StreamEvents into AG-UI protocol events.
type Emitter struct {
	threadID  string
	runID     string
	messageID string
}

// NewEmitter creates a new AG-UI event emitter.
func NewEmitter() *Emitter {
	return &Emitter{}
}

// Convert maps one OpenClaw StreamEvent to zero or more AG-UI events.
// The mapping follows the AG-UI protocol specification:
//
//	gwproto.StreamEventTypeRunStarted      → RUN_STARTED
//	gwproto.StreamEventTypeRunCompleted     → RUN_FINISHED
//	gwproto.StreamEventTypeMessageDelta     → TEXT_MESSAGE_CONTENT
//	gwproto.StreamEventTypeMessageCompleted → TEXT_MESSAGE_END
//	gwproto.StreamEventTypeRunProgress      → TOOL_CALL_START/END or STEP_STARTED/FINISHED
//	gwproto.StreamEventTypeStateDelta       → STATE_DELTA
//	gwproto.StreamEventTypeRunError         → RUN_FINISHED (with error)
//	gwproto.StreamEventTypeThoughtDelta     → CUSTOM (thought)
func (e *Emitter) Convert(se *gwproto.StreamEvent) []Event {
	if se == nil {
		return nil
	}

	// Track IDs from the stream event.
	if se.SessionID != "" {
		e.threadID = se.SessionID
	}
	if se.RequestID != "" {
		e.runID = se.RequestID
	}
	if se.MessageID != "" {
		e.messageID = se.MessageID
	}

	switch se.Type {
	case gwproto.StreamEventTypeRunStarted:
		return []Event{e.runStarted(se)}

	case gwproto.StreamEventTypeRunCompleted:
		return []Event{e.runFinished(se)}

	case gwproto.StreamEventTypeRunError:
		return []Event{e.runFinished(se)}

	case gwproto.StreamEventTypeRunCanceled:
		return []Event{e.runFinished(se)}

	case gwproto.StreamEventTypeMessageDelta:
		return e.messageDelta(se)

	case gwproto.StreamEventTypeMessageCompleted:
		return e.messageCompleted(se)

	case gwproto.StreamEventTypeThoughtDelta:
		return e.thoughtDelta(se)

	case gwproto.StreamEventTypeThoughtCompleted:
		return e.thoughtCompleted(se)

	case gwproto.StreamEventTypeRunProgress:
		return e.runProgress(se)

	case gwproto.StreamEventTypeStateDelta:
		return e.stateDelta(se)

	default:
		return nil
	}
}

func (e *Emitter) runStarted(se *gwproto.StreamEvent) Event {
	return Event{
		Type: EventTypeRunStarted,
		Data: marshalData(RunStartedData{
			ThreadID: e.threadID,
			RunID:    e.runID,
		}),
	}
}

func (e *Emitter) runFinished(se *gwproto.StreamEvent) Event {
	return Event{
		Type: EventTypeRunFinished,
		Data: marshalData(RunFinishedData{
			ThreadID: e.threadID,
			RunID:    e.runID,
		}),
	}
}

func (e *Emitter) messageDelta(se *gwproto.StreamEvent) []Event {
	var events []Event

	// Emit TEXT_MESSAGE_START on first delta if we haven't seen this message yet.
	if e.messageID != "" && se.Delta != "" {
		events = append(events, Event{
			Type: EventTypeTextMessageStart,
			Data: marshalData(TextMessageStartData{
				MessageID: e.messageID,
				Role:      "assistant",
			}),
		})
	}

	events = append(events, Event{
		Type: EventTypeTextMessageContent,
		Data: marshalData(TextMessageContentData{
			MessageID: e.messageID,
			Delta:     se.Delta,
		}),
	})

	return events
}

func (e *Emitter) messageCompleted(se *gwproto.StreamEvent) []Event {
	return []Event{
		{
			Type: EventTypeTextMessageEnd,
			Data: marshalData(TextMessageEndData{
				MessageID: e.messageID,
			}),
		},
	}
}

func (e *Emitter) thoughtDelta(se *gwproto.StreamEvent) []Event {
	return []Event{
		{
			Type: EventTypeCustom,
			Data: marshalData(CustomData{
				Name:  "thought",
				Value: json.RawMessage(fmt.Sprintf(`"%s"`, jsonEscape(se.Delta))),
			}),
		},
	}
}

func (e *Emitter) thoughtCompleted(se *gwproto.StreamEvent) []Event {
	return []Event{
		{
			Type: EventTypeCustom,
			Data: marshalData(CustomData{
				Name:  "thought_completed",
				Value: json.RawMessage(fmt.Sprintf(`"%s"`, jsonEscape(se.Reply))),
			}),
		},
	}
}

func (e *Emitter) runProgress(se *gwproto.StreamEvent) []Event {
	var events []Event

	switch se.ToolStatus {
	case gwproto.StreamToolStatusRunning:
		// Tool call started.
		events = append(events, Event{
			Type: EventTypeToolCallStart,
			Data: marshalData(ToolCallStartData{
				ToolCallID: se.ToolCallID,
				ToolName:   se.ToolName,
			}),
		})
		// Also emit a step event for the tool.
		events = append(events, Event{
			Type: EventTypeStepStarted,
			Data: marshalData(StepStartedData{
				StepName: se.ToolName,
			}),
		})

	case gwproto.StreamToolStatusCompleted:
		// Tool call finished.
		events = append(events, Event{
			Type: EventTypeStepFinished,
			Data: marshalData(StepFinishedData{
				StepName: se.ToolName,
			}),
		})
		events = append(events, Event{
			Type: EventTypeToolCallEnd,
			Data: marshalData(ToolCallEndData{
				ToolCallID: se.ToolCallID,
			}),
		})

	default:
		// Generic progress — emit as step.
		events = append(events, Event{
			Type: EventTypeStepStarted,
			Data: marshalData(StepStartedData{
				StepName: string(se.Stage),
			}),
		})
	}

	return events
}

func (e *Emitter) stateDelta(se *gwproto.StreamEvent) []Event {
	return []Event{
		{
			Type: EventTypeStateDelta,
			Data: marshalData(StateDeltaData{
				Delta: se.StateDelta,
			}),
		},
	}
}

// marshalData marshals a data payload into a json.RawMessage pointer.
func marshalData(v interface{}) *json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	rm := json.RawMessage(raw)
	return &rm
}

// jsonEscape returns a JSON-escaped version of the input string.
func jsonEscape(s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	// json.Marshal wraps in quotes; strip them.
	if len(raw) >= 2 {
		return string(raw[1 : len(raw)-1])
	}
	return ""
}
