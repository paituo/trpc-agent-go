//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// SearchEvents implements session.SearchableService.
// It returns the top-K events most semantically relevant
// to the given query text within the requested user scope.
// Requires an embedder to be configured via WithEmbedder.
func (s *Service) SearchEvents(
	ctx context.Context,
	req session.EventSearchRequest,
) ([]session.EventSearchResult, error) {
	if s.opts.embedder == nil {
		return nil, fmt.Errorf(
			"sqlite session: embedder not configured for search")
	}
	if err := req.UserKey.CheckUserKey(); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, nil
	}
	if req.SearchMode == "" {
		req.SearchMode = session.SearchModeDense
	}
	if req.SearchMode != session.SearchModeDense &&
		req.SearchMode != session.SearchModeHybrid {
		return nil, fmt.Errorf(
			"unsupported session search mode: %s", req.SearchMode)
	}
	topK := req.MaxResults
	if topK <= 0 {
		topK = s.opts.maxResults
	}

	searchCtx := ctx
	if s.opts.embedTimeout > 0 {
		var cancel context.CancelFunc
		searchCtx, cancel = context.WithTimeout(
			ctx, s.opts.embedTimeout,
		)
		defer cancel()
	}

	// Generate query embedding.
	qEmb, err := s.opts.embedder.GetEmbedding(searchCtx, query)
	if err != nil {
		return nil, fmt.Errorf(
			"generate query embedding: %w", err)
	}
	if len(qEmb) == 0 {
		return nil, fmt.Errorf(
			"empty embedding returned for query")
	}

	if req.SearchMode == session.SearchModeDense {
		return s.executeDenseSearch(
			searchCtx, req, qEmb, topK,
		)
	}

	// Hybrid mode.
	candidateLimit := resolveHybridCandidateLimit(
		topK,
		req.HybridCandidateRatio,
		s.opts.candidateRatio,
	)
	denseResults, err := s.executeDenseSearch(
		searchCtx, req, qEmb, candidateLimit,
	)
	if err != nil {
		return nil, err
	}
	keywordResults, err := s.executeKeywordSearch(
		searchCtx, req, query, candidateLimit,
	)
	if err != nil {
		log.WarnfContext(
			ctx,
			"sqlite session keyword search failed, fallback to dense only: %v",
			err,
		)
		return truncateEventSearchResults(denseResults, topK), nil
	}
	rrfK := req.HybridRRFK
	if rrfK <= 0 {
		rrfK = s.opts.hybridRRFK
	}
	if rrfK <= 0 {
		rrfK = defaultHybridRRFK
	}
	return mergeHybridEventResults(
		denseResults,
		keywordResults,
		rrfK,
		topK,
	), nil
}

