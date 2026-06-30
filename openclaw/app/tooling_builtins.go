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
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo"
	"trpc.group/trpc-go/trpc-agent-go/tool/file"
	"trpc.group/trpc-go/trpc-agent-go/tool/luaexec"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcp"

	arxivsearch "trpc.group/trpc-go/trpc-agent-go/tool/arxivsearch"
	email "trpc.group/trpc-go/trpc-agent-go/tool/email"
	googlesearch "trpc.group/trpc-go/trpc-agent-go/tool/google/search"
	openapitool "trpc.group/trpc-go/trpc-agent-go/tool/openapi"
	httpfetch "trpc.group/trpc-go/trpc-agent-go/tool/webfetch/httpfetch"
	"trpc.group/trpc-go/trpc-agent-go/tool/wikipedia"

	ocbrowser "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/browser"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/imageinspect"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

const (
	toolProviderBrowser    = "browser"
	toolProviderDuckDuckGo = "duckduckgo"
	toolProviderImage      = "image_inspect"
	toolProviderWebFetch   = "webfetch_http"

	toolSetProviderMCP       = "mcp"
	toolSetProviderFile      = "file"
	toolSetProviderOpenAPI   = "openapi"
	toolSetProviderGoogle    = "google"
	toolSetProviderWiki      = "wikipedia"
	toolSetProviderArxiv     = "arxivsearch"
	toolSetProviderEmail     = "email"
	toolSetProviderLua       = "lua"
	toolSetProviderAgentTool = "agent_tool"

	defaultHTTPTimeout = 30 * time.Second

	envGoogleAPIKey   = "GOOGLE_API_KEY"
	envGoogleEngineID = "GOOGLE_SEARCH_ENGINE_ID"

	mcpTransportStdio      = "stdio"
	mcpTransportSSE        = "sse"
	mcpTransportStreamable = "streamable"

	browserArtifactDirName = ".playwright-mcp"
)

func init() {
	must(registry.RegisterToolProvider(
		toolProviderBrowser,
		newBrowserTools,
	))
	must(registry.RegisterToolProvider(
		toolProviderDuckDuckGo,
		newDuckDuckGoTools,
	))
	must(registry.RegisterToolProvider(
		toolProviderImage,
		newImageInspectTools,
	))
	must(registry.RegisterToolProvider(
		toolProviderWebFetch,
		newHTTPWebFetchTools,
	))

	must(registry.RegisterToolSetProvider(
		toolSetProviderMCP,
		newMCPToolSet,
	))
	must(registry.RegisterToolSetProvider(
		toolSetProviderFile,
		newFileToolSet,
	))
	must(registry.RegisterToolSetProvider(
		toolSetProviderOpenAPI,
		newOpenAPIToolSet,
	))
	must(registry.RegisterToolSetProvider(
		toolSetProviderGoogle,
		newGoogleToolSet,
	))
	must(registry.RegisterToolSetProvider(
		toolSetProviderWiki,
		newWikipediaToolSet,
	))
	must(registry.RegisterToolSetProvider(
		toolSetProviderArxiv,
		newArxivToolSet,
	))
	must(registry.RegisterToolSetProvider(
		toolSetProviderEmail,
		newEmailToolSet,
	))
	must(registry.RegisterToolProvider(
		toolSetProviderLua,
		newLuaExecToolProvider,
	))
	must(registry.RegisterToolSetProvider(
		toolSetProviderAgentTool,
		newAgentToolSet,
	))
}

type httpToolConfig struct {
	BaseURL   string        `yaml:"base_url,omitempty"`
	Backend   string        `yaml:"backend,omitempty"`
	UserAgent string        `yaml:"user_agent,omitempty"`
	Timeout   time.Duration `yaml:"timeout,omitempty"`
}

type duckDuckGoToolConfig struct {
	httpToolConfig `yaml:",inline"`

	BlockedResultURLPatterns []string `yaml:"blocked_result_url_patterns,omitempty"`
}

func newBrowserTools(
	_ registry.ToolProviderDeps,
	spec registry.PluginSpec,
) ([]tool.Tool, error) {
	var cfg ocbrowser.Config
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	browserTool, err := ocbrowser.NewTool(cfg)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{browserTool}, nil
}

