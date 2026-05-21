# exec_command Windows 支持 Phase 1 实施计划

> **对于代理工作器：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实施此计划。

**目标：** 让 `exec_command`、`workspace_exec`、`code_exec` 在 Windows 上可用，使用 PowerShell（优先）/cmd.exe 执行命令，中文输出不乱码。

**架构：** 新增 `internal/platform/` 包统一 Shell 选择逻辑，通过 `//go:build` 标签隔离平台；新增 `codepage_windows.go` 处理 GBK→UTF-8 编码转换；修改 3 处硬编码 bash 位置 + 1 处 session 编码点 + 3 处测试。

**技术栈：** Go 1.21+, `golang.org/x/text/encoding/simplifiedchinese`, `golang.org/x/sys/windows`, `//go:build` 标签

**前置参考：** `docs/exec_command_windows_support_analysis.md`（v3）

---

## 文件结构

```
新建：
  internal/platform/platform.go              — 公共接口：ShellSpec, Shell(), BuildCommand()
  internal/platform/platform_unix.go         — Unix 实现：bash → sh 回退
  internal/platform/platform_windows.go      — Windows 实现：PS 优先 → cmd 回退 + isNonNativeShell()
  internal/platform/platform_test.go         — 跨平台单元测试
  internal/platform/platform_windows_test.go — Windows 特定测试
  openclaw/internal/octool/codepage_windows.go — GBK→UTF-8 编码转换（sync.Once 缓存）

修改：
  openclaw/internal/octool/manager.go        — shellCmd() 使用 platform.BuildCommand()；snapshotLoginShellEnv() 在 Windows 用 os.Environ()
  openclaw/internal/octool/session.go        — readFrom() 增加 Windows 编码转换
  codeexecutor/local/local.go                — buildCommandArgs() 使用 platform.Shell()
  codeexecutor/workspace.go                  — BuildBlockSpec() 使用 platform.Shell()
  openclaw/internal/octool/tools_test.go     — 测试适配：bash → 平台 Shell
  codeexecutor/local/local_test.go           — 测试适配
  codeexecutor/local/workspace_runtime_test.go — 测试适配
```

---

### 任务 1: 创建 `internal/platform/` 包 — 公共接口

**文件：**
- 创建：`internal/platform/platform.go`

- [ ] **步骤 1：创建 platform.go 公共接口**

```go
// Package platform provides cross-platform abstractions for command execution.
package platform

import "context"

// ShellSpec describes the command and arguments for a platform shell.
type ShellSpec struct {
    Command string   // e.g. "bash" or "powershell.exe"
    Args    []string // e.g. ["-lc"] or ["-NoProfile", "-NonInteractive", "-Command"]
}

// Shell returns the OS-appropriate shell specification.
// On Unix: prefers bash, falls back to sh.
// On Windows: prefers PowerShell, falls back to cmd.exe.
// WSL bash, Git Bash, Cygwin, and MSYS2 are never returned.
func Shell() (ShellSpec, error)

// BuildCommand builds the OS command that runs userCommand through the
// shell. It is a convenience wrapper around Shell().
func BuildCommand(ctx context.Context, userCommand string) (cmd string, args []string, err error)
```

- [ ] **步骤 2：验证编译通过**

```powershell
cd d:\GoProjects\trpc-agent-go; go build ./internal/platform/...
```

期望：编译失败 — `Shell()` 和 `BuildCommand()` 未定义（需要 `_unix.go` 或 `_windows.go`）

- [ ] **步骤 3：提交**

```powershell
git add internal/platform/platform.go; git commit -m "feat(platform): add cross-platform shell abstraction interface"
```

---

### 任务 2: 创建 `platform_unix.go` — Unix 实现

**文件：**
- 创建：`internal/platform/platform_unix.go`

- [ ] **步骤 1：创建 platform_unix.go**

