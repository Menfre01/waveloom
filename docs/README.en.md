<p align="center">
  <strong>English</strong>
  &nbsp;·&nbsp;
  <a href="../README.md">简体中文</a>
</p>

<p align="center">
  <img src="./logo.svg" alt="Waveloom" width="420"/>
</p>

<p align="center">
  <a href="https://github.com/Menfre01/waveloom/releases/latest"><img src="https://img.shields.io/github/v/release/Menfre01/waveloom?style=flat-square&color=00ADD8&labelColor=161b22" alt="release"/></a>
  <a href="#"><img src="https://img.shields.io/badge/language-Go-00ADD8?style=flat-square&logo=go&logoColor=white&labelColor=161b22" alt="Go"/></a>
  <a href="#"><img src="https://img.shields.io/badge/DeepSeek-native-4D6BFE?style=flat-square&labelColor=161b22" alt="DeepSeek"/></a>
  <a href="../LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-8b949e?style=flat-square&labelColor=161b22" alt="license"/></a>
  <a href="https://github.com/charmbracelet/bubbletea"><img src="https://img.shields.io/badge/TUI-Bubble%20Tea-5fafd7?style=flat-square&labelColor=161b22" alt="Bubble Tea"/></a>
  <a href="#"><img src="https://img.shields.io/badge/status-alpha-d4a76a?style=flat-square&labelColor=161b22" alt="alpha"/></a>
  <a href="https://github.com/Menfre01/waveloom/releases"><img src="https://img.shields.io/github/downloads/Menfre01/waveloom/total?style=flat-square&color=3fb950&labelColor=161b22&label=downloads" alt="downloads"/></a>
</p>

---

**Waveloom** is a terminal Code Agent **purpose-built for DeepSeek prefix caching** (pure Go). It leverages DeepSeek's prefix cache mechanism — with a fixed System Prompt anchor, turn-accumulated message history, and compaction that never mutates bytes — to push context cache hit rates to **95–99%**, slashing input token costs to **1/50 ~ 1/120** of the cache-miss price.

You describe what you want in natural language. The agent reads code, analyzes logic, edits files, and executes commands — right in your terminal. Every write and command execution requires your consent first. Primary recommended model: `deepseek-v4-pro`. Also compatible with `deepseek-v4-flash` and OpenAI-compatible endpoints.