func newDuckDuckGoTools(
	_ registry.ToolProviderDeps,
	spec registry.PluginSpec,
) ([]tool.Tool, error) {
	var cfg duckDuckGoToolConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	opts := make([]duckduckgo.Option, 0, 5)
	if backend := strings.TrimSpace(cfg.Backend); backend != "" {
		if !isSupportedDuckDuckGoBackend(backend) {
			return nil, fmt.Errorf(
				"duckduckgo backend must be api, html, or lite: %q",
				backend,
			)
		}
		opts = append(opts, duckduckgo.WithBackend(backend))
	}
	if baseURL := strings.TrimSpace(cfg.BaseURL); baseURL != "" {
		opts = append(opts, duckduckgo.WithBaseURL(baseURL))
	}
	if ua := strings.TrimSpace(cfg.UserAgent); ua != "" {
		opts = append(opts, duckduckgo.WithUserAgent(ua))
	}
	if cfg.Timeout > 0 {
		opts = append(opts, duckduckgo.WithTimeout(cfg.Timeout))
	}
	if len(cfg.BlockedResultURLPatterns) > 0 {
		opts = append(
			opts,
			duckduckgo.WithBlockedResultURLPatterns(
				cfg.BlockedResultURLPatterns...,
			),
		)
	}

	return []tool.Tool{duckduckgo.NewTool(opts...)}, nil
}

func newImageInspectTools(
	_ registry.ToolProviderDeps,
	spec registry.PluginSpec,
) ([]tool.Tool, error) {
	var cfg imageinspect.Config
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	imageTool, err := imageinspect.NewTool(cfg)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{imageTool}, nil
}

func isSupportedDuckDuckGoBackend(backend string) bool {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "api", "html", "lite":
		return true
	default:
		return false
	}
}

type httpWebFetchConfig struct {
	AllowedDomains     []string      `yaml:"allowed_domains,omitempty"`
	BlockedDomains     []string      `yaml:"blocked_domains,omitempty"`
	AllowAll           bool          `yaml:"allow_all_domains,omitempty"`
	Timeout            time.Duration `yaml:"timeout,omitempty"`
	MainContentOnly    bool          `yaml:"main_content_only,omitempty"`
	AllowSearchPages   *bool         `yaml:"allow_search_result_pages,omitempty"`
	DetectBlockedPages *bool         `yaml:"detect_blocked_pages,omitempty"`

	MaxContentLength      int `yaml:"max_content_length,omitempty"`
	MaxTotalContentLength int `yaml:"max_total_content_length,omitempty"`
}

func newHTTPWebFetchTools(
	_ registry.ToolProviderDeps,
	spec registry.PluginSpec,
) ([]tool.Tool, error) {
	var cfg httpWebFetchConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	if !cfg.AllowAll && len(cfg.AllowedDomains) == 0 {
		return nil, errors.New(
			"webfetch_http requires allowed_domains or allow_all_domains",
		)
	}

	client := &http.Client{Timeout: defaultHTTPTimeout}
	if cfg.Timeout > 0 {
		client.Timeout = cfg.Timeout
	}

	opts := make([]httpfetch.Option, 0, 6)
	opts = append(opts, httpfetch.WithHTTPClient(client))
	if cfg.MaxContentLength > 0 {
		opts = append(
			opts,
			httpfetch.WithMaxContentLength(cfg.MaxContentLength),
		)
	}
	if cfg.MaxTotalContentLength > 0 {
		opts = append(
			opts,
			httpfetch.WithMaxTotalContentLength(
				cfg.MaxTotalContentLength,
			),
		)
	}
	if cfg.MainContentOnly {
		opts = append(opts, httpfetch.WithMainContentExtraction(true))
	}
	if cfg.AllowSearchPages == nil || !*cfg.AllowSearchPages {
		opts = append(opts, httpfetch.WithSearchResultPageBlocking(true))
	}
	if cfg.DetectBlockedPages == nil || *cfg.DetectBlockedPages {
		opts = append(opts, httpfetch.WithBlockedPageDetection(true))
	}
	if len(cfg.AllowedDomains) > 0 {
		opts = append(opts, httpfetch.WithAllowedDomains(cfg.AllowedDomains))
	}
	if len(cfg.BlockedDomains) > 0 {
		opts = append(opts, httpfetch.WithBlockedDomains(cfg.BlockedDomains))
	}

	return []tool.Tool{httpfetch.NewTool(opts...)}, nil
}

type mcpFilterConfig struct {
	Mode  string   `yaml:"mode,omitempty"`
	Names []string `yaml:"names,omitempty"`
}

