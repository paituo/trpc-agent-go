import { useEffect, useMemo, useRef, useState, type FC, type PointerEvent as ReactPointerEvent } from "react";
import { ChatMarkdown, ChatMessage, ToolCallRenderer, agentToolcallRegistry, type ToolCall } from "@tdesign-react/chat";
import {
  Alert,
  Button,
  Divider,
  Drawer,
  Input,
  Layout,
  Progress,
  Space,
  Tag,
  Textarea,
} from "tdesign-react";
import { ChatIcon, RefreshIcon, StopCircleIcon } from "tdesign-icons-react";
import { formatTimestamp } from "./agui/format";
import { useAguiChat, type RawAguiEvent, type StepStatus, type UiMessage } from "./hooks/useAguiChat";

const AGUI_OPEN_REPORT_EVENT = "agui-open-report";
const AGUI_GRAPH_APPROVAL_EVENT = "agui-graph-approval";
const REPORT_OPEN_TOOL_NAME = "open_report_sidebar";
const GRAPH_APPROVAL_TOOL_NAME = "graph_interrupt_approval";
const DEFAULT_INPUT_MESSAGE = "计算123+456";
const EXTERNAL_HITL_TOOL_NAMES = ["hitl_notification", "hitl_decision", "hitl_permission", "hitl_progress"];
type HITLType = "notification" | "decision" | "permission" | "progress";

/** AG-UI 协议 tools 字段中的工具声明格式 */
interface AguiToolDeclaration {
  name: string;
  description: string;
  parameters: Record<string, any>;
}

/** 四种 HITL 扩展工具的完整声明，通过 payload.tools 传递给服务端 */
const HITL_TOOL_DECLARATIONS: AguiToolDeclaration[] = [
  {
    name: "hitl_notification",
    description: "向用户发送通知并等待确认。用于需要用户知悉的重要信息、警告或状态变更。",
    parameters: {
      type: "object",
      properties: {
        title: { type: "string", description: "通知标题" },
        detail: { type: "string", description: "通知详细内容" },
        severity: { type: "string", enum: ["info", "warning", "critical"], description: "严重程度" },
        request_id: { type: "string", description: "请求唯一标识" },
        affected_resources: { type: "array", items: { type: "string" }, description: "受影响的资源列表" },
      },
      required: ["title", "request_id"],
      "x-hitl-type": "notification",
    },
  },
  {
    name: "hitl_decision",
    description: "向用户请求决策选择。用于需要人工判断的多选项场景，支持预设选项和自由输入。",
    parameters: {
      type: "object",
      properties: {
        title: { type: "string", description: "决策标题" },
        description: { type: "string", description: "决策背景说明" },
        request_id: { type: "string", description: "请求唯一标识" },
        options: {
          type: "array",
          items: {
            type: "object",
            properties: {
              id: { type: "string", description: "选项标识" },
              label: { type: "string", description: "选项显示文本" },
              style: { type: "string", enum: ["primary", "danger", "default"], description: "选项样式" },
            },
            required: ["id", "label"],
          },
          description: "可选决策项列表",
        },
        allow_free_input: { type: "boolean", description: "是否允许用户自由输入" },
      },
      required: ["title", "request_id", "options"],
      "x-hitl-type": "decision",
    },
  },
  {
    name: "hitl_permission",
    description: "向用户请求操作权限审批。用于高风险操作（如文件删除、数据修改、外部调用）的人工授权。",
    parameters: {
      type: "object",
      properties: {
        title: { type: "string", description: "权限请求标题" },
        action: { type: "string", description: "请求执行的操作" },
        resource: { type: "string", description: "目标资源" },
        request_id: { type: "string", description: "请求唯一标识" },
        risk_level: { type: "string", enum: ["low", "medium", "high", "critical"], description: "风险等级" },
        justification: { type: "string", description: "操作理由" },
        required_roles: { type: "array", items: { type: "string" }, description: "需要具备的角色" },
        scope_constraints: {
          type: "array",
          items: {
            type: "object",
            properties: {
              key: { type: "string", description: "约束键名" },
              label: { type: "string", description: "约束显示名" },
              options: { type: "array", items: { type: "string" }, description: "可选范围值" },
            },
            required: ["key", "label", "options"],
          },
          description: "范围约束列表",
        },
        expires_in_seconds: { type: "number", description: "权限过期时间（秒）" },
      },
      required: ["title", "action", "resource", "request_id"],
      "x-hitl-type": "permission",
    },
  },
  {
    name: "hitl_progress",
    description: "向用户展示任务进度并接受操作指令。用于长时间运行任务的进度跟踪和人工干预。",
    parameters: {
      type: "object",
      properties: {
        title: { type: "string", description: "任务标题" },
        progress: { type: "number", description: "进度百分比 (0-100)" },
        status: { type: "string", enum: ["running", "waiting", "blocked", "completed"], description: "任务状态" },
        request_id: { type: "string", description: "请求唯一标识" },
        detail: { type: "string", description: "进度详情" },
        next_step: { type: "string", description: "下一步操作描述" },
        estimated_remaining_seconds: { type: "number", description: "预计剩余秒数" },
        issues: {
          type: "array",
          items: {
            type: "object",
            properties: {
              level: { type: "string", enum: ["error", "warning"], description: "问题级别" },
              message: { type: "string", description: "问题描述" },
            },
            required: ["level", "message"],
          },
          description: "当前问题列表",
        },
        allow_actions: { type: "array", items: { type: "string", enum: ["continue", "pause", "abort"] }, description: "允许的操作" },
      },
      required: ["title", "progress", "request_id"],
      "x-hitl-type": "progress",
    },
  },
];

function getHITLType(toolName: string, parametersSchema?: any): HITLType | null {
  if (parametersSchema?.["x-hitl-type"]) {
    return parametersSchema["x-hitl-type"];
  }
  const HITL_PREFIX = "hitl_";
  if (toolName.startsWith(HITL_PREFIX)) {
    return toolName.slice(HITL_PREFIX.length) as HITLType;
  }
  return null;
}

function createThreadId(): string {
  if (typeof crypto !== "undefined") {
    if (typeof crypto.randomUUID === "function") {
      return crypto.randomUUID().replace(/-/g, "").slice(0, 12);
    }
    if (typeof crypto.getRandomValues === "function") {
      const bytes = new Uint8Array(6);
      crypto.getRandomValues(bytes);
      return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
    }
  }
  const rand = Math.random().toString(16).slice(2).padEnd(12, "0");
  return rand.slice(0, 12);
}

function isHttpUrl(value: string): boolean {
  return /^https?:\/\//i.test(value.trim());
}

function isLoopbackHost(hostname: string): boolean {
  const normalized = hostname.trim().toLowerCase();
  return normalized === "localhost" || normalized === "127.0.0.1";
}

function parseHostname(address: string): string {
  const trimmed = address.trim();
  if (!trimmed) {
    return "";
  }
  try {
    return new URL(trimmed).hostname;
  } catch {}
  try {
    return new URL(`http://${trimmed}`).hostname;
  } catch {}
  return "";
}

function shouldUseDevProxy(base: string): boolean {
  if (!import.meta.env.DEV) {
    return false;
  }
  if (typeof window === "undefined") {
    return false;
  }
  const baseHost = parseHostname(base);
  if (!isLoopbackHost(baseHost)) {
    return false;
  }
  return !isLoopbackHost(window.location.hostname);
}

function normalizePath(path: string): string {
  const trimmed = path.trim();
  if (!trimmed) {
    return "/";
  }
  if (trimmed.startsWith("/")) {
    return trimmed;
  }
  return `/${trimmed}`;
}

function buildHttpUrl(base: string, pathOrUrl: string): string {
  const trimmed = pathOrUrl.trim();
  if (!trimmed) {
    return "";
  }
  if (isHttpUrl(trimmed)) {
    if (shouldUseDevProxy(trimmed)) {
      try {
        const url = new URL(trimmed);
        return normalizePath(url.pathname + url.search);
      } catch {
        return trimmed;
      }
    }
    return trimmed;
  }
  if (shouldUseDevProxy(base)) {
    return normalizePath(trimmed);
  }
  const baseTrimmed = base.trim() || "127.0.0.1:8080";
  const baseUrl = isHttpUrl(baseTrimmed) ? baseTrimmed : `http://${baseTrimmed}`;
  return new URL(normalizePath(trimmed), baseUrl).toString();
}

const SIDER_WIDTH_STORAGE_KEY = "agui-tdesign-chat:sider-width";
const DEFAULT_SIDER_WIDTH = 600;
const MIN_SIDER_WIDTH = 320;
const MAX_SIDER_WIDTH = 960;

function clampNumber(value: number, min: number, max: number): number {
  if (!Number.isFinite(value)) {
    return min;
  }
  return Math.min(max, Math.max(min, value));
}

function loadSiderWidth(): number {
  if (typeof window === "undefined") {
    return DEFAULT_SIDER_WIDTH;
  }
  try {
    const raw = window.localStorage.getItem(SIDER_WIDTH_STORAGE_KEY);
    if (!raw) {
      return DEFAULT_SIDER_WIDTH;
    }
    const parsed = Number(raw);
    if (!Number.isFinite(parsed)) {
      return DEFAULT_SIDER_WIDTH;
    }
    return clampNumber(parsed, MIN_SIDER_WIDTH, MAX_SIDER_WIDTH);
  } catch {
    return DEFAULT_SIDER_WIDTH;
  }
}

