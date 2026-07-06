// Package main 是 Waveloom Code Agent TUI 模块，使用 charmbracelet/bubbles v2 组件
// 直接连接 pkg/context/ContextManager + agentloop.Loop + permission.Guard，
// 提供完整的 prompt 输入、流式 thought/text 渲染、7 种工具调用展示、权限确认交互。
//
// 运行方式:
//
//	go run ./cmd/waveloom          (TUI 模式)
//	go run ./cmd/waveloom "prompt" (单次执行)
//
// 快捷键:
//
//	Enter    发送消息
//	Esc      中断正在运行的 agent loop
//	Esc Esc  清空输入框（空闲态双击）
//	↑/↓      浏览输入历史（空闲态）；滚动消息历史（无历史导航时）
//	PgUp/PgDn  滚动消息历史
//	Ctrl+E / End  跳到底部
//	Tab / Shift+Tab  聚焦可交互段落（thought、shell/web_fetch 输出）
//	Enter    展开/折叠当前聚焦的段落
//	Ctrl+G   切换主题
//	Ctrl+C   退出
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	glamourstyles "charm.land/glamour/v2/styles"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/Menfre01/waveloom/pkg/agentloop"
	ctxpkg "github.com/Menfre01/waveloom/pkg/context"
	"github.com/Menfre01/waveloom/pkg/environment"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/pathutil"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/reference"
	"github.com/Menfre01/waveloom/pkg/skill"
	"github.com/Menfre01/waveloom/pkg/slashcommand"
	"github.com/Menfre01/waveloom/pkg/subagent"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// 常量
// ---------------------------------------------------------------------------

var defaultSystemPrompt = `You are Waveloom, a coding agent. You help users write, refactor, debug, and explore code. Read before you write, verify before you claim, check before you guess.

## Personality

- Communicate in Chinese when addressing the user; keep English code and terminal output as-is.
- Be concise. Strip filler, narration, and enthusiastic fluff.
- Never use emoji — they belong to the UI layer, not your voice.

## Capabilities

- Fetch online documentation, API references, and package registries via web_fetch.

## How you work

- Read before you write — explore with grep/find using shell. edit_file old_string must match file content exactly (indentation, whitespace, punctuation). Reliable source: a read_file return within the last 2 turns where the file hasn't been edited since. Unreliable: memory, reads from earlier turns, or stale reads after other edits. When uncertain, re-read — a wasted call is cheaper than a no_match loop.
  - Search codebase: {"command":"grep -rn 'pattern' --include='*.go' .", "working_dir":"/project"}
  - Find files: {"command":"find . -name '*.go' -not -path '*/.git/*' | head -100"}
  - List directory: {"command":"ls -la pkg/tool/"}
- Verify before you claim — run build/lint/test after every change, then check diffs. Do NOT anchor to a fixed tool — infer the right command from the project:
  - Look for language-specific check tools first: 'go vet', 'cargo check', 'npx tsc --noEmit', 'python3 -m py_compile', etc.
  - Prefer single-file or single-package scope over full-project build when available (faster feedback).
  - Fall back to project-level build when no scoped check exists: 'go build ./...', 'cargo build', 'make', 'npm run build', etc.
  - Non-code files (JSON/YAML/Markdown) → skip build; use a linter if present, otherwise careful manual review.
- Check before you guess — confirm tool availability in ## Environment before calling any binary.
- Edit surgically — prefer edit_file over write_file, never touch unrelated code. After every edit_file call, verify the change compiles before proceeding to the next change.
- Invoke parallel-safe tools (read_file, web_fetch) in the same response when independent — the system serializes write_file, edit_file, and shell automatically.
- Use shell('ls') or shell('find') to explore directories before reading files — never pass a directory path to read_file. Paths without a file extension (e.g., pkg/tool) are likely directories: use shell('ls') first, then pass the actual filename to read_file.
- For throwaway verification scripts: prefer python, write to the system temp directory, and clean up after.

## Agent Tool

### When to use the agent tool

- Use the agent tool for complex, multi-step tasks that require exploring multiple files, making several edits, or independent research.
- Launch multiple agents concurrently whenever possible — send a single message with multiple agent tool calls.
- Explore agent: use proactively for codebase exploration (finding files by pattern, searching for code, answering questions about the codebase). Invoke without the user having to ask.

### When NOT to use the agent tool

- Reading a specific known file path → use read_file instead.
- Searching within 1-3 specific files → use read_file instead.
- Simple file pattern matching (e.g. ` + "`" + `find . -name '*.go'` + "`" + `) → use shell instead.

### When to fork (omit subagent_type)

- Fork when the intermediate tool output isn't worth keeping in your context — "will I need this output again", not task size.
- Research: fork open-ended questions. If research can be broken into independent questions, launch parallel forks in one message.
- Implementation: prefer to fork work that requires more than a couple of edits.
- Fork results are returned synchronously — wait for the tool result before acting on the fork's findings.

### When to use a cold agent (with subagent_type)

- Use a cold agent when you need an independent perspective — e.g. code review, where the agent should not see your own analysis.
- Use Explore for read-only codebase exploration — it is faster and cannot modify files.
- Use general-purpose when the task needs a different tool set or permission mode than the parent.

### Writing the prompt

- The description parameter is a 3-5 word task label (e.g. "Fix login bug", "Audit auth flow") — not a full sentence.
- Cold agents (with subagent_type): brief like a smart colleague who just walked in — explain what you're trying to accomplish, what you've learned, and why it matters.
- Fork prompts (omit subagent_type): write as a directive — the fork inherits your context. Be specific about scope; don't re-explain background.
- Never delegate understanding. Include file paths, line numbers, what specifically to change. Don't write "based on your findings, fix the bug."

## Plan Mode

- Call enter_plan_mode ONLY when you need to implement a complex feature or refactoring (3+ files, architectural decisions, multiple valid approaches).
- Do NOT use plan mode for: code review, bug analysis, performance investigation, explaining code, answering questions, or any task that does not involve writing implementation code.
- Skip for single-file fixes, trivial bugs, or when the user gives precise step-by-step instructions.
- Once in plan mode, follow the instructions in the [plan:start] system message.

## Coding standards

- The first user message in every conversation is the project's AGENTS.md — project-specific rules with the same binding force as this system prompt. Before writing or editing any code, scan AGENTS.md for rules relevant to the current task (build commands, test conventions, commit format, file layout, naming, etc.) and apply them. AGENTS.md and system prompt are cumulative — when they truly conflict, system prompt wins, but only for the specific point of conflict, not the entire file.
- Follow existing codebase conventions and linter configurations.
- Write clear, self-documenting names. Avoid abbreviations.
- Keep changes minimal — no unnecessary refactors or rewrites.

## Termination

- Stop and report completion when the user's request is fully satisfied.
- If you cannot complete a task, explain the bottleneck concisely and propose next steps.
- Do NOT loop on the same sub-task repeatedly. If stuck, ask for guidance.

## Tool Error Handling

- On error, identify the kind, then decide: retry once or stop.
- Fatal (do not retry): permission_denied, security_violation, disk_full.
- Recoverable (retry once with corrected input): command_failed, command_not_found, command_permission_denied, timeout, file_not_found, invalid_args, no_match, no_results, not_dir, binary_file, multiple_matches.
- For not_dir: the error message includes a directory listing and may suggest a specific file (Did you mean). Pick a file from the listing or use the suggestion, then retry immediately.
- For file_not_found: the error message includes CWD and may suggest a similar path (Did you mean). Use the suggested path, or use shell('find') to locate the correct file.
- For binary_file: the file is not a readable text file — verify you have the correct filename; use shell('ls') to check the directory contents.
- For no_match: the error includes a hint with the closest matching lines and line numbers — use read_file to verify the exact content at those lines, then copy text verbatim (including indentation).
- For multiple_matches: the error shows each match location with surrounding context and line numbers. Pick one occurrence and include 1-2 unique surrounding lines in your old_string to disambiguate.
- For no_results: the skill was not found or not applicable — try a different skill name or check available skills.

## Backoff & loop protection

- The loop tracks consecutive turns where ALL tool calls fail with the same (tool, error_kind) pair and NO tool succeeds. For example: bash + command_not_found, read_file + file_not_found.
- Changing the tool OR changing the error kind resets the counter — the loop recognizes this as a strategy pivot and does not penalize it.
- Any successful tool call resets the counter entirely.
- At 3 consecutive failures with the same (tool, kind), you receive a [system] warning. At 5, a stronger warning. At 8, the loop terminates to prevent infinite retries.
- **You should change your approach before the warning appears.** After any tool fails twice with the same error:
  - Try a different tool to achieve the same goal.
  - Try the same tool with substantially different arguments (different path, different command, different pattern).
  - If neither works, stop and ask the user for guidance.`

// ---------------------------------------------------------------------------
// 自定义消息类型
// ---------------------------------------------------------------------------

// maxParas 是段落列表的硬上限，超出时从头部淘汰旧段落。
// 200 个段落 ≈ 40–60 个典型 turn，保证渲染性能稳定。
const maxParas = 200

// maxToolResultBytes 是单个工具结果的最大存储字节数。
// 超出部分截断，展开时被截断内容不可见，
// 但完整结果已通过 agent loop 传递给 LLM，不影响上下文。
const maxToolResultBytes = 100 * 1024 // 100 KB

// buildSystemPrompt 构造完整的系统提示词。
// CWD 在会话期间固定，不存在 cd 工具。
func buildSystemPrompt(cwd string, loc Locale) string {
	prompt := defaultSystemPrompt
	// 根据 locale 替换 Personality 中的语言指令
	switch loc {
	case LocaleEnUS:
		prompt = strings.Replace(prompt,
			"- Communicate in Chinese when addressing the user; keep English code and terminal output as-is.",
			"- Communicate in English when addressing the user.", 1)
	default:
		// zh-CN / auto → 保持中文指令不变
	}
	cwdInfo := fmt.Sprintf(`

## Workspace

Current working directory: %s
All file paths are resolved relative to this directory unless a working_dir is specified.

### Working Directory Rules

- The workspace directory is the default base for all operations — not a boundary. You may read, write, and execute in any directory.
- Shell commands run in isolated subprocesses — "cd" inside a shell command has NO effect on subsequent commands. Use the working_dir parameter to change the execution directory per command.
- To operate in a different directory, use the working_dir parameter: {"command":"ls", "working_dir":"/project"} (Unix/macOS) or {"command":"ls", "working_dir":"C:/project"} (Windows).
`, cwd)
	return prompt + cwdInfo
}

// keyMap 定义所有快捷键绑定。
type keyMap struct {
	Enter         key.Binding
	Interrupt     key.Binding
	Quit          key.Binding
	FocusNext     key.Binding
	FocusPrev     key.Binding
	Up            key.Binding
	Down          key.Binding
	PageUp        key.Binding
	PageDown      key.Binding
	ToggleTheme   key.Binding
	JumpBottom    key.Binding
	Picker        key.Binding
	Paste         key.Binding
}

// permKeyBindings / questionSingleKeyBindings 等已移至 tui_overlay.go，
// 作为接受 *Messages 的函数实现，支持国际化。

var defaultKeys = keyMap{
	Enter:         key.NewBinding(key.WithKeys("enter"), key.WithHelp("⏎", "发送消息")),
	Interrupt:     key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", "中断 agent loop")),
	Quit:          key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("Ctrl+C", "退出")),
	FocusNext:     key.NewBinding(key.WithKeys("tab"), key.WithHelp("Tab", "聚焦下一个可交互段落")),
	FocusPrev:     key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("Shift+Tab", "聚焦上一个可交互段落")),
	Up:            key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "向上滚动")),
	Down:          key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "向下滚动")),
	PageUp:        key.NewBinding(key.WithKeys("pgup"), key.WithHelp("PgUp", "向上翻页")),
	PageDown:      key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("PgDn", "向下翻页")),
	ToggleTheme:   key.NewBinding(key.WithKeys("ctrl+g"), key.WithHelp("Ctrl+G", "切换主题 (dark/light/auto)")),
	JumpBottom:    key.NewBinding(key.WithKeys("ctrl+e", "end"), key.WithHelp("Ctrl+E/End", "跳到底部")),
	Picker:        key.NewBinding(key.WithKeys("@"), key.WithHelp("@", "选择文件/目录")),
	Paste:         key.NewBinding(key.WithKeys("ctrl+v"), key.WithHelp("Ctrl+V", "粘贴")),
}

// makeKeyMap 根据 locale 生成带翻译帮助文本的 keyMap。
func makeKeyMap(lc *Messages) keyMap {
	return keyMap{
		Enter:       key.NewBinding(key.WithKeys("enter"), key.WithHelp("⏎", lc.KeySend)),
		Interrupt:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", lc.KeyInterrupt)),
		Quit:        key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("Ctrl+C", lc.KeyQuit)),
		FocusNext:   key.NewBinding(key.WithKeys("tab"), key.WithHelp("Tab", lc.KeyFocusNext)),
		FocusPrev:   key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("Shift+Tab", lc.KeyFocusPrev)),
		Up:          key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", lc.KeyScrollUp)),
		Down:        key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", lc.KeyScrollDown)),
		PageUp:      key.NewBinding(key.WithKeys("pgup"), key.WithHelp("PgUp", lc.KeyPageUp)),
		PageDown:    key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("PgDn", lc.KeyPageDown)),
		ToggleTheme: key.NewBinding(key.WithKeys("ctrl+g"), key.WithHelp("Ctrl+G", lc.KeyToggleTheme)),
		JumpBottom:  key.NewBinding(key.WithKeys("ctrl+e", "end"), key.WithHelp("Ctrl+E/End", lc.KeyJumpBottom)),
		Picker:      key.NewBinding(key.WithKeys("@"), key.WithHelp("@", lc.KeyPicker)),
		Paste:       key.NewBinding(key.WithKeys("ctrl+v"), key.WithHelp("Ctrl+V", lc.KeyPaste)),
	}
}

// permissionReqMsg 权限确认请求。
type permissionReqMsg struct {
	toolName   string
	args       string
	reason     string
	reasonKind permission.DecisionReason
	reply      chan<- permission.UserChoice
}

// questionReqMsg AskUserQuestion 请求。
type questionReqMsg struct {
	questions []permission.QuestionPrompt
	reply     chan<- []permission.QuestionResponse
}

// planEnterReqMsg 进入 plan 模式确认请求。
type planEnterReqMsg struct {
	reply chan<- bool
}

// planExitReqMsg 退出 plan 模式审批请求（含 plan 内容）。
type planExitReqMsg struct {
	plan  string
	reply chan<- permission.PlanApproval
}

// enterPlanModeByUserMsg 用户通过 Shift+Tab 主动进入 plan 模式的消息。
type enterPlanModeByUserMsg struct{}

// exitPlanModeByUserMsg 用户通过审批界面批准退出 plan 模式的消息。
type exitPlanModeByUserMsg struct{}

