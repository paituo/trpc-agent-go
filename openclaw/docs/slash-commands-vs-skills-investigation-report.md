# 调查报告：Slash Commands vs Skills 对比分析 + 博微造价对接方案

> 日期：2026-06-06
> 项目：OPENCLAW (trpc-agent-go)
> 场景：集成 AG-UI + 博微造价软件对接

---

## 第一阶段：调查（收集证据）

### 1.1 OPENCLAW 现有机制映射

通过代码审查，OPENCLAW 当前有两个核心扩展机制：

**技能系统（Skills）** — `internal/skills/repository.go` + `internal/skills/frontmatter.go`

```
SKILL.md 格式:
---
name: hello
description: Write a hello file to the workspace output directory.
---

Overview / Command / Output Files (Markdown Body)
```

Frontmatter 支持的元数据字段：
- `always` — 是否始终启用
- `skillKey` — 配置键映射
- `primaryEnv` — 主环境变量（API Key）
- `emoji` / `homepage` — 展示用
- `os` — 操作系统限制
- `requires` — 依赖（bins、anyBins、env、config）
- `install` — 安装指令

**stdin 插件** — `plugins/stdin/stdin.go`

当前仅支持两个命令：
- `/exit` — 退出
- `/quit` — 退出

**关键发现：** stdin 插件在 `Run()` 方法中直接将用户输入文本通过 `processMessage()` 发送给 Gateway，**没有任何命令路由逻辑**。

### 1.2 行业 Slash Command 机制映射

| 机制 | 定义方式 | 执行方式 | 参数化 | 可发现性 | 作用域 |
|------|---------|---------|--------|---------|--------|
| **Claude Code Slash Commands** | `.claude/commands/*.md` | Prompt 注入 | `$ARGUMENTS`, `$1`..`$N`, Bash 预执行 | `/help` 列出 | 项目级 + 用户级 + MCP + Plugin |
| **Claude Code Skills** | `.claude/skills/*/SKILL.md` | Agent 行为定义 | 通过 Agent 理解 | Agent 自动选择 | 项目级 + 用户级 |
| **Cursor Rules** | `.cursor/rules/*.mdc` | 上下文注入 | `${selectedText}`, `${file}` | 自动/手动触发 | 项目级 |
| **Aider Commands** | Python `cmd_*` 方法 | 直接执行代码 | 方法参数 | `/help` + TAB | 内置 |
| **Cline Commands** | 内置 + `.clinerules` | Prompt 注入 | 无 | 输入 `/` 列出 | 内置 |
| **GitHub Copilot Prompts** | `.github/prompts/*.prompt.md` | Prompt 注入 | `${selectedText}`, VS Code 变量 | 输入 `/` 列出 | 项目级 + 用户级 |
| **Trae SOLO Commands** | UI 配置面板 | Prompt 注入 | 无 | 输入 `/` 列出 | 云端/本地 |
| **OPENCLAW Skills** | `skills/*/SKILL.md` | Agent 行为定义 | 无 | Agent 自动选择 | 多根目录分层 |

---

## 第二阶段：分析（形成假设）

### 2.1 Slash Commands vs Skills：本质区别

```
┌─────────────────────────────────────────────────────────────┐
│                    用户意图光谱                               │
│                                                             │
│  明确指令 ──────────────────────────────────── 模糊意图      │
│                                                             │
│  /commit          /review          "帮我优化这段代码"         │
│  /test            /refactor        "这个设计有什么问题"       │
│  /clear           /security        "分析一下项目风险"         │
│                                                             │
│  ◄── Slash Commands ──►◄──── Skills ────►◄── 自然语言 ──►   │
│  用户主动触发           Agent 自动选择        完全开放        │
│  确定性输出             引导性输出             不确定性输出    │
└─────────────────────────────────────────────────────────────┘
```

**核心假设 H1：Slash Commands 和 Skills 是互补关系，不是替代关系。**

