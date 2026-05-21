# OpenClaw exec_command 机制完整分析与 Windows 支持方案

> **说明**：本文档分为"业务说明"和"技术实现"两大部分。
> - **业务说明**（第一~三章）：面向产品/业务人员。
> - **技术实现**（第四~九章）：面向开发人员。

---

# 第一部分：业务说明

---

## 一、exec_command 是什么？

`exec_command` 是 OpenClaw 智能体平台中让 AI 助手**在用户主机上执行命令行操作**的核心工具。当用户让 AI 操作文件、运行脚本、管理系统时，AI 通过 exec_command 在用户机器上实际执行命令。

### 1.1 典型使用场景

| 场景 | 示例命令 | 说明 |
|------|----------|------|
| 文件操作 | `ls -la`, `cp`, `mv`, `find` | 浏览、复制、移动文件 |
| 数据处理 | `grep`, `awk`, `sed`, `sort` | 文本和数据处理 |
| 系统管理 | `ps`, `top`, `df -h` | 查看系统状态 |
| 脚本执行 | `python script.py` | 运行自定义脚本 |

### 1.2 当前状态简述

exec_command 目前主要针对 Linux/macOS 设计。核心问题：
- 多处代码硬编码使用 `bash`/`sh` 作为命令解释器
- PTY 终端在 Windows 上直接返回"不支持"错误
- 进程树管理使用 Unix 进程组信号，Windows 上仅杀了主进程

### 1.3 改造业务价值

- **跨平台覆盖**：大量企业办公环境使用 Windows
- **功能对齐**：Windows 用户体验与 Linux/macOS 一致

---

## 二、两级命令体系

```
用户 / AI 助手
  │
  ├── exec_command (hostexec)  →  直接在主机执行命令
  │   工具集: exec_command / write_stdin / kill_session
  │
  └── workspace_exec (workspaceexec)  →  在隔离工作空间执行
      工具集: workspace_exec / workspace_write_stdin / workspace_kill_session
```

---

# 第二部分：技术实现

---

## 三、完整源码架构

### 3.1 文件结构

> **注意**：以下路径基于方案设计时的代码布局。实际代码库中对应路径为
> `openclaw/internal/octool/`（而非 `tool/hostexec/`），进程管理逻辑内联
> 在 `manager.go` 中（无独立 `procgroup_*.go` 文件）。
> 以下按实际代码布局描述。

```
openclaw/internal/octool/
├── hostexec.go           # 工具集声明、工具定义、参数解析 (tools.go)
├── manager.go            # 命令执行核心：shell选择、前后台、进程管理（内联）
├── session.go            # 会话输出缓冲、poll/write/kill/trim
├── pty_unix.go           # Unix PTY（creack/pty） //go:build !windows
└── pty_windows.go        # Windows PTY: 返回"not supported"错误 //go:build windows

codeexecutor/local/
├── local.go              # CodeExecutor，硬编码 bash/python3
├── workspace_runtime.go  # workspace RunProgram + Collect
├── workspace_runtime_interactive.go  # 交互式会话 + PTY
├── pty_unix.go           # Unix PTY //go:build !windows
└── pty_windows.go        # Windows PTY: 返回"not supported" //go:build windows

codeexecutor/
└── workspace.go          # BuildBlockSpec 硬编码 bash
```

### 3.2 核心调用链

```
exec_command Tool.Call(ctx, args)
  │
  ├─ JSON 反序列化 → execInput{Command, Workdir, Env, Pty, Background, YieldMs, TimeoutS}
  ├─ resolveWorkdir() → 解析工作目录
  │
  └─ manager.exec(ctx, execParams{...})
        │
        ├─ 运行模式判断:
        │   ├─ 非后台 && yieldMs==0 && !Pty → runForeground() → 阻塞等完成
        │   └─ 其他 → startBackground() → 可能返回 session_id
        │
        └─ Shell 构建 → shellCmd() → shellSpec()
              ├─ Windows: cmd.exe /d /s /c <命令>
              ├─ Unix: bash -lc <命令>
              └─ 回退: sh -lc <命令>
```

**Process 启动 (startSession)**:

```
startSession(id, params, timeout, baseEnv, maxLines)
  │
  ├─ shellCmd() → 创建 *exec.Cmd
  ├─ cmd.Dir = workdir
  ├─ cmd.Env = merged(baseEnv, userEnv, os.Environ())
  │
  ├─ Pty=true → startPTY(cmd)
  │   ├─ Unix:   pty.Start(cmd) + Setsid + Pdeathsig
  │   └─ Windows: 返回 error "not supported"
  │
  └─ Pty=false → startPipes(cmd)
      ├─ stdin/stdout/stderr 三条管道
      ├─ Unix:   Setpgid + Pdeathsig → cmd.Start()
      ├─ Windows: 直接 cmd.Start()
      │
      ├─ 2个读 goroutine (stdout + stderr) → session.appendOutput()
      ├─ 1个等待 goroutine:
      │     cmd.Process.Wait()
      │     waitDone(ioDone, 1s drain)
      │     terminateProcessTree()  → Unix: SIGTERM→SIGKILL进程组
      │                            → Windows: process.Kill() 仅主进程
      │     session.markDone(code)
      │     cancel()
      └─ 1个超时 goroutine: timeout → sess.kill()
```

**会话输出管理 (session)**:

```
session
├── 输出存储: lines[] (环形缓冲, maxLines=20000) + partial (未完成行)
├── readFrom(reader) → bufio逐行读取 → appendOutput()
│   ├── \r\n → \n 统一
│   ├── split by \n → 完整行追加到 lines
│   └── trimLocked(): 超过 maxLines 丢弃旧行
├── poll(limit) → processPoll{Status, Output, Offset, NextOffset, ExitCode}
├── pollTail(n) → 尾N行
├── allOutput() → 全部输出 + exitCode
└── write(data, newline) → stdin 写入
```

---

## 四、平台差异详细分析

### 4.1 已存在的平台适配

| 文件 | Build Tag | 作用 |
|------|-----------|------|
| `openclaw/internal/octool/pty_unix.go` | `!windows` | creack/pty.Start() |
| `openclaw/internal/octool/pty_windows.go` | `windows` | error("not supported") |
| `codeexecutor/local/pty_unix.go` | `!windows` | PTY 创建 |
| `codeexecutor/local/pty_windows.go` | `windows` | error("not supported") |
| `openclaw/internal/octool/session.go` | (无标签) | `runtime.GOOS != "windows"` 判断 SIGTERM vs Kill |