// pickerScanDoneMsg 文件扫描完成消息（异步）。
type pickerScanDoneMsg struct {
	items []pickerItem
	gen   int // 扫描代数，用于丢弃过期结果
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

// pickerItem 表示文件选择器中的一个候选项。
type pickerItem struct {
	Path    string // 相对于 cwd 的路径
	IsDir   bool   // 是否为目录
	Display string // 渲染用的显示文本（目录带 / 后缀）
}

// fileItem 是文件选择器列表项，实现 list.DefaultItem 接口。
type fileItem struct {
	path    string
	display string
	isDir   bool
}

func (i fileItem) Title() string       { return i.display }
func (i fileItem) Description() string { return "" }
func (i fileItem) FilterValue() string { return i.display }

// permItem 是权限面板列表项，实现 list.DefaultItem 接口。
type permItem struct {
	title       string
	description string
	choice      permChoice
}

func (i permItem) Title() string       { return i.title }
func (i permItem) Description() string { return i.description }
func (i permItem) FilterValue() string { return i.title }

// themeItem 是主题选择器列表项。
type themeItem struct {
	label string
	mode  string
}

func (i themeItem) Title() string       { return i.label }
func (i themeItem) Description() string { return "" }
func (i themeItem) FilterValue() string { return i.label }

// modelPickerItem 是模型选择器列表项。
type modelPickerItem struct {
	modelID  string
	ownedBy  string
}

func (i modelPickerItem) Title() string       { return i.modelID }
func (i modelPickerItem) Description() string { return i.ownedBy }
func (i modelPickerItem) FilterValue() string { return i.modelID }

// commandPickerItem 是 slash 命令选择器列表项。
type commandPickerItem struct {
	name        string
	aliases     []string
	description string
	args        string // 参数占位符，如 "model"；无参数时为空
}

func (i commandPickerItem) Title() string {
	label := i.name
	if i.args != "" {
		label = i.name + " [" + i.args + "]"
	}
	if len(i.aliases) > 0 {
		label += " / " + strings.Join(i.aliases, " / ")
	}
	return "/" + label + " " + i.description
}
func (i commandPickerItem) Description() string { return "" }
func (i commandPickerItem) FilterValue() string { return i.name }

type model struct {
	// 外部依赖
	cm            *ctxpkg.ContextManager
	llmClient     llm.Client
	registry      tool.Registry
	guard         permission.Guard
	expander      *reference.Expander
	slashRegistry *slashcommand.Registry
	skillLoader   *skill.Loader
	cwd           string
	verboseLog io.Writer // --verbose 日志输出（nil = 不记录）
	loop       *agentloop.Loop

	// 国际化
	lc *Messages // 当前语言的文案实例（nil 时回退到 enUS）

	// slash command 共享消息引用（locale 切换时原地更新以保持命令描述一致）
	slashMessages *slashcommand.SlashMessages

	// 段落模型（消息内容的数据源）
	paras []Paragraph

	// Glamour markdown 渲染器
	glamourRenderer *glamour.TermRenderer

	// 覆盖层状态
	overlay      Overlay
	permReq      *permissionReqMsg     // 当前待确认的权限请求
	permList     list.Model            // 权限选项列表（bubbles/list）
	permDelegate *list.DefaultDelegate // 权限列表的 delegate 指针，主题切换时更新样式

	questionReq         *questionReqMsg            // 当前待回答的问题
	questionIdx         int                        // 当前问题索引（0-based）
	questionAnswers     []permission.QuestionResponse // 已收集的答案
	questionForm        *huh.Form                  // huh 表单（选择题部分）
	questionFormMaxHeight int                      // 表单自适应最大高度（resize 时复用）
	questionPendingOther    bool                   // 是否等待 Other 自定义输入
	questionPendingAnswers []string                // Other 输入前的临时答案（多选时合并用）
	questionFormInitCmd tea.Cmd                    // 待返回的表单 Init 命令
	questionFormIsOther bool                       // 当前是否展示 Other 自定义输入（直接用 textinput，非 huh）

	// 主题选择器覆盖层
	themeList     list.Model
	themeDelegate *list.DefaultDelegate

	// 模型选择器覆盖层
	modelPickerList     list.Model
	modelPickerDelegate *list.DefaultDelegate
	modelPickerItems    []llm.ModelInfo // 模型列表数据，主题切换时更新样式

	// 语言选择器覆盖层
	localeList     list.Model
	localeDelegate *list.DefaultDelegate

	// 文件选择器
	pickerVisible         bool
	pickerFilter          string
	pickerItems           []pickerItem
	pickerAllItems        []pickerItem
	pickerScanGen         int                   // 扫描代数，每次发起异步扫描时递增，用于丢弃过期结果
	pickerScanCancel      context.CancelFunc    // 取消上一次未完成的扫描
	pickerLastScannedBase string                // 上次触发磁盘扫描的 filepath.Base(filter)，base 未变则跳过扫描
	pickerDismissValue    string                // 选择器关闭时的 input 值，防止立即重新触发
	pickerLastValue       string                // 上次刷新时的 input 值，避免 spinner tick 触发重复扫描
	pickerScanning        bool                  // 是否正在异步扫描中（用于显示加载状态）
	pickerList            list.Model            // 文件列表组件（bubbles/list）
	pickerDelegate        *list.DefaultDelegate // 文件选择器 delegate 指针，主题切换时更新样式

	// 命令选择器（/ 触发）
	commandPickerVisible      bool
	commandPickerList         list.Model
	commandPickerDelegate     *list.DefaultDelegate
	commandPickerItems        []slashcommand.CommandInfo
	commandPickerFilter       string
	commandPickerDismissValue string
	commandPickerLastValue    string

	// HUD 会话级累积（footer 显示用，跨 loop 不归零）
	hudModel      string
	hudTurns      int
	hudMessages   int
	hudCacheHit   int
	hudCacheMiss  int
	hudLatMs      int64
	turnStartTime time.Time // 本轮启动时间，用于计算延迟

	// loop 级增量（透传给 CompleteRun → cm.stats，loop 结束归零）
	loopPrompt     int
	loopCompl      int
	loopCacheHit   int
	loopCacheMiss  int
	loopReasoning  int
	lastTurnPrompt int // 本 loop 最后一个 TurnStats 的 PromptTokens（供 CompleteRun 透传）
	lastTurnCompl  int // 本 loop 最后一个 TurnStats 的 CompletionTokens

	hudBalance      string // 余额显示字符串，空表示未查询或不支持
	hudBalanceAvail bool   // 账户是否有可用余额

	contextLimit     int // 上下文窗口 token 上限
	maxTurns         int // --max-turns（0 = 无限制）
	toolTimeout      time.Duration // --tool-timeout（0 = 无限制）
	toolTimeoutSource string      // 超时配置来源（CLI / settings.json / 默认）
	bypassPerm       bool
	lastPromptTokens int // ctx bar 实时值（TurnStats → API 真实值，Tier 3 完成 → 归零）

	// Session 管理
	agentsMdText  string // AGENTS.md 文本，/new 时重新注入
	sessionDir    string // session 文件目录
	settingsStore *tuiSettingsStore // /model settings 读写

	// Transcript 持久化
	transcriptPath    string // session_id.jsonl 文件路径
	transcriptWritten int    // 已写入 transcript 的段落数

	// Bubbletea 组件
	program        *tea.Program // 在 main 中注入，用于 goroutine → TUI 通信
	keys           keyMap
	help           help.Model
	spinner        spinner.Model  // 通用 spinner（HUD 加载指示）
	focusIndex     int            // 段落焦点：-1 = 输入框，>=0 = 段落索引
	spAsst         spinner.Model  // assistant 流式前缀动画
	spThought      spinner.Model  // thought 流式前缀动画
	spTool         spinner.Model  // tool 执行中前缀动画
	spSubagent     spinner.Model  // subagent 执行中前缀动画（独立视觉）
	ctxProgress    progress.Model // ctx 窗口进度条（bubbles progress 组件）
	input          textarea.Model

	otherInput          textinput.Model // Other 自定义输入框
	otherInputVisStart  int             // otherInput 水平滚动起始偏移
	otherInputLastValue string          // otherInput 上一次同步值

	// 输入历史
	inputHistory []string // 已提交的输入，最新在前
	historyPos   int      // 当前历史位置（-1 = 不在历史导航中）
	historyDraft string   // 进入历史导航前输入框中的草稿文本

	// 双击 Esc 清空输入
	lastEscTime time.Time // 上次在空闲态按 Esc 的时间

	// 状态
	running       bool               // agent loop 正在执行中
	inPlanMode    bool               // 当前是否在 plan 模式
	planEnteredByUser bool           // plan 模式由用户快捷键进入（true）还是 LLM 调用 enter_plan_mode（false）
	planFile      string             // plan 文件路径
	planPairID    string             // START/END 配对 ID
	planEnterReply   chan<- bool              // plan 进入确认的 reply channel
	planExitReply  chan<- permission.PlanApproval // plan 退出审批的 reply channel
	planExitPending bool              // 用户快捷键退出 plan，下轮需注入 [plan:end]
	planExitPendingPairID string     // 待注入的配对 ID
	planStartSent  bool              // [plan:start] 已注入消息历史（退出时需配对 [plan:end]）
	cancelRun     context.CancelFunc // 取消当前运行的 agent loop（nil 表示无运行中 loop）
	runGeneration int                // 每次 doTurn 递增，闭包捕获后用于 LoopDone 去重
	themeMode     string             // 当前主题模式: auto / dark / light
	autoDark      bool               // 启动时 auto 检测结果：true = 深色背景，缓存避免运行时调用 HasDarkBackground
	palette   palette            // 当前配色
	width     int
	height    int

	// 滚动状态
	scrollTop      int  // body 内容区第一可见行在 lines 中的索引
	pinnedToBottom bool // 是否锁定在底部（自动跟随最新内容）
	bodyHeight     int  // 上次渲染时 body 区域可用高度（行数），用于翻页计算

	updateCache environment.UpdateCache // 版本更新检查结果缓存

	// 全局通知（footer banner）
	noticeBanner  string // 非空时在 footer 显示通知（版本更新等）
	updating      bool   // 更新进行中
	latestVersion string // 缓存的最新版本号
}

// ---------------------------------------------------------------------------
// 构造函数
// ---------------------------------------------------------------------------
// Glamour 样式
// ---------------------------------------------------------------------------

// waveloomGlamourStyle 返回基于当前 Waveloom 色板定制的 Glamour 样式配置。
// Margin 统一清零（由 TUI 前缀缩进控制对齐），关键块级颜色映射到 Waveloom 色板。
func waveloomGlamourStyle(p palette) ansi.StyleConfig {
	var base ansi.StyleConfig
	if p.GlamourStyle == "light" {
		base = glamourstyles.LightStyleConfig
	} else {
		base = glamourstyles.DarkStyleConfig
	}

	// 清零 margin
	zero := uint(0)
	emptyHex := ""
	base.Document.Margin = &zero
	base.CodeBlock.Margin = &zero
	base.Paragraph.Margin = &zero
	base.Heading.Margin = &zero

	// 提取 Waveloom 色板 hex 值
	toolCode := colorHex(p.ToolCode)
	toolCodeBg := colorHex(p.ToolCodeBg)
	accent := colorHex(p.AccentGold)
	headerAccent := colorHex(p.HeaderAccent)

	// 行内代码 → ToolCode / ToolCodeBg
	base.Code.Color = &toolCode
	base.Code.BackgroundColor = &toolCodeBg

	// 代码块文本 → ToolCode
	base.CodeBlock.Color = &toolCode

	// 代码块 Chroma 背景 → ToolCodeBg，文本 → ToolCode
	if base.CodeBlock.Chroma != nil {
		base.CodeBlock.Chroma.Background.BackgroundColor = &toolCodeBg
		base.CodeBlock.Chroma.Text.Color = &toolCode

		// 覆盖 Chroma Error token 样式：默认 Dark/Light 主题使用红底白字
		// （#F05B5B / #FF5555 背景），在 Waveloom 主题中不适用。
		// 改为无色背景、前景用工具代码色，避免刺眼的红色底色块。
		base.CodeBlock.Chroma.Error.Color = &toolCode
		base.CodeBlock.Chroma.Error.BackgroundColor = &emptyHex
	}

	// 标题 → HeaderAccent
	base.Heading.Color = &headerAccent

	// H1 背景 → AccentGold
	base.H1.BackgroundColor = &accent

	// 链接 → AccentGold
	base.Link.Color = &accent
	base.LinkText.Color = &accent

	return base
}

// colorHex 从 color.Color 提取 hex 字符串。
func colorHex(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", uint8(r>>8), uint8(g>>8), uint8(b>>8))
}

// ---------------------------------------------------------------------------

// newTUIModel 创建 TUI model，依赖由外部注入（LLM client / tool registry / guard / expander / verboseLog / locale）。
func newTUIModel(llmClient llm.Client, registry tool.Registry, guard permission.Guard, expander *reference.Expander, modelName string, theme string, verboseLog io.Writer, contextLimit int, maxTurns int, toolTimeout time.Duration, toolTimeoutSource string, loc Locale) *model {
	if modelName == "" {
		modelName = "deepseek-v4"
	}

	cwd, _ := os.Getwd()
	cm := ctxpkg.New(buildSystemPrompt(cwd, loc))
	lc := messagesFor(loc)

	ti := textarea.New()
	ti.Placeholder = lc.InputPlaceholder
	ti.CharLimit = 2048
	ti.ShowLineNumbers = false
	ti.MaxHeight = 2
	ti.EndOfBufferCharacter = ' '
	ti.SetPromptFunc(2, func(_ textarea.PromptInfo) string {
		return "  "
	})
	ti.SetHeight(2)
	ti.SetWidth(0)
	ti.SetVirtualCursor(false) // real cursor 避免 virtual cursor 反色 ANSI 泄漏
	ti.Focus()

	// Other 自定义输入框（与主输入框同款 real cursor 模式）
	otherTi := textinput.New()
	otherTi.Prompt = "> "
	otherTi.Placeholder = lc.InputOtherPlaceholder
	otherTi.CharLimit = 200
	otherTi.SetVirtualCursor(false)
	otherStyles := otherTi.Styles()
	otherStyles.Cursor.Blink = true
	otherTi.SetStyles(otherStyles)

	// 初始化 bubbles spinner 组件（通用 HUD）
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(darkPalette.OK) // 初始值，initTheme 同步

	// 初始化 assistant 流式前缀 spinner（MiniDot — 微妙不抢眼）
	spAsst := spinner.New()
	spAsst.Spinner = spinner.MiniDot
	spAsst.Style = lipgloss.NewStyle().Foreground(darkPalette.OK) // 初始值，initTheme 同步

	// 初始化 thought 流式前缀 spinner（Points — 点填充感，暗示思考）
	spThought := spinner.New()
	spThought.Spinner = spinner.Points
	spThought.Style = lipgloss.NewStyle().Foreground(darkPalette.Gray) // 初始值，initTheme 同步

	// 初始化 tool 执行中前缀 spinner（Line — 经典旋转，清晰表示进行中）
	spTool := spinner.New()
	spTool.Spinner = spinner.Line
	spTool.Style = lipgloss.NewStyle().Foreground(darkPalette.Gray) // 初始值，initTheme 同步

	// 初始化 subagent 执行中前缀 spinner（Jump — 横向脉冲，区分子 agent 与普通工具）
	spSubagent := spinner.New()
	spSubagent.Spinner = spinner.Jump
	spSubagent.Style = lipgloss.NewStyle().Foreground(darkPalette.Gray) // 初始值，initTheme 同步

	// 初始化 ctx 进度条（bubbles progress 组件，全块字符 █  提供清晰的逐格填充）
	// 宽度在 renderCtxBarCompact() 中每次设置（20 列，每列 5%）。
	cp := progress.New(
		progress.WithFillCharacters('█', '░'),
		progress.WithColorFunc(func(total, current float64) color.Color {
			if total < 0.5 {
				return colorOK
			}
			if total < 0.8 {
				return colorAccentGold
			}
			return colorErr
		}),
		progress.WithoutPercentage(),
	)
	cp.EmptyColor = darkPalette.FooterFg

	// 初始化 Glamour markdown 渲染器（宽度在首个 WindowSizeMsg 后调整）
	glamourR, err := glamour.NewTermRenderer(
		glamour.WithWordWrap(80),
		glamour.WithStyles(waveloomGlamourStyle(darkPalette)),
	)
	if err != nil {
		// 降级：nil renderer 时回退到纯文本
		glamourR = nil
	}

	return &model{
		cm:              cm,
		llmClient:       llmClient,
		registry:        registry,
		guard:           guard,
		expander:        expander,
		cwd:             cwd,
		verboseLog:      verboseLog,
		hudModel:        normalizeWidth(modelName),
		contextLimit:    contextLimit,
		maxTurns:        maxTurns,
		toolTimeout:     toolTimeout,
		toolTimeoutSource: toolTimeoutSource,
		glamourRenderer: glamourR,
		keys:            makeKeyMap(lc),
		help:            help.New(),
		spinner:         sp,
		spAsst:          spAsst,
		spThought:       spThought,
		spTool:          spTool,
		spSubagent:      spSubagent,
		ctxProgress:     cp,
		input:           ti,
		otherInput:      otherTi,
		historyPos:      -1,
		overlay:         overlayNone,
		themeMode:       theme,
		palette:         darkPalette, // 默认，initTheme 覆盖
		focusIndex:      -1,
		pinnedToBottom:  true,
		lc:              lc,
	}
}

// msg 返回当前语言的 Messages 实例，nil 时回退 enUS。
func (m *model) msg() *Messages {
	if m.lc != nil {
		return m.lc
	}
	return &enUS
}

// wireLoop 在 model 创建后、program 注入后调用，创建 agent loop 并注入 tuiUserResponder。
func (m *model) wireLoop() {
	guard := m.guard
	if m.bypassPerm {
		guard = permission.NewGuard(permission.WithBypassMode(true))
	}
	m.loop = agentloop.New(m.llmClient, m.registry, agentloop.Config{
		MaxTurns:      m.maxTurns,
		SystemPrompt:  "",
		Guard:         guard,
		UserResponder: &tuiUserResponder{program: m.program, cwd: m.cwd},
		VerboseWriter: m.verboseLog,
		Compactor:     m.cm.Compactor(),
		ToolTimeout:   m.toolTimeout,
		AgentsMD:      m.agentsMdText,
		EventCallback: func(ev agentloop.TurnEvent) {
			if m.program != nil {
				m.program.Send(ev)
			}
		},
	})
}

// ---------------------------------------------------------------------------
// bubbletea.Model
// ---------------------------------------------------------------------------

func (m *model) Init() tea.Cmd {
	return tea.Batch(
		m.input.Focus(),
		textarea.Blink,
		m.spinner.Tick,
		m.spAsst.Tick,
		m.spThought.Tick,
		m.spTool.Tick,
		m.spSubagent.Tick,
		m.checkUpdateCmd(),
	)
}

// ---------------------------------------------------------------------------
// 更新检查
// ---------------------------------------------------------------------------

type updateCheckMsg struct {
	info *environment.UpdateInfo
}

// updateProgressMsg 更新进度推送（下载/解压/安装）。
type updateProgressMsg struct {
	phase    string  // "download", "extract", "install", "done"
	pct      int     // 0-100
	detail   string  // 人类可读的进度描述
	err      string  // 非空表示失败
}

// updateDoneMsg 更新流程完成。
type updateDoneMsg struct {
	err   string // 非空表示失败
	durMs int64  // 更新耗时（毫秒）
}

func (m *model) checkUpdateCmd() tea.Cmd {
	return func() tea.Msg {
		info, err := environment.CheckForUpdate(context.Background(), Version)
		if err != nil || info == nil {
			return updateCheckMsg{}
		}
		return updateCheckMsg{info: info}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	// ------------------------------------------------------------------
	// 更新检查
	// ------------------------------------------------------------------
	case updateCheckMsg:
		if msg.info != nil {
			m.updateCache.Set(msg.info)
			if msg.info.UpdateAvailable {
				m.noticeBanner = fmt.Sprintf(m.msg().UpdateAvailable, msg.info.LatestVersion)
				m.latestVersion = msg.info.LatestVersion
			}
		}
		return m, nil

	// ------------------------------------------------------------------
	// 更新进度
	// ------------------------------------------------------------------
	case updateProgressMsg:
		m.handleUpdateProgress(msg)
		return m, nil

	case updateDoneMsg:
		m.handleUpdateDone(msg)
		return m, nil

	// ------------------------------------------------------------------
	// 窗口尺寸
	// ------------------------------------------------------------------
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		contentWidth := max(msg.Width-4, 20)

		m.input.SetWidth(contentWidth)

		// 重建 Glamour renderer 以适配新宽度
		if m.glamourRenderer != nil {
			_ = m.glamourRenderer.Close()
		}
		glamourR, err := glamour.NewTermRenderer(
			glamour.WithWordWrap(max(m.width-6, 20)),
			glamour.WithStyles(waveloomGlamourStyle(m.palette)),
		)
		if err == nil {
			m.glamourRenderer = glamourR
		}

		return m, nil

	// ------------------------------------------------------------------
	// 鼠标 — 滚轮仅滚动页面内容，不触发输入历史、权限面板、文件选择器。
	// 文本选择请使用 Shift+点击（终端标准惯例）。
	// ------------------------------------------------------------------
	case tea.MouseMsg:
		mouse := msg.Mouse()
		switch mouse.Button {
		case tea.MouseWheelUp:
			m.scrollUp(3)
		case tea.MouseWheelDown:
			m.scrollDown(3)
		}
		return m, nil

	// ------------------------------------------------------------------
	// 键盘 — 先处理全局/覆盖层快捷键，未消费则传给子组件
	// ------------------------------------------------------------------
	case tea.KeyPressMsg:
		if handled, cmd := m.handleKeyPress(msg); handled {
			return m, cmd
		}
		// 未消费 → 继续执行下方子组件更新（input / viewport）

	// ------------------------------------------------------------------
	// Spinner 帧动画（全部 4 个 spinner 统一路由）
	// ------------------------------------------------------------------
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)

		m.spAsst, cmd = m.spAsst.Update(msg)
		cmds = append(cmds, cmd)

		m.spThought, cmd = m.spThought.Update(msg)
		cmds = append(cmds, cmd)

		m.spTool, cmd = m.spTool.Update(msg)
		cmds = append(cmds, cmd)

		m.spSubagent, cmd = m.spSubagent.Update(msg)
		cmds = append(cmds, cmd)

		return m, tea.Batch(cmds...)

	// ------------------------------------------------------------------
	// Agent Loop 流式事件（由 goroutine 通过 p.Send 推送）
	// ------------------------------------------------------------------
	case agentloop.StreamDelta:
		m.handleStreamDelta(msg)
		m.flushTranscript()
		return m, nil

	case agentloop.ToolCallStart:
		m.handleToolStart(msg)
		m.flushTranscript()
		return m, nil

	case agentloop.ToolCallStream:
		m.handleToolStream(msg)
		return m, nil

	case agentloop.ToolCallResult:
		m.handleToolResult(msg)
		m.flushTranscript()
		return m, nil

	case agentloop.TurnStats:
		m.loopPrompt += msg.PromptTokens // per-loop 增量 → CompleteRun
		m.loopCompl += msg.CompletionTokens
		m.loopCacheHit += msg.CacheHitTokens
		m.loopCacheMiss += msg.CacheMissTokens
		m.loopReasoning += msg.ReasoningTokens
		m.hudCacheHit += msg.CacheHitTokens // 会话级累积 → footer
		m.hudCacheMiss += msg.CacheMissTokens

		// ctx bar：有压缩 → 用估算值（PromptTokens - TokensSaved）；无压缩 → 用 API 真实值
		if msg.PromptTokens > 0 {
			if msg.Compaction.HasCompaction() {
				m.lastPromptTokens = msg.PromptTokens - msg.Compaction.TokensSaved
				if m.lastPromptTokens < 0 {
					m.lastPromptTokens = 0
				}
			} else {
				m.lastPromptTokens = msg.PromptTokens
			}
			m.lastTurnPrompt = msg.PromptTokens
			m.lastTurnCompl = msg.CompletionTokens
		}
		if msg.Compaction.SummaryDone {
			m.lastPromptTokens = 0
		}
		if msg.Model != "" {
			m.hudModel = normalizeWidth(msg.Model)
		}
		if msg.MessageCount > 0 {
			m.hudMessages = msg.MessageCount
		}

		// 压缩通知段落
		if msg.Compaction.SummaryDone {
			m.paras = append(m.paras, Paragraph{
				Type:      paraSystem,
				State:     stateDone,
				Text:      m.msg().SysCompactionDone,
				NotifKind: notifInfo,
			})
			m.trimParas()
		}
		if msg.Compaction.HardLimitReached {
			msgText := m.msg().SysContextHardLimit
			msgKind := notifWarn
			if msg.Compaction.HardLimitReason == "tier3_failures" {
				msgText = m.msg().SysSummaryFailed
				msgKind = notifError
			}
			m.paras = append(m.paras, Paragraph{
				Type:      paraSystem,
				State:     stateDone,
				Text:      msgText,
				NotifKind: msgKind,
			})
			m.trimParas()
		}
		m.flushTranscript()
		return m, nil

	case agentloop.BalanceUpdate:
		if msg.Balance != nil {
			m.hudBalanceAvail = msg.Balance.IsAvailable
			m.hudBalance = formatBalance(msg.Balance)
		}
		return m, nil

	case agentloop.PlanModeEnter:
		m.inPlanMode = true
		m.planEnteredByUser = false
		m.planPairID = msg.PairID
		m.planStartSent = true
		m.input.Placeholder = m.msg().InputPlanModePlaceholder
		return m, nil

	case agentloop.PlanModeExit:
		if msg.Approved {
			m.inPlanMode = false
			m.planEnteredByUser = false
			m.planStartSent = false
			m.input.Placeholder = m.msg().InputPlaceholder
		}
		return m, nil

	case agentloop.LoopDoneWithGen:
		m.handleLoopDone(msg.LoopDone, msg.Generation)
		m.flushTranscript()
		return m, nil

	case agentloop.LoopDone:
		// 不应该走到这里——所有 LoopDone 都应该被 goroutine 包装为 LoopDoneWithGen。
		// 保留此分支作为防御（如单次模式 runner.go 直接使用 agentloop.LoopDone）。
		m.handleLoopDone(msg, 0)
		m.flushTranscript()
		return m, nil

	// ------------------------------------------------------------------
	// Subagent 事件（由 AgentTool 通过 EventCallback 推送）
	// ------------------------------------------------------------------
	case subagent.SubagentStart:
		m.handleSubagentStart(msg)
		return m, nil

	case subagent.SubagentEvent:
		m.handleSubagentEvent(msg)
		return m, nil

	case subagent.SubagentEnd:
		m.handleSubagentEnd(msg)
		m.flushTranscript()
		return m, nil

	// ------------------------------------------------------------------
	// 权限确认请求（由 tuiUserResponder 通过 p.Send 推送）
	// ------------------------------------------------------------------
	case permissionReqMsg:
		m.overlay = overlayPermission
		m.permReq = &msg
		m.input.Blur()
		m.permList = m.buildPermList()
		return m, nil

	// ------------------------------------------------------------------
	// AskUserQuestion 请求（由 tuiUserResponder.AnswerQuestion 推送）
	// ------------------------------------------------------------------
	case questionReqMsg:
		m.overlay = overlayQuestion
		m.questionReq = &msg
		m.questionIdx = 0
		m.questionAnswers = make([]agentloop.QuestionResponse, 0, len(msg.questions))
		m.input.Blur()
		m.buildQuestionForm()
		initCmd := m.questionFormInitCmd
		m.questionFormInitCmd = nil
		return m, initCmd

	// ------------------------------------------------------------------
	// Plan 模式进入确认 / 退出审批（由 tuiUserResponder 推送）
	// ------------------------------------------------------------------
	case planEnterReqMsg:
		m.overlay = overlayPlanEnter
		m.planEnterReply = msg.reply
		m.input.Blur()
		return m, nil

	case planExitReqMsg:
		// plan 内容作为段落插入消息流（Markdown 渲染），审批框仅显示确认提示
		m.paras = append(m.paras, Paragraph{
			Type:  paraAssistant,
			State: stateDone,
			Text:  msg.plan,
		})
		m.overlay = overlayPlanExit
		m.planExitReply = msg.reply
		m.input.Blur()
		return m, nil

	// ------------------------------------------------------------------
	// Plan 模式用户快捷键消息
	// ------------------------------------------------------------------
	case enterPlanModeByUserMsg:
		m.inPlanMode = true
		m.planEnteredByUser = true
		return m, nil

	case exitPlanModeByUserMsg:
		m.inPlanMode = false
		return m, nil

	// ------------------------------------------------------------------
	// 文件扫描完成（异步）
	// ------------------------------------------------------------------
	case pickerScanDoneMsg:
		// 丢弃过期扫描结果（用户输入已变化，发起了更新代数的新扫描）
		if msg.gen != m.pickerScanGen {
			return m, nil
		}
		m.pickerScanning = false
		m.pickerAllItems = msg.items
		m.pickerItems = fuzzyFilter(m.pickerFilter, m.pickerAllItems)
		m.buildPickerList()
		return m, nil

	// ------------------------------------------------------------------
	// 剪贴板
	// ------------------------------------------------------------------
	case tea.ClipboardMsg:
		if m.overlay == overlayNone && !m.running {
			insert := strings.Map(func(r rune) rune {
				if r == '\n' || r == '\r' {
					return ' '
				}
				return r
			}, msg.Content)
			m.input.InsertString(insert)
		}
		return m, nil
	}

	// 子组件更新（仅在无覆盖层时传递）
	switch m.overlay {
	case overlayNone:
		var taCmd tea.Cmd
		m.input, taCmd = m.input.Update(msg)
		cmds = append(cmds, taCmd)
	case overlayPermission:
		// 权限面板活跃时，将按键传给 list 组件（↑↓ 导航）
		var cmd tea.Cmd
		m.permList, cmd = m.permList.Update(msg)
		cmds = append(cmds, cmd)
	case overlayQuestion:
		// Other 自定义输入：直接用 textinput（real cursor 模式，与主输入框一致）
		if m.questionFormIsOther {
			var cmd tea.Cmd
			m.otherInput, cmd = m.otherInput.Update(msg)
			cmds = append(cmds, cmd)
			m.syncOtherInputVisibleStart()

			// Enter 提交（Esc 在 handleKeyPress 中拦截）
			if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
				if keyMsg.String() == "enter" {
					m.handleOtherInputSubmit()
				}
			}
		} else if m.questionForm != nil {
			// huh 表单活跃时，所有消息路由到 Form.Update。
			// WindowSizeMsg 需缩放高度：表单嵌在 overlay 盒子内，可用高度小于终端全高。
			formMsg := msg
			if wsMsg, ok := msg.(tea.WindowSizeMsg); ok {
				// resize 时用当前选项数重新计算自适应高度
				q := m.questionReq.questions[m.questionIdx]
				formMaxHeight := m.overlayMaxFormHeight(len(q.Options) + 1)
				m.questionFormMaxHeight = formMaxHeight
				formMsg = tea.WindowSizeMsg{Width: wsMsg.Width, Height: formMaxHeight}
			}
			fm, cmd := m.questionForm.Update(formMsg)
			m.questionForm = fm.(*huh.Form)
			cmds = append(cmds, cmd)
			// 检测表单完成/取消
			switch m.questionForm.State {
			case huh.StateCompleted:
				m.handleQuestionFormComplete()
			case huh.StateAborted:
				m.handleQuestionFormAborted()
			}
		}
		// 返回待处理的 Init 命令（新表单创建时设置）
		if m.questionFormInitCmd != nil {
			cmds = append(cmds, m.questionFormInitCmd)
			m.questionFormInitCmd = nil
		}
	case overlayThemePicker:
		var cmd tea.Cmd
		m.themeList, cmd = m.themeList.Update(msg)
		cmds = append(cmds, cmd)
	case overlayModelPicker:
		var cmd tea.Cmd
		m.modelPickerList, cmd = m.modelPickerList.Update(msg)
		cmds = append(cmds, cmd)
	case overlayLocalePicker:
		var cmd tea.Cmd
		m.localeList, cmd = m.localeList.Update(msg)
		cmds = append(cmds, cmd)
	}

	// 文件选择器：过滤同步 + 激活/关闭检测
	if m.pickerVisible {
		// 若用户删除了 @ 或输入已包含空格（路径已完成），则自动关闭
		if !shouldActivatePicker(m.input.Value()) {
			m.closePicker()
		} else if m.input.Value() != m.pickerLastValue {
			// 输入值变化 → 仅内存过滤，不重新扫描磁盘
			m.updatePickerFilter()
			m.pickerLastValue = m.input.Value()
		}
	} else if shouldActivatePicker(m.input.Value()) && !m.running && m.overlay == overlayNone && !m.commandPickerVisible {
		// 选择器刚关闭且 input 值未变 → 阻止立即重新触发
		if m.input.Value() != m.pickerDismissValue {
			m.activatePicker()
			cmds = append(cmds, m.startPickerScan())
		}
	}

	// 命令选择器：过滤同步 + 激活/关闭检测
	if m.commandPickerVisible {
		if !shouldActivateCommandPicker(m.input.Value()) {
			m.closeCommandPicker()
		} else if m.input.Value() != m.commandPickerLastValue {
			m.updateCommandPickerFilter()
			m.commandPickerLastValue = m.input.Value()
		}
	} else if shouldActivateCommandPicker(m.input.Value()) && !m.running && m.overlay == overlayNone && !m.pickerVisible {
		if m.input.Value() != m.commandPickerDismissValue {
			m.activateCommandPicker()
		}
	} else {
		// 输入不触发命令选择器时，清除 dismiss 记录，避免 Esc 后清空 / 再输入 / 无法激活
		m.commandPickerDismissValue = ""
	}

	return m, tea.Batch(cmds...)
}

// ---------------------------------------------------------------------------
// 键盘处理
// ---------------------------------------------------------------------------

