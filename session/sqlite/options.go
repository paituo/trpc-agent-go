//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

const (
	defaultSessionEventLimit = 1000

	defaultChanBufferSize = 100

	defaultAsyncPersisterNum = 1

	defaultCleanupInterval = 5 * time.Minute

	defaultDBInitTimeout = 30 * time.Second

	defaultAsyncPersistTimeout = 10 * time.Second

	defaultAsyncSummaryNum   = 3
	defaultSummaryQueueSize  = 100
	defaultSummaryJobTimeout = 60 * time.Second

	defaultIndexDimension = 1536
	defaultMaxResults     = 5
	defaultEmbedTimeout   = 30 * time.Second
	defaultHybridRRFK     = 60
	defaultCandidateRatio = 3
)

// ServiceOpts is the options for the sqlite session service.
type ServiceOpts struct {
	sessionEventLimit int

	sessionTTL         time.Duration
	appStateTTL        time.Duration
	userStateTTL       time.Duration
	enableAsyncPersist bool
	asyncPersisterNum  int
	softDelete         bool
	cleanupInterval    time.Duration

	// summarizer integrates LLM summarization.
	summarizer                summary.SessionSummarizer
	asyncSummaryNum           int
	summaryQueueSize          int
	summaryJobTimeout         time.Duration
	summaryFilterAllowlist    []string
	cascadeFullSessionSummary *bool

	// skipDBInit skips database initialization.
	skipDBInit bool

	// tablePrefix is the prefix for all table names.
	tablePrefix string

	appendEventHooks []session.AppendEventHook
	getSessionHooks  []session.GetSessionHook

	// search-related options
	embedder       embedder.Embedder
	embedTimeout   time.Duration
	indexDimension int
	maxResults     int
	hybridRRFK     int
	candidateRatio int
	enableFTS      bool
	syncIndexing   bool
}

// ServiceOpt is the option for the sqlite session service.
type ServiceOpt func(*ServiceOpts)

var defaultOptions = ServiceOpts{
	sessionEventLimit: defaultSessionEventLimit,
	asyncPersisterNum: defaultAsyncPersisterNum,
	asyncSummaryNum:   defaultAsyncSummaryNum,
	summaryQueueSize:  defaultSummaryQueueSize,
	summaryJobTimeout: defaultSummaryJobTimeout,
	softDelete:        true,
	indexDimension:    defaultIndexDimension,
	maxResults:        defaultMaxResults,
	embedTimeout:      defaultEmbedTimeout,
	hybridRRFK:        defaultHybridRRFK,
	candidateRatio:    defaultCandidateRatio,
}

func (opts ServiceOpts) shouldCascadeFullSessionSummary() bool {
	if opts.cascadeFullSessionSummary == nil {
		return true
	}
	return *opts.cascadeFullSessionSummary
}

// WithSessionEventLimit sets the event limit per session.
func WithSessionEventLimit(limit int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.sessionEventLimit = limit
	}
}

// WithSessionTTL sets the TTL for session state and event list.
func WithSessionTTL(ttl time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.sessionTTL = ttl
	}
}

// WithAppStateTTL sets the TTL for app state.
func WithAppStateTTL(ttl time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.appStateTTL = ttl
	}
}

// WithUserStateTTL sets the TTL for user state.
func WithUserStateTTL(ttl time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.userStateTTL = ttl
	}
}

// WithEnableAsyncPersist enables async persistence.
func WithEnableAsyncPersist(enable bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.enableAsyncPersist = enable
	}
}

// WithAsyncPersisterNum sets the number of async persister workers.
func WithAsyncPersisterNum(num int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if num < 1 {
			num = defaultAsyncPersisterNum
		}
		opts.asyncPersisterNum = num
	}
}

// WithSoftDelete enables or disables soft delete.
func WithSoftDelete(enable bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.softDelete = enable
	}
}

// WithCleanupInterval sets the cleanup interval for expired data.
func WithCleanupInterval(interval time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.cleanupInterval = interval
	}
}

