# exec_command Windows 支持 Phase 2 — 进程树管理实施计划

> **对于代理工作器：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实施此计划。

**目标：** 在 Windows 上实现完整进程树终止——`kill_session` 可以终止主进程及其所有子进程，无残留。

**架构：** 在 `startBackground()` 中，`cmd.Start()` 后立即创建 Windows Job Object 并附加进程（方案 A）。`session.kill()` 和 `interactiveSession.Kill()` 利用 `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` 在 Job Object 句柄关闭时由操作系统自动清理进程树。新建 `openclaw/internal/octool/procgroup_windows.go` 封装 Job Object 操作。

**技术栈：** Go 1.21+, `golang.org/x/sys/windows`（已在项目依赖中），`//go:build windows`

**前置参考：** `docs/exec_command_windows_support_analysis.md`（v3，Phase 2 章节）

---

## 文件结构

```
新建：
  openclaw/internal/octool/procgroup_windows.go       — Job Object 创建/附加/关闭
  openclaw/internal/octool/procgroup_windows_test.go  — Job Object 单元测试

修改：
  openclaw/internal/octool/manager.go                 — startBackground() 集成 Job Object
  openclaw/internal/octool/session.go                 — session 结构体 + kill() 方法
  codeexecutor/local/workspace_runtime_interactive.go — Kill() 方法 Windows 分支
```

---

## 关键设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| Job Object 创建时机 | `cmd.Start()` 之后立即 | Go 的 `exec.Cmd` 不支持 `CREATE_SUSPENDED`，先启动再附加 |
| 竞态窗口接受度 | **接受方案 A**：`cmd.Start()` → `AssignProcessToJobObject` 之间有微秒级窗口 | 方案 B（`syscall.CreateProcess`）需手动管理管道，复杂度远高于收益 |
| Job Object 句柄生命周期 | 存储在 `session` 结构体中，`markDone` 时关闭 | 确保进程树与 session 生命周期绑定 |
| 终止策略 | `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`：关闭 Job Object 句柄时系统自动终止所有进程 | 无需手动枚举子进程，操作系统保证原子性 |
| Kill 流程 | Unix 不变；Windows：不可发送 SIGTERM → 直接 `CloseHandle` + grace 等待 | Job Object 关闭是强制终止，等价于 SIGKILL |
| `workspace_runtime_interactive.go` | 同样集成 Job Object，Kill 时关闭 Job Object | 与 octool 保持一致 |

---

### 任务 1: 创建 `procgroup_windows.go` — Job Object 封装

**文件：**
- 创建：`openclaw/internal/octool/procgroup_windows.go`

- [ ] **步骤 1：创建 procgroup_windows.go**

```go
//go:build windows

package octool

import (
    "errors"
    "fmt"
    "os"
    "unsafe"

    "golang.org/x/sys/windows"
)

var kernel32 = windows.NewLazySystemDLL("kernel32.dll")

var (
    procCreateJobObjectW        = kernel32.NewProc("CreateJobObjectW")
    procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
    procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
)

const (
    // jobObjectLimitKillOnJobClose causes all processes in the job to be
    // terminated when the last handle to the job object is closed.
    jobObjectLimitKillOnJobClose = 0x2000

    // jobObjectExtendedLimitInformation is the information class for
    // JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
    jobObjectExtendedLimitInformation = 9
)

// jobObject wraps a Windows Job Object handle.
// It must be closed by calling Close() to release the handle and
// trigger KILL_ON_JOB_CLOSE (if enabled).
type jobObject struct {
    handle windows.Handle
}

// newJobObject creates a new unnamed Job Object.
func newJobObject() (*jobObject, error) {
    name, _ := windows.UTF16PtrFromString("")
    h, _, err := procCreateJobObjectW.Call(
        0, // lpJobAttributes = NULL (default security)
        uintptr(unsafe.Pointer(name)),
    )
    if h == 0 {
        return nil, fmt.Errorf("CreateJobObjectW failed: %w", err)
    }
    return &jobObject{handle: windows.Handle(h)}, nil
}

// enableKillOnClose sets JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE.
// When the last handle to this job object is closed, all processes
// associated with the job will be terminated.
func (j *jobObject) enableKillOnClose() error {
    type jobObjectExtendedLimitInfo struct {
        basicLimit   windows.JOBOBJECT_BASIC_LIMIT_INFORMATION
        ioInfo       windows.IO_COUNTERS
        processMem   windows.SIZE_T
        jobMem       windows.SIZE_T
        peakProcess  windows.SIZE_T
        peakJob      windows.SIZE_T
    }

    var info jobObjectExtendedLimitInfo
    info.basicLimit.LimitFlags = jobObjectLimitKillOnJobClose

    ret, _, err := procSetInformationJobObject.Call(
        uintptr(j.handle),
        jobObjectExtendedLimitInformation,
        uintptr(unsafe.Pointer(&info)),
        unsafe.Sizeof(info),
    )
    if ret == 0 {
        return fmt.Errorf("SetInformationJobObject(KILL_ON_CLOSE) failed: %w", err)
    }
    return nil
}

// assignProcess assigns the given OS process to this Job Object.
// The process must be running (already started).
func (j *jobObject) assignProcess(p *os.Process) error {
    ret, _, err := procAssignProcessToJobObject.Call(
        uintptr(j.handle),
        uintptr(p.Pid),
    )
    if ret == 0 {
        return fmt.Errorf("AssignProcessToJobObject(pid=%d) failed: %w", p.Pid, err)
    }
    return nil
}

// close releases the Job Object handle. If KILL_ON_JOB_CLOSE was set,
// this will cause all associated processes to be terminated.
func (j *jobObject) close() error {
    if j.handle == 0 {
        return nil
    }
    err := windows.CloseHandle(j.handle)
    j.handle = 0
    if err != nil {
        return fmt.Errorf("CloseHandle(JobObject) failed: %w", err)
    }
    return nil
}
```

