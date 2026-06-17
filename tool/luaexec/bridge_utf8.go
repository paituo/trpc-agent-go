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
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	lua "github.com/yuin/gopher-lua"
	"golang.org/x/text/cases"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/language"
)

// registerUTF8Bridge registers the utf8 module in the Lua VM.
// This module unifies character-level operations, regex matching (from re module),
// and encoding conversion — all UTF-8 text processing in one module.
func registerUTF8Bridge(L *lua.LState) {
	mod := L.NewTable()

	// Character-level operations (compatible with Lua 5.3 utf8 standard library)
	L.SetField(mod, "len", L.NewFunction(bridgeUTF8Len))
	L.SetField(mod, "sub", L.NewFunction(bridgeUTF8Sub))
	L.SetField(mod, "reverse", L.NewFunction(bridgeUTF8Reverse))
	L.SetField(mod, "upper", L.NewFunction(bridgeUTF8Upper))
	L.SetField(mod, "lower", L.NewFunction(bridgeUTF8Lower))
	L.SetField(mod, "char", L.NewFunction(bridgeUTF8Char))
	L.SetField(mod, "codepoint", L.NewFunction(bridgeUTF8Codepoint))
	L.SetField(mod, "codes", L.NewFunction(bridgeUTF8Codes))
	L.SetField(mod, "byteoffset", L.NewFunction(bridgeUTF8Byteoffset))
	L.SetField(mod, "validate", L.NewFunction(bridgeUTF8Validate))

	// Regex matching (from re module, Go regexp natively supports UTF-8)
	L.SetField(mod, "find", L.NewFunction(bridgeUTF8Find))
	L.SetField(mod, "match", L.NewFunction(bridgeUTF8Match))
	L.SetField(mod, "gsub", L.NewFunction(bridgeUTF8Gsub))
	L.SetField(mod, "matches", L.NewFunction(bridgeUTF8Matches))

	// Encoding conversion
	L.SetField(mod, "encode", L.NewFunction(bridgeUTF8Encode))
	L.SetField(mod, "decode", L.NewFunction(bridgeUTF8Decode))
	L.SetField(mod, "detect", L.NewFunction(bridgeUTF8Detect))

	L.SetGlobal("utf8", mod)

	// Backward compatibility: register re as alias for utf8
	L.SetGlobal("re", mod)
}

// bridgeUTF8Len implements utf8.len(s) → number of characters.
func bridgeUTF8Len(L *lua.LState) int {
	s := L.CheckString(1)
	L.Push(lua.LNumber(utf8.RuneCountInString(s)))
	return 1
}

// bridgeUTF8Sub implements utf8.sub(s, i, j) → substring by character index.
// utf8SubByChar returns the substring of s from character index i to the end.
func utf8SubByChar(s string, i int, j int) string {
	n := utf8.RuneCountInString(s)
	if j == 0 {
		j = n
	}
	if i < 0 {
		i = n + i + 1
	}
	if j < 0 {
		j = n + j + 1
	}
	if i < 1 {
		i = 1
	}
	if j > n {
		j = n
	}
	if i > j {
		return ""
	}
	startByte := 0
	for k := 1; k < i; k++ {
		_, size := utf8.DecodeRuneInString(s[startByte:])
		startByte += size
	}
	endByte := startByte
	for k := i; k <= j; k++ {
		_, size := utf8.DecodeRuneInString(s[endByte:])
		endByte += size
	}
	return s[startByte:endByte]
}

func bridgeUTF8Sub(L *lua.LState) int {
	s := L.CheckString(1)
	i := L.CheckInt(2)
	j := L.OptInt(3, 0)
	L.Push(lua.LString(utf8SubByChar(s, i, j)))
	return 1
}

// bridgeUTF8Reverse implements utf8.reverse(s) → reversed string by character.
func bridgeUTF8Reverse(L *lua.LState) int {
	s := L.CheckString(1)
	runes := []rune(s)
	var buf strings.Builder
	buf.Grow(len(s))
	for i := len(runes) - 1; i >= 0; i-- {
		buf.WriteRune(runes[i])
	}
	L.Push(lua.LString(buf.String()))
	return 1
}

// bridgeUTF8Upper implements utf8.upper(s) → uppercase string.
func bridgeUTF8Upper(L *lua.LState) int {
	s := L.CheckString(1)
	L.Push(lua.LString(cases.Upper(language.Und).String(s)))
	return 1
}

// bridgeUTF8Lower implements utf8.lower(s) → lowercase string.
func bridgeUTF8Lower(L *lua.LState) int {
	s := L.CheckString(1)
	L.Push(lua.LString(cases.Lower(language.Und).String(s)))
	return 1
}

// bridgeUTF8Char implements utf8.char(cp1, cp2, ...) → string from codepoints.
func bridgeUTF8Char(L *lua.LState) int {
	n := L.GetTop()
	var buf strings.Builder
	for i := 1; i <= n; i++ {
		cp := L.CheckInt(i)
		buf.WriteRune(rune(cp))
	}
	L.Push(lua.LString(buf.String()))
	return 1
}