> [!IMPORTANT]
> **Safe & Transparent**: The agent always asks for confirmation before writing files or executing commands — nothing happens silently. **API Key Required**: Get one from [DeepSeek](https://platform.deepseek.com/api_keys), then run `wvl setup`.

---

## Why Waveloom

| Dimension | Waveloom's Approach | Why It Matters |
|-----------|-------------------|----------------|
| **Terminal-Native TUI** | Built on [Bubble Tea](https://github.com/charmbracelet/bubbletea) v2 + [Glamour](https://github.com/charmbracelet/glamour) Markdown rendering + [Lipgloss](https://github.com/charmbracelet/lipgloss) styling | Streaming rendering of thought/text/tool output with collapse/expand — not a "black box chat", fully transparent and reviewable |
| **DeepSeek Prefix Cache Optimization** | System prompt fixed as `messages[0]`, message history accumulated across turns without reset, compacted bytes never change | Maximum common prefix stays cache-hot; cache-hit token price is **1/50 ~ 1/120** of cache-miss |
| **Four-Tier Watermark Context Compaction** | 60% → Snip (tool output truncation), 80% → Prune (reasoning removal + placeholders), 95% → Summarize (LLM incremental summary), 98% → Hard cutoff | Automatic management of million-token context window — long conversations keep what matters, drop noise, and never suffer Context Rot |
| **Native LSP Integration** | Built-in LSP client; agent can proactively call `lsp_diagnostic` / `lsp_definition` / `lsp_references` / `lsp_hover` | Agent understands code like you do — jump to definitions, find references, inspect type signatures — not coding blind |
| **Permission Safety Model** | Three-tier decisions (allow / deny / ask), rule engine with pattern matching like `shell(git *)`, CI `--bypass-permissions` | You always have the final say; file writes and command execution never happen silently |
| **Single Binary Deployment** | Pure Go, zero runtime dependencies, ~15MB pre-built binary | One `curl` command to install; macOS / Linux AMD64 & ARM64 all supported |

---

## Install

Requires: [DeepSeek API Key](https://platform.deepseek.com/api_keys).

**macOS**

```sh
sudo curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/wvl_darwin_arm64.tar.gz | sudo tar -xz -C /usr/local/bin wvl
```

**Linux**

```sh
sudo curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/wvl_linux_amd64.tar.gz | sudo tar -xz -C /usr/local/bin wvl
```

**After install**

```sh
wvl setup                           # First-time setup (once only)
wvl "Hello, tell me about yourself"  # Start using
```

> Supports macOS / Linux AMD64 & ARM64. Build from source, update, alternative install paths, etc. — see [`install.md`](./install.en.md).

---

## What the Agent Can Do

Waveloom has the following built-in tools that the agent invokes autonomously:

| Tool | Capability |
|------|------------|
| `read_file` | Read file contents |
| `write_file` | Create or overwrite files |
| `edit_file` | Exact string-based find-and-replace in files |
| `grep` | Search codebase for matching lines |
| `search_file` | Find files by name pattern |
| `ls` | List directory contents |
| `shell` | Execute arbitrary shell commands |
| `web_fetch` | Fetch online docs, API references |
| `lsp_diagnostic` | Get compile errors and lint hints |
| `lsp_definition` | Jump to symbol definition |
| `lsp_references` | Find all references to a symbol |
| `lsp_hover` | Get symbol type signature and documentation |

> **LSP Prerequisites**: LSP tools require the corresponding language server available in PATH. For Go projects, install [gopls](https://pkg.go.dev/golang.org/x/tools/gopls) (`go install golang.org/x/tools/gopls@latest`). The agent automatically starts the LSP server on first LSP tool invocation.

Typical use cases: writing unit tests, refactoring a module, debugging an issue, explaining design intent behind a piece of code, adding new features.

---

## Usage

```sh
wvl                      # Interactive TUI mode
wvl "explain the design of pkg/llm/client.go"  # One-shot
wvl ls                   # List recent sessions
wvl --continue           # Resume the most recent session
wvl --resume <id>        # Resume a specific session
```

In interactive mode: Enter to send, Esc to interrupt, `Ctrl+T` to expand/collapse thought, `Ctrl+O` for tool output, `Ctrl+G` to toggle theme. Type `@` for a fuzzy file picker. See [`usage.md`](./usage.en.md) for details.

---

## Permission & Safety

Before the agent performs a write operation or shell command, it goes through a permission check. Each tool invocation results in one of three decisions:

- **Allow**: Pass through directly (read-only operations are allowed by default)
- **Deny**: Hard block (e.g., `rm -rf /`)
- **Ask**: Show a confirmation dialog for you to decide

<p align="center">
  <img src="./permission.png" alt="Permission confirmation dialog" width="560"/>
</p>

Configure permission rules in `settings.json` (file location: `~/.waveloom/settings.json` or project root `.waveloom/settings.json`):

```json
{
  "permissions": {
    "allow": ["read_file", "search_file", "grep", "ls"],
    "deny":  ["shell(rm -rf /*)"],
    "ask":   ["write_file", "edit_file"]
  }
}
```

Rule format: `ToolName` or `ToolName(pattern)`, e.g., `shell(git *)` matches all commands starting with `git `.

For CI / automation scenarios, use `--bypass-permissions` to skip all checks.

---

## Configuration

### settings.json

On first run, Waveloom generates a default config at `.waveloom/settings.json`. The minimal config only requires `api_key`:

```json
{
  "llm": {
    "api_key": "sk-your-deepseek-key"
  }
}
```

Full `llm` configuration options (all have defaults, override as needed):

| Field | Description | Default |
|-------|-------------|---------|
| `api_key` | DeepSeek API Key, falls back to `LLM_API_KEY` env var when empty | — |
| `provider` | `deepseek` or `openai` | `deepseek` |
| `model` | Model name | `deepseek-v4-pro` |
| `base_url` | API endpoint | `https://api.deepseek.com` |
| `timeout` | Request timeout | `600s` |
| `extra_params` | Extra parameters (thinking, reasoning_effort, etc.) | Thinking mode on by default |

Priority: **CLI flags > `.waveloom/settings.json` (project) > `~/.waveloom/settings.json` (global)**

### Environment Tool Configuration

The agent auto-detects available toolchains at startup. For tools not in PATH or to pin a specific version, configure via `environment.tools`. See [`environment.en.md`](./environment.en.md) for details.

### CLI Flags

| Flag | Description |
|------|-------------|
| `--model` | Model name |
| `--system-prompt` | Custom system prompt |
| `--max-turns N` | Maximum turns, 0 = unlimited |
| `--context-limit 1M` | Context window size, supports `1M` / `200k` / raw number |
| `--theme auto/dark/light` | Theme, auto detects terminal background |
| `--verbose` | Log detailed output to `.waveloom/wvl.log` |
| `--bypass-permissions` | Skip all permission checks |
| `--resume ID` | Resume a specific session |
| `--continue` | Resume the most recent session |
| `--settings PATH` | Specify config file path |
| `--version` | Show version |

---

## Context Management & Prefix Caching

DeepSeek's prefix cache compares requests from `messages[0]` onward to find the longest common prefix — cache-hit price is just **1/50 ~ 1/120** of cache-miss. Waveloom optimizes for this with a fixed System Prompt anchor, turn-accumulated message history, and four-tier watermark compaction (Snip → Prune → Summarize → Hard cutoff) that never mutates compacted bytes, achieving **95–99%** cache hit rates.

```mermaid
flowchart LR
    t0["Tier 0<br/>idle<br/>&lt; 60%"]
    t1["Tier 1 · Snip<br/>tool output truncation<br/>60-80%"]
    t2["Tier 2 · Prune<br/>clear reasoning<br/>80-95%"]
    t3["Tier 3 · Summarize<br/>LLM incremental summary<br/>≥ 95%"]
    stop["Hard limit<br/>block further LLM calls<br/>≥ 98%"]

    t0 --> t1 --> t2 --> t3 --> stop

    style t0 fill:#2d8,stroke:#333,color:#fff
    style t1 fill:#5b5,stroke:#333,color:#fff
    style t2 fill:#da5,stroke:#333,color:#000
    style t3 fill:#e73,stroke:#333,color:#fff
    style stop fill:#c22,stroke:#333,color:#fff
```

See [`prefix-cache.en.md`](./prefix-cache.en.md) for details.

---

## Troubleshooting

Common install, config, and usage issues — see [`faq.en.md`](./faq.en.md).

---

## Development

```sh
make build       # Build → bin/wvl
make install     # Install → $HOME/go/bin/wvl
make test        # Test
```

```
waveloom/
├── cmd/waveloom/          # Entry point + TUI
├── pkg/
│   ├── agentloop/         # Think-Act-Observe loop
│   ├── context/           # Context accumulation + four-tier watermark compaction
│   ├── llm/               # LLM API client
│   ├── memory/            # AGENTS.md hierarchical loading
│   ├── permission/        # Permission gatekeeper
│   ├── reference/         # @ file reference expansion
│   └── tool/              # Built-in tools
├── specs/                 # Component design specs
├── docs/                  # Documentation
└── Makefile
```

---

Apache License 2.0
