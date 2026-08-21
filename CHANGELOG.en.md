## [v0.7.5] — 2026-08-21

### Added
- **Multimodal vision**: DeepSeek `deepseek-v4-flash-vision-exp` image understanding — reference images with `@path.png` to describe, OCR, and analyze charts (jpg/png/gif/webp, ≤4MB each); images are user-only messages, and non-vision models block before sending with a switch hint
- **Input text selection**: bubbles v2.2.0 — mouse drag and keyboard selection (Shift+arrows, Ctrl+Shift+A select all, Ctrl+Shift+C copy); Ctrl+V replaces the selection
- **Thinking-effort low tier**: DeepSeek options now off/low/high/max (official three-tier semantics); HUD colors each tier (gray/green/orange)
- **Image search in `@` picker**: png/jpg/jpeg/gif/webp are no longer filtered, so image files can be referenced directly

### Fixed
- **Injection-scanner false positives**: pages with inline JSON/JS escapes (e.g. GitHub) repeatedly triggered "Encoding Obfuscation" warnings — detection now decodes escapes and requires the instruction keyword to overlap escaped characters; fake-context JSON tightened to full message structures (role=system + content)
- **DeepSeek pricing calibration**: cache-hit/miss input ratio updated to 1/30 (docs and descriptions still carried the old 1/50~1/120); vision-exp pricing added
- **Historical images break non-vision models**: images left in history by a vision session caused every request to be rejected — now stripped at the wire layer (restored automatically when switching back to a vision model)
- **Thinking-effort mapping calibrated**: `low` is no longer remapped to `high` (official three-tier semantics)

---

## [v0.7.4] — 2026-08-20

### Fixed
- **DeepSeek thinking-mode 400 errors**: requests carrying the `tools` parameter did not fully echo back `reasoning_content` (turns without tool calls were stripped, empty strings were filtered out, and the reasoning item was placed in the wrong order), so the server returned `400 The reasoning_text in the thinking mode must be passed back to the API` and multi-turn tool loops aborted mid-session. Now every assistant turn is echoed unconditionally when tools are present (empty reasoning is sent as a space placeholder), and the reasoning item is placed before the message item (the ordering the server validates against, as verified in practice)

---

## [v0.7.3] — 2026-08-17

### Fixed
- **Preview-text protection fallback**: when the model ends with a preview suffix (colon/dash/arrow/ellipsis) but sends no tool calls, the loop injects a `[system:continue]` reminder and runs one more turn so the announced action is actually executed — a plain-text response is no longer mis-judged as the final answer that aborts the task. The mechanism first shipped in v0.7.0 and regressed when v0.7.1 reverted it; now reimplemented on the current StepCount/MaxSteps semantics, with the prompt-layer Tool Use Discipline restored

---

## [v0.7.2] — 2026-08-17

### Added
- **DeepSeek peak/off-peak pricing**: pricing now applies Beijing-time peak hours (9:00-12:00, 14:00-18:00) at ×1 and off-peak hours at ×0.5, with cost stats computed for the current time slot
- **Session naming**: `--name` names a new session, `/rename` renames it; `waveloom ls` and the TUI header show session names
- **Thinking-effort panel**: press `e` in the model picker to configure thinking effort, with provider-filtered options (DeepSeek off/high/max, OpenAI low/medium/high, Kimi max) and `off` to disable thinking; HUD label think → effort, and the tok short format supports the M unit

### Fixed
- **Resume context doubling**: `--resume` now uses JSON as the authoritative context source, fixing doubled context tokens caused by raw JSONL remnants
- **HUD thinking-effort display**: wrong effort display fixed; profile-configured effort is no longer lost on the startup path
- **Subagent summary separator**: subagentSuffix joins lazily, eliminating stray and doubled commas
- **LLM config merge**: ResolveProfile no longer overrides merged config with nil extra_params

### Refactored
- **agentloop terminology aligned**: loop/turn terms aligned with the ACP/Claude Code ecosystem (turn = one Run, step = internal iteration); behavior unchanged

---

## [v0.7.1] — 2026-08-15

### Refactored
- **Reverted v0.7.0 eval-driven changes**: removed the SWE-bench eval system (`eval/`), the max-turns guard (`[system:maxturns]` wrap-up reminder), the preview-text protection (`[system:continue]` follow-up reminder) and the hardened agent behavior rules (no rollback / first-edit deadline, etc.); the agent loop now behaves as in v0.6.2
- **Kept the rest of v0.7.0**: the `--no-sandbox` flag, the flags-after-prompt parsing fix, built-in pyright LSP support, and the deepseek-v4-pro Responses API adaptation

