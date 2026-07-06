<p align="center">
  <strong>English</strong>
  &nbsp;·&nbsp;
  <a href="./docs/README.zh-CN.md">简体中文</a>
</p>

<p align="center">
  <img src="./assets/logo.svg" alt="Waveloom" width="360"/>
</p>

<p align="center">
  <a href="https://github.com/Menfre01/waveloom/releases/latest"><img src="https://img.shields.io/github/v/release/Menfre01/waveloom?style=flat-square&color=00ADD8&labelColor=161b22" alt="release"/></a>
  <a href="https://github.com/Menfre01/waveloom/actions/workflows/ci.yml"><img src="https://github.com/Menfre01/waveloom/actions/workflows/ci.yml/badge.svg?style=flat-square&labelColor=161b22" alt="CI"/></a>
  <a href="https://github.com/Menfre01/waveloom/releases"><img src="https://img.shields.io/github/downloads/Menfre01/waveloom/total?style=flat-square&color=00ADD8&label=GitHub%20downloads&labelColor=161b22" alt="downloads"/></a>
  <a href="https://go.dev"><img src="https://img.shields.io/badge/language-Go-00ADD8?style=flat-square&logo=go&logoColor=white&labelColor=161b22" alt="Go"/></a>
  <a href="https://platform.deepseek.com"><img src="https://img.shields.io/badge/DeepSeek-native-4D6BFE?style=flat-square&labelColor=161b22" alt="DeepSeek"/></a>
  <a href="./LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-8b949e?style=flat-square&labelColor=161b22" alt="license"/></a>
</p>

---

**The most polished terminal Code Agent for DeepSeek.** Claude Code-level TUI — streaming reasoning, rich diff, permission dialogs, `@` file picker, `/` command palette — combined with architecture-level prefix cache optimization. Your `.claude/skills/` work out of the box. DeepSeek charges up to 120× more for cache misses than hits — Waveloom keeps the longest common prefix cache-hot across turns.

**curl one-liner (macOS / Linux)**

```sh
curl -fsSL https://raw.githubusercontent.com/Menfre01/waveloom/main/install.sh | sh
```

**Homebrew**

```sh
brew trust menfre01/tap
brew install Menfre01/tap/waveloom
```

> Supports macOS / Linux / Windows, AMD64 & ARM64. Installs to `~/.local/bin`, no sudo needed.

