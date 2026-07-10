# Changelog

## [v0.1.0-beta.5] — 2026-07-10

### Added
- **Checkpoint/Rewind time travel**: Rewind conversation to any previous user message with full file state restoration. Automatic file backup to `.waveloom/file-history/` before each edit, checkpoint creation per user turn. TUI selection interface (message list + confirmation dialog) with Fork mode (original session preserved intact)
- **Glamour Dracula syntax highlighting**: Dark theme Glamour Markdown code blocks switched from DarkStyle to Dracula palette — 25+ token types (Comment, Keyword, LiteralString, etc.) now render with significantly improved contrast

### Fixed
- **Dark theme readability**: Gray / Muted colors brightened for better text contrast on dark terminals
- **HUD layout fixes**: New-content notification no longer displaces HUD display line; fixed expanded-view width overflow; tool output truncation now respects UTF-8 rune boundaries, preventing multi-byte character corruption
- **i18n completion**: Filled in 4 hardcoded Chinese strings including subagent suffix, unifying Messages internationalization

### Changed
- Streamlined `todo_write` prompts, centralizing rules in system prompt to reduce tool description token cost

## [v0.1.0-beta.4] — 2026-07-09

### Added
- **`web_search` built-in tool**: Dual-backend search (DDG default + Brave optional), forming a search→read loop with `web_fetch`; dedicated TUI paragraph rendering with query display, snippet preview, and expandable results
- **MCP desktop auto-discovery**: Automatically detects Claude desktop config (macOS/Windows/Linux paths), no manual setup needed to connect existing MCP servers

### Changed
- **`todo_write` trigger threshold optimization**: Trigger tightened from ≥2 turns to ≥5 turns, parallel subagents → serial subagents, idleTodoReminder adjusted from 2 to 3, reducing abuse on trivial tasks

## [v0.1.0-beta.3] — 2026-07-09

### Added
- **Color-blind dual themes**: ColorBlind split into Dark CB (dark terminal) and Light CB (light terminal), preserving blue/orange diff colors with full dedicated palettes
- **Theme persistence**: Theme changes via Ctrl+G / `/theme` are saved to settings.json and restored on next launch
- **Glamour full theme sync**: 12+ Markdown element colors (paragraphs, blockquotes, tables, horizontal rules, emphasis/strikethrough, list bullets, etc.) now fully synchronized with Waveloom palettes
- **Emoji rendering**: `:rocket:` shortcodes now render as Unicode emoji
- **True color syntax highlighting**: Chroma upgraded to `terminal16m` (16.7M colors)
- **`?` shortcut help overlay**: Press `?` to see all keyboard shortcuts in a vertically-laid overlay that fits narrow terminals

### Fixed
- Subagent token usage and cache hit rates now accumulated into main agent HUD stats
- Windows `splitPathParts` infinite loop on drive letters causing 5-minute timeout
- Welcome hint not reappearing after `/new` (now ignores system-only paragraphs)
- New-content notification incorrectly occupying a render line causing cursor drift

### Changed
- Help overlay switched from FullHelpView column layout to ShortHelpView vertical rendering, eliminating narrow-terminal clipping
- Empty-state check generalized to ignore system paragraphs, preventing future system messages from blocking the welcome hint

## [v0.1.0-beta.2] — 2026-07-08

### Added
- **Subagent structured event rendering**: TUI expanded view now renders events with distinct styles — thought processes in dimmed italic, tool names in green bold + args in code color, tool output with │ prefix indentation; new `SubagentThought` and `SubagentToolStream` event types
- **Layer 3 post-hoc security classifier**: Automatically scans subagent events after execution, detecting dangerous commands (rm/chmod/sudo/shutdown etc.) and sensitive file operations (.env writes), generating `HIGH`/`MEDIUM`/`LOW` security warnings injected as `<subagent_security_warning>` XML block into the parent LLM result
- **Explore auto light-model**: `Explore` subagents now automatically use the configured `sub_model` (e.g., `deepseek-v4-flash`) when no model is explicitly specified, reducing token costs for discovery tasks
- **Footer thinking effort display**: Model name now shows `(think high)` / `(think max)` badge, auto-resolved from `reasoning_effort` config; hidden when thinking is disabled
- **Subagent transcript persistence**: `TranscriptLine` gains 8 subagent fields (type/model/turns/tokens/events JSON), enabling full subagent paragraph state restoration on `--resume`

### Fixed
- `extractPath` edit_file format adaptation: switched from emoji prefix `"✅ Edit applied to"` to `"Edited file:"` prefix parsing
- `ToolCallStream` event Kind corrected from `SubagentToolResult` to independent `SubagentToolStream`, preventing duplicate rendering of stream chunks and final results

### Changed
- Streamlined system prompt and tool descriptions, separating concerns to reduce token consumption

## [v0.1.0-beta.1] — 2026-07-07

