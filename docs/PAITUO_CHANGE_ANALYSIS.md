# PAITUO 提交逐项分析报告

> 生成日期：2026-05-30（已根据上游 #1876 worktree 对比结果 + 精确 diff 统计更新）
> 对比基线：`upstream/main` (`4f2670297`) vs 当前 HEAD（含 PAITUO 合并）
> 变更规模：93 个文件，+5109/-2729 行
> **Worktree 模块结论：上游 #1876 已 100% 覆盖，PAITUO 无需保留任何 worktree 代码**
> **ParentInvocationID + trace_link_test.go 归属第十节 Trace Linking，非 worktree 功能**
> **核心模块精确 diff：49 个文件，+3015/-2371 行**

---

## YC Office Hours 总览

**问题：** PAITUO fork 要在一个纯 Linux/macOS 的 Go agent 框架上跑 Windows 生产环境，同时需要更细粒度的遥测、subagent 隔离和 token 预算控制。上游没做这些——不是它们不重要，是上游只面向 Linux 开发者。

**用户：** 在 Windows 服务器上部署 openclaw 的企业团队，需要进程隔离、中文编码支持、生产级可观测性。

**今天：** 上游代码在 Windows 上部分功能直接不可用（shell 检测、进程树管理、控制台编码），上下文压缩缺少工具维度的控制。团队只能 fork + 改造。

**关键发现：** subagent worktree 隔离已在同期上游 `#1876` 中实现且质量更高。PAITUO 此部分改动从 1186 行缩减为约 70 行（仅 2 项增量）。

**风险：** 整体变更 5109 行。但上游 #1876 已覆盖 1926 行 worktree 逻辑，实际 PAITUO 独有改动约 3000 行。telemetry 和 llmflow 仍是最需关注的高冲突区域。

---

## 变更量精确统计

基于 `git diff upstream/main..HEAD --stat` 按模块汇总：

| 模块 | 文件数 | +行数 | -行数 | 净增 | 风险 |
|------|--------|-------|-------|------|------|
| `openclaw/internal/subagentrun/` | 7 | +939 | -1071 | -132 | **高**（上游 #1876 覆盖） |
| `agent/taskrun/` | 6 | +380 | -742 | -362 | **高**（上游 #1876 覆盖） |
| `internal/telemetry/` | 4 | +1106 | 0 | +1106 | 中 |
| `internal/flow/llmflow/` + `processor/` | 7 | +305 | -75 | +230 | **高** |
| `openclaw/app/app.go` | 1 | +192 | -81 | +111 | 中 |
| `openclaw/app/run_options.go` | 1 | +97 | -8 | +89 | 中 |
| `tool/file/` | 7 | +370 | 0 | +370 | 中 |
| `internal/platform/` | 5 | +309 | 0 | +309 | 低 |
| `model/` | 2 | +19 | 0 | +19 | 低 |
| `codeexecutor/local/` | 5 | +161 | -35 | +126 | 低 |
| `plugin/guardrail/` | 6 | +71 | 0 | +71 | 低 |
| `knowledge/embedder/` | 4 | +30 | -1 | +29 | 低 |
| `agent/` (不含 taskrun) | 4 | +50 | 0 | +50 | 中 |
| `openclaw/internal/octool/` | 3 | +280+ | 0 | +280 | 低 |
| **其余文件** | ~30 | ~800 | ~717 | ~83 | 多样 |

---

## 逐项分析（基于精确 diff）

### 一、Windows 跨平台 Shell 抽象 (`internal/platform/`)

| 维度 | 详情 |
|------|------|
| **状态** | 新增 5 个文件（A 状态） |
| **精确文件** | `platform.go` (+17), `platform_test.go` (+58), `platform_unix.go` (+46), `platform_windows.go` (+111), `platform_windows_test.go` (+77) |
| **合计** | +309 行，零删除 |
| **内容** | `platform.go` 定义 `Shell()` / `BuildCommand()` 接口；`_unix.go` 返回 bash/sh；`_windows.go` 检测 PowerShell/cmd，排除非原生 shell（Git Bash、MSYS2） |
| **原因** | 上游硬编码了 `bash` 路径查找，Windows 上没有 bash，代码执行器直接挂。这是生存问题，不是优化问题。 |
| **上游状态** | 上游没有平台抽象层，`codeexecutor/local/` 直接调用 `exec.LookPath("bash")` |
| **必要性** | **必须**。没有这个，Windows 上 `codeexecutor` 完全不可用。 |
| **方案评价** | 设计合理。`BuildCommand` 返回值是 `(name string, args ...string)` 的标准 Go exec 接口。`_windows.go` 排除了 Git Bash/MSYS2 的非原生 shell 是经验之举——那些 shell 在 Windows 环境下行为诡异。 |
| **建议** | 可以向上游提 PR。平台抽象是纯粹的增量，零破坏性。 |

---

### 二、Windows 进程树管理

**(a) `codeexecutor/local/procgroup_windows.go`**

| 维度 | 详情 |
|------|------|
| **精确文件** | `procgroup_windows.go` (+90 新文件), `procgroup_other.go` (+24 新文件), `local.go` (+10), `local_test.go` (+35), `workspace_runtime_interactive.go` (+32), `workspace.go` (+8), `workspace_test.go` (+9) |
| **合计** | +208 行 |
| **内容** | Windows Job Object 包装器，用于子进程树生命周期管理。`Kill()` 时通过 `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` 实现整棵进程树统一清理。`procgroup_other.go` 是 Unix 桩。 |
| **原因** | Windows 没有 Unix 的进程组（process group）概念。`taskkill /T` 不可靠——子进程可能逃逸。Job Object 是 Windows 正确的进程树管理方式。 |
| **方案评价** | 实现精良。有并发安全（mutex 保护 `close()`）、跨平台编译（other.go 桩）、单元测试。 |