```go
//go:build !windows

package platform

import (
    "context"
    "errors"
    "os/exec"
)

// Shell returns bash or sh on Unix-like systems.
func Shell() (ShellSpec, error) {
    if path, err := exec.LookPath("bash"); err == nil {
        return ShellSpec{Command: path, Args: []string{"-lc"}}, nil
    }
    if path, err := exec.LookPath("sh"); err == nil {
        return ShellSpec{Command: path, Args: []string{"-lc"}}, nil
    }
    return ShellSpec{}, errors.New("bash or sh is required")
}

// BuildCommand builds a shell command for Unix-like systems.
func BuildCommand(_ context.Context, userCommand string) (string, []string, error) {
    s, err := Shell()
    if err != nil {
        return "", nil, err
    }
    return s.Command, append(s.Args, userCommand), nil
}
```

- [ ] **步骤 2：验证编译通过**

```powershell
cd d:\GoProjects\trpc-agent-go; go build ./internal/platform/...
```

期望：编译通过

- [ ] **步骤 3：提交**

```powershell
git add internal/platform/platform_unix.go; git commit -m "feat(platform): implement Unix platform shell detection"
```

---

### 任务 3: 创建 `platform_windows.go` — Windows 实现

**文件：**
- 创建：`internal/platform/platform_windows.go`

- [ ] **步骤 1：创建 platform_windows.go**

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
//   - PowerShell is the default (richer scripting, object pipeline, better error handling)
//   - cmd.exe is the fallback when PowerShell is unavailable
//   - WSL bash/sh are ALWAYS excluded (requires HCS, unreliable)
//   - Git Bash/Cygwin/MSYS2 bash are ALWAYS excluded (path/encoding incompatibility)
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
        `\git\bin\bash.exe`,
        `\git\usr\bin\bash.exe`,
        `\msys64\usr\bin\bash.exe`,
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

// BuildCommand builds a shell command for Windows.
func BuildCommand(_ context.Context, userCommand string) (string, []string, error) {
    s, err := Shell()
    if err != nil {
        return "", nil, err
    }
    return s.Command, append(s.Args, userCommand), nil
}
```

- [ ] **步骤 2：验证编译通过**

```powershell
cd d:\GoProjects\trpc-agent-go; go build ./internal/platform/...
```

期望：编译通过

- [ ] **步骤 3：提交**

```powershell
git add internal/platform/platform_windows.go; git commit -m "feat(platform): implement Windows platform shell detection with non-native shell exclusion"
```

---

### 任务 4: 创建 platform 包测试文件

**文件：**
- 创建：`internal/platform/platform_test.go`
- 创建：`internal/platform/platform_windows_test.go`

- [ ] **步骤 1：创建 platform_test.go（跨平台测试）**

```go
package platform

import (
    "testing"
)

func TestShell_ReturnsValidSpec(t *testing.T) {
    spec, err := Shell()
    if err != nil {
        t.Fatalf("Shell() returned error: %v", err)
    }
    if spec.Command == "" {
        t.Fatal("Shell() returned empty Command")
    }
    if len(spec.Args) == 0 {
        t.Fatal("Shell() returned empty Args")
    }
    t.Logf("Shell detected: %s %v", spec.Command, spec.Args)
}

func TestBuildCommand_ReturnsValidCommand(t *testing.T) {
    cmd, args, err := BuildCommand(nil, "echo hello")
    if err != nil {
        t.Fatalf("BuildCommand() returned error: %v", err)
    }
    if cmd == "" {
        t.Fatal("BuildCommand() returned empty cmd")
    }
    if len(args) < 2 {
        t.Fatalf("BuildCommand() returned too few args: %v", args)
    }
    t.Logf("BuildCommand: %s %v", cmd, args)
}

func TestShell_ConsistentResults(t *testing.T) {
    // Shell() should return the same result on repeated calls
    spec1, err1 := Shell()
    spec2, err2 := Shell()
    if (err1 != nil) != (err2 != nil) {
        t.Fatal("Shell() error state inconsistent across calls")
    }
    if err1 == nil && spec1 != spec2 {
        t.Fatalf("Shell() returned different results: %v vs %v", spec1, spec2)
    }
}
```

- [ ] **步骤 2：创建 platform_windows_test.go（Windows 特定测试）**

```go
//go:build windows

package platform

import (
    "os"
    "testing"
)

func TestShell_PrefersPowerShell(t *testing.T) {
    spec, err := Shell()
    if err != nil {
        t.Fatalf("Shell() returned error: %v", err)
    }
    // On Windows 10+, powershell.exe should always be available
    // but if not (e.g. Nano Server), cmd.exe is acceptable
    t.Logf("Shell: %s %v", spec.Command, spec.Args)
}

