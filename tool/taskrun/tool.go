//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package taskrun provides tools for controlling background task runs.
package taskrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	taskrunruntime "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	toolSpawn      = "start_task_run"
	toolList       = "list_task_runs"
	toolGet        = "get_task_run"
	toolCancel     = "cancel_task_run"
	toolWait       = "wait_task_run"
	toolTranscript = "read_task_run_transcript"

	argAgentName      = "agent_name"
	argID             = "id"
	argIDs            = "ids"
	argLimit          = "limit"
	argMode           = "mode"
	argStrategy       = "strategy"
	argTask           = "task"
	argTimeoutSeconds = "timeout_seconds"
	argWaitSeconds    = "wait_timeout_seconds"

	spawnModeAsync = "async"
	spawnModeSync  = "sync"

	strategyAll = "all"
	strategyAny = "any"

	defaultTranscriptEventLimit = 40
	maxTranscriptEventLimit     = 200

	schemaTypeArray   = "array"
	schemaTypeInteger = "integer"
	schemaTypeObject  = "object"
	schemaTypeString  = "string"
)

// Option configures the generated tools.
type Option func(*options)

type options struct {
	defaultAgentName        string
	runtimeState            map[string]any
	injectedContextMessages []model.Message
	sessionService          session.Service
	allowNested             bool
	propagateParentAppName  bool
}

// WithDefaultAgentName configures the agent selected by spawn when the caller
// does not provide one.
func WithDefaultAgentName(name string) Option {
	return func(opts *options) {
		opts.defaultAgentName = strings.TrimSpace(name)
	}
}

// WithRuntimeState merges static runtime state into each spawned run.
func WithRuntimeState(state map[string]any) Option {
	return func(opts *options) {
		opts.runtimeState = cloneRuntimeState(state)
	}
}

// WithInjectedContextMessages appends non-persisted context messages to each
// spawned run.
func WithInjectedContextMessages(messages []model.Message) Option {
	return func(opts *options) {
		opts.injectedContextMessages = append(
			[]model.Message(nil),
			messages...,
		)
	}
}

// WithSessionService enables transcript reads for child task sessions.
func WithSessionService(service session.Service) Option {
	return func(opts *options) {
		opts.sessionService = service
	}
}

// WithParentAppNamePropagation copies the current invocation app name into
// spawned runs. It is disabled by default so workers keep their runner-level
// default app namespace unless callers explicitly opt in.
func WithParentAppNamePropagation(enabled bool) Option {
	return func(opts *options) {
		opts.propagateParentAppName = enabled
	}
}

// WithNestedSpawns allows a task run to spawn additional task runs.
func WithNestedSpawns(enabled bool) Option {
	return func(opts *options) {
		opts.allowNested = enabled
	}
}

// Tools contains all task run control tools.
type Tools struct {
	state *toolState

	spawn  *spawnTool
	list   *listTool
	get    *getTool
	cancel *cancelTool
	wait   *waitTool
	read   *transcriptTool
}

type toolState struct {
	controller taskrunruntime.Controller
	options    options
}

// NewTools creates task run control tools.
func NewTools(
	controller taskrunruntime.Controller,
	opts ...Option,
) Tools {
	options := options{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	state := &toolState{
		controller: controller,
		options:    options,
	}
	t := Tools{
		state: state,
	}
	t.spawn = &spawnTool{state: state}
	t.list = &listTool{state: state}
	t.get = &getTool{state: state}
	t.cancel = &cancelTool{state: state}
	t.wait = &waitTool{state: state}
	if options.sessionService != nil {
		t.read = &transcriptTool{state: state}
	}
	return t
}

// SetController updates the controller used by all tools.
func (t *Tools) SetController(controller taskrunruntime.Controller) {
	if t == nil {
		return
	}
	if t.state == nil {
		t.state = &toolState{}
	}
	t.state.controller = controller
}

// All returns all tool declarations.
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
	if t.read != nil {
		tools = append(tools, t.read)
	}
	return tools
}

type spawnTool struct {
	state *toolState
}

type listTool struct {
	state *toolState
}

type getTool struct {
	state *toolState
}

type cancelTool struct {
	state *toolState
}

type waitTool struct {
	state *toolState
}

