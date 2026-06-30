# Changelog

## [v0.1.0-alpha.6] — 2026-06-30

### Added
- **Skill system**: Claude Code Skill format compatible — auto-loads existing skills from `~/.claude/skills/` with zero migration
- **Skill whitelist & conditional activation**: `allowed-tools` Bash command whitelist, `paths` conditional activation (gitignore-style glob), Guard permission integration
- **AskUserQuestion**: LLM-initiated single/multi-select, Other custom input, and decline interaction, TUI overlay rendering
- **edit_file whitespace normalization**: auto-fix whitespace differences on unique match, reducing LLM retry rounds
- **edit_file whitespace fallback**: no_match diagnostic enhancement, relaxed whitespace matching fallback

### Fixed
- `--resume` restore losing tool_calls Name/Arguments during deserialization
- Session restore empty-response guard, enhanced deserialization integrity checks
- web_fetch HTML entity decoding, missing Content-Type tolerance, timeout partial content return
- Tool error state expand/collapse rendering fix, ToolError fallback when ToolResult is empty
- Reasoning gap between system prompt and tool descriptions eliminated
- macOS/Linux symlink deviation causing path misjudgment in IsWithinDir

### Changed
- TUI input horizontal scrolling refactored to syncInputVisibleStart

## [v0.1.0-alpha.5] — 2026-06-29

### Added
- **Shell completions**: `waveloom completion <bash|zsh|fish>` generates shell completion scripts
- **Homebrew support**: `brew install Menfre01/tap/waveloom`

### Changed
- Binary renamed `wvl` → `waveloom`, Go module path migrated to `github.com/Menfre01/waveloom`
- Log file `.waveloom/wvl.log` → `.waveloom/waveloom.log`

### Chore
- New `release.yml`: tag-triggered cross-compile, GitHub Release creation, Homebrew formula sync
- New `ci.yml`: push/PR build / test / lint / cross-compile
- New community files: CODEOWNERS, PR template, SECURITY, CONTRIBUTING, CHANGELOG, NOTICE
- Bilingual docs for CONTRIBUTING / SECURITY / CHANGELOG
- Issue template overhaul (bug report / feature request)
- Removed CLAUDE.md (superseded by AGENTS.md)

## [v0.1.0-alpha.4] — 2025-07-09

### Added
- **Slash command system**: type `/` to open command picker, supports /new /model /theme /help, ↑↓ navigation, Enter to confirm, Tab to autocomplete
- **ToolTimeout protection**: configurable single-tool execution timeout (CLI `--tool-timeout` / settings.json `tool_timeout`), prevents tools from blocking indefinitely

### Fixed
- diff_view now strictly follows POSIX/GNU unified diff spec
- HUD footer color threshold adjustments (elap/cache indicators)

### Changed
- Extracted `pathutil` package for unified path safety logic
- LSP Client dependency injection refactor
- LLM interaction text translated from Chinese to English (Schema / Description / error messages / placeholders) to improve DeepSeek prefix cache hit rate

## [v0.1.0-alpha.3] — 2025-07-02

### Added
- **AGENTS.md @ reference expansion**: mirrors Claude Code sub-file splitting, supports `@path/to/file` external file references, auto-expanded, merged, and deduplicated
- **Three-level truncation**: tool result truncation strategy upgrade (lines→total chars→single-line length), code fence long-line protection

### Fixed
- Hunk merging and cross-hunk line offset correction in `replace_all` scenarios
- DiffAdd line numbers now use NewNum, fixing incremental line number display errors

### Changed
- TUI notification text streamlined, footer layout adjusted (latency/balance order swapped)

### Other
- Footer now shows elap latency display
- Install path changed from `/usr/local/bin` to `~/.local/bin`, no sudo required

## [v0.1.0-alpha.2] — 2025-06-27

### Added
- **Tab/Enter focus interaction**: replaces Ctrl+O/Ctrl+T; Tab navigates between interactive paragraphs, Enter expands/collapses

### Fixed
- Collapse preview and expanded view now truncate by wrapped line count, preventing ultra-long single lines from filling the viewport

## [v0.1.0-alpha.1] — 2025-06-20

### Added
- `--model` CLI flag to override config file model selection
- TUI supports `--max-turns` and `--bypass-permissions` flags

## [v0.0.3] — 2025-06-15

### Added
- **Session management**: transcript replay, recent.json session log, `--continue` and `ls` commands
- **setup subcommand**: first-time configuration wizard to guide API Key entry
- **Default model switch**: deepseek-v4-pro as the default model
- `--version` flag, unified version injection

### Fixed
- IME input ghosting fix
- Session hang during tool execution and detection dead loop fix
- Missing compaction stats when no tool calls fix

### Changed
- Removed viewport component, switched to manual scroll control
