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

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const commandFileName = "COMMAND.md"

// Repository manages slash command definitions.
type Repository struct {
	mu       sync.RWMutex
	commands map[string]*Command // name -> Command
}

// NewRepository creates a new command repository.
func NewRepository() *Repository {
	return &Repository{
		commands: make(map[string]*Command),
	}
}

// Register adds a command to the repository.
func (r *Repository) Register(cmd *Command) error {
	if cmd == nil {
		return fmt.Errorf("commands: nil command")
	}
	name := strings.TrimSpace(cmd.Name)
	if name == "" {
		return fmt.Errorf("commands: empty command name")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands[name] = cmd
	return nil
}

// Get retrieves a command by name.
func (r *Repository) Get(name string) (*Command, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cmd, ok := r.commands[strings.ToLower(strings.TrimSpace(name))]
	return cmd, ok
}

// List returns all registered commands sorted by name.
func (r *Repository) List() []*Command {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*Command, 0, len(r.commands))
	for _, cmd := range r.commands {
		out = append(out, cmd)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// LoadFromDir loads command definitions from a directory.
// Each subdirectory with a COMMAND.md file defines one command.
// The command name is derived from the directory name.
func (r *Repository) LoadFromDir(dir string, source CommandSource) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("commands: read dir %s: %w", dir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(entry.Name()))
		if name == "" {
			continue
		}

		cmdPath := filepath.Join(dir, entry.Name(), commandFileName)
		cmd, err := parseCommandFile(cmdPath, name, source)
		if err != nil {
			continue // Skip invalid command files
		}
		if err := r.Register(cmd); err != nil {
			continue
		}
	}
	return nil
}

// LoadFromFlatDir loads command definitions from a flat directory.
// Each .md file in the directory defines one command.
// The command name is derived from the filename (without .md extension).
func (r *Repository) LoadFromFlatDir(dir string, source CommandSource) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("commands: read dir %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		name = strings.TrimSuffix(name, ".md")
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}

		cmdPath := filepath.Join(dir, entry.Name())
		cmd, err := parseCommandFile(cmdPath, name, source)
		if err != nil {
			continue
		}
		if err := r.Register(cmd); err != nil {
			continue
		}
	}
	return nil
}

// Render renders a command's body with the given arguments.
func (r *Repository) Render(call *CommandCall) (string, error) {
	cmd, ok := r.Get(call.Name)
	if !ok {
		return "", fmt.Errorf("commands: unknown command: /%s", call.Name)
	}

	body := cmd.Body

	// Replace $ARGUMENTS with the raw argument string
	body = strings.ReplaceAll(body, "$ARGUMENTS", call.RawArgs)

	// Replace positional parameters $1, $2, etc.
	for i, arg := range call.Args {
		placeholder := fmt.Sprintf("$%d", i+1)
		body = strings.ReplaceAll(body, placeholder, arg)
	}

	return body, nil
}

// HelpText generates help text for all registered commands.
func (r *Repository) HelpText() string {
	cmds := r.List()
	if len(cmds) == 0 {
		return "No commands available."
	}

	var sb strings.Builder
	sb.WriteString("Available commands:\n")
	for _, cmd := range cmds {
		sourceLabel := ""
		switch cmd.Source {
		case SourceProject:
			sourceLabel = " (project)"
		case SourceUser:
			sourceLabel = " (user)"
		}
		sb.WriteString(fmt.Sprintf("  /%-15s %s%s\n", cmd.Name, cmd.Description, sourceLabel))
	}
	return sb.String()
}
