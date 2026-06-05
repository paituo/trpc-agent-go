# 上下文使用情况可视化 — 数据获取功能实现计划

## 摘要

在现有 trpc-agent-go 平台中增加"上下文使用情况"数据获取功能，使前端（AG-UI 客户端）能够：

1. **获取上下文窗口用量概览**：总上下文窗口大小、已使用 token 数、剩余 token 数、以及各类内容（system prompt、用户消息、助手回复、工具调用/结果等）分别占用的 token 数
2. **查看上下文中具体包含哪些内容**：列出当前上下文中每条消息的摘要信息，包括文件名列表、图片/音频等附件信息、工具调用名称与参数摘要、文本内容前 N 个字符预览等

前端可据此渲染柱状图（用量概览）+ 内容清单列表（具体内容详情）。

## 现状分析

### 已有能力

1. **Token 计数器** — [model/token_tailor.go](file:///workspace/model/token_tailor.go)
   - `TokenCounter` 接口：`CountTokens(ctx, Message)` / `CountTokensRange(ctx, []Message, start, end)`
   - `SimpleTokenCounter`：基于 UTF-8 rune 的启发式估算（默认 4 rune/token，Qwen/DeepSeek ~1.7 rune/token，GLM ~1.8 rune/token）
   - `NewTokenCounter(modelName)` 工厂：根据模型名自动选择合适的计数器
   - 支持通过 `SetTokenCounterFromModel` 注册自定义工厂（如 tiktoken）

2. **模型上下文窗口注册表** — [model/internal/model/model_info.go](file:///workspace/model/internal/model/model_info.go)
   - `ModelContextWindows` 全局 map：200+ 模型的 context window 大小
   - `LookupContextWindow(modelName)` / `ResolveContextWindow(modelName)` 查询接口
   - `model.Info.ContextWindow` 字段：模型实例级别的上下文窗口大小
   - `modelcontext.ResolveContextWindow(m Model)` 统一解析入口

3. **消息与事件体系**
   - [model/request.go](file:///workspace/model/request.go)：`Message` 结构体，包含以下内容类型：
     - `Content string`：纯文本内容
     - `ContentParts []ContentPart`：多模态内容，支持 4 种类型：
       - `ContentTypeText`：文本
       - `ContentTypeImage`：图片（URL / Data + Format + Detail）
       - `ContentTypeAudio`：音频（Data + Format）
       - `ContentTypeFile`：文件（Name / URL / Data / FileID / MimeType）
     - `ToolCalls []ToolCall`：工具调用（Type / Function.Name / Function.Arguments / ID）
     - `ToolID` + `ToolName`：工具结果标识
     - `ReasoningContent`：推理/思考内容
   - [model/response.go](file:///workspace/model/response.go)：`Response.Usage` 包含 `PromptTokens`/`CompletionTokens`/`TotalTokens`
   - [event/event.go](file:///workspace/event/event.go)：`Event` 嵌入 `*model.Response`，含 `StateDelta`/`Extensions`

4. **会话管理** — [session/session.go](file:///workspace/session/session.go)
   - `Session` 结构体含 `Events []event.Event`，可遍历所有历史事件
   - `Session.Summaries` 支持按 filterKey 获取摘要

5. **AG-UI 协议通信** — [server/agui/](file:///workspace/server/agui/)
   - SSE 传输：[server/agui/service/sse/sse.go](file:///workspace/server/agui/service/sse/sse.go)
   - Runner：[server/agui/runner/runner.go](file:///workspace/server/agui/runner/runner.go) — 将内部事件翻译为 AG-UI 事件
   - Translator：[server/agui/translator/translator.go](file:///workspace/server/agui/translator/translator.go) — 支持 `CustomEvent` 扩展
   - 已有 `MessagesSnapshotter` 接口用于获取历史消息快照

6. **遥测指标** — [telemetry/](file:///workspace/telemetry/)
   - 已有 `MetricContextUsageRatio` 和 `MetricContextUsageRatioByInitial` 指标
   - 已有 `ChatMetricGenAIClientTokenUsage` token 使用量记录

### 缺失能力

1. **无上下文使用详情 API** — 当前没有 HTTP 端点返回"当前会话上下文使用明细"
2. **无按类别统计 token 的逻辑** — TokenCounter 只能统计整条消息，没有按 system/user/assistant/tool/reasoning 等类别聚合
3. **无上下文内容清单** — 当前无法查看上下文中具体包含哪些文件、图片、工具调用等内容
4. **无前端数据结构** — AG-UI 协议中没有上下文使用情况的标准化事件/消息类型
5. **无实时推送机制** — 当前 token 使用信息仅在 LLM 响应的 `Usage` 字段中返回，不会主动推送给前端

---

## 方案设计

### 核心思路

在后端新增"上下文使用情况"计算模块，提供两个维度的数据：

- **维度一：用量概览**（ContextUsage）— token 数量统计，用于渲染柱状图
- **维度二：内容清单**（ContextContents）— 上下文中每条消息的摘要信息，用于查看具体包含什么内容

通过 AG-UI 的 **CustomEvent** 机制将数据推送到前端，同时提供 **REST API 端点** 供前端主动查询。

### 数据模型

#### 用量概览

```go
// ContextUsage 表示当前会话的上下文窗口使用详情
type ContextUsage struct {
    ModelName       string          `json:"modelName"`
    ContextWindow   int             `json:"contextWindow"`    // 总上下文窗口大小（tokens）
    UsedTokens      int             `json:"usedTokens"`       // 已使用 token 总数
    RemainingTokens int             `json:"remainingTokens"`  // 剩余可用 token 数
    Breakdown       []CategoryUsage `json:"breakdown"`        // 按类别统计明细
    UpdatedAt       time.Time       `json:"updatedAt"`
}

// CategoryUsage 表示某一类内容的 token 使用情况
type CategoryUsage struct {
    Category string `json:"category"` // "system" | "user" | "assistant" | "tool" | "reasoning" | "overhead"
    Tokens   int    `json:"tokens"`
    Count    int    `json:"count"`    // 该类别的消息条数
}
```

#### 内容清单（新增）

```go
// ContextContents 表示当前上下文中的具体内容清单
type ContextContents struct {
    ModelName   string              `json:"modelName"`
    TotalItems  int                 `json:"totalItems"`   // 内容条目总数
    Items       []ContextContentItem `json:"items"`        // 每条消息的内容摘要
    UpdatedAt   time.Time           `json:"updatedAt"`
}

// ContextContentItem 表示上下文中一条消息的内容摘要
type ContextContentItem struct {
    Index    int                  `json:"index"`     // 在上下文中的序号（从0开始）
    Role     string               `json:"role"`      // "system" | "user" | "assistant" | "tool"
    Tokens   int                  `json:"tokens"`    // 该条消息的估算 token 数
    TextPreview string            `json:"textPreview,omitempty"` // 文本内容前100字符预览
    Files    []FileSummary        `json:"files,omitempty"`       // 包含的文件列表
    Images   []ImageSummary       `json:"images,omitempty"`      // 包含的图片列表
    Audio    []AudioSummary       `json:"audio,omitempty"`       // 包含的音频列表
    ToolCalls []ToolCallSummary   `json:"toolCalls,omitempty"`   // 工具调用信息
    ToolResult *ToolResultSummary `json:"toolResult,omitempty"`  // 工具结果信息
    HasReasoning bool             `json:"hasReasoning"`          // 是否包含推理内容
}

// FileSummary 文件摘要
type FileSummary struct {
    Name     string `json:"name"`               // 文件名
    MimeType string `json:"mimeType,omitempty"`  // MIME 类型（如 "application/pdf"）
    Source   string `json:"source"`              // 来源类型："url" | "data" | "file_id"
    SizeHint string `json:"sizeHint,omitempty"`  // 大小提示（如 "2.3KB"），仅 data 来源时可用
}

// ImageSummary 图片摘要
type ImageSummary struct {
    URL    string `json:"url,omitempty"`    // 图片 URL（如有）
    Format string `json:"format,omitempty"` // 格式（如 "png", "jpeg"）
    Detail string `json:"detail,omitempty"` // 细节级别（"low", "high", "auto"）
}

// AudioSummary 音频摘要
type AudioSummary struct {
    Format string `json:"format"` // 格式（如 "wav", "mp3"）
}

// ToolCallSummary 工具调用摘要
type ToolCallSummary struct {
    ID       string `json:"id"`                 // 工具调用 ID
    Name     string `json:"name"`               // 工具名称（如 "search_web"）
    ArgsPreview string `json:"argsPreview,omitempty"` // 参数前100字符预览
}

// ToolResultSummary 工具结果摘要
type ToolResultSummary struct {
    ToolID   string `json:"toolId"`             // 对应的工具调用 ID
    ToolName string `json:"toolName,omitempty"` // 工具名称
    ContentPreview string `json:"contentPreview,omitempty"` // 结果内容前100字符预览
}
```

### 变更清单

#### 1. 新增上下文使用统计模块

**文件**: `session/context_usage.go`（新建）

- 定义 `ContextUsage`、`CategoryUsage` 结构体
- 定义 `ContextContents`、`ContextContentItem`、`FileSummary`、`ImageSummary`、`AudioSummary`、`ToolCallSummary`、`ToolResultSummary` 结构体
- 实现 `ComputeContextUsage()` 函数 — 遍历 Events，按角色分类统计 token
- 实现 `ComputeContextContents()` 函数 — 遍历 Events，提取每条消息的内容摘要
  - 提取 `Message.Content` 前 100 字符作为 `TextPreview`
  - 遍历 `Message.ContentParts`，按类型提取文件/图片/音频摘要
  - 遍历 `Message.ToolCalls`，提取工具名称和参数预览
  - 对 `Role == "tool"` 的消息，提取 `ToolID`/`ToolName` 和内容预览
  - 检查 `ReasoningContent` 是否存在，标记 `HasReasoning`
- 定义 `ContextUsageConfig` 配置结构体（含 ProtocolOverheadTokens、ReserveOutputTokens、SafetyMarginRatio、MaxInputTokensRatio）
- 定义 `resolveContextWindow()` 辅助函数

#### 2. 扩展 Token 计数便捷方法

**文件**: `model/token_tailor.go`（修改）

- 新增 `CategoryTokenCount` 结构体
- 新增 `CountTokensByCategory(ctx, tokenCounter, messages)` 函数 — 按 `Message.Role` 分类统计 token 数，`ReasoningContent` 单独计入 `ReasoningTokens` 字段
- 新增 `categoryTokenAccumulator` 内部类型

#### 3. 在 AG-UI Runner 中注入上下文使用信息

**文件**: `server/agui/runner/options.go`（修改）

- 新增 `ContextUsageEnabled` 配置字段（默认 `false`）
- 新增 `WithEnableContextUsage(enabled bool) Option`

**文件**: `server/agui/runner/runner.go`（修改）

- 在 `runner` 结构体中新增 `contextUsageEnabled` 字段
- 在 `New()` 中从 Options 读取 `ContextUsageEnabled`
- 在 `handleAgentEvent()` 中，翻译完事件后，如果 `contextUsageEnabled && isLLMCompletionEvent(customEvent)`：
  - 调用 `session.ComputeContextUsage()` 计算用量概览
  - 构造 `CustomEvent("context_usage", value=ContextUsage)` 推送
  - 调用 `session.ComputeContextContents()` 计算内容清单
  - 构造 `CustomEvent("context_contents", value=ContextContents)` 推送
- 新增 `isLLMCompletionEvent()` 辅助函数 — 判断事件是否为含 Usage 的 LLM 完成响应
- 新增 `emitContextUsageEvent()` 方法 — 计算并推送上下文使用信息

#### 4. 新增 REST API 端点（主动查询）

**文件**: `server/agui/service/options.go`（修改）

- 新增 `ContextUsageEnabled`、`ContextUsagePath`、`SessionService` 字段
- 新增 `WithContextUsageEnabled()`、`WithContextUsagePath()`、`WithSessionService()` Option 函数

**文件**: `server/agui/service/sse/sse.go`（修改）

- 在 `sse` 结构体中新增 `contextUsageEnabled`、`contextUsagePath`、`sessionService` 字段
- 在 `New()` 中注册 `contextUsagePath` 路由
- 新增 `handleContextUsage()` 方法 — POST 请求处理
  - 请求体：`{ "threadId": "xxx", "userId": "xxx", "appName": "xxx", "modelName": "gpt-4o" }`
  - 从 `sessionService.GetSession()` 获取会话
  - 调用 `session.ComputeContextUsage()` + `session.ComputeContextContents()`
  - 返回合并的 JSON 响应：`{ "usage": ContextUsage, "contents": ContextContents }`

**文件**: `server/agui/options.go`（修改）

- 新增 `contextUsageEnabled`、`contextUsagePath` 字段
- 新增 `WithEnableContextUsage()`、`WithContextUsagePath()` Option 函数

**文件**: `server/agui/agui.go`（修改）

- 在 `newService()` 中，当 `contextUsageEnabled` 时注册 context usage 路由
- 校验 `sessionService` 不为空

### 数据流

```
前端请求 → AG-UI SSE/REST API → Runner/Service
                                    ↓
                            Session.GetSession()
                                    ↓
                        遍历 Session.Events 提取 Messages
                                    ↓
                    ┌────────────────┴────────────────┐
                    ↓                                 ↓
          ComputeContextUsage()              ComputeContextContents()
          按 Role 分类统计 token             提取每条消息的内容摘要
          （文件名、图片、工具调用等）
                    ↓                                 ↓
              ContextUsage                      ContextContents
              （柱状图数据）                    （内容清单数据）
                    ↓                                 ↓
            CustomEvent 推送              CustomEvent 推送
            或 REST API JSON 响应 → 前端渲染
```

### 前端消费方式

1. **实时推送**：
   - 前端监听 SSE 事件流中的 `CustomEvent`
   - `name === "context_usage"` → 解析 `value` 获取 `ContextUsage`，渲染柱状图
   - `name === "context_contents"` → 解析 `value` 获取 `ContextContents`，渲染内容清单列表

2. **主动查询**：
   - 前端通过 POST `/api/context-usage` 端点按需获取完整数据（usage + contents）

3. **渲染建议**：
   - **柱状图**：根据 `ContextUsage.breakdown` 渲染堆叠柱状图，不同类别用不同颜色标识
   - **内容清单**：根据 `ContextContents.items` 渲染列表，每条显示角色图标、文本预览、附件标签（文件名、图片缩略图等）、token 数
   - **文件列表**：从 `items` 中筛选含 `files` 的条目，汇总展示所有文件名和类型

### API 响应示例

```json
{
  "usage": {
    "modelName": "gpt-4o",
    "contextWindow": 128000,
    "usedTokens": 24500,
    "remainingTokens": 103500,
    "breakdown": [
      { "category": "system", "tokens": 1500, "count": 1 },
      { "category": "user", "tokens": 8000, "count": 5 },
      { "category": "assistant", "tokens": 12000, "count": 5 },
      { "category": "tool", "tokens": 3000, "count": 3 }
    ],
    "updatedAt": "2026-06-05T10:30:00Z"
  },
  "contents": {
    "modelName": "gpt-4o",
    "totalItems": 14,
    "items": [
      {
        "index": 0,
        "role": "system",
        "tokens": 1500,
        "textPreview": "You are a helpful assistant that can analyze documents..."
      },
      {
        "index": 1,
        "role": "user",
        "tokens": 2000,
        "textPreview": "Please analyze the following financial report...",
        "files": [
          { "name": "Q1_report.pdf", "mimeType": "application/pdf", "source": "file_id" },
          { "name": "data.csv", "mimeType": "text/csv", "source": "data", "sizeHint": "15.2KB" }
        ],
        "images": [
          { "url": "https://example.com/chart.png", "format": "png", "detail": "auto" }
        ]
      },
      {
        "index": 5,
        "role": "assistant",
        "tokens": 3500,
        "textPreview": "Based on the financial report, I can see that...",
        "toolCalls": [
          { "id": "call_abc123", "name": "search_web", "argsPreview": "{\"query\": \"Q1 2026 revenue trends\"}" }
        ],
        "hasReasoning": true
      },
      {
        "index": 6,
        "role": "tool",
        "tokens": 800,
        "toolResult": {
          "toolId": "call_abc123",
          "toolName": "search_web",
          "contentPreview": "Found 10 results for Q1 2026 revenue trends..."
        }
      }
    ],
    "updatedAt": "2026-06-05T10:30:00Z"
  }
}
```

---

## 假设与决策

1. **Token 计数精度**：使用现有 `SimpleTokenCounter` 启发式估算，精度足够用于可视化展示。如需更高精度，可通过 `SetTokenCounterFromModel` 注册 tiktoken 实现。
2. **性能考量**：每次 LLM 响应后计算上下文使用情况需要遍历事件列表并逐条计数，对于长会话可能有性能开销。建议：
   - 默认关闭实时推送，仅通过 REST API 按需查询
   - 如开启实时推送，可缓存上次计算结果，增量更新
3. **上下文窗口来源优先级**：`model.Info.ContextWindow` > `LookupModelContextWindow(modelName)` > `defaultContextWindow(128K)`
4. **Token Tailoring 配置影响**：计算剩余可用 token 时需考虑 `ReserveOutputTokens` 和 `SafetyMarginRatio`
5. **AG-UI 协议兼容性**：使用 `CustomEvent` 扩展，不修改 AG-UI 协议核心定义，确保向后兼容
6. **内容预览截断**：文本预览最多显示前 100 个字符，工具参数预览最多 100 字符，避免响应过大
7. **文件大小提示**：仅对 `source == "data"` 的文件计算 `SizeHint`（基于 `len(Data)`），URL 和 FileID 来源无法获取大小
8. **敏感信息**：内容预览可能包含敏感信息，后续可考虑添加脱敏选项（当前版本暂不处理）

## 验证步骤

1. **单元测试**：
   - `ComputeContextUsage()` 函数测试：验证不同角色消息的分类统计正确性
   - `ComputeContextContents()` 函数测试：验证文件/图片/工具调用等内容的正确提取
   - `CountTokensByCategory()` 函数测试：验证按角色分类的 token 计数
   - 边界情况：空会话、超大消息、未知模型名、无附件消息

2. **集成测试**：
   - 验证 AG-UI Runner 在 `contextUsageEnabled=true` 时正确推送 CustomEvent
   - 验证 REST API 端点返回正确的 JSON（含 usage + contents）
   - 验证 SSE 事件流中 CustomEvent 的格式正确

3. **端到端测试**：
   - 使用 AG-UI 前端客户端连接后端，发送多轮对话（含文件、图片、工具调用）
   - 验证上下文使用情况数据随对话进行正确更新
   - 验证内容清单中正确显示文件名、图片信息、工具调用名称等
