# Changelog

## [v0.1.0-alpha.12] — 2026-07-04

### Added
- **Multi-line input**: Input area upgraded from single-line textinput to multi-line textarea, fixed at 2 lines height with automatic word wrapping; first line shows `›` prefix, subsequent lines use aligned indentation; native terminal real cursor replaces ANSI virtual cursor; layout dynamically calculates input height to prevent HUD挤压
- **Windows platform support**: Full Git Bash integration and Windows toolchain support
- **RiskClassSafe security grading**: `kill_background_task` now defaults to ALLOW, reducing unnecessary permission prompts

### Fixed
- **Streaming output jitter**: Added `wrapLineStable` hard-break wrapping replacing word-wrap during streaming; line break positions are determined solely by column index, unaffected by growing content; covers all three streaming paths (assistant/thought/tool)
- **Error color distinction**: Recoverable errors now show in gold, Fatal errors in red (previously all red)
- **/clear alias search & skill refresh**: Command picker now supports fuzzy alias search; slash registry rebuilt on session reset to refresh skill command list

## [v0.1.0-alpha.11] — 2026-07-03

### Added
- **Full background command support**: `ShellParams` now has an explicit `run_in_background` parameter; `&` backward compatibility (single-line `&` → stripped and run in background, multi-line `&` → foreground + log hint); `Execute`/`ExecuteStreaming` share file fd output to eliminate SIGPIPE; `task.Registry` for background task registration, status tracking, and exit code recording; `kill_background_task` SIGKILL process group termination; cross-turn `<background-task>` notification injection; Skill execShell background commands no longer freeze

### Fixed
- **Permission substring false-positive fix**: 10 dual-keyword inline execution patterns (`sh -c`, `bash -c`, etc.) now have `FirstTokenOnly` enabled, preventing path/flag substring matches from incorrectly flagging RiskHigh; permission test coverage improved to 95%

## [v0.1.0-alpha.10] — 2026-07-03

### Added
- **Shell streaming output**: Long-running commands (e.g., `make build`, `npm install`) now stream output line-by-line to the TUI in real time — no more waiting until completion to see progress
- **Enhanced @ file picker**: supports `../` sibling directories, absolute paths, and `~/` external directory search for cross-project file references
- **Glob `**` recursive matching**: `matchGlob` in permission rules now supports `**` recursive path matching

### Fixed
- **Background command pipe leak causing TUI freeze**: `bash -c "command &"` no longer freezes the TUI. Background processes are automatically redirected to temp log files; `ExecuteStreaming` `wg.Wait()` and `executeToolCalls` concurrent tool waits now have timeout protection — three layers of defense ensure the TUI never freezes permanently
- **Permission security hardening**: added dangerous command interception patterns (privilege escalation, inline execution), expanded safe command whitelist (grep/find/echo/mkdir and build tools), first-token exact match prevents path substring false positives, adjacency matching replaces substring AND matching
- **edit_file Unicode normalization**: added Unicode normalization and line-number prefix auto-repair fallback, reducing LLM no_match retries caused by invisible character differences
- **Shell Description optimization**: single-line command hard constraint, removed multi-line continuation tutorial, reducing invalid JSON generation by the LLM

### Refactored
- **LSP module removed**: eliminated grep/search_file/ls tools, toolset converged from 13 to 9 core tools — all code verification now goes through build tools, reducing complexity
- **Full i18n**: System Prompt now dynamically switches between Chinese and English based on locale, CLI output fully bilingual

## [v0.1.0-alpha.9] — 2026-07-02

### Added
- **Plan Mode — plan-first workflow**: In Plan Mode, only plan files are writable; source files are write-protected; shell risk routing (RiskLow elevated to ALLOW, RiskMedium/High unchanged); code edits require plan approval; `Shift+Tab` shortcut to enter/exit; `enter_plan_mode` / `exit_plan_mode` tools; TUI overlay approval dialog; `[plan:start #xxxx]` / `[plan:end #xxxx]` message pair tracking

### Fixed
- **Shell multi-line continuation JSON escaping guide**: Added `\\\n` multi-line command escaping examples to Shell tool Description, reducing invalid JSON escaping from the LLM

## [v0.1.0-alpha.8] — 2026-07-01

### Added
- **Bilingual slash commands**: SlashMessages injection mechanism enables automatic locale-based slash command text switching
- **Enhanced not_dir**: read_file on a directory now provides Did you mean file suggestions with blank-line auto-correction

### Fixed
- **DenialTracker circuit breaker removed**: tools are no longer blocked after consecutive denials; each request is evaluated independently (Step 1.5 removed)
- **LSP tool schema**: added soft constraints for non-code files to reduce misuse

### Refactored
- **Component decoupling**: eliminated compile-time coupling among tool / slashcommand / context / agentloop / pathutil for independent evolution

## [v0.1.0-alpha.7] — 2026-07-01

### Added
- **i18n multilingual support**: full zh-CN / en-US bilingual UI, auto-detection from LANG environment variable
- **--locale CLI flag**: `auto` (default) / `zh-CN` / `en-US`, three-tier priority: CLI > settings.json > LANG
- **/locale slash command**: switch UI language in real time within TUI, persists to settings.json
- **Bilingual CLI --help**: displays help text in the corresponding language based on locale
- **Setup wizard rewrite**: Bubble Tea + huh form interaction with integrated language/theme/provider/model configuration
- **Self-update check**: detects new GitHub Release versions when idle, Enter to download and install

### Fixed
- Permission command-chain bypass fix, risk level extension, DenialTracker sensitive path integration
- Esc interrupt kills process group, preventing stuck long-running bash commands
- install.sh removed GitHub API rate-limit dependency, switched to releases/latest/download redirects

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
- **AGENTS.md @ reference expansion**: supports `@path/to/file` external file references, auto-expanded, merged, and deduplicated
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
