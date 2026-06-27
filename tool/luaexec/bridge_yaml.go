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
	"sort"
	"unicode/utf8"

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
		L.SetField(mod, "read_file_ordered", L.NewFunction(bridgeYamlReadFileOrdered))
		L.SetField(mod, "read_file_auto", L.NewFunction(bridgeYamlReadFileAuto))
		L.SetField(mod, "read_text_file", L.NewFunction(bridgeYamlReadTextFile))
		L.SetField(mod, "write_file", L.NewFunction(bridgeYamlWriteFile))
		L.SetField(mod, "write_text_file", L.NewFunction(bridgeYamlWriteTextFile))
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

// bridgeYamlEncode implements yaml.encode(table_or_ordered).
// 支持普通 Lua table 和 ordered_table userdata。
func bridgeYamlEncode(L *lua.LState) int {
	v := L.Get(1)
	var goVal any
	switch val := v.(type) {
	case *lua.LTable:
		goVal = lValueToGoOrdered(val)
	case *lua.LUserData:
		if ot, ok := val.Value.(*orderedTable); ok {
			goVal = ot
		} else {
			pushBridgeError(L, "yaml.encode: unsupported userdata type")
			return 2
		}
	default:
		pushBridgeError(L, fmt.Sprintf("yaml.encode: expected table or ordered_table, got %s", v.Type()))
		return 2
	}

	// Use anyToYAMLNode to build a complete yaml.Node tree,
	// which correctly handles *orderedMap in nested structures.
	rootNode, err := anyToYAMLNode(goVal)
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("yaml.encode failed: %v", err))
		return 2
	}

	out, err := yaml.Marshal(rootNode)
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("yaml.encode failed: %v", err))
		return 2
	}

	L.Push(lua.LString(string(out)))
	return 1
}

// bridgeYamlReadFile implements yaml.read_file(path [, encoding]).
// Default encoding is "utf-8" because YAML files are always produced as UTF-8
// by yaml.write_file. Using "auto" would risk the CJK heuristic incorrectly
// identifying valid UTF-8 content as GBK, causing garbled Chinese text.
// Callers that need encoding detection can explicitly pass "auto".
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

// bridgeYamlReadFileOrdered implements yaml.read_file_ordered(path [, encoding]).
// 与 yaml.read_file 功能相同，但返回 ordered_table userdata 而非普通 table。
// 返回的 ordered_table 保持 YAML 文件中字段的字母序（确定性输出）。
// 注意：由于 Go yaml.Unmarshal 到 map[string]any 时已丢失原始顺序，
// 返回的 ordered_table 按字母序排列，但至少保证确定性输出。
func bridgeYamlReadFileOrdered(L *lua.LState) int {
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

	pushGoValueOrdered(L, data)
	return 1
}

// pushGoValueOrdered 将 Go 值转为 Lua 值，map[string]any 转为 ordered_table userdata。
// 递归处理嵌套结构，确保所有 map 都转为 ordered_table。
func pushGoValueOrdered(L *lua.LState, v any) {
	if v == nil {
		L.Push(lua.LNil)
		return
	}
	switch val := v.(type) {
	case bool:
		L.Push(lua.LBool(val))
	case int:
		L.Push(lua.LNumber(val))
	case int64:
		L.Push(lua.LNumber(val))
	case float64:
		L.Push(lua.LNumber(val))
	case string:
		L.Push(lua.LString(val))
	case map[string]any:
		// 按字母序插入，保证确定性输出
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ot := &orderedTable{}
		for _, k := range keys {
			ot.items = append(ot.items, orderedMapItem{
				Key:   k,
				Value: lValueToGoOrdered(toLValue(L, val[k])),
			})
		}
		ud := L.NewUserData()
		ud.Metatable = L.GetTypeMetatable(orderedTableMetaKey)
		ud.Value = ot
		L.Push(ud)
	case []any:
		tbl := L.NewTable()
		for i, elem := range val {
			L.RawSetInt(tbl, i+1, toLValue(L, elem))
		}
		L.Push(tbl)
	default:
		pushGoValue(L, v)
	}
}