// handleKeyPress 处理按键。返回 (handled, cmd)。
// handled=false 表示 key 未被消费，调用方应传给 input / viewport。
//
// 路由优先级：
//  1. 全局快捷键（所有状态/覆盖层下生效）：Quit, ToggleTheme, JumpBottom, PageUp/Down
//  2. 文件选择器活跃 → handlePickerKey
//  3. 命令选择器活跃 → handleCommandPickerKey
//  4. 权限面板活跃 → permList 导航 + handlePermKey / handleThemePickerKey / handleModelPickerKey
//  5. 正常模式快捷键
func (m *model) handleKeyPress(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	// =====================================================================
	// 1. 全局快捷键（所有状态/覆盖层下生效，行为高度统一）
	// =====================================================================
	switch {
	case key.Matches(msg, m.keys.Quit):
		return true, tea.Quit

	case key.Matches(msg, m.keys.ToggleTheme):
		m.toggleTheme()
		return true, nil
	}

	// =====================================================================
	// 2. 文件选择器活跃时路由
	// =====================================================================
	if m.pickerVisible {
		return m.handlePickerKey(msg)
	}

	// =====================================================================
	// 2b. 命令选择器活跃时路由
	// =====================================================================
	if m.commandPickerVisible {
		return m.handleCommandPickerKey(msg)
	}

	// =====================================================================
	// 3. 权限面板活跃时路由
	// =====================================================================
	if m.overlay == overlayPermission {
		keyStr := msg.String()
		// ↑↓ 仅导航权限列表，不透传给 viewport
		if keyStr == "up" || keyStr == "down" {
			var cmd tea.Cmd
			m.permList, cmd = m.permList.Update(msg)
			return true, cmd
		}
		return m.handlePermKey(keyStr)
	}

	// =====================================================================
	// 3a. 选择题面板活跃时路由
	// =====================================================================
	if m.overlay == overlayQuestion {
		// Esc → 拒绝回答（关闭 overlay，发送 nil）
		if key.Matches(msg, m.keys.Interrupt) {
			// Other 输入中取消 → 回退到选项列表
			if m.questionFormIsOther {
				m.handleOtherInputCancel()
				return true, nil
			}
			m.handleQuestionFormAborted()
			return true, nil
		}
		// 其余按键交由 huh 或 otherInput Update 处理
		return false, nil
	}

	// =====================================================================
	// 3b. 主题选择器活跃时路由
	// =====================================================================
	if m.overlay == overlayThemePicker {
		return m.handleThemePickerKey(msg)
	}

	// =====================================================================
	// 3c. 模型选择器活跃时路由
	// =====================================================================
	if m.overlay == overlayModelPicker {
		return m.handleModelPickerKey(msg)
	}

	// =====================================================================
	// 3d. 语言选择器活跃时路由
	// =====================================================================
	if m.overlay == overlayLocalePicker {
		return m.handleLocalePickerKey(msg)
	}

	// =====================================================================
	// 3e. Plan 进入确认活跃时路由
	// =====================================================================
	if m.overlay == overlayPlanEnter {
		keyStr := msg.String()
		switch keyStr {
		case "enter":
			m.overlay = overlayNone
			m.inPlanMode = true
			m.planEnteredByUser = false
			m.input.Placeholder = m.msg().InputPlanModePlaceholder
			// plan 文件路径和配对 ID 由 Loop 的 executeEnterPlanMode 生成，
			// [plan:start] 由 Loop 注入消息历史，TUI 不重复管理。
			if m.planEnterReply != nil {
				m.planEnterReply <- true
				m.planEnterReply = nil
			}
			m.input.Focus()
			return true, nil
		case "esc":
			m.overlay = overlayNone
			if m.planEnterReply != nil {
				m.planEnterReply <- false
				m.planEnterReply = nil
			}
			m.input.Focus()
			return true, nil
		}
		return false, nil
	}

	// =====================================================================
	// 3f. Plan 退出审批活跃时路由
	// =====================================================================
	if m.overlay == overlayPlanExit {
		keyStr := msg.String()
		switch keyStr {
		case "enter":
			m.overlay = overlayNone
			m.inPlanMode = false
			m.planStartSent = false
			m.guard.ExitPlanMode()
			m.input.Placeholder = m.msg().InputPlaceholder
			if m.planExitReply != nil {
				m.planExitReply <- permission.PlanApproval{Approved: true}
				m.planExitReply = nil
			}
			m.input.Focus()
			return true, nil
		case "esc":
			m.overlay = overlayNone
			m.input.Placeholder = m.msg().InputPlanModePlaceholder
			if m.planExitReply != nil {
				m.planExitReply <- permission.PlanApproval{Approved: false, Feedback: "user declined the plan"}
				m.planExitReply = nil
			}
			m.input.Focus()
			return true, nil
		}
		return false, nil
	}

	// =====================================================================
	// 4. 正常模式快捷键
	// =====================================================================
	switch {
	// 段落焦点导航（Tab / Shift+Tab），仅在空闲态生效
	case key.Matches(msg, m.keys.FocusNext):
		if m.running || m.overlay != overlayNone || m.pickerVisible {
			return false, nil
		}
		return true, m.focusNext()

	case key.Matches(msg, m.keys.FocusPrev):
		if m.running || m.overlay != overlayNone || m.pickerVisible {
			return false, nil
		}
		if m.focusIndex >= 0 {
			return true, m.focusPrev()
		}
		// 无焦点 → plan 模式切换
		if m.inPlanMode {
			// plan 模式中 → 直接退出（用户主动切换，不走审批）
			return true, m.exitPlanModeByUser()
		}
		// 非 plan 模式 → 直接进入 plan 模式
		return true, m.enterPlanModeByUser()

	case key.Matches(msg, m.keys.Enter):
		// 焦点在段落上 → 展开/折叠
		if m.focusIndex >= 0 && m.focusIndex < len(m.paras) {
			m.toggleParagraphFocus()
			return true, nil
		}
		if !m.running {
			userInput := strings.TrimSpace(m.input.Value())
			if userInput == "" {
			// 空 Enter + 更新 banner 可见 → 触发更新
			if m.noticeBanner != "" && !m.updating {
			return true, m.startUpdate()
		}
		return true, nil
	}
			if strings.EqualFold(userInput, "exit") {
				return true, tea.Quit
			}

			// Slash 命令拦截：以 / 开头的输入在到达 Agent Loop 之前执行
			if strings.HasPrefix(userInput, "/") {
				return true, m.handleSlashCommand(userInput)
			}

			// 硬临界值阻断：上下文已达上限时直接拒绝新 prompt
			if m.cm.Compactor().LastResult().HardLimitReached {
				reason := m.cm.Compactor().LastResult().HardLimitReason
				msg := m.msg().SysContextHardLimit
				msgKind := notifWarn
				if reason == "tier3_failures" {
					msg = m.msg().SysSummaryFailed
					msgKind = notifError
				}
				m.paras = append(m.paras, Paragraph{
					Type:      paraSystem,
					State:     stateDone,
					Text:      msg,
					NotifKind: msgKind,
				})
				return true, nil
			}

		m.input.Reset()
		return true, m.doTurn(userInput)
	}

// running == true：用户输入优先于 agent loop。
// 立即中断当前 loop，将输入内容作为新任务启动。
userInput := strings.TrimSpace(m.input.Value())
if userInput == "" {
	// 空输入 → 不做任何响应，不中断运行中的 session
	return true, nil
}
m.input.Reset()
return true, m.doTurn(userInput)

	case key.Matches(msg, m.keys.Interrupt):
		// 焦点在段落上 → 归位到输入框
		if m.focusIndex >= 0 {
			m.focusIndex = -1
			return true, m.exitFocusMode()
		}
		if m.running && m.cancelRun != nil {
			m.cancelRun()
			m.cancelRun = nil
			return true, nil
		}
	// 空闲态 + 通知 banner 可见 → 单击 Esc 关闭通知
	if !m.running && m.overlay == overlayNone && !m.pickerVisible && m.noticeBanner != "" && !m.updating {
		m.noticeBanner = ""
		m.lastEscTime = time.Time{}
		return true, nil
	}
		// 空闲态双击 Esc → 清空输入框
		if !m.running && m.overlay == overlayNone && !m.pickerVisible {
			now := time.Now()
			if !m.lastEscTime.IsZero() && now.Sub(m.lastEscTime) < 500*time.Millisecond {
				m.input.Reset()
				m.lastEscTime = time.Time{}
				return true, nil
			}
			m.lastEscTime = now
		}
		return false, nil

	case key.Matches(msg, m.keys.Up):
		// 焦点在段落上 → 移到上一个可交互段落
		if m.focusIndex >= 0 {
			return true, m.focusPrev()
		}
		// 空闲态 ↑ → 输入历史导航（仅在未处于历史导航中时尝试）
		if !m.running && m.overlay == overlayNone && !m.pickerVisible {
			if m.historyPos == -1 || m.historyPos < len(m.inputHistory)-1 {
				if m.navigateHistoryUp() {
					return true, nil
				}
			}
		}
		// 未消费 → 向上滚动
		m.scrollUp(1)
		return true, nil

	case key.Matches(msg, m.keys.Down):
		// 焦点在段落上 → 移到下一个可交互段落
		if m.focusIndex >= 0 {
			return true, m.focusNext()
		}
		// 空闲态 ↓ → 输入历史导航
		if !m.running && m.overlay == overlayNone && !m.pickerVisible {
			if m.navigateHistoryDown() {
				return true, nil
			}
		}
		// 未消费 → 向下滚动
		m.scrollDown(1)
		return true, nil

	case key.Matches(msg, m.keys.PageUp):
		m.scrollUp(m.bodyHeight)
		return true, nil

	case key.Matches(msg, m.keys.PageDown):
		m.scrollDown(m.bodyHeight)
		return true, nil

	case key.Matches(msg, m.keys.JumpBottom):
		m.scrollToBottom()
		return true, nil

	case key.Matches(msg, m.keys.Paste):
		// 仅在空闲态且无覆盖层时粘贴
		if !m.running && m.overlay == overlayNone {
			return true, func() tea.Msg { return tea.ReadClipboard() }
		}
		return true, nil
	}

	// 未匹配的按键 → 传给 input
	return false, nil
}

// handlePermKey 处理权限确认框内的按键。返回 (handled, cmd)。
// ↑/↓ 由 list 组件内部处理；Enter / Esc 在此拦截。
func (m *model) handlePermKey(key string) (bool, tea.Cmd) {
	switch key {
	case "enter":
		if m.permReq != nil {
			m.permReq.reply <- m.permListChoice()
		}
		m.overlay = overlayNone
		m.permReq = nil
		m.input.Focus()
		return true, nil

	case "esc":
		// Esc = Deny
		if m.permReq != nil {
			m.permReq.reply <- permission.UserChoice{Decision: permission.DecisionDeny}
		}
		m.overlay = overlayNone
		m.permReq = nil
		m.input.Focus()
		return true, nil
	}

	// 未处理的按键忽略
	return false, nil
}

// buildPermList 构建权限确认选项列表。
func (m *model) buildPermList() list.Model {
	items := []list.Item{
		permItem{title: m.msg().PermAllow, choice: permAllow},
		permItem{title: m.msg().PermAllowAll, choice: permAllowAll},
		permItem{title: m.msg().PermDeny, choice: permDeny},
	}

	delegate := list.NewDefaultDelegate()
	// 单行列表，不显示 description，无行距
	delegate.ShowDescription = false
	delegate.SetSpacing(0)
	delegate.Styles = listItemStyles()
	m.permDelegate = &delegate

	l := list.New(items, m.permDelegate, 0, 3)
	l.SetShowTitle(false)
	l.SetShowPagination(false)
	l.SetShowStatusBar(false)
	l.SetShowFilter(false)
	l.SetShowHelp(false)
	// 禁用 list 内置的 enter/esc 处理，由 TUI 层统一拦截
	l.KeyMap.Quit = key.NewBinding()
	l.KeyMap.ForceQuit = key.NewBinding()

	return l
}

// permListChoice 将 list 当前选中项转换为 UserChoice。
func (m *model) permListChoice() permission.UserChoice {
	item, ok := m.permList.SelectedItem().(permItem)
	if !ok {
		return permission.UserChoice{Decision: permission.DecisionDeny}
	}
	switch item.choice {
	case permAllow:
		return permission.UserChoice{Decision: permission.DecisionAllow}
	case permAllowAll:
		return permission.UserChoice{
			Decision:      permission.DecisionAllow,
			RememberScope: permission.ScopeConfig,
		}
	case permDeny:
		return permission.UserChoice{Decision: permission.DecisionDeny}
	default:
		return permission.UserChoice{Decision: permission.DecisionDeny}
	}
}

// ---------------------------------------------------------------------------
// AskUserQuestion 面板处理（huh 表单）
// ---------------------------------------------------------------------------

const otherOptionKey = "___other___"
func (m *model) overlayInnerWidth() int {
	contentWidth := max(m.width-4, 20)
	boxWidth := contentWidth
	if boxWidth > 70 {
		boxWidth = 70
	}
	return boxWidth - 2 - 4 // border(左右各1) + padding(左右各2)
}

// overlayMaxFormHeight 返回 huh 表单在 overlay 内的自适应最大高度。
// 选项少时紧凑显示全部，选项多时撑开到合理上限，超出由 huh 内部滚动。
func (m *model) overlayMaxFormHeight(optionCount int) int {
	// 固定外壳开销：styleApp padding(1) + header(8) + 底部(5) + overlay chrome(8)
	const fixedOverhead = 1 + 8 + 5 + 8
	maxAvailable := m.height - fixedOverhead
	if maxAvailable < 5 {
		maxAvailable = 5
	}
	// 所需高度：每个选项约 1 行 + 标题 2 行 + 过滤栏 1 行
	needed := optionCount + 3
	if needed < 5 {
		needed = 5
	}
	// 取 needed 和 maxAvailable 的较小值，但不超过 20 行（约 15+ 选项可见，不侵占聊天区）
	const absoluteMax = 20
	h := min(needed, maxAvailable)
	if h > absoluteMax {
		h = absoluteMax
	}
	return h
}
func themeWaveloom() huh.Theme {
	return huh.ThemeFunc(func(isDark bool) *huh.Styles {
		t := huh.ThemeBase(isDark)
	// 聚焦字段：与权限面板统一 —— 普通左边框 + colorOK 前景，无 ">" 选择符
	t.Focused.Title = t.Focused.Title.Foreground(colorHeaderAccent)
	t.Focused.Description = t.Focused.Description.Foreground(colorFooterFg)
	t.Focused.Base = lipgloss.NewStyle().
		PaddingLeft(1).
		BorderStyle(lipgloss.NormalBorder()).
		BorderLeft(true).
		BorderForeground(colorHeaderAccent)
	// 单选：使用 "▌" 选择符，与权限面板左边框视觉一致
	t.Focused.SelectSelector = lipgloss.NewStyle().Foreground(colorOK).SetString("▌ ")
	t.Blurred.SelectSelector = lipgloss.NewStyle().Foreground(colorMuted).SetString("  ")
	// 多选：光标指示器与单选统一 "▌"，选择状态用 [✓] / [ ]
	t.Focused.MultiSelectSelector = lipgloss.NewStyle().Foreground(colorOK).SetString("▌ ")
	t.Blurred.MultiSelectSelector = lipgloss.NewStyle().Foreground(colorMuted).SetString("  ")
	t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(colorOK).SetString("[✓] ")
	t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(colorMuted).SetString("[ ] ")
	t.Blurred.SelectedPrefix = lipgloss.NewStyle().Foreground(colorMuted).SetString("[✓] ")
	t.Blurred.UnselectedPrefix = lipgloss.NewStyle().Foreground(colorMuted).SetString("[ ] ")
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(colorOK)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(colorOK)
	// 未聚焦字段
	t.Blurred.Title = t.Blurred.Title.Foreground(colorHeaderFg)
	t.Blurred.Description = t.Blurred.Description.Foreground(colorFooterFg)
	t.Blurred.Base = t.Blurred.Base.BorderForeground(lipgloss.Color("#444444"))
	return t
	})
}

// buildQuestionForm 使用 huh 构建当前问题的表单。
func (m *model) buildQuestionForm() {
	if m.questionReq == nil || m.questionIdx >= len(m.questionReq.questions) {
		return
	}
	q := m.questionReq.questions[m.questionIdx]

	// 构建选项列表（含 "Other..."）
	opts := make([]huh.Option[string], len(q.Options)+1)
	for i, opt := range q.Options {
		key := opt.Label
		if strings.HasSuffix(opt.Label, "(Recommended)") {
			key = "★ " + opt.Label
		}
		opts[i] = huh.NewOption(key, opt.Label)
	}
	opts[len(q.Options)] = huh.NewOption(m.msg().QuestionOtherOption, otherOptionKey)

	theme := themeWaveloom()
	formWidth := m.overlayInnerWidth()
	optionCount := len(opts)
	formMaxHeight := m.overlayMaxFormHeight(optionCount)
	m.questionFormMaxHeight = formMaxHeight

	if q.MultiSelect {
		var selected []string
		field := huh.NewMultiSelect[string]().
			Key("answer").
			Title(q.Question).
			Options(opts...).
			Value(&selected).
			WithTheme(theme).
			WithHeight(formMaxHeight)

		f := huh.NewForm(huh.NewGroup(field)).
			WithTheme(theme).
			WithWidth(formWidth).
			WithShowHelp(false)

		m.questionForm = f
		// 表单在 WindowSizeMsg 之后动态创建，需手动注入尺寸让其计算视口高度。
		// 使用 overlayMaxFormHeight 而非终端全高 m.height，避免视口计算过大导致选项被裁剪。
		m.questionFormInitCmd = tea.Batch(
			f.Init(),
			func() tea.Msg { return tea.WindowSizeMsg{Width: formWidth, Height: formMaxHeight} },
		)
	} else {
		var selected string
		field := huh.NewSelect[string]().
			Key("answer").
			Title(q.Question).
			Options(opts...).
			Value(&selected).
			WithTheme(theme).
			WithHeight(formMaxHeight)

		f := huh.NewForm(huh.NewGroup(field)).
			WithTheme(theme).
			WithWidth(formWidth).
			WithShowHelp(false)

		m.questionForm = f
		m.questionFormInitCmd = tea.Batch(
			f.Init(),
			func() tea.Msg { return tea.WindowSizeMsg{Width: formWidth, Height: formMaxHeight} },
		)
	}
}

// buildOtherForm 构建 "Other" 自定义文本输入（使用 real cursor textinput，与主输入框一致）。
func (m *model) buildOtherForm() {
	m.questionFormIsOther = true
	m.questionForm = nil
	m.questionFormInitCmd = nil
	m.otherInputVisStart = 0
	m.otherInputLastValue = ""
	m.otherInput.SetValue("")
	// 宽度对齐盒子内部可用宽度：innerWidth - prompt（与 huh 表单宽度一致）
	inputWidth := m.overlayInnerWidth() - lipgloss.Width(m.otherInput.Prompt)
	if inputWidth < 10 {
		inputWidth = 10
	}
	m.otherInput.SetWidth(inputWidth)
	// 延迟 Focus：Init 命令在下一帧执行，避免同一帧内 Focus + SetValue 状态不一致
	m.questionFormInitCmd = m.otherInput.Focus()
}

// handleQuestionFormComplete 在 huh 表单完成时调用，提取答案并推进流程。
func (m *model) handleQuestionFormComplete() {
	if m.questionReq == nil || m.questionIdx >= len(m.questionReq.questions) {
		return
	}
	q := m.questionReq.questions[m.questionIdx]

	answerValue := m.questionForm.Get("answer")

	if q.MultiSelect {
		// 多选结果
		selected := answerValue.([]string)
		var hasOther bool
		var finalAnswers []string
		for _, v := range selected {
			if v == otherOptionKey {
				hasOther = true
			} else {
				finalAnswers = append(finalAnswers, v)
			}
		}
		if hasOther {
			m.questionPendingOther = true
			m.questionPendingAnswers = finalAnswers
			m.buildOtherForm()
			return
		}
		m.recordQuestionAnswer(finalAnswers)
	} else {
		// 单选结果
		selected := answerValue.(string)
		if selected == otherOptionKey {
			m.questionPendingOther = true
			m.questionPendingAnswers = nil
			m.buildOtherForm()
			return
		}
		m.recordQuestionAnswer([]string{selected})
	}
	m.advanceQuestion()
}

// handleQuestionFormAborted 在用户取消表单（Esc）时调用。
func (m *model) handleQuestionFormAborted() {
	// 拒绝回答
	if m.questionReq != nil && m.questionReq.reply != nil {
		m.questionReq.reply <- nil
	}
	m.closeQuestionOverlay()
}

// handleOtherInputSubmit 在用户按 Enter 提交 Other 自定义文本时调用。
func (m *model) handleOtherInputSubmit() {
	text := m.otherInput.Value()
	m.questionFormIsOther = false
	m.otherInput.Blur()

	// 将 Other 文本合并到 pending answers，由 advanceQuestion 统一记录，避免重复响应覆盖
	if m.questionPendingAnswers == nil {
		m.questionPendingAnswers = []string{"Other: " + text}
	} else {
		m.questionPendingAnswers = append(m.questionPendingAnswers, "Other: "+text)
	}
	m.advanceQuestion()
}

// handleOtherInputCancel 在用户按 Esc 取消 Other 自定义文本时调用，回退到选项列表。
func (m *model) handleOtherInputCancel() {
	m.questionFormIsOther = false
	m.questionPendingOther = false
	m.questionPendingAnswers = nil
	m.otherInput.Blur()
	m.buildQuestionForm()
}

// recordQuestionAnswer 记录当前问题的答案。
func (m *model) recordQuestionAnswer(answers []string) {
	if m.questionReq == nil || m.questionIdx >= len(m.questionReq.questions) {
		return
	}
	q := m.questionReq.questions[m.questionIdx]
	m.questionAnswers = append(m.questionAnswers, permission.QuestionResponse{
		Question: q.Question,
		Answers:  answers,
	})
}

// advanceQuestion 前进到下一个问题或提交全部答案。
func (m *model) advanceQuestion() {
	// 如果之前有待合并的 Other 答案，合并之
	if m.questionPendingOther {
		m.questionPendingOther = false
		m.recordQuestionAnswer(m.questionPendingAnswers)
		m.questionPendingAnswers = nil
	}

	m.questionIdx++
	if m.questionReq == nil || m.questionIdx >= len(m.questionReq.questions) {
		// 所有问题已回答，提交答案
		if m.questionReq.reply != nil {
			m.questionReq.reply <- m.questionAnswers
		}
		m.closeQuestionOverlay()
		return
	}
	// 显示下一个问题
	m.buildQuestionForm()
}

// closeQuestionOverlay 关闭选择题面板。
func (m *model) closeQuestionOverlay() {
	m.overlay = overlayNone
	m.questionReq = nil
	m.questionAnswers = nil
	m.questionForm = nil
	m.questionFormIsOther = false
	m.questionPendingOther = false
	m.questionPendingAnswers = nil
	m.otherInputVisStart = 0
	m.otherInputLastValue = ""
	m.otherInput.Blur()
	m.input.Focus()
}

// ---------------------------------------------------------------------------
// 文件选择器键盘处理
// ---------------------------------------------------------------------------

// handlePickerKey 处理文件选择器活跃时的按键。返回 (handled, cmd)。
// ↑/↓ 由 bubbles/list 组件处理；Enter/Tab/Esc 在此拦截。
func (m *model) handlePickerKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	keyStr := msg.String()

	switch keyStr {
	case "up", "down":
		// ↑↓ 导航由 bubbles/list 组件处理
		var cmd tea.Cmd
		m.pickerList, cmd = m.pickerList.Update(msg)
		return true, cmd

	case "esc":
		m.closePicker()
		return true, nil

	case "enter":
		idx := m.pickerList.Index()
		if idx >= 0 && idx < len(m.pickerItems) {
			m.commitPickerSelection(idx)
		}
		m.closePicker()
		return true, nil

	case "tab":
		idx := m.pickerList.Index()
		if idx >= 0 && idx < len(m.pickerItems) {
			m.completePickerFilter(idx)
			// Tab 补全可能进入子目录 → 异步重新扫描磁盘
			m.pickerFilter = resolveTilde(extractFilterAfterAt(m.input.Value()))
			m.pickerLastScannedBase = "" // 强制重新扫描
			m.scanFilesAsync()
			m.pickerItems = fuzzyFilter(m.pickerFilter, m.pickerAllItems)
			m.buildPickerList()
			m.pickerLastValue = m.input.Value()
		}
		return true, nil

	default:
		// 可打印字符 → 传给 input，Update() 中会触发 re-filter
		return false, nil
	}
}

// closePicker 关闭文件选择器。
func (m *model) closePicker() {
	m.pickerVisible = false
	m.pickerScanning = false
	m.pickerDismissValue = m.input.Value()
	m.pickerLastValue = ""
	m.pickerLastScannedBase = ""
	m.pickerAllItems = nil
}

// completePickerFilter 将选中路径补全到 @ 过滤器，保持选择器打开。
// 用户可继续输入以进一步缩小范围（fzf 风格 Tab 补全）。
func (m *model) completePickerFilter(idx int) {
	if idx < 0 || idx >= len(m.pickerItems) {
		return
	}
	selected := m.pickerItems[idx].Path
	value := m.input.Value()
	atIdx := strings.LastIndex(value, "@")
	if atIdx < 0 {
		return
	}
	// 替换 @ 及其后的内容为 @{selectedPath}
	// 目录自动追加 /，方便继续向下过滤（如 @cmd/waveloom/ 后可继续输入 m 匹配 main.go）
	newValue := value[:atIdx] + "@" + selected
	if m.pickerItems[idx].IsDir && !strings.HasSuffix(selected, "/") {
		newValue += "/"
	}
	m.input.SetValue(newValue)
	m.input.CursorEnd()
}

// commitPickerSelection 将选中路径回填到 textinput，关闭选择器。
func (m *model) commitPickerSelection(idx int) {
	if idx < 0 || idx >= len(m.pickerItems) {
		return
	}
	selected := m.pickerItems[idx].Path
	value := m.input.Value()
	atIdx := strings.LastIndex(value, "@")
	if atIdx < 0 {
		return
	}
	// 替换 @ 及其后的内容为 @{selectedPath} （追加空格，与 / 命令选择器行为一致）
	newValue := value[:atIdx] + "@" + selected + " "
	m.input.SetValue(newValue)
	// 光标移到末尾
	m.input.CursorEnd()
}

