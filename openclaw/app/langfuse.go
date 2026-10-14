//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/admin"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/runtimeprofile"
	langfuseobs "trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse"
)

const (
	langfuseHostEnv        = "LANGFUSE_HOST"
	langfuseInsecureEnv    = "LANGFUSE_INSECURE"
	langfuseInitProjectEnv = "LANGFUSE_INIT_PROJECT_ID"

	langfuseTraceIDPlaceholder = "{{trace_id}}"

	langfuseTraceNameKey           = "langfuse.trace.name"
	langfuseUserIDKey              = "langfuse.user.id"
	langfuseSessionIDKey           = "langfuse.session.id"
	langfuseMetadataPrefix         = "langfuse.trace.metadata."
	langfuseMetadataAppName        = langfuseMetadataPrefix + "app_name"
	langfuseMetadataChannel        = langfuseMetadataPrefix + "channel"
	langfuseMetadataRequestID      = langfuseMetadataPrefix + "request_id"
	langfuseMetadataMessageID      = langfuseMetadataPrefix + "message_id"
	langfuseMetadataProfileID      = langfuseMetadataPrefix + "profile_id"
	langfuseMetadataProfileVersion = langfuseMetadataPrefix +
		"profile_version"
	langfuseMetadataAgentName = langfuseMetadataPrefix + "agent_name"
	// Parent trace / session correlation keys (Solution B/C): a sub-agent run
	// copies these baggage members (carrying the parent's OTel trace ID and
	// parent session ID) from the parent context into its own trace metadata,
	// so a Langfuse trace for a spawned sub-agent can be traced back to the
	// parent conversation that invoked it.
	langfuseMetadataParentTraceID = langfuseMetadataPrefix + "parent_trace_id"
	langfuseMetadataSubagentOf    = langfuseMetadataPrefix + "subagent_of"
	langfuseParentTraceIDBaggage  = "langfuse.trace.metadata.parent_trace_id"
	langfuseParentSessionBaggage  = "langfuse.trace.metadata.subagent_of"

	langfuseMetadataDebugTracePath = langfuseMetadataPrefix + "debug_trace"
	langfuseMetadataCorrelationID  = langfuseMetadataPrefix + "correlation_id"

	langfuseEnvironmentKey = "langfuse.environment"

	langfuseTraceDefaultName = "request"
)

const defaultDebugRecorderDirName = "debug"

var langfuseStart = langfuseobs.Start

type langfuseRuntime struct {
	adminStatus       admin.LangfuseStatus
	runOptionResolver gateway.RunOptionResolver
	shutdown          func(context.Context) error
}

func maybeEnableLangfuse(
	ctx context.Context,
	opts runOptions,
) (*langfuseRuntime, error) {
	status := buildLangfuseAdminStatus(opts)

	// Always build the debug recorder resolver for TraceID propagation.
	debugResolver := buildDebugRecorderRunOptionResolver(opts)

	if !opts.LangfuseEnabled {
		return &langfuseRuntime{
			adminStatus:       status,
			runOptionResolver: debugResolver,
		}, nil
	}

	shutdown, err := langfuseStart(
		ctx,
		langfuseStartOptions(opts)...,
	)
	if err != nil {
		status.Error = err.Error()
		if opts.LangfuseRequired {
			return nil, err
		}
		log.Warnf("openclaw: langfuse disabled: %v", err)
		return &langfuseRuntime{
			adminStatus:       status,
			runOptionResolver: debugResolver,
		}, nil
	}

	status.Ready = true
	langfuseResolver := buildLangfuseRunOptionResolver(opts)
	combined := chainRunOptionResolvers(debugResolver, langfuseResolver)
	return &langfuseRuntime{
		adminStatus:       status,
		runOptionResolver: combined,
		shutdown:          shutdown,
	}, nil
}

func langfuseStartOptions(
	opts runOptions,
) []langfuseobs.Option {
	if opts.LangfuseObservationLeafValueMaxBytes == nil {
		return nil
	}
	return []langfuseobs.Option{
		langfuseobs.WithObservationLeafValueMaxBytes(
			*opts.LangfuseObservationLeafValueMaxBytes,
		),
	}
}

