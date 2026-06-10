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
	"net/http"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
)

// SSEServer serves AG-UI events via Server-Sent Events.
type SSEServer struct {
	mu      sync.RWMutex
	subs    map[string]chan Event // sessionID -> event channel
	emitter *Emitter
}

// NewSSEServer creates a new AG-UI SSE server.
func NewSSEServer() *SSEServer {
	return &SSEServer{
		subs:    make(map[string]chan Event),
		emitter: NewEmitter(),
	}
}

// Subscribe registers a subscriber for a given session and returns
// a channel that receives AG-UI events.
func (s *SSEServer) Subscribe(sessionID string) <-chan Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan Event, 64)
	s.subs[sessionID] = ch
	return ch
}

// Unsubscribe removes a subscriber for a given session.
func (s *SSEServer) Unsubscribe(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ch, ok := s.subs[sessionID]; ok {
		close(ch)
		delete(s.subs, sessionID)
	}
}

// PublishStreamEvent converts a gateway StreamEvent to AG-UI events
// and publishes them to the subscriber for the event's session.
func (s *SSEServer) PublishStreamEvent(se *gwproto.StreamEvent) {
	if se == nil {
		return
	}

	events := s.emitter.Convert(se)
	if len(events) == 0 {
		return
	}

	s.mu.RLock()
	ch, ok := s.subs[se.SessionID]
	s.mu.RUnlock()

	if !ok {
		return
	}

	for _, evt := range events {
		select {
		case ch <- evt:
		default:
			// Drop event if subscriber is too slow.
		}
	}
}

// HandleSSE handles an HTTP request for the AG-UI SSE stream.
// The session_id query parameter identifies the subscriber.
func (s *SSEServer) HandleSSE(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := s.Subscribe(sessionID)
	defer s.Unsubscribe(sessionID)

	for {
		select {
		case <-r.Context().Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Type, data)
			flusher.Flush()
		}
	}
}
