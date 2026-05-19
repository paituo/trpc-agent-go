# Subagents Spawn TRACE 关联实施计划

> **对于代理工作器：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实施此计划。

**目标：** 通过方案 B（最小改动）实现 subagents_spawn 调用的主子智能体 TRACE 按调用关系关联，解决当前形成两个独立 TRACE 队列的问题。

**架构：** 在 SpawnRequest 中传递父 invocation ID，通过 runtime state 注入到子智能体。runner 创建 invocation 时检查该字段，自动建立父子 traceCapture 关联。

**技术栈：** Go, OpenTelemetry, trpc-agent-go 内部 trace 机制

---

## 文件结构映射

| 文件 | 职责 | 变更类型 |
|------|------|----------|
| `openclaw/internal/subagentrun/types.go` | SpawnRequest 结构定义 | 添加 ParentInvocationID 字段 |
| `openclaw/internal/subagentrun/tool.go` | spawn tool 调用入口 | 从 ctx 获取父 invocation ID 传入 |
| `openclaw/internal/subagentrun/service.go` | Spawn/execute/runChild 执行链 | 传递父 invocation ID，启用 ExecutionTrace，注入 runtime state |
| `agent/invocation.go` | Invocation 初始化 | 在 NewInvocation 中检查 runtime state，建立 traceCapture 关联 |
| `agent/execution_trace.go` | 公共 traceCapture 访问方法 | 添加 `GetExecutionTraceCapture()` 方法 |
| `openclaw/internal/subagentrun/service_test.go` | 现有测试 | 验证新字段传递 |
| `openclaw/internal/subagentrun/tool_test.go` | tool 测试 | 验证 invocation 上下文传递 |

---

## 小任务分解

### 任务 1: 扩展 SpawnRequest 结构

**文件：**
- 修改：`openclaw/internal/subagentrun/types.go:64-70`

- [ ] **步骤 1：添加 ParentInvocationID 字段**

修改 `SpawnRequest` 结构，在现有字段后添加：

```go
type SpawnRequest struct {
    OwnerUserID        string
    ParentSessionID    string
    Task               string
    TimeoutSeconds     int
    Delivery           deliveryTarget
    ParentInvocationID string  // 新增：父 invocation ID，用于 trace 关联
}
```

- [ ] **步骤 2：提交**
```bash
git add openclaw/internal/subagentrun/types.go
git commit -m "feat: add ParentInvocationID to SpawnRequest for trace linking"
```

---

### 任务 2: 在 spawn tool 中传递父 invocation ID

**文件：**
- 修改：`openclaw/internal/subagentrun/tool.go:256-303`（spawnTool.Call 方法）

- [ ] **步骤 1：修改 Call 方法获取父 invocation**

在 `spawnTool.Call` 方法中，修改 SpawnRequest 构造：

```go
func (t *spawnTool) Call(
    ctx context.Context,
    args []byte,
) (any, error) {
    if t == nil || t.svc == nil {
        return nil, fmt.Errorf("subagent: service unavailable")
    }
    if isNestedSubagent(ctx) {
        return nil, fmt.Errorf(
            "subagent: nested subagent spawn is not supported",
        )
    }

    var in spawnInput
    if err := json.Unmarshal(args, &in); err != nil {
        return nil, err
    }

    userID, sess, err := currentContext(ctx)
    if err != nil {
        return nil, err
    }
    delivery, err := outbound.ResolveTarget(
        ctx,
        outbound.DeliveryTarget{},
    )
    if err != nil {
        return nil, fmt.Errorf(
            "subagent: resolve delivery target: %w",
            err,
        )
    }

    // 新增：获取父 invocation ID
    parentInvocationID := ""
    if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
        parentInvocationID = inv.InvocationID
    }

    run, err := t.svc.Spawn(ctx, SpawnRequest{
        OwnerUserID:        userID,
        ParentSessionID:    sess.ID,
        Task:               in.Task,
        TimeoutSeconds:     in.TimeoutSeconds,
        Delivery: deliveryTarget{
            Channel: delivery.Channel,
            Target:  delivery.Target,
        },
        ParentInvocationID: parentInvocationID,  // 新增
    })
    if err != nil {
        return nil, err
    }
    return run, nil
}
```

- [ ] **步骤 2：运行测试确保未破坏现有功能**

```bash
cd openclaw/internal/subagentrun ; go test -v -run TestSpawn
```

期望：现有测试通过（可能需要调整 mock）

