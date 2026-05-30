//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package metric provides metrics collection functionality for the trpc-agent-go framework.
// It integrates with OpenTelemetry to provide comprehensive metrics capabilities.
package metric

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/metric/histogram"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
)

// InitMeterProvider initializes the meter provider and default meters.
func InitMeterProvider(mp metric.MeterProvider) error {
	itelemetry.MeterProvider = mp

	itelemetry.ChatMeter = mp.Meter(metrics.MeterNameChat)
	var err error
	if itelemetry.ChatMetricTRPCAgentGoClientRequestCnt, err = itelemetry.ChatMeter.Int64Counter(
		metrics.MetricTRPCAgentGoClientRequestCnt,
		metric.WithDescription("Total number of client requests"),
		metric.WithUnit("1"),
	); err != nil {
		return fmt.Errorf("failed to create chat metric TRPCAgentGoClientRequestCnt: %w", err)
	}
	if itelemetry.ChatMetricGenAIClientTokenUsage, err = histogram.NewDynamicInt64Histogram(
		mp,
		metrics.MeterNameChat,
		metrics.MetricGenAIClientTokenUsage,
		metric.WithDescription("Token usage for client"),
		metric.WithUnit("{token}"),
	); err != nil {
		return fmt.Errorf("failed to create chat metric GenAIClientTokenUsage: %w", err)
	}
	if itelemetry.ChatMetricGenAIClientOperationDuration, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metrics.MeterNameChat,
		metrics.MetricGenAIClientOperationDuration,
		metric.WithDescription("Duration of client operation"),
		metric.WithUnit("s"),
	); err != nil {
		return fmt.Errorf("failed to create chat metric GenAIClientOperationDuration: %w", err)
	}
	if itelemetry.ChatMetricGenAIServerTimeToFirstToken, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metrics.MeterNameChat,
		metrics.MetricGenAIServerTimeToFirstToken,
		metric.WithDescription("Time to first token for server"),
		metric.WithUnit("s"),
	); err != nil {
		return fmt.Errorf("failed to create chat metric GenAIServerTimeToFirstToken: %w", err)
	}
	if itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metrics.MeterNameChat,
		metrics.MetricTRPCAgentGoClientTimeToFirstToken,
		metric.WithDescription("Time to first token (legacy metric name)"),
		metric.WithUnit("s"),
	); err != nil {
		return fmt.Errorf("failed to create chat metric TRPCAgentGoClientTimeToFirstToken: %w", err)
	}
	if itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metrics.MeterNameChat,
		metrics.MetricTRPCAgentGoClientTimePerOutputToken,
		metric.WithDescription("Time per output token for client"),
		metric.WithUnit("s"),
	); err != nil {
		return fmt.Errorf("failed to create chat metric TRPCAgentGoClientTimePerOutputToken: %w", err)
	}
	if itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metrics.MeterNameChat,
		metrics.MetricTRPCAgentGoClientOutputTokenPerTime,
		metric.WithDescription("Output token per time for client"),
		metric.WithUnit("{token}"),
	); err != nil {
		return fmt.Errorf("failed to create chat metric TRPCAgentGoClientOutputTokenPerTime: %w", err)
	}

	itelemetry.ExecuteToolMeter = mp.Meter(metrics.MeterNameExecuteTool)
	if itelemetry.ExecuteToolMetricTRPCAgentGoClientRequestCnt, err = itelemetry.ExecuteToolMeter.Int64Counter(
		metrics.MetricTRPCAgentGoClientRequestCnt,
		metric.WithDescription("Total number of client requests"),
		metric.WithUnit("1"),
	); err != nil {
		return fmt.Errorf("failed to create execute tool metric TRPCAgentGoClientRequestCnt: %w", err)
	}
	if itelemetry.ExecuteToolMetricGenAIClientOperationDuration, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metrics.MeterNameExecuteTool,
		metrics.MetricGenAIClientOperationDuration,
		metric.WithDescription("Duration of client operation"),
		metric.WithUnit("s"),
	); err != nil {
		return fmt.Errorf("failed to create execute tool metric GenAIClientOperationDuration: %w", err)
	}

	if err := initInvokeAgentMetrics(mp); err != nil {
		return err
	}
	if err := initWorkflowMetrics(mp); err != nil {
		return err
	}
	if err := initContextMetrics(mp); err != nil {
		return err
	}

	return nil
}