---

## [v0.7.0] — 2026-08-13

### Added
- **Max-turns guard & preview-text protection** (attributed from 33-instance SWE-bench eval: every failed instance ran out of max-turns and often discarded existing changes at the limit):
  - Max-turns guard: with ≤5 turns remaining, a `[system:maxturns]` wrap-up reminder is injected (persist changes / do not roll back / verify with tests / stop exploring), at most once per Run, reset across Runs
  - Preview-text protection: when the model ends with a preview suffix (colon/arrow etc.) but sends no tool calls, a `[system:continue]` reminder is injected and the loop continues one turn instead of mis-judging the task as complete. Shipped scope: also active in plan mode; reminder reworded as a factual suffix description to eliminate false positives; all 12 colon-like Unicode variants plus ASCII ellipsis are matched; when injection happens on the final MaxTurns turn, one grace turn is granted to re-issue the tool call
- **Agent behavior rules hardened** (eval attribution folded into the shared system prompt, effective for every task): `git checkout --` / `git reset` / `git stash` discarding on-disk changes is forbidden; verify after each change; First-edit deadline replaces Stop exploring (must start editing after 3-5 reads); announced actions must come with tool calls
- **deepseek-v4-pro adapts to the Responses API**: the pro model now switches to the Responses protocol automatically (aligned with the v0.6.1 flash adaptation)
- **Built-in Python LSP support**: `.py` files automatically use pyright-langserver (eval exposed that without an LSP, the agent missed unresolved-import diagnostics and failed directly)
- **`--no-sandbox` flag**: scenarios where one-shot/ACP force sandbox activation (eval Docker isolation / CI) can explicitly disable the sandbox; highest priority, short-circuits all activation checks

### Fixed
- **Flags after a positional prompt were ignored**: `waveloom "prompt" --max-turns 25` dropped every flag after the prompt (Go flag stops at the first non-flag argument); parseCLI was refactored to a loop parser supporting interleaved positionals/flags and `--` terminator semantics

---

## [v0.6.2] — 2026-08-09

### Fixed
- **deepseek-v4-flash reasoning & search-context fixes**: non-streaming reasoning content is no longer lost (Responses API reasoning content-block parsing corrected); server-side search results context is preserved across turns (web_search_call items are echoed back as-is); content-filter truncation is now correctly distinguished from max_output_tokens truncation
- **Server-side search calls survive resume**: assistant messages carrying web_search_calls are no longer dropped by message validation, so search context can still be echoed back after resuming a session

---

## [v0.6.1] — 2026-08-08