type mcpReconnectConfig struct {
	Enabled     bool `yaml:"enabled,omitempty"`
	MaxAttempts int  `yaml:"max_attempts,omitempty"`
}

type mcpToolSetConfig struct {
	Transport string            `yaml:"transport,omitempty"`
	ServerURL string            `yaml:"server_url,omitempty"`
	Headers   map[string]string `yaml:"headers,omitempty"`
	Command   string            `yaml:"command,omitempty"`
	Args      []string          `yaml:"args,omitempty"`
	Timeout   time.Duration     `yaml:"timeout,omitempty"`

	ToolFilter *mcpFilterConfig    `yaml:"tool_filter,omitempty"`
	Reconnect  *mcpReconnectConfig `yaml:"reconnect,omitempty"`
}

func newMCPToolSet(
	_ registry.ToolSetProviderDeps,
	spec registry.PluginSpec,
) (tool.ToolSet, error) {
	var cfg mcpToolSetConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	conn := mcp.ConnectionConfig{
		Transport: strings.TrimSpace(cfg.Transport),
		ServerURL: strings.TrimSpace(cfg.ServerURL),
		Headers:   cfg.Headers,
		Command:   strings.TrimSpace(cfg.Command),
		Args:      cfg.Args,
		Timeout:   cfg.Timeout,
	}

	if err := validateMCPConnection(conn); err != nil {
		return nil, err
	}

	options := make([]mcp.ToolSetOption, 0, 4)
	if name := strings.TrimSpace(spec.Name); name != "" {
		options = append(options, mcp.WithName(name))
	}

	filter, err := buildMCPToolFilter(cfg.ToolFilter)
	if err != nil {
		return nil, err
	}
	if filter != nil {
		options = append(options, mcp.WithToolFilterFunc(filter))
	}

	if cfg.Reconnect != nil && cfg.Reconnect.Enabled {
		attempts := cfg.Reconnect.MaxAttempts
		if attempts <= 0 {
			attempts = 3
		}
		options = append(options, mcp.WithSessionReconnect(attempts))
	}

	return mcp.NewMCPToolSet(conn, options...), nil
}

func validateMCPConnection(cfg mcp.ConnectionConfig) error {
	t := strings.ToLower(strings.TrimSpace(cfg.Transport))
	switch t {
	case mcpTransportStdio:
		if strings.TrimSpace(cfg.Command) == "" {
			return errors.New("mcp transport stdio requires command")
		}
		return nil
	case mcpTransportSSE, mcpTransportStreamable, "streamable_http":
		if strings.TrimSpace(cfg.ServerURL) == "" {
			return errors.New("mcp transport requires server_url")
		}
		return nil
	default:
		return fmt.Errorf("unsupported mcp transport: %s", cfg.Transport)
	}
}

func buildMCPToolFilter(cfg *mcpFilterConfig) (tool.FilterFunc, error) {
	if cfg == nil {
		return nil, nil
	}

	names := make([]string, 0, len(cfg.Names))
	for _, name := range cfg.Names {
		v := strings.TrimSpace(name)
		if v == "" {
			continue
		}
		names = append(names, v)
	}
	if len(names) == 0 {
		return nil, nil
	}

	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	switch mode {
	case "", "include":
		return tool.NewIncludeToolNamesFilter(names...), nil
	case "exclude":
		return tool.NewExcludeToolNamesFilter(names...), nil
	default:
		return nil, fmt.Errorf("unsupported mcp tool_filter.mode: %s", cfg.Mode)
	}
}

type fileToolSetConfig struct {
	BaseDir         string   `yaml:"base_dir,omitempty"`
	ReadOnlyDirs    []string `yaml:"read_only_dirs,omitempty"`
	RuntimeReadDirs *bool    `yaml:"runtime_read_dirs,omitempty"`
	ReadOnly        *bool    `yaml:"read_only,omitempty"`
	EnableSave      *bool    `yaml:"enable_save,omitempty"`
	EnableReplace   *bool    `yaml:"enable_replace,omitempty"`

	EnableRead          *bool `yaml:"enable_read,omitempty"`
	EnableReadMultiple  *bool `yaml:"enable_read_multiple,omitempty"`
	EnableList          *bool `yaml:"enable_list,omitempty"`
	EnableSearchFile    *bool `yaml:"enable_search_file,omitempty"`
	EnableSearchContent *bool `yaml:"enable_search_content,omitempty"`

	EnableMove   *bool `yaml:"enable_move,omitempty"`
	EnableCopy   *bool `yaml:"enable_copy,omitempty"`
	EnableDelete *bool `yaml:"enable_delete,omitempty"`

	MaxFileSize        int64 `yaml:"max_file_size,omitempty"`
	MaxToolResultChars int64 `yaml:"max_tool_result_chars,omitempty"`
}

