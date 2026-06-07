//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package subagentrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
	openclawsubagent "trpc.group/trpc-go/trpc-agent-go/openclaw/subagent"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	toolSubagentsSpawn  = "subagents_spawn"
	toolSubagentsList   = "subagents_list"
	toolSubagentsGet    = "subagents_get"
	toolSubagentsCancel = "subagents_cancel"
	toolSubagentsWait   = "subagents_wait"

	toolSessionsSpawn  = "sessions_spawn"
	toolSessionsList   = "sessions_list"
	toolSessionsGet    = "sessions_get"
	toolSessionsCancel = "sessions_cancel"

	argID             = "id"
	argMode           = "mode"
	argIsolation      = "isolation"
	argTask           = "task"
	argTitle          = "title"
	argRef            = "ref"
	argTimeoutSeconds = "timeout_seconds"
	argWaitSeconds    = "wait_timeout_seconds"

	spawnModeAsync  = "async"
	spawnModeSync   = "sync"
	spawnModeReview = "review"

	schemaTypeInteger = "integer"
	schemaTypeObject  = "object"
	schemaTypeString  = "string"
)

// ToolsConfig configures the subagent tool set.
type ToolsConfig struct {
	// EnableSessionAlias registers the deprecated sessions_*
	// tool names as compatibility aliases for subagents_*.
	EnableSessionAlias bool
}

type Tools struct {
	spawn  *spawnTool
	list   *listTool
	get    *getTool
	cancel *cancelTool
	wait   *waitTool

	spawnAlias  *spawnTool
	listAlias   *listTool
	getAlias    *getTool
	cancelAlias *cancelTool

	cfg ToolsConfig
}

func NewTools(svc *Service, cfg ToolsConfig) Tools {
	return Tools{
		spawn:       &spawnTool{name: toolSubagentsSpawn, svc: svc},
		list:        &listTool{name: toolSubagentsList, svc: svc},
		get:         &getTool{name: toolSubagentsGet, svc: svc},
		cancel:      &cancelTool{name: toolSubagentsCancel, svc: svc},
		wait:        &waitTool{name: toolSubagentsWait, svc: svc},
		spawnAlias:  &spawnTool{name: toolSessionsSpawn, alias: true, svc: svc},
		listAlias:   &listTool{name: toolSessionsList, alias: true, svc: svc},
		getAlias:    &getTool{name: toolSessionsGet, alias: true, svc: svc},
		cancelAlias: &cancelTool{name: toolSessionsCancel, alias: true, svc: svc},
		cfg:         cfg,
	}
}

func (t *Tools) SetService(svc *Service) {
	if t == nil {
		return
	}
	for _, item := range []serviceAwareTool{
		t.spawn,
		t.list,
		t.get,
		t.cancel,
		t.wait,
		t.spawnAlias,
		t.listAlias,
		t.getAlias,
		t.cancelAlias,
	} {
		if item != nil {
			item.setService(svc)
		}
	}
}

func (t *Tools) All() []tool.Tool {
	if t == nil {
		return nil
	}
	tools := []tool.Tool{
		t.spawn,
		t.list,
		t.get,
		t.cancel,
		t.wait,
	}
	if t.cfg.EnableSessionAlias {
		tools = append(tools,
			t.spawnAlias,
			t.listAlias,
			t.getAlias,
			t.cancelAlias,
		)
	}
	return tools
}

type serviceAwareTool interface {
	setService(svc *Service)
}

type spawnTool struct {
	name  string
	alias bool
	svc   *Service
}

type listTool struct {
	name  string
	alias bool
	svc   *Service
}

type getTool struct {
	name  string
	alias bool
	svc   *Service
}

type cancelTool struct {
	name  string
	alias bool
	svc   *Service
}

type waitTool struct {
	name string
	svc  *Service
}

type spawnInput struct {
	Title              string `json:"title"`
	Ref                string `json:"ref"`
	Task               string `json:"task"`
	Mode               string `json:"mode"`
	Isolation          string `json:"isolation"`
	TimeoutSeconds     int    `json:"timeout_seconds"`
	WaitTimeoutSeconds int    `json:"wait_timeout_seconds"`
}