type transcriptTool struct {
	state *toolState
}

type spawnInput struct {
	Task               string `json:"task"`
	AgentName          string `json:"agent_name"`
	Mode               string `json:"mode"`
	TimeoutSeconds     int    `json:"timeout_seconds"`
	WaitTimeoutSeconds int    `json:"wait_timeout_seconds"`
}

type runIDInput struct {
	ID             string `json:"id"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type waitInput struct {
	IDs            []string `json:"ids"`
	Strategy       string   `json:"strategy"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

type waitResult struct {
	Strategy string          `json:"strategy"`
	Runs     []waitRunResult `json:"runs"`
}

type waitRunResult struct {
	ID     string                `json:"id"`
	Status taskrunruntime.Status `json:"status"`
	Run    *taskrunruntime.Run   `json:"run,omitempty"`
	Error  string                `json:"error,omitempty"`
}

// waitResultItem 是批量等待内部使用的中间结果类型。
type waitResultItem struct {
	id  string
	run *taskrunruntime.Run
	err error
}

type transcriptInput struct {
	ID    string `json:"id"`
	Limit int    `json:"limit"`
}

type listResult struct {
	Runs []taskrunruntime.Run `json:"runs,omitempty"`
}

type transcriptResult struct {
	ID             string                `json:"id,omitempty"`
	Status         taskrunruntime.Status `json:"status,omitempty"`
	ChildSessionID string                `json:"child_session_id,omitempty"`
	Events         []transcriptEvent     `json:"events,omitempty"`
}

type transcriptEvent struct {
	ID        string     `json:"id,omitempty"`
	Author    string     `json:"author,omitempty"`
	Object    string     `json:"object,omitempty"`
	Role      model.Role `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	ToolID    string     `json:"tool_id,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
	ToolCalls []string   `json:"tool_calls,omitempty"`
	Error     string     `json:"error,omitempty"`
	Timestamp time.Time  `json:"timestamp,omitempty"`
}

func (t *spawnTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolSpawn,
		Description: "Start one task run for the current session. " +
			"By default mode is async and the tool returns " +
			"immediately with a run id. Use mode=sync when the " +
			"parent agent must wait for the delegated run to " +
			"finish before continuing.",
		InputSchema: &tool.Schema{
			Type:     schemaTypeObject,
			Required: []string{argTask},
			Properties: map[string]*tool.Schema{
				argTask: {
					Type: schemaTypeString,
					Description: "Delegated task for the " +
						"background run.",
				},
				argAgentName: {
					Type: schemaTypeString,
					Description: "Optional registered agent " +
						"name to run.",
				},
				argMode: {
					Type: schemaTypeString,
					Description: "Optional execution mode: " +
						spawnModeAsync + " or " +
						spawnModeSync + ". Default is " +
						spawnModeAsync + ".",
				},
				argTimeoutSeconds: {
					Type: schemaTypeInteger,
					Description: "Optional timeout in seconds " +
						"for the delegated run.",
				},
				argWaitSeconds: {
					Type: schemaTypeInteger,
					Description: "Optional wait timeout in " +
						"seconds when mode is " +
						spawnModeSync + ".",
				},
			},
		},
	}
}

