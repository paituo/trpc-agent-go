//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package luaexec

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// registerFSBridge registers the fs module in the Lua VM.
// The fs module provides controlled filesystem operations restricted to
// allowed_script_dirs when configured.
func registerFSBridge(L *lua.LState, cfg *Config) {
	mod := L.NewTable()
	L.SetField(mod, "read_file", L.NewFunction(bridgeFSReadFile))
	L.SetField(mod, "write_file", L.NewFunction(bridgeFSWriteFile))
	L.SetField(mod, "list_dir", L.NewFunction(bridgeFSListDir))
	L.SetField(mod, "file_exists", L.NewFunction(bridgeFSFileExists))
	L.SetField(mod, "is_dir", L.NewFunction(bridgeFSIsDir))
	L.SetField(mod, "mkdir", L.NewFunction(bridgeFSMkdir))
	L.SetField(mod, "remove", L.NewFunction(bridgeFSRemove))
	L.SetField(mod, "copy", L.NewFunction(bridgeFSCopy))
	L.SetField(mod, "move", L.NewFunction(bridgeFSMove))
	L.SetField(mod, "stat", L.NewFunction(bridgeFSStat))

	// Store allowed dirs for path validation.
	dirTbl := L.NewTable()
	for i, dir := range cfg.AllowedScriptDirs {
		dirTbl.RawSetInt(i+1, lua.LString(dir))
	}
	L.SetField(mod, "__allowed_dirs", dirTbl)

	L.SetGlobal("fs", mod)
}

// bridgeFSReadFile implements fs.read_file(path) → content_string.
func bridgeFSReadFile(L *lua.LState) int {
	path := L.CheckString(1)

	absPath, err := resolveFSPath(path, L)
	if err != nil {
		pushFSError(L, err.Error())
		return 2
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		pushFSError(L, fmt.Sprintf("read_file失败: %v", err))
		return 2
	}

	L.Push(lua.LString(string(data)))
	return 1
}

// bridgeFSWriteFile implements fs.write_file(path, content).
func bridgeFSWriteFile(L *lua.LState) int {
	path := L.CheckString(1)
	content := L.CheckString(2)

	absPath, err := resolveFSPath(path, L)
	if err != nil {
		pushFSError(L, err.Error())
		return 2
	}

	if err := writeFileBytes(absPath, []byte(content)); err != nil {
		pushFSError(L, fmt.Sprintf("write_file失败: %v", err))
		return 2
	}

	L.Push(lua.LBool(true))
	return 1
}

// bridgeFSListDir implements fs.list_dir(path) → {name1, name2, ...}.
func bridgeFSListDir(L *lua.LState) int {
	path := L.CheckString(1)

	absPath, err := resolveFSPath(path, L)
	if err != nil {
		pushFSError(L, err.Error())
		return 2
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		pushFSError(L, fmt.Sprintf("list_dir失败: %v", err))
		return 2
	}

	tbl := L.NewTable()
	for i, entry := range entries {
		L.RawSetInt(tbl, i+1, lua.LString(entry.Name()))
	}
	L.Push(tbl)
	return 1
}

// bridgeFSFileExists implements fs.file_exists(path) → boolean.
func bridgeFSFileExists(L *lua.LState) int {
	path := L.CheckString(1)

	absPath, err := resolveFSPath(path, L)
	if err != nil {
		L.Push(lua.LFalse)
		return 1
	}

	_, err = os.Stat(absPath)
	L.Push(lua.LBool(err == nil))
	return 1
}

// bridgeFSIsDir implements fs.is_dir(path) → boolean.
func bridgeFSIsDir(L *lua.LState) int {
	path := L.CheckString(1)

	absPath, err := resolveFSPath(path, L)
	if err != nil {
		L.Push(lua.LFalse)
		return 1
	}

	info, err := os.Stat(absPath)
	if err != nil {
		L.Push(lua.LFalse)
		return 1
	}

	L.Push(lua.LBool(info.IsDir()))
	return 1
}

// bridgeFSMkdir implements fs.mkdir(path).
func bridgeFSMkdir(L *lua.LState) int {
	path := L.CheckString(1)
	// Optional mode parameter (default 0755).
	mode := os.FileMode(0755)
	if L.GetTop() >= 2 {
		mode = os.FileMode(L.CheckInt(2))
	}

	absPath, err := resolveFSPath(path, L)
	if err != nil {
		pushFSError(L, err.Error())
		return 2
	}

	if err := os.MkdirAll(absPath, mode); err != nil {
		pushFSError(L, fmt.Sprintf("mkdir失败: %v", err))
		return 2
	}

	L.Push(lua.LBool(true))
	return 1
}

