//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package stdin

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	occhannel "trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

type stubGateway struct {
	reqs []gwclient.MessageRequest
}

func (g *stubGateway) SendMessage(
	_ context.Context,
	req gwclient.MessageRequest,
) (gwclient.MessageResponse, error) {
	g.reqs = append(g.reqs, req)
	if req.Text == "fail" {
		return gwclient.MessageResponse{}, errors.New("boom")
	}
	if req.Text == "ignore" {
		return gwclient.MessageResponse{Ignored: true}, nil
	}
	return gwclient.MessageResponse{Reply: "ok"}, nil
}

func (g *stubGateway) Cancel(context.Context, string) (bool, error) {
	return false, nil
}

type stubStreamingGateway struct {
	stubGateway
	streamEvents []gwclient.StreamEvent
}

func (g *stubStreamingGateway) StreamMessage(
	_ context.Context,
	req gwclient.MessageRequest,
) (<-chan gwclient.StreamEvent, error) {
	g.reqs = append(g.reqs, req)
	out := make(chan gwclient.StreamEvent, len(g.streamEvents))
	for _, evt := range g.streamEvents {
		out <- evt
	}
	close(out)
	return out, nil
}

func TestInit_RegistersChannel(t *testing.T) {
	f, ok := registry.LookupChannel(pluginType)
	require.True(t, ok)
	require.NotNil(t, f)
}

func TestNewChannel_NilGatewayFails(t *testing.T) {
	t.Parallel()

	_, err := newChannel(
		registry.ChannelDeps{Gateway: nil},
		registry.PluginSpec{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil gateway")
}

func TestNewChannel_DefaultFromAndID(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{}
	ch, err := newChannel(
		registry.ChannelDeps{Gateway: gw},
		registry.PluginSpec{},
	)
	require.NoError(t, err)

	got, ok := ch.(*channel)
	require.True(t, ok)
	require.Equal(t, pluginType, got.ID())
	require.Equal(t, defaultFrom, got.from)
}

func TestNewChannel_OverridesFromThreadAndID(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{}

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(
		"from: u1\nthread: t1\n",
	), &node))

	ch, err := newChannel(
		registry.ChannelDeps{Gateway: gw},
		registry.PluginSpec{
			Name:   "c1",
			Config: &node,
		},
	)
	require.NoError(t, err)

	got, ok := ch.(*channel)
	require.True(t, ok)
	require.Equal(t, "c1", got.ID())
	require.Equal(t, "u1", got.from)
	require.Equal(t, "t1", got.thread)
}

func TestNewChannel_OverridesLabelsAndPrompt(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{}

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(
		"show_prompt: true\n"+
			"show_role_labels: true\n"+
			"user_label: You\n"+
			"assistant_label: Bot\n",
	), &node))

	ch, err := newChannel(
		registry.ChannelDeps{Gateway: gw},
		registry.PluginSpec{Config: &node},
	)
	require.NoError(t, err)

	got, ok := ch.(*channel)
	require.True(t, ok)
	require.True(t, got.showPrompt)
	require.True(t, got.showRoleLabels)
	require.Equal(t, "You", got.userLabel)
	require.Equal(t, "Bot", got.assistantLabel)
}

func TestNewChannel_DefaultLabelsAndTrimmedOverrides(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{}

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(
		"user_label: \"   \"\n"+
			"assistant_label: \"  Bot  \"\n",
	), &node))

	ch, err := newChannel(
		registry.ChannelDeps{Gateway: gw},
		registry.PluginSpec{Config: &node},
	)
	require.NoError(t, err)

	got, ok := ch.(*channel)
	require.True(t, ok)
	require.False(t, got.showPrompt)
	require.False(t, got.showRoleLabels)
	require.Equal(t, defaultUserLabel, got.userLabel)
	require.Equal(t, "Bot", got.assistantLabel)
}

