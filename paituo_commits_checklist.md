# Paituo Git 提交源码修改清单与合并风险评估报告

> 生成时间: 2026-06-11
> 作者: paituo
> 提交总数: 96
> 涉及文件总数: 3912
> 新增代码行: ~1,282,398
> 删除代码行: ~3,267

---

## 第一部分: 修改整体统计与分类

### 1.1 修改目的分类

| 类型 | 说明 | 涉及提交数 | 合并风险等级 |
|------|------|-----------|------------|
| **新功能(feat)** | 新增功能模块、工具集、扩展能力 | ~50 | ⚠️ 中-高 |
| **重构(refactor)** | 对核心逻辑、数据结构的调整 | ~15 | 🔴 高 |
| **修复(fix)** | Bug修复、错误处理补全 | ~15 | 🟡 中 |
| **构建/依赖(build/chore)** | go.mod/go.sum 依赖版本更新 | ~5 | 🟢 低 |
| **配置(config)** | openclaw.yaml 等配置文件调整 | ~5 | 🟢 低 |

### 1.2 按内容模块划分

```
├── 🔴 OpenClaw 应用核心 (修改最频繁, 风险最高)
│   ├── openclaw/app/              (app.go 被修改14次)
│   ├── openclaw/internal/gateway/ (stream.go 被修改9次)
│   ├── openclaw/app/run_options.go (被修改9次)
│   ├── openclaw/app/tooling_builtins.go (被修改6次)
│   ├── openclaw/registry/registry.go (被修改5次)
│   ├── openclaw/internal/subagentrun/ (service.go 被修改6次)
│   ├── openclaw/gwproto/types.go (被修改4次)
│   ├── openclaw/internal/gateway/server.go (被修改2次)
│   └── openclaw/openclaw.stdin.sqlite.yaml (被修改3次)
│
├── 🔴 Agent 框架核心 (高风险)
│   ├── agent/llmagent/llm_agent.go (被修改5次)
│   ├── agent/graphagent/graph_agent.go (被修改3次)
│   ├── agent/invocation.go (新增 ParentInvocationID)
│   └── agent/invocation_options.go (新增 ParentInvocationID)
│
├── 🔴 模型层 (高风险 - 改动核心抽象)
│   ├── model/token_tailor.go (被修改5次)
│   ├── model/tiktoken/tiktoken.go (被修改4次)
│   ├── model/model.go (被修改2次)
│   ├── model/openai/openai.go (被修改3次)
│   ├── model/token_tailor_test.go (被修改4次)
│   ├── [新增] model/calibrating_token_counter.go
│   ├── [新增] model/token_counter_registry.go
│   └── [新增] model/testing.go
│
├── 🟡 流程处理层 (中风险)
│   ├── internal/flow/llmflow/llmflow.go (被修改5次)
│   ├── internal/flow/processor/context_compact.go (被修改2次)
│   ├── internal/flow/processor/content.go (被修改2次)
│   └── internal/telemetry/metric_context.go (新增 + 修改)
│
├── 🟡 工具层 (中-高风险)
│   ├── tool/file/readfile.go (被修改5次 - 新增截断能力)
│   ├── tool/file/file.go (被修改4次 - 路径逻辑调整)
│   ├── tool/file/searchcontent.go (被修改2次)
│   ├── tool/file/readmultiplefiles.go (被修改2次)
│   ├── tool/todo/todo.go (被修改2次 - ID处理重构)
│   └── [新增大量] tool/luaexec/ (完整Lua工具集)
│
├── 🟢 遥测追踪 (低风险 - 多为新增埋点)
│   ├── telemetry/langfuse/exporter.go (被修改2次)
│   ├── telemetry/semconv/metrics/metrics.go (被修改2次)
│   └── [新增] internal/telemetry/metric_context.go
│
├── 🟢 平台层 (低风险 - 跨平台支持)
│   ├── [新增] internal/platform/platform.go
│   ├── [新增] internal/platform/platform_unix.go
│   ├── [新增] internal/platform/platform_windows.go
│   ├── [新增] openclaw/internal/octool/codepage_windows.go
│   └── [新增] openclaw/internal/octool/procgroup_windows.go
│
├── 🟢 代码执行器 (低风险 - 多为新增)
│   └── [新增] codeexecutor/local/procgroup_windows.go
│   └── [新增] codeexecutor/local/procgroup_other.go
│
├── 🟢 知识库嵌入器 (低风险 - 多为新增)
│   └── knowledge/embedder/* (各模型 embedder 新增 request/response trace)
│
├── 🟡 服务与UI (中风险)
│   ├── server/agui/translator/translator_test.go (被修改2次)
│   ├── openclaw/admin/static.go (被修改3次 - 静态资源挂载)
│   └── session/summary/checker.go (被修改2次)
│
├── 🟡 Runner/Plugin (中风险)
│   ├── runner/runner.go (被修改2次)
│   ├── plugin/toolsearch/llm_search.go (新增)
│   └── plugin/guardrail/* (新增 options pattern)
│
└── 🟢 其他 (低风险 - 全为新增)
    ├── openclaw/skills/* 预定义技能
    ├── examples/* 示例代码
    ├── docs/* 文档
    └── .github/* CI/CD 配置
```

