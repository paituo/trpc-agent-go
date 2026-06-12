# OpenClaw 三大 Agent 协作机制深度对比分析

## 一、总览对比表

| 维度 | `subagents_spawn` | `agent/taskrun` | `agenttool.NewTool` / `transfer_to_agent` |
|------|-------------------|-----------------|-------------------------------------------|
| **所属层** | OpenClaw 产品层（openclaw/） | 框架核心层（agent/taskrun/） | 工具层（tool/agent/ + tool/transfer/） |
| **定位** | OpenClaw 后台子Agent运行平台 | 通用持久化后台任务运行控制器 | Agent 即工具 / 控制权移交 |
| **执行模型** | 异步后台 goroutine + Runner | 异步后台 goroutine + Runner | 同步内联调用（共享父 Invocation） |
| **生命周期** | 完整状态机：queued→running→finalizing→completed/failed/canceled | 完整状态机：queued→running→finalizing→completed/failed/canceled | 无独立生命周期，跟随父调用 |
| **持久化** | FileStore（runs.json） | 可插拔 Store（Memory/File） | 无持久化 |
| **会话隔离** | 独立子 Session（新 ChildSessionID） | 独立子 Session（新 ChildSessionID） | 共享父 Session（子 Invocation 克隆） |
| **历史继承** | 不继承（独立会话） | 不继承（独立会话） | 可选继承（HistoryScopeIsolated / ParentBranch） |
| **嵌套** | 禁止嵌套 | 可配置允许（WithNestedSpawns） | 禁止递归（排除自身和 transfer_to_agent） |
| **通知** | 完成后通过 Router 推送通知 | Observer 回调 | 无通知机制 |
| **工作区隔离** | 支持 Git Worktree 隔离 | 不支持 | 不支持 |
| **Review 模式** | 支持（mode=review） | 不支持 | 不支持 |
| **流式支持** | 无 | 无 | 支持（StreamableCall） |

---

## 二、各机制实现原理与调用链路

### 1. `subagents_spawn` — OpenClaw 后台子Agent

#### 实现原理

这是 OpenClaw 产品层的"重量级"子Agent机制。核心思想是：**父Agent通过 LLM 工具调用发起一个后台子Agent运行，子Agent拥有独立的 Session、可选的 Git Worktree 隔离工作区，完成后通过消息通道通知父会话**。

关键特性：
- **三种模式**：`async`（异步立即返回）、`sync`（同步等待完成）、`review`（完成后暂停等用户确认）
- **Worktree 隔离**：子Agent可在独立 Git Worktree 中工作，不影响主仓库
- **完成通知**：通过 `outbound.Router` 向用户推送完成消息
- **防嵌套**：通过 `RuntimeStateKeyRun` 标记检测，禁止子Agent再 spawn 子Agent
- **Finalizer 机制**：子Agent结束后执行 Worktree 清理等收尾工作

#### 调用流程链路

```
LLM 调用 subagents_spawn 工具
  │
  ▼
spawnTool.Call()                              [tool.go:262]
  ├── 检查是否嵌套子Agent (isNestedSubagent)
  ├── 解析参数 spawnInput{task, mode, isolation, ...}
  ├── 解析 delivery target (outbound.ResolveTarget)
  │
  ▼
Service.Spawn()                               [service.go:121]
  ├── validateSpawnRequest()
  ├── normalizeIsolation() → 判断是否需要 worktree
  ├── newSubagentID() → "subagent:<uuid>"
  │
  ├── [如果 isolation=worktree]
  │   └── s.createWorktree() → gitworktree.Lease
  │
  ├── 构建 runtimeState (delivery + worktree 元数据)
  ├── 构建 runOptions / runContext
  ├── 构建 injectedContextMessages (subagentRunPrompt)
  │
  ▼
s.core.Spawn()                                [taskrun inprocess service.go:182]
  ├── 创建 Run 对象 (Status=Queued)
  ├── 持久化到 Store
  ├── 启动 goroutine: go s.execute()
  │
  ▼
s.execute()                                   [service.go:373]
  ├── markRunning() → Status=Running, 创建子 Session
  ├── runChild() → 调用 runner.Run() 执行子Agent
  │   └── runner.Run(ctx, userID, childSessionID, userMessage, runOpts...)
  │       └── 子Agent在独立Session中执行
  │
  ├── markExiting()
  └── finishRun()
      ├── finishedRunView() → 判断 completed/failed/canceled
      ├── finalizeRun() → Finalizer.FinalizeRun() [清理 worktree]
      ├── 持久化最终状态
      ├── notify() → Observer.OnRunUpdate()
      └── wake() → 唤醒等待者

[回到 spawnTool.Call()]
  ├── async: 直接返回 run
  ├── sync:  调用 s.core.Wait() 阻塞等待
  └── review: 等待 + markAwaitingReview() 暂停父Agent

[子Agent完成时]
  └── Service.OnRunUpdate()                   [service.go:275]
      └── notifyCompletion() → router.SendText() 推送通知
```

