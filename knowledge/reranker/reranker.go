//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package reranker provides result re-ranking for knowledge systems.
package reranker

import (
	"context"
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/query"
)

// Reranker re-ranks search results based on various criteria.
type Reranker interface {
	// Rerank re-orders search results based on ranking criteria.
	Rerank(ctx context.Context, query *Query, results []*Result) ([]*Result, error)
}

// ConversationMessage represents a message in a conversation history.
// It's an alias to the query package type for API compatibility.
type ConversationMessage = query.ConversationMessage

// Query represents a search query for re-ranking.
type Query struct {
	// Text is the query text for semantic search.
	Text string
	// FinalQuery is the final processed query after enhancements.
	FinalQuery string
	// History contains recent conversation messages for context.
	// Should be limited to last N messages for performance.
	History []ConversationMessage

	// UserID can help with personalized search results.
	UserID string

	// SessionID can help with session-specific context.
	SessionID string
}

// Result represents a rankable search result.
type Result struct {
	// Document is the search result document.
	Document *document.Document

	// Score is the relevance score.
	Score float64
}

// BuildRerankInputJSON builds a compact JSON string of reranker input for telemetry.
func BuildRerankInputJSON(q *Query, results []*Result) string {
	type inputDoc struct {
		ID   string `json:"id,omitempty"`
		Name string `json:"name,omitempty"`
		Text string `json:"text,omitempty"`
	}
	queryText := ""
	if q != nil {
		queryText = q.Text
	}
	input := struct {
		Query     string    `json:"query"`
		InputDocs []inputDoc `json:"input_docs"`
	}{
		Query:     queryText,
		InputDocs: make([]inputDoc, 0, len(results)),
	}
	for _, r := range results {
		doc := inputDoc{}
		if r.Document != nil {
			doc.ID = r.Document.ID
			doc.Name = r.Document.Name
			if len(r.Document.Content) > 200 {
				doc.Text = r.Document.Content[:200]
			} else {
				doc.Text = r.Document.Content
			}
		}
		input.InputDocs = append(input.InputDocs, doc)
	}
	b, _ := json.Marshal(input)
	return string(b)
}

// BuildRerankOutputJSON builds a compact JSON string of reranker output for telemetry.
func BuildRerankOutputJSON(results []*Result) string {
	type outputDoc struct {
		ID    string  `json:"id,omitempty"`
		Name  string  `json:"name,omitempty"`
		Score float64 `json:"score"`
	}
	docs := make([]outputDoc, 0, len(results))
	for _, r := range results {
		doc := outputDoc{Score: r.Score}
		if r.Document != nil {
			doc.ID = r.Document.ID
			doc.Name = r.Document.Name
		}
		docs = append(docs, doc)
	}
	b, _ := json.Marshal(docs)
	return string(b)
}