// GetMeterProvider returns the meter provider.
func GetMeterProvider() metric.MeterProvider {
	return itelemetry.MeterProvider
}

// SetHistogramBuckets updates bucket boundaries for a specific histogram metric.
// The metricName should be one of the defined metric names in the metrics package.
// Note: This creates a new histogram instrument; old data is not migrated.
func SetHistogramBuckets(meterName string, metricName string, boundaries []float64) error {
	switch meterName {
	case metrics.MeterNameChat:
		return setChatHistogramBuckets(metricName, boundaries)
	case metrics.MeterNameExecuteTool:
		return setExecuteToolHistogramBuckets(metricName, boundaries)
	case metrics.MeterNameInvokeAgent:
		return setInvokeAgentHistogramBuckets(metricName, boundaries)
	case metrics.MeterNameWorkflow:
		return setWorkflowHistogramBuckets(metricName, boundaries)
	case metrics.MeterNameContext:
		return setContextHistogramBuckets(metricName, boundaries)
	default:
		return fmt.Errorf("unknown or unsupported meter name: %s", meterName)
	}
}

func setChatHistogramBuckets(metricName string, boundaries []float64) error {
	switch metricName {
	case metrics.MetricGenAIClientOperationDuration:
		if itelemetry.ChatMetricGenAIClientOperationDuration == nil {
			return fmt.Errorf("chat metric %s not initialized", metricName)
		}
		return itelemetry.ChatMetricGenAIClientOperationDuration.SetBuckets(boundaries)
	case metrics.MetricGenAIClientTokenUsage:
		if itelemetry.ChatMetricGenAIClientTokenUsage == nil {
			return fmt.Errorf("chat metric %s not initialized", metricName)
		}
		return itelemetry.ChatMetricGenAIClientTokenUsage.SetBuckets(boundaries)
	case metrics.MetricGenAIServerTimeToFirstToken:
		if itelemetry.ChatMetricGenAIServerTimeToFirstToken == nil {
			return fmt.Errorf("chat metric %s not initialized", metricName)
		}
		return itelemetry.ChatMetricGenAIServerTimeToFirstToken.SetBuckets(boundaries)
	case metrics.MetricTRPCAgentGoClientTimeToFirstToken:
		if itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken == nil {
			return fmt.Errorf("chat metric %s not initialized", metricName)
		}
		return itelemetry.ChatMetricTRPCAgentGoClientTimeToFirstToken.SetBuckets(boundaries)
	case metrics.MetricTRPCAgentGoClientTimePerOutputToken:
		if itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken == nil {
			return fmt.Errorf("chat metric %s not initialized", metricName)
		}
		return itelemetry.ChatMetricTRPCAgentGoClientTimePerOutputToken.SetBuckets(boundaries)
	case metrics.MetricTRPCAgentGoClientOutputTokenPerTime:
		if itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime == nil {
			return fmt.Errorf("chat metric %s not initialized", metricName)
		}
		return itelemetry.ChatMetricTRPCAgentGoClientOutputTokenPerTime.SetBuckets(boundaries)
	default:
		return fmt.Errorf("unknown or unsupported chat histogram metric: %s", metricName)
	}
}

func setExecuteToolHistogramBuckets(metricName string, boundaries []float64) error {
	switch metricName {
	case metrics.MetricGenAIClientOperationDuration:
		if itelemetry.ExecuteToolMetricGenAIClientOperationDuration == nil {
			return fmt.Errorf("execute tool metric %s not initialized", metricName)
		}
		return itelemetry.ExecuteToolMetricGenAIClientOperationDuration.SetBuckets(boundaries)
	default:
		return fmt.Errorf("unknown or unsupported execute tool histogram metric: %s", metricName)
	}
}