func newFileToolSet(
	deps registry.ToolSetProviderDeps,
	spec registry.PluginSpec,
) (tool.ToolSet, error) {
	var cfg fileToolSetConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	readOnly := true
	if cfg.ReadOnly != nil {
		readOnly = *cfg.ReadOnly
	}

	saveEnabled := !readOnly
	if cfg.EnableSave != nil {
		saveEnabled = *cfg.EnableSave
	}
	replaceEnabled := !readOnly
	if cfg.EnableReplace != nil {
		replaceEnabled = *cfg.EnableReplace
	}

	opts := make([]file.Option, 0, 10)
	if baseDir := strings.TrimSpace(cfg.BaseDir); baseDir != "" {
		opts = append(opts, file.WithBaseDir(baseDir))
	}
	readOnlyDirs := append(
		[]string{},
		cfg.ReadOnlyDirs...,
	)
	runtimeReadDirs := true
	if cfg.RuntimeReadDirs != nil {
		runtimeReadDirs = *cfg.RuntimeReadDirs
	}
	if runtimeReadDirs {
		readOnlyDirs = append(
			readOnlyDirs,
			defaultFileReadOnlyDirs(deps.StateDir)...,
		)
	}
	if len(readOnlyDirs) > 0 {
		opts = append(opts, file.WithReadOnlyDirs(readOnlyDirs...))
	}
	opts = append(opts, file.WithSaveFileEnabled(saveEnabled))
	opts = append(opts, file.WithReplaceContentEnabled(replaceEnabled))

	if cfg.EnableRead != nil {
		opts = append(opts, file.WithReadFileEnabled(*cfg.EnableRead))
	}
	if cfg.EnableReadMultiple != nil {
		opts = append(
			opts,
			file.WithReadMultipleFilesEnabled(*cfg.EnableReadMultiple),
		)
	}
	if cfg.EnableList != nil {
		opts = append(opts, file.WithListFileEnabled(*cfg.EnableList))
	}
	if cfg.EnableSearchFile != nil {
		opts = append(
			opts,
			file.WithSearchFileEnabled(*cfg.EnableSearchFile),
		)
	}
	if cfg.EnableSearchContent != nil {
		opts = append(
			opts,
			file.WithSearchContentEnabled(*cfg.EnableSearchContent),
		)
	}

	moveEnabled := !readOnly
	if cfg.EnableMove != nil {
		moveEnabled = *cfg.EnableMove
	}
	opts = append(opts, file.WithMoveFilesEnabled(moveEnabled))

	copyEnabled := !readOnly
	if cfg.EnableCopy != nil {
		copyEnabled = *cfg.EnableCopy
	}
	opts = append(opts, file.WithCopyFilesEnabled(copyEnabled))

	deleteEnabled := !readOnly
	if cfg.EnableDelete != nil {
		deleteEnabled = *cfg.EnableDelete
	}
	opts = append(opts, file.WithDeleteFilesEnabled(deleteEnabled))

	if cfg.MaxFileSize > 0 {
		opts = append(opts, file.WithMaxFileSize(cfg.MaxFileSize))
	}
	if cfg.MaxToolResultChars > 0 {
		opts = append(opts, file.WithMaxToolResultChars(cfg.MaxToolResultChars))
	}
	if name := strings.TrimSpace(spec.Name); name != "" {
		opts = append(opts, file.WithName(name))
	}

	return file.NewToolSet(opts...)
}

func defaultFileReadOnlyDirs(stateDir string) []string {
	roots := []string{absPathOrOriginal(os.TempDir())}
	if runtime.GOOS != "windows" {
		roots = append(roots, absPathOrOriginal("/tmp"))
	}
	if cwd, err := os.Getwd(); err == nil {
		if cwd = strings.TrimSpace(cwd); cwd != "" {
			artifactDir, ok := browserArtifactReadRoot(cwd)
			if ok {
				roots = append(roots, artifactDir)
			}
		}
	}
	if stateDir := strings.TrimSpace(stateDir); stateDir != "" {
		roots = append(
			roots,
			absPathOrOriginal(filepath.Join(stateDir, "runtime", "tmp")),
			absPathOrOriginal(filepath.Join(stateDir, "workspaces", "scratch")),
		)
	}
	return roots
}

