# trpc-agent-go 智能体框架上下文管理功能全景分析

> 基于源码深度分析，涵盖所有上下文管理措施的触发条件、处理策略、作用对象、意图与副作用。

---

## 目录

1. [功能一：Token 裁剪（Token Tailoring）](#功能一token-裁剪token-tailoring)
2. [功能二：会话摘要（Session Summarization）](#功能二会话摘要session-summarization)
3. [功能三：消息序列验证与修复（Message Sequence Validation）](#功能三消息序列验证与修复message-sequence-validation)
4. [功能四：会话事件过滤（Session Event Filtering）](#功能四会话事件过滤session-event-filtering)
5. [功能五：摘要作用域过滤（Summary Scope Filtering）](#功能五摘要作用域过滤summary-scope-filtering)
6. [功能六：Token 计数与校准（Token Counting & Calibration）](#功能六token-计数与校准token-counting--calibration）
7. [功能间协作关系](#功能间协作关系)

---

## 功能一：Token 裁剪（Token Tailoring）

**核心文件**：
- `model/token_tailor.go` — 裁剪策略接口与三种实现
- `model/internal/model/token_tailor.go` — 预算计算公式
- `model/openai/openai.go` — 裁剪的调用入口（`applyTokenTailoring`）
- `model/provider/options.go` — 裁剪配置选项

### 触发条件

**前提条件（全部满足才触发）**：
1. 模型配置中 `enableTokenTailoring = true`（通过 `WithEnableTokenTailoring(true)` 开启）
2. 当前请求的 `request.Messages` 不为空
3. 消息总 token 数超过 `maxInputTokens` 预算

**maxInputTokens 预算计算优先级**：
1. 用户显式配置的 `maxInputTokens`（通过 `WithMaxInputTokens`）
2. 自动计算（当 `maxInputTokens <= 0` 时）：
   ```
   safetyMargin = contextWindow × safetyMarginRatio(默认10%)
   calculatedMax = contextWindow - reserveOutputTokens(默认2048) - protocolOverheadTokens(默认512) - safetyMargin
   ratioLimit = contextWindow × maxInputTokensRatio(默认100%)
   maxInputTokens = max(min(calculatedMax, ratioLimit), inputTokensFloor(默认1024))
   ```
3. 最终值受 `hardInputBudget` 上限约束：
   ```
   hardBudget = contextWindow - outputReserveTokens - protocolOverheadTokens - safetyMargin
   maxInputTokens = min(maxInputTokens, hardBudget)
   ```
4. 若为自动预算，还需减去工具（tools）的 token 估算

**contextWindow 解析优先级**：
1. 模型实例配置的 `contextWindow`
2. 模型名称注册表查询（`imodel.ResolveContextWindow`）
3. 默认值

### 处理策略（3种）

#### 策略1：MiddleOutStrategy（中间裁剪，默认策略）

**原理**：基于 "Lost-in-the-Middle" 现象——LLM 对序列首尾的内容注意力更高，中间内容容易被忽略。

**作用对象**：消息列表中"用户锚定轮次"（user-anchored rounds），即以 user 消息开始、到下一个 user 消息之前的一组消息。

**处理流程**：
1. **保护头部**：所有连续的 system 消息（`preservedHead`）始终保留
2. **构建轮次**：将非 system 消息按 user 消息为锚点划分为轮次
3. **保护尾部**：最后一个轮次始终保留
4. **中间裁剪**：从保留的中间轮次中，找到中间位置的轮次，移除之
5. **循环**：重复移除中间轮次，直到总 token 数 ≤ maxInputTokens
6. **兜底**：若裁剪后仍超预算，调用 `ensureTailoredWithinBudget` 保留 system 前缀 + 最后一个有效轮次

**意图**：优先保留 system 指令（头部）和最近交互（尾部），这两部分对生成质量影响最大；移除中间部分损失最小。

**副作用**：
- 丢失中间轮次的对话历史，可能导致模型对早期对话细节失忆
- 裁剪后消息序列可能不合法，需经 `validateAndFixMessageSequence` 修复
- 极端情况下（最小保护上下文仍超预算），返回 `tokenTailoringOverflowError`，但仍会尽力返回最小保护上下文

#### 策略2：HeadOutStrategy（头部裁剪）

**作用对象**：从最老的轮次开始，依次移除

**处理流程**：
1. 保护 system 消息和最后一个轮次
2. 从第 0 个轮次开始，依次标记为移除（`keep[dropIdx] = false`）
3. 每移除一个轮次，递减 currentTotal
4. 直到 total ≤ maxInputTokens 或只剩最后一个轮次

**意图**：保留最近的对话上下文，适合"只关心最近对话"的场景。类似滑动窗口效果。

**副作用**：
- 丢失最早的对话历史，模型无法回忆早期交互
- 适合短期对话密集型场景，不适合需要长期记忆的场景

#### 策略3：TailOutStrategy（尾部裁剪）

**作用对象**：从倒数第二个轮次开始，向头部方向依次移除

**处理流程**：
1. 保护 system 消息和最后一个轮次
2. 从 `lastRoundIdx - 1` 开始，依次标记为移除
3. 每移除一个轮次，递减 currentTotal
4. 直到 total ≤ maxInputTokens 或无更多轮次可移除

**意图**：保留最早的对话历史和 system 指令，适合需要保持初始指令完整性的场景。

**副作用**：
- 丢失最近的对话上下文，模型可能无法理解当前对话状态
- 实际使用较少，因为丢失最近交互通常影响最大

### 三种策略的共同特性

| 特性 | 说明 |
|------|------|
| 保护头部 system 消息 | `calculatePreservedHeadCount` 计算连续 system 消息数量，始终保留 |
| 保护最后一个轮次 | 最后一个用户锚定轮次始终不被主动移除 |
| 前缀和优化 | 使用 `buildPrefixSum` 构建前缀和数组，O(1) 增量更新 token 计数 |
| 兜底机制 | `ensureTailoredWithinBudget` 确保至少返回 system 前缀 + 最后有效轮次 |
| 溢出错误 | 最小保护上下文仍超预算时返回 `tokenTailoringOverflowError` |
| 消息修复 | 裁剪后调用 `validateAndFixMessageSequence` 确保消息序列合法 |

### 裁剪溢出时的处理

当 `tailoringStrategy.TailorMessages` 返回 error 时：
- **若有返回消息**（`len(tailored) > 0`）：使用尽力而为的结果，记录警告日志
- **若无返回消息**：记录警告日志，不修改原始请求（可能导致 API 调用失败）

---

## 功能二：会话摘要（Session Summarization）

**核心文件**：
- `session/summary/summary.go` — 摘要接口定义
- `session/summary/summarizer.go` — 摘要生成器实现
- `session/summary/checker.go` — 触发条件检查器
- `session/summary/options.go` — 配置选项
- `session/summary/hook.go` — 前后置钩子
- `session/internal/summary/summary.go` — 摘要调度与持久化

### 触发条件（4种检查器，可组合）

#### 检查器1：CheckEventThreshold（事件数量阈值）

**触发条件**：自上次摘要以来的增量事件（delta events）数量 > 配置的 `eventCount`

**配置方式**：`WithEventThreshold(eventCount)`

**作用对象**：
- 增量事件：通过 `filterDeltaEvents` 获取上次摘要边界之后的事件
- 阈值事件：通过 `filterThresholdEventsForSession` 过滤，全会话检查只计算主智能体活动，分支检查计算分支及后代

**意图**：当对话轮次积累到一定数量时触发摘要，防止上下文无限增长

**副作用**：
- 事件数量不等于 token 数量，可能事件少但 token 多（长消息），或事件多但 token 少
- 需与其他检查器组合使用效果更好

#### 检查器2：CheckTimeThreshold（时间阈值）

**触发条件**：自最近一个相关事件以来经过的时间 > 配置的 `interval`

**配置方式**：`WithTimeThreshold(interval)`

**作用对象**：会话中过滤后的最后一个事件的时间戳

**意图**：在对话空闲一段时间后触发摘要，适合"对话暂停时压缩"的场景

**副作用**：
- 时间间隔与上下文大小无直接关系，可能在上下文很小时就触发摘要
- 适合与其他检查器 AND 组合使用

#### 检查器3：CheckTokenThreshold（固定 Token 阈值）

**触发条件**：自上次摘要以来的增量事件的估算 token 数 > 配置的 `tokenCount`

**配置方式**：`WithTokenThreshold(tokenCount)`

**作用对象**：
- 优先使用注入的 token 阈值消息（`tokenThresholdConversationTextStateKey`）
- 否则从增量事件中提取对话文本并计算 token

**意图**：基于 token 数量精确控制摘要触发时机，与模型上下文窗口直接相关

**副作用**：
- token 计数为估算值，可能不精确
- 旧版 `CheckTokenThreshold` 不接受 context，使用 `context.Background()`；新版 `CheckTokenThresholdContext` 支持 context

#### 检查器4：CheckContextThreshold（动态上下文窗口阈值，推荐）

**触发条件**：增量事件的估算 token 数 > 模型上下文窗口 × 阈值比例（默认 50%）

**配置方式**：`WithContextThreshold(opts...)`

**可配置参数**：
| 参数 | 默认值 | 说明 |
|------|--------|------|
| `thresholdRatio` | 0.5 (50%) | 触发摘要的上下文窗口占比 |
| `fallbackContextWindow` | 8192 | 无法识别模型时的回退窗口大小 |
| `minTokenThreshold` | 2000 | 绝对最小 token 阈值，防止小窗口过早触发 |

**contextWindow 解析优先级**：
1. per-run 模型上下文窗口覆盖（`agent.ModelContextWindowFromRunOptions`）
2. invocation 中的模型实例配置 → 注册表查询
3. 用户配置的 fallback
4. 框架默认值 8192

**意图**：零配置体验，自动根据模型能力决定何时压缩，切换模型时阈值自动调整

**副作用**：
- 不同模型上下文窗口差异大，切换模型可能导致摘要频率剧烈变化
- 估算 token 与实际 token 可能有偏差

#### 检查器组合逻辑

- **AND 组合**（`ChecksAll` / `WithChecksAll`）：所有检查器都返回 true 才触发
- **OR 组合**（`ChecksAny` / `WithChecksAny`）：任一检查器返回 true 即触发
- **默认行为**：summarizer 内部所有 checks 为 AND 关系（`allContextChecks`）

### 摘要生成流程

1. **过滤事件**：
   - `filterDeltaEvents`：获取上次摘要边界之后的增量事件
   - `filterEventsForSummary`：应用 `SkipRecentFunc` 跳过最近 N 个事件
   - `filterSummaryInputEventsForSession`：按作用域过滤

2. **检查可摘要内容**：`hasSummarizableContent` 检查是否有文本、工具调用或工具结果

3. **提取对话文本**：`extractConversationText` 将事件转为对话文本格式
   - user 消息：`user: 内容`
   - assistant 工具调用：`assistant: [Called tool: name with args: {args}]`
   - tool 结果：`tool: [toolName returned: content]`
   - 支持自定义格式化器（`WithToolCallFormatter` / `WithToolResultFormatter`）

4. **Pre-hook**：可选的前置钩子，可修改对话文本或上下文

5. **LLM 生成摘要**：调用配置的模型生成摘要
   - 支持自定义 prompt（`WithPrompt`），必须包含 `{conversation_text}` 占位符
   - 支持系统 prompt（`WithSystemPrompt`），不可包含 `{conversation_text}`
   - 支持最大摘要字数限制（`WithMaxSummaryWords`），prompt 中需包含 `{max_summary_words}`
   - 非流式生成

6. **记录边界**：`recordLastIncludedBoundary` 将最后包含的事件时间戳和 ID 写入 session state

7. **Post-hook**：可选的后置钩子，可修改摘要文本

### SkipRecent 机制

**配置方式**：`WithSkipRecent(skipFunc)`

**作用**：跳过最近 N 个事件不参与摘要，保留最新交互的完整性

**处理逻辑**：
1. 调用 `skipFunc(events)` 获取跳过数量
2. 截取 `events[:len(events)-skipCount]`
3. 检查截取后的事件是否包含 user 消息
4. 若无 user 消息但存在预置的摘要上下文，保留
5. 否则返回空列表（不生成摘要）

**意图**：避免摘要覆盖正在进行的对话轮次，保持最新交互的原始性

**副作用**：可能导致部分事件既不被摘要也不被保留在上下文中（如果裁剪也移除了它们）

### 摘要边界管理

**边界类型**：`SummaryBoundary`，包含：
- `FilterKey`：作用域标识
- `CutoffAt`：截断时间戳
- `LastEventID`：最后包含的事件 ID（精确边界）

**边界选择优先级**：
1. summarizer 记录的 `lastIncludedBoundary`（从 session state 读取）
2. 增量计算得到的 `latestBoundary`

**增量摘要**：
- 首次摘要：处理全部事件
- 后续摘要：只处理上次边界之后的事件
- 前次摘要文本作为合成 system 事件前置（`prependPrevSummary`），保持上下文连续性

### 摘要并发控制

- 同一 session + filterKey 的摘要生成通过 `summaryLockGroup` 串行化
- 使用带引用计数的二值信号量实现
- 支持 context 取消

### 摘要级联策略

`CreateSessionSummaryWithCascade` 支持一次触发生成多个作用域的摘要：
- 当所有事件都属于同一 filterKey 时，只调用一次 LLM，然后将结果复制到全会话摘要
- 当事件分布在多个 filterKey 时，并行生成各作用域的摘要

---

## 功能三：消息序列验证与修复（Message Sequence Validation）

**核心文件**：`model/message_validator.go`

### 触发条件

每次裁剪策略执行完毕后自动调用 `validateAndFixMessageSequence(result)`

### 处理策略（4步流水线）

#### 步骤1：removeInvalidRoleMessages — 移除无效角色消息

**作用对象**：角色不属于 `system`/`user`/`assistant`/`tool` 的消息

**处理**：直接移除

**意图**：确保消息序列只包含 API 合法的角色

**副作用**：可能移除扩展角色的消息（如果框架使用了非标准角色）

#### 步骤2：ensureNonEmptyContent — 确保内容非空

**作用对象**：`Content` 为空但有 `ContentParts`/`ToolCalls`/`ReasoningContent` 的消息

**处理**：将 `Content` 设为空格占位符 `" "`

**意图**：API 要求消息 Content 不能为空，但工具调用等消息可能没有文本内容

**副作用**：引入一个空格字符，对模型输出影响极小

#### 步骤3：filterValidRounds — 过滤有效轮次

**作用对象**：用户锚定轮次（以 user 消息开始的消息组）

**处理**：
1. 将消息分为 system 前缀和用户锚定轮次
2. 检查每个轮次是否有效：
   - 第一个非 system 消息必须是 user
   - user/tool 组和 assistant 组必须交替出现
3. 无效轮次整体移除

**意图**：API 要求消息角色交替出现（user/tool → assistant → user/tool → ...），整轮移除保持语义完整性

**副作用**：可能移除包含有用信息的轮次，但比部分移除更安全

#### 步骤4：ensureLastMessageIsUserOrTool — 确保最后一条消息是 user 或 tool

**作用对象**：消息序列末尾

**处理**：
1. 移除末尾的 system 消息
2. 若最后一条是 assistant，移除末尾连续的 assistant 消息组
3. 若结果为空，返回 nil

**意图**：API 要求最后一条消息必须是 user 或 tool（模型需要"回复"的目标）

**副作用**：可能移除最后的 assistant 回复，但这通常发生在裁剪后序列不完整的情况

---

## 功能四：会话事件过滤（Session Event Filtering）

**核心文件**：`session/session.go`

### 触发条件

在 `UpdateUserSession` 中追加新事件后自动调用 `ApplyEventFiltering(opts...)`

### 处理策略（3种，按顺序执行）

#### 策略1：EventTime 过滤

**配置方式**：`WithEventTime(time)`

**触发条件**：配置了非零的 `EventTime`

**作用对象**：时间戳早于 `EventTime` 的事件

**处理**：从事件列表中找到第一个时间戳 ≥ `EventTime` 的事件，截取其后的所有事件

**意图**：只保留指定时间之后的事件，实现时间窗口滑动

**副作用**：可能丢失时间窗口之前的重要上下文

#### 策略2：EventNum 过滤

**配置方式**：`WithEventNum(num)`

**触发条件**：配置了 `EventNum > 0` 且事件数量超过限制

**作用对象**：最老的（超出的）事件

**处理**：保留最后 `EventNum` 个事件

**意图**：限制事件数量，防止内存无限增长

**副作用**：丢失早期对话历史

#### 策略3：确保包含用户消息

**触发条件**：过滤后的事件列表中无 user 消息

**处理**：
1. 在过滤后的事件中查找第一个 user 消息，截取其后的所有事件
2. 若无 user 消息，从原始事件中查找最后一个 user 消息，前置到过滤后的事件列表
3. 若原始事件中也无 user 消息，清空事件列表

**意图**：确保事件列表至少包含一条 user 消息，否则模型无法正常工作

**副作用**：可能引入一条与后续事件不连续的 user 消息

### EnsureEventStartWithUser

**触发条件**：显式调用

**作用对象**：事件列表开头非 user 的事件

**处理**：从开头移除事件，直到找到第一条 user 消息；若无 user 消息则清空

**意图**：确保事件列表以 user 消息开头，符合 API 要求

**副作用**：移除开头的 system/assistant 消息

---

## 功能五：摘要作用域过滤（Summary Scope Filtering）

**核心文件**：
- `session/summary/checker.go` — 过滤函数
- `session/internal/summaryscope/scope.go` — 作用域元数据管理
- `session/internal/summary/summary.go` — 摘要调度

### 触发条件

在摘要检查和生成过程中自动应用

### 处理策略（2种过滤模式）

#### 模式1：filterEventsInScope — 分支作用域过滤

**作用对象**：事件列表中 `FilterKey` 不匹配的事件

**匹配规则**：
- `FilterKey` 为空的事件（合成事件）→ 保留
- `FilterKey` 精确匹配 `scopeKey` → 保留
- `FilterKey` 以 `scopeKey + delimiter` 为前缀（后代分支）→ 保留

**意图**：在多智能体/多分支场景中，只摘要当前分支及其后代的活动

**副作用**：其他分支的活动不被当前摘要覆盖，需要各自的摘要

#### 模式2：filterEventsWithExactKey — 精确键过滤

**作用对象**：事件列表中 `FilterKey` 不精确匹配的事件

**匹配规则**：
- `FilterKey` 为空的事件 → 保留
- `FilterKey` 精确匹配 `filterKey` → 保留

**意图**：全会话阈值检查只计算主智能体活动，排除子分支

**副作用**：子分支的活动不计入全会话阈值，可能导致全会话摘要触发延迟

### 作用域元数据

- `SetScopeFilterKey`：在临时 session 的 `ServiceMeta` 中设置作用域标识
- `GetScopeFilterKey`：读取作用域标识
- 仅用于摘要检查阶段的临时 session，非线程安全

---

## 功能六：Token 计数与校准（Token Counting & Calibration）

**核心文件**：
- `model/token_tailor.go` — `TokenCounter` 接口与 `SimpleTokenCounter`
- `model/openai/openai.go` — `RecordEstimate` 调用点

### Token 计数器类型

#### SimpleTokenCounter（启发式估算）

**原理**：`estimated tokens = UTF-8 rune count / approxRunesPerToken`

**模型路由表**：
| 模型前缀 | runes/token | 说明 |
|----------|-------------|------|
| qwen*, qwq* | 1.7 | 约 1.7 字符/token |
| deepseek* | 1.7 | 官方文档：1 中文字 ≈ 0.6 token |
| glm-5*, glm5* | 1.7 | 使用 Qwen 相同参数 |
| doubao*, hunyuan*, minimax* | 1.7 | 中文模型通用参数 |
| claude* | 1.7 | — |
| gpt-4o*, gpt-4.1* | 1.7 | — |
| gpt-4*, gpt-3.5* | 1.7 | — |
| llama*, yi-* | 1.7 | — |
| glm* | 1.8 | 智谱官方：1 token ≈ 1.8 中文字 |
| 其他 | 4.0 (默认) | 保守估计 |

**计数内容**：
- `message.Content`：主文本内容
- `message.ReasoningContent`：推理内容
- `message.ContentParts`：多模态文本部分
- `message.ToolCalls`：工具调用的 type + id + name + description + arguments

**意图**：在不依赖外部 tokenizer 的情况下提供快速估算

**副作用**：估算不精确，尤其是混合语言内容；通过安全边际（10%）补偿

#### 自定义 TokenCounter

**注册方式**：
1. `SetTokenCounterFromModel(fn)`：注册工厂函数（如 tiktoken），全局只注册一次（`sync.Once`）
2. `globalRegistry.Lookup(modelName)`：最长前缀匹配注册表
3. 回退到启发式路由

**校准机制**：
- `RecordEstimate`：在发送 API 请求前记录估算值
- `CalibrateFromActual`：根据 API 返回的实际用量计算修正因子
- 仅在 `applyTokenTailoring` 的最终结果上调用 `RecordEstimate`，避免内部裁剪操作的重复计数

**意图**：通过实际用量反馈逐步修正估算偏差

**副作用**：校准需要多次请求才能收敛；首次请求的估算可能偏差较大

---

## 功能间协作关系

```
用户发送消息
    │
    ▼
Session.UpdateUserSession ──────► ApplyEventFiltering (事件数量/时间过滤)
    │                                     │
    │                                     ▼
    │                              EnsureEventStartWithUser
    │
    ▼
Agent 运行循环
    │
    ├── ShouldSummarize? ─────────► CheckEventThreshold
    │                              CheckTimeThreshold
    │                              CheckTokenThreshold
    │                              CheckContextThreshold
    │         │
    │         │ (触发)
    │         ▼
    │   Summarize ────────────────► filterDeltaEvents (增量事件)
    │         │                    filterEventsForSummary (SkipRecent)
    │         │                    filterSummaryInputEventsForSession (作用域)
    │         │                    extractConversationText
    │         │                    PreSummaryHook
    │         │                    LLM 生成摘要
    │         │                    recordLastIncludedBoundary
    │         │                    PostSummaryHook
    │         │
    │         ▼
    │   摘要文本注入上下文
    │
    ▼
Model.GenerateContent
    │
    ├── applyTokenTailoring ──────► 计算 maxInputTokens 预算
    │         │                    选择裁剪策略
    │         ▼
    │   TailoringStrategy.TailorMessages
    │         │                    MiddleOut / HeadOut / TailOut
    │         │
    │         ▼
    │   validateAndFixMessageSequence
    │         │                    移除无效角色
    │         │                    确保内容非空
    │         │                    过滤有效轮次
    │         │                    确保最后消息是 user/tool
    │         │
    │         ▼
    │   RecordEstimate (校准)
    │
    ▼
API 请求发送
```

### 关键设计决策总结

1. **两层防线**：摘要（主动压缩）+ 裁剪（被动截断），摘要优先于裁剪
2. **保护优先级**：system 指令 > 最近交互 > 中间历史
3. **增量摘要**：只处理新增事件，前次摘要作为上下文保留
4. **尽力而为**：裁剪溢出时仍返回最小保护上下文，而非完全失败
5. **零配置体验**：`CheckContextThreshold` 根据模型能力自动调整阈值
6. **多作用域支持**：分支智能体可以有独立的摘要和阈值检查
7. **并发安全**：摘要生成通过锁串行化，session 读写通过 mutex 保护
