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
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/hook"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// AppendEvent appends an event to a session.
func (s *Service) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	e *event.Event,
	opts ...session.Option,
) error {
	if sess == nil {
		return session.ErrNilSession
	}
	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	hctx := &session.AppendEventContext{
		Context: ctx,
		Session: sess,
		Event:   e,
		Key:     key,
	}
	final := func(c *session.AppendEventContext, next func() error) error {
		return s.appendEventInternal(
			c.Context,
			c.Session,
			c.Event,
			c.Key,
			opts...,
		)
	}
	return hook.RunAppendEventHooks(s.opts.appendEventHooks, hctx, final)
}

func (s *Service) appendEventInternal(
	ctx context.Context,
	sess *session.Session,
	e *event.Event,
	key session.Key,
	opts ...session.Option,
) error {
	sess.UpdateUserSession(e, opts...)

	if s.opts.enableAsyncPersist {
		return s.enqueueEventPersist(ctx, sess, key, e)
	}

	if err := s.addEvent(ctx, key, e); err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	s.indexEventAfterPersist(ctx, sess, e)
	return nil
}

func (s *Service) enqueueEventPersist(
	ctx context.Context,
	sess *session.Session,
	key session.Key,
	e *event.Event,
) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok &&
				e.Error() == "send on closed channel" {
				log.ErrorfContext(
					ctx,
					"async persist event: %v",
					r,
				)
				err = nil
				return
			}
			panic(r)
		}
	}()

	index := sess.Hash % len(s.eventPairChans)
	select {
	case s.eventPairChans[index] <- &sessionEventPair{key: key, event: e}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// AppendTrackEvent appends a track event to a session.
func (s *Service) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	trackEvent *session.TrackEvent,
	opts ...session.Option,
) error {
	if sess == nil {
		return session.ErrNilSession
	}
	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	if err := sess.AppendTrackEvent(trackEvent, opts...); err != nil {
		return fmt.Errorf("append track event: %w", err)
	}

	if s.opts.enableAsyncPersist {
		return s.enqueueTrackPersist(ctx, sess, key, trackEvent)
	}

	if err := s.addTrackEvent(ctx, key, trackEvent); err != nil {
		return fmt.Errorf("append track event: %w", err)
	}
	return nil
}

// GetTrackEvents returns persisted track events for the given session track.
func (s *Service) GetTrackEvents(
	ctx context.Context,
	key session.Key,
	track session.Track,
	opts ...session.Option,
) (*session.TrackEvents, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	opt := applyOptions(opts...)
	trackEvents, err := s.getTrackEventsByTrackLists(
		ctx,
		[]session.Key{key},
		[][]session.Track{{track}},
		opt.EventNum,
		opt.EventTime,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite session service get track events failed: %w", err)
	}
	return &session.TrackEvents{Track: track, Events: trackEvents[0][track]}, nil
}

func (s *Service) enqueueTrackPersist(
	ctx context.Context,
	sess *session.Session,
	key session.Key,
	e *session.TrackEvent,
) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok &&
				e.Error() == "send on closed channel" {
				log.ErrorfContext(
					ctx,
					"async persist track event: %v",
					r,
				)
				err = nil
				return
			}
			panic(r)
		}
	}()

	index := sess.Hash % len(s.trackEventChans)
	select {
	case s.trackEventChans[index] <- &trackEventPair{key: key, event: e}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) startAsyncPersistWorker() {
	persisterNum := s.opts.asyncPersisterNum
	s.eventPairChans = make([]chan *sessionEventPair, persisterNum)
	s.trackEventChans = make([]chan *trackEventPair, persisterNum)

	for i := 0; i < persisterNum; i++ {
		s.eventPairChans[i] = make(
			chan *sessionEventPair,
			defaultChanBufferSize,
		)
		s.trackEventChans[i] = make(
			chan *trackEventPair,
			defaultChanBufferSize,
		)
	}

	s.persistWg.Add(persisterNum * 2)

	for _, ch := range s.eventPairChans {
		go func(ch chan *sessionEventPair) {
			defer s.persistWg.Done()
			for pair := range ch {
				ctx := context.Background()
				ctx, cancel := context.WithTimeout(
					ctx,
					defaultAsyncPersistTimeout,
				)
				if err := s.addEvent(ctx, pair.key, pair.event); err != nil {
					log.ErrorfContext(
						ctx,
						"async persist event: %v",
						err,
					)
				}
				cancel()
			}
		}(ch)
	}

	for _, ch := range s.trackEventChans {
		go func(ch chan *trackEventPair) {
			defer s.persistWg.Done()
			for pair := range ch {
				ctx := context.Background()
				ctx, cancel := context.WithTimeout(
					ctx,
					defaultAsyncPersistTimeout,
				)
				if err := s.addTrackEvent(
					ctx,
					pair.key,
					pair.event,
				); err != nil {
					log.ErrorfContext(
						ctx,
						"async persist track event: %v",
						err,
					)
				}
				cancel()
			}
		}(ch)
	}
}

// ---------- Embedding indexing ----------

// shouldIndexEvent reports whether the event should be indexed for search.
func shouldIndexEvent(evt *event.Event) bool {
	return evt != nil && evt.Response != nil &&
		!evt.IsPartial && evt.IsValidContent()
}