**(b) `openclaw/internal/octool/procgroup_windows.go`**

独立于 (a)，为 octool 提供进程树管理，架构相同。建议与 (a) 合并为一个 PR。

**建议**：非常好的上游 PR 候选。

---

### 三、Windows 控制台编码转换 (`openclaw/internal/octool/codepage_windows.go`)

| 维度 | 详情 |
|------|------|
| **精确文件** | `codepage_windows.go`（新增 1 文件） |
| **内容** | 使用 `golang.org/x/sys/windows` 的 `GetACP()` / `MultiByteToWideChar` 将 Windows 控制台 GBK/GB2312 输出转为 UTF-8 |
| **原因** | Windows 中文版控制台默认编码是 GBK（CP936），而 Go 字符串是 UTF-8。不转换直接乱码。 |
| **必要性** | **必须**。中文 Windows 环境下子进程输出直接乱码。 |
| **方案评价** | `sync.Once` 缓存是好的优化。但只处理了 `MultiByteToWideChar`，没做 UTF-8 有效性检测。 |
| **建议** | 重实现时加 UTF-8 探测快速路径（`utf8.Valid()` 检测，避免误转已为 UTF-8 的输出）。 |

---

### 四、详细上下文遥测 (`internal/telemetry/`)

| 维度 | 详情 |
|------|------|
| **精确文件** | `metric_context.go` (+626 新文件), `metric_context_test.go` (+353 新文件), `trace.go` (+127), 另修改 `metric.go`、`semconv/metrics.go`、`langfuse/exporter.go` |
| **合计** | +1106 行，零删除 |
| **内容** | 定义了细粒度上下文指标：compaction 触发次数、保留/清理的工具名列表、token 预算使用率等。`trace.go` 新增 span 属性记录。 |
| **原因** | 上游的遥测太粗——只知道"压缩发生了"，不知道压缩了什么、为什么。 |
| **方案评价** | 新增文件的方式好——不污染上游现有代码。但 1106 行量较大，超过一半在测试文件中（353 行测试），核心逻辑 626 行。一些指标如果实际运维中用不上，会成为死代码。 |
| **建议** | 保留核心指标（compaction 触发、token 使用率），裁剪次要指标后再提交。首次提交只保留 ~200 行核心逻辑，其余标记为 TODO。 |

---

### 五、配置/YAML/模型字段扩展（已大幅细分）

此类别覆盖 3 个文件的精确变更：

#### 5a. `openclaw/app/run_options.go` (+97/-8 = +89 行净增)

实际新增内容比原分析多得多：

| 新增项 | 类型 | YAML 字段 |
|--------|------|-----------|
| `SessionSummaryInjectionMode` | `string` | `session_summary_injection_mode` |
| `SyncSummaryIntraRun` | `bool` | `sync_summary_intra_run` |
| `ContextCompactionThresholdRatio` | `float64` | `context_compaction_threshold_ratio` |
| `ContextCompactionToolResultMaxTokens` | `int` | `context_compaction_tool_result_max_tokens` |
| `ContextCompactionKeepRecentRequests` | `int` | `context_compaction_keep_recent_requests` |
| `PlannerType` | `string` | `planner_type` |
| `PlannerConfig` | `map[string]any` | `planner_config` |
| `ModelContextWindow` | `int` | `context_window`（在 modelConfig 下） |

每个字段需要三步：① `runOptions` 字段声明 → ② `agentRunConfig`/`modelConfig` yaml tag → ③ `fileConfig.apply()` flag override 逻辑。

#### 5b. `openclaw/app/app.go` (+192/-81 = +111 行净增)

**结构级变更（非简单字段添加）：**

| 变更 | 类型 | 说明 |
|------|------|------|
| 移除 `MainWithOptions`/`RunWithOptions` | 删除 | 简化公开 API，仅保留 `Main` |
| 移除 `runner.WithAwaitUserReplyRouting(true)` | 删除 | 移除硬编码的 await-user-reply 路由 |
| 移动 `SubagentService` 接口 | 重构 | 从 `Runtime` struct 后移到 struct 前 |
| `agentConfig` 新增字段 | 新增 | 见下表 |
| `buildPlannerFromConfig` 函数 | 新增 | Planner 插件化创建 |
| `newAgent()` 增加 Planner 注入 | 修改 | 条件判断 + 调用 buildPlannerFromConfig |
| `newAgent()` 增加 llmagent option 传递 | 修改 | `WithSyncSummaryIntraRun`、`WithContextCompactionThresholdRatio` 等 |
| `newOpenAIModel()` 增加 `ContextWindow` | 修改 | 传递 spec 中的 context window |
| `modelFromOptions()` 传递 `ContextWindow` | 修改 | 从 opts 到 spec |
| 导入新增 `processor`、`planner` 包 | 修改 | — |

`agentConfig` 新增字段清单：

```
SessionSummaryInjectionMode         string
SyncSummaryIntraRun                 bool
ContextCompactionThresholdRatio     float64
ContextCompactionToolResultMaxTokens int
ContextCompactionKeepRecentRequests int
PlannerType                         string
PlannerConfig                       map[string]any
OpenClawToolingGuide               *string   (已存在但新增赋值路径)
```