func setInvokeAgentHistogramBuckets(metricName string, boundaries []float64) error {
	switch metricName {
	case metrics.MetricTRPCAgentGoClientTimeToFirstToken:
		if itelemetry.InvokeAgentMetricGenAIClientTimeToFirstToken == nil {
			return fmt.Errorf("invoke agent metric %s not initialized", metricName)
		}
		return itelemetry.InvokeAgentMetricGenAIClientTimeToFirstToken.SetBuckets(boundaries)
	case metrics.MetricGenAIClientTokenUsage:
		if itelemetry.InvokeAgentMetricGenAIClientTokenUsage == nil {
			return fmt.Errorf("invoke agent metric %s not initialized", metricName)
		}
		return itelemetry.InvokeAgentMetricGenAIClientTokenUsage.SetBuckets(boundaries)
	case metrics.MetricGenAIClientOperationDuration:
		if itelemetry.InvokeAgentMetricGenAIClientOperationDuration == nil {
			return fmt.Errorf("invoke agent metric %s not initialized", metricName)
		}
		return itelemetry.InvokeAgentMetricGenAIClientOperationDuration.SetBuckets(boundaries)
	default:
		return fmt.Errorf("unknown or unsupported invoke agent histogram metric: %s", metricName)
	}
}

func setWorkflowHistogramBuckets(metricName string, boundaries []float64) error {
	switch metricName {
	case metrics.MetricGenAIClientOperationDuration:
		if itelemetry.WorkflowMetricGenAIClientOperationDuration == nil {
			return fmt.Errorf("workflow metric %s not initialized", metricName)
		}
		return itelemetry.WorkflowMetricGenAIClientOperationDuration.SetBuckets(boundaries)
	case metrics.MetricGenAIWorkflowElapsedTime:
		if itelemetry.WorkflowMetricGenAIWorkflowElapsedTime == nil {
			return fmt.Errorf("workflow metric %s not initialized", metricName)
		}
		return itelemetry.WorkflowMetricGenAIWorkflowElapsedTime.SetBuckets(boundaries)
	default:
		return fmt.Errorf("unknown or unsupported workflow histogram metric: %s", metricName)
	}
}

func initInvokeAgentMetrics(mp metric.MeterProvider) error {
	if mp == nil {
		return fmt.Errorf("invoke agent meter provider is nil")
	}

	itelemetry.InvokeAgentMeter = mp.Meter(metrics.MeterNameInvokeAgent)
	meterName := metrics.MeterNameInvokeAgent
	var err error
	if itelemetry.InvokeAgentMetricGenAIRequestCnt, err = itelemetry.InvokeAgentMeter.Int64Counter(
		metrics.MetricTRPCAgentGoClientRequestCnt,
		metric.WithDescription("Total number of gen ai requests"),
		metric.WithUnit("1"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricTRPCAgentGoClientRequestCnt, err)
	}
	if itelemetry.InvokeAgentMetricGenAIClientTokenUsage, err = histogram.NewDynamicInt64Histogram(
		mp,
		metrics.MeterNameInvokeAgent,
		metrics.MetricGenAIClientTokenUsage,
		metric.WithDescription("Input tokens usage"),
		metric.WithUnit("{token}"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricGenAIClientTokenUsage, err)
	}
	if itelemetry.InvokeAgentMetricGenAIClientTimeToFirstToken, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metrics.MeterNameInvokeAgent,
		metrics.MetricTRPCAgentGoClientTimeToFirstToken,
		metric.WithDescription("Time to first token for client"),
		metric.WithUnit("s"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricTRPCAgentGoClientTimeToFirstToken, err)
	}
	if itelemetry.InvokeAgentMetricGenAIClientOperationDuration, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metrics.MeterNameInvokeAgent,
		metrics.MetricGenAIClientOperationDuration,
		metric.WithDescription("Duration of client operation"),
		metric.WithUnit("s"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricGenAIClientOperationDuration, err)
	}

	return nil
}

func initWorkflowMetrics(mp metric.MeterProvider) error {
	if mp == nil {
		return fmt.Errorf("workflow meter provider is nil")
	}

	itelemetry.WorkflowMeter = mp.Meter(metrics.MeterNameWorkflow)
	meterName := metrics.MeterNameWorkflow
	var err error
	if itelemetry.WorkflowMetricGenAIClientOperationDuration, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metrics.MeterNameWorkflow,
		metrics.MetricGenAIClientOperationDuration,
		metric.WithDescription("Duration of graph workflow/node execution"),
		metric.WithUnit("s"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricGenAIClientOperationDuration, err)
	}
	if itelemetry.WorkflowMetricGenAIWorkflowElapsedTime, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metrics.MeterNameWorkflow,
		metrics.MetricGenAIWorkflowElapsedTime,
		metric.WithDescription("Elapsed time from root workflow start to current workflow end"),
		metric.WithUnit("s"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricGenAIWorkflowElapsedTime, err)
	}

	return nil
}

