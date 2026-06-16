//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sqlitevec

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// ---------- 分词辅助方法 ----------

// segmentText segments text using gse for indexing.
// Returns space-separated tokens suitable for FTS5 storage.
func (s *Store) segmentText(text string) string {
	if s.segmenter == nil || text == "" {
		return ""
	}
	tokens := s.segmenter.Cut(text, true)
	return strings.Join(tokens, " ")
}

// segmentQuery segments a search query using gse search mode.
// CutSearch extracts sub-grams from long words for better recall.
func (s *Store) segmentQuery(text string) string {
	if s.segmenter == nil || text == "" {
		return ""
	}
	tokens := s.segmenter.CutSearch(text, true)
	return strings.Join(tokens, " ")
}

// buildFTSQuery converts segmented tokens into a safe FTS5 MATCH expression.
// Each token is wrapped in double quotes (phrase query) to prevent FTS5
// syntax injection from tokens containing special characters like *, +, -.
func (s *Store) buildFTSQuery(tokens []string) string {
	escaped := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		// Escape double quotes within the token.
		token = strings.ReplaceAll(token, `"`, `""`)
		escaped = append(escaped, `"`+token+`"`)
	}
	return strings.Join(escaped, " ")
}

// ---------- 关键词搜索 ----------

// searchByKeyword performs full-text search using FTS5 + bm25 ranking.
func (s *Store) searchByKeyword(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if query.Query == "" {
		return nil, errors.New("sqlitevec: query text is required for keyword search")
	}

	tokens := s.segmenter.CutSearch(query.Query, true)
	ftsQuery := s.buildFTSQuery(tokens)
	if ftsQuery == "" {
		return &vectorstore.SearchResult{Results: nil}, nil
	}

	limit := query.Limit
	if limit <= 0 {
		limit = s.opts.maxResults
	}

	searchSQL := fmt.Sprintf(`
		SELECT doc_id, rank
		FROM %s
		WHERE %s MATCH ?
		ORDER BY bm25(%s)
		LIMIT ?`,
		s.opts.ftsTableName, s.opts.ftsTableName, s.opts.ftsTableName)

	rows, err := s.db.QueryContext(ctx, searchSQL, ftsQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlitevec keyword search: %w", err)
	}
	defer rows.Close()

	type ftsRow struct {
		docID string
		rank  float64
	}
	var ftsRows []ftsRow
	for rows.Next() {
		var r ftsRow
		if err := rows.Scan(&r.docID, &r.rank); err != nil {
			return nil, fmt.Errorf("sqlitevec keyword search scan: %w", err)
		}
		ftsRows = append(ftsRows, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitevec keyword search iterate: %w", err)
	}

	if len(ftsRows) == 0 {
		return &vectorstore.SearchResult{Results: nil}, nil
	}

	// Fetch full documents by IDs and compute text scores.
	docIDs := make([]string, len(ftsRows))
	for i, r := range ftsRows {
		docIDs[i] = r.docID
	}

	placeholders := make([]string, len(docIDs))
	params := make([]any, len(docIDs))
	for i, id := range docIDs {
		placeholders[i] = "?"
		params[i] = id
	}

	fetchSQL := fmt.Sprintf(`SELECT
		id, name, content, metadata, created_at, updated_at
		FROM %s WHERE id IN (%s)`,
		s.opts.tableName, strings.Join(placeholders, ","))

	docRows, err := s.db.QueryContext(ctx, fetchSQL, params...)
	if err != nil {
		return nil, fmt.Errorf("sqlitevec keyword search fetch: %w", err)
	}
	defer docRows.Close()

	type docRow struct {
		id           string
		name         sql.NullString
		content      sql.NullString
		metadataJSON sql.NullString
		createdAtNs  int64
		updatedAtNs  int64
	}
	var scanned []docRow
	for docRows.Next() {
		var r docRow
		if err := docRows.Scan(&r.id, &r.name, &r.content, &r.metadataJSON, &r.createdAtNs, &r.updatedAtNs); err != nil {
			return nil, fmt.Errorf("sqlitevec keyword search fetch scan: %w", err)
		}
		scanned = append(scanned, r)
	}
	if err := docRows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitevec keyword search fetch iterate: %w", err)
	}
	_ = docRows.Close()

	// Build a map from docID to scored document.
	docMap := make(map[string]*vectorstore.ScoredDocument, len(scanned))
	for _, r := range scanned {
		sd, err := s.buildScoredDocument(ctx, r.id, r.name, r.content, r.metadataJSON, r.createdAtNs, r.updatedAtNs, 0)
		if err != nil {
			return nil, err
		}
		docMap[r.id] = sd
	}

	// Assemble results in FTS rank order with normalized text scores.
	var results []*vectorstore.ScoredDocument
	for _, r := range ftsRows {
		sd, ok := docMap[r.docID]
		if !ok {
			continue
		}
		// bm25 returns negative values; more relevant = closer to 0.
		absRank := -r.rank
		textScore := absRank / (absRank + s.opts.sparseNormConstant)
		sd.Score = textScore
		if sd.Document.Metadata == nil {
			sd.Document.Metadata = make(map[string]any)
		}
		sd.Document.Metadata[source.MetadataDenseScore] = 0.0
		sd.Document.Metadata[source.MetadataSparseScore] = textScore
		if textScore >= query.MinScore {
			results = append(results, sd)
		}
	}

	return &vectorstore.SearchResult{Results: results}, nil
}