| 维度 | Slash Commands | Skills |
|------|---------------|--------|
| **触发方式** | 用户显式输入 `/command` | Agent 根据上下文自动选择 |
| **确定性** | 高——每次执行相同模板 | 中——Agent 理解后自主决策 |
| **复杂度** | 轻量——Prompt 模板 | 重量——行为定义 + 依赖 + 安装 |
| **参数化** | `$ARGUMENTS`, 位置参数 | 无显式参数（Agent 理解自然语言） |
| **可发现性** | `/help` 或 TAB 补全 | Agent 内部，用户不可见 |
| **适用场景** | 重复性操作、标准化流程 | 复杂能力扩展、环境依赖功能 |
| **类比** | 快捷键 / 别名 | 插件 / 能力包 |
| **Frontmatter** | `description`, `thinking` | `requires`, `install`, `os`, `always` |

**核心假设 H2：在博微造价场景中，Slash Commands 是"对话式造价操作"的最佳交互模式。**

原因：
- 造价编制流程是**高度结构化**的（创建项目 → 导入清单 → 套定额 → 调价 → 出报表）
- 每个步骤都有**明确的输入输出**（如 `/套定额 110kV变压器`）
- 用户需要**精确控制**执行什么操作，而不是让 Agent 自由发挥
- 但同时需要 Agent 的**智能判断**（如自动匹配定额、推荐材料价格）

### 2.2 AG-UI 在 OPENCLAW + 博微场景中的角色

**核心假设 H3：AG-UI 协议是连接 OPENCLAW Agent 与博微造价软件 UI 的最佳桥梁。**

```
┌──────────────────────────────────────────────────────────────────┐
│                    系统架构全景                                    │
│                                                                  │
│  ┌─────────────┐    AG-UI (SSE)    ┌──────────────────────┐     │
│  │  前端 UI     │ ◄──────────────► │  OPENCLAW Agent      │     │
│  │  (CopilotKit│    事件流          │  ┌────────────────┐  │     │
│  │   / 自定义)  │                   │  │ Slash Commands  │  │     │
│  │             │                   │  │ /新建工程        │  │     │
│  │  ┌───────┐  │                   │  │ /导入清单        │  │     │
│  │  │造价表  │  │                   │  │ /套定额          │  │     │
│  │  │格视图  │  │                   │  │ /调价            │  │     │
│  │  └───────┘  │                   │  │ /出报表          │  │     │
│  │  ┌───────┐  │                   │  └────────────────┘  │     │
│  │  │3D可视 │  │                   │  ┌────────────────┐  │     │
│  │  │化面板  │  │                   │  │ Skills          │  │     │
│  │  └───────┘  │                   │  │ 定额库查询       │  │     │
│  │  ┌───────┐  │                   │  │ 材料价格比对     │  │     │
│  │  │对话面板│  │                   │  │ 合规性检查       │  │     │
│  │  └───────┘  │                   │  └────────────────┘  │     │
│  └─────────────┘                   │  ┌────────────────┐  │     │
│                                    │  │ Tools (MCP)     │  │     │
│                                    │  │ 博微API适配器    │  │     │
│                                    │  │ 定额数据库       │  │     │
│                                    │  └────────────────┘  │     │
│                                    └──────────┬───────────┘     │
│                                               │                  │
│                                    ┌──────────▼───────────┐     │
│                                    │  博微造价软件          │     │
│                                    │  (REST API / COM)     │     │
│                                    └──────────────────────┘     │
└──────────────────────────────────────────────────────────────────┘
```

---

## 第三阶段：假设测试

### H1 测试：Slash Commands 和 Skills 互补

**证据支持：**

Claude Code 官方文档明确区分了两者：
- **Slash Commands**：用于"单次、可重复的任务"，如脚手架组件、代码审查
- **Skills**：用于"持久的行为模式"，如编码风格、项目规范

在 OPENCLAW 中：
- **Skills** 已有完整的依赖检查、环境验证、安装指令机制——适合复杂能力
- **Slash Commands**（尚不存在）——需要轻量级的 prompt 模板路由