func (t *spawnTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	state, err := requireTools(t.state)
	if err != nil {
		return nil, err
	}
	if !state.options.allowNested && isNestedTaskRun(ctx) {
		return nil, fmt.Errorf(
			"taskrun: nested task runs are not supported",
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
	userID, sessionID, err := currentContext(ctx)
	if err != nil {
		return nil, err
	}
	agentName := strings.TrimSpace(in.AgentName)
	if agentName == "" {
		agentName = state.options.defaultAgentName
	}
	run, err := state.controller.Spawn(ctx, taskrunruntime.SpawnRequest{
		OwnerUserID:             userID,
		ParentSessionID:         sessionID,
		ParentAppName:           currentAppName(ctx),
		AppName:                 appNameForSpawnTool(ctx, state.options),
		AgentName:               agentName,
		Task:                    in.Task,
		Timeout:                 secondsDuration(in.TimeoutSeconds),
		RuntimeState:            cloneRuntimeState(state.options.runtimeState),
		InjectedContextMessages: state.options.injectedContextMessages,
	})
	if err != nil {
		return nil, err
	}
	if mode == spawnModeSync {
		waitCtx, cancel := waitContext(ctx, in.WaitTimeoutSeconds)
		if cancel != nil {
			defer cancel()
		}
		final, err := state.controller.Wait(waitCtx, run.ID)
		if waitTimedOut(ctx, waitCtx, err, in.WaitTimeoutSeconds) {
			return state.controller.Get(ctx, run.ID)
		}
		if err != nil {
			return nil, err
		}
		return final, nil
	}
	return run, nil
}

func (t *listTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolList,
		Description: "List background task runs created " +
			"from the current session.",
		InputSchema: &tool.Schema{
			Type: schemaTypeObject,
		},
	}
}

func (t *listTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	state, err := requireTools(t.state)
	if err != nil {
		return nil, err
	}
	if err := validateEmptyArgs(args); err != nil {
		return nil, err
	}
	key, err := currentSessionKey(ctx)
	if err != nil {
		return nil, err
	}
	runs, err := state.controller.List(ctx, taskrunruntime.ListFilter{
		OwnerUserID:     key.UserID,
		ParentSessionID: key.SessionID,
		ParentAppName:   key.AppName,
	})
	if err != nil {
		return nil, err
	}
	return listResult{Runs: runs}, nil
}

func (t *getTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolGet,
		Description: "Get the latest status and result for one " +
			"background task run.",
		InputSchema: runIDSchema(),
	}
}

func (t *getTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	state, err := requireTools(t.state)
	if err != nil {
		return nil, err
	}
	runID, userID, err := decodeRunIDArgs(ctx, args)
	if err != nil {
		return nil, err
	}
	run, err := state.controller.Get(ctx, runID)
	if err != nil {
		return nil, err
	}
	if !sameOwner(run, userID) {
		return nil, taskrunruntime.ErrRunNotFound
	}
	return run, nil
}

func (t *cancelTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolCancel,
		Description: "Cancel one background task run. " +
			"This is best-effort.",
		InputSchema: runIDSchema(),
	}
}

func (t *cancelTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	state, err := requireTools(t.state)
	if err != nil {
		return nil, err
	}
	runID, userID, err := decodeRunIDArgs(ctx, args)
	if err != nil {
		return nil, err
	}
	run, err := state.controller.Get(ctx, runID)
	if err != nil {
		return nil, err
	}
	if !sameOwner(run, userID) {
		return nil, taskrunruntime.ErrRunNotFound
	}
	canceled, _, err := state.controller.Cancel(ctx, runID)
	if err != nil {
		return nil, err
	}
	return canceled, nil
}

func (t *waitTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolWait,
		Description: "Wait until one or more background task runs " +
			"reach a terminal status. Use strategy=all (default) to " +
			"wait for all runs, or strategy=any to return as soon " +
			"as any run finishes.",
		InputSchema: waitSchema(),
	}
}

func (t *waitTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	state, err := requireTools(t.state)
	if err != nil {
		return nil, err
	}
	in, userID, err := decodeWaitArgs(ctx, args)
	if err != nil {
		return nil, err
	}

	// 去重
	runIDs := uniqueStrings(in.IDs)

	// 预校验：所有 run 必须存在且属于当前用户
	for _, id := range runIDs {
		run, err := state.controller.Get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("taskrun: run %q not found", id)
		}
		if !sameOwner(run, userID) {
			return nil, fmt.Errorf("taskrun: run %q not found", id)
		}
	}

	// 设置超时
	waitCtx := ctx
	var cancel context.CancelFunc
	if in.TimeoutSeconds > 0 {
		waitCtx, cancel = waitContext(ctx, in.TimeoutSeconds)
		defer cancel()
	}

	switch in.Strategy {
	case strategyAny:
		return t.waitAny(waitCtx, state, runIDs)
	default:
		return t.waitAll(waitCtx, state, runIDs)
	}
}

