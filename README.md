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

**A terminal Code Agent purpose-built for DeepSeek prefix caching.** Feels like Claude Code — your existing Skills work out of the box. DeepSeek charges up to 120× more for cache misses than hits. Waveloom keeps the System Prompt and message prefix stable at the architecture level, so the longest common prefix stays cache-hot.

**Homebrew (recommended)**

```sh
brew trust menfre01/tap
brew install Menfre01/tap/waveloom
```

**curl one-liner**

```sh
curl -fsSL https://raw.githubusercontent.com/Menfre01/waveloom/main/install.sh | sh
```

> Supports macOS / Linux, AMD64 & ARM64. Installs to `~/.local/bin`, no sudo needed.

Then configure your key and start:

```sh
waveloom setup
waveloom
```

> [!IMPORTANT]
> API key connects directly to DeepSeek / OpenAI — your code never passes through a third-party server. Every file write and command execution requires your confirmation.

<p align="center">
  <img src="./assets/demo.gif" alt="Waveloom Demo" width="900"/>
</p>

---

## How does it compare to Claude Code?

| | Waveloom | Claude Code |
|---|---|---|
| Cache design | Built for DeepSeek prefix matching: fixed System Prompt, append-only, in-place compaction | Built for Anthropic `cache_control`: dynamic System Prompt sections, compaction replaces messages |
| Context compaction | In-place, prefix-stable | Replaces messages with summary |
| Runtime | Single binary ~17MB | Node.js |

**Choose Waveloom if**: you use DeepSeek, care about API costs, have Claude Code Skills, need a zero-dependency binary  
**Choose Claude Code if**: you use Anthropic, need MCP, are deep in the Claude ecosystem

---

## Highlights

- **Prefix cache optimized** — Fixed System Prompt, append-only message history, four-tier watermark compaction. Maximum common prefix stays cache-hot across turns.
- **Native LSP integration** — Agent proactively calls `lsp_diagnostic` / `lsp_definition` / `lsp_references` / `lsp_hover` to understand your codebase.
- **Permission safety** — Three-tier decisions (allow / deny / ask) with pattern-matching rule engine. Every write operation requires your confirmation.
- **Session persistence** — Close the terminal, come back days later with `waveloom --continue`. The agent remembers all prior context.
- **Plan Mode** — Two-stage workflow: explore & design first, implement after approval. `Shift+Tab` to enter/exit, Guard-enforced write protection, `[plan:start]/[plan:end]` message pairing for prefix-cache-safe context.
- **16 built-in tools** — `read_file` / `edit_file` / `grep` / `shell` / `web_fetch` / `ask_user_question` / `enter_plan_mode` / `exit_plan_mode` / `skill` / LSP tools — invoked autonomously by the agent.
- **i18n multilingual** — Full zh-CN / en-US bilingual UI. `--locale` CLI flag, `/locale` command, `settings.json` persistence, auto-detect from LANG env var.
- **TUI interactions** — `@` file references / `@` fuzzy file picker / `/` command palette / `/locale` switch language / `Tab` paragraph navigation / `Shift+Tab` Plan Mode / `Ctrl+G` theme toggle

---

## FAQ

**Q: How do I switch models?**  
Type `/model` in interactive mode, or `waveloom --model deepseek-v4-flash`.

**Q: Is my API key safe?**  
Stored locally at `~/.waveloom/`. Keys connect directly to DeepSeek / OpenAI — no third-party relay.

**Q: How do I switch languages?**  
Type `/locale` to toggle between Chinese and English, or `waveloom --locale zh-CN`. The setting persists automatically in `settings.json`.

**Q: What languages are supported?**  
LSP-native Go support (built-in gopls integration). Any language with an LSP server works. Plain-text projects can use `read_file` / `edit_file` / `grep` without LSP.

---

## Docs

| Document | Content |
|----------|---------|
| [`usage`](./docs/usage.en.md) | Interactive mode, shortcuts, Skill system |
| [`install`](./docs/install.en.md) | Homebrew / curl / source / shell completions |
| [`settings`](./docs/settings.en.md) | API key, model, timeout, compaction |
| [`prefix-cache`](./docs/prefix-cache.en.md) | DeepSeek caching, four-tier compaction |
| [`environment`](./docs/environment.en.md) | LSP server, toolchain probing |
| [`faq`](./docs/faq.en.md) | Frequently asked questions |

---

## Development

Go 1.25+, `make build` / `make test`. See [`CONTRIBUTING.md`](./CONTRIBUTING.md) for project structure and contribution guide.

---

Apache License 2.0 © 2026 Waveloom Contributors
