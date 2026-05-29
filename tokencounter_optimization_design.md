# TokenCounter 优化方案：智能体级依赖传递

## 一、设计原则

1. **不要求统一三处上下文管理机制使用同一个 TokenCounter** — 各机制（压缩、裁剪、摘要）可使用不同精度的 counter
2. **TokenCounter 应是智能体级或模型级共享的** — 非必须创建自己独有的计数器外，优先使用被计算对象所提供的计数器
3. **先梳理各处使用目的** — 区分"构造默认"、"外部覆盖"、"nil 兜底"、"业务使用"，再决定改造方式

## 二、各处 TokenCounter 使用目的分类

### 类型 A：对象构造默认值（已提供外部设置方式）— 不修改

| 位置 | 所属对象 | 使用目的 | 外部覆盖方式 |
|------|----------|----------|-------------|
| `openai/options.go:L146` | OpenAI 模型默认选项 | 构造时默认 | `openai.WithTokenCounter()` |
| `anthropic/options.go:L110` | Anthropic 模型默认选项 | 构造时默认 | `anthropic.WithTokenCounter()` |
| `gemini/options.go:L94` | Gemini 模型默认选项 | 构造时默认 | `gemini.WithTokenCounter()` |
| `ollama/options.go:L110` | Ollama 模型默认选项 | 构造时默认 | `ollama.WithTokenCounter()` |
| `hunyuan/options.go:L30` | Hunyuan 模型默认选项 | 构造时默认 | `hunyuan.WithTokenCounter()` |
| `huggingface/options.go:L97` | HuggingFace 模型默认选项 | 构造时默认 | `huggingface.WithTokenCounter()` |
| `checker.go:L39` | 会话摘要检查器 | 构造时默认 | `summary.SetTokenCounter()` |

**不修改理由**：这些是合理的默认值，且已有完整的外部覆盖机制。当外部通过 `WithTokenCounter()` 传入时，默认值被覆盖。

### 类型 B：运行时 nil 兜底 — 保持不变，增加警告日志

| 位置 | 所属函数 | 触发条件 | 是否实际触发 |
|------|----------|----------|-------------|
| `context_compact.go:L369` | `truncateOversizedToolResultMessageWithCounter()` | counter 参数为 nil | 几乎不触发（上游已从 config 传入） |
| `context_compact.go:L488` | `compactHistoricalToolResultMessageWithCounter()` | counter 参数为 nil | 几乎不触发（同上） |
| `llmflow.go:L1372` | `shouldSyncCompactContext()` | counter 参数为 nil | 几乎不触发（从 config 传入） |
| `token_tailor.go:L694` | `buildPrefixSum()` | tokenCounter 为 nil | 几乎不触发（从策略构造传入） |

**不修改理由**：这些是防御性编程的 nil 兜底，上游已确保 counter 不为 nil。保持 `NewSimpleTokenCounter()` 作为兜底是合理的。

**新增要求**：在走到兜底流程时输出警告日志，方便排查问题。

**具体改动**：每处 nil 兜底在创建 `NewSimpleTokenCounter()` 前增加 `log.WarnfContext`：

```go
// context_compact.go:L369 改为：
if counter == nil {
    log.WarnfContext(ctx, "token counter is nil in truncateOversizedToolResultMessageWithCounter, using SimpleTokenCounter fallback")
    counter = model.NewSimpleTokenCounter()
}
```

同理处理 L488、llmflow.go:L1372、token_tailor.go:L694。

**改动量**：4 处各增加 1 行日志，共 4 行。

### 类型 C：normalize 阶段兜底 — 需要修改

| 位置 | 所属函数 | 使用目的 | 问题 |
|------|----------|----------|------|
| `context_compact.go:L81` | `normalizeContextCompactionConfig()` | Agent 未配置 ContextCompactionTokenCounter 时的兜底 | 使用默认 4.0 比例，中文场景不准确 |

**修改方式**：当 Agent 未配置时，从模型级获取 counter 作为兜底，而非直接创建新的 SimpleTokenCounter。

### 类型 D：便捷函数硬编码 — 需要修改

| 位置 | 便捷函数 | 委托给 | 问题 |
|------|----------|--------|------|
| `context_compact.go:L349` | `truncateOversizedToolResultMessage()` | `truncateOversizedToolResultMessageWithCounter()` | 硬编码 NewSimpleTokenCounter() |
| `context_compact.go:L469` | `compactHistoricalToolResultMessage()` | `compactHistoricalToolResultMessageWithCounter()` | 硬编码 NewSimpleTokenCounter() |

