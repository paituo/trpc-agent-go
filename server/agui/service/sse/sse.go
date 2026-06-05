//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sse provides SSE service implementation.
package sse

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	aguisse "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// sse is a SSE service implementation.
type sse struct {
	path                    string
	messagesSnapshotPath    string
	cancelPath              string
	contextUsagePath        string
	writer                  *aguisse.SSEWriter
	runner                  aguirunner.Runner
	handler                 http.Handler
	messagesSnapshotEnabled bool
	cancelEnabled           bool
	contextUsageEnabled     bool
	heartbeatInterval       time.Duration
	sessionService          interface {
		GetSession(ctx context.Context, key session.Key) (*session.Session, error)
	}
}

// New creates a new SSE service.
func New(runner aguirunner.Runner, opt ...service.Option) service.Service {
	opts := service.NewOptions(opt...)
	s := &sse{
		path:                    opts.Path,
		messagesSnapshotPath:    opts.MessagesSnapshotPath,
		cancelPath:              opts.CancelPath,
		contextUsagePath:        opts.ContextUsagePath,
		runner:                  runner,
		writer:                  aguisse.NewSSEWriter(),
		messagesSnapshotEnabled: opts.MessagesSnapshotEnabled,
		cancelEnabled:           opts.CancelEnabled,
		contextUsageEnabled:     opts.ContextUsageEnabled,
		heartbeatInterval:       opts.HeartbeatInterval,
		sessionService:          opts.SessionService,
	}
	h := http.NewServeMux()
	h.HandleFunc(s.path, s.handle)
	if s.messagesSnapshotEnabled {
		h.HandleFunc(s.messagesSnapshotPath, s.handleMessagesSnapshot)
	}
	if s.cancelEnabled {
		h.HandleFunc(s.cancelPath, s.handleCancel)
	}
	if s.contextUsageEnabled {
		h.HandleFunc(s.contextUsagePath, s.handleContextUsage)
	}
	s.handler = h
	return s
}

// Handler returns an http.Handler that exposes the AG-UI SSE endpoint.
func (s *sse) Handler() http.Handler {
	return s.handler
}