> **注意**：当前源码中**没有** `procgroup_*.go` 文件。进程树的创建和终止逻辑直接写在 `manager.go:startBackground()` 和 `session.go:kill()` 中。`startBackground()` 使用 `cmd.Process.Wait()` + `defaultIODrain` + `markDone` 管理生命周期；`session.kill()` 通过 `runtime.GOOS` 分支决定是否发送 SIGTERM。Phase 2 实施时需要决定是在现有函数内扩展还是重构为独立的 `procgroup_windows.go` 文件。

### 4.2 manager.go:shellCmd() 硬编码 bash

> **勘误**：方案此前引用 `shellSpec()` 函数，但实际代码中使用的是 `shellCmd()`（定义在 `openclaw/internal/octool/manager.go:L257-264`）。该函数使用包级常量 `shellProgram = "bash"` 和 `shellLoginFlag = "-lc"`，完全没有平台判断。

```go
// 实际代码（openclaw/internal/octool/manager.go）：
const (
    shellProgram        = "bash"
    shellLoginFlag      = "-lc"
    shellEnvDumpCommand = "env -0"
)

func shellCmd(ctx context.Context, command string) *exec.Cmd {
    return exec.CommandContext(
        ctx,
        shellProgram,
        shellLoginFlag,
        command,
    )
}
```

**这说明 octool 的 exec_command 在 Windows 上完全不行！** 以下位置硬编码。

### 4.3 仍然硬编码的位置

| 位置 | 硬编码 | 函数 |
|------|--------|------|
| `openclaw/internal/octool/manager.go:L37-40` | `shellProgram = "bash"`, `shellLoginFlag = "-lc"` | `shellCmd()` |
| `codeexecutor/local/local.go:L276` | `[]string{"bash", filePath}` | `buildCommandArgs()` |
| `codeexecutor/workspace.go:L229` | `"bash", nil, nil` | `BuildBlockSpec()` |

### 4.4 进程树管理对比

> **勘误**：当前源码中不存在独立的 `procgroup_*.go` 文件。以下对比基于 Phase 2 计划实现的功能描述。

| 功能 | Unix（`session.go:kill()` 当前逻辑） | Windows（当前） | Windows（Phase 2 目标） |
|------|--------------------------------------|-----------------|------------------------|
| 进程树终止 | SIGTERM → 等 grace → SIGKILL | `cmd.Process.Kill()` 仅主进程 | Job Object 进程树统一终止 |
| 存活检查 | 依赖 `syscall.SIGTERM` 信号 | `process.Signal(0)` → 可能无效 | Job Object 状态查询 |
| 父进程死亡 | `Pdeathsig=SIGTERM` (Linux) | 无此机制 | `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` |

**关键风险（Phase 2）**：Go 的 `exec.Cmd.Start()` 不支持 `CREATE_SUSPENDED` 标志，`cmd.Start()` 后立即 `AssignProcessToJobObject()` 存在竞态窗口——子进程可能在此窗口内创建孙进程，孙进程不属于 Job Object。缓解方案：
- **A）接受限制**：Phase 1 不实现 Job Object，维持当前 `process.Kill()` 行为。
- **B）syscall.CreateProcess**（推荐 Phase 2 实现）：绕过 Go 标准库，直接调用 Windows API，使用 `CREATE_SUSPENDED` 启动进程 → 加入 Job Object → 恢复执行。代码复杂度增加，但消除竞态。

### 4.5 PTY 对比

| Unix | Windows |
|------|---------|
| `openclaw/internal/octool/pty_unix.go`: `creack/pty.Start(cmd)` | `openclaw/internal/octool/pty_windows.go`: `errors.New("pty is not supported on windows")` |
| `codeexecutor/local/pty_unix.go`: PTY 正常工作 | `codeexecutor/local/pty_windows.go`: 同样返回不支持 |

### 4.6 交互式 Kill 对比

`codeexecutor/local/workspace_runtime_interactive.go:L218-219`:
```go
if runtime.GOOS != "windows" {
    _ = cmd.Process.Signal(syscall.SIGTERM)  // 先优雅终止
}
// Windows 跳过 SIGTERM，直接等 Kill
```

### 4.7 WSL/bash.exe 陷阱 —— 真实错误案例分析

#### 4.7.1 真实错误现场

来自实际运行中 Windows 环境的报错：

