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

**A DeepSeek-native terminal code agent engineered for cache economics.** Prefix-cache architecture keeps the longest common prefix cache-hot across turns; LLM auto-selects pro for deep reasoning and flash for routine tasks — maximizing cache hits and minimizing token cost. Premium TUI with `.claude/skills/` and `.claude.json` MCP configs drop in — zero-friction onboarding. One Go binary.

**DeepSeek 原生终端编码代理，围绕缓存经济学设计。** 前缀缓存架构让最长公共前缀跨轮次持续命中；LLM 自动按任务选模型——pro 做深度推理，flash 处理常规任务——最大化缓存命中，最小化 token 成本。专业级 TUI，`.claude/skills/` 和 `.claude.json` MCP 配置开箱兼容，零摩擦迁移。单一 Go 二进制。

<p align="center">
  <img src="./assets/demo.en.gif" alt="Waveloom Demo" width="900"/>
</p>

---

## Quick Start

### macOS / Linux

```sh
curl -fsSL https://raw.githubusercontent.com/Menfre01/waveloom/main/install.sh | sh
```

Or via Homebrew:

```sh
brew install menfre01/tap/waveloom
```

> Supports macOS / Linux / Windows, AMD64 & ARM64. Installs to `~/.local/bin`, no sudo needed.

### Windows

