# run-go-tests.ps1 - Windows-native counterpart of .github/scripts/run-go-tests.sh
#
# Plan B (dual-track CI): the Linux jobs keep using the bash script and stay
# aligned with upstream. This script is the single Windows entry point so that
# Windows runs never launch any .sh file (avoids file-association dialogs and
# bash/python3/jq PATH problems on Windows).
#
# Usage:
#   pwsh -NoProfile -File .github/scripts/run-go-tests.ps1
#   pwsh -NoProfile -File .github/scripts/run-go-tests.ps1 -Module go.mod
#   powershell -NoProfile -File .github/scripts/run-go-tests.ps1 -IncludeAll
#
# Requirements: go on PATH; for CGO sqlite modules a C compiler plus the
# sqlite3.h vendored at the repository root (CGO_CFLAGS default is set below).

[CmdletBinding()]
param(
    # Explicit go.mod paths (relative to the repo root), like --module in the sh version.
    [string[]]$Module = @(),
    # Extra regex patterns (repo-relative slash paths) to exclude beyond defaults.
    [string[]]$ExcludePattern = @(),
    # Include scratch/GUI modules (bowei-openclaw, temp2) that are not
    # runnable on headless CI runners.
    [switch]$IncludeAll,
    # Skip FTS5-enabled sqlite builds (adds -tags sqlite_fts5 by default).
    [switch]$NoFts5Tag,
    # Wrap go test in a dead-proxy offline guard (disabled by default;
    # see the per-module loop for the rationale). Enable on networks
    # that black-hole external API dials instead of refusing them.
    [switch]$OfflineGuard,
    # Max packages built/tested in parallel (go test -p). Kept below the
    # core count so a full-suite run does not saturate the machine.
    [int]$MaxPackages = 2,
    # Max parallel tests within each package (go test -parallel).
    [int]$MaxTests = 4
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
Set-Location $repoRoot

# ---------------------------------------------------------------------------
# Environment compatibility for Windows
# ---------------------------------------------------------------------------

# CGO sqlite modules need a C compiler plus the sqlite3.h vendored at the
# repository root. Default to it when the caller has not set CGO_CFLAGS.
if (-not $env:CGO_CFLAGS) {
    $env:CGO_CFLAGS = "-I$repoRoot"
}

# -- sh/bash on PATH ---------------------------------------------------------
# Many tests execute POSIX shell snippets (localexec, skillstage, workspaceio,
# hostexec). GitHub's windows runner image already ships Git for Windows on
# PATH; developer machines often resolve `bash` to a WSL stub instead, so
# prepend Git's own shell directory when it exists.
function Find-GitShellDirs {
    $git = (Get-Command git.exe -ErrorAction SilentlyContinue).Source
    if (-not $git) { return @() }
    # ...\Git\cmd\git.exe -> ...\Git\bin and ...\Git\usr\bin
    $gitRoot = Split-Path -Parent (Split-Path -Parent $git)
    $dirs = @()
    foreach ($rel in @('usr\bin', 'bin')) {
        $d = Join-Path $gitRoot $rel
        if (Test-Path -LiteralPath (Join-Path $d 'bash.exe')) { $dirs += $d }
    }
    return $dirs
}
$shellDirs = @(Find-GitShellDirs)
if ($shellDirs.Count -gt 0) {
    $env:PATH = (($shellDirs -join ';') + ';' + $env:PATH)
    Write-Output "posix shell dirs prepended to PATH: $($shellDirs -join ', ')"
}

# -- python3 shim --------------------------------------------------------------
# Code-runner tests exec `python3`. On Windows only `python` typically exists
# (the WindowsApps python3.exe alias is a silent Store stub), which surfaces
# as exit status 9009. Build a tiny pass-through python3.exe into a temp dir
# and prepend it to PATH so child go test processes find it via LookPath.
function Test-Python3Usable {
    # The WindowsApps app-execution-alias stub exits silently (often with
    # 9009 or by hanging on a Store dialog), so success alone is not
    # enough: require the interpreter to actually print the probe value.
    $out = (& python3 -c "print(1)" 2>$null)
    return ($LASTEXITCODE -eq 0 -and ($out | Out-String).Trim() -eq '1')
}
if (-not (Test-Python3Usable)) {
    $python = (Get-Command python.exe -ErrorAction SilentlyContinue).Source
    if ($python) {
        $shimDir = Join-Path $env:TEMP 'trpc-go-test-shim'
        if (-not (Test-Path -LiteralPath $shimDir)) {
            New-Item -ItemType Directory -Path $shimDir | Out-Null
        }
        $shimExe = Join-Path $shimDir 'python3.exe'
        # Rebuild every run: the interpreter's absolute path is baked into
        # the shim because hardened child environments strip PATH, and a
        # name-based lookup inside the shim would then fail silently.
        if ($true) {
            $srcDir = Join-Path $shimDir 'py3shim'
            New-Item -ItemType Directory -Path $srcDir -Force | Out-Null
            $mainGo = Join-Path $srcDir 'main.go'
            $bakedPython = $python.Replace('\', '\\')
            $shimSource = @'
// python3 pass-through shim for Windows test environments.
package main

import (
	"os"
	"os/exec"
)

// pythonExe is the absolute interpreter path baked at build time so the
// shim keeps working when the child environment has no PATH.
var pythonExe = "__PYTHON_EXE__"

func main() {
	cmd := exec.Command(pythonExe, os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		os.Exit(1)
	}
}
'@
            $shimSource = $shimSource.Replace('__PYTHON_EXE__', $bakedPython)
            Set-Content -LiteralPath $mainGo -Value $shimSource -Encoding UTF8
            Push-Location $srcDir
            # Native commands write informational output to stderr; with
            # ErrorActionPreference=Stop PowerShell turns that into a
            # terminating error, so relax it just for the shim build.
            $previousPreference = $ErrorActionPreference
            $ErrorActionPreference = 'Continue'
            try {
                & go mod init py3shim 2>$null | Out-Null
                & go build -o $shimExe . 2>$null | Out-Null
                if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $shimExe)) {
                    Write-Output 'WARNING: failed to build python3 shim; python3-dependent tests may fail'
                }
            } finally {
                $ErrorActionPreference = $previousPreference
                Pop-Location
            }
        }
        if (Test-Path -LiteralPath $shimExe) {
            $env:PATH = $shimDir + ';' + $env:PATH
            Write-Output "python3 shim active: $shimExe"
        }
    } else {
        Write-Output 'WARNING: no python interpreter found for python3 shim'
    }
}