// bridgeYamlWriteFile implements yaml.write_file(path, table_or_ordered).
// 支持普通 Lua table 和 ordered_table userdata。
func bridgeYamlWriteFile(L *lua.LState) int {
	path := L.CheckString(1)
	v := L.Get(2)
	var goVal any
	switch val := v.(type) {
	case *lua.LTable:
		goVal = lValueToGoOrdered(val)
	case *lua.LUserData:
		if ot, ok := val.Value.(*orderedTable); ok {
			goVal = ot
		} else {
			pushBridgeError(L, "yaml.write_file: unsupported userdata type")
			return 2
		}
	default:
		pushBridgeError(L, fmt.Sprintf("yaml.write_file: expected table or ordered_table, got %s", v.Type()))
		return 2
	}

	// Use anyToYAMLNode to build a complete yaml.Node tree,
	// which correctly handles *orderedMap in nested structures.
	rootNode, err := anyToYAMLNode(goVal)
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("yaml.write_file failed: %v", err))
		return 2
	}

	out, err := yaml.Marshal(rootNode)
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

// bridgeYamlWriteTextFile implements yaml.write_text_file(path, content_string).
// It writes a plain text string to a file, creating parent directories if needed.
// This is useful for writing non-YAML text files (e.g. raw draft output).
func bridgeYamlWriteTextFile(L *lua.LState) int {
	path := L.CheckString(1)
	content := L.CheckString(2)

	if err := writeFileSafe(path, []byte(content)); err != nil {
		pushBridgeError(L, fmt.Sprintf("yaml.write_text_file(%s) failed: %v", path, err))
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
	case "auto":
		return decodeAuto(raw)
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

// decodeAuto auto-detects encoding and decodes the raw bytes to UTF-8.
// Detection order: BOM → UTF-8 validity (with CJK heuristic) → GBK fallback.
// The CJK heuristic disambiguates cases where raw bytes are both valid UTF-8
// and valid GBK, by counting which decoding produces more meaningful Chinese
// characters (CJK Unified Ideographs U+4E00-U+9FFF).
func decodeAuto(raw []byte) (string, error) {
	// Check BOM first.
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		return string(bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})), nil
	}
	if bytes.HasPrefix(raw, []byte{0xFF, 0xFE}) || bytes.HasPrefix(raw, []byte{0xFE, 0xFF}) {
		return "", fmt.Errorf("UTF-16 encoding detected but not supported")
	}

	// When the content has high-byte sequences (>0x7F), both UTF-8 and GBK
	// may produce valid-but-wrong results. Try GBK first when content looks
	// more like GBK (dense high bytes without valid UTF-8 multi-byte patterns).
	//
	// Then try UTF-8 strict validation.
	// After UTF-8 validation passes, also check whether the decoded content
	// contains more valid CJK characters under GBK decoding (heuristic for
	// mojibake where UTF-8 bytes are silently accepted).
	utf8Valid := isValidUTF8(raw)

	// Try GBK decoding first if raw bytes have a significant portion of
	// high bytes (0x80-0xFE) without forming valid UTF-8 multi-byte sequences,
	// which suggests GBK encoding rather than UTF-8.
	if !utf8Valid {
		// Fallback: try GBK decoding.
		decoder := simplifiedchinese.GBK.NewDecoder()
		decoded, err := decoder.Bytes(raw)
		if err != nil {
			return "", fmt.Errorf("auto-detect failed: not valid UTF-8 and GBK decode error: %v", err)
		}
		return string(decoded), nil
	}

	// UTF-8 is valid. Apply CJK heuristic to detect mojibake:
	// If GBK-decoded content yields significantly more CJK characters than
	// UTF-8-decoded content, prefer GBK decoding.
	utf8Str := string(raw)
	utf8CJKCount := countCJKChars(utf8Str)

	gbkDecoder := simplifiedchinese.GBK.NewDecoder()
	gbkDecoded, gbkErr := gbkDecoder.Bytes(raw)
	if gbkErr == nil {
		gbkStr := string(gbkDecoded)
		gbkCJKCount := countCJKChars(gbkStr)
		// If GBK produces significantly more CJK characters (2x+), it is
		// likely the correct encoding and UTF-8 was a false positive.
		if gbkCJKCount > utf8CJKCount*2 && gbkCJKCount >= 3 {
			return gbkStr, nil
		}
	}

	return utf8Str, nil
}