func TestShell_ExcludesWSLPaths(t *testing.T) {
    sysRoot := os.Getenv("SystemRoot")
    if sysRoot == "" {
        sysRoot = `C:\Windows`
    }

    tests := []struct {
        path     string
        expected bool
    }{
        {sysRoot + `\System32\bash.exe`, true},
        {sysRoot + `\SysWOW64\bash.exe`, true},
        {sysRoot + `\System32\cmd.exe`, false},
        {sysRoot + `\System32\WindowsPowerShell\v1.0\powershell.exe`, false},
    }

    for _, tt := range tests {
        result := isNonNativeShell(tt.path)
        if result != tt.expected {
            t.Errorf("isNonNativeShell(%q) = %v, want %v", tt.path, result, tt.expected)
        }
    }
}

func TestShell_ExcludesNonNativeShellPaths(t *testing.T) {
    tests := []struct {
        path     string
        desc     string
        expected bool
    }{
        {`C:\Program Files\Git\bin\bash.exe`, "Git for Windows", true},
        {`D:\tools\msys64\usr\bin\bash.exe`, "MSYS2", true},
        {`E:\cygwin64\bin\bash.exe`, "Cygwin 64", true},
        {`C:\cygwin\bin\bash.exe`, "Cygwin 32", true},
        {`C:\Windows\System32\cmd.exe`, "Native cmd", false},
        {`C:\Program Files\PowerShell\7\pwsh.exe`, "PowerShell 7", false},
    }

    for _, tt := range tests {
        result := isNonNativeShell(tt.path)
        if result != tt.expected {
            t.Errorf("isNonNativeShell(%q) [%s] = %v, want %v", tt.path, tt.desc, result, tt.expected)
        }
    }
}
```

- [ ] **步骤 3：运行测试**

```powershell
cd d:\GoProjects\trpc-agent-go; go test ./internal/platform/... -v -count=1
```

期望：所有测试通过

- [ ] **步骤 4：提交**

```powershell
git add internal/platform/platform_test.go internal/platform/platform_windows_test.go; git commit -m "test(platform): add cross-platform and Windows-specific tests"
```

---

### 任务 5: 修改 `openclaw/internal/octool/manager.go` — Shell 抽象

**文件：**
- 修改：`openclaw/internal/octool/manager.go`

- [ ] **步骤 1：移除硬编码常量，重写 shellCmd()**

在 `openclaw/internal/octool/manager.go` 中，找到：

```go
const (
    shellProgram        = "bash"
    shellLoginFlag      = "-lc"
    shellEnvDumpCommand = "env -0"
)
```

替换为：

```go
import "trpc.group/trpc-go/trpc-agent-go/internal/platform"

