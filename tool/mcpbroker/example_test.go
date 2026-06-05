//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mcpbroker

import (
	"context"
	"fmt"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	mcpcfg "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

type exampleTestDummyModel struct{}

func (m *exampleTestDummyModel) Info() model.Info { return model.NewTestInfo("example-test-dummy") }
func (m *exampleTestDummyModel) GenerateContent(_ context.Context, _ *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func ExampleNew() {
	broker := New(
		WithServers(map[string]mcpcfg.ConnectionConfig{
			"local_stdio": {
				Command: "go",
				Args:    []string{"run", "./mcpserver"},
			},
		}),
		WithAllowAdHocHTTP(true),
	)

	agent := llmagent.New(
		"assistant",
		llmagent.WithModel(&exampleTestDummyModel{}),
		llmagent.WithTools(broker.Tools()),
	)

	names := make([]string, 0, len(agent.Tools()))
	for _, tl := range agent.Tools() {
		names = append(names, tl.Declaration().Name)
	}
	sort.Strings(names)
	fmt.Println(names)
	// Output: [mcp_call mcp_inspect_tools mcp_list_servers mcp_list_tools]
}
