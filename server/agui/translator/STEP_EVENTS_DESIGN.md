# AGUI Translator 层 STEP 事件统一实现方案 V3.1

## AutoPlan 评审决议

| 评审问题 | 决议 |
|---------|------|
| 工具步骤的 parentId | 策略 A：translator 维护 `currentTodoStepID`，工具步骤自动继承 |
| UUID 依赖 | 策略 A：引入 `github.com/google/uuid` |
| 事件流示例修正 | `todoStepEventsEnabled=true` 时 todo_write 不发射通用 tool step |
| activeStep 存储 | 改为 `map[string]*activeStepInfo`，包含 stepID + toolName |
| reasoning step 关闭 | 推理结束时关闭，允许后续推理重新打开 |
| todo_write oldTodos | 工具必须输出 `oldTodos` 字段，否则跳过 diff |

## 概述

在 AGUI translator 层统一处理步骤事件，将智能体的计划/执行/思考过程映射为 AGUI CUSTOM 事件（`step.started` / `step.finished`），使前端能渲染 DIFY 风格的步骤卡片列表。

### 核心设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| 事件类型 | AGUI `CUSTOM`（name=`step.started/step.finished`） | 摆脱 `STEP_STARTED/STEP_FINISHED` 的 `ValidateSequence` 约束，payload 完全自定义 |
| 步骤标识 | GUID 做 ID | 跨会话/跨实例唯一，后期兼容性好 |
| todo 显示名 | `activeForm` 做 `displayName` | "正在新建工程" 比 "todo-1" 对用户更友好 |
| 嵌套支持 | `parentId` 字段 | 工具步骤可关联到 todo 步骤，前端构建步骤树 |
| 排除工具 | 数组硬编码 | 简单直接，后期优化 |
| todo diff 数据源 | `Output.OldTodos` | 不依赖 translator 状态，无状态丢失风险 |
| todo_write 双重 STEP | 替代模式 | `todoStepEventsEnabled=true` 时 todo_write 不发射通用 STEP |

## CUSTOM 事件 Payload 设计

### step.started

```json
{
  "type": "CUSTOM",
  "name": "step.started",
  "value": {
    "id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
    "parentId": "parent-guid-here",
    "displayName": "正在设置工程信息",
    "stepType": "todo",
    "toolName": "",
    "status": "in_progress"
  }
}
```

### step.finished

```json
{
  "type": "CUSTOM",
  "name": "step.finished",
  "value": {
    "id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
    "parentId": "parent-guid-here",
    "displayName": "设置工程信息",
    "stepType": "todo",
    "toolName": "",
    "status": "completed"
  }
}
```

### stepType 枚举

| stepType | 含义 | displayName 来源 | 触发时机 |
|----------|------|-----------------|---------|
| `tool` | 通用工具调用步骤 | toolName | TOOL_CALL_START 前 / TOOL_CALL_RESULT 后 |
| `todo` | TODO 任务步骤 | activeForm (started) / content (finished) | todo_write 工具结果 diff |
| `reasoning` | 推理/思考步骤 | 固定 "思考中" | ReasoningContent 首次出现 / 推理结束 |

### status 枚举

| status | 含义 |
|--------|------|
| `running` | 工具/推理步骤正在执行 |
| `in_progress` | TODO 任务正在执行 |
| `completed` | 步骤已完成 |

---

## 第一部分：修改 `tool/todo/todo.go`

### Item 结构变更

```go
type Item struct {
    // ID is a unique identifier for this item. If empty, the tool
    // auto-generates a GUID. Used by AG-UI step events to pair
    // step.started with step.finished and to support nested hierarchies.
    ID string `json:"id" description:"Unique identifier for this task item. Auto-generated GUID if empty."`

    // ParentID references the parent item's ID. When non-empty,
    // this item is a sub-task of the referenced parent. Frontends
    // can render a nested step tree using ID/ParentID relationships.
    ParentID string `json:"parentId,omitempty" description:"ID of the parent task item. Empty for top-level items."`

    Content    string `json:"content"    description:"Imperative description of the task, e.g. 'Run tests'"`
    ActiveForm string `json:"activeForm" description:"Present-continuous form shown while the task is running, e.g. 'Running tests'"`
    Status     Status `json:"status"     jsonschema:"enum=pending,enum=in_progress,enum=completed" description:"One of: pending | in_progress | completed"`
}
```

### Declaration schema 变更