func buildLangfuseAdminStatus(
	opts runOptions,
) admin.LangfuseStatus {
	uiBaseURL := resolvedLangfuseUIBaseURL(opts)
	return admin.LangfuseStatus{
		Enabled:   opts.LangfuseEnabled,
		UIBaseURL: uiBaseURL,
		TraceURLTemplate: resolvedLangfuseTraceURLTemplate(
			opts,
			uiBaseURL,
		),
	}
}

func resolvedLangfuseUIBaseURL(opts runOptions) string {
	if baseURL := strings.TrimSpace(opts.LangfuseUIBaseURL); baseURL != "" {
		return strings.TrimRight(baseURL, "/")
	}

	host := strings.TrimSpace(os.Getenv(langfuseHostEnv))
	if host == "" {
		return ""
	}
	if strings.Contains(host, "://") {
		return strings.TrimRight(host, "/")
	}

	scheme := "https"
	if strings.EqualFold(
		strings.TrimSpace(os.Getenv(langfuseInsecureEnv)),
		"true",
	) {
		scheme = "http"
	}
	return scheme + "://" + host
}

func resolvedLangfuseTraceURLTemplate(
	opts runOptions,
	uiBaseURL string,
) string {
	if template := strings.TrimSpace(
		opts.LangfuseTraceURLTemplate,
	); template != "" {
		return template
	}
	projectID := strings.TrimSpace(os.Getenv(langfuseInitProjectEnv))
	if uiBaseURL == "" || projectID == "" {
		return ""
	}
	return strings.TrimRight(uiBaseURL, "/") +
		"/project/" + projectID + "/traces/" +
		langfuseTraceIDPlaceholder
}

func buildLangfuseRunOptionResolver(
	opts runOptions,
) gateway.RunOptionResolver {
	appName := strings.TrimSpace(opts.AppName)
	debugRoot := filepath.Join(
		strings.TrimSpace(opts.StateDir),
		defaultDebugRecorderDirName,
	)
	return func(
		ctx context.Context,
		input gateway.RunOptionInput,
	) (context.Context, []agent.RunOption, error) {
		correlationID := strings.TrimSpace(input.RequestID)

		ctx = withLangfuseBaggage(ctx, appName, input, debugRoot, correlationID)

		runOpts := make([]agent.RunOption, 0, 2)
		resolvedAppName := runtimeprofile.AppNameFromContext(ctx, appName)
		traceName := buildLangfuseTraceName(resolvedAppName, input)
		// Allow distinguishing conversations & sub-agent calls by agent name in
		// the Langfuse trace list. The resolved runtime profile carries the
		// agent identity (parent conversation agent for gateway runs, the
		// resolved sub-agent profile for spawned sub-agent runs). Append it to
		// the trace name without changing buildLangfuseTraceName's contract.
		if profile, ok := runtimeprofile.ProfileFromContext(ctx); ok {
			if agentName := strings.TrimSpace(profile.AgentName); agentName != "" {
				traceName = fmt.Sprintf("%s [%s]", traceName, agentName)
			}
		}
		if traceName != "" {
			// Propagate langfuse.trace.name via baggage so that child spans
			// exported before the root span still carry the trace name.
			ctx = withLangfuseTraceNameBaggage(ctx, traceName)
			runOpts = append(
				runOpts,
				agent.WithSpanAttributes(
					attribute.String(
						langfuseTraceNameKey,
						traceName,
					),
				),
			)
		}
		return ctx, runOpts, nil
	}
}

// buildDebugRecorderRunOptionResolver returns a RunOptionResolver that
// propagates the OpenTelemetry trace ID back to the debug recorder trace.
// This ensures the debug recorder always carries the OTel trace ID for
// cross-system correlation, regardless of Langfuse enablement status.
func buildDebugRecorderRunOptionResolver(
	opts runOptions,
) gateway.RunOptionResolver {
	return func(
		ctx context.Context,
		input gateway.RunOptionInput,
	) (context.Context, []agent.RunOption, error) {
		if input.Trace == nil {
			return ctx, nil, nil
		}
		traceRef := input.Trace
		return ctx, []agent.RunOption{
			agent.WithTraceStartedCallback(
				func(spanCtx oteltrace.SpanContext) {
					if !spanCtx.IsValid() {
						return
					}
					if err := traceRef.SetTraceID(
						spanCtx.TraceID().String(),
					); err != nil {
						log.Warnf(
							"openclaw: persist trace id failed: %v",
							err,
						)
					}
				},
			),
		}, nil
	}
}