#### 5c. `model/model.go` + `model/token_tailor.go` (+19 行)

**`model.Info` struct 新增 8 字段：**

```go
TailoringStrategyName   string   // e.g. "middle_out"
EnableTokenTailoring    bool     // 自动 token 裁剪开关
ProtocolOverheadTokens  int      // 协议开销预留 token
ReserveOutputTokens     int      // 输出预留 token
InputTokensFloor        int      // 最小输入 token
SafetyMarginRatio       float64  // 安全余量比例
MaxInputTokensRatio     float64  // 最大输入 token 比例
```

**`token_tailor.go`**：仅 +5 行，在 `buildPrefixSum` 中增加 token counter 为 nil 时的 warning log。

#### 5d. 原分析问题修正

**原分析声称的 "TokenTailoringConfig *yaml.Node 未赋值导致 nil panic" 经 diff 验证并不存在**。PAITUO 的实际实现是直接在 `model.Info` struct 中添加原子字段（见 5c），而非延迟解析的 `*yaml.Node` 模式。这是一个更好的设计——字段在配置加载时就完成了解析，不会产生 nil 引用。

**原分析声称的 7 个 YAML 字段也是不准确的**——实际新增远多于 7 个（见 5a 表），且包含 3 个 CLI flag 字段、Planner 注册字段、Model 扩展字段等多个维度。

**正确的问题描述**应是：上游 `KnownFields(true)` 模式下，YAML 文件中的任何字段如果 Go struct 中没有对应的 `yaml` tag，解析就会失败。PAITUO 的 YAML 配置文件包含了上游 Go 结构体中没有的字段，因此需要在对应结构体中新增这些字段及其应用逻辑。

---

### 六、Planner 注册与配置

| 维度 | 详情 |
|------|------|
| **精确文件** | `openclaw/app/planners.go` (+113 行新文件)，`openclaw/app/app.go` 中 `buildPlannerFromConfig` 函数（见 5b） |
| **内容** | Planner 注册机制——允许在 `openclaw.yaml` 中配置不同的 planner 类型并注册到运行时 |
| **方案评价** | 注册模式设计合理（参考了 Go 标准的 `database/sql` 注册模式）。`buildPlannerFromConfig` 在 `newAgent()` 中被条件调用。 |
| **注意** | `PlannerType` 和 `PlannerConfig` 字段已在 5a/5b 中覆盖，此处仅指 `planners.go` 的注册机制代码。 |

---

### 七、文件工具增强 (`tool/file/`)

| 维度 | 详情 |
|------|------|
| **精确文件** | `readfile.go` (+184), `readfile_test.go` (+18), `readmultiplefiles.go` (+47), `readmultiplefiles_test.go` (+4), `searchcontent.go` (+107), `searchcontent_test.go` (+5), `listfile.go` (+5) |
| **合计** | +370 行，零删除 |
| **内容** | `readfile.go` 支持大文件截断与部分读取；`searchcontent.go` 重构搜索逻辑；`listfile.go` 补全错误提示 |
| **建议** | 重实现时把部分读取逻辑抽成独立函数（如 `readPartialLines`），减少与上游代码的耦合面。 |

---

### 八、Subagent Worktree 隔离 — 已对比上游 #1876（结论：上游已实现）

**对比日期：** 2026-05-29  
**上游 commit：** `9308b28d3 {agent/taskrun, openclaw}: support worktree-isolated subagents (#1876)`

#### 上游 #1876 架构

```
internal/gitworktree/manager.go          (+753 行)  Manager 接口 + 实现
internal/gitworktree/manager_test.go     (+1173 行) 完整生命周期测试
openclaw/internal/subagentrun/worktree.go (+228 行) subagent 集成层
openclaw/subagent/subagent.go            (+20 行)   Status/Workspace 类型
openclaw/internal/subagentrun/types.go   (+39 行)   常量 + Isolation 字段
openclaw/internal/subagentrun/service.go (+316/-191) 注入 worktree 创建/清理
```

#### PAITUO subagent 变更精确统计

| 文件 | +行 | -行 | 净增 | 结论 |
|------|-----|-----|------|------|
| `subagentrun/service.go` | +750 | -? | 混合重构 | **上游覆盖** |
| `subagentrun/service_test.go` | +1159 | -? | 测试扩展 | **上游覆盖** |
| `subagentrun/types.go` | +101 | -? | 含 ParentInvocationID | **部分保留** |
| `subagentrun/trace_link_test.go` | +69 | 0 | 新测试 | **保留** |
| `subagentrun/tool.go` | +19 | -? | 小修改 | 需审查 |
| `subagentrun/tool_test.go` | +21 | -? | 测试扩展 | 需审查 |
| `taskrun/inprocess/service.go` | -625 | +? | 大量删除 | **上游覆盖** |
| `taskrun/inprocess/service_test.go` | -745 | +? | 大量删除 | **上游覆盖** |
| `taskrun/inprocess/types.go` | +32/-? | — | 类型变更 | **上游覆盖** |
| `taskrun/types.go` | +8/-2 | — | 小修改 | **上游覆盖** |

#### 结论：上游 #1876 全面优于 PAITUO

PAITUO 仅需保留 2 项增量：