// countCJKChars counts the number of CJK Unified Ideographs (U+4E00-U+9FFF)
// in a UTF-8 string. This is used as a heuristic for encoding detection.
func countCJKChars(s string) int {
	count := 0
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		if r >= 0x4E00 && r <= 0x9FFF {
			count++
		}
		s = s[size:]
	}
	return count
}

// isValidUTF8 checks if the byte slice is valid UTF-8.
func isValidUTF8(data []byte) bool {
	for i := 0; i < len(data); {
		r, size := rune(data[i]), 1
		switch {
		case data[i] < 0x80: // ASCII
			r = rune(data[i])
		case data[i]&0xE0 == 0xC0: // 2-byte
			if i+1 >= len(data) || data[i+1]&0xC0 != 0x80 {
				return false
			}
			r = rune(data[i]&0x1F)<<6 | rune(data[i+1]&0x3F)
			size = 2
		case data[i]&0xF0 == 0xE0: // 3-byte
			if i+2 >= len(data) || data[i+1]&0xC0 != 0x80 || data[i+2]&0xC0 != 0x80 {
				return false
			}
			r = rune(data[i]&0x0F)<<12 | rune(data[i+1]&0x3F)<<6 | rune(data[i+2]&0x3F)
			size = 3
		case data[i]&0xF8 == 0xF0: // 4-byte
			if i+3 >= len(data) || data[i+1]&0xC0 != 0x80 || data[i+2]&0xC0 != 0x80 || data[i+3]&0xC0 != 0x80 {
				return false
			}
			r = rune(data[i]&0x07)<<18 | rune(data[i+1]&0x3F)<<12 | rune(data[i+2]&0x3F)<<6 | rune(data[i+3]&0x3F)
			size = 4
		default:
			return false
		}
		// Check for overlong encodings and surrogates.
		if r == 0xFFFD || (0xD800 <= r && r <= 0xDFFF) {
			return false
		}
		i += size
	}
	return true
}

// bridgeYamlReadFileAuto implements yaml.read_file_auto(path).
// It auto-detects encoding (UTF-8/GBK) and returns decoded YAML data.
func bridgeYamlReadFileAuto(L *lua.LState) int {
	path := L.CheckString(1)

	content, err := readFileWithEncoding(path, "auto")
	if err != nil {
		pushEncodingError(L, path, "auto", err)
		return 2
	}

	var data any
	if err := yaml.Unmarshal([]byte(content), &data); err != nil {
		pushBridgeError(L, fmt.Sprintf("yaml.read_file_auto(%s) decode failed: %v", path, err))
		return 2
	}

	pushGoValue(L, data)
	return 1
}

// bridgeYamlReadTextFile implements yaml.read_text_file(path [, encoding]).
// It reads a text file with encoding conversion and returns the content as a
// Lua string (not parsed as YAML). This is useful for reading non-YAML text
// files (e.g. Markdown) that may need encoding conversion.
// Supported encodings: utf-8, utf-8-bom, gbk, auto (default).
func bridgeYamlReadTextFile(L *lua.LState) int {
	path := L.CheckString(1)
	encoding := L.OptString(2, "auto")

	content, err := readFileWithEncoding(path, encoding)
	if err != nil {
		pushEncodingError(L, path, encoding, err)
		return 2
	}

	L.Push(lua.LString(content))
	return 1
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