// renderPickerDropdown 渲染文件选择器下拉列表。
func (m *model) renderPickerDropdown(contentWidth int) string {
	// REGRESSION: 空 items 时返回 "" → 下拉面板完全不可见，用户在大目录中输入 @ 后
	// 看到的是"没反应"，实际上扫描异步进行中但无任何视觉反馈。
	// 修复：扫描中显示 spinner，无结果显示空状态，均不返回空字符串。
	// 不可单测：依赖 TUI 模型状态 + lipgloss 样式 + spinner 组件。
	// 扫描中 → 显示加载状态
	if m.pickerScanning && len(m.pickerItems) == 0 {
		spinner := m.spinner.View()
		boxStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorHeaderAccent).
			Padding(0, 1).
			Width(contentWidth)
		return boxStyle.Render(spinner + " " + m.msg().PickerScanning)
	}

	// 扫描完成但无结果 → 显示空状态
	if len(m.pickerItems) == 0 {
		emptyStyle := lipgloss.NewStyle().Foreground(colorGray)
		boxStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorHeaderAccent).
			Padding(0, 1).
			Width(contentWidth)
		return boxStyle.Render(emptyStyle.Render(m.msg().PickerNoResults))
	}

	// 同步 list 宽度
	m.pickerList.SetSize(contentWidth-4, m.pickerList.Height())

	// 带边框的下拉面板
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorHeaderAccent).
		Padding(1, 1).
		Width(contentWidth)

	return boxStyle.Render(m.pickerList.View())
}

// ---------------------------------------------------------------------------
// 命令选择器（/ 触发）
// ---------------------------------------------------------------------------

// activateCommandPicker 首次激活命令选择器。
func (m *model) activateCommandPicker() {
	m.commandPickerVisible = true
	m.commandPickerFilter = extractFilterAfterSlash(m.input.Value())
	m.commandPickerLastValue = m.input.Value()

	// 从 registry 获取命令列表
	m.commandPickerItems = m.slashRegistry.List()

	// 立即过滤
	m.updateCommandPickerFilter()
}

// closeCommandPicker 关闭命令选择器。
func (m *model) closeCommandPicker() {
	m.commandPickerVisible = false
	m.commandPickerDismissValue = m.input.Value()
	m.commandPickerLastValue = ""
	m.commandPickerItems = nil
}

// extractFilterAfterSlash 提取 / 之后的文本作为命令过滤条件。
func extractFilterAfterSlash(value string) string {
	if !strings.HasPrefix(value, "/") {
		return ""
	}
	return value[1:]
}

// updateCommandPickerFilter 根据当前输入过滤命令列表。
func (m *model) updateCommandPickerFilter() {
	m.commandPickerFilter = extractFilterAfterSlash(m.input.Value())

	filter := strings.ToLower(m.commandPickerFilter)
	if filter == "" {
		m.buildCommandPickerList(m.commandPickerItems)
		return
	}

	// 为每个命令计算最佳匹配：prefix 优先，然后按匹配位置（越左越优先）+ 字母序
	var matches []cmdMatch
	for _, cmd := range m.commandPickerItems {
		match := bestCommandMatch(filter, cmd)
		if match.position >= 0 {
			matches = append(matches, match)
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].isPrefix != matches[j].isPrefix {
			return matches[i].isPrefix
		}
		if matches[i].position != matches[j].position {
			return matches[i].position < matches[j].position
		}
		return matches[i].cmd.Name < matches[j].cmd.Name
	})

	filtered := make([]slashcommand.CommandInfo, len(matches))
	for i, m := range matches {
		filtered[i] = m.cmd
	}

	m.buildCommandPickerList(filtered)
}

// cmdMatch 是命令匹配的中间结果。
type cmdMatch struct {
	cmd      slashcommand.CommandInfo
	isPrefix bool
	position int
}

// bestCommandMatch 计算命令与 filter 的最佳匹配。
// position = -1 表示不匹配。
func bestCommandMatch(filter string, cmd slashcommand.CommandInfo) cmdMatch {
	m := cmdMatch{cmd: cmd, position: -1}

	check := func(s string) {
		sl := strings.ToLower(s)
		idx := strings.Index(sl, filter)
		if idx < 0 {
			return
		}
		isPrefix := idx == 0 && strings.HasPrefix(sl, filter)
		better := false
		if m.position < 0 {
			better = true
		} else if isPrefix && !m.isPrefix {
			better = true
		} else if isPrefix == m.isPrefix && idx < m.position {
			better = true
		}
		if better {
			m.isPrefix = isPrefix
			m.position = idx
		}
	}

	check(cmd.Name)
	for _, alias := range cmd.Aliases {
		check(alias)
	}
	return m
}

// buildCommandPickerList 从 CommandInfo 列表构建 bubbles/list 组件。
func (m *model) buildCommandPickerList(items []slashcommand.CommandInfo) {
	listItems := make([]list.Item, len(items))
	for i, cmd := range items {
		listItems[i] = commandPickerItem{name: cmd.Name, aliases: cmd.Aliases, description: cmd.Description, args: cmd.Args}
	}

	height := len(listItems)
	if height > 5 {
		height = 5
	}
	if height < 1 {
		height = 1
	}

	// 复用已有 list，仅更新 items + height
	if m.commandPickerList.Items() != nil {
		m.commandPickerList.SetItems(listItems)
		m.commandPickerList.SetSize(0, height)
		return
	}

	// 首次创建
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false
	delegate.SetSpacing(0)
	delegate.Styles = listItemStyles()

	l := list.New(listItems, delegate, 0, height)
	l.SetShowTitle(false)
	l.SetShowPagination(false)
	l.SetShowStatusBar(false)
	l.SetShowFilter(false)
	l.SetShowHelp(false)
	l.KeyMap.Quit = key.NewBinding()
	l.KeyMap.ForceQuit = key.NewBinding()

	m.commandPickerList = l
	m.commandPickerDelegate = &delegate
}

// handleCommandPickerKey 处理命令选择器中的按键。
func (m *model) handleCommandPickerKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	keyStr := msg.String()

	switch keyStr {
	case "up", "down":
		var cmd tea.Cmd
		m.commandPickerList, cmd = m.commandPickerList.Update(msg)
		return true, cmd

	case "esc":
		m.closeCommandPicker()
		return true, nil

	case "enter":
		idx := m.commandPickerList.Index()
		if idx >= 0 && idx < len(m.commandPickerList.Items()) {
			item, ok := m.commandPickerList.Items()[idx].(commandPickerItem)
			if ok && item.args == "" {
				// 无参数命令：直接执行
				m.closeCommandPicker()
				return true, m.handleSlashCommand("/" + item.name)
			}
			m.commitCommandPickerSelection(idx)
			m.closeCommandPicker()
			return true, nil
		}
		// 无匹配项（如别名 /clear）：将当前输入作为 slash 命令执行
		m.closeCommandPicker()
		return true, m.handleSlashCommand(m.input.Value())

	case "tab":
		idx := m.commandPickerList.Index()
		if idx >= 0 && idx < len(m.commandPickerList.Items()) {
			m.completeCommandPickerFilter(idx)
		}
		return true, nil

	default:
		// 可打印字符 → 传给 input，Update() 中会触发 re-filter
		return false, nil
	}
}

// commitCommandPickerSelection 将选中命令回填到 textinput，关闭选择器。
func (m *model) commitCommandPickerSelection(idx int) {
	items := m.commandPickerList.Items()
	if idx < 0 || idx >= len(items) {
		return
	}
	item, ok := items[idx].(commandPickerItem)
	if !ok {
		return
	}
	// 替换 / 及其后的内容为 /{commandName} （保留空格以便用户输入参数）
	newValue := "/" + item.name + " "
	m.input.SetValue(newValue)
	m.input.CursorEnd()
}

// completeCommandPickerFilter 将选中命令名补全到 / 过滤器，保持选择器打开。
func (m *model) completeCommandPickerFilter(idx int) {
	items := m.commandPickerList.Items()
	if idx < 0 || idx >= len(items) {
		return
	}
	item, ok := items[idx].(commandPickerItem)
	if !ok {
		return
	}
	newValue := "/" + item.name
	m.input.SetValue(newValue)
	m.input.CursorEnd()
	// 更新过滤，保持选择器打开但只显示匹配项
	m.commandPickerFilter = item.name
	m.updateCommandPickerFilter()
	m.commandPickerLastValue = m.input.Value()
}

// renderCommandPickerDropdown 渲染命令选择器下拉列表。
func (m *model) renderCommandPickerDropdown(contentWidth int) string {
	if m.commandPickerList.Items() == nil || len(m.commandPickerList.Items()) == 0 {
		return ""
	}

	// 同步 list 宽度
	m.commandPickerList.SetSize(contentWidth-4, m.commandPickerList.Height())

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorHeaderAccent).
		Padding(1, 1).
		Width(contentWidth)

	return boxStyle.Render(m.commandPickerList.View())
}

// shouldActivatePicker 检测输入框当前内容是否触发文件选择器。
// 条件: 最后一个 @ 在行首或空格之后，且 @ 之后无空格。
func shouldActivatePicker(value string) bool {
	idx := strings.LastIndex(value, "@")
	if idx < 0 {
		return false
	}
	// @ 前必须是行首或空格
	if idx > 0 && value[idx-1] != ' ' {
		return false
	}
	// @ 之后不能已经包含空格（路径已完成，避免重新触发）
	afterAt := value[idx+1:]
	return !strings.Contains(afterAt, " ")
}

// shouldActivateCommandPicker 检测输入框当前内容是否触发命令选择器。
// 条件: / 在行首，且 / 之后无空格（命令未完成）。
func shouldActivateCommandPicker(value string) bool {
	if !strings.HasPrefix(value, "/") {
		return false
	}
	afterSlash := value[1:]
	// / 之后不能已经包含空格（命令已完成，如 "/help" 整体提交或 "/model v4"）
	if strings.Contains(afterSlash, " ") {
		return false
	}
	return true
}

// extractFilterAfterAt 提取最后一个 @ 之后的文本作为过滤条件。
func extractFilterAfterAt(value string) string {
	idx := strings.LastIndex(value, "@")
	if idx < 0 {
		return ""
	}
	return value[idx+1:]
}

// updatePickerFilter 根据当前输入重新过滤文件列表，并异步重扫磁盘。
func (m *model) updatePickerFilter() {
	m.pickerFilter = resolveTilde(extractFilterAfterAt(m.input.Value()))
	// 立即用现有 allItems 做内存过滤，提供即时反馈
	m.pickerItems = fuzzyFilter(m.pickerFilter, m.pickerAllItems)
	m.buildPickerList()
	// 异步重扫磁盘，确保 pickerAllItems 覆盖新 filter
	m.scanFilesAsync()
}

// scanFilesAsync 发起异步磁盘扫描，与 startPickerScan 行为一致。
// 若 filepath.Base(filter) 未变化则跳过（现有 allItems 已覆盖），
// 否则等待 150ms 防抖后再执行扫描。
func (m *model) scanFilesAsync() {
	filter := m.pickerFilter
	base := filepath.Base(filter)

	// base 未变化 → 现有 allItems 已覆盖更具体的同 base 过滤，无需重扫
	if base == m.pickerLastScannedBase && len(m.pickerAllItems) > 0 {
		return
	}
	m.pickerLastScannedBase = base

	// 取消上一次未完成的扫描
	if m.pickerScanCancel != nil {
		m.pickerScanCancel()
		m.pickerScanCancel = nil
	}

	m.pickerScanning = true
	m.pickerScanGen++
	gen := m.pickerScanGen
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	m.pickerScanCancel = cancel

	go func() {
		defer cancel()
		// 150ms 防抖：等待用户停止输入后再扫描
		select {
		case <-time.After(150 * time.Millisecond):
		case <-ctx.Done():
			return
		}
		// 若在此期间有新扫描发起，代数已递增，跳过本次
		if m.pickerScanGen != gen {
			return
		}
		items := doScanFilesWithContext(ctx, m.registry, m.cwd, filter)
		if m.program != nil {
			m.program.Send(pickerScanDoneMsg{items: items, gen: gen})
		}
	}()
}

// activatePicker 首次激活文件选择器，异步扫描磁盘。
func (m *model) activatePicker() {
	m.pickerVisible = true
	m.pickerFilter = resolveTilde(extractFilterAfterAt(m.input.Value()))
	m.pickerLastValue = m.input.Value()
	m.pickerScanning = true

	// 立即用空列表占位，避免 View() 中 nil list
	m.pickerItems = nil
	m.buildPickerList()
}

// startPickerScan 返回一个 tea.Cmd，在 goroutine 中扫描文件并回传结果。
// 在调用时捕获 filter 和 generation，避免竞态。
func (m *model) startPickerScan() tea.Cmd {
	filter := m.pickerFilter
	m.pickerLastScannedBase = filepath.Base(filter)

	// 取消上一次未完成的扫描
	if m.pickerScanCancel != nil {
		m.pickerScanCancel()
		m.pickerScanCancel = nil
	}

	m.pickerScanGen++
	gen := m.pickerScanGen
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	m.pickerScanCancel = cancel

	return func() tea.Msg {
		defer cancel()
		items := doScanFilesWithContext(ctx, m.registry, m.cwd, filter)
		return pickerScanDoneMsg{items: items, gen: gen}
	}
}

// buildPickerList 从 pickerItems 更新 bubbles/list 组件。
// 首次调用时创建新 list，后续调用复用已有 list 仅更新 items。
func (m *model) buildPickerList() {
	items := make([]list.Item, len(m.pickerItems))
	for i, item := range m.pickerItems {
		items[i] = fileItem{
			path:    item.Path,
			display: item.Display,
			isDir:   item.IsDir,
		}
	}

	maxHeight := len(items)
	if maxHeight > 5 {
		maxHeight = 5
	}
	if maxHeight < 1 {
		maxHeight = 1
	}

	// 复用已有 list，仅更新 items + height
	if m.pickerList.Items() != nil {
		m.pickerList.SetItems(items)
		m.pickerList.SetSize(0, maxHeight)
		return
	}

	// 首次创建
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false
	delegate.SetSpacing(0)
	delegate.Styles = listItemStyles()

	l := list.New(items, delegate, 0, maxHeight)
	l.SetShowTitle(false)
	l.SetShowPagination(false)
	l.SetShowStatusBar(false)
	l.SetShowFilter(false)
	l.SetShowHelp(false)
	l.KeyMap.Quit = key.NewBinding()
	l.KeyMap.ForceQuit = key.NewBinding()

	m.pickerList = l
	m.pickerDelegate = &delegate
}

// doScanFilesWithContext 传递 context 用于超时/取消。
func doScanFilesWithContext(ctx context.Context, registry tool.Registry, cwd, filter string) []pickerItem {
	filter = resolveTilde(filter)

	if filepath.IsAbs(filter) {
		return doScanAbsolute(ctx, registry, cwd, filter)
	}
	return doScanRelative(ctx, registry, cwd, filter)
}

// doScanRelative 相对路径扫描：基于 cwd，深度扫描项目内部，浅层列出父目录兄弟。
func doScanRelative(ctx context.Context, registry tool.Registry, cwd, filter string) []pickerItem {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	searchRoot := "."
	searchDir := cwd
	dirPrefix := extractDirPrefix(filter)
	if dirPrefix != "" && dirPrefix != "." {
		resolved := dirPrefix
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(cwd, resolved)
		}
		resolved = filepath.Clean(resolved)
		if info, err := os.Stat(resolved); err == nil && info.IsDir() {
			searchRoot = resolved
			searchDir = resolved
		}
	}

	// 父目录顶层 → 浅层列出兄弟；其他 → 浅层扫描（5 层），
	// 用户输入更多路径分量后 searchRoot 自然收窄，无需深层扫描。
	maxDepth := 5
	excludeCWD := false
	if searchRoot == filepath.Dir(cwd) {
		maxDepth = 2
		excludeCWD = true
	}

	doSearch := func(namePattern string) (files []string) {
		// 使用 filepath.WalkDir 替代 find 命令：
		// - 跨平台兼容（Windows 无 find）
		// - 无外部依赖、无 shell 转义、无 MaxShellLines 截断
		// - WalkDirFunc 内直接 prune / 深度控制 / 名称过滤

		// 根目录绝对化，用于深度计算
		absRoot, err := filepath.Abs(filepath.Join(searchDir, searchRoot))
		if err != nil {
			absRoot = filepath.Join(searchDir, searchRoot)
		}
		absRoot = filepath.Clean(absRoot)
		// 计算根目录的路径分量数，用于深度判断
		rootDepth := len(strings.Split(absRoot, string(filepath.Separator)))

		var found []string
		walkCtx, walkCancel := context.WithTimeout(ctx, 8*time.Second)
		defer walkCancel()

		walkFn := func(path string, d os.DirEntry, walkErr error) error {
			select {
			case <-walkCtx.Done():
				return walkCtx.Err()
			default:
			}

			if walkErr != nil {
				return nil // 跳过无法访问的目录
			}

			// 深度控制
			currentDepth := len(strings.Split(path, string(filepath.Separator))) - rootDepth
			if currentDepth > maxDepth {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			// prune: 跳过 .git / node_modules 整树
			base := d.Name()
			if d.IsDir() && (base == ".git" || base == "node_modules") {
				return filepath.SkipDir
			}

			// 隐藏目录/文件跳过（非 . 或 ..）
			if strings.HasPrefix(base, ".") && base != "." && base != ".." {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			// excludeCWD: 跳过 CWD 下的所有条目
			if excludeCWD {
				cwdAbs, _ := filepath.Abs(cwd)
				if strings.HasPrefix(path+string(filepath.Separator), cwdAbs+string(filepath.Separator)) {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
			}

			if !d.IsDir() {
				// 名称过滤（只对文件，目录由目录推断生成）
				if namePattern != "" && namePattern != "*" {
					matched, _ := filepath.Match(namePattern, base)
					if !matched {
						return nil
					}
				}
				// 转为相对路径（相对于 CWD，确保 ../ 等前缀在 relativizePaths 中正确处理）
				rel, err := filepath.Rel(cwd, path)
				if err != nil {
					rel = path
				}
				found = append(found, rel)
			}

			return nil
		}

		_ = filepath.WalkDir(searchDir, walkFn)
		// WalkDir 可能因 context 超时返回 error，found 中已有部分结果可用
		sort.Strings(found)
		return relativizePaths(found, cwd)
	}

	files := doSearch("*")

	seenDirs := make(map[string]bool)
	var items []pickerItem

	for _, file := range files {
		if isHiddenOrBinary(file) {
			continue
		}
		items = append(items, pickerItem{
			Path:    file,
			IsDir:   false,
			Display: file,
		})

		dir := filepath.Dir(file)
		for dir != "." && dir != "/" && dir != "" {
			if seenDirs[dir] {
				break
			}
			if isHiddenOrBinary(dir) {
				break
			}
			seenDirs[dir] = true
			items = append(items, pickerItem{
				Path:    dir,
				IsDir:   true,
				Display: dir + "/",
			})
			dir = filepath.Dir(dir)
		}
	}

	// 父目录扫描时 CWD 内文件被 excludeCWD 排除，
	// 但 CWD 目录本身应作为候选项，支持 @../wav 模糊匹配 ../waveloom/
	// 插入到 items 开头，避免被 500 条截断丢弃
	if excludeCWD {
		cwdRel, _ := filepath.Rel(searchRoot, cwd)
		if cwdRel != "." && cwdRel != "" {
			cwdDisplay := filepath.Join("..", cwdRel) + "/"
			if !isHiddenOrBinary(cwdDisplay) {
				items = append([]pickerItem{{
					Path:    filepath.Join("..", cwdRel),
					IsDir:   true,
					Display: cwdDisplay,
				}}, items...)
			}
		}
	}

	// 当 dirPrefix 经由 .. 解析回 CWD 时（如 ../waveloom/ → CWD），
	// 显示路径需加上 dirPrefix 前缀，与 filter 保持一致，
	// 否则 filter("../waveloom/A") 无法匹配 display("AGENTS.md")。
	if searchRoot == cwd && dirPrefix != "" && dirPrefix != "." {
		prefix := dirPrefix
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		for i := range items {
			items[i].Path = prefix + items[i].Path
			items[i].Display = prefix + items[i].Display
		}
	}

	if len(items) == 0 {
		return nil
	}

	// REGRESSION: 500 条截断在 sortPickerItems 之前。
	// 4205 个目录中 waveloom/ 排在 ~4000 位，被截断提前丢弃，
	// fuzzyFilter 从未收到该条目 → 永远搜不到字母序靠后的目录。
	// 修复：去掉 500 截断，靠 fuzzyFilter（上限 20 条）自然限流。
	// 不可单测：依赖真实目录结构，条目数随 CWD 变化。
	sortPickerItems(items)
	return items
}

// doScanAbsolute 绝对路径扫描：展示绝对路径，深度随导航层级递增。
func doScanAbsolute(ctx context.Context, registry tool.Registry, cwd, filter string) []pickerItem {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// 提取目录前缀作为搜索起点
	dirPrefix := extractDirPrefix(filter)
	if dirPrefix == "" || dirPrefix == "." {
		return nil
	}
	resolved := dirPrefix
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(cwd, resolved)
	}
	resolved = filepath.Clean(resolved)
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return nil
	}
	searchRoot := resolved

	relFilter := strings.TrimPrefix(filter, searchRoot)
	relFilter = strings.Trim(relFilter, "/")

	// REGRESSION: 深度策略对模糊匹配（如 @/Use → searchRoot=/）使用 extraLevels+2，
	// 根目录扫描 6+ 层极易超时，导致间歇性搜索不到。
	// 修复：完整目录（relFilter=""）深度 5，模糊匹配（relFilter 非空）深度 1。
	// 不可单测：依赖真实文件系统，扫描耗时随系统负载变化。
	maxDepth := 5
	if relFilter != "" {
		maxDepth = 1
	}
	if maxDepth < 1 {
		maxDepth = 1
	}
	if maxDepth > 12 {
		maxDepth = 12
	}

	// 搜索策略：
	// - 有部分名称（如 Workben）→ -name 'Workben*' 精搜，防截断
	// - 仅目录前缀（如 ~/）→ 全量扫描，供浏览
	namePattern := "*"
	if relFilter != "" {
		baseName := filepath.Base(relFilter)
		if baseName != "" && baseName != "." && baseName != "/" {
			namePattern = baseName + "*"
		}
	}

	doSearch := func(typeFilter string) (files []string) {
		// 使用 filepath.WalkDir 替代 find：跨平台兼容，无外部依赖。

		absRoot := filepath.Clean(searchRoot)
		rootDepth := len(strings.Split(absRoot, string(filepath.Separator)))

		var results []string
		walkCtx, walkCancel := context.WithTimeout(ctx, 8*time.Second)
		defer walkCancel()

		walkFn := func(path string, d os.DirEntry, walkErr error) error {
			select {
			case <-walkCtx.Done():
				return walkCtx.Err()
			default:
			}

			if walkErr != nil {
				return nil
			}

			currentDepth := len(strings.Split(path, string(filepath.Separator))) - rootDepth
			if currentDepth > maxDepth {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			base := d.Name()
			// prune .git / node_modules
			if d.IsDir() && (base == ".git" || base == "node_modules") {
				return filepath.SkipDir
			}

			// 隐藏目录跳过
			if strings.HasPrefix(base, ".") && base != "." && base != ".." {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			// 类型过滤
			if typeFilter == "d" && !d.IsDir() {
				return nil
			}
			if typeFilter == "f" && d.IsDir() {
				return nil
			}

			// 名称过滤
			if namePattern != "*" {
				matched, _ := filepath.Match(namePattern, base)
				if !matched {
					return nil
				}
			}

			results = append(results, path)
			return nil
		}

		_ = filepath.WalkDir(absRoot, walkFn)
		sort.Strings(results)
		return results
	}

	dirFiles := doSearch("d")
	regFiles := doSearch("f")

	if len(dirFiles) == 0 && len(regFiles) == 0 {
		return nil
	}

	seenDirs := make(map[string]bool)
	var items []pickerItem

	// 目录条目（find -type d 已确认类型，无需 os.Stat）
	for _, entry := range dirFiles {
		if seenDirs[entry] {
			continue
		}
		seenDirs[entry] = true
		items = append(items, pickerItem{
			Path:    entry,
			IsDir:   true,
			Display: entry + "/",
		})

		// 提取父目录链
		dir := filepath.Dir(entry)
		for dir != searchRoot && dir != "/" && dir != "" && strings.HasPrefix(dir, searchRoot) {
			if seenDirs[dir] {
				break
			}
			seenDirs[dir] = true
			items = append(items, pickerItem{
				Path:    dir,
				IsDir:   true,
				Display: dir + "/",
			})
			dir = filepath.Dir(dir)
		}
	}

	// 文件条目（find -type f 已确认类型）
	for _, entry := range regFiles {
		if isHiddenOrBinary(entry) {
			continue
		}
		items = append(items, pickerItem{
			Path:    entry,
			IsDir:   false,
			Display: entry,
		})

		// 提取父目录链
		dir := filepath.Dir(entry)
		for dir != searchRoot && dir != "/" && dir != "" && strings.HasPrefix(dir, searchRoot) {
			if seenDirs[dir] {
				break
			}
			if isHiddenOrBinary(dir) {
				break
			}
			seenDirs[dir] = true
			items = append(items, pickerItem{
				Path:    dir,
				IsDir:   true,
				Display: dir + "/",
			})
			dir = filepath.Dir(dir)
		}
	}

	sortPickerItems(items)
	return items
}

// extractDirPrefix 从 filter 中提取可能的外部目录前缀。
// 若 filter 以 /、~ 或 . 开头，返回其目录部分作为搜索起点。
// 绝对路径（/）和 ~ 直接使用；相对路径（./、../）基于 cwd 解析。
func extractDirPrefix(filter string) string {
	if filter == "" {
		return ""
	}
	if filter[0] == '/' || filter[0] == '~' || filter[0] == '.' {
		if idx := strings.LastIndex(filter, "/"); idx >= 0 {
			return filter[:idx+1]
		}
		return filter
	}
	return ""
}

// resolveTilde 展开 filter 中的 ~ 和 ~user 前缀为实际 home 目录路径。
// ~ → 当前用户 home，~user → 指定用户 home。
func resolveTilde(filter string) string {
	if !strings.HasPrefix(filter, "~") {
		return filter
	}
	end := strings.Index(filter, "/")
	tildePart := filter
	suffix := ""
	if end >= 0 {
		tildePart = filter[:end]
		suffix = filter[end:]
	}

	var homeDir string
	if tildePart == "~" {
		homeDir, _ = os.UserHomeDir()
	} else {
		username := tildePart[1:]
		if u, err := user.Lookup(username); err == nil {
			homeDir = u.HomeDir
		}
	}
	if homeDir == "" {
		return filter
	}
	return homeDir + suffix
}

// relativizePaths 将绝对路径或 ./ 前缀路径转换为相对于 cwd 的路径。
// find 在外部目录搜索时输出绝对路径，需要转回 cwd 相对路径以支持模糊过滤和 @ 引用。
func relativizePaths(paths []string, cwd string) []string {
	result := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// 转绝对路径
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, abs)
		}
		// 转 cwd 相对路径
		rel, err := filepath.Rel(cwd, abs)
		if err != nil {
			rel = abs
		}
		result = append(result, rel)
	}
	return result
}

