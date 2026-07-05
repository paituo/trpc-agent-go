# Paituo：基于模型最佳工作上下文区间的上下文策略调配

## 一、核心问题：模型存在最佳工作上下文区间

```
模型质量
  ↑
  │  ┌──────────────────┐
  │  │   最佳工作区间    │ ← 高质量、低延迟、低成本
  │  │  (30%-60%)        │
  └──┤──────────────────┤
     │                  │
  ↓  │  膨胀区          │  衰退区
  质 │  (60%-85%)       │  (>85%)
  量 │  延迟↑ 成本↑     │  遗漏↑ 幻觉↑
  下 │  精度开始下降     │  事实丢失严重
  降 │                  │  重复操作增多
     └──────────────────┘
     0%        50%  60%    85%      100%
              上下文窗口利用率
```

**关键发现**：模型在上下文利用率超过60%后，输出质量开始衰退；超过85%后，事实遗漏和幻觉显著增加。最佳工作区间为**30%-60%**。

---

## 二、整体工作方法：测→定→配→验→固

```
  ┌─────────┐    ┌─────────┐    ┌─────────┐    ┌─────────┐    ┌─────────┐
  │  1.测量  │ →  │  2.定界  │ →  │  3.调配  │ →  │  4.验证  │ →  │  5.固化  │
  │ 上下文  │    │ 最佳区间 │    │ 策略措施 │    │ 评估效果 │    │ 最佳配置 │
  └─────────┘    └─────────┘    └─────────┘    └─────────┘    └─────────┘
```

### 第1步：测量 — 建立上下文利用率基线

**方法**：通过 Langfuse 全链路追踪 + Context Budget Plugin，记录每次 LLM 调用的：
- 实际 token 数 vs 上下文窗口
- 上下文利用率（ratio）
- 各类内容占比（system prompt / 工具结果 / 对话历史 / 摘要）

**代码支撑**：
- `openclaw/conversation/context_budget_plugin.go` 第201行：`span.SetAttributes(context_budget.tokens, context_budget.window, context_budget.ratio)`
- `openclaw/app/langfuse.go` 的 Baggage 传播机制

**造价场景发现**：

| 内容类型 | 占比 | 特征 |
|---------|------|------|
| 工具结果（定额查询/文件读取） | 50-70% | 体积大、信息密度低 |
| 对话历史 | 20-30% | 含关键约束（费率/定额编号） |
| 系统提示+摘要 | 10-15% | 信息密度高、不可丢失 |

### 第2步：定界 — 确定最佳工作上下文区间

**评估维度**：

| 上下文利用率 | 任务完成率 | 事实准确性 | 延迟 | 成本 | 判定 |
|------------|----------|----------|------|------|------|
| <30% | 高 | 高 | 最低 | 低 | 浪费窗口 |
| 30%-50% | 高 | 高 | 低 | 适中 | **最佳区间** |
| 50%-60% | 高 | 高 | 适中 | 偏高 | **可接受区间** |
| 60%-70% | 中 | 开始下降 | 升高 | 高 | 需要干预 |
| 70%-85% | 下降 | 明显下降 | 高 | 很高 | 紧急干预 |
| >85% | 严重下降 | 幻觉/遗漏 | 很高 | 极高 | 不可接受 |

**造价场景的特殊考量**：
- 定额编号、取费标准等约束一旦确认不可遗忘 → 摘要必须保留关键约束
- 工具结果占比极大 → Compaction 压缩空间最大
- 单次编制任务可达50+轮 → 需要多次摘要循环

### 第3步：调配 — 针对性设计三层防线

基于最佳区间（30%-60%），反向推导每层防线的触发点：

```
上下文利用率:  0%────────50%──────60%────────70%──────────85%
              │         │        │          │            │
设计逻辑:     │    ← 在60%之前把利用率压回30-50%区间 →    │
              │         │        │          │            │
第1层         │  Compaction    │          │            │
Compaction    │  压缩历史工具结果│          │            │
              │  (50%触发)     │          │            │
              │         │        │          │            │
第2层         │         Summary │          │            │
Summary       │         增量摘要压缩│       │            │
              │         (60%触发) │         │            │
              │         │        │          │            │
第3层         │         │        │  Budget提醒  Budget强限 │
Budget        │         │        │  "拆子任务"  "停止批量" │
              │         │        │          │            │
```

**每层的选择依据**：