**结论：H1 确认。** 两者服务不同场景，应共存。

### H2 测试：博微造价场景适合 Slash Commands

**证据支持：**

博微造价软件的操作流程高度结构化：
1. 创建项目（指定工程类型、地区、定额版本）
2. 导入工程量清单（Excel/CSV）
3. 套定额（自动/手动匹配定额子目）
4. 调整材料价格（市场价/信息价）
5. 费用汇总与取费
6. 生成报表（概预算表、费用汇总表）

每个步骤都可以映射为一个 Slash Command：
- `/新建工程 110kV变电站 浙江 2018定额`
- `/导入清单 清单文件.xlsx`
- `/套定额 自动`
- `/调价 市场价`
- `/出报表 概预算表`

**结论：H2 确认。** 造价编制的结构化流程天然适合 Slash Command 模式。

### H3 测试：AG-UI 是最佳桥梁

**证据支持：**

1. AG-UI 已被 AWS AgentCore、CopilotKit、AG2、LangGraph 等主流框架采纳
2. AG-UI 的核心特性完美匹配造价场景需求：
   - **实时流式输出** → 造价计算过程可视化
   - **Tool Call 事件** → 博微 API 调用进度展示
   - **共享状态** → 造价数据双向同步
   - **Human-in-the-Loop** → 关键操作人工审批（如调价确认）
   - **Generative UI** → 动态生成造价表格、3D 可视化

3. OPENCLAW 已有 A2A 服务和 Gateway 流处理，AG-UI 可以复用这些基础设施

**结论：H3 确认。** AG-UI 是当前最成熟的 Agent-UI 交互协议。

---

## 第四阶段：实施方案

### 4.1 博微造价 Slash Commands 设计

```
.openclaw/commands/
├── 新建工程.md          # /新建工程
├── 导入清单.md          # /导入清单
├── 套定额.md            # /套定额
├── 调价.md              # /调价
├── 出报表.md            # /出报表
├── 审核.md              # /审核
├── 对比.md              # /对比
└── 造价/
    ├── 概预算.md         # /概预算
    ├── 招投标.md         # /招投标
    └── 结算.md           # /结算
```

**示例命令定义 — `套定额.md`：**

```markdown
---
description: "对当前工程量清单套取匹配的定额子目"
thinking: true
---

对当前打开的工程造价项目执行定额套用操作：

1. 读取当前项目的工程量清单
2. 根据清单项目特征，在定额库中查找匹配的定额子目
3. 优先使用 $1 指定的匹配策略（auto/manual/semi-auto，默认 auto）
4. 对于无法自动匹配的项目，列出候选定额供用户选择
5. 套用完成后，显示匹配率和未匹配项清单

匹配策略说明：
- auto: 全自动匹配，置信度 > 0.8 的直接套用
- semi-auto: 自动匹配但每项需确认
- manual: 仅列出候选，由用户逐一选择

定额版本：使用项目创建时指定的定额版本（2018版/2021版）
地区系数：按项目所在地区自动应用
```

### 4.2 博微造价 Skills 设计

```
.openclaw/skills/
├── 定额查询/
│   └── SKILL.md         # 定额库智能查询能力
├── 材料比价/
│   └── SKILL.md         # 材料价格比对与推荐
├── 合规检查/
│   └── SKILL.md         # 造价合规性自动检查
└── 风险评估/
    └── SKILL.md         # 造价风险智能评估
```

**示例技能定义 — `合规检查/SKILL.md`：**

```markdown
---
name: 合规检查
description: 检查工程造价编制的合规性，包括定额套用、取费标准、规费计算等
metadata:
  openclaw:
    requires:
      config:
        - bowei.api
---

概述

本技能对当前工程造价项目执行合规性检查，涵盖以下方面：

1. 定额套用合规性
   - 检查是否使用了正确的定额版本
   - 验证定额子目与工程特征的匹配度
   - 标记可能的错套、漏套、重套

2. 取费标准合规性
   - 验证管理费、利润、规费费率是否符合当地标准
   - 检查税金计算是否正确
   - 核实措施费计取是否合理

3. 数据一致性
   - 工程量与清单数量是否一致
   - 单价合价是否计算正确
   - 汇总数据是否与明细一致

输出格式为合规性检查报告，包含问题等级（严重/警告/建议）和修复建议。
```