// isHiddenOrBinary 检查路径是否应被过滤。
func isHiddenOrBinary(path string) bool {
	// 检查每个路径段是否以 . 开头（隐藏文件/目录），排除 . 和 ..（合法路径导航）
	parts := strings.Split(path, string(filepath.Separator))
	for _, p := range parts {
		if strings.HasPrefix(p, ".") && p != "." && p != ".." {
			return true
		}
		// 常见巨型目录
		switch p {
		case "node_modules", "__pycache__", "vendor", "dist", "build":
			return true
		}
	}

	// 二进制文件扩展名
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".exe", ".dll", ".so", ".dylib", ".o", ".a", ".class", ".pyc", ".jar",
		".war", ".zip", ".tar", ".gz", ".bz2", ".7z", ".rar", ".png", ".jpg",
		".jpeg", ".gif", ".ico", ".pdf", ".woff", ".woff2", ".ttf", ".eot", ".wasm":
		return true
	}
	return false
}

// sortPickerItems 排序候选项：目录在前，文件在后，字母序。
func sortPickerItems(items []pickerItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsDir != items[j].IsDir {
			return items[i].IsDir
		}
		return items[i].Display < items[j].Display
	})
}

// fuzzyFilter 对候选项执行模糊过滤（按路径分量最小前缀匹配）。
func fuzzyFilter(filter string, items []pickerItem) []pickerItem {
	if filter == "" {
		// 无过滤时返回前 20 项
		if len(items) > 20 {
			return items[:20]
		}
		return items
	}

	filter = strings.ToLower(filter)

	// 分类：按分量前缀匹配、子串匹配、其他
	// 每组内按匹配位置升序（越左越优先），位置相同按字母序
	var prefix, substr, other []pickerItem
	for _, item := range items {
		display := strings.ToLower(item.Display)
		switch {
		case pathPrefixMatch(filter, display):
			prefix = append(prefix, item)
		case strings.Contains(display, filter):
			substr = append(substr, item)
		default:
			other = append(other, item)
		}
	}

	sortByMatchPos(filter, prefix)
	sortByMatchPos(filter, substr)

	result := append(prefix, substr...)
	result = append(result, other...)

	if len(result) > 20 {
		return result[:20]
	}
	return result
}

// pathPrefixMatch 按路径分量检查 filter 是否为 display 的最小前缀匹配。
// filter 的每个 / 分隔分量都必须为 display 对应分量的前缀。
// 例：spec/reference 匹配 specs/reference-context.md，
//
//	因为 spec ≤ specs 且 reference ≤ reference-context.md。
func pathPrefixMatch(filter, display string) bool {
	filterParts := strings.Split(filter, "/")
	displayParts := strings.Split(display, "/")

	if len(filterParts) > len(displayParts) {
		return false
	}

	for i, fp := range filterParts {
		if i >= len(displayParts) {
			return false
		}
		if !strings.HasPrefix(displayParts[i], fp) {
			return false
		}
	}
	return true
}

// sortByMatchPos 按 filter 在 Display 中的首次出现位置升序排列，
// 位置越靠左越优先；无法作为连续子串匹配的（Index 返回 -1）排在最后；
// 位置相同时按 Display 字母序。
func sortByMatchPos(filter string, items []pickerItem) {
	sort.Slice(items, func(i, j int) bool {
		di := strings.ToLower(items[i].Display)
		dj := strings.ToLower(items[j].Display)
		pi := strings.Index(di, filter)
		pj := strings.Index(dj, filter)
		// -1 排在所有有效位置之后
		if pi < 0 {
			pi = 1 << 30
		}
		if pj < 0 {
			pj = 1 << 30
		}
		if pi != pj {
			return pi < pj
		}
		return di < dj
	})
}

// ---------------------------------------------------------------------------
// 流式事件处理（状态机）
// ---------------------------------------------------------------------------

// handleStreamDelta 处理 LLM 流式增量。
func (m *model) handleStreamDelta(ev agentloop.StreamDelta) {
	// Reasoning delta → thought 段落
	if ev.ReasoningDelta != "" {
		last := lastPara(m.paras)
		if last != nil && last.Type == paraThought && last.State == stateStreaming {
			last.Text += ev.ReasoningDelta
		} else {
			// 新建 thought 段落
			// 如果前一个 assistant 正在流式，先结束它
			if last != nil && last.Type == paraAssistant && last.State == stateStreaming {
				last.State = stateDone
				last.renderDirty = true
			}
			m.paras = append(m.paras, Paragraph{
				Type:  paraThought,
				State: stateStreaming,
				Text:  ev.ReasoningDelta,
			})
		}
	}

	// Content delta → assistant 段落
	if ev.ContentDelta != "" {
		last := lastPara(m.paras)
		// 如果当前 thought 正在流式（用户未手动展开），先收敛它
		if last != nil && last.Type == paraThought && last.State == stateStreaming {
			last.ThoughtTokens = len([]rune(last.Text)) / 3
			if last.ThoughtTokens < 1 {
				last.ThoughtTokens = 1
			}
			last.State = stateCollapsed // 自动收敛为 collapsed
			last.renderDirty = true
		}

		// 追加到当前 assistant 段落
		last2 := lastPara(m.paras)
		if last2 != nil && last2.Type == paraAssistant && last2.State == stateStreaming {
			last2.Text += ev.ContentDelta
		} else {
			m.paras = append(m.paras, Paragraph{
				Type:  paraAssistant,
				State: stateStreaming,
				Text:  ev.ContentDelta,
			})
		}
	}
}

// handleToolStart 处理工具调用开始。
func (m *model) handleToolStart(ev agentloop.ToolCallStart) {
	// 如果当前 thought 正在流式，先收敛它（reasoning → tool call 直通场景）
	last := lastPara(m.paras)
	if last != nil && last.Type == paraThought && last.State == stateStreaming {
		last.ThoughtTokens = len([]rune(last.Text)) / 3
		if last.ThoughtTokens < 1 {
			last.ThoughtTokens = 1
		}
		last.State = stateCollapsed
		last.renderDirty = true
	}

	// 结束当前 assistant 段落的流式状态
	last = lastPara(m.paras)
	if last != nil && last.Type == paraAssistant && last.State == stateStreaming {
		last.State = stateDone
		last.renderDirty = true
	}

	// 创建 tool 段落（agent 工具由 SubagentStart 事件创建 paraSubagent 替代）
	if ev.ToolCallName != "agent" {
		m.paras = append(m.paras, Paragraph{
			Type:     paraTool,
			State:    stateStreaming,
			ToolName: ev.ToolCallName,
			ToolArgs: formatToolArgs(ev.ToolCallName, ev.Arguments, m.cwd),
		})
	}
}

// handleToolStream 处理工具执行中的增量输出流。
func (m *model) handleToolStream(ev agentloop.ToolCallStream) {
	for i := len(m.paras) - 1; i >= 0; i-- {
		p := &m.paras[i]
		if p.Type == paraTool && p.State == stateStreaming && p.ToolName == ev.ToolCallName {
			p.ToolResult = truncateToolStreamOutput(p.ToolResult + ev.Chunk)
			p.renderDirty = true
			return
		}
	}
}

// handleToolResult 处理工具执行结果。
func (m *model) handleToolResult(ev agentloop.ToolCallResult) {
	// 查找匹配的 tool 段落（按 tool name + args 匹配，取最后一个 streaming 的）
	for i := len(m.paras) - 1; i >= 0; i-- {
		p := &m.paras[i]
		if p.Type == paraTool && p.State == stateStreaming && p.ToolName == ev.ToolCallName {
			p.ToolResult = truncateToolResult(ev.Result)
			p.ToolError = ev.Error
			p.ToolErrorKind = ev.ErrorKind
			p.ToolDurMs = ev.DurationMs
			p.ToolDenied = ev.Denied
			p.ToolFatal = ev.Fatal
			p.DiffHunks = ev.DiffHunks
		if ev.IsError() || ev.Denied {
			p.State = stateError
		} else if ev.DiffHunks != nil {
			p.State = stateExpanded // edit_file 直接展开完整 diff 视图
		} else if p.ToolName == "ask_user_question" {
			p.State = stateExpanded // ask_user_question 默认展开显示完整问答
		} else {
			p.State = stateDone // 其他工具完成即折叠
		}
			p.renderDirty = true

			// 条件 skill 激活：文件操作工具完成后检查路径匹配
			if m.skillLoader != nil {
				filePaths := extractToolFilePaths(ev.ToolCallName, p.ToolArgs)
				if len(filePaths) > 0 {
					if activated := m.skillLoader.ActivateForPaths(filePaths); len(activated) > 0 {
						m.paras = append(m.paras, Paragraph{
							Type:      paraSystem,
							State:     stateDone,
							Text:      fmt.Sprintf(m.msg().SysSkillActivated, strings.Join(activated, ", ")),
							NotifKind: notifInfo,
						})
					}
				}
			}

			return
		}
	}
}

// extractToolFilePaths 从已格式化的 ToolArgs 中提取涉及的文件路径。
// ToolArgs 来自 formatToolArgs，对于文件操作工具直接返回目标路径。
func extractToolFilePaths(toolName string, toolArgs string) []string {
	switch toolName {
	case "read_file", "write_file", "edit_file":
		if toolArgs != "" && !strings.HasPrefix(toolArgs, "{") {
			return []string{toolArgs}
		}
	case "search_file":
		// search_file: toolArgs 格式为 "pattern in dir" 或 "dir"
		if idx := strings.LastIndex(toolArgs, " in "); idx > 0 {
			return []string{toolArgs[idx+4:]}
		}
		return []string{toolArgs}
	}
	return nil
}
// shortTokens 将 token 数格式化为短格式（≥1000 时用 k 后缀，保留一位小数）。
func shortTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	v := float64(n) / 1000
	return fmt.Sprintf("%.1fk", v)
}

// 完整结果已通过 agent loop 传递给 LLM，截断仅影响 TUI 展示。
func truncateToolResult(result string) string {
	if len(result) <= maxToolResultBytes {
		return result
	}
	return result[:maxToolResultBytes] + "\n... (output truncated)"
}

// maxToolStreamLines 是流式输出在 TUI 中保留的最大行数。
// 超过此数时从头部丢弃旧行，防止长时间命令撑爆内存。
const maxToolStreamLines = 2000

// truncateToolStreamOutput 对累积的流式输出做滚动窗口截断。
func truncateToolStreamOutput(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= maxToolStreamLines {
		return s
	}
	// 保留尾部行，用截断标记替换头部
	head := fmt.Sprintf("... (stream truncated, showing last %d lines)\n", maxToolStreamLines)
	tail := lines[len(lines)-maxToolStreamLines:]
	return head + strings.Join(tail, "\n")
}

// handleLoopDone 处理循环终止。
// generation 为 loop 启动时的 runGeneration 值。
// generation > 0 且与当前 runGeneration 不一致 → 该 LoopDone 属于已被取代的旧 loop。
func (m *model) handleLoopDone(ev agentloop.LoopDone, generation int) {
	isStale := generation > 0 && generation != m.runGeneration
	var loopIn, loopOut int
	var elapsedMs int64

	if !isStale {
		m.running = false
		m.focusIndex = -1
		if m.inPlanMode {
			m.input.Placeholder = m.msg().InputPlanModePlaceholder
		} else {
			m.input.Placeholder = m.msg().InputPlaceholder
		}
		m.cancelRun = nil

		// 计算延迟（必须在 CompleteRun 之前，因为 CompleteRun 需要 durationMs）
		if !m.turnStartTime.IsZero() {
			elapsedMs = time.Since(m.turnStartTime).Milliseconds()
			m.hudLatMs = elapsedMs
		}

		// 捕获 loop 级 token 增量
		loopIn = m.loopPrompt
		loopOut = m.loopCompl

		// 提交到 ContextManager（stats 累加 + 落盘；压缩已在 Loop 内完成）
		result := m.cm.CompleteRun(ev.Messages, m.loopPrompt, m.lastTurnPrompt, m.loopCompl, m.loopCacheHit, m.loopCacheMiss, m.loopReasoning, m.hudModel, elapsedMs, string(ev.Reason))

		// loop 级增量归零，准备下一个 loop
		m.loopPrompt = 0
		m.loopCompl = 0
		m.loopCacheHit = 0
		m.loopCacheMiss = 0
		m.loopReasoning = 0
		m.lastTurnPrompt = 0
		m.lastTurnCompl = 0

		// Tier 3 完成后 ctx bar 归零，等待下一个 TurnStats 用 API 真实值恢复
		if result.Compaction.Tier3SummaryDone {
			m.lastPromptTokens = 0
		}
	}

	// 收敛未完成的 thought → stateCollapsed（用户手动展开的保持展开）
	for i := range m.paras {
		p := &m.paras[i]
		if p.Type == paraThought && (p.State == stateStreaming || p.State == stateExpanded) {
			// 估算 thought token 数
			if p.ThoughtTokens == 0 {
				p.ThoughtTokens = len([]rune(p.Text)) / 3 // 粗略估算
				if p.ThoughtTokens < 1 {
					p.ThoughtTokens = 1
				}
			}
			if p.State == stateStreaming {
				p.State = stateCollapsed
				p.renderDirty = true
			}
		}
		if p.Type == paraAssistant && p.State == stateStreaming {
			p.State = stateDone
			p.renderDirty = true
		}
		if p.Type == paraTool && p.State == stateStreaming {
			// 可能某些 tool 没有结果（异常情况）
			p.State = stateDone
			p.renderDirty = true
		}
		// 如果 paraSubagent 在 loop 终止时仍为 streaming，标记为 error
		// 正常情况下 SubagentEnd 事件会在 loop 终止前到达
		if p.Type == paraSubagent && p.State == stateStreaming {
			p.State = stateError
			p.ToolError = "subagent interrupted (loop terminated)"
			p.renderDirty = true
		}
	}

	// 所有终止原因都追加系统提示段落（仅当前 run）
	if !isStale {
		elapsedStr := formatDuration(elapsedMs)
		switch ev.Reason {
		case agentloop.ReasonCompleted:
			m.paras = append(m.paras, Paragraph{
				Type:      paraSystem,
				State:     stateDone,
				Text:      fmt.Sprintf(m.msg().LoopCompleted, ev.Turn, elapsedStr, shortTokens(loopIn), shortTokens(loopOut)),
				NotifKind: notifInfo,
			})
		case agentloop.ReasonMaxTurns:
			m.paras = append(m.paras, Paragraph{
				Type:      paraSystem,
				State:     stateDone,
				Text:      fmt.Sprintf(m.msg().LoopMaxTurns, ev.Turn, elapsedStr, shortTokens(loopIn), shortTokens(loopOut)),
				NotifKind: notifInfo,
			})
		case agentloop.ReasonAborted:
			abortText := fmt.Sprintf(m.msg().LoopAborted, elapsedStr)
			abortKind := notifInfo
			if m.toolTimeout > 0 && isTimeoutError(ev.Err) {
				abortText = fmt.Sprintf(m.msg().LoopToolTimeout, m.toolTimeoutSource, formatDuration(m.toolTimeout.Milliseconds()), elapsedStr)
				abortKind = notifError
			}
			m.paras = append(m.paras, Paragraph{
				Type:      paraSystem,
				State:     stateDone,
				Text:      abortText,
				NotifKind: abortKind,
			})
		case agentloop.ReasonModelError:
			m.paras = append(m.paras, Paragraph{
				Type:      paraSystem,
				State:     stateDone,
				Text:      fmt.Sprintf(m.msg().LoopModelError, elapsedStr, ev.Err),
				NotifKind: notifError,
			})
		case agentloop.ReasonToolFatal:
			text := fmt.Sprintf(m.msg().LoopToolFatal, elapsedStr, ev.Err)
			if m.toolTimeout > 0 && isTimeoutError(ev.Err) {
				text = fmt.Sprintf(m.msg().LoopToolTimeout, m.toolTimeoutSource, formatDuration(m.toolTimeout.Milliseconds()), elapsedStr)
			}
			m.paras = append(m.paras, Paragraph{
				Type:      paraSystem,
				State:     stateDone,
				Text:      text,
				NotifKind: notifError,
			})
		}
	}

	// 段落数超过上限时淘汰旧段落，防止内存无限增长
	m.trimParas()

	// 异步查询余额（不影响主流程）
	if !isStale && m.llmClient.SupportsBalance() {
		client := m.llmClient
		program := m.program
		go func() {
			if balance, err := client.GetBalance(context.Background()); err == nil && balance != nil {
				if program != nil {
					program.Send(agentloop.BalanceUpdate{Balance: balance})
				}
			}
		}()
	}
}

// ---------------------------------------------------------------------------
// Subagent 事件处理
// ---------------------------------------------------------------------------

// handleSubagentStart 在子 agent 开始执行时创建 paraSubagent 段落。
func (m *model) handleSubagentStart(ev subagent.SubagentStart) {
	m.paras = append(m.paras, Paragraph{
		Type:           paraSubagent,
		State:          stateStreaming,
		SubagentType:   ev.AgentType,
		SubagentPrompt: ev.Prompt,
	})
}

// handleSubagentEvent 处理子 agent 的内部事件——追加文本或记录工具调用。
func (m *model) handleSubagentEvent(ev subagent.SubagentEvent) {
	// 找到最后一个 paraSubagent 段落（stateStreaming）
	for i := len(m.paras) - 1; i >= 0; i-- {
		p := &m.paras[i]
		if p.Type == paraSubagent && p.State == stateStreaming {
			switch ev.Kind {
			case subagent.SubagentText:
				p.Text += ev.TextDelta
				p.renderDirty = true
			case subagent.SubagentToolStart:
				// 内联工具调用摘要
				line := fmt.Sprintf("● %s  %s", ev.ToolName, ev.ToolArgs)
				if p.Text == "" {
					p.Text = line
				} else {
					p.Text += "\n" + line
				}
				p.renderDirty = true
			case subagent.SubagentToolResult:
				if ev.ToolResult != "" {
					// 追加工具结果
					p.Text += "\n" + ev.ToolResult
					p.renderDirty = true
				}
			}
			return
		}
	}
}

// handleSubagentEnd 在子 agent 结束时更新段落状态。
func (m *model) handleSubagentEnd(ev subagent.SubagentEnd) {
	for i := len(m.paras) - 1; i >= 0; i-- {
		p := &m.paras[i]
		if p.Type == paraSubagent && p.State == stateStreaming {
			p.SubagentTurns = ev.TotalTurns
			p.SubagentPromptTok = ev.PromptTokens
			p.SubagentComplTok = ev.CompletionTokens
			p.ToolDurMs = ev.DurationMs
			if ev.Error != "" {
				p.State = stateError
				p.ToolError = ev.Error
			} else {
				p.State = stateDone
			}
			p.renderDirty = true
			return
		}
	}
}

// isTimeoutError 判断错误是否为超时引起（context deadline exceeded 或包含 deadline exceeded 字样）。
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return strings.Contains(err.Error(), "deadline exceeded")
}

// ---------------------------------------------------------------------------
// Transcript 持久化
// ---------------------------------------------------------------------------

// flushTranscript 将新完成的段落写入 transcript JSONL 文件。
// 应在每次修改 m.paras 后调用。
func (m *model) flushTranscript() {
	if m.transcriptPath == "" {
		return
	}
	for i := m.transcriptWritten; i < len(m.paras); i++ {
		p := &m.paras[i]
		// 跳过仍在流式中的段落（tool、assistant、thought）
		if p.State == stateStreaming {
			continue
		}
		line := paragraphToTranscriptLine(p)
		if err := ctxpkg.AppendTranscriptLine(m.transcriptPath, line); err != nil {
			// 静默失败：transcript 是 best-effort 增强，不应影响主流程
			return
		}
		m.transcriptWritten = i + 1
	}
}

// paragraphToTranscriptLine 将 TUI Paragraph 转为 transcript 行。
func paragraphToTranscriptLine(p *Paragraph) ctxpkg.TranscriptLine {
	line := ctxpkg.TranscriptLine{
		Text:          p.Text,
		ToolName:      p.ToolName,
		ToolArgs:      p.ToolArgs,
		ToolResult:    p.ToolResult,
		ToolError:     p.ToolError,
		ToolDurMs:     p.ToolDurMs,
		ThoughtTokens: p.ThoughtTokens,
	}
	switch p.NotifKind {
	case notifWarn:
		line.NotifKind = "warn"
	case notifError:
		line.NotifKind = "error"
	default:
		line.NotifKind = "info"
	}
	switch p.Type {
	case paraUser:
		line.Type = "user"
	case paraThought:
		line.Type = "thought"
	case paraAssistant:
		line.Type = "assistant"
	case paraTool:
		line.Type = "tool"
	case paraSystem:
		line.Type = "system"
	case paraSubagent:
		line.Type = "subagent"
	}
	switch p.State {
	case stateDone:
		line.State = "done"
	case stateCollapsed:
		line.State = "collapsed"
	case stateExpanded:
		line.State = "expanded"
	case stateError:
		line.State = "error"
	}
	return line
}

// transcriptLineToParagraph 将 transcript 行还原为 TUI Paragraph。
func transcriptLineToParagraph(line ctxpkg.TranscriptLine) Paragraph {
	p := Paragraph{
		Text:          line.Text,
		ToolName:      line.ToolName,
		ToolArgs:      line.ToolArgs,
		ToolResult:    line.ToolResult,
		ToolError:     line.ToolError,
		ToolDurMs:     line.ToolDurMs,
		ThoughtTokens: line.ThoughtTokens,
	}
	switch line.NotifKind {
	case "warn":
		p.NotifKind = notifWarn
	case "error":
		p.NotifKind = notifError
	default:
		p.NotifKind = notifInfo
	}
	switch line.Type {
	case "user":
		p.Type = paraUser
	case "thought":
		p.Type = paraThought
	case "assistant":
		p.Type = paraAssistant
	case "tool":
		p.Type = paraTool
	case "system":
		p.Type = paraSystem
	case "subagent":
		p.Type = paraSubagent
	}
	switch line.State {
	case "done":
		p.State = stateDone
	case "collapsed":
		p.State = stateCollapsed
	case "expanded":
		p.State = stateExpanded
	case "error":
		p.State = stateError
	}
	return p
}

// replayTranscript 从 transcript 文件加载最后 N 行并还原为段落。
// 用于 --resume 时在 viewport 中显示最近的对话历史。
func (m *model) replayTranscript() {
	if m.transcriptPath == "" {
		return
	}
	lines, err := ctxpkg.LoadTranscriptLines(m.transcriptPath)
	if err != nil || len(lines) == 0 {
		return
	}

	m.paras = make([]Paragraph, 0, len(lines))
	for _, l := range lines {
		m.paras = append(m.paras, transcriptLineToParagraph(l))
	}
	m.transcriptWritten = len(m.paras)
}

// ---------------------------------------------------------------------------
// 折叠/展开切换
// ---------------------------------------------------------------------------

// hasStreamingPara 返回当前是否存在流式中的段落（thought / assistant / tool）。
// 仅检查尾部 3 个段落——流式段落始终在列表末尾，O(1)。
func (m *model) hasStreamingPara() bool {
	for i := len(m.paras) - 1; i >= 0 && i >= len(m.paras)-3; i-- {
		if m.paras[i].State == stateStreaming {
			return true
		}
	}
	return false
}