- [ ] **步骤 2：验证编译通过**

```powershell
cd d:\GoProjects\trpc-agent-go\openclaw; go build ./internal/octool/...
```

期望：编译通过

- [ ] **步骤 3：提交**

```powershell
git add openclaw/internal/octool/procgroup_windows.go; git commit -m "feat(octool): add Windows Job Object wrapper for process tree management"
```

---

### 任务 2: 创建 `procgroup_windows_test.go` — Job Object 测试

**文件：**
- 创建：`openclaw/internal/octool/procgroup_windows_test.go`

- [ ] **步骤 1：创建测试文件**

```go
//go:build windows

package octool

import (
    "os"
    "os/exec"
    "testing"
    "time"
)

func TestJobObject_CreateAndClose(t *testing.T) {
    j, err := newJobObject()
    if err != nil {
        t.Fatalf("newJobObject() failed: %v", err)
    }
    if err := j.close(); err != nil {
        t.Fatalf("close() failed: %v", err)
    }
}

func TestJobObject_AssignProcess(t *testing.T) {
    j, err := newJobObject()
    if err != nil {
        t.Fatalf("newJobObject() failed: %v", err)
    }
    defer func() { _ = j.close() }()

    cmd := exec.Command("cmd.exe", "/d", "/c", "exit 0")
    if err := cmd.Start(); err != nil {
        t.Skipf("cannot start cmd.exe: %v", err)
    }

    if err := j.assignProcess(cmd.Process); err != nil {
        t.Fatalf("assignProcess() failed: %v", err)
    }

    cmd.Wait() // Process already terminated, but it was assigned correctly.
}

func TestJobObject_KillOnClose(t *testing.T) {
    j, err := newJobObject()
    if err != nil {
        t.Fatalf("newJobObject() failed: %v", err)
    }
    if err := j.enableKillOnClose(); err != nil {
        t.Fatalf("enableKillOnClose() failed: %v", err)
    }

    // Start a long-running process (timeout command)
    cmd := exec.Command("cmd.exe", "/d", "/c", "timeout /t 60 /nobreak")
    if err := cmd.Start(); err != nil {
        t.Skipf("cannot start cmd.exe: %v", err)
    }

    if err := j.assignProcess(cmd.Process); err != nil {
        t.Fatalf("assignProcess() failed: %v", err)
    }

    // Also start a child process to verify tree termination
    childCmd := exec.Command("cmd.exe", "/d", "/c", "timeout /t 60 /nobreak")
    if err := childCmd.Start(); err != nil {
        t.Logf("warning: cannot start child process for tree test: %v", err)
    } else {
        if err := j.assignProcess(childCmd.Process); err != nil {
            t.Logf("warning: cannot assign child process: %v", err)
        }
    }

    // Close the job object → should kill all processes
    start := time.Now()
    if err := j.close(); err != nil {
        t.Fatalf("close() failed: %v", err)
    }

    // Verify the main process terminated quickly
    err = cmd.Wait()
    elapsed := time.Since(start)
    t.Logf("process terminated in %v, wait error: %v", elapsed, err)

    if elapsed > 5*time.Second {
        t.Errorf("process took too long to terminate after job close: %v", elapsed)
    }

    // Verify child also terminated
    childDead := make(chan error, 1)
    go func() {
        childDead <- childCmd.Wait()
    }()
    select {
    case <-childDead:
        t.Log("child process also terminated")
    case <-time.After(5 * time.Second):
        t.Log("warning: child process may still be running")
    }
}

func TestJobObject_DoubleClose(t *testing.T) {
    j, err := newJobObject()
    if err != nil {
        t.Fatalf("newJobObject() failed: %v", err)
    }
    _ = j.enableKillOnClose()

    cmd := exec.Command("cmd.exe", "/d", "/c", "exit 0")
    _ = cmd.Start()
    _ = j.assignProcess(cmd.Process)
    _ = cmd.Wait()

    // First close should succeed.
    if err := j.close(); err != nil {
        t.Fatalf("first close() failed: %v", err)
    }
    // Second close should be a no-op.
    if err := j.close(); err != nil {
        t.Errorf("second close() should be no-op, got: %v", err)
    }
}
```