// waitAll 等待所有 run 达到终态。
func (t *waitTool) waitAll(
	ctx context.Context,
	state *toolState,
	runIDs []string,
) (any, error) {
	var g errgroup.Group
	results := make([]waitResultItem, len(runIDs))
	for i, id := range runIDs {
		i, id := i, id
		g.Go(func() error {
			run, err := state.controller.Wait(ctx, id)
			results[i] = waitResultItem{id: id, run: run, err: err}
			return nil
		})
	}
	_ = g.Wait()

	return buildWaitResult(strategyAll, runIDs, results), nil
}

// waitAny 等待任意一个 run 达到终态后立即返回。
func (t *waitTool) waitAny(
	ctx context.Context,
	state *toolState,
	runIDs []string,
) (any, error) {
	// 用带取消的 context 实现"任意一个完成即返回"
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	type firstResult struct {
		id  string
		run *taskrunruntime.Run
		err error
	}

	resultCh := make(chan firstResult, len(runIDs))
	for _, id := range runIDs {
		id := id
		go func() {
			run, err := state.controller.Wait(waitCtx, id)
			select {
			case resultCh <- firstResult{id: id, run: run, err: err}:
			default:
			}
		}()
	}

	// 等待第一个结果
	first := <-resultCh
	cancel() // 通知其他 goroutine 停止等待

	// 收集所有 run 的当前状态
	results := make([]waitResultItem, len(runIDs))
	for i, id := range runIDs {
		if id == first.id {
			results[i] = waitResultItem{id: id, run: first.run, err: first.err}
		} else {
			run, err := state.controller.Get(ctx, id)
			if err != nil {
				results[i] = waitResultItem{id: id, err: err}
			} else {
				results[i] = waitResultItem{id: id, run: run}
			}
		}
	}

	return buildWaitResult(strategyAny, runIDs, results), nil
}

func buildWaitResult(
	strategy string,
	runIDs []string,
	results []waitResultItem,
) waitResult {
	out := make([]waitRunResult, len(runIDs))
	for i, r := range results {
		wr := waitRunResult{ID: r.id}
		if r.err != nil {
			wr.Error = r.err.Error()
		} else if r.run != nil {
			wr.Status = r.run.Status
			wr.Run = r.run
		}
		out[i] = wr
	}
	return waitResult{
		Strategy: strategy,
		Runs:     out,
	}
}

func (t *transcriptTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolTranscript,
		Description: "Read recent transcript events from one child " +
			"task run session.",
		InputSchema: &tool.Schema{
			Type:     schemaTypeObject,
			Required: []string{argID},
			Properties: map[string]*tool.Schema{
				argID: {
					Type:        schemaTypeString,
					Description: "Task run id returned by start.",
				},
				argLimit: {
					Type: schemaTypeInteger,
					Description: "Optional number of recent " +
						"events to read. Defaults to 40 and " +
						"is capped at 200.",
				},
			},
		},
	}
}

func (t *transcriptTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	state, err := requireTools(t.state)
	if err != nil {
		return nil, err
	}
	if state.options.sessionService == nil {
		return nil, fmt.Errorf("taskrun: session service unavailable")
	}
	in, key, err := decodeTranscriptArgs(ctx, args)
	if err != nil {
		return nil, err
	}
	run, err := state.controller.Get(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if !sameOwnerAndParent(run, key.UserID, key.AppName, key.SessionID) {
		return nil, taskrunruntime.ErrRunNotFound
	}
	if strings.TrimSpace(run.ChildSessionID) == "" {
		return nil, fmt.Errorf("taskrun: child session id unavailable")
	}
	appName := appNameForTranscript(run, key.AppName)
	limit := normalizeTranscriptLimit(in.Limit)
	child, err := state.options.sessionService.GetSession(
		ctx,
		session.Key{
			AppName:   appName,
			UserID:    run.OwnerUserID,
			SessionID: run.ChildSessionID,
		},
		session.WithEventNum(limit),
	)
	if err != nil {
		return nil, err
	}
	if child == nil {
		return nil, taskrunruntime.ErrRunNotFound
	}
	return transcriptResult{
		ID:             run.ID,
		Status:         run.Status,
		ChildSessionID: run.ChildSessionID,
		Events:         transcriptEvents(trimTranscriptEvents(child.GetEvents(), limit)),
	}, nil
}

func normalizeSpawnMode(mode string) (string, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return spawnModeAsync, nil
	}
	switch mode {
	case spawnModeAsync, spawnModeSync:
		return mode, nil
	default:
		return "", fmt.Errorf("taskrun: unsupported mode %q", mode)
	}
}

