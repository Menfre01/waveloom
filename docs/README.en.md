<p align="right"><a href="../README.md">中文版</a></p>

# Waveloom

<p align="center">
  <img src="https://img.shields.io/badge/language-Go-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go"/>
  <img src="https://img.shields.io/badge/DeepSeek-native-4D6BFE?style=flat-square" alt="DeepSeek"/>
  <img src="https://img.shields.io/badge/license-Apache%202.0-8b949e?style=flat-square" alt="license"/>
  <img src="https://img.shields.io/badge/TUI-Bubble%20Tea-5fafd7?style=flat-square" alt="Bubble Tea"/>
  <img src="https://img.shields.io/badge/status-alpha-d4a76a?style=flat-square" alt="alpha"/>
</p>

> A pure-Go, DeepSeek-native TUI coding agent.

**Waveloom** is a pure-Go terminal coding agent purpose-built for the DeepSeek API. It delivers a full interactive coding experience inside your terminal — reading, analyzing, editing, and executing, all done autonomously by the AI while you observe and direct.

---

## Core Design

### The Terminal is the Interface

Waveloom is not a "print logs to terminal" CLI tool. It is a full TUI application:

- **Sandwich layout** — Fixed Header (ASCII art + workspace path) + flexible Viewport (conversation history) + fixed Input + Footer HUD (real-time status bar)
- **Four role prefixes + Spinner animations** — `›` user / `·` assistant / `·` thought / `•` tool, with distinct spinner animations during streaming and static colored markers on completion
- **Real-time Markdown rendering** — Assistant replies rendered as GitHub Flavored Markdown during streaming, with headings, tables, and syntax-highlighted code blocks
- **Thought folding** — Reasoning content displayed in full while streaming, collapsing to a one-line summary on completion (`· thought for 234 tokens — Ctrl+T to expand`)
- **Tool output preview** — Tool results collapsed by default; write_file and shell show first few lines preview, Ctrl+O to expand
- **Unified Diff view** — edit_file changes rendered in `git diff -U3` style with line numbers and green/red background coloring
- **@ File picker** — Type `@` to open an fzf-style fuzzy finder (prefix > substring > initials), real-time filtering, directories marked with `/`
- **Permission overlay** — List-based interaction: `▲ Permission Required` → `▶ Allow` / `Allow All` / `Deny` / `Deny All`, arrow-key navigation + Enter to confirm
- **Footer HUD** — Real-time context bar (`ctx ██████░░░░ 67%`), cache hit rate (`cache 42%`), Turns, Messages, and latency — all color-coded by thresholds

### Prefix Cache First

Waveloom's context management strategy is built around **DeepSeek's Prefix Cache**:

- **Message continuity** — ContextManager accumulates message history across Agent Loop calls, maximizing the common prefix of each request
- **System Prompt anchor** — `messages[0]` is always the system prompt, serving as the fixed starting point of the cache prefix
- **Four-tier watermark compaction** — Triggers at 60% / 80% / 95% with a 98% hard block, escalating progressively with local operations preferred
- **Monotonic boundary guarantees** — Once a compaction decision is made, it never changes for the remainder of the session, preventing cache invalidation from repeated re-compression
- **Sliding-window reasoning clearing** — Retains reasoning_content from the most recent N loops, clears everything older

---

## Install