```json
{
  "status": "exited",
  "output": "1u\u0000N*g[ň@b\u0007\u0004vyr'`\u000c\u0005e\u0006l/T\u0013R\u0004d\\O\u00020 \n\u0019\ufffd\ufffd\ufffd\ufffdN\u0001x: Bash/Service/CreateInstance/CreateVm/HCS/HCS_E_SERVICE_NOT_AVAILABLE",
  "exit_code": 1
}
```

#### 4.7.2 错误解读

输出分为两个部分：

| 部分 | 内容 | 原因 |
|------|------|------|
| 前半乱码 | `1uN*g[ň@b...` | WSL bash.exe 的 stderr 使用系统代码页（如 GBK）输出，`session.readFrom()` 按 UTF-8 解释 |
| 后半可读 | `Bash/Service/CreateInstance/CreateVm/HCS/HCS_E_SERVICE_NOT_AVAILABLE` | ASCII 范围内字节能正确显示 |

#### 4.7.3 错误链条完整还原

```
① manager.go:L37-40 → 包级常量 shellProgram = "bash", shellLoginFlag = "-lc"
    ↓
② shellCmd() 调用 exec.CommandContext(ctx, "bash", "-lc", "...")
    ↓
③ Go runtime 在 PATH 中搜索 "bash" → 找到 C:\Windows\System32\bash.exe
    （WSL 安装后会自动注册 bash.exe 和 sh.exe 到 System32）
    ↓
④ bash.exe 启动 → 尝试创建 WSL Linux 虚拟机实例
    ↓
⑤ WSL 依赖 HCS (Host Compute Service) → 但 HCS 服务未运行
    ↓
⑥ bash.exe 失败 → 向 stderr 输出错误（使用系统代码页编码）
    ↓
⑦ session.go:readFrom() 按原始字节读取 → \r\n→\n 转换
    → 但**不做编码转换** → 非 ASCII 字节变成乱码
    ↓
⑧ 返回给 AI: exitCode=1, output=乱码+可读错误路径
```

#### 4.7.4 根因定位

| 层次 | 根因 | 解释 |
|------|------|------|
| 直接原因 | `exec.LookPath("bash")` 在 Windows 上找到了 WSL 的 bash | WSL 安装后 `C:\Windows\System32\bash.exe` 可被 Go 的 `exec.LookPath` 发现 |
| 深层原因 | `shellCmd()` 硬编码 `shellProgram = "bash"` 没有任何平台判断，也从不调用 `LookPath` | `shellCmd()` 直接使用包级常量，Windows 代码路径也未做平台适配 |
| 表象原因 | `readFrom()` 未做编码转换 | 非 UTF-8 字节流直接当 UTF-8 解读 |

#### 4.7.5 Windows 上 WSL 的三个关键事实

| 事实 | 细节 | 影响 |
|------|------|------|
| WSL 的 bash 在 System32 | `C:\Windows\System32\bash.exe` 是 WSL 入口 | `exec.LookPath("bash")` **总是能找到它**（如果 WSL 已安装） |
| HCS 可能未运行 | 需要 Hyper-V 平台、HCS 服务、虚拟机管理服务 | 即使是已安装 WSL 的机器，HCS 也可能因各种原因不可用 |
| 非 WSL 路径不可用 | `bash.exe` 无法脱离 WSL 独立运行 | 没有 WSL 的 Windows 机器上 `exec.LookPath("bash")` 返回 error，但这是好事 |

#### 4.7.6 编码乱码的机制

```
WSL bash.exe 的错误输出过程：

bash.exe (WSL 入口，Windows 原生进程)
  │
  ├─ 尝试创建 WSL 实例 → 失败
  │
  └─ fprintf(stderr, "Bash/Service/CreateInstance/...HCS_E_SERVICE_NOT_AVAILABLE")
       │
       └─ Windows 控制台宿主进程 (conhost.exe)
            │
            └─ 使用当前控制台代码页 (如 CP936=GBK)
                 对宽字符进行编码 → 输出字节流
                 │
                 └─ session.readFrom() 按 UTF-8 解读字节流
                      │
                      └─ 中文字符/特殊字符 → 乱码
                         ASCII 范围内字符 → 正常显示
```

**中文系统典型代码页**：

| 代码页 | 编码 | 触发条件 |
|--------|------|----------|
| 936 | GBK/GB2312 | 简体中文 Windows 默认 |
| 950 | Big5 | 繁体中文 Windows 默认 |
| 65001 | UTF-8 | 手动设置 `chcp 65001` 后 |

---

## 五、Windows 支持问题汇总

| 序号 | 问题 | 位置 | 严重度 | 现象 |
|------|------|------|--------|------|
| P1 | Manager 硬编码 `bash` | `openclaw/internal/octool/manager.go:L37-40` | **致命** | exec_command 调用 WSL bash → HCS 不可用则崩溃 |
| P2 | CodeExecutor 硬编码 `bash` | `codeexecutor/local/local.go:L276` | **致命** | code_exec 调用 WSL bash → 同上 |
| P3 | BuildBlockSpec 硬编码 `bash` | `codeexecutor/workspace.go:L229` | **致命** | ExecuteInline 调用 WSL bash → 同上 |
| P4 | PTY 不支持 | `pty_windows.go`（两处） | 高 | 无交互式命令 |
| P5 | 进程树不完整终止 | `session.go:kill()` + `manager.go:startBackground()` | 中 | 子进程泄露 |
| P6 | 无优雅终止信号 | `workspace_runtime_interactive.go:L218` | 中 | 强制 Kill 可能丢数据 |
| P7 | 编码问题（GBK→UTF-8） | `session.go:readFrom()` | **高** | **已在实际运行中确认**：中文 Windows 输出乱码 |
| P8 | WSL bash/sh 被误判为可用 Shell | `shellCmd()` 无平台判断 | **致命** | `shellProgram = "bash"` 在 Windows 上直接调用 WSL 入口 |
| P9 | Git Bash 的 bash.exe 也不应使用 | `shellCmd()` 无平台判断 | **高** | Git Bash 的 bash 与 WSL bash 类似，不应作为通用 Shell |

> **P7 严重度提升说明**：此前标记为"低"，但在实际运行中确认：即使 WSL bash 错误消息的英文部分可读，但命令正常输出的中文内容（如 `dir` 的文件名、`type` 的文件内容等）会完全乱码，严重影响 AI 对命令结果的解读。因此提升为**高**。同时 P7 也应前移到 Phase 1 执行。

---

## 六、Windows 支持设计方案

### 6.1 设计原则

1. **不改 Unix 端**：所有改动仅通过 `//go:build` 标签隔离
2. **最小依赖**：优先使用标准库 + `golang.org/x/sys/windows`（已有）+ `golang.org/x/text`（新增）
3. **功能对齐**：Windows 实现功能与 Unix 一致
4. **渐进实施**：分阶段实现，每个阶段可独立交付
5. **保守默认**：默认行为安全可预期，高级选项可由用户配置

### 6.2 新增平台抽象包

新建 `internal/platform/` 包，统一管理跨平台差异：

```
internal/platform/
├── platform.go              # 公共接口（无 build tag）
├── platform_unix.go         //go:build !windows
├── platform_windows.go      //go:build windows
├── platform_test.go         # 跨平台单元测试
└── platform_windows_test.go //go:build windows — Windows 特定测试
```

**公共接口**：

```go
package platform

import "context"

// ShellSpec 描述平台 Shell 的命令和参数。
type ShellSpec struct {
    Command string   // 如 "bash" 或 "cmd.exe"
    Args    []string // 如 ["-lc"] 或 ["/d", "/s", "/c"]
}

// Shell returns the OS-appropriate shell specification.
func Shell() (ShellSpec, error) { ... }

// BuildCommand builds the OS command that runs userCommand through the shell.
func BuildCommand(ctx context.Context, userCommand string) (cmd string, args []string, err error) { ... }
```

**Unix 实现** (`platform_unix.go`):

```go
//go:build !windows
package platform

import (
    "context"
    "errors"
    "os/exec"
)

func Shell() (ShellSpec, error) {
    if path, err := exec.LookPath("bash"); err == nil {
        return ShellSpec{Command: path, Args: []string{"-lc"}}, nil
    }
    if path, err := exec.LookPath("sh"); err == nil {
        return ShellSpec{Command: path, Args: []string{"-lc"}}, nil
    }
    return ShellSpec{}, errors.New("bash or sh is required")
}

func BuildCommand(_ context.Context, cmd string) (string, []string, error) {
    s, err := Shell()
    if err != nil {
        return "", nil, err
    }
    return s.Command, append(s.Args, cmd), nil
}
```

**Windows 实现** (`platform_windows.go`):

> **用户决策（v3）**：默认使用 PowerShell（功能最强），cmd.exe 作为回退。
> 理由：PowerShell 提供更丰富的脚本能力、对象管道、更好的错误处理，
> 与 AI 生成的命令语义匹配度更高。变量展开/管道的差异在实践中可通过
> `-NoProfile -NonInteractive -Command` 参数消解。

```go
//go:build windows
package platform

import (
    "context"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
)

// Shell returns the OS-appropriate shell for Windows.
//
// Design decisions (v3, user decision):
//   - PowerShell is the default: richer scripting, object pipeline, better error handling
//   - cmd.exe is the fallback when PowerShell is unavailable
//   - WSL bash/sh are ALWAYS excluded (requires HCS, unreliable)
//   - Git Bash/Cygwin/MSYS2 bash are ALWAYS excluded (path/encoding incompatibility)
//
// WSL bash appears at C:\Windows\System32\bash.exe and can be found by
// exec.LookPath("bash"), but it requires HCS (Host Compute Service) to
// be running. If HCS is down, bash.exe fails with:
//   "Bash/Service/CreateInstance/CreateVm/HCS/HCS_E_SERVICE_NOT_AVAILABLE"
func Shell() (ShellSpec, error) {
    // 1. PowerShell (pwsh.exe on new systems, powershell.exe on older)
    for _, name := range []string{"powershell.exe", "pwsh.exe"} {
        if p, err := exec.LookPath(name); err == nil {
            if !isNonNativeShell(p) {
                return ShellSpec{
                    Command: p,
                    Args:    []string{"-NoProfile", "-NonInteractive", "-Command"},
                }, nil
            }
        }
    }
    // 2. cmd.exe — always available, last resort
    if p, err := exec.LookPath("cmd.exe"); err == nil {
        if !isNonNativeShell(p) {
            return ShellSpec{
                Command: p,
                Args:    []string{"/d", "/s", "/c"},
            }, nil
        }
    }
    return ShellSpec{}, fmt.Errorf("no usable shell found on Windows")
}

// isNonNativeShell detects whether the given executable path belongs to
// a non-native shell environment (WSL, Git Bash, Cygwin, MSYS2).
//
// These shells should NOT be used as a general-purpose command executor:
//   - WSL bash: requires Hyper-V Host Compute Service (HCS)
//   - Git Bash (MinGW/MSYS2): uses Unix-style paths incompatible with
//     Windows-native tools
//   - Cygwin: similar path compatibility issues
func isNonNativeShell(p string) bool {
    p = strings.ToLower(filepath.Clean(p))
    sysRoot := strings.ToLower(os.Getenv("SystemRoot"))
    if sysRoot == "" {
        sysRoot = `c:\windows`
    }

    // WSL bash.exe / sh.exe in System32 or SysWOW64
    wslBashSystem32 := filepath.Join(sysRoot, "system32", "bash.exe")
    wslBashSysWOW64 := filepath.Join(sysRoot, "syswow64", "bash.exe")
    if p == wslBashSystem32 || p == wslBashSysWOW64 {
        return true
    }

    // WSL internal paths
    if strings.Contains(p, "lxss") {
        return true
    }

    // Git Bash / MSYS2 / Cygwin — detect by well-known install paths
    nonNativeShellSuffixes := []string{
        // Git for Windows
        `\git\bin\bash.exe`,
        `\git\usr\bin\bash.exe`,
        // MSYS2
        `\msys64\usr\bin\bash.exe`,
        // Cygwin
        `\cygwin64\bin\bash.exe`,
        `\cygwin\bin\bash.exe`,
    }
    for _, suffix := range nonNativeShellSuffixes {
        if strings.HasSuffix(p, suffix) {
            return true
        }
    }

    return false
}

func BuildCommand(_ context.Context, cmd string) (string, []string, error) {
    s, err := Shell()
    if err != nil {
        return "", nil, err
    }
    return s.Command, append(s.Args, cmd), nil
}
```

### 6.3 各硬编码位置的修改

**P1: `openclaw/internal/octool/manager.go:L37-40` (shellCmd)**:

```go
// 旧代码（包级常量）:
const (
    shellProgram   = "bash"
    shellLoginFlag = "-lc"
)

func shellCmd(ctx context.Context, command string) *exec.Cmd {
    return exec.CommandContext(ctx, shellProgram, shellLoginFlag, command)
}

// 新代码:
func shellCmd(ctx context.Context, command string) *exec.Cmd {
    s, err := platform.Shell()
    if err != nil {
        // 回退到 bash（Unix 兼容路径）
        return exec.CommandContext(ctx, "bash", "-lc", command)
    }
    return exec.CommandContext(ctx, s.Command, append(s.Args, command)...)
}
```

> **注意**：还需要修改 `manager.go` 中 `snapshotLoginShellEnv()` 函数。它使用 `shellCmd(ctx, shellEnvDumpCommand)` 来获取 shell 环境变量快照。Windows 上 `cmd.exe /d /s /c "env -0"` 无效，需要适配：
> - **cmd.exe**：`echo %PATH%` + `echo %TEMP%` 等逐变量获取
> - **或跳过 login shell env snapshot**：Windows 上直接使用 `os.Environ()`

**P2: `codeexecutor/local/local.go:L276`**:

```go
// 旧代码:
case "bash", "sh":
    return []string{"bash", filePath}

// 新代码:
case "bash", "sh":
    shell, err := platform.Shell()
    if err != nil {
        return nil  // 由上层处理
    }
    return append(shell.Args, filePath)
```

**P3: `codeexecutor/workspace.go:L229`**:

```go
// 旧代码:
case "bash", "sh":
    return fmt.Sprintf("code_%d.sh", idx), DefaultExecFileMode, "bash", nil, nil

// 新代码:
case "bash", "sh":
    shell, err := platform.Shell()
    if err != nil {
        return "", 0, "", nil, err
    }
    return fmt.Sprintf("code_%d.sh", idx), DefaultExecFileMode, shell.Command, shell.Args, nil
```

### 6.4 进程树管理：Job Object 机制

**原理**：Windows 的 Job Object 可以跟踪和管理一组进程。设置 `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` 标志后，关闭 Job Object Handle 时自动终止所有关联进程。

**核心函数**（`procgroup_windows.go` 重写）：

```go
//go:build windows
package hostexec

import (
    "os"
    "os/exec"
    "syscall"
)

// createJobObject 创建 Windows Job Object。
// 设置 JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE 确保关闭 Handle 时终止所有进程。
func createJobObject() (syscall.Handle, error) {
    // 调用 kernel32!CreateJobObjectW
    // 调用 kernel32!SetInformationJobObject（JobObjectExtendedLimitInformation）
    // LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
}

// assignProcessToJobObject 将进程加入 Job Object。
func assignProcessToJobObject(job syscall.Handle, proc *os.Process) error {
    // 调用 kernel32!AssignProcessToJobObject
}

// terminateProcessTree 终止进程树。
// Windows: 先发 CTRL_BREAK_EVENT → 等 grace → TerminateJobObject(KILL)
func terminateProcessTree(ctx context.Context, proc *os.Process, pgid int, grace time.Duration) error {
    if proc == nil {
        return nil
    }
    // 尝试优雅终止
    proc.Signal(os.Interrupt)
    if waitProcessExit(proc, grace) {
        return nil
    }
    // 通过 Job Object 强制终止（包括所有子进程）
    return killProcess(proc)
}
```

**关键点**：
- `cmd.Start()` 之后立即 `assignProcessToJobObject(job, cmd.Process)`
- Job Object Handle 在 session 生命周期内保持打开
- session 销毁时 CloseHandle → 操作系统自动终止所有关联进程

**竞态风险与缓解**：

Go 的 `exec.Cmd.Start()` 不支持 `CREATE_SUSPENDED` 标志。在 `cmd.Start()` 和 `AssignProcessToJobObject()` 之间存在窗口：子进程可能在此窗口内创建孙进程，孙进程不在 Job Object 中。

| 方案 | 复杂度 | 安全性 | 推荐 |
|------|--------|--------|------|
| A) 接受窗口：`cmd.Start()` → 立即 `AssignProcessToJobObject()` | 低 | 中等（通常窗口够小） | Phase 2 初始实现 |
| B) `syscall.CreateProcess` + `CREATE_SUSPENDED`：绕过 Go 标准库 | 高（需要手动管理 stdin/stdout/stderr 管道） | 高 | Phase 2 最终方案 |

