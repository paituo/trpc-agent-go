# tRPC-Agent-Go 工作空间、代码执行与沙箱：设计与实现文档

## 目录

1. [整体架构概览](#1-整体架构概览)
2. [工作空间 (Workspace)](#2-工作空间-workspace)
3. [代码执行 (CodeExecutor)](#3-代码执行-codeexecutor)
4. [沙箱 (Sandbox)](#4-沙箱-sandbox)
5. [三者交互流程](#5-三者交互流程)
6. [设计理念与核心抽象](#6-设计理念与核心抽象)
7. [使用指南](#7-使用指南)

---

## 1. 整体架构概览

tRPC-Agent-Go 是一个 Go 语言 AI Agent 框架，其核心执行链路为：

```
用户消息 → Runner → Session → Agent(LLMAgent) → Tool → CodeExecutor → Engine → Workspace/Sandbox
```

### 核心模块关系图

```
┌─────────────────────────────────────────────────────────┐
│                        Runner                           │
│  (编排执行管线，管理 Session/Memory/Agent 生命周期)        │
└──────────────┬──────────────────────────────────────────┘
               │
               ▼
┌──────────────────────────┐
│        Session           │
│  (会话状态、事件追踪)      │
└──────────────┬───────────┘
               │
               ▼
┌──────────────────────────┐      ┌──────────────────┐
│       LLMAgent           │─────▶│   Model (LLM)    │
│  (处理请求、调用工具)      │      │  (OpenAI等)       │
└──────────────┬───────────┘      └──────────────────┘
               │
               ▼
┌──────────────────────────┐
│         Tool             │
│  (execute_code /         │
│   workspace_exec /       │
│   skill_run 等)          │
└──────────────┬───────────┘
               │
               ▼
┌──────────────────────────────────────────────────────────┐
│                    CodeExecutor                          │
│  (代码执行统一接口，支持 local/container/e2b 三种后端)     │
└──────────────┬───────────────────────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────────────────────┐
│                      Engine                              │
│  (WorkspaceManager + WorkspaceFS + ProgramRunner)        │
│  ┌──────────────┐ ┌──────────────┐ ┌──────────────────┐ │
│  │ Manager      │ │     FS       │ │     Runner       │ │
│  │(创建/清理WS) │ │(文件读写/    │ │(程序执行)        │ │
│  │              │ │ 输入输出)    │ │                  │ │
│  └──────────────┘ └──────────────┘ └──────────────────┘ │
└──────────────┬───────────────────────────────────────────┘
               │
     ┌─────────┼─────────┐
     ▼         ▼         ▼
┌────────┐ ┌────────┐ ┌────────┐
│ Local  │ │Container│ │  E2B   │
│(本地)   │ │(Docker) │ │(云沙箱) │
└────────┘ └────────┘ └────────┘
```

### 关键包一览

| 包路径 | 职责 |
|--------|------|
| `codeexecutor` | 定义核心接口：CodeExecutor、Engine、WorkspaceManager、WorkspaceFS、ProgramRunner |
| `codeexecutor/local` | 本地执行后端：直接在宿主机上创建目录和执行命令 |
| `codeexecutor/container` | Docker 容器执行后端：在 Docker 容器内执行代码 |
| `codeexecutor/e2b` | E2B 云沙箱执行后端：在远程 E2B 沙箱中执行代码 |
| `codeexecutor/workspaceio` | Workspace 的 Go 层门面（facade），供 Agent 回调和自定义工具使用 |
| `codeexecutor/workspace_init` | 工作空间初始化钩子机制 |
| `internal/workspacesession` | 工作空间与 Session 的关联解析器 |
| `internal/workspaceprep` | 工作空间预处理协调器 |
| `internal/workspacefacade` | 路径验证、工件保存等辅助逻辑 |
| `tool/codeexec` | `execute_code` 工具：LLM 调用代码执行的入口 |
| `tool/workspaceexec` | `workspace_exec` 工具集：交互式 shell 命令执行 |

---

## 2. 工作空间 (Workspace)

### 2.1 概念与作用

**工作空间是一个隔离的执行环境**，它为每次代码执行提供一个独立的文件系统区域。工作空间的核心价值：

- **隔离性**：每次执行在独立目录中运行，互不干扰
- **生命周期管理**：创建 → 使用 → 清理，自动管理临时资源
- **标准化布局**：统一的目录结构，方便工具和技能共享文件
- **输入输出管理**：支持从多种来源（artifact、host、workspace、skill）导入数据，支持收集和持久化输出

### 2.2 核心类型

#### Workspace 结构体

```go
// codeexecutor/workspace.go
type Workspace struct {
    ID   string  // 逻辑标识符（通常是 session ID）
    Path string  // 宿主机路径（local）或逻辑挂载路径（container/e2b）
}
```

这是一个轻量级的值对象，仅包含 ID 和路径。实际功能由 Engine 的三个子接口提供。

#### WorkspacePolicy

```go
type WorkspacePolicy struct {
    Isolated     bool   // 是否启用运行时隔离（如容器）
    Persist      bool   // 是否在 Cleanup 后保留工作空间
    MaxDiskBytes int64  // 磁盘使用软限制
}
```

#### 工作空间标准目录布局

```
ws_<id>_<timestamp>/
├── skills/          # DirSkills - 技能工作副本
├── work/            # DirWork   - 可写共享中间文件
│   └── inputs/      #           - 自动映射的输入文件
├── runs/            # DirRuns   - 每次运行的独立目录
├── out/             # DirOut    - 收集的输出文件（用于工件化）
├── src/             # InlineSourceDir - 内联代码块执行目录
└── metadata.json    # 元数据文件（记录输入/输出历史）
```

每个子目录对应一个环境变量，在程序执行时自动注入：

| 目录 | 环境变量 | 用途 |
|------|----------|------|
| `skills/` | `SKILLS_DIR` | 技能脚本和资源 |
| `work/` | `WORK_DIR` | 可写中间文件 |
| `runs/` | `RUN_DIR` | 每次运行的独立目录 |
| `out/` | `OUTPUT_DIR` | 输出文件收集区 |
| 根目录 | `WORKSPACE_DIR` | 工作空间根路径 |

### 2.3 三大核心接口

工作空间的所有操作通过 Engine 暴露的三个子接口完成：

#### WorkspaceManager — 生命周期管理

```go
type WorkspaceManager interface {
    CreateWorkspace(ctx context.Context, execID string, pol WorkspacePolicy) (Workspace, error)
    Cleanup(ctx context.Context, ws Workspace) error
}
```

- `CreateWorkspace`：创建工作空间目录，调用 `EnsureLayout` 生成标准子目录，执行初始化钩子
- `Cleanup`：删除工作空间目录（`Persist=true` 或 `TrustedLocal` 模式下为 no-op）

#### WorkspaceFS — 文件系统操作

```go
type WorkspaceFS interface {
    PutFiles(ctx context.Context, ws Workspace, files []PutFile) error
    StageDirectory(ctx context.Context, ws Workspace, src, to string, opt StageOptions) error
    Collect(ctx context.Context, ws Workspace, patterns []string) ([]File, error)
    StageInputs(ctx context.Context, ws Workspace, specs []InputSpec) error
    CollectOutputs(ctx context.Context, ws Workspace, spec OutputSpec) (OutputManifest, error)
}
```

- `PutFiles`：批量写入文件
- `StageDirectory`：将宿主机目录复制/挂载到工作空间
- `Collect`：按 glob 模式收集工作空间中的文件
- `StageInputs`：声明式输入映射，支持四种协议前缀：
  - `artifact://` — 从工件服务加载
  - `host://` — 从宿主机文件系统加载
  - `workspace://` — 从工作空间内部复制
  - `skill://` — 从技能目录加载
- `CollectOutputs`：声明式输出收集，支持限制大小、持久化到工件服务

#### ProgramRunner — 程序执行

```go
type ProgramRunner interface {
    RunProgram(ctx context.Context, ws Workspace, spec RunProgramSpec) (RunResult, error)
}
```

`RunProgramSpec` 描述一次程序调用：

```go
type RunProgramSpec struct {
    Cmd     string            // 命令（如 "python3"、"bash"）
    Args    []string          // 命令参数
    Env     map[string]string // 额外环境变量
    CleanEnv bool             // 是否从空环境启动（安全特性）
    Cwd     string            // 工作目录（相对于工作空间根）
    Stdin   string            // 标准输入
    Timeout time.Duration     // 超时时间
    Limits  ResourceLimits    // 资源限制
}
```

`RunResult` 返回执行结果：

```go
type RunResult struct {
    Stdout   string
    Stderr   string
    ExitCode int
    Duration time.Duration
    TimedOut bool
}
```

### 2.4 WorkspaceRegistry — 工作空间复用

```go
// codeexecutor/registry.go
type WorkspaceRegistry struct {
    byID     map[string]Workspace
    inflight map[string]*workspaceCreateCall
}
```

**核心方法 `Acquire`**：创建或复用已有工作空间。

- 同一个 ID 的并发首次获取会合并为一次 `CreateWorkspace` 调用
- 已存在的工作空间直接返回，避免重复创建
- 这确保了同一 Session 内的多个工具调用共享同一个工作空间

### 2.5 工作空间初始化钩子

```go
type WorkspaceInitHook func(ctx context.Context, env WorkspaceInitEnv) error
```

钩子在 `CreateWorkspace` 成功后、返回给调用者之前执行。用途：

- 预置输入文件（`InputSpec`）
- 安装依赖（运行初始化命令）
- 确保工作空间在首次使用前处于正确状态

声明式定义方式：

```go
type WorkspaceInitSpec struct {
    Inputs  []InputSpec           // 先 stage 输入
    Commands []WorkspaceInitCommand // 再执行命令
}
```

通过 `NewWorkspaceInitExecutor` 包装原始 CodeExecutor，使每次 `CreateWorkspace` 自动运行钩子。

### 2.6 workspaceio — Go 层门面

`workspaceio.Workspace` 是面向业务代码的高级封装，隐藏了 Engine/Workspace 的绑定细节：

```go
// 从 Agent 回调或自定义工具中获取
ws := workspaceio.WorkspaceFromContext(ctx)

// 核心操作
ws.Collect(ctx, "out/*.json")           // 收集文件
ws.PutFiles(ctx, putFiles...)           // 写入文件
ws.SaveArtifact(ctx, "out/report.pdf")  // 持久化为工件
ws.StageInputs(ctx, inputSpecs)         // 导入输入
ws.RunProgram(ctx, runSpec)             // 执行程序
```

**设计要点**：
- 具体类型而非接口（因为后端变化在 codeexecutor 层，此层无需多态）
- 通过 `workspacesession.Resolver` 自动解析当前 Session 对应的工作空间
- 路径安全：`Cwd` 参数经过 `NormalizeWorkspaceCWD` 校验，防止路径逃逸

---

## 3. 代码执行 (CodeExecutor)

### 3.1 概念与作用

CodeExecutor 是代码执行的统一入口接口。它解决的核心问题：

- **统一 API**：无论底层是本地、Docker 还是 E2B 沙箱，上层调用方式一致
- **代码块解析**：从 LLM 输出中提取 Markdown 围栏代码块
- **多语言支持**：Python、Bash（E2B 额外支持 JS/TS/R/Java）

### 3.2 核心接口

```go
type CodeExecutor interface {
    ExecuteCode(ctx context.Context, input CodeExecutionInput) (CodeExecutionResult, error)
    CodeBlockDelimiter() CodeBlockDelimiter
}
```

**输入**：

```go
type CodeExecutionInput struct {
    CodeBlocks  []CodeBlock  // 代码块列表
    ExecutionID string       // 执行标识符
}

type CodeBlock struct {
    Code     string  // 代码内容
    Language string  // 语言标识（python/bash 等）
}
```

**输出**：

```go
type CodeExecutionResult struct {
    Output      string  // 合并的标准输出
    OutputFiles []File  // 执行产生的文件
}
```

### 3.3 代码块提取

`ExtractCodeBlock` 函数使用正则表达式从 Markdown 文本中提取围栏代码块：

```go
// 输入: "```python\nprint('hello')\n```"
// 输出: []CodeBlock{{Code: "print('hello')\n", Language: "python"}}
```

### 3.4 BuildBlockSpec — 代码块到执行命令的映射

```go
func BuildBlockSpec(idx int, b CodeBlock) (file string, mode uint32, cmd string, args []string, err error)
```

| 语言 | 文件名 | 模式 | 命令 |
|------|--------|------|------|
| python/py/python3 | `code_0.py` | 0o644 | `python3` |
| bash/sh | `code_0.sh` | 0o755 | `bash` |

### 3.5 三种执行后端

#### Local — 本地执行

**文件**：[local/local.go](codeexecutor/local/local.go)、[local/workspace_runtime.go](codeexecutor/local/workspace_runtime.go)

**特点**：
- 直接在宿主机文件系统上创建临时目录
- 通过 `os/exec` 调用解释器执行代码
- **无隔离**：代码可以访问宿主机的完整文件系统和网络
- 两种工作空间模式：
  - `WorkspaceModeIsolated`（默认）：每次创建唯一临时目录
  - `WorkspaceModeTrustedLocal`：复用指定目录，Cleanup 为 no-op

**执行流程**：
1. 创建临时目录（或使用 WorkDir）
2. 将代码写入临时文件（`code_0.py` / `code_0.sh`）
3. 构建命令参数（`python3 /tmp/code_0.py`）
4. 通过 `exec.CommandContext` 执行
5. 收集 stdout/stderr 并返回

**Engine 能力声明**：`SupportsCleanEnv: true`（本地运行时支持 `CleanEnv` 模式）

#### Container — Docker 容器执行

**文件**：[container/container.go](codeexecutor/container/container.go)

**特点**：
- 在 Docker 容器内执行代码
- 默认使用 `python:3.9-slim` 镜像
- 容器配置：
  - `AutoRemove: true`（停止后自动删除）
  - `Privileged: false`（非特权模式）
  - `NetworkMode: "none"`（无网络访问）
  - `tail -f /dev/null`（保持容器运行）
- 支持自定义 Dockerfile、绑定挂载、自定义容器配置

**执行流程**：
1. 初始化 Docker 客户端
2. 构建/拉取镜像
3. 创建并启动容器
4. 对每个代码块：通过 `ContainerExecCreate` + `ContainerExecAttach` 执行
5. 通过 `stdcopy` 分离 stdout/stderr
6. 合并输出并返回

**安全特性**：
- 无网络访问（`NetworkMode: "none"`）
- 非特权模式运行
- 文件系统隔离（容器内执行）

#### E2B — 云沙箱执行

**文件**：[e2b/e2b.go](codeexecutor/e2b/e2b.go)、[e2b/workspace_runtime.go](codeexecutor/e2b/workspace_runtime.go)、[e2b/internal/codeinterpreter/](codeexecutor/e2b/internal/codeinterpreter/)

**特点**：
- 在 E2B 云端沙箱中执行代码
- 通过 Jupyter Kernel Protocol 执行代码
- 支持更多语言：Python、JavaScript、TypeScript、Bash、R、Java
- 支持二进制输出（PNG/JPEG/PDF/SVG/LaTeX）
- 支持连接已有沙箱（`WithSandboxID`）
- 工作空间持久化模式：
  - `WorkspacePersistencePerTurn`（默认）：每轮创建新工作空间
  - `WorkspacePersistencePerSession`：同一 Session 复用工作空间

**执行流程**：
1. 通过 E2B API 创建沙箱（或连接已有沙箱）
2. 对每个代码块：调用 `sbx.RunCode` 通过 Jupyter API 执行
3. 流式接收 stdout/stderr 回调
4. 从执行结果中提取文本和二进制表示
5. 合并输出并返回

**E2B 沙箱内部架构**：

```
CodeExecutor
    │
    ├── Sandbox (ci.Sandbox)
    │   ├── 创建/连接/终止沙箱
    │   └── 通过 HTTP API 与 E2B 服务通信
    │
    ├── CodeInterpreter (ci.CodeInterpreter)
    │   ├── RunCode — 执行代码
    │   ├── CreateContext — 创建执行上下文
    │   └── 通过 Jupyter REST API 通信
    │
    └── workspaceRuntime
        ├── CreateWorkspace — 在沙箱内创建目录
        ├── PutFiles — 通过包装脚本写入文件
        ├── RunProgram — 通过包装脚本执行命令
        └── Collect — 通过包装脚本收集文件
```

### 3.6 Engine 能力声明

```go
type Capabilities struct {
    Isolation       string  // 隔离级别描述
    NetworkAllowed  bool    // 是否允许网络访问
    ReadOnlyMount   bool    // 是否支持只读挂载
    Streaming       bool    // 是否支持流式输出
    MaxDiskBytes    int64   // 最大磁盘使用
    SupportsCleanEnv bool   // 是否支持 CleanEnv（安全特性）
}
```

| 后端 | SupportsCleanEnv | 说明 |
|------|-------------------|------|
| Local | `true` | 本地运行时通过 `exec.Cmd.Env` 实现 |
| Container | `true` | 通过 `env -i` + `bash --noprofile --norc` 实现 |
| E2B | `false` | 尚未审计，fail-closed |

`SupportsCleanEnv` 是安全关键能力：当 `RunProgramSpec.CleanEnv=true` 时，子进程仅接收 `spec.Env` 中的环境变量，而非继承父进程的全部环境。`tool/workspaceexec` 的策略模式依赖此能力。

### 3.7 EngineProvider 接口

```go
type EngineProvider interface {
    Engine() Engine
}
```

CodeExecutor 的可选接口，暴露底层 Engine 供技能工具使用。Local、Container、E2B 三个后端都实现了此接口。

---

## 4. 沙箱 (Sandbox)

### 4.1 概念与定位

在 tRPC-Agent-Go 中，"沙箱"不是一个独立的模块，而是 **CodeExecutor 后端的一种实现方式**。具体来说：

- **Local 后端**：无沙箱，代码直接在宿主机运行
- **Container 后端**：Docker 容器即沙箱，提供文件系统和网络隔离
- **E2B 后端**：E2B 云沙箱，提供最强的隔离性和多语言支持

### 4.2 Docker 容器沙箱

**安全隔离机制**：

| 维度 | 机制 | 配置 |
|------|------|------|
| 文件系统 | 容器隔离 | 默认工作目录 `/` |
| 网络 | `NetworkMode: "none"` | 无网络访问 |
| 权限 | `Privileged: false` | 非特权模式 |
| 生命周期 | `AutoRemove: true` | 停止后自动删除 |
| 用户 | 容器默认用户 | 非 root 需自行配置 |

**工作空间在容器中的实现**：

```
workspaceRuntime
    │
    ├── CreateWorkspace → 在容器内创建目录（通过 docker exec）
    ├── PutFiles → 通过 docker cp + tar 写入文件
    ├── RunProgram → 通过 docker exec 执行命令
    └── Collect → 通过 docker cp 收集文件
```

### 4.3 E2B 云沙箱

**架构**：

```
┌──────────────────────┐     HTTP API      ┌──────────────────────┐
│   tRPC-Agent-Go      │ ────────────────▶ │   E2B Cloud          │
│   CodeExecutor       │                    │   Sandbox Service    │
│                      │ ◀──────────────── │                      │
└──────────────────────┘     JSON/SSE       └──────────┬───────────┘
                                                         │
                                                         ▼
                                              ┌──────────────────────┐
                                              │   Jupyter Kernel     │
                                              │   (代码执行)          │
                                              │                      │
                                              │   文件系统 (tmpfs)    │
                                              └──────────────────────┘
```

**Sandbox 核心方法**（[e2b/internal/codeinterpreter/sandbox.go](codeexecutor/e2b/internal/codeinterpreter/sandbox.go)）：

```go
// 创建新沙箱
func Create(ctx context.Context, opts *SandboxOpts) (*Sandbox, error)

// 连接已有沙箱
func Connect(ctx context.Context, sandboxID string, opts *SandboxOpts) (*Sandbox, error)

// 终止沙箱
func (s *Sandbox) Kill(ctx context.Context) error

// 获取沙箱 ID
func (s *Sandbox) SandboxID() string
```

**CodeInterpreter 核心方法**（[e2b/internal/codeinterpreter/code_interpreter.go](codeexecutor/e2b/internal/codeinterpreter/code_interpreter.go)）：

```go
// 执行代码
func (ci *CodeInterpreter) RunCode(ctx context.Context, code string, opts *RunCodeOpts) (*Execution, error)

// 创建执行上下文（隔离的命名空间）
func (ci *CodeInterpreter) CreateContext(ctx context.Context) (*Context, error)

// 列出所有上下文
func (ci *CodeInterpreter) ListContexts(ctx context.Context) ([]Context, error)
```

**安全隔离机制**：

| 维度 | 机制 |
|------|------|
| 文件系统 | 云端独立虚拟机，完全隔离 |
| 网络 | 沙箱内网络受限 |
| 进程 | 独立内核，进程隔离 |
| 资源 | E2B 平台限制 CPU/内存/磁盘 |
| 生命周期 | 可配置超时自动终止 |

**E2B 沙箱配置选项**：

```go
type SandboxOpts struct {
    APIKey         string            // E2B API 密钥
    AccessToken    string            // 访问令牌
    Domain         string            // 域名（默认 e2b.app）
    APIURL         string            // 自定义 API URL
    Debug          bool              // 调试模式（HTTP）
    RequestTimeout time.Duration     // HTTP 请求超时
    Timeout        time.Duration     // 沙箱生命周期
    Template       string            // 沙箱模板
    Metadata       map[string]string // 附加元数据
    EnvVars        map[string]string // 环境变量
    HTTPClient     *http.Client      // 自定义 HTTP 客户端
    Headers        map[string]string // 额外 HTTP 头
}
```

### 4.4 沙箱选择指南

| 场景 | 推荐后端 | 原因 |
|------|----------|------|
| 开发调试 | Local | 快速迭代，无需额外依赖 |
| 生产环境（低安全要求） | Container | Docker 隔离，本地部署 |
| 生产环境（高安全要求） | E2B | 云端隔离，最强安全性 |
| 需要多语言支持 | E2B | 支持 Python/JS/TS/R/Java/Bash |
| 需要二进制输出（图表等） | E2B | 原生支持 PNG/JPEG/PDF/SVG |
| 无 Docker 环境 | Local 或 E2B | Local 无需 Docker，E2B 是云服务 |

---

## 5. 三者交互流程

### 5.1 完整执行流程

以 LLM Agent 执行代码为例，完整流程如下：

```
1. 用户发送消息
   │
   ▼
2. Runner.Run() 创建/获取 Session
   │
   ▼
3. LLMAgent 处理消息，调用 LLM 模型
   │
   ▼
4. LLM 返回包含代码块的响应
   │
   ▼
5. LLMAgent 的 ResponseProcessor 提取代码块
   │
   ├─── 方式A: execute_code 工具
   │    │
   │    ▼
   │    tool/codeexec.NewTool(executor)
   │    │
   │    ▼
   │    executor.ExecuteCode(ctx, CodeExecutionInput{CodeBlocks: ...})
   │    │
   │    ▼
   │    ┌─ Local: 写临时文件 → exec.CommandContext → 返回输出
   │    ├─ Container: docker exec → 返回输出
   │    └─ E2B: sbx.RunCode → Jupyter API → 返回输出
   │
   ├─── 方式B: workspace_exec 工具（交互式）
   │    │
   │    ▼
   │    tool/workspaceexec.ExecTool
   │    │
   │    ▼
   │    workspacesession.Resolver.CreateWorkspace()
   │    │  → WorkspaceRegistry.Acquire(sessionID)
   │    │  → Engine.Manager().CreateWorkspace()
   │    │  → 运行 InitHooks
   │    │
   │    ▼
   │    Engine.Runner().RunProgram(ws, spec)
   │    │
   │    ▼
   │    返回 RunResult
   │
   └─── 方式C: skill_run 工具
        │
        ▼
        skill.NewRunTool(repo, localexec.New())
        │
        ▼
        在工作空间中执行 SKILL.md 定义的命令
```

### 5.2 工作空间生命周期

```
创建阶段:
  WorkspaceRegistry.Acquire(sessionID)
    │
    ├── 首次: Engine.Manager().CreateWorkspace()
    │         ├── 创建目录（ws_<id>_<timestamp>）
    │         ├── EnsureLayout() → 创建 skills/work/runs/out 子目录
    │         ├── 写入 metadata.json
    │         ├── StageInputs()（自动映射 host inputs）
    │         └── 运行 InitHooks
    │
    └── 已存在: 直接返回缓存的 Workspace

使用阶段:
  ├── PutFiles() → 写入代码/数据文件
  ├── RunProgram() → 执行命令
  ├── Collect() → 收集输出文件
  ├── SaveArtifact() → 持久化到工件服务
  └── StageInputs() → 导入外部输入

清理阶段:
  Engine.Manager().Cleanup(ws)
    ├── Isolated 模式: os.RemoveAll(ws.Path)
    └── TrustedLocal 模式: no-op（保留目录）
```

### 5.3 Session 与 Workspace 的绑定

```
Session (session-001)
    │
    ▼
WorkspaceRegistry
    │  byID["session-001"] → Workspace{ID: "session-001", Path: "/tmp/ws_session-001_1234567890"}
    │
    ▼
同一 Session 内的所有工具调用共享同一个 Workspace:
    ├── workspace_exec → 使用同一工作空间
    ├── skill_run → 使用同一工作空间
    └── workspaceio.Workspace → 使用同一工作空间
```

**关键实现**：`workspacesession.Resolver.CreateWorkspace` 从 context 中提取 `agent.Invocation`，获取 Session ID，然后通过 `WorkspaceRegistry.Acquire` 确保同一 Session 复用同一工作空间。

### 5.4 输入映射流程

```
InputSpec{From: "artifact://report.pdf", To: "work/inputs/report.pdf", Mode: "copy"}
    │
    ▼
StageInputs()
    ├── artifact:// → 从 ArtifactService 加载 → 写入工作空间
    ├── host:// → 从宿主机复制或符号链接
    ├── workspace:// → 从工作空间内部复制或符号链接
    └── skill:// → 从技能目录复制或符号链接
    │
    ▼
记录到 metadata.json 的 Inputs 数组
```

### 5.5 输出收集流程

```
OutputSpec{Globs: ["out/*.pdf"], Save: true, MaxFileBytes: 4MB}
    │
    ▼
CollectOutputs()
    ├── NormalizeGlobs() → 将 $OUTPUT_DIR/*.pdf 转换为 out/*.pdf
    ├── Glob 匹配文件
    ├── 读取文件内容（受 MaxFileBytes/MaxTotalBytes 限制）
    ├── Save=true → 调用 ArtifactService 持久化
    └── 记录到 metadata.json 的 Outputs 数组
    │
    ▼
返回 OutputManifest{Files: [...], LimitsHit: false}
```

---

## 6. 设计理念与核心抽象

### 6.1 接口分层 — 关注点分离

```
Layer 1: CodeExecutor (业务接口)
    │  "执行这些代码块，返回结果"
    │
Layer 2: Engine (能力接口)
    │  "提供工作空间管理 + 文件操作 + 程序执行"
    │
Layer 3: Backend (实现层)
    │  Local / Container / E2B
    │  "具体怎么做"
```

- **CodeExecutor** 面向 LLM 工具，关注"执行代码"
- **Engine** 面向技能和高级工具，关注"工作空间操作"
- **Backend** 面向基础设施，关注"如何实现隔离"

### 6.2 值对象 vs 行为对象

- `codeexecutor.Workspace` 是**值对象**（仅 ID + Path），不可变
- `workspaceio.Workspace` 是**行为对象**（封装 Engine + Resolver），可操作

这种分离使得：
- 值对象可以安全地在并发环境中传递
- 行为对象隐藏了 Engine 绑定和 Session 解析的复杂性

### 6.3 声明式输入/输出

`InputSpec` 和 `OutputSpec` 采用声明式设计：

```go
// 声明"我需要什么"，而非"怎么做"
InputSpec{
    From: "artifact://report.pdf",
    To:   "work/inputs/report.pdf",
    Mode: "copy",  // 或 "link"
    Pin:  true,    // 固定版本
}
```

好处：
- 后端无关：同一 spec 可在 local/container/e2b 上运行
- 可审计：所有输入/输出记录在 metadata.json
- 可复现：Pin 版本确保确定性

### 6.4 Fail-Closed 安全策略

`Capabilities.SupportsCleanEnv` 采用 fail-closed 设计：

- 默认值为 `false`（不信任）
- 只有经过审计的后端才声明为 `true`
- 依赖此能力的工具（如 workspaceexec 策略模式）在 `false` 时拒绝运行
- 避免了"静默降级安全策略"的风险

### 6.5 WorkspaceRegistry 的并发安全

```go
func (r *WorkspaceRegistry) Acquire(ctx, m, id) (Workspace, error) {
    // 1. 已存在 → 直接返回
    // 2. 正在创建 → 等待完成（coalescing）
    // 3. 首次创建 → 启动 goroutine，其他等待者共享结果
}
```

这确保了：
- 同一 Session 的并发工具调用不会重复创建工作空间
- InitHooks 只运行一次
- 避免竞态条件

### 6.6 装饰器模式 — WorkspaceInitExecutor

```go
// 原始 CodeExecutor
exec := local.New(...)

// 包装为带初始化钩子的 CodeExecutor
wrapped, _ := codeexecutor.NewWorkspaceInitExecutor(exec,
    codeexecutor.NewWorkspaceInitHook(spec),
)
```

`workspaceInitExecutor` 委托所有方法给原始执行器，仅在 `CreateWorkspace` 时额外运行钩子。这是经典的装饰器模式，不修改原始实现即可扩展行为。

---

## 7. 使用指南

### 7.1 基本代码执行

```go
// 创建本地执行器
exec := local.New(
    local.WithWorkDir("/tmp/agent"),
    local.WithTimeout(30*time.Second),
)

// 创建工具并注册到 Agent
codeTool := codeexec.NewTool(exec,
    codeexec.WithName("execute_code"),
    codeexec.WithLanguages("python", "bash"),
)

agent := llmagent.New("assistant",
    llmagent.WithModel(model),
    llmagent.WithTools([]tool.Tool{codeTool}),
)
```

### 7.2 使用 Docker 容器执行

```go
exec, _ := container.New(
    container.WithContainerConfig(container.Config{
        Image: "python:3.11-slim",
    }),
    container.WithHostConfig(container.HostConfig{
        NetworkMode: "none",
    }),
)

agent := llmagent.New("assistant",
    llmagent.WithModel(model),
    llmagent.WithCodeExecutor(exec),
)
```

### 7.3 使用 E2B 云沙箱

```go
exec, _ := e2b.New(
    e2b.WithAPIKey("your-e2b-api-key"),
    e2b.WithTemplate("code-interpreter-v1"),
    e2b.WithExecutionTimeout(60*time.Second),
    e2b.WithWorkspacePersistence(e2b.WorkspacePersistencePerSession),
)
defer exec.Close()

agent := llmagent.New("assistant",
    llmagent.WithModel(model),
    llmagent.WithCodeExecutor(exec),
)
```

### 7.4 在 Agent 回调中操作工作空间

```go
// 从 context 获取工作空间
ws := workspaceio.WorkspaceFromContext(ctx)

// 读取文件
files, _ := ws.Collect(ctx, "out/*.json")

// 写入文件
ws.PutFiles(ctx, codeexecutor.PutFile{
    Path:    "work/input.txt",
    Content: []byte("hello"),
    Mode:    codeexecutor.DefaultScriptFileMode,
})

// 执行命令
result, _ := ws.RunProgram(ctx, codeexecutor.RunProgramSpec{
    Cmd:     "python3",
    Args:    []string{"script.py"},
    Cwd:     "work",
    Timeout: 30 * time.Second,
})

// 持久化输出为工件
ref, _ := ws.SaveArtifact(ctx, "out/report.pdf")
```

### 7.5 使用工作空间初始化钩子

```go
spec := codeexecutor.WorkspaceInitSpec{
    Inputs: []codeexecutor.InputSpec{
        {From: "artifact://dataset.csv", To: "work/data.csv", Mode: "copy"},
    },
    Commands: []codeexecutor.WorkspaceInitCommand{
        {Name: "install-deps", Cmd: "pip", Args: []string{"install", "pandas"}},
    },
}

wrappedExec, _ := codeexecutor.NewWorkspaceInitExecutor(
    baseExec,
    codeexecutor.NewWorkspaceInitHook(spec),
)
```

### 7.6 使用 Skill 工具

```go
repo, _ := skill.NewFSRepository("./skills")

tools := []tool.Tool{
    skilltool.NewLoadTool(repo),
    skilltool.NewRunTool(repo, localexec.New()),
}

agent := llmagent.New("assistant",
    llmagent.WithModel(model),
    llmagent.WithTools(tools),
    llmagent.WithCodeExecutor(localexec.New()),
    // 禁用自动代码执行，仅通过 skill_run 执行
    llmagent.WithEnableCodeExecutionResponseProcessor(false),
)
```

### 7.7 配置建议

| 配置项 | 开发环境 | 生产环境 |
|--------|----------|----------|
| 执行后端 | Local | Container 或 E2B |
| 工作空间模式 | TrustedLocal | Isolated |
| CleanEnv | 开启 | 开启 |
| 网络访问 | 按需 | 默认关闭 |
| 超时 | 30s | 按业务调整 |
| 工件持久化 | 关闭 | 开启 |
| 工作空间持久化 | PerTurn | PerSession（多轮对话） |

---

## 附录：关键文件索引

| 文件 | 核心内容 |
|------|----------|
| [codeexecutor/codeexecutor.go](codeexecutor/codeexecutor.go) | CodeExecutor 接口、CodeBlock、CodeExecutionInput/Result |
| [codeexecutor/workspace.go](codeexecutor/workspace.go) | Workspace、Engine、WorkspaceManager/FS/Runner 接口 |
| [codeexecutor/registry.go](codeexecutor/registry.go) | WorkspaceRegistry 工作空间复用 |
| [codeexecutor/workspace_init.go](codeexecutor/workspace_init.go) | 初始化钩子机制 |
| [codeexecutor/metadata.go](codeexecutor/metadata.go) | 目录布局常量、元数据管理 |
| [codeexecutor/local/local.go](codeexecutor/local/local.go) | 本地 CodeExecutor |
| [codeexecutor/local/workspace_runtime.go](codeexecutor/local/workspace_runtime.go) | 本地 Runtime（Manager+FS+Runner 实现） |
| [codeexecutor/container/container.go](codeexecutor/container/container.go) | Docker 容器 CodeExecutor |
| [codeexecutor/e2b/e2b.go](codeexecutor/e2b/e2b.go) | E2B 云沙箱 CodeExecutor |
| [codeexecutor/e2b/workspace_runtime.go](codeexecutor/e2b/workspace_runtime.go) | E2B 工作空间运行时 |
| [codeexecutor/e2b/internal/codeinterpreter/sandbox.go](codeexecutor/e2b/internal/codeinterpreter/sandbox.go) | E2B 沙箱生命周期管理 |
| [codeexecutor/e2b/internal/codeinterpreter/code_interpreter.go](codeexecutor/e2b/internal/codeinterpreter/code_interpreter.go) | E2B 代码解释器 |
| [codeexecutor/workspaceio/workspace_io.go](codeexecutor/workspaceio/workspace_io.go) | Go 层工作空间门面 |
| [internal/workspacesession/resolver.go](internal/workspacesession/resolver.go) | Session-Workspace 绑定解析 |
| [tool/codeexec/codeexec.go](tool/codeexec/codeexec.go) | execute_code 工具 |
| [tool/workspaceexec/workspace_exec.go](tool/workspaceexec/workspace_exec.go) | workspace_exec 工具集 |