### Added
- **deepseek-v4-flash auto-adapts to the Responses API + server-side web_search**: when the model is `deepseek-v4-flash`, LLM requests automatically switch to the Responses API protocol (`POST /v1/responses` instead of Chat Completions); `web_search` becomes a server-side built-in search (no more reliance on unstable DuckDuckGo HTML parsing) — the server executes the search and injects results into context; the TUI shows a server-side search paragraph (status + real duration, Enter to expand). Other models (e.g. `deepseek-v4-pro`) keep Chat Completions + local DuckDuckGo/Brave search unchanged
- **`proplan` model choice**: selecting `--model proplan` or `/model proplan` uses the pro model in plan mode and the flash model for regular tasks (aligned with Claude Code's opusplan alias); 200k context guard (plan context over the window auto-degrades to flash); the choice persists to `profiles.<provider>.curr_model`, isolated across provider switches

---

## [v0.6.0] — 2026-08-04

### Added
- **Full ACP v1 agent implementation** (Resolves #5): JSON-RPC over stdio (initialize/session lifecycle/streaming/usage_update), per-session MCP integration, Terminal Auth handshake with Zed terminal-auth compatibility (`waveloom acp setup`), slash command system, per-request cancellation, error codes aligned with the official schema
- **Unified permission model for non-interactive entries**: one-shot/ACP get an unconditional binary decision (DENY/ALLOW only, no ASK) with auto-activated sandbox; one-shot no longer registers interactive tools (ask_user_question / enter_plan_mode / exit_plan_mode); TUI `--bypass-permissions` also enters the binary decision — no more permission prompt dialogs; one-shot **piped input** (non-tty stdin) requires an explicit `--bypass-permissions` to enable the binary decision, otherwise write/bash degrade to deny (untrusted pipe content stays locked down)
- **Sandbox network defaults to `on`**: sandboxed commands get direct network access when `network.mode` is unset (non-interactive entries work out of the box with networked tools); opt out explicitly via `--sandbox-network off` or `network.mode: off`
- **Layered registry + unified context-window parsing + subagent compaction**: TUI / one-shot / ACP share the same compaction config; subagents inherit it

### Fixed
- **Binary hijack env vars hard-blocked**: the "stripped-for-checking, executed-with-injection" wash path (`LD_PRELOAD` / `DYLD_*` / `NODE_OPTIONS` etc.) is closed — the check moved to Step 0.5 (before ask rules/whitelist short-circuits), with `env` prefix and compound-command segment detection; build-arg vars like `CFLAGS` are only stripped, never mis-blocked
- **Credential-masking warning now visible**: network `on` without `denyRead` / `credentials.files` prints a stderr warning (credentials are readable and exfiltratable), only when the sandbox is actually active
- **`/model` panic without LLM config**: SetModel crashed when LoadLLM returned nil
- **Tier3 summarization timeout protection**: stuck summaries no longer block the session; protected region/cursor fixes
- **Windows install script**: PowerShell 5.1 compatibility + custom directory support
- **TUI Enter latency**: OSC background-color query racing stdin caused Enter delay

### Refactored
- **Removed the default read-masking list and deprecated `allowRead`**: credential protection now relies solely on explicit `denyRead` / `credentials.files` (`~/.ssh` etc. are no longer masked by default — configure explicitly)

---

## [v0.5.1] — 2026-08-02

### Fixed
- **bwrap socket masking**: socket targets like `/var/run/docker.sock` are now masked via a tmpfs on their parent directory (resolving the real path, compatible with the `/var/run` → `/run` symlink) — fixes all sandboxed commands failing on Linux when docker is running (bwrap cannot bind-mount over an existing socket target)
- **Streaming pipe race**: `readPipesStreaming` now uses `io.Pipe`, eliminating occasional loss of short-command output caused by `cmd.Wait()` closing the `StdoutPipe` read end (reliably reproduced on slow/high-load machines)

---

## [v0.5.0] — 2026-08-02

### Added
- **OS-level sandbox isolation**: bubblewrap (Linux) / Seatbelt (macOS) with read-only root, workspace-only writes, credential masking (`~/.ssh`, keychains, docker.sock, tokens, etc.), env var stripping, and network control (`off` offline / `on` direct); auto-activated by `--bypass-permissions` or non-interactive ACP, configurable via `settings.json` and the `--sandbox-network off|on` flag
- **`--bypass-permissions` binary decision**: in non-interactive entries (one-shot/ACP) ASK → ALLOW, keeping only deny rules and high-risk hard blocks; TUI keeps prompts
- **Subagent sandbox + rule inheritance**: `bash_subagent` runs sandboxed like the main agent; subagent Guard inherits parent deny/ask rules
- **`make test-sandbox` dual-platform integration tests**: real backend on the host + bubblewrap in a Linux container, one command
- **bwrap first-run install guide**: distro-aware install commands (apt/dnf/pacman/apk/zypper) when bwrap is missing; Flatpak users get a "may already be installed" hint
- **`sandbox.allowRead` config**: explicitly allow paths hidden by the built-in default masking list (root rejected); fixes a `seen` map collision that silently disabled explicit masking; `.gitconfig` removed from the default mask list — git works again
- **Sandbox env injection & build-cache redirect**: `sandbox.env` injects GOPATH/GOMODCACHE/GOCACHE, npm_config_cache, etc., with `./` `~/` `/` path-prefix semantics, eliminating host-cache write failures under the read-only root

### Fixed
- **4 fatal background-task lifecycle bugs**: registry leak, kill race, cross-session state residue, etc.
- **Compaction pair-integrity redline**: post-compaction validation repairs orphan assistant↔tool pairings, preventing API 400 from killing the session
- **Tier3 hard-limit deadlock**: hard-limit path now retries summarization and lifts the limit on success; sessions no longer terminate permanently
- **Compaction pair-boundary alignment**: Tier3 deletion boundaries align with tool-call pairings (incl. parallel batches); cursor clamping prevents compaction from stalling
- **Hunk path tolerance & diff renumbering**: relative header paths no longer double-join with file_path; ReadStateStore path normalization; TUI diff line numbers corrected for non-standard @@ headers
- **edit read-first enforcement**: the mandatory read-before-edit rule now lands in the live system prompt (`pkg/prompt/default.md`); `bash cat`/diagnostic snippets don't count as read state; failure messages clearly attribute missing reads

### Changed
- **Sandbox wired through the whole chain**: agentloop per-command status injection, Shell tool wrapping, `<sandbox_violations>` annotations, escape-hatch hints
- **Compaction validation only on actual modification**: zero overhead on no-op rounds

---

## [v0.4.4] — 2026-07-30

### Fixed
- **edit hunk empty line matching**: bare empty lines in hunk body are now correctly treated as context lines, fixing hunk match failures caused by trailing empty lines
- **Unicode punctuation mapping**: normalizeUnicode2 now covers 152 additional Unicode punctuation mappings, improving hunk match tolerance for non-ASCII punctuation
- **Windows path separator compatibility**: ExtractHunkFilePaths now recognizes Unix-style absolute paths (e.g. /tmp/config.go) in hunk headers on all platforms

### Changed
- **Prompt read-first search guidance**: removed residual rg/hashline references, eliminated grep signal contradictions, guiding LLM to use read(pattern=...) for single-file search and grep only for multi-file scanning

---

## [v0.4.3] — 2026-07-30

### Added
- **edit tool**: envelope format + DiffHunks diff view; `write` now displays diff output

### Fixed
- `?` no longer triggers help overlay when input is non-empty
- Tool summary line truncation now uses displayWidth-aware truncation, fixing CJK character width issues
- Session UUID now conforms to Claude Code standard UUID v4 format
- Subagent AgentTool now correctly syncs LLMClient after provider switch

---

## [v0.4.2] — 2026-07-27

### Fixed
- **Windows cross-platform path compatibility**: PathToURI/ResolvePath no longer incorrectly treat Unix-style absolute paths as relative and join with CWD on Windows, fixing LSP URI generation errors
- **@ reference JSON permission check**: @ file reference expansion now normalizes backslash paths before embedding in JSON, fixing permission check failures on Windows

---

## [v0.4.1] — 2026-07-27

### Added
- **read outline mode**: read tool now supports `outline` parameter for quick file structure browsing via LSP document symbols; regex fallback covers Shell/Makefile/Markdown/Ruby/YAML files
- **Stream layer safety**: added safety boundaries in streaming output layer to prevent malformed stream data from affecting TUI rendering

### Fixed
- **rewind HUD values lost**: session-level cumulative HUD tokens/stats were reset after rewind; now preserved
- **LSP Windows compatibility**: mockLSPPath now handles .exe extension on Windows, fixing test failures
- **Pricing fallback**: model pricing falls back to safe defaults when prompt cache is missing, preventing zero-cost billing; hudCost supports session persistence recovery
- **LSP idle_timeout_ms**: LoadUserServers now correctly returns idle_timeout_ms from settings

---

## [v0.4.0] — 2026-07-27

### Added
- **LSP diagnostic auto-verification**: `edit`/`write` tools automatically run LSP diagnostics on changed files and inject compile errors/warnings into tool output, so the LLM catches syntax and type errors without manual builds
- **Multi-language support**: built-in configs for gopls (Go), rust-analyzer (Rust), typescript-language-server (TS/JS), clangd (C/C++); custom LSP servers configurable via settings.json
- **Environment probe integration**: LSP server probes merged into startup detection; silently skipped when not installed
- **LLM Token pricing**: new `pkg/pricing` module with dual-currency (CNY/USD) and model-level incremental billing; TUI footer shows real-time costs

### Fixed
- **hashline TAG=0000**: returns "has not been read yet" instead of ambiguous "TAG mismatch"
- **fakeContextRe false positive**: [system:xx] no longer misidentified as external data
- **Message ID gaps**: filled missing message IDs, system dedup, NewMessageID made public
- **TUI subagent resume**: subagent paragraphs no longer misaligned after resume
- **Multiple in_progress warning**: todo injects [system:todo] user message when multiple items are in_progress

### Changed
- **Tool simplification**: removed rg preference hints and sed/awk/python suggestions, unified on grep
- **hashline editing model**: `+` escaping uses HasPrefix detection, INS unified effectiveLine, post-edit context moved before section results
- **hashline dead code**: removed unused effectiveLine method and test helper types

---

## [v0.3.3] — 2026-07-25

### Fixed
- **Hashline marker false positive**: scanner incorrectly triggered marker detection on non-hashline content; enhanced matching precision prevents read output anomalies
- **Diff dual coordinate confusion**: tool name now shows gray pending state, resolving precision confusion between diff line numbers and actual file line numbers

### Changed
- **staticcheck QF1003**: if-else chain in tui_renderer rewritten as switch statement per Go conventions

---

📝 [Changelog (简体中文)](https://github.com/Menfre01/waveloom/blob/dev/CHANGELOG.md)

## [v0.3.2] — 2026-07-25

### New Features
- **IDE MCP Server integration**: LLM prioritizes IDE index (workspace symbols, find references) over shell commands, reducing search overhead
- **read pattern parameter**: read tool supports pattern for single-step file content navigation, eliminating grep→read round trips; summary shows params for visibility
- **System prompt refinements**: read-first search strategy, tool error self-check guidance, nearest-diff-first bug investigation strategy
- **UX improvements**: input history now Ctrl+P/N (↑↓ back to pure scroll), Ctrl+C double-tap quit protection
- **Subagent parallel debugging**: preserve model config per agent type, deduplication, improved parallel execution
- **Setup context window config**: setup wizard now includes context window configuration step

### Fixes
- **Permission ALLOW bypass**: allow rules incorrectly short-circuited by safety ALLOW bit, causing silent allowance after user denial
- **Plan mode resume recovery**: fixed Guard/Loop state loss after resume, nil loop panic, and doTurn fallback recovery
- **Streaming OOM protection**: single-line byte truncation in truncateToolStreamOutput prevents large JSON lines from exhausting memory
- **JSON escaped quotes**: extractField correctly handles \\\" to fix display truncation of bash commands with quotes in permission panel
- **Multi-line continuation folding**: fixed \\ line continuation being collapsed to space in streaming output
- **Skill tool summary truncation**: adaptive width truncation prevents layout overflow from long skill names
- **Resume HTML entities**: fixed `&amp;quot;` causing incorrect symbol rendering and missing cwd prefix after resume
- **Edit partial success misreporting**: multi-file edit no longer reports overall failure on partial success
- **Multi-edit consolidation**: same-file multiple edits now merged into single edit call, preventing tag conflicts

### Refactoring
- **Input device rollback**: restored mouse tracking for wheel scrolling, ↑↓ idle history navigation / scroll at boundary; removed overlay scroll passthrough and boundary scrolling
- **System prompt slimming**: removed tool lock-in restoring LLM autonomy, restructured three-zone layout, added Tier3 summary notification
- **Removed advisor mode / tool description cleanup**: simplified model config and setup flow

---

📝 [Changelog (简体中文)](https://github.com/Menfre01/waveloom/blob/dev/CHANGELOG.md)

# Changelog

## [v0.3.1] — 2026-07-23

### Fixed
- **Interrupt path input leak**: User-entered prompt was not cleared on loop interrupt, causing the next loop to reuse the stale prompt across turns
- **Native text selection**: Removed mouse tracking mode; terminal restores native mouse behavior, allowing text selection and copy without holding Shift

---

## [v0.3.0] — 2026-07-22

### Added
- **Security defense alignment**: 24 Shell security detectors (mvdan/sh AST pre-check eliminates false positives), parser differential attack detection (backslash operators/brace expansion/obfuscated flags), recursive JSON sanitization; 198 security test cases total
- **Five-layer tool output security pipeline**: Unicode sanitization (NFKC normalization + Cf/Co/Cs category detection), prompt injection pattern scanning (4 categories, 30+ regex patterns), external data boundary markers, risk classification, safe truncation
- **Hook system**: Supports 5 event types including PreToolUse / PostToolUse / Notification / Stop with permission_mode field; configurable via settings.json; runtime fail-open preserves tool execution
- **`/provider` interactive overlay**: No-argument `/provider` shows an ↑↓ select / Enter switch / Esc cancel overlay for runtime LLM Provider switching
- **Tiered timeout mechanism**: agent/subagent/MCP tools default to 30min, regular tools 5min, Shell tools 5min; `ToolWithTimeout` interface for customization
- **todo_create / todo_update split**: ID-first matching eliminates duplicate creation and state loss
- **Unified JSONL persistence**: Cross-turn session, plan mode, filehistory, and subagent state unified into JSONL format

### Fixed
- **Hashline edit model fixes**: Recovery TAG validation, readBody truncation fix, multi-section atomic writes, operator line prefix error reporting
- **Skill/built-in command name collision panic**: `HasCommand` collision detection skips conflicting skills
- **`settings.json` parse error silent fallback → immediate panic**
- **Kimi baseUrl corrected**: Default changed to `https://api.kimi.com/coding/v1`
- **Provider switch 404 and balance residue fixes**
- **Resume viewport system message leak**: Removed redundant compaction path injection
- **Streaming output FD mode double-output fix**
- **Shell timeout hard cap removed**: `Shell` implements `ToolWithTimeout`

### Changed
- **Subagent model tightened to pro/flash enum**: Explore locked to flash, evaluate/verification locked to pro; fork max turns 200→50
- **Prompt extracted to go:embed**: Agent system prompt extracted to standalone .md files
- **Environment probe cache TTL shortened**: 24 hours → 15 minutes
- **Removed legacy read_file/edit_file**: Unified under hashline edit model

---
## [v0.2.0-beta.3] — 2026-07-21

### Fixed
- **Skill/built-in command name collision causes panic on startup**: When a user has a skill or plugin command named identically to a built-in command (e.g. `help`), `newSlashRegistry` registers built-in commands first then iterates over skills, causing `Register` to panic on duplicate name. Added `HasCommand` collision detection — conflicting skills are skipped while built-in commands are preserved.

## [v0.2.0-beta.2] — 2026-07-20

- **`/provider` interactive picker overlay**: `/provider` without arguments now opens an ↑↓ select / Enter switch / Esc cancel overlay, matching the `/model` picker experience
- **Shell tool timeout increase**: Default timeout 120s→300s, maximum timeout 600s→1800s, supporting longer build/test tasks
- **`shell_prompt` rg guidance**: System prompt now guides the model to prefer `rg` (ripgrep) for code search, closing the loop with environment detection

### Fixed
- **Kimi baseUrl corrected**: Default URL changed to `https://api.kimi.com/coding/v1`
- **Provider switch balance residue**: After switching providers, async query new balance to avoid displaying stale provider data
- **Hook matcher case-insensitive**: `Match()` now uses `strings.EqualFold`, compatible with both Claude Code and Waveloom naming conventions
- **Resume viewport system message leak**: Removed redundant todo injection in compaction path, added 5 filter rules aligned with live TUI silent behavior
- **`settings.json` parse error silent fallback → immediate panic**: Corrupted config no longer silently enters TUI; errors are surfaced immediately

### Changed
- **Subagent model parameter tightened to pro/flash enum**: No longer accepts actual model names; added `SettingsProvider` + `resolveModel` dynamic mapping; Explore locked to flash, evaluate/verification locked to pro; fork max turns 200→50; tightened subagent usage guidance
- **Environment probe cache TTL shortened**: 24 hours → 15 minutes, reflecting toolchain changes faster

---
## [v0.2.0-beta.1] — 2026-07-20

### Added
- **`/provider` command**: Dynamically switch LLM Provider (deepseek / openai / kimi) at runtime, auto-resolving per-profile API Key, Base URL, and model configuration
- **Claude Code-compatible Hook system**: Supports 5 event types including PreToolUse / PostToolUse and permission_mode field, compatible with `.claude/settings.json` hooks configuration
- **Tiered timeout mechanism**: agent / subagent / MCP tools default to 30min timeout, regular tools default to 5min, with `ToolWithTimeout` interface for customization
- **edit response smart context layering**: Small files (≤200 lines) shown in full; large files use context windows + directory index to reduce re-read frequency
- **write tool TAG return**: Written files return a TAG, enabling direct chained edits without re-reading
- **`todo_write` split into `todo_create` / `todo_update`**: ID-first matching with content fuzzy fallback, eliminating duplicate creation and state loss
- **Unified JSONL persistence**: Cross-turn session, plan mode, filehistory, and subagent state unified into JSONL format

### Fixed
- **Shell timeout hard cap removed**: `Shell` tool implements `ToolWithTimeout`, no longer constrained by global `MaxShellTimeoutMs`
- **Provider switch 404**: Fixed multi-provider profiles where some provider API Keys were not applied, causing 404 errors
- **Kimi usage stats missing**: Fixed missing token usage statistics for Kimi provider
- **Multi-section atomic write**: Multiple patch sections for the same file now apply in a single atomic write, eliminating intermediate-state residue
- **resume TUI rendering fixes**: Fixed paragraph reconstruction, plan/rewind/subagent overlay rendering on session resume
- **`reasoning_content` roundtrip fix**: Fixed reasoning_content loss across turns in streaming responses, plus empty-response regression guard
- **`todo_write` infinite creation fix**: Prevented duplicate todo creation from ID-less content matching
- **File FD output duplication fix**: Fixed doubled output in `ExecuteStreaming` file descriptor mode
- **Hashline error clarity**: Leading `+` on operation lines and missing `+` on body lines now produce clear error messages instead of silent drops

### Changed
- **Removed legacy read_file / edit_file**: Unified to hashline read / edit / write; prompts extracted to `go:embed` .md files
- **Edit delta context display**: Post-apply context lines shown around changes for better LLM editing continuity
- **Tool error recovery guidance**: C2→C1 cross-tool reference migration for clearer error recovery paths
## [v0.1.0-beta.10] — 2026-07-16

### Added
- **Multi-file edit diff rendering**: Multi-file edit patch responses now include file header path summaries, so the LLM can visually confirm post-edit content state for each file

### Fixed
- **CLI help and code alignment**: `--help` output and shell completion now match actual code functionality; oneshot mode output internationalized
- **Agent loop concurrency race**: barrierTool now context-aware, eliminating race conditions in concurrent tool call scenarios
- **Windows path compatibility**: Replaced `/dev/zero` with Windows-compatible `NUL` device in tests
- **Hashline stability**: Lint fixes and read error handling improvements for a more robust editing model
- **Lint zero-warning cleanup**: Eliminated all SA5011/ineffassign/SA4010 static analysis warnings

### Changed
- **Hashline edit model refactor**: Introduced declaration-order offset calculation and operation overlap detection for more reliable multi-operation patch parsing
- **Todo apply mechanism optimization**: Apply switched from full replacement to content-based matching merge, preventing lost tasks from concurrent updates
- **Structured logging migration**: `fmt.Fprintf(os.Stderr)` + `--verbose` fully migrated to `log/slog` with unified, controllable log levels and formatting
- **Session package rename and module split**: `pkg/context` → `pkg/session`, tool schema inlined, `tui.go` split into 4 files by responsibility

## [v0.1.0-beta.9] — 2026-07-14

### Added
- **Hashline editing model**: New hashline read/edit/write editing tools replace the old read_file/edit_file, with TAG-anchored edits, SWAP/INS/DEL/REM/MV operations, and empty body line preservation; edit responses auto-append post-edit context so the LLM can chain edits without re-reading
- **yarn/pnpm/clang toolchain detection**: Environment probes now detect yarn, pnpm, and clang, covering more frontend and C/C++ project scenarios
- **web_search timeout control**: web_search now supports `timeout_ms` parameter to prevent long-running search requests from blocking
- **Skill $@ bash-compatible syntax**: Variable substitution now supports `$@` bash-compatible syntax

### Fixed
- **Hashline TAG stability**: TAG digest algorithm refactored to ensure unchanged file content yields a stable TAG; recovery range invariant validation eliminates silent corruption risks
- **Hashline LLM format tolerance**: Tolerates trailing colons, leading whitespace, end-of-line comments, colon-less INS, mixed case, and single-quoted paths — common LLM format deviations
- **Hashline path alignment**: Fixed misalignment between edit and read paths causing cross-turn tag_mismatch
- **Hashline syntax confusion**: parseLineRange now provides friendly error hints for `:=` format confusion
- **Subagent fork message cleanup**: Orphan tool_calls cleaned from fork message construction, fixing cache hit rate anomalies
- **Subagent write operation tracking**: Fixed missing hashline edit write tracking in subagent_write_operations
- **Subagent/permission security hardening**: Fixed fork boilerplate, sensitive file classification, and cleanup guidance
- **Permission rule fix**: edit_file/write_file rules were ineffective due to missing normalizeToolName compatibility mapping
- **Permission Bash allowlist**: Prefix matching now includes command chain operator detection to prevent bypass
- **Agent loop todo residue**: Detects lingering todos before ReasonCompleted and injects last-chance reminder to prevent abandoned lists
- **Memory UTF-8 handling**: Invalid UTF-8 sequences are no longer silently replaced with U+FFFD — original content is preserved with a warning
- **Shellutil background detection**: IsBackgroundCommand no longer misidentifies `&&`-terminated commands as background commands
- **TempDir symlink**: os.TempDir() replaced with pathutil.TempDir() to resolve macOS symlink path inconsistencies
- **Context/task persistence**: lastBackgroundCheck is now persisted across --resume, restoring interrupted task state
- **TUI help overlay**: Fixed insufficient text contrast on ? help overlay shortcut key labels

### Changed
- **Unified tool naming**: Removed old read_file/edit_file tool registrations and the hashline feature flag; read/edit/write short names are now uniformly registered
- **TUI logo layout**: Logo moved from header into the scrollable viewport, freeing up fixed header height
## [v0.1.0-beta.8] — 2026-07-13

### Added
- **First-run experience optimization**: Auto-launches setup wizard on first run without config instead of erroring out; empty API Key stays in-place with error prompt; validates API Key before saving (lightweight ListModels check); TUI empty state shows onboarding panel (/ commands, @ references, ⏎ send, sample prompts); human-readable error mapping (humanizeError) instead of raw JSON leaks; environment probe results cached for 24h with PATH-change auto-invalidation, zero wait on second launch; update notification switched to footer 3-state toggle

### Fixed
- **plugin lint**: Fixed unchecked os.MkdirAll return value causing lint errcheck warning
- **Windows path compatibility**: Normalized path separators in `stripCWDPrefix`, `pathPrefixMatch`, `extractDirPrefix` using `filepath.ToSlash`/`IsAbs`/`Dir` instead of hardcoded `/`, fixing file picker filtering and display issues on Windows

### Changed
- **System prompt reorder**: C2 behavioral constraints merged into C1, sections reordered by attention mechanism priority, C3c switched to Append strategy for improved instruction adherence
- **TodoWrite tool split**: New ToolWithPrompt optional interface — tools can provide separate short Description (~60 tokens) and Prompt usage guide (~1200 tokens). Registry auto-concatenates them; system message untouched, prefix cache unaffected
- **Todo reminder system hardening**: StatusSummary changed from passive notes to active instruction checkpoints; idleTodoWrite/idleTodoReminder threshold lowered from 3 to 2; todoReminderText embeds staleness count and removes escape hatches; 14 new regression tests covering all reminder/injection/counter paths
- **Subagent-todo lifecycle binding**: Reverted TodoState context propagation (removed WithTodoState chain), replaced with C1 END prompt guidance: parent agent sets todo to in_progress before spawning subagent, updates to completed after return; explicit 3-turn cadence for parallel subagents; `todo_write` added to subagent `allAgentDisallowed`

## [v0.1.0-beta.7] — 2026-07-12

### Added
- **Claude Code plugin compatibility**: Automatically discover and load skills/commands from installed Claude Code plugins via `installed_plugins.json` + `enabledPlugins` config. User-created skills take priority over plugin skills with the same name. Supports both standard skills/commands directories and manifest-declared custom paths ([#2](https://github.com/Menfre01/waveloom/issues/2))
- **/model advisor mode notice**: When switching models via `/model` in advisor mode, appends a warning that model switching doesn't change normal/advisor mode
- **Tool error backoff**: Added graduated backoff for tool errors in advisor mode, reducing token waste on consecutive failures

## [v0.1.0-beta.6] — 2026-07-11

### Added
- **Advisor Mode cost optimization**: Advisor subagents now use the flash model for evaluation tasks while the main agent retains pro for deep reasoning — evaluation token costs reduced by ~50%
- **Overlay/Rewind TUI enhancements**: Overlay panels now span full terminal width, eliminating narrow-edge clipping; rewind message selector supports adaptive width, content truncation, and scroll interaction

### Fixed
- **TUI persistence fix**: Dark/light detection switched to Bubble Tea `BackgroundColorMsg` system events, fixing silent theme persistence failure on some terminals
- **Plan Mode model switch fix**: Advisor model not switching from pro to flash when entering plan mode manually
- **System prompt reasoning vulnerability**: Comprehensive audit and fix of 2 reasoning gaps in agent system prompt that could allow LLM constraint bypass

### Changed
- **Model config fully settings-driven**: LLM model configuration now entirely driven by settings.json, removing all hardcoded model constants — users can customize arbitrary LLM parameters via settings

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