// trimParas 当段落数超过 maxParas 时从头部淘汰旧段落。
func (m *model) trimParas() {
	if len(m.paras) <= maxParas {
		return
	}
	remove := len(m.paras) - maxParas

	// 淘汰旧段落
	m.paras = append([]Paragraph{}, m.paras[remove:]...)

	// 焦点索引跟随偏移
	if m.focusIndex >= 0 {
		m.focusIndex -= remove
		if m.focusIndex < 0 {
			m.focusIndex = -1
			m.exitFocusMode() // cmd 无法传播，仅重置状态
		}
	}

	// 同步 transcript 写入指针
	if m.transcriptWritten > 0 {
		m.transcriptWritten -= remove
		if m.transcriptWritten < 0 {
			m.transcriptWritten = 0
		}
	}
}

// isExpandable 判断段落是否可通过焦点 Enter 展开/折叠。
// contentWidth 用于计算内容换行后的行数，避免将未溢出预览区的段落标记为可交互。
func isExpandable(p *Paragraph, contentWidth int) bool {
	switch p.Type {
	case paraThought:
		if p.State != stateCollapsed && p.State != stateExpanded {
			return false
		}
		// 计算折叠预览是否真的需要展开：折叠态仅展示前 2 行，
		// 若全部内容换行后 ≤ 2 行则无需展开，直接跳过。
		if p.State == stateCollapsed {
			return countWrappedLines(p.Text, contentWidth-2) > 2
		}
		return true
	case paraTool:
		// shell / web_fetch / skill 的输出值得展开/折叠，其他工具的输出
		// 或为结构化摘要（grep/ls/search_file/lsp_*）或通过预览行已传达完整信息。
		switch p.ToolName {
		case "bash", "web_fetch", "skill":
			if p.State != stateDone && p.State != stateCollapsed && p.State != stateExpanded && p.State != stateError {
				return false
			}
			// 折叠预览至多展示 maxPreviewWrapped 行，若全部输出未溢出则无需展开。
			// 阈值与 renderToolPreview 中 writeWrappedPreview 的截断条件保持一致。
			if p.State == stateDone || p.State == stateCollapsed || p.State == stateError {
				body := stripToolStatusHeader(p.ToolResult)
				if body == "" && p.ToolError != "" {
					body = p.ToolError
				}
				if p.ToolName == "web_fetch" {
					// web_fetch 预览跳过空行，计数时也跳过
					body = parseWebFetchBody(p.ToolResult)
					if body == "" && p.ToolError != "" {
						body = p.ToolError
					}
					return countWrappedLinesNonEmpty(body, contentWidth-2) >= maxPreviewWrapped
				}
				return countWrappedLines(body, contentWidth-2) >= maxPreviewWrapped
			}
			return true
		}
		return false
	case paraSubagent:
		// subagent 容器始终可展开（done/collapsed/expanded/error 态均可交互）
		return p.State != stateStreaming
	}
	return false
}

// countWrappedLines 计算文本在指定宽度下换行后的总行数。
func countWrappedLines(text string, width int) int {
	if width < 1 {
		width = 1
	}
	total := 0
	for _, line := range strings.Split(text, "\n") {
		total += len(wrapLine(line, width))
	}
	return total
}

// countWrappedLinesNonEmpty 同 countWrappedLines，但跳过空行。
// 用于 web_fetch 预览行数估算，与 renderToolPreview 跳过空行的行为保持一致。
func countWrappedLinesNonEmpty(text string, width int) int {
	if width < 1 {
		width = 1
	}
	total := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		total += len(wrapLine(line, width))
	}
	return total
}

// enterFocusMode 进入段落焦点模式：输入框失焦、placeholder 提示退出方式。
func (m *model) enterFocusMode() {
	m.input.Blur()
	m.input.Placeholder = m.msg().InputFocusModePlaceholder
}

// exitFocusMode 退出段落焦点模式：输入框恢复焦点、placeholder 恢复默认。
// 返回 Focus 的 tea.Cmd（光标闪烁），调用方应将其合并到 Update 返回值中。
func (m *model) exitFocusMode() tea.Cmd {
	// Prompt 在 New() 中默认设为 "> "，零值 Model 为空字符串，
	// 借此区分测试中未初始化的 input，避免 virtualCursor.Blink 空指针。
	if m.input.Prompt != "" {
		cmd := m.input.Focus()
		m.input.Placeholder = m.msg().InputPlaceholder
		return cmd
	}
	m.input.Placeholder = m.msg().InputPlaceholder
	return nil
}

// focusIndexForViewport 返回当前聚焦段落对应的 viewport 行号，-1 表示未聚焦或不可见。
// 由 View() 调用以计算滚动偏移，将聚焦段落拉入可见区域。

// focusNext 将焦点移到下一个可交互段落（环形），无可交互段落时焦点归位。
func (m *model) focusNext() tea.Cmd {
	if len(m.paras) == 0 {
		m.focusIndex = -1
		return m.exitFocusMode()
	}

	contentWidth := max(m.width-4, 20)
	wasFocused := m.focusIndex >= 0
	start := m.focusIndex + 1
	if start < 0 {
		start = 0
	}
	for i := 0; i < len(m.paras); i++ {
		idx := (start + i) % len(m.paras)
		if isExpandable(&m.paras[idx], contentWidth) {
			m.focusIndex = idx
			if !wasFocused {
				m.enterFocusMode()
			}
			return nil
		}
	}
	// 无可交互段落
	m.focusIndex = -1
	return m.exitFocusMode()
}

// focusPrev 将焦点移到上一个可交互段落（环形），无可交互段落时焦点归位。
func (m *model) focusPrev() tea.Cmd {
	if len(m.paras) == 0 {
		m.focusIndex = -1
		return m.exitFocusMode()
	}

	contentWidth := max(m.width-4, 20)
	wasFocused := m.focusIndex >= 0
	start := m.focusIndex - 1
	if start < 0 {
		start = len(m.paras) - 1
	}
	for i := 0; i < len(m.paras); i++ {
		idx := (start - i + len(m.paras)) % len(m.paras)
		if isExpandable(&m.paras[idx], contentWidth) {
			m.focusIndex = idx
			if !wasFocused {
				m.enterFocusMode()
			}
			return nil
		}
	}
	m.focusIndex = -1
	return m.exitFocusMode()
}

// toggleParagraphFocus 切换当前聚焦段落的展开/折叠状态。
func (m *model) toggleParagraphFocus() {
	if m.focusIndex < 0 || m.focusIndex >= len(m.paras) {
		return
	}
	p := &m.paras[m.focusIndex]
	switch p.Type {
	case paraThought:
		switch p.State {
		case stateCollapsed:
			p.State = stateExpanded
		case stateExpanded:
			p.State = stateCollapsed
		}
	case paraTool:
		switch p.State {
		case stateDone, stateCollapsed:
			p.State = stateExpanded
		case stateError:
			p.State = stateExpanded
		case stateExpanded:
			if p.ToolError != "" || p.ToolDenied {
				p.State = stateError
			} else {
				p.State = stateDone
			}
		}
	case paraSubagent:
		switch p.State {
		case stateDone, stateCollapsed:
			p.State = stateExpanded
		case stateExpanded:
			p.State = stateDone
		case stateError:
			p.State = stateExpanded
		}
	}
	p.renderDirty = true
}

// viewportCtx 返回段落渲染所需的上下文（spinners + Glamour renderer）。
func (m *model) viewportCtx() ViewportCtx {
	contentWidth := max(m.width-4, 20)
	return ViewportCtx{
		Asst:     m.spAsst,
		Thought:  m.spThought,
		Tool:     m.spTool,
		Subagent: m.spSubagent,
		Glamour:  m.glamourRenderer,
		Width:    contentWidth,
		LC:       m.msg(),
	}
}

// streamingIsLastOnly 返回是否仅最后一个段落处于流式状态。
func (m *model) streamingIsLastOnly() bool {
	if len(m.paras) == 0 {
		return false
	}
	last := m.paras[len(m.paras)-1]
	return last.State == stateStreaming
}

// ---------------------------------------------------------------------------
// Agent Loop 集成
// ---------------------------------------------------------------------------

// enterPlanModeByUser 用户通过 Shift+Tab 快捷键直接进入 plan 模式。
func (m *model) enterPlanModeByUser() tea.Cmd {
	m.inPlanMode = true
	m.planEnteredByUser = true
	m.input.Placeholder = m.msg().InputPlanModePlaceholder
	// 生成 plan 文件路径 + 配对 ID（Guard 由 loop.SetPlanMode 在 doTurn 时激活）
	m.planFile = m.generatePlanFilePath()
	m.planPairID = generatePairIDForTUI()
	// 直接设置状态即可，Update 返回后 Bubble Tea 会自动重绘。
	// 不调用 m.program.Send() —— 在 Update 内同步 Send 会导致死锁：
	// Send 写入 unbuffered p.msgs channel，而主循环在 Update 返回前不会读 channel。
	return nil
}

// exitPlanModeByUser 用户通过 Shift+Tab 快捷键直接退出 plan 模式（不走审批）。
//
// 无论进入方式（用户快捷键 / LLM enter_plan_mode），均注入 [plan:end] 通知 LLM。
// Guard 恢复由 TUI 管理（用户入口）或 Loop 后续处理（LLM 入口，Agent 下次执行时
// executeExitPlanMode 会再次调用 Guard.ExitPlanMode，重复调用无害）。
func (m *model) exitPlanModeByUser() tea.Cmd {
	m.guard.ExitPlanMode()
	m.loop.ResetPlanMode()
	if m.planStartSent && m.planPairID != "" {
		m.planExitPending = true
		m.planExitPendingPairID = m.planPairID
	}
	m.inPlanMode = false
	m.planEnteredByUser = false
	m.planFile = ""
	m.planPairID = ""
	m.planStartSent = false
	m.input.Placeholder = m.msg().InputPlaceholder
	return nil
}

// doTurn 启动一轮真实的 agent loop 执行（异步流式）。
func (m *model) doTurn(userInput string) tea.Cmd {
	// 关闭文件选择器（如有）
	m.closePicker()

	// 保存到输入历史（去重相邻重复；限制 100 条）
	m.saveToHistory(userInput)

	// 0. 解析并展开 @ 引用
	expanded, _, expandErr := m.expander.Expand(context.Background(), userInput, m.cwd)
	if expandErr != nil {
		expanded = userInput
	}

	// 前置更新：Loop 计数器 +1，HUD 显示"正要开始第 N 轮"
	m.hudTurns++

	// 1. PrepareRun — 使用展开后的输入
	messagesSnapshot := m.cm.PrepareRun(expanded)

	// 1.4 上轮用户快捷键退出 plan 模式 → 注入 [plan:end] 通知 LLM
	if m.planExitPending {
		endMsg := llm.Message{
			Role:    llm.RoleUser,
			Content: fmt.Sprintf("[plan:end #%s] User exited plan mode. You are now in normal mode.", m.planExitPendingPairID),
		}
		messagesSnapshot = append(messagesSnapshot, endMsg)
		m.planExitPending = false
		m.planExitPendingPairID = ""
	}

	// 1.5 如果当前处于 plan 模式（用户快捷键进入），注入 [plan:start] 消息并配置 Loop
	// 仅在首次进入时注入，后续轮次不重复注入（避免产生多个 [plan:start] 孤对）
	if m.inPlanMode && m.planFile != "" && !m.planStartSent {
		m.loop.SetPlanFile(m.planFile)
		pairID, startMsg := m.loop.SetPlanMode(m.planFile)
		m.planPairID = pairID
		m.planStartSent = true
		// 在 user 消息之后注入 plan:start
		messagesSnapshot = append(messagesSnapshot, startMsg)
	}

	// ctx bar 保持上轮压缩后值，待 TurnStats 用 API PromptTokens 更新

	// 2. 追加 user 段落
	m.paras = append(m.paras, Paragraph{
		Type:  paraUser,
		State: stateDone,
		Text:  userInput,
	})
	m.flushTranscript()

	m.running = true
	m.focusIndex = -1
	m.input.Placeholder = m.msg().InputAgentRunning
	m.runGeneration++
	m.turnStartTime = time.Now()
	m.scrollToBottom() // 用户输入新消息 → 滚动到底部

	// 若已有运行中的 loop，先取消（用户中断后立即发新任务）。
	if m.cancelRun != nil {
		m.cancelRun()
		m.cancelRun = nil
		// 旧 loop 的 TurnStats 可能已累加到 loop 级计数器，
		// 其 LoopDone 将以 isStale 到达（不会归零），需显式清理。
		m.loopPrompt = 0
		m.loopCompl = 0
		m.loopCacheHit = 0
		m.loopCacheMiss = 0
		m.loopReasoning = 0
	}

	// 创建可取消的 context（在 goroutine 外创建，避免 race）
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelRun = cancel

	// 3. 返回一个 tea.Cmd：在 goroutine 中消费 loop.Run() channel，
	//    通过 p.Send 实时推送事件到 TUI Update。
	// 注意：goroutine 仅 defer cancel() 自己的 ctx，不管理 m.cancelRun。
	// m.cancelRun 的生命周期由 doTurn（创建）和 handleLoopDone（清除）控制。
	gen := m.runGeneration // 闭包捕获当前代数，LoopDone 时带回比对
	return func() tea.Msg {
		defer cancel()

		for ev := range m.loop.Run(ctx, messagesSnapshot) {
			// 将 LoopDone 包装为 loopDoneWithGen，携带代数用于 handleLoopDone 去重。
			if done, ok := ev.(agentloop.LoopDone); ok {
				ev = agentloop.LoopDoneWithGen{LoopDone: done, Generation: gen}
			}
			// 推送事件到 TUI（goroutine-safe via buffered channel）
			if m.program != nil {
				m.program.Send(ev)
			}
		}

		return nil
	}
}

// ---------------------------------------------------------------------------
// 输入历史
// ---------------------------------------------------------------------------

// saveToHistory 将输入保存到历史列表。跳过空输入和相邻重复。
func (m *model) saveToHistory(input string) {
	input = strings.TrimSpace(input)
	if input == "" {
		return
	}
	if len(m.inputHistory) > 0 && m.inputHistory[0] == input {
		return
	}
	m.inputHistory = append([]string{input}, m.inputHistory...)
	if len(m.inputHistory) > 100 {
		m.inputHistory = m.inputHistory[:100]
	}
	m.historyPos = -1
}

// navigateHistoryUp 向上导航历史（更早的输入）。返回 true 表示已消费。
func (m *model) navigateHistoryUp() bool {
	if len(m.inputHistory) == 0 {
		return false
	}
	if m.historyPos == -1 {
		// 首次进入历史导航，保存当前草稿
		m.historyDraft = m.input.Value()
		m.historyPos = 0
	} else if m.historyPos < len(m.inputHistory)-1 {
		m.historyPos++
	} else {
		// 已到最早记录，不再前进
		return true
	}
	m.input.SetValue(m.inputHistory[m.historyPos])
	m.input.CursorEnd()
	return true
}

// navigateHistoryDown 向下导航历史（更新的输入或回到草稿）。返回 true 表示已消费。
func (m *model) navigateHistoryDown() bool {
	if m.historyPos == -1 {
		return false
	}
	if m.historyPos > 0 {
		m.historyPos--
		m.input.SetValue(m.inputHistory[m.historyPos])
		m.input.CursorEnd()
		return true
	}
	// historyPos == 0，恢复到进入导航前的草稿
	m.historyPos = -1
	m.input.SetValue(m.historyDraft)
	m.input.CursorEnd()
	return true
}

// ---------------------------------------------------------------------------
// 滚动控制
// ---------------------------------------------------------------------------

// scrollUp 向上滚动 delta 行（查看更早的内容）。
func (m *model) scrollUp(delta int) {
	if delta <= 0 {
		return
	}
	m.pinnedToBottom = false
	m.scrollTop -= delta
	if m.scrollTop < 0 {
		m.scrollTop = 0
	}
}

// scrollDown 向下滚动 delta 行（查看更新的内容）。
func (m *model) scrollDown(delta int) {
	if delta <= 0 {
		return
	}
	m.scrollTop += delta
}

// scrollToBottom 滚动到底部并锁定自动跟随。
func (m *model) scrollToBottom() {
	m.scrollTop = 0
	m.pinnedToBottom = true
}

// ---------------------------------------------------------------------------
// 视图
// ---------------------------------------------------------------------------

// syncOtherInputVisibleStart 与 syncInputVisibleStart 对称，用于 otherInput。
func (m *model) syncOtherInputVisibleStart() {
	value := m.otherInput.Value()
	pos := m.otherInput.Position()
	w := m.otherInput.Width()
	runes := []rune(value)
	m.otherInputLastValue = value

	totalWidth := 0
	for _, r := range runes {
		totalWidth += lipgloss.Width(string(r))
	}
	if w <= 0 || totalWidth <= w {
		m.otherInputVisStart = 0
		return
	}

	if m.otherInputVisStart < 0 || m.otherInputVisStart > len(runes) {
		m.otherInputVisStart = 0
	}
	if pos < m.otherInputVisStart {
		m.otherInputVisStart = pos
	}

	visEnd := m.otherInputVisStart
	visWidth := 0
	for visEnd < len(runes) {
		cw := lipgloss.Width(string(runes[visEnd]))
		if visWidth+cw > w {
			break
		}
		visWidth += cw
		visEnd++
	}

	if pos >= visEnd {
		m.otherInputVisStart = pos
		visWidth = 0
		for m.otherInputVisStart > 0 {
			cw := lipgloss.Width(string(runes[m.otherInputVisStart-1]))
			if visWidth+cw > w {
				break
			}
			visWidth += cw
			m.otherInputVisStart--
		}
	}
}

func (m *model) View() tea.View {
	if m.height < 10 {
		// 终端太小，无法正常布局，返回空视图
		v := tea.NewView("")
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}

	contentWidth := max(m.width-4, 20)

	// 1. 渲染段落内容（全量，后续根据滚动偏移裁剪可见区域）
	ctx := m.viewportCtx()
	allLines, _ := buildViewportContent(m.paras, ctx, m.focusIndex, 0)

	// 2. 渲染覆盖层
	var overlayContent string
	switch m.overlay {
	case overlayPermission:
		if m.permReq != nil {
			boxWidth := contentWidth
			if boxWidth > 70 {
				boxWidth = 70
			}
			overlayContent = m.renderPermOverlay(boxWidth)
		}
	case overlayQuestion:
		if m.questionReq != nil {
			boxWidth := contentWidth
			if boxWidth > 70 {
				boxWidth = 70
			}
			overlayContent = m.renderQuestionOverlay(boxWidth)
		}
	case overlayThemePicker:
		boxWidth := contentWidth
		if boxWidth > 50 {
			boxWidth = 50
		}
		overlayContent = m.renderThemePickerOverlay(boxWidth)
	case overlayModelPicker:
		boxWidth := contentWidth
		if boxWidth > 60 {
			boxWidth = 60
		}
		overlayContent = m.renderModelPickerOverlay(boxWidth)
	case overlayLocalePicker:
		boxWidth := contentWidth
		if boxWidth > 50 {
			boxWidth = 50
		}
		overlayContent = m.renderLocalePickerOverlay(boxWidth)
	case overlayPlanEnter:
		boxWidth := contentWidth
		if boxWidth > 70 {
			boxWidth = 70
		}
		overlayContent = m.renderPlanEnterOverlay(boxWidth)
	case overlayPlanExit:
		boxWidth := contentWidth
		if boxWidth > 80 {
			boxWidth = 80
		}
		overlayContent = m.renderPlanExitOverlay(boxWidth)
	}
	var pickerContent string
	if m.pickerVisible {
		pickerContent = m.renderPickerDropdown(contentWidth)
	}
	var commandPickerContent string
	if m.commandPickerVisible {
		commandPickerContent = m.renderCommandPickerDropdown(contentWidth)
	}

	overlayLines := 0
	if overlayContent != "" {
		overlayLines = strings.Count(overlayContent, "\n") + 1
	}
	pickerLines := 0
	if pickerContent != "" {
		pickerLines = strings.Count(pickerContent, "\n") + 1
	}
	commandPickerLines := 0
	if commandPickerContent != "" {
		commandPickerLines = strings.Count(commandPickerContent, "\n") + 1
	}

	// 3. 计算固定区域高度
	header := m.renderHeader()
	headerHeight := lipgloss.Height(header) + 1 // +1 是 header 后的空行

	footer := m.renderFooter()
	footerHeight := lipgloss.Height(footer) // 2 行（Line 1 + Line 2）

	// 先渲染输入框，获取实际高度（textarea 可能多行）
	separator := m.renderInputSeparator(contentWidth)
	rawView := m.input.View()
	// 将第一行开头的缩进空格替换为 › 前缀（area prompt 统一用缩进空格，
	// 但视觉上希望第一行显示 ›，后续行保持缩进对齐）。
	// 替换策略：查找第一个不由 ANSI 序列开头的 "  " 位置。
	if idx := findFirstPromptPos(rawView); idx >= 0 {
		rawView = rawView[:idx] + "› " + rawView[idx+2:]
	}
	inputView := lipgloss.NewStyle().Width(contentWidth).Render(rawView)
	inputHeight := lipgloss.Height(inputView)

	// 固定底部元素（在 styleApp 内）：
	// separator(1) + input(inputHeight) + footer(footerHeight)
	fixedBottomHeight := 1 + inputHeight + footerHeight

	// styleApp 顶部 padding 1 行，底部 0；内区可用高度 = m.height - 1
	innerHeight := m.height - 1
	bodyHeight := innerHeight - headerHeight - fixedBottomHeight - overlayLines - pickerLines - commandPickerLines
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	m.bodyHeight = bodyHeight

	// 4. 根据滚动偏移裁剪可见内容
	totalLines := len(allLines)
	maxScrollTop := max(0, totalLines-bodyHeight)

	if m.pinnedToBottom {
		m.scrollTop = maxScrollTop
	} else {
		// 内容增长时 scrollTop 可能超出新的 maxScrollTop
		if m.scrollTop > maxScrollTop {
			m.scrollTop = maxScrollTop
		}
		if m.scrollTop < 0 {
			m.scrollTop = 0
		}
		// 用户已滚动到底部 → 重新锁定
		if m.scrollTop >= maxScrollTop {
			m.pinnedToBottom = true
			m.scrollTop = maxScrollTop
		}
	}

	var visibleLines []string
	if totalLines > 0 {
		end := m.scrollTop + bodyHeight
		if end > totalLines {
			end = totalLines
		}
		visibleLines = allLines[m.scrollTop:end]
	}

	// 5. 构建 parts：header + body(刚好 bodyHeight 行) + overlays + 固定底部
	parts := []string{header, ""}
	parts = append(parts, visibleLines...)

	// 用空行补足 body 区域到 bodyHeight 行，确保 footer 位置固定
	padLines := bodyHeight - len(visibleLines)
	for i := 0; i < padLines; i++ {
		parts = append(parts, "")
	}

	if overlayContent != "" {
		parts = append(parts, overlayContent)
	}
	if pickerContent != "" {
		parts = append(parts, pickerContent)
	}
	if commandPickerContent != "" {
		parts = append(parts, commandPickerContent)
	}
	parts = append(parts, separator, inputView, footer)

	mainBody := lipgloss.JoinVertical(lipgloss.Left, parts...)
	mainContent := styleApp.Render(mainBody)

	v := tea.NewView(mainContent)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion

	// real cursor 模式：定位输入光标
	if m.overlay == overlayNone {
		if cur := m.input.Cursor(); cur != nil {
			// 布局：styleApp top(1) + header + 空行 + body + overlays + picker + separator(1)
			cur.Y += 1 + headerHeight + bodyHeight + overlayLines + pickerLines + commandPickerLines + 1
			cur.X += 2 // styleApp 左 padding
			if cur.X > m.width-2 {
				cur.X = m.width - 2
			}
			if cur.Y >= m.height {
				cur.Y = m.height - 1
			}
			v.Cursor = cur
		}
	} else if m.overlay == overlayQuestion && m.questionFormIsOther {
		// Other 自定义输入：光标定位在 overlay box 内
		// 布局：styleApp top(1) + header + 空行 + body(contentWidth) + overlay box
		//   overlay box 内部：borderTop(0), padTop(1), title(2), 空行(3), otherInput(4)
		if cur := m.otherInput.Cursor(); cur != nil {
			pos := m.otherInput.Position()
			value := m.otherInput.Value()
			runes := []rune(value)
			if pos > len(runes) {
				pos = len(runes)
			}
			// 钳位滚动偏移（与主输入框同款防御）
			if m.otherInputVisStart < 0 || m.otherInputVisStart > len(runes) || m.otherInputVisStart > pos {
				m.syncOtherInputVisibleStart()
				if pos > len(runes) {
					pos = len(runes)
				}
				if m.otherInputVisStart < 0 {
					m.otherInputVisStart = 0
				}
				if m.otherInputVisStart > pos {
					m.otherInputVisStart = pos
				}
			}
			visibleBeforeCursor := string(runes[m.otherInputVisStart:pos])
			cursorX := lipgloss.Width(m.otherInput.Prompt) + lipgloss.Width(visibleBeforeCursor)
			// X: styleApp 左 padding(2) + box border(1) + box 左 padding(2) = 5
			cur.X = cursorX + 5
			if cur.X > m.width-2 {
				cur.X = m.width - 2
			}
			// Y: styleApp top(1) + headerHeight + bodyHeight（已含 header 后空行） + 4(box内偏移: border+pad+title+空行)
			cur.Y = 1 + headerHeight + bodyHeight + 4
			if cur.Y >= m.height {
				cur.Y = m.height - 1
			}
			v.Cursor = cur
		}
	}
	return v
}