```go
itemSchema := &tool.Schema{
    Type: "object",
    Properties: map[string]*tool.Schema{
        "id": {
            Type:        "string",
            Description: "Unique identifier for this task item. Auto-generated GUID if empty.",
        },
        "parentId": {
            Type:        "string",
            Description: "ID of the parent task item. Empty for top-level items.",
        },
        "content":   { /* 不变 */ },
        "activeForm": { /* 不变 */ },
        "status":    { /* 不变 */ },
    },
    Required: []string{"content", "activeForm", "status"}, // id 和 parentId 可选
}
```

### Run 方法中自动生成 GUID

```go
import "github.com/google/uuid"

// 在 Run 方法中，执行逻辑前：
for i := range input.Todos {
    if input.Todos[i].ID == "" {
        input.Todos[i].ID = uuid.New().String()
    }
}
```

---

## 第二部分：新建 `translator/step_events.go`

```go
package translator

import (
    "encoding/json"

    "github.com/google/uuid"
    aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
    "trpc.group/trpc-go/trpc-agent-go/model"
)

// stepExcludedTools lists tool names that should not produce step events.
var stepExcludedTools = []string{
    "sequential_thinking",
    "skill_load",
    "session_load",
    "session_search",
}

// stepPayload is the value payload for CUSTOM events with name "step.started"
// or "step.finished".
type stepPayload struct {
    ID          string `json:"id"`
    ParentID    string `json:"parentId,omitempty"`
    DisplayName string `json:"displayName"`
    StepType    string `json:"stepType"`    // "tool" | "todo" | "reasoning"
    ToolName    string `json:"toolName,omitempty"`
    Status      string `json:"status"`       // "running"/"in_progress" | "completed"
}

// todoOutput matches the JSON structure of todo_write's Output.
type todoOutput struct {
    Message  string     `json:"message"`
    Todos    []todoItem `json:"todos"`
    OldTodos []todoItem `json:"oldTodos,omitempty"`
}

// todoItem matches todo.Item for JSON deserialization.
type todoItem struct {
    ID         string `json:"id"`
    ParentID   string `json:"parentId,omitempty"`
    Content    string `json:"content"`
    ActiveForm string `json:"activeForm"`
    Status     string `json:"status"`
}

const (
    statusPending    = "pending"
    statusInProgress = "in_progress"
    statusCompleted  = "completed"
)

// newStepID generates a GUID for step events.
func newStepID() string {
    return uuid.New().String()
}

// isStepExcludedTool checks if a tool name is in the exclusion list.
func isStepExcludedTool(toolName string) bool {
    for _, excluded := range stepExcludedTools {
        if toolName == excluded {
            return true
        }
    }
    return false
}

// isTodoWriteTool checks if the tool is todo_write.
func isTodoWriteTool(name string) bool {
    return name == "todo_write"
}

// stepEventsForToolCall emits step.started CUSTOM events before TOOL_CALL_START.
// When todoStepEventsEnabled is true, todo_write is skipped here
// (todo diff events take over instead).
func (t *translator) stepEventsForToolCall(rsp *model.Response) []aguievents.Event {
    if !t.opts.stepEventsEnabled || rsp == nil {
        return nil
    }
    var events []aguievents.Event
    for _, choice := range rsp.Choices {
        for _, tc := range choice.Message.ToolCalls {
            if isStepExcludedTool(tc.Function.Name) {
                continue
            }
            if t.opts.todoStepEventsEnabled && isTodoWriteTool(tc.Function.Name) {
                continue // todo diff events will handle this
            }
            stepID := newStepID()
            if t.activeStep == nil {
                t.activeStep = make(map[string]string) // toolCallID → stepID
            }
            t.activeStep[tc.ID] = stepID
            events = append(events, aguievents.NewCustomEvent("step.started",
                aguievents.WithValue(stepPayload{
                    ID:          stepID,
                    DisplayName: tc.Function.Name,
                    StepType:    "tool",
                    ToolName:    tc.Function.Name,
                    Status:      "running",
                }),
            ))
        }
        // Delta tool calls (streaming)
        for _, tc := range choice.Delta.ToolCalls {
            if tc.Function.Name == "" {
                continue
            }
            if isStepExcludedTool(tc.Function.Name) {
                continue
            }
            if t.opts.todoStepEventsEnabled && isTodoWriteTool(tc.Function.Name) {
                continue
            }
            stepID := newStepID()
            if t.activeStep == nil {
                t.activeStep = make(map[string]string)
            }
            t.activeStep[tc.ID] = stepID
            events = append(events, aguievents.NewCustomEvent("step.started",
                aguievents.WithValue(stepPayload{
                    ID:          stepID,
                    DisplayName: tc.Function.Name,
                    StepType:    "tool",
                    ToolName:    tc.Function.Name,
                    Status:      "running",
                }),
            ))
        }
    }
    return events
}

// stepEventsForToolResult emits step.finished CUSTOM events after TOOL_CALL_RESULT.
func (t *translator) stepEventsForToolResult(rsp *model.Response) []aguievents.Event {
    if !t.opts.stepEventsEnabled || rsp == nil {
        return nil
    }
    var events []aguievents.Event
    for _, choice := range rsp.Choices {
        events = append(events, t.finishStepForToolID(choice.Message.ToolID, choice.Message.ToolName)...)
        events = append(events, t.finishStepForToolID(choice.Delta.ToolID, choice.Delta.ToolName)...)
    }
    return events
}

func (t *translator) finishStepForToolID(toolCallID string, toolName string) []aguievents.Event {
    if toolCallID == "" {
        return nil
    }
    stepID, ok := t.activeStep[toolCallID]
    if !ok {
        return nil
    }
    delete(t.activeStep, toolCallID)
    displayName := toolName
    if displayName == "" {
        displayName = stepID
    }
    return []aguievents.Event{
        aguievents.NewCustomEvent("step.finished",
            aguievents.WithValue(stepPayload{
                ID:          stepID,
                DisplayName: displayName,
                StepType:    "tool",
                ToolName:    toolName,
                Status:      "completed",
            }),
        ),
    }
}

// todoStepEventsFromResult diffs todo_write tool results and emits
// step.started / step.finished CUSTOM events for items that changed status.
// Uses OldTodos from the output for diffing (no translator state needed).
func (t *translator) todoStepEventsFromResult(rsp *model.Response) []aguievents.Event {
    if !t.opts.todoStepEventsEnabled || rsp == nil {
        return nil
    }
    var todoContent string
    for _, choice := range rsp.Choices {
        if isTodoWriteTool(choice.Message.ToolName) && choice.Message.Content != "" {
            todoContent = choice.Message.Content
            break
        }
    }
    if todoContent == "" {
        return nil
    }

    var output todoOutput
    if err := json.Unmarshal([]byte(todoContent), &output); err != nil {
        return nil
    }

    // Build old status map from OldTodos (provided by the tool)
    oldStatus := make(map[string]string, len(output.OldTodos))
    for _, item := range output.OldTodos {
        oldStatus[item.ID] = string(item.Status)
    }

    var events []aguievents.Event
    for _, item := range output.Todos {
        old := oldStatus[item.ID]
        switch {
        case old != statusInProgress && item.Status == statusInProgress:
            // pending → in_progress: step.started
            events = append(events, aguievents.NewCustomEvent("step.started",
                aguievents.WithValue(stepPayload{
                    ID:          item.ID,
                    ParentID:    item.ParentID,
                    DisplayName: item.ActiveForm,
                    StepType:    "todo",
                    Status:      statusInProgress,
                }),
            ))
        case old != statusCompleted && item.Status == statusCompleted:
            // in_progress → completed: step.finished
            events = append(events, aguievents.NewCustomEvent("step.finished",
                aguievents.WithValue(stepPayload{
                    ID:          item.ID,
                    ParentID:    item.ParentID,
                    DisplayName: item.Content,
                    StepType:    "todo",
                    Status:      statusCompleted,
                }),
            ))
        }
    }
    return events
}

// reasoningStepStarted emits step.started for reasoning phase.
func (t *translator) reasoningStepStarted(rsp *model.Response) []aguievents.Event {
    if !t.opts.stepEventsEnabled || t.reasoningStepOpen {
        return nil
    }
    hasReasoning := false
    for _, choice := range rsp.Choices {
        if choice.Delta.ReasoningContent != "" || choice.Message.ReasoningContent != "" {
            hasReasoning = true
            break
        }
    }
    if !hasReasoning {
        return nil
    }
    t.reasoningStepOpen = true
    t.reasoningStepID = newStepID()
    return []aguievents.Event{
        aguievents.NewCustomEvent("step.started",
            aguievents.WithValue(stepPayload{
                ID:          t.reasoningStepID,
                DisplayName: "思考中",
                StepType:    "reasoning",
                Status:      "running",
            }),
        ),
    }
}

// closeActiveSteps emits step.finished for all unclosed steps.
// Called from PostRunFinalizationEvents.
func (t *translator) closeActiveSteps() []aguievents.Event {
    var events []aguievents.Event
    if t.reasoningStepOpen {
        t.reasoningStepOpen = false
        events = append(events, aguievents.NewCustomEvent("step.finished",
            aguievents.WithValue(stepPayload{
                ID:          t.reasoningStepID,
                DisplayName: "思考中",
                StepType:    "reasoning",
                Status:      "completed",
            }),
        ))
    }
    for toolCallID, stepID := range t.activeStep {
        events = append(events, aguievents.NewCustomEvent("step.finished",
            aguievents.WithValue(stepPayload{
                ID:          stepID,
                DisplayName: stepID,
                StepType:    "tool",
                Status:      "completed",
            }),
        ))
        delete(t.activeStep, toolCallID)
    }
    return events
}
```