| 序号 | 内容 | 位置 | 行数 |
|------|------|------|------|
| 1 | `ParentInvocationID string` 字段 | `types.go` SpawnRequest | ~3 行 |
| 2 | `trace_link_test.go` | 新增测试文件 | ~69 行 |

**原预估 1186 行 → 约 70 行。subagentrun 和 taskrun 的 2000+ 行改动全部由上游 #1876 接管。**

---

### 九、Guardrail 选项化 & Embedder 遥测

| 维度 | 详情 |
|------|------|
| **精确文件** | `plugin/guardrail/approval/{approval.go (+5), option.go (+16)}`、`promptinjection/{promptinjection.go (+5), option.go (+20)}`、`unsafeintent/{unsafeintent.go (+5), option.go (+20)}` |
| **embedder 文件** | `gemini/gemini.go (+8)`, `huggingface/huggingface.go (+7)`, `ollama/ollama.go (+8)`, `openai/openai.go (+7/-1)` |
| **合计** | guardrail +71 行，embedder +30 行 |
| **方案评价** | 小改动、低风险、高质量。guardrail 的选项化是标准的 Go 模式。 |
| **建议** | 保留。可以直接提 PR。 |

---

### 十、Trace Linking & Parent Invocation ID

| 维度 | 详情 |
|------|------|
| **精确文件** | `agent/execution_trace.go` (+2), `agent/invocation.go` (+24), `agent/invocation_options.go` (+9), `agent/llmagent/llm_agent.go` (+1), `agent/llmagent/option.go` (+15), `runner/runner.go`（有变更） |
| **合计** | ~50 行净增 |
| **内容** | 将 `ParentInvocationID` 通过 spawn/execute 链传递，启用 trace linking |
| **方案评价** | 字段传递路径正确（SpawnRequest → execute → goroutine span context）。 |
| **建议** | 保留。关注上游 `agent/invocation.go` 是否有相关重构。 |

---

### 十一、LLM Flow 增强 (`internal/flow/`)

| 维度 | 详情 |
|------|------|
| **精确文件** | `llmflow/llmflow.go` (+204), `llmflow/llmflow_test.go` (+19), `processor/content.go` (+56/-? 净增减), `processor/content_test.go` (+28/-?), `processor/context_compact.go` (+55/-?), `processor/context_compact_test.go` (+9), `processor/functioncall.go` (+9) |
| **合计** | +380 行（含测试），净增 ~230 行 |
| **内容** | 上下文压缩过程中增加工具结果阈值控制、失败 tool call 遥测、增量消息获取重构 |
| **上游状态** | 上游 `#1880 session: respect tool result threshold for summaries` 已处理部分问题，但不包含按工具名的选择性控制。`#1843 tool result compaction controls` 也有关联。 |

#### 与上游 #1843/#1880 重叠分析

| 功能点 | 上游 #1843 | 上游 #1880 | PAITUO | 重叠？ |
|--------|-----------|-----------|--------|--------|
| tool result compaction 控制 | ✅ | — | ✅ | **部分重叠** |
| tool result threshold for summaries | — | ✅ | ✅ | **部分重叠** |
| keep/force-clean 工具名单 | — | — | ✅ | PAITUO 独有 |
| 失败 tool call 遥测 | — | — | ✅ | PAITUO 独有 |
| 增量消息获取 | — | — | ✅ | PAITUO 独有 |

**结论**：上游已覆盖 threshold 和 compaction 基础控制，PAITUO 的 keep/force-clean 名单和 tool call 失败遥测是真正的增量。重实现时必须先读上游 #1843/#1880 代码，确认不重叠部分，然后将独有功能抽象为独立钩子。

**建议**：最高风险区域。重实现时：
1. 先审查上游 #1843 + #1880 的完整代码
2. 将 keep/force-clean 名单抽象为 `context_compaction_rules.go`（新文件）
3. 在 `content.go` 和 `context_compact.go` 中通过配置注入钩子
4. `llmflow.go` 的 204 行改动仅保留增量收集逻辑

---

## 上游动向追踪

| 上游改动 | 日期 | 影响 PAITUO 的类别 | 处理 |
|----------|------|-------------------|------|
| `#1876` worktree-isolated subagents | 2026-05 | Subagent Worktree（八） | **上游已覆盖，PAITUO 仅保留 2 项增量** |
| `#1880` tool result threshold for summaries | 2026-05 | LLM Flow（十一） | 部分重叠，PAITUO 的 keep/force-clean 名单是增量 |
| `#1843` tool result compaction controls | 早于 merge | LLM Flow（十一） | 上游已有 compaction 控制 |
| `#1731` knowledge: GraphRAG code search | 2026-05 | 无冲突 | — |
| `#1887` tool/mcp: propagate annotations | 2026-05 | 无冲突 | — |
| `#1889` benchmark submodule | 2026-05 | 无冲突 | — |

**关键信号：** `#1843`、`#1876`、`#1880` 说明上游正积极开发上下文压缩和 subagent 隔离功能。PAITUO 的 llmflow 改动需要尽快抽象化，否则越往后冲突越大。

---

## 整体评估矩阵

