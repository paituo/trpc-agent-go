//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package stdin registers a simple "stdin channel" plugin.
//
// It is intended as a reference implementation for writing custom
// channels. The channel reads one line per message from STDIN, sends it
// to the OpenClaw gateway, and prints the reply to STDOUT.
package stdin

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	occhannel "trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/plugins/stdin/mdrenderer"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

const (
	pluginType = "stdin"

	defaultFrom           = "local"
	defaultUserLabel      = "User"
	defaultAssistantLabel = "Assistant"
	defaultReasoningLabel = "Thought"

	exitCmd1 = "/exit"
	exitCmd2 = "/quit"

	defaultScannerBufBytes = 64 * 1024
	defaultScannerMaxBytes = 1 << 20
)

var (
	esc          = fmt.Sprintf("%c", 27)
	colorReset   = esc + "[0m"
	colorCyan    = esc + "[36m"
	colorGreen   = esc + "[32m"
	colorYellow  = esc + "[33m"
	colorGray    = esc + "[90m"
	colorMagenta = esc + "[35m"
)

func init() {
	if err := registry.RegisterChannel(pluginType, newChannel); err != nil {
		panic(err)
	}
}

type channelCfg struct {
	From           string `yaml:"from"`
	Thread         string `yaml:"thread"`
	MaxLineBytes   int    `yaml:"max_line_bytes"`
	ShowPrompt     bool   `yaml:"show_prompt,omitempty"`
	ShowRoleLabels bool   `yaml:"show_role_labels,omitempty"`
	ShowReasoning  *bool  `yaml:"show_reasoning,omitempty"`
	ReasoningLabel string `yaml:"reasoning_label,omitempty"`
	UserLabel      string `yaml:"user_label,omitempty"`
	AssistantLabel string `yaml:"assistant_label,omitempty"`
	EnableMarkdown bool   `yaml:"enable_markdown,omitempty"`
}

func newChannel(
	deps registry.ChannelDeps,
	spec registry.PluginSpec,
) (occhannel.Channel, error) {
	if deps.Gateway == nil {
		return nil, errors.New("stdin channel: nil gateway client")
	}

	var cfg channelCfg
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	from := strings.TrimSpace(cfg.From)
	if from == "" {
		from = defaultFrom
	}

	maxLineBytes := cfg.MaxLineBytes
	if maxLineBytes <= 0 {
		maxLineBytes = defaultScannerMaxBytes
	}
	bufBytes := defaultScannerBufBytes
	if maxLineBytes < bufBytes {
		bufBytes = maxLineBytes
	}

	id := pluginType
	if strings.TrimSpace(spec.Name) != "" {
		id = strings.TrimSpace(spec.Name)
	}

	showReasoning := true
	if cfg.ShowReasoning != nil {
		showReasoning = *cfg.ShowReasoning
	}

	return &channel{
		id:             id,
		gw:             deps.Gateway,
		from:           from,
		thread:         strings.TrimSpace(cfg.Thread),
		bufBytes:       bufBytes,
		maxLineBytes:   maxLineBytes,
		showPrompt:     cfg.ShowPrompt,
		showRoleLabels: cfg.ShowRoleLabels,
		showReasoning:  showReasoning,
		reasoningLabel: defaultLabel(cfg.ReasoningLabel, defaultReasoningLabel),
		userLabel:      defaultLabel(cfg.UserLabel, defaultUserLabel),
		assistantLabel: defaultLabel(cfg.AssistantLabel, defaultAssistantLabel),
		enableMarkdown: cfg.EnableMarkdown,
	}, nil
}

type channel struct {
	id     string
	gw     registry.GatewayClient
	from   string
	thread string

	showPrompt     bool
	showRoleLabels bool
	showReasoning  bool
	reasoningLabel string
	userLabel      string
	assistantLabel string
	enableMarkdown bool

	bufBytes     int
	maxLineBytes int
}

func (c *channel) ID() string { return c.id }