**修改方式**：这两个便捷函数仅在 `context_compact.go` 内部使用，且调用者已持有 `config.TokenCounter`。应删除便捷函数，改为直接调用 WithCounter 版本。

### 类型 E：插件独立创建 — 需要修改

| 位置 | 所属对象 | 使用目的 | 问题 |
|------|----------|----------|------|
| `approval/approval.go:L55` | Approval 插件 | 安全阈值判断 | 硬编码，无法注入 |
| `unsafeintent/unsafeintent.go:L38` | UnsafeIntent 插件 | 安全阈值判断 | 硬编码，无法注入 |
| `promptinjection/promptinjection.go:L38` | PromptInjection 插件 | 安全阈值判断 | 硬编码，无法注入 |

**修改方式**：为每个插件增加 `WithTokenCounter()` Option，默认使用 `model.NewSimpleTokenCounter()`，但允许外部注入。

## 三、改造方案

### 改动 1：OpenClaw App 层创建共享 counter 并传递

**文件**：`openclaw/app/backends.go`

**当前代码**（L177-189）：
```go
if opts.SessionSummaryApproxRunesPerToken > 0 {
    summary.SetTokenCounter(
        model.NewSimpleTokenCounter(
            model.WithApproxRunesPerToken(
                opts.SessionSummaryApproxRunesPerToken,
            ),
        ),
    )
}
```

**改为**：
```go
var sharedTokenCounter model.TokenCounter

if opts.SessionSummaryApproxRunesPerToken > 0 {
    sharedTokenCounter = model.NewSimpleTokenCounter(
        model.WithApproxRunesPerToken(
            opts.SessionSummaryApproxRunesPerToken,
        ),
    )
    summary.SetTokenCounter(sharedTokenCounter)
}
```

**文件**：`openclaw/app/app.go`

**在创建 Agent 的 opts 中增加**（约 L2451 附近）：
```go
if sharedTokenCounter != nil {
    opts = append(opts,
        llmagent.WithContextCompactionTokenCounter(sharedTokenCounter),
    )
}
```

**在创建 Model 时增加**（`newOpenAIModel` 函数，L3091 附近）：

需要将 `sharedTokenCounter` 传递到模型工厂。由于 `newOpenAIModel` 通过 `registry.ModelFactory` 调用，需要扩展 `registry.ModelSpec` 或通过其他方式传递。

**推荐方式**：在 `registry.ModelSpec` 中增加 `TokenCounter` 字段：

```go
// openclaw/registry/registry.go
type ModelSpec struct {
    Type                 string
    Name                 string
    BaseURL              string
    OpenAIVariant        string
    DebugRecorderEnabled bool
    ContextWindow        int
    Config               map[string]any
    TokenCounter         model.TokenCounter  // 新增
}
```

然后在 `newOpenAIModel` 中使用：
```go
if spec.TokenCounter != nil {
    opts = append(opts, openai.WithTokenCounter(spec.TokenCounter))
}
```

### 改动 2：context_compact.go 便捷函数改用 config.TokenCounter

**文件**：`internal/flow/processor/context_compact.go`

**当前**（L115-122 附近，applyHistoricalToolResultPass 内部）：
```go
compactHistoricalToolResultMessage(ctx, msg, cfg.ToolResultMaxTokens)
```

**改为**：
```go
compactHistoricalToolResultMessageWithCounter(ctx, msg, cfg.ToolResultMaxTokens, cfg.TokenCounter)
```

同理，`applyOversizedToolResultPass` 内部的调用也改为 WithCounter 版本。

**然后删除两个便捷函数**（L460-471 和 L340-351）：
```go
// 删除：不再需要无 Counter 版本的便捷函数
func compactHistoricalToolResultMessage(...)
func truncateOversizedToolResultMessage(...)
```

### 改动 3：安全插件增加 WithTokenCounter Option

以 `approval` 为例（其他两个同理）：

**文件**：`plugin/guardrail/approval/approval.go`

```go
// 新增 Option 类型
type Option func(*options)

type options struct {
    tokenCounter model.TokenCounter
}

func WithTokenCounter(counter model.TokenCounter) Option {
    return func(o *options) {
        if counter != nil {
            o.tokenCounter = counter
        }
    }
}

// 修改 New 函数
func New(reviewer Reviewer, opts ...Option) (*Plugin, error) {
    if reviewer == nil {
        return nil, fmt.Errorf("newing approval plugin: reviewer is nil")
    }
    o := options{
        tokenCounter: model.NewSimpleTokenCounter(),  // 默认值
    }
    for _, opt := range opts {
        opt(&o)
    }
    return &Plugin{
        name:              "approval",
        reviewer:          reviewer,
        defaultToolPolicy: opts.defaultToolPolicy,
        toolPolicies:      opts.toolPolicies,
        tokenCounter:      o.tokenCounter,
    }, nil
}
```