**建议**：Phase 2 先用方案 A 实现，记录已知局限；如需更高可靠性再升级到方案 B。

### 6.5 PTY 支持：ConPTY

**方案选择**：

| 库 | 维护者 | 接口复杂度 | 推荐 |
|----|-------|-----------|------|
| `github.com/admpub/conpty` v0.2.4 | admpub（UserExistsError fork，2025.9 更新) | 低 — `conpty.Start(cmdLine, opts)` → 一个 `ConPty` 对象 | **推荐** |
| `tailscale.com/util/winutil/conpty` | Tailscale（v1.98.2，2026.5 更新） | 中 — `NewPseudoConsole(size)` → `ConfigureStartupInfo(sib)` | 备选 |
| 直接调用 `kernel32!CreatePseudoConsole` | — | 高 — 需要手动调用 6+ 个 API | 仅学习参考 |

**需要修改的文件**：
1. `openclaw/internal/octool/pty_windows.go` + `codeexecutor/local/pty_windows.go`：重写 `startPTY()` 使用 ConPTY
2. `codeexecutor/local/workspace_runtime_interactive.go:L400`：移除 Windows TTY 拒绝 `if runtime.GOOS == "windows"` 逻辑

```go
//go:build windows
package octool

func startPTY(cmd *exec.Cmd) (*os.File, func() error, error) {
    // 降级检查：ConPTY 仅在 Windows 10 1809+ 可用
    if !conpty.IsConPtyAvailable() {
        return nil, nil, errors.New("conpty is not available on this Windows version")
    }
    cpty, err := conpty.Start(cmd.Path, conpty.ConPtyDimensions(80, 40))
    if err != nil {
        return nil, nil, err
    }
    return cpty, cpty.Close, nil
}
```