---

## 第三部分：修改 `translator/translator.go`

### 新增状态字段

```go
type translator struct {
    // ... 现有字段 ...

    // activeStep maps toolCallID → stepID for tool steps.
    activeStep map[string]string

    // reasoningStepOpen tracks whether a reasoning step is open.
    reasoningStepOpen bool

    // reasoningStepID is the step ID for the current reasoning step.
    reasoningStepID string
}
```

### Translate 方法修改

在 `rsp.IsToolCallResponse()` 分支中，`toolCallEvent` 之前插入：

```go
events = append(events, t.stepEventsForToolCall(rsp)...)
```

在 `rsp.IsToolResultResponse()` 分支中，`toolResultEvent` 前后插入：

```go
// todo step events before tool result
events = append(events, t.todoStepEventsFromResult(rsp)...)

toolResultEvents, err := t.toolResultEvent(rsp, event.ID)
// ... 现有代码 ...
events = append(events, toolResultEvents...)

// step.finished after tool result
events = append(events, t.stepEventsForToolResult(rsp)...)
```

### reasoningEvents 修改

在 `reasoningEvents` 方法开头插入：

```go
events = append(events, t.reasoningStepStarted(rsp)...)
```

### PostRunFinalizationEvents 修改

在现有 reasoning/text 关闭逻辑之后插入：