// NewMeterProvider creates a new meter provider with optional configuration.
// The environment variables described below can be used for Endpoint configuration.
// OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_METRICS_ENDPOINT (default: "https://localhost:4317")
// https://pkg.go.dev/go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc
func NewMeterProvider(ctx context.Context, opts ...Option) (*sdkmetric.MeterProvider, error) {
	// Set default options
	options := &options{
		serviceName:      itelemetry.ServiceName,
		serviceVersion:   itelemetry.ServiceVersion,
		serviceNamespace: itelemetry.ServiceNamespace,
		protocol:         itelemetry.ProtocolGRPC, // Default to gRPC
	}
	for _, opt := range opts {
		opt(options)
	}

	// Set endpoint based on protocol if not explicitly set
	if options.metricsEndpoint == "" {
		options.metricsEndpoint = metricsEndpoint(options.protocol)
	}

	res, err := buildResource(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	var meterProvider *sdkmetric.MeterProvider
	switch options.protocol {
	case itelemetry.ProtocolHTTP:
		meterProvider, err = newHTTPMeterProvider(ctx, res, options.metricsEndpoint)
	default:
		meterProvider, err = newGRPCMeterProvider(ctx, res, options.metricsEndpoint)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to initialize meter provider: %w", err)
	}

	return meterProvider, nil
}

func metricsEndpoint(protocol string) string {
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"); endpoint != "" {
		return endpoint
	}
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		return endpoint
	}

	// Return different default endpoints based on protocol
	switch protocol {
	case itelemetry.ProtocolHTTP:
		return "localhost:4318" // HTTP endpoint base URL (otlpmetrichttp will add /v1/metrics automatically)
	default:
		return "localhost:4317" // gRPC endpoint (host:port)
	}
}

// Initializes an OTLP HTTP exporter, and configures the corresponding meter provider.
func newHTTPMeterProvider(ctx context.Context, res *resource.Resource, endpoint string) (*sdkmetric.MeterProvider, error) {
	metricExporter, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpoint(endpoint),
		otlpmetrichttp.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP metrics exporter: %w", err)
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithResource(res),
	)

	return meterProvider, nil
}

// Initializes an OTLP gRPC exporter, and configures the corresponding meter provider.
func newGRPCMeterProvider(ctx context.Context, res *resource.Resource, endpoint string) (*sdkmetric.MeterProvider, error) {
	metricsConn, err := itelemetry.NewGRPCConn(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics connection: %w", err)
	}

	metricExporter, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithGRPCConn(metricsConn))
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics exporter: %w", err)
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithResource(res),
	)

	return meterProvider, nil
}

// Option is a function that configures meter options.
type Option func(*options)

// options holds the configuration options for meter.
type options struct {
	metricsEndpoint    string
	serviceName        string
	serviceVersion     string
	serviceNamespace   string
	protocol           string // Protocol to use (grpc or http)
	resourceAttributes *[]attribute.KeyValue
}

// WithEndpoint sets the metrics endpoint(host and port) the Exporter will connect to.
// The provided endpoint should resemble "example.com:4317" (no scheme or path).
// If the OTEL_EXPORTER_OTLP_ENDPOINT or OTEL_EXPORTER_OTLP_METRICS_ENDPOINT environment variable is set,
// and this option is not passed, that variable value will be used.
// If both environment variables are set, OTEL_EXPORTER_OTLP_METRICS_ENDPOINT will take precedence.
// If an environment variable is set, and this option is passed, this option will take precedence.
func WithEndpoint(endpoint string) Option {
	return func(opts *options) {
		opts.metricsEndpoint = endpoint
	}
}

// WithProtocol sets the protocol to use for metrics export.
// Supported protocols are "grpc" (default) and "http".
func WithProtocol(protocol string) Option {
	return func(opts *options) {
		opts.protocol = protocol
	}
}

// WithServiceName overrides the service.name resource attribute.
func WithServiceName(serviceName string) Option {
	return func(opts *options) {
		opts.serviceName = serviceName
	}
}

// WithServiceNamespace overrides the service.namespace resource attribute.
func WithServiceNamespace(serviceNamespace string) Option {
	return func(opts *options) {
		opts.serviceNamespace = serviceNamespace
	}
}