### 6.6 编码处理

**问题**：中文 Windows 控制台默认代码页是 GBK(936)，`exec.Command` 输出是原始字节。

**真实案例**：在实际运行中，WSL bash.exe 的错误输出被 `session.readFrom()` 按 UTF-8 直接解读，导致前半部分（非 ASCII 字节）变成乱码，只有 ASCII 范围内的 `Bash/Service/CreateInstance/CreateVm/HCS/HCS_E_SERVICE_NOT_AVAILABLE` 可读。即使修复了 Shell 选择问题，中文 Windows 上 `cmd.exe` 和 `powershell.exe` 的 `dir`、`type` 等命令输出中文文件名或内容时，同样会遇到此问题。

**解决优先级调整**：此问题之前被标记为 P7（低优先级），但实际运行确认：
- 即使命令成功执行，中文输出也会乱码
- AI 无法正确解析乱码输出，影响任务执行质量
- 应提升到 **Phase 1** 实施

**方案**：在 `session.readFrom()` 中增加代码页检测和编码转换。

**新增文件 `openclaw/internal/octool/codepage_windows.go`**：

> **v2 优化**：使用 `golang.org/x/text/encoding/simplifiedchinese`（相比 `charmap`，simplifiedchinese 包
> 对中文编码有更专门的优化处理）。编码对象缓存到 session 初始化时，避免每行输出都做 syscall。