| 防线 | 触发点 | 选择依据 |
|------|--------|---------|
| Compaction@50% | 窗口半满时启动 | 工具结果占比50-70%，压缩工具结果是最高效的瘦身方式，无需LLM调用、零成本 |
| Summary@60% | 接近最佳区间上界 | Compaction只能压缩工具结果，当工具结果已压缩但历史轮次仍多时，需要摘要压缩语义信息 |
| Budget@70%/85% | 进入衰退区 | 主动提醒LLM调整行为，从"建议"升级到"强制"，防止进入不可接受区间 |

### 第4步：验证 — 评估组合效果

**验证方法**：

| 验证项 | 方法 | 指标 |
|-------|------|------|
| 上下文利用率是否回落 | Langfuse追踪 ratio 曲线 | 触发后 ratio 应回落至30-50% |
| 关键约束是否保留 | 造价任务中检查定额编号/费率准确性 | 约束零丢失 |
| 工具调用效率 | 对比启用/禁用Lua合并的轮次 | 同类调用轮次 ↓N倍 |
| 长任务完成率 | 50+轮造价编制任务端到端测试 | 完成率显著提升 |

### 第5步：固化 — 沉淀为最佳配置

---

## 三、原有能力 → Paituo 补齐完善

| 维度 | 框架原有能力 | Paituo 补齐完善 |
|------|------------|----------------|
| **会话摘要** | 基础 Summary 机制 | 增量摘要边界追踪（lastIncludedTimestamp/EventID）、Skip-Recent 保留近期记忆、Dynamic Summarizer 按请求切换模型/提示词 |
| **工具执行** | 单次工具调用 | Lua 批量合并执行 + pcall 错误隔离 + 本地摘要桥接（TextRank/TF-IDF），N次调用→1次执行 |
| **上下文感知** | 被动压缩 | Context Budget Plugin 主动预算提醒（50%/70%/85% 三级阈值），引导 LLM 自主调节行为 |
| **压缩策略** | 简单截断 | 分级压缩：普通结果1024 tokens / 超大结果8192 tokens / 指定工具保留完整 / 指定工具强制清理 |
| **可观测性** | 基础 OTel 追踪 | Langfuse 全链路集成：预算指标注入 Span、摘要关联父 TraceID、Debug Recorder 路径跨系统跳转 |

---

## 四、三大核心能力详解

### 1. Lua 执行工具 — 批量操作合并与错误隔离

**代码位置**：`tool/luaexec/`

| 模块 | 功能 |
|------|------|
| `sandbox.go` | 安全沙箱：`SkipOpenLibs: true` 仅加载安全标准库；过滤 `io.popen`、`os.execute` 等危险函数；Context 超时中断 |
| `bridge_tool.go` | 工具桥接：Lua 脚本可调用 `tool.工具名({...})` 动态调用已注册工具 |
| `bridge_summarize.go` | 摘要桥接：TextRank + TF-IDF + gse 中文分词，提供 `summarize.textrank()`、`summarize.tfidf()`、`summarize.keywords()` |
| `kb_module.go` | 知识库桥接：Lua 脚本内可直接操作知识库（创建、导入、搜索） |
| `bridge_fs.go` | 文件系统桥接：受控的文件读写访问 |
| `config.go` | 细粒度配置：超时、输出长度限制、模块黑名单、栈大小、文件系统权限 |

**Lua合并执行在造价场景的典型模式**：

| 模式 | 传统方式 | Lua合并方式 | 收益 |
|------|---------|------------|------|
| 批量定额查询 | 5次LLM调用+5份结果 | 1次Lua脚本循环查询 | 上下文占用↓5x |
| 链式计算（读→算→写） | 3次LLM调用，中间结果占上下文 | 1次Lua脚本完成全流程 | 上下文占用↓3x，无中间结果泄漏 |
| 多文件对比分析 | N次fs_read | Lua批量读取+summarize.textrank提取要点 | 只返回摘要，原始内容不入上下文 |

### 2. 三层上下文防线

#### 第一层：Context Budget Plugin（实时预算监控）

`openclaw/conversation/context_budget_plugin.go` 通过 `BeforeModel` 回调机制，在每次 LLM 调用前注入预算提醒：

| 阈值 | 注入内容 |
|------|---------|
| 首次调用 | 静态指导：告知 LLM 已启用上下文压缩，优先精确搜索、善用子任务拆分 |
| ≥50% | "建议后续优先使用 search_content/grep，避免批量读取大文件" |
| ≥70% | "请主动考虑：1) task_run 隔离子任务；2) 结果可被压缩；3) 优先精确搜索" |
| ≥85% | "请立即停止批量操作。优先使用 task_run 处理后续任务" |

