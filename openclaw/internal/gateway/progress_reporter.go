//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gateway

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
)

// gatewayProgressReporter 是 gwproto.ToolProgressReporter 的 gateway 侧实现。
// 它将工具执行进度通过 public.delta / public.completed SSE 事件推送到前端。
type gatewayProgressReporter struct {
	ctx       context.Context
	out       chan<- gwproto.StreamEvent
	sessionID string
	requestID string
}

// ReportProgressDelta 发送增量文本（会以 public.delta 推送到前端）。
func (r *gatewayProgressReporter) ReportProgressDelta(delta string) {
	if r == nil || r.out == nil {
		return
	}
	sendStreamEvent(r.ctx, r.out, gwproto.StreamEvent{
		Type:      gwproto.StreamEventTypePublicDelta,
		SessionID: r.sessionID,
		RequestID: r.requestID,
		Delta:     delta,
	})
}

// ReportProgressComplete 发送完整文本（会以 public.completed 推送到前端）。
func (r *gatewayProgressReporter) ReportProgressComplete(text string) {
	if r == nil || r.out == nil {
		return
	}
	sendStreamEvent(r.ctx, r.out, gwproto.StreamEvent{
		Type:      gwproto.StreamEventTypePublicCompleted,
		SessionID: r.sessionID,
		RequestID: r.requestID,
		Reply:     text,
	})
}

// 编译时检查：确保 gatewayProgressReporter 实现了 gwproto.ToolProgressReporter 接口。
var _ gwproto.ToolProgressReporter = (*gatewayProgressReporter)(nil)
