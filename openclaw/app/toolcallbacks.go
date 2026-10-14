package app

import (
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mergeRegisteredToolCallbacks merges globally registered tool-callbacks
// contributors into base. Contributors registered by downstream plugins (e.g.
// bowei's auto-guid / workflow-confirm after-tool hooks) then apply to every
// llm agent constructed through newAgent, including the default main
// assistant which otherwise has no plugin hook injection point.
//
// Each contributor is evaluated lazily and may return nil while the hooks it
// exposes are not assembled yet; nil results are skipped.
func mergeRegisteredToolCallbacks(base *tool.Callbacks) {
	if base == nil {
		return
	}
	for _, makeCB := range registry.LookupToolCallbacksContributors() {
		if makeCB == nil {
			continue
		}
		extra := makeCB()
		if extra == nil {
			continue
		}
		mergeToolCallbacks(base, extra)
	}
}

// mergeToolCallbacks appends src's registered before/after tool callbacks to
// dst. dst's ToolResultMessages (framework default) is preserved so that the
// framework-owned result formatter keeps precedence.
func mergeToolCallbacks(dst, src *tool.Callbacks) {
	if dst == nil || src == nil {
		return
	}
	dst.BeforeTool = append(dst.BeforeTool, src.BeforeTool...)
	dst.AfterTool = append(dst.AfterTool, src.AfterTool...)
}