### 4.3 Slash Commands 与 Skills 的协作模式

```
用户输入: /套定额 auto
    │
    ▼
┌─────────────────────────────────────────────────────┐
│ Slash Command 路由层                                  │
│ 1. 解析 /套定额 → 加载 套定额.md 模板               │
│ 2. 替换参数: $1 = "auto"                             │
│ 3. 将渲染后的 prompt 注入 Agent 对话                 │
└──────────────────────┬──────────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────────┐
│ OPENCLAW Agent                                       │
│ 1. 理解 prompt: 需要对清单套定额                     │
│ 2. 自动激活 "定额查询" Skill                         │
│ 3. 调用博微 API Tool: GET /projects/{id}/quantities  │
│ 4. 调用博微 API Tool: POST /projects/{id}/auto-match │
│ 5. 返回匹配结果                                      │
└──────────────────────┬──────────────────────────────┘
                       │ AG-UI 事件流
                       ▼
┌─────────────────────────────────────────────────────┐
│ 前端 UI (AG-UI Client)                               │
│ - TOOL_CALL_START: "正在读取工程量清单..."            │
│ - TOOL_CALL_START: "正在自动匹配定额..."              │
│ - STATE_SNAPSHOT: { 匹配率: 87%, 未匹配: 15项 }      │
│ - TEXT_MESSAGE: 匹配结果摘要                          │
│ - Generative UI: 渲染匹配结果表格                    │
└─────────────────────────────────────────────────────┘
```

### 4.4 博微 API 对接层设计

| 对接方式 | 适用场景 | 成熟度 |
|---------|---------|--------|
| RESTful API | 项目 CRUD、清单导入导出、计算触发 | 主流，推荐 |
| COM 组件 | 桌面端自动化操作 | 传统，兼容性好 |
| 文件接口 | Excel/CSV 导入导出 | 最基础，通用 |
| 插件机制 | 地区定制、流程扩展 | 博微已有插件生态 |

**OPENCLAW Tool 适配器设计：**

```go
// 博微造价 API Tool - 作为 OPENCLAW 的 MCP Tool 注册
type BoweiAPITool struct {
    baseURL    string
    apiKey     string
    httpClient *http.Client
}

// 支持的操作（映射为 Agent 可调用的 Tool）
// - bowei_create_project    → 创建造价项目
// - bowei_import_quantities → 导入工程量清单
// - bowei_auto_match_quota  → 自动套定额
// - bowei_adjust_price      → 调整材料价格
// - bowei_calculate         → 执行造价计算
// - bowei_export_report     → 导出报表
// - bowei_get_project       → 获取项目详情
// - bowei_list_quotas       → 查询定额库
// - bowei_audit_check       → 审核检查
```

### 4.5 大模型智能体拔高造价专业性的具体路径

| 拔高方向 | 实现方式 | Slash Command / Skill |
|---------|---------|----------------------|
| **智能定额匹配** | LLM 理解工程特征描述，语义匹配定额子目 | `/套定额` + "定额查询" Skill |
| **材料价格预测** | 基于历史数据和市场趋势，预测材料价格走势 | `/调价 预测` + "材料比价" Skill |
| **合规性自动审查** | LLM 交叉验证定额套用、取费标准、计算逻辑 | `/审核` + "合规检查" Skill |
| **多方案造价对比** | 自动生成不同工艺/材料方案的造价对比 | `/对比` |
| **风险预警** | 识别超概风险、价格异常、遗漏项 | "风险评估" Skill（自动触发） |
| **自然语言编制造价** | 用户用自然语言描述需求，Agent 自动拆解为操作步骤 | 对话模式 + Skills 联动 |
| **历史经验复用** | 匹配相似历史项目，推荐定额和价格参考 | "定额查询" Skill |