// handle handles an AG-UI run request.
func (s *sse) handle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log.DebugfContext(
		ctx,
		"agui handle: path: %s, method: %s",
		s.path,
		r.Method,
	)
	if r.Method == http.MethodOptions {
		log.DebugContext(ctx, "agui handle: options request")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", http.MethodPost)
		if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
			w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		log.DebugfContext(
			ctx,
			"agui handle: method not allowed, method: %s",
			r.Method,
		)
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.runner == nil {
		log.ErrorfContext(
			ctx,
			"agui handle: runner not configured",
		)
		http.Error(w, "runner not configured", http.StatusInternalServerError)
		return
	}
	runAgentInput, err := runAgentInputFromReader(r.Body)
	if err != nil {
		log.WarnfContext(
			ctx,
			"agui handle: parse run agent input: %v",
			err,
		)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	eventsCh, err := s.runner.Run(ctx, runAgentInput)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"agui handle: threadID: %s, runID: %s, run agent: %v",
			runAgentInput.ThreadID,
			runAgentInput.RunID,
			err,
		)
		status := http.StatusInternalServerError
		if errors.Is(err, aguirunner.ErrRunAlreadyExists) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if err := s.handleEvents(ctx, w, eventsCh, true); err != nil {
		log.ErrorfContext(
			ctx,
			"agui handle: threadID: %s, runID: %s, write event: %v",
			runAgentInput.ThreadID,
			runAgentInput.RunID,
			err,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleMessagesSnapshot streams a synthetic snapshot run to the client.
func (s *sse) handleMessagesSnapshot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log.DebugfContext(
		ctx,
		"agui handle messages snapshot: path: %s, method: %s",
		s.messagesSnapshotPath,
		r.Method,
	)
	if r.Method == http.MethodOptions {
		log.DebugContext(
			ctx,
			"agui handle messages snapshot: options request",
		)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", http.MethodPost)
		if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
			w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		log.DebugfContext(
			ctx,
			"agui handle messages snapshot: method not allowed, "+
				"method: %s",
			r.Method,
		)
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.runner == nil {
		log.ErrorfContext(
			ctx,
			"agui handle messages snapshot: runner not configured",
		)
		http.Error(w, "runner not configured", http.StatusInternalServerError)
		return
	}
	runAgentInput, err := runAgentInputFromReader(r.Body)
	if err != nil {
		log.WarnfContext(
			ctx,
			"agui handle messages snapshot: parse run agent "+
				"input: %v",
			err,
		)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	messagesSnapshotter, ok := s.runner.(aguirunner.MessagesSnapshotter)
	if !ok {
		log.ErrorfContext(
			ctx,
			"agui handle messages snapshot: runner does not "+
				"support messages snapshot",
		)
		http.Error(w, "runner does not support messages snapshot", http.StatusNotImplemented)
		return
	}
	eventsCh, err := messagesSnapshotter.MessagesSnapshot(ctx, runAgentInput)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"agui handle messages snapshot: threadID: %s, runID: "+
				"%s, messages snapshot: %v",
			runAgentInput.ThreadID,
			runAgentInput.RunID,
			err,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if err := s.handleEvents(ctx, w, eventsCh, false); err != nil {
		log.ErrorfContext(
			ctx,
			"agui handle messages snapshot: threadID: %s, "+
				"runID: %s, write event: %v",
			runAgentInput.ThreadID,
			runAgentInput.RunID,
			err,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *sse) handleEvents(
	ctx context.Context,
	w http.ResponseWriter,
	events <-chan aguievents.Event,
	drain bool,
) error {
	var heartbeat <-chan time.Time
	if s.heartbeatInterval > 0 {
		ticker := time.NewTicker(s.heartbeatInterval)
		defer ticker.Stop()
		heartbeat = ticker.C
	}
	for {
		select {
		case <-ctx.Done():
			if drain {
				go drainEvents(events)
			}
			return nil
		case <-heartbeat:
			if err := writeHeartbeat(w); err != nil {
				if drain {
					go drainEvents(events)
				}
				return err
			}
		case evt, ok := <-events:
			if !ok {
				return nil
			}
			if err := s.writer.WriteEvent(ctx, w, evt); err != nil {
				if drain {
					go drainEvents(events)
				}
				return err
			}
		}
	}
}

func writeHeartbeat(w http.ResponseWriter) error {
	if _, err := w.Write([]byte(":\n\n")); err != nil {
		return err
	}
	if flusher, ok := w.(interface{ Flush() }); ok {
		flusher.Flush()
	}
	return nil
}

// handleCancel cancels a running run identified by the request payload.
func (s *sse) handleCancel(w http.ResponseWriter, r *http.Request) {
	ctx := context.WithoutCancel(r.Context())
	log.DebugfContext(
		ctx,
		"agui handle cancel: path: %s, method: %s",
		s.cancelPath,
		r.Method,
	)
	if r.Method == http.MethodOptions {
		log.DebugContext(ctx, "agui handle cancel: options request")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", http.MethodPost)
		if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
			w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		log.DebugfContext(
			ctx,
			"agui handle cancel: method not allowed, method: %s",
			r.Method,
		)
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.runner == nil {
		log.ErrorfContext(
			ctx,
			"agui handle cancel: runner not configured",
		)
		http.Error(w, "runner not configured", http.StatusInternalServerError)
		return
	}
	runAgentInput, err := runAgentInputFromReader(r.Body)
	if err != nil {
		log.WarnfContext(
			ctx,
			"agui handle cancel: parse run agent input: %v",
			err,
		)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	canceler, ok := s.runner.(aguirunner.Canceler)
	if !ok {
		log.ErrorfContext(
			ctx,
			"agui handle cancel: runner does not support cancel",
		)
		http.Error(w, "runner does not support cancel", http.StatusNotImplemented)
		return
	}
	if err := canceler.Cancel(ctx, runAgentInput); err != nil {
		log.ErrorfContext(
			ctx,
			"agui handle cancel: threadID: %s, runID: %s, cancel: %v",
			runAgentInput.ThreadID,
			runAgentInput.RunID,
			err,
		)
		status := http.StatusInternalServerError
		if errors.Is(err, aguirunner.ErrRunNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
}

// runAgentInputFromReader parses an AG-UI run request payload from a reader.
func runAgentInputFromReader(r io.Reader) (*adapter.RunAgentInput, error) {
	var input adapter.RunAgentInput
	dec := json.NewDecoder(r)
	if err := dec.Decode(&input); err != nil {
		return nil, err
	}
	return &input, nil
}

func drainEvents(events <-chan aguievents.Event) {
	for range events {
	}
}

// contextUsageRequest is the request body for the context usage endpoint.
type contextUsageRequest struct {
	ThreadID  string `json:"threadId"`
	UserID    string `json:"userId"`
	AppName   string `json:"appName"`
	ModelName string `json:"modelName"`
}

// contextUsageResponse is the combined response for context usage and contents.
type contextUsageResponse struct {
	Usage    *session.ContextUsage    `json:"usage"`
	Contents *session.ContextContents `json:"contents"`
}

// handleContextUsage handles a POST request to retrieve the current context
// usage overview and content inventory for a session.
func (s *sse) handleContextUsage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log.DebugfContext(
		ctx,
		"agui handle context usage: path: %s, method: %s",
		s.contextUsagePath,
		r.Method,
	)
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", http.MethodPost)
		if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
			w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.sessionService == nil {
		log.ErrorfContext(ctx, "agui handle context usage: session service not configured")
		http.Error(w, "session service not configured", http.StatusInternalServerError)
		return
	}

	var req contextUsageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.WarnfContext(ctx, "agui handle context usage: parse request: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.AppName == "" || req.UserID == "" || req.ThreadID == "" {
		http.Error(w, "appName, userId, and threadId are required", http.StatusBadRequest)
		return
	}
	if req.ModelName == "" {
		http.Error(w, "modelName is required", http.StatusBadRequest)
		return
	}

	key := session.Key{
		AppName:   req.AppName,
		UserID:    req.UserID,
		SessionID: req.ThreadID,
	}

	sess, err := s.sessionService.GetSession(ctx, key)
	if err != nil {
		log.ErrorfContext(ctx, "agui handle context usage: get session: %v", err)
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	usage, err := session.ComputeContextUsage(ctx, sess, req.ModelName, nil, session.DefaultContextUsageConfig())
	if err != nil {
		log.ErrorfContext(ctx, "agui handle context usage: compute usage: %v", err)
		http.Error(w, "failed to compute context usage", http.StatusInternalServerError)
		return
	}

	contents, err := session.ComputeContextContents(ctx, sess, req.ModelName, nil)
	if err != nil {
		log.ErrorfContext(ctx, "agui handle context usage: compute contents: %v", err)
		http.Error(w, "failed to compute context contents", http.StatusInternalServerError)
		return
	}

	resp := contextUsageResponse{
		Usage:    usage,
		Contents: contents,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.ErrorfContext(ctx, "agui handle context usage: encode response: %v", err)
	}
}