func browserArtifactReadRoot(cwd string) (string, bool) {
	return browserArtifactReadRootWith(cwd, os.Lstat, os.MkdirAll)
}

type browserArtifactLstatFunc func(string) (os.FileInfo, error)

type browserArtifactMkdirAllFunc func(string, os.FileMode) error

func browserArtifactReadRootWith(
	cwd string,
	lstat browserArtifactLstatFunc,
	mkdirAll browserArtifactMkdirAllFunc,
) (string, bool) {
	artifactDir := filepath.Join(cwd, browserArtifactDirName)
	info, err := lstat(artifactDir)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", false
		}
	case os.IsNotExist(err):
		if err := mkdirAll(artifactDir, 0o755); err != nil {
			return "", false
		}
	default:
		return "", false
	}

	info, err = lstat(artifactDir)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", false
	}
	return artifactDir, true
}

func absPathOrOriginal(path string) string {
	if strings.TrimSpace(path) == "" {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

type openAPISpecConfig struct {
	File   string `yaml:"file,omitempty"`
	URL    string `yaml:"url,omitempty"`
	Inline string `yaml:"inline,omitempty"`
}

type openAPIToolSetConfig struct {
	Spec              *openAPISpecConfig `yaml:"spec,omitempty"`
	AllowExternalRefs bool               `yaml:"allow_external_refs,omitempty"`
	UserAgent         string             `yaml:"user_agent,omitempty"`
	Timeout           time.Duration      `yaml:"timeout,omitempty"`
}

func newOpenAPIToolSet(
	_ registry.ToolSetProviderDeps,
	spec registry.PluginSpec,
) (tool.ToolSet, error) {
	var cfg openAPIToolSetConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}
	if cfg.Spec == nil {
		return nil, errors.New("openapi requires config.spec")
	}

	loader, err := openAPILoader(*cfg.Spec, cfg.AllowExternalRefs)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	options := make([]openapitool.Option, 0, 4)
	options = append(options, openapitool.WithSpecLoader(loader))
	if ua := strings.TrimSpace(cfg.UserAgent); ua != "" {
		options = append(options, openapitool.WithUserAgent(ua))
	}
	if cfg.Timeout > 0 {
		client := &http.Client{Timeout: cfg.Timeout}
		options = append(options, openapitool.WithHTTPClient(client))
	}
	if name := strings.TrimSpace(spec.Name); name != "" {
		options = append(options, openapitool.WithName(name))
	}

	return openapitool.NewToolSet(ctx, options...)
}

func openAPILoader(
	cfg openAPISpecConfig,
	allowExternalRefs bool,
) (openapitool.Loader, error) {
	opts := []openapitool.LoaderOption{
		openapitool.WithExternalRefs(allowExternalRefs),
	}

	filePath := strings.TrimSpace(cfg.File)
	urlStr := strings.TrimSpace(cfg.URL)
	inline := strings.TrimSpace(cfg.Inline)

	count := 0
	if filePath != "" {
		count++
	}
	if urlStr != "" {
		count++
	}
	if inline != "" {
		count++
	}
	if count != 1 {
		return nil, errors.New(
			"openapi.spec requires exactly one of file, url, inline",
		)
	}

	if filePath != "" {
		return openapitool.NewFileLoader(filePath, opts...)
	}
	if urlStr != "" {
		return openapitool.NewURILoader(urlStr, opts...)
	}
	return openapitool.NewDataLoader([]byte(inline), opts...)
}

type googleToolSetConfig struct {
	APIKey   string        `yaml:"api_key,omitempty"`
	EngineID string        `yaml:"engine_id,omitempty"`
	BaseURL  string        `yaml:"base_url,omitempty"`
	Size     int           `yaml:"size,omitempty"`
	Offset   int           `yaml:"offset,omitempty"`
	Lang     string        `yaml:"lang,omitempty"`
	Timeout  time.Duration `yaml:"timeout,omitempty"`
}

func newGoogleToolSet(
	_ registry.ToolSetProviderDeps,
	spec registry.PluginSpec,
) (tool.ToolSet, error) {
	var cfg googleToolSetConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv(envGoogleAPIKey))
	}
	engineID := strings.TrimSpace(cfg.EngineID)
	if engineID == "" {
		engineID = strings.TrimSpace(os.Getenv(envGoogleEngineID))
	}

	ctx := context.Background()
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	options := make([]googlesearch.Option, 0, 6)
	options = append(options, googlesearch.WithAPIKey(apiKey))
	options = append(options, googlesearch.WithEngineID(engineID))
	if baseURL := strings.TrimSpace(cfg.BaseURL); baseURL != "" {
		options = append(options, googlesearch.WithBaseURL(baseURL))
	}
	if cfg.Size > 0 {
		options = append(options, googlesearch.WithSize(cfg.Size))
	}
	if cfg.Offset > 0 {
		options = append(options, googlesearch.WithOffset(cfg.Offset))
	}
	if lang := strings.TrimSpace(cfg.Lang); lang != "" {
		options = append(options, googlesearch.WithLanguage(lang))
	}

	ts, err := googlesearch.NewToolSet(ctx, options...)
	if err != nil {
		return nil, err
	}
	return overrideToolSetName(ts, spec.Name), nil
}

