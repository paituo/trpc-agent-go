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

// bridgeReFind implements re.find(s, pattern) → found, cap1, cap2, ...
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
		L.Push(lua.LBool(false))
		return 1
	}

	L.Push(lua.LBool(true))
	for i := 1; i < len(submatch); i++ {
		L.Push(lua.LString(submatch[i]))
	}
	return len(submatch)
}

// bridgeReGsub implements re.gsub(s, pattern, replacement) → string.
// Only string replacement is supported (no function callback).
// Capture groups can be referenced with $1, $2, etc.
func bridgeReGsub(L *lua.LState) int {
	s := L.CheckString(1)
	pattern := L.CheckString(2)
	replacement := L.CheckString(3)

	re, err := regexp.Compile(pattern)
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("re.gsub: invalid pattern %q: %v", pattern, err))
		return 2
	}

	result := re.ReplaceAllString(s, replacement)
	L.Push(lua.LString(result))
	return 1
}

// bridgeReMatches implements re.matches(s, pattern) → bool.
func bridgeReMatches(L *lua.LState) int {
	s := L.CheckString(1)
	pattern := L.CheckString(2)

	re, err := regexp.Compile(pattern)
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("re.matches: invalid pattern %q: %v", pattern, err))
		return 2
	}

	L.Push(lua.LBool(re.MatchString(s)))
	return 1
}