// WithServiceVersion overrides the service.version resource attribute.
func WithServiceVersion(serviceVersion string) Option {
	return func(opts *options) {
		opts.serviceVersion = serviceVersion
	}
}

// WithResourceAttributes appends custom resource attributes.
func WithResourceAttributes(attrs ...attribute.KeyValue) Option {
	return func(opts *options) {
		if len(attrs) == 0 {
			return
		}
		if opts.resourceAttributes == nil {
			opts.resourceAttributes = &[]attribute.KeyValue{}
		}
		*opts.resourceAttributes = append(*opts.resourceAttributes, attrs...)
	}
}

func buildResource(ctx context.Context, options *options) (*resource.Resource, error) {
	// Build resource with options values
	resourceOpts := []resource.Option{
		resource.WithAttributes(
			semconv.ServiceNamespace(options.serviceNamespace),
			semconv.ServiceName(options.serviceName),
			semconv.ServiceVersion(options.serviceVersion),
		),
		resource.WithFromEnv(),
		resource.WithHost(),         // Adds host.name
		resource.WithTelemetrySDK(), // Adds telemetry.sdk.{name,language,version}
	}

	// Append custom resource attributes
	if options.resourceAttributes != nil && len(*options.resourceAttributes) > 0 {
		resourceOpts = append(resourceOpts, resource.WithAttributes(*options.resourceAttributes...))
	}

	return resource.New(ctx, resourceOpts...)
}

