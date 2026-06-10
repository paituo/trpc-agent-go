//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package bowei registers a tool provider for the Bowei Cost Engineering
// (博微造价) API.
//
// It wraps the Bowei REST API as OPENCLAW tools so that an agent can create
// projects, import bills of quantities, match quotas, adjust prices, calculate
// costs, export reports, list quotas, and run audit checks.
//
// Register by anonymous import:
//
//	import _ "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/tools/bowei"
package bowei

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

const (
	pluginType = "bowei"

	schemaTypeObject  = "object"
	schemaTypeString  = "string"
	schemaTypeNumber  = "number"
	schemaTypeBoolean = "boolean"
	schemaTypeArray   = "array"

	// Default API base URL for the Bowei service.
	defaultBaseURL = "http://localhost:8080/api/v1"

	// HTTP request timeout.
	defaultTimeout = 30 * time.Second
)

func init() {
	if err := registry.RegisterToolProvider(pluginType, newTools); err != nil {
		panic(err)
	}
}

// providerCfg holds the YAML configuration for the bowei tool provider.
type providerCfg struct {
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key"`
	Timeout string `yaml:"timeout"`
}

// boweiClient wraps the HTTP communication with the Bowei REST API.
type boweiClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// newBoweiClient creates a new Bowei API client from the provider config.
func newBoweiClient(cfg providerCfg) *boweiClient {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	timeout := defaultTimeout
	if t := strings.TrimSpace(cfg.Timeout); t != "" {
		if d, err := time.ParseDuration(t); err == nil {
			timeout = d
		}
	}

	return &boweiClient{
		baseURL: baseURL,
		apiKey:  strings.TrimSpace(cfg.APIKey),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// doRequest sends a JSON request to the Bowei API and returns the response
// body.
func (c *boweiClient) doRequest(
	ctx context.Context,
	method string,
	path string,
	body any,
) (json.RawMessage, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("bowei: marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(
		ctx, method, c.baseURL+path, reqBody,
	)
	if err != nil {
		return nil, fmt.Errorf("bowei: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bowei: request failed: %w", err)
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bowei: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf(
			"bowei: API error %d: %s",
			resp.StatusCode,
			string(respData),
		)
	}

	var result struct {
		Data   json.RawMessage `json:"data"`
		Error  string          `json:"error"`
		Code   int             `json:"code"`
	}
	if err := json.Unmarshal(respData, &result); err != nil {
		// If the response is not JSON-wrapped, return raw.
		return json.RawMessage(respData), nil
	}
	if result.Code != 0 && result.Error != "" {
		return nil, fmt.Errorf("bowei: API error %d: %s", result.Code, result.Error)
	}
	if result.Data != nil {
		return result.Data, nil
	}
	return json.RawMessage(respData), nil
}

// newTools is the ToolProviderFactory that creates all Bowei tool instances.
func newTools(
	_ registry.ToolProviderDeps,
	spec registry.PluginSpec,
) ([]tool.Tool, error) {
	var cfg providerCfg
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	client := newBoweiClient(cfg)

	return []tool.Tool{
		&boweiCreateProjectTool{client: client},
		&boweiGetProjectTool{client: client},
		&boweiImportQuantitiesTool{client: client},
		&boweiAutoMatchQuotaTool{client: client},
		&boweiAdjustPriceTool{client: client},
		&boweiCalculateTool{client: client},
		&boweiExportReportTool{client: client},
		&boweiListQuotasTool{client: client},
		&boweiAuditCheckTool{client: client},
	}, nil
}

// ---------------------------------------------------------------------------
// Tool: bowei_create_project
// ---------------------------------------------------------------------------

type boweiCreateProjectTool struct {
	client *boweiClient
}

func (t *boweiCreateProjectTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "bowei_create_project",
		Description: "创建新的博微造价工程项目",
		InputSchema: &tool.Schema{
			Type: schemaTypeObject,
			Properties: map[string]*tool.Schema{
				"name": {
					Type:        schemaTypeString,
					Description: "工程名称",
				},
				"project_type": {
					Type:        schemaTypeString,
					Description: "工程类型（如：变电站、线路、配网、通信）",
				},
				"region": {
					Type:        schemaTypeString,
					Description: "所在地区（如：浙江、广东、北京）",
				},
				"quota_version": {
					Type:        schemaTypeString,
					Description: "定额版本（如：2018版、2021版）",
					Default:     "2018版",
				},
			},
			Required: []string{"name", "project_type", "region"},
		},
	}
}

func (t *boweiCreateProjectTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (any, error) {
	var args struct {
		Name         string `json:"name"`
		ProjectType  string `json:"project_type"`
		Region       string `json:"region"`
		QuotaVersion string `json:"quota_version"`
	}
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, err
	}
	if args.QuotaVersion == "" {
		args.QuotaVersion = "2018版"
	}

	return t.client.doRequest(ctx, http.MethodPost, "/projects", map[string]any{
		"name":          args.Name,
		"project_type":  args.ProjectType,
		"region":        args.Region,
		"quota_version": args.QuotaVersion,
	})
}

// ---------------------------------------------------------------------------
// Tool: bowei_get_project
// ---------------------------------------------------------------------------

type boweiGetProjectTool struct {
	client *boweiClient
}

func (t *boweiGetProjectTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "bowei_get_project",
		Description: "获取博微造价项目详情",
		InputSchema: &tool.Schema{
			Type: schemaTypeObject,
			Properties: map[string]*tool.Schema{
				"project_id": {
					Type:        schemaTypeString,
					Description: "项目ID",
				},
			},
			Required: []string{"project_id"},
		},
	}
}