// chainRunOptionResolvers composes multiple RunOptionResolvers into one.
// Each resolver runs in order; the context from the previous resolver is
// passed to the next, and all run options are merged.
func chainRunOptionResolvers(
	resolvers ...gateway.RunOptionResolver,
) gateway.RunOptionResolver {
	active := make([]gateway.RunOptionResolver, 0, len(resolvers))
	for _, r := range resolvers {
		if r != nil {
			active = append(active, r)
		}
	}
	if len(active) == 0 {
		return nil
	}
	if len(active) == 1 {
		return active[0]
	}
	return func(
		ctx context.Context,
		input gateway.RunOptionInput,
	) (context.Context, []agent.RunOption, error) {
		var allOpts []agent.RunOption
		for _, resolver := range active {
			resolvedCtx, opts, err := resolver(ctx, input)
			if err != nil {
				return ctx, allOpts, err
			}
			if resolvedCtx != nil {
				ctx = resolvedCtx
			}
			allOpts = append(allOpts, opts...)
		}
		return ctx, allOpts, nil
	}
}

func withLangfuseBaggage(
	ctx context.Context,
	appName string,
	input gateway.RunOptionInput,
	debugRoot string,
	correlationID string,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	bag := baggage.FromContext(ctx)
	// LANGFUSE_ENVIRONMENT, when set, explicitly overrides the auto-derived
	// environment. Otherwise the environment defaults to the local host /
	// computer name so that traces from different machines are distinguishable
	// in Langfuse via the standard environment field. The value is normalized
	// to Langfuse's environment naming constraint (see
	// normalizeLangfuseEnvironment).
	env := strings.TrimSpace(os.Getenv("LANGFUSE_ENVIRONMENT"))
	if env == "" {
		if host, err := os.Hostname(); err == nil {
			env = host
		}
	}
	if normalized := normalizeLangfuseEnvironment(env); normalized != "" {
		bag = setLangfuseBaggageMember(
			bag,
			langfuseEnvironmentKey,
			normalized,
		)
	}
	bag = setLangfuseBaggageMember(
		bag,
		langfuseUserIDKey,
		input.UserID,
	)
	bag = setLangfuseBaggageMember(
		bag,
		langfuseSessionIDKey,
		input.SessionID,
	)
	bag = setLangfuseBaggageMember(
		bag,
		langfuseMetadataAppName,
		runtimeprofile.AppNameFromContext(ctx, appName),
	)
	if profile, ok := runtimeprofile.ProfileFromContext(ctx); ok {
		bag = setLangfuseBaggageMember(
			bag,
			langfuseMetadataProfileID,
			profile.ID,
		)
		bag = setLangfuseBaggageMember(
			bag,
			langfuseMetadataProfileVersion,
			profile.Version,
		)
		// FR-1/FR-2: surface the readable agent identity in Langfuse metadata so
		// conversations/spawned runs can be filtered by agent name.
		bag = setLangfuseBaggageMember(
			bag,
			langfuseMetadataAgentName,
			profile.AgentName,
		)
	}

	// FR-3/FR-4: a spawned sub-agent run carries the parent OTel trace ID and
	// parent session ID via baggage (propagated through taskrun's
	// mergeObservabilityContext, which forwards baggage but not OTel span
	// context). Surface both on the sub-agent's own trace metadata so the
	// parent->child relationship is visible in Langfuse. Parent-conversation
	// (gateway) runs have no such baggage, leaving these fields empty.
	if parentTraceID := baggageMember(bag, langfuseParentTraceIDBaggage); parentTraceID != "" {
		bag = setLangfuseBaggageMember(
			bag,
			langfuseMetadataParentTraceID,
			parentTraceID,
		)
	}
	if parentSession := baggageMember(bag, langfuseParentSessionBaggage); parentSession != "" {
		bag = setLangfuseBaggageMember(
			bag,
			langfuseMetadataSubagentOf,
			parentSession,
		)
	}
	bag = setLangfuseBaggageMember(
		bag,
		langfuseMetadataChannel,
		input.Inbound.Channel,
	)
	bag = setLangfuseBaggageMember(
		bag,
		langfuseMetadataRequestID,
		input.RequestID,
	)
	bag = setLangfuseBaggageMember(
		bag,
		langfuseMetadataMessageID,
		input.Inbound.MessageID,
	)

	// Debug recorder trace path (Solution A):
	// Store the relative trace directory path in Langfuse metadata so that
	// users can navigate from a Langfuse trace to the corresponding local
	// debug recorder files.
	if input.Trace != nil {
		debugRoot = strings.TrimSpace(debugRoot)
		if debugRoot != "" {
			traceRel, err := filepath.Rel(
				debugRoot,
				input.Trace.Dir(),
			)
			if err == nil {
				bag = setLangfuseBaggageMember(
					bag,
					langfuseMetadataDebugTracePath,
					filepath.ToSlash(traceRel),
				)
			}
		}
	}

	// Correlation ID (Solution B):
	// Inject a unified correlation identifier into Langfuse metadata for
	// cross-system search and traceability.
	if correlationID != "" {
		bag = setLangfuseBaggageMember(
			bag,
			langfuseMetadataCorrelationID,
			correlationID,
		)
	}

	return baggage.ContextWithBaggage(ctx, bag)
}

