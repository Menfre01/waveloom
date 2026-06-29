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

**内置工具（12 个，全部已实现）：**

| 工具 | 并发安全 | 说明 |
|------|---------|------|
| `read_file` | 🟢 是 | 读取文件，支持 offset/limit，含二进制检测 |
| `write_file` | 🔴 否 | 创建/覆写文件，自动创建父目录 |
| `edit_file` | 🔴 否 | 基于 old_string 精确匹配的查找替换 |
| `shell` | 🔴 否 | 执行 Shell 命令，含超时 + 危险模式检测 |
| `search_file` | 🟢 是 | Glob 文件搜索 |
| `grep` | 🟢 是 | 正则内容搜索，支持 context_lines |
| `ls` | 🟢 是 | 列出目录，支持递归深度 |
| `web_fetch` | 🟢 是 | 获取在线文档、API 参考 |
| `lsp_diagnostic` | 🟢 是 | 获取文件编译错误和 lint 提示 |
| `lsp_definition` | 🟢 是 | 跳转到符号定义 |
| `lsp_references` | 🟢 是 | 查找符号的所有引用位置 |
| `lsp_hover` | 🟢 是 | 获取符号类型签名和文档 |

**扩展工具（P2，后续 Wave）：**
| 工具 | 参考 |
|------|------|
| `web_search` | Claude Code `WebSearchTool` |
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
- `CompleteRun(messages, stats)` → 提交 Loop 产出，自动触发四级水位线压缩
- `Reset()` → 清空历史保留 system prompt
- `Stats()` → 累计 Token 统计
- Session 持久化（`Save` / `LoadFromFile` / `RemoveSession`）
- `InjectUserInstructions` → 注入 AGENTS.md 内容
- `NewWithCompaction` → 集成 CompactionConfig + Summarizer

详见 `specs/context-manager.md`。

---

### 6. Compaction（四级水位线上下文压缩）

| 属性 | 值 |
|------|-----|
| **优先级** | P1——长对话必须控制上下文成本 |
| **状态** | ✅ 已实现 |
| **职责** | 在跨轮次对话中自动压缩上下文，保持 DeepSeek 前缀缓存命中率 |

**四级水位线：**
| 级别 | 阈值 | 操作 | 说明 |
|------|------|------|------|
| Tier 0 | < 60% | 无操作 | 什么都不做 |
| Tier 1 | 60-80% | Snip | 工具结果差分截断（纯本地，零 API 调用） |
| Tier 2 | 80-95% | Prune | reasoning 清除 + 占位符替换 + 用户代码块压缩（纯本地） |
| Tier 3 | ≥ 95% | Summarize | LLM 增量摘要（需 API 调用） |
| 硬临界 | ≥ 98% | 阻止 | 拒绝后续 LLM 调用 |

**核心保证：** 单调边界 — 一旦对某条消息做出压缩决策，该决策在本次 session 的所有后续轮次中永远不变。

详见 `specs/compaction.md`。

---

### 7. LSP Client

| 属性 | 值 |
|------|-----|
| **优先级** | P1——Agent 需要像人一样理解代码 |
| **状态** | ✅ 已实现 |
| **职责** | LSP 协议客户端：Server 生命周期管理、代码诊断、定义跳转、引用查找、类型签名 |

**已实现功能：**
- 多 Server 管理（`Manager`），按文件扩展名自动匹配
- `gopls` 内置默认配置，支持通过 settings.json 覆盖 Server 路径和参数
- 四类查询：`lsp_diagnostic`、`lsp_definition`、`lsp_references`、`lsp_hover`
- Server 空闲超时自动回收、JSON-RPC over stdio
- 文件同步（`textDocument/didOpen`、`didChange`、`didClose`）

详见 `specs/lsp-tools.md`。

---

### 8. Environment（工具链探测）

| 属性 | 值 |
|------|-----|
| **优先级** | P1——避免 Agent 因命令缺失陷入探测死循环 |
| **状态** | ✅ 已实现 |
| **职责** | 启动时自动探测系统可用工具链，注入 System Prompt |

**已实现功能：**
- 内置探针列表：`go`, `node`, `npm`, `python3`, `rustc`, `cargo`, `gcc`, `g++`, `java`, `mvn`, `make`, `cmake`, `git`, `docker`
- 输出格式化为 `## Environment` 节追加到 System Prompt
- 支持 settings.json 中 `environment.tools` 路径覆盖（全局 + 项目合并，项目优先）

详见 `docs/environment.md`。

---

### 9. Reference（@ 文件引用展开）

| 属性 | 值 |
|------|-----|
| **优先级** | P1——方便引用项目文件 |
| **状态** | ✅ 已实现 |
| **职责** | 解析用户输入中的 `@` 引用，将文件内容注入消息上下文 |

**已实现功能：**
- `@` 符号触发文件引用，支持模糊匹配
- TUI 中集成文件选择器（Picker），输入 `@` 弹出下拉列表
- 与 Guard 集成，引用前检查文件读取权限

