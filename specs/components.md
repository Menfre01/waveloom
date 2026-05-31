# Waveloom 组件列表

## 文档说明

本文档列出 Waveloom Code Agent 的完整组件清单，每项标注：
- **参考来源**：Claude Code / Codex CLI 中的对应实现
- **优先级**：P0（核心路径，不可缺）→ P1（重要增强）→ P2（锦上添花）
- **状态**：✅ 已实现 / 🔶 进行中 / ⬜ 待实施

---

## 总览

```
┌─────────────────────────────────────────────────────────────────┐
│                        Waveloom Code Agent                       │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌─────────┐  ┌──────────┐  ┌──────────┐  ┌──────────────────┐ │
│  │  Layer  │  │  Agent   │  │  Tool    │  │   Permission     │ │
│  │   LLM   │→ │  Loop    │→ │  System  │→ │   & Safety       │ │
│  │  Client │  │  (编排器) │  │  (执行层) │  │   (守门人)       │ │
│  └─────────┘  └────┬─────┘  └──────────┘  └──────────────────┘ │
│                    │                                              │
│       ┌────────────┼────────────┬──────────────┐                 │
│       ▼            ▼            ▼              ▼                 │
│  ┌─────────┐ ┌──────────┐ ┌──────────┐ ┌──────────────┐        │
│  │ Context │ │  Event   │ │  Memory  │ │   Session    │        │
│  │ Manager │ │  Stream  │ │& Persist │ │   Manager    │        │
│  └─────────┘ └──────────┘ └──────────┘ └──────────────┘        │
│                                                                  │
│  ┌─────────┐ ┌──────────┐ ┌──────────┐ ┌──────────────┐        │
│  │  Task   │ │   MCP    │ │  Plugin  │ │   Sub-Agent  │        │
│  │ Planner │ │  Client  │ │  System  │ │  Orchestrator│        │
│  └─────────┘ └──────────┘ └──────────┘ └──────────────┘        │
│                                                                  │
│  ┌──────────────────────────────────────────────────────┐       │
│  │              Server (daemon) + Protocol               │       │
│  │    JSON-RPC over HTTP+SSE，对接 ink / mobile 客户端    │       │
│  └──────────────────────────────────────────────────────┘       │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

---

## 组件清单

### 1. LLM Client

| 属性 | 值 |
|------|-----|
| **优先级** | P0——没有它 Loop 无法运转 |
| **状态** | ✅ 已实现 |
| **职责** | 封装 LLM API 调用：请求构造、流式/非流式响应、多 Provider 适配、重试与退避 |

**参考：**
- Claude Code: `api/claude.ts` — `queryModelWithStreaming`
- Codex CLI: `core/src/client.rs` — `ModelClient` + `ModelClientSession`

**接口：**
```go
type Client interface {
    SendMessage(ctx context.Context, messages []Message, tools []ToolSpec) (*Response, error)
    SendMessageStream(ctx context.Context, messages []Message, tools []ToolSpec) (<-chan StreamingEvent, error)
}
```

**已实现功能：**
- Provider 适配（DeepSeek + OpenAI）
- 流式响应（SSE） + 非流式回退
- 指数退避重试 + jitter（`RetryPolicy`）
- 错误分类（Retryable vs NonRetryable）
- API Key / Settings 管理（`.waveloom/settings.json`）
- 请求超时 600s（对齐 DeepSeek 服务端保活）

详见 `specs/llm-client.md`。

---

### 2. Agent Loop

| 属性 | 值 |
|------|-----|
| **优先级** | P0——Agent 的心脏 |
| **状态** | ✅ 已实现 |
| **职责** | Think-Act-Observe 循环：调用 LLM → 解析流式响应 → 执行工具 → 收集结果 → 决定继续或终止 |

**参考：**
- Claude Code: `src/query.ts` — `queryLoop`
- Codex CLI: `core/src/session/turn.rs` — `run_turn`

**事件驱动的 channel 接口：**
```go
func (l *Loop) Run(ctx context.Context, messages []Message) <-chan TurnEvent
```

事件类型：`StreamDelta`（流式增量）、`ToolCallStart`（工具调用开始）、`ToolCallResult`（执行结果）、`TurnStats`（Token 统计）、`LoopDone`（终止）。

详见 `specs/agent-loop.md`。

---

### 3. Tool System

| 属性 | 值 |
|------|-----|
| **优先级** | P0——没有工具 Agent 只是个聊天 Bot |
| **状态** | ✅ 已实现 |
| **职责** | 工具注册/发现、参数校验、执行调度、结果格式化 |

**内置工具（7 个，全部已实现）：**
| 工具 | 并发安全 | 说明 |
|------|---------|------|
| `read_file` | 🟢 是 | 读取文件，支持 offset/limit，含二进制检测 |
| `write_file` | 🔴 否 | 创建/覆写文件，自动创建父目录 |
| `edit_file` | 🔴 否 | 基于 old_string 精确匹配的查找替换 |
| `shell` | 🔴 否 | 执行 Shell 命令，含超时 + 危险模式检测 |
| `search_file` | 🟢 是 | Glob 文件搜索 |
| `grep` | 🟢 是 | 正则内容搜索，支持 context_lines |
| `ls` | 🟢 是 | 列出目录，支持递归深度 |

**扩展工具（P2，后续 Wave）：**
| 工具 | 参考 |
|------|------|
| `lsp_*` | Claude Code `LSPTool` |
| `web_search` / `web_fetch` | Claude Code `WebSearchTool` |
| `task` | Claude Code `TaskCreate` |
| `notebook` | Claude Code `NotebookEdit` |

详见 `specs/tool-system.md`。

---

### 4. Permission & Safety

| 属性 | 值 |
|------|-----|
| **优先级** | P0——安全底线 |
| **状态** | ✅ 已实现 |
| **职责** | 操作前权限检查、允许/拒绝/询问、规则管理、路径安全、命令安全、会话记忆 |

**已实现功能：**
- `Guard` 接口：`Check()` → allow / deny / ask
- 规则引擎：工具级 + 内容级规则匹配
- 规则来源：config / session / CLI
- 路径安全检查（危险路径拦截）
- 命令安全检查（20+ 危险模式）
- Session 记忆（用户批准的操作自动放行）
- Bypass 模式（CI/测试场景）
- 拒绝跟踪（连续拒绝达上限终止）

详见 `specs/permission-safety.md`。

---

### 5. Context Manager

| 属性 | 值 |
|------|-----|
| **优先级** | P1——长对话必须累积历史 |
| **状态** | ✅ 已实现 |
| **职责** | 跨 Agent Loop 调用累积消息历史，使 DeepSeek 前缀缓存跨轮次命中 |

**已实现功能：**
- `PrepareRun(userInput)` → 返回完整历史副本
- `CompleteRun(messages, stats)` → 提交 Loop 产出
- `Reset()` → 清空历史保留 system prompt
- `Stats()` → 累计 Token 统计

详见 `specs/context-manager.md`。

---

### 6. Server / Protocol（原 CLI / Server）

| 属性 | 值 |
|------|-----|
| **优先级** | P0——用户入口 |
| **状态** | 🔶 进行中（one-shot CLI 已就绪，daemon + 协议设计中） |
| **职责** | 命令行入口 → 演进为 daemon server，通过 JSON-RPC over HTTP+SSE 对接 ink 及移动端客户端 |

**当前状态：**

```
cmd/waveloom/main.go      — 单次 CLI 入口 "wvl 'prompt'"
cmd/waveloom/runner.go    — one-shot 执行，消费 TurnEvent channel
cmd/waveloom/config.go    — CLI 参数解析
```

**架构演进方向（C/S 分离）：**

```
┌─────────────────────────────────────────┐
│              Waveloom Server (daemon)    │
│                                          │
│  POST /rpc        ← JSON-RPC 请求       │
│  GET /sessions/:id/events ← SSE 通知     │
│                                          │
│  methods:                                │
│    session/create     run/start          │
│    run/cancel         run/status         │
│    permission/resolve                    │
│                                          │
│  notifications (SSE):                    │
│    streamDelta       toolCallStart       │
│    permissionRequired  toolCallResult    │
│    turnStats         loopDone            │
│    error             system              │
└─────────────────────────────────────────┘
         │                    ▲
         │ HTTP+SSE           │ POST /rpc
         ▼                    │