---

### 2. `agent/taskrun` — 通用后台任务运行控制器

#### 实现原理

这是框架核心层的**通用持久化后台任务运行基础设施**。它是一个纯粹的"任务编排层"，不关心具体的业务语义（如 worktree、通知等），只负责：

- **生命周期管理**：完整的状态机（queued → running → finalizing → completed/failed/canceled）
- **持久化**：通过可插拔的 Store 接口（MemoryStore / FileStore）
- **取消与等待**：支持 Cancel、Wait（基于 channel 的等待/唤醒机制）
- **Observer/Finalizer**：可插拔的生命周期钩子

`subagents_spawn` 的底层就是通过 `taskrun.Controller` 接口实现的——OpenClaw 的 `subagentrun.Service` 包装了 `taskruninprocess.Service`。

`tool/taskrun` 包则提供了面向 LLM 的工具封装（`start_task_run` 等），让 Agent 可以通过工具调用启动后台任务。

#### 调用流程链路

```
LLM 调用 start_task_run 工具
  │
  ▼
spawnTool.Call()                              [tool/taskrun/tool.go:303]
  ├── 检查是否嵌套 (isNestedTaskRun)
  ├── 解析参数 spawnInput{task, agent_name, mode, ...}
  ├── 解析 userID, sessionID
  │
  ▼
controller.Spawn()                            [agent/taskrun/inprocess/service.go:182]
  ├── validateSpawnRequest()
  ├── 创建 Run 对象 (Status=Queued, ID=uuid)
  ├── 存入 s.runs map
  ├── 持久化到 Store
  ├── notify() → Observer.OnRunUpdate()
  ├── 启动 goroutine: go s.execute()
  │
  ▼
s.execute()                                   [service.go:373]
  ├── markRunning()
  │   ├── 生成 childSessionID ("taskrun:<id>:<timestamp>")
  │   ├── 生成 requestID
  │   ├── 创建带超时的 context
  │   ├── Status → Running
  │   └── 持久化 + notify
  │
  ├── runChild()                              [service.go:458]
  │   ├── 构建 RunOptions (appName, requestID, runtimeState, injectedMessages, agentName)
  │   └── s.runner.Run(ctx, userID, childSessionID, userMessage, runOpts...)
  │       └── 通过 Runner 执行 Agent
  │
  ├── markExiting()
  └── finishRun()                             [service.go:559]
      ├── finishedRunView() → 判断最终状态
      ├── finalizeRun() → Finalizer.FinalizeRun()
      ├── 合并 finalMetadata
      ├── 持久化
      ├── notify → Observer.OnRunUpdate()
      └── wake() → 唤醒 Wait 的调用者

[回到 spawnTool.Call()]
  ├── async: 直接返回 run
  └── sync:  调用 controller.Wait() 阻塞等待
      └── 循环检查 status + channel 等待
```

---

### 3. `agenttool.NewTool` / `transfer_to_agent` — Agent 即工具 / 控制权移交