// ---------- 混合搜索 (RRF) ----------

// rrfRankedID holds a document ID and its rank from a sub-search.
type rrfRankedID struct {
	id   string
	rank int
}

// searchByHybrid performs hybrid search using Reciprocal Rank Fusion.
// It runs vector and text sub-searches, fuses ranks in Go, then fetches
// full documents for the top-N fused results.
func (s *Store) searchByHybrid(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	if len(query.Vector) == 0 {
		return nil, errors.New("sqlitevec: vector is required for hybrid search")
	}

	limit := query.Limit
	if limit <= 0 {
		limit = s.opts.maxResults
	}
	candidateLimit := limit * s.opts.rrfCandidateRatio

	// --- Vector sub-search ---
	vectorRanks, err := s.vectorRankQuery(ctx, query, candidateLimit)
	if err != nil {
		return nil, fmt.Errorf("sqlitevec hybrid vector search: %w", err)
	}

	// --- Text sub-search (if query text is provided) ---
	var textRanks []rrfRankedID
	if query.Query != "" {
		textRanks, err = s.textRankQuery(ctx, query, candidateLimit)
		if err != nil {
			return nil, fmt.Errorf("sqlitevec hybrid text search: %w", err)
		}
	}

	// --- RRF Fusion ---
	type rrfScore struct {
		vectorScore float64
		textScore   float64
	}
	scoreMap := make(map[string]*rrfScore)

	for _, r := range vectorRanks {
		s2, ok := scoreMap[r.id]
		if !ok {
			s2 = &rrfScore{}
			scoreMap[r.id] = s2
		}
		s2.vectorScore = 1.0 / float64(s.opts.rrfK+r.rank)
	}
	for _, r := range textRanks {
		s2, ok := scoreMap[r.id]
		if !ok {
			s2 = &rrfScore{}
			scoreMap[r.id] = s2
		}
		s2.textScore = 1.0 / float64(s.opts.rrfK+r.rank)
	}

	// Sort by combined RRF score descending.
	type idScore struct {
		id          string
		vectorScore float64
		textScore   float64
		score       float64
	}
	ranked := make([]idScore, 0, len(scoreMap))
	for id, s2 := range scoreMap {
		ranked = append(ranked, idScore{
			id:          id,
			vectorScore: s2.vectorScore,
			textScore:   s2.textScore,
			score:       s2.vectorScore + s2.textScore,
		})
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].score > ranked[j].score
	})

	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	if len(ranked) == 0 {
		return &vectorstore.SearchResult{
			Results: make([]*vectorstore.ScoredDocument, 0),
		}, nil
	}

	// --- Fetch full documents by IDs ---
	fetchIDs := make([]any, len(ranked))
	for i, r := range ranked {
		fetchIDs[i] = r.id
	}

	placeholders := make([]string, len(fetchIDs))
	for i := range fetchIDs {
		placeholders[i] = "?"
	}
	fetchSQL := fmt.Sprintf(`SELECT
		id, name, content, metadata, created_at, updated_at
		FROM %s WHERE id IN (%s)`,
		s.opts.tableName, strings.Join(placeholders, ","))

	docMap := make(map[string]*vectorstore.ScoredDocument)
	docRows, err := s.db.QueryContext(ctx, fetchSQL, fetchIDs...)
	if err != nil {
		return nil, fmt.Errorf("sqlitevec hybrid fetch documents: %w", err)
	}
	defer docRows.Close()

	type docRow struct {
		id           string
		name         sql.NullString
		content      sql.NullString
		metadataJSON sql.NullString
		createdAtNs  int64
		updatedAtNs  int64
	}
	var docScanned []docRow
	for docRows.Next() {
		var r docRow
		if err := docRows.Scan(&r.id, &r.name, &r.content, &r.metadataJSON, &r.createdAtNs, &r.updatedAtNs); err != nil {
			return nil, fmt.Errorf("sqlitevec hybrid fetch scan: %w", err)
		}
		docScanned = append(docScanned, r)
	}
	if err := docRows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitevec hybrid fetch iterate: %w", err)
	}
	_ = docRows.Close()

	for _, r := range docScanned {
		sd, err := s.buildScoredDocument(ctx, r.id, r.name, r.content, r.metadataJSON, r.createdAtNs, r.updatedAtNs, 0)
		if err != nil {
			return nil, err
		}
		docMap[r.id] = sd
	}

	// Assemble final results in ranked order.
	results := make([]*vectorstore.ScoredDocument, 0, len(ranked))
	for _, r := range ranked {
		sd, ok := docMap[r.id]
		if !ok {
			continue
		}
		sd.Score = r.score
		if sd.Document.Metadata == nil {
			sd.Document.Metadata = make(map[string]any)
		}
		sd.Document.Metadata[source.MetadataDenseScore] = r.vectorScore
		sd.Document.Metadata[source.MetadataSparseScore] = r.textScore
		results = append(results, sd)
	}

	return &vectorstore.SearchResult{Results: results}, nil
}