function persistSiderWidth(width: number): void {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.localStorage.setItem(SIDER_WIDTH_STORAGE_KEY, String(width));
  } catch {}
}

type ToolcallStatus = "idle" | "executing" | "complete" | "error";
type ToolcallComponentProps = {
  status: ToolcallStatus;
  args: Record<string, unknown>;
  result?: unknown;
  error?: Error;
  respond?: (response: unknown) => void;
  agentState?: Record<string, unknown>;
};

function formatToolcallValue(value: unknown): string {
  if (value === null || value === undefined) {
    return "";
  }
  if (typeof value === "string") {
    return value.trim();
  }
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function toolcallStatusLabel(status: ToolcallStatus): string {
  if (status === "executing") {
    return "Running";
  }
  if (status === "complete") {
    return "Done";
  }
  if (status === "error") {
    return "Error";
  }
  return "Idle";
}

function toolcallStatusTheme(status: ToolcallStatus): "default" | "primary" | "success" | "danger" {
  if (status === "executing") {
    return "primary";
  }
  if (status === "complete") {
    return "success";
  }
  if (status === "error") {
    return "danger";
  }
  return "default";
}

function reportStatusTheme(status: string): "warning" | "success" | "default" {
  if (status === "open") {
    return "warning";
  }
  if (status === "closed") {
    return "success";
  }
  return "default";
}

function reportStatusLabel(status: string): string {
  if (status === "open") {
    return "生成中";
  }
  if (status === "closed") {
    return "已完成";
  }
  return status || "Unknown";
}

const toolcallComponentCache = new Map<string, FC<ToolcallComponentProps>>();

function extractReportDocumentId(result: unknown): string {
  if (!result || typeof result !== "object") {
    return "";
  }
  const anyResult = result as any;
  const documentId = typeof anyResult.documentId === "string"
    ? anyResult.documentId
    : typeof anyResult.documentID === "string"
      ? anyResult.documentID
      : "";
  return documentId.trim();
}

function extractReportTitle(args: Record<string, unknown>, result: unknown): string {
  if (result && typeof result === "object" && typeof (result as any).title === "string") {
    return String((result as any).title).trim() || "Report";
  }
  if (typeof args?.title === "string") {
    return String(args.title).trim() || "Report";
  }
  return "Report";
}

function getGenericToolcallComponent(toolCallName: string): FC<ToolcallComponentProps> {
  const cached = toolcallComponentCache.get(toolCallName);
  if (cached) {
    return cached;
  }
  const Component: FC<ToolcallComponentProps> = ({ status, args, result, error }) => {
    const argsText = formatToolcallValue(args);
    const resultText = formatToolcallValue(result);
    const errorText = error ? String(error.message || error) : "";
    const hasResult = result !== undefined;

    return (
      <details className="toolcall">
        <summary className="toolcall__summary">
          <span className="toolcall__summary-title">{toolCallName}</span>
        </summary>
        <div className="toolcall__body">
          <Space size="small" align="center" breakLine>
            <Tag theme={toolcallStatusTheme(status)} variant="outline">{toolcallStatusLabel(status)}</Tag>
          </Space>
          <Divider style={{ margin: "10px 0" }} />
          <div className="toolcall__panels">
            <details className="toolcall__panel">
              <summary className="toolcall__panel-summary">
                <span className="toolcall__panel-title">工具调用</span>
              </summary>
              <pre className="toolcall__code">{argsText || "(empty)"}</pre>
            </details>
            <details className="toolcall__panel">
              <summary className="toolcall__panel-summary">
                <span className="toolcall__panel-title">工具结果</span>
              </summary>
              {status === "error" ? (
                <pre className="toolcall__code">{errorText || "(unknown error)"}</pre>
              ) : (
                <pre className="toolcall__code">{hasResult ? resultText || "(empty)" : "等待结果..."}</pre>
              )}
            </details>
          </div>
        </div>
      </details>
    );
  };
  toolcallComponentCache.set(toolCallName, Component);
  return Component;
}

function ensureToolcallRegistered(toolCallName: string) {
  if (!toolCallName) {
    return;
  }
  if (agentToolcallRegistry.get(toolCallName)) {
    return;
  }
  agentToolcallRegistry.register({
    name: toolCallName,
    description: `AG-UI tool call: ${toolCallName}.`,
    component: getGenericToolcallComponent(toolCallName),
  });
}

ensureToolcallRegistered("calculator");
ensureToolcallRegistered("open_report_document");
ensureToolcallRegistered("close_report_document");

agentToolcallRegistry.register({
  name: REPORT_OPEN_TOOL_NAME,
  description: "Open a report sidebar in the AG-UI frontend.",
  component: ({ args, result }) => {
    const documentId = extractReportDocumentId(result);
    const title = extractReportTitle(args, result);
    const reportStatus = result && typeof result === "object" && typeof (result as any).status === "string"
      ? String((result as any).status)
      : "open";
    const createdAt = result && typeof result === "object" && typeof (result as any).createdAt === "string"
      ? String((result as any).createdAt)
      : "";
    const closedAt = result && typeof result === "object" && typeof (result as any).closedAt === "string"
      ? String((result as any).closedAt)
      : "";
    const reason = result && typeof result === "object" && typeof (result as any).reason === "string"
      ? String((result as any).reason)
      : "";

    return (
      <div className="toolcall">
        <div className="toolcall__summary">
          <span className="toolcall__summary-title">打开报告</span>
        </div>
        <div className="toolcall__body">
          <Space size="small" align="center" breakLine>
            <Tag theme={reportStatusTheme(reportStatus)} variant="outline">{reportStatusLabel(reportStatus)}</Tag>
            <Button
              size="small"
              theme="primary"
              disabled={!documentId}
              onClick={() => {
                if (!documentId) {
                  return;
                }
                window.dispatchEvent(new CustomEvent(AGUI_OPEN_REPORT_EVENT, { detail: { documentId } }));
              }}
            >
              打开报告
            </Button>
          </Space>
          <Divider style={{ margin: "10px 0" }} />
          <Space direction="vertical" size="small" style={{ width: "100%" }}>
            <div>
              <Tag theme="default" variant="outline">Title</Tag>{" "}
              <span>{title}</span>
            </div>
            {documentId ? (
              <div>
                <Tag theme="default" variant="outline">docId</Tag>{" "}
                <span>{documentId}</span>
              </div>
            ) : null}
            {createdAt ? (
              <div>
                <Tag theme="default" variant="outline">createdAt</Tag>{" "}
                <span>{createdAt}</span>
              </div>
            ) : null}
            {closedAt ? (
              <div>
                <Tag theme="default" variant="outline">closedAt</Tag>{" "}
                <span>{closedAt}</span>
              </div>
            ) : null}
            {reason ? (
              <div>
                <Tag theme="default" variant="outline">reason</Tag>{" "}
                <span>{reason}</span>
              </div>
            ) : null}
          </Space>
        </div>
      </div>
    );
  },
});

agentToolcallRegistry.register({
  name: GRAPH_APPROVAL_TOOL_NAME,
  description: "Approve an AG-UI graph interrupt in the frontend.",
  component: ({ args }) => {
    const prompt = typeof (args as any)?.prompt === "string" ? String((args as any).prompt) : "";
    const decision = typeof (args as any)?.decision === "string" ? String((args as any).decision) : "pending";
    const decided = decision === "approve" || decision === "dismiss";
    const alertTheme: "success" | "warning" | "error" = decision === "approve" ? "success" : decision === "dismiss" ? "error" : "warning";
    const decisionLabel = decision === "approve" ? "已选择：允许" : decision === "dismiss" ? "已选择：拒绝" : "";
    const decisionTagTheme: "success" | "danger" = decision === "approve" ? "success" : "danger";

    return (
      <div className="toolcall">
        <Alert
          theme={alertTheme}
          title="审批（人机交互）"
          message={prompt ? <div style={{ whiteSpace: "pre-wrap" }}>{prompt}</div> : undefined}
          operation={(
            decided ? (
              <Tag theme={decisionTagTheme} variant="outline">{decisionLabel}</Tag>
            ) : (
              <Space size="small">
                <Button
                  size="small"
                  theme="primary"
                  onClick={() => window.dispatchEvent(new CustomEvent(AGUI_GRAPH_APPROVAL_EVENT, { detail: { action: "approve" } }))}
                >
                  允许
                </Button>
                <Button
                  size="small"
                  variant="outline"
                  theme="danger"
                  onClick={() => window.dispatchEvent(new CustomEvent(AGUI_GRAPH_APPROVAL_EVENT, { detail: { action: "dismiss" } }))}
                >
                  拒绝
                </Button>
              </Space>
            )
          )}
        />
      </div>
    );
  },
});

function summarizeRawEvent(event: RawAguiEvent): string {
  if (event.kind === "request") {
    const payload = event.payload as any;
    const endpoint = typeof payload?.endpoint === "string" ? payload.endpoint : "";
    const body = payload?.payload as any;
    const threadId = typeof body?.threadId === "string" ? body.threadId : "";
    const runId = typeof body?.runId === "string" ? body.runId : "";
    const role = typeof body?.messages?.[0]?.role === "string" ? body.messages[0].role : "";
    return [
      endpoint ? `endpoint=${endpoint}` : "",
      threadId ? `threadId=${threadId}` : "",
      runId ? `runId=${runId}` : "",
      role ? `role=${role}` : "",
    ].filter(Boolean).join(" ");
  }
  const payload = event.payload as any;
  if (event.type === "CUSTOM") {
    const name = typeof payload?.name === "string" ? payload.name : "";
    return name ? `name=${name}` : "";
  }
  if (event.type === "ACTIVITY_DELTA") {
    const activityType = typeof payload?.activityType === "string" ? payload.activityType : "";
    return activityType ? `activityType=${activityType}` : "";
  }
  if (event.type === "TEXT_MESSAGE_START") {
    const id = typeof payload?.messageId === "string" ? payload.messageId : "";
    return id ? `messageId=${id}` : "";
  }
  if (event.type === "TEXT_MESSAGE_CONTENT") {
    const delta = typeof payload?.delta === "string" ? payload.delta : "";
    const trimmed = delta.replace(/\s+/g, " ").trim();
    if (!trimmed) {
      return "";
    }
    const short = trimmed.length > 42 ? `${trimmed.slice(0, 42)}…` : trimmed;
    return `delta=${short}`;
  }
  if (event.type === "TOOL_CALL_START") {
    const tool = typeof payload?.toolCallName === "string" ? payload.toolCallName : "";
    return tool ? `tool=${tool}` : "";
  }
  if (event.type === "TOOL_CALL_ARGS") {
    const delta = typeof payload?.delta === "string" ? payload.delta : "";
    const trimmed = delta.replace(/\s+/g, " ").trim();
    if (!trimmed) {
      return "";
    }
    const short = trimmed.length > 42 ? `${trimmed.slice(0, 42)}…` : trimmed;
    return `args=${short}`;
  }
  if (event.type === "TOOL_CALL_RESULT") {
    const id = typeof payload?.toolCallId === "string" ? payload.toolCallId : "";
    return id ? `toolCallId=${id}` : "";
  }
  if (event.type === "RUN_FINISHED") {
    const result = typeof payload?.result === "string" ? payload.result : "";
    return result ? `result=${result}` : "";
  }
  return "";
}

function isVisibleMainMessage(message: UiMessage): boolean {
  if (message.kind === "thinking") {
    return true;
  }
  if (message.kind === "tool-call") {
    return true;
  }
  if (message.kind === "step" || message.kind === "block") {
    return true;
  }
  if (message.kind === "text" && (message.role === "user" || message.role === "assistant")) {
    return true;
  }
  return false;
}

/** 根据工具名返回合适的图标 emoji。找不到对应时返回默认 🔧。 */
function pickToolIcon(name: string | undefined | null): string {
  if (!name) return "🔧";
  const low = String(name).toLowerCase();
  if (low.includes("search") || low.includes("检索") || low.includes("知识库") || low.includes("vector")) return "🔍";
  if (low.includes("file") || low.includes("fs_") || low.includes("save") || low.includes("write") || low.includes("读取") || low.includes("文件")) return "📁";
  if (low.includes("web") || low.includes("http") || low.includes("browser") || low.includes("网页")) return "🌐";
  if (low.includes("read") || low.includes("pdf") || low.includes("doc")) return "📄";
  if (low.includes("code") || low.includes("run") || low.includes("exec") || low.includes("代码") || low.includes("执行")) return "💻";
  if (low.includes("image") || low.includes("draw") || low.includes("图片") || low.includes("画图")) return "🎨";
  if (low.includes("db") || low.includes("sql") || low.includes("数据库")) return "🗄️";
  if (low.includes("todo") || low.includes("plan") || low.includes("计划") || low.includes("todo")) return "✅";
  if (low.includes("agent") || low.includes("智能")) return "🤖";
  return "🔧";
}

/** 步骤状态图标：用于步骤面板内的圆形状态指示器。 */
function stepStatusGlyph(status: StepStatus): string {
  switch (status) {
    case "pending": return "·";
    case "running":
    case "in_progress": return "↻";
    case "completed": return "✓";
    case "failed": return "✕";
    case "skipped": return "—";
    default: return "·";
  }
}

/** 步骤状态中文标签。 */
function stepStatusText(status: StepStatus): string {
  switch (status) {
    case "pending": return "待执行";
    case "running":
    case "in_progress": return "执行中";
    case "completed": return "已完成";
    case "failed": return "失败";
    case "skipped": return "已跳过";
    default: return status ?? "未知";
  }
}

type RenderedChatItem =
  | {
      kind: "user";
      key: string;
      message: UiMessage;
    }
  | {
      kind: "assistant";
      key: string;
      messages: UiMessage[];
    };

function groupChatItems(messages: UiMessage[]): RenderedChatItem[] {
  const items: RenderedChatItem[] = [];
  let assistantGroup: UiMessage[] = [];

  const flushAssistantGroup = () => {
    if (assistantGroup.length === 0) {
      return;
    }
    items.push({
      kind: "assistant",
      key: `assistant-${assistantGroup[0]?.id ?? "unknown"}`,
      messages: assistantGroup,
    });
    assistantGroup = [];
  };

  // 规则：
  // - user text 消息：先 flush 之前的 assistant 组，再作为独立 user 块插入。
  // - 其它消息（assistant text、thinking、tool-call、step 块）都归入"最近的 assistant 组"。
  //   这样 step 块会与它前后的 assistant 文本放在同一个气泡中，文本在前、step 块在后。
  for (const message of messages) {
    if (message.kind === "text" && message.role === "user") {
      flushAssistantGroup();
      items.push({ kind: "user", key: `user-${message.id}`, message });
      continue;
    }
    assistantGroup.push(message);
  }

  flushAssistantGroup();
  return items;
}

export default function App() {
  const [siderWidth, setSiderWidth] = useState<number>(() => loadSiderWidth());
  const [isResizingSider, setIsResizingSider] = useState(false);
  const siderResizeRef = useRef<{ startX: number; startWidth: number } | null>(null);
  const [serverAddress, setServerAddress] = useState<string>("127.0.0.1:7878");
  const [serverAddressDraft, setServerAddressDraft] = useState<string>("127.0.0.1:7878");
  const [endpointPath, setEndpointPath] = useState<string>("/chat");
  const [endpointPathDraft, setEndpointPathDraft] = useState<string>("/chat");
  const [historyPathDraft, setHistoryPathDraft] = useState<string>("/history");
  const [historyHint, setHistoryHint] = useState<string>("");
  const [userId, setUserId] = useState<string>("demo-user");
  const initialThreadId = useMemo(() => createThreadId(), []);
  const [threadId, setThreadId] = useState<string>(initialThreadId);
  const [threadIdDraft, setThreadIdDraft] = useState<string>(initialThreadId);
  const [input, setInput] = useState<string>(DEFAULT_INPUT_MESSAGE);
  const [isComposing, setIsComposing] = useState(false);
  const [errorDrawerOpen, setErrorDrawerOpen] = useState(false);
  const [dismissedError, setDismissedError] = useState<string | null>(null);
  const [externalToolLineageDrafts, setExternalToolLineageDrafts] = useState<Record<string, string>>({});
  // HITL card states
  const [hitlDecisionSelections, setHitlDecisionSelections] = useState<Record<string, string>>({});
  const [hitlDecisionFreeInputs, setHitlDecisionFreeInputs] = useState<Record<string, string>>({});
  const [hitlPermissionScopes, setHitlPermissionScopes] = useState<Record<string, Record<string, string>>>({});
  const [hitlPermissionDeniedReasons, setHitlPermissionDeniedReasons] = useState<Record<string, string>>({});
  const [hitlProgressInstructions, setHitlProgressInstructions] = useState<Record<string, string>>({});

  useEffect(() => {
    persistSiderWidth(siderWidth);
  }, [siderWidth]);

  useEffect(() => {
    if (!isResizingSider) {
      return;
    }
    const body = document.body;
    const prevCursor = body.style.cursor;
    const prevUserSelect = body.style.userSelect;
    body.style.cursor = "col-resize";
    body.style.userSelect = "none";
    return () => {
      body.style.cursor = prevCursor;
      body.style.userSelect = prevUserSelect;
    };
  }, [isResizingSider]);

  const handleSiderResizeStart = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (typeof e.button === "number" && e.button !== 0) {
      return;
    }
    e.preventDefault();
    siderResizeRef.current = { startX: e.clientX, startWidth: siderWidth };
    setIsResizingSider(true);
    e.currentTarget.setPointerCapture(e.pointerId);
  };

  const handleSiderResizeMove = (e: ReactPointerEvent<HTMLDivElement>) => {
    const current = siderResizeRef.current;
    if (!current) {
      return;
    }
    const delta = e.clientX - current.startX;
    const viewportWidth = typeof window !== "undefined" ? window.innerWidth : MAX_SIDER_WIDTH;
    const maxWidth = Math.max(MIN_SIDER_WIDTH, Math.min(MAX_SIDER_WIDTH, viewportWidth - 320));
    const nextWidth = clampNumber(current.startWidth + delta, MIN_SIDER_WIDTH, maxWidth);
    setSiderWidth(nextWidth);
  };

  const handleSiderResizeEnd = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (!siderResizeRef.current) {
      return;
    }
    siderResizeRef.current = null;
    setIsResizingSider(false);
    try {
      e.currentTarget.releasePointerCapture(e.pointerId);
    } catch {}
  };

  const forwardedProps = useMemo(() => {
    const props: Record<string, unknown> = {};
    if (userId.trim()) {
      props.userid = userId.trim();
    }
    return props;
  }, [userId]);
  const endpoint = useMemo(() => buildHttpUrl(serverAddress, endpointPath), [endpointPath, serverAddress]);

  const chat = useAguiChat({
    endpoint,
    threadId,
    forwardedProps,
    tools: HITL_TOOL_DECLARATIONS,
  });

  useEffect(() => {
    const nextLineageId = (chat.graphInterrupt?.lineageId || "").trim();
    const prompt = (chat.graphInterrupt?.prompt || "").trim();
    if (!nextLineageId || !prompt) {
      return;
    }
    const toolCallId = prompt;
    const toolCallMessage = chat.messages.find((msg) => {
      return msg.kind === "tool-call"
        && msg.toolCall?.toolCallId === toolCallId
        && EXTERNAL_HITL_TOOL_NAMES.includes(msg.toolCall.toolCallName);
    });
    if (!toolCallMessage) {
      return;
    }
    setExternalToolLineageDrafts((prev) => {
      const existing = (prev[toolCallId] || "").trim();
      if (existing) {
        return prev;
      }
      return { ...prev, [toolCallId]: nextLineageId };
    });
  }, [chat.graphInterrupt, chat.messages]);

  const activeReport = useMemo(() => {
    if (!chat.activeReportId) {
      return null;
    }
    return chat.reportSessions.find((session) => session.documentId === chat.activeReportId) ?? null;
  }, [chat.activeReportId, chat.reportSessions]);

  useEffect(() => {
    const handler = (event: Event) => {
      const detail = (event as CustomEvent).detail as any;
      const documentId = typeof detail?.documentId === "string" ? detail.documentId : "";
      if (!documentId) {
        return;
      }
      chat.openReport(documentId);
    };
    window.addEventListener(AGUI_OPEN_REPORT_EVENT, handler as EventListener);
    return () => {
      window.removeEventListener(AGUI_OPEN_REPORT_EVENT, handler as EventListener);
    };
  }, [chat.openReport]);

  useEffect(() => {
    const handler = (event: Event) => {
      const detail = (event as CustomEvent).detail as any;
      const action = typeof detail?.action === "string" ? detail.action : "";
      if (action === "approve") {
        void chat.approveGraphInterrupt();
        return;
      }
      if (action === "dismiss") {
        void chat.dismissGraphInterrupt();
      }
    };
    window.addEventListener(AGUI_GRAPH_APPROVAL_EVENT, handler as EventListener);
    return () => {
      window.removeEventListener(AGUI_GRAPH_APPROVAL_EVENT, handler as EventListener);
    };
  }, [chat.approveGraphInterrupt, chat.dismissGraphInterrupt]);

  const send = async () => {
    if (chat.inProgress) {
      return;
    }
    const text = input.trim();
    if (!text) {
      return;
    }
    setInput("");
    await chat.send(text);
  };

  const chatRef = useRef<HTMLDivElement | null>(null);
  const [shouldAutoScroll, setShouldAutoScroll] = useState(true);
  const rawRef = useRef<HTMLDivElement | null>(null);
  const [rawShouldAutoScroll, setRawShouldAutoScroll] = useState(true);

  useEffect(() => {
    if (!rawShouldAutoScroll) {
      return;
    }
    const container = rawRef.current;
    if (!container) {
      return;
    }
    const id = window.requestAnimationFrame(() => {
      container.scrollTop = container.scrollHeight;
    });
    return () => window.cancelAnimationFrame(id);
  }, [chat.rawEvents, rawShouldAutoScroll]);

  const visibleMessages = useMemo(() => chat.messages.filter(isVisibleMainMessage), [chat.messages]);
  const suppressGraphApproval = useMemo(() => {
    const key = (chat.graphInterrupt?.key ?? "").trim();
    if (key === "external_tool") {
      return true;
    }
    const prompt = (chat.graphInterrupt?.prompt ?? "").trim();
    if (!prompt) {
      return false;
    }
    return /^call_[a-zA-Z0-9_-]+$/.test(prompt);
  }, [chat.graphInterrupt]);

  useEffect(() => {
    if (!shouldAutoScroll) {
      return;
    }
    const container = chatRef.current;
    if (!container) {
      return;
    }
    const id = window.requestAnimationFrame(() => {
      container.scrollTop = container.scrollHeight;
    });
    return () => window.cancelAnimationFrame(id);
  }, [visibleMessages, shouldAutoScroll]);

  const renderedItems = useMemo(() => groupChatItems(visibleMessages), [visibleMessages]);

  useEffect(() => {
    if (!chat.lastError) {
      setDismissedError(null);
      setErrorDrawerOpen(false);
    }
  }, [chat.lastError]);

  const errorSummary = useMemo(() => {
    const error = chat.lastError || "";
    if (!error) {
      return "";
    }
    const firstLine = error.split(/\r?\n/)[0] ?? error;
    const trimmed = firstLine.trim() || error.trim();
    if (!trimmed) {
      return "";
    }
    return trimmed.length > 140 ? `${trimmed.slice(0, 140)}…` : trimmed;
  }, [chat.lastError]);

  const showErrorBanner = Boolean(chat.lastError) && dismissedError !== chat.lastError;

  const applyConnection = async () => {
    if (chat.inProgress) {
      chat.stop();
    }
    chat.reset();
    setInput(DEFAULT_INPUT_MESSAGE);
    setExternalToolLineageDrafts({});

    const nextServerAddress = serverAddressDraft.trim() || "127.0.0.1:7878";
    const nextEndpointPath = endpointPathDraft.trim() || "/chat";
    const nextHistoryPath = historyPathDraft.trim() || "/history";
    const nextThreadId = threadIdDraft.trim() || createThreadId();

    setServerAddress(nextServerAddress);
    setServerAddressDraft(nextServerAddress);
    setEndpointPath(nextEndpointPath);
    setEndpointPathDraft(nextEndpointPath);
    setHistoryPathDraft(nextHistoryPath);
    setThreadId(nextThreadId);
    setThreadIdDraft(nextThreadId);

    const nextForwardedProps: Record<string, unknown> = {};
    if (userId.trim()) {
      nextForwardedProps.userid = userId.trim();
    }

    setHistoryHint("正在载入历史...");
    const result = await chat.loadHistory({
      endpoint: buildHttpUrl(nextServerAddress, nextHistoryPath),
      threadId: nextThreadId,
      forwardedProps: nextForwardedProps,
    });
    if (result.ok) {
      setHistoryHint(result.count > 0 ? "" : "暂无历史记录");
      return;
    }
    const message = result.message || "";
    if (message.includes("session not found")) {
      setHistoryHint("暂无历史记录");
      return;
    }
    setHistoryHint("");
  };

  const toolcallNames = useMemo(() => {
    const names = new Set<string>();
    for (const message of visibleMessages) {
      if (message.kind === "tool-call" && message.toolCall?.toolCallName) {
        names.add(message.toolCall.toolCallName);
      }
    }
    return Array.from(names).sort();
  }, [visibleMessages]);

  useEffect(() => {
    for (const name of toolcallNames) {
      ensureToolcallRegistered(name);
    }
  }, [toolcallNames]);

  return (
    <Layout className="app">
      <Layout.Content className="app__content">
        <div className="app__body" style={{ ["--sider-width" as any]: `${siderWidth}px` }}>
          <aside className="app__sider">
            <div className="app__sider-header">
              <Space size="small" align="center">
                <strong>AG-UI 事件</strong>
                <Tag theme="default" variant="outline">{chat.rawEvents.length}</Tag>
              </Space>
              <Space size="small">
                <Button size="small" variant="outline" onClick={chat.clearRawEvents}>
                  清空
                </Button>
              </Space>
            </div>
	            <div
	              className="app__sider-content"
	              ref={rawRef}
	              onScroll={() => {
                const el = rawRef.current;
                if (!el) {
                  return;
                }
	                const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
	                setRawShouldAutoScroll(distance < 80);
	              }}
	            >
	              {chat.rawEvents.length === 0 ? (
	                <div className="raw-events__empty">等待事件...</div>
	              ) : (
	                <div className="raw-events">
	                  {chat.rawEvents.map((event) => (
                    <details
                      key={event.id}
                      className={event.kind === "request" ? "raw-event raw-event--request" : "raw-event"}
                    >
                      <summary className="raw-event__summary">
                        <span className="raw-event__time">{formatTimestamp(event.timestamp)}</span>
                        <Tag theme={event.kind === "request" ? "primary" : "default"} variant="outline">{event.type}</Tag>
                        <span className="raw-event__extra">{summarizeRawEvent(event)}</span>
                      </summary>
                      <pre className="raw-event__body">
                        {JSON.stringify(event.payload, null, 2)}
                      </pre>
                    </details>
	                  ))}
	                </div>
                  )}
            </div>
            <div
              className={isResizingSider ? "app__sider-resizer app__sider-resizer--active" : "app__sider-resizer"}
              role="separator"
              aria-orientation="vertical"
              aria-label="Resize sidebar"
              title="拖拽调整侧边栏宽度"
              onPointerDown={handleSiderResizeStart}
              onPointerMove={handleSiderResizeMove}
              onPointerUp={handleSiderResizeEnd}
              onPointerCancel={handleSiderResizeEnd}
            />
          </aside>

          <div className="app__chat-area">
	            <div className="app__topbar">
	              <div className="status-row">
                <div className="status-row__left">
                  <Space size="small" align="center">
                    <ChatIcon />
                    <strong>AG-UI TDesign Chat</strong>
                  </Space>
                  {chat.graphNodeId ? <Tag theme="warning" variant="outline">node: {chat.graphNodeId}</Tag> : null}
                  {chat.finishReason && chat.finishReason !== "stop"
                    ? <Tag theme="success" variant="outline">finish: {chat.finishReason}</Tag>
                    : null}
                </div>

                <div className="status-row__center">
                  <Input
                    label="IP:Port"
                    value={serverAddressDraft}
                    onChange={(v) => {
                      const next = String(v);
                      setServerAddressDraft(next);
                      setServerAddress(next);
                    }}
                    className="header-field"
                    style={{ width: "100%" }}
                    placeholder="127.0.0.1:8080"
                  />

                  <Input
                    label="实时对话"
                    value={endpointPathDraft}
                    onChange={(v) => {
                      const next = String(v);
                      setEndpointPathDraft(next);
                      setEndpointPath(next);
                    }}
                    className="header-field"
                    style={{ width: "100%" }}
                    placeholder="/chat 或 完整URL"
                  />

                  <Input
                    label="消息快照"
                    value={historyPathDraft}
                    onChange={(v) => setHistoryPathDraft(String(v))}
                    className="header-field"
                    style={{ width: "100%" }}
                    placeholder="/history"
                  />

                  <Input
                    label="Thread"
                    value={threadIdDraft}
                    onChange={(v) => {
                      const next = String(v);
                      setThreadIdDraft(next);
                      if (next.trim()) {
                        setThreadId(next);
                      }
                    }}
                    className="header-field"
                    style={{ width: "100%" }}
                    placeholder="留空自动生成"
                  />

                  <Input
                    label="User"
                    value={userId}
                    onChange={(v) => setUserId(String(v))}
                    className="header-field"
                    style={{ width: "100%" }}
                    placeholder="demo-user"
                  />
                </div>

                <div className="status-row__right">
                  {chat.progress ? (
                    <Progress
                      theme="line"
                      percentage={Math.max(0, Math.min(100, Math.round(chat.progress.percent)))}
                      label={chat.progress.label ? chat.progress.label : undefined}
                      style={{ width: 200 }}
                    />
                  ) : null}

                  {historyHint ? (
                    <span className="status-row__hint" title={historyHint}>
                      <Tag theme="default" variant="outline">{historyHint}</Tag>
                    </span>
                  ) : null}

                  <Button variant="outline" onClick={applyConnection} disabled={chat.inProgress}>
                    载入历史
                  </Button>

                  <Button
                    variant="outline"
                    icon={<RefreshIcon />}
                    onClick={() => {
                      chat.reset();
                      setExternalToolLineageDrafts({});
                      const nextThreadId = createThreadId();
                      setThreadId(nextThreadId);
                      setThreadIdDraft(nextThreadId);
                      setHistoryHint("");
                      setInput(DEFAULT_INPUT_MESSAGE);
                    }}
                  >
                    新会话
                  </Button>
                </div>
              </div>
            </div>

            <div
              className="chat"
              ref={chatRef}
              onScroll={() => {
                const el = chatRef.current;
                if (!el) {
                  return;
                }
                const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
                setShouldAutoScroll(distance < 80);
              }}
            >
              {visibleMessages.length === 0 ? (
                <div className="chat__empty">
                  <div>输入内容后发送。</div>
                </div>
              ) : (
                <div className="chat__list">
                  {renderedItems.map((item) => {
                    if (item.kind === "user") {
                      return (
                        <ChatMessage
                          key={item.key}
                          role="user"
                          name={`You · ${formatTimestamp(item.message.timestamp)}`}
                          placement="right"
                          content={[{ type: "text", data: item.message.content || "" }] as any}
                          variant="base"
                        />
                      );
                    }

                    const blocks: any[] = [];
                    const toolSlots: JSX.Element[] = [];

                    // 构建 block 树形结构映射
                    const blockIds = new Set<string>();
                    const childBlocksMap = new Map<string, UiMessage[]>();
                    for (const msg of item.messages) {
                      if (msg.kind === "step" || msg.kind === "block") {
                        blockIds.add(msg.id);
                      }
                    }
                    for (const msg of item.messages) {
                      if ((msg.kind === "step" || msg.kind === "block") && msg.stepId && blockIds.has(msg.stepId) && msg.stepId !== msg.id) {
                        const siblings = childBlocksMap.get(msg.stepId) ?? [];
                        siblings.push(msg);
                        childBlocksMap.set(msg.stepId, siblings);
                      }
                    }

                    // 递归渲染 block 元素（支持多层嵌套）
                    const renderBlockElement = (blockMsg: UiMessage, depth: number, slotAttr?: string): JSX.Element => {
                      const hasChildren = Array.isArray(blockMsg.children) && blockMsg.children.length > 0;
                      const statusLabel = (blockMsg.stepStatus ?? "pending") as StepStatus;
                      const stepKey = `block-${blockMsg.id}-${depth}`;
                      const statusIcon = stepStatusGlyph(statusLabel);
                      const statusText = stepStatusText(statusLabel);
                      const isRunning = statusLabel === "running" || statusLabel === "in_progress";
                      const isCompleted = statusLabel === "completed";
                      const isFailed = statusLabel === "failed";
                      const isAgent = blockMsg.blockType === "agent";
                      const defaultOpen = hasChildren || isRunning;
                      const childBlocks = childBlocksMap.get(blockMsg.id) ?? [];
                      const hasContent = hasChildren || childBlocks.length > 0;

                      return (
                        <details
                          key={stepKey}
                          {...(slotAttr ? { slot: slotAttr } : {})}
                          className={`block-panel ${depth > 0 ? "block-panel--nested" : ""} ${isAgent ? "block-panel--agent" : ""}`}
                          data-status={statusLabel}
                          open={defaultOpen}
                        >
                          <summary className="block-panel__summary">
                            <span className="block-panel__chevron" aria-hidden="true">▸</span>
                            <span
                              className={`block-panel__status-icon ${isRunning ? "block-panel__status-icon--spin" : ""}`}
                              aria-hidden="true"
                              data-status={statusLabel}
                            >
                              {statusIcon}
                            </span>
                            <span className="block-panel__title-block">
                              <span className="block-panel__title" title={blockMsg.stepTitle ?? blockMsg.title ?? blockMsg.id}>
                                {isAgent ? `🤖 ${blockMsg.stepTitle ?? blockMsg.title ?? blockMsg.id}` : (blockMsg.stepTitle ?? blockMsg.title ?? blockMsg.id)}
                              </span>
                              <span className="block-panel__meta">
                                {hasChildren ? `${blockMsg.children!.length} 项` : null}
                                {blockMsg.startedAt ? (
                                  <span className="block-panel__time">
                                    {formatTimestamp(blockMsg.startedAt)}
                                    {blockMsg.completedAt ? ` → ${formatTimestamp(blockMsg.completedAt)}` : ""}
                                  </span>
                                ) : null}
                              </span>
                            </span>
                            <span
                              className={`block-panel__status-tag block-panel__status-tag--${
                                isCompleted ? "success" : isRunning ? "running" : isFailed ? "failed" : "pending"
                              }`}
                            >
                              {statusText}
                            </span>
                          </summary>
                          <div className="block-panel__body">
                            {hasContent ? (
                              <div className="block-panel__children">
                                {hasChildren ? blockMsg.children!.map((child, idx) => {
                                  if (child.kind === "thinking") {
                                    return (
                                      <details key={`${stepKey}-th-${idx}`} className="step-item step-item--thinking" data-kind="thinking" open={true}>
                                        <summary className="step-item__header">
                                          <span className="step-item__chevron" aria-hidden="true">▸</span>
                                          <span className="step-item__icon step-item__icon--thinking" aria-hidden="true">💭</span>
                                          <span className="step-item__label">思考</span>
                                          <span className="step-item__title" title={child.title ?? "深度思考"}>
                                            {child.title ?? "深度思考"}
                                          </span>
                                        </summary>
                                        <div className="step-item__body">
                                          <ChatMarkdown content={child.content || ""} />
                                        </div>
                                      </details>
                                    );
                                  }
                                  if (child.kind === "tool-call" && child.toolCall) {
                                    const tCall: ToolCall = {
                                      toolCallId: child.toolCall.toolCallId,
                                      toolCallName: child.toolCall.toolCallName,
                                      parentMessageId: child.toolCall.parentMessageId,
                                      args: child.toolCall.args,
                                      result: child.toolCall.result,
                                    };
                                    const argsText = typeof tCall.args === "string" ? tCall.args.trim() : "";
                                    const hasResult = typeof tCall.result === "string" && tCall.result.trim().length > 0;
                                    const icon = pickToolIcon(tCall.toolCallName);
                                    const displayArgs = argsText && tCall.toolCallName && argsText.startsWith(tCall.toolCallName)
                                      ? argsText.slice(tCall.toolCallName.length).replace(/^[\s:：]+/, "")
                                      : argsText;
                                    return (
                                      <div key={`${stepKey}-tc-${idx}`} className={`step-item-tool ${hasResult ? "step-item-tool--done" : ""}`}>
                                        <span className="step-item-tool__icon" aria-hidden="true">{icon}</span>
                                        <span className="step-item-tool__name">{tCall.toolCallName}</span>
                                        <span className="step-item-tool__args" title={argsText || tCall.toolCallName}>
                                          {displayArgs || tCall.toolCallName}
                                        </span>
                                        {hasResult ? <span className="step-item-tool__dot" /> : null}
                                      </div>
                                    );
                                  }
                                  if (child.kind === "text" && child.role === "assistant") {
                                    const textContent = (child.content || "").trim();
                                    if (!textContent) return null;
                                    return (
                                      <div key={`${stepKey}-tx-${idx}`} className="step-item step-item--text" data-kind="text">
                                        <ChatMarkdown content={child.content || ""} />
                                      </div>
                                    );
                                  }
                                  return null;
                                }) : null}
                                {childBlocks.map(child => renderBlockElement(child, depth + 1))}
                              </div>
                            ) : (
                              <div className="step-panel__empty">
                                {isRunning ? "步骤正在执行中…" : isCompleted ? "已完成" : isFailed ? "失败" : "等待执行"}
                              </div>
                            )}
                          </div>
                        </details>
                      );
                    };

                    const renderMessageIntoBlocks = (message: UiMessage) => {
                      if (message.kind === "thinking") {
                        blocks.push({
                          type: "thinking",
                          data: { title: message.title ?? "Thinking", text: message.content || "" },
                          status: message.status,
                        });
                        return;
                      }

                      if (message.kind === "tool-call" && message.toolCall) {
                        if (suppressGraphApproval && message.toolCall.toolCallName === GRAPH_APPROVAL_TOOL_NAME) {
                          return;
                        }
                        const toolCall: ToolCall = {
                          toolCallId: message.toolCall.toolCallId,
                          toolCallName: message.toolCall.toolCallName,
                          parentMessageId: message.toolCall.parentMessageId,
                          args: message.toolCall.args,
                          result: message.toolCall.result,
                        };
                        const isExternalTool = EXTERNAL_HITL_TOOL_NAMES.includes(toolCall.toolCallName);
                        const hitlType = getHITLType(toolCall.toolCallName);
                        const toolLineageDraft = externalToolLineageDrafts[toolCall.toolCallId];
                        const toolLineageId = (typeof toolLineageDraft === "string" ? toolLineageDraft : "").trim();
                        const lineageReady = Boolean(toolLineageId);
                        const canResume = isExternalTool
                          && !toolCall.result
                          && !chat.inProgress
                          && lineageReady;
                        let parsedArgs: Record<string, any> = {};
                        try {
                          parsedArgs = JSON.parse((toolCall.args || "{}").trim() || "{}");
                        } catch {
                          // Fallback: parse key=value format (e.g. "progress=60, title=xxx, options=[...]")
                          const raw = (toolCall.args || "").trim();
                          if (raw) {
                            try {
                              // Try wrapping in braces to make it valid JSON-like
                              const wrapped = "{" + raw
                                .replace(/(\w+)=/g, '"$1":')
                                .replace(/，/g, ",") + "}";
                              parsedArgs = JSON.parse(wrapped);
                            } catch {
                              // Last resort: extract key=value pairs manually
                              const pairs = raw.replace(/，/g, ",").split(/,(?=\s*\w+=)/);
                              for (const pair of pairs) {
                                const eqIdx = pair.indexOf("=");
                                if (eqIdx > 0) {
                                  const key = pair.slice(0, eqIdx).trim();
                                  let val: any = pair.slice(eqIdx + 1).trim();
                                  // Try to parse value as JSON
                                  try { val = JSON.parse(val); } catch {}
                                  parsedArgs[key] = val;
                                }
                              }
                            }
                          }
                        }
                        const index = blocks.length;
                        blocks.push({ type: "toolcall", data: toolCall });
                        toolSlots.push(
                          <div key={toolCall.toolCallId} slot={`toolcall-${index}`} className="toolcall__slot">
                            {isExternalTool && hitlType ? (
                              <>
                                <ToolCallRenderer toolCall={toolCall} />
                                {!toolCall.result ? (
                              <div className="hitl-card">
                                <div className="hitl-card__header">
                                  <Space size="small" align="center" breakLine>
                                    <Tag theme="primary" variant="outline">HITL</Tag>
                                    <Tag theme="default" variant="outline">{hitlType}</Tag>
                                    <Tag theme="default" variant="outline">request_id: {parsedArgs.request_id || "N/A"}</Tag>
                                  </Space>
                                </div>

                                {!lineageReady ? (
                                  <Alert
                                    className="hitl-card__alert"
                                    theme="warning"
                                    title="HITL 工具需要 lineage_id"
                                    message="等待服务端在 graph.node.interrupt 事件中返回 lineageId；如未返回可在下方高级配置手动填写。"
                                  />
                                ) : null}

                                {/* Notification Card */}
                                {hitlType === "notification" && (
                                  <div className="hitl-card__body">
                                    <div className="hitl-card__title">
                                      {parsedArgs.severity === "critical" ? <Tag theme="danger" variant="light">严重</Tag> : null}
                                      {parsedArgs.severity === "warning" ? <Tag theme="warning" variant="light">警告</Tag> : null}
                                      {parsedArgs.severity === "info" ? <Tag theme="primary" variant="light">信息</Tag> : null}
                                      <span style={{ marginLeft: 6 }}>{parsedArgs.title || "通知"}</span>
                                    </div>
                                    {parsedArgs.detail ? (
                                      <div className="hitl-card__detail">{parsedArgs.detail}</div>
                                    ) : null}
                                    {parsedArgs.affected_resources?.length > 0 ? (
                                      <div className="hitl-card__resources">
                                        <span className="hitl-card__label">受影响资源：</span>
                                        <Space size="small">
                                          {parsedArgs.affected_resources.map((r: string, i: number) => (
                                            <Tag key={i} size="small" variant="outline">{r}</Tag>
                                          ))}
                                        </Space>
                                      </div>
                                    ) : null}
                                    <div className="hitl-card__actions">
                                      <Button
                                        size="small"
                                        theme="primary"
                                        disabled={!canResume}
                                        onClick={() => {
                                          const result = JSON.stringify({
                                            request_id: parsedArgs.request_id || "unknown",
                                            acknowledged: true,
                                            acknowledged_by: "current_user",
                                            acknowledged_at: new Date().toISOString(),
                                          });
                                          void chat.sendToolResult({
                                            toolCallId: toolCall.toolCallId,
                                            toolCallName: toolCall.toolCallName,
                                            content: result,
                                            messageId: `tool-result-${toolCall.toolCallId}`,
                                            forwardedProps: { lineage_id: toolLineageId },
                                          });
                                        }}
                                      >
                                        已阅
                                      </Button>
                                    </div>
                                  </div>
                                )}

                                {/* Decision Card */}
                                {hitlType === "decision" && (
                                  <div className="hitl-card__body">
                                    <div className="hitl-card__title">{parsedArgs.title || "请做出决策"}</div>
                                    {parsedArgs.description ? (
                                      <div className="hitl-card__detail">{parsedArgs.description}</div>
                                    ) : null}
                                    <div className="hitl-card__options">
                                      {(parsedArgs.options || []).map((opt: Record<string, any>) => {
                                        const selected = hitlDecisionSelections[toolCall.toolCallId] === opt.id;
                                        const btnTheme = opt.style === "danger" ? "danger" : opt.style === "primary" ? "primary" : "default";
                                        return (
                                          <Button
                                            key={opt.id}
                                            size="small"
                                            variant={selected ? "base" : "outline"}
                                            theme={selected ? btnTheme : "default"}
                                            disabled={!canResume}
                                            onClick={() => {
                                              setHitlDecisionSelections((prev) => ({ ...prev, [toolCall.toolCallId]: opt.id }));
                                              setHitlDecisionFreeInputs((prev) => ({ ...prev, [toolCall.toolCallId]: "" }));
                                            }}
                                          >
                                            {opt.label}
                                          </Button>
                                        );
                                      })}
                                    </div>
                                    {parsedArgs.allow_free_input ? (
                                      <div className="hitl-card__free-input">
                                        <div className="hitl-card__label">自定义输入（选择此项将覆盖上方选项）</div>
                                        <Textarea
                                          value={hitlDecisionFreeInputs[toolCall.toolCallId] || ""}
                                          onChange={(v) => {
                                            const next = String(v);
                                            setHitlDecisionFreeInputs((prev) => ({ ...prev, [toolCall.toolCallId]: next }));
                                            if (next.trim()) {
                                              setHitlDecisionSelections((prev) => ({ ...prev, [toolCall.toolCallId]: "" }));
                                            }
                                          }}
                                          placeholder="输入自定义决策..."
                                          autosize={{ minRows: 2, maxRows: 4 }}
                                          disabled={chat.inProgress}
                                        />
                                      </div>
                                    ) : null}
                                    <div className="hitl-card__actions">
                                      <Button
                                        size="small"
                                        theme="primary"
                                        disabled={!canResume || (!hitlDecisionSelections[toolCall.toolCallId] && !(hitlDecisionFreeInputs[toolCall.toolCallId] || "").trim())}
                                        onClick={() => {
                                          const freeInput = (hitlDecisionFreeInputs[toolCall.toolCallId] || "").trim();
                                          const result = JSON.stringify({
                                            request_id: parsedArgs.request_id || "unknown",
                                            selected_option_id: freeInput ? null : (hitlDecisionSelections[toolCall.toolCallId] || null),
                                            free_input: freeInput || null,
                                            decided_by: "current_user",
                                            decided_at: new Date().toISOString(),
                                          });
                                          void chat.sendToolResult({
                                            toolCallId: toolCall.toolCallId,
                                            toolCallName: toolCall.toolCallName,
                                            content: result,
                                            messageId: `tool-result-${toolCall.toolCallId}`,
                                            forwardedProps: { lineage_id: toolLineageId },
                                          });
                                        }}
                                      >
                                        确认决策
                                      </Button>
                                    </div>
                                  </div>
                                )}

                                {/* Permission Card */}
                                {hitlType === "permission" && (
                                  <div className="hitl-card__body">
                                    <div className="hitl-card__title">
                                      {parsedArgs.risk_level === "critical" ? <Tag theme="danger" variant="light">极高风险</Tag> : null}
                                      {parsedArgs.risk_level === "high" ? <Tag theme="danger" variant="light">高风险</Tag> : null}
                                      {parsedArgs.risk_level === "medium" ? <Tag theme="warning" variant="light">中风险</Tag> : null}
                                      {parsedArgs.risk_level === "low" ? <Tag theme="success" variant="light">低风险</Tag> : null}
                                      <span style={{ marginLeft: 6 }}>{parsedArgs.title || "权限请求"}</span>
                                    </div>
                                    <div className="hitl-card__detail">
                                      <div><strong>操作：</strong>{parsedArgs.action || "N/A"}</div>
                                      <div><strong>目标资源：</strong>{parsedArgs.resource || "N/A"}</div>
                                      {parsedArgs.justification ? (
                                        <div><strong>理由：</strong>{parsedArgs.justification}</div>
                                      ) : null}
                                      {parsedArgs.required_roles?.length > 0 ? (
                                        <div>
                                          <strong>需要角色：</strong>
                                          <Space size="small">
                                            {parsedArgs.required_roles.map((r: string, i: number) => (
                                              <Tag key={i} size="small" variant="outline">{r}</Tag>
                                            ))}
                                          </Space>
                                        </div>
                                      ) : null}
                                    </div>
                                    {parsedArgs.scope_constraints?.length > 0 ? (
                                      <div className="hitl-card__scopes">
                                        <div className="hitl-card__label">范围约束</div>
                                        {parsedArgs.scope_constraints.map((c: Record<string, any>) => (
                                          <div key={c.key} className="hitl-card__scope-item">
                                            <span className="hitl-card__scope-label">{c.label}：</span>
                                            <Space size="small">
                                              {(c.options || []).map((opt: string) => {
                                                const currentScopes = hitlPermissionScopes[toolCall.toolCallId] || {};
                                                const selected = currentScopes[c.key] === opt;
                                                return (
                                                  <Tag
                                                    key={opt}
                                                    size="small"
                                                    variant={selected ? "dark" : "outline"}
                                                    theme={selected ? "primary" : "default"}
                                                    style={{ cursor: "pointer" }}
                                                    onClick={() => {
                                                      setHitlPermissionScopes((prev) => ({
                                                        ...prev,
                                                        [toolCall.toolCallId]: {
                                                          ...(prev[toolCall.toolCallId] || {}),
                                                          [c.key]: opt,
                                                        },
                                                      }));
                                                    }}
                                                  >
                                                    {opt}
                                                  </Tag>
                                                );
                                              })}
                                            </Space>
                                          </div>
                                        ))}
                                      </div>
                                    ) : null}
                                    <div className="hitl-card__actions">
                                      <Space size="small">
                                        <Button
                                          size="small"
                                          theme="primary"
                                          disabled={!canResume}
                                          onClick={() => {
                                            const result = JSON.stringify({
                                              request_id: parsedArgs.request_id || "unknown",
                                              granted: true,
                                              scope: hitlPermissionScopes[toolCall.toolCallId] || {},
                                              denied_reason: null,
                                              granted_by: "current_user",
                                              granted_at: new Date().toISOString(),
                                              expires_at: parsedArgs.expires_in_seconds
                                                ? new Date(Date.now() + parsedArgs.expires_in_seconds * 1000).toISOString()
                                                : null,
                                            });
                                            void chat.sendToolResult({
                                              toolCallId: toolCall.toolCallId,
                                              toolCallName: toolCall.toolCallName,
                                              content: result,
                                              messageId: `tool-result-${toolCall.toolCallId}`,
                                              forwardedProps: { lineage_id: toolLineageId },
                                            });
                                          }}
                                        >
                                          批准
                                        </Button>
                                        <Button
                                          size="small"
                                          theme="danger"
                                          variant="outline"
                                          disabled={!canResume}
                                          onClick={() => {
                                            const reason = (hitlPermissionDeniedReasons[toolCall.toolCallId] || "").trim();
                                            const result = JSON.stringify({
                                              request_id: parsedArgs.request_id || "unknown",
                                              granted: false,
                                              scope: {},
                                              denied_reason: reason || "用户拒绝",
                                              granted_by: "current_user",
                                              granted_at: new Date().toISOString(),
                                              expires_at: null,
                                            });
                                            void chat.sendToolResult({
                                              toolCallId: toolCall.toolCallId,
                                              toolCallName: toolCall.toolCallName,
                                              content: result,
                                              messageId: `tool-result-${toolCall.toolCallId}`,
                                              forwardedProps: { lineage_id: toolLineageId },
                                            });
                                          }}
                                        >
                                          拒绝
                                        </Button>
                                      </Space>
                                      <div className="hitl-card__deny-reason" style={{ marginTop: 8 }}>
                                        <Input
                                          value={hitlPermissionDeniedReasons[toolCall.toolCallId] || ""}
                                          onChange={(v) => {
                                            setHitlPermissionDeniedReasons((prev) => ({
                                              ...prev,
                                              [toolCall.toolCallId]: String(v),
                                            }));
                                          }}
                                          placeholder="拒绝理由（可选）"
                                          disabled={chat.inProgress}
                                        />
                                      </div>
                                    </div>
                                  </div>
                                )}

                                {/* Progress Card */}
                                {hitlType === "progress" && (
                                  <div className="hitl-card__body">
                                    <div className="hitl-card__title">{parsedArgs.title || "任务进度"}</div>
                                    <div className="hitl-card__progress">
                                      <Progress
                                        percentage={parsedArgs.progress ?? 0}
                                        theme="line"
                                        color={
                                          parsedArgs.status === "blocked" ? "#e34d59"
                                          : parsedArgs.status === "waiting" ? "#ed7b2f"
                                          : parsedArgs.status === "completed" ? "#00a870"
                                          : "#0052d9"
                                        }
                                        status={
                                          parsedArgs.status === "completed" ? "success"
                                          : parsedArgs.status === "blocked" ? "error"
                                          : "active"
                                        }
                                      />
                                    </div>
                                    <div className="hitl-card__status">
                                      <Tag
                                        variant="light"
                                        theme={
                                          parsedArgs.status === "running" ? "primary"
                                          : parsedArgs.status === "waiting" ? "warning"
                                          : parsedArgs.status === "blocked" ? "danger"
                                          : "success"
                                        }
                                      >
                                        {parsedArgs.status === "running" ? "运行中"
                                         : parsedArgs.status === "waiting" ? "等待中"
                                         : parsedArgs.status === "blocked" ? "已阻塞"
                                         : "已完成"}
                                      </Tag>
                                      {parsedArgs.estimated_remaining_seconds != null ? (
                                        <span className="hitl-card__eta">
                                          预计剩余 {Math.ceil(parsedArgs.estimated_remaining_seconds / 60)} 分钟
                                        </span>
                                      ) : null}
                                    </div>
                                    {parsedArgs.detail ? (
                                      <div className="hitl-card__detail">{parsedArgs.detail}</div>
                                    ) : null}
                                    {parsedArgs.next_step ? (
                                      <div className="hitl-card__next-step">
                                        <span className="hitl-card__label">下一步：</span>{parsedArgs.next_step}
                                      </div>
                                    ) : null}
                                    {parsedArgs.issues?.length > 0 ? (
                                      <div className="hitl-card__issues">
                                        {parsedArgs.issues.map((issue: Record<string, any>, i: number) => (
                                          <Alert
                                            key={i}
                                            theme={issue.level === "error" ? "error" : "warning"}
                                            message={issue.message}
                                            style={{ marginTop: 4 }}
                                          />
                                        ))}
                                      </div>
                                    ) : null}
                                    <div className="hitl-card__instruction">
                                      <div className="hitl-card__label">附加指令（可选）</div>
                                      <Textarea
                                        value={hitlProgressInstructions[toolCall.toolCallId] || ""}
                                        onChange={(v) => {
                                          setHitlProgressInstructions((prev) => ({
                                            ...prev,
                                            [toolCall.toolCallId]: String(v),
                                          }));
                                        }}
                                        placeholder="输入附加指令..."
                                        autosize={{ minRows: 1, maxRows: 3 }}
                                        disabled={chat.inProgress}
                                      />
                                    </div>
                                    <div className="hitl-card__actions">
                                      <Space size="small">
                                        {(() => {
                                          const actions = parsedArgs.allow_actions || ["continue"];
                                          return actions.map((action: string) => {
                                            const btnTheme = action === "abort" ? "danger"
                                              : action === "pause" ? "warning"
                                              : "primary";
                                            const btnLabel = action === "continue" ? "继续"
                                              : action === "pause" ? "暂停"
                                              : "终止";
                                            return (
                                              <Button
                                                key={action}
                                                size="small"
                                                theme={btnTheme}
                                                variant={action === "abort" ? "outline" : "base"}
                                                disabled={!canResume}
                                                onClick={() => {
                                                  const instruction = (hitlProgressInstructions[toolCall.toolCallId] || "").trim();
                                                  const result = JSON.stringify({
                                                    request_id: parsedArgs.request_id || "unknown",
                                                    action,
                                                    instruction: instruction || null,
                                                    responded_by: "current_user",
                                                    responded_at: new Date().toISOString(),
                                                  });
                                                  void chat.sendToolResult({
                                                    toolCallId: toolCall.toolCallId,
                                                    toolCallName: toolCall.toolCallName,
                                                    content: result,
                                                    messageId: `tool-result-${toolCall.toolCallId}`,
                                                    forwardedProps: { lineage_id: toolLineageId },
                                                  });
                                                }}
                                              >
                                                {btnLabel}
                                              </Button>
                                            );
                                          });
                                        })()}
                                      </Space>
                                    </div>
                                  </div>
                                )}

                                <details className="hitl-card__advanced">
                                  <summary className="hitl-card__advanced-summary">高级配置</summary>
                                  <div className="hitl-card__advanced-body">
                                    <Input
                                      label="lineage_id"
                                      value={typeof toolLineageDraft === "string" ? toolLineageDraft : ""}
                                      onChange={(v) => {
                                        const next = String(v);
                                        setExternalToolLineageDrafts((prev) => ({ ...prev, [toolCall.toolCallId]: next }));
                                      }}
                                      className="header-field"
                                      style={{ width: "100%" }}
                                      placeholder="会写入 forwardedProps.lineage_id"
                                      disabled={chat.inProgress}
                                    />
                                  </div>
                                </details>
                              </div>
                            ) : null}
                            </>
                          ) : (
                            <div className="toolcall__compact">
                              <span className="toolcall__compact-icon">{pickToolIcon(toolCall.toolCallName)}</span>
                              <span className="toolcall__compact-name">{toolCall.toolCallName}</span>
                              <span className="toolcall__compact-args">{
                                (() => {
                                  const raw = (toolCall.args || toolCall.toolCallName).trim();
                                  // 去除 args 中与工具名重复的前缀
                                  if (raw && toolCall.toolCallName && raw.startsWith(toolCall.toolCallName)) {
                                    const stripped = raw.slice(toolCall.toolCallName.length).replace(/^[\s:：]+/, "");
                                    return stripped || raw;
                                  }
                                  return raw;
                                })()
                              }</span>
                              {toolCall.result ? <span className="toolcall__compact-dot" /> : null}
                            </div>
                          )}
                          </div>,
                        );
                        return;
                      }

                      if (message.kind === "step" || message.kind === "block") {
                        // 如果有 parentId 且父 block 存在且不是自己（避免 parentId === id 时跳过自身），跳过（由父 block 递归渲染）
                        if (message.stepId && blockIds.has(message.stepId) && message.stepId !== message.id) {
                          return;
                        }
                        const statusLabel = (message.stepStatus ?? "pending") as StepStatus;
                        const statusText = stepStatusText(statusLabel);
                        blocks.push({
                          type: "custom",
                          data: {
                            title: message.stepTitle ?? message.title ?? message.id,
                            text: statusText,
                            collapsed: false,
                          },
                        });
                        toolSlots.push(renderBlockElement(message, 0, `custom-${blocks.length - 1}`));
                        return;
                      }

                      if (message.kind === "text" && message.role === "assistant") {
                        // 合并相邻的 markdown 条目，减少 content 数组长度
                        const lastBlock = blocks[blocks.length - 1];
                        if (lastBlock && lastBlock.type === "markdown") {
                          lastBlock.data += (message.content || "");
                        } else {
                          blocks.push({ type: "markdown", data: message.content || "" });
                        }
                      }
                    };

                    // 三阶段排序：前文本 → 步骤(含中间文本) → 后文本
                    // 1) 找到 step/block 消息在时间线中的范围
                    // 2) 位置在此范围之前的 text/thinking → "前文本"（渲染在步骤上方）
                    // 3) 位置在此范围之后的 text/thinking → "后文本"（渲染在步骤下方）
                    // 4) 所有 step/block/tool-call + 区间内的 text/thinking → "步骤组"（按时间线顺序渲染）
                    // 这样 slot 名称基于 content 数组位置索引正确匹配
                    let firstStepIdx = -1;
                    let lastStepIdx = -1;
                    for (let i = 0; i < item.messages.length; i++) {
                      const m = item.messages[i];
                      if (m.kind === "step" || m.kind === "block") {
                        if (firstStepIdx === -1) firstStepIdx = i;
                        lastStepIdx = i;
                      }
                    }
                    const beforeText: UiMessage[] = [];
                    const stepGroup: UiMessage[] = [];
                    const afterText: UiMessage[] = [];
                    for (let i = 0; i < item.messages.length; i++) {
                      const m = item.messages[i];
                      if (firstStepIdx >= 0 && i < firstStepIdx && (m.kind === "text" || m.kind === "thinking")) {
                        beforeText.push(m);
                      } else if (lastStepIdx >= 0 && i > lastStepIdx && (m.kind === "text" || m.kind === "thinking")) {
                        afterText.push(m);
                      } else {
                        stepGroup.push(m);
                      }
                    }
                    for (const message of beforeText) {
                      renderMessageIntoBlocks(message);
                    }
                    for (const message of stepGroup) {
                      renderMessageIntoBlocks(message);
                    }
                    for (const message of afterText) {
                      renderMessageIntoBlocks(message);
                    }

                    return (
                      <ChatMessage
                        key={item.key}
                        role="assistant"
                        name={`Assistant · ${formatTimestamp(item.messages[0]?.timestamp)}`}
                        placement="left"
                        content={blocks as any}
                        variant="base"
                        chatContentProps={{
                          thinking: {
                            maxHeight: 320,
                            layout: "border",
                            collapsed: false,
                          },
                        }}
                      >
                        {toolSlots}
                      </ChatMessage>
                    );
                  })}
                </div>
              )}
            </div>

            <div className="composer">
              <Space direction="vertical" style={{ width: "100%" }} size="small">
                {showErrorBanner ? (
                  <Alert
                    theme="error"
                    title="运行失败"
                    message={errorSummary || "发生错误，点击“详情”查看。"}
                    closeBtn
                    onClose={() => {
                      setDismissedError(chat.lastError);
                      setErrorDrawerOpen(false);
                    }}
                    operation={(
                      <Button
                        size="small"
                        variant="outline"
                        onClick={() => setErrorDrawerOpen(true)}
                      >
                        详情
                      </Button>
                    )}
                  />
                ) : null}
                <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
                  <div style={{ flex: 1 }}>
                    <Textarea
                      value={input}
                      onChange={(v) => setInput(String(v))}
                      placeholder={chat.inProgress ? "等待停止后继续输入..." : "输入消息..."}
                      autosize={{ minRows: 2, maxRows: 6 }}
                      disabled={chat.inProgress}
                      onCompositionStart={() => setIsComposing(true)}
                      onCompositionEnd={() => setIsComposing(false)}
                      onKeydown={(_, ctx) => {
                        const e = ctx.e;
                        if (e.key !== "Enter") {
                          return;
                        }
                        if (e.shiftKey) {
                          return;
                        }
                        if (isComposing) {
                          return;
                        }
                        e.preventDefault();
                        void send();
                      }}
                    />
                  </div>
                  {chat.inProgress ? (
                    <Button
                      theme="danger"
                      variant="outline"
                      icon={<StopCircleIcon />}
                      onClick={() => void chat.cancel()}
                      className="composer__stop-btn"
                    >
                      停止
                    </Button>
                  ) : (
                    <Button theme="primary" onClick={send} disabled={!input.trim()}>
                      发送
                    </Button>
                  )}
                </div>
              </Space>
            </div>
          </div>
        </div>
      </Layout.Content>

      <Drawer
        header={activeReport ? `Report · ${activeReport.title}` : "Report"}
        visible={chat.reportDrawerOpen}
        onClose={() => chat.setReportOpen(false)}
        closeBtn={false}
        footer={(
          <div className="report-drawer__footer">
            <Button theme="primary" onClick={() => chat.setReportOpen(false)}>
              关闭
            </Button>
          </div>
        )}
        size="480px"
      >
        {activeReport ? (
          <div className="report-drawer__content">
            <Space direction="vertical" style={{ width: "100%" }} size="small">
              <Space size="small" align="center">
                <Tag theme={activeReport.status === "open" ? "warning" : "success"} variant="outline">
                  {activeReport.status === "open" ? "生成中" : "已完成"}
                </Tag>
                <Tag theme="default" variant="outline">docId: {activeReport.documentId}</Tag>
                {activeReport.closedAt ? (
                  <Tag theme="default" variant="outline">closedAt: {activeReport.closedAt}</Tag>
                ) : null}
              </Space>
              {activeReport.reason ? (
                <div>
                  <Tag theme="default" variant="outline">reason</Tag>{" "}
                  <span>{activeReport.reason}</span>
                </div>
              ) : null}
              <Divider style={{ margin: "8px 0" }} />
              <ChatMarkdown content={activeReport.content || "Waiting for report content..."} />
            </Space>
          </div>
        ) : (
          <div>Waiting for report document...</div>
        )}
      </Drawer>

      <Drawer
        header="错误详情"
        visible={errorDrawerOpen}
        onClose={() => setErrorDrawerOpen(false)}
        closeBtn={false}
        footer={(
          <div className="report-drawer__footer">
            <Button theme="primary" onClick={() => setErrorDrawerOpen(false)}>
              关闭
            </Button>
          </div>
        )}
        size="520px"
      >
        <pre className="toolcall__code">{chat.lastError || "暂无错误信息。"}</pre>
      </Drawer>
    </Layout>
  );
}