$goTestTags = @()
if (-not $NoFts5Tag) {
    # paituo's sqlite-backed tests create FTS5 virtual tables; the mattn
    # driver only compiles FTS5 support in with this build tag.
    $goTestTags += '-tags'
    $goTestTags += 'sqlite_fts5'
}

# ---------------------------------------------------------------------------
# Module discovery (mirrors run-go-tests.sh)
# ---------------------------------------------------------------------------

$excludePrefixes = @('.resource/', 'docs/', 'examples/', 'test/')
$defaultExcludes = @('^(bowei-openclaw|temp2)/')

$patterns = @()
if ($ExcludePattern) { $patterns += $ExcludePattern }
if (-not $IncludeAll) { $patterns += $defaultExcludes }

if ($Module.Count -gt 0) {
    $gomodules = @()
    foreach ($m in $Module) {
        $rel = $m -replace '^(\./|\\)', ''
        $full = Join-Path $repoRoot $rel
        if (-not (Test-Path -LiteralPath $full)) {
            Write-Error "module file not found: $m"
            exit 1
        }
        $gomodules += $rel
    }
} else {
    $gomodules = Get-ChildItem -LiteralPath $repoRoot -Recurse -Filter 'go.mod' -File |
        ForEach-Object { $_.FullName.Substring($repoRoot.Length + 1) -replace '\\', '/' } |
        Where-Object {
            $rel = $_
            $skip = $false
            foreach ($p in $excludePrefixes) { if ($rel.StartsWith($p, 'OrdinalIgnoreCase')) { $skip = $true; break } }
            if ($skip) { return $false }
            foreach ($rx in $patterns) { if ($rel -match $rx) { return $false } }
            return $true
        } |
        Where-Object {
            # Skip template modules marked "DO NOT USE!" like the sh version.
            $head = Get-Content -LiteralPath (Join-Path $repoRoot $_) -TotalCount 5 -Encoding UTF8 -ErrorAction SilentlyContinue
            -not ($head -match 'DO NOT USE!')
        } |
        Sort-Object
}

if ($gomodules.Count -eq 0) {
    Write-Error 'no go modules found'
    exit 1
}

Write-Output "testing $($gomodules.Count) go modules on windows (pwsh native, no bash)"

# ---------------------------------------------------------------------------
# Per-module test + coverage collection
# ---------------------------------------------------------------------------

$coverageDir = Join-Path $repoRoot 'coverage'
if (Test-Path -LiteralPath $coverageDir) { Remove-Item -LiteralPath $coverageDir -Recurse -Force }
New-Item -ItemType Directory -Path $coverageDir | Out-Null
$combinedProfile = Join-Path $repoRoot 'coverage.out'
if (Test-Path -LiteralPath $combinedProfile) { Remove-Item -LiteralPath $combinedProfile -Force }

$failed = @()