| 类别 | 必要性 | 上游冲突风险 | 可提 PR | 净增行数 | 方案状态 |
|------|--------|-------------|---------|---------|---------|
| Windows Shell 抽象 | 必须 | 低 | 是 | +309 | 就绪 |
| Windows 进程树 | 必须 | 低 | 是 | +208 | 就绪 |
| Windows 编码转换 | 必须 | 低 | 是 | ~+80 | 需加 UTF-8 探测 |
| 上下文遥测 | 需要 | 中 | 条件 | +1106 | 需裁剪 |
| 配置/YAML/模型字段 | 必须 | 中 | 否 | +219 | 就绪 |
| Planner 注册 | 需要 | 低 | 条件 | +113 | 就绪 |
| 文件工具增强 | 需要 | 中 | 条件 | +370 | 需解耦 |
| **Subagent Worktree** | **已解决** | **—** | **—** | **~70** | **已确定** |
| Guardrail 选项 | 需要 | 低 | 是 | +71 | 就绪 |
| Embedder Trace | 可选 | 低 | 是 | +30 | 就绪 |
| Trace Linking | 需要 | 中 | 条件 | ~+50 | 就绪 |
| LLM Flow 增强 | 部分需要 | **高** | 条件 | +230 净增 | 需重构 |

---

## 分支创建策略

```bash
git fetch upstream
git checkout -b feat/paituo-base upstream/main
```

分支命名规范：`feat/paituo-{module}`，每个功能模块独立分支，最终集成到 `feat/paituo-integration`。

### 分支依赖链

```
upstream/main (4f2670297)
    │
    └── feat/paituo-base
          │
          ├── feat/paituo-windows-platform        ──┐
          ├── feat/paituo-windows-procgroup         ├── 第 1 批：零冲突增量
          ├── feat/paituo-windows-codepage          │   (7 个分支，无文件交集)
          ├── feat/paituo-telemetry-context         │
          ├── feat/paituo-guardrail-options         │
          ├── feat/paituo-embedder-telemetry       ──┘
          ├── feat/paituo-planner-registry         ──┐
          │                                          │
          ├── feat/paituo-config-yaml               ├── 第 2 批：中风险改动
          ├── feat/paituo-model-token               │   (5 个分支，文件交集少)
          ├── feat/paituo-agent-trace               │
          ├── feat/paituo-file-tools               ──┘
          │
          ├── feat/paituo-trace-link-test           ── 第 3 批：worktree 增量（~70 行）
          │
          └── feat/paituo-llmflow-enhance           ── 第 4 批：需先审查上游代码

feat/paituo-integration ← 所有分支 rebased 到此
```

---

## 提交信息模板

匹配上游格式 `{pkg}: description (#PR)`：

```
platform: add cross-platform shell abstraction

Define Shell() and BuildCommand() interfaces in the platform
package, with Unix implementation returning bash/sh and Windows
implementation detecting PowerShell/cmd while excluding non-native
shells like Git Bash and MSYS2.

Reimplements-PAITUO: feat(platform): cross-platform shell abstraction
Upstream-Compatible: yes
```

```
openclaw/app: add YAML config fields for compaction, summary, and planner

Add missing struct fields and flag overrides for:
- session_summary_injection_mode, sync_summary_intra_run
- context_compaction_threshold_ratio, context_compaction_tool_result_max_tokens,
  context_compaction_keep_recent_requests
- planner_type, planner_config
- model.context_window

These fields are required because loadConfigFile uses KnownFields(true)
which rejects any YAML key without a corresponding Go struct field.

Reimplements-PAITUO: feat: add missing YAML config struct fields
Upstream-Compatible: yes
```

```
subagentrun: add ParentInvocationID for trace linking

Extend SpawnRequest to carry the parent invocation ID through the
spawn/execute chain, enabling complete trace topology across parent
and child agents.

This is the only worktree-related PAITUO field not covered by
upstream #1876 (support worktree-isolated subagents).

Reimplements-PAITUO: feat: add ParentInvocationID to SpawnRequest
Upstream-Compatible: yes
```

---

## 代码风格规范

| 规则 | 示例 |
|------|------|
| 新增文件用 `_windows.go` / `_unix.go` 后缀 | `platform_windows.go` |
| 非目标平台用 `_other.go` 桩 | `procgroup_other.go` |
| Options 模式用 `WithXxx` 命名 | `WithGuardrailApproval(...)` |
| 错误处理用 `fmt.Errorf` + `%w` | `return fmt.Errorf("xxx: %w", err)` |
| 导出类型有文档注释 | `// Shell returns the platform's preferred shell command.` |
| 测试文件与源文件同目录 | `platform_test.go` 放在 `internal/platform/` |
| 不修改上游已有函数签名 | 通过新增函数/配置项注入行为 |
| Windows 专属代码用 `//go:build windows` 标签 | 禁止 `runtime.GOOS` 运行时判断 |
| 新增结构体字段放已有字段后面 | 不重新排列已有字段顺序 |

---

## 合并 & 同步流程

### 集成到 `feat/paituo-integration`

```bash
# 推荐方式：rebase 保持线性
git checkout feat/paituo-windows-procgroup
git rebase feat/paituo-windows-platform
git checkout feat/paituo-windows-codepage
git rebase feat/paituo-windows-procgroup
# ... 依此类推

# 最终集成分支
git checkout feat/paituo-integration
git rebase feat/paituo-llmflow-enhance
```

### 与上游同步

```bash
git fetch upstream
# 逐个 rebase 第 1 批分支（零冲突，可批量处理）
for branch in feat/paituo-windows-platform feat/paituo-windows-procgroup ...; do
    git checkout $branch
    git rebase upstream/main
done
# 第 2-4 批分支手动 rebase（可能有冲突）
```

### PR 提交流程

