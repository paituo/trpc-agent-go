//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package commands

import "strings"

const commandPrefix = "/"

// ParseCall parses a user input line into a CommandCall.
// Returns nil if the input is not a slash command.
func ParseCall(text string) *CommandCall {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, commandPrefix) {
		return nil
	}

	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return nil
	}

	name := strings.TrimPrefix(fields[0], commandPrefix)
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return nil
	}

	rawArgs := strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0]))

	var args []string
	if rawArgs != "" {
		args = strings.Fields(rawArgs)
	}

	return &CommandCall{
		Name:    name,
		Args:    args,
		RawArgs: rawArgs,
	}
}

// IsCommand checks if the input starts with a slash command prefix.
func IsCommand(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, commandPrefix) && len(trimmed) > 1
}