func (s *Service) executeDenseSearch(
	ctx context.Context,
	req session.EventSearchRequest,
	embedding []float64,
	limit int,
) ([]session.EventSearchResult, error) {
	// Serialize query embedding to vec0 blob.
	f32 := make([]float32, len(embedding))
	for i, v := range embedding {
		f32[i] = float32(v)
	}
	blob, err := vecSerializeFloat32(f32)
	if err != nil {
		return nil, fmt.Errorf(
			"serialize query embedding: %w", err)
	}

	// Build vec0 MATCH query.
	var whereParts []string
	var params []any

	whereParts = append(whereParts,
		"v.embedding MATCH "+vecBlobPlaceholder)
	params = append(params, blob)

	whereParts = append(whereParts, "v.k = ?")
	params = append(params, limit)

	whereParts = append(whereParts,
		"v.app_name = ?", "v.user_id = ?")
	params = append(params, req.UserKey.AppName, req.UserKey.UserID)

	// Session filters.
	sessionIDs := compactStrings(req.SessionIDs)
	if len(sessionIDs) > 0 {
		placeholders := make([]string, len(sessionIDs))
		for i, id := range sessionIDs {
			placeholders[i] = "?"
			params = append(params, id)
		}
		whereParts = append(whereParts,
			fmt.Sprintf("v.session_id IN (%s)",
				strings.Join(placeholders, ",")))
	}
	excludeSessionIDs := compactStrings(req.ExcludeSessionIDs)
	if len(excludeSessionIDs) > 0 {
		placeholders := make([]string, len(excludeSessionIDs))
		for i, id := range excludeSessionIDs {
			placeholders[i] = "?"
			params = append(params, id)
		}
		whereParts = append(whereParts,
			fmt.Sprintf("v.session_id NOT IN (%s)",
				strings.Join(placeholders, ",")))
	}

	// Role filter.
	roles := compactRoles(req.Roles)
	if len(roles) > 0 {
		placeholders := make([]string, len(roles))
		for i, r := range roles {
			placeholders[i] = "?"
			params = append(params, r)
		}
		whereParts = append(whereParts,
			fmt.Sprintf("v.role IN (%s)",
				strings.Join(placeholders, ",")))
	}

	// Time range filters.
	if req.CreatedAfter != nil {
		whereParts = append(whereParts, "v.created_at >= ?")
		params = append(params,
			req.CreatedAfter.UTC().UnixNano())
	}
	if req.CreatedBefore != nil {
		whereParts = append(whereParts, "v.created_at <= ?")
		params = append(params,
			req.CreatedBefore.UTC().UnixNano())
	}

	selectSQL := fmt.Sprintf(`SELECT
  v.rowid, v.app_name, v.user_id, v.session_id,
  v.content_text, v.role, v.created_at, v.distance
FROM %s v
WHERE %s`, s.vecTableName(),
		strings.Join(whereParts, " AND "))

	rows, err := s.db.QueryContext(ctx, selectSQL, params...)
	if err != nil {
		return nil, fmt.Errorf(
			"vec0 search: %w", err)
	}
	defer rows.Close()

	type vecRow struct {
		rowID      int64
		appName    string
		userID     string
		sessionID  string
		contentTxt sql.NullString
		role       sql.NullString
		createdNs  int64
		distance   float64
	}
	var scanned []vecRow
	for rows.Next() {
		var r vecRow
		if err := rows.Scan(
			&r.rowID, &r.appName, &r.userID, &r.sessionID,
			&r.contentTxt, &r.role, &r.createdNs, &r.distance,
		); err != nil {
			return nil, fmt.Errorf(
				"scan vec0 row: %w", err)
		}
		scanned = append(scanned, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"iterate vec0 rows: %w", err)
	}
	_ = rows.Close()

	if len(scanned) == 0 {
		return nil, nil
	}

	// Fetch full event data from session_events.
	rowIDs := make([]int64, len(scanned))
	for i, r := range scanned {
		rowIDs[i] = r.rowID
	}
	events, err := s.fetchEventsByRowIDs(ctx, rowIDs)
	if err != nil {
		return nil, fmt.Errorf(
			"fetch events by row ids: %w", err)
	}

	// Build results.
	results := make([]session.EventSearchResult, 0, len(scanned))
	for _, r := range scanned {
		evt, ok := events[r.rowID]
		if !ok {
			continue
		}
		similarity := 1.0 - r.distance/2.0
		if req.MinScore > 0 && similarity < req.MinScore {
			continue
		}
		resultText := strings.TrimSpace(r.contentTxt.String)
		resultRole := model.Role(r.role.String)
		if resultText == "" || resultRole == "" {
			if fallbackText, fallbackRole := extractEventText(&evt); resultText == "" {
				resultText = fallbackText
				if resultRole == "" {
					resultRole = fallbackRole
				}
			} else if resultRole == "" {
				resultRole = fallbackRole
			}
		}
		results = append(results, session.EventSearchResult{
			SessionKey: session.Key{
				AppName:   r.appName,
				UserID:    r.userID,
				SessionID: r.sessionID,
			},
			SessionCreatedAt: time.Unix(0, r.createdNs).UTC(),
			EventCreatedAt:   evt.Timestamp,
			Event:            evt,
			Role:             resultRole,
			Text:             resultText,
			Score:            similarity,
			DenseScore:       similarity,
		})
	}

	return results, nil
}