// bridgeUTF8Codepoint implements utf8.codepoint(s, i, j) → codepoints.
func bridgeUTF8Codepoint(L *lua.LState) int {
	s := L.CheckString(1)
	i := L.OptInt(2, 1)
	j := L.OptInt(3, i)

	n := utf8.RuneCountInString(s)
	if i < 1 {
		i = 1
	}
	if j > n {
		j = n
	}

	byteIdx := 0
	for k := 1; k < i; k++ {
		_, size := utf8.DecodeRuneInString(s[byteIdx:])
		byteIdx += size
	}

	count := 0
	for k := i; k <= j; k++ {
		r, size := utf8.DecodeRuneInString(s[byteIdx:])
		L.Push(lua.LNumber(r))
		byteIdx += size
		count++
	}
	return count
}

// utf8CodesState holds iteration state for utf8.codes iterator.
type utf8CodesState struct {
	str     string
	offsets []int
	runes   []rune
	pos     int
}

// bridgeUTF8Codes implements utf8.codes(s) → iterator, state, 0.
func bridgeUTF8Codes(L *lua.LState) int {
	s := L.CheckString(1)
	runes := []rune(s)

	offsets := make([]int, len(runes))
	byteIdx := 0
	for i, r := range runes {
		offsets[i] = byteIdx + 1
		byteIdx += utf8.RuneLen(r)
	}

	state := &utf8CodesState{
		str:     s,
		offsets: offsets,
		runes:   runes,
		pos:     0,
	}

	ud := L.NewUserData()
	ud.Value = state

	iterFn := L.NewClosure(bridgeUTF8CodesIter, ud)
	L.Push(iterFn)
	L.Push(ud)
	L.Push(lua.LNumber(0))
	return 3
}

func bridgeUTF8CodesIter(L *lua.LState) int {
	ud := L.CheckUserData(1)
	state, ok := ud.Value.(*utf8CodesState)
	if !ok || state.pos >= len(state.runes) {
		return 0
	}

	L.Push(lua.LNumber(state.offsets[state.pos]))
	L.Push(lua.LNumber(state.runes[state.pos]))
	state.pos++
	return 2
}

// bridgeUTF8Byteoffset implements utf8.byteoffset(s, n) → byte offset of nth character.
func bridgeUTF8Byteoffset(L *lua.LState) int {
	s := L.CheckString(1)
	n := L.CheckInt(2)

	if n < 1 {
		L.Push(lua.LNil)
		return 1
	}

	byteIdx := 0
	for i := 0; i < n-1; i++ {
		_, size := utf8.DecodeRuneInString(s[byteIdx:])
		if size == 0 {
			L.Push(lua.LNil)
			return 1
		}
		byteIdx += size
	}

	if byteIdx > len(s) {
		L.Push(lua.LNil)
		return 1
	}

	L.Push(lua.LNumber(byteIdx + 1))
	return 1
}

// bridgeUTF8Validate implements utf8.validate(s) → boolean.
func bridgeUTF8Validate(L *lua.LState) int {
	s := L.CheckString(1)
	L.Push(lua.LBool(utf8.ValidString(s)))
	return 1
}

// bridgeUTF8Find implements utf8.find(s, pattern [, init]) → full_match, cap1, cap2, ... or nil.
// Returns the first match: full matched string + capture groups.
// Returns nil if no match found.
// init is an optional 1-based character index (negative means counting from end).
func bridgeUTF8Find(L *lua.LState) int {
	s := L.CheckString(1)
	pattern := L.CheckString(2)

	re, err := regexp.Compile(pattern)
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("utf8.find: invalid pattern %q: %v", pattern, err))
		return 2
	}

	// Handle optional init parameter (1-based character index).
	if L.GetTop() >= 3 {
		initChar := L.CheckInt(3)
		searchStr := utf8SubByChar(s, initChar, 0)
		submatch := re.FindStringSubmatch(searchStr)
		if submatch == nil {
			L.Push(lua.LNil)
			return 1
		}
		L.Push(lua.LString(submatch[0]))
		for i := 1; i < len(submatch); i++ {
			L.Push(lua.LString(submatch[i]))
		}
		return len(submatch)
	}

	submatch := re.FindStringSubmatch(s)
	if submatch == nil {
		L.Push(lua.LNil)
		return 1
	}

	L.Push(lua.LString(submatch[0]))
	for i := 1; i < len(submatch); i++ {
		L.Push(lua.LString(submatch[i]))
	}
	return len(submatch)
}

// bridgeUTF8Match implements utf8.match(s, pattern) → array of matches.
func bridgeUTF8Match(L *lua.LState) int {
	s := L.CheckString(1)
	pattern := L.CheckString(2)

	re, err := regexp.Compile(pattern)
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("utf8.match: invalid pattern %q: %v", pattern, err))
		return 2
	}

	matches := re.FindAllString(s, -1)
	tbl := L.NewTable()
	for i, m := range matches {
		L.RawSetInt(tbl, i+1, lua.LString(m))
	}
	L.Push(tbl)
	return 1
}