func initContextMetrics(mp metric.MeterProvider) error {
	if mp == nil {
		return fmt.Errorf("context meter provider is nil")
	}

	itelemetry.ContextMeter = mp.Meter(metrics.MeterNameContext)
	meterName := metrics.MeterNameContext
	var err error

	if itelemetry.ContextMetricInputTokens, err = histogram.NewDynamicInt64Histogram(
		mp,
		meterName,
		metrics.MetricContextInputTokens,
		metric.WithDescription("Number of input tokens sent to the LLM after all context controls"),
		metric.WithUnit("{token}"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextInputTokens, err)
	}
	if itelemetry.ContextMetricWindowSize, err = histogram.NewDynamicInt64Histogram(
		mp,
		meterName,
		metrics.MetricContextWindowSize,
		metric.WithDescription("Model context window size in tokens"),
		metric.WithUnit("{token}"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextWindowSize, err)
	}
	if itelemetry.ContextMetricUsageRatio, err = histogram.NewDynamicFloat64Histogram(
		mp,
		meterName,
		metrics.MetricContextUsageRatio,
		metric.WithDescription("Context window usage ratio (input_tokens / window_size)"),
		metric.WithUnit("1"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextUsageRatio, err)
	}
	if itelemetry.ContextMetricInitialTokens, err = histogram.NewDynamicInt64Histogram(
		mp,
		meterName,
		metrics.MetricContextInitialTokens,
		metric.WithDescription("Estimated token count after preprocess before context controls"),
		metric.WithUnit("{token}"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextInitialTokens, err)
	}
	if itelemetry.ContextMetricInitialMessageCount, err = histogram.NewDynamicInt64Histogram(
		mp,
		meterName,
		metrics.MetricContextInitialMessageCount,
		metric.WithDescription("Number of messages after preprocess before context controls"),
		metric.WithUnit("{message}"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextInitialMessageCount, err)
	}
	if itelemetry.ContextMetricTailoredTokens, err = histogram.NewDynamicInt64Histogram(
		mp,
		meterName,
		metrics.MetricContextTailoredTokens,
		metric.WithDescription("Number of tokens removed by token tailoring"),
		metric.WithUnit("{token}"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextTailoredTokens, err)
	}
	if itelemetry.ContextMetricTailoredMessages, err = histogram.NewDynamicInt64Histogram(
		mp,
		meterName,
		metrics.MetricContextTailoredMessages,
		metric.WithDescription("Number of messages removed by token tailoring"),
		metric.WithUnit("{message}"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextTailoredMessages, err)
	}
	if itelemetry.ContextMetricCompactedTokens, err = histogram.NewDynamicInt64Histogram(
		mp,
		meterName,
		metrics.MetricContextCompactedTokens,
		metric.WithDescription("Number of tokens saved by context compaction"),
		metric.WithUnit("{token}"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextCompactedTokens, err)
	}
	if itelemetry.ContextMetricMessageCount, err = histogram.NewDynamicInt64Histogram(
		mp,
		meterName,
		metrics.MetricContextMessageCount,
		metric.WithDescription("Number of messages sent to the LLM after all context controls"),
		metric.WithUnit("{message}"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextMessageCount, err)
	}

	if itelemetry.ContextMetricCompletionTokens, err = histogram.NewDynamicInt64Histogram(
		mp,
		meterName,
		metrics.MetricContextCompletionTokens,
		metric.WithDescription("Number of completion tokens in the LLM response"),
		metric.WithUnit("{token}"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextCompletionTokens, err)
	}
	if itelemetry.ContextMetricTotalTokens, err = histogram.NewDynamicInt64Histogram(
		mp,
		meterName,
		metrics.MetricContextTotalTokens,
		metric.WithDescription("Total tokens in the LLM response"),
		metric.WithUnit("{token}"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextTotalTokens, err)
	}
	if itelemetry.ContextMetricCachedTokens, err = histogram.NewDynamicInt64Histogram(
		mp,
		meterName,
		metrics.MetricContextCachedTokens,
		metric.WithDescription("Number of cached tokens in the LLM request"),
		metric.WithUnit("{token}"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextCachedTokens, err)
	}
	if itelemetry.ContextMetricReasoningTokens, err = histogram.NewDynamicInt64Histogram(
		mp,
		meterName,
		metrics.MetricContextReasoningTokens,
		metric.WithDescription("Number of reasoning tokens in the LLM response"),
		metric.WithUnit("{token}"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextReasoningTokens, err)
	}
	if itelemetry.ContextMetricToolDefinitionTokens, err = histogram.NewDynamicInt64Histogram(
		mp,
		meterName,
		metrics.MetricContextToolDefinitionTokens,
		metric.WithDescription("Estimated token count of tool/function definitions"),
		metric.WithUnit("{token}"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextToolDefinitionTokens, err)
	}
	if itelemetry.ContextMetricUsageRatioByInitial, err = histogram.NewDynamicFloat64Histogram(
		mp,
		meterName,
		metrics.MetricContextUsageRatioByInitial,
		metric.WithDescription("Context window usage ratio (initial_tokens / window_size)"),
		metric.WithUnit("1"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextUsageRatioByInitial, err)
	}

	if itelemetry.ContextMetricCompactionTrigger, err = itelemetry.ContextMeter.Int64Counter(
		metrics.MetricContextCompactionTrigger,
		metric.WithDescription("Number of times context compaction was triggered"),
		metric.WithUnit("1"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextCompactionTrigger, err)
	}
	if itelemetry.ContextMetricTailoringTrigger, err = itelemetry.ContextMeter.Int64Counter(
		metrics.MetricContextTailoringTrigger,
		metric.WithDescription("Number of times token tailoring was triggered"),
		metric.WithUnit("1"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextTailoringTrigger, err)
	}
	if itelemetry.ContextMetricSummaryTrigger, err = itelemetry.ContextMeter.Int64Counter(
		metrics.MetricContextSummaryTrigger,
		metric.WithDescription("Number of times session summary was triggered"),
		metric.WithUnit("1"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextSummaryTrigger, err)
	}
	if itelemetry.ContextMetricToolCompactionTrigger, err = itelemetry.ContextMeter.Int64Counter(
		metrics.MetricContextToolCompactionTrigger,
		metric.WithDescription("Number of times tool result compaction was triggered"),
		metric.WithUnit("1"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextToolCompactionTrigger, err)
	}
	if itelemetry.ContextMetricOversizedTruncationTrigger, err = itelemetry.ContextMeter.Int64Counter(
		metrics.MetricContextOversizedTruncationTrigger,
		metric.WithDescription("Number of times oversized tool result truncation was triggered"),
		metric.WithUnit("1"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextOversizedTruncationTrigger, err)
	}
	if itelemetry.ContextMetricHistoryTrimTrigger, err = itelemetry.ContextMeter.Int64Counter(
		metrics.MetricContextHistoryTrimTrigger,
		metric.WithDescription("Number of times history trim was triggered"),
		metric.WithUnit("1"),
	); err != nil {
		return fmt.Errorf("failed to create %s metric %s: %w", meterName, metrics.MetricContextHistoryTrimTrigger, err)
	}

	return nil
}