---

## 调查结论

```
调查完成
═══════════════════════════════════════════════════

问题: OPENCLAW 缺乏 Slash Command 机制，且需对接博微造价软件
严重性: P1 (功能缺失，影响用户交互体验和行业落地)
可复现性: 总是 (当前 stdin 插件仅支持 /exit 和 /quit)

根因: OPENCLAW 的 stdin 插件将所有用户输入直接路由到 Gateway，
      没有命令解析层；Skills 系统面向 Agent 自动选择，
      不支持用户显式触发

已测试假设:
  ✓ H1: Slash Commands 和 Skills 是互补关系
  ✓ H2: 博微造价场景适合 Slash Commands
  ✓ H3: AG-UI 是连接 Agent 与造价 UI 的最佳桥梁

实施路径:
  1. 在 stdin 插件中添加 Slash Command 路由层
  2. 实现 .openclaw/commands/ Markdown 文件加载
  3. 开发博微造价 API Tool 适配器 (MCP Tool)
  4. 集成 AG-UI 协议实现前端交互
  5. 定义造价领域专用 Slash Commands 和 Skills

优先级:
  P0: Slash Command 路由 + 5 个内置命令
  P0: 博微 API Tool 适配器 (核心 CRUD)
  P1: AG-UI 集成 + 造价专用命令
  P2: 造价 Skills (合规检查、风险评估)
  P2: Generative UI (3D 可视化、动态表格)
```

### 关键决策点

1. **Slash Command 定义格式**：建议复用 SKILL.md 的 Frontmatter 解析逻辑，但使用不同的文件名（`COMMAND.md` vs `SKILL.md`）和目录（`commands/` vs `skills/`），保持一致性

2. **AG-UI 集成方式**：建议在 OPENCLAW Gateway 层实现 AG-UI 事件发射器，复用现有的 `StreamMessage` 流式机制，将 `gwproto.StreamEvent` 映射为 AG-UI 事件类型

3. **博微对接优先级**：先实现 RESTful API 对接（最通用），再考虑 COM 组件（桌面端深度集成），文件接口作为兜底

---

## 已交付代码清单

### 1. Slash Command 核心包 (`internal/commands/`)

| 文件 | 功能 |
|------|------|
| `command.go` | 核心类型：`Command`、`CommandSource`、`CommandCall` |
| `parser.go` | 输入解析：`ParseCall()`、`IsCommand()` |
| `repository.go` | 注册表：`Register`/`Get`/`List`/`Render`/`LoadFromFlatDir`/`HelpText` |
| `parser_file.go` | Markdown 命令文件解析（YAML Frontmatter + Body） |
| `builtin.go` | 5 个内置命令：`/help`、`/clear`、`/compact`、`/review`、`/init` |

### 2. stdin 插件改造 (`plugins/stdin/`)

| 文件 | 改动 |
|------|------|
| `stdin.go` | 添加 `cmdRepo` 字段、命令路由逻辑、`handleCommand()` 方法、项目级/用户级命令加载 |

### 3. 博微造价领域命令 (`commands/`)

| 文件 | 命令 |
|------|------|
| `新建工程.md` | `/新建工程` |
| `导入清单.md` | `/导入清单` |
| `套定额.md` | `/套定额` |
| `调价.md` | `/调价` |
| `出报表.md` | `/出报表` |
| `审核.md` | `/审核` |
| `对比.md` | `/对比` |

### 4. 博微 API Tool 适配器 (`internal/tools/bowei/`)

| 文件 | 功能 |
|------|------|
| `tool.go` | 9 个博微 API 工具函数，通过 registry 注册 |

### 5. AG-UI 事件发射器 (`internal/agui/`)

| 文件 | 功能 |
|------|------|
| `types.go` | AG-UI 协议 13 种事件类型定义 |
| `emitter.go` | `gwproto.StreamEvent` → AG-UI `Event` 映射 |
| `server.go` | SSE 服务器，基于 session 的发布/订阅 |
