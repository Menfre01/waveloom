<p align="right"><a href="../README.md">中文版</a></p>

<p align="center">
  <img src="./logo.svg" alt="Waveloom" width="420"/>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/language-Go-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go"/>
  <img src="https://img.shields.io/badge/DeepSeek-native-4D6BFE?style=flat-square" alt="DeepSeek"/>
  <img src="https://img.shields.io/badge/license-Apache%202.0-8b949e?style=flat-square" alt="license"/>
  <img src="https://img.shields.io/badge/TUI-Bubble%20Tea-5fafd7?style=flat-square" alt="Bubble Tea"/>
  <img src="https://img.shields.io/badge/status-alpha-d4a76a?style=flat-square" alt="alpha"/>
</p>

---

**Waveloom** is a pure-Go terminal coding agent. You tell it what to do in natural language, and it reads code, analyzes logic, edits files, and executes commands — right in your terminal. You observe, review, and step in to make decisions when needed.

It's not a chatbot — it actually operates on your filesystem, runs commands, and modifies code. Every write and every command execution requires your consent first.

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

Typical use cases: writing unit tests, refactoring a module, debugging an issue, explaining design intent behind a piece of code, adding new features.

---

## Install

Requires: [DeepSeek API Key](https://platform.deepseek.com/api_keys).

### Pre-built Binary (Recommended)

No Go required. Grab the right binary from [Releases](https://github.com/Menfre01/waveloom/releases/latest).

```sh
# macOS (ARM64 — Apple Silicon)
curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/wvl_darwin_arm64.tar.gz | tar -xz -C /usr/local/bin wvl

# macOS (AMD64 — Intel)
curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/wvl_darwin_amd64.tar.gz | tar -xz -C /usr/local/bin wvl

# Linux (AMD64)
curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/wvl_linux_amd64.tar.gz | tar -xz -C /usr/local/bin wvl

# Linux (ARM64)
curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/wvl_linux_arm64.tar.gz | tar -xz -C /usr/local/bin wvl
```

> No write permission for `/usr/local/bin`? Install to `~/.local/bin`:
> ```sh
> mkdir -p ~/.local/bin
> curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/wvl_darwin_arm64.tar.gz | tar -xz -C ~/.local/bin wvl
> export PATH="$HOME/.local/bin:$PATH"  # add to ~/.bashrc or ~/.zshrc
> ```
>
> macOS Gatekeeper? Allow it with:
> ```sh
> xattr -d com.apple.quarantine /usr/local/bin/wvl
> ```

### Build from Source

Prerequisites: **Go 1.25+**.

```sh
git clone https://github.com/Menfre01/waveloom.git
cd waveloom && make install
# wvl is installed to $HOME/go/bin — make sure it's on PATH:
export PATH=$HOME/go/bin:$PATH
```

### First-time Setup

```sh
# Interactive guide (once only)
wvl setup
# → Choose Provider → Enter API Key → Choose Model → Done

# Or skip config entirely with an env var:
LLM_API_KEY=sk-... wvl
```

> **One-shot mode**: `wvl "write unit tests for the HTTP server"`

---

## Usage

### Interactive Mode

```sh
wvl
```

Once in the TUI, type like a chat and press Enter to send. The agent autonomously invokes tools to read files, search code, edit, and run tests.

<p align="center">
  <img src="./tui.png" alt="Waveloom screenshot" width="720"/>
</p>

The prefix character at the beginning of each line tells you **who is speaking**:

| Prefix | Role | Meaning |
|--------|------|---------|
| `›` | You | Your message, in blue |
| `·` / spinner | Assistant | AI reply, in green, Markdown rendered |
| `·` / spinner | Thought | AI's reasoning, in gray, collapsed to one line when done (`Ctrl+T` to expand) |
| `•` / spinner | Tool | AI's actions (read, write, run), green = success / red = failure |

**Keyboard shortcuts**:

| Key | Action |
|-----|--------|
| `Enter` | Send message |
| `Esc` | Interrupt running agent |
| `↑` `↓` / `PgUp` `PgDn` | Scroll conversation history |
| `Ctrl+E` / `End` | Jump to bottom |
| `Ctrl+T` | Expand/collapse the most recent thought |
| `Ctrl+O` | Expand/collapse the most recent tool output |
| `Ctrl+G` | Toggle theme (dark / light / auto) |
| `Ctrl+V` | Paste |
| `Ctrl+C` | Quit |

The **footer status bar** shows: current model, context usage (progress bar), cache hit rate, loop count, latency, balance.

### One-shot

```sh
wvl "explain the design of pkg/llm/client.go"
wvl --model deepseek-v4-flash "write unit tests for UserService"
echo "review the code under pkg/llm/" | wvl
```

### @ File References

Type `@` in the input to open a fuzzy file picker (prefix > substring matching). `Tab` enters subdirectories. Selected file contents are automatically injected into the message context.

```
help me optimize the error handling in @pkg/auth/login.go
```

---

## Permission & Safety

Before the agent performs a write operation or shell command, it goes through a permission check. Each tool invocation results in one of three decisions:

- **Allow**: Pass through directly (read-only operations are allowed by default)
- **Deny**: Hard block (e.g., `rm -rf /`)
- **Ask**: Show a confirmation dialog for you to decide

<p align="center">
  <img src="./permission.png" alt="Permission confirmation dialog" width="560"/>
</p>

Configure permission rules in `settings.json`:

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
| `model` | Model name | `deepseek-v4-flash` |
| `base_url` | API endpoint | `https://api.deepseek.com` |
| `timeout` | Request timeout | `600s` |
| `extra_params` | Extra parameters (thinking, reasoning_effort, etc.) | Thinking mode on by default |

Priority: **CLI flags > `.waveloom/settings.json` (project) > `~/.waveloom/settings.json` (global)**

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
| `--settings PATH` | Specify config file path |

---

## Design Highlights

Waveloom is deeply optimized for long conversations — it uses a four-tier watermark compaction strategy to automatically manage context, preserving critical information while preventing context window overflows. Once compacted, the byte content remains stable, so DeepSeek's prefix cache continues to hit, keeping API costs under control.

> See [`specs/compaction.md`](../specs/compaction.md) — complete design of context compaction.

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