type wikipediaToolSetConfig struct {
	Language   string        `yaml:"language,omitempty"`
	MaxResults int           `yaml:"max_results,omitempty"`
	UserAgent  string        `yaml:"user_agent,omitempty"`
	Timeout    time.Duration `yaml:"timeout,omitempty"`
}

func newWikipediaToolSet(
	_ registry.ToolSetProviderDeps,
	spec registry.PluginSpec,
) (tool.ToolSet, error) {
	var cfg wikipediaToolSetConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	options := make([]wikipedia.Option, 0, 4)
	if lang := strings.TrimSpace(cfg.Language); lang != "" {
		options = append(options, wikipedia.WithLanguage(lang))
	}
	if cfg.MaxResults > 0 {
		options = append(options, wikipedia.WithMaxResults(cfg.MaxResults))
	}
	if ua := strings.TrimSpace(cfg.UserAgent); ua != "" {
		options = append(options, wikipedia.WithUserAgent(ua))
	}
	if cfg.Timeout > 0 {
		options = append(options, wikipedia.WithTimeout(cfg.Timeout))
	}

	ts, err := wikipedia.NewToolSet(options...)
	if err != nil {
		return nil, err
	}
	return overrideToolSetName(ts, spec.Name), nil
}

type arxivToolSetConfig struct {
	BaseURL      string        `yaml:"base_url,omitempty"`
	PageSize     int           `yaml:"page_size,omitempty"`
	DelaySeconds time.Duration `yaml:"delay_seconds,omitempty"`
	NumRetries   int           `yaml:"num_retries,omitempty"`
}

func newArxivToolSet(
	_ registry.ToolSetProviderDeps,
	spec registry.PluginSpec,
) (tool.ToolSet, error) {
	var cfg arxivToolSetConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	options := make([]arxivsearch.Option, 0, 4)
	if baseURL := strings.TrimSpace(cfg.BaseURL); baseURL != "" {
		options = append(options, arxivsearch.WithBaseURL(baseURL))
	}
	if cfg.PageSize > 0 {
		options = append(options, arxivsearch.WithPageSize(cfg.PageSize))
	}
	if cfg.DelaySeconds > 0 {
		options = append(
			options,
			arxivsearch.WithDelaySeconds(cfg.DelaySeconds),
		)
	}
	if cfg.NumRetries > 0 {
		options = append(
			options,
			arxivsearch.WithNumRetries(cfg.NumRetries),
		)
	}

	ts, err := arxivsearch.NewToolSet(options...)
	if err != nil {
		return nil, err
	}
	return overrideToolSetName(ts, spec.Name), nil
}

func newEmailToolSet(
	_ registry.ToolSetProviderDeps,
	spec registry.PluginSpec,
) (tool.ToolSet, error) {
	ts, err := email.NewToolSet()
	if err != nil {
		return nil, err
	}
	return overrideToolSetName(ts, spec.Name), nil
}

type toolSetNameOverride struct {
	name string
	tool tool.ToolSet
}

func (t toolSetNameOverride) Tools(ctx context.Context) []tool.Tool {
	return t.tool.Tools(ctx)
}

func (t toolSetNameOverride) Close() error { return t.tool.Close() }

func (t toolSetNameOverride) Name() string { return t.name }

