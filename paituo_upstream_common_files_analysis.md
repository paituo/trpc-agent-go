# Paituo 分支与 Main 分支共同修改文件分析报告

> 生成时间: 2026-06-13
> 分析对象: feat/paituo-integration vs main-upstream
> 仓库: https://github.com/paituo/trpc-agent-go

---

## 第一部分: 共同修改文件清单

经过分析，共发现 **20个文件** 被 paituo 分支和 main 分支**都修改过**。这些文件按冲突风险等级分类如下：

### 🔴 高冲突风险文件（两分支修改方向不同）

| 文件 | Paituo提交次数 | Main提交次数 | 冲突风险 |
|------|--------------|-------------|---------|
| [openclaw/app/app.go](file:///workspace/openclaw/app/app.go) | 18 | 48 | 🔴极高 |
| [openclaw/internal/gateway/stream.go](file:///workspace/openclaw/internal/gateway/stream.go) | 10 | 8 | 🔴极高 |
| [openclaw/app/run_options.go](file:///workspace/openclaw/app/run_options.go) | 12 | 26 | 🔴极高 |
| [agent/llmagent/llm_agent.go](file:///workspace/agent/llmagent/llm_agent.go) | 7 | 127 | 🔴极高 |
| [model/openai/openai.go](file:///workspace/model/openai/openai.go) | 6 | 97 | 🔴极高 |
| [internal/flow/llmflow/llmflow.go](file:///workspace/internal/flow/llmflow/llmflow.go) | 7 | 81 | 🔴极高 |
| [internal/flow/processor/content.go](file:///workspace/internal/flow/processor/content.go) | 4 | 91 | 🔴极高 |
| [agent/graphagent/graph_agent.go](file:///workspace/agent/graphagent/graph_agent.go) | 4 | 55 | 🔴高 |
| [model/model.go](file:///workspace/model/model.go) | 4 | 11 | 🔴高 |
| [openclaw/internal/gateway/server.go](file:///workspace/openclaw/internal/gateway/server.go) | 3 | 16 | 🔴高 |
| [telemetry/langfuse/exporter.go](file:///workspace/telemetry/langfuse/exporter.go) | 4 | 19 | 🔴高 |

### 🟡 中等冲突风险文件

| 文件 | Paituo提交次数 | Main提交次数 | 冲突风险 |
|------|--------------|-------------|---------|
| [openclaw/registry/registry.go](file:///workspace/openclaw/registry/registry.go) | 7 | 5 | 🟡中 |
| [openclaw/internal/subagentrun/service.go](file:///workspace/openclaw/internal/subagentrun/service.go) | 9 | 3 | 🟡中 |
| [model/token_tailor.go](file:///workspace/model/token_tailor.go) | 7 | 10 | 🟡中 |
| [openclaw/gwproto/types.go](file:///workspace/openclaw/gwproto/types.go) | 5 | 9 | 🟡中 |
| [internal/flow/processor/context_compact.go](file:///workspace/internal/flow/processor/context_compact.go) | 4 | 7 | 🟡中 |
| [tool/file/readfile.go](file:///workspace/tool/file/readfile.go) | 7 | 6 | 🟡中 |
| [tool/file/file.go](file:///workspace/tool/file/file.go) | 5 | 8 | 🟡中 |
| [model/tiktoken/tiktoken.go](file:///workspace/model/tiktoken/tiktoken.go) | 5 | 2 | 🟡中 |
| [openclaw/app/tooling_builtins.go](file:///workspace/openclaw/app/tooling_builtins.go) | 7 | 2 | 🟡中 |

### 🟢 低冲突风险文件

| 文件 | Paituo提交次数 | Main提交次数 | 冲突风险 |
|------|--------------|-------------|---------|
| [tool/todo/todo.go](file:///workspace/tool/todo/todo.go) | 3 | 2 | 🟢低 |

---

## 第二部分: 每个共同修改文件的具体改动分析

### 🔴 1. [openclaw/app/app.go](file:///workspace/openclaw/app/app.go)

#### Main 分支主要改动（48次提交）
| 提交 | 改动内容 |
|------|---------|
| `e9553d04` | 支持 code-driven runtime profiles |
| `f4dcee2e` | 拆分通用运行时 |
| `164bbd14` | 添加 cron scheduled run debug traces |
| `0e3f1239` | 添加 runtime profiles |
| `41e7464e` | 重构 knowledge config 为统一 provider pattern |
| `24d76245` | 使 skills 成为持久的 capability 路径 |
| 其他... | session summary、skill 整合等 |

#### Paituo 分支主要改动（18次提交）
| 提交 | 改动内容 |
|------|---------|
| `edd97f64` | 添加 lua debug config 和 openai adaptive token tailoring |
| `89797876` | Lua 脚本新增 HTML 表格解析与文本摘要 |
| `7c84b0ee` | Subagent 添加 debug tracing 支持 |
| `2c9c21a2` | 重构令牌计数与上下文压缩，新增 Lua 工具 |
| `f6d20f97` | 添加文件移动复制删除工具支持 |
| `d752c373` | 调整 tailoring 策略相关实现 |
| `e5d7f6ef` | 添加 vLLM model variant 支持 |

#### 🔴 冲突风险分析
**高度冲突** — 两分支修改方向完全不同：
- **Main**: 侧重 runtime profiles、skills 整合、cron 支持
- **Paituo**: 侧重 Lua 工具、token tailoring、文件操作工具

**推荐做法**:
- 保留 paituo 的 Lua 工具和 token tailoring 相关代码
- 接受 main 的 runtime profiles 和 skills 整合
- 需要手动合并两套配置注册逻辑

---

### 🔴 2. [openclaw/internal/gateway/stream.go](file:///workspace/openclaw/internal/gateway/stream.go)

#### Main 分支主要改动（8次提交）
| 提交 | 改动内容 |
|------|---------|
| `bc7eb85e` | Stream tool detail |
| `1324fdea` | 改进 stream progress visibility |
| `7b9ac653` | 实现 model context window lookup |
| `7ec7233b` | Stream reasoning as thought events |
| `01cdd7bb` | 添加 langfuse admin trace links |
| `c5e21e1b` | 添加 streaming gateway replies |

#### Paituo 分支主要改动（10次提交）
| 提交 | 改动内容 |
|------|---------|
| `91d10cc5` | 修复 LLM 工具调用参数非 JSON 导致的 JSON 结构破坏 |
| `5a95db67` | 修复进度消息的 messageID 关联 |
| `f284f2e9` | 添加 response id tracking 和 child trace association |
| `7c84b0ee` | Subagent 添加 debug tracing 支持 |
| `18026b84` | 新增流式事件状态 delta、模型和结束原因等元数据 |
| `597fd6e7` | 添加 tool result field 到 stream event |

#### 🔴 冲突风险分析
**高度冲突** — 两分支都扩展了 stream 事件结构：
- **Main**: 侧重 thought events、tool detail streaming、context window lookup
- **Paituo**: 侧重 delta/status/model 元数据、tool result 字段、response id tracking

**推荐做法**:
- Stream 字段扩展尽量放在结构体尾部（向后兼容）
- 将 paituo 的自定义字段（delta、tool_result）与 main 的 thought events 分开
- 建议抽离 `stream_extensions.go` 存放本分支自定义逻辑

---

### 🔴 3. [openclaw/app/run_options.go](file:///workspace/openclaw/app/run_options.go)

#### Main 分支主要改动（26次提交）
| 提交 | 改动内容 |
|------|---------|
| `0e3f1239` | 添加 runtime profiles |
| `06e68795` | 添加 knowledge description field |
| `41e7464e` | 重构 knowledge config |
| `033dfdce` | Gate oversized tool result truncation behind EnableContextCompaction |
| `8570b07a` | 添加 configurable skill tool profiles |
| `c02d5bd3` | Watch local skill roots |
| `8ee017e1` | 支持 model generation config |
| `277d4bdd` | 添加 context-aware summarization with auto mode |

#### Paituo 分支主要改动（12次提交）
| 提交 | 改动内容 |
|------|---------|
| `2c9c21a2` | 重构令牌计数与上下文压缩逻辑 |
| `f6d20f97` | 添加文件工具支持，优化技能根路径加载 |
| `b286c5b0` | 添加 context threshold config for session summarization |
| `3e6f0c64` | 添加 static file mounting 支持 |
| `5a2d292d` | 添加 token tailoring support |
| `49dbe7ea` | 添加 enable-execute-tools switch |

#### 🔴 冲突风险分析
**极高冲突** — 配置结构体被两分支反复扩展：
- **Main**: runtime profiles、skill profiles、context-aware summarization
- **Paituo**: token tailoring、context threshold、file tool profiles

**推荐做法**:
- 将 paituo 的配置字段抽离为独立子结构体（如 `TokenTailoringConfig`）
- 使用组合模式而非直接添加字段到主结构体
- 避免在相同位置插入配置字段

---

### 🔴 4. [model/openai/openai.go](file:///workspace/model/openai/openai.go)

#### Main 分支主要改动（97次提交）
| 提交 | 改动内容 |
|------|---------|
| `172a18dc` | Preserve system during token tailoring |
| `8b249092` | 添加 URL file input support |
| `43dea096` | Share stable tool ordering |
| `aa9159da` | 添加 local context window options |
| `5c04c0e2` | 添加 opt-in OpenAI chat telemetry |
| `c06eb38e` | Tighten token tailoring budgets |
| `7fe7d61c` | 支持 request extra fields |
| `9549d938` | Backfill DeepSeek tool reasoning |
| `152bbaa4` | Backfill reasoning content for assistant history |

#### Paituo 分支主要改动（6次提交）
| 提交 | 改动内容 |
|------|---------|
| `e5d7f6ef` | 添加 vLLM model variant 支持 |
| `fdee3715` | 统一模型 Info 构造 |
| `5d350869` | 合并冲突解决提交 |
| `d35d00b1` | 合并提交 |

#### 🔴 冲突风险分析
**极高冲突** — Token tailoring 策略两分支都在演进：
- **Main**: preserve system、tighten budgets、request extra fields
- **Paituo**: vLLM variant、Info 统一构造

**推荐做法**:
- vLLM 支持可以保留，但需确保与 main 的 model variant 机制兼容
- Info 统一构造逻辑需要与 main 的最新实现对齐
- Token tailoring 相关代码需合并到 main 的策略框架

---

### 🔴 5. [agent/llmagent/llm_agent.go](file:///workspace/agent/llmagent/llm_agent.go)

#### Main 分支主要改动（127次提交）
| 提交 | 改动内容 |
|------|---------|
| `7a328f49` | 添加 command allow/deny policy for sandboxed exec |
| `162aa924` | 添加 tool result compaction controls |
| `b9c54e4d` | 添加 agent-scoped extension API |
| `70844ded` | 添加 Workspace facade for in-process file & exec |
| `34abffae` | 复用 WorkspaceRegistry across invocations |
| `47152dc6` | 添加 per-call model selector |
| `12af1045` | 添加 on-demand session recall |

#### Paituo 分支主要改动（7次提交）
| 提交 | 改动内容 |
|------|---------|
| `28a55234` | 重构令牌计数与代理追踪逻辑 |
| `947fc02f` | 添加 token counter override 和 calibration 支持 |
| `fdee3715` | 统一模型 Info 构造 |
| `a58e189b` | 添加 context metrics tracking 和 model-aware token counter |

#### 🔴 冲突风险分析
**极高冲突** — Agent 核心逻辑两分支都在密集修改：
- **Main**: workspace facade、extension API、per-call model selector、tool compaction
- **Paituo**: token counter calibration、context metrics

**推荐做法**:
- Token counter calibration 可以作为独立组件集成到 main 的架构中
- Context metrics 埋点位置需与 main 的 tracing 框架对齐
- Workspace facade 相关代码不应与 paituo 的 workspace 处理冲突

---

### 🔴 6. [internal/flow/llmflow/llmflow.go](file:///workspace/internal/flow/llmflow/llmflow.go)

#### Main 分支主要改动（81次提交）
| 提交 | 改动内容 |
|------|---------|
| `5c3372ae` | 添加 external tools run option |
| `47152dc6` | 添加 per-call model selector |
| `aa9159da` | 添加 local context window options |
| `419905d9` | Deep clone request extra fields |
| `7fe7d61c` | 支持 request extra fields |
| `43fbb0c5` | 暴露 timing info to callbacks |
| `e0f61923` | 记录 applied surface ids in trace |
| `98af66ac` | Harden context compaction rebuild |

#### Paituo 分支主要改动（7次提交）
| 提交 | 改动内容 |
|------|---------|
| `28a55234` | 重构令牌计数与代理追踪逻辑 |
| `fdee3715` | 统一模型 Info 构造 |
| `4b994e64` | 修复 LLM 调用失败时链路追踪缺失 IO 属性 |
| `9f525685` | 修复多处遥测追踪不完整的问题 |

#### 🔴 冲突风险分析
**极高冲突** — LLM 流处理层两分支都在密集修改：
- **Main**: external tools、per-call selector、context window、timing info
- **Paituo**: token counter 重构、tracing 修复、metrics

**推荐做法**:
- 考虑将 paituo 的 metrics 埋点改为 main 的 tracing 框架方式
- Token counter 重构需与 main 的 calibration 机制协调
- Tracing 修复可以独立提交，不依赖架构变更

---

### 🔴 7. [internal/flow/processor/content.go](file:///workspace/internal/flow/processor/content.go)

#### Main 分支主要改动（91次提交）
| 提交 | 改动内容 |
|------|---------|
| `a0371e05` | Respect tool result threshold for summaries |
| `63175f62` | Track summary boundaries |
| `c5f81138` | Protect recent force-clean results |
| `162aa924` | 添加 tool result compaction controls |
| `f1d6aa24` | Omit preload topic labels |
| `7aa087d9` | Keep tool results in matching rounds |
| `cc3be255` | 添加 AWS Bedrock model adapter |

#### Paituo 分支主要改动（4次提交）
| 提交 | 改动内容 |
|------|---------|
| `fdee3715` | 统一模型 Info 构造 |
| `1a9bb8e9` | 重构增量消息获取逻辑并新增统计埋点 |
| `d35d00b1` | 合并提交 |

#### 🔴 冲突风险分析
**极高冲突** — Content 处理逻辑两分支都在密集修改：
- **Main**: summary boundaries、force-clean protection、tool result threshold
- **Paituo**: 增量消息重构、Info 统一构造

**推荐做法**:
- Paituo 的增量消息重构可以保留，但需与 main 的 summary 机制兼容
- 统计埋点改为 main 的 metric 框架方式

---

### 🔴 8. [agent/graphagent/graph_agent.go](file:///workspace/agent/graphagent/graph_agent.go)

#### Main 分支主要改动（55次提交）
| 提交 | 改动内容 |
|------|---------|
| `c06eb38e` | Tighten token tailoring budgets |
| `c5d1570f` | Support preserve foreign messages |
| `f53d6a99` | 统一 telemetry error labels |
| `60c486f4` | Enhance session summary handling with injection mode |
| `89f8586c` | Guard oversized tool results |
| `abc96510` | 添加 context compaction |

#### Paituo 分支主要改动（4次提交）
| 提交 | 改动内容 |
|------|---------|
| `28a55234` | 重构令牌计数与代理追踪逻辑 |
| `fdee3715` | 统一模型 Info 构造 |
| `9f525685` | 修复多处遥测追踪不完整的问题 |

#### 🔴 冲突风险分析
**高冲突** — Graph Agent 注入逻辑：
- **Main**: token tailoring、session summary、tool result guard
- **Paituo**: token counter 重构、tracing 修复

**推荐做法**:
- Token counter 重构需与 main 的 tailoring 策略协调
- Tracing 修复可以独立提交

---

### 🔴 9. [model/model.go](file:///workspace/model/model.go)

#### Main 分支主要改动（11次提交）
| 提交 | 改动内容 |
|------|---------|
| `aa9159da` | 添加 local context window options |
| `eff76d7a` | 支持 iterator-based model |

#### Paituo 分支主要改动（4次提交）
| 提交 | 改动内容 |
|------|---------|
| `fdee3715` | 统一模型 Info 构造，新增测试工具 |
| `78214a29` | 添加 token tailoring fields to Info struct |
| `d35d00b1` | 合并提交 |

#### 🔴 冲突风险分析
**高冲突** — Model Info 结构体：
- **Main**: local context window options、iterator-based model
- **Paituo**: token tailoring fields、统一 Info 构造

**推荐做法**:
- Token tailoring fields 可以作为 Info 结构体扩展字段保留
- 统一 Info 构造逻辑需与 main 对齐

---

### 🔴 10. [openclaw/internal/gateway/server.go](file:///workspace/openclaw/internal/gateway/server.go)

#### Main 分支主要改动（16次提交）
| 提交 | 改动内容 |
|------|---------|
| `0e3f1239` | 添加 runtime profiles |
| `8c191f49` | 支持 explicit conversation storage scopes |
| `277d4bdd` | Context-aware summarization with auto mode |
| `7b9ac653` | Model context window lookup |
| `8ef3d9e1` | 改进 shared conversation 和 cron handling |
| `7ec7233b` | Stream reasoning as thought events |

#### Paituo 分支主要改动（3次提交）
| 提交 | 改动内容 |
|------|---------|
| `f284f2e9` | 添加 response id tracking 和 child trace association |
| `18026b84` | 新增流式事件状态 delta 等元数据 |

#### 🔴 冲突风险分析
**高冲突** — Gateway 服务层：
- **Main**: runtime profiles、conversation scopes、context window
- **Paituo**: 流式事件元数据、response tracking

**推荐做法**:
- Response tracking 作为独立功能保留
- 流式事件元数据与 main 的 thought events 协调

---

### 🔴 11. [telemetry/langfuse/exporter.go](file:///workspace/telemetry/langfuse/exporter.go)

#### Main 分支主要改动（19次提交）
| 提交 | 改动内容 |
|------|---------|
| `2343bf51` | 添加 otel-compatible message attributes |
| `682a77ef` | 支持 multimodal otel |
| `0e6b7f1c` | 修复 tool_id => tool_call_id |
| `e62f6b8b` | 替换 telemetry keys 为 semconvtrace constants |
| `f3848e19` | 使用 max bytes to leaf value bytes Truncation |

#### Paituo 分支主要改动（4次提交）
| 提交 | 改动内容 |
|------|---------|
| `2c9c21a2` | 重构令牌计数与上下文压缩，新增 Langfuse 追踪优化 |
| `c8cb3abb` | 添加 context control metrics 和 embedding span |
| `d35d00b1` | 合并提交 |

#### 🔴 冲突风险分析
**高冲突** — Langfuse 追踪：
- **Main**: multimodal otel、semconv constants、truncation
- **Paituo**: context metrics、embedding span

**推荐做法**:
- Metrics 埋点改为 main 的 span attribute 方式
- 确保与 main 的 semconv constants 命名一致

---

### 🟡 12. [openclaw/registry/registry.go](file:///workspace/openclaw/registry/registry.go)

#### Main 分支主要改动（5次提交）
| 提交 | 改动内容 |
|------|---------|
| `41e7464e` | 重构 knowledge config 为 unified provider pattern |
| `55be8f5f` | 记录 final chat requests in debug traces |
| `c5e21e1b` | 添加 streaming gateway replies |
| `f892c22d` | 使 telegram 成为 channel plugin |
| `333feb44` | 添加 openclaw-like gateway 和 telegram demo |

#### Paituo 分支主要改动（7次提交）
| 提交 | 改动内容 |
|------|---------|
| `77a26b26` | 新增流式传输选项与工具调用相关功能 |
| `5a2d292d` | 添加 token tailoring support |
| `4ede08f8` | 添加 planner plugin registration |

#### 🟡 冲突风险分析
**中等冲突** — 注册表扩展：
- **Main**: knowledge provider pattern、telegram plugin
- **Paituo**: token tailoring、planner plugin

**推荐做法**:
- Token tailoring 注册可以与 main 的注册表机制兼容
- Planner plugin 作为独立扩展保留

---

### 🟡 13. [openclaw/internal/subagentrun/service.go](file:///workspace/openclaw/internal/subagentrun/service.go)

#### Main 分支主要改动（3次提交）
| 提交 | 改动内容 |
|------|---------|
| `9308b28d` | 支持 worktree-isolated subagents |
| `f4dcee2e` | 拆分通用运行时 |
| `3011929c` | 添加 subagent runtime |

#### Paituo 分支主要改动（9次提交）
| 提交 | 改动内容 |
|------|---------|
| `f284f2e9` | 添加 response id tracking 和 child trace association |
| `7c84b0ee` | 添加 debug tracing 支持 |
| `eb210af4` | 添加 otel span attributes 和 session alias config |
| `4b98ec04` | 添加 title 和 ref field 支持 |
| `44008c1b` | 添加 inline global instruction 支持 |

#### 🟡 冲突风险分析
**中等冲突** — Subagent 运行时：
- **Main**: worktree-isolated subagents、runtime 拆分
- **Paituo**: debug tracing、span attributes、inline instruction

**推荐做法**:
- Debug tracing 和 span attributes 可以保留
- 与 main 的 worktree isolation 协调确保兼容性

---

### 🟡 14. [model/token_tailor.go](file:///workspace/model/token_tailor.go)

#### Main 分支主要改动（10次提交）
| 提交 | 改动内容 |
|------|---------|
| `633527e3` | Clarify token counter units |
| `172a18dc` | Preserve system during token tailoring |
| `d2d3d690` | Not to calculate max_completion_tokens automatically |
| `28085bc9` | Optimize token tailor calculate |
| `934b4cd5` | 添加 configurable approx runes per token |
| `17b6c2eb` | Implement token tailoring for provider |
| `ecd0071f` | Update token tailoring formula |

#### Paituo 分支主要改动（7次提交）
| 提交 | 改动内容 |
|------|---------|
| `28a55234` | 重构令牌计数与代理追踪逻辑 |
| `947fc02f` | 添加 token counter override 和 calibration |
| `fdee3715` | 统一模型 Info 构造 |
| `7eb0c922` | 添加 model-aware token counter |

#### 🟡 冲突风险分析
**中等冲突** — Token 裁剪策略：
- **Main**: preserve system、max_completion_tokens、runes per token
- **Paituo**: token counter override、calibration、model-aware

**推荐做法**:
- Token counter override 可以作为 main calibration 机制的补充
- 确保与 main 的 token counter 框架兼容

---

### 🟡 15. [openclaw/gwproto/types.go](file:///workspace/openclaw/gwproto/types.go)

#### Main 分支主要改动（9次提交）
| 提交 | 改动内容 |
|------|---------|
| `bc7eb85e` | Stream tool detail |
| `1324fdea` | 改进 stream progress visibility |
| `277d4bdd` | Context-aware summarization with auto mode |
| `7b9ac653` | Model context window lookup |
| `8ef3d9e1` | 改进 shared conversation 和 cron handling |
| `7ec7233b` | Stream reasoning as thought events |

#### Paituo 分支主要改动（5次提交）
| 提交 | 改动内容 |
|------|---------|
| `2c9c21a2` | 重构令牌计数与上下文压缩 |
| `77a26b26` | 新增流式传输选项 |
| `18026b84` | 新增流式事件状态 delta、模型和结束原因 |
| `597fd6e7` | 添加 tool result field |

#### 🟡 冲突风险分析
**中等冲突** — 网关协议类型：
- **Main**: tool detail streaming、progress visibility、thought events
- **Paituo**: delta/status/model 元数据、tool result field

**推荐做法**:
- 新增字段放在结构体尾部保持向后兼容
- 避免修改已有字段的顺序或类型

---

### 🟡 16. [internal/flow/processor/context_compact.go](file:///workspace/internal/flow/processor/context_compact.go)

#### Main 分支主要改动（7次提交）
| 提交 | 改动内容 |
|------|---------|
| `c5f81138` | Protect recent force-clean results |
| `162aa924` | 添加 tool result compaction controls |
| `aa9159da` | 添加 local context window options |
| `c06eb38e` | Tighten token tailoring budgets |
| `033dfdce` | Gate oversized tool result truncation behind EnableContextCompaction |
| `89f8586c` | Guard oversized tool results |

#### Paituo 分支主要改动（4次提交）
| 提交 | 改动内容 |
|------|---------|
| `fdee3715` | 统一模型 Info 构造 |
| `1a9bb8e9` | 重构增量消息获取逻辑并新增统计埋点 |

#### 🟡 冲突风险分析
**中等冲突** — 上下文压缩：
- **Main**: tool result controls、force-clean protection、EnableContextCompaction gate
- **Paituo**: 增量消息重构、统计埋点

**推荐做法**:
- 统计埋点改为 main 的 metric 框架方式
- 增量消息重构与 main 的 compression 逻辑兼容

---

### 🟡 17. [tool/file/readfile.go](file:///workspace/tool/file/readfile.go)

#### Main 分支主要改动（6次提交）
| 提交 | 改动内容 |
|------|---------|
| `a00c35e9` | Align official skill dependency metadata support |
| `21dbea85` | 添加 schema descriptions for file tool inputs |
| `92543ee3` | Omit non-text inline content |
| `295590e7` | Harden skill_run outputs and cwd |

#### Paituo 分支主要改动（7次提交）
| 提交 | 改动内容 |
|------|---------|
| `75a1ca51` | 添加 max tool result chars config |
| `b131cf90` | 调整路径校验逻辑 |
| `09218a01` | 修复文件读取和搜索逻辑 |
| `a71fdfbc` | 添加 truncated、total lines、file size 元数据 |

#### 🟡 冲突风险分析
**中等冲突** — 文件读取工具：
- **Main**: skill dependency、schema descriptions、inline content
- **Paituo**: max chars config、truncation 元数据、路径校验

**推荐做法**:
- Truncation 元数据可以保留
- 路径校验逻辑需确保与 main 的安全策略一致

---

### 🟡 18. [tool/file/file.go](file:///workspace/tool/file/file.go)

#### Main 分支主要改动（8次提交）
| 提交 | 改动内容 |
|------|---------|
| `a00c35e9` | Align official skill dependency metadata support |
| `295590e7` | Harden skill_run outputs and cwd |
| `ff18a15b` | 添加 Name interface 和 NamedToolSet struct |

#### Paituo 分支主要改动（5次提交）
| 提交 | 改动内容 |
|------|---------|
| `00f4ecec` | 新增移动、复制、删除文件工具 |
| `75a1ca51` | 添加 max tool result chars config |
| `1b18ebaf` | 修复基准目录依赖当前工作目录的问题 |
| `b131cf90` | 调整路径校验逻辑 |

#### 🟡 冲突风险分析
**中等冲突** — 文件工具框架：
- **Main**: Name interface、NamedToolSet、skill outputs
- **Paituo**: move/copy/delete 工具、max chars config

**推荐做法**:
- Move/copy/delete 作为新工具添加到 tool set
- 路径校验逻辑与 main 的安全策略对齐

---

### 🟡 19. [model/tiktoken/tiktoken.go](file:///workspace/model/tiktoken/tiktoken.go)

#### Main 分支主要改动（2次提交）
| 提交 | 改动内容 |
|------|---------|
| `550d84d3` | Enhance token counting to include tool calls |
| `8e8ca6a6` | Implement token tailoring functionality |

#### Paituo 分支主要改动（5次提交）
| 提交 | 改动内容 |
|------|---------|
| `28a55234` | 重构令牌计数与代理追踪逻辑 |
| `947fc02f` | 添加 token counter override 和 calibration |
| `fdee3715` | 统一模型 Info 构造 |
| `7eb0c922` | 添加 model-aware token counter |

#### 🟡 冲突风险分析
**中等冲突** — Tokenizer：
- **Main**: include tool calls、token tailoring
- **Paituo**: token counter override、calibration、model-aware

**推荐做法**:
- Token counter override 可以作为 main calibration 机制的补充
- 确保与 main 的 token counting 框架兼容

---

### 🟡 20. [openclaw/app/tooling_builtins.go](file:///workspace/openclaw/app/tooling_builtins.go)

#### Main 分支主要改动（2次提交）
| 提交 | 改动内容 |
|------|---------|
| `6f1ef160` | 添加 native browser provider runtime |
| `333feb44` | 添加 openclaw-like gateway 和 telegram demo |

#### Paituo 分支主要改动（7次提交）
| 提交 | 改动内容 |
|------|---------|
| `edd97f64` | 添加 lua debug config |
| `89797876` | Lua 脚本新增 HTML 表格解析与文本摘要 |
| `fa9f99f2` | Lua 脚本新增路径加载功能 |
| `2c9c21a2` | 新增 Lua 工具 |

#### 🟡 冲突风险分析
**中等冲突** — 工具注册：
- **Main**: native browser provider、gateway demo
- **Paituo**: Lua 工具注册

**推荐做法**:
- Lua 工具作为独立工具注册，可以与 main 的工具框架兼容
- 避免在工具注册 switch/case 中添加冲突分支

---

### 🟢 21. [tool/todo/todo.go](file:///workspace/tool/todo/todo.go)

#### Main 分支主要改动（2次提交）
| 提交 | 改动内容 |
|------|---------|
| `d91d1dd6` | 添加 hard TODO compliance extension |
| `17224e40` | 添加 todo_write tool for multi-step plan tracking |

#### Paituo 分支主要改动（3次提交）
| 提交 | 改动内容 |
|------|---------|
| `257f8bbf` | 重构 ID 处理逻辑，强化 ID 稳定性 |
| `a6008ffa` | 新增任务 ID 和父 ID 支持 |

#### 🟢 冲突风险分析
**低冲突** — Todo 工具：
- **Main**: compliance extension、multi-step tracking
- **Paituo**: ID 稳定性、parent_id

**推荐做法**:
- ID 处理重构可以保留
- 与 main 的 compliance extension 兼容

---

## 第三部分: 冲突风险总结

### 🔴 极高冲突风险（需要人工合并）

1. **[openclaw/app/app.go](file:///workspace/openclaw/app/app.go)** — 18 vs 48次提交
   - 配置注册逻辑完全不同
   - 需要手动合并两套工具注册

2. **[openclaw/app/run_options.go](file:///workspace/openclaw/app/run_options.go)** — 12 vs 26次提交
   - 配置结构体被两分支反复扩展
   - 需要拆分为子结构体

3. **[agent/llmagent/llm_agent.go](file:///workspace/agent/llmagent/llm_agent.go)** — 7 vs 127次提交
   - Agent 核心逻辑都在密集修改
   - Token counter 机制需协调

4. **[model/openai/openai.go](file:///workspace/model/openai/openai.go)** — 6 vs 97次提交
   - Token tailoring 策略两分支都在演进
   - vLLM 支持与 main 框架需协调

5. **[internal/flow/llmflow/llmflow.go](file:///workspace/internal/flow/llmflow/llmflow.go)** — 7 vs 81次提交
   - LLM 流处理层都在密集修改
   - Metrics 框架需对齐

### 🟡 中等冲突风险（可协商合并）

6. [internal/flow/processor/content.go](file:///workspace/internal/flow/processor/content.go) — 4 vs 91次提交
7. [openclaw/internal/gateway/stream.go](file:///workspace/openclaw/internal/gateway/stream.go) — 10 vs 8次提交
8. [agent/graphagent/graph_agent.go](file:///workspace/agent/graphagent/graph_agent.go) — 4 vs 55次提交
9. [model/model.go](file:///workspace/model/model.go) — 4 vs 11次提交
10. [openclaw/internal/gateway/server.go](file:///workspace/openclaw/internal/gateway/server.go) — 3 vs 16次提交
11. [telemetry/langfuse/exporter.go](file:///workspace/telemetry/langfuse/exporter.go) — 4 vs 19次提交
12. [model/token_tailor.go](file:///workspace/model/token_tailor.go) — 7 vs 10次提交

### 🟢 低冲突风险（自动合并或保留）

13. [openclaw/registry/registry.go](file:///workspace/openclaw/registry/registry.go)
14. [openclaw/internal/subagentrun/service.go](file:///workspace/openclaw/internal/subagentrun/service.go)
15. [openclaw/gwproto/types.go](file:///workspace/openclaw/gwproto/types.go)
16. [internal/flow/processor/context_compact.go](file:///workspace/internal/flow/processor/context_compact.go)
17. [tool/file/readfile.go](file:///workspace/tool/file/readfile.go)
18. [tool/file/file.go](file:///workspace/tool/file/file.go)
19. [model/tiktoken/tiktoken.go](file:///workspace/model/tiktoken/tiktoken.go)
20. [openclaw/app/tooling_builtins.go](file:///workspace/openclaw/app/tooling_builtins.go)
21. [tool/todo/todo.go](file:///workspace/tool/todo/todo.go)

---

## 第四部分: 推荐做法总结

### 1. 配置结构体重构（最高优先级）

**[openclaw/app/run_options.go](file:///workspace/openclaw/app/run_options.go)** 和 **[openclaw/app/app.go](file:///workspace/openclaw/app/app.go)**：

```go
// 建议：将 paituo 的配置字段抽离为独立子结构体
type PaituoLocalConfig struct {
    TokenTailoringConfig TokenTailoringConfig
    ContextThreshold     int
    EnableExecuteTools   bool
    // ... paituo 自定义配置
}

type AppConfig struct {
    // ... main 的公共配置
    *PaituoLocalConfig  // 组合而非直接添加字段
}
```

### 2. Token Counter 机制统一

**[model/token_tailor.go](file:///workspace/model/token_tailor.go)**、**[model/tiktoken/tiktoken.go](file:///workspace/model/tiktoken/tiktoken.go)**、**[agent/llmagent/llm_agent.go](file:///workspace/agent/llmagent/llm_agent.go)**：

```go
// 建议：paituo 的 override/calibration 机制作为 main calibration 的选项
type TokenCounterCalibrationOptions struct {
    Override bool
    ModelAware bool
    // ...
}

// 而非直接在模型层注入 override 逻辑
```

### 3. Metrics 与 Tracing 框架对齐

**[internal/flow/llmflow/llmflow.go](file:///workspace/internal/flow/llmflow/llmflow.go)**、**[telemetry/langfuse/exporter.go](file:///workspace/telemetry/langfuse/exporter.go)**：

```go
// 建议：使用 main 的 span attribute 方式
span.SetAttributes(
    semconv.KeyContextMetrics.Int64(paituoMetrics.Value),
)

// 而非独立的 metrics 变量
```

### 4. Stream 扩展字段隔离

**[openclaw/internal/gateway/stream.go](file:///workspace/openclaw/internal/gateway/stream.go)**、**[openclaw/gwproto/types.go](file:///workspace/openclaw/gwproto/types.go)**：

```go
// 建议：将 paituo 自定义字段放在结构体尾部
type StreamEvent struct {
    // ... main 的公共字段
    // --- paituo 扩展 ---
    PaituoDelta      string `json:"paituo_delta,omitempty"`
    PaituoToolResult *ToolResult `json:"paituo_tool_result,omitempty"`
}
```

### 5. 工具注册改为插件模式

**[openclaw/app/tooling_builtins.go](file:///workspace/openclaw/app/tooling_builtins.go)**、**[tool/luaexec/luaexec.go](file:///workspace/tool/luaexec/luaexec.go)**：

```go
// 建议：Lua 工具通过插件注册机制加载
func init() {
    registry.RegisterTool("lua", NewLuaTool(options...))
}
```

### 6. 文件工具扩展

**[tool/file/file.go](file:///workspace/tool/file/file.go)**、**[tool/file/readfile.go](file:///workspace/tool/file/readfile.go)**：

- Move/copy/delete 作为新工具添加
- Truncation 元数据保留
- 路径校验逻辑与 main 的安全策略对齐

---

## 第五部分: 合并操作建议

### 步骤1: 确定合并基准

```bash
# 确保 feat/paituo-integration 已同步到最新
git fetch origin
git checkout feat/paituo-integration
git pull origin feat/paituo-integration

# 确保 main 分支最新
git fetch origin main:refs/heads/main-latest
```

### 步骤2: 分层合并策略

1. **先合并低风险文件**（自动合并或简单协调）
   - tool/todo/todo.go
   - openclaw/app/tooling_builtins.go
   - model/tiktoken/tiktoken.go

2. **再合并中等风险文件**（手动协调）
   - model/token_tailor.go
   - openclaw/gwproto/types.go
   - openclaw/registry/registry.go

3. **最后合并高风险文件**（人工合并）
   - openclaw/app/run_options.go
   - agent/llmagent/llm_agent.go
   - model/openai/openai.go
   - internal/flow/llmflow/llmflow.go
   - openclaw/app/app.go

### 步骤3: 冲突解决原则

1. **配置字段冲突**：采用子结构体组合模式
2. **Token counter 冲突**：统一使用 main 的 calibration 框架
3. **Metrics/tracing 冲突**：使用 main 的 span attribute 方式
4. **Stream 字段冲突**：新增字段放在结构体尾部
5. **工具注册冲突**：采用插件注册模式

### 步骤4: 验证测试

```bash
go build ./...
go test ./... -count=1
```

重点测试：
- Token tailoring 流程
- Subagent tracing 链路
- 文件工具路径处理
- Stream 事件结构

---

## 总结

共发现 **20个文件** 被 paituo 和 main 分支**都修改过**，其中：

- **🔴 极高冲突**: 5个文件（需要人工深度合并）
- **🟡 中等冲突**: 11个文件（可协商合并）
- **🟢 低冲突**: 4个文件（自动合并）

**核心建议**:
1. 将 paituo 的配置字段抽离为独立子结构体
2. Token counter 机制与 main 的 calibration 框架统一
3. Metrics/tracing 改为使用 main 的 span attribute 方式
4. Stream 扩展字段放在结构体尾部
5. 工具注册改为插件模式

建议合并顺序：低风险 → 中等风险 → 高风险，每个阶段都进行测试验证。