func (s *Service) executeKeywordSearch(
	ctx context.Context,
	req session.EventSearchRequest,
	query string,
	limit int,
) ([]session.EventSearchResult, error) {
	if !s.opts.enableFTS {
		return nil, fmt.Errorf(
			"keyword search requires enableFTS=true")
	}

	// Build FTS5 MATCH query.
	var params []any

	// Escape query for FTS5.
	ftsQuery := escapeFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}

	// We need to join FTS5 with vec0 to get app_name/user_id/session_id.
	// FTS5 stores rowid which matches vec0 rowid.
	selectSQL := fmt.Sprintf(`SELECT
  v.rowid, v.app_name, v.user_id, v.session_id,
  v.content_text, v.role, v.created_at
FROM %s f
JOIN %s v ON v.rowid = f.rowid
WHERE f.content_text MATCH ?
ORDER BY bm25(%s)`,
		s.ftsTableName(), s.vecTableName(), s.ftsTableName())
	params = append(params, ftsQuery)

	// Add user scope filter.
	selectSQL += ` AND v.app_name = ? AND v.user_id = ?`
	params = append(params, req.UserKey.AppName, req.UserKey.UserID)

	// Session filters.
	sessionIDs := compactStrings(req.SessionIDs)
	if len(sessionIDs) > 0 {
		placeholders := make([]string, len(sessionIDs))
		for i, id := range sessionIDs {
			placeholders[i] = "?"
			params = append(params, id)
		}
		selectSQL += fmt.Sprintf(
			" AND v.session_id IN (%s)",
			strings.Join(placeholders, ","))
	}
	excludeSessionIDs := compactStrings(req.ExcludeSessionIDs)
	if len(excludeSessionIDs) > 0 {
		placeholders := make([]string, len(excludeSessionIDs))
		for i, id := range excludeSessionIDs {
			placeholders[i] = "?"
			params = append(params, id)
		}
		selectSQL += fmt.Sprintf(
			" AND v.session_id NOT IN (%s)",
			strings.Join(placeholders, ","))
	}

	// Role filter.
	roles := compactRoles(req.Roles)
	if len(roles) > 0 {
		placeholders := make([]string, len(roles))
		for i, r := range roles {
			placeholders[i] = "?"
			params = append(params, r)
		}
		selectSQL += fmt.Sprintf(
			" AND v.role IN (%s)",
			strings.Join(placeholders, ","))
	}

	// Time range filters.
	if req.CreatedAfter != nil {
		selectSQL += " AND v.created_at >= ?"
		params = append(params,
			req.CreatedAfter.UTC().UnixNano())
	}
	if req.CreatedBefore != nil {
		selectSQL += " AND v.created_at <= ?"
		params = append(params,
			req.CreatedBefore.UTC().UnixNano())
	}

	selectSQL += fmt.Sprintf(" LIMIT %d", limit)

	rows, err := s.db.QueryContext(ctx, selectSQL, params...)
	if err != nil {
		return nil, fmt.Errorf(
			"fts5 search: %w", err)
	}
	defer rows.Close()

	type ftsRow struct {
		rowID      int64
		appName    string
		userID     string
		sessionID  string
		contentTxt sql.NullString
		role       sql.NullString
		createdNs  int64
	}
	var scanned []ftsRow
	for rows.Next() {
		var r ftsRow
		if err := rows.Scan(
			&r.rowID, &r.appName, &r.userID, &r.sessionID,
			&r.contentTxt, &r.role, &r.createdNs,
		); err != nil {
			return nil, fmt.Errorf(
				"scan fts5 row: %w", err)
		}
		scanned = append(scanned, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"iterate fts5 rows: %w", err)
	}
	_ = rows.Close()

	if len(scanned) == 0 {
		return nil, nil
	}

	// Fetch full event data.
	rowIDs := make([]int64, len(scanned))
	for i, r := range scanned {
		rowIDs[i] = r.rowID
	}
	events, err := s.fetchEventsByRowIDs(ctx, rowIDs)
	if err != nil {
		return nil, fmt.Errorf(
			"fetch events by row ids: %w", err)
	}

	results := make([]session.EventSearchResult, 0, len(scanned))
	for _, r := range scanned {
		evt, ok := events[r.rowID]
		if !ok {
			continue
		}
		resultText := strings.TrimSpace(r.contentTxt.String)
		resultRole := model.Role(r.role.String)
		if resultText == "" || resultRole == "" {
			if fallbackText, fallbackRole := extractEventText(&evt); resultText == "" {
				resultText = fallbackText
				if resultRole == "" {
					resultRole = fallbackRole
				}
			} else if resultRole == "" {
				resultRole = fallbackRole
			}
		}
		// FTS5 BM25 rank is negative; convert to a positive score.
		// We assign a default score since BM25 rank is not directly
		// available in this query path.
		results = append(results, session.EventSearchResult{
			SessionKey: session.Key{
				AppName:   r.appName,
				UserID:    r.userID,
				SessionID: r.sessionID,
			},
			SessionCreatedAt: time.Unix(0, r.createdNs).UTC(),
			EventCreatedAt:   evt.Timestamp,
			Event:            evt,
			Role:             resultRole,
			Text:             resultText,
			SparseScore:      1.0,
		})
	}

	return results, nil
}