```
feat/paituo-windows-platform ──→ PR to upstream    (最高优先级)
feat/paituo-windows-procgroup  ──→ 可合并提交
feat/paituo-windows-codepage   ──→ 可合并提交
feat/paituo-guardrail-options  ──→ PR to upstream
feat/paituo-embedder-telemetry ──→ PR to upstream
...其他分支暂保留在 PAITUO 内...
```

---

## 详细实施步骤

---

### Batch 0：关键基础设施（1 个合并分支，必须先完成）

> **P0 优先级**——YAML 配置解析失败会导致程序无法启动，必须先修复。

#### 0-1. `feat/paituo-config-and-model`

**目标**：合并原 5a（run_options.go）、5b（app.go）、5c（model/）、6（planners.go）为一个分支，因为它们在文件层面强耦合（app.go 同时引用 run_options.go 的字段和 planners.go 的注册）。

| 步骤 | 操作 | 涉及文件 | 预计行数 |
|------|------|---------|---------|
| 1 | 创建分支 | `git checkout -b feat/paituo-config-and-model feat/paituo-base` | — |
| 2 | **Commit 1**: 扩展 `model.Info` struct | `model/model.go` | +14 |
| 3 | **Commit 2**: token_tailor nil counter warning | `model/token_tailor.go` | +5 |
| 4 | **Commit 3**: 扩展 `runOptions` + `agentRunConfig` + `modelConfig` YAML 字段 | `openclaw/app/run_options.go` | +97/-8 |
| 5 | **Commit 4**: `fileConfig.apply()` 中新增字段的 flag override 逻辑 | `openclaw/app/run_options.go` | (包含在上步) |
| 6 | **Commit 5**: 扩展 `agentConfig` struct 字段 | `openclaw/app/app.go` | ~+15 |
| 7 | **Commit 6**: `buildAgentConfig` / `newAgent` 中传递新字段 | `openclaw/app/app.go` | ~+40 |
| 8 | **Commit 7**: `newOpenAIModel` ContextWindow + `modelFromOptions` 传递 | `openclaw/app/app.go` | ~+10 |
| 9 | **Commit 8**: 新增 `planners.go` 注册文件 | `openclaw/app/planners.go` | +113 |
| 10 | **Commit 9**: `buildPlannerFromConfig` + `newAgent` Planner 注入 | `openclaw/app/app.go` | ~+25 |
| 11 | 编译验证 | `go build ./...` | — |
| 12 | openclaw 模块测试 | `cd openclaw && go test ./app/...` | — |

**验证命令**：

```bash
go build ./...
go vet ./openclaw/app/...
go test ./openclaw/app/...
```

**注意**：此分支不包含 `openclaw/app/app.go` 中移除 `MainWithOptions`/`RunWithOptions` 和 `runner.WithAwaitUserReplyRouting` 的变更——那些是运行时行为变更，应作为独立 commit 评估是否需要。当前仅实施字段扩展和 Planner 注册。

---

### 第 1 批：零冲突增量（新增文件为主，7 个分支可并行）

#### 1-1. `feat/paituo-windows-platform`（+309 行，5 个新文件）

| 步骤 | 操作 | 涉及文件 | 行数 |
|------|------|---------|------|
| 1 | 创建分支 | `git checkout -b feat/paituo-windows-platform feat/paituo-base` | — |
| 2 | Commit 1: 接口定义 + 通用测试 | `internal/platform/platform.go`, `platform_test.go` | +75 |
| 3 | Commit 2: Unix 实现 | `internal/platform/platform_unix.go` | +46 |
| 4 | Commit 3: Windows 实现 + 测试 | `internal/platform/platform_windows.go`, `platform_windows_test.go` | +188 |
| 5 | 验证 | `go test ./internal/platform/... -count=1` | — |

#### 1-2. `feat/paituo-windows-procgroup`（+208 行）

| 步骤 | 操作 | 涉及文件 |
|------|------|---------|
| 1 | 基于 `feat/paituo-windows-platform` 创建 | `git checkout -b feat/paituo-windows-procgroup feat/paituo-windows-platform` |
| 2 | Commit 1: codeexecutor procgroup (Windows 实现 + other 桩) | `codeexecutor/local/procgroup_windows.go` (+90), `procgroup_other.go` (+24) |
| 3 | Commit 2: codeexecutor local.go/workspace 集成 | `codeexecutor/local/local.go` (+10), `local_test.go` (+35), `workspace_runtime_interactive.go` (+32), `workspace.go` (+8), `workspace_test.go` (+9) |
| 4 | Commit 3: octool procgroup | `openclaw/internal/octool/procgroup_windows.go`（从 PAITUO 提取） |
| 5 | 验证 | `go test ./codeexecutor/local/... -count=1` |

#### 1-3. `feat/paituo-windows-codepage`（~+80 行，含 UTF-8 探测增强）

| 步骤 | 操作 |
|------|------|
| 1 | 基于 `feat/paituo-windows-procgroup` 创建 |
| 2 | 提取 `openclaw/internal/octool/codepage_windows.go` |
| 3 | **增强**：在解码前加 `utf8.Valid()` 快速路径，避免误转已为 UTF-8 的输出 |
| 4 | 单个 commit |
| 5 | Windows 环境验证 |

**UTF-8 快速路径伪代码**：
```go
func toUTF8(input []byte) (string, error) {
    if utf8.Valid(input) {
        return string(input), nil  // 已是 UTF-8，直接返回
    }
    // 否则做 MultiByteToWideChar 转换
    ...
}
```