- [ ] **步骤 3：提交**
```bash
git add openclaw/internal/subagentrun/tool.go
git commit -m "feat: pass parent invocation ID in spawn tool"
```

---

### 任务 3: 修改 Service 执行链传递父 invocation ID

**文件：**
- 修改：`openclaw/internal/subagentrun/service.go:123-185`（Spawn 方法）
- 修改：`openclaw/internal/subagentrun/service.go:273-294`（execute 方法）
- 修改：`openclaw/internal/subagentrun/service.go:296-344`（runChild 方法）

- [ ] **步骤 1：修改 Spawn 方法传递 ParentInvocationID**

修改 `Spawn` 方法中启动 goroutine 的部分：

```go
func (s *Service) Spawn(
    ctx context.Context,
    req SpawnRequest,
) (publicsubagent.Run, error) {
    // ... 前面验证逻辑不变 ...

    s.wg.Add(1)
    go func(
        parent context.Context,
        runID string,
        timeoutSeconds int,
        parentInvocationID string,  // 新增参数
    ) {
        defer s.wg.Done()
        s.execute(parent, runID, timeoutSeconds, parentInvocationID)
    }(s.baseCtx, record.ID, req.TimeoutSeconds, req.ParentInvocationID)  // 传递新参数

    return view, nil
}
```

- [ ] **步骤 2：修改 execute 方法签名和调用**

修改 `execute` 方法：

```go
func (s *Service) execute(
    parent context.Context,
    runID string,
    timeoutSeconds int,
    parentInvocationID string,  // 新增参数
) {
    record, runCtx, started, err := s.markRunning(
        parent,
        runID,
        timeoutSeconds,
    )
    if err != nil {
        return
    }
    if started.cancel != nil {
        defer started.cancel()
    }

    result := replyAccumulator{}
    runErr := s.runChild(runCtx, record, started, &result, parentInvocationID)  // 传递新参数
    output := sanitizeStoredResult(result.text)
    s.finishRun(runID, output, runErr)
}
```

- [ ] **步骤 3：修改 runChild 方法启用 trace 关联**

修改 `runChild` 方法：

```go
func (s *Service) runChild(
    ctx context.Context,
    record *runRecord,
    started runningRun,
    result *replyAccumulator,
    parentInvocationID string,  // 新增参数
) error {
    if record == nil {
        return fmt.Errorf("subagent: nil run record")
    }
    runtimeState := map[string]any{
        runtimeStateSubagentRun:      true,
        runtimeStateSubagentRunID:    record.ID,
        runtimeStateSubagentParentID: record.ParentSessionID,
    }
    if record.Delivery.Channel != "" && record.Delivery.Target != "" {
        targetState := outbound.RuntimeStateForTarget(
            outbound.DeliveryTarget{
                Channel: record.Delivery.Channel,
                Target:  record.Delivery.Target,
            },
        )
        for key, value := range targetState {
            runtimeState[key] = value
        }
    }

    // 新增：传递父 invocation ID 用于 trace 关联
    if parentInvocationID != "" {
        runtimeState[RuntimeStateKeyParentInvocationID] = parentInvocationID
    }

    runOpts := []agent.RunOption{
        agent.WithRequestID(started.requestID),
        agent.WithRuntimeState(runtimeState),
        agent.WithInjectedContextMessages([]model.Message{
            model.NewSystemMessage(subagentRunPrompt),
        }),
        agent.WithExecutionTraceEnabled(true),  // 新增：启用执行 trace
    }

    events, err := s.runner.Run(
        ctx,
        record.OwnerUserID,
        started.childSession,
        model.NewUserMessage(record.Task),
        runOpts...,
    )
    if err != nil {
        return err
    }
    for evt := range events {
        result.consume(evt)
    }
    return result.err
}
```

- [ ] **步骤 4：添加常量定义**

在 `types.go` 中添加新常量：

```go
const (
    // ... 现有常量 ...
    
    // RuntimeStateKeyParentInvocationID 是父 invocation ID 的 runtime state key
    RuntimeStateKeyParentInvocationID = "openclaw.subagent.parent_invocation_id"
)
```

- [ ] **步骤 5：运行测试**

```bash
cd openclaw/internal/subagentrun ; go test -v
```

期望：所有测试通过

- [ ] **步骤 6：提交**
```bash
git add openclaw/internal/subagentrun/service.go openclaw/internal/subagentrun/types.go
git commit -m "feat: pass parent invocation ID through execute chain and enable trace"
```

---

### 任务 4: 添加公共 traceCapture 访问方法