func (t *boweiGetProjectTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (any, error) {
	var args struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, err
	}

	path := fmt.Sprintf("/projects/%s", args.ProjectID)
	return t.client.doRequest(ctx, http.MethodGet, path, nil)
}

// ---------------------------------------------------------------------------
// Tool: bowei_import_quantities
// ---------------------------------------------------------------------------

type boweiImportQuantitiesTool struct {
	client *boweiClient
}

func (t *boweiImportQuantitiesTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "bowei_import_quantities",
		Description: "将工程量清单文件导入到当前造价项目",
		InputSchema: &tool.Schema{
			Type: schemaTypeObject,
			Properties: map[string]*tool.Schema{
				"project_id": {
					Type:        schemaTypeString,
					Description: "项目ID",
				},
				"file_path": {
					Type:        schemaTypeString,
					Description: "清单文件路径（支持 Excel/CSV 格式）",
				},
				"format": {
					Type:        schemaTypeString,
					Description: "文件格式（excel/csv）",
					Default:     "excel",
				},
			},
			Required: []string{"project_id", "file_path"},
		},
	}
}

func (t *boweiImportQuantitiesTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (any, error) {
	var args struct {
		ProjectID string `json:"project_id"`
		FilePath  string `json:"file_path"`
		Format    string `json:"format"`
	}
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, err
	}
	if args.Format == "" {
		args.Format = "excel"
	}

	path := fmt.Sprintf("/projects/%s/quantities/import", args.ProjectID)
	return t.client.doRequest(ctx, http.MethodPost, path, map[string]any{
		"file_path": args.FilePath,
		"format":    args.Format,
	})
}

// ---------------------------------------------------------------------------
// Tool: bowei_auto_match_quota
// ---------------------------------------------------------------------------

type boweiAutoMatchQuotaTool struct {
	client *boweiClient
}

func (t *boweiAutoMatchQuotaTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "bowei_auto_match_quota",
		Description: "对工程量清单套取匹配的定额子目",
		InputSchema: &tool.Schema{
			Type: schemaTypeObject,
			Properties: map[string]*tool.Schema{
				"project_id": {
					Type:        schemaTypeString,
					Description: "项目ID",
				},
				"strategy": {
					Type:        schemaTypeString,
					Description: "匹配策略（auto/semi-auto/manual）",
					Default:     "auto",
					Enum:        []any{"auto", "semi-auto", "manual"},
				},
				"confidence_threshold": {
					Type:        schemaTypeNumber,
					Description: "自动匹配的置信度阈值（0-1之间，默认0.8）",
					Default:     0.8,
				},
			},
			Required: []string{"project_id"},
		},
	}
}

func (t *boweiAutoMatchQuotaTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (any, error) {
	var args struct {
		ProjectID            string  `json:"project_id"`
		Strategy             string  `json:"strategy"`
		ConfidenceThreshold  float64 `json:"confidence_threshold"`
	}
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, err
	}
	if args.Strategy == "" {
		args.Strategy = "auto"
	}
	if args.ConfidenceThreshold == 0 {
		args.ConfidenceThreshold = 0.8
	}

	path := fmt.Sprintf("/projects/%s/quota/match", args.ProjectID)
	return t.client.doRequest(ctx, http.MethodPost, path, map[string]any{
		"strategy":              args.Strategy,
		"confidence_threshold":  args.ConfidenceThreshold,
	})
}

// ---------------------------------------------------------------------------
// Tool: bowei_adjust_price
// ---------------------------------------------------------------------------

type boweiAdjustPriceTool struct {
	client *boweiClient
}

func (t *boweiAdjustPriceTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "bowei_adjust_price",
		Description: "调整材料价格（市场价/信息价/预算价）",
		InputSchema: &tool.Schema{
			Type: schemaTypeObject,
			Properties: map[string]*tool.Schema{
				"project_id": {
					Type:        schemaTypeString,
					Description: "项目ID",
				},
				"price_source": {
					Type:        schemaTypeString,
					Description: "价格来源（市场价/信息价/预算价）",
					Default:     "市场价",
					Enum:        []any{"市场价", "信息价", "预算价"},
				},
				"material_category": {
					Type:        schemaTypeString,
					Description: "材料类别（如：钢材、水泥、电缆），默认全部",
				},
				"dry_run": {
					Type:        schemaTypeBoolean,
					Description: "是否仅预览变动而不实际更新（默认false）",
					Default:     false,
				},
			},
			Required: []string{"project_id"},
		},
	}
}

func (t *boweiAdjustPriceTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (any, error) {
	var args struct {
		ProjectID        string `json:"project_id"`
		PriceSource      string `json:"price_source"`
		MaterialCategory string `json:"material_category"`
		DryRun           bool   `json:"dry_run"`
	}
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, err
	}
	if args.PriceSource == "" {
		args.PriceSource = "市场价"
	}

	path := fmt.Sprintf("/projects/%s/prices/adjust", args.ProjectID)
	return t.client.doRequest(ctx, http.MethodPost, path, map[string]any{
		"price_source":      args.PriceSource,
		"material_category": args.MaterialCategory,
		"dry_run":           args.DryRun,
	})
}

// ---------------------------------------------------------------------------
// Tool: bowei_calculate
// ---------------------------------------------------------------------------

type boweiCalculateTool struct {
	client *boweiClient
}

func (t *boweiCalculateTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "bowei_calculate",
		Description: "执行造价计算（含分部分项费、措施费、其他费、规费、税金）",
		InputSchema: &tool.Schema{
			Type: schemaTypeObject,
			Properties: map[string]*tool.Schema{
				"project_id": {
					Type:        schemaTypeString,
					Description: "项目ID",
				},
				"force_recalculate": {
					Type:        schemaTypeBoolean,
					Description: "是否强制重新计算（默认false）",
					Default:     false,
				},
			},
			Required: []string{"project_id"},
		},
	}
}

func (t *boweiCalculateTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (any, error) {
	var args struct {
		ProjectID        string `json:"project_id"`
		ForceRecalculate bool   `json:"force_recalculate"`
	}
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, err
	}

	path := fmt.Sprintf("/projects/%s/calculate", args.ProjectID)
	return t.client.doRequest(ctx, http.MethodPost, path, map[string]any{
		"force_recalculate": args.ForceRecalculate,
	})
}

// ---------------------------------------------------------------------------
// Tool: bowei_export_report
// ---------------------------------------------------------------------------

type boweiExportReportTool struct {
	client *boweiClient
}

func (t *boweiExportReportTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "bowei_export_report",
		Description: "生成造价报表（概预算表/费用汇总表/材料汇总表/分部分项表）",
		InputSchema: &tool.Schema{
			Type: schemaTypeObject,
			Properties: map[string]*tool.Schema{
				"project_id": {
					Type:        schemaTypeString,
					Description: "项目ID",
				},
				"report_type": {
					Type:        schemaTypeString,
					Description: "报表类型（概预算表/费用汇总表/材料汇总表/分部分项表/全选）",
					Default:     "概预算表",
					Enum: []any{
						"概预算表",
						"费用汇总表",
						"材料汇总表",
						"分部分项表",
						"全选",
					},
				},
				"output_format": {
					Type:        schemaTypeString,
					Description: "输出格式（Excel/PDF）",
					Default:     "Excel",
					Enum:        []any{"Excel", "PDF"},
				},
			},
			Required: []string{"project_id"},
		},
	}
}