**Windows** requires [Git for Windows](https://git-scm.com/downloads/win). Open PowerShell and run:

```powershell
powershell -c "irm https://raw.githubusercontent.com/Menfre01/waveloom/main/install.ps1 | iex"
```

> [!TIP]
> **For the best experience on Windows, use [WSL2](https://learn.microsoft.com/en-us/windows/wsl/install).** Install the Linux binary inside WSL2 and enjoy native performance — no Git Bash forwarding layer, smoother terminal rendering, and faster shell commands.
>
> Prefer Git Bash? Waveloom requires `bash.exe` — cmd and PowerShell are not supported. After installation, **open Git Bash** and run the commands below. If `waveloom` is not found, add `%USERPROFILE%\.local\bin` to your Windows PATH (the installer handles this automatically).

Then configure your key and start:

```sh
waveloom setup
waveloom
```

> [!IMPORTANT]
> API key connects directly to DeepSeek / OpenAI — your code never passes through a third-party server. Every file write and command execution requires your confirmation.

<p align="center">
  <img src="./assets/demo.en.gif" alt="Waveloom Demo" width="900"/>
</p>

---

## How does it compare?

| | Waveloom | Claude Code | Reasonix |
|---|---|---|---|
| Skill format | Drop-in: `.claude/skills/` SKILL.md, 9/15 frontmatter fields (`$ARGUMENTS`, `paths`, `` !`cmd` `` injection, etc.) | Native SKILL.md + commands | 6/15 fields, no variable substitution in skills (commands only) |
| Cache design | DeepSeek prefix matching: 4-tier watermark (Snip → Prune → Summarize), compaction bytes never change | Anthropic `cache_control`: `cache_edits` API, dynamic system prompt sections | DeepSeek prefix matching: 4-tier (notice → snip → compact → force), `session.Replace()` bumps rewrite version |
| Compaction | Monotonic — `compactionDecisionSet` + dual cursor, each message compacted once | Per-turn independent, no durability guarantee | Prefix bytes preserved across compact, but no per-message decision tracking |
| Plan mode | Guard restricts writes to plan file only; build tools auto-allowed | Full write block at permission layer; rich exit UI | `planmode.Policy` with trust gates for bash/MCP; Marker string injected; no plan file |
| Sub-agents | Fork (inherits context) / Cold (filtered tools) / Explore (read-only) | Fork + Cold + In-process + Coordinator (tmux spawn) | `task` tool with nested agent, background via job manager |
| Runtime | Go binary ~18MB, zero deps | Node.js | Go binary + Desktop app, external plugin host |
| Permission | 8-step pipeline, 3-tier command safety (RiskNone/RiskLow/RiskHigh) | 8-source rule merge + LLM classifier auto-approval | Policy + Approver, 9-stage execute pipeline, shellsafe readOnly detect |
| TUI polish | Streaming reasoning, rich diff, permission dialogs, `@` fuzzy picker, `/` palette, i18n, theme toggle — Claude Code parity | Native TUI (Ink/React), gold standard | Basic TUI, functional but no diff/syntax highlight polish |

**Choose Waveloom if**: you use DeepSeek, have `.claude/skills/`, want Premium terminal UX without the cache miss cost.  
**Choose Claude Code if**: you use Anthropic, need MCP + coordinator mode, deep in the Claude ecosystem.  
**Choose Reasonix if**: you want a desktop GUI, bot integrations (Feishu/WeChat/QQ), or LSP integration.

---

## Why TUI

**Waveloom is the only DeepSeek-native agent with Claude Code-level terminal polish.** Streaming reasoning with syntax highlighting, rich diff, permission dialogs, `@` fuzzy file picker, `/` command palette, light/dark theme toggle, zh-CN / en-US i18n. Most DeepSeek agents treat the TUI as an afterthought — raw text streaming, no interaction design. Fire it up and feel the difference.

---

## Highlights

- **Prefix cache optimized** — Fixed System Prompt, append-only message history, four-tier watermark compaction. Maximum common prefix stays cache-hot across turns.
- **Permission safety** — Three-tier decisions (allow / deny / ask) with pattern-matching rule engine. Every write operation requires your confirmation.
- **Session persistence** — Close the terminal, come back days later with `waveloom --continue`. The agent remembers all prior context.
- **Plan Mode** — Two-stage workflow: explore & design first, implement after approval. `Shift+Tab` to enter/exit, Guard-enforced write protection.
- **10 built-in tools** — `read_file` / `write_file` / `edit_file` / `shell` / `web_fetch` / `ask_user_question` / `enter_plan_mode` / `exit_plan_mode` / `skill` / `agent`.
- **i18n multilingual** — Full zh-CN / en-US bilingual UI. `--locale` CLI flag, `/locale` command, auto-detect from LANG.

---

## FAQ

**Q: How do I switch models?**  
Type `/model` in interactive mode, or `waveloom --model deepseek-v4-flash`.

**Q: Is my API key safe?**  
Stored locally at `~/.waveloom/`. Keys connect directly to DeepSeek / OpenAI — no third-party relay.

**Q: How do I switch languages?**  
Type `/locale` to toggle between Chinese and English, or `waveloom --locale zh-CN`. The setting persists automatically in `settings.json`.

**Q: What languages are supported?**  
Waveloom works with any text-based project. Code verification uses each language's native build tools (`go build`, `npx tsc`, `cargo build`, `make`, etc.) — no LSP server required.

---

## Docs

| Document | Content |
|----------|---------|
| [`usage`](./docs/usage.en.md) | Interactive mode, shortcuts, Skill system |
| [`install`](./docs/install.en.md) | Homebrew / curl / source / shell completions |
| [`settings`](./docs/settings.en.md) | API key, model, timeout, compaction |
| [`prefix-cache`](./docs/prefix-cache.en.md) | DeepSeek caching, four-tier compaction |
| [`environment`](./docs/environment.en.md) | Toolchain probing |
| [`faq`](./docs/faq.en.md) | Frequently asked questions |

---

## Development

Go 1.25+, `make build` / `make test`. See [`CONTRIBUTING.md`](./CONTRIBUTING.md) for project structure and contribution guide.

---

Apache License 2.0 © 2026 Waveloom Contributors