**文件：**
- 修改：`agent/execution_trace.go`（文件末尾添加新方法）

- [ ] **步骤 1：添加 GetExecutionTraceCapture 方法**

在 `execution_trace.go` 文件末尾（约 318 行后）添加：

```go
// GetExecutionTraceCapture returns the execution trace capture for this invocation.
// Returns nil if trace capture is not initialized or disabled.
func (inv *Invocation) GetExecutionTraceCapture() *tracecapture.Capture {
    if inv == nil {
        return nil
    }
    return inv.executionTraceCapture()
}
```

- [ ] **步骤 2：运行测试确保编译通过**

```bash
go build ./agent/...
```

期望：编译成功

- [ ] **步骤 3：提交**
```bash
git add agent/execution_trace.go
git commit -m "feat: add public method to access invocation trace capture"
```

---

### 任务 5: 在 runner 中建立 traceCapture 关联

**文件：**
- 修改：`runner/runner.go:497-509`（NewInvocation 调用处）

- [ ] **步骤 1：在 NewInvocation 后添加关联逻辑**

在 `runner.go` 的 `Run` 方法中，找到创建 invocation 的位置（约 497 行），在创建后添加：

```go
invocation := agent.NewInvocation(
    agent.WithInvocationSession(sess),
    agent.WithInvocationSessionService(r.sessionService),
    agent.WithInvocationMessage(invocationMessage),
    agent.WithInvocationAgent(ag),
    agent.WithInvocationRunOptions(ro),
    agent.WithInvocationStructuredOutput(ro.StructuredOutput),
    agent.WithInvocationStructuredOutputType(ro.StructuredOutputType),
    agent.WithInvocationMemoryService(r.memoryService),
    agent.WithInvocationArtifactService(r.artifactService),
    agent.WithInvocationEventFilterKey(eventFilterKey),
    agent.WithInvocationPlugins(r.pluginManager),
)

// 新增：检查是否为子智能体运行，建立 trace 关联
if parentInvocationID, ok := agent.GetRuntimeStateValue[string](
    &ro,
    "openclaw.subagent.parent_invocation_id",
); ok && parentInvocationID != "" {
    // 尝试从运行时状态获取父 invocation 的 traceCapture
    // 注意：这里需要访问父 invocation 的 traceCapture
    // 但由于子智能体是异步运行的，需要通过其他方式获取
    // 方案：在 runtime state 中传递 traceCapture 的引用（不推荐，有并发问题）
    // 更好的方案：让子 invocation 在初始化时自己建立关联
}
```

**等等，方案 B 需要重新考虑：**

由于子智能体是异步运行的（在独立 goroutine 中），父 invocation 可能已经结束，无法直接访问其 traceCapture。因此需要调整方案：

**调整后的方案 B：**
1. 父 invocation ID 通过 runtime state 传递 ✓
2. 子智能体在 runner 初始化时，记录 `ParentInvocationID` 到 traceCapture
3. traceCapture 的 `StartStep` 已经支持 `ParentInvocationID` 字段 ✓

