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

// RegisterBuiltinCommands registers the built-in slash commands.
func RegisterBuiltinCommands(r *Repository) {
	builtin := []struct {
		name        string
		description string
		body        string
	}{
		{
			name:        "help",
			description: "Show available commands",
			body:        "", // Handled specially by the channel
		},
		{
			name:        "clear",
			description: "Clear conversation history",
			body:        "", // Handled specially by the channel
		},
		{
			name:        "compact",
			description: "Compact conversation to save context",
			body:        "Summarize our conversation so far, preserving key decisions, code changes, and important context. Then continue from where we left off.",
		},
		{
			name:        "review",
			description: "Review code for issues and improvements",
			body:        "Review the following code for:\n\n1. **Security issues:** SQL injection, XSS, hardcoded secrets, insecure dependencies\n2. **Performance:** N+1 queries, memory leaks, unnecessary re-renders\n3. **Code quality:** Error handling, type safety, naming conventions\n4. **Best practices:** Design patterns, SOLID principles, DRY\n\nFormat findings as:\n- ✅ Looks good / ⚠️ Issue found\n- Specific line numbers\n- Suggested fix in code block\n\n$ARGUMENTS",
		},
		{
			name:        "init",
			description: "Initialize project with configuration",
			body:        "Analyze this project's structure and create an appropriate configuration:\n\n1. Detect the project type (language, framework, build system)\n2. Identify key directories and entry points\n3. Create or update the project configuration file with:\n   - Project description and conventions\n   - Key file locations\n   - Coding standards and best practices\n   - Common workflows and commands\n\n$ARGUMENTS",
		},
	}

	for _, b := range builtin {
		_ = r.Register(&Command{
			Name:        b.name,
			Description: b.description,
			Body:        b.body,
			Source:      SourceBuiltin,
		})
	}
}
