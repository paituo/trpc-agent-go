import { useCallback, useRef, useReducer } from "react";
import { formatStructured, safeJsonParse } from "../agui/format";
import { streamAguiSse, type AguiSseEvent } from "../agui/sse";
import { chatReducer, initialChatState } from "../store";

export type UiMessageRole = "user" | "assistant" | "tool" | "system";

export type UiMessageKind =
  | "text"
  | "thinking"
  | "tool-call"
  | "tool-result"
  | "custom"
  | "system"
  | "step"
  | "block";

export type StepStatus = "pending" | "running" | "in_progress" | "completed" | "failed" | "skipped";

export type BlockType = "todo" | "in-progress" | "done" | "error" | "tool" | "agent";

export type UiMessage = {
  id: string;
  role: UiMessageRole;
  kind: UiMessageKind;
  title?: string;
  content: string;
  status?: "pending" | "streaming" | "complete" | "stop" | "error";
  toolCall?: {
    toolCallId: string;
    toolCallName: string;
    parentMessageId?: string;
    args?: string;
    result?: string;
  };
  timestamp: number;
  startedAt?: number;
  completedAt?: number;
  /** 块容器专用字段 */
  stepId?: string;
  stepTitle?: string;
  blockType?: BlockType;
  stepStatus?: StepStatus;
  children?: UiMessage[];
};

export type RawAguiEvent = {
  id: string;
  kind: "event" | "request";
  type: string;
  timestamp: number;
  payload: unknown;
};

export type GraphInterrupt = {
  key: string;
  prompt: string;
  checkpointId?: string;
  lineageId?: string;
};

export type ReportSession = {
  status: "open" | "closed";
  title: string;
  documentId: string;
  createdAt?: string;
  closedAt?: string;
  reason?: string;
  content: string;
};

export type AguiToolDeclaration = {
  name: string;
  description: string;
  parameters: Record<string, any>;
};

export type AguiChatConfig = {
  endpoint: string;
  threadId: string;
  forwardedProps?: Record<string, unknown>;
  /** 扩展工具声明列表，通过 payload.tools 传递给服务端，使 LLM 可调用这些工具 */
  tools?: AguiToolDeclaration[];
};

export type HistoryLoadResult =
  | { ok: true; count: number }
  | { ok: false; message: string };

const REPORT_OPEN_TOOL_NAME = "open_report_sidebar";
const GRAPH_APPROVAL_TOOL_NAME = "graph_interrupt_approval";

function randomId(prefix: string) {
  const now = Date.now();
  const rand = Math.random().toString(16).slice(2);
  return `${prefix}_${now}_${rand}`;
}

function sanitizeIdPart(value: string, maxLength = 96): string {
  return value.replace(/[^a-zA-Z0-9_-]/g, "_").slice(0, maxLength);
}

function fnv1aHash32(value: string): string {
  let hash = 0x811c9dc5;
  for (let i = 0; i < value.length; i += 1) {
    hash ^= value.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0).toString(16).padStart(8, "0");
}

function reasoningUiMessageId(messageId: string): string {
  const normalized = messageId.trim();
  if (!normalized) {
    return randomId("reasoning");
  }
  const sanitized = sanitizeIdPart(normalized, 64) || "id";
  return `reasoning_${sanitized}_${fnv1aHash32(normalized)}`;
}

function graphInterruptIdentity(interrupt: GraphInterrupt): string {
  return [interrupt.checkpointId ?? "", interrupt.lineageId ?? "", interrupt.key ?? ""].filter(Boolean).join("|");
}

function looksLikeToolCallId(prompt: string): boolean {
  const normalized = prompt.trim();
  if (!normalized) {
    return false;
  }
  return /^call_[a-zA-Z0-9_-]+$/.test(normalized);
}

function shouldSuppressGraphApproval(interrupt: GraphInterrupt, toolCallNameById: Map<string, string>): boolean {
  if (interrupt.key === "external_tool") {
    return true;
  }
  const normalized = interrupt.prompt.trim();
  if (!normalized) {
    return false;
  }
  if (toolCallNameById.has(normalized)) {
    return true;
  }
  return looksLikeToolCallId(normalized);
}

function isSessionNotFoundError(message: string): boolean {
  return /session not found/i.test(message);
}

function normalizeRole(value: unknown): UiMessageRole {
  if (value === "user" || value === "assistant" || value === "tool" || value === "system") {
    return value;
  }
  return "assistant";
}

function extractActivityValue(value: unknown): Record<string, any> | null {
  if (!value || typeof value !== "object") {
    return null;
  }
  return value as Record<string, any>;
}

function applyJsonPatch(root: Record<string, any>, ops: any[]) {
  for (const op of ops) {
    const operation = op?.op;
    const path = typeof op?.path === "string" ? op.path : "";
    const value = op?.value;
    if (!path.startsWith("/")) {
      continue;
    }
    const segments = path
      .split("/")
      .slice(1)
      .filter(Boolean)
      .map((seg: string) => seg.replace(/~1/g, "/").replace(/~0/g, "~"));
    if (segments.length === 0) {
      continue;
    }
    let target: any = root;
    for (let i = 0; i < segments.length - 1; i += 1) {
      const key = segments[i];
      if (!target[key] || typeof target[key] !== "object") {
        target[key] = {};
      }
      target = target[key];
    }
    const last = segments[segments.length - 1];
    if (operation === "remove") {
      delete target[last];
      continue;
    }
    if (operation === "add" || operation === "replace") {
      target[last] = value;
    }
  }
}

function normalizeJsonString(raw: string): string | undefined {
  const trimmed = raw.trim();
  if (!trimmed) {
    return undefined;
  }
  try {
    JSON.parse(trimmed);
    return trimmed;
  } catch {
    return JSON.stringify(trimmed);
  }
}