// withLangfuseTraceNameBaggage adds langfuse.trace.name to the context baggage
// so that the baggageBatchSpanProcessor propagates it to all child spans.
func withLangfuseTraceNameBaggage(
	ctx context.Context,
	traceName string,
) context.Context {
	bag := baggage.FromContext(ctx)
	bag = setLangfuseBaggageMember(bag, langfuseTraceNameKey, traceName)
	return baggage.ContextWithBaggage(ctx, bag)
}

// baggageMember returns the value of the baggage member with the given key,
// trimmed, or "" when absent.
// normalizeLangfuseEnvironment returns a Langfuse-compliant environment value
// derived from the given host/environment string, or "" when nothing usable.
//
// Langfuse requires environment names to match `^(?!langfuse)[a-z0-9-_]+$` with
// a maximum of 40 chars (lowercase letters, digits, '-' and '_', and must not
// start with "langfuse"). Windows computer names typically contain uppercase
// letters and dots, so we lowercase, map invalid chars to '-', drop a reserved
// leading "langfuse", and truncate to 40 chars.
func normalizeLangfuseEnvironment(host string) string {
	in := strings.ToLower(strings.TrimSpace(host))
	if in == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if strings.HasPrefix(out, "langfuse") {
		out = "host-" + out
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

func baggageMember(bag baggage.Baggage, key string) string {
	if len(bag.Members()) == 0 {
		return ""
	}
	return strings.TrimSpace(bag.Member(key).Value())
}

func setLangfuseBaggageMember(
	bag baggage.Baggage,
	key string,
	value string,
) baggage.Baggage {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return bag
	}

	member, err := baggage.NewMemberRaw(key, value)
	if err != nil {
		return bag
	}
	next, err := bag.SetMember(member)
	if err != nil {
		return bag
	}
	return next
}

func buildLangfuseTraceName(
	fallbackAppName string,
	input gateway.RunOptionInput,
) string {
	channel := strings.TrimSpace(input.Inbound.Channel)
	if channel == "" {
		channel = strings.TrimSpace(fallbackAppName)
	}
	if channel == "" {
		channel = appName
	}
	userID := strings.TrimSpace(input.UserID)
	prefix := channel
	if userID != "" {
		prefix = channel + " " + userID
	}
	// Keep the trace name human-readable in the Langfuse trace list:
	// messageID/requestID are random req_<uuid> tokens that add no signal there
	// and are already surfaced as metadata (request_id / correlation_id), so we
	// intentionally omit them from the name.
	return prefix + " " + langfuseTraceDefaultName
}
