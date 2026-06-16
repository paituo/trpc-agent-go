//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package retriever

import (
	"context"
	"encoding/json"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/internal/trace"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/query"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/log"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

// DefaultRetriever implements the complete RAG pipeline.
type DefaultRetriever struct {
	embedder      embedder.Embedder
	vectorStore   vectorstore.VectorStore
	queryEnhancer query.Enhancer
	reranker      reranker.Reranker
}

// Option represents a functional option for configuring DefaultRetriever.
type Option func(*DefaultRetriever)

// WithEmbedder sets the embedder for the retriever.
func WithEmbedder(e embedder.Embedder) Option {
	return func(dr *DefaultRetriever) {
		dr.embedder = e
	}
}

// WithVectorStore sets the vector store for the retriever.
func WithVectorStore(vs vectorstore.VectorStore) Option {
	return func(dr *DefaultRetriever) {
		dr.vectorStore = vs
	}
}

// WithQueryEnhancer sets the query enhancer for the retriever.
func WithQueryEnhancer(qe query.Enhancer) Option {
	return func(dr *DefaultRetriever) {
		dr.queryEnhancer = qe
	}
}

// WithReranker sets the reranker for the retriever.
func WithReranker(r reranker.Reranker) Option {
	return func(dr *DefaultRetriever) {
		dr.reranker = r
	}
}

// New creates a new default retriever with the given options.
func New(opts ...Option) *DefaultRetriever {
	dr := &DefaultRetriever{}

	for _, opt := range opts {
		opt(dr)
	}

	return dr
}

// Retrieve implements the Retriever interface by executing the complete RAG pipeline.
func (dr *DefaultRetriever) Retrieve(ctx context.Context, q *Query) (result *Result, err error) {
	ctx, span, started := trace.StartSpan(ctx, nil, telemetry.NewKnowledgeRetrieveSpanName())
	if started {
		defer span.End()
		span.SetAttributes(
			attribute.String(semconvtrace.KeyGenAIOperationName, telemetry.OperationKnowledgeRetrieve),
			attribute.String(semconvtrace.KeyKnowledgeRetrieveInput, q.Text),
			attribute.Int("knowledge.retrieve.limit", q.Limit),
			attribute.Float64("knowledge.retrieve.min_score", q.MinScore),
		)
	}

	// Step 1: Enhance query (if enhancer is available).
	finalQuery := q.Text
	if dr.queryEnhancer != nil && shouldEnhanceQuery(q) {
		// Create query request with full context.
		queryReq := &query.Request{
			Query:     q.Text,
			History:   q.History,
			UserID:    q.UserID,
			SessionID: q.SessionID,
		}
		enhanced, err := dr.queryEnhancer.EnhanceQuery(ctx, queryReq)
		if err != nil {
			if started {
				span.SetStatus(codes.Error, err.Error())
				span.RecordError(err)
			}
			return nil, err
		}
		finalQuery = enhanced.Enhanced
		if finalQuery != q.Text {
			log.DebugfContext(ctx, "query enhanced: %q -> %q", q.Text, finalQuery)
		}
	}

	// Step 2: Generate embedding.
	var embedding []float64
	if dr.embedder != nil && finalQuery != "" {
		var err error
		embedding, err = dr.embedder.GetEmbedding(ctx, finalQuery)
		if err != nil {
			if started {
				span.SetStatus(codes.Error, err.Error())
				span.RecordError(err)
			}
			return nil, err
		}
	}

	// Step 3: Search vector store.
	searchResults, err := dr.vectorStore.Search(ctx, &vectorstore.SearchQuery{
		Query:      finalQuery,
		Vector:     embedding,
		Limit:      q.Limit,
		MinScore:   q.MinScore,
		Filter:     convertQueryFilter(q.Filter),
		SearchMode: q.SearchMode,
	})
	if err != nil {
		if started {
			span.SetStatus(codes.Error, err.Error())
			span.RecordError(err)
		}
		return nil, err
	}

	// Step 4: Convert to reranker format.
	rerankerResults := make([]*reranker.Result, len(searchResults.Results))
	for i, doc := range searchResults.Results {
		rerankerResults[i] = &reranker.Result{
			Document: doc.Document,
			Score:    doc.Score,
		}
	}

	// Step 5: Rerank results (if reranker is available).
	if dr.reranker != nil {
		rerankerResults, err = dr.reranker.Rerank(ctx, &reranker.Query{
			Text:       q.Text,
			FinalQuery: finalQuery,
			History:    q.History,
			UserID:     q.UserID,
			SessionID:  q.SessionID,
		}, rerankerResults)
		if err != nil {
			if started {
				span.SetStatus(codes.Error, err.Error())
				span.RecordError(err)
			}
			return nil, err
		}
	}

	// Step 6: Convert back to retriever format.
	finalResults := make([]*RelevantDocument, len(rerankerResults))
	for i, result := range rerankerResults {
		finalResults[i] = &RelevantDocument{
			Document: result.Document,
			Score:    result.Score,
		}
	}

	result = &Result{
		Documents: finalResults,
	}
	if started {
		outputJSON := buildRetrieveOutputJSON(finalResults)
		span.SetAttributes(
			attribute.Int("knowledge.retrieve.result_count", len(finalResults)),
			attribute.String(semconvtrace.KeyKnowledgeRetrieveOutput, outputJSON),
		)
	}
	return result, nil
}

// buildRetrieveOutputJSON builds a compact JSON string of retrieved documents for telemetry.
func buildRetrieveOutputJSON(results []*RelevantDocument) string {
	type resultSummary struct {
		ID    string  `json:"id"`
		Name  string  `json:"name"`
		Score float64 `json:"score"`
	}
	summaries := make([]resultSummary, 0, len(results))
	for _, r := range results {
		if r.Document != nil {
			summaries = append(summaries, resultSummary{
				ID:    r.Document.ID,
				Name:  r.Document.Name,
				Score: r.Score,
			})
		}
	}
	b, _ := json.Marshal(summaries)
	return string(b)
}

// Close implements the Retriever interface.
func (dr *DefaultRetriever) Close() error {
	// Close components if they support closing.
	return nil
}

func shouldEnhanceQuery(q *Query) bool {
	return !(q.Text == "" && q.SearchMode == vectorstore.SearchModeFilter)
}

// convertQueryFilter converts retriever.QueryFilter to vectorstore.SearchFilter.
func convertQueryFilter(qf *QueryFilter) *vectorstore.SearchFilter {
	if qf == nil {
		return nil
	}

	return &vectorstore.SearchFilter{
		IDs:             qf.DocumentIDs,
		Metadata:        qf.Metadata,
		FilterCondition: qf.FilterCondition,
	}
}