func overrideToolSetName(ts tool.ToolSet, name string) tool.ToolSet {
	if ts == nil {
		return nil
	}
	v := strings.TrimSpace(name)
	if v == "" || v == ts.Name() {
		return ts
	}
	return toolSetNameOverride{name: v, tool: ts}
}

// --- Lua tool set provider ---

type luaToolSetConfig struct {
	DefaultTimeout     int      `yaml:"default_timeout,omitempty"`
	MaxOutputLen       int      `yaml:"max_output_len,omitempty"`
	MaxErrorLen        int      `yaml:"max_error_len,omitempty"`
	DeniedModules      []string `yaml:"denied_modules,omitempty"`
	AllowIOLib         *bool    `yaml:"allow_io_lib,omitempty"`
	AllowOSLib         *bool    `yaml:"allow_os_lib,omitempty"`
	AllowFSLib         *bool    `yaml:"allow_fs_lib,omitempty"`
	DeniedTools        []string `yaml:"denied_tools,omitempty"`
	AllowedScriptDirs  []string `yaml:"allowed_script_dirs,omitempty"`
	AddSkillScriptDirs *bool    `yaml:"add_skill_script_dirs,omitempty"`
	EnableDebug        *bool    `yaml:"enable_debug,omitempty"`
	MaxLogEntries      int      `yaml:"max_log_entries,omitempty"`
}

func newLuaExecToolProvider(
	deps registry.ToolProviderDeps,
	spec registry.PluginSpec,
) ([]tool.Tool, error) {
	var cfg luaToolSetConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	opts := make([]luaexec.Option, 0, 8)
	if name := strings.TrimSpace(spec.Name); name != "" {
		opts = append(opts, luaexec.WithName(name))
	}
	if cfg.DefaultTimeout > 0 {
		opts = append(opts, luaexec.WithDefaultTimeout(cfg.DefaultTimeout))
	}
	if cfg.MaxOutputLen > 0 {
		opts = append(opts, luaexec.WithMaxOutputLen(cfg.MaxOutputLen))
	}
	if cfg.MaxErrorLen > 0 {
		opts = append(opts, luaexec.WithMaxErrorLen(cfg.MaxErrorLen))
	}
	if len(cfg.DeniedModules) > 0 {
		opts = append(opts, luaexec.WithDeniedModules(cfg.DeniedModules...))
	}
	if cfg.AllowIOLib != nil {
		opts = append(opts, luaexec.WithAllowIOLib(*cfg.AllowIOLib))
	}
	if cfg.AllowOSLib != nil {
		opts = append(opts, luaexec.WithAllowOSLib(*cfg.AllowOSLib))
	}
	if cfg.AllowFSLib != nil {
		opts = append(opts, luaexec.WithAllowFSLib(*cfg.AllowFSLib))
	}
	if len(cfg.DeniedTools) > 0 {
		opts = append(opts, luaexec.WithDeniedTools(cfg.DeniedTools...))
	}
	if cfg.EnableDebug != nil {
		opts = append(opts, luaexec.WithEnableDebug(*cfg.EnableDebug))
	}
	if cfg.MaxLogEntries > 0 {
		opts = append(opts, luaexec.WithMaxLogEntries(cfg.MaxLogEntries))
	}

	// Build the final allowed_script_dirs list.
	// When add_skill_script_dirs is true (default), automatically append
	// all skill roots to the explicitly configured allowed_script_dirs.
	addSkillDirs := true // default
	if cfg.AddSkillScriptDirs != nil {
		addSkillDirs = *cfg.AddSkillScriptDirs
	}

	scriptDirs := make([]string, 0, len(cfg.AllowedScriptDirs)+len(deps.SkillsRoots))
	scriptDirs = append(scriptDirs, cfg.AllowedScriptDirs...)
	if addSkillDirs {
		scriptDirs = append(scriptDirs, deps.SkillsRoots...)
	}

	if len(scriptDirs) > 0 {
		opts = append(opts, luaexec.WithAllowedScriptDirs(scriptDirs...))
	}

	// Use ToolsProvider to dynamically obtain the tool list from
	// InvocationContext at runtime, solving the chicken-and-egg problem
	// where the full tool list is not yet available at ToolSet creation time.
	opts = append(opts, luaexec.WithToolsProvider(toolsFromInvocationContext))

	// Pass KB embedder config to luaexec for kb module initialization.
	if deps.KBEmbedder != nil {
		opts = append(opts, luaexec.WithKBConfig(luaexec.KBModuleConfig{
			EmbedderBaseURL: deps.KBEmbedder.BaseURL,
			EmbedderModel:   deps.KBEmbedder.Model,
			EmbedderAPIKey:  deps.KBEmbedder.APIKey,
			Dimensions:      deps.KBEmbedder.Dimensions,
		}))
	}

	t, err := luaexec.NewTool(opts...)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{t}, nil
}

