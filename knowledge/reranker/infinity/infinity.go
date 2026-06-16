//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package infinity provides a Reranker implementation compatible with Infinity.
package infinity

import (
	"context"
	"errors"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/internal/trace"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/internal/httpclient"
	"trpc.group/trpc-go/trpc-agent-go/log"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

var (
	// errEndpointEmpty is returned when the endpoint is empty.
	errEndpointEmpty = errors.New("infinity endpoint cannot be empty")
)

// Reranker implements Reranker using a self-hosted Infinity/TEI instance.
type Reranker struct {
	endpoint   string
	apiKey     string
	modelName  string
	topN       int
	httpClient *httpclient.Client
}

// Option configures Reranker.
type Option func(*Reranker)

// WithAPIKey sets the API key (optional for self-hosted).
func WithAPIKey(key string) Option {
	return func(r *Reranker) {
		r.apiKey = key
	}
}

// WithModel sets the model name (optional, depends on server config).
func WithModel(model string) Option {
	return func(r *Reranker) {
		r.modelName = model
	}
}

// WithTopN sets the TopN.
func WithTopN(n int) Option {
	return func(r *Reranker) {
		r.topN = n
	}
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(r *Reranker) {
		r.httpClient = httpclient.NewClient(client)
	}
}

// WithEndpoint sets the endpoint URL.
func WithEndpoint(endpoint string) Option {
	return func(r *Reranker) {
		r.endpoint = endpoint
	}
}

// New creates a new Infinity reranker.
func New(opts ...Option) (*Reranker, error) {
	r := &Reranker{
		topN:       -1,
		httpClient: httpclient.NewClient(nil),
	}
	for _, opt := range opts {
		opt(r)
	}
	if r.endpoint == "" {
		return nil, errEndpointEmpty
	}
	return r, nil
}

// Rerank implements the Reranker interface.
func (r *Reranker) Rerank(
	ctx context.Context,
	query *reranker.Query,
	results []*reranker.Result,
) ([]*reranker.Result, error) {
	ctx, span, started := trace.StartSpan(ctx, nil, telemetry.NewRerankSpanName(r.modelName))
	if started {
		defer span.End()
		span.SetAttributes(
			attribute.String(semconvtrace.KeyGenAIOperationName, telemetry.OperationRerank),
			attribute.String(semconvtrace.KeyRerankInput, reranker.BuildRerankInputJSON(query, results)),
			attribute.Int("reranker.input_count", len(results)),
			attribute.Int("reranker.top_n", r.topN),
		)
	}

	if len(results) == 0 {
		if started {
			span.SetAttributes(
				attribute.String(semconvtrace.KeyRerankOutput, "[]"),
			)
		}
		return results, nil
	}

	docs := make([]string, len(results))
	for i, res := range results {
		if res.Document != nil {
			docs[i] = res.Document.Content
		} else {
			log.WarnfContext(ctx, "infinity reranker: result[%d].Document is nil", i)
		}
	}

	req := httpclient.RerankRequest{
		Model:     r.modelName,
		Query:     query.FinalQuery,
		Documents: docs,
		TopN:      r.topN,
	}

	reranked, err := r.httpClient.Rerank(ctx, r.endpoint, r.apiKey, req, results)
	if err != nil {
		if started {
			span.SetStatus(codes.Error, err.Error())
			span.RecordError(err)
		}
		return nil, err
	}

	if r.topN > 0 && len(reranked) > r.topN {
		reranked = reranked[:r.topN]
	}
	if started {
		span.SetAttributes(
			attribute.Int("reranker.output_count", len(reranked)),
			attribute.String(semconvtrace.KeyRerankOutput, reranker.BuildRerankOutputJSON(reranked)),
		)
	}
	return reranked, nil
}