Requires [Git for Windows](https://git-scm.com/downloads/win). Open PowerShell and run:

```powershell
powershell -c "irm https://raw.githubusercontent.com/Menfre01/waveloom/main/install.ps1 | iex"
```

> [!TIP]
> **For the best experience on Windows, use [WSL2](https://learn.microsoft.com/en-us/windows/wsl/install).** Install the Linux binary inside WSL2 and enjoy native performance — no Git Bash forwarding layer, smoother terminal rendering, and faster shell commands.
>
> Prefer Git Bash? Waveloom requires `bash.exe` — cmd and PowerShell are not supported. After installation, **open Git Bash** and run the commands below. If `waveloom` is not found, add `%USERPROFILE%\.local\bin` to your Windows PATH (the installer handles this automatically).

### Configure

```sh
waveloom setup
waveloom
```

> [!IMPORTANT]
> API key connects directly to DeepSeek / Kimi / OpenAI — your code never passes through a third-party server. Every file write and command execution requires your confirmation.

---

## How does it compare?

| | Waveloom | Claude Code | Reasonix |
|---|---|---|---|
| Skill/Plugin | Drop-in: `.claude/skills/` SKILL.md + `.claude/plugins/` installed plugins, 9 frontmatter fields (`$ARGUMENTS`, `paths`, `` !`cmd` `` injection, etc.) | Native SKILL.md + commands + plugin system | 13 frontmatter fields, no variable substitution in skill bodies |
| Cache design | DeepSeek prefix matching: 4-tier watermark (Snip → Prune → Summarize), compaction bytes never change | Anthropic `cache_control`: `cache_edits` API, dynamic system prompt sections | DeepSeek prefix matching: 4-tier (notice → snip → compact → force), `session.Replace()` bumps rewrite version |
| Compaction | Monotonic — `compactionDecisionSet` + triple cursor, each message compacted once | Per-turn independent, no durability guarantee | Prefix bytes preserved across compact, but no per-message decision tracking |
| Plan mode | Guard restricts writes to plan file only; build tools auto-allowed | Write restricted to plan file only; rich exit UI | `planmode.Policy` with trust gates for bash/MCP; Marker string injected; no plan file |
| Sub-agents | Fork (inherits context) / Cold: Evaluate (code review) • Explore (read-only) • Verification (adversarial) • Advisor (deep analysis) | Fork + Cold + In-process + Coordinator | `task` tool with nested agent, background via job manager |
| Runtime | Go binary ~20MB, zero deps | Node.js | Go binary + Desktop app, external plugin host |
| MCP | Full client (config, transport, tool proxy), registered alongside built-in tools | Native MCP support | Native MCP support |
| Permission | 7-step pipeline, 4-tier command safety (RiskNone/RiskLow/RiskMedium/RiskHigh) | 8-source rule merge + LLM classifier auto-approval | Policy + Approver, 9-stage execute pipeline, shellsafe readOnly detect |
| TUI polish | Streaming reasoning, rich diff, permission dialogs, `@` fuzzy picker, `/` palette, i18n, theme toggle — premium terminal UX | Native TUI (Ink/React), gold standard | Functional TUI, different UX paradigm |

**Choose Waveloom if**: you want premium terminal UX with multi-provider support (DeepSeek / Kimi / OpenAI), `.claude/skills/` + `.claude/plugins/` drop-in, without the cache miss cost.  
**Choose Claude Code if**: you use Anthropic, need coordinator mode, deep in the Claude ecosystem.  
**Choose Reasonix if**: you want a desktop GUI, QQ Bot integration, or a larger community ecosystem.

---

## Why TUI

**Waveloom is the only DeepSeek-native agent with premium terminal polish.** Streaming reasoning with syntax highlighting, rich diff, permission dialogs, `@` fuzzy file picker, `/` command palette, light/dark/color-blind theme toggle, `?` shortcut help, zh-CN / en-US i18n. Most DeepSeek agents treat the TUI as an afterthought — raw text streaming, no interaction design. Fire it up and feel the difference.

---

## Highlights

- **Prefix cache optimized** — Fixed System Prompt, append-only message history, four-tier watermark compaction. Maximum common prefix stays cache-hot across turns.
- **Permission safety** — Three-tier decisions (allow / deny / ask) with pattern-matching rule engine. Every write operation requires your confirmation.
- **Session persistence** — Close the terminal, come back days later with `waveloom --continue`. The agent remembers all prior context.
- **Checkpoint/Rewind** — Rewind to any previous message with full file state restoration. Fork mode preserves original session intact — history never lost.
- **Plan Mode** — Two-stage workflow: explore & design first, implement after approval. `Shift+Tab` to enter/exit, Guard-enforced write protection.
- **Advisor Mode** — Price-optimized dual-model routing: flash handles routine coding, pro auto-activates for plan mode and code review. Enable with `"mode": "advisor"` in settings.
- **14 built-in tools** — `read` / `write` / `edit` / `bash` / `web_fetch` / `web_search` / `ask_user_question` / `enter_plan_mode` / `exit_plan_mode` / `skill` / `agent` / `kill_background_task` / `todo_create` / `todo_update`.
- **i18n multilingual** — Full zh-CN / en-US bilingual UI. `--locale` CLI flag, `/locale` command, auto-detect from LANG.

---

## FAQ

**Q: How do I switch models?**  
Type `/model` in interactive mode, or `waveloom --model deepseek-v4-flash`.

**Q: How do I switch LLM providers?**  
Type `/provider` to list available providers (DeepSeek, Kimi, OpenAI), or `/provider kimi` to switch. Profiles are configured in `settings.json` under `llm.profiles`.

**Q: Is my API key safe?**  
Stored locally at `~/.waveloom/`. Keys connect directly to DeepSeek / Kimi / OpenAI — no third-party relay.

**Q: How do I switch languages?**  
Type `/locale` to toggle between Chinese and English, or `waveloom --locale zh-CN`. The setting persists automatically in `settings.json`.

**Q: How do I enable advisor mode?**  
Add `"mode": "advisor"` to the `llm` section in `settings.json`. In advisor mode, the agent uses the secondary model (`sub_model`, e.g. `deepseek-v4-flash`) for routine coding and auto-switches to the primary model (`model`, e.g. `deepseek-v4-pro`) inside plan mode and for code review — cutting token costs by ~50%.

**Q: What languages are supported?**  
Waveloom works with any text-based project. Code verification uses each language's native build tools (`go build`, `npx tsc`, `cargo build`, `make`, etc.) — no LSP server required.

---

## Docs

| Document | Content |
|----------|---------|
| [`usage`](./docs/usage.en.md) | Interactive mode, shortcuts, Skill system |
| [`install`](./docs/install.en.md) | Homebrew / curl / source / shell completions |
| [`settings`](./docs/settings.en.md) | API key, model, timeout, compaction, advisor mode |
| [`prefix-cache`](./docs/prefix-cache.en.md) | DeepSeek caching, four-tier compaction |
| [`environment`](./docs/environment.en.md) | Toolchain probing |
| [`mcp`](./docs/mcp.en.md) | MCP client, config sources, CLI management |
| [`faq`](./docs/faq.en.md) | Frequently asked questions |

---

## Development

Go 1.25+, `make build` / `make test`. See [`CONTRIBUTING.md`](./CONTRIBUTING.md) for project structure and contribution guide.

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) (TUI framework),
[Glamour](https://github.com/charmbracelet/glamour) (Markdown rendering),
and [Lip Gloss](https://github.com/charmbracelet/lipgloss) (terminal styling) — part of the [Charm](https://charm.sh) ecosystem.


## Recommended Tools

These third-party tools are not required but significantly improve the Waveloom experience:

| Tool | Description |
|------|-------------|
| [ripgrep](https://github.com/BurntSushi/ripgrep) (`rg`) | High-performance recursive grep, ~10x faster than `grep -r`. Waveloom prefers `rg` over `grep` for all code searches when available. |
| [RTK](https://github.com/rtk-ai/rtk) (`rtk`) | Token-optimized CLI proxy — rewrites common commands to compact equivalents, saving 60–90% input tokens. Waveloom loads it via `~/.claude/hooks/rtk-rewrite.sh`. |

---

Apache License 2.0 © 2026 Waveloom Contributors