- [ ] **步骤 2：运行测试**

```powershell
cd d:\GoProjects\trpc-agent-go\openclaw; go test ./internal/octool/ -run TestJobObject -v -count=1 -timeout 30s
```

期望：所有 4 个测试通过（`TestJobObject_KillOnClose` 验证进程树在 Job Object 关闭时立即终止）

- [ ] **步骤 3：提交**

```powershell
git add openclaw/internal/octool/procgroup_windows_test.go; git commit -m "test(octool): add Job Object unit tests including kill-on-close verification"
```

---

### 任务 3: 修改 `session.go` — 添加 Job Object 到 session 结构体

**文件：**
- 修改：`openclaw/internal/octool/session.go`

- [ ] **步骤 1：在 session 结构体中添加 jobObject 字段**

在 `openclaw/internal/octool/session.go` 的 `session` 结构体中，`ioWG` 字段后添加：

```go
// job is the Windows Job Object for process tree management (nil on non-Windows).
job *jobObject `json:"-" msgpack:"-"`
```

完整结构体修改后（仅展示新增字段）：

```go
type session struct {
    id      string
    command string
    redact  func(string) string

    cmd     *exec.Cmd
    stdin   io.WriteCloser
    closeIO func() error
    cancel  context.CancelFunc
    // job is the Windows Job Object for process tree management.
    // When the session finishes, closing this handle terminates
    // all processes in the job automatically.
    job     *jobObject `json:"-" msgpack:"-"`

    // ... 其余字段不变 ...
}
```

- [ ] **步骤 2：在 kill() 方法中添加 Job Object 关闭逻辑**

在 `session.go:L341` 的 `kill()` 方法中，在 `cancel()` 调用之后、grace 等待之前，添加 Job Object 关闭：

```go
func (s *session) kill(grace time.Duration) error {
    s.mu.Lock()
    cmd := s.cmd
    cancel := s.cancel
    j := s.job
    s.mu.Unlock()

    if cancel != nil {
        cancel()
    }
    // On Windows, close the Job Object handle to terminate the process tree.
    // This must happen after cancel() to ensure the context is done first.
    if j != nil {
        _ = j.close()
    }
    if cmd == nil || cmd.Process == nil {
        return nil
    }

    if runtime.GOOS != "windows" {
        _ = cmd.Process.Signal(syscall.SIGTERM)
    }

    select {
    case <-s.doneCh:
        return nil
    case <-time.After(grace):
        return cmd.Process.Kill()
    }
}
```

- [ ] **步骤 3：在 markDone/清理路径中关闭 Job Object**

在 `session.go` 中找到 `markDone()` 调用后的关闭逻辑，确保 Job Object 在 session 结束时被关闭。由于 `startBackground()` 中的 goroutine 在 `cmd.Process.Wait()` 后调用 `markDone(code)`，而 `kill()` 已经通过 `j.close()` 终止进程树，无需 `markDone()` 中额外关闭。但需确认 `markDone` 对应路径无误。