// toolsFromInvocationContext obtains the tool list from the GopherLua VM's context.
//
// Call chain:
//
//	L.Context() → agent.InvocationFromContext(ctx) → inv.Agent.Tools()
//
// GopherLua's L.Context() inherits the ctx parameter from lua_exec Call(),
// which is wrapped by FunctionCallResponseProcessor via agent.NewInvocationContext,
// so agent.InvocationFromContext can retrieve the Invocation,
// and then Invocation.Agent.Tools() returns the current Agent's tool list.
func toolsFromInvocationContext(ctx context.Context) []tool.Tool {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Agent == nil {
		return nil
	}
	return inv.Agent.Tools()
}

// --- Agent tool set provider ---

// agentToolSetConfig maps the YAML "tools.toolsets[].config" section for agent_tool.
type agentToolSetConfig struct {
	Enabled             bool     `yaml:"enabled,omitempty"`
	HistoryScope        string   `yaml:"history_scope,omitempty"`
	StreamInner         bool     `yaml:"stream_inner,omitempty"`
	ExposeToolSelection bool     `yaml:"expose_tool_selection,omitempty"`
	ExposeInstruction   bool     `yaml:"expose_instruction,omitempty"`
	ExcludeTools        []string `yaml:"exclude_tools,omitempty"`
}

func (c *agentToolSetConfig) toHistoryScope() agenttool.HistoryScope {
	switch strings.ToLower(strings.TrimSpace(c.HistoryScope)) {
	case "parent_branch":
		return agenttool.HistoryScopeParentBranch
	default:
		return agenttool.HistoryScopeIsolated
	}
}

// filterByBlacklist filters tools by excluding those whose names match the blacklist.
func filterByBlacklist(tools []tool.Tool, blacklist []string) []tool.Tool {
	if len(blacklist) == 0 {
		return tools
	}
	exclude := make(map[string]bool, len(blacklist))
	for _, name := range blacklist {
		exclude[name] = true
	}
	out := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		if !exclude[t.Declaration().Name] {
			out = append(out, t)
		}
	}
	return out
}

func newAgentToolSet(
	_ registry.ToolSetProviderDeps,
	spec registry.PluginSpec,
) (tool.ToolSet, error) {
	var cfg agentToolSetConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, nil
	}

	// Use CapabilitySurfaceProvider to dynamically obtain the tool list
	// from the parent invocation at call time, solving the chicken-and-egg
	// problem where the full tool list is not yet available at ToolSet
	// creation time. This mirrors the Lua tool's toolsFromInvocationContext pattern.
	provider := func(ctx context.Context, parentInv *agent.Invocation) ([]tool.Tool, map[string]bool) {
		if parentInv == nil || parentInv.Agent == nil {
			return nil, nil
		}
		allTools := parentInv.Agent.Tools()
		return filterByBlacklist(allTools, cfg.ExcludeTools), nil
	}

	agentTool := agenttool.NewDynamicTool(
		agenttool.WithCapabilityProvider(provider),
		agenttool.WithHistoryScope(cfg.toHistoryScope()),
		agenttool.WithStreamInner(cfg.StreamInner),
		agenttool.WithExposeToolSelection(cfg.ExposeToolSelection),
		agenttool.WithExposeInstruction(cfg.ExposeInstruction),
	)

	name := strings.TrimSpace(spec.Name)
	if name == "" {
		name = toolSetProviderAgentTool
	}
	return &singleToolToolSet{tool: agentTool, name: name}, nil
}

// singleToolToolSet wraps a single tool.Tool as a tool.ToolSet.
type singleToolToolSet struct {
	tool tool.Tool
	name string
}

func (s *singleToolToolSet) Tools(_ context.Context) []tool.Tool {
	if s == nil || s.tool == nil {
		return nil
	}
	return []tool.Tool{s.tool}
}

func (s *singleToolToolSet) Name() string {
	if s == nil {
		return ""
	}
	return s.name
}

func (s *singleToolToolSet) Close() error { return nil }
