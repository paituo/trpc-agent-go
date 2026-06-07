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
	"bytes"
	"encoding/json"
	"fmt"

	lua "github.com/yuin/gopher-lua"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/encoding/simplifiedchinese"
	"gopkg.in/yaml.v3"
)

// registerYAMLBridge registers the yaml module in the Lua VM.
// When allowIO is false, read_file and write_file are not available.
func registerYAMLBridge(L *lua.LState, allowIO bool) {
	mod := L.NewTable()
	L.SetField(mod, "decode", L.NewFunction(bridgeYamlDecode))
	L.SetField(mod, "encode", L.NewFunction(bridgeYamlEncode))
	if allowIO {
		L.SetField(mod, "read_file", L.NewFunction(bridgeYamlReadFile))
		L.SetField(mod, "write_file", L.NewFunction(bridgeYamlWriteFile))
	}
	L.SetGlobal("yaml", mod)
}

// bridgeYamlDecode implements yaml.decode(yaml_string).
func bridgeYamlDecode(L *lua.LState) int {
	input := L.CheckString(1)

	var data any
	if err := yaml.Unmarshal([]byte(input), &data); err != nil {
		pushBridgeError(L, fmt.Sprintf("yaml.decode failed: %v", err))
		return 2
	}

	pushGoValue(L, data)
	return 1
}

// bridgeYamlEncode implements yaml.encode(table).
func bridgeYamlEncode(L *lua.LState) int {
	tbl := L.CheckTable(1)
	goVal := lValueToGo(tbl)

	out, err := yaml.Marshal(goVal)
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("yaml.encode failed: %v", err))
		return 2
	}

	L.Push(lua.LString(string(out)))
	return 1
}

// bridgeYamlReadFile implements yaml.read_file(path [, encoding]).
func bridgeYamlReadFile(L *lua.LState) int {
	path := L.CheckString(1)
	encoding := L.OptString(2, "utf-8")

	content, err := readFileWithEncoding(path, encoding)
	if err != nil {
		pushEncodingError(L, path, encoding, err)
		return 2
	}

	var data any
	if err := yaml.Unmarshal([]byte(content), &data); err != nil {
		pushBridgeError(L, fmt.Sprintf("yaml.decode(%s) failed: %v", path, err))
		return 2
	}

	pushGoValue(L, data)
	return 1
}

// bridgeYamlWriteFile implements yaml.write_file(path, table).
func bridgeYamlWriteFile(L *lua.LState) int {
	path := L.CheckString(1)
	tbl := L.CheckTable(2)
	goVal := lValueToGo(tbl)

	out, err := yaml.Marshal(goVal)
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("yaml.encode failed: %v", err))
		return 2
	}

	if err := writeFileSafe(path, out); err != nil {
		pushBridgeError(L, fmt.Sprintf("yaml.write_file(%s) failed: %v", path, err))
		return 2
	}

	L.Push(lua.LBool(true))
	return 1
}

// registerJSONBridge registers the json module in the Lua VM.
func registerJSONBridge(L *lua.LState) {
	mod := L.NewTable()
	L.SetField(mod, "decode", L.NewFunction(bridgeJsonDecode))
	L.SetField(mod, "encode", L.NewFunction(bridgeJsonEncode))
	L.SetGlobal("json", mod)
}

// bridgeJsonDecode implements json.decode(json_string).
func bridgeJsonDecode(L *lua.LState) int {
	input := L.CheckString(1)

	var data any
	if err := json.Unmarshal([]byte(input), &data); err != nil {
		pushBridgeError(L, fmt.Sprintf("json.decode failed: %v", err))
		return 2
	}

	pushGoValue(L, data)
	return 1
}

// bridgeJsonEncode implements json.encode(table [, options]).
func bridgeJsonEncode(L *lua.LState) int {
	tbl := L.CheckTable(1)
	goVal := lValueToGo(tbl)

	out, err := json.Marshal(goVal)
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("json.encode failed: %v", err))
		return 2
	}

	L.Push(lua.LString(string(out)))
	return 1
}

// readFileWithEncoding reads a file and decodes it from the specified encoding.
func readFileWithEncoding(path, encoding string) (string, error) {
	raw, err := readFileSafe(path)
	if err != nil {
		return "", err
	}

	switch encoding {
	case "utf-8":
		return string(raw), nil
	case "utf-8-bom":
		return string(bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})), nil
	case "gbk":
		decoder := simplifiedchinese.GBK.NewDecoder()
		decoded, err := decoder.Bytes(raw)
		if err != nil {
			return "", err
		}
		return string(decoded), nil
	default:
		enc, _ := htmlindex.Get(encoding)
		if enc == nil {
			return "", fmt.Errorf("unsupported encoding: %s", encoding)
		}
		decoded, err := enc.NewDecoder().Bytes(raw)
		if err != nil {
			return "", err
		}
		return string(decoded), nil
	}
}

// readFileSafe reads a file safely (will be replaced with workspace-aware path in future).
func readFileSafe(path string) ([]byte, error) {
	return readFileBytes(path)
}

// writeFileSafe writes a file safely.
func writeFileSafe(path string, data []byte) error {
	return writeFileBytes(path, data)
}

// pushEncodingError pushes an encoding error onto the Lua stack.
func pushEncodingError(L *lua.LState, path, encoding string, err error) {
	L.Push(lua.LNil)
	errTbl := L.NewTable()
	L.SetField(errTbl, "type", lua.LString(ErrTypeEncoding))
	L.SetField(errTbl, "message", lua.LString(fmt.Sprintf("encoding %s for %s: %v", encoding, path, err)))
	L.Push(errTbl)
}