检查 `manager.go:378-394` 中的 goroutine：`kill()` → `j.close()` 关闭 Job Object → 操作系统向进程树发送终止 → `cmd.Process.Wait()` 返回 → `markDone(code)`。流程正确，无需额外修改。

- [ ] **步骤 4：验证编译通过**

```powershell
cd d:\GoProjects\trpc-agent-go\openclaw; go build ./internal/octool/...
```

期望：编译通过

- [ ] **步骤 5：提交**

```powershell
git add openclaw/internal/octool/session.go; git commit -m "feat(octool): integrate Job Object into session lifecycle for Windows process tree cleanup"
```

---

### 任务 4: 修改 `manager.go:startBackground()` — 集成 Job Object 创建

**文件：**
- 修改：`openclaw/internal/octool/manager.go:L312-397`

- [ ] **步骤 1：读取 startBackground() 当前代码**

确认当前代码结构，特别是 `cmd.Start()` (L361) 和 `cmd.Process.Wait()` (L385) 的位置。

- [ ] **步骤 2：在 cmd.Start() 后创建并附加 Job Object**

在 `manager.go:startBackground()` 中，在 `cmd.Start()` 之后（非 PTY 模式，L361-365 之后）、在 goroutine 启动之前，添加：

```go
// Windows: create a Job Object to manage the process tree.
// This ensures all child processes are terminated when the session ends.
var j *jobObject
if runtime.GOOS == "windows" && cmd.Process != nil {
    job, err := newJobObject()
    if err != nil {
        // Log and continue without Job Object — better than failing the session.
        // The main process is already running, and cmd.Process.Kill() will
        // still terminate the parent process.
        // TODO: consider logging this warning.
    } else {
        _ = job.enableKillOnClose()
        if err := job.assignProcess(cmd.Process); err != nil {
            _ = job.close() // Clean up on failure.
        } else {
            j = job
        }
    }
}
```

这段代码应插入在 PTY/非PTY 分支的汇合点（L367 之前），即在 `cmd.Start()` 之后、`sess.cmd` 赋值之前。具体位置：

在 `manager.go` 中，找到：

```go
    } // end of if params.Pty else block (L366)

    go func() {
        sess.ioWG.Wait()
        close(sess.ioDone)
    }()
```

在这两段之间插入 Job Object 创建代码。

- [ ] **步骤 3：将 Job Object 赋值给 session**

在 `sess.cmd = cmd` (L373) 之后添加：

```go
    sess.job = j
```

- [ ] **步骤 4：在 waiter goroutine 中关闭 Job Object**

在 `manager.go:L378-394` 的 waiter goroutine 中，`markDone(code)` 之后、`cancel()` 之前，添加：

```go
    // On Windows, close the Job Object when the session is done.
    // This is a safety net for non-kill session endings (process exits naturally).
    if j != nil {
        _ = j.close()
    }
```

- [ ] **步骤 5：验证编译通过**

```powershell
cd d:\GoProjects\trpc-agent-go\openclaw; go build ./internal/octool/...
```

期望：编译通过

- [ ] **步骤 6：提交**

```powershell
git add openclaw/internal/octool/manager.go; git commit -m "feat(octool): create and attach Windows Job Object in startBackground for process tree management"
```

---

### 任务 5: 修改 `workspace_runtime_interactive.go:Kill()` — 添加 Job Object 关闭

**文件：**
- 修改：`codeexecutor/local/workspace_runtime_interactive.go:L205-238`

- [ ] **步骤 1：理解当前 Kill 逻辑**

当前 `Kill()` 方法（L205-238）：
- Unix：发送 SIGTERM → 等待 grace → Kill
- Windows：跳过 SIGTERM → 等待 grace → Kill（仅主进程）

问题：`cmd.Process.Kill()` 仅终止主进程，子进程（如 PowerShell 启动的其他命令）可能残留。

- [ ] **步骤 2：添加 Job Object 创建和集成**

首先需要在 `interactiveSession` 结构体中添加 `job *jobObject` 字段。但由于 `interactiveSession` 在 `codeexecutor` 包中，而 `jobObject` 在 `octool` 包中，需要重新考虑架构。

**两种方案：**

**方案 A（推荐）**：在 `codeexecutor/local/` 包中创建独立的 `procgroup_windows.go`，复制 Job Object 封装（仅 3 个 API 调用，代码量小）。

