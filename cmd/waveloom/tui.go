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
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	glamourstyles "charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"

	"github.com/Menfre01/waveloom/pkg/agentloop"
	ctxpkg "github.com/Menfre01/waveloom/pkg/context"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/pathutil"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/reference"
	"github.com/Menfre01/waveloom/pkg/slashcommand"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// 常量
// ---------------------------------------------------------------------------

var defaultSystemPrompt = `You are Waveloom, a terminal-based coding agent. You help users write, refactor, debug, and explore code. You are precise, safe, and efficient.

## Personality

- Be concise and direct. Remove filler, narration, and redundant summaries.
- Do NOT use emoji in outputs — icons like ⚠️ ❌ ✅ belong to the UI layer, not agent text.
- Communicate in Chinese unless analyzing code or terminal output that is in English.
- When you finish work, hand it off clearly — no "terrific" or "woohoo" sign-offs.

## Capabilities

- Read, write, and edit files. Run shell commands. Search code with grep and glob. List directories with ls.
- Query LSP diagnostics, definitions, references, and hover info for precise code understanding.
- Fetch online documentation, API references, and package registries via web_fetch.
- Execute in a sandboxed workspace. Commands that modify files or install packages may require approval.
- View structured tool outputs (git diffs, file listings, search results) and base further actions on them.

## How you work

- Explore the codebase before making changes — use search_file and grep, then read_file.
- After editing code, use lsp_diagnostic to check for compile errors and warnings.
- Use lsp_definition to understand third-party library types, function signatures, and definitions.
- Use lsp_references to trace dependencies and analyze impact before refactoring.
- Use lsp_hover to quickly view type signatures and API documentation.
- Use web_fetch to consult online docs, API references, and package registry information.
- Make surgical, minimal edits. Do not refactor unrelated code or add unnecessary comments.
- Prefer edit_file (with unified diff patches) over write_file for small changes.
- When using edit_file, copy old_string verbatim from read_file output — same indentation, same whitespace, same line breaks. Never reconstruct from memory.
- When using shell, prefer checking exit codes over parsing output.
- If rg (ripgrep) is listed in Available tools under ## Environment, prefer it over grep for faster searches; otherwise use grep.
- When using shell, use the working_dir parameter to set the working directory. Do NOT prepend "cd <path> &&" to the command — this breaks permission pattern matching.
- After making changes, verify them — compile, run tests, or check diffs where applicable.
- Before calling any binary via shell, check ## Environment: if it is listed under "Not found", do NOT attempt to call it — use a built-in tool or ask the user to install it.
- When you have multiple independent read-only operations (read_file, grep, search_file, lsp_*), batch them in a single response as parallel tool calls.

## Coding standards

- Follow existing codebase conventions. Do not introduce new patterns without justification.
- Use clear, self-documenting names. Avoid abbreviations and single-letter variables.
- Maintain consistent error handling — errors propagate cleanly, not with raw stack traces to the client.
- Keep functions small and focused. Extract helpers only when reuse is clear.

## Termination

- Stop and report completion when the user's request is fully satisfied.
- If you cannot complete a task, explain the bottleneck concisely and propose next steps.
- Do NOT loop on the same sub-task repeatedly. If stuck, ask for guidance.

## Tool Error Handling

- When a tool returns an error, analyze the error kind before retrying.
- Error kinds you may encounter:
  command_not_found — The binary is not installed. Report to user, do NOT retry.
  command_failed — The command ran but exited non-zero. Check stderr, fix args, retry once.
  timeout — Command exceeded time limit. Increase timeout_ms or simplify the command.
  file_not_found — Check the path with search_file or ls; retry with corrected path.
  no_match — The old_string was not found in the file. Re-read the file with read_file,
         then copy the exact text verbatim (including indentation and whitespace)
         for old_string. Never retry from memory.
  invalid_args — Fix the parameter syntax and retry.
  permission_denied — Cannot access. Use an alternative path or ask user.
  security_violation — Fatal. The operation is blocked by policy. Do not retry.
- command_not_found is special: it means the tool binary is absent, NOT that your command syntax was wrong. Never retry a command_not_found error with different flags or arguments — the binary itself is missing.
- Do not retry the same operation more than twice. If a tool fails twice with the same error kind, stop and ask the user for guidance.
- When you need a compiler, build tool, or runtime, check its availability once under ## Environment. If absent, ask the user to provide the path or install it.`

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
func buildSystemPrompt(cwd string) string {
	cwdInfo := fmt.Sprintf(`

## Workspace

Current working directory: %s
All file paths are resolved relative to this directory unless a working_dir is specified.

### Working Directory Rules

- The workspace directory is fixed for the entire session.
- Shell commands run in isolated subprocesses — "cd" inside a shell command has NO effect on subsequent commands.
- To operate in a different directory, use the working_dir parameter: {"command":"ls", "working_dir":"/tmp"}
- Never prefix commands with "cd <path> &&" — this breaks permission pattern matching and is unnecessary.`, cwd)
	return defaultSystemPrompt + cwdInfo
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

// permKeys 权限覆盖层的快捷键（用于 help 组件渲染）。
var permKeys = []key.Binding{
	key.NewBinding(key.WithKeys("↑/↓"), key.WithHelp("↑/↓", "导航")),
	key.NewBinding(key.WithKeys("enter"), key.WithHelp("Enter", "确认")),
	key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", "拒绝")),
}

var defaultKeys = keyMap{
	Enter:         key.NewBinding(key.WithKeys("enter"), key.WithHelp("Enter", "发送消息")),
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

// permissionReqMsg 权限确认请求。
type permissionReqMsg struct {
	toolName   string
	args       string
	reason     string
	reasonKind permission.DecisionReason
	reply      chan<- permission.UserChoice
}

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
	description string
	args        string // 参数占位符，如 "model"；无参数时为空
}

func (i commandPickerItem) Title() string {
	if i.args != "" {
		return "/" + i.name + " [" + i.args + "] " + i.description
	}
	return "/" + i.name + " " + i.description
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
	cwd           string
	verboseLog io.Writer // --verbose 日志输出（nil = 不记录）
	loop       *agentloop.Loop

	// 段落模型（消息内容的数据源）
	paras []Paragraph

	// Glamour markdown 渲染器
	glamourRenderer *glamour.TermRenderer

	// 覆盖层状态
	overlay      Overlay
	permReq      *permissionReqMsg     // 当前待确认的权限请求
	permList     list.Model            // 权限选项列表（bubbles/list）
	permDelegate *list.DefaultDelegate // 权限列表的 delegate 指针，主题切换时更新样式

	// 主题选择器覆盖层
	themeList     list.Model
	themeDelegate *list.DefaultDelegate

	// 模型选择器覆盖层
	modelPickerList     list.Model
	modelPickerDelegate *list.DefaultDelegate
	modelPickerItems    []llm.ModelInfo // 模型列表数据，主题切换时更新样式

	// 文件选择器
	pickerVisible         bool
	pickerFilter          string
	pickerItems           []pickerItem
	pickerAllItems        []pickerItem
	pickerScanGen         int                   // 扫描代数，每次发起异步扫描时递增，用于丢弃过期结果
	pickerLastScannedBase string                // 上次触发磁盘扫描的 filepath.Base(filter)，base 未变则跳过扫描
	pickerDismissValue    string                // 选择器关闭时的 input 值，防止立即重新触发
	pickerLastValue       string                // 上次刷新时的 input 值，避免 spinner tick 触发重复扫描
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
	ctxProgress    progress.Model // ctx 窗口进度条（bubbles progress 组件）
	input          textinput.Model

	// 输入历史
	inputHistory []string // 已提交的输入，最新在前
	historyPos   int      // 当前历史位置（-1 = 不在历史导航中）
	historyDraft string   // 进入历史导航前输入框中的草稿文本

	// 双击 Esc 清空输入
	lastEscTime time.Time // 上次在空闲态按 Esc 的时间

	// 状态
	running       bool               // agent loop 正在执行中
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
	base.CodeBlock.StylePrimitive.Color = &toolCode

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

// newTUIModel 创建 TUI model，依赖由外部注入（LLM client / tool registry / guard / expander / verboseLog）。
func newTUIModel(llmClient llm.Client, registry tool.Registry, guard permission.Guard, expander *reference.Expander, modelName string, theme string, verboseLog io.Writer, contextLimit int, maxTurns int, toolTimeout time.Duration, toolTimeoutSource string) *model {
	if modelName == "" {
		modelName = "deepseek-v4"
	}

	cwd, _ := os.Getwd()
	cm := ctxpkg.New(buildSystemPrompt(cwd))

	ti := textinput.New()
	ti.Prompt = "› "
	ti.Placeholder = "输入消息, Enter 发送 · / 命令 · @ 选择文件 · Esc 中断"
	ti.CharLimit = 2048
	ti.SetVirtualCursor(false) // real cursor 避免 virtual cursor 反色 ANSI 泄漏
	styles := ti.Styles()
	styles.Cursor.Blink = true
	ti.SetStyles(styles)

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
		keys:            defaultKeys,
		help:            help.New(),
		spinner:         sp,
		spAsst:          spAsst,
		spThought:       spThought,
		spTool:          spTool,
		ctxProgress:     cp,
		input:           ti,
		historyPos:      -1,
		overlay:         overlayNone,
		themeMode:       theme,
		palette:         darkPalette, // 默认，initTheme 覆盖
		focusIndex:      -1,
		pinnedToBottom:  true,
	}
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
	})
}