**注意**：当前 `approval.New()` 的签名是 `New(reviewer Reviewer) (*Plugin, error)`，需要改为可变参数 Option 模式。这会改变公开 API，需要评估兼容性。

**兼容方案**：保持原有签名不变，增加 `NewWithOptions()` 函数：
```go
func New(reviewer Reviewer) (*Plugin, error) {
    return NewWithOptions(reviewer)
}

func NewWithOptions(reviewer Reviewer, opts ...Option) (*Plugin, error) {
    // ... 新逻辑
}
```

## 四、传递规则汇总

### 4.1 智能体级 → 上下文压缩

```
OpenClaw App (sharedTokenCounter)
  → llmagent.WithContextCompactionTokenCounter(counter)
    → processor.WithContextCompactionTokenCounter(counter)
      → ContentRequestProcessor.ContextCompactionConfig.TokenCounter
        → normalizeContextCompactionConfig()  // L81: nil 时使用此 counter
        → compactHistoricalToolResultMessageWithCounter()  // L349: 直接传入
        → truncateOversizedToolResultMessageWithCounter()  // L469: 直接传入
        → shouldSyncCompactContext()  // L1070: 直接传入
```

**规则**：优先使用外部传入的 counter；Agent 未配置时，由 normalize 创建默认值兜底。

### 4.2 模型级 → 令牌裁剪

```
OpenClaw App (sharedTokenCounter)
  → registry.ModelSpec.TokenCounter
    → newOpenAIModel() → openai.WithTokenCounter(spec.TokenCounter)
      → openai.Model.tokenCounter
        → NewMiddleOutStrategy(counter)
          → buildPrefixSum(ctx, s.tokenCounter, messages)
```

**规则**：优先使用外部传入的 counter；Model 未配置时，使用 defaultOptions 中的默认值。

### 4.3 进程级 → 会话摘要

```
OpenClaw App (sharedTokenCounter)
  → summary.SetTokenCounter(counter)
    → checker.defaultTokenCounter
      → checkTokenThresholdFromText()
```

**规则**：使用进程级全局变量，应用启动时设置一次。

### 4.4 插件级 → 安全阈值

```
用户代码
  → approval.NewWithOptions(reviewer, approval.WithTokenCounter(counter))
    → Plugin.tokenCounter
      → currentinput.countTranscriptTokens()
```

**规则**：默认使用 SimpleTokenCounter(4.0)；用户可通过 Option 注入自定义 counter。

## 五、改动量汇总

| 改动 | 文件 | 新增行 | 修改行 | 影响 |
|------|------|--------|--------|------|
| 改动1a | `openclaw/app/backends.go` | +5 | -3 | 创建共享 counter |
| 改动1b | `openclaw/app/app.go` | +4 | 0 | Agent 传入 counter |
| 改动1c | `openclaw/registry/registry.go` | +2 | 0 | ModelSpec 增加 TokenCounter |
| 改动1d | `openclaw/app/app.go` (newOpenAIModel) | +3 | 0 | Model 传入 counter |
| 改动2 | `internal/flow/processor/context_compact.go` | 0 | -4 | 删除便捷函数，改用 WithCounter 版本 |
| 改动3a | `plugin/guardrail/approval/approval.go` | +18 | -2 | 增加 Option 模式 |
| 改动3b | `plugin/guardrail/unsafeintent/unsafeintent.go` | +18 | -2 | 增加 Option 模式 |
| 改动3c | `plugin/guardrail/promptinjection/promptinjection.go` | +18 | -2 | 增加 Option 模式 |
| **合计** | **8 个文件** | **+68** | **~13** | — |

## 六、不需要修改的位置

| 位置 | 理由 |
|------|------|
| 6 个模型的 `defaultOptions` | 类型 A：构造默认值，已有 WithTokenCounter 覆盖 |
| `checker.go:L39` | 类型 A：构造默认值，已有 SetTokenCounter 覆盖 |
| `context_compact.go:L369,L488` | 类型 B：nil 兜底，上游已确保不为 nil |
| `llmflow.go:L1372` | 类型 B：nil 兜底，上游已确保不为 nil |
| `token_tailor.go:L694,L698` | 类型 B：nil 兜底，策略构造时已传入 |
| 所有 `*_test.go` | 测试中使用默认值是合理的 |

## 七、中文场景配置示例

改造后，用户只需在 OpenClaw 配置中设置一个参数：