**方案 B**：将 `jobObject` 导出到 `internal/platform` 包。

**选择方案 A** — 最小改动，避免跨包依赖。`codeexecutor/local/` 中已有 `pty_windows.go` 使用 `//go:build windows`，风格一致。

- [ ] **步骤 3：创建 `codeexecutor/local/procgroup_windows.go`**

按任务 1 中 `jobObject` 的相同接口创建（仅 `enableKillOnClose` 和 `assignProcess` 为 KILL_ON_CLOSE 场景所需）：

```go
//go:build windows

package local

import (
    "fmt"
    "os"
    "unsafe"

    "golang.org/x/sys/windows"
)

var kernel32 = windows.NewLazySystemDLL("kernel32.dll")

var (
    procCreateJobObjectW          = kernel32.NewProc("CreateJobObjectW")
    procAssignProcessToJobObject  = kernel32.NewProc("AssignProcessToJobObject")
    procSetInformationJobObject   = kernel32.NewProc("SetInformationJobObject")
)

const (
    jobObjectLimitKillOnJobClose       = 0x2000
    jobObjectExtendedLimitInformation  = 9
)

type localJobObject struct {
    handle windows.Handle
}

func newLocalJobObject() (*localJobObject, error) {
    name, _ := windows.UTF16PtrFromString("")
    h, _, err := procCreateJobObjectW.Call(0, uintptr(unsafe.Pointer(name)))
    if h == 0 {
        return nil, fmt.Errorf("CreateJobObjectW failed: %w", err)
    }
    return &localJobObject{handle: windows.Handle(h)}, nil
}

func (j *localJobObject) enableKillOnClose() error {
    type JELInfo struct {
        basicLimit   windows.JOBOBJECT_BASIC_LIMIT_INFORMATION
        ioInfo       windows.IO_COUNTERS
        processMem   windows.SIZE_T
        jobMem       windows.SIZE_T
        peakProcess  windows.SIZE_T
        peakJob      windows.SIZE_T
    }
    var info JELInfo
    info.basicLimit.LimitFlags = jobObjectLimitKillOnJobClose
    ret, _, err := procSetInformationJobObject.Call(
        uintptr(j.handle), jobObjectExtendedLimitInformation,
        uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info),
    )
    if ret == 0 {
        return fmt.Errorf("SetInformationJobObject failed: %w", err)
    }
    return nil
}

func (j *localJobObject) assignProcess(p *os.Process) error {
    ret, _, err := procAssignProcessToJobObject.Call(uintptr(j.handle), uintptr(p.Pid))
    if ret == 0 {
        return fmt.Errorf("AssignProcessToJobObject failed: %w", err)
    }
    return nil
}

func (j *localJobObject) close() error {
    if j.handle == 0 {
        return nil
    }
    err := windows.CloseHandle(j.handle)
    j.handle = 0
    return err
}
```

- [ ] **步骤 4：在 interactiveSession 中集成 Job Object**

修改 `codeexecutor/local/workspace_runtime_interactive.go`：

1. 在 `interactiveSession` 结构体添加 `job *localJobObject` 字段
2. 在创建 `interactiveSession` 的函数中（找到 `newInteractiveSession` 或创建处），`cmd.Start()` 后创建 Job Object
3. 在 `Kill()` 方法中，关闭 Job Object

`Kill()` 修改后：

```go
func (s *interactiveSession) Kill(grace time.Duration) error {
    s.mu.Lock()
    cmd := s.cmd
    cancel := s.cancel
    j := s.job
    s.mu.Unlock()

    if cancel != nil {
        cancel()
    }
    // On Windows, close the Job Object to terminate the process tree.
    if j != nil {
        _ = j.close()
    }
    if cmd == nil || cmd.Process == nil {
        return nil
    }

    if runtime.GOOS != "windows" {
        _ = cmd.Process.Signal(syscall.SIGTERM)
    }

    select {
    case <-s.doneCh:
        return nil
    case <-time.After(grace):
        return cmd.Process.Kill()
    }
}
```

- [ ] **步骤 5：在 interactiveSession 创建处集成 Job Object**

找到 `newInteractiveSession` 函数（或在 `workspace_runtime_interactive.go` 中搜索创建 session 的函数），在 `cmd.Start()` 之后添加：