```go
//go:build windows

package octool

import (
    "sync"
    "syscall"

    "golang.org/x/text/encoding"
    "golang.org/x/text/encoding/simplifiedchinese"
    "golang.org/x/text/encoding/unicode"
    "golang.org/x/text/transform"
)

var (
    kernel32GetConsoleOutputCP = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleOutputCP")
    kernel32GetACP             = syscall.NewLazyDLL("kernel32.dll").NewProc("GetACP")

    cachedEncoding     encoding.Encoding
    cachedEncodingOnce sync.Once
)

// getConsoleEncoding returns the encoding for the current console output.
// Result is cached via sync.Once — code page doesn't change during process lifetime.
// On Chinese Windows this is typically GBK (code page 936).
func getConsoleEncoding() encoding.Encoding {
    cachedEncodingOnce.Do(func() {
        cachedEncoding = resolveConsoleEncoding()
    })
    return cachedEncoding
}

func resolveConsoleEncoding() encoding.Encoding {
    cp := getConsoleCodePage()
    switch cp {
    case 65001: // UTF-8 — 无需转换
        return encoding.Nop
    case 936: // GBK / GB2312 — 简体中文
        return simplifiedchinese.GBK
    case 950: // Big5 — 繁体中文
        return simplifiedchinese.HZGB2312 // 繁体用传统中文包（需额外import）
    case 1200: // UTF-16 LE
        return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)
    default:
        return encoding.Nop // 未知代码页，UTF-8 直读
    }
}

func getConsoleCodePage() uint32 {
    // 先尝试控制台输出代码页
    ret, _, _ := kernel32GetConsoleOutputCP.Call()
    if ret == 0 {
        // 回退到系统 ANSI 代码页（更可靠）
        ret, _, _ = kernel32GetACP.Call()
    }
    if ret == 0 {
        return 65001 // 默认 UTF-8
    }
    return uint32(ret)
}

// decodeConsoleOutput converts console-encoded bytes to UTF-8.
func decodeConsoleOutput(input []byte) string {
    enc := getConsoleEncoding()
    if enc == encoding.Nop {
        return string(input)
    }
    decoder := enc.NewDecoder()
    result, _, err := transform.Bytes(decoder, input)
    if err != nil {
        return string(input) // 转换失败保留原始字节，比丢弃好
    }
    return string(result)
}
```

**修改 `session.go:readFrom()`** — 与旧方案相同（`chunk = []byte(decodeConsoleOutput(chunk))` 包裹在 `runtime.GOOS == "windows"` 分支中）。

**关键设计决策**：

| 决策 | 选择 | 理由 |
|------|------|------|
| 转换时机 | 每行读取后立即转换 | 避免缓冲整个输出再转换，内存友好 |
| 转换位置 | `readFrom()` 中 | 在数据进入 `appendOutput()` 之前转换，下游代码无感知 |
| 转换失败处理 | 保留原始字节 | 比丢弃数据更安全，AI 可能从上下文推断含义 |
| 依赖包 | `golang.org/x/text/encoding/simplifiedchinese` | Go 官方维护，3300+ 项目验证 |
| 编码检测性能 | `sync.Once` 缓存编码对象 | syscall 只需一次，后续调用零开销 |
| 代码页回退 | `GetConsoleOutputCP` → `GetACP` | 子进程无控制台时 GetConsoleOutputCP 可能返回 0，GetACP 更可靠 |

**效果对比**：

| 场景 | 转换前 | 转换后 |
|------|--------|--------|
| `dir` 输出中文文件名 | `???_????.md` (乱码) | `电力设计说明书.md` (正常) |
| WSL bash 错误 | `1uN*g[ň@b...HCS_E_SERVICE_NOT_AVAILABLE` | `错误: ...HCS_E_SERVICE_NOT_AVAILABLE` |
| `type 文件.txt` 含中文内容 | 整行乱码 | 正常中文显示 |

### 6.7 交互式 Kill 优化

```go
func (s *interactiveSession) Kill(grace time.Duration) error {
    ...
    if runtime.GOOS == "windows" {
        // 发送 Ctrl+C 模拟优雅终止
        cmd.Process.Signal(os.Interrupt)
    } else {
        cmd.Process.Signal(syscall.SIGTERM)
    }
    // 等待 grace 时间
    select {
    case <-s.doneCh: return nil
    case <-time.After(grace):
    }
    // 强制终止
    cmd.Process.Kill()
    ...
}
```

---

## 七、实施计划

### 7.0 前置条件：测试环境准备

**测试发现**：当前所有与 exec_command 相关的测试都以 `exec.LookPath("bash")` 为前置条件，
在 Windows 上全部跳过（`t.Skip`）。Phase 1 必须解决测试覆盖问题。

| 测试文件 | 当前行为 | Phase 1 改进 |
|----------|---------|-------------|
| `openclaw/internal/octool/tools_test.go` | `t.Skip("bash is not available")` | 改为 `platform.Shell().Command` 执行测试命令 |
| `codeexecutor/local/local_test.go` | bash 硬编码编写/测试 | 适配 `platform.Shell()` 返回的 cmd.exe |
| `codeexecutor/local/workspace_runtime_test.go` | bash 硬编码 | 同上 |

### Phase 1：Shell 抽象层 + WSL/GitBash 排除 + 编码处理（基础功能，必须）

| 步骤 | 文件 | 操作 | 对应问题 |
|------|------|------|----------|
| 1 | 新建 `internal/platform/platform.go` | 定义 `ShellSpec`、`Shell()`、`BuildCommand()` 接口 | — |
| 2 | 新建 `internal/platform/platform_unix.go` | 实现 Unix Shell()：bash → sh 回退 | — |
| 3 | 新建 `internal/platform/platform_windows.go` | 实现 Windows：cmd.exe 默认 + `isNonNativeShell()` 排除 WSL/GitBash/Cygwin | **P8**、**P9** |
| 4 | 新建 `internal/platform/platform_test.go` | 跨平台单元测试：Shell() 返回合法 spec | — |
| 5 | 新建 `internal/platform/platform_windows_test.go` | Windows 特有：WSL 路径检测、Git Bash 排除、cmd.exe 可用性 | — |
| 6 | 修改 `openclaw/internal/octool/manager.go:L37-40` | `shellCmd()` 调用 `platform.BuildCommand()` 替代 `shellProgram`/`shellLoginFlag` 常量；适配 `snapshotLoginShellEnv()` | **P1** |
| 7 | 修改 `codeexecutor/local/local.go:L276` | `buildCommandArgs()` 调用 `platform.Shell()` | **P2** |
| 8 | 修改 `codeexecutor/workspace.go:L229` | `BuildBlockSpec()` 调用 `platform.Shell()` | **P3** |
| 9 | 新建 `openclaw/internal/octool/codepage_windows.go` | `sync.Once` 缓存编码对象 + `GetConsoleOutputCP`→`GetACP` 回退 | **P7** |
| 10 | 修改 `openclaw/internal/octool/session.go:readFrom()` | `runtime.GOOS == "windows"` 分支调用 `decodeConsoleOutput()` | **P7** |
| 11 | 修改 `openclaw/internal/octool/tools_test.go` | 测试适配：使用 `platform.Shell()` 替代 `exec.LookPath("bash")` | 测试 |
| 12 | 修改 `codeexecutor/local/local_test.go` | 测试适配：bash 硬编码 → 平台 Shell | 测试 |
| 13 | 修改 `codeexecutor/local/workspace_runtime_test.go` | 测试适配：bash 硬编码 → 平台 Shell | 测试 |