#### 1-4. `feat/paituo-telemetry-context`（裁剪后 ~+400 行）

| 步骤 | 操作 |
|------|------|
| 1 | 基于 `feat/paituo-windows-codepage` 创建 |
| 2 | Commit 1: 新增 `metric_context.go`（**裁剪版**：仅保留 compaction 触发 + token 使用率两个核心指标，~200 行） |
| 3 | Commit 2: 新增 `metric_context_test.go`（裁剪版 ~120 行） |
| 4 | Commit 3: 修改 `trace.go` 的 span 属性记录（+127 行） |
| 5 | Commit 4: 修改 `metric/metric.go` + `semconv/metrics.go` + `langfuse/exporter.go` |
| 6 | 验证 | `go test ./internal/telemetry/... -count=1` |

**裁剪原则**：首次只提交 compaction 触发次数和 token 使用率两个指标。其余指标（如各工具名列表）标记 `// TODO: add metric X after validation in production`。

#### 1-5. `feat/paituo-guardrail-options`（+71 行，6 个文件）

| 步骤 | 操作 |
|------|------|
| 1 | 基于 `feat/paituo-telemetry-context` 创建 |
| 2 | Commit 1: approval options | `plugin/guardrail/approval/option.go` (+16), `approval.go` (+5) |
| 3 | Commit 2: promptinjection options | `plugin/guardrail/promptinjection/option.go` (+20), `promptinjection.go` (+5) |
| 4 | Commit 3: unsafeintent options | `plugin/guardrail/unsafeintent/option.go` (+20), `unsafeintent.go` (+5) |
| 5 | 验证 | `go test ./plugin/guardrail/... -count=1` |

#### 1-6. `feat/paituo-embedder-telemetry`（+30 行，4 个文件）

| 步骤 | 操作 |
|------|------|
| 1 | 基于 `feat/paituo-guardrail-options` 创建 |
| 2 | Commit 1: gemini | `knowledge/embedder/gemini/gemini.go` (+8) |
| 3 | Commit 2: huggingface | `knowledge/embedder/huggingface/huggingface.go` (+7) |
| 4 | Commit 3: ollama | `knowledge/embedder/ollama/ollama.go` (+8) |
| 5 | Commit 4: openai | `knowledge/embedder/openai/openai.go` (+7/-1) |
| 6 | 验证 | `go test ./knowledge/embedder/... -count=1` |

#### 1-7. `feat/paituo-planner-registry`（+113 行）

**注意**：`planners.go` 已在 Batch 0 中实施。此分支在 Batch 0 完成后再做则为空或做额外增强。如果 Batch 0 先行，此分支可跳过。

---

### 第 2 批：中风险改动（5 个分支，修改已有逻辑）

> **前置条件**：Batch 0 + 第 1 批全部完成并 rebase 到同一基线。

#### 2-1. `feat/paituo-agent-trace`（~+50 行）

| 步骤 | 操作 | 涉及文件 |
|------|------|---------|
| 1 | 基于第 1 批末端的 `feat/paituo-planner-registry` 创建 | — |
| 2 | Commit 1: invocation 字段 + options | `agent/invocation.go` (+24), `agent/invocation_options.go` (+9) |
| 3 | Commit 2: execution_trace + llm_agent + option | `agent/execution_trace.go` (+2), `agent/llmagent/llm_agent.go` (+1), `agent/llmagent/option.go` (+15) |
| 4 | Commit 3: runner 集成 | `runner/runner.go`（提取相关 diff） |
| 5 | 验证 | `go test ./agent/... -count=1` |

#### 2-2. `feat/paituo-file-tools`（+370 行，需解耦）

| 步骤 | 操作 | 涉及文件 |
|------|------|---------|
| 1 | 基于 `feat/paituo-agent-trace` 创建 | — |
| 2 | **前置重构**：将 `readfile.go` 的部分读取逻辑抽成 `readPartialLines()` 独立函数 | `tool/file/readfile.go` |
| 3 | Commit 1: listfile 错误补全 | `tool/file/listfile.go` (+5) |
| 4 | Commit 2: readfile 部分读取（含解耦后的独立函数） | `tool/file/readfile.go` (+184), `readfile_test.go` (+18) |
| 5 | Commit 3: readmultiplefiles 支持 | `tool/file/readmultiplefiles.go` (+47), `readmultiplefiles_test.go` (+4) |
| 6 | Commit 4: searchcontent 重构 | `tool/file/searchcontent.go` (+107), `searchcontent_test.go` (+5) |
| 7 | 验证 | `go test ./tool/file/... -count=1` |

#### 2-3. `feat/paituo-trace-link-test`（~70 行，worktree 增量）

| 步骤 | 操作 |
|------|------|
| 1 | 基于 `feat/paituo-agent-trace`（已有 ParentInvocationID 主链传递）创建 |
| 2 | Commit 1: SpawnRequest 增加 `ParentInvocationID string` | `openclaw/internal/subagentrun/types.go` (~3 行) |
| 3 | Commit 2: service.go 中传递该字段（聚焦改动，不重构） | `openclaw/internal/subagentrun/service.go` (~2 行) |
| 4 | Commit 3: 新增 `trace_link_test.go` | `openclaw/internal/subagentrun/trace_link_test.go` (+69) |
| 5 | 验证 | `go test ./openclaw/internal/subagentrun/... -count=1` |
| 6 | **关键**：确认与上游 #1876 的 `internal/gitworktree/` 和 `worktree.go` 无冲突 |