// extractEventText extracts indexable text and role from an event.
func extractEventText(evt *event.Event) (string, model.Role) {
	if !shouldIndexEvent(evt) {
		return "", ""
	}
	if len(evt.Response.Choices) == 0 {
		return "", ""
	}
	msg := evt.Response.Choices[0].Message
	if len(msg.ToolCalls) > 0 {
		return "", ""
	}
	content := msg.Content
	if content == "" && len(msg.ContentParts) > 0 {
		var sb strings.Builder
		for _, p := range msg.ContentParts {
			if p.Text != nil {
				sb.WriteString(*p.Text)
				sb.WriteString(" ")
			}
		}
		content = strings.TrimSpace(sb.String())
	}
	if content == "" {
		return "", ""
	}
	role := msg.Role
	if role == "" {
		role = model.RoleAssistant
	}
	if msg.ToolID != "" || role == model.RoleTool {
		role = model.RoleTool
	}
	if role == model.RoleTool {
		toolName := strings.TrimSpace(msg.ToolName)
		if toolName != "" {
			content = toolName + ": " + content
		}
	}
	return content, role
}

// indexEventAfterPersist triggers embedding generation after event persistence.
func (s *Service) indexEventAfterPersist(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
) {
	if s.opts.embedder == nil {
		return
	}
	if !shouldIndexEvent(evt) {
		return
	}
	if s.opts.syncIndexing {
		ictx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			s.opts.embedTimeout,
		)
		defer cancel()
		s.asyncIndexEvent(ictx, sess, evt)
		return
	}
	s.triggerAsyncIndexEvent(ctx, sess, evt)
}

// triggerAsyncIndexEvent detaches indexing work from the request context.
func (s *Service) triggerAsyncIndexEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
) {
	if !shouldIndexEvent(evt) {
		return
	}
	go func() {
		ictx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			s.opts.embedTimeout,
		)
		defer cancel()
		s.asyncIndexEvent(ictx, sess, evt)
	}()
}

// asyncIndexEvent generates embedding and updates the vec0 index.
func (s *Service) asyncIndexEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
) {
	text, role := extractEventText(evt)
	if text == "" {
		return
	}
	if s.opts.embedder == nil {
		return
	}
	emb, err := s.opts.embedder.GetEmbedding(ctx, text)
	if err != nil {
		log.DebugfContext(ctx,
			"sqlite session: embedding failed: %v", err)
		return
	}
	if len(emb) == 0 {
		log.DebugfContext(ctx,
			"sqlite session: empty embedding returned")
		return
	}
	if err := s.updateEventEmbedding(
		ctx, sess, evt, text, string(role), emb,
	); err != nil {
		log.DebugfContext(ctx,
			"sqlite session: update embedding failed: %v", err)
	}
}

// updateEventEmbedding updates the vec0 index with the generated embedding.
func (s *Service) updateEventEmbedding(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	contentText string,
	role string,
	emb []float64,
) error {
	// Serialize embedding to vec0 blob format.
	f32 := make([]float32, len(emb))
	for i, v := range emb {
		f32[i] = float32(v)
	}
	blob, err := vecSerializeFloat32(f32)
	if err != nil {
		return fmt.Errorf("serialize embedding: %w", err)
	}

	// Find the matching event row in session_events.
	var rowID int64
	matchExpr := `event->>'id' = ?`
	matchValue := evt.ID
	if matchValue == "" {
		eventBytes, err := json.Marshal(evt)
		if err != nil {
			return fmt.Errorf("marshal event: %w", err)
		}
		matchExpr = `event = ?`
		matchValue = string(eventBytes)
	}

	query := fmt.Sprintf(`SELECT id FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ?
AND %s AND deleted_at IS NULL
ORDER BY created_at DESC LIMIT 1`,
		s.tableSessionEvents, matchExpr)

	err = s.db.QueryRowContext(ctx, query,
		matchValue,
		sess.AppName, sess.UserID, sess.ID,
	).Scan(&rowID)
	if err != nil {
		return fmt.Errorf("find event row: %w", err)
	}

	// Insert or replace into vec0 table.
	now := time.Now().UTC()
	vecSQL := fmt.Sprintf(`INSERT OR REPLACE INTO %s(
  rowid, embedding, app_name, user_id, session_id,
  content_text, role, created_at
) VALUES (?, `+vecBlobPlaceholder+`, ?, ?, ?, ?, ?, ?)`,
		s.vecTableName())

	_, err = s.db.ExecContext(ctx, vecSQL,
		rowID, blob,
		sess.AppName, sess.UserID, sess.ID,
		contentText, role, now.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("insert vec0: %w", err)
	}

	// Update FTS5 index if enabled.
	if s.opts.enableFTS {
		ftsSQL := fmt.Sprintf(`INSERT OR REPLACE INTO %s(rowid, content_text) VALUES(?, ?)`,
			s.ftsTableName())
		if _, err := s.db.ExecContext(ctx, ftsSQL, rowID, contentText); err != nil {
			return fmt.Errorf("insert fts5: %w", err)
		}
	}

	return nil
}

// vecBlobPlaceholder is the SQL placeholder for vec_f32 serialized blob.
const vecBlobPlaceholder = "vec_f32(?)"

// indexerWg is used to wait for async indexing goroutines to complete.
// It is embedded in the Service struct via the initDB path.
var globalIndexerWg sync.WaitGroup