type runIDInput struct {
	ID             string `json:"id"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type listResult struct {
	Runs []openclawsubagent.Run `json:"runs,omitempty"`
}

func (t *spawnTool) setService(svc *Service) {
	t.svc = svc
}

func (t *listTool) setService(svc *Service) {
	t.svc = svc
}

func (t *getTool) setService(svc *Service) {
	t.svc = svc
}

func (t *cancelTool) setService(svc *Service) {
	t.svc = svc
}

func (t *waitTool) setService(svc *Service) {
	t.svc = svc
}

func (t *spawnTool) Declaration() *tool.Declaration {
	description := "Spawn one OpenClaw background subagent for " +
		"the current session. Use mode=async for long-running " +
		"or parallel work that can continue after this turn, " +
		"mode=sync when the parent must wait for the result, " +
		"and mode=review when the parent must show the result " +
		"to the user and wait for the next user reply before " +
		"continuing."
	if t.alias {
		description = "Compatibility alias for " +
			toolSubagentsSpawn + ". " + description
	}
	return &tool.Declaration{
		Name:        t.name,
		Description: description,
		InputSchema: &tool.Schema{
			Type:     schemaTypeObject,
			Required: []string{argTask},
			Properties: map[string]*tool.Schema{
				argTitle: {
					Type: schemaTypeString,
					Description: "Optional short title for the " +
						"subagent run. If not specified, it is " +
						"auto-generated from the task description.",
				},
				argRef: {
					Type: schemaTypeString,
					Description: "Optional business reference ID " +
						"for the subagent run, useful for " +
						"identification in logs and UI.",
				},
				argTask: {
					Type: schemaTypeString,
					Description: "Delegated task for the " +
						"background subagent.",
				},
				argMode: {
					Type: schemaTypeString,
					Description: "Optional execution mode: " +
						spawnModeAsync + ", " +
						spawnModeSync + ", or " +
						spawnModeReview + ". Default is " +
						spawnModeAsync + ".",
				},
				argIsolation: {
					Type: schemaTypeString,
					Description: "Optional isolation mode. " +
						"Use " + isolationWorktree +
						" to run the subagent in a " +
						"managed Git worktree.",
				},
				argTimeoutSeconds: {
					Type: schemaTypeInteger,
					Description: "Optional timeout in " +
						"seconds for the delegated run.",
				},
				argWaitSeconds: {
					Type: schemaTypeInteger,
					Description: "Optional wait timeout in " +
						"seconds for " + spawnModeSync +
						" or " + spawnModeReview +
						" mode.",
				},
			},
		},
	}
}

func (t *spawnTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	if t == nil || t.svc == nil {
		return nil, fmt.Errorf("subagent: service unavailable")
	}
	if isNestedSubagent(ctx) {
		return nil, fmt.Errorf(
			"subagent: nested subagent spawn is not supported",
		)
	}

	var in spawnInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	mode, err := normalizeSpawnMode(in.Mode)
	if err != nil {
		return nil, err
	}
	userID, sess, err := currentContext(ctx)
	if err != nil {
		return nil, err
	}
	delivery, err := outbound.ResolveTarget(
		ctx,
		outbound.DeliveryTarget{},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"subagent: resolve delivery target: %w",
			err,
		)
	}

	run, err := t.svc.Spawn(ctx, SpawnRequest{
		OwnerUserID:                    userID,
		ParentSessionID:                sess.ID,
		Title:                          deriveTitle(in.Title, in.Task),
		Ref:                            strings.TrimSpace(in.Ref),
		Task:                           in.Task,
		TimeoutSeconds:                 in.TimeoutSeconds,
		Isolation:                      in.Isolation,
		SuppressCompletionNotification: mode != spawnModeAsync,
		Delivery: deliveryTarget{
			Channel: delivery.Channel,
			Target:  delivery.Target,
		},
	})
	if err != nil {
		return nil, err
	}
	if mode == spawnModeAsync {
		return run, nil
	}
	waitCtx, cancel := waitContext(ctx, in.WaitTimeoutSeconds)
	if cancel != nil {
		defer cancel()
	}
	final, err := t.svc.WaitForUser(waitCtx, userID, run.ID)
	if waitTimedOut(ctx, waitCtx, err, in.WaitTimeoutSeconds) {
		return t.svc.GetForUser(userID, run.ID)
	}
	if err != nil {
		// Enhance context cancellation errors with diagnostic information
		return nil, enhanceWaitError(
			ctx,
			err,
			run.ID,
			mode,
			in.TimeoutSeconds,
			in.WaitTimeoutSeconds,
		)
	}
	if mode == spawnModeReview {
		if err := markAwaitingReview(ctx); err != nil {
			return nil, err
		}
	}
	return final, nil
}

func (t *listTool) Declaration() *tool.Declaration {
	description := "List OpenClaw background subagents created " +
		"from the current session."
	if t.alias {
		description = "Compatibility alias for " +
			toolSubagentsList + ". " + description
	}
	return &tool.Declaration{
		Name:        t.name,
		Description: description,
		InputSchema: &tool.Schema{
			Type: schemaTypeObject,
		},
	}
}

func (t *listTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	if t == nil || t.svc == nil {
		return nil, fmt.Errorf("subagent: service unavailable")
	}
	if err := validateEmptyArgs(args); err != nil {
		return nil, err
	}
	userID, sess, err := currentContext(ctx)
	if err != nil {
		return nil, err
	}
	return listResult{
		Runs: t.svc.ListForUser(
			userID,
			openclawsubagent.ListFilter{
				ParentSessionID: sess.ID,
			},
		),
	}, nil
}

func (t *getTool) Declaration() *tool.Declaration {
	description := "Get the latest status and result for one " +
		"OpenClaw background subagent run."
	if t.alias {
		description = "Compatibility alias for " +
			toolSubagentsGet + ". " + description
	}
	return &tool.Declaration{
		Name:        t.name,
		Description: description,
		InputSchema: runIDSchema(),
	}
}

func (t *getTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	if t == nil || t.svc == nil {
		return nil, fmt.Errorf("subagent: service unavailable")
	}
	runID, userID, err := decodeRunIDArgs(ctx, args)
	if err != nil {
		return nil, err
	}
	return t.svc.GetForUser(userID, runID)
}

func (t *cancelTool) Declaration() *tool.Declaration {
	description := "Cancel one OpenClaw background subagent run. " +
		"This is best-effort."
	if t.alias {
		description = "Compatibility alias for " +
			toolSubagentsCancel + ". " + description
	}
	return &tool.Declaration{
		Name:        t.name,
		Description: description,
		InputSchema: runIDSchema(),
	}
}

func (t *cancelTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	if t == nil || t.svc == nil {
		return nil, fmt.Errorf("subagent: service unavailable")
	}
	runID, userID, err := decodeRunIDArgs(ctx, args)
	if err != nil {
		return nil, err
	}
	run, _, err := t.svc.CancelForUser(userID, runID)
	if err != nil {
		return nil, err
	}
	return run, nil
}

func (t *waitTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: t.name,
		Description: "Wait until one OpenClaw background subagent " +
			"run reaches a terminal status.",
		InputSchema: waitSchema(),
	}
}

func (t *waitTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	if t == nil || t.svc == nil {
		return nil, fmt.Errorf("subagent: service unavailable")
	}
	in, userID, err := decodeWaitArgs(ctx, args)
	if err != nil {
		return nil, err
	}
	waitCtx := ctx
	var cancel context.CancelFunc
	if in.TimeoutSeconds > 0 {
		waitCtx, cancel = waitContext(ctx, in.TimeoutSeconds)
		defer cancel()
	}
	return t.svc.WaitForUser(waitCtx, userID, in.ID)
}

func normalizeSpawnMode(mode string) (string, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return spawnModeAsync, nil
	}
	switch mode {
	case spawnModeAsync, spawnModeSync, spawnModeReview:
		return mode, nil
	default:
		return "", fmt.Errorf("subagent: unsupported mode %q", mode)
	}
}

func waitContext(
	ctx context.Context,
	timeoutSeconds int,
) (context.Context, context.CancelFunc) {
	if timeoutSeconds <= 0 {
		return ctx, nil
	}
	return context.WithTimeout(
		ctx,
		time.Duration(timeoutSeconds)*time.Second,
	)
}

func markAwaitingReview(ctx context.Context) error {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return fmt.Errorf("subagent: current invocation unavailable")
	}
	if err := agent.MarkAwaitingUserReply(inv); err != nil {
		return fmt.Errorf("subagent: mark review continuation: %w", err)
	}
	return nil
}

func waitTimedOut(
	ctx context.Context,
	waitCtx context.Context,
	err error,
	timeoutSeconds int,
) bool {
	return timeoutSeconds > 0 &&
		ctx.Err() == nil &&
		errors.Is(waitCtx.Err(), context.DeadlineExceeded) &&
		errors.Is(err, context.DeadlineExceeded)
}

func runIDSchema() *tool.Schema {
	return &tool.Schema{
		Type:     schemaTypeObject,
		Required: []string{argID},
		Properties: map[string]*tool.Schema{
			argID: {
				Type:        schemaTypeString,
				Description: "Subagent run id returned by spawn.",
			},
		},
	}
}

func waitSchema() *tool.Schema {
	schema := runIDSchema()
	schema.Properties[argTimeoutSeconds] = &tool.Schema{
		Type:        schemaTypeInteger,
		Description: "Optional wait timeout in seconds.",
	}
	return schema
}

func validateEmptyArgs(args []byte) error {
	trimmed := strings.TrimSpace(string(args))
	if trimmed == "" || trimmed == "{}" {
		return nil
	}
	var ignored map[string]any
	return json.Unmarshal(args, &ignored)
}

func decodeRunIDArgs(
	ctx context.Context,
	args []byte,
) (string, string, error) {
	var in runIDInput
	if err := json.Unmarshal(args, &in); err != nil {
		return "", "", err
	}
	userID, _, err := currentContext(ctx)
	if err != nil {
		return "", "", err
	}
	in.ID = strings.TrimSpace(in.ID)
	if in.ID == "" {
		return "", "", fmt.Errorf("subagent: empty run id")
	}
	return in.ID, userID, nil
}

func decodeWaitArgs(
	ctx context.Context,
	args []byte,
) (runIDInput, string, error) {
	var in runIDInput
	if err := json.Unmarshal(args, &in); err != nil {
		return runIDInput{}, "", err
	}
	userID, _, err := currentContext(ctx)
	if err != nil {
		return runIDInput{}, "", err
	}
	in.ID = strings.TrimSpace(in.ID)
	if in.ID == "" {
		return runIDInput{}, "", fmt.Errorf("subagent: empty run id")
	}
	return in, userID, nil
}

func currentContext(
	ctx context.Context,
) (string, *session.Session, error) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return "", nil, fmt.Errorf(
			"subagent: current session context is unavailable",
		)
	}
	userID := strings.TrimSpace(inv.Session.UserID)
	if userID == "" {
		return "", nil, fmt.Errorf(
			"subagent: current user id is unavailable",
		)
	}
	if strings.TrimSpace(inv.Session.ID) == "" {
		return "", nil, fmt.Errorf(
			"subagent: current session id is unavailable",
		)
	}
	return userID, inv.Session, nil
}

func isNestedSubagent(ctx context.Context) bool {
	nested, ok := agent.GetRuntimeStateValueFromContext[bool](
		ctx,
		openclawsubagent.RuntimeStateKeyRun,
	)
	return ok && nested
}

// enhanceWaitError enhances context cancellation errors with detailed
// diagnostic information to help developers troubleshoot subagent wait failures.
func enhanceWaitError(
	ctx context.Context,
	err error,
	runID string,
	mode string,
	timeoutSeconds int,
	waitTimeoutSeconds int,
) error {
	if err == nil {
		return nil
	}

	// Only enhance context cancellation errors
	if !errors.Is(err, context.Canceled) &&
		!errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	var builder strings.Builder
	builder.WriteString("subagent wait failed: ")

	// Determine the root cause
	if errors.Is(err, context.DeadlineExceeded) {
		builder.WriteString("timeout exceeded")
	} else {
		builder.WriteString("context canceled")
	}

	// Add diagnostic information
	builder.WriteString("\n  - subagent run ID: ")
	builder.WriteString(runID)

	builder.WriteString("\n  - spawn mode: ")
	builder.WriteString(mode)

	// Add timeout configuration
	if timeoutSeconds > 0 {
		builder.WriteString("\n  - subagent timeout: ")
		builder.WriteString(fmt.Sprintf("%ds", timeoutSeconds))
	} else {
		builder.WriteString("\n  - subagent timeout: default (unlimited)")
	}

	if waitTimeoutSeconds > 0 {
		builder.WriteString("\n  - wait timeout: ")
		builder.WriteString(fmt.Sprintf("%ds", waitTimeoutSeconds))
	} else {
		builder.WriteString("\n  - wait timeout: inherited from parent context")
	}

	// Check parent context state
	if parentErr := ctx.Err(); parentErr != nil {
		builder.WriteString("\n  - parent context status: ")
		if errors.Is(parentErr, context.Canceled) {
			builder.WriteString("canceled")
		} else if errors.Is(parentErr, context.DeadlineExceeded) {
			builder.WriteString("deadline exceeded")
		} else {
			builder.WriteString(parentErr.Error())
		}
	} else {
		builder.WriteString("\n  - parent context status: active")
	}

	// Add suggestions
	builder.WriteString("\n  - suggestions:")
	if waitTimeoutSeconds <= 0 {
		builder.WriteString("\n    • set wait_timeout_seconds to create an independent timeout context")
	}
	if timeoutSeconds > 0 && waitTimeoutSeconds > 0 && waitTimeoutSeconds <= timeoutSeconds {
		builder.WriteString("\n    • consider setting wait_timeout_seconds > timeout_seconds")
	}
	builder.WriteString("\n    • check Runner timeout configuration (default: 2h)")
	builder.WriteString("\n    • check for user-initiated cancellation or network issues")

	// Wrap the original error to preserve error chain
	return fmt.Errorf("%s: %w", builder.String(), err)
}