---

### 第 3 批：需上游审查后实施

#### 3-1. `feat/paituo-llmflow-enhance`（+230 净增，需重构）

| 步骤 | 操作 |
|------|------|
| 1 | **先审查**上游 #1843 和 #1880 的完整代码，用 `git diff` 对比确认不重叠部分 |
| 2 | 将 keep/force-clean 名单抽象为 `context_compaction_rules.go`（新文件，~60 行） |
| 3 | Commit 1: `context_compaction_rules.go` 新增 + `content.go` 钩子注入 | `internal/flow/processor/context_compaction_rules.go`, `content.go` |
| 4 | Commit 2: `context_compact.go` 钩子注入 | `internal/flow/processor/context_compact.go` |
| 5 | Commit 3: `functioncall.go` 失败 tool call 遥测 | `internal/flow/processor/functioncall.go` (+9) |
| 6 | Commit 4: `llmflow.go` 增量收集逻辑（重构后仅保留 ~50 行核心逻辑） | `internal/flow/llmflow/llmflow.go` |
| 7 | Commit 5: 各测试文件更新 | `llmflow_test.go`, `content_test.go`, `context_compact_test.go` |
| 8 | 验证 | `go test ./internal/flow/... -count=1` |

**重构目标**：将 llmflow.go 的 204 行改动缩减到 ~50 行，其余逻辑通过新文件和钩子模式实现。

---

### 最终集成

```bash
git checkout feat/paituo-integration
git rebase feat/paituo-llmflow-enhance

# 全量验证
go build ./...
go test $(go list ./... | grep -v openclaw$)  # 跳过 openclaw（E2E 依赖）
cd openclaw && go test ./...                     # openclaw 模块测试
```

---

## 验证检查清单

每个功能分支完成后：

```bash
go build ./...                                        # 编译全量
go test ./path/to/module/... -count=1                 # 模块测试（禁用缓存）
go vet ./path/to/module/...                           # 静态分析
gofmt -r 'interface{} -> any' -l .                    # 格式检查
git diff --stat feat/paituo-base..HEAD                # 只应包含本模块文件
git log --oneline feat/paituo-base..HEAD              # 查看本分支 commit 历史
```

全量验证（集成后）：

```bash
# 根模块
go build ./...
go test ./...

# openclaw 子模块
cd openclaw
go build ./...
go test ./...
```

---

## 变更量预估汇总

| 批次 | 分支 | 文件数 | 净增行数 | 预计时间 | 可并行 |
|------|------|--------|---------|---------|--------|
| **Batch 0** | `feat/paituo-config-and-model` | 4 | ~+332 | 60 min | 否 |
| 第 1 批 | `feat/paituo-windows-platform` | 5 | +309 | 15 min | ✅ |
| 第 1 批 | `feat/paituo-windows-procgroup` | 6 | +208 | 20 min | ✅ |
| 第 1 批 | `feat/paituo-windows-codepage` | 1 | +80 | 15 min | ✅ |
| 第 1 批 | `feat/paituo-telemetry-context` | 6 | +400 | 30 min | ✅ |
| 第 1 批 | `feat/paituo-guardrail-options` | 6 | +71 | 10 min | ✅ |
| 第 1 批 | `feat/paituo-embedder-telemetry` | 4 | +30 | 10 min | ✅ |
| 第 1 批 | `feat/paituo-planner-registry` | 1 | +113 | 10 min | ✅ |
| 第 2 批 | `feat/paituo-agent-trace` | 6 | +50 | 30 min | 否 |
| 第 2 批 | `feat/paituo-file-tools` | 7 | +370 | 40 min | 否 |
| 第 2 批 | `feat/paituo-trace-link-test` | 3 | +70 | 15 min | 否 |
| 第 3 批 | `feat/paituo-llmflow-enhance` | 7 | +230 | 3h+ | 否 |
| **合计** | **13 个分支** | **~56 文件** | **~+2263** | **约 7h** | — |

**相比原 PAITUO 的 5109 行净增，方案缩减为约 2263 行（减少 56%），主要得益于上游 #1876 接管了 1926 行 worktree 代码。**

---

## 优先级排序

| 优先级 | 分支 | 确定性 | 说明 |
|--------|------|--------|------|
| **P0** | `feat/paituo-config-and-model` | 确定 | 程序启动必备，YAML 解析修复 |
| **P1-A** | `feat/paituo-windows-platform` | 确定 | 7 分支无文件交集，可并行 |
| **P1-A** | `feat/paituo-windows-procgroup` | 确定 | — |
| **P1-A** | `feat/paituo-windows-codepage` | 确定 | — |
| **P1-A** | `feat/paituo-guardrail-options` | 确定 | — |
| **P1-A** | `feat/paituo-embedder-telemetry` | 确定 | — |
| **P1-A** | `feat/paituo-planner-registry` | 确定 | — |
| **P1-B** | `feat/paituo-telemetry-context` | 确定 | 裁剪后独立实施 |
| **P2** | `feat/paituo-agent-trace` | 确定 | 依赖 P1 完成 |
| **P2** | `feat/paituo-file-tools` | 确定 | 解耦后实施 |
| **P2** | `feat/paituo-trace-link-test` | 确定 | 依赖 agent-trace |
| **P3** | `feat/paituo-llmflow-enhance` | 需先审查上游 | 非阻塞 |