func (t *boweiExportReportTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (any, error) {
	var args struct {
		ProjectID    string `json:"project_id"`
		ReportType   string `json:"report_type"`
		OutputFormat string `json:"output_format"`
	}
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, err
	}
	if args.ReportType == "" {
		args.ReportType = "概预算表"
	}
	if args.OutputFormat == "" {
		args.OutputFormat = "Excel"
	}

	path := fmt.Sprintf("/projects/%s/reports/export", args.ProjectID)
	return t.client.doRequest(ctx, http.MethodPost, path, map[string]any{
		"report_type":   args.ReportType,
		"output_format": args.OutputFormat,
	})
}

// ---------------------------------------------------------------------------
// Tool: bowei_list_quotas
// ---------------------------------------------------------------------------

type boweiListQuotasTool struct {
	client *boweiClient
}

func (t *boweiListQuotasTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "bowei_list_quotas",
		Description: "查询定额库中的定额子目",
		InputSchema: &tool.Schema{
			Type: schemaTypeObject,
			Properties: map[string]*tool.Schema{
				"keyword": {
					Type:        schemaTypeString,
					Description: "搜索关键词（定额名称或编码）",
				},
				"quota_version": {
					Type:        schemaTypeString,
					Description: "定额版本（如：2018版、2021版）",
					Default:     "2018版",
				},
				"category": {
					Type:        schemaTypeString,
					Description: "定额分类（如：土建、安装、装饰）",
				},
				"page": {
					Type:        schemaTypeNumber,
					Description: "页码（从1开始）",
					Default:     1,
				},
				"page_size": {
					Type:        schemaTypeNumber,
					Description: "每页条数（默认20）",
					Default:     20,
				},
			},
			Required: []string{"keyword"},
		},
	}
}

func (t *boweiListQuotasTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (any, error) {
	var args struct {
		Keyword      string  `json:"keyword"`
		QuotaVersion string  `json:"quota_version"`
		Category     string  `json:"category"`
		Page         float64 `json:"page"`
		PageSize     float64 `json:"page_size"`
	}
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, err
	}
	if args.QuotaVersion == "" {
		args.QuotaVersion = "2018版"
	}
	if args.Page == 0 {
		args.Page = 1
	}
	if args.PageSize == 0 {
		args.PageSize = 20
	}

	return t.client.doRequest(ctx, http.MethodGet, "/quotas", map[string]any{
		"keyword":       args.Keyword,
		"quota_version": args.QuotaVersion,
		"category":      args.Category,
		"page":          args.Page,
		"page_size":     args.PageSize,
	})
}

// ---------------------------------------------------------------------------
// Tool: bowei_audit_check
// ---------------------------------------------------------------------------

type boweiAuditCheckTool struct {
	client *boweiClient
}

func (t *boweiAuditCheckTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "bowei_audit_check",
		Description: "审核造价编制的合规性和合理性",
		InputSchema: &tool.Schema{
			Type: schemaTypeObject,
			Properties: map[string]*tool.Schema{
				"project_id": {
					Type:        schemaTypeString,
					Description: "项目ID",
				},
				"scope": {
					Type:        schemaTypeString,
					Description: "审核范围（quota/quantity/price/fee/consistency/all，默认all）",
					Default:     "all",
					Enum: []any{
						"quota",
						"quantity",
						"price",
						"fee",
						"consistency",
						"all",
					},
				},
			},
			Required: []string{"project_id"},
		},
	}
}

func (t *boweiAuditCheckTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (any, error) {
	var args struct {
		ProjectID string `json:"project_id"`
		Scope     string `json:"scope"`
	}
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, err
	}
	if args.Scope == "" {
		args.Scope = "all"
	}

	path := fmt.Sprintf("/projects/%s/audit", args.ProjectID)
	return t.client.doRequest(ctx, http.MethodPost, path, map[string]any{
		"scope": args.Scope,
	})
}