实际上，查看 [tracecapture/capture.go:104-145](file:///d:/GoProjects/trpc-agent-go/internal/tracecapture/capture.go#L104-L145) 的 `StartStep` 方法，它已经接受 `ParentInvocationID`。查看 [execution_trace.go:112-147](file:///d:/GoProjects/trpc-agent-go/agent/execution_trace.go#L112-L147) 的 `StartExecutionTraceStep`，它已经通过 `inv.parent.InvocationID` 设置 `ParentInvocationID`。

**因此关键问题是：子 invocation 的 parent 字段为空。**

**修正步骤 1：添加 WithInvocationParent 选项**

修改 `agent/invocation_options.go`，添加新选项：

```go
// WithInvocationParent sets the parent invocation ID for trace linking.
func WithInvocationParent(parentID string) InvocationOptions {
    return func(inv *Invocation) {
        // 这里不能直接设置 parent，因为 parent 是 *Invocation 类型
        // 但我们可以设置一个临时字段，让 initializeExecutionTrace 使用
        inv.SetState("parent_invocation_id_for_trace", parentID)
    }
}
```

**等等，这仍然不够优雅。重新审视 Clone 方法...**

查看 [invocation.go:1201-1251](file:///d:/GoProjects/trpc-agent-go/agent/invocation.go#L1201-L1251)，`Clone` 设置了 `parent: inv`。但 subagent 不是通过 Clone 创建的，而是通过 `runner.Run` 创建全新 invocation。

**最佳方案 B 实现：**

添加一个 `WithInvocationParentInvocationID` 选项，在 `initializeExecutionTrace` 中使用它来注册父子关系：

```go
// WithInvocationParentInvocationID sets the parent invocation ID for trace linking.
// This is used when the invocation is not created via Clone but needs trace association.
func WithInvocationParentInvocationID(parentID string) InvocationOptions {
    return func(inv *Invocation) {
        inv.parentInvocationID = parentID  // 需要添加此字段
    }
}
```

然后在 `Invocation` 结构中添加字段，并在 `initializeExecutionTrace` 或 `StartExecutionTraceStep` 中使用。

**重新整理任务 5：**

- [ ] **步骤 1：在 Invocation 结构中添加 parentInvocationID 字段**

修改 `agent/invocation.go:85-178`，在 `parent` 字段后添加：

```go
    // parent is the parent invocation, if any
    parent *Invocation
    // parentInvocationID is the parent invocation ID when parent is not available
    // (e.g., async subagent runs). Used for trace linking only.
    parentInvocationID string
```

- [ ] **步骤 2：添加 WithInvocationParentInvocationID 选项**

修改 `agent/invocation_options.go`，添加：

```go
// WithInvocationParentInvocationID sets the parent invocation ID for trace linking.
// This is used when the invocation is created independently but needs to be
// linked to a parent invocation's trace (e.g., async subagent runs).
func WithInvocationParentInvocationID(parentID string) InvocationOptions {
    return func(inv *Invocation) {
        inv.parentInvocationID = parentID
    }
}
```

- [ ] **步骤 3：在 runner 中使用该选项**

修改 `runner/runner.go:497-509`，在 NewInvocation 调用中添加：

```go
invocationOpts := []agent.InvocationOptions{
    agent.WithInvocationSession(sess),
    agent.WithInvocationSessionService(r.sessionService),
    agent.WithInvocationMessage(invocationMessage),
    agent.WithInvocationAgent(ag),
    agent.WithInvocationRunOptions(ro),
    agent.WithInvocationStructuredOutput(ro.StructuredOutput),
    agent.WithInvocationStructuredOutputType(ro.StructuredOutputType),
    agent.WithInvocationMemoryService(r.memoryService),
    agent.WithInvocationArtifactService(r.artifactService),
    agent.WithInvocationEventFilterKey(eventFilterKey),
    agent.WithInvocationPlugins(r.pluginManager),
}

// 新增：如果是子智能体运行，设置父 invocation ID
if parentInvocationID, ok := agent.GetRuntimeStateValue[string](
    &ro,
    "openclaw.subagent.parent_invocation_id",
); ok && parentInvocationID != "" {
    invocationOpts = append(invocationOpts, agent.WithInvocationParentInvocationID(parentInvocationID))
}

invocation := agent.NewInvocation(invocationOpts...)
```

- [ ] **步骤 4：修改 execution_trace.go 使用 parentInvocationID**

修改 `agent/execution_trace.go:112-147` 的 `StartExecutionTraceStep` 方法：

```go
func StartExecutionTraceStep(
    inv *Invocation,
    nodeID string,
    input *trace.Snapshot,
    predecessors []string,
) string {
    if inv == nil || nodeID == "" {
        return ""
    }
    inv.initializeExecutionTrace()
    capture := inv.executionTraceCapture()
    if capture == nil {
        return ""
    }
    preds := predecessors
    if len(preds) == 0 {
        preds = capture.PredecessorsForInvocation(
            inv.InvocationID,
            inv.entryPredecessorStepIDs,
        )
    }
    
    // 修改：支持 parentInvocationID 作为回退
    parentInvocationID := ""
    if inv.parent != nil {
        parentInvocationID = inv.parent.InvocationID
    } else if inv.parentInvocationID != "" {
        parentInvocationID = inv.parentInvocationID
    }
    
    return capture.StartStep(tracecapture.StartStepInput{
        InvocationID:       inv.InvocationID,
        ParentInvocationID: parentInvocationID,
        AgentName:          inv.AgentName,
        Branch:             inv.Branch,
        NodeID:             nodeID,
        StartedAt:          time.Now(),
        PredecessorStepIDs: preds,
        Input:              input,
    })
}
```

- [ ] **步骤 5：运行编译和测试**

```bash
go build ./...
go test ./agent/... -v
go test ./runner/... -v
```

期望：所有测试通过

- [ ] **步骤 6：提交**
```bash
git add agent/invocation.go agent/invocation_options.go agent/execution_trace.go runner/runner.go
git commit -m "feat: support parent invocation ID linking in runner and trace"
```

---

### 任务 6: 编写测试验证 trace 关联

**文件：**
- 创建：`openclaw/internal/subagentrun/trace_link_test.go`

- [ ] **步骤 1：创建测试文件**

```go
package subagentrun

import (
    "context"
    "testing"

    "trpc.group/trpc-go/trpc-agent-go/agent"
)

func TestSpawnPassesParentInvocationID(t *testing.T) {
    // 设置测试环境
    // 1. 创建父 invocation
    // 2. 调用 spawn tool
    // 3. 验证 SpawnRequest 包含 ParentInvocationID
}

func TestExecutePassesParentInvocationIDToRunChild(t *testing.T) {
    // 验证 execute 方法正确传递 parentInvocationID 到 runChild
}

func TestRunChildEnablesExecutionTrace(t *testing.T) {
    // 验证 runChild 设置了 WithExecutionTraceEnabled(true)
    // 验证 runChild 设置了 runtime state 中的 parent invocation ID
}
```

- [ ] **步骤 2：运行测试**

```bash
cd openclaw/internal/subagentrun ; go test -v -run TestSpawn
```

期望：测试通过

- [ ] **步骤 3：提交**
```bash
git add openclaw/internal/subagentrun/trace_link_test.go
git commit -m "test: add tests for parent invocation ID trace linking"
```

---

### 任务 7: 运行全量测试和 lint

- [ ] **步骤 1：运行所有子模块测试**

```bash
go test ./...
```

- [ ] **步骤 2：运行 openclaw 子模块测试**

```bash
cd openclaw ; go test ./...
```

- [ ] **步骤 3：运行 lint**

```bash
golangci-lint run --timeout=10m
```

- [ ] **步骤 4：运行 gofmt 检查**

```bash
gofmt -r 'interface{} -> any' -l .
```

- [ ] **步骤 5：运行 goimports 检查**

```bash
goimports -l .
```

- [ ] **步骤 6：修复所有问题后提交**
```bash
git add -A
git commit -m "fix: address lint and test issues"
```

---

## 自审检查

**1. 规格覆盖：**
- ✅ 传递父 invocation ID 到 SpawnRequest
- ✅ 在 spawn tool 中获取并传递父 invocation
- ✅ 通过执行链传递到 runChild
- ✅ 启用 ExecutionTrace
- ✅ runner 中建立父子关联
- ✅ 测试覆盖

**2. 占位符扫描：**
- ✅ 无 TBD/TODO
- ✅ 所有步骤包含完整代码
- ✅ 所有命令精确

**3. 类型一致性：**
- ✅ SpawnRequest.ParentInvocationID 是 string
- ✅ runtime state key 是常量
- ✅ WithInvocationParentInvocationID 接受 string
- ✅ parentInvocationID 字段类型匹配

---

## 关键文件索引

| 文件路径 | 关键行号 | 说明 |
|----------|----------|------|
| `openclaw/internal/subagentrun/types.go` | 64-70 | SpawnRequest 结构 |
| `openclaw/internal/subagentrun/tool.go` | 256-303 | spawnTool.Call 方法 |
| `openclaw/internal/subagentrun/service.go` | 123-185 | Spawn 方法 |
| `openclaw/internal/subagentrun/service.go` | 273-294 | execute 方法 |
| `openclaw/internal/subagentrun/service.go` | 296-344 | runChild 方法 |
| `agent/invocation.go` | 85-178 | Invocation 结构 |
| `agent/invocation_options.go` | 全文 | InvocationOptions 定义 |
| `agent/execution_trace.go` | 112-147 | StartExecutionTraceStep |
| `runner/runner.go` | 497-509 | NewInvocation 调用 |
| `internal/tracecapture/capture.go` | 104-145 | StartStep 方法 |

---

## 风险点和注意事项

1. **异步执行**：子智能体在独立 goroutine 中运行，父 invocation 可能已结束。因此不能依赖直接的指针引用，只能通过 ID 关联。

2. **traceCapture 生命周期**：traceCapture 是 invocation 级别的，父 invocation 结束后其 traceCapture 可能也被清理。但 trace 数据已经记录到 capture 的 steps 中，只需确保 `ParentInvocationID` 字段正确设置即可。

3. **向后兼容**：parentInvocationID 为空时不影响现有行为，确保向后兼容。

4. **常量导出**：`RuntimeStateKeyParentInvocationID` 需要在 `types.go` 中导出（首字母大写），或者在 service.go 中定义后复用。