```go
// Windows: create Job Object for process tree management.
var j *localJobObject
if runtime.GOOS == "windows" && cmd.Process != nil {
    job, err := newLocalJobObject()
    if err == nil {
        _ = job.enableKillOnClose()
        if err := job.assignProcess(cmd.Process); err != nil {
            _ = job.close()
        } else {
            j = job
        }
    }
}
```

然后将 `j` 赋值给 `session.job` 字段。

- [ ] **步骤 6：验证编译通过**

```powershell
cd d:\GoProjects\trpc-agent-go; go build ./codeexecutor/local/...
```

期望：编译通过

- [ ] **步骤 7：提交**

```powershell
git add codeexecutor/local/workspace_runtime_interactive.go codeexecutor/local/procgroup_windows.go; git commit -m "feat(codeexecutor): integrate Job Object into interactive session for Windows process tree cleanup"
```

---

### 任务 6: 全量验证

**文件：** 无修改

- [ ] **步骤 1：octool 模块测试**

```powershell
cd d:\GoProjects\trpc-agent-go\openclaw; go test ./internal/octool/... -v -count=1 -timeout 60s
```

期望：所有测试通过（Windows 上 Job Object 测试通过）

- [ ] **步骤 2：codeexecutor 模块测试**

```powershell
cd d:\GoProjects\trpc-agent-go; go test ./codeexecutor/... -v -count=1 -timeout 60s
```

期望：所有测试通过或合理跳过

- [ ] **步骤 3：跨平台编译验证**

```powershell
cd d:\GoProjects\trpc-agent-go; $env:GOOS="linux"; $env:GOARCH="amd64"; go build ./openclaw/internal/octool/... ./codeexecutor/...; $env:GOOS=""; $env:GOARCH=""
```

期望：Linux 交叉编译通过（procgroup_windows.go 被 build tag 排除）

- [ ] **步骤 4：golangci-lint**

```powershell
cd d:\GoProjects\trpc-agent-go; golangci-lint run --timeout=10m ./openclaw/internal/octool/... ./codeexecutor/...
```

期望：无新 lint 错误

- [ ] **步骤 5：提交**

```powershell
git commit --allow-empty -m "chore: Phase 2 cross-platform compilation and test verification"
```

---

## 实施顺序依赖

```
任务 1 (procgroup_windows.go — octool)       → 任务 3, 4
任务 2 (procgroup_windows_test.go — octool)  → 任务 6
任务 3 (session.go + jobObject 字段)         → 任务 4
任务 4 (manager.go startBackground)          → 任务 6
任务 5 (workspace_runtime_interactive.go)    → 任务 6（可独立，依赖任务 1 的设计模式）
任务 6 (全量验证)                             → 依赖 1-5 全部完成
```

**推荐执行顺序**：1 → 2 → 3 → 4 → 5 → 6

---

## 验收标准（Phase 2 完成标志）

- [ ] Windows Job Object 在 `startBackground()` 中创建并附加到进程
- [ ] `session.kill()` 关闭 Job Object → 操作系统自动终止进程树
- [ ] `interactiveSession.Kill()` 关闭 Job Object → 进程树完全终止
- [ ] Job Object 单元测试通过（创建、附加、KILL_ON_CLOSE 验证）
- [ ] `kill_session` 后无残留子进程（通过 `TestJobObject_KillOnClose` 验证）
- [ ] 跨平台编译通过（Linux/macOS 构建不受影响）
- [ ] 已知局限文档化：方案 A 在 `cmd.Start()` 和 `AssignProcessToJobObject` 之间存在微秒级竞态窗口

---

## 已知局限

| 局限 | 说明 | 影响 |
|------|------|------|
| 竞态窗口 | `cmd.Start()` → `AssignProcessToJobObject` 之间，子进程可能创建孙进程 | 极低概率（微秒级窗口），孙进程不在 Job Object 中 |
| PTY 模式 | 当前 PTY 在 Windows 上仍返回 "not supported"，Phase 2 不需要处理 PTY 的 Job Object 集成 | Phase 3 实现 ConPTY 时一并处理 |
| `cmd.Process.Kill()` 回退 | 如果 Job Object 创建失败（极端异常），仍使用 `Kill()` 作为回退 | 仅主进程被终止，子进程可能残留 |
| 无 SIGNAL 支持 | Windows 不支持 SIGTERM，Job Object 关闭 = 强制终止，无优雅退出 | 等价于 Unix 的 SIGKILL，Phase 2 目标是确保终止而非优雅 |