function restoreFromMessagesSnapshot(evt: AguiSseEvent): {
  messages: UiMessage[];
  reportSessions: ReportSession[];
  activeReportId: string | null;
  graphNodeId?: string;
  graphInterrupt: GraphInterrupt | null;
} {
  const now = Date.now();
  const timestamp = typeof evt.timestamp === "number" ? evt.timestamp : now;
  const snapshotMessages = Array.isArray((evt as any).messages) ? (evt as any).messages : [];

  const uiMessages: UiMessage[] = [];
  const messageIndexById = new Map<string, number>();
  const toolCallIndexById = new Map<string, number>();
  const toolCallNameById = new Map<string, string>();
  const toolCallArgsById = new Map<string, string>();

  const activityState: Record<string, any> = {};
  let graphNodeId: string | undefined;
  let graphInterrupt: GraphInterrupt | null = null;

  let activeThinkingIndex: number | null = null;
  const reportSessions: ReportSession[] = [];
  let activeReportId: string | null = null;

  const upsertUiMessage = (id: string, updater: (prev: UiMessage | null) => UiMessage) => {
    const index = messageIndexById.get(id);
    if (index === undefined) {
      const created = updater(null);
      messageIndexById.set(created.id, uiMessages.length);
      uiMessages.push(created);
      return;
    }
    const prev = uiMessages[index] ?? null;
    uiMessages[index] = updater(prev);
  };

  const upsertThinking = (updater: (prev: UiMessage | null) => UiMessage) => {
    if (activeThinkingIndex === null) {
      const created = updater(null);
      activeThinkingIndex = uiMessages.length;
      messageIndexById.set(created.id, activeThinkingIndex);
      uiMessages.push(created);
      return;
    }
    const prev = uiMessages[activeThinkingIndex] ?? null;
    uiMessages[activeThinkingIndex] = updater(prev);
  };

  const getActiveReportSession = () => {
    if (!activeReportId) {
      return null;
    }
    return reportSessions.find((session) => session.documentId === activeReportId) ?? null;
  };

  const upsertReportSession = (next: ReportSession) => {
    const index = reportSessions.findIndex((session) => session.documentId === next.documentId);
    if (index === -1) {
      reportSessions.push(next);
      return;
    }
    reportSessions[index] = { ...reportSessions[index], ...next };
  };

  const appendReportContent = (text: string) => {
    const active = getActiveReportSession();
    if (!active || active.status !== "open") {
      return;
    }
    const needsGap = active.content.trim().length > 0;
    active.content = needsGap ? `${active.content}\n\n${text}` : `${active.content}${text}`;
  };

  for (const msg of snapshotMessages) {
    const role = typeof msg?.role === "string" ? msg.role : "";

    if (role === "user") {
      const userContent = typeof msg?.content === "string" ? msg.content : formatStructured(msg?.content);
      if (!userContent.trim()) {
        activeThinkingIndex = null;
        continue;
      }

      uiMessages.push({
        id: typeof msg?.id === "string" ? msg.id : randomId("user_snapshot"),
        role: "user",
        kind: "text",
        title: typeof msg?.name === "string" ? msg.name : "You",
        content: userContent,
        timestamp,
      });
      messageIndexById.set(uiMessages[uiMessages.length - 1].id, uiMessages.length - 1);
      activeThinkingIndex = null;
      continue;
    }

    if (role === "reasoning") {
      const content = typeof msg?.content === "string" ? msg.content : formatStructured(msg?.content);
      if (!content.trim()) {
        continue;
      }
      const messageId = typeof msg?.id === "string" ? msg.id : randomId("reasoning_snapshot");
      const thinkingId = reasoningUiMessageId(messageId);
      uiMessages.push({
        id: thinkingId,
        role: "assistant",
        kind: "thinking",
        title: "Thinking",
        content,
        status: "complete",
        timestamp,
      });
      messageIndexById.set(thinkingId, uiMessages.length - 1);
      activeThinkingIndex = null;
      continue;
    }

    if (role === "assistant") {
      const content = typeof msg?.content === "string" ? msg.content : formatStructured(msg?.content);
      const rawMessageId = typeof msg?.id === "string" ? msg.id : "";
      if (rawMessageId.startsWith("reasoning_")) {
        const thinkingId = reasoningUiMessageId(rawMessageId);
        if (content.trim()) {
          uiMessages.push({
            id: thinkingId,
            role: "assistant",
            kind: "thinking",
            title: "Thinking",
            content,
            status: "complete",
            timestamp,
          });
          messageIndexById.set(thinkingId, uiMessages.length - 1);
        }
        activeThinkingIndex = null;
        continue;
      }
      if (getActiveReportSession()?.status === "open") {
        if (content.trim()) {
          appendReportContent(content);
        }
      } else if (content.trim()) {
        uiMessages.push({
          id: typeof msg?.id === "string" ? msg.id : randomId("assistant_snapshot"),
          role: "assistant",
          kind: "text",
          title: typeof msg?.name === "string" ? msg.name : "Assistant",
          content,
          timestamp,
        });
        messageIndexById.set(uiMessages[uiMessages.length - 1].id, uiMessages.length - 1);
      }

      const toolCalls = Array.isArray(msg?.toolCalls) ? msg.toolCalls : [];
      for (const toolCall of toolCalls) {
        const toolCallId = typeof toolCall?.id === "string" ? toolCall.id : randomId("toolcall_snapshot");
        const toolCallName = typeof toolCall?.function?.name === "string"
          ? toolCall.function.name
          : typeof toolCall?.name === "string"
            ? toolCall.name
            : "tool";
        const args = typeof toolCall?.function?.arguments === "string"
          ? toolCall.function.arguments
          : typeof toolCall?.arguments === "string"
            ? toolCall.arguments
            : "";

        toolCallNameById.set(toolCallId, toolCallName);
        toolCallArgsById.set(toolCallId, args);

        const next: UiMessage = {
          id: toolCallId,
          role: "assistant",
          kind: "tool-call",
          title: `Tool call: ${toolCallName}`,
          content: args,
          status: "complete",
          toolCall: {
            toolCallId,
            toolCallName,
            parentMessageId: typeof msg?.id === "string" ? msg.id : undefined,
            args,
          },
          timestamp,
        };
        toolCallIndexById.set(toolCallId, uiMessages.length);
        messageIndexById.set(toolCallId, uiMessages.length);
        uiMessages.push(next);
      }
      continue;
    }

    if (role === "tool") {
      const toolCallId = typeof msg?.toolCallId === "string" ? msg.toolCallId : "";
      const raw = typeof msg?.content === "string" ? msg.content : formatStructured(msg?.content);
      const normalized = raw ? normalizeJsonString(raw) : undefined;
      const toolCallName = toolCallId ? toolCallNameById.get(toolCallId) : undefined;

      if (toolCallName === "open_report_document") {
        const payload = raw ? safeJsonParse(raw) : {};
        const title = typeof (payload as any)?.title === "string" ? (payload as any).title : "Report";
        const documentId = typeof (payload as any)?.documentId === "string"
          ? (payload as any).documentId
          : toolCallId || randomId("doc");
        const createdAt = typeof (payload as any)?.createdAt === "string" ? (payload as any).createdAt : undefined;
        upsertReportSession({ status: "open", title, documentId, createdAt, content: "" });
        activeReportId = documentId;
        const actionPayload: Record<string, any> = { title, documentId, status: "open" };
        if (createdAt) {
          actionPayload.createdAt = createdAt;
        }
        uiMessages.push({
          id: `report_open_${documentId}`,
          role: "assistant",
          kind: "tool-call",
          title: "Open report",
          content: "",
          status: "complete",
          toolCall: {
            toolCallId: `report_open_${documentId}`,
            toolCallName: REPORT_OPEN_TOOL_NAME,
            args: "",
            result: JSON.stringify(actionPayload, null, 2),
          },
          timestamp,
        });
      }

      if (toolCallName === "close_report_document" && activeReportId) {
        const payload = raw ? safeJsonParse(raw) : {};
        const closedAt = typeof (payload as any)?.closedAt === "string" ? (payload as any).closedAt : undefined;
        const reason = typeof (payload as any)?.reason === "string"
          ? (payload as any).reason
          : typeof (payload as any)?.message === "string"
            ? (payload as any).message
            : undefined;
        const active = getActiveReportSession();
        upsertReportSession({
          status: "closed",
          title: active?.title ?? "Report",
          documentId: activeReportId,
          createdAt: active?.createdAt,
          content: active?.content ?? "",
          closedAt,
          reason,
        });

        const actionMessageId = `report_open_${activeReportId}`;
        const actionIndex = uiMessages.findIndex((entry) => entry.id === actionMessageId);
        if (actionIndex !== -1) {
          const payload: Record<string, any> = {
            title: active?.title ?? "Report",
            documentId: activeReportId,
            status: "closed",
          };
          if (active?.createdAt) {
            payload.createdAt = active.createdAt;
          }
          if (closedAt) {
            payload.closedAt = closedAt;
          }
          if (reason) {
            payload.reason = reason;
          }
          const prev = uiMessages[actionIndex];
          uiMessages[actionIndex] = {
            ...prev,
            toolCall: prev.toolCall
              ? { ...prev.toolCall, result: JSON.stringify(payload, null, 2) }
              : { toolCallId: actionMessageId, toolCallName: REPORT_OPEN_TOOL_NAME, args: "", result: JSON.stringify(payload, null, 2) },
          };
        }
      }

      if (toolCallId && toolCallIndexById.has(toolCallId)) {
        const index = toolCallIndexById.get(toolCallId)!;
        const prev = uiMessages[index];
        const name = prev?.toolCall?.toolCallName ?? toolCallNameById.get(toolCallId) ?? "tool";
        const args = prev?.toolCall?.args ?? toolCallArgsById.get(toolCallId) ?? prev?.content ?? "";
        uiMessages[index] = {
          id: toolCallId,
          role: "assistant",
          kind: "tool-call",
          title: `Tool call: ${name}`,
          content: args,
          status: "complete",
          toolCall: {
            toolCallId,
            toolCallName: name,
            parentMessageId: prev?.toolCall?.parentMessageId,
            args,
            result: normalized ?? prev?.toolCall?.result,
          },
          timestamp: prev?.timestamp ?? timestamp,
        };
        messageIndexById.set(toolCallId, index);
      } else if (toolCallId && raw.trim()) {
        const name = toolCallNameById.get(toolCallId) ?? "tool";
        const args = toolCallArgsById.get(toolCallId) ?? "";
        toolCallIndexById.set(toolCallId, uiMessages.length);
        messageIndexById.set(toolCallId, uiMessages.length);
        uiMessages.push({
          id: toolCallId,
          role: "assistant",
          kind: "tool-call",
          title: `Tool call: ${name}`,
          content: args,
          status: "complete",
          toolCall: {
            toolCallId,
            toolCallName: name,
            args,
            result: normalized,
          },
          timestamp,
        });
      } else if (raw.trim()) {
        const toolCallIdFallback = typeof msg?.id === "string" ? msg.id : randomId("tool_result");
        const name = typeof msg?.name === "string" ? msg.name : "tool";
        toolCallNameById.set(toolCallIdFallback, name);
        toolCallArgsById.set(toolCallIdFallback, "");
        toolCallIndexById.set(toolCallIdFallback, uiMessages.length);
        messageIndexById.set(toolCallIdFallback, uiMessages.length);
        uiMessages.push({
          id: toolCallIdFallback,
          role: "assistant",
          kind: "tool-call",
          title: `Tool result: ${name}`,
          content: "",
          status: "complete",
          toolCall: {
            toolCallId: toolCallIdFallback,
            toolCallName: name,
            args: "",
            result: normalized,
          },
          timestamp,
        });
      }
      activeThinkingIndex = null;
      continue;
    }

    if (role === "activity") {
      const activityType = typeof msg?.activityType === "string" ? msg.activityType : "";
      if (activityType === "ACTIVITY_DELTA") {
        const content = msg?.content as any;
        const patch = Array.isArray(content?.patch) ? content.patch : [];
        applyJsonPatch(activityState, patch);

        const node = activityState.node;
        if (node && typeof node.nodeId === "string") {
          graphNodeId = node.nodeId;
        }

        if (Object.prototype.hasOwnProperty.call(activityState, "interrupt")) {
          const interrupt = activityState.interrupt;
          if (!interrupt) {
            graphInterrupt = null;
          } else if (typeof interrupt === "object") {
            const key = typeof interrupt.key === "string" ? interrupt.key : "";
            const prompt = typeof interrupt.prompt === "string" ? interrupt.prompt : "Interrupt received.";
            graphInterrupt = {
              key,
              prompt,
              checkpointId: typeof interrupt.checkpointId === "string" ? interrupt.checkpointId : undefined,
              lineageId: typeof interrupt.lineageId === "string" ? interrupt.lineageId : undefined,
            };

            if (shouldSuppressGraphApproval(graphInterrupt, toolCallNameById)) {
              continue;
            }

            const identity = graphInterruptIdentity(graphInterrupt);
            const messageId = identity ? `graph_interrupt_${sanitizeIdPart(identity) || "interrupt"}` : randomId("graph_interrupt");
            upsertUiMessage(messageId, (prev) => {
              const prevArgs = prev?.toolCall?.args ?? "";
              const parsed = prevArgs ? safeJsonParse(prevArgs) : null;
              const prevDecision = parsed && typeof parsed === "object" && typeof (parsed as any).decision === "string"
                ? String((parsed as any).decision)
                : "pending";
              const decision = prevDecision === "approve" || prevDecision === "dismiss" ? prevDecision : "pending";
              const args = JSON.stringify({ prompt, decision }, null, 2);
              const nextTimestamp = prev?.timestamp ?? timestamp;
              return {
                id: messageId,
                role: "assistant",
                kind: "tool-call",
                title: "Graph interrupt approval",
                content: "",
                status: "complete",
                toolCall: {
                  toolCallId: messageId,
                  toolCallName: GRAPH_APPROVAL_TOOL_NAME,
                  parentMessageId: prev?.toolCall?.parentMessageId,
                  args,
                  result: prev?.toolCall?.result,
                },
                timestamp: nextTimestamp,
              };
            });
          }
        }

        if (activityState.resume && typeof activityState.resume === "object") {
          const resume = activityState.resume as any;
          const checkpointId = typeof resume.checkpointId === "string" ? resume.checkpointId : "";
          const lineageId = typeof resume.lineageId === "string" ? resume.lineageId : "";
          const resumeMap = resume.resumeMap && typeof resume.resumeMap === "object" ? resume.resumeMap as Record<string, any> : {};
          const resumeKey = Object.keys(resumeMap)[0] ?? "";
          const resumeValue = resumeKey ? resumeMap[resumeKey] : undefined;
          const decision = resumeKey && resumeValue ? "approve" : "dismiss";

          const identity = [checkpointId, lineageId, resumeKey].filter(Boolean).join("|");
          if (identity) {
            const messageId = `graph_interrupt_${sanitizeIdPart(identity) || "interrupt"}`;
            upsertUiMessage(messageId, (prev) => {
              const prevArgs = prev?.toolCall?.args ?? "";
              const parsed = prevArgs ? safeJsonParse(prevArgs) : null;
              const prompt = parsed && typeof parsed === "object" ? String((parsed as any).prompt ?? "") : "";
              const args = JSON.stringify({ prompt, decision }, null, 2);
              const nextTimestamp = prev?.timestamp ?? timestamp;
              return {
                id: messageId,
                role: "assistant",
                kind: "tool-call",
                title: "Graph interrupt approval",
                content: "",
                status: "complete",
                toolCall: {
                  toolCallId: messageId,
                  toolCallName: GRAPH_APPROVAL_TOOL_NAME,
                  parentMessageId: prev?.toolCall?.parentMessageId,
                  args,
                  result: prev?.toolCall?.result,
                },
                timestamp: nextTimestamp,
              };
            });
          }
        }

        delete activityState.resume;
        continue;
      }

      if (activityType !== "CUSTOM") {
        continue;
      }
      const content = msg?.content;
      const name = typeof (content as any)?.name === "string" ? (content as any).name : "";
      const value = (content as any)?.value;

      if (name === "think_start") {
        upsertThinking(() => ({
          id: typeof msg?.id === "string" ? msg.id : randomId("thinking_snapshot"),
          role: "assistant",
          kind: "thinking",
          title: "Thinking",
          content: "",
          status: "streaming",
          timestamp,
        }));
        continue;
      }

      if (name === "think_content") {
        const chunk = typeof value === "string" ? value : formatStructured(value);
        if (!chunk) {
          continue;
        }
        upsertThinking((prev) => {
          const previous = prev?.content ?? "";
          return {
            id: prev?.id ?? randomId("thinking_snapshot"),
            role: "assistant",
            kind: "thinking",
            title: prev?.title ?? "Thinking",
            content: previous + chunk,
            status: "streaming",
            timestamp: prev?.timestamp ?? timestamp,
          };
        });
        continue;
      }

      if (name === "think_end") {
        if (activeThinkingIndex !== null) {
          const prev = uiMessages[activeThinkingIndex] ?? null;
          uiMessages[activeThinkingIndex] = {
            id: prev?.id ?? (typeof msg?.id === "string" ? msg.id : randomId("thinking_snapshot")),
            role: "assistant",
            kind: "thinking",
            title: prev?.title ?? "Thinking",
            content: prev?.content ?? "",
            status: "complete",
            timestamp: prev?.timestamp ?? timestamp,
          };
        }
        activeThinkingIndex = null;
        continue;
      }

      if (name === "node.progress") {
        continue;
      }

      if (name.startsWith("react.")) {
        const suffix = name.slice("react.".length);
        const title = suffix ? `React ${suffix}` : "React event";
        const text = formatStructured(value);
        if (!text) {
          continue;
        }
        uiMessages.push({
          id: typeof msg?.id === "string" ? msg.id : randomId("react_snapshot"),
          role: "assistant",
          kind: "thinking",
          title,
          content: text,
          status: "complete",
          timestamp,
        });
        activeThinkingIndex = null;
        continue;
      }

      const text = formatStructured(value);
      if (!text) {
        continue;
      }
      uiMessages.push({
        id: typeof msg?.id === "string" ? msg.id : randomId("custom_snapshot"),
        role: "assistant",
        kind: "thinking",
        title: name ? `Custom ${name}` : "Custom event",
        content: text,
        status: "complete",
        timestamp,
      });
      activeThinkingIndex = null;
      continue;
    }
  }

  if (activeThinkingIndex !== null) {
    const prev = uiMessages[activeThinkingIndex] ?? null;
    uiMessages[activeThinkingIndex] = {
      id: prev?.id ?? randomId("thinking_snapshot"),
      role: "assistant",
      kind: "thinking",
      title: prev?.title ?? "Thinking",
      content: prev?.content ?? "",
      status: prev?.status ?? "complete",
      timestamp: prev?.timestamp ?? timestamp,
    };
  }
  return {
    messages: uiMessages,
    reportSessions,
    activeReportId,
    graphNodeId,
    graphInterrupt,
  };
}