关键设计细节：
- **低于 50% 不注入**，避免 LLM 过早自我干预执行策略
- **预算指标写入 OTel Span**，与 Langfuse 联动追踪
- **去重机制**：检测最后一条消息是否已包含 `<context_budget>` 标签，防止重复注入

#### 第二层：Session Summary（增量摘要压缩）

`session/summary/summarizer.go` 实现了完整的增量摘要机制：

- **触发条件灵活**：支持 `context_threshold_ratio`、`token_threshold`、`event_threshold`、`idle_threshold` 多种策略，可配置 "any"（任一满足即触发）或 "all"
- **Skip-Recent 机制**：摘要时跳过最近 N 条事件，保留 Agent 的短期工作记忆
- **增量摘要**：通过 `lastIncludedTimestamp` 和 `lastIncludedEventID` 追踪摘要边界，下次只摘要新增部分
- **工具调用格式化**：`ToolCallFormatter` / `ToolResultFormatter` 将工具调用和结果格式化为可摘要的文本
- **Pre/Post Hook**：支持摘要前后的自定义处理逻辑
- **Dynamic Summarizer**：`dynamic.go` 允许按请求上下文动态切换摘要模型和提示词

#### 第三层：Context Compaction（工具结果压缩）

配置项体现了精细的压缩策略：

- **Pass 1** — 历史工具结果占位替换（`tool_result_max_tokens: 1024`）
  - 只作用于旧 request 中超过阈值的 tool result
  - 当前 request 和最近 N 条受保护
  - 适合清理已不重要的历史工具输出

- **Pass 2** — 超大工具结果截断（`oversized_tool_result_max_tokens: 8192`）
  - 作用于几乎所有 tool result，包括当前 request
  - 超过阈值的 tool result 使用首尾保留策略截断
  - 防止单个超大 tool result 直接撑爆 context window

- **按工具名控制**：
  - `keep_tool_names: ["todo_write"]` — 编制进度不可压缩
  - `force_clean_tool_names: ["subagents_get"]` — 子代理结果已内化，强制清理

### 3. Langfuse 可观测性 — 全链路追踪与评估

**代码位置**：`telemetry/langfuse/`、`openclaw/app/langfuse.go`

#### 追踪链路

`langfuse.go` 通过 OTel Baggage 机制将关键元数据传播到所有子 Span：

| Baggage Key | 含义 |
|-------------|------|
| `langfuse.trace.name` | 追踪名称（channel + userID + messageID） |
| `langfuse.user.id` | 用户标识 |
| `langfuse.session.id` | 会话标识 |
| `langfuse.trace.metadata.app_name` | 应用名 |
| `langfuse.trace.metadata.channel` | 渠道 |
| `langfuse.trace.metadata.request_id` | 请求ID |
| `langfuse.trace.metadata.profile_id` | 运行配置版本 |
| `langfuse.trace.metadata.debug_trace` | 本地调试录制路径（跨系统关联） |
| `langfuse.trace.metadata.correlation_id` | 统一关联标识 |

#### 与 Context Budget 的联动

`context_budget_plugin.go` 第201-207行将预算指标写入当前 OTel Span：

```go
span.SetAttributes(
    attribute.Int("context_budget.tokens", tokens),
    attribute.Int("context_budget.window", contextWindow),
    attribute.Float64("context_budget.ratio", ratio),
)
```

这些指标会通过 Langfuse SpanProcessor 自动导出到 Langfuse，实现**上下文预算使用率的实时可视化监控**。

#### 与 Summary 的联动

`summarizer.go` 第667-681行在摘要生成时创建独立 Span，并将父追踪 ID 作为元数据记录：

```go
span.SetAttributes(attribute.String(
    "langfuse.trace.metadata.summary_parent_trace_id",
    parentSpanCtx.TraceID().String(),
))
```

即使父 Span 已结束，仍可通过 Langfuse 关联摘要调用与原始请求。

#### Debug Recorder 关联

`langfuse.go` 第338-357行将本地调试录制路径写入 Baggage，实现从 Langfuse Trace 页面直接跳转到本地调试文件，打通云端追踪与本地诊断。

---

## 五、基于最佳区间推导的完整配置体系

### 窗口设定

```yaml
max_context_budget_window: 260000  # 有效上下文窗口
model:
  context_window: 128000           # 模型原生窗口
  token_tailoring:
    enabled: true
    strategy: "middle_out"          # 中间裁剪，保留首尾
```

**逻辑**：Budget Window(260K) > 模型窗口(128K)，让三层防线在模型窗口内提前干预，Token Tailoring 作为最后兜底。