**验收标准**：
- `exec_command`、`workspace_exec`、`code_exec` 在 Windows 上可执行基本命令
- **不会**调用 WSL 的 bash/sh（即使 WSL 已安装）
- **不会**调用 Git Bash/Cygwin/MSYS2 的 bash
- 中文输出（`dir` 文件名、`type` 文件内容等）正确显示
- 现有 Linux/macOS 测试全部通过
- Windows 平台测试全部通过

### Phase 2：进程树管理（稳定性）

| 步骤 | 文件 | 操作 |
|------|------|------|
| 1 | 新建 `openclaw/internal/octool/procgroup_windows.go` 或扩展 `manager.go` | 实现 Job Object 进程树管理（方案 A：`cmd.Start()` 后立即 `AssignProcessToJobObject`） |
| 2 | 修改 `manager.go:startBackground()` | 集成 Job Object |
| 3 | 修改 `workspace_runtime_interactive.go` | Windows 优雅 Kill（`os.Interrupt` → grace → Kill） |
| 4 | 评估方案 B（`syscall.CreateProcess + CREATE_SUSPENDED`）可行性 | 决定是否升级到方案 B |

**验收标准**：kill_session 可终止全部子进程，无泄露（已知局限：方案 A 有极窄窗口的孙进程泄露可能）。

### Phase 3：PTY 支持（可选）

| 步骤 | 文件 | 操作 |
|------|------|------|
| 1 | 集成 conpty 库 | `go get github.com/admpub/conpty` |
| 2 | 重写 `pty_windows.go`（两处） | ConPTY 实现，`IsConPtyAvailable()` 版本检测 |
| 3 | 修改 `workspace_runtime_interactive.go` | 移除 Windows TTY 拒绝 |

**验收标准**：tty=true 的 exec_command 在 Windows 10 1809+ 上可用。

---

## 八、依赖项与兼容性

### 8.1 依赖项

| 包 | 用途 | Phase | 必要性 | 外部验证 |
|----|------|-------|--------|---------|
| `golang.org/x/text/encoding/simplifiedchinese` | GBK→UTF-8 编码转换 | Phase 1 | **必须** | Go 官方维护，3300+ 项目使用 |
| `golang.org/x/sys/windows` → `AssignProcessToJobObject` | Job Object 进程树管理 | Phase 2 | **必须** | Go 官方维护，已在项目依赖中 |
| `github.com/admpub/conpty` (v0.2.4+) | Windows ConPTY API | Phase 3 | 可选 | admpub fork 持续维护（2025.9） |

### 8.2 兼容性注意事项

| 项目 | 最低要求 | 说明 |
|------|----------|------|
| Windows 版本 | Windows 10 1809+ | ConPTY 需要 |
| Go 版本 | 1.21+ | `syscall` 支持 |
| 编译架构 | amd64 / arm64 | 均支持 |
| 管理员权限 | 非必需 | symlink 等操作需要时提示 |
| WSL 共存 | 不影响 | `isWSLPath()` 确保不选择 WSL bash，不影响 WSL 的正常独立使用 |

### 8.3 安全注意事项

| 风险 | 说明 | 缓解措施 |
|------|------|----------|
| 命令注入 | cmd.exe 的 `/s` 引号处理可能绕过 | 保持 `/d /s /c` 参数组合 |
| Job Object 权限 | 某些进程（如系统服务）无法加入 Job | 优雅降级到 direct Kill |
| PowerShell 脚本执行策略 | Restricted 策略阻止脚本运行 | 使用 `-Command` 而非脚本文件 |
| ConPTY 兼容性 | 仅 Windows 10 1809+ 支持 | 检测版本，降级到 Pipe |

---

## 九、Shell 选择优先级（v2 修订）

> **变更（v2）**：经评审，默认 Shell 从 PowerShell 改为 cmd.exe。
> PowerShell 保留作为可配置选项，但不由 `Shell()` 函数自动优先选择。

| 优先级 | Shell | 参数 | 说明 |
|--------|-------|------|------|
| 1 | **cmd.exe** | `/d /s /c` | 所有 Windows 都有，行为可预期，命令语义与用户一致 |
| — | powershell.exe | `-NoProfile -NonInteractive -Command` | 未来可选升级（需用户配置开关） |
| — | pwsh.exe | `-NoProfile -NonInteractive -Command` | 跨平台 PS（需用户配置开关） |
| **永远排除** | **WSL bash.exe** | — | 依赖 HCS 服务，不可靠 |
| **永远排除** | **WSL sh.exe** | — | 同上 |
| **永远排除** | **Git Bash bash.exe** | — | Unix 路径不兼容 Windows 原生工具 |
| **永远排除** | **Cygwin/MSYS2 bash** | — | 同上 |

### `isNonNativeShell()` 排除逻辑（v2 扩展）

`isNonNativeShell()` 函数的多层检测策略：

| 检测层 | 方法 | 覆盖场景 |
|--------|------|----------|
| 路径匹配 | `%SystemRoot%\System32\bash.exe` | WSL1/WSL2 标准安装 |
| 路径匹配 | `%SystemRoot%\SysWOW64\bash.exe` | 32位进程中的 WSL 入口 |
| 路径包含 | `strings.Contains(p, "lxss")` | WSL 内部文件系统路径 |
| 路径后缀 | `*\git\bin\bash.exe` | Git for Windows |
| 路径后缀 | `*\msys64\usr\bin\bash.exe` | MSYS2 |
| 路径后缀 | `*\cygwin64\bin\bash.exe` | Cygwin（新增） |

> **设计说明**：不再单独判断"WSL 是否安装"，因为即使 WSL 未安装，
> System32\bash.exe 也可能"可执行但不返回任何内容"。排除策略基于路径特征，
> 独立于 WSL 安装状态。