func TestChannel_Run_SendsMessagesAndPrintsReply(t *testing.T) {
	gw := &stubGateway{}
	c := &channel{
		id:           "x",
		gw:           gw,
		from:         "u",
		thread:       "t",
		bufBytes:     defaultScannerBufBytes,
		maxLineBytes: defaultScannerMaxBytes,
	}

	stdin := os.Stdin
	stdout := os.Stdout
	stderr := os.Stderr

	inR, inW, err := os.Pipe()
	require.NoError(t, err)
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)

	os.Stdin = inR
	os.Stdout = outW
	os.Stderr = errW
	t.Cleanup(func() {
		os.Stdin = stdin
		os.Stdout = stdout
		os.Stderr = stderr
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	_, _ = io.WriteString(inW, "fail\nignore\nhello\n/quit\n")
	require.NoError(t, inW.Close())

	require.NoError(t, <-done)

	require.NoError(t, outW.Close())
	require.NoError(t, errW.Close())

	out, err := io.ReadAll(outR)
	require.NoError(t, err)
	require.Contains(t, string(out), "STDIN channel started.")
	require.Contains(t, string(out), "(ignored)")
	require.Contains(t, string(out), "ok")

	errOut, err := io.ReadAll(errR)
	require.NoError(t, err)
	require.Contains(t, string(errOut), "boom")

	require.Len(t, gw.reqs, 3)
	require.Equal(t, "fail", gw.reqs[0].Text)
	require.Equal(t, "ignore", gw.reqs[1].Text)
	require.Equal(t, "hello", gw.reqs[2].Text)
	require.Equal(t, "u", gw.reqs[2].From)
	require.Equal(t, "t", gw.reqs[2].Thread)
}

func TestChannel_Run_AllowsLongLine(t *testing.T) {
	gw := &stubGateway{}
	c := &channel{
		id:           "x",
		gw:           gw,
		from:         "u",
		thread:       "t",
		bufBytes:     defaultScannerBufBytes,
		maxLineBytes: defaultScannerMaxBytes,
	}

	stdin := os.Stdin
	stdout := os.Stdout
	stderr := os.Stderr

	inR, inW, err := os.Pipe()
	require.NoError(t, err)
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)

	os.Stdin = inR
	os.Stdout = outW
	os.Stderr = errW
	t.Cleanup(func() {
		os.Stdin = stdin
		os.Stdout = stdout
		os.Stderr = stderr
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	longLine := strings.Repeat("a", 70*1024)
	_, _ = io.WriteString(inW, longLine+"\n/quit\n")
	require.NoError(t, inW.Close())

	require.NoError(t, <-done)

	require.NoError(t, outW.Close())
	require.NoError(t, errW.Close())

	_, err = io.ReadAll(outR)
	require.NoError(t, err)

	errOut, err := io.ReadAll(errR)
	require.NoError(t, err)
	require.Empty(t, string(errOut))

	require.Len(t, gw.reqs, 1)
	require.Equal(t, longLine, gw.reqs[0].Text)
}

func TestChannel_Run_PrintsPromptAndRoleLabels(t *testing.T) {
	gw := &stubGateway{}
	c := &channel{
		id:             "x",
		gw:             gw,
		from:           "u",
		thread:         "t",
		showPrompt:     true,
		showRoleLabels: true,
		userLabel:      "You",
		assistantLabel: "Assistant",
		bufBytes:       defaultScannerBufBytes,
		maxLineBytes:   defaultScannerMaxBytes,
	}

	stdin := os.Stdin
	stdout := os.Stdout
	stderr := os.Stderr

	inR, inW, err := os.Pipe()
	require.NoError(t, err)
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)

	os.Stdin = inR
	os.Stdout = outW
	os.Stderr = errW
	t.Cleanup(func() {
		os.Stdin = stdin
		os.Stdout = stdout
		os.Stderr = stderr
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	_, _ = io.WriteString(inW, "hello\n/quit\n")
	require.NoError(t, inW.Close())

	require.NoError(t, <-done)

	require.NoError(t, outW.Close())
	require.NoError(t, errW.Close())

	out, err := io.ReadAll(outR)
	require.NoError(t, err)
	require.Contains(t, string(out), "You: ")
	require.Contains(t, string(out), "Assistant: ok")

	errOut, err := io.ReadAll(errR)
	require.NoError(t, err)
	require.Empty(t, string(errOut))
}

func TestChannel_Run_ClearsPromptOnEOF(t *testing.T) {
	gw := &stubGateway{}
	c := &channel{
		id:           "x",
		gw:           gw,
		from:         "u",
		thread:       "t",
		showPrompt:   true,
		userLabel:    defaultUserLabel,
		bufBytes:     defaultScannerBufBytes,
		maxLineBytes: defaultScannerMaxBytes,
	}

	stdin := os.Stdin
	stdout := os.Stdout
	stderr := os.Stderr

	inR, inW, err := os.Pipe()
	require.NoError(t, err)
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)

	os.Stdin = inR
	os.Stdout = outW
	os.Stderr = errW
	t.Cleanup(func() {
		os.Stdin = stdin
		os.Stdout = stdout
		os.Stderr = stderr
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	require.NoError(t, inW.Close())
	require.NoError(t, <-done)

	require.NoError(t, outW.Close())
	require.NoError(t, errW.Close())

	out, err := io.ReadAll(outR)
	require.NoError(t, err)
	require.Contains(t, string(out), defaultUserLabel+": \n")

	errOut, err := io.ReadAll(errR)
	require.NoError(t, err)
	require.Empty(t, string(errOut))
}

func TestNewChannel_OverridesReasoningAndMarkdown(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{}

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(
		"show_reasoning: false\n"+
			"reasoning_label: Think\n"+
			"enable_markdown: true\n",
	), &node))

	ch, err := newChannel(
		registry.ChannelDeps{Gateway: gw},
		registry.PluginSpec{Config: &node},
	)
	require.NoError(t, err)

	got, ok := ch.(*channel)
	require.True(t, ok)
	require.False(t, got.showReasoning)
	require.Equal(t, "Think", got.reasoningLabel)
	require.True(t, got.enableMarkdown)
}

func TestNewChannel_DefaultReasoningTrue(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{}
	ch, err := newChannel(
		registry.ChannelDeps{Gateway: gw},
		registry.PluginSpec{},
	)
	require.NoError(t, err)

	got, ok := ch.(*channel)
	require.True(t, ok)
	require.True(t, got.showReasoning)
	require.Equal(t, defaultReasoningLabel, got.reasoningLabel)
	require.False(t, got.enableMarkdown)
}

func TestChannel_Run_StreamingWithThinking(t *testing.T) {
	gw := &stubStreamingGateway{
		streamEvents: []gwclient.StreamEvent{
			{Type: gwproto.StreamEventTypeThoughtCompleted, Reply: "step 1"},
			{Type: gwproto.StreamEventTypeMessageCompleted, Reply: "final answer"},
		},
	}
	c := &channel{
		id:             "x",
		gw:             gw,
		from:           "u",
		thread:         "t",
		showReasoning:  true,
		reasoningLabel: "Thought",
		bufBytes:       defaultScannerBufBytes,
		maxLineBytes:   defaultScannerMaxBytes,
	}

	stdin := os.Stdin
	stdout := os.Stdout
	stderr := os.Stderr

	inR, inW, err := os.Pipe()
	require.NoError(t, err)
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)

	os.Stdin = inR
	os.Stdout = outW
	os.Stderr = errW
	t.Cleanup(func() {
		os.Stdin = stdin
		os.Stdout = stdout
		os.Stderr = stderr
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	_, _ = io.WriteString(inW, "hello\n/quit\n")
	require.NoError(t, inW.Close())

	require.NoError(t, <-done)

	require.NoError(t, outW.Close())
	require.NoError(t, errW.Close())

	out, err := io.ReadAll(outR)
	require.NoError(t, err)
	require.Contains(t, string(out), "Thought:")
	require.Contains(t, string(out), "step 1")
	require.Contains(t, string(out), "final answer")

	errOut, err := io.ReadAll(errR)
	require.NoError(t, err)
	require.Empty(t, string(errOut))
}

func TestChannel_Run_StreamingSuppressesReasoning(t *testing.T) {
	gw := &stubStreamingGateway{
		streamEvents: []gwclient.StreamEvent{
			{Type: gwproto.StreamEventTypeThoughtCompleted, Reply: "step 1"},
			{Type: gwproto.StreamEventTypeMessageCompleted, Reply: "final answer"},
		},
	}
	c := &channel{
		id:             "x",
		gw:             gw,
		from:           "u",
		thread:         "t",
		showReasoning:  false,
		reasoningLabel: "Thought",
		bufBytes:       defaultScannerBufBytes,
		maxLineBytes:   defaultScannerMaxBytes,
	}

	stdin := os.Stdin
	stdout := os.Stdout
	stderr := os.Stderr

	inR, inW, err := os.Pipe()
	require.NoError(t, err)
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)

	os.Stdin = inR
	os.Stdout = outW
	os.Stderr = errW
	t.Cleanup(func() {
		os.Stdin = stdin
		os.Stdout = stdout
		os.Stderr = stderr
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	_, _ = io.WriteString(inW, "hello\n/quit\n")
	require.NoError(t, inW.Close())

	require.NoError(t, <-done)

	require.NoError(t, outW.Close())
	require.NoError(t, errW.Close())

	out, err := io.ReadAll(outR)
	require.NoError(t, err)
	require.NotContains(t, string(out), "Thought:")
	require.Contains(t, string(out), "final answer")

	errOut, err := io.ReadAll(errR)
	require.NoError(t, err)
	require.Empty(t, string(errOut))
}

func TestChannel_Run_StreamingIgnoredAndError(t *testing.T) {
	gw := &stubStreamingGateway{
		streamEvents: []gwclient.StreamEvent{
			{Type: gwproto.StreamEventTypeRunIgnored},
			{Type: gwproto.StreamEventTypeRunError, Error: &gwclient.APIError{Message: "oops"}},
		},
	}
	c := &channel{
		id:           "x",
		gw:           gw,
		from:         "u",
		thread:       "t",
		bufBytes:     defaultScannerBufBytes,
		maxLineBytes: defaultScannerMaxBytes,
	}

	stdin := os.Stdin
	stdout := os.Stdout
	stderr := os.Stderr

	inR, inW, err := os.Pipe()
	require.NoError(t, err)
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)

	os.Stdin = inR
	os.Stdout = outW
	os.Stderr = errW
	t.Cleanup(func() {
		os.Stdin = stdin
		os.Stdout = stdout
		os.Stderr = stderr
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	_, _ = io.WriteString(inW, "hello\n/quit\n")
	require.NoError(t, inW.Close())

	require.NoError(t, <-done)

	require.NoError(t, outW.Close())
	require.NoError(t, errW.Close())

	out, err := io.ReadAll(outR)
	require.NoError(t, err)
	require.Contains(t, string(out), "(ignored)")

	errOut, err := io.ReadAll(errR)
	require.NoError(t, err)
	require.Contains(t, string(errOut), "oops")
}

func TestChannel_ColorOutput(t *testing.T) {
	c := &channel{}

	stdout := os.Stdout
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = outW
	t.Cleanup(func() { os.Stdout = stdout })

	c.printReply("hello")
	c.printReasoning("thinking")
	c.printIgnored()
	require.NoError(t, outW.Close())

	out, err := io.ReadAll(outR)
	require.NoError(t, err)
	output := string(out)
	require.Contains(t, output, colorGreen)
	require.Contains(t, output, colorCyan)
	require.Contains(t, output, colorYellow)
	require.Contains(t, output, colorReset)
	require.Contains(t, output, "hello")
	require.Contains(t, output, "thinking")
	require.Contains(t, output, "(ignored)")
}

func TestChannel_SendMessage_OOB(t *testing.T) {
	c := &channel{
		id:   "stdin",
		from: "local",
	}

	stdout := os.Stdout
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = outW
	t.Cleanup(func() { os.Stdout = stdout })

	c.SendMessage(context.Background(), "user1", occhannel.OutboundMessage{
		Text: "hello from system",
	})
	require.NoError(t, outW.Close())

	out, err := io.ReadAll(outR)
	require.NoError(t, err)
	output := string(out)
	require.Contains(t, output, colorMagenta)
	require.Contains(t, output, colorReset)
	require.Contains(t, output, "[stdin -> user1]")
	require.Contains(t, output, "hello from system")
}