// ---------------------------------------------------------------------------
// bubbletea.Model
// ---------------------------------------------------------------------------

func (m *model) Init() tea.Cmd {
	return tea.Batch(
		m.input.Focus(),
		textinput.Blink,
		m.spinner.Tick,
		m.spAsst.Tick,
		m.spThought.Tick,
		m.spTool.Tick,
	)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	// ------------------------------------------------------------------
	// 窗口尺寸
	// ------------------------------------------------------------------
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		contentWidth := max(msg.Width-4, 20)

		m.input.SetWidth(contentWidth - 4)

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
				Text:      "压缩完成。",
				NotifKind: notifInfo,
			})
			m.trimParas()
		}
		if msg.Compaction.HardLimitReached {
			msgText := "上下文已满（98%）。/reset 重建。"
			msgKind := notifWarn
			if msg.Compaction.HardLimitReason == "tier3_failures" {
				msgText = "摘要连续失败。/reset 重建。"
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
	// 权限确认请求（由 tuiUserResponder 通过 p.Send 推送）
	// ------------------------------------------------------------------
	case permissionReqMsg:
		m.overlay = overlayPermission
		m.permReq = &msg
		m.input.Blur()
		m.permList = m.buildPermList()
		return m, nil

	// ------------------------------------------------------------------
	// 文件扫描完成（异步）
	// ------------------------------------------------------------------
	case pickerScanDoneMsg:
		// 丢弃过期扫描结果（用户输入已变化，发起了更新代数的新扫描）
		if msg.gen != m.pickerScanGen {
			return m, nil
		}
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
			current := m.input.Value()
			pos := m.input.Position()
			newValue := current[:pos] + insert + current[pos:]
			m.input.SetValue(newValue)
			m.input.SetCursor(pos + len(insert))
		}
		return m, nil
	}

	// 子组件更新（仅在无覆盖层时传递）
	switch m.overlay {
	case overlayNone:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	case overlayPermission:
		// 权限面板活跃时，将按键传给 list 组件（↑↓ 导航）
		var cmd tea.Cmd
		m.permList, cmd = m.permList.Update(msg)
		cmds = append(cmds, cmd)
	case overlayThemePicker:
		var cmd tea.Cmd
		m.themeList, cmd = m.themeList.Update(msg)
		cmds = append(cmds, cmd)
	case overlayModelPicker:
		var cmd tea.Cmd
		m.modelPickerList, cmd = m.modelPickerList.Update(msg)
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
		return true, m.focusPrev()

	case key.Matches(msg, m.keys.Enter):
		// 焦点在段落上 → 展开/折叠
		if m.focusIndex >= 0 && m.focusIndex < len(m.paras) {
			m.toggleParagraphFocus()
			return true, nil
		}
		if !m.running {
			userInput := strings.TrimSpace(m.input.Value())
			if userInput == "" {
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
				msg := "上下文已满（98%）。/reset 重建。"
				msgKind := notifWarn
				if reason == "tier3_failures" {
					msg = "摘要连续失败。/reset 重建。"
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
			// 空输入 → 仅中断，不发新任务
			if m.cancelRun != nil {
				m.cancelRun()
				m.cancelRun = nil
			}
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
		permItem{title: "Allow (本次放行)", choice: permAllow},
		permItem{title: "Always Allow (记住，不再询问)", choice: permAllowAll},
		permItem{title: "Deny (本次拒绝)", choice: permDeny},
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
			// Tab 补全可能进入子目录 → 重新扫描磁盘
			m.pickerFilter = extractFilterAfterAt(m.input.Value())
			m.pickerLastScannedBase = filepath.Base(m.pickerFilter)
			m.scanFiles()
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
	if len(m.pickerItems) == 0 {
		return ""
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
	var filtered []slashcommand.CommandInfo
	for _, cmd := range m.commandPickerItems {
		if filter == "" || strings.Contains(strings.ToLower(cmd.Name), filter) {
			filtered = append(filtered, cmd)
		}
	}

	m.buildCommandPickerList(filtered)
}

// buildCommandPickerList 从 CommandInfo 列表构建 bubbles/list 组件。
func (m *model) buildCommandPickerList(items []slashcommand.CommandInfo) {
	listItems := make([]list.Item, len(items))
	for i, cmd := range items {
		listItems[i] = commandPickerItem{name: cmd.Name, description: cmd.Description, args: cmd.Args}
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
	m.pickerFilter = extractFilterAfterAt(m.input.Value())
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

	m.pickerScanGen++
	gen := m.pickerScanGen
	go func() {
		// 150ms 防抖：等待用户停止输入后再扫描
		time.Sleep(150 * time.Millisecond)
		// 若在此期间有新扫描发起，代数已递增，跳过本次
		if m.pickerScanGen != gen {
			return
		}
		items := doScanFiles(m.registry, m.cwd, filter)
		if m.program != nil {
			m.program.Send(pickerScanDoneMsg{items: items, gen: gen})
		}
	}()
}

// activatePicker 首次激活文件选择器，异步扫描磁盘。
func (m *model) activatePicker() {
	m.pickerVisible = true
	m.pickerFilter = extractFilterAfterAt(m.input.Value())
	m.pickerLastValue = m.input.Value()

	// 立即用空列表占位，避免 View() 中 nil list
	m.pickerItems = nil
	m.buildPickerList()
}

// startPickerScan 返回一个 tea.Cmd，在 goroutine 中扫描文件并回传结果。
// 在调用时捕获 filter 和 generation，避免竞态。
func (m *model) startPickerScan() tea.Cmd {
	filter := m.pickerFilter
	m.pickerLastScannedBase = filepath.Base(filter)
	m.pickerScanGen++
	gen := m.pickerScanGen
	return func() tea.Msg {
		items := doScanFiles(m.registry, m.cwd, filter)
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

// scanFiles 扫描工作区文件列表，结果存入 m.pickerAllItems（在主 goroutine 调用）。
func (m *model) scanFiles() {
	m.pickerAllItems = doScanFiles(m.registry, m.cwd, m.pickerFilter)
}

// doScanFiles 执行实际的文件扫描（通过 search_file 工具），返回结果。
func doScanFiles(registry tool.Registry, cwd, filter string) []pickerItem {
	ctx := context.Background()

	doSearch := func(pattern string) (files []string) {
		jsonBytes, _ := json.Marshal(map[string]string{
			"pattern":     pattern,
			"working_dir": cwd,
		})
		result, err := registry.Execute(ctx, "search_file", jsonBytes)
		if err != nil || result.IsError() {
			return nil
		}
		return parseSearchFileOutput(result.Content)
	}

	var files []string
	if filter != "" {
		lastComp := filepath.Base(filter)
		if lastComp != "" && lastComp != "." && lastComp != "/" {
			files = doSearch("**/" + lastComp + "*")
		}
		if len(files) == 0 {
			files = doSearch("**/*")
		}
	} else {
		files = doSearch("**/*")
	}

	if len(files) == 0 {
		return nil
	}

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

	if len(items) > 500 {
		items = items[:500]
	}

	sortPickerItems(items)
	return items
}

// parseSearchFileOutput 解析 search_file 工具的输出为文件路径列表。
func parseSearchFileOutput(content string) []string {
	lines := strings.Split(content, "\n")
	var files []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// 跳过摘要头、截断警告和空行
		if strings.HasPrefix(line, "Found ") ||
			strings.HasPrefix(line, "Results truncated") ||
			strings.HasPrefix(line, "⚠️") ||
			strings.HasPrefix(line, "No files") ||
			strings.HasPrefix(line, "Searched under") {
			continue
		}
		files = append(files, line)
	}
	return files
}

// isHiddenOrBinary 检查路径是否应被过滤。
func isHiddenOrBinary(path string) bool {
	// 检查每个路径段是否以 . 开头（隐藏文件/目录）
	parts := strings.Split(path, string(filepath.Separator))
	for _, p := range parts {
		if strings.HasPrefix(p, ".") {
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

	// 创建 tool 段落
	m.paras = append(m.paras, Paragraph{
		Type:     paraTool,
		State:    stateStreaming,
		ToolName: ev.ToolCallName,
		ToolArgs: formatToolArgs(ev.ToolCallName, ev.Arguments, m.cwd),
	})
}

// handleToolResult 处理工具执行结果。
func (m *model) handleToolResult(ev agentloop.ToolCallResult) {
	// 查找匹配的 tool 段落（按 tool name + args 匹配，取最后一个 streaming 的）
	for i := len(m.paras) - 1; i >= 0; i-- {
		p := &m.paras[i]
		if p.Type == paraTool && p.State == stateStreaming && p.ToolName == ev.ToolCallName {
			p.ToolResult = truncateToolResult(ev.Result)
			p.ToolError = ev.Error
			p.ToolDurMs = ev.DurationMs
			p.ToolDenied = ev.Denied
			p.DiffHunks = ev.DiffHunks
			if ev.IsError() || ev.Denied {
				p.State = stateError
			} else if ev.DiffHunks != nil {
				p.State = stateExpanded // edit_file 直接展开完整 diff 视图
			} else {
				p.State = stateDone // 其他工具完成即折叠
			}
			p.renderDirty = true

			return
		}
	}
}

// truncateToolResult 将超长工具结果截断到 maxToolResultBytes，末尾追加截断提示。
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
		m.input.Placeholder = "输入消息, Enter 发送 · / 命令 · @ 选择文件 · Esc 中断"
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
	}

	// 所有终止原因都追加系统提示段落（仅当前 run）
	if !isStale {
		elapsedStr := formatDuration(elapsedMs)
		switch ev.Reason {
		case agentloop.ReasonCompleted:
			m.paras = append(m.paras, Paragraph{
				Type:      paraSystem,
				State:     stateDone,
				Text:      fmt.Sprintf("完成（%d轮, %s, ↑%s, ↓%s）", ev.Turn, elapsedStr, shortTokens(loopIn), shortTokens(loopOut)),
				NotifKind: notifInfo,
			})
		case agentloop.ReasonMaxTurns:
			m.paras = append(m.paras, Paragraph{
				Type:      paraSystem,
				State:     stateDone,
				Text:      fmt.Sprintf("已达最大轮次（%d轮, %s, ↑%s, ↓%s）。继续对话。", ev.Turn, elapsedStr, shortTokens(loopIn), shortTokens(loopOut)),
				NotifKind: notifInfo,
			})
		case agentloop.ReasonAborted:
			abortText := fmt.Sprintf("已中断（%s）", elapsedStr)
			abortKind := notifInfo
			if m.toolTimeout > 0 && isTimeoutError(ev.Err) {
				abortText = fmt.Sprintf("工具执行超时（%s %s）%s", m.toolTimeoutSource, formatDuration(m.toolTimeout.Milliseconds()), elapsedStr)
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
				Text:      fmt.Sprintf("模型错误（%s, %v）", elapsedStr, ev.Err),
				NotifKind: notifError,
			})
		case agentloop.ReasonToolFatal:
			text := fmt.Sprintf("工具错误（%s, %v）", elapsedStr, ev.Err)
			if m.toolTimeout > 0 && isTimeoutError(ev.Err) {
				text = fmt.Sprintf("工具执行超时（%s %s）%s", m.toolTimeoutSource, formatDuration(m.toolTimeout.Milliseconds()), elapsedStr)
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
		// 仅 shell 和 web_fetch 的输出值得展开/折叠，其他工具的输出
		// 或为结构化摘要（grep/ls/search_file/lsp_*）或通过预览行已传达完整信息。
		switch p.ToolName {
		case "shell", "web_fetch":
			if p.State != stateDone && p.State != stateCollapsed && p.State != stateExpanded {
				return false
			}
			// 折叠预览至多展示 maxPreviewWrapped 行，若全部输出未溢出则无需展开。
			// 阈值与 renderToolPreview 中 writeWrappedPreview 的截断条件保持一致。
			if p.State == stateDone || p.State == stateCollapsed {
				body := stripToolStatusHeader(p.ToolResult)
				if p.ToolName == "web_fetch" {
					// web_fetch 预览跳过空行，计数时也跳过
					body = parseWebFetchBody(p.ToolResult)
					return countWrappedLinesNonEmpty(body, contentWidth-2) >= maxPreviewWrapped
				}
				return countWrappedLines(body, contentWidth-2) >= maxPreviewWrapped
			}
			return true
		}
		return false
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
	m.input.Placeholder = "段落已聚焦 · Enter 展开/折叠 · Esc 回到输入"
}

// exitFocusMode 退出段落焦点模式：输入框恢复焦点、placeholder 恢复默认。
// 返回 Focus 的 tea.Cmd（光标闪烁），调用方应将其合并到 Update 返回值中。
func (m *model) exitFocusMode() tea.Cmd {
	// Prompt 在 New() 中默认设为 "> "，零值 Model 为空字符串，
	// 借此区分测试中未初始化的 input，避免 virtualCursor.Blink 空指针。
	if m.input.Prompt != "" {
		cmd := m.input.Focus()
		m.input.Placeholder = "输入消息, Enter 发送 · / 命令 · @ 选择文件 · Esc 中断"
		return cmd
	}
	m.input.Placeholder = "输入消息, Enter 发送 · / 命令 · @ 选择文件 · Esc 中断"
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
		case stateExpanded:
			p.State = stateDone
		}
	}
	p.renderDirty = true
}

// viewportCtx 返回段落渲染所需的上下文（spinners + Glamour renderer）。
func (m *model) viewportCtx() ViewportCtx {
	contentWidth := max(m.width-4, 20)
	return ViewportCtx{
		Asst:    m.spAsst,
		Thought: m.spThought,
		Tool:    m.spTool,
		Glamour: m.glamourRenderer,
		Width:   contentWidth,
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
	m.input.Placeholder = "Agent 执行中... Esc 中断"
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

// visibleOffset 计算 textinput 水平滚动后在可视范围内光标的起始偏移量。
// textinput 内容超出可视宽度时内部维护 offset（未公开），通过从光标位置
// 反向扫描累积显示宽度到等于输入框宽度来确定可视起点。
func visibleOffset(value string, pos int, maxWidth int) int {
	runes := []rune(value)
	if pos > len(runes) {
		pos = len(runes)
	}
	if maxWidth <= 0 {
		return 0
	}

	width := 0
	offset := pos
	for offset > 0 {
		w := lipgloss.Width(string(runes[offset-1]))
		if width+w > maxWidth {
			break
		}
		width += w
		offset--
	}
	return offset
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
	footerHeight := lipgloss.Height(footer) // 2 行

	// 固定底部元素（在 styleApp 内）：
	// separator(1) + input(1) + 空行(1) + footer(footerHeight)
	fixedBottomHeight := 1 + 1 + 1 + footerHeight

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
	separator := m.renderInputSeparator(contentWidth)
	inputView := lipgloss.NewStyle().Width(contentWidth).Render(m.input.View())

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
	parts = append(parts, separator, inputView, "", footer)

	mainBody := lipgloss.JoinVertical(lipgloss.Left, parts...)
	mainContent := styleApp.Render(mainBody)

	v := tea.NewView(mainContent)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion

	// real cursor 模式：定位输入光标
	if m.overlay == overlayNone {
		if cur := m.input.Cursor(); cur != nil {
			value := m.input.Value()
			pos := m.input.Position()
			offset := visibleOffset(value, pos, m.input.Width())
			visibleBeforeCursor := string([]rune(value)[offset:pos])
			cursorX := lipgloss.Width(m.input.Prompt) + lipgloss.Width(visibleBeforeCursor)
			cur.Position.X = cursorX + 2 // styleApp 左 padding
			if cur.Position.X > m.width-2 {
				cur.Position.X = m.width - 2
			}
			// 在 alt screen 下，header + body + overlays + separator 之后是 input 行
			// input 行在终端中的 Y 坐标（0-based）：
			//   styleApp top(1) + headerHeight + bodyHeight + overlayLines + pickerLines + commandPickerLines + separator(1)
			cur.Position.Y = 1 + headerHeight + bodyHeight + overlayLines + pickerLines + commandPickerLines + 1
			if cur.Position.Y >= m.height {
				cur.Position.Y = m.height - 1
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
// 焦点模式下嵌入段落聚焦提示，正常模式为纯横线。
func (m *model) renderInputSeparator(contentWidth int) string {
	if m.focusIndex >= 0 {
		hint := " ◆ 段落已聚焦  Enter 展开/折叠  Esc 退出焦点模式 ◆ "
		hintStyle := lipgloss.NewStyle().Foreground(colorAccentGold)
		lineStyle := lipgloss.NewStyle().Foreground(colorMuted)
		// hint 居中，两侧用 ─ 补齐
		pad := contentWidth - lipgloss.Width(hint)
		if pad < 2 {
			pad = 2
		}
		left := strings.Repeat("─", pad/2)
		right := strings.Repeat("─", pad-pad/2)
		return lineStyle.Render(left) + hintStyle.Render(hint) + lineStyle.Render(right)
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

	// 信息行：session ID（左） + 版本号（右对齐），形成统一顶栏
	sidLine := ""
	if sid := m.cm.SessionID(); sid != "" {
		sidPart := styleHeaderAccent.Render("session: ") +
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

	// Line 1: spinner + model name + ctx progress bar
	indicator := styleFooterLabel.Render("•") + " "
	if m.running {
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

	return line1 + "\n" + line2
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
	if toolName == "shell" && argsSummary != "" {
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
			Text:      fmt.Sprintf("未知命令: %s。输入 /help 查看可用命令。", input),
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
			Text:      fmt.Sprintf("命令执行失败: %v", err),
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
			strings.Contains(result.Text, "无法") || strings.Contains(result.Text, "error") {
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
			m.focusIndex = -1
			m.paras = append(m.paras, Paragraph{
				Type:      paraSystem,
				State:     stateDone,
				Text:      "新 session 已创建。",
				NotifKind: notifInfo,
			})

		case slashcommand.SideEffectOpenThemePicker:
			m.buildThemeList()
			m.overlay = overlayThemePicker
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
	return writeFullSettings(s.projectPath, settings, "")
}

func (s *tuiSettingsStore) SaveTheme(mode string) error {
	return writeFullSettings(s.projectPath, nil, mode)
}

// writeFullSettings 全量 read-modify-write settings.json，替换 llm section 和/或 theme。
// llmSettings 为 nil 时保留已有的 llm section；theme 为空时保留已有的 theme。
func writeFullSettings(path string, llmSettings *llm.LLMSettings, theme string) error {
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

// newSlashRegistry 创建 slash command 注册表，注入 TUI 侧依赖实现。
func newSlashRegistry(creator slashcommand.SessionCreator, store slashcommand.SettingsStore, lister slashcommand.ModelLister, currentModel string) *slashcommand.Registry {
	r := slashcommand.NewRegistry()
	r.Register(slashcommand.NewNewCommand(creator))
	r.Register(slashcommand.NewModelCommand(store, lister, currentModel))
	r.Register(slashcommand.NewThemeCommand())
	r.Register(slashcommand.NewHelpCommand(r))
	return r
}

// ---------------------------------------------------------------------------
// 覆盖层 — 主题选择器
// ---------------------------------------------------------------------------

// themeItems 返回主题选择器的固定选项。
var themeItems = []themeItem{
	{label: "Auto（自动检测终端背景色）", mode: "auto"},
	{label: "Dark", mode: "dark"},
	{label: "Light", mode: "light"},
}

// buildThemeList 构建主题选择列表覆盖层。
func (m *model) buildThemeList() {
	items := make([]list.Item, len(themeItems))
	selectedIdx := 0
	for i, ti := range themeItems {
		items[i] = ti
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
	if err := m.settingsStore.SaveLLM(settings); err != nil {
		// 忽略写入错误，用户感知到 HUD 已更新
	}
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
func runTUI(llmClient llm.Client, registry tool.Registry, guard permission.Guard, expander *reference.Expander, modelName string, theme string, verboseLog io.Writer, contextLimit int, maxTurns int, toolTimeout time.Duration, toolTimeoutSource string, bypassPerm bool, ctxMgr *ctxpkg.ContextManager, isResume bool, sessionDir string, globalPath string, projectPath string, agentsMdText string) {
	m := newTUIModel(llmClient, registry, guard, expander, modelName, theme, verboseLog, contextLimit, maxTurns, toolTimeout, toolTimeoutSource)
	m.agentsMdText = agentsMdText
	m.sessionDir = sessionDir

	// 构造 slash command registry（TUI 侧依赖实现）
	store := &tuiSettingsStore{projectPath: projectPath, globalPath: globalPath}
	m.settingsStore = store
	lister := &tuiModelLister{client: llmClient}
	sessionCreator := &tuiSessionCreator{m: m}
	m.slashRegistry = newSlashRegistry(sessionCreator, store, lister, modelName)

	m.bypassPerm = bypassPerm
	// 用外部创建的 ContextManager 替换 newTUIModel 内部创建的
	m.cm = ctxMgr
	// 恢复会话级 HUD 累积值
	m.hudCacheHit = ctxMgr.Stats().TotalCacheHitTokens
	m.hudCacheMiss = ctxMgr.Stats().TotalCacheMissTokens
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
		if err := ctxpkg.UpdateRecentSessions(sessionDir, sid, stats.MessageCount); err != nil {
			// 静默失败
		}
	}

	p := tea.NewProgram(m)
	m.program = p
	m.wireLoop()  // 注入 tuiUserResponder，此时 program 已就绪
	m.initTheme() // 根据 themeMode + 终端背景自动检测并应用主题

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI 运行出错: %v\n", err)
		os.Exit(1)
	}

	// 正常退出时保存 session 并提示 session ID
	m.cm.Save()
	if sid := m.cm.SessionID(); sid != "" {
		// 退出时用最终统计更新 recent.json（覆盖启动时写入的初始值）
		stats := m.cm.Stats()
		_ = ctxpkg.UpdateRecentSessions(sessionDir, sid, stats.MessageCount)
		fmt.Fprintf(os.Stderr, "已保存 session: %s\n", sid)
		fmt.Fprintf(os.Stderr, "  恢复对话: waveloom --resume %s\n", sid)
	}
}
