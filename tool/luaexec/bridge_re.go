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
	"regexp"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// registerReBridge registers the re module (Go regexp wrapper) in the Lua VM.
func registerReBridge(L *lua.LState) {
	mod := L.NewTable()
	L.SetField(mod, "match", L.NewFunction(bridgeReMatch))
	L.SetField(mod, "find", L.NewFunction(bridgeReFind))
	L.SetField(mod, "gsub", L.NewFunction(bridgeReGsub))
	L.SetField(mod, "matches", L.NewFunction(bridgeReMatches))
	L.SetGlobal("re", mod)
}

// bridgeReMatch implements re.match(s, pattern) → array of matches.
// Returns all non-overlapping matches of pattern in s as an array.
// Equivalent to Python re.findall() without capture groups.
func bridgeReMatch(L *lua.LState) int {
	s := L.CheckString(1)
	pattern := L.CheckString(2)

	re, err := regexp.Compile(pattern)
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("re.match: invalid pattern %q: %v", pattern, err))
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

// bridgeReFind implements re.find(s, pattern) → full_match, cap1, cap2, ...
// Returns the first match: full matched string + capture groups.
// Returns nil if no match found.
// Equivalent to Python re.search() behavior.
func bridgeReFind(L *lua.LState) int {
	s := L.CheckString(1)
	pattern := L.CheckString(2)

	re, err := regexp.Compile(pattern)
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("re.find: invalid pattern %q: %v", pattern, err))
		return 2
	}

	submatch := re.FindStringSubmatch(s)
	if submatch == nil {
		L.Push(lua.LNil)
		return 1
	}

	// Return: full_match, cap1, cap2, ...
	L.Push(lua.LString(submatch[0]))
	for i := 1; i < len(submatch); i++ {
		L.Push(lua.LString(submatch[i]))
	}
	return len(submatch)
}

// bridgeReGsub implements re.gsub(s, pattern, replacement) → string.
// Only string replacement is supported (no function callback).
// Capture groups can be referenced with $1, $2, etc.
// Note: $N is automatically converted to ${N} for Go regexp compatibility,
// so both $1 and ${1} syntaxes work.
func bridgeReGsub(L *lua.LState) int {
	s := L.CheckString(1)
	pattern := L.CheckString(2)
	replacement := L.CheckString(3)

	re, err := regexp.Compile(pattern)
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("re.gsub: invalid pattern %q: %v", pattern, err))
		return 2
	}

	// Auto-convert $N to ${N} for Go regexp compatibility.
	// Go's regexp requires ${1} syntax when $1 is followed by digits,
	// but LLMs commonly write $1, $2 etc. We normalize automatically.
	normalized := normalizeGsubReplacement(replacement)
	result := re.ReplaceAllString(s, normalized)
	L.Push(lua.LString(result))
	return 1
}

// bridgeReMatches implements re.matches(s, pattern) → array of all matches.
// Returns all non-overlapping matches as an array of strings.
// If pattern has capture groups, returns array of capture group arrays.
// Equivalent to Python re.findall() behavior.
func bridgeReMatches(L *lua.LState) int {
	s := L.CheckString(1)
	pattern := L.CheckString(2)

	re, err := regexp.Compile(pattern)
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("re.matches: invalid pattern %q: %v", pattern, err))
		return 2
	}

	// Check if pattern has capture groups.
	hasGroups := strings.Contains(pattern, "(")

	tbl := L.NewTable()
	if hasGroups {
		// Return array of capture group arrays.
		submatches := re.FindAllStringSubmatch(s, -1)
		for i, sub := range submatches {
			subTbl := L.NewTable()
			for j, s := range sub {
				L.RawSetInt(subTbl, j+1, lua.LString(s))
			}
			L.RawSetInt(tbl, i+1, subTbl)
		}
	} else {
		// Return array of matched strings.
		matches := re.FindAllString(s, -1)
		for i, m := range matches {
			L.RawSetInt(tbl, i+1, lua.LString(m))
		}
	}
	L.Push(tbl)
	return 1
}

// normalizeGsubReplacement converts $N references to ${N} for Go regexp.
// Go's regexp.Expand requires ${N} when N is followed by digits,
// but $1, $2 etc. are the common syntax in most languages.
// This function converts $1, $2, ... $9 to ${1}, ${2}, ... ${9}
// only when not already in ${N} form.
func normalizeGsubReplacement(s string) string {
	var sb strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '$' && i+1 < len(s) {
			// Check if already ${N} form.
			if s[i+1] == '{' {
				// Already ${...} form, copy as-is.
				sb.WriteByte(s[i])
				i++
				continue
			}
			// Check if $N form (N is digit 1-9).
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