注意：`isNonNativeShell()` 当前仅 `Shell()` 在查找 Shell 时调用。在 `Shell()` 中，我们使用 `exec.LookPath("cmd.exe")` 查找（而非 `exec.LookPath("bash")`），因此正常情况下不会触发 WSL/Git Bash 路径。但 `isNonNativeShell()` 作为防御层，防止未来因配置或其他代码路径传入 `bash` 查找。

---

## 十、关键文件索引（v2 修正）

| 文件 | 当前状态 | 修改类型 | Phase |
|------|----------|----------|-------|
| `openclaw/internal/octool/manager.go` | 硬编码 `shellProgram="bash"` | 轻改（使用 platform.BuildCommand） | P1 |
| `openclaw/internal/octool/session.go` | 输出缓冲，无编码转换 | 轻改（编码转换） | P1 |
| `openclaw/internal/octool/codepage_windows.go` | **不存在** | **新增**（sync.Once 缓存编码） | P1 |
| `openclaw/internal/octool/pty_windows.go` | 返回 "not supported" | **重写**（ConPTY） | P3 |
| `openclaw/internal/octool/pty_unix.go` | 正常（creack/pty） | 不改 | — |
| `codeexecutor/local/local.go` | 硬编码 bash | 轻改（platform.Shell） | P1 |
| `codeexecutor/local/workspace_runtime_interactive.go` | 拒绝 Windows TTY | 轻改 | P3 |
| `codeexecutor/local/pty_windows.go` | 返回不支持 | **重写**（ConPTY） | P3 |
| `codeexecutor/workspace.go` | BuildBlockSpec 硬编码 bash | 轻改（platform.Shell） | P1 |
| `internal/platform/platform.go` | **不存在** | **新增** | P1 |
| `internal/platform/platform_unix.go` | **不存在** | **新增** | P1 |
| `internal/platform/platform_windows.go` | **不存在** | **新增** | P1 |
| `internal/platform/platform_test.go` | **不存在** | **新增** | P1 |
| `internal/platform/platform_windows_test.go` | **不存在** | **新增** | P1 |
| `openclaw/internal/octool/tools_test.go` | bash 测试全部跳过 | 修改（适配平台 Shell） | P1 |
| `codeexecutor/local/local_test.go` | bash 硬编码 | 修改（适配平台 Shell） | P1 |
| `codeexecutor/local/workspace_runtime_test.go` | bash 硬编码 | 修改（适配平台 Shell） | P1 |

---

## 十一、AutoPlan 综合评审报告

> 评审日期：2026-05-27
> 已运行评审：CEO ✓ / 设计 ✗（无 UI 组件）/ 工程 ✓
> 总体结论：**有条件通过**

### 11.1 CEO 评审结论

| 维度 | 评估 |
|------|------|
| 痛点具体性 | 已有真实错误现场（WSL HCS 崩溃、中文 GBK 乱码） |
| 用户画像 | 企业 Windows 办公用户（运维、开发、数据处理） |
| 切入点 | Shell 抽象 + 编码转换 = 基础可用（80/20 原则） |
| 竞争 | 无竞品在 AI 智能体领域关注此问题 |
| 10 星愿景 | Windows 体验与 Linux 完全对等 |

### 11.2 工程评审结论

**风险等级：中等**

**架构**：`internal/platform/` 包设计合理（8/10）。`//go:build` 标签隔离 Unix 不变，接口抽象简洁。

**测试覆盖**：**当前为 0（Windows 上全被跳过）**——这是最大风险。Phase 1 必须新增 2 个测试文件 + 修改 3 个现有测试文件。

**边界情况**：
- cmd.exe `/d /s /c` 对带空格的路径处理与 Unix shell 不同
- PowerShell 变量展开/管道语法差异（已通过 cmd.exe 默认规避）
- Job Object `cmd.Start()` 竞态窗口（Phase 2 记录已知局限）

### 11.3 整合行动项

**阻塞项（开始实施前）：**

| # | 项目 | 状态 |
|---|------|------|
| 1 | 路径勘误（`tool/hostexec/` → `openclaw/internal/octool/`） | ✅ v2 已修正 |
| 2 | Shell 优先级改为 cmd.exe 默认 | ✅ v2 已修正 |
| 3 | 扩展 WSL/Git Bash/Cygwin 排除 | ✅ v2 已修正 |
| 4 | 确认 `snapshotLoginShellEnv()` 在 Windows 上的行为（cmd.exe 不支持 `env -0`） | ⚠ 待解决 |

**本周期（Phase 1 必须做）：**

| # | 项目 |
|---|------|
| 1 | 实现 `internal/platform/` 包 + 4 个文件（platform.go, _unix.go, _windows.go, _test.go, _windows_test.go） |
| 2 | 实现 `codepage_windows.go`（sync.Once 缓存 + simplifiedchinese 编码） |
| 3 | 修改 3 个硬编码位置（manager.go, local.go, workspace.go） |
| 4 | 修改 3 个测试文件适配平台 Shell |
| 5 | `snapshotLoginShellEnv()` 在 Windows 上使用 `os.Environ()` 跳过 shell env dump |

**后续（Phase 2+）：**

| # | 项目 |
|---|------|
| 1 | Job Object 进程树管理（先方案 A，再评估方案 B） |
| 2 | ConPTY 交互式命令（`github.com/admpub/conpty`） |

### 11.4 v3 变更清单（用户决策）

| 决策 | 选择 | 影响 |
|------|------|------|
| Shell 默认选择 | **PowerShell 优先**，cmd.exe 回退 | 功能最强，与 AI 生成的复杂命令语义匹配 |
| `snapshotLoginShellEnv()` 在 Windows 上 | 直接使用 `os.Environ()` | 简化实现，跳过 shell env dump |

### 11.5 历史变更记录

| 版本 | 关键变更 |
|------|---------|
| v1（原方案） | PS 优先 → cmd 回退，`isWSLPath`，`charmap`，`tool/hostexec/` 路径 |
| v2（评审修正） | cmd 默认（后撤销）、扩展 Git Bash/Cygwin 排除、`simplifiedchinese.GBK`、`sync.Once` 缓存、路径勘误、测试计划 |
| v3（用户决策） | **PS 优先**（恢复）、`os.Environ()` 替代 shell env dump