// fetchEventsByRowIDs retrieves full event data from session_events by row IDs.
func (s *Service) fetchEventsByRowIDs(
	ctx context.Context,
	rowIDs []int64,
) (map[int64]event.Event, error) {
	if len(rowIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(rowIDs))
	params := make([]any, len(rowIDs))
	for i, id := range rowIDs {
		placeholders[i] = "?"
		params[i] = id
	}
	query := fmt.Sprintf(`SELECT id, event, created_at FROM %s
WHERE id IN (%s) AND deleted_at IS NULL`,
		s.tableSessionEvents,
		strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, fmt.Errorf(
			"query events by ids: %w", err)
	}
	defer rows.Close()

	result := make(map[int64]event.Event, len(rowIDs))
	for rows.Next() {
		var (
			id         int64
			eventBytes []byte
			createdNs  int64
		)
		if err := rows.Scan(&id, &eventBytes, &createdNs); err != nil {
			return nil, fmt.Errorf(
				"scan event row: %w", err)
		}
		var evt event.Event
		if err := json.Unmarshal(eventBytes, &evt); err != nil {
			return nil, fmt.Errorf(
				"unmarshal event: %w", err)
		}
		evt.Timestamp = time.Unix(0, createdNs).UTC()
		result[id] = evt
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"iterate event rows: %w", err)
	}
	return result, nil
}

// escapeFTSQuery escapes a user query for safe FTS5 MATCH usage.
func escapeFTSQuery(query string) string {
	// Split into words and wrap each in quotes to prevent
	// FTS5 syntax injection.
	words := strings.Fields(query)
	if len(words) == 0 {
		return ""
	}
	escaped := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.ReplaceAll(w, `"`, `""`)
		escaped = append(escaped, `"`+w+`"`)
	}
	return strings.Join(escaped, " ")
}

// ---------- Shared helpers (adapted from pgvector search) ----------

func resolveHybridCandidateLimit(
	topK int,
	reqRatio int,
	defaultRatio int,
) int {
	ratio := reqRatio
	if ratio <= 0 {
		ratio = defaultRatio
	}
	if ratio <= 1 {
		return topK
	}
	return topK * ratio
}

func truncateEventSearchResults(
	results []session.EventSearchResult,
	limit int,
) []session.EventSearchResult {
	if limit <= 0 || len(results) <= limit {
		return results
	}
	return results[:limit]
}

func mergeHybridEventResults(
	denseResults []session.EventSearchResult,
	keywordResults []session.EventSearchResult,
	k int,
	maxResults int,
) []session.EventSearchResult {
	if k <= 0 {
		k = defaultHybridRRFK
	}

	type hybridEntry struct {
		result session.EventSearchResult
		score  float64
	}

	merged := make(
		map[string]*hybridEntry,
		len(denseResults)+len(keywordResults),
	)
	addResult := func(
		results []session.EventSearchResult,
		dense bool,
	) {
		for rank, result := range results {
			id := eventSearchResultID(result)
			rrfScore := 1.0 / float64(k+rank+1)
			if existing, ok := merged[id]; ok {
				existing.score += rrfScore
				if dense && existing.result.DenseScore == 0 {
					existing.result.DenseScore = result.DenseScore
				}
				if !dense && existing.result.SparseScore == 0 {
					existing.result.SparseScore = result.SparseScore
				}
				if strings.TrimSpace(existing.result.Text) == "" {
					existing.result.Text = result.Text
				}
				if existing.result.Role == "" {
					existing.result.Role = result.Role
				}
				continue
			}
			merged[id] = &hybridEntry{
				result: result,
				score:  rrfScore,
			}
		}
	}

	addResult(denseResults, true)
	addResult(keywordResults, false)

	fused := make([]*hybridEntry, 0, len(merged))
	for _, entry := range merged {
		entry.result.Score = entry.score
		fused = append(fused, entry)
	}
	sort.Slice(fused, func(i, j int) bool {
		if fused[i].score == fused[j].score {
			return fused[i].result.EventCreatedAt.After(
				fused[j].result.EventCreatedAt,
			)
		}
		return fused[i].score > fused[j].score
	})

	results := make(
		[]session.EventSearchResult,
		0,
		min(len(fused), maxResults),
	)
	for i, entry := range fused {
		if maxResults > 0 && i >= maxResults {
			break
		}
		results = append(results, entry.result)
	}
	return results
}

func eventSearchResultID(
	result session.EventSearchResult,
) string {
	keyParts := []string{
		result.SessionKey.AppName,
		result.SessionKey.UserID,
		result.SessionKey.SessionID,
	}
	if id := strings.TrimSpace(result.Event.ID); id != "" {
		return strings.Join(append(keyParts, id), "|")
	}
	if eventBytes, err := json.Marshal(result.Event); err == nil {
		return strings.Join(
			append(keyParts, string(eventBytes)),
			"|",
		)
	}
	return strings.Join(
		append(keyParts,
			result.EventCreatedAt.UTC().Format(time.RFC3339Nano),
			strings.TrimSpace(result.Role.String()),
			strings.TrimSpace(result.Text),
		),
		"|",
	)
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func compactRoles(roles []model.Role) []string {
	if len(roles) == 0 {
		return nil
	}
	out := make([]string, 0, len(roles))
	seen := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		value := strings.TrimSpace(role.String())
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