// bridgeUTF8Gsub implements utf8.gsub(s, pattern, replacement) → string.
func bridgeUTF8Gsub(L *lua.LState) int {
	s := L.CheckString(1)
	pattern := L.CheckString(2)
	replacement := L.CheckString(3)

	re, err := regexp.Compile(pattern)
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("utf8.gsub: invalid pattern %q: %v", pattern, err))
		return 2
	}

	normalized := normalizeGsubReplacement(replacement)
	result := re.ReplaceAllString(s, normalized)
	L.Push(lua.LString(result))
	return 1
}

// bridgeUTF8Matches implements utf8.matches(s, pattern) → array of all matches.
func bridgeUTF8Matches(L *lua.LState) int {
	s := L.CheckString(1)
	pattern := L.CheckString(2)

	re, err := regexp.Compile(pattern)
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("utf8.matches: invalid pattern %q: %v", pattern, err))
		return 2
	}

	hasGroups := strings.Contains(pattern, "(")

	tbl := L.NewTable()
	if hasGroups {
		submatches := re.FindAllStringSubmatch(s, -1)
		for i, sub := range submatches {
			subTbl := L.NewTable()
			for j, s := range sub {
				L.RawSetInt(subTbl, j+1, lua.LString(s))
			}
			L.RawSetInt(tbl, i+1, subTbl)
		}
	} else {
		matches := re.FindAllString(s, -1)
		for i, m := range matches {
			L.RawSetInt(tbl, i+1, lua.LString(m))
		}
	}
	L.Push(tbl)
	return 1
}

// normalizeGsubReplacement converts $N references to ${N} for Go regexp.
func normalizeGsubReplacement(s string) string {
	var sb strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '$' && i+1 < len(s) {
			if s[i+1] == '{' {
				sb.WriteByte(s[i])
				i++
				continue
			}
			if s[i+1] >= '1' && s[i+1] <= '9' {
				sb.WriteString("${")
				sb.WriteByte(s[i+1])
				sb.WriteString("}")
				i += 2
				continue
			}
		}
		sb.WriteByte(s[i])
		i++
	}
	return sb.String()
}

// bridgeUTF8Encode implements utf8.encode(s, from, to) → encoded string.
func bridgeUTF8Encode(L *lua.LState) int {
	s := L.CheckString(1)
	from := L.CheckString(2)
	to := L.CheckString(3)

	var utf8Str string
	if isUTF8Name(from) {
		utf8Str = s
	} else {
		enc, err := getEncoder(from)
		if err != nil {
			pushBridgeError(L, fmt.Sprintf("utf8.encode: %v", err))
			return 2
		}
		decoded, err := enc.NewDecoder().Bytes([]byte(s))
		if err != nil {
			pushBridgeError(L, fmt.Sprintf("utf8.encode: decode from %s failed: %v", from, err))
			return 2
		}
		utf8Str = string(decoded)
	}

	if isUTF8Name(to) {
		L.Push(lua.LString(utf8Str))
		return 1
	}

	enc, err := getEncoder(to)
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("utf8.encode: %v", err))
		return 2
	}
	encoded, err := enc.NewEncoder().Bytes([]byte(utf8Str))
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("utf8.encode: encode to %s failed: %v", to, err))
		return 2
	}
	L.Push(lua.LString(string(encoded)))
	return 1
}

// bridgeUTF8Decode implements utf8.decode(s, from) → UTF-8 string.
func bridgeUTF8Decode(L *lua.LState) int {
	s := L.CheckString(1)
	from := L.CheckString(2)

	if isUTF8Name(from) {
		L.Push(lua.LString(s))
		return 1
	}

	enc, err := getEncoder(from)
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("utf8.decode: %v", err))
		return 2
	}
	decoded, err := enc.NewDecoder().Bytes([]byte(s))
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("utf8.decode: decode from %s failed: %v", from, err))
		return 2
	}
	L.Push(lua.LString(string(decoded)))
	return 1
}

// bridgeUTF8Detect implements utf8.detect(s) → encoding name.
func bridgeUTF8Detect(L *lua.LState) int {
	s := L.CheckString(1)
	raw := []byte(s)

	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		L.Push(lua.LString("utf-8-bom"))
		return 1
	}

	if utf8.Valid(raw) {
		L.Push(lua.LString("utf-8"))
		return 1
	}

	decoder := simplifiedchinese.GBK.NewDecoder()
	if _, err := decoder.Bytes(raw); err == nil {
		L.Push(lua.LString("gbk"))
		return 1
	}

	L.Push(lua.LString("unknown"))
	return 1
}

// isUTF8Name checks if the encoding name indicates UTF-8.
func isUTF8Name(name string) bool {
	return name == "utf-8" || name == "utf8" || name == "UTF-8" || name == "UTF8"
}

// getEncoder returns an encoding.Encoding for the given name.
func getEncoder(name string) (encoding.Encoding, error) {
	switch name {
	case "gbk", "GBK":
		return simplifiedchinese.GBK, nil
	default:
		enc, err := htmlindex.Get(name)
		if enc == nil {
			return nil, fmt.Errorf("unsupported encoding: %s", name)
		}
		return enc, err
	}
}