// bridgeFSRemove implements fs.remove(path) → boolean.
func bridgeFSRemove(L *lua.LState) int {
	path := L.CheckString(1)

	absPath, err := resolveFSPath(path, L)
	if err != nil {
		pushFSError(L, err.Error())
		return 2
	}

	if err := os.RemoveAll(absPath); err != nil {
		pushFSError(L, fmt.Sprintf("remove失败: %v", err))
		return 2
	}

	L.Push(lua.LBool(true))
	return 1
}

// bridgeFSCopy implements fs.copy(src, dst).
func bridgeFSCopy(L *lua.LState) int {
	src := L.CheckString(1)
	dst := L.CheckString(2)

	absSrc, err := resolveFSPath(src, L)
	if err != nil {
		pushFSError(L, err.Error())
		return 2
	}

	absDst, err := resolveFSPath(dst, L)
	if err != nil {
		pushFSError(L, err.Error())
		return 2
	}

	if err := copyFile(absSrc, absDst); err != nil {
		pushFSError(L, fmt.Sprintf("copy失败: %v", err))
		return 2
	}

	L.Push(lua.LBool(true))
	return 1
}

// bridgeFSMove implements fs.move(src, dst).
func bridgeFSMove(L *lua.LState) int {
	src := L.CheckString(1)
	dst := L.CheckString(2)

	absSrc, err := resolveFSPath(src, L)
	if err != nil {
		pushFSError(L, err.Error())
		return 2
	}

	absDst, err := resolveFSPath(dst, L)
	if err != nil {
		pushFSError(L, err.Error())
		return 2
	}

	// Create parent directory if needed.
	dir := filepath.Dir(absDst)
	if err := os.MkdirAll(dir, 0755); err != nil {
		pushFSError(L, fmt.Sprintf("move失败(创建目录): %v", err))
		return 2
	}

	if err := os.Rename(absSrc, absDst); err != nil {
		pushFSError(L, fmt.Sprintf("move失败: %v", err))
		return 2
	}

	L.Push(lua.LBool(true))
	return 1
}

// bridgeFSStat implements fs.stat(path) → {size=N, is_dir=B, mod_time=S}.
func bridgeFSStat(L *lua.LState) int {
	path := L.CheckString(1)

	absPath, err := resolveFSPath(path, L)
	if err != nil {
		pushFSError(L, err.Error())
		return 2
	}

	info, err := os.Stat(absPath)
	if err != nil {
		pushFSError(L, fmt.Sprintf("stat失败: %v", err))
		return 2
	}

	tbl := L.NewTable()
	L.SetField(tbl, "size", lua.LNumber(info.Size()))
	L.SetField(tbl, "is_dir", lua.LBool(info.IsDir()))
	L.SetField(tbl, "mod_time", lua.LString(info.ModTime().Format("2006-01-02T15:04:05Z07:00")))
	L.SetField(tbl, "mode", lua.LString(info.Mode().String()))
	L.Push(tbl)
	return 1
}

// resolveFSPath resolves and validates a filesystem path:
//   - Relative paths are resolved against CWD.
//   - When allowed_script_dirs are configured, the resolved absolute path
//     must be under one of the allowed directories.
func resolveFSPath(path string, L *lua.LState) (string, error) {
	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("路径解析失败: %w", err)
	}

	dirsTbl := L.GetField(L.GetGlobal("fs"), "__allowed_dirs")
	if dirTbl, ok := dirsTbl.(*lua.LTable); ok && dirTbl.Len() > 0 {
		allowed := false
		dirTbl.ForEach(func(_ lua.LValue, val lua.LValue) {
			if dirStr, ok := val.(lua.LString); ok {
				absDir, err := filepath.Abs(filepath.Clean(string(dirStr)))
				if err != nil {
					return
				}
				prefix := absDir
				if !strings.HasSuffix(prefix, string(filepath.Separator)) {
					prefix += string(filepath.Separator)
				}
				if strings.HasPrefix(absPath, prefix) || absPath == absDir {
					allowed = true
				}
			}
		})

		if !allowed {
			return "", fmt.Errorf("路径不在允许的脚本目录中: %s", path)
		}
		return absPath, nil
	}

	return absPath, nil
}

// copyFile copies a file from src to dst, creating parent directories if needed.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// pushFSError pushes an fs module error onto the Lua stack.
func pushFSError(L *lua.LState, msg string) {
	L.Push(lua.LNil)
	errTbl := L.NewTable()
	L.SetField(errTbl, "type", lua.LString(ErrTypeBridge))
	L.SetField(errTbl, "module", lua.LString("fs"))
	L.SetField(errTbl, "message", lua.LString(msg))
	L.Push(errTbl)
}