// WithSummarizer injects a summarizer for LLM-based summaries.
func WithSummarizer(s summary.SessionSummarizer) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.summarizer = s
	}
}

// WithAsyncSummaryNum sets the number of async summary workers.
func WithAsyncSummaryNum(num int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if num < 1 {
			num = defaultAsyncSummaryNum
		}
		opts.asyncSummaryNum = num
	}
}

// WithSummaryQueueSize sets the size of the summary job queue.
func WithSummaryQueueSize(size int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if size < 1 {
			size = defaultSummaryQueueSize
		}
		opts.summaryQueueSize = size
	}
}

// WithSummaryJobTimeout sets the timeout for processing one summary job.
func WithSummaryJobTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		if timeout <= 0 {
			return
		}
		opts.summaryJobTimeout = timeout
	}
}

// WithSummaryFilterAllowlist restricts which non-empty filterKeys may trigger
// branch summaries. Keys use the same exact format as event filter keys.
func WithSummaryFilterAllowlist(filterKeys ...string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.summaryFilterAllowlist = append([]string{}, filterKeys...)
	}
}

// WithCascadeFullSessionSummary controls whether an allowed branch summary also
// refreshes the full-session summary keyed by SummaryFilterKeyAllContents.
func WithCascadeFullSessionSummary(enable bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		enabled := enable
		opts.cascadeFullSessionSummary = &enabled
	}
}

// WithSkipDBInit skips database initialization (DDL).
func WithSkipDBInit(skip bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.skipDBInit = skip
	}
}

// WithTablePrefix sets a prefix for all table names.
//
// Security: Uses internal/session/sqldb.ValidateTablePrefix to prevent SQL
// injection.
func WithTablePrefix(prefix string) ServiceOpt {
	return func(opts *ServiceOpts) {
		if prefix == "" {
			opts.tablePrefix = ""
			return
		}
		sqldb.MustValidateTablePrefix(prefix)
		opts.tablePrefix = prefix
	}
}

// WithAppendEventHook adds AppendEvent hooks.
func WithAppendEventHook(hooks ...session.AppendEventHook) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.appendEventHooks = append(opts.appendEventHooks, hooks...)
	}
}

// WithGetSessionHook adds GetSession hooks.
func WithGetSessionHook(hooks ...session.GetSessionHook) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.getSessionHooks = append(opts.getSessionHooks, hooks...)
	}
}

// WithEmbedder sets the embedder for generating event embeddings.
// Required for SearchableService support (semantic search).
func WithEmbedder(e embedder.Embedder) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.embedder = e
	}
}

// WithEmbedTimeout sets the timeout for embedding API calls.
func WithEmbedTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		if timeout > 0 {
			opts.embedTimeout = timeout
		}
	}
}

// WithIndexDimension sets the embedding vector dimension (default: 1536).
func WithIndexDimension(dim int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if dim > 0 {
			opts.indexDimension = dim
		}
	}
}

// WithMaxResults sets the default max results for SearchEvents (default: 5).
func WithMaxResults(n int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if n > 0 {
			opts.maxResults = n
		}
	}
}

// WithHybridRRFK sets the RRF constant used when SearchModeHybrid is enabled (default: 60).
func WithHybridRRFK(k int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if k > 0 {
			opts.hybridRRFK = k
		}
	}
}

// WithHybridCandidateRatio sets how many candidates each hybrid branch fetches before fusion (default: 3x).
func WithHybridCandidateRatio(ratio int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if ratio > 0 {
			opts.candidateRatio = ratio
		}
	}
}

// WithEnableFTS enables full-text search using FTS5 for keyword search.
// Requires building with -tags=sqlite_fts5 to enable FTS5 in the SQLite driver.
func WithEnableFTS(enable bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.enableFTS = enable
	}
}

// WithSyncIndexing controls whether event embeddings are generated synchronously
// after persistence. When false (default), embeddings are generated asynchronously.
func WithSyncIndexing(sync bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.syncIndexing = sync
	}
}