export function useAguiChat(config: AguiChatConfig) {
  const [state, dispatch] = useReducer(chatReducer, initialChatState);
  const { messages, rawEvents, session, reportSessions, activeReportId, reportDrawerOpen } = state;
  const { inProgress, finishReason, lastError, graphNodeId, graphInterrupt, progress } = session;

  const abortRef = useRef<AbortController | null>(null);
  const currentRunIdRef = useRef<string>("");
  const cancelTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const messageIndexByIdRef = useRef<Map<string, number>>(new Map());
  const toolCallNameByIdRef = useRef<Map<string, string>>(new Map());
  const toolCallArgsByIdRef = useRef<Map<string, string>>(new Map());
  const activityStateRef = useRef<Record<string, any>>({});
  const activeThinkingRef = useRef<{ id: string; active: boolean } | null>(null);
  const activeReasoningIdsRef = useRef<Set<string>>(new Set());
  const lastReasoningMessageIdRef = useRef<string>("");
  const currentStepIdRef = useRef<string>("");
  const contentBindMap = useRef<Map<string, string>>(new Map());
  const reportSessionsRef = useRef<ReportSession[]>([]);
  const writingReportIdRef = useRef<string | null>(null);
  const graphInterruptIdentityRef = useRef<string | null>(null);
  const graphApprovalMessageIdRef = useRef<string | null>(null);
  const rawEventsRef = useRef<RawAguiEvent[]>([]);
  const rawEventsFlushRef = useRef<number | null>(null);

  reportSessionsRef.current = state.reportSessions;

  /** 使用 useRef 追踪最新消息数组，避免闭包快照过时问题 */
  const messagesRef = useRef<UiMessage[]>(state.messages);
  messagesRef.current = state.messages;

  /** 递归查找指定 ID 的 block/step 消息在消息数组中的索引路径。
   *  返回 [顶层索引, children索引路径] 或 null（未找到）。
   *  children索引路径是一个数组，表示从顶层到目标 block 的每一层 children 索引。
   *  例如 [2] 表示顶层第 3 个消息的 children 中；[2, 1] 表示再下一层。
   *  支持任意多层嵌套 block 的递归查找。 */
  const findBlockIndex = useCallback((messages: UiMessage[], blockId: string): [number, number[]] | null => {
    for (let i = 0; i < messages.length; i++) {
      const msg = messages[i];
      if ((msg.kind === "block" || msg.kind === "step") && msg.id === blockId) {
        return [i, []];
      }
      if ((msg.kind === "block" || msg.kind === "step") && Array.isArray(msg.children)) {
        const result = findBlockIndex(msg.children, blockId);
        if (result) {
          return [i, [result[0], ...result[1]]];
        }
      }
    }
    return null;
  }, []);

  const addMessage = useCallback((message: UiMessage, contentParentId?: string) => {
    const nestable = message.kind === "thinking" || message.kind === "tool-call" || (message.kind === "text" && message.role === "assistant");
    const prev = messagesRef.current;
    // 先追加到顶层
    const next = [...prev, message];
    const msgIdx = next.length - 1;
    messageIndexByIdRef.current.set(message.id, msgIdx);

    // 确定归属 parentId：优先使用传入的 contentParentId，其次查 contentBindMap，最后回退 block.context
    const activeStepId = contentParentId || contentBindMap.current.get(message.id) || currentStepIdRef.current;
    if (activeStepId && nestable) {
      const found = findBlockIndex(next, activeStepId);
      if (found) {
        const [parentIdx, childPath] = found;
        // 从顶层移除
        next.splice(msgIdx, 1);
        // 根据移除后的位置调整索引
        const adjParentIdx = msgIdx < parentIdx ? parentIdx - 1 : parentIdx;
        if (childPath.length === 0) {
          // 顶层 block：直接加入 children
          const container = next[adjParentIdx];
          next[adjParentIdx] = {
            ...container,
            children: [...(container.children ?? []), { ...message, stepId: activeStepId }],
          };
        } else {
          // 嵌套 block：沿 childPath 递归找到目标 block 并加入 children
          const updateNestedChildren = (msgs: UiMessage[], path: number[], depth: number): UiMessage[] => {
            const idx = path[depth];
            const updated = msgs.slice();
            if (depth === path.length - 1) {
              // 最后一层：直接修改目标 block 的 children
              const target = updated[idx];
              updated[idx] = {
                ...target,
                children: [...(target.children ?? []), { ...message, stepId: activeStepId }],
              };
            } else {
              // 中间层：递归修改
              const target = updated[idx];
              updated[idx] = {
                ...target,
                children: updateNestedChildren(target.children ?? [], path, depth + 1),
              };
            }
            return updated;
          };
          const container = next[adjParentIdx];
          next[adjParentIdx] = {
            ...container,
            children: updateNestedChildren(container.children ?? [], childPath, 0),
          };
        }
        // 更新 ref 映射
        messageIndexByIdRef.current.clear();
        next.forEach((msg, i) => {
          messageIndexByIdRef.current.set(msg.id, i);
        });
      }
    }

    dispatch({ type: "SET_MESSAGES", payload: next });
    messagesRef.current = next;
  }, [findBlockIndex]);

  const replaceMessages = useCallback((next: UiMessage[]) => {
    messageIndexByIdRef.current.clear();
    toolCallNameByIdRef.current.clear();
    toolCallArgsByIdRef.current.clear();
    activeThinkingRef.current = null;
    activeReasoningIdsRef.current.clear();
    lastReasoningMessageIdRef.current = "";

    next.forEach((msg, index) => {
      messageIndexByIdRef.current.set(msg.id, index);
      if (msg.kind === "tool-call" && msg.toolCall?.toolCallId) {
        toolCallNameByIdRef.current.set(msg.toolCall.toolCallId, msg.toolCall.toolCallName);
        if (typeof msg.toolCall.args === "string") {
          toolCallArgsByIdRef.current.set(msg.toolCall.toolCallId, msg.toolCall.args);
        }
      }
    });

    dispatch({ type: "REPLACE_MESSAGES", payload: { messages: next, blocks: [], reportSessions: [], activeReportId: null } });
    messagesRef.current = next;
  }, []);

  const upsertMessage = useCallback((id: string, updater: (msg: UiMessage | null) => UiMessage, contentParentId?: string) => {
    const prev = messagesRef.current;
    const index = messageIndexByIdRef.current.get(id);
    if (index !== undefined && index >= 0 && index < prev.length && prev[index]?.id === id) {
      // 顶层找到，直接更新
      const existing = prev[index];
      const updated = updater(existing);
      const next = prev.slice();
      next[index] = updated;
      dispatch({ type: "SET_MESSAGES", payload: next });
      messagesRef.current = next;
      return;
    }

    // 回退：递归搜索块容器的 children（支持多层嵌套）
    let found = false;
    const updateNested = (msgs: UiMessage[]): UiMessage[] => {
      return msgs.map((msg) => {
        if ((msg.kind === "block" || msg.kind === "step") && Array.isArray(msg.children)) {
          const childIdx = msg.children.findIndex((c) => c.id === id);
          if (childIdx !== -1) {
            found = true;
            const newChildren = msg.children.slice();
            newChildren[childIdx] = updater(newChildren[childIdx]);
            return { ...msg, children: newChildren };
          }
          // 递归搜索嵌套 block 的 children
          const updatedChildren = updateNested(msg.children);
          if (updatedChildren !== msg.children) {
            found = true;
            return { ...msg, children: updatedChildren };
          }
        }
        return msg;
      });
    };
    const next = updateNested(prev);

    if (found) {
      dispatch({ type: "SET_MESSAGES", payload: next });
      messagesRef.current = next;
      return;
    }

    // 都不存在：创建新消息
    const created = updater(null);
    // block 类型只有在明确指定 contentParentId 时才可嵌套（由 block.started 传入 parentId）
    const nestable = created.kind === "thinking" || created.kind === "tool-call" || (created.kind === "text" && created.role === "assistant") || (created.kind === "block" && !!contentParentId);
    // 确定归属 parentId：优先使用传入的 contentParentId，其次查 contentBindMap，最后回退 block.context
    const activeStepId = contentParentId || contentBindMap.current.get(created.id) || currentStepIdRef.current;

    if (activeStepId && nestable) {
      const found2 = findBlockIndex(next, activeStepId);
      if (found2) {
        const [parentIdx, childPath] = found2;
        if (childPath.length === 0) {
          // 顶层 block
          const container = next[parentIdx];
          next[parentIdx] = {
            ...container,
            children: [...(container.children ?? []), { ...created, stepId: activeStepId }],
          };
        } else {
          // 嵌套 block：沿 childPath 递归找到目标 block 并加入 children
          const updateNestedChildren = (msgs: UiMessage[], path: number[], depth: number): UiMessage[] => {
            const idx = path[depth];
            const updated = msgs.slice();
            if (depth === path.length - 1) {
              const target = updated[idx];
              updated[idx] = {
                ...target,
                children: [...(target.children ?? []), { ...created, stepId: activeStepId }],
              };
            } else {
              const target = updated[idx];
              updated[idx] = {
                ...target,
                children: updateNestedChildren(target.children ?? [], path, depth + 1),
              };
            }
            return updated;
          };
          const container = next[parentIdx];
          next[parentIdx] = {
            ...container,
            children: updateNestedChildren(container.children ?? [], childPath, 0),
          };
        }
        // 重建索引映射
        messageIndexByIdRef.current.clear();
        next.forEach((msg, i) => {
          messageIndexByIdRef.current.set(msg.id, i);
        });
        dispatch({ type: "SET_MESSAGES", payload: next });
        messagesRef.current = next;
        return;
      }
    }

    const result = [...next, created];
    messageIndexByIdRef.current.set(created.id, result.length - 1);
    dispatch({ type: "SET_MESSAGES", payload: result });
    messagesRef.current = result;
  }, [findBlockIndex]);

  const flushRawEvents = useCallback(() => {
    rawEventsFlushRef.current = null;
    dispatch({ type: "SET_RAW_EVENTS", payload: rawEventsRef.current });
  }, []);

  const setReportOpen = useCallback((open: boolean) => {
    dispatch({ type: "SET_REPORT_DRAWER", payload: open });
  }, []);

  const openReport = useCallback((documentId: string) => {
    if (!documentId) {
      return;
    }
    dispatch({ type: "SET_ACTIVE_REPORT", payload: documentId });
    dispatch({ type: "SET_REPORT_DRAWER", payload: true });
  }, []);

  const updateReportSessions = useCallback((updater: (prev: ReportSession[]) => ReportSession[]) => {
    const next = updater(reportSessionsRef.current);
    reportSessionsRef.current = next;
    dispatch({ type: "SET_REPORT_SESSIONS", payload: next });
  }, []);

  const appendRawEvent = useCallback(
    (evt: AguiSseEvent) => {
      const timestamp = typeof evt.timestamp === "number" ? evt.timestamp : Date.now();
      const type = typeof evt.type === "string" ? evt.type : "UNKNOWN";
      const next: RawAguiEvent = {
        id: randomId("raw"),
        kind: "event",
        type,
        timestamp,
        payload: evt,
      };
      const prev = rawEventsRef.current;
      rawEventsRef.current = [...prev, next];
      if (rawEventsFlushRef.current === null) {
        rawEventsFlushRef.current = window.requestAnimationFrame(flushRawEvents);
      }
    },
    [flushRawEvents],
  );

  const appendRequest = useCallback(
    (request: { endpoint: string; payload: Record<string, any> }) => {
      const next: RawAguiEvent = {
        id: randomId("raw_request"),
        kind: "request",
        type: "RunAgentInput",
        timestamp: Date.now(),
        payload: request,
      };
      const prev = rawEventsRef.current;
      rawEventsRef.current = [...prev, next];
      if (rawEventsFlushRef.current === null) {
        rawEventsFlushRef.current = window.requestAnimationFrame(flushRawEvents);
      }
    },
    [flushRawEvents],
  );

  const clearRawEvents = useCallback(() => {
    rawEventsRef.current = [];
    flushRawEvents();
  }, [flushRawEvents]);

  const abortActiveRun = useCallback(() => {
    const controller = abortRef.current;
    if (!controller) {
      return;
    }
    controller.abort();
    abortRef.current = null;
  }, []);

  const handleCustomEvent = useCallback(
    (evt: AguiSseEvent) => {
      const name = typeof evt.name === "string" ? evt.name : "custom";
      const value = evt.value;

      if (name === "think_start") {
        const id = randomId("thinking");
        activeThinkingRef.current = { id, active: true };
        addMessage({
          id,
          role: "assistant",
          kind: "thinking",
          title: "Thinking",
          content: "",
          status: "streaming",
          timestamp: evt.timestamp ?? Date.now(),
        });
        return;
      }

      if (name === "state.delta") {
        // Gateway sends state.delta with await_external_tool=true when the LLM
        // calls an external tool (EndInvocation). The frontend should display
        // a tool-result input UI after RUN_FINISHED arrives.
        return;
      }

      if (name === "think_content") {
        const chunk = typeof value === "string" ? value : formatStructured(value);
        const active = activeThinkingRef.current;
        if (!active?.id) {
          const id = randomId("thinking");
          activeThinkingRef.current = { id, active: true };
          addMessage({
            id,
            role: "assistant",
            kind: "thinking",
            title: "Thinking",
            content: chunk,
            status: "streaming",
            timestamp: evt.timestamp ?? Date.now(),
          });
          return;
        }
        upsertMessage(active.id, (msg) => {
          const prev = msg?.content ?? "";
          return {
            id: active.id,
            role: "assistant",
            kind: "thinking",
            title: "Thinking",
            content: prev + chunk,
            status: "streaming",
            timestamp: msg?.timestamp ?? evt.timestamp ?? Date.now(),
          };
        });
        return;
      }

      if (name === "think_end") {
        const active = activeThinkingRef.current;
        if (active?.id) {
          upsertMessage(active.id, (msg) => {
            return {
              id: active.id,
              role: "assistant",
              kind: "thinking",
              title: msg?.title ?? "Thinking",
              content: msg?.content ?? "",
              status: "complete",
              timestamp: msg?.timestamp ?? evt.timestamp ?? Date.now(),
            };
          });
        }
        activeThinkingRef.current = null;
        return;
      }

      if (name === "node.progress") {
        const parsed = extractActivityValue(value) ?? {};
        const percent = Number(parsed.progress ?? parsed.percent ?? parsed.value ?? 0);
        const label = typeof parsed.message === "string" ? parsed.message : undefined;
        if (!Number.isNaN(percent)) {
          dispatch({ type: "SET_PROGRESS", payload: { percent, label } });
        }
        return;
      }

      if (name.startsWith("react.")) {
        const content = formatStructured(value);
        if (!content) {
          return;
        }
        const suffix = name.slice("react.".length);
        const title = suffix ? `React ${suffix}` : "React event";
        addMessage({
          id: randomId("react"),
          role: "assistant",
          kind: "thinking",
          title,
          content,
          status: "complete",
          timestamp: evt.timestamp ?? Date.now(),
        });
        return;
      }

      // ---- 分块事件处理 ----

      if (name === "block.context") {
        // value: { stepId: string } — 设置当前分块上下文（降级兜底）
        const payload = extractActivityValue(value) ?? {};
        const ctxStepId = typeof payload.stepId === "string" ? payload.stepId : "";
        currentStepIdRef.current = ctxStepId;
        return;
      }

      if (name === "block.content_bind") {
        // value: { contentId: string, parentId: string } — 内容归属声明
        const payload = extractActivityValue(value) ?? {};
        const contentId = typeof payload.contentId === "string" ? payload.contentId : "";
        const bindParentId = typeof payload.parentId === "string" ? payload.parentId : "";
        if (contentId) {
          contentBindMap.current.set(contentId, bindParentId);
        }
        return;
      }

      if (name === "block.started") {
        // value: { id: string, parentId?: string, status?: string, displayName?: string, blockType?: string, ... }
        const payload = extractActivityValue(value) ?? {};
        const blockId = typeof payload.id === "string" ? payload.id : "";
        if (!blockId) return;
        const blockParentId = typeof payload.parentId === "string" ? payload.parentId : "";
        dispatch({
          type: "BLOCK_STARTED",
          payload: {
            blockId,
            parentId: blockParentId || undefined,
            displayName: payload.displayName,
            blockType: payload.blockType,
            status: payload.status,
            timestamp: evt.timestamp ?? Date.now(),
          },
        });
        upsertMessage(blockId, (msg) => ({
          id: blockId,
          role: "assistant",
          kind: "block",
          title: payload.displayName ?? msg?.title ?? blockId,
          content: "",
          status: "complete",
          children: msg?.children ?? [],
          startedAt: evt.timestamp ?? Date.now(),
          stepId: blockParentId || msg?.stepId,
          stepTitle: payload.displayName ?? msg?.stepTitle ?? blockId,
          blockType: payload.blockType ?? msg?.blockType ?? "todo",
          stepStatus: payload.status ?? "in_progress",
          timestamp: msg?.timestamp ?? Date.now(),
        }), blockParentId || undefined);
        return;
      }

      if (name === "block.finished") {
        // value: { id: string, parentId?: string, status?: string, ... }
        const payload = extractActivityValue(value) ?? {};
        const blockId = typeof payload.id === "string" ? payload.id : "";
        if (!blockId) return;
        const blockParentId = typeof payload.parentId === "string" ? payload.parentId : "";
        dispatch({
          type: "BLOCK_FINISHED",
          payload: {
            blockId,
            parentId: blockParentId || undefined,
            status: payload.status,
            displayName: payload.displayName,
            timestamp: evt.timestamp ?? Date.now(),
          },
        });
        upsertMessage(blockId, (msg) => ({
          id: blockId,
          role: "assistant",
          kind: "block",
          title: payload.displayName ?? msg?.title ?? blockId,
          content: "",
          status: "complete",
          children: msg?.children ?? [],
          startedAt: msg?.startedAt,
          completedAt: evt.timestamp ?? Date.now(),
          stepId: blockParentId || msg?.stepId,
          stepTitle: payload.displayName ?? msg?.stepTitle ?? blockId,
          blockType: payload.blockType ?? msg?.blockType ?? "todo",
          stepStatus: payload.status ?? "completed",
          timestamp: msg?.timestamp ?? Date.now(),
        }));
        // 如果当前上下文指向该分块，回退到父级 ID（支持多层嵌套回退）
        if (currentStepIdRef.current === blockId) {
          currentStepIdRef.current = blockParentId;
        }
        return;
      }

      if (name === "block.plan") {
        // value: { steps: Array<{ id, displayName?, blockType?, status?, parentId? }> }
        const plan = extractActivityValue(value) ?? {};
        const steps = Array.isArray(plan.steps) ? plan.steps : [];
        if (steps.length === 0) return;

        dispatch({
          type: "BLOCK_PLAN",
          payload: { steps: steps.map((s: any) => ({ id: s.id, displayName: s.displayName, blockType: s.blockType, status: s.status, parentId: s.parentId })) },
        });

        // 同时更新 messages 中的 block（用于渲染）
        const prev = messagesRef.current;
        const newIds = new Set(steps.map((s: any) => String(s.id ?? "")).filter(Boolean));
        const orphanIndices = new Set<number>();
        prev.forEach((msg, i) => {
          if ((msg.kind === "step" || msg.kind === "block") && msg.id && !newIds.has(msg.id)) {
            orphanIndices.add(i);
          }
        });

        const nextMsgs = prev.slice();
        const toRemove = new Set(orphanIndices);
        const now = Date.now();

        for (let si = 0; si < steps.length; si++) {
          const ps = steps[si] as any;
          const stepId = String(ps.id ?? "");
          if (!stepId) continue;

          const existingIdx = nextMsgs.findIndex((m) => m.id === stepId && (m.kind === "block" || m.kind === "step"));
          if (existingIdx !== -1) {
            const existing = nextMsgs[existingIdx];
            nextMsgs[existingIdx] = {
              ...existing,
              title: ps.displayName ?? existing.title,
              stepTitle: ps.displayName ?? existing.stepTitle,
              stepId: ps.parentId ?? existing.stepId,
            };
            continue;
          }

          const fresh: UiMessage = {
            id: stepId,
            role: "assistant",
            kind: "block",
            title: ps.displayName ?? stepId,
            content: "",
            status: "complete",
            stepId: ps.parentId || undefined,
            children: [],
            stepTitle: ps.displayName ?? stepId,
            blockType: ps.blockType ?? "todo",
            stepStatus: ps.status ?? "pending",
            timestamp: now + si,
          };
          nextMsgs.push(fresh);
        }

        const sortedRemove = Array.from(toRemove).sort((a, b) => b - a);
        for (const ri of sortedRemove) {
          nextMsgs.splice(ri, 1);
        }

        messageIndexByIdRef.current.clear();
        nextMsgs.forEach((msg, i) => {
          messageIndexByIdRef.current.set(msg.id, i);
        });

        dispatch({ type: "SET_MESSAGES", payload: nextMsgs });
        messagesRef.current = nextMsgs;
        return;
      }
    },
    [addMessage, upsertMessage, dispatch],
  );

  const handleReasoningEvent = useCallback(
    (evt: AguiSseEvent) => {
      const type = typeof evt.type === "string" ? evt.type : "";
      const rawMessageId = typeof evt.messageId === "string" ? evt.messageId : "";
      if (type === "REASONING_MESSAGE_CHUNK" && rawMessageId) {
        lastReasoningMessageIdRef.current = rawMessageId;
      }
      let messageId = rawMessageId;
      if (!messageId && type === "REASONING_MESSAGE_CHUNK") {
        messageId = lastReasoningMessageIdRef.current;
      }
      if (!messageId) {
        return;
      }
      const thinkingId = reasoningUiMessageId(messageId);
      const timestamp = evt.timestamp ?? Date.now();
      // Start or resume a reasoning stream.
      if (type === "REASONING_START" || type === "REASONING_MESSAGE_START") {
        activeReasoningIdsRef.current.add(thinkingId);
        // 通过 contentBindMap 查找归属 parentId（使用原始 messageId）
        const reasoningParentId = contentBindMap.current.get(rawMessageId) || undefined;
        upsertMessage(thinkingId, (msg) => {
          return {
            id: thinkingId,
            role: "assistant",
            kind: "thinking",
            title: msg?.title ?? "Thinking",
            content: msg?.content ?? "",
            status: "streaming",
            timestamp: msg?.timestamp ?? timestamp,
          };
        }, reasoningParentId);
        return;
      }
      if (type === "REASONING_MESSAGE_CHUNK") {
        const delta = typeof evt.delta === "string" ? evt.delta : undefined;
        // Treat an explicit empty delta as a close signal for the current reasoning message.
        if (delta === "") {
          activeReasoningIdsRef.current.delete(thinkingId);
          if (lastReasoningMessageIdRef.current === messageId) {
            lastReasoningMessageIdRef.current = "";
          }
          if (!messageIndexByIdRef.current.has(thinkingId)) {
            return;
          }
          upsertMessage(thinkingId, (msg) => {
            return {
              id: thinkingId,
              role: "assistant",
              kind: "thinking",
              title: msg?.title ?? "Thinking",
              content: msg?.content ?? "",
              status: "complete",
              timestamp: msg?.timestamp ?? timestamp,
            };
          });
          return;
        }
        if (!delta) {
          return;
        }
        activeReasoningIdsRef.current.add(thinkingId);
        upsertMessage(thinkingId, (msg) => {
          const prev = msg?.content ?? "";
          return {
            id: thinkingId,
            role: "assistant",
            kind: "thinking",
            title: msg?.title ?? "Thinking",
            content: prev + delta,
            status: "streaming",
            timestamp: msg?.timestamp ?? timestamp,
          };
        });
        return;
      }
      // Append reasoning delta to the UI message.
      if (type === "REASONING_MESSAGE_CONTENT") {
        const delta = typeof evt.delta === "string" ? evt.delta : "";
        if (!delta) {
          return;
        }
        activeReasoningIdsRef.current.add(thinkingId);
        upsertMessage(thinkingId, (msg) => {
          const prev = msg?.content ?? "";
          // Fix: Server may send the complete accumulated text as a final flush delta
          // right before REASONING_MESSAGE_END. If delta starts with prev and is longer,
          // it's a final flush — replace content instead of appending to avoid duplication.
          if (prev && delta.length > prev.length && delta.startsWith(prev)) {
            return {
              id: thinkingId,
              role: "assistant",
              kind: "thinking",
              title: msg?.title ?? "Thinking",
              content: delta,
              status: "streaming",
              timestamp: msg?.timestamp ?? timestamp,
            };
          }
          return {
            id: thinkingId,
            role: "assistant",
            kind: "thinking",
            title: msg?.title ?? "Thinking",
            content: prev + delta,
            status: "streaming",
            timestamp: msg?.timestamp ?? timestamp,
          };
        });
        return;
      }
      // Mark the reasoning message as complete.
      if (type === "REASONING_MESSAGE_END" || type === "REASONING_END") {
        activeReasoningIdsRef.current.delete(thinkingId);
        if (lastReasoningMessageIdRef.current === messageId) {
          lastReasoningMessageIdRef.current = "";
        }
        if (!messageIndexByIdRef.current.has(thinkingId)) {
          return;
        }
        upsertMessage(thinkingId, (msg) => {
          return {
            id: thinkingId,
            role: "assistant",
            kind: "thinking",
            title: msg?.title ?? "Thinking",
            content: msg?.content ?? "",
            status: "complete",
            timestamp: msg?.timestamp ?? timestamp,
          };
        });
      }
    },
    [upsertMessage],
  );

  const handleActivityDelta = useCallback(
    (evt: AguiSseEvent) => {
      const ops = Array.isArray(evt.patch) ? evt.patch : [];
      const root = activityStateRef.current;
      applyJsonPatch(root, ops);

      const node = root.node;
      if (node && typeof node.nodeId === "string") {
        dispatch({ type: "SET_GRAPH_NODE_ID", payload: node.nodeId });
      }

      if (Object.prototype.hasOwnProperty.call(root, "interrupt")) {
        const interrupt = root.interrupt;
        if (!interrupt) {
          dispatch({ type: "SET_GRAPH_INTERRUPT", payload: null });
          graphInterruptIdentityRef.current = null;
          graphApprovalMessageIdRef.current = null;
        } else if (typeof interrupt === "object") {
          const key = typeof interrupt.key === "string" ? interrupt.key : "";
          const prompt = typeof interrupt.prompt === "string" ? interrupt.prompt : "Interrupt received.";
          const nextInterrupt: GraphInterrupt = {
            key,
            prompt,
            checkpointId: typeof interrupt.checkpointId === "string" ? interrupt.checkpointId : undefined,
            lineageId: typeof interrupt.lineageId === "string" ? interrupt.lineageId : undefined,
          };
          dispatch({ type: "SET_GRAPH_INTERRUPT", payload: nextInterrupt });

          if (shouldSuppressGraphApproval(nextInterrupt, toolCallNameByIdRef.current)) {
            graphInterruptIdentityRef.current = null;
            graphApprovalMessageIdRef.current = null;
            delete root.resume;
            return;
          }

          const identity = graphInterruptIdentity(nextInterrupt);
          if (identity && graphInterruptIdentityRef.current !== identity) {
            graphInterruptIdentityRef.current = identity;
            graphApprovalMessageIdRef.current = `graph_interrupt_${sanitizeIdPart(identity) || "interrupt"}`;
          }
          if (!graphApprovalMessageIdRef.current) {
            graphApprovalMessageIdRef.current = randomId("graph_interrupt");
          }

          const messageId = graphApprovalMessageIdRef.current;
          upsertMessage(messageId, (msg) => {
            const prevArgs = msg?.toolCall?.args ?? "";
            const parsed = prevArgs ? safeJsonParse(prevArgs) : null;
            const prevDecision = parsed && typeof parsed === "object" && typeof (parsed as any).decision === "string"
              ? String((parsed as any).decision)
              : "pending";
            const decision = prevDecision === "approve" || prevDecision === "dismiss" ? prevDecision : "pending";
            const args = JSON.stringify({ prompt, decision }, null, 2);
            const timestamp = msg?.timestamp ?? evt.timestamp ?? Date.now();
            return {
              id: messageId,
              role: "assistant",
              kind: "tool-call",
              title: "Graph interrupt approval",
              content: "",
              status: "complete",
              toolCall: {
                toolCallId: messageId,
                toolCallName: GRAPH_APPROVAL_TOOL_NAME,
                parentMessageId: msg?.toolCall?.parentMessageId,
                args,
                result: msg?.toolCall?.result,
              },
              timestamp,
            };
          });
        }
      }

      delete root.resume;
    },
    [upsertMessage, dispatch],
  );

  const handleToolCallResult = useCallback(
    (evt: AguiSseEvent) => {
      const toolCallId = typeof evt.toolCallId === "string" ? evt.toolCallId : "";
      const toolName = toolCallNameByIdRef.current.get(toolCallId) ?? "";
      const raw = typeof evt.content === "string" ? evt.content : "";
      const structured = raw ? formatStructured(safeJsonParse(raw)) : "";
      const normalized = structured ? normalizeJsonString(structured) : undefined;

      if (toolName === "open_report_document") {
        const payload = (typeof raw === "string" && raw ? safeJsonParse(raw) : {}) as any;
        const title = typeof payload?.title === "string" ? payload.title : "Report";
        const documentId = typeof payload?.documentId === "string" ? payload.documentId : toolCallId || randomId("doc");
        const createdAt = typeof payload?.createdAt === "string" ? payload.createdAt : undefined;
        const next: ReportSession = { status: "open", title, documentId, createdAt, content: "" };
        writingReportIdRef.current = documentId;
        updateReportSessions((prev) => {
          const index = prev.findIndex((session) => session.documentId === documentId);
          if (index === -1) {
            return [...prev, next];
          }
          const copy = prev.slice();
          copy[index] = { ...copy[index], ...next };
          return copy;
        });
        dispatch({ type: "SET_ACTIVE_REPORT", payload: documentId });
        dispatch({ type: "SET_REPORT_DRAWER", payload: true });

        const actionMessageId = `report_open_${documentId}`;
        const actionPayload: Record<string, any> = { title, documentId, status: "open" };
        if (createdAt) {
          actionPayload.createdAt = createdAt;
        }
        upsertMessage(actionMessageId, (msg) => {
          const timestamp = msg?.timestamp ?? evt.timestamp ?? Date.now();
          return {
            id: actionMessageId,
            role: "assistant",
            kind: "tool-call",
            title: "Open report",
            content: "",
            status: "complete",
            toolCall: {
              toolCallId: actionMessageId,
              toolCallName: REPORT_OPEN_TOOL_NAME,
              parentMessageId: typeof evt.parentMessageId === "string" ? evt.parentMessageId : msg?.toolCall?.parentMessageId,
              args: "",
              result: JSON.stringify(actionPayload, null, 2),
            },
            timestamp,
          };
        });
      }

      if (toolName === "close_report_document") {
        const payload = (typeof raw === "string" && raw ? safeJsonParse(raw) : {}) as any;
        const closedAt = typeof payload?.closedAt === "string" ? payload.closedAt : undefined;
        const reason = typeof payload?.reason === "string"
          ? payload.reason
          : typeof payload?.message === "string"
            ? payload.message
            : undefined;
        const payloadDocumentId = typeof payload?.documentId === "string" ? payload.documentId : "";
        const targetId = payloadDocumentId.trim()
          || writingReportIdRef.current
          || reportSessionsRef.current.slice().reverse().find((session) => session.status === "open")?.documentId
          || "";
        if (targetId) {
          const session = reportSessionsRef.current.find((entry) => entry.documentId === targetId) ?? null;
          updateReportSessions((prev) => {
            const index = prev.findIndex((session) => session.documentId === targetId);
            if (index === -1) {
              return prev;
            }
            const copy = prev.slice();
            copy[index] = { ...copy[index], status: "closed", closedAt, reason };
            return copy;
          });
          const actionMessageId = `report_open_${targetId}`;
          const actionPayload: Record<string, any> = {
            title: session?.title ?? "Report",
            documentId: targetId,
            status: "closed",
          };
          if (session?.createdAt) {
            actionPayload.createdAt = session.createdAt;
          }
          if (closedAt) {
            actionPayload.closedAt = closedAt;
          }
          if (reason) {
            actionPayload.reason = reason;
          }
          upsertMessage(actionMessageId, (msg) => {
            const timestamp = msg?.timestamp ?? evt.timestamp ?? Date.now();
            return {
              id: actionMessageId,
              role: "assistant",
              kind: "tool-call",
              title: "Open report",
              content: "",
              status: "complete",
              toolCall: {
                toolCallId: actionMessageId,
                toolCallName: REPORT_OPEN_TOOL_NAME,
                parentMessageId: msg?.toolCall?.parentMessageId,
                args: "",
                result: JSON.stringify(actionPayload, null, 2),
              },
              timestamp,
            };
          });
        }
        if (writingReportIdRef.current && writingReportIdRef.current === targetId) {
          writingReportIdRef.current = null;
        }
      }

      if (toolCallId) {
        upsertMessage(toolCallId, (msg) => {
          const args = msg?.toolCall?.args ?? toolCallArgsByIdRef.current.get(toolCallId) ?? msg?.content ?? "";
          const name = msg?.toolCall?.toolCallName ?? (toolName || "tool");
          return {
            id: toolCallId,
            role: "assistant",
            kind: "tool-call",
            title: `Tool call: ${name}`,
            content: args,
            status: "complete",
            toolCall: {
              toolCallId,
              toolCallName: name,
              parentMessageId: typeof evt.parentMessageId === "string" ? evt.parentMessageId : msg?.toolCall?.parentMessageId,
              args,
              result: normalized ?? msg?.toolCall?.result,
            },
            timestamp: msg?.timestamp ?? evt.timestamp ?? Date.now(),
          };
        });
      }
    },
    [updateReportSessions, upsertMessage],
  );

  const handleEvent = useCallback(
    (evt: AguiSseEvent) => {
      const type = typeof evt.type === "string" ? evt.type : "UNKNOWN";

      if (type === "RUN_STARTED") {
        appendRawEvent(evt);
        dispatch({ type: "RUN_STARTED", payload: { timestamp: evt.timestamp ?? Date.now() } });
        lastReasoningMessageIdRef.current = "";
        const activeReasoningIds = Array.from(activeReasoningIdsRef.current);
        activeReasoningIdsRef.current.clear();
        const timestamp = evt.timestamp ?? Date.now();
        for (const thinkingId of activeReasoningIds) {
          if (!messageIndexByIdRef.current.has(thinkingId)) {
            continue;
          }
          upsertMessage(thinkingId, (msg) => {
            return {
              id: thinkingId,
              role: "assistant",
              kind: "thinking",
              title: msg?.title ?? "Thinking",
              content: msg?.content ?? "",
              status: "complete",
              timestamp: msg?.timestamp ?? timestamp,
            };
          });
        }
        return;
      }

      if (type === "RUN_FINISHED") {
        appendRawEvent(evt);
        // 清除取消超时兜底（如果 cancel 后正常收到了 RunFinished）
        if (cancelTimeoutRef.current !== null) {
          clearTimeout(cancelTimeoutRef.current);
          cancelTimeoutRef.current = null;
        }
        abortRef.current = null;
        currentRunIdRef.current = "";
        contentBindMap.current.clear();
        const result = typeof evt.result === "string" ? evt.result : undefined;
        dispatch({ type: "RUN_FINISHED", payload: { result, timestamp: evt.timestamp ?? Date.now() } });
        lastReasoningMessageIdRef.current = "";
        const activeReasoningIds = Array.from(activeReasoningIdsRef.current);
        activeReasoningIdsRef.current.clear();
        const timestamp = evt.timestamp ?? Date.now();
        for (const thinkingId of activeReasoningIds) {
          if (!messageIndexByIdRef.current.has(thinkingId)) {
            continue;
          }
          upsertMessage(thinkingId, (msg) => {
            return {
              id: thinkingId,
              role: "assistant",
              kind: "thinking",
              title: msg?.title ?? "Thinking",
              content: msg?.content ?? "",
              status: "complete",
              timestamp: msg?.timestamp ?? timestamp,
            };
          });
        }
        // 标记所有 streaming 状态的 tool-call 消息为 complete
        // （EndInvocation 时 Run 停止但 tool-call 未收到完成事件，需在此兜底）
        for (const msg of messagesRef.current) {
          if (msg.kind === "tool-call" && msg.status === "streaming") {
            upsertMessage(msg.id, (m) => ({
              ...m!,
              status: "complete" as const,
            }));
          }
        }
        return;
      }

      if (type === "RUN_ERROR") {
        appendRawEvent(evt);
        // 清除取消超时兜底
        if (cancelTimeoutRef.current !== null) {
          clearTimeout(cancelTimeoutRef.current);
          cancelTimeoutRef.current = null;
        }
        abortRef.current = null;
        currentRunIdRef.current = "";
        const msg = typeof evt.message === "string" ? evt.message : "Run error.";
        dispatch({ type: "RUN_ERROR", payload: { message: msg } });
        lastReasoningMessageIdRef.current = "";
        const activeReasoningIds = Array.from(activeReasoningIdsRef.current);
        activeReasoningIdsRef.current.clear();
        const timestamp = evt.timestamp ?? Date.now();
        for (const thinkingId of activeReasoningIds) {
          if (!messageIndexByIdRef.current.has(thinkingId)) {
            continue;
          }
          upsertMessage(thinkingId, (msg) => {
            return {
              id: thinkingId,
              role: "assistant",
              kind: "thinking",
              title: msg?.title ?? "Thinking",
              content: msg?.content ?? "",
              status: "complete",
              timestamp: msg?.timestamp ?? timestamp,
            };
          });
        }
        return;
      }
      // Handle REASONING_* events.
      if (type.startsWith("REASONING_")) {
        appendRawEvent(evt);
        handleReasoningEvent(evt);
        return;
      }
      if (type === "TEXT_MESSAGE_START") {
        appendRawEvent(evt);
        const writingId = writingReportIdRef.current;
        if (writingId) {
          const active = reportSessionsRef.current.find((session) => session.documentId === writingId) ?? null;
          if (active?.status === "open") {
            updateReportSessions((prev) => {
              const index = prev.findIndex((session) => session.documentId === writingId);
              if (index === -1) {
                return prev;
              }
              const current = prev[index];
              const needsGap = current.content.trim().length > 0;
              if (!needsGap) {
                return prev;
              }
              const copy = prev.slice();
              copy[index] = { ...current, content: `${current.content}\n\n` };
              return copy;
            });
            return;
          }
        }
        const messageId = typeof evt.messageId === "string" ? evt.messageId : randomId("assistant");
        // 通过 contentBindMap 查找归属 parentId
        const textParentId = contentBindMap.current.get(messageId) || undefined;
        addMessage({
          id: messageId,
          role: normalizeRole(evt.role),
          kind: "text",
          title: "Assistant",
          content: "",
          timestamp: evt.timestamp ?? Date.now(),
        }, textParentId);
        return;
      }

      if (type === "TEXT_MESSAGE_CONTENT") {
        appendRawEvent(evt);
        const delta = typeof evt.delta === "string" ? evt.delta : "";
        if (!delta) {
          return;
        }
        const writingId = writingReportIdRef.current;
        if (writingId) {
          const active = reportSessionsRef.current.find((session) => session.documentId === writingId) ?? null;
          if (active?.status === "open") {
            updateReportSessions((prev) => {
              const index = prev.findIndex((session) => session.documentId === writingId);
              if (index === -1) {
                return prev;
              }
              const current = prev[index];
              const copy = prev.slice();
              copy[index] = { ...current, content: current.content + delta };
              return copy;
            });
            return;
          }
        }
        const messageId = typeof evt.messageId === "string" ? evt.messageId : "";
        if (!messageId) {
          return;
        }
        upsertMessage(messageId, (msg) => {
          const prev = msg?.content ?? "";
          return {
            id: messageId,
            role: "assistant",
            kind: "text",
            title: msg?.title ?? "Assistant",
            content: prev + delta,
            timestamp: msg?.timestamp ?? evt.timestamp ?? Date.now(),
          };
        });
        return;
      }

      if (type === "TOOL_CALL_START") {
        appendRawEvent(evt);
        const toolCallId = typeof evt.toolCallId === "string" ? evt.toolCallId : randomId("tool_call");
        const toolCallName = typeof evt.toolCallName === "string" ? evt.toolCallName : "tool";
        toolCallNameByIdRef.current.set(toolCallId, toolCallName);
        toolCallArgsByIdRef.current.set(toolCallId, "");
        // 通过 contentBindMap 查找归属 parentId
        const toolParentId = contentBindMap.current.get(toolCallId) || undefined;
        addMessage({
          id: toolCallId,
          role: "assistant",
          kind: "tool-call",
          title: `Tool call: ${toolCallName}`,
          content: "",
          status: "streaming",
          toolCall: {
            toolCallId,
            toolCallName,
            parentMessageId: typeof evt.parentMessageId === "string" ? evt.parentMessageId : undefined,
            args: "",
          },
          timestamp: evt.timestamp ?? Date.now(),
        }, toolParentId);
        return;
      }

      if (type === "TOOL_CALL_ARGS") {
        appendRawEvent(evt);
        const toolCallId = typeof evt.toolCallId === "string" ? evt.toolCallId : "";
        const delta = typeof evt.delta === "string" ? evt.delta : "";
        if (!toolCallId || !delta) {
          return;
        }
        const prev = toolCallArgsByIdRef.current.get(toolCallId) ?? "";
        toolCallArgsByIdRef.current.set(toolCallId, prev + delta);
        upsertMessage(toolCallId, (msg) => {
          const next = (msg?.content ?? "") + delta;
          const toolCallName = msg?.toolCall?.toolCallName ?? toolCallNameByIdRef.current.get(toolCallId) ?? "tool";
          return {
            id: toolCallId,
            role: "assistant",
            kind: "tool-call",
            title: msg?.title ?? "Tool call",
            content: next,
            status: msg?.status ?? "streaming",
            toolCall: {
              toolCallId,
              toolCallName,
              parentMessageId: msg?.toolCall?.parentMessageId,
              args: next,
              result: msg?.toolCall?.result,
            },
            timestamp: msg?.timestamp ?? evt.timestamp ?? Date.now(),
          };
        });
        return;
      }

      if (type === "TOOL_CALL_RESULT") {
        appendRawEvent(evt);
        handleToolCallResult(evt);
        return;
      }

      if (type === "CUSTOM") {
        appendRawEvent(evt);
        handleCustomEvent(evt);
        return;
      }

      if (type === "ACTIVITY_DELTA") {
        appendRawEvent(evt);
        handleActivityDelta(evt);
        return;
      }
      appendRawEvent(evt);
    },
    [addMessage, appendRawEvent, handleActivityDelta, handleCustomEvent, handleReasoningEvent, handleToolCallResult, updateReportSessions, upsertMessage],
  );

  const stop = useCallback(() => {
    abortActiveRun();
    dispatch({ type: "RUN_FINISHED", payload: { result: "stopped", timestamp: Date.now() } });
  }, [abortActiveRun]);

  const cancel = useCallback(async () => {
    const runId = currentRunIdRef.current;

    // 先向服务端发送 cancel 请求，让后端通过 SSE 发送收尾事件（RunCanceled + RunFinished）
    // 前端等收到 RunFinished 后由 handleEvent 自动重置 inProgress 和 abortRef
    if (!runId) {
      // 没有 runId 时直接中断 SSE 连接
      abortActiveRun();
      dispatch({ type: "RUN_FINISHED", payload: { result: "cancelled", timestamp: Date.now() } });
      return;
    }
    const cancelPayload: Record<string, any> = {
      threadId: config.threadId,
      runId,
    };
    if (config.forwardedProps && Object.keys(config.forwardedProps).length > 0) {
      cancelPayload.forwardedProps = config.forwardedProps;
    }
    try {
      // 从 endpoint 推导 cancel 路径：/chat -> /cancel, /agui -> /cancel
      const baseUrl = new URL(config.endpoint, window.location.origin);
      const cancelUrl = new URL("/cancel", baseUrl.origin);
      const response = await fetch(cancelUrl.toString(), {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(cancelPayload),
      });
      if (response.ok) {
        // cancel 请求成功，等待 RunFinished 事件通过 SSE 到达后自然断开
        // 设置超时兜底：如果 5 秒内没收到 RunFinished，强制中断
        const timeoutId = setTimeout(() => {
          abortActiveRun();
          dispatch({ type: "RUN_FINISHED", payload: { result: "cancelled", timestamp: Date.now() } });
          currentRunIdRef.current = "";
        }, 5000);
        // 保存 timeoutId 以便 RunFinished 到达时清除
        cancelTimeoutRef.current = timeoutId;
        return;
      }
    } catch {
      // cancel 请求失败，静默处理
    }
    // cancel 请求失败或响应非 OK，直接中断 SSE 连接
    abortActiveRun();
    dispatch({ type: "RUN_FINISHED", payload: { result: "cancelled", timestamp: Date.now() } });
    currentRunIdRef.current = "";
  }, [abortActiveRun, config.endpoint, config.forwardedProps, config.threadId]);

  const run = useCallback(
    async (payload: Record<string, any>) => {
      abortActiveRun();
      const controller = new AbortController();
      abortRef.current = controller;
      currentRunIdRef.current = typeof payload.runId === "string" ? payload.runId : "";
      dispatch({ type: "RUN_STARTED", payload: { timestamp: Date.now() } });
      appendRequest({ endpoint: config.endpoint, payload });

      try {
        await streamAguiSse(config.endpoint, payload, {
          signal: controller.signal,
          onEvent: handleEvent,
        });
      } catch (error: any) {
        if (controller.signal.aborted) {
          return;
        }
        abortRef.current = null;
        dispatch({ type: "RUN_ERROR", payload: { message: String(error?.message ?? error) } });
      }
    },
    [abortActiveRun, appendRequest, config.endpoint, handleEvent],
  );

  const loadHistory = useCallback(
    async (options?: { endpoint?: string; threadId?: string; forwardedProps?: Record<string, unknown> }): Promise<HistoryLoadResult> => {
      const historyEndpoint = options?.endpoint ?? "";
      if (!historyEndpoint) {
        const message = "history endpoint is empty";
        dispatch({ type: "RUN_ERROR", payload: { message } });
        return { ok: false, message };
      }

      abortActiveRun();
      dispatch({ type: "RUN_STARTED", payload: { timestamp: Date.now() } });
      const controller = new AbortController();
      abortRef.current = controller;

      let snapshotEvent: AguiSseEvent | null = null;
      let runError: string | null = null;

      const threadId = options?.threadId ?? config.threadId;
      const forwardedProps = options?.forwardedProps ?? config.forwardedProps;

      const payload: Record<string, any> = {
        threadId,
        runId: randomId("history"),
        messages: [{ role: "user", content: "" }],
      };
      if (forwardedProps && Object.keys(forwardedProps).length > 0) {
        payload.forwardedProps = forwardedProps;
      }
      appendRequest({ endpoint: historyEndpoint, payload });

      return await new Promise<HistoryLoadResult>((resolve) => {
        let settled = false;
        const settle = (result: HistoryLoadResult) => {
          if (settled) {
            return;
          }
          settled = true;
          resolve(result);
        };

        const applySnapshot = (evt: AguiSseEvent) => {
          snapshotEvent = evt;
          const restored = restoreFromMessagesSnapshot(evt);
          replaceMessages(restored.messages);
          reportSessionsRef.current = restored.reportSessions;
          dispatch({ type: "SET_REPORT_SESSIONS", payload: restored.reportSessions });
          const openReport = restored.reportSessions.slice().reverse().find((session) => session.status === "open") ?? null;
          writingReportIdRef.current = openReport ? openReport.documentId : null;
          const nextActiveReportId = openReport?.documentId ?? restored.activeReportId;
          dispatch({ type: "SET_ACTIVE_REPORT", payload: nextActiveReportId });
          dispatch({ type: "SET_REPORT_DRAWER", payload: Boolean(openReport) });
          dispatch({ type: "SET_GRAPH_NODE_ID", payload: restored.graphNodeId });
          dispatch({ type: "SET_GRAPH_INTERRUPT", payload: restored.graphInterrupt ?? null });
          settle({ ok: true, count: restored.messages.length });
        };

        streamAguiSse(historyEndpoint, payload, {
          signal: controller.signal,
          onEvent: (evt) => {
            const type = typeof evt.type === "string" ? evt.type : "";
            if (type === "RUN_ERROR") {
              appendRawEvent(evt);
              const message = typeof (evt as any).message === "string" ? (evt as any).message : "Run error.";
              runError = message;
              dispatch({ type: "RUN_ERROR", payload: { message } });
              if (abortRef.current === controller) {
                abortRef.current = null;
              }
              if (!snapshotEvent) {
                settle({ ok: false, message });
              }
              return;
            }
            if (type === "MESSAGES_SNAPSHOT") {
              appendRawEvent(evt);
              applySnapshot(evt);
              return;
            }
            handleEvent(evt);
          },
        }).then(() => {
          if (controller.signal.aborted) {
            settle({ ok: false, message: "aborted" });
            return;
          }
          if (!snapshotEvent) {
            const message = runError ?? "history snapshot not found";
            if (!isSessionNotFoundError(message)) {
              dispatch({ type: "RUN_ERROR", payload: { message } });
            }
            settle({ ok: false, message });
            if (abortRef.current === controller) {
              abortRef.current = null;
              dispatch({ type: "RUN_FINISHED", payload: { result: "error", timestamp: Date.now() } });
            }
            return;
          }
          if (abortRef.current === controller) {
            abortRef.current = null;
            dispatch({ type: "RUN_FINISHED", payload: { result: "completed", timestamp: Date.now() } });
          }
        }).catch((error: any) => {
          if (controller.signal.aborted) {
            settle({ ok: false, message: "aborted" });
            if (abortRef.current === controller) {
              abortRef.current = null;
              dispatch({ type: "RUN_FINISHED", payload: { result: "aborted", timestamp: Date.now() } });
            }
            return;
          }
          const message = String(error?.message ?? error);
          if (!isSessionNotFoundError(message)) {
            dispatch({ type: "RUN_ERROR", payload: { message } });
          }
          settle({ ok: false, message });
          if (abortRef.current === controller) {
            abortRef.current = null;
            dispatch({ type: "RUN_FINISHED", payload: { result: "error", timestamp: Date.now() } });
          }
        });
      });
    },
    [
      abortActiveRun,
      appendRawEvent,
      appendRequest,
      config.forwardedProps,
      config.threadId,
      handleEvent,
      replaceMessages,
      dispatch,
    ],
  );

  const send = useCallback(
    async (text: string, options?: { forwardedProps?: Record<string, unknown> }) => {
      const trimmed = text.trim();
      if (!trimmed) {
        return;
      }

      addMessage({
        id: randomId("user"),
        role: "user",
        kind: "text",
        title: "You",
        content: trimmed,
        timestamp: Date.now(),
      });

      const payload: Record<string, any> = {
        threadId: config.threadId,
        runId: randomId("run"),
        messages: [{ role: "user", content: trimmed }],
      };
      // 将用户名称也放入消息的 name 字段，确保服务端能通过消息级路径提取用户ID
      const userIdFromProps = config.forwardedProps?.userId;
      if (typeof userIdFromProps === "string" && userIdFromProps.trim()) {
        payload.messages[0].name = userIdFromProps.trim();
      }
      if (config.tools && config.tools.length > 0) {
        payload.tools = config.tools;
      }
      const mergedForwardedProps = {
        ...(config.forwardedProps && Object.keys(config.forwardedProps).length > 0 ? config.forwardedProps : {}),
        ...(options?.forwardedProps && Object.keys(options.forwardedProps).length > 0 ? options.forwardedProps : {}),
      };
      if (Object.keys(mergedForwardedProps).length > 0) {
        payload.forwardedProps = mergedForwardedProps;
      }

      await run(payload);
    },
    [addMessage, config.forwardedProps, config.threadId, config.tools, run],
  );

  const sendToolResult = useCallback(
    async (args: {
      toolCallId: string;
      toolCallName: string;
      content: string;
      messageId?: string;
      forwardedProps?: Record<string, unknown>;
    }) => {
      if (inProgress) {
        return;
      }

      const toolCallId = (args.toolCallId || "").trim();
      const toolCallName = (args.toolCallName || "").trim();
      const content = (args.content || "").trim();
      if (!toolCallId || !content) {
        return;
      }

      // Build messages array: assistant toolCalls + tool result.
      // The assistant message carries the tool_call info so the backend
      // can inject it into the LLM context (EndInvocation doesn't persist it).
      const savedName = toolCallNameByIdRef.current.get(toolCallId) || toolCallName;
      const savedArgs = toolCallArgsByIdRef.current.get(toolCallId) || "";
      const messages: Record<string, any>[] = [
        {
          id: `assistant-toolcall-${toolCallId}`,
          role: "assistant",
          toolCalls: [{
            id: toolCallId,
            type: "function",
            function: {
              name: savedName,
              arguments: savedArgs,
            },
          }],
        },
        {
          id: (args.messageId || `tool-result-${toolCallId}`).trim(),
          role: "tool",
          toolCallId,
          name: savedName || "tool",
          content,
        },
      ];

      const payload: Record<string, any> = {
        threadId: config.threadId,
        runId: randomId("run"),
        messages,
      };
      if (config.tools && config.tools.length > 0) {
        payload.tools = config.tools;
      }
      const mergedForwardedProps = {
        ...(config.forwardedProps && Object.keys(config.forwardedProps).length > 0 ? config.forwardedProps : {}),
        ...(args.forwardedProps && Object.keys(args.forwardedProps).length > 0 ? args.forwardedProps : {}),
      };
      if (Object.keys(mergedForwardedProps).length > 0) {
        payload.forwardedProps = mergedForwardedProps;
      }

      await run(payload);
    },
    [config.forwardedProps, config.threadId, config.tools, inProgress, run],
  );

  const approveGraphInterrupt = useCallback(async () => {
    if (!graphInterrupt) {
      return;
    }

    const identity = graphInterruptIdentity(graphInterrupt);
    const derivedMessageId = identity ? `graph_interrupt_${sanitizeIdPart(identity) || "interrupt"}` : "";
    const messageId = graphApprovalMessageIdRef.current || derivedMessageId || randomId("graph_interrupt");
    graphApprovalMessageIdRef.current = messageId;

    upsertMessage(messageId, (msg) => {
      const prevArgs = msg?.toolCall?.args ?? "";
      const parsed = prevArgs ? safeJsonParse(prevArgs) : null;
      const prompt = graphInterrupt.prompt || (parsed && typeof parsed === "object" ? String((parsed as any).prompt ?? "") : "");
      const args = JSON.stringify({ prompt, decision: "approve" }, null, 2);
      return {
        id: msg?.id ?? messageId,
        role: "assistant",
        kind: "tool-call",
        title: msg?.title ?? "Graph interrupt approval",
        content: msg?.content ?? "",
        status: msg?.status ?? "complete",
        toolCall: {
          toolCallId: messageId,
          toolCallName: GRAPH_APPROVAL_TOOL_NAME,
          parentMessageId: msg?.toolCall?.parentMessageId,
          args,
          result: msg?.toolCall?.result,
        },
        timestamp: msg?.timestamp ?? Date.now(),
      };
    });

    const resumeMap: Record<string, any> = {};
    if (graphInterrupt.key) {
      resumeMap[graphInterrupt.key] = true;
    }

    const state: Record<string, any> = {};
    if (graphInterrupt.lineageId) {
      state.lineage_id = graphInterrupt.lineageId;
    }
    if (graphInterrupt.checkpointId) {
      state.checkpoint_id = graphInterrupt.checkpointId;
    }
    state.resume_map = resumeMap;

    const payload: Record<string, any> = {
      threadId: config.threadId,
      runId: randomId("run"),
      state,
      messages: [{ role: "user", content: "" }],
    };
    const mergedForwardedProps = {
      ...(config.forwardedProps && Object.keys(config.forwardedProps).length > 0 ? config.forwardedProps : {}),
      ...(graphInterrupt.lineageId ? { lineage_id: graphInterrupt.lineageId } : {}),
    };
    if (Object.keys(mergedForwardedProps).length > 0) {
      payload.forwardedProps = mergedForwardedProps;
    }

    dispatch({ type: "SET_GRAPH_INTERRUPT", payload: null });
    graphInterruptIdentityRef.current = null;
    graphApprovalMessageIdRef.current = null;
    await run(payload);
  }, [config.forwardedProps, config.threadId, graphInterrupt, run, upsertMessage, dispatch]);

  const dismissGraphInterrupt = useCallback(async () => {
    const identity = graphInterrupt ? graphInterruptIdentity(graphInterrupt) : "";
    const derivedMessageId = identity ? `graph_interrupt_${sanitizeIdPart(identity) || "interrupt"}` : "";
    const messageId = graphApprovalMessageIdRef.current || derivedMessageId || (graphInterrupt ? randomId("graph_interrupt") : "");
    if (messageId) {
      graphApprovalMessageIdRef.current = messageId;
      upsertMessage(messageId, (msg) => {
        const prevArgs = msg?.toolCall?.args ?? "";
        const parsed = prevArgs ? safeJsonParse(prevArgs) : null;
        const prompt = graphInterrupt?.prompt || (parsed && typeof parsed === "object" ? String((parsed as any).prompt ?? "") : "");
        const args = JSON.stringify({ prompt, decision: "dismiss" }, null, 2);
        return {
          id: msg?.id ?? messageId,
          role: "assistant",
          kind: "tool-call",
          title: msg?.title ?? "Graph interrupt approval",
          content: msg?.content ?? "",
          status: msg?.status ?? "complete",
          toolCall: {
            toolCallId: messageId,
            toolCallName: GRAPH_APPROVAL_TOOL_NAME,
            parentMessageId: msg?.toolCall?.parentMessageId,
            args,
            result: msg?.toolCall?.result,
          },
          timestamp: msg?.timestamp ?? Date.now(),
        };
      });
    }

    if (!graphInterrupt) {
      dispatch({ type: "SET_GRAPH_INTERRUPT", payload: null });
      graphInterruptIdentityRef.current = null;
      graphApprovalMessageIdRef.current = null;
      return;
    }

    const resumeMap: Record<string, any> = {};
    if (graphInterrupt.key) {
      resumeMap[graphInterrupt.key] = false;
    }

    const state: Record<string, any> = {};
    if (graphInterrupt.lineageId) {
      state.lineage_id = graphInterrupt.lineageId;
    }
    if (graphInterrupt.checkpointId) {
      state.checkpoint_id = graphInterrupt.checkpointId;
    }
    state.resume_map = resumeMap;

    const payload: Record<string, any> = {
      threadId: config.threadId,
      runId: randomId("run"),
      state,
      messages: [{ role: "user", content: "" }],
    };
    const mergedForwardedProps = {
      ...(config.forwardedProps && Object.keys(config.forwardedProps).length > 0 ? config.forwardedProps : {}),
      ...(graphInterrupt?.lineageId ? { lineage_id: graphInterrupt.lineageId } : {}),
    };
    if (Object.keys(mergedForwardedProps).length > 0) {
      payload.forwardedProps = mergedForwardedProps;
    }

    dispatch({ type: "SET_GRAPH_INTERRUPT", payload: null });
    graphInterruptIdentityRef.current = null;
    graphApprovalMessageIdRef.current = null;
    await run(payload);
  }, [config.forwardedProps, config.threadId, graphInterrupt, run, upsertMessage, dispatch]);

  const reset = useCallback(() => {
    abortActiveRun();
    dispatch({ type: "RESET" });
    messageIndexByIdRef.current.clear();
    toolCallNameByIdRef.current.clear();
    toolCallArgsByIdRef.current.clear();
    activityStateRef.current = {};
    contentBindMap.current.clear();
    currentStepIdRef.current = "";
    activeThinkingRef.current = null;
    reportSessionsRef.current = [];
    writingReportIdRef.current = null;
    clearRawEvents();
    graphInterruptIdentityRef.current = null;
    graphApprovalMessageIdRef.current = null;
  }, [abortActiveRun, clearRawEvents]);

  return {
    messages,
    rawEvents,
    inProgress,
    finishReason,
    lastError,
    graphNodeId,
    graphInterrupt,
    progress,
    reportSessions,
    activeReportId,
    reportDrawerOpen,
    setReportOpen,
    setReportDrawerOpen: setReportOpen,
    openReport,
    loadHistory,
    send,
    stop,
    cancel,
    reset,
    approveGraphInterrupt,
    dismissGraphInterrupt,
    sendToolResult,
    clearRawEvents,
  };
}
