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

// Command represents a slash command definition.
type Command struct {
	Name        string        // e.g., "review" (without the / prefix)
	Description string        // Human-readable description
	Body        string        // The prompt template body
	Source      CommandSource // Where this command was loaded from
	Thinking    bool          // Whether to enable thinking mode for this command
}

// CommandSource indicates where a command was defined.
type CommandSource string

const (
	SourceBuiltin CommandSource = "builtin"
	SourceProject CommandSource = "project"
	SourceUser    CommandSource = "user"
)

// CommandCall represents a parsed slash command invocation.
type CommandCall struct {
	Name    string   // The command name (without /)
	Args    []string // Positional arguments
	RawArgs string   // The raw argument string
}