// ---------------------------------------------------------------------------
// Header 渲染
// ---------------------------------------------------------------------------

// asciiArt 是 "WAVELOOM" 的 header 字标。
var asciiArt = []string{
	`██╗    ██╗ █████╗ ██╗   ██╗███████╗██╗      ██████╗  ██████╗ ███╗   ███╗`,
	`██║    ██║██╔══██╗██║   ██║██╔════╝██║     ██╔═══██╗██╔═══██╗████╗ ████║`,
	`██║ █╗ ██║███████║██║   ██║█████╗  ██║     ██║   ██║██║   ██║██╔████╔██║`,
	`██║███╗██║██╔══██║╚██╗ ██╔╝██╔══╝  ██║     ██║   ██║██║   ██║██║╚██╔╝██║`,
	`╚███╔███╔╝██║  ██║ ╚████╔╝ ███████╗███████╗╚██████╔╝╚██████╔╝██║ ╚═╝ ██║`,
	` ╚══╝╚══╝ ╚═╝  ╚═╝  ╚═══╝  ╚══════╝╚══════╝ ╚═════╝  ╚═════╝ ╚═╝     ╚═╝`,
}

// renderInputSeparator 渲染输入框上方的分隔行。
// 焦点模式：居中全宽提示（操作指引，需要醒目）
// plan 模式：左侧 "▌Plan" 前缀 + ─ 填充（状态标记，不抢眼）
// 正常：纯横线
func (m *model) renderInputSeparator(contentWidth int) string {
	if m.focusIndex >= 0 {
		hint := m.msg().FocusSeparatorHint
		hintStyle := lipgloss.NewStyle().Foreground(colorAccentGold)
		lineStyle := lipgloss.NewStyle().Foreground(colorMuted)
		pad := contentWidth - lipgloss.Width(hint)
		if pad < 2 {
			pad = 2
		}
		left := strings.Repeat("─", pad/2)
		right := strings.Repeat("─", pad-pad/2)
		return lineStyle.Render(left) + hintStyle.Render(hint) + lineStyle.Render(right)
	}
	if m.inPlanMode {
		prefix := lipgloss.NewStyle().Foreground(colorAccentGold).Render("▌Plan")
		lineStyle := lipgloss.NewStyle().Foreground(colorMuted)
		rest := strings.Repeat("─", max(contentWidth-lipgloss.Width(prefix), 0))
		return prefix + lineStyle.Render(rest)
	}
	return lipgloss.NewStyle().
		Foreground(colorMuted).
		Render(strings.Repeat("─", contentWidth))
}

// ---------------------------------------------------------------------------