┌─────────────┐    ┌──────────────┐
│  ink (TUI)  │    │  mobile app  │
└─────────────┘    └──────────────┘
```

**协议设计原则：**
- SSE 通道只发 JSON-RPC 通知（无 `id`），server 不请求 client
- Client 是所有请求的发起方（`run/start`、`permission/resolve` 等走 POST /rpc）
- `toolCallId` 串联 `toolCallStart` → `permissionRequired` → `toolCallResult` 三个事件
- Session 隔离：每个 session 持有独立的 `ContextManager` + `Guard` + `AgentLoop`

---

### 7. Event Stream / ObserverBus

| 属性 | 值 |
|------|-----|
| **优先级** | P1——可观测性基础设施 |
| **状态** | 🔶 基础事件已实现（`chan TurnEvent`），ObserverBus 待建 |
| **职责** | Loop 生命周期事件发布、工具调用通知、状态变更广播 |

**已实现：** `agentloop.TurnEvent` 接口 + 5 种事件类型（`StreamDelta`, `ToolCallStart`, `ToolCallResult`, `TurnStats`, `LoopDone`）通过 Go channel 推送。

**待建设：** 正式 ObserverBus（多订阅者、事件持久化、Hook 系统）。

---

### 8. Memory & Persistence

| 属性 | 值 |
|------|-----|
| **优先级** | P1——记住项目知识和用户偏好 |
| **状态** | ⬜ 待实施 |
| **职责** | 项目级知识存储（CLAUDE.md）、用户偏好记忆、会话持久化、对话历史恢复 |

**参考：**
- Claude Code: `memory/` — `SessionMemory`, `extractMemories`
- Codex CLI: `core/src/session/` — session state persistence

---

### 9. Task Planner

| 属性 | 值 |
|------|-----|
| **优先级** | P2——复杂任务拆解 |
| **状态** | ⬜ 待实施 |
| **职责** | 用户意图理解 → 任务拆解 → 子任务依赖管理 → 进度追踪 |

---

### 10. MCP Client

| 属性 | 值 |
|------|-----|
| **优先级** | P2——外部工具生态 |
| **状态** | ⬜ 待实施 |
| **职责** | MCP 协议实现：Server 连接/生命周期管理、Tool/Resource/Prompt 发现 |

---

### 11. Sub-Agent Orchestrator

| 属性 | 值 |
|------|-----|
| **优先级** | P2——多 Agent 协作 |
| **状态** | ⬜ 待实施 |
| **职责** | 子 Agent 生命周期管理、Context fork、结果收集、并行调度 |

---

## 实现状态总表

```
✅ LLM Client          — 流式/非流式、DeepSeek+OpenAI、重试退避
✅ Agent Loop          — Think-Act-Observe、channel 事件流、并发/串行工具执行
✅ Tool System         — 7 个内置工具、泛型+类型擦除、ConcurrentSafe 分流
✅ Permission & Safety — Guard 接口、规则引擎、路径/命令安全、session 记忆
✅ Context Manager     — PrepareRun/CompleteRun、前缀缓存优化
──────────────────────────────────────────────────  ← 🎯 最小可用 Agent 分界线
🔶 Server / Protocol   — one-shot CLI 就绪，daemon + JSON-RPC over HTTP+SSE 设计中
🔶 Event Stream        — 基础 TurnEvent channel 就绪，ObserverBus 待建
⬜ Memory & Persistence
⬜ Task Planner
⬜ MCP Client
⬜ Sub-Agent Orchestrator
```

---

## 依赖关系图

```
                         ┌──────────────┐
                         │  LLM Client  │
                         └──────┬───────┘
                                │
                                ▼
                         ┌──────────────┐
                         │  Agent Loop  │◄──────── Event Stream (chan TurnEvent)
                         └──────┬───────┘
                                │
              ┌─────────────────┼─────────────────┐
              ▼                 ▼                  ▼
       ┌──────────┐     ┌──────────────┐   ┌──────────────┐
       │  Tool    │     │  Permission  │   │   Context    │
       │  System  │     │  & Safety    │   │   Manager    │
       └────┬─────┘     └──────────────┘   └──────────────┘
            │
            ▼
    ┌──────────────┐      ┌──────────────┐
    │   Server +   │      │   Memory &   │
    │   Protocol   │      │  Persistence │
    └──────┬───────┘      └──────────────┘
           │
    ┌──────┴───────┐
    ▼              ▼