这两个是**完全不同的机制**，虽然都涉及 Agent 间协作，但语义和实现截然不同。

#### 3a. `agenttool.NewTool` — 将 Agent 包装为同步工具

##### 实现原理

核心思想：**将一个 Agent 包装成一个 tool.Tool，父Agent在工具调用时同步执行子Agent，子Agent的结果作为工具返回值**。

关键特性：
- **同步执行**：子Agent在父Agent的工具调用周期内完成
- **共享 Session**：子 Invocation 是父 Invocation 的 Clone，共享 Session 事件流
- **历史继承**：支持 `HistoryScopeIsolated`（默认，子Agent看不到父历史）和 `HistoryScopeParentBranch`（子Agent可看到父历史）
- **流式支持**：`StreamableCall` 可将子Agent的流式输出转发给父Agent
- **事件镜像**：子Agent的事件通过 `wrapWithCallSemantics` 镜像到父 Session

##### 调用流程链路

```
父Agent LLM 调用 agent_tool 工具
  │
  ▼
Tool.Call()                                   [agent_tool.go:314]
  ├── [dynamic模式] → callDynamic() → 见 NewDynamicTool
  │
  ├── 构建 userMessage
  │
  ├── [有父Invocation] → callWithParentInvocation()
  │   ├── flush.Invoke() → 刷新父Session事件
  │   ├── parentInvocationWithLiveSession() → 恢复活跃Session指针
  │   ├── buildChildFilterKey() → 构建子事件过滤键
  │   │   ├── Isolated: "agentName-<uuid>"
  │   │   └── ParentBranch: "parentKey/agentName-<uuid>"
  │   │
  │   ├── parentInv.Clone() → 克隆父Invocation创建子Invocation
  │   │   └── childInvocationOptions():
  │   │       ├── WithInvocationAgent(at.agent)
  │   │       ├── WithInvocationMessage(message)
  │   │       └── WithInvocationEventFilterKey(childKey)
  │   │
  │   ├── agent.RunWithPlugins(subCtx, subInv, at.agent)
  │   │   └── 子Agent在克隆的Invocation中执行
  │   │
  │   ├── wrapWithCallSemantics() → 事件处理管道
  │   │   ├── ensureUserMessageForCall() → 确保Session有user消息
  │   │   ├── 遍历子Agent事件:
  │   │   │   ├── shouldMirrorEventToSession() → 镜像到父Session
  │   │   │   ├── shouldDelayVisibleCompletionSessionMirror() → 延迟可见完成
  │   │   │   ├── evt.RequiresCompletion → inv.NotifyCompletion()
  │   │   │   └── 转发事件到输出channel
  │   │   └── flushPendingVisibleCompletionForSession()
  │   │
  │   └── collectResponse() → 收集子Agent的assistant文本作为工具返回值
  │
  └── [无父Invocation] → callWithIsolatedRunner()
      ├── runner.NewRunner() → 创建独立Runner
      ├── r.Run() → 在独立内存Session中执行
      └── collectResponse()
```

#### 3b. `NewDynamicTool` — 动态子Agent工具

这是 `NewTool` 的增强变体，允许**每次调用动态选择子Agent的能力边界**（工具、技能、指令）。

