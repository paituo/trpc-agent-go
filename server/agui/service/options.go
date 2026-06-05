//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package service

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	// defaultPath is the default path for the AG-UI service.
	defaultPath = "/"
	// defaultMessagesSnapshotPath is the default path for the messages snapshot handler.
	defaultMessagesSnapshotPath = "/history"
	// defaultCancelPath is the default path for the cancel handler.
	defaultCancelPath = "/cancel"
	// defaultContextUsagePath is the default path for the context usage handler.
	defaultContextUsagePath = "/context-usage"
)

// Options holds the options for an AG-UI transport implementation.
type Options struct {
	AppName                 string        // AppName is the name of the application.
	Path                    string        // Path is the request URL path served by the handler.
	MessagesSnapshotEnabled bool          // MessagesSnapshotEnabled enables the messages snapshot handler.
	MessagesSnapshotPath    string        // MessagesSnapshotPath is the HTTP path for the messages snapshot handler.
	CancelEnabled           bool          // CancelEnabled enables the cancel handler.
	CancelPath              string        // CancelPath is the HTTP path for the cancel handler.
	HeartbeatInterval       time.Duration // HeartbeatInterval controls how often heartbeat frames are sent.
	ContextUsageEnabled     bool          // ContextUsageEnabled enables the context usage handler.
	ContextUsagePath        string        // ContextUsagePath is the HTTP path for the context usage handler.
	SessionService          interface {
		GetSession(ctx context.Context, key session.Key) (*session.Session, error)
	} // SessionService is used to retrieve sessions for context usage computation.
}

// NewOptions creates a new options instance.
func NewOptions(opt ...Option) *Options {
	opts := &Options{}
	for _, o := range opt {
		o(opts)
	}
	if opts.Path == "" {
		opts.Path = defaultPath
	}
	if opts.MessagesSnapshotEnabled && opts.MessagesSnapshotPath == "" {
		opts.MessagesSnapshotPath = defaultMessagesSnapshotPath
	}
	if opts.CancelEnabled && opts.CancelPath == "" {
		opts.CancelPath = defaultCancelPath
	}
	if opts.ContextUsageEnabled && opts.ContextUsagePath == "" {
		opts.ContextUsagePath = defaultContextUsagePath
	}
	return opts
}

// Option is a function that configures the options.
type Option func(*Options)

// WithPath sets the request path.
func WithPath(p string) Option {
	return func(s *Options) {
		s.Path = p
	}
}

// WithMessagesSnapshot enables the messages snapshot handler and configures its dependencies.
func WithMessagesSnapshotEnabled(e bool) Option {
	return func(s *Options) {
		s.MessagesSnapshotEnabled = e
	}
}

// WithMessagesSnapshotPath sets the HTTP path for the snapshot handler.
func WithMessagesSnapshotPath(p string) Option {
	return func(s *Options) {
		s.MessagesSnapshotPath = p
	}
}

// WithCancelEnabled enables the cancel handler.
func WithCancelEnabled(e bool) Option {
	return func(s *Options) {
		s.CancelEnabled = e
	}
}

// WithCancelPath sets the HTTP path for the cancel handler.
func WithCancelPath(p string) Option {
	return func(s *Options) {
		s.CancelPath = p
	}
}

// WithHeartbeatInterval sets how often the transport sends heartbeat frames.
func WithHeartbeatInterval(d time.Duration) Option {
	return func(s *Options) {
		s.HeartbeatInterval = d
	}
}

// WithContextUsageEnabled enables the context usage handler.
func WithContextUsageEnabled(e bool) Option {
	return func(s *Options) {
		s.ContextUsageEnabled = e
	}
}

// WithContextUsagePath sets the HTTP path for the context usage handler.
func WithContextUsagePath(p string) Option {
	return func(s *Options) {
		s.ContextUsagePath = p
	}
}

// WithSessionService sets the session service for context usage computation.
func WithSessionService(svc interface {
	GetSession(ctx context.Context, key session.Key) (*session.Session, error)
}) Option {
	return func(s *Options) {
		s.SessionService = svc
	}
}