func (c *channel) Run(ctx context.Context) error {
	fmt.Fprintln(os.Stdout, "STDIN channel started.")
	fmt.Fprintln(os.Stdout, "Type /quit or /exit to stop.")

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, c.bufBytes), c.maxLineBytes)
	for {
		if ctx.Err() != nil {
			return nil
		}

		c.printPrompt()
		if !in.Scan() {
			c.printPromptTerminator()
			if err := in.Err(); err != nil {
				return err
			}
			return nil
		}

		text := strings.TrimSpace(in.Text())
		if text == "" {
			continue
		}
		if text == exitCmd1 || text == exitCmd2 {
			return nil
		}

		if err := c.processMessage(ctx, text); err != nil {
			return err
		}
	}
}

func (c *channel) processMessage(ctx context.Context, text string) error {
	streamClient, ok := c.gw.(registry.StreamingGatewayClient)
	if !ok {
		rsp, err := c.gw.SendMessage(ctx, gwclient.MessageRequest{
			Channel: c.id,
			From:    c.from,
			Thread:  c.thread,
			Text:    text,
			UserID:  c.from,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return nil
		}
		if rsp.Ignored {
			c.printIgnored()
			return nil
		}
		reply := rsp.Reply
		if c.enableMarkdown {
			reply = mdrenderer.Render(reply)
		}
		c.printReply(reply)
		return nil
	}

	stream, err := streamClient.StreamMessage(ctx, gwclient.MessageRequest{
		Channel: c.id,
		From:    c.from,
		Thread:  c.thread,
		Text:    text,
		UserID:  c.from,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil
	}

	ignored := false

	for evt := range stream {
		switch evt.Type {
		case gwproto.StreamEventTypeRunIgnored:
			ignored = true
		case gwproto.StreamEventTypeThoughtCompleted:
			if c.showReasoning && evt.Reply != "" {
				reply := evt.Reply
				if c.enableMarkdown {
					reply = mdrenderer.Render(reply)
				}
				c.printReasoning(reply)
			}
		case gwproto.StreamEventTypeMessageCompleted:
			if evt.Reply != "" {
				reply := evt.Reply
				if c.enableMarkdown {
					reply = mdrenderer.Render(reply)
				}
				c.printReply(reply)
			}
		case gwproto.StreamEventTypeRunError, gwproto.StreamEventTypeRunCanceled:
			if evt.Error != nil {
				fmt.Fprintf(os.Stderr, "error: %s\n", evt.Error.Message)
			}
		}
	}

	if ignored {
		c.printIgnored()
	}
	return nil
}

func defaultLabel(raw string, fallback string) string {
	label := strings.TrimSpace(raw)
	if label == "" {
		return fallback
	}
	return label
}

func (c *channel) printPrompt() {
	if !c.showPrompt {
		return
	}
	fmt.Fprintf(os.Stdout, "%s: ", c.userLabel)
}

func (c *channel) printPromptTerminator() {
	if !c.showPrompt {
		return
	}
	fmt.Fprintln(os.Stdout)
}

func (c *channel) printIgnored() {
	fmt.Fprintf(os.Stdout, "%s(ignored)%s\n", colorYellow, colorReset)
}

func (c *channel) printReasoning(reasoning string) {
	fmt.Fprintf(os.Stdout, "%s%s: %s%s\n", colorCyan, c.reasoningLabel, reasoning, colorReset)
}

func (c *channel) printReply(reply string) {
	if c.showRoleLabels {
		fmt.Fprintf(os.Stdout, "%s%s: %s%s\n", colorGreen, c.assistantLabel, reply, colorReset)
		return
	}
	fmt.Fprintf(os.Stdout, "%s%s%s\n", colorGreen, reply, colorReset)
}

func (c *channel) SendMessage(
	ctx context.Context,
	target string,
	msg occhannel.OutboundMessage,
) error {
	if c == nil {
		return fmt.Errorf("stdin: channel unavailable")
	}

	userID := strings.TrimSpace(target)
	if userID == "" {
		userID = c.from
	}

	if strings.TrimSpace(msg.Text) != "" {
		fmt.Fprintf(os.Stdout, "%s[stdin -> %s]%s %s\n", colorMagenta, userID, colorReset, msg.Text)
	}

	for _, file := range msg.Files {
		fileName := strings.TrimSpace(file.Name)
		if fileName == "" {
			fileName = filepath.Base(file.Path)
		}
		fmt.Fprintf(os.Stdout, "%s[stdin -> %s]%s [file] %s\n", colorMagenta, userID, colorReset, fileName)
	}

	return nil
}