┌────────┐   ┌──────────┐
│  ink   │   │  mobile  │
└────────┘   └──────────┘
```

### 依赖层次

```
Layer 0 — 通信基础       │  LLM Client
Layer 1 — 编排核心       │  Agent Loop  ←── Event Stream
Layer 2 — 能力+安全+状态  │  Tool System  │  Permission  │  Context Manager
                         │                              ← 🎯 最小可用 Agent
Layer 3 — 接入层          │  Server + Protocol (JSON-RPC over HTTP+SSE)
Layer 4 — 体验增强       │  Memory & Persistence
Layer 5 — 生态扩展       │  Task Planner  │  MCP Client
Layer 6 — 协作规模       │  Sub-Agent Orchestrator
```

---

## 补充说明

### 为什么这个顺序？

**核心原则：尽快交付一个可用的、安全的 code agent，然后通过 C/S 架构开放给多端客户端。**

1. **Layer 0-2（LLM Client + Agent Loop + Tool System + Permission + Context Manager）** ✅ 已完成：最小能力单元。Agent 能"想"和"做"，受权限系统保护，消息历史跨轮次累积。

2. **Layer 3（Server / Protocol）** 🔶 进行中：将 Go 核心拆分为 daemon server，通过 JSON-RPC over HTTP+SSE 协议对外开放。ink (TUI) 和移动端通过同一协议接入。Session 隔离、权限确认、流式输出全部走标准 JSON-RPC 语义。

3. **Layer 4（Memory & Persistence）**：体验增强。项目知识和用户偏好跨会话保留。

4. **Layer 5-6（Task + MCP + Sub-Agent）**：生态和规模。解锁复杂任务、外部工具和多 Agent 协作。

### 参考文件索引

**Claude Code 源码（extracted_sources/）：**

| 目录/文件 | 对应组件 |
|-----------|---------|
| `src/query.ts` | Agent Loop |
| `api/claude.ts` | LLM Client |
| `tools/toolOrchestration.ts` | Tool System |
| `tools/StreamingToolExecutor.ts` | Tool System（流式） |
| `tools/toolExecution.ts` | Tool System |
| `src/QueryEngine.ts` | Session Manager |
| `src/query/stopHooks.ts` | Event Stream |
| `src/query/tokenBudget.ts` | Context Manager |
| `memory/` | Memory & Persistence |
| `tasks/LocalMainSessionTask.ts` | Task Planner |
| `AgentTool/runAgent.ts` | Sub-Agent Orchestrator |
| `mcp/` | MCP Client |
| `permissions/` | Permission & Safety |

**Codex CLI 源码（codex/codex-rs/）：**

| 目录/文件 | 对应组件 |
|-----------|---------|
| `core/src/session/turn.rs` | Agent Loop |
| `core/src/session/handlers.rs` | Session Manager |
| `core/src/session/turn_context.rs` | Context Manager |
| `core/src/client.rs` | LLM Client |
| `tools/src/tool_executor.rs` | Tool System |
| `tools/src/router.rs` | Tool System |
| `core/src/tools/parallel.rs` | Tool System（并发） |
| `core/src/agent/control.rs` | Sub-Agent Orchestrator |
| `core/src/agent/status.rs` | Event Stream |
| `protocol/src/protocol.rs` | Event Stream |
| `core/src/codex_thread.rs` | Session Manager |
| `core/src/state.rs` | Memory & Persistence |
