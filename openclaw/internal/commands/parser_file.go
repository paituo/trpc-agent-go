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
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type commandFrontMatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Thinking    bool   `yaml:"thinking"`
}

// parseCommandFile reads and parses a COMMAND.md file.
func parseCommandFile(path string, defaultName string, source CommandSource) (*Command, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)
	content = strings.ReplaceAll(content, "\r\n", "\n")

	var fm commandFrontMatter
	body := content

	// Parse optional YAML front matter
	if strings.HasPrefix(content, "---\n") {
		idx := strings.Index(content[4:], "\n---\n")
		if idx >= 0 {
			raw := content[4 : 4+idx]
			if err := yaml.Unmarshal([]byte(raw), &fm); err != nil {
				// If front matter parsing fails, treat entire content as body
				fm = commandFrontMatter{}
			} else {
				body = strings.TrimSpace(content[4+idx+5:])
			}
		}
	}

	name := strings.TrimSpace(fm.Name)
	if name == "" {
		name = defaultName
	}

	description := strings.TrimSpace(fm.Description)

	return &Command{
		Name:        strings.ToLower(name),
		Description: description,
		Body:        strings.TrimSpace(body),
		Source:      source,
		Thinking:    fm.Thinking,
	}, nil
}
