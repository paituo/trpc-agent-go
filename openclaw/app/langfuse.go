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

	langfuseMetadataDebugTracePath = langfuseMetadataPrefix + "debug_trace"
	langfuseMetadataCorrelationID  = langfuseMetadataPrefix + "correlation_id"

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
	if messageID := strings.TrimSpace(
		input.Inbound.MessageID,
	); messageID != "" {
		return prefix + " " + messageID
	}
	if requestID := strings.TrimSpace(input.RequestID); requestID != "" {
		return prefix + " " + requestID
	}
	return prefix + " " + langfuseTraceDefaultName
}