foreach ($mod in $gomodules) {
    $modFull = Join-Path $repoRoot $mod
    $modDir = Split-Path -Parent $modFull
    if ((-not $modDir) -or ($modDir -eq $repoRoot)) {
        $modDir = $repoRoot
        $readable = 'root'
    } else {
        $readable = $modDir.Substring($repoRoot.Length + 1) -replace '\\', '/'
    }
    $safe = ($readable -replace '/', '_') -replace '\.', '_'

    Write-Output "::group::Testing $safe"
    Write-Output "module dir: $readable"

    $modulePath = (Get-Content -LiteralPath $modFull -Encoding UTF8 |
        Where-Object { $_ -match '^\s*module\s+(\S+)' } |
        ForEach-Object { $Matches[1] } | Select-Object -First 1)
    Write-Output "module import path: $modulePath"

    Push-Location $modDir
    # Offline guard (opt-in via -OfflineGuard): a few upstream tests build
    # real OpenAI-compatible clients that dial api.openai.com and rely on
    # the request failing fast. On networks that black-hole such dials
    # (SYN dropped) the test binary hangs instead. The guard points the
    # proxy at a closed local port so external dials fail immediately
    # while local httptest servers stay reachable through NO_PROXY.
    # It is opt-in because the proxy also breaks legitimate network tests
    # (git-clone based sources) on machines that do have internet access;
    # CI runners reach external APIs fast and never need it.
    $hadProxy = $null -ne $env:HTTP_PROXY -or $null -ne $env:HTTPS_PROXY
    $useGuard = $OfflineGuard.IsPresent -and -not $hadProxy
    if ($useGuard) {
        $env:HTTP_PROXY = 'http://127.0.0.1:9'
        $env:HTTPS_PROXY = 'http://127.0.0.1:9'
        $env:NO_PROXY = 'localhost,127.0.0.1,::1'
    }
    try {
        # Build the native argument list as one array; splatting mixed with
        # bareword flags is unreliable across PowerShell versions.
        # Concurrency caps keep a full-suite run from saturating the CPU:
        # -p bounds parallel package builds/runs, -parallel bounds the
        # in-package parallel tests (go defaults both to GOMAXPROCS).
        $goArgs = @('test') + $goTestTags
        if ($MaxPackages -gt 0) { $goArgs += @('-p', "$MaxPackages") }
        if ($MaxTests -gt 0) { $goArgs += @('-parallel', "$MaxTests") }
        $goArgs += @('-coverprofile=coverage.out', './...')
        & go @goArgs
        $code = $LASTEXITCODE
    } finally {
        if ($useGuard) {
            Remove-Item Env:HTTP_PROXY, Env:HTTPS_PROXY, Env:NO_PROXY -ErrorAction SilentlyContinue
        }
        Pop-Location
    }

    $localProfile = Join-Path $modDir 'coverage.out'
    if (Test-Path -LiteralPath $localProfile) {
        # Rewrite module-qualified paths to repository-relative paths so
        # `go tool cover` and Codecov can consume the merged profile
        # (PowerShell port of the python3 block in run-go-tests.sh).
        $prefix = "$modulePath/"
        $outLines = New-Object System.Collections.Generic.List[string]
        $index = 0
        foreach ($line in [System.IO.File]::ReadAllLines($localProfile)) {
            if ($index -eq 0) { $outLines.Add($line); $index++; continue }
            if ($line.StartsWith($prefix)) {
                $rest = $line.Substring($prefix.Length)
                if ($readable -ne 'root') { $outLines.Add("./$readable/$rest") }
                else { $outLines.Add("./$rest") }
            } else {
                $outLines.Add($line)
            }
            $index++
        }
        [System.IO.File]::WriteAllLines((Join-Path $coverageDir "$safe.out"), $outLines)
        Remove-Item -LiteralPath $localProfile -Force
    }

    if ($code -ne 0) {
        $failed += $readable
        Write-Output "FAIL: $readable (exit $code)"
    } else {
        Write-Output "PASS: $readable"
    }
    Write-Output '::endgroup::'
}

# ---------------------------------------------------------------------------
# Merge coverage profiles
# ---------------------------------------------------------------------------

$first = $true
foreach ($profile in (Get-ChildItem -LiteralPath $coverageDir -Filter '*.out' | Sort-Object Name)) {
    if ($first) {
        Copy-Item -LiteralPath $profile.FullName -Destination $combinedProfile
        $first = $false
    } else {
        Get-Content -LiteralPath $profile.FullName -Encoding UTF8 |
            Select-Object -Skip 1 |
            Add-Content -LiteralPath $combinedProfile -Encoding UTF8
    }
}
Write-Output "Combined coverage written to coverage.out"

if ($failed.Count -gt 0) {
    Write-Output ''
    Write-Output "FAILED MODULES ($($failed.Count)):"
    foreach ($f in $failed) { Write-Output "  - $f" }
    exit 1
}
Write-Output "ALL $($gomodules.Count) MODULES PASSED"
exit 0