func waitContext(
	ctx context.Context,
	timeoutSeconds int,
) (context.Context, context.CancelFunc) {
	if timeoutSeconds <= 0 {
		return ctx, nil
	}
	return context.WithTimeout(ctx, secondsDuration(timeoutSeconds))
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
				Description: "Task run id returned by start.",
			},
		},
	}
}

func waitSchema() *tool.Schema {
	return &tool.Schema{
		Type:     schemaTypeObject,
		Required: []string{argIDs},
		Properties: map[string]*tool.Schema{
			argIDs: {
				Type:        schemaTypeArray,
				Description: "Task run ids to wait for.",
				Items: &tool.Schema{
					Type: schemaTypeString,
				},
			},
			argStrategy: {
				Type: schemaTypeString,
				Description: "Wait strategy: " +
					`"all" waits for all runs to finish (default), ` +
					`"any" returns as soon as any run finishes.`,
			},
			argTimeoutSeconds: {
				Type:        schemaTypeInteger,
				Description: "Optional wait timeout in seconds.",
			},
		},
	}
}

func requireTools(state *toolState) (*toolState, error) {
	if state == nil || state.controller == nil {
		return nil, fmt.Errorf("taskrun: controller unavailable")
	}
	return state, nil
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
	runID := strings.TrimSpace(in.ID)
	if runID == "" {
		return "", "", fmt.Errorf("taskrun: empty run id")
	}
	return runID, userID, nil
}

func decodeWaitArgs(
	ctx context.Context,
	args []byte,
) (waitInput, string, error) {
	var in waitInput
	if err := json.Unmarshal(args, &in); err != nil {
		return waitInput{}, "", err
	}
	userID, _, err := currentContext(ctx)
	if err != nil {
		return waitInput{}, "", err
	}
	if len(in.IDs) == 0 {
		return waitInput{}, "", fmt.Errorf("taskrun: empty run ids")
	}
	for i := range in.IDs {
		in.IDs[i] = strings.TrimSpace(in.IDs[i])
	}
	in.Strategy = strings.TrimSpace(in.Strategy)
	if in.Strategy == "" {
		in.Strategy = strategyAll
	}
	switch in.Strategy {
	case strategyAll, strategyAny:
	default:
		return waitInput{}, "", fmt.Errorf(
			"taskrun: unsupported strategy %q", in.Strategy,
		)
	}
	return in, userID, nil
}

func decodeTranscriptArgs(
	ctx context.Context,
	args []byte,
) (transcriptInput, session.Key, error) {
	var in transcriptInput
	if err := json.Unmarshal(args, &in); err != nil {
		return transcriptInput{}, session.Key{}, err
	}
	key, err := currentSessionKey(ctx)
	if err != nil {
		return transcriptInput{}, session.Key{}, err
	}
	in.ID = strings.TrimSpace(in.ID)
	if in.ID == "" {
		return transcriptInput{}, session.Key{}, fmt.Errorf(
			"taskrun: empty run id",
		)
	}
	return in, key, nil
}

func currentContext(ctx context.Context) (string, string, error) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return "", "", fmt.Errorf(
			"taskrun: current session context is unavailable",
		)
	}
	userID := strings.TrimSpace(inv.Session.UserID)
	if userID == "" {
		return "", "", fmt.Errorf(
			"taskrun: current user id is unavailable",
		)
	}
	sessionID := strings.TrimSpace(inv.Session.ID)
	if sessionID == "" {
		return "", "", fmt.Errorf(
			"taskrun: current session id is unavailable",
		)
	}
	return userID, sessionID, nil
}

func currentSessionKey(ctx context.Context) (session.Key, error) {
	userID, sessionID, err := currentContext(ctx)
	if err != nil {
		return session.Key{}, err
	}
	appName := currentAppName(ctx)
	if appName == "" {
		return session.Key{}, fmt.Errorf(
			"taskrun: current app name is unavailable",
		)
	}
	return session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	}, nil
}