---

## 第二部分: 高风险修改（对原有文件机制的侵入性修改）

### 🔴 2.1 核心入口文件的大量修改（最高风险）

**[openclaw/app/app.go](file:///workspace/openclaw/app/app.go)** — 修改14次，文件大小3462行
- 注入了大量自定义配置（context_threshold、chars_per_token、enable_execute_tools、planner_config 等）
- 注册了新的工具（file/move/copy/delete、luaexec）
- 注入了自定义 Langfuse 追踪逻辑
- 合并时极大概率会与上游的新功能注册产生冲突

**[openclaw/internal/gateway/stream.go](file:///workspace/openclaw/internal/gateway/stream.go)** — 修改9次，文件大小2156行
- 新增流式事件的 delta/status/model/finish_reason 等元数据字段
- 新增 tool_result 字段到 StreamEvent
- 新增 progress tracking 逻辑
- 这是网关核心协议层，修改频繁，合并时极易冲突

**[openclaw/app/run_options.go](file:///workspace/openclaw/app/run_options.go)** — 修改9次，文件大小2455行
- 新增大量可选配置字段（token tailoring、context compaction、planner、agent_instruction 等）
- 结构体字段增删极易与上游冲突

### 🔴 2.2 Agent 核心抽象的改动

**[agent/llmagent/llm_agent.go](file:///workspace/agent/llmagent/llm_agent.go)** — 修改5次，2337行
- 注入 token counter override / calibration 支持
- 新增 ParentInvocationID 传播

**[agent/graphagent/graph_agent.go](file:///workspace/agent/graphagent/graph_agent.go)** — 修改3次
- 注入类似的追踪和计数逻辑

**[agent/invocation.go](file:///workspace/agent/invocation.go) / [agent/invocation_options.go](file:///workspace/agent/invocation_options.go)**
- 新增 ParentInvocationID 字段，影响跨模块调用链

### 🔴 2.3 模型层核心抽象重构（最高风险之一）

**[model/token_tailor.go](file:///workspace/model/token_tailor.go)** — 修改5次，876行
- 这是对 token 裁剪策略的核心实现
- 同时在 model/model.go、model/openai/openai.go、model/tiktoken/tiktoken.go 中注入了对 tailoring 的调用
- 跨多个文件的连锁修改，合并风险极高

**新增文件(但调用链渗透到核心):**
- [model/calibrating_token_counter.go](file:///workspace/model/calibrating_token_counter.go)
- [model/token_counter_registry.go](file:///workspace/model/token_counter_registry.go)
- [model/testing.go](file:///workspace/model/testing.go)

### 🔴 2.4 流处理层的侵入性修改

**[internal/flow/llmflow/llmflow.go](file:///workspace/internal/flow/llmflow/llmflow.go)** — 修改5次，2034行
- 注入 context metrics tracking
- 新增 model-aware token counter 调用
- 修复 LLM 调用失败时的链路追踪

**[internal/flow/processor/context_compact.go](file:///workspace/internal/flow/processor/context_compact.go) / [content.go](file:///workspace/internal/flow/processor/content.go)**
- 修改2次
- 注入压缩逻辑的埋点和自定义行为

### 🔴 2.5 工具层对原有文件处理逻辑的改动

**[tool/file/readfile.go](file:///workspace/tool/file/readfile.go)** — 修改5次
- 新增 readPartialLines 大文件截断
- 新增 truncated/total_lines/file_size 元数据
- 修改路径校验逻辑

**[tool/file/file.go](file:///workspace/tool/file/file.go)** — 修改4次
- 修复基准目录依赖当前工作目录的问题
- 调整路径校验逻辑

**[tool/todo/todo.go](file:///workspace/tool/todo/todo.go)** — 修改2次
- 重构 ID 处理逻辑，强化 ID 稳定性
- 新增 parent_id 字段支持

### 🔴 2.6 Subagent 运行时的大量改动

**[openclaw/internal/subagentrun/service.go](file:///workspace/openclaw/internal/subagentrun/service.go)** — 修改6次
- 新增 ParentInvocationID 传播
- 新增 inline global instruction 支持
- 新增 otel span attributes
- 新增 title/ref 字段
- 新增 enhanced error diagnostic
- 新增 trace link test

**[openclaw/internal/subagentrun/tool.go](file:///workspace/openclaw/internal/subagentrun/tool.go)** — 修改3次
- 新增调试追踪支持

---

## 第三部分: 新增功能（低/中合并风险，主要是新增文件）

### 🟡 3.1 Lua 脚本执行工具集（[tool/luaexec/](file:///workspace/tool/luaexec/)）

这是最大的一个新增模块，提交中反复修改和扩展：

| 文件 | 说明 | 提交次数 |
|------|------|---------|
| [bridge_html.go](file:///workspace/tool/luaexec/bridge_html.go) | HTML 解析桥接 | 多次 |
| [bridge_md.go](file:///workspace/tool/luaexec/bridge_md.go) | Markdown 桥接 | 多次 |
| [bridge_yaml.go](file:///workspace/tool/luaexec/bridge_yaml.go) | YAML 序列化桥接 | 多次 |
| [bridge_table.go](file:///workspace/tool/luaexec/bridge_table.go) | 表格处理桥接 | 多次 |
| [bridge_tool.go](file:///workspace/tool/luaexec/bridge_tool.go) | 工具调用桥接 | 多次 |
| [bridge_summarize.go](file:///workspace/tool/luaexec/bridge_summarize.go) | TF-IDF 摘要 | 最新 |
| [bridge_re.go](file:///workspace/tool/luaexec/bridge_re.go) | 正则桥接 | 多次 |
| [bridge_log.go](file:///workspace/tool/luaexec/bridge_log.go) | 日志桥接 | 多次 |
| [lua_exec.go](file:///workspace/tool/luaexec/lua_exec.go) | Lua 执行核心 | 3次 |
| [luaexec.go](file:///workspace/tool/luaexec/luaexec.go) | Lua 工具主入口 | 3次 |
| [config.go](file:///workspace/tool/luaexec/config.go) | 配置 | 3次 |
| [sandbox.go](file:///workspace/tool/luaexec/sandbox.go) | 沙箱环境 | 3次 |
| [converter.go](file:///workspace/tool/luaexec/converter.go) | 数据转换 | 2次 |
| [fileio.go](file:///workspace/tool/luaexec/fileio.go) | 文件IO | 2次 |
| [errors.go](file:///workspace/tool/luaexec/errors.go) | 错误处理 | 2次 |

**⚠️ 注意：** 虽然 luaexec 本身是新增文件（低合并冲突风险），但它在 openclaw/app/app.go、openclaw/app/tooling_builtins.go 中被注册为工具，这些注册点会与上游产生冲突。

### 🟡 3.2 新增文件操作工具（[tool/file/](file:///workspace/tool/file/)）

- [movefiles.go](file:///workspace/tool/file/movefiles.go) / [movefiles_test.go](file:///workspace/tool/file/movefiles_test.go) — 移动文件
- [copyfiles.go](file:///workspace/tool/file/copyfiles.go) / [copyfiles_test.go](file:///workspace/tool/file/copyfiles_test.go) — 复制文件
- [deletefiles.go](file:///workspace/tool/file/deletefiles.go) / [deletefiles_test.go](file:///workspace/tool/file/deletefiles_test.go) — 删除文件

### 🟢 3.3 平台/跨平台支持（[internal/platform/](file:///workspace/internal/platform/)）

- [platform.go](file:///workspace/internal/platform/platform.go) — 定义 Shell 和 BuildCommand 接口
- [platform_unix.go](file:///workspace/internal/platform/platform_unix.go) — Unix shell 实现
- [platform_windows.go](file:///workspace/internal/platform/platform_windows.go) — Windows shell 实现

- [openclaw/internal/octool/codepage_windows.go](file:///workspace/openclaw/internal/octool/codepage_windows.go) — Windows 控制台代码页转 UTF-8
- [openclaw/internal/octool/procgroup_windows.go](file:///workspace/openclaw/internal/octool/procgroup_windows.go) — Windows 进程组管理

### 🟢 3.4 代码执行器 Windows 支持

- [codeexecutor/local/procgroup_windows.go](file:///workspace/codeexecutor/local/procgroup_windows.go) — Windows Job Objects 进程组管理
- [codeexecutor/local/procgroup_other.go](file:///workspace/codeexecutor/local/procgroup_other.go) — 非 Windows stub

### 🟢 3.5 遥测/追踪增强（多为新增埋点）

- [internal/telemetry/metric_context.go](file:///workspace/internal/telemetry/metric_context.go) — 新增 metric context
- 修改 [telemetry/langfuse/exporter.go](file:///workspace/telemetry/langfuse/exporter.go) — 优化追踪名称、加入用户ID

### 🟡 3.6 Guardrail options pattern 改造（[plugin/guardrail/](file:///workspace/plugin/guardrail/)）

- [approval/approval.go](file:///workspace/plugin/guardrail/approval/approval.go) — 新增 WithApprovalEnabled
- [promptinjection/promptinjection.go](file:///workspace/plugin/guardrail/promptinjection/promptinjection.go) — 新增 WithPromptInjectionEnabled
- [unsafeintent/unsafeintent.go](file:///workspace/plugin/guardrail/unsafeintent/unsafeintent.go) — 新增 WithUnsafeIntentEnabled

这属于对既有文件的侵入性修改，虽然是扩展，但若上游也在演进同名接口，会冲突。

### 🟢 3.7 知识库 embedder 追踪增强（[knowledge/embedder/](file:///workspace/knowledge/embedder/)）

对 openai/ollama/huggingface/gemini 各 embedder 增加了 request/response trace 埋点。
主要是新增调用，一般不冲突。

### 🟡 3.8 OpenClaw 配置文件

- [openclaw/openclaw.stdin.sqlite.yaml](file:///workspace/openclaw/openclaw.stdin.sqlite.yaml) — 修改3次
- [openclaw/openclaw.yaml](file:///workspace/openclaw/openclaw.yaml) — 主配置（可能与上游结构不同）

---

## 第四部分: 合并风险等级详表

### 🔴 高合并风险（侵入核心抽象/协议/入口，极可能冲突）

| 文件 | 修改次数 | 侵入性说明 | 建议处理方式 |
|------|---------|-----------|-------------|
| [openclaw/app/app.go](file:///workspace/openclaw/app/app.go) | 14 | 主应用入口，配置与工具注册密集改动 | ⚠️ 建议 cherry-pick 逐条合并 |
| [openclaw/internal/gateway/stream.go](file:///workspace/openclaw/internal/gateway/stream.go) | 9 | 流式事件协议扩展 | ⚠️ 建议抽离为独立 proto 定义 |
| [openclaw/app/run_options.go](file:///workspace/openclaw/app/run_options.go) | 9 | 配置结构体反复扩展 | ⚠️ 建议使用组合模式而非直接添加字段 |
| [model/token_tailor.go](file:///workspace/model/token_tailor.go) | 5 | Token 裁剪策略核心实现 | ⚠️ 考虑作为独立 plugin 模块 |
| [agent/llmagent/llm_agent.go](file:///workspace/agent/llmagent/llm_agent.go) | 5 | LLM Agent 主流程注入 | ⚠️ 改用回调/观察者模式 |
| [internal/flow/llmflow/llmflow.go](file:///workspace/internal/flow/llmflow/llmflow.go) | 5 | LLM 流处理层注入 | ⚠️ 抽离中间件模式 |
| [openclaw/internal/subagentrun/service.go](file:///workspace/openclaw/internal/subagentrun/service.go) | 6 | Subagent 运行时多次修改 | ⚠️ 建议保留为本地扩展 |
| [model/tiktoken/tiktoken.go](file:///workspace/model/tiktoken/tiktoken.go) | 4 | Tokenizer 接口扩展 | ⚠️ 考虑通过选项模式扩展 |
| [openclaw/app/tooling_builtins.go](file:///workspace/openclaw/app/tooling_builtins.go) | 6 | 工具注册入口反复修改 | ⚠️ 建议使用插件式注册 |
| [openclaw/registry/registry.go](file:///workspace/openclaw/registry/registry.go) | 5 | 注册表注入 planner/token_tailor | ⚠️ 建议通过选项模式扩展 |
| [agent/graphagent/graph_agent.go](file:///workspace/agent/graphagent/graph_agent.go) | 3 | Graph Agent 注入 | ⚠️ 同上 |
| [model/openai/openai.go](file:///workspace/model/openai/openai.go) | 3 | OpenAI 模型层扩展 vLLM/tailoring | ⚠️ 考虑独立 variant 分支 |
| [openclaw/gwproto/types.go](file:///workspace/openclaw/gwproto/types.go) | 4 | 网关协议类型扩展 | ⚠️ 建议做向后兼容的字段添加 |

### 🟡 中等合并风险（侵入既有文件，但改动范围可控）

| 文件 | 修改次数 | 说明 |
|------|---------|------|
| [tool/file/readfile.go](file:///workspace/tool/file/readfile.go) | 5 | 大文件截断/元数据增强 |
| [tool/file/file.go](file:///workspace/tool/file/file.go) | 4 | 路径校验逻辑调整 |
| [tool/todo/todo.go](file:///workspace/tool/todo/todo.go) | 2 | ID 处理逻辑重构 |
| [internal/flow/processor/context_compact.go](file:///workspace/internal/flow/processor/context_compact.go) | 2 | 上下文压缩埋点 |
| [internal/flow/processor/content.go](file:///workspace/internal/flow/processor/content.go) | 2 | 内容处理埋点 |
| [openclaw/internal/subagentrun/tool.go](file:///workspace/openclaw/internal/subagentrun/tool.go) | 3 | Subagent tool 调试追踪 |
| [openclaw/internal/gateway/server.go](file:///workspace/openclaw/internal/gateway/server.go) | 2 | 网关服务 SSE 修复 |
| [openclaw/internal/gateway/server_test.go](file:///workspace/openclaw/internal/gateway/server_test.go) | 3 | 测试文件扩展 |
| [openclaw/app/app_test.go](file:///workspace/openclaw/app/app_test.go) | 3 | 测试文件扩展 |
| [openclaw/app/backends.go](file:///workspace/openclaw/app/backends.go) | 3 | 后端初始化扩展 |
| [openclaw/admin/static.go](file:///workspace/openclaw/admin/static.go) | 3 | 静态资源挂载 |
| [telemetry/langfuse/exporter.go](file:///workspace/telemetry/langfuse/exporter.go) | 2 | Langfuse 追踪优化 |
| [runner/runner.go](file:///workspace/runner/runner.go) | 2 | Runner 扩展 |
| [session/summary/checker.go](file:///workspace/session/summary/checker.go) | 2 | 会话摘要检查器 |
| [model/model.go](file:///workspace/model/model.go) | 2 | 模型 Info 结构体统一 |
| [openclaw/openclaw.stdin.sqlite.yaml](file:///workspace/openclaw/openclaw.stdin.sqlite.yaml) | 3 | 配置文件 |
| [openclaw/go.mod](file:///workspace/openclaw/go.mod) | 3 | 依赖版本变更 |
| [agent/invocation.go](file:///workspace/agent/invocation.go) | 1+ | ParentInvocationID 注入 |
| [agent/invocation_options.go](file:///workspace/agent/invocation_options.go) | 1+ | ParentInvocationID 注入 |

### 🟢 低合并风险（以新增文件为主，几乎不冲突）

| 目录/文件 | 说明 |
|----------|------|
| [tool/luaexec/](file:///workspace/tool/luaexec/) | Lua 工具集（几乎全新文件） |
| [tool/file/movefiles.go](file:///workspace/tool/file/movefiles.go) + [copyfiles.go](file:///workspace/tool/file/copyfiles.go) + [deletefiles.go](file:///workspace/tool/file/deletefiles.go) | 新增文件操作工具 |
| [internal/platform/](file:///workspace/internal/platform/) | 新增跨平台 shell 抽象 |
| [openclaw/internal/octool/codepage_windows.go](file:///workspace/openclaw/internal/octool/codepage_windows.go) | 新增 Windows 代码页处理 |
| [openclaw/internal/octool/procgroup_windows.go](file:///workspace/openclaw/internal/octool/procgroup_windows.go) | 新增 Windows 进程组管理 |
| [codeexecutor/local/procgroup_windows.go](file:///workspace/codeexecutor/local/procgroup_windows.go) | 新增 Windows Job Objects |
| [codeexecutor/local/procgroup_other.go](file:///workspace/codeexecutor/local/procgroup_other.go) | 新增非 Windows stub |
| [model/calibrating_token_counter.go](file:///workspace/model/calibrating_token_counter.go) | 新增校准 token counter |
| [model/token_counter_registry.go](file:///workspace/model/token_counter_registry.go) | 新增 token counter 注册表 |
| [model/testing.go](file:///workspace/model/testing.go) | 新增测试工具 |
| [internal/telemetry/metric_context.go](file:///workspace/internal/telemetry/metric_context.go) | 新增 metric context |
| [openclaw/app/planners.go](file:///workspace/openclaw/app/planners.go) | 新增 planner 相关 |
| [openclaw/plugins/stdin/mdrenderer/render.go](file:///workspace/openclaw/plugins/stdin/mdrenderer/render.go) | 新增 markdown 渲染 |
| [openclaw/skills/*](file:///workspace/openclaw/skills/) | 新增预定义技能（大量文件） |
| [openclaw/examples/*](file:///workspace/openclaw/examples/) | 新增示例 |
| [docs/*](file:///workspace/docs/) | 新增文档 |
| [.github/*](file:///workspace/.github/) | 新增 CI/CD 配置 |

---

## 第五部分: 关键修改主题归纳（按提交分组）

### 主题1: Token 计数与裁剪重构（跨模块连锁改动）
涉及提交:
- 5a2d292d feat(registry,app): add token tailoring support for openai models
- 7eb0c922 feat(model, session): add model-aware token counter support
- a58e189b feat(llm,flow): add context metrics tracking and model-aware token counter
- fdee3715 refactor: 统一模型Info构造，新增测试工具和token counter管理
- d752c373 refactor(openai model): 调整tailoring策略相关实现
- 947fc02f feat(llmagent,model): add token counter override and calibration support
- e5d7f6ef feat(openai): add support for vLLM model variant
- 78214a29 / d630b682 model: Info 字段扩展 / token counter fallback

**风险点**: 这是最核心的侵入性改动，跨 model/registry/app/llmagent/flow 多层。如果上游在演进自己的 token 管理方案，几乎必然需要大规模重构合并。

### 主题2: Lua 工具集反复迭代（多次提交）
涉及提交:
- 059d5c4b feat(luaexec): 新增GopherLua脚本执行工具集
- 5f4dab04 feat(luaexec): 新增HTML和Markdown桥接模块
- fa9f99f2 feat(luaexec): 新增脚本路径加载功能与编码自动检测
- 62af2232 feat(luaexec): 新增日志收集、编码处理和工具增强
- 89797876 feat: Lua脚本中新增HTML表格解析与文本摘要能力
- edd97f64 feat: add lua debug config
- e00e6018 feat(luaexec): add TF-IDF based summarization and keyword extraction

**风险点**: Lua 工具自身代码相对独立，但每次迭代都修改 config.go/sandbox.go/lua_exec.go，多次叠加修改，重构时建议按模块拆解。

### 主题3: Subagent 调试追踪与 ParentInvocationID 传播
涉及提交:
- 9a5a56f4 agent: add ParentInvocationID to Invocation and RunOptions
- ac5896ef agent/llmagent/runner: propagate ParentInvocationID through trace chain
- b8363279 subagentrun: propagate ParentInvocationID in service dispatch
- c6898441 subagentrun: add ParentInvocationID to SpawnRequest
- 44008c1b feat(subagentrun): add inline global instruction support
- 4b98ec04 feat(subagentrun): add title and ref field support
- 271f9eb4 feat(subagentrun): add enhanced error diagnostic
- 7c84b0ee feat(subagent): add debug tracing support
- f284f2e9 feat: add response id tracking and child trace association
- eb210af4 feat(subagentrun): add otel span attributes and config
- 5209e53a subagentrun: add trace link test

**风险点**: ParentInvocationID 是对 Invocation 核心数据结构的修改，向上游合并前需确认上游也在做类似演进。

### 主题4: 文件工具增强与路径处理重构
涉及提交:
- a71fdfbc feat(file tool): add truncated, total lines and file size metadata
- cc7f371c readfile: add readPartialLines for automatic line count truncation
- 3bf5fefb readmultiplefiles: apply readPartialLines truncation to output
- abe38555 searchcontent: skip binary files and limit matches per file
- 7f117c53 listfile: improve empty directory message and use shared summary helper
- 09218a01 fix(tool/file): 修复文件读取和搜索逻辑
- b131cf90 fix(file): 调整路径校验逻辑，允许使用baseDir内的绝对路径
- 1b18ebaf fix(file): 修复基准目录依赖当前工作目录的问题
- 00f4ecec feat(filetool): 新增移动、复制、删除文件工具
- f6d20f97 feat: 添加文件移动复制删除工具支持，优化技能根路径加载逻辑
- 75a1ca51 feat(filetool): add max tool result chars config to limit response size

**风险点**: 对已有路径逻辑有改动，需重点验证合并后行为一致性。

### 主题5: 网关/流事件协议扩展
涉及提交:
- 18026b84 feat(gateway): 新增流式事件状态 delta、模型和结束原因元数据
- 597fd6e7 feat(gwproto/stream): add tool result field to stream event and progress tracking
- f4b8622a refactor(processor): 重构增量消息获取逻辑并新增统计埋点
- 09550ff7 fix(gateway): 修复JSON结构破坏问题
- 635507b fix(gateway test): 修复单元测试中缺少 nil 错误参数

### 主题6: 遥测与 Langfuse 追踪优化
涉及提交:
- 7e1ed5c9 refactor(langfuse): 优化追踪名称生成，加入用户ID信息
- 9f525685 fix: 修复多处遥测追踪不完整的问题
- b04aab0f telemetry: add core context compaction and token metrics
- 3725875a telemetry: add metric context tests
- 61137aa7 + 58932fd0 + 879f324b + 2d7171e3 knowledge/embedder: 为各模型 embedder 添加 request/response trace

### 主题7: Windows 跨平台支持（纯新增）
涉及提交:
- c82ded3f refactor(internal/octool): 替换旧版Windows代码页常量为新命名
- bdf59b6 openclaw/octool: add Windows console codepage to UTF-8 conversion
- afcc6192 openclaw/octool: add Windows process group management for octool
- d219bd8 codeexecutor: add Windows process group management via Job Objects
- e6214a4 platform: add Windows shell implementation
- f4eeebb7 platform: add Unix shell implementation
- c8277bd8 platform: define Shell and BuildCommand interfaces with common tests

### 主题8: OpenClaw 配置与场景化
涉及提交:
- 9f82a176 refactor(openclaw-config): 更新配置为电力造价工程师专属场景
- c8d7118d config(stdin-sqlite): 重构并完善 openclaw 配置文件
- 0b6a4e3 / 6d1e475a / 9aca874b / 8f524894 openclaw/app: 多次注入 YAML struct 字段和 agentConfig wiring

### 主题9: 其他杂项改进
- 17f56d4b feat(log): add file log output and debug log auto-enable
- 097faaa2 chore: 添加 DIAG-CTX-CANCEL 调试日志
- 9a7433cc fix(workspace&tool): 补全错误处理链路并优化错误返回
- dc533ee feat(stdin): 新增彩色终端输出、markdown渲染和OOB消息支持
- 3e6f0c64 feat(admin): add static file mounting support for admin server
- b1789b38 fix(team/runtime) + 257f8bbf refactor(tool/todo): 修复编码问题，重构ID处理
- e00e6018 feat(luaexec): add TF-IDF based summarization (最新一次提交)
- 49dbe7ea feat(app): add enable-execute-tools switch and refine tool guidance
- 68bb2d17 feat(app): 添加上下文压缩每token近似符文数配置
- d8a496db / e4d3d0fb / 9c24c36e guardrail/*: add options pattern

---

## 第六部分: 合并建议与优化方案

### 6.1 最高优先级: 隔离对核心文件的侵入

**问题**: [openclaw/app/app.go](file:///workspace/openclaw/app/app.go) 被修改14次，包含大量本地定制。
**建议**:
1. 将所有本地自定义配置字段抽离到一个独立的 `app_local_config.go` 文件，通过 `func init()` 或 Option 模式注入
2. 将 luaexec、file/move/copy/delete 等工具的注册，从 `tooling_builtins.go` 改为通过插件注册机制动态加载
3. 将 token tailoring 相关代码抽离为独立的 `model/tailoring/` 子模块，通过选项模式（Options Pattern）在模型初始化时注入，而非直接修改 `model/model.go`

**问题**: [openclaw/internal/gateway/stream.go](file:///workspace/openclaw/internal/gateway/stream.go) 被修改9次，涉及协议字段扩展
**建议**:
1. 新增字段尽量放在结构体尾部保持向后兼容
2. delta/status/finish_reason 等自定义字段考虑放在 `map[string]interface{}` 或独立扩展块中
3. 抽离 `stream_extensions.go` 存放本分支自定义逻辑

### 6.2 ParentInvocationID 跨模块传播的合并策略

这是对 [agent/invocation.go](file:///workspace/agent/invocation.go) 和 [agent/invocation_options.go](file:///workspace/agent/invocation_options.go) 的结构性修改。
**建议**:
- 如果上游没有类似字段，建议在合并时保留，但用注释标记为"本分支扩展字段"
- 尽量通过 "propagate through trace chain" 的方式（而非在调用链每个节点添加参数）实现，减少对每个调用点的侵入

### 6.3 Lua 工具集的隔离策略

虽然 [tool/luaexec/](file:///workspace/tool/luaexec/) 是新增模块（合并安全），但它的初始化和注册渗透在：
- openclaw/app/app.go（工具注册）
- openclaw/app/tooling_builtins.go
- 多个 run_options 配置字段

**建议**:
- 将 luaexec 工具注册改为通过 go:build tag 或 环境变量启用
- 配置字段放到独立的 `luaexec_config` 子结构体下
- 避免直接在 tooling_builtins.go 的大 switch/case 中添加分支

### 6.4 Windows 跨平台代码

这部分几乎全是新增文件，风险很低。注意事项：
- 确保 `internal/platform/` 的接口设计足够通用，上游如果也做了类似抽象，比较命名和签名
- 对 `openclaw/internal/octool/` 的新增文件，确认不会与上游将来的 Windows 支持实现重名冲突

### 6.5 文件工具的路径逻辑修改

对 [tool/file/file.go](file:///workspace/tool/file/file.go) 和 [tool/file/readfile.go](file:///workspace/tool/file/readfile.go) 的路径处理逻辑改动需要重点关注：
- 建议编写专门的路径解析单测，合并上游后重跑验证
- "允许使用baseDir内的绝对路径"这个改动可能与上游的安全策略冲突，需重点确认

### 6.6 go.mod/go.sum 依赖变更

- 对依赖版本的变更（go.mod 修改3次）合并时建议以 `go mod tidy` 重新生成后合并，而非直接应用 diff
- 如果上游引入了与本地不同版本的依赖，可能需要人工解决版本冲突

### 6.7 Guardrail options pattern 改造

对 `plugin/guardrail/*` 三个文件添加了 WithXxxEnabled 选项模式。
**建议**:
- 检查上游是否已有类似或更好的扩展方式
- 如果上游也在演进 options pattern，可能需要重构为同一模式

### 6.8 测试文件的大量新增

多个 `*_test.go` 文件被修改（app_test.go、server_test.go、token_tailor_test.go 等），这本身不影响运行时行为，但：
- 新增测试可能依赖本分支新增的功能（如 ParentInvocationID）
- 合并上游时可能需要先合并功能代码，再调整测试

---

## 第七部分: 合并操作的建议流程

### 步骤1: 隔离新增文件（低风险优先合并）
1. 先将纯新增的文件（tool/luaexec/*, internal/platform/*, model/calibrating_token_counter.go 等）cherry-pick 或直接复制到最新上游分支
2. 这部分基本无冲突，可以立即完成

### 步骤2: 处理增量扩展功能（中等风险）
1. 对 gateway/stream.go 的扩展字段（放在结构体尾部）
2. 对 file/readfile.go 的截断能力（作为独立函数添加而非改造原函数）
3. 对 todo/todo.go 的 ID 稳定化
4. 对 session/summary/checker.go 的扩展

### 步骤3: 处理侵入性修改（高风险，逐条处理）
这是最耗时的部分，建议按主题逐一处理：
1. Token tailoring 主题 → 先抽离为独立模块再合并
2. ParentInvocationID 主题 → 检查上游是否已有对应概念，有则对齐，无则保持
3. Subagent debug tracing 主题 → 检查追踪字段命名是否与上游一致
4. Langfuse 追踪优化 → 与上游追踪实现比对命名和字段

### 步骤4: 入口文件与配置文件（最后处理）
- openclaw/app/app.go, run_options.go, registry.go — 在所有功能模块合并完成后，最终串联配置
- openclaw/openclaw*.yaml — 场景化配置保留为本地 config 文件，不要强行合并到上游默认配置

### 步骤5: 验证与测试
- 运行 `go build ./...` 和 `go test ./...` 确认编译通过
- 特别关注路径处理、Windows 平台逻辑的端到端测试
- 对比修改前后的 Langfuse trace 结构，确保追踪连续性

---

## 总结: 合并难度评估

| 维度 | 状态 | 说明 |
|------|------|------|
| 总提交数 | 96 | 中等规模 |
| 纯新增文件占比 | ≈ 70% | 大多数是新增代码，冲突可控 |
| 核心文件侵入 | 高 | app.go、stream.go、run_options.go、token_tailor.go 等被反复修改 |
| 跨模块连锁修改 | 高 | ParentInvocationID、token tailoring 主题跨多层 |
| 建议合并方式 | 分主题 cherry-pick | 不要试图一次性 rebase 96个提交 |
| 预估工作量 | 较大 | 需要 2-3 个工作日仔细甄别每个主题与上游的兼容性 |
| 建议策略 | 抽离插件化 + 逐步替换 | 将侵入性代码改为 Options Pattern / Plugin Pattern 再合并 |

> **核心建议**: 最关键的优化是将对 `openclaw/app/app.go`、`openclaw/internal/gateway/stream.go`、`model/token_tailor.go`、`agent/llmagent/llm_agent.go` 这几个"高频修改文件"的侵入式改动，抽离为独立模块 + Options Pattern 注入，这会显著降低每次上游同步的合并成本。