详见 `specs/reference-context.md`。

---

### 10. TUI（终端交互界面）

| 属性 | 值 |
|------|-----|
| **优先级** | P0——用户入口 |
| **状态** | ✅ 已实现（one-shot + 交互式） |
| **职责** | 终端用户界面：流式渲染、多面板布局、权限确认、@ 文件选择 |

**已实现功能：**
- 基于 Bubble Tea v2 + Glamour Markdown 渲染 + Lipgloss 样式
- 流式渲染 thought / text / tool 输出，支持折叠展开（`Ctrl+T` / `Ctrl+O`）
- 权限确认弹框（allow / deny / ask）
- @ 文件选择器，模糊匹配
- 暗色/亮色/自动主题切换（`Ctrl+G`）
- IME 输入支持（CJK 等宽渲染）
- `--continue` / `--resume` session 恢复
- `waveloom ls` 列出最近 sessions

详见 `specs/tui.md`。

---

### 11. Server / Protocol（原 CLI / Server）

| 属性 | 值 |
|------|-----|
| **优先级** | P0——用户入口 |
| **状态** | ⬜ 待实施 |
| **职责** | 命令行入口 → 演进为 daemon server，通过 JSON-RPC over HTTP+SSE 对接 ink 及移动端客户端 |

**当前状态：**

```
cmd/waveloom/main.go      — 单次 CLI 入口 "waveloom 'prompt'"
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

### 12. Event Stream / ObserverBus

| 属性 | 值 |
|------|-----|
| **优先级** | P1——可观测性基础设施 |
| **状态** | 🔶 基础事件已实现（`chan TurnEvent`），ObserverBus 待建 |
| **职责** | Loop 生命周期事件发布、工具调用通知、状态变更广播 |

**已实现：** `agentloop.TurnEvent` 接口 + 5 种事件类型（`StreamDelta`, `ToolCallStart`, `ToolCallResult`, `TurnStats`, `LoopDone`）通过 Go channel 推送。

**待建设：** 正式 ObserverBus（多订阅者、事件持久化、Hook 系统）。

---

### 13. Memory & Persistence

| 属性 | 值 |
|------|-----|
| **优先级** | P1——记住项目知识和用户偏好 |
| **状态** | ✅ 已实现（AGENTS.md 层级加载 + session 持久化） |
| **职责** | 项目级知识存储（AGENTS.md）、会话持久化（session 落盘/恢复/列出） |

**已实现功能：**
- AGENTS.md 层级发现与加载（home → CWD → 项目根，根→叶序拼接）
- Session 持久化（JSON 落盘到 `~/.waveloom/<project>/sessions/`）
- `--resume <id>` 恢复指定 session，`--continue` 恢复最近 session
- `waveloom ls` 列出最近 sessions
- Transcript 回放（以文本格式记录对话，含行级时间戳）

**参考：**
- Claude Code: `memory/` — `SessionMemory`, `extractMemories`
- Codex CLI: `core/src/session/` — session state persistence

详见 `specs/agents-md-memory.md`。

---

### 14. Task Planner

| 属性 | 值 |
|------|-----|
| **优先级** | P2——复杂任务拆解 |
| **状态** | ⬜ 待实施 |
| **职责** | 用户意图理解 → 任务拆解 → 子任务依赖管理 → 进度追踪 |

---

### 15. MCP Client

| 属性 | 值 |
|------|-----|
| **优先级** | P2——外部工具生态 |
| **状态** | ⬜ 待实施 |
| **职责** | MCP 协议实现：Server 连接/生命周期管理、Tool/Resource/Prompt 发现 |

---

### 16. Sub-Agent Orchestrator

| 属性 | 值 |
|------|-----|
| **优先级** | P2——多 Agent 协作 |
| **状态** | ⬜ 待实施 |
| **职责** | 子 Agent 生命周期管理、Context fork、结果收集、并行调度 |

---

### 17. SlashCommand（Slash 命令系统）

| 属性 | 值 |
|------|-----|
| **优先级** | P0——用户本地交互命令 |
| **状态** | ⬜ 待实施 |
| **职责** | 拦截 `/` 前缀输入，执行本地命令：session 重置、模型/Provider 热切换、settings 编辑覆盖层、主题切换、状态查询 |

**参考：**
- Claude Code: `/clear`, `/compact`, `/config`, `/status`, `/cost`, `/init`, `/doctor`
- Codex CLI: `core/src/commands/`

**首版命令（4 个）：**
| 命令 | 说明 |
|------|------|
| `/new` (`/clear`) | 创建全新 session（新 session ID，全新上下文） |
| `/model [name]` | 显示/热切换模型（不重启 Loop），同步更新 HUD |
| `/theme` | 调起主题选择列表覆盖层（Auto / Dark / Light） |
| `/help` | 列出所有可用命令 |

> `/status` 不需要 — Footer HUD 已实时显示 session 状态。
> `/provider` / `/config` 不需要 — 用户直接编辑 settings.json 即可。
> `/skill` 纳入独立 spec 规划。

**关键设计决策：**
- 与 Tool System 解耦：slash 命令不发送给 LLM，不经过 Agent Loop
- `/config` 调起 Bubble Tea 覆盖层提供 UI 编辑 settings
- `/model` / `/provider` 热替换 llmClient 参数，不重启 session
- 命令自动补全复用 @picker 的 list.Model + fuzzyFilter 基础设施
- Registry 模式对齐 Tool System，构造期注册

详见 `specs/slash-command.md`。

---

## 实现状态总表

```
✅ LLM Client          — 流式/非流式、DeepSeek+OpenAI、重试退避
✅ Agent Loop          — Think-Act-Observe、channel 事件流、并发/串行工具执行
✅ Tool System         — 12 个内置工具、泛型+类型擦除、ConcurrentSafe 分流
✅ Permission & Safety — Guard 接口、规则引擎、路径/命令安全、session 记忆
✅ Context Manager     — PrepareRun/CompleteRun、前缀缓存优化
✅ Compaction          — 四级水位线、Snip/Prune/Summarize、单调边界保证
✅ LSP Client          — gopls 等 Server 自动启动、四类 LSP 查询
✅ Memory              — AGENTS.md 层级加载、session 落盘/恢复/列出
✅ TUI                 — Bubble Tea v2 流式渲染、Markdown、@ 文件选择器
✅ Environment         — 工具链自动探测、settings.json 路径覆盖
✅ Reference           — @ 文件引用展开
✅ Diff View           — edit_file 统一 diff 视图（行号、上下文、着色）
✅ HUD                 — Footer HUD 与 ContextManager 解耦，CompleteResult 驱动
──────────────────────────────────────────────────  ← 🎯 最小可用 Agent 分界线
⬜ Server / Protocol   — daemon + JSON-RPC over HTTP+SSE 待建
🔶 Event Stream        — 基础 TurnEvent channel 就绪，ObserverBus 待建
⬜ Task Planner
⬜ MCP Client
⬜ Sub-Agent Orchestrator
⬜ SlashCommand         — /new /model /theme /help
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
       └────┬─────┘     └──────┬───────┘   └──────┬───────┘
            │                  │                   │
            ▼                  │                   ▼
    ┌───────────────┐          │          ┌──────────────┐
    │  LSP Client   │          │          │  Compaction  │
    │  Environment  │          │          │  (四级水位线) │
    └───────────────┘          │          └──────────────┘
                               │
            ┌──────────────────┼──────────────────┐
            ▼                  ▼                  ▼
    ┌──────────────┐  ┌──────────────┐  ┌──────────────┐
    │   Memory &   │  │   Server +   │  │  Reference   │
    │  Persistence │  │   Protocol   │  │  (@ 展开)    │
    └──────────────┘  └──────┬───────┘  └──────────────┘
                             │
                      ┌──────┴───────┐
                      ▼              ▼
              ┌────────────┐ ┌──────────────┐
              │  ink (TUI) │ │  mobile app  │
              └─────┬──────┘ └──────────────┘
                    │
                    ▼
              ┌──────────────┐
              │  SlashCommand │  ← /new /model /theme /help
              │  (本地命令层)  │
              └──────────────┘