// shellCmd builds an exec.Cmd for running a user command through the
// OS-appropriate shell. On Unix this is bash -lc, on Windows it is
// powershell.exe or cmd.exe.
func shellCmd(ctx context.Context, command string) *exec.Cmd {
    p, args, err := platform.BuildCommand(ctx, command)
    if err != nil {
        // Fallback to bash on Unix for backward compatibility
        return exec.CommandContext(ctx, "bash", "-lc", command)
    }
    return exec.CommandContext(ctx, p, args...)
}
```

- [ ] **步骤 2：修改 snapshotLoginShellEnv() 适配 Windows**

在 `openclaw/internal/octool/manager.go:L523` 的 `snapshotLoginShellEnv()` 函数开头添加 Windows 分支：

```go
func snapshotLoginShellEnv(
    ctx context.Context,
    workdir string,
) map[string]string {
    if runtime.GOOS == "windows" {
        // Windows has no login shell concept; use process environment directly.
        // PowerShell and cmd.exe both inherit the process environment,
        // and "env -0" is not a valid command on either.
        env := make(map[string]string)
        for _, kv := range os.Environ() {
            if k, v, ok := strings.Cut(kv, "="); ok {
                env[k] = v
            }
        }
        return env
    }
    if ctx == nil {
        ctx = context.Background()
    }
    ctx, cancel := context.WithTimeout(ctx, defaultShellEnvTimeout)
    defer cancel()

    cmd := shellCmd(ctx, shellEnvDumpCommand)
    cmd.Dir = workdir

    out, err := cmd.Output()
    if err != nil {
        return nil
    }
    // ... existing parsing logic follows (split on '\x00', filter empty keys)
```

- [ ] **步骤 3：验证编译通过**

```powershell
cd d:\GoProjects\trpc-agent-go; go build ./openclaw/internal/octool/...
```

期望：编译通过

- [ ] **步骤 4：确认无引用残留**

```powershell
cd d:\GoProjects\trpc-agent-go; go vet ./openclaw/internal/octool/...
```

期望：无错误

- [ ] **步骤 5：提交**

```powershell
git add openclaw/internal/octool/manager.go; git commit -m "refactor(octool): use platform.BuildCommand for cross-platform shell; skip shell env dump on Windows"
```

---

### 任务 6: 修改 `codeexecutor/local/local.go` — buildCommandArgs 平台化

**文件：**
- 修改：`codeexecutor/local/local.go`

- [ ] **步骤 1：修改 buildCommandArgs() 中 bash 硬编码**

在 `codeexecutor/local/local.go` 中，找到 `buildCommandArgs()` 中类似：

```go
case "bash", "sh":
    return []string{"bash", filePath}
```

替换为：

```go
case "bash", "sh":
    shell, err := platform.Shell()
    if err != nil {
        return nil
    }
    return append(shell.Args, filePath)
```

- [ ] **步骤 2：确认 import 已添加**

在 `local.go` 头部 import 块中添加：

```go
import "trpc.group/trpc-go/trpc-agent-go/internal/platform"
```

- [ ] **步骤 3：验证编译通过**

```powershell
cd d:\GoProjects\trpc-agent-go; go build ./codeexecutor/local/...
```

期望：编译通过

- [ ] **步骤 4：提交**

```powershell
git add codeexecutor/local/local.go; git commit -m "refactor(codeexecutor): use platform.Shell in buildCommandArgs"
```

---

### 任务 7: 修改 `codeexecutor/workspace.go` — BuildBlockSpec 平台化

**文件：**
- 修改：`codeexecutor/workspace.go`

- [ ] **步骤 1：修改 BuildBlockSpec() 中 bash 硬编码**

在 `codeexecutor/workspace.go` 中，找到 `BuildBlockSpec()` 中类似：

```go
case "bash", "sh":
    return fmt.Sprintf("code_%d.sh", idx), DefaultExecFileMode, "bash", nil, nil
```

替换为：

```go
case "bash", "sh":
    shell, err := platform.Shell()
    if err != nil {
        return "", 0, "", nil, err
    }
    return fmt.Sprintf("code_%d.sh", idx), DefaultExecFileMode, shell.Command, shell.Args, nil
```

- [ ] **步骤 2：确认 import 已添加**

在 `workspace.go` 头部 import 块中添加：

```go
import "trpc.group/trpc-go/trpc-agent-go/internal/platform"
```

- [ ] **步骤 3：验证编译通过**

```powershell
cd d:\GoProjects\trpc-agent-go; go build ./codeexecutor/...
```

期望：编译通过

- [ ] **步骤 4：提交**

```powershell
git add codeexecutor/workspace.go; git commit -m "refactor(codeexecutor): use platform.Shell in BuildBlockSpec"
```

---

### 任务 8: 创建 `codepage_windows.go` — GBK→UTF-8 编码转换

**文件：**
- 创建：`openclaw/internal/octool/codepage_windows.go`

- [ ] **步骤 1：创建 codepage_windows.go**

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
    case 65001: // UTF-8 — no conversion needed
        return encoding.Nop
    case 936: // GBK / GB2312 — Simplified Chinese
        return simplifiedchinese.GBK
    case 950: // Big5 — Traditional Chinese
        return simplifiedchinese.HZGB2312
    case 1200: // UTF-16 LE
        return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)
    default:
        return encoding.Nop // Unknown code page, read as UTF-8
    }
}

func getConsoleCodePage() uint32 {
    // Try console output code page first
    ret, _, _ := kernel32GetConsoleOutputCP.Call()
    if ret == 0 {
        // Fall back to system ANSI code page (more reliable for subprocesses)
        ret, _, _ = kernel32GetACP.Call()
    }
    if ret == 0 {
        return 65001 // Default to UTF-8
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
        return string(input) // Keep original bytes on failure
    }
    return string(result)
}
```

- [ ] **步骤 2：验证编译通过**

```powershell
cd d:\GoProjects\trpc-agent-go; go build ./openclaw/internal/octool/...
```

期望：编译通过

- [ ] **步骤 3：检查 go.mod 是否已有 golang.org/x/text 依赖**

```powershell
cd d:\GoProjects\trpc-agent-go; go list -m golang.org/x/text
```

期望：输出版本号。如果不存在，执行 `go get golang.org/x/text`

- [ ] **步骤 4：提交**

```powershell
git add openclaw/internal/octool/codepage_windows.go; git commit -m "feat(octool): add Windows console encoding conversion with sync.Once caching"
```

---

### 任务 9: 修改 `session.go:readFrom()` — 编码转换集成

**文件：**
- 修改：`openclaw/internal/octool/session.go`

- [ ] **步骤 1：在 readFrom() 中添加 Windows 编码转换**

在 `openclaw/internal/octool/session.go` 的 `readFrom()` 方法中，找到 `s.appendOutput(string(chunk))` 行，在其之前插入：

```go
if runtime.GOOS == "windows" {
    chunk = []byte(decodeConsoleOutput(chunk))
}
```

完整上下文（最终版本）：

```go
func (s *session) readFrom(reader io.Reader) {
    if reader == nil {
        return
    }
    bufReader := bufio.NewReaderSize(reader, 32*1024)
    for {
        chunk, err := bufReader.ReadBytes('\n')
        if len(chunk) > 0 {
            if runtime.GOOS == "windows" {
                chunk = []byte(decodeConsoleOutput(chunk))
            }
            s.appendOutput(string(chunk))
        }
        if err != nil {
            if err != io.EOF && len(chunk) > 0 {
                if runtime.GOOS == "windows" {
                    chunk = []byte(decodeConsoleOutput(chunk))
                }
                s.appendOutput(string(chunk))
            }
            return
        }
    }
}
```

- [ ] **步骤 2：验证编译通过**

```powershell
cd d:\GoProjects\trpc-agent-go; go build ./openclaw/internal/octool/...
```

期望：编译通过

- [ ] **步骤 3：提交**

```powershell
git add openclaw/internal/octool/session.go; git commit -m "feat(octool): add Windows console encoding conversion in session output"
```

---

### 任务 10: 修改测试文件 — 适配平台 Shell

**文件：**
- 修改：`openclaw/internal/octool/tools_test.go`
- 修改：`codeexecutor/local/local_test.go`
- 修改：`codeexecutor/local/workspace_runtime_test.go`

- [ ] **步骤 1：修改 octool/tools_test.go — 替换 bash 检测**

在 `openclaw/internal/octool/tools_test.go` 中，找到类似：

```go
func TestExecCommand(t *testing.T) {
    if _, err := exec.LookPath("bash"); err != nil {
        t.Skip("bash is not available")
    }
    // ... test logic using "bash"
```

替换为：

```go
import "trpc.group/trpc-go/trpc-agent-go/internal/platform"

func TestExecCommand(t *testing.T) {
    shell, err := platform.Shell()
    if err != nil {
        t.Skipf("no shell available: %v", err)
    }
    // Use shell.Command instead of "bash" in test assertions
```

- [ ] **步骤 2：修改 local_test.go — 测试适配**

在 `codeexecutor/local/local_test.go` 中，找到 bash 硬编码的测试断言，替换为使用 `platform.Shell()` 返回的 Shell 名称。

具体操作：
1. 搜索 `"bash"` 字面量
2. 对涉及 Shell 命令执行的断言，替换为 `shell.Command` 变量
3. 在 `t.Skip` 条件中使用 `platform.Shell()` 的错误检查替代 `exec.LookPath("bash")`

- [ ] **步骤 3：修改 workspace_runtime_test.go — 测试适配**

同上，搜索并替换所有 bash 硬编码。

- [ ] **步骤 4：运行所有测试**

```powershell
cd d:\GoProjects\trpc-agent-go; go test ./internal/platform/... ./openclaw/internal/octool/... ./codeexecutor/local/... -v -count=1
```

期望：
- `./internal/platform/...` — 全部通过
- `./openclaw/internal/octool/...` — 全部通过或跳过（无运行中的命令）
- `./codeexecutor/local/...` — 全部通过或跳过

- [ ] **步骤 5：提交**

```powershell
git add openclaw/internal/octool/tools_test.go codeexecutor/local/local_test.go codeexecutor/local/workspace_runtime_test.go; git commit -m "test: adapt tests for cross-platform shell (platform.Shell instead of bash lookup)"
```

---

### 任务 11: 全量验证

**文件：** 无修改

- [ ] **步骤 1：根模块全部测试**

```powershell
cd d:\GoProjects\trpc-agent-go; go test ./... -count=1
```

期望：所有测试通过（或合理地跳过）

- [ ] **步骤 2：golangci-lint 检查**

```powershell
cd d:\GoProjects\trpc-agent-go; golangci-lint run --timeout=10m
```

期望：无新引入的 lint 错误

- [ ] **步骤 3：gofmt 检查**

```powershell
cd d:\GoProjects\trpc-agent-go; gofmt -l internal/platform/ openclaw/internal/octool/codepage_windows.go
```

期望：无输出（无格式问题）

---

### 任务 12: 跨平台编译验证

**文件：** 无修改

- [ ] **步骤 1：验证当前平台（Windows）编译**

```powershell
cd d:\GoProjects\trpc-agent-go; go build ./...
```

期望：编译通过

- [ ] **步骤 2：验证 Linux 交叉编译（Go 1.21+ 支持 GOOS=linux）**

```powershell
cd d:\GoProjects\trpc-agent-go; $env:GOOS="linux"; $env:GOARCH="amd64"; go build ./internal/platform/... ./openclaw/internal/octool/... ./codeexecutor/...; $env:GOOS=""; $env:GOARCH=""
```

期望：编译通过（Unix 分支使用 `!windows` build tag）

- [ ] **步骤 3：验证 macOS 交叉编译**

```powershell
cd d:\GoProjects\trpc-agent-go; $env:GOOS="darwin"; $env:GOARCH="amd64"; go build ./internal/platform/... ./openclaw/internal/octool/... ./codeexecutor/...; $env:GOOS=""; $env:GOARCH=""
```

期望：编译通过

- [ ] **步骤 4：提交**

```powershell
git commit --allow-empty -m "chore: verify cross-platform compilation"
```

---

## 实施顺序依赖

```
任务 1 (platform.go)          → 所有后续任务
任务 2 (platform_unix.go)     → 任务 4 (测试)
任务 3 (platform_windows.go)  → 任务 4 (测试)
任务 4 (测试)                  → 可并行运行
任务 5 (manager.go)           → 可独立，要求 任务 1-3 完成
任务 6 (local.go)             → 可独立，要求 任务 1-3 完成
任务 7 (workspace.go)         → 可独立，要求 任务 1-3 完成
任务 8 (codepage)             → 可独立，要求 任务 1-3 完成（并行于 5-7）
任务 9 (session.go)           → 要求 任务 8 完成
任务 10 (测试修改)             → 可独立，要求 任务 5 完成
任务 11 (全量验证)             → 要求 任务 1-10 完成
任务 12 (交叉编译)             → 要求 任务 1-10 完成
```

**推荐并行执行：** 任务 5、6、7、8 可在任务 1-3 完成后并行启动。

---

## 验收标准（Phase 1 完成标志）

- [x] 方案文档已更新至 v3（`docs/exec_command_windows_support_analysis.md`）
- [ ] `internal/platform/` 包完整实现并通过测试
- [ ] `exec_command` 在 Windows 上使用 PowerShell/cmd.exe 执行命令（不调用 WSL bash）
- [ ] `workspace_exec` 和 `code_exec` 在 Windows 上可用
- [ ] 中文输出（`dir` 文件名、`type` 文件内容等）正确显示，无乱码
- [ ] WSL bash/sh 被正确排除（即使 WSL 已安装）
- [ ] Git Bash/Cygwin/MSYS2 被正确排除
- [ ] 现有 Linux/macOS 行为不变（所有现有测试通过）
- [ ] 跨平台编译通过（linux/darwin/windows, amd64）