### Added
- **MCP Client**: Full MCP client — connects to external MCP servers, automatic tool discovery and registration alongside built-in tools; supports SSE and stdio transports, `mcpServers` config compatible with Claude Code `.claude.json`
- **Todo task list**: Complete todo state management — `todo_write` tool, TUI side panel, periodic reminders, pending/in_progress/completed state transitions; supports parallel subagent multi-in_progress, headline shows completion progress
- **Subagent enhancements**: Fork identity injection keeps call chain traceable; evaluation/verification cold agents (independent review, adversarial verification); model auto-switching (deepseek-v4-pro for deep reasoning vs flash for routine); cache-friendly message construction maximizes prefix hits
- **Periodic todo reminders**: Replaces one-shot ReminderInjected — auto-reminds the LLM about incomplete todo items on a clock cadence

### Fixed
- **MCP**: Goroutine leak, SSE line parsing errors, exit code bugs — 9 issues in one patch; log output now defaults to `io.Discard` to prevent TUI leakage
- **Agent Loop**: `resultsCh` double-panic, Guard nil dereference — 4 defects fixed; `ReminderInjected` now resets across turns when stale todos remain
- **Subagent**: `forwardEvents` fan-out channel decoupling eliminates deadlock; concurrent event routing fixes, mid-turn text trimming, `bash_subagent` isolation
- **Todo**: Merge mode no longer drops unmentioned items; LLM workflow guidance shifted from incremental updates to full-list replacement
- **TUI**: Multi-line user messages now show `›` prefix on every line; `--resume` no longer resurrects cleared todolists; todo panel pending items now use default text color
- **Windows**: `install.ps1` auto-configures PATH and Git Bash `~/.bashrc`; Go module paths adapted for Windows backslash separators

### Changed
- Todo removes ID and merge mechanism — LLM passes the complete list each time, eliminating state inconsistency
- Todo drops single-in_progress restriction, allowing parallel subagent tasks to be in_progress simultaneously
- Subagent extracts `ensureNonEmpty` to eliminate anyText state tracking
- Tighten `todo_write` trigger conditions to reduce abuse on trivial tasks
- Strengthen `deepseek-v4-flash` default recommendation in system prompt

## [v0.1.0-alpha.15] — 2026-07-06

### Added
- **Subagent delegation**: New `agent` tool supporting fork and cold agent modes; subagents can autonomously execute complex multi-step tasks; cold agents start with fresh context for exploratory tasks

### Fixed
- **Windows Git Bash compatibility**: Shell interpreter detection now prefers `exec.LookPath` to find `bash.exe` in PATH, fixing the "setup works but normal startup crashes" issue; `resolveWindowsShell` no longer calls `os.Exit(1)`, returning empty string for caller handling instead
- **Permission rule engine Windows path adaptation**: `splitPath`/`matchGlob` now normalize `\` to `/` via `filepath.ToSlash`, fixing Windows file path glob rule matching (e.g., `src/**`)
- **Self-update `os.Chmod` Windows guard**: `SelfUpdate` and `extractWaveloom` now check `runtime.GOOS != "windows"` before calling `Chmod(0o755)`, preventing update failures on Windows
- **`/tmp` working directory whitelist platform guard**: `Guard` initialization only adds `/tmp` on Unix; Windows uses `os.TempDir()` instead
- **Command safety `extractFirstToken` backslash fallback**: Added `\` fallback for correct command extraction from Windows absolute paths
- **`/proc/self/fd/` platform guard**: Added `runtime.GOOS != "windows"` guard since Windows has no `/proc/` filesystem

## [v0.1.0-alpha.14] — 2026-07-04

### Added
- **Backoff mechanism refactored**: Tool+Kind dual-key backoff tracking with three-tier progressive warnings (3/5/8 strikes), cross-turn backoff state persistence across loops, reducing pointless retries on same-class errors

### Fixed
- **@ file picker unresponsive in large directories**: `filepath.WalkDir` traversal no longer truncates in huge directories, shows real-time progress; absolute path search no longer times out
- **@ ../ path base error**: `doScanRelative` CWD base fix when resolving `../` relative paths, ensuring correct sibling directory search results
- **Windows CI test failure**: `relativizePaths` unit tests used hardcoded Unix paths; on Windows, `filepath.IsAbs` returns false without a drive letter, causing `filepath.Rel` to misbehave — fixed with cross-platform absolute path construction

### Refactored
- **@ picker cross-platform compatibility**: Replaced external `find` command with `filepath.WalkDir`, unified search logic across Windows / Linux / Darwin

## [v0.1.0-alpha.13] — 2026-07-04

### Fixed
- **@ parent directory search missing current project**: `doScanRelative` now prepends CWD directory item to avoid 500-item truncation loss; also fixes `../waveloom/` prefix being lost when resolving back to CWD, which broke subsequent child file search
- **@ / / picker sorting optimization**: Prefix and substring groups now sorted by match position (leftmost first); non-contiguous matches fall to the end; `/` command picker uses same strategy
- **Expander `ls` pseudo-tool cleanup**: File and directory references now use unified `read_file` permission check, removing dependency on deleted `ls` tool

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