func setContextHistogramBuckets(metricName string, boundaries []float64) error {
	switch metricName {
	case metrics.MetricContextInputTokens:
		if itelemetry.ContextMetricInputTokens == nil {
			return fmt.Errorf("context metric %s not initialized", metricName)
		}
		return itelemetry.ContextMetricInputTokens.SetBuckets(boundaries)
	case metrics.MetricContextWindowSize:
		if itelemetry.ContextMetricWindowSize == nil {
			return fmt.Errorf("context metric %s not initialized", metricName)
		}
		return itelemetry.ContextMetricWindowSize.SetBuckets(boundaries)
	case metrics.MetricContextUsageRatio:
		if itelemetry.ContextMetricUsageRatio == nil {
			return fmt.Errorf("context metric %s not initialized", metricName)
		}
		return itelemetry.ContextMetricUsageRatio.SetBuckets(boundaries)
	case metrics.MetricContextInitialTokens:
		if itelemetry.ContextMetricInitialTokens == nil {
			return fmt.Errorf("context metric %s not initialized", metricName)
		}
		return itelemetry.ContextMetricInitialTokens.SetBuckets(boundaries)
	case metrics.MetricContextInitialMessageCount:
		if itelemetry.ContextMetricInitialMessageCount == nil {
			return fmt.Errorf("context metric %s not initialized", metricName)
		}
		return itelemetry.ContextMetricInitialMessageCount.SetBuckets(boundaries)
	case metrics.MetricContextTailoredTokens:
		if itelemetry.ContextMetricTailoredTokens == nil {
			return fmt.Errorf("context metric %s not initialized", metricName)
		}
		return itelemetry.ContextMetricTailoredTokens.SetBuckets(boundaries)
	case metrics.MetricContextTailoredMessages:
		if itelemetry.ContextMetricTailoredMessages == nil {
			return fmt.Errorf("context metric %s not initialized", metricName)
		}
		return itelemetry.ContextMetricTailoredMessages.SetBuckets(boundaries)
	case metrics.MetricContextCompactedTokens:
		if itelemetry.ContextMetricCompactedTokens == nil {
			return fmt.Errorf("context metric %s not initialized", metricName)
		}
		return itelemetry.ContextMetricCompactedTokens.SetBuckets(boundaries)
	case metrics.MetricContextMessageCount:
		if itelemetry.ContextMetricMessageCount == nil {
			return fmt.Errorf("context metric %s not initialized", metricName)
		}
		return itelemetry.ContextMetricMessageCount.SetBuckets(boundaries)
	case metrics.MetricContextCompletionTokens:
		if itelemetry.ContextMetricCompletionTokens == nil {
			return fmt.Errorf("context metric %s not initialized", metricName)
		}
		return itelemetry.ContextMetricCompletionTokens.SetBuckets(boundaries)
	case metrics.MetricContextTotalTokens:
		if itelemetry.ContextMetricTotalTokens == nil {
			return fmt.Errorf("context metric %s not initialized", metricName)
		}
		return itelemetry.ContextMetricTotalTokens.SetBuckets(boundaries)
	case metrics.MetricContextCachedTokens:
		if itelemetry.ContextMetricCachedTokens == nil {
			return fmt.Errorf("context metric %s not initialized", metricName)
		}
		return itelemetry.ContextMetricCachedTokens.SetBuckets(boundaries)
	case metrics.MetricContextReasoningTokens:
		if itelemetry.ContextMetricReasoningTokens == nil {
			return fmt.Errorf("context metric %s not initialized", metricName)
		}
		return itelemetry.ContextMetricReasoningTokens.SetBuckets(boundaries)
	case metrics.MetricContextToolDefinitionTokens:
		if itelemetry.ContextMetricToolDefinitionTokens == nil {
			return fmt.Errorf("context metric %s not initialized", metricName)
		}
		return itelemetry.ContextMetricToolDefinitionTokens.SetBuckets(boundaries)
	case metrics.MetricContextUsageRatioByInitial:
		if itelemetry.ContextMetricUsageRatioByInitial == nil {
			return fmt.Errorf("context metric %s not initialized", metricName)
		}
		return itelemetry.ContextMetricUsageRatioByInitial.SetBuckets(boundaries)
	default:
		return fmt.Errorf("unknown or unsupported context histogram metric: %s", metricName)
	}
}