```go
// Close any unclosed step events
events = append(events, t.closeActiveSteps()...)
```

---

## 第四部分：修改 `translator/options.go`

```go
type options struct {
    // ... 现有字段 ...
    stepEventsEnabled     bool
    todoStepEventsEnabled bool
}

func WithStepEventsEnabled(enabled bool) Option {
    return func(o *options) { o.stepEventsEnabled = enabled }
}

func WithTodoStepEventsEnabled(enabled bool) Option {
    return func(o *options) { o.todoStepEventsEnabled = enabled }
}
```

---

## 第五部分：修改 `runner/options.go` + `runner/runner.go`

透传选项到 translator，不添加逻辑。

---

## 事件流示例

### 博微造价场景

```jsonl
{"type":"RUN_STARTED",...}

{"type":"CUSTOM","name":"step.started","value":{"id":"guid-1","displayName":"思考中","stepType":"reasoning","status":"running"}}
{"type":"REASONING_START",...}
{"type":"REASONING_MESSAGE_CONTENT","delta":"用户需要编制变电站工程...",...}
{"type":"REASONING_END",...}
{"type":"CUSTOM","name":"step.finished","value":{"id":"guid-1","displayName":"思考中","stepType":"reasoning","status":"completed"}}

{"type":"CUSTOM","name":"step.started","value":{"id":"guid-2","displayName":"todo_write","stepType":"tool","toolName":"todo_write","status":"running"}}
{"type":"TOOL_CALL_START","toolCallId":"tc-1","toolCallName":"todo_write",...}
{"type":"TOOL_CALL_ARGS","toolCallId":"tc-1","delta":"{todos:[...]}",...}
{"type":"TOOL_CALL_END","toolCallId":"tc-1",...}
{"type":"TOOL_CALL_RESULT","toolCallId":"tc-1","content":"{todos:[...],oldTodos:[]}",...}
{"type":"CUSTOM","name":"step.started","value":{"id":"todo-guid-1","displayName":"正在新建工程","stepType":"todo","status":"in_progress"}}
{"type":"CUSTOM","name":"step.started","value":{"id":"todo-guid-2","parentId":"todo-guid-1","displayName":"正在设置工程信息","stepType":"todo","status":"in_progress"}}
{"type":"CUSTOM","name":"step.started","value":{"id":"todo-guid-3","parentId":"todo-guid-1","displayName":"正在录入工程量","stepType":"todo","status":"in_progress"}}
{"type":"CUSTOM","name":"step.finished","value":{"id":"guid-2","displayName":"todo_write","stepType":"tool","toolName":"todo_write","status":"completed"}}

{"type":"CUSTOM","name":"step.started","value":{"id":"guid-3","parentId":"todo-guid-3","displayName":"read_document","stepType":"tool","toolName":"read_document","status":"running"}}
{"type":"TOOL_CALL_START","toolCallId":"tc-2","toolCallName="read_document",...}
{"type":"TOOL_CALL_ARGS","toolCallId":"tc-2","delta":"{path:\"设计图纸.pdf\"}",...}
{"type":"TOOL_CALL_END","toolCallId":"tc-2",...}
{"type":"TOOL_CALL_RESULT","toolCallId":"tc-2","content":"图纸内容...",...}
{"type":"CUSTOM","name":"step.finished","value":{"id":"guid-3","displayName":"read_document","stepType":"tool","toolName":"read_document","status":"completed"}}

{"type":"CUSTOM","name":"step.finished","value":{"id":"todo-guid-1","displayName":"新建工程","stepType":"todo","status":"completed"}}

{"type":"TEXT_MESSAGE_START",...}
{"type":"TEXT_MESSAGE_CONTENT","delta":"工程编制已完成...",...}
{"type":"TEXT_MESSAGE_END",...}

{"type":"RUN_FINISHED",...}
```

