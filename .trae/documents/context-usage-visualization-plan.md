# 上下文使用情况可视化 — 数据获取功能实现计划

## 摘要

在现有 trpc-agent-go 平台中增加"上下文使用情况"数据获取功能，使前端（AG-UI 客户端）能够获取当前会话的上下文窗口使用详情，包括：总上下文窗口大小、已使用 token 数、剩余 token 数、以及各类内容（system prompt、用户消息、助手回复、工具调用/结果、摘要等）分别占用的 token 数。前端可据此渲染柱状图等可视化组件。

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
   - [model/request.go](file:///workspace/model/request.go)：`Message` 结构体（Role/Content/ContentParts/ToolCalls/ReasoningContent）
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
3. **无前端数据结构** — AG-UI 协议中没有上下文使用情况的标准化事件/消息类型
4. **无实时推送机制** — 当前 token 使用信息仅在 LLM 响应的 `Usage` 字段中返回，不会主动推送给前端

## 方案设计

### 核心思路

在 **后端** 新增一个"上下文使用情况"计算模块，通过 AG-UI 的 **CustomEvent** 机制将数据推送到前端，同时提供一个 **REST API 端点** 供前端主动查询。

### 数据模型

```go
// ContextUsage 表示当前会话的上下文使用详情
type ContextUsage struct {
    ModelName      string            `json:"modelName"`
    ContextWindow  int               `json:"contextWindow"`  // 总上下文窗口大小（tokens）
    UsedTokens     int               `json:"usedTokens"`     // 已使用 token 总数
    RemainingTokens int              `json:"remainingTokens"` // 剩余可用 token 数
    Breakdown      []CategoryUsage   `json:"breakdown"`      // 按类别统计明细
    UpdatedAt      time.Time         `json:"updatedAt"`      // 最后更新时间
}

// CategoryUsage 表示某一类内容的 token 使用情况
type CategoryUsage struct {
    Category string `json:"category"` // "system" | "user" | "assistant" | "tool" | "reasoning" | "summary" | "overhead"
    Tokens   int    `json:"tokens"`   // 该类别占用的 token 数
    Count    int    `json:"count"`    // 该类别的消息条数
}
```

### 变更清单

#### 1. 新增上下文使用统计模块

**文件**: `session/context_usage.go`（新建）

- 定义 `ContextUsage` 和 `CategoryUsage` 结构体
- 实现 `ComputeContextUsage(sess *Session, modelName string, tokenCounter model.TokenCounter) (*ContextUsage, error)` 函数
  - 遍历 `sess.Events`，按 `Event.Response.Choices[0].Message.Role` 分类
  - 对每条消息调用 `tokenCounter.CountTokens()` 累加
  - 特殊处理：`ReasoningContent` 单独归类为 "reasoning"
  - 从 `model.LookupModelContextWindow(modelName)` 或 `model.Info.ContextWindow` 获取窗口大小
  - 计算 `RemainingTokens = ContextWindow - UsedTokens`
  - 考虑 `TokenTailoringConfig` 中的 `ProtocolOverheadTokens`、`ReserveOutputTokens`、`SafetyMarginRatio` 等参数

#### 2. 在 AG-UI Runner 中注入上下文使用信息

**文件**: `server/agui/runner/runner.go`（修改）

- 在 `run()` 方法中，每次收到 LLM 响应事件后（即 `handleAgentEvent` 处理完 `chat.completion` 或 `chat.completion.chunk` 事件后），如果响应包含 `Usage` 信息：
  - 调用 `ComputeContextUsage()` 计算当前上下文使用情况
  - 构造 AG-UI `CustomEvent`，事件名为 `"context_usage"`，value 为 `ContextUsage` 的 JSON
  - 通过 `emitEvent()` 推送给前端

- 新增配置项 `contextUsageEnabled`（默认关闭），通过 `WithEnableContextUsage()` Option 开启
- 新增配置项 `contextUsageInterval`（默认每次 LLM 响应后推送），可配置为按时间间隔推送

**文件**: `server/agui/runner/options.go`（修改）

- 新增 `WithEnableContextUsage(enabled bool) Option`
- 新增 `WithContextUsageInterval(interval time.Duration) Option`

#### 3. 新增 REST API 端点（主动查询）

**文件**: `server/agui/service/sse/sse.go`（修改）

- 新增 `contextUsagePath` 路由处理
- POST 请求，接收 `{ "threadId": "xxx", "modelName": "gpt-4o" }` 参数
- 从 `sessionService` 获取会话，调用 `ComputeContextUsage()` 返回 JSON

**文件**: `server/agui/agui.go`（修改）

- 新增 `WithEnableContextUsage()` 和 `WithContextUsagePath()` Option
- 在 `newService()` 中注册 context usage 路由

**文件**: `server/agui/options.go`（修改）

- 新增 `contextUsageEnabled`、`contextUsagePath` 字段

#### 4. Session Service 扩展

**文件**: `session/session.go`（修改）

- 新增 `ComputeContextUsage(ctx context.Context, key Key, modelName string) (*ContextUsage, error)` 便捷方法
- 该方法从 Session 的 Events 中提取所有消息，按角色分类计算 token 数

#### 5. Token 计数优化

**文件**: `model/token_tailor.go`（修改）

- 新增 `CountTokensByCategory(ctx context.Context, messages []Message) (map[string]int, error)` 便捷方法
- 按 `Message.Role` 分类统计，返回 `{"system": N, "user": N, "assistant": N, "tool": N}` 的 map
- `ReasoningContent` 的 token 数单独计入 "reasoning" 类别

### 数据流

```
前端请求 → AG-UI SSE/REST API → Runner/Service
                                    ↓
                            Session.GetSession()
                                    ↓
                        遍历 Session.Events 提取 Messages
                                    ↓
                    TokenCounter.CountTokens() 按角色分类统计
                                    ↓
                    LookupContextWindow() 获取窗口大小
                                    ↓
                        构造 ContextUsage 结构体
                                    ↓
                    CustomEvent("context_usage") → SSE 推送
                    或 REST API JSON 响应 → 前端渲染
```

### 前端消费方式

1. **实时推送**：前端监听 SSE 事件流中的 `CustomEvent`，当 `name === "context_usage"` 时解析 `value` 字段获取 `ContextUsage` 数据
2. **主动查询**：前端通过 POST `/api/context-usage` 端点按需获取当前上下文使用情况
3. **渲染**：前端根据 `ContextUsage.breakdown` 渲染堆叠柱状图，不同类别用不同颜色标识

## 假设与决策

1. **Token 计数精度**：使用现有 `SimpleTokenCounter` 启发式估算，精度足够用于可视化展示。如需更高精度，可通过 `SetTokenCounterFromModel` 注册 tiktoken 实现。
2. **性能考量**：每次 LLM 响应后计算上下文使用情况需要遍历事件列表并逐条计数，对于长会话可能有性能开销。建议：
   - 默认关闭实时推送，仅通过 REST API 按需查询
   - 如开启实时推送，可缓存上次计算结果，增量更新
3. **上下文窗口来源优先级**：`model.Info.ContextWindow` > `LookupModelContextWindow(modelName)` > `defaultContextWindow(128K)`
4. **Token Tailoring 配置影响**：计算剩余可用 token 时需考虑 `ReserveOutputTokens` 和 `SafetyMarginRatio`，即 `EffectiveRemaining = ContextWindow * MaxInputTokensRatio * (1 - SafetyMarginRatio) - UsedTokens`
5. **AG-UI 协议兼容性**：使用 `CustomEvent` 扩展，不修改 AG-UI 协议核心定义，确保向后兼容
6. **Session 事件 vs Messages**：Session 存储的是 Events（包含 Response），需要从 Events 中提取 Messages 来计算 token。对于已摘要的事件，只计算摘要部分。

## 验证步骤

1. **单元测试**：
   - `ComputeContextUsage()` 函数测试：验证不同角色消息的分类统计正确性
   - `CountTokensByCategory()` 函数测试：验证按角色分类的 token 计数
   - 边界情况：空会话、超大消息、未知模型名

2. **集成测试**：
   - 验证 AG-UI Runner 在 `contextUsageEnabled=true` 时正确推送 CustomEvent
   - 验证 REST API 端点返回正确的 ContextUsage JSON
   - 验证 SSE 事件流中 CustomEvent 的格式正确

3. **端到端测试**：
   - 使用 AG-UI 前端客户端连接后端，发送多轮对话
   - 验证上下文使用情况数据随对话进行正确更新
   - 验证 token 计数与 LLM API 返回的 Usage 数据基本一致（允许启发式估算误差）
