//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mdrenderer

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	esc = fmt.Sprintf("%c", 27)

	reBold       = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reItalic     = regexp.MustCompile(`(?m)\*(.+?)\*`)
	reInlineCode = regexp.MustCompile("`([^`\n]+?)`")
	reLink       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reHeader     = regexp.MustCompile(`(?m)^(#{1,6})\s+(.+)$`)
)

// Render converts a subset of markdown to terminal-formatted text
// using ANSI escape sequences.
func Render(s string) string {
	s = reHeader.ReplaceAllStringFunc(s, func(m string) string {
		parts := reHeader.FindStringSubmatch(m)
		if len(parts) < 3 {
			return m
		}
		level := len(parts[1])
		bold := esc + "[1m"
		color := esc + fmt.Sprintf("[%dm", 36-level) // cyan(36) → blue(34)
		reset := esc + "[0m"

		prefix := ""
		if level == 1 {
			prefix = "\n"
		}
		return prefix + color + bold + parts[2] + reset
	})

	s = reBold.ReplaceAllString(s, esc+"[1m$1"+esc+"[0m")

	s = reItalic.ReplaceAllString(s, esc+"[3m$1"+esc+"[0m")

	s = reInlineCode.ReplaceAllString(s, esc+"[33m$1"+esc+"[0m")

	s = reLink.ReplaceAllString(s, "$1 ($2)")

	s = strings.ReplaceAll(s, "```", "")
	s = strings.ReplaceAll(s, "~~~", "")

	return s
}