func (m *model) renderHeader() string {
	contentWidth := max(m.width-4, 20)
	var sb strings.Builder

	if contentWidth >= 80 {
		// 宽屏：6 行渐变色 ASCII art logo（颜色来自当前主题）
		for i, line := range asciiArt {
			s := lipgloss.NewStyle().
				Foreground(colorLogoGradient[i]).
				Bold(true).
				Width(contentWidth).
				Align(lipgloss.Center).
				Render(line)
			sb.WriteString(s)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	} else {
		// 窄屏：单行紧凑 logo（首色）
		logoLine := lipgloss.NewStyle().
			Foreground(colorLogoGradient[0]).
			Bold(true).
			Width(contentWidth).
			Align(lipgloss.Center).
			Render("WAVELOOM")
		sb.WriteString(logoLine)
		sb.WriteString("\n\n")
	}

	// 信息行：session ID（左） + 版本号（右）
	sidLine := ""
	if sid := m.cm.SessionID(); sid != "" {
		sidPart := styleHeaderAccent.Render(m.msg().HeaderSession) +
			styleHeader.Render(sid)
		verStr := styleHeaderAccent.Render(Version)
		leftWidth := lipgloss.Width(sidPart)
		rightWidth := lipgloss.Width(verStr)
		pad := contentWidth - leftWidth - rightWidth
		if pad < 1 {
			pad = 1
		}
		sidLine = lipgloss.NewStyle().Width(contentWidth).Render(
			sidPart + strings.Repeat(" ", pad) + verStr,
		)
	} else {
		// 无 session 时版本号右对齐
		verStr := styleHeaderAccent.Render(Version)
		sidLine = lipgloss.NewStyle().Width(contentWidth).Align(lipgloss.Right).Render(verStr)
	}
	sb.WriteString(sidLine)
	sb.WriteString("\n")

	// 工作区
	cwdDisplay := m.cwd
	maxCwdLen := contentWidth - 6
	if maxCwdLen < 10 {
		maxCwdLen = 10
	}
	if len(cwdDisplay) > maxCwdLen {
		cwdDisplay = cwdDisplay[:maxCwdLen] + "..."
	}
	cwdPart := styleHeaderAccent.Render("↳ ") + styleHeader.Render(cwdDisplay)

	lineCwd := lipgloss.NewStyle().Width(contentWidth).Render(cwdPart)
	sb.WriteString(lineCwd)

	// 通知 banner：右对齐，无背景
	if m.noticeBanner != "" {
		sb.WriteString("\n")
		bannerStyle := lipgloss.NewStyle().
			Foreground(colorAccentGold).
			Width(contentWidth).
			Align(lipgloss.Right)
		sb.WriteString(bannerStyle.Render(m.noticeBanner))
	}

	return sb.String()
}

// normalizeWidth 将全角字符转换为半角，用于模型名前归一化显示。
// 处理范围：全角标点/字母/数字（U+FF01–U+FF5E → U+0021–U+007E）及全角空格。
func normalizeWidth(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == 0x3000: // 全角空格 → 半角空格
			b.WriteByte(' ')
		case 0xFF01 <= r && r <= 0xFF5E: // 全角标点/字母/数字
			b.WriteRune(r - 0xFEE0)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Footer HUD 渲染
// ---------------------------------------------------------------------------

func (m *model) renderFooter() string {
	contentWidth := max(m.width-4, 20)
	sep := "  "

	var sb strings.Builder

	// Line 1: spinner + model name + ctx progress bar
	indicator := styleFooterLabel.Render("•") + " "
	if m.running && m.overlay != overlayQuestion {
		indicator = m.spinner.View() + " "
	}
	modelPart := indicator + styleFooterModel.Render(m.hudModel)
	ctxPart := m.renderCtxBarCompact()

	line1Parts := []string{modelPart, ctxPart}
	line1 := styleFooter.Width(contentWidth).Render(strings.Join(line1Parts, sep))

	// Line 2: cache + turns + messages + latency + balance
	compactingPart := m.renderCacheRate()
	turnsPart := styleFooterLabel.Render("Loop") + " " + styleFooterValue.Render(fmt.Sprintf("%d", m.hudTurns))
	messagesPart := styleFooterLabel.Render("M") + " " + styleFooterValue.Render(fmt.Sprintf("%d", m.hudMessages))
	latencyPart := m.renderLatency()
	balancePart := m.renderBalance()

	line2Parts := []string{compactingPart, turnsPart, messagesPart, latencyPart, balancePart}
	line2Content := strings.Join(line2Parts, sep)
	line2 := styleFooter.Width(contentWidth).Render(line2Content)

	sb.WriteString(line1)
	sb.WriteString("\n")
	sb.WriteString(line2)

	return sb.String()
}

// renderCtxBarCompact 渲染固定宽度的上下文窗口进度条（barWidth=20，每格 5%，ratio < 5% 时进度条留空）。
// currentTokens 来自 lastPromptTokens，由 TurnStats 实时更新。
func (m *model) renderCtxBarCompact() string {
	currentTokens := m.lastPromptTokens
	if currentTokens == 0 {
		return styleFooterLabel.Render("ctx") + " " + styleFooterValueMuted.Render("--")
	}

	barWidth := 20
	ratio := float64(currentTokens) / float64(m.contextLimit)
	if ratio > 1 {
		ratio = 1
	}

	pct := ratio * 100

	// 量化到 5% 步进（20 格，每格 5%），避免部分填充造成视觉误导
	displayRatio := float64(int(ratio*20)) / 20
	m.ctxProgress.SetWidth(barWidth)
	barStr := m.ctxProgress.ViewAs(displayRatio)

	var tokenStyle lipgloss.Style
	switch {
	case pct < 50:
		tokenStyle = styleCtxBarGreenFg
	case pct < 80:
		tokenStyle = styleCtxBarGoldFg
	default:
		tokenStyle = styleCtxBarRedFg
	}

	tokenStr := tokenStyle.Render(fmt.Sprintf("%s/%s",
		formatTokens(currentTokens), formatTokens(m.contextLimit)))

	return styleFooterLabel.Render("ctx") + " " + barStr + " " + tokenStr
}

// renderBalance 渲染余额信息。
func (m *model) renderBalance() string {
	label := styleFooterLabel.Render("bal")
	if m.hudBalance == "" {
		return label + " " + styleFooterValueMuted.Render("--")
	}
	var valStyle lipgloss.Style
	if m.hudBalanceAvail {
		valStyle = styleCacheGreen
	} else {
		valStyle = styleFooterLatRed
	}
	return label + " " + valStyle.Render(m.hudBalance)
}

// renderCacheRate 渲染缓存命中率。
func (m *model) renderCacheRate() string {
	label := styleFooterLabel.Render("cache")
	total := m.hudCacheHit + m.hudCacheMiss
	if total == 0 {
		return label + " " + styleFooterValueMuted.Render("--")
	}

	pct := int(float64(m.hudCacheHit) / float64(total) * 100)

	var valStyle lipgloss.Style
	switch {
	case pct >= 95:
		valStyle = styleCacheGreen
	case pct >= 75:
		valStyle = styleCacheGold
	default:
		valStyle = styleFooterLatRed
	}

	return label + " " + valStyle.Render(fmt.Sprintf("%d%%", pct))
}

// renderLatency 渲染最近一次 loop 耗时（运行中实时计时，结束后显示最终值）。
func (m *model) renderLatency() string {
	label := styleFooterLabel.Render("elap")

	// 运行中：实时计算 time.Since(turnStartTime)
	var elapsed int64
	if m.running && !m.turnStartTime.IsZero() {
		elapsed = time.Since(m.turnStartTime).Milliseconds()
	} else {
		elapsed = m.hudLatMs
	}

	if elapsed == 0 {
		return label + " " + styleFooterValueMuted.Render("--")
	}

	var valStyle lipgloss.Style
	switch {
	case elapsed < 120000:
		valStyle = styleFooterValue
	case elapsed < 600000:
		valStyle = styleCacheGold
	default:
		valStyle = styleFooterLatRed
	}

	return label + " " + valStyle.Render(formatDuration(elapsed))
}



// ---------------------------------------------------------------------------
// tuiUserResponder — 权限确认的 TUI 实现
// ---------------------------------------------------------------------------

// tuiUserResponder 实现 permission.UserResponder 接口。
// 通过 channel 将权限请求发送到 TUI goroutine，阻塞等待用户选择。
type tuiUserResponder struct {
	program *tea.Program
	cwd     string
}

func (r *tuiUserResponder) AskUser(ctx context.Context, toolName string, input json.RawMessage, result permission.DecisionResult) permission.UserChoice {
	replyCh := make(chan permission.UserChoice, 1)

	// 格式化参数摘要；shell 命令做 cd 归一化后再展示，避免权限面板显示冗长的 cd 前缀
	argsSummary := formatToolArgs(toolName, string(input), r.cwd)
	if toolName == "bash" && argsSummary != "" {
		if normalized, _ := pathutil.NormalizeShellCommand(argsSummary); normalized != "" {
			argsSummary = normalized
		}
	}
	if argsSummary == "" {
		argsSummary = string(input)
	}

	// 默认 reason 不展示（如 "tool 'shell' requires confirmation"），
	// 仅安全检查或规则命中时展示具体原因。
	reasonMsg := ""
	if result.Reason != permission.ReasonDefault {
		reasonMsg = result.Message
	}

	// 发送权限请求到 TUI
	if r.program != nil {
		r.program.Send(permissionReqMsg{
			toolName:   toolName,
			args:       argsSummary,
			reason:     reasonMsg,
			reasonKind: result.Reason,
			reply:      replyCh,
		})
	}

	// 阻塞等待用户选择
	select {
	case choice := <-replyCh:
		return choice
	case <-ctx.Done():
		return permission.UserChoice{Decision: permission.DecisionDeny}
	}
}

// AnswerQuestion 实现 permission.UserResponder。
// 发送 questionReqMsg 到 TUI，阻塞等待用户回答。
func (r *tuiUserResponder) AnswerQuestion(ctx context.Context, questions []permission.QuestionPrompt) ([]permission.QuestionResponse, error) {
	replyCh := make(chan []permission.QuestionResponse, 1)

	if r.program != nil {
		r.program.Send(questionReqMsg{
			questions: questions,
			reply:     replyCh,
		})
	}

	select {
	case responses := <-replyCh:
		if responses == nil {
			return nil, fmt.Errorf("user declined to answer")
		}
		return responses, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// EnterPlan 实现 permission.UserResponder。
// 发送 planEnterReqMsg 到 TUI，阻塞等待用户确认。
func (r *tuiUserResponder) EnterPlan(ctx context.Context) (bool, error) {
	replyCh := make(chan bool, 1)

	if r.program != nil {
		r.program.Send(planEnterReqMsg{reply: replyCh})
	}

	select {
	case confirmed := <-replyCh:
		return confirmed, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// ApprovePlan 实现 permission.UserResponder。
// 发送 planExitReqMsg 到 TUI，阻塞等待用户审批。
func (r *tuiUserResponder) ApprovePlan(ctx context.Context, plan string) (permission.PlanApproval, error) {
	replyCh := make(chan permission.PlanApproval, 1)

	if r.program != nil {
		r.program.Send(planExitReqMsg{
			plan: plan,
			reply: replyCh,
		})
	}

	select {
	case approval := <-replyCh:
		return approval, nil
	case <-ctx.Done():
		return permission.PlanApproval{}, ctx.Err()
	}
}

// ---------------------------------------------------------------------------
// TUI 入口（由 cmd/waveloom/main.go 在无 prompt 时调用）
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// 主题管理
// ---------------------------------------------------------------------------

// initTheme 根据 themeMode 和终端背景色初始化主题。
// 在 runTUI 中 program 就绪后调用，确保可以检测终端背景色。
func (m *model) initTheme() {
	var p palette
	switch m.themeMode {
	case "dark":
		p = darkPalette
	case "light":
		p = lightPalette
	case "auto":
		m.autoDark = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
		if m.autoDark {
			p = darkPalette
		} else {
			p = lightPalette
		}
	default:
		p = darkPalette
	}
	applyTheme(p)
	m.palette = p
	m.syncThemeComponents()
}

// toggleTheme 循环切换主题: dark → light → auto → dark ...
func (m *model) toggleTheme() {
	switch m.themeMode {
	case "dark":
		m.themeMode = "light"
		applyTheme(lightPalette)
		m.palette = lightPalette
	case "light":
		m.themeMode = "auto"
		if m.autoDark {
			applyTheme(darkPalette)
			m.palette = darkPalette
		} else {
			applyTheme(lightPalette)
			m.palette = lightPalette
		}
	case "auto":
		m.themeMode = "dark"
		applyTheme(darkPalette)
		m.palette = darkPalette
	}
	m.syncThemeComponents()
}

// syncThemeComponents 同步 spinner、glamour 等组件的样式。
func (m *model) syncThemeComponents() {
	m.spinner.Style = lipgloss.NewStyle().Foreground(colorOK)
	m.spAsst.Style = lipgloss.NewStyle().Foreground(colorOK)
	m.spThought.Style = lipgloss.NewStyle().Foreground(colorGray)
	m.spTool.Style = lipgloss.NewStyle().Foreground(colorGray)
	m.spSubagent.Style = lipgloss.NewStyle().Foreground(colorGray)

	// 同步 ctx 进度条轨道颜色
	m.ctxProgress.EmptyColor = colorFooterFg

	// 同步 help 组件主题
	isDark := m.themeMode == "dark" || (m.themeMode == "auto" && m.autoDark)
	m.help.Styles = help.DefaultStyles(isDark)

	// 同步 input 组件样式 —— 提示符与用户消息前缀联动，placeholder 使用 muted 色
	inputStyles := m.input.Styles()
	inputStyles.Focused.Prompt = lipgloss.NewStyle().Foreground(colorUser).Bold(true)
	inputStyles.Blurred.Prompt = lipgloss.NewStyle().Foreground(colorUser).Bold(true)
	inputStyles.Focused.Placeholder = lipgloss.NewStyle().Foreground(colorMuted)
	inputStyles.Blurred.Placeholder = lipgloss.NewStyle().Foreground(colorMuted)
	inputStyles.Cursor.Blink = true
	m.input.SetStyles(inputStyles)

	// 同步 otherInput 样式（与主输入框一致）
	otherStyles := m.otherInput.Styles()
	otherStyles.Focused.Prompt = lipgloss.NewStyle().Foreground(colorUser).Bold(true)
	otherStyles.Blurred.Prompt = lipgloss.NewStyle().Foreground(colorUser).Bold(true)
	otherStyles.Focused.Placeholder = lipgloss.NewStyle().Foreground(colorMuted)
	otherStyles.Blurred.Placeholder = lipgloss.NewStyle().Foreground(colorMuted)
	otherStyles.Cursor.Blink = true
	m.otherInput.SetStyles(otherStyles)

	// 同步 permList delegate 样式（若已构建）
	if m.permDelegate != nil {
		m.permDelegate.Styles = listItemStyles()
		m.permList.SetDelegate(m.permDelegate)
	}

	// 同步 themeList delegate 样式
	if m.themeDelegate != nil {
		m.themeDelegate.Styles = listItemStyles()
		m.themeList.SetDelegate(m.themeDelegate)
	}

	// 同步 modelPickerList delegate 样式
	if m.modelPickerDelegate != nil {
		m.modelPickerDelegate.Styles = listItemStyles()
		m.modelPickerList.SetDelegate(m.modelPickerDelegate)
	}

	// 同步 pickerList delegate 样式（若已构建）
	if m.pickerDelegate != nil {
		m.pickerDelegate.Styles = listItemStyles()
		m.pickerList.SetDelegate(m.pickerDelegate)
	}

	// 同步 commandPickerList delegate 样式（若已构建）
	if m.commandPickerDelegate != nil {
		m.commandPickerDelegate.Styles = listItemStyles()
		m.commandPickerList.SetDelegate(m.commandPickerDelegate)
	}

	// 重建 glamour markdown 渲染器以匹配主题
	contentWidth := max(m.width-6, 20)
	glamourR, err := glamour.NewTermRenderer(
		glamour.WithWordWrap(contentWidth),
		glamour.WithStyles(waveloomGlamourStyle(m.palette)),
	)
	if err == nil {
		m.glamourRenderer = glamourR
	}

	// 主题切换后所有段落缓存失效（Glamour 输出颜色变化，样式前缀颜色变化）
	for i := range m.paras {
		m.paras[i].renderDirty = true
		m.paras[i].renderedCache = ""
	}
}

// ---------------------------------------------------------------------------
// Slash Command 集成
// ---------------------------------------------------------------------------

// handleSlashCommand 处理以 / 开头的本地命令。
// 命令名大小写不敏感，无匹配时显示帮助提示。
func (m *model) handleSlashCommand(input string) tea.Cmd {
	cmd, args := m.slashRegistry.Match(input)
	if cmd == nil {
		m.paras = append(m.paras, Paragraph{
			Type:      paraSystem,
			State:     stateDone,
			Text:      fmt.Sprintf(m.msg().SysUnknownCommand, input),
			NotifKind: notifWarn,
		})
		m.trimParas()
		m.flushTranscript()
		m.input.Reset()
		return nil
	}

	result, err := cmd.Execute(context.Background(), args)
	if err != nil {
		m.paras = append(m.paras, Paragraph{
			Type:      paraSystem,
			State:     stateDone,
			Text:      fmt.Sprintf(m.msg().SysCommandFailed, err),
			NotifKind: notifError,
		})
		m.trimParas()
		m.flushTranscript()
		m.input.Reset()
		return nil
	}

	// 追加文本输出
	if result.Text != "" {
		notifKind := notifInfo
		// 含错误关键词时用 warn 样式
		if strings.Contains(result.Text, "失败") || strings.Contains(result.Text, "未知") ||
			strings.Contains(result.Text, "无法") || strings.Contains(result.Text, "error") ||
			strings.Contains(result.Text, "failed") || strings.Contains(result.Text, "unknown") ||
			strings.Contains(result.Text, "unable") {
			notifKind = notifWarn
		}
		m.paras = append(m.paras, Paragraph{
			Type:      paraSystem,
			State:     stateDone,
			Text:      result.Text,
			NotifKind: notifKind,
		})
		m.trimParas()
	}

	// 处理副作用
	for _, se := range result.SideEffects {
		switch se.Kind {
		case slashcommand.SideEffectSessionReset:
			m.paras = nil
			m.transcriptWritten = 0
			m.hudTurns = 0
			m.hudMessages = 0
			m.hudCacheHit = 0
			m.hudCacheMiss = 0
			m.hudLatMs = 0
			m.lastPromptTokens = 0
			m.focusIndex = -1
			// 重新注册技能命令，确保技能列表刷新
			if m.skillLoader != nil {
				homeDir, _ := os.UserHomeDir()
				skillLoader := skill.NewLoader(m.cwd, homeDir, "", "medium", m.guard)
				m.skillLoader = skillLoader
				creator := &tuiSessionCreator{m: m}
				lister := &tuiModelLister{client: m.llmClient}
				m.slashRegistry = newSlashRegistry(creator, m.settingsStore, lister, m.hudModel, skillLoader, m.registry, m.slashMessages)
			}
			m.paras = append(m.paras, Paragraph{
				Type:      paraSystem,
				State:     stateDone,
				Text:      m.msg().SysNewSessionCreated,
				NotifKind: notifInfo,
			})

		case slashcommand.SideEffectOpenThemePicker:
			m.buildThemeList()
			m.overlay = overlayThemePicker
			m.input.Blur()

		case slashcommand.SideEffectOpenLocalePicker:
			m.buildLocaleList()
			m.overlay = overlayLocalePicker
			m.input.Blur()

		case slashcommand.SideEffectOpenModelPicker:
			var models []llm.ModelInfo
			if err := json.Unmarshal([]byte(se.Detail), &models); err == nil {
				m.modelPickerItems = models
				m.buildModelPickerList()
				m.overlay = overlayModelPicker
				m.input.Blur()
			}

		case slashcommand.SideEffectModelSwitched:
			m.hudModel = normalizeWidth(se.Detail)
			m.reconfigureLLMClient(se.Detail)

		case slashcommand.SideEffectInvokeSkill:
			skillBody := se.Detail    // 由 SkillCommand.Execute 通过 SkillExecutor 获取
			skillName := se.Detail2
			skillArgs := se.Detail3
			skillError := se.Detail4  // 加载失败时的错误消息
			if skillBody == "" {
				// 加载失败：渲染为 paraTool 错误态（与 LLM 调用 skill 工具失败一致）
				if skillError != "" {
					args := skillName
					if skillArgs != "" {
						args += " " + skillArgs
					}
					m.paras = append(m.paras, Paragraph{
						Type:         paraTool,
						State:        stateError,
						ToolName:     "skill",
						ToolArgs:     args,
						ToolError:    fmt.Sprintf(m.msg().SysSkillLoadFailed, skillName, skillError),
						ToolErrorKind: "no_results",
					})
					m.trimParas()
					m.flushTranscript()
				}
				break
			}
			args := skillName
			if skillArgs != "" {
				args += " " + skillArgs
			}
			// paraTool 段落
			m.paras = append(m.paras, Paragraph{
				Type:       paraTool,
				State:      stateDone,
				ToolName:   "skill",
				ToolArgs:   args,
				ToolResult: truncateToolResult(skillBody),
			})
			m.trimParas()
			m.flushTranscript()

			// 启动 loop（复用 doTurn 逻辑，但不添加 paraUser + 不展开 @）
			// PrepareRun 将 skill body 作为 user 消息注入并返回消息快照
			m.closePicker()
			m.hudTurns++
			messagesSnapshot := m.cm.PrepareRun(skillBody)
			m.running = true
			m.focusIndex = -1
			m.input.Placeholder = m.msg().InputAgentRunning
			m.runGeneration++
			m.turnStartTime = time.Now()
			m.scrollToBottom()

			if m.cancelRun != nil {
				m.cancelRun()
				m.cancelRun = nil
				m.loopPrompt = 0
				m.loopCompl = 0
				m.loopCacheHit = 0
				m.loopCacheMiss = 0
				m.loopReasoning = 0
			}

			m.input.Reset()

			ctx, cancel := context.WithCancel(context.Background())
			m.cancelRun = cancel
			gen := m.runGeneration
			return func() tea.Msg {
				defer cancel()
				for ev := range m.loop.Run(ctx, messagesSnapshot) {
					if done, ok := ev.(agentloop.LoopDone); ok {
						ev = agentloop.LoopDoneWithGen{LoopDone: done, Generation: gen}
					}
					if m.program != nil {
						m.program.Send(ev)
					}
				}
				return nil
			}
		}
	}

	m.flushTranscript()
	m.input.Reset()
	return nil
}

// handleModelSwap 处理模型切换滞后。
// 命令层写入 settings 后通过 SideEffect 通知 TUI 更新 HUD + 重建 Client。
func (m *model) reconfigureLLMClient(newModel string) {
	settings, err := m.settingsStore.LoadLLM()
	if err != nil {
		return
	}
	settings.Model = newModel
	client, _, err := llm.NewClientFromLLMSettings(settings)
	if err != nil {
		return
	}
	m.llmClient = client
	if m.loop != nil {
		m.wireLoop()
	}
}

// ---------------------------------------------------------------------------
// SettingsStore & ModelLister 实现（TUI 侧，供 slash command 注入）
// ---------------------------------------------------------------------------

// tuiSettingsStore 实现 slashcommand.SettingsStore。
// SaveLLM 全量 read-modify-write，保留 settings.json 其他 section（lsp, compaction 等）。
type tuiSettingsStore struct {
	projectPath string
	globalPath  string
}

func (s *tuiSettingsStore) LoadLLM() (*llm.LLMSettings, error) {
	global, _ := llm.LoadSettingsIfExists(s.globalPath)
	project, _ := llm.LoadSettingsIfExists(s.projectPath)
	return llm.MergeLLMSettings(global, project), nil
}

func (s *tuiSettingsStore) SaveLLM(settings *llm.LLMSettings) error {
	return writeFullSettings(s.projectPath, settings, "", "")
}

func (s *tuiSettingsStore) SaveTheme(mode string) error {
	return writeFullSettings(s.projectPath, nil, mode, "")
}

func (s *tuiSettingsStore) SaveLocale(locale string) error {
	return writeFullSettings(s.projectPath, nil, "", locale)
}

func (s *tuiSettingsStore) plansDirectory() string {
	// 优先项目 settings，其次全局 settings
	for _, p := range []string{s.projectPath, s.globalPath} {
		if p == "" {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var cfg struct {
			PlansDirectory string `json:"plans_directory"`
		}
		if json.Unmarshal(data, &cfg) == nil && cfg.PlansDirectory != "" {
			if filepath.IsAbs(cfg.PlansDirectory) {
				return cfg.PlansDirectory
			}
			// 相对路径：相对于配置文件所在目录解析
			abs, err := filepath.Abs(filepath.Join(filepath.Dir(p), cfg.PlansDirectory))
			if err == nil {
				return abs
			}
		}
	}
	return ""
}

// writeFullSettings 全量 read-modify-write settings.json，替换 llm / theme / locale section。
// llmSettings 为 nil 时保留已有的 llm section；theme / locale 为空时保留已有值。
func writeFullSettings(path string, llmSettings *llm.LLMSettings, theme, locale string) error {
	// 读取现有完整文件
	full := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &full)
	}

	if llmSettings != nil {
		// 将 LLMSettings 转为 map
		llmMap := make(map[string]any)
		b, err := json.Marshal(llmSettings)
		if err != nil {
			return err
		}
		_ = json.Unmarshal(b, &llmMap)
		full["llm"] = llmMap
	}

	if theme != "" {
		full["theme"] = theme
	}

	if locale != "" {
		full["locale"] = locale
	}

	// 写回
	out, err := json.MarshalIndent(full, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// tuiModelLister 实现 slashcommand.ModelLister，委托给 llm.Client。
type tuiModelLister struct {
	client llm.Client
}

func (l *tuiModelLister) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	return l.client.ListModels(ctx)
}

// tuiSessionCreator 实现 slashcommand.SessionCreator，委托给 TUI model。
type tuiSessionCreator struct {
	m *model
}

func (c *tuiSessionCreator) NewSession() error {
	m := c.m
	// 生成新 session ID + 路径
	sid := ctxpkg.NewSessionID()
	sessionPath := filepath.Join(m.sessionDir, sid+".json")
	m.cm.SetSessionPath(sessionPath)

	// 重置 ContextManager
	m.cm.Reset()

	// 重新注入 AGENTS.md
	if m.agentsMdText != "" {
		m.cm.InjectUserInstructions(m.agentsMdText)
	}

	// 更新 transcript 路径
	m.transcriptPath = ctxpkg.TranscriptPath(m.sessionDir, sid)
	m.transcriptWritten = 0

	return nil
}

// tuiSkillExecutor 实现 slashcommand.SkillExecutor，将 skill 执行委托给 tool.Registry。
// 这样用户 /skill-name 和 LLM 调用 skill 工具走相同的代码路径。
type tuiSkillExecutor struct {
	registry tool.Registry
}

func (e *tuiSkillExecutor) ExecuteSkill(ctx context.Context, name, args string) (string, error) {
	paramsJSON, err := json.Marshal(tool.SkillParams{Name: name, Arguments: args})
	if err != nil {
		return "", err
	}
	result, err := e.registry.Execute(ctx, "skill", paramsJSON)
	if err != nil {
		return "", err
	}
	if result.Error != nil {
		return "", fmt.Errorf("%s", result.Error.Message)
	}
	return result.Content, nil
}

// newSlashRegistry 创建 slash command 注册表，注入 TUI 侧依赖实现。
// slashMessagesFrom 将 TUI Messages 转换为 slashcommand.SlashMessages。
func slashMessagesFrom(lc *Messages) *slashcommand.SlashMessages {
	return &slashcommand.SlashMessages{
		NewDescription:        lc.SlashNewDescription,
		NewCreated:            lc.SlashNewCreated,
		NewFailed:             lc.SlashNewFailed,
		ModelDescription:      lc.SlashModelDescription,
		ModelListFailed:       lc.SlashModelListFailed,
		ModelListFailedNoNet:  lc.SlashModelListFailedNoNet,
		ModelUnknown:          lc.SlashModelUnknown,
		ModelConfigReadFailed: lc.SlashModelConfigReadFailed,
		ModelConfigSaveFailed: lc.SlashModelConfigSaveFailed,
		ModelSwitched:         lc.SlashModelSwitched,
		ThemeDescription:      lc.SlashThemeDescription,
		LocaleDescription:     lc.SlashLocaleDescription,
		HelpDescription:       lc.SlashHelpDescription,
		HelpText:              lc.SlashHelpText,
	}
}

func newSlashRegistry(creator slashcommand.SessionCreator, store slashcommand.SettingsStore, lister slashcommand.ModelLister, currentModel string, skillLoader *skill.Loader, registry tool.Registry, sm *slashcommand.SlashMessages) *slashcommand.Registry {
	r := slashcommand.NewRegistry()
	r.Register(slashcommand.NewNewCommand(creator, sm))
	r.Register(slashcommand.NewModelCommand(store, lister, currentModel, sm))
	r.Register(slashcommand.NewThemeCommand(sm))
	r.Register(slashcommand.NewLocaleCommand(sm))
	r.Register(slashcommand.NewHelpCommand(r, sm))

	// 注册 user-invocable skills
	// skill body 的加载统一走 skill 工具（通过 SkillExecutor 接口）
	if skillLoader != nil {
		skills, _ := skillLoader.List()
		executor := &tuiSkillExecutor{registry: registry}
		for _, info := range skills {
			if info.UserInvocable {
				r.Register(slashcommand.NewSkillCommand(slashcommand.SkillDescriptor{
					Name:        info.Name,
					Description: info.Description,
					Args:        info.Args,
				}, executor))
			}
		}
	}

	return r
}

// ---------------------------------------------------------------------------
// 覆盖层 — 主题选择器
// ---------------------------------------------------------------------------

// themeItems 返回主题选择器的固定选项。label 为占位值，运行时由 buildThemeList 根据 locale 替换。
var themeItems = []themeItem{
	{label: "Auto", mode: "auto"},
	{label: "Dark", mode: "dark"},
	{label: "Light", mode: "light"},
}

// buildThemeList 构建主题选择列表覆盖层。
func (m *model) buildThemeList() {
	items := make([]list.Item, len(themeItems))
	selectedIdx := 0
	for i, ti := range themeItems {
		label := ti.label
		if ti.mode == "auto" {
			label = m.msg().PickerThemeAuto
		}
		items[i] = themeItem{label: label, mode: ti.mode}
		if ti.mode == m.themeMode {
			selectedIdx = i
		}
	}

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false
	delegate.SetSpacing(0)
	delegate.Styles = listItemStyles()
	m.themeDelegate = &delegate

	l := list.New(items, delegate, 0, 3)
	l.SetShowTitle(false)
	l.SetShowPagination(false)
	l.SetShowStatusBar(false)
	l.SetShowFilter(false)
	l.SetShowHelp(false)
	l.KeyMap.Quit = key.NewBinding()
	l.KeyMap.ForceQuit = key.NewBinding()
	if selectedIdx < 3 {
		l.Select(selectedIdx)
	}
	m.themeList = l
}

// handleThemePickerKey 处理主题选择器中的按键。
func (m *model) handleThemePickerKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	keyStr := msg.String()
	switch keyStr {
	case "up", "down":
		var cmd tea.Cmd
		m.themeList, cmd = m.themeList.Update(msg)
		return true, cmd
	case "enter":
		idx := m.themeList.Index()
		if idx >= 0 && idx < len(themeItems) {
			m.applyThemeMode(themeItems[idx].mode)
		}
		m.closeThemePicker()
		return true, nil
	case "esc":
		m.closeThemePicker()
		return true, nil
	}
	return false, nil
}

// applyThemeMode 应用指定主题模式并保存到 settings.json。
func (m *model) applyThemeMode(mode string) {
	m.themeMode = mode
	var p palette
	switch mode {
	case "dark":
		p = darkPalette
	case "light":
		p = lightPalette
	case "auto":
		if m.autoDark {
			p = darkPalette
		} else {
			p = lightPalette
		}
	}
	applyTheme(p)
	m.palette = p
	m.syncThemeComponents()
	// 落盘到 project settings.json
	if m.settingsStore != nil {
		_ = m.settingsStore.SaveTheme(mode)
	}
}

func (m *model) closeThemePicker() {
	m.overlay = overlayNone
	m.input.Focus()
}

// ---------------------------------------------------------------------------
// 覆盖层 — 语言选择器
// ---------------------------------------------------------------------------

// localeItem 表示语言选择器的选项。
type localeItem struct {
	label  string
	locale Locale
}

func (i localeItem) Title() string       { return i.label }
func (i localeItem) Description() string { return "" }
func (i localeItem) FilterValue() string { return i.label }

// localeItems 返回语言选择器的固定选项。
var localeItems = []localeItem{
	{label: "简体中文", locale: LocaleZhCN},
	{label: "English", locale: LocaleEnUS},
}

// buildLocaleList 构建语言选择列表覆盖层。
func (m *model) buildLocaleList() {
	items := make([]list.Item, len(localeItems))
	selectedIdx := 0
	currentLocale := LocaleEnUS
	if m.lc != nil {
		switch m.lc {
		case &zhCN:
			currentLocale = LocaleZhCN
		}
	}
	for i, li := range localeItems {
		items[i] = li
		if li.locale == currentLocale {
			selectedIdx = i
		}
	}

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false
	delegate.SetSpacing(0)
	delegate.Styles = listItemStyles()
	m.localeDelegate = &delegate

	l := list.New(items, delegate, 0, 2)
	l.SetShowTitle(false)
	l.SetShowPagination(false)
	l.SetShowStatusBar(false)
	l.SetShowFilter(false)
	l.SetShowHelp(false)
	l.KeyMap.Quit = key.NewBinding()
	l.KeyMap.ForceQuit = key.NewBinding()
	if selectedIdx < 2 {
		l.Select(selectedIdx)
	}
	m.localeList = l
}

// handleLocalePickerKey 处理语言选择器中的按键。
func (m *model) handleLocalePickerKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	keyStr := msg.String()
	switch keyStr {
	case "up", "down":
		var cmd tea.Cmd
		m.localeList, cmd = m.localeList.Update(msg)
		return true, cmd
	case "enter":
		idx := m.localeList.Index()
		if idx >= 0 && idx < len(localeItems) {
			m.applyLocale(localeItems[idx].locale)
		}
		m.closeLocalePicker()
		return true, nil
	case "esc":
		m.closeLocalePicker()
		return true, nil
	}
	return false, nil
}

// applyLocale 应用指定语言并保存到 settings.json。
func (m *model) applyLocale(loc Locale) {
	m.lc = messagesFor(loc)
	// 即时更新 input placeholder
	m.input.Placeholder = m.msg().InputPlaceholder
	m.otherInput.Placeholder = m.msg().InputOtherPlaceholder
	// 刷新 slash command 文案（共享 SlashMessages 指针原地更新）
	if m.slashMessages != nil {
		*m.slashMessages = *slashMessagesFrom(m.lc)
	}
	// 刷新 command picker 缓存（下次打开 / 时用新文案重建列表）
	m.commandPickerItems = nil
	if m.settingsStore != nil {
		_ = m.settingsStore.SaveLocale(string(loc))
	}
}

func (m *model) closeLocalePicker() {
	m.overlay = overlayNone
	m.input.Focus()
}

// ---------------------------------------------------------------------------
// 覆盖层 — 模型选择器
// ---------------------------------------------------------------------------

// buildModelPickerList 从 modelPickerItems 构建模型选择列表。
// 当前使用的模型（m.hudModel）在列表中高亮。
func (m *model) buildModelPickerList() {
	items := make([]list.Item, len(m.modelPickerItems))
	selectedIdx := 0
	for i, mi := range m.modelPickerItems {
		items[i] = modelPickerItem{modelID: mi.ID, ownedBy: mi.OwnedBy}
		if mi.ID == m.hudModel {
			selectedIdx = i
		}
	}

	height := len(items)
	if height > 5 {
		height = 5
	}
	if height < 1 {
		height = 1
	}

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false
	delegate.SetSpacing(0)
	delegate.Styles = listItemStyles()
	m.modelPickerDelegate = &delegate

	l := list.New(items, delegate, 0, height)
	l.SetShowTitle(false)
	l.SetShowPagination(false)
	l.SetShowStatusBar(false)
	l.SetShowFilter(false)
	l.SetShowHelp(false)
	l.KeyMap.Quit = key.NewBinding()
	l.KeyMap.ForceQuit = key.NewBinding()
	if selectedIdx < height {
		l.Select(selectedIdx)
	}
	m.modelPickerList = l
}

// handleModelPickerKey 处理模型选择器中的按键。
func (m *model) handleModelPickerKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	keyStr := msg.String()
	switch keyStr {
	case "up", "down":
		var cmd tea.Cmd
		m.modelPickerList, cmd = m.modelPickerList.Update(msg)
		return true, cmd
	case "enter":
		idx := m.modelPickerList.Index()
		if idx >= 0 && idx < len(m.modelPickerItems) {
			m.commitModelSwitch(m.modelPickerItems[idx].ID)
		}
		m.closeModelPicker()
		return true, nil
	case "esc":
		m.closeModelPicker()
		return true, nil
	}
	return false, nil
}

// commitModelSwitch 确认模型切换：写 settings + 热替换。
func (m *model) commitModelSwitch(modelID string) {
	settings, err := m.settingsStore.LoadLLM()
	if err != nil {
		settings = &llm.LLMSettings{}
	}
	settings.Model = modelID
	_ = m.settingsStore.SaveLLM(settings) // 忽略写入错误，用户感知到 HUD 已更新
	m.hudModel = normalizeWidth(modelID)
	m.reconfigureLLMClient(modelID)
}

func (m *model) closeModelPicker() {
	m.overlay = overlayNone
	m.input.Focus()
}

// ---------------------------------------------------------------------------
// runTUI
// ---------------------------------------------------------------------------

// runTUI 启动交互式 TUI 模式。依赖由 main() 统一初始化后传入，无需重复创建。
func runTUI(llmClient llm.Client, registry tool.Registry, guard permission.Guard, expander *reference.Expander, modelName string, theme string, verboseLog io.Writer, contextLimit int, maxTurns int, toolTimeout time.Duration, toolTimeoutSource string, bypassPerm bool, ctxMgr *ctxpkg.ContextManager, isResume bool, sessionDir string, globalPath string, projectPath string, agentsMdText string, loc Locale) {
	m := newTUIModel(llmClient, registry, guard, expander, modelName, theme, verboseLog, contextLimit, maxTurns, toolTimeout, toolTimeoutSource, loc)
	m.agentsMdText = agentsMdText
	m.sessionDir = sessionDir

	// 构造 slash command registry（TUI 侧依赖实现）
	store := &tuiSettingsStore{projectPath: projectPath, globalPath: globalPath}
	m.settingsStore = store
	lister := &tuiModelLister{client: llmClient}
	sessionCreator := &tuiSessionCreator{m: m}

	// 构造 skill loader（用于注册 skill 命令）
	homeDir, _ := os.UserHomeDir()
	skillLoader := skill.NewLoader(m.cwd, homeDir, ctxMgr.SessionID(), "medium", guard)
	m.slashMessages = slashMessagesFrom(m.lc)
	m.slashRegistry = newSlashRegistry(sessionCreator, store, lister, modelName, skillLoader, registry, m.slashMessages)
	m.skillLoader = skillLoader

	m.bypassPerm = bypassPerm
	// 用外部创建的 ContextManager 替换 newTUIModel 内部创建的
	m.cm = ctxMgr
	// 恢复会话级 HUD 累积值
	m.hudCacheHit = ctxMgr.Stats().TotalCacheHitTokens
	m.hudCacheMiss = ctxMgr.Stats().TotalCacheMissTokens
	// REGRESSION: --resume 后 hudTurns 未从 Stats.TotalTurns 恢复，状态栏计数从 0 开始。
	// 无法单测：runTUI 依赖 Bubble Tea Program 实例。
	m.hudTurns = ctxMgr.Stats().TotalTurns
	// ctx bar 初始为 0，首个 TurnStats 会用 API 精确值更新
	m.lastPromptTokens = 0

	// Transcript 路径：从 sessionPath 推导
	if sp := ctxMgr.SessionPath(); sp != "" {
		sid := ctxMgr.SessionID()
		m.transcriptPath = ctxpkg.TranscriptPath(sessionDir, sid)
	}

	// Resume：重放 transcript 到 viewport
	if isResume && m.transcriptPath != "" {
		m.replayTranscript()
	}

	// 写入 recent.json（TUI 启动时唯一写入点）
	if sid := ctxMgr.SessionID(); sid != "" {
		stats := ctxMgr.Stats()
		_ = ctxpkg.UpdateRecentSessions(sessionDir, sid, stats.MessageCount) // 静默失败
	}

	p := tea.NewProgram(m)
	m.program = p
	m.wireLoop()  // 注入 tuiUserResponder，此时 program 已就绪
	m.initTheme() // 根据 themeMode + 终端背景自动检测并应用主题

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI runtime error: %v\n", err)
		os.Exit(1)
	}

	// 正常退出时保存 session 并提示 session ID
	m.cm.Save()
	if sid := m.cm.SessionID(); sid != "" {
		// 退出时用最终统计更新 recent.json（覆盖启动时写入的初始值）
		stats := m.cm.Stats()
		_ = ctxpkg.UpdateRecentSessions(sessionDir, sid, stats.MessageCount)
		fmt.Fprintf(os.Stderr, m.lc.SessionSaved, sid)
		fmt.Fprintf(os.Stderr, m.lc.SessionResumeHint, sid)
	}
}

// ---------------------------------------------------------------------------
// 自更新
// ---------------------------------------------------------------------------

// startUpdate 返回一个 tea.Cmd，在 goroutine 中下载并安装新版本。
// 进度通过 program.Send 推送到 TUI Update 循环。
func (m *model) startUpdate() tea.Cmd {
	m.updating = true
	m.noticeBanner = "" // 清除 banner，避免重复触发

	// 追加 tool 段落
	m.paras = append(m.paras, Paragraph{
		Type:     paraTool,
		State:    stateStreaming,
		ToolName: "▲",
		ToolArgs: "waveloom self-update → " + m.latestVersion,
	})

	return func() tea.Msg {
		startTime := time.Now()

		execPath, err := os.Executable()
		if err != nil {
			m.program.Send(updateDoneMsg{err: fmt.Sprintf("failed to get current binary path: %v", err), durMs: time.Since(startTime).Milliseconds()})
			return nil
		}

		downloadURL := environment.BuildDownloadURL()

		err = environment.SelfUpdate(context.Background(), execPath, downloadURL,
			func(phase environment.SelfUpdatePhase, pct int, detail string) {
				m.program.Send(updateProgressMsg{
					phase:  string(phase),
					pct:    pct,
					detail: detail,
				})
			})

		durMs := time.Since(startTime).Milliseconds()

		if err != nil {
			m.program.Send(updateDoneMsg{err: err.Error(), durMs: durMs})
			return nil
		}

		m.program.Send(updateDoneMsg{durMs: durMs})
		return nil
	}
}

// handleUpdateProgress 更新 tool 段落的内容。
func (m *model) handleUpdateProgress(msg updateProgressMsg) {
	if msg.err != "" {
		// 进度中的错误转为 done 消息
		m.program.Send(updateDoneMsg{err: msg.err})
		return
	}
	// 找到最后一个 stateStreaming 的 paraTool 段落
	for i := len(m.paras) - 1; i >= 0; i-- {
		p := &m.paras[i]
		if p.Type == paraTool && p.State == stateStreaming {
			p.ToolResult += msg.detail + "\n"
			p.renderDirty = true
			return
		}
	}
}

// handleUpdateDone 完成或失败更新流程。
func (m *model) handleUpdateDone(msg updateDoneMsg) {
	m.updating = false

	// 找到最后一个 streaming tool 段落
	for i := len(m.paras) - 1; i >= 0; i-- {
		p := &m.paras[i]
		if p.Type == paraTool && p.State == stateStreaming {
			p.ToolDurMs = msg.durMs
			if msg.err != "" {
				p.ToolResult += "✗ " + msg.err + "\n"
				p.ToolError = msg.err
				p.State = stateError
			} else {
				p.State = stateDone
			}
			p.renderDirty = true
			break
		}
	}

	if msg.err != "" {
		m.paras = append(m.paras, Paragraph{
			Type:      paraSystem,
			State:     stateDone,
			Text:      m.msg().SysUpdateFailed,
			NotifKind: notifError,
		})
	} else {
		m.paras = append(m.paras, Paragraph{
			Type:      paraSystem,
			State:     stateDone,
			Text:      fmt.Sprintf(m.msg().SysUpdateInstalled, m.latestVersion),
			NotifKind: notifInfo,
		})
		m.trimParas()
	}
}

// ---------------------------------------------------------------------------
// Plan 模式辅助函数
// ---------------------------------------------------------------------------

// generatePlanFilePath 在 plans 目录下生成随机 word slug 文件路径。
// 优先使用 settings.json 中 plans_directory，否则使用 ~/.waveloom/plans/。
func (m *model) generatePlanFilePath() string {
	plansDir := m.plansDirectory()
	_ = os.MkdirAll(plansDir, 0o755)

	for i := 0; i < 10; i++ {
		slug := generateTUISlug()
		path := filepath.Join(plansDir, slug+".md")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path
		}
	}
	b := make([]byte, 4)
	rand.Read(b)
	return filepath.Join(plansDir, hex.EncodeToString(b)+".md")
}

// plansDirectory 返回 plan 文件存储目录。
func (m *model) plansDirectory() string {
	// 读取 settings.json 中的 plans_directory 配置
	if dir := loadPlansDirectory(m.settingsStore); dir != "" {
		return dir
	}
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".waveloom", "plans")
}

// loadPlansDirectory 从 settings 读取 plans_directory 配置。
func loadPlansDirectory(store *tuiSettingsStore) string {
	if store == nil {
		return ""
	}
	// settingsStore 提供 settings 数据
	return store.plansDirectory()
}

// generatePairIDForTUI 生成 4 位 hex 随机配对 ID。
func generatePairIDForTUI() string {
	b := make([]byte, 2)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// generateTUISlug 生成 "adjective-noun" 格式的随机 slug。
func generateTUISlug() string {
	adj := tuiAdjectives[tuiRandInt(len(tuiAdjectives))]
	noun := tuiNouns[tuiRandInt(len(tuiNouns))]
	return adj + "-" + noun
}

func tuiRandInt(max int) int {
	b := make([]byte, 4)
	rand.Read(b)
	return int(uint32(b[0])|uint32(b[1])<<8|uint32(b[2])<<16|uint32(b[3])<<24) % max
}

var tuiAdjectives = []string{
	"happy", "clever", "brave", "bright", "calm",
	"eager", "fancy", "fresh", "grand", "green",
	"jolly", "keen", "lucky", "merry", "noble",
	"proud", "quick", "sharp", "smart", "swift",
	"vivid", "warm", "wise", "bold", "cool",
}

var tuiNouns = []string{
	"badger", "crane", "dolphin", "eagle", "falcon",
	"gecko", "heron", "ibis", "jackal", "koala",
	"lemur", "marlin", "newt", "otter", "puffin",
	"quokka", "raven", "salmon", "tapir", "urchin",
	"viper", "weasel", "xerus", "yak", "zebra",
}