// vectorRankQuery returns (docID, rank) pairs from vector similarity search.
func (s *Store) vectorRankQuery(ctx context.Context, query *vectorstore.SearchQuery, limit int) ([]rrfRankedID, error) {
	blob, err := s.serializeEmbedding(query.Vector)
	if err != nil {
		return nil, fmt.Errorf("serialize embedding: %w", err)
	}

	var whereParts []string
	var params []any

	whereParts = append(whereParts, "v.embedding MATCH "+sqlVectorFromBlob)
	params = append(params, blob)
	whereParts = append(whereParts, "v.k = ?")
	params = append(params, limit)

	if query.Filter != nil {
		filterSQL, filterParams, err := s.filterB.buildFilterClauses(
			query.Filter.IDs,
			query.Filter.Metadata,
			query.Filter.FilterCondition,
		)
		if err != nil {
			return nil, fmt.Errorf("build filter: %w", err)
		}
		if filterSQL != "" {
			whereParts = append(whereParts, filterSQL)
			params = append(params, filterParams...)
		}
	}

	rankSQL := fmt.Sprintf(`SELECT v.id, ROW_NUMBER() OVER (ORDER BY v.distance) as rank
		FROM %s v
		WHERE %s`,
		s.opts.tableName, strings.Join(whereParts, " AND "))

	rows, err := s.db.QueryContext(ctx, rankSQL, params...)
	if err != nil {
		return nil, fmt.Errorf("vector rank query: %w", err)
	}
	defer rows.Close()

	var ranks []rrfRankedID
	for rows.Next() {
		var r rrfRankedID
		if err := rows.Scan(&r.id, &r.rank); err != nil {
			return nil, fmt.Errorf("vector rank scan: %w", err)
		}
		ranks = append(ranks, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vector rank iterate: %w", err)
	}

	return ranks, nil
}

// textRankQuery returns (docID, rank) pairs from FTS5 full-text search.
func (s *Store) textRankQuery(ctx context.Context, query *vectorstore.SearchQuery, limit int) ([]rrfRankedID, error) {
	tokens := s.segmenter.CutSearch(query.Query, true)
	ftsQuery := s.buildFTSQuery(tokens)
	if ftsQuery == "" {
		return nil, nil
	}

	rankSQL := fmt.Sprintf(`SELECT doc_id, ROW_NUMBER() OVER (ORDER BY bm25(%s)) as rank
		FROM %s
		WHERE %s MATCH ?
		LIMIT ?`,
		s.opts.ftsTableName, s.opts.ftsTableName, s.opts.ftsTableName)

	rows, err := s.db.QueryContext(ctx, rankSQL, ftsQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("text rank query: %w", err)
	}
	defer rows.Close()

	var ranks []rrfRankedID
	for rows.Next() {
		var r rrfRankedID
		if err := rows.Scan(&r.id, &r.rank); err != nil {
			return nil, fmt.Errorf("text rank scan: %w", err)
		}
		ranks = append(ranks, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("text rank iterate: %w", err)
	}

	return ranks, nil
}