### 三层防线配置

```yaml
# 第1层：Compaction @ 50% — 压缩工具结果（最高性价比）
enable_context_compaction: true
context_compaction_threshold_ratio: 0.5
context_compaction_tool_result_max_tokens: 1024       # Pass1: 旧结果→占位符
context_compaction_oversized_tool_result_max_tokens: 8192  # Pass2: 超大结果→首尾保留
context_compaction_keep_recent_tool_results: 30       # 保留近30条（覆盖5-8步计算链路）
context_compaction_keep_tool_names: ["todo_write"]    # 编制进度不可压缩
context_compaction_force_clean_tool_names: ["subagents_get"]  # 子代理结果已内化

# 第2层：Summary @ 60% — 压缩语义信息
add_session_summary: true
session_summary_injection_mode: "system"              # 注入system，不被滑动窗口淘汰
session:
  summary:
    enabled: true
    policy: "any"                    # 任一条件满足即触发
    context_threshold_ratio: 0.6     # 60%触发
    token_threshold: 12288           # 或新增12K tokens触发
    max_words: 1500                  # 摘要上限（造价约束多，不能过短）

# 第3层：Budget @ 50%/70%/85% — 行为引导
enable_context_budget: true
max_context_budget_window: 260000
```

### Lua合并执行配置

```yaml
# 配合Budget"合并同类调用"的引导
toolsets:
  - type: "lua"
    config:
      default_timeout: 600           # 造价计算耗时长
      allow_io_lib: true
      allow_os_lib: true
      allow_fs_lib: true
      enable_debug: true
      allowed_script_dirs:
        - ./.openclaw-state
        - ./
```

### 可观测配置

```yaml
debug_recorder:
  enabled: true
  mode: "full"                       # 全量录制，用于回放验证
observability:
  langfuse:
    enabled: true
    required: false                  # 可观测不影响主流程
```

---

## 六、策略调配的逻辑闭环

```
                    ┌──────────────────────┐
                    │  Langfuse 追踪数据     │
                    │  (ratio/tokens/cost)  │
                    └──────────┬───────────┘
                               │
                    ┌──────────▼───────────┐
                    │  分析上下文利用率曲线   │
                    │  定位最佳工作区间      │
                    └──────────┬───────────┘
                               │
                    ┌──────────▼───────────┐
                    │  推导三层防线触发点    │
                    │  50%压缩→60%摘要→85%强限│
                    └──────────┬───────────┘
                               │
                    ┌──────────▼───────────┐
                    │  验证+迭代配置参数     │
                    │  (阈值/保留数/摘要词数) │
                    └──────────┬───────────┘
                               │
                    ┌──────────▼───────────┐
                    │  固化为生产配置       │
                    │  (openclaw.yaml)      │
                    └──────────┬───────────┘
                               │
                    ┌──────────▼───────────┐
                    │  持续监控（Langfuse）  │
                    │  → 发现偏差 → 重新调配 │
                    └──────────────────────┘
```

---

## 七、验证结论

| 评估维度 | 无管控基线 | 最佳配置 | 改善 |
|---------|----------|---------|------|
| 长任务完成率 | 长对话后上下文溢出中断 | 三层防线逐级管控 | 显著提升 |
| 上下文利用率 | 不可控，常超85% | 持续维持在30-60% | 有效空间释放50%+ |
| 关键约束保持 | 摘要后丢失定额编号/费率 | Skip-Recent+keep_tool_names | 近期约束零丢失 |
| 单步故障影响 | 一个工具失败→任务中断 | pcall隔离+超时兜底 | 单步故障不扩散 |
| 问题定位效率 | "不知道哪步出了问题" | Langfuse全链路+预算指标 | 分钟级定位 |

---

## 八、一页总结

> **问题**：长任务中上下文膨胀导致模型超出最佳工作区间，质量下降、成本失控
>
> **方法**：测→定→配→验→固，五步闭环
>
> **发现**：模型最佳工作上下文区间为30%-60%，超60%质量下降，超85%不可接受
>
> **策略**：三层防线将利用率压回最佳区间
> - **50% Compaction**：压缩工具结果（占比70%，最高效瘦身）
> - **60% Summary**：增量摘要压缩语义（保留关键约束，1500词上限）
> - **70-85% Budget**：引导LLM行为调整（拆子任务/停批量）
>
> **保障**：Lua合并减少上下文膨胀源头 + Langfuse全链路可观测
>
> **成果**：AI造价编制场景长任务从"能跑"到"稳跑"，上下文利用率持续维持在30-60%最佳区间