func currentAppName(ctx context.Context) string {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return ""
	}
	return strings.TrimSpace(inv.Session.AppName)
}

func appNameForSpawnTool(ctx context.Context, opts options) string {
	if !opts.propagateParentAppName {
		return ""
	}
	return currentAppName(ctx)
}

func isNestedTaskRun(ctx context.Context) bool {
	nested, ok := agent.GetRuntimeStateValueFromContext[bool](
		ctx,
		taskrunruntime.RuntimeStateKeyRun,
	)
	return ok && nested
}

func sameOwner(run *taskrunruntime.Run, userID string) bool {
	return run != nil &&
		strings.TrimSpace(run.OwnerUserID) == strings.TrimSpace(userID)
}

func sameOwnerAndParent(
	run *taskrunruntime.Run,
	userID string,
	appName string,
	sessionID string,
) bool {
	return sameOwner(run, userID) &&
		sameParentApp(run, appName) &&
		strings.TrimSpace(run.ParentSessionID) ==
			strings.TrimSpace(sessionID)
}

func sameParentApp(run *taskrunruntime.Run, appName string) bool {
	if run == nil {
		return false
	}
	runAppName := strings.TrimSpace(run.ParentAppName)
	if runAppName == "" {
		runAppName = strings.TrimSpace(run.AppName)
	}
	return runAppName == strings.TrimSpace(appName)
}

func secondsDuration(seconds int) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func cloneRuntimeState(state map[string]any) map[string]any {
	if len(state) == 0 {
		return nil
	}
	out := make(map[string]any, len(state))
	for key, value := range state {
		out[key] = value
	}
	return out
}

func normalizeTranscriptLimit(limit int) int {
	if limit <= 0 {
		return defaultTranscriptEventLimit
	}
	if limit > maxTranscriptEventLimit {
		return maxTranscriptEventLimit
	}
	return limit
}

func appNameForTranscript(run *taskrunruntime.Run, fallback string) string {
	if run != nil {
		if appName := strings.TrimSpace(run.AppName); appName != "" {
			return appName
		}
	}
	return strings.TrimSpace(fallback)
}

func trimTranscriptEvents(events []event.Event, limit int) []event.Event {
	if limit <= 0 || len(events) <= limit {
		return events
	}
	return events[len(events)-limit:]
}

func transcriptEvents(events []event.Event) []transcriptEvent {
	if len(events) == 0 {
		return nil
	}
	out := make([]transcriptEvent, 0, len(events))
	for i := range events {
		out = append(out, transcriptEventFromEvent(&events[i]))
	}
	return out
}

func transcriptEventFromEvent(evt *event.Event) transcriptEvent {
	if evt == nil {
		return transcriptEvent{}
	}
	out := transcriptEvent{
		ID:        evt.ID,
		Author:    evt.Author,
		Timestamp: evt.Timestamp,
	}
	if evt.Response == nil {
		return out
	}
	out.Object = evt.Response.Object
	if evt.Response.Error != nil {
		out.Error = evt.Response.Error.Message
	}
	for _, choice := range evt.Response.Choices {
		mergeTranscriptMessage(&out, choice.Message)
		mergeTranscriptMessage(&out, choice.Delta)
	}
	return out
}

func mergeTranscriptMessage(out *transcriptEvent, msg model.Message) {
	if out == nil {
		return
	}
	if out.Role == "" && msg.Role != "" {
		out.Role = msg.Role
	}
	if out.Content == "" && msg.Content != "" {
		out.Content = msg.Content
	}
	if out.ToolID == "" && msg.ToolID != "" {
		out.ToolID = msg.ToolID
	}
	if out.ToolName == "" && msg.ToolName != "" {
		out.ToolName = msg.ToolName
	}
	for _, toolCall := range msg.ToolCalls {
		name := strings.TrimSpace(toolCall.Function.Name)
		if name == "" {
			continue
		}
		out.ToolCalls = append(out.ToolCalls, name)
	}
}

// uniqueStrings 返回去重后的字符串切片，保持首次出现的顺序。
func uniqueStrings(s []string) []string {
	if len(s) == 0 {
		return s
	}
	seen := make(map[string]struct{}, len(s))
	out := make([]string, 0, len(s))
	for _, v := range s {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