### 前端渲染效果

```
┌─ 工程编制进度 ──────────────────────────────────┐
│                                                   │
│  ✅ 新建工程                      (todo-guid-1)  │
│     ├─ 🔄 设置工程信息             (todo-guid-2)  │
│     └─ 🔄 录入工程量               (todo-guid-3)  │
│           └─ ✅ read_document     (guid-3)       │
│  ⏳ 完善措施规费                    (todo-guid-4)  │
│  ⏳ 设置材机价差                    (todo-guid-5)  │
│  ⏳ 生成报表                       (todo-guid-6)  │
│                                                   │
│  💬 助手: 正在设置工程信息...                      │
└───────────────────────────────────────────────────┘
```

---

## 文件修改汇总

| 文件 | 修改类型 | 行数估计 |
|------|---------|---------|
| `tool/todo/todo.go` | Item 增加 ID/ParentID + GUID 自动生成 + schema 更新 | +50 行 |
| `translator/step_events.go` | 新建 | ~230 行 |
| `translator/step_events_test.go` | 新建 | ~280 行 |
| `translator/options.go` | 新增 2 个选项 | +15 行 |
| `translator/translator.go` | Translate/PostRunFinalizationEvents 插入 STEP 逻辑 | +20 行 |
| `runner/options.go` | 透传 2 个选项 | +15 行 |
| `runner/runner.go` | 传递选项到 translator | +5 行 |

**总计约 615 行新增/修改代码**。

---

## 开关控制

```go
// 启用所有 STEP 事件（通用工具 + 推理 + todo）
aguirunner.WithStepEventsEnabled(true),
aguirunner.WithTodoStepEventsEnabled(true),

// 只启用 todo STEP 事件（最小切入点）
aguirunner.WithTodoStepEventsEnabled(true),

// 只启用通用工具 STEP 事件
aguirunner.WithStepEventsEnabled(true),
```

---

## 异常处理

1. **RUN_ERROR 时 STEP_FINISHED 缺失** — `PostRunFinalizationEvents` 中调用 `closeActiveSteps()`，确保所有未完成的 STEP 都被关闭
2. **todo_write 结果解析失败** — `json.Unmarshal` 失败时静默返回 nil，不影响其他事件
3. **toolCallID 不匹配** — `finishStepForToolID` 找不到对应 stepID 时返回 nil，不发射 step.finished

---

## 与 AG-UI 协议的兼容性

- `CUSTOM` 事件是 AG-UI 协议的一等公民，`ValidateSequence` 不对 CUSTOM 事件做内容验证
- `step.started` / `step.finished` 是自定义 name，不与协议内置事件冲突
- reduce 层的 `handleActivity` 已有 `CustomEvent` 处理分支
- 前端 AG-UI SDK 的 `EventDecoder` 已支持 `EventTypeCustom` 解码