```
Tool.Call() [dynamic=true]
  │
  ▼
callDynamic()                                 [dynamic_tool.go:438]
  │
  ▼
buildDynamicSubInvocation()                   [dynamic_tool.go:490]
  ├── 获取 parentInv
  ├── parseDynamicArgs() → 解析 {request, instruction, tools, skills}
  ├── 确定 baseAgent (templateAgent 或 parentInv.Agent)
  ├── flush.Invoke() → 刷新父Session
  │
  ├── buildDynamicPatch() → 构建能力边界补丁
  │   ├── instruction patch → SetInstruction()
  │   ├── tools patch:
  │   │   ├── dynamicMaxToolSurface() → 解析最大工具集
  │   │   │   ├── [WithCapabilityProvider] → provider()
  │   │   │   ├── [WithCapabilityTools] → 静态工具集
  │   │   │   └── [默认] → toolsurface.EffectiveWithExternal() 从父Invocation派生
  │   │   ├── selectDynamicTools() → 按model选择过滤
  │   │   │   └── 排除自身 + transfer_to_agent + external tools
  │   │   └── patch.SetTools() + patch.SetSuppressSubAgentTransfer()
  │   │
  │   └── skills patch:
  │       ├── dynamicMaxSkillRepo() → 解析最大技能库
  │       └── selectDynamicSkills() → 按model选择过滤
  │
  ├── parentInv.Clone() → 克隆并应用 SurfacePatch
  │   └── dynamicChildInvocationOptions():
  │       ├── WithInvocationAgent(baseAgent)
  │       ├── WithSurfacePatchForNode() → 挂载能力边界
  │       └── sanitizeChildRunOptions() → 清理父级泄漏的RunOptions
  │
  └── 返回 subCtx, subInv, warnings

  │
  ▼
agent.RunWithPlugins(subCtx, subInv, subInv.Agent)
  └── 子Agent在受限能力边界内执行

  │
  ▼
collectResponse() + formatResponseWithWarnings()
```

#### 3c. `transfer_to_agent` — 控制权移交

##### 实现原理

核心思想：**不是执行子Agent，而是将当前会话的控制权移交给另一个Agent**。这是一个"信号"工具——它不执行目标Agent，而是在当前 Invocation 上设置 `TransferInfo`，由上层 Runner/Flow 框架检测并执行实际的 Agent 切换。

关键特性：
- **控制权移交**，不是子任务委派
- **不执行目标Agent**，只设置信号
- **目标Agent由名称查找**，从预注册的 `[]agent.Info` 列表中匹配
- **上层框架负责**实际的Agent切换执行

##### 调用流程链路

```
父Agent LLM 调用 transfer_to_agent 工具
  │
  ▼
Tool.Call()                                   [transfer_tool.go:114]
  ├── 解析 Request{agent_name, message}
  ├── findAgentInfo() → 从 availableAgents 列表查找目标Agent
  │   └── [未找到] → 返回错误 Response
  │
  ├── agent.InvocationFromContext(ctx) → 获取当前Invocation
  │
  ├── invocation.TransferInfo = &agent.TransferInfo{
  │       TargetAgentName: targetAgentInfo.Name,
  │       Message:         req.Message,
  │   }
  │   └── 在Invocation上设置移交信号
  │
  └── 返回 Response{Success: true, TransferType: "agent_handoff"}

[上层框架检测 TransferInfo]
  └── Runner/Flow 检测到 TransferInfo 后执行实际的Agent切换
```

---

## 三、异同深度对比

### 相同点

1. **都是 Agent 间协作机制**：解决"一个 Agent 无法独立完成所有任务"的问题
2. **都通过 tool.Tool 接口暴露给 LLM**：LLM 通过工具调用触发
3. **都从 Invocation Context 获取当前会话信息**：使用 `agent.InvocationFromContext(ctx)`

### 核心差异

#### 执行模型

```
subagents_spawn / taskrun:
  父Agent ──spawn──▶ [独立goroutine + 独立Session + Runner.Run()]
  父Agent ◀──notify── [子Agent完成通知]
  → 异步、独立生命周期、可并行多个

agenttool.NewTool:
  父Agent ──Call──▶ [子Invocation(Clone) + agent.RunWithPlugins()]
  父Agent ◀──return── [子Agent结果作为工具返回值]
  → 同步、共享Session、串行执行

transfer_to_agent:
  父Agent ──signal──▶ [设置TransferInfo]
  上层框架 ──switch──▶ [切换到目标Agent继续会话]
  → 控制权移交、不返回原Agent
```

#### 会话与历史

| 机制 | Session | 历史可见性 | 事件流 |
|------|---------|-----------|--------|
| subagents_spawn | 独立子Session | 不继承 | 完全隔离 |
| taskrun | 独立子Session | 不继承 | 完全隔离 |
| agenttool.NewTool | 共享父Session | 可选继承 | 镜像到父Session |
| transfer_to_agent | 同一Session | 完全继承 | 同一事件流 |