Prerequisites: **Go 1.25+**, and a [DeepSeek API Key](https://platform.deepseek.com/api_keys).

```sh
# 1. Clone
git clone https://github.com/Menfre01/waveloom.git
cd waveloom && make install

# If wvl is not found, ensure $HOME/go/bin is in PATH:
export PATH=$HOME/go/bin:$PATH  # add to ~/.bashrc or ~/.zshrc

# 2. First-time setup (interactive, once only)
wvl setup
# → Choose Provider → Enter API Key → Choose Model → Done

# Or create config manually (see settings.example.json):
# cp settings.example.json .waveloom/settings.json && vim .waveloom/settings.json

# 3. Start coding
wvl
```

> **One-shot mode**: `wvl "write unit tests for the HTTP server"`
>
> **Skip config file**: `LLM_API_KEY=sk-... wvl`

---

## Configuration

### settings.json

Resolution order: **CLI flags > `.waveloom/settings.json` (project) > `~/.waveloom/settings.json` (global)**

```json
{
  "llm": {
    "api_key": "sk-...",
    "provider": "deepseek",
    "model": "deepseek-v4-flash",
    "base_url": "https://api.deepseek.com",
    "timeout": "600s",
    "retry": {
      "max_retries": 3,
      "initial_backoff": "1s",
      "max_backoff": "30s",
      "multiplier": 2.0
    },
    "extra_params": {
      "thinking": true,
      "reasoning_effort": "medium"
    }
  },
  "permissions": {
    "allow": ["read_file", "search_file", "grep", "ls"],
    "deny":  ["shell(rm -rf /*)"],
    "ask":   ["write_file", "edit_file"]
  },
  "session": {
    "dir": ".waveloom/sessions"
  }
}
```

| Field | Description |
|-------|-------------|
| `llm.api_key` | API key (falls back to `LLM_API_KEY` env var when empty) |
| `llm.provider` | Provider type (`deepseek` / `openai`) |
| `llm.model` | Model name |
| `llm.base_url` | API endpoint |
| `llm.timeout` | Request timeout |
| `llm.retry` | Retry configuration (exponential backoff with jitter) |
| `llm.extra_params` | Provider-specific extra parameters (thinking, reasoning_effort, etc.) |
| `permissions.allow` | Rules to always allow |
| `permissions.deny` | Rules to always deny (highest priority) |
| `permissions.ask` | Rules requiring user confirmation |
| `session.dir` | Session storage directory |

Permission rule format: `ToolName` or `ToolName(pattern)`, with glob support.

### CLI Flags

| Flag | Description |
|------|-------------|
| `--model` | Model name |
| `--system-prompt` | Custom system prompt |
| `--max-turns N` | Maximum turns (0 = unlimited) |
| `--context-limit N` | Context window token limit, supports `1M` / `200k` / `1048576` |
| `--theme auto/dark/light` | Theme mode (auto detects terminal background) |
| `--verbose` | Log detailed output to `.waveloom/wvl.log` |
| `--bypass-permissions` | Skip permission checks (CI/testing) |
| `--resume ID` | Resume a specific session |
| `--settings PATH` | Explicit config file path |

---

## Usage

### Interactive TUI

```sh
wvl
```

Enter the TUI and type like a chat. Press Enter to send. The agent autonomously invokes tools to read files, search code, edit, and run tests.

**Keyboard shortcuts**:

| Key | Action |
|-----|--------|
| `Enter` | Send message |
| `Esc` | Interrupt running agent |
| `↑` `↓` / `PgUp` `PgDn` | Scroll conversation history |
| `Ctrl+E` / `End` | Jump to bottom |
| `Ctrl+T` | Expand/collapse most recent thought |
| `Ctrl+O` | Expand/collapse most recent tool output |
| `Ctrl+G` | Toggle theme (dark / light / auto) |
| `Ctrl+V` | Paste |
| `Ctrl+C` | Quit |

The **footer HUD** shows: current model, context usage (progress bar), cache hit rate, loop count, latency, balance.

### One-shot

```sh
wvl "explain how this function works"
wvl --model deepseek-v4-flash "write unit tests for UserService"
echo "review the code under pkg/llm/" | wvl
```

### @ File references

Type `@` in the input to open a fuzzy file picker (prefix > substring matching). `Tab` enters subdirectories. Selected file contents are automatically injected into the message context.

```
help me optimize the error handling in @pkg/auth/login.go
```

---

## TUI Interface

```
╦ ╦╔═╗╦ ╦╔═╗╦  ╔═╗╔═╗╔╦╗
║║║╠═╣ ║ ║╣ ║  ║ ║║ ║║║║
╚╩╝╩ ╩ ╩ ╚═╝╩═╝╚═╝╚═╝╩ ╩
Workspace: /home/user/project

├─ Viewport ────────────────────────────────────────────────────────┤
│                                                                   │
│   › refactor the auth module, add a rate limiter                  │
│                                                                   │
│   · thought for 234 tokens — Ctrl+T to expand                     │
│   · Okay, let me start by reading auth/login.go...                │
│       Multi-line assistant reply with **GitHub Flavored**         │
│       `markdown` rendering. Code blocks, lists, headings          │
│       are all displayed correctly.                                │
│                                                                   │
│   • read_file   auth/login.go  (230B, 8ms)                        │
│   • write_file  auth/login.go  (1.2KB, 45ms)                      │
│   │  package auth                                                  │
│   │  import "fmt"                                                  │
│   │  ... (Ctrl+O to expand)                                       │
│   • shell       go test ./...  (exit 0, 120ms)                    │
│   │  ok   waveloom/pkg/auth  0.234s                                │
│   │  ... (Ctrl+O to expand)                                       │
│                                                                   │
├─ Input ───────────────────────────────────────────────────────────┤
│ › Type a message, Enter to send...                                 │
├─ Footer HUD ──────────────────────────────────────────────────────┤
  deepseek-v4-flash   ctx ██████░░░░ 67%   cache 42%   T 3   M 15   lat 234ms
└──────────────────────────────────────────────────────────────────┘
```

<p align="center">
  <img src="./docs/tui.png" alt="Waveloom screenshot" width="720"/>
</p>

### Role Prefixes

| Prefix | Role | Streaming Animation | Completed State |
|--------|------|---------------------|-----------------|
| `›` | User | — | Blue |
| `·` | Assistant | Spinner (green pulse) | Green static, Markdown rendered |
| `·` | Thought | Spinner (gray pulse) | Gray, collapsed to one line (Ctrl+T to expand) |
| `•` | Tool | Spinner (line spin) | Green (success) / Red (failure) |

### Footer HUD

| Metric | Format | Description |
|--------|--------|-------------|
| Model | `deepseek-v4-flash` | Green bold |
| Context | `ctx ██████░░░░ 67%` | 10-char bar, <50% green / 50-80% amber / >80% red |
| Cache | `cache 42%` | Hit rate, >50% green / 25-50% amber / <25% muted |
| Turns | `T 3` | Total loops |
| Messages | `M 15` | Current message count |
| Latency | `lat 234ms` | <500ms green / 500ms-2s amber / >2s red |

---

## Architecture

```
                       ┌──────────────┐
                       │  LLM Client  │  API calls (streaming-first, non-streaming fallback)
                       └──────┬───────┘
                              │
                              ▼
                       ┌──────────────┐
                       │  Agent Loop  │  Think-Act-Observe loop
                       └──────┬───────┘
                              │
            ┌─────────────────┼─────────────────┐
            ▼                 ▼                  ▼
     ┌──────────┐    ┌──────────────┐   ┌──────────────┐
     │  Tool    │    │  Permission  │   │   Context    │
     │  System  │    │  & Safety    │   │   Manager    │
     └──────────┘    └──────────────┘   └──────────────┘
```

### Components

| Component | Package | Responsibility |
|-----------|---------|----------------|
| **LLM Client** | `pkg/llm/` | API calls: streaming/non-streaming, DeepSeek + OpenAI providers, exponential backoff with jitter |
| **Agent Loop** | `pkg/agentloop/` | Think-Act-Observe loop: call LLM → parse response → execute tools → collect results → decide termination |
| **Tool System** | `pkg/tool/` | 12 built-in tools: read_file / write_file / edit_file / grep / search_file / ls / shell / web_fetch / lsp_diagnostic / lsp_definition / lsp_references / lsp_hover |
| **Permission** | `pkg/permission/` | Guard interface: allow/deny/ask decisions + rule engine + path safety + command safety + session memory |
| **Context Manager** | `pkg/context/` | Cross-loop message accumulation, four-tier watermark compaction, sliding-window reasoning clearing, session persistence |
| **Memory** | `pkg/memory/` | AGENTS.md hierarchical discovery and loading (global → project root → subdirectories) |
| **Reference** | `pkg/reference/` | @ reference expansion (@file / @folder) + file picker |

### Tools

| Tool | Concurrent-safe | Description |
|------|----------------|-------------|
| `read_file` | 🟢 Yes | Read files with offset/limit, binary detection |
| `write_file` | 🔴 No | Create/overwrite files, auto-create parent directories |
| `edit_file` | 🔴 No | Find-and-replace based on exact old_string matching |
| `grep` | 🟢 Yes | Regex content search with context_lines |
| `search_file` | 🟢 Yes | Glob file search |
| `ls` | 🟢 Yes | List directories with recursive depth |
| `shell` | 🔴 No | Execute shell commands with timeout + dangerous pattern detection |
| `web_fetch` | 🟢 Yes | Fetch online documentation, API references |
| `lsp_diagnostic` | 🟢 Yes | Get file compile errors and lint hints |
| `lsp_definition` | 🟢 Yes | Jump to symbol definition |
| `lsp_references` | 🟢 Yes | Find all references to a symbol |
| `lsp_hover` | 🟢 Yes | Get symbol type signature and documentation |

---

## Context Compaction

Waveloom's four-tier watermark system is a core differentiator:

```
Tier 0 (<60%)   Do nothing — context is ample
Tier 1 (60-80%) Snip — Tool result truncation by strategy (pure local, zero API cost)
Tier 2 (80-95%) Prune — Reasoning clearing + placeholder replacement (pure local)
Tier 3 (≥95%)   Summarize — LLM incremental summary (handoff memo format)
Hard Limit 98%  — Block subsequent LLM calls to prevent API 400 errors
```

**Monotonic boundary guarantee**: Once a compaction decision is made (recorded in the CompactionDecision table), it never changes for the remainder of the session. Tier1Cursor / Tier2Cursor / Tier3Cursor only advance forward — eliminating the "sliding window" pattern that destroys prefix cache on every turn.

**Sliding-window reasoning clearing**: DeepSeek thinking mode's `reasoning_content` is not required across loops. Retain reasoning from the most recent N loops, clear everything older — reducing context volume without altering message structure.

---

## Permission & Safety

- **Three-state decisions**: allow / deny / ask
- **Two-level rules**: Tool-level (`read_file`) + content-level (`shell(git *)`), glob matching
- **Path safety**: Three risk tiers (Safe / Sensitive / Dangerous), with operation-type overlay
- **Command safety**: 20+ dangerous pattern hard blocks + knownSafeCommands fast path
- **Session memory**: User-approved "Allow All" writes to session rules, auto-allow for the remainder of the session
- **Denial tracking**: ≥3 consecutive denials triggers termination
- **Bypass mode**: `--bypass-permissions` or `WAVELOOM_BYPASS_PERMISSIONS=true` (CI/testing)

<p align="center">
  <img src="./docs/permission.png" alt="Permission confirmation dialog" width="560"/>
</p>

---

## Development

```sh
make build       # Build → bin/wvl
make install     # Install → $HOME/go/bin/wvl
make run         # Run
make test        # Test
make clean       # Clean
```

### Project Structure

```
waveloom/
├── cmd/waveloom/          # CLI entry point + TUI
├── pkg/
│   ├── agentloop/         # Agent Loop orchestrator
│   ├── context/           # Context Manager + compaction
│   ├── llm/               # LLM Client
│   ├── memory/            # AGENTS.md persistent memory
│   ├── permission/        # Permission & safety
│   ├── reference/         # @ reference expansion
│   └── tool/              # Tool system
├── specs/                 # Design specs
├── docs/                  # Documentation
├── Makefile
└── go.mod
```

---

## License

Apache License 2.0