```

### 依赖层次

```
Layer 0 — 通信基础       │  LLM Client
Layer 1 — 编排核心       │  Agent Loop  ←── Event Stream
Layer 2 — 能力+安全+状态  │  Tool System  │  Permission  │  Context Manager
                         │  LSP Client   │  Compaction  │  Environment
                         │                              ← 🎯 最小可用 Agent
Layer 3 — 接入层          │  Server + Protocol (JSON-RPC over HTTP+SSE)
                         │  TUI (Bubble Tea v2)  │  Reference  │  SlashCommand
Layer 4 — 体验增强       │  Memory & Persistence
Layer 5 — 生态扩展       │  Task Planner  │  MCP Client
Layer 6 — 协作规模       │  Sub-Agent Orchestrator
```

---

## 补充说明

### 为什么这个顺序？

**核心原则：尽快交付一个可用的、安全的 code agent，然后通过 C/S 架构开放给多端客户端。**

1. **Layer 0-2（LLM Client + Agent Loop + Tool System + Permission + Context Manager + Compaction + LSP Client + Environment）** ✅ 已完成：最小能力单元。Agent 能"想"和"做"，受权限系统保护，消息历史跨轮次累积，支持四级水位线压缩、LSP 代码理解和环境工具链探测。TUI 提供终端交互界面，Reference 支持 @ 引用展开。

2. **Layer 3（Server / Protocol）** 🔶 进行中：将 Go 核心拆分为 daemon server，通过 JSON-RPC over HTTP+SSE 协议对外开放。ink (TUI) 和移动端通过同一协议接入。Session 隔离、权限确认、流式输出全部走标准 JSON-RPC 语义。

3. **Layer 4（Memory & Persistence）** ✅ 已实现：AGENTS.md 层级加载（home → CWD → 项目根），session 落盘/恢复/列出。

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