#### 适用场合

| 场景 | 推荐机制 | 原因 |
|------|---------|------|
| 长时间后台任务（代码重构、批量处理） | `subagents_spawn` | 异步不阻塞、有完成通知、支持worktree隔离 |
| 需要并行执行多个子任务 | `subagents_spawn` | 可spawn多个async子Agent |
| 需要用户审核子Agent结果后继续 | `subagents_spawn (review模式)` | review模式暂停等用户确认 |
| 简单的同步子任务委派 | `agenttool.NewTool` | 同步返回、共享上下文、开销小 |
| 需要子Agent看到父对话历史 | `agenttool.NewTool (ParentBranch)` | 历史继承 |
| 需要动态限制子Agent能力边界 | `NewDynamicTool` | 按调用选择tools/skills/instruction |
| 需要将对话转给专业Agent | `transfer_to_agent` | 控制权移交 |
| 通用后台任务编排（非OpenClaw场景） | `agent/taskrun` | 纯框架层、无产品绑定 |

---

## 四、架构层次关系

```
┌─────────────────────────────────────────────────┐
│              OpenClaw 产品层                      │
│  subagentrun.Service (包装 taskrun + worktree    │
│  + delivery通知 + review模式)                     │
│  工具: subagents_spawn/list/get/cancel/wait      │
├─────────────────────────────────────────────────┤
│              框架核心层                           │
│  taskrun.Controller (接口)                       │
│  taskruninprocess.Service (实现: goroutine+Store) │
│  工具: start_task_run/list/get/cancel/wait       │
├─────────────────────────────────────────────────┤
│              工具层                               │
│  agenttool.NewTool (Agent→Tool同步包装)           │
│  agenttool.NewDynamicTool (动态能力边界)          │
│  transfer_to_agent (控制权移交信号)               │
└─────────────────────────────────────────────────┘
```

**关键洞察**：`subagents_spawn` 的底层就是 `taskrun`——OpenClaw 的 `subagentrun.Service` 内部持有 `taskruninprocess.Service` 实例（service.go:48 `core *taskruninprocess.Service`），在其 `Spawn` 方法中调用 `s.core.Spawn()`（service.go:191）。`subagents_spawn` 是 `taskrun` 的产品化封装，增加了 worktree 隔离、完成通知、review 模式等 OpenClaw 特有能力。

---

## 五、关键源码文件索引

| 机制 | 文件路径 | 核心内容 |
|------|---------|---------|
| subagents_spawn 工具定义 | `openclaw/internal/subagentrun/tool.go` | spawnTool/listTool/getTool/cancelTool/waitTool |
| subagents_spawn 服务层 | `openclaw/internal/subagentrun/service.go` | Service.Spawn/OnRunUpdate/notifyCompletion |
| subagents_spawn 类型 | `openclaw/internal/subagentrun/types.go` | SpawnRequest/deliveryTarget/metadata常量 |
| subagent 公共接口 | `openclaw/subagent/subagent.go` | Run/Status/Workspace/Service接口 |
| taskrun 接口定义 | `agent/taskrun/types.go` | Controller/SpawnRequest/Run/Status |
| taskrun 进程内实现 | `agent/taskrun/inprocess/service.go` | Service.Spawn/execute/finishRun/Wait |
| taskrun 进程内类型 | `agent/taskrun/inprocess/types.go` | Finalizer/Observer/类型别名 |
| taskrun 工具封装 | `tool/taskrun/tool.go` | start_task_run等LLM工具 |
| agenttool.NewTool | `tool/agent/agent_tool.go` | Tool.Call/callWithParentInvocation/collectResponse |
| agenttool.NewDynamicTool | `tool/agent/dynamic_tool.go` | callDynamic/buildDynamicSubInvocation/buildDynamicPatch |
| transfer_to_agent | `tool/transfer/transfer_tool.go` | Tool.Call/TransferInfo设置 |