```yaml
session_summary_approx_runes_per_token: 1.5
```

即可自动影响以下所有组件：

| 组件 | 改造前 | 改造后 |
|------|--------|--------|
| 上下文压缩 | SimpleTokenCounter(4.0) | SimpleTokenCounter(1.5) |
| 令牌裁剪 | SimpleTokenCounter(4.0) | SimpleTokenCounter(1.5) |
| 会话摘要 | SimpleTokenCounter(1.5) | SimpleTokenCounter(1.5) |
| 安全插件 | SimpleTokenCounter(4.0) | SimpleTokenCounter(4.0)（需用户显式传入） |

安全插件不自动使用共享 counter 的理由：安全插件的 token 计数用于安全阈值判断，与上下文管理的计数目的不同，应由用户自行决定是否注入。

## 八、决策记录：不新增 TokenCounterProvider 接口

### 8.1 决策

**不实施**第八章原方案（新增 `TokenCounterProvider` 可选接口 + Agent 自动回退逻辑）。当前方案（改动 1-3）已足够解决核心问题。

### 8.2 原因分析

#### 当前方案（改动 1-3）已闭环

当前方案通过 OpenClaw App 层显式传递 `sharedTokenCounter`，已覆盖所有需要 counter 的组件：

```
sharedTokenCounter ─┬→ openai.WithTokenCounter()          → 令牌裁剪 ✅
                    ├→ llmagent.WithContextCompactionTokenCounter() → 上下文压缩 ✅
                    └→ summary.SetTokenCounter()           → 会话摘要 ✅
```

**OpenClaw App 用户**（主要使用场景）：只需配置 `session_summary_approx_runes_per_token: 1.5`，App 层自动将 counter 传递到所有组件。无需关心 counter 从哪里来。

#### 不做第八章的影响

| # | 影响项 | 严重程度 | 说明 |
|---|--------|---------|------|
| 1 | 直接 API 用户需在两处配置 counter | **低** | 直接使用 `llmagent.New()` 的用户需同时配置 `openai.WithTokenCounter()` 和 `llmagent.WithContextCompactionTokenCounter()`。但这类用户通常是高级用户，能理解两处配置的关系。且文档已有说明（[session.md:L1910](file:///workspace/docs/mkdocs/zh/session.md#L1910)）。 |
| 2 | 遗漏配置时上下文压缩回退到 4.0 比例 | **低** | 若用户只配置了模型级 counter 但忘记配置 Agent 级，上下文压缩会回退到 `SimpleTokenCounter(4.0)`。但 `normalizeContextCompactionConfig()` 已有兜底逻辑（类型 C 改动会进一步优化），且类型 B 的警告日志会在触发时提醒用户。 |
| 3 | 无法自动保证模型与 Agent 使用同一 counter | **低** | 需用户/App 层显式保证一致性。但 OpenClaw App 层已通过 `sharedTokenCounter` 变量确保一致性，直接 API 用户也可轻松做到。 |
| 4 | graphagent 也需独立配置 | **低** | `graphagent` 也有 `WithContextCompactionTokenCounter()` 选项（[graphagent/option.go:L331](file:///workspace/agent/graphagent/option.go#L331)），与 `llmagent` 同理，需显式配置。 |

#### 做第八章的代价

| # | 代价项 | 说明 |
|---|--------|------|
| 1 | 新增公开接口 `TokenCounterProvider` | 一旦发布不可轻易移除，属于 API 承诺 |
| 2 | 6 个模型实现各需新增 `TokenCounter()` 方法 | 增加维护负担，且暴露了模型内部实现细节 |
| 3 | Agent 内部类型断言逻辑 | 增加代码复杂度，需处理 `TokenCounterProvider` 未实现的情况 |
| 4 | 多模型场景的歧义未解决 | 方案 C 仍需选择用哪个模型的 counter，并未真正消除多模型问题 |
| 5 | 改动量增加 54% | 从 8 文件 +68 行增至 15 文件 +104 行 |

### 8.3 结论

**YAGNI 原则**：当前方案已解决核心问题（中文场景 counter 不一致），额外引入 `TokenCounterProvider` 接口的收益（自动回退便利性）不足以抵消其代价（新增公开 API + 增加复杂度 + 暴露内部实现）。

若未来出现以下场景，可再考虑引入 `TokenCounterProvider`：
- 大量直接 API 用户反馈遗漏配置 Agent 级 counter
- 需要更细粒度的 counter 自动发现机制
- Model 接口本身需要暴露更多内部状态

### 8.4 保留的参考信息

原第八章的三种方案分析（方案 A/B/C）作为未来参考保留，不纳入当前实施范围。
