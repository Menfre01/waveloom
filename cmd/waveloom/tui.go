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
//	Ctrl+O   展开/折叠最后一个 tool 输出
//	Ctrl+T   展开/折叠最后一个 thought
//	Ctrl+G   切换主题
//	Ctrl+C   退出
package main

import (
	"context"
	"encoding/json"
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
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	glamourstyles "charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"

	"waveloom/pkg/agentloop"
	ctxpkg "waveloom/pkg/context"
	"waveloom/pkg/llm"
	"waveloom/pkg/permission"
	"waveloom/pkg/reference"
	"waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// 常量
// ---------------------------------------------------------------------------

var defaultSystemPrompt = fmt.Sprintf(`You are Waveloom %s, a terminal-based coding agent. You help users write, refactor, debug, and explore code. You are precise, safe, and efficient.

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
- When using shell, prefer rg over grep, and prefer checking exit codes over parsing output.
- When using shell, use the working_dir parameter to set the working directory. Do NOT prepend "cd <path> &&" to the command — this breaks permission pattern matching.
- After making changes, verify them — compile, run tests, or check diffs where applicable.

## Coding standards

- Follow existing codebase conventions. Do not introduce new patterns without justification.
- Use clear, self-documenting names. Avoid abbreviations and single-letter variables.
- Maintain consistent error handling — errors propagate cleanly, not with raw stack traces to the client.
- Keep functions small and focused. Extract helpers only when reuse is clear.

## Termination

- Stop and report completion when the user's request is fully satisfied.
- If you cannot complete a task, explain the bottleneck concisely and propose next steps.
- Do NOT loop on the same sub-task repeatedly. If stuck, ask for guidance.`, Version)

// ---------------------------------------------------------------------------
// 自定义消息类型
// ---------------------------------------------------------------------------

// maxParas 是段落列表的硬上限，超出时从头部淘汰旧段落。
// 200 个段落 ≈ 40–60 个典型 turn，保证渲染性能稳定。
const maxParas = 200

// maxToolResultBytes 是单个工具结果的最大存储字节数。
// 超出部分截断，展开时通过 Ctrl+O 无法看到被截断内容，
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

// viewportKeyMap 返回 viewport 的按键映射，去掉与打字冲突的单字母键。
// 仅保留方向键和组合键作为 viewport 导航：
//
//	↑/↓       逐行滚动
//	PgUp/PgDn 翻页
//	Ctrl+U/D  半页滚动
func viewportKeyMap() viewport.KeyMap {
	km := viewport.DefaultKeyMap()
	// 禁用单字母导航键（j/k/u/d/h/l），否则在 input 中键入这些字符时
	// viewport 也会收到并触发滚动，导致界面抖动。
	km.Up = key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "line up"))
	km.Down = key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "line down"))
	km.Left = key.NewBinding()
	km.Right = key.NewBinding()
	km.HalfPageUp = key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "½ page up"))
	km.HalfPageDown = key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "½ page down"))
	km.PageUp = key.NewBinding(key.WithKeys("pgup"), key.WithHelp("PgUp", "page up"))
	km.PageDown = key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("PgDn", "page down"))
	return km
}

// keyMap 定义所有快捷键绑定。
type keyMap struct {
	Enter         key.Binding
	Interrupt     key.Binding
	Quit          key.Binding
	ToggleTool    key.Binding
	ToggleThought key.Binding
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
	ToggleTool:    key.NewBinding(key.WithKeys("ctrl+o"), key.WithHelp("Ctrl+O", "展开/折叠 shell 输出")),
	ToggleThought: key.NewBinding(key.WithKeys("ctrl+t"), key.WithHelp("Ctrl+T", "展开/折叠 thought")),
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

type model struct {
	// 外部依赖
	cm         *ctxpkg.ContextManager
	llmClient  llm.Client
	registry   tool.Registry
	guard      permission.Guard
	expander   *reference.Expander
	cwd        string
	verboseLog io.Writer // --verbose 日志输出（nil = 不记录）
	loop       *agentloop.Loop

	// 段落模型（viewport 内容的数据源）
	paras []Paragraph

	// 段落行号索引（paraLineStarts[i] = 段落 i 在 viewport 内容中的起始行号）
	paraLineStarts []int

	// viewport 内容的缓存（避免 renderShrunkViewport 重复 build）
	cachedViewportLines []string

	// Glamour markdown 渲染器
	glamourRenderer *glamour.TermRenderer

	// 覆盖层状态
	overlay      Overlay
	permReq      *permissionReqMsg     // 当前待确认的权限请求
	permList     list.Model            // 权限选项列表（bubbles/list）
	permDelegate *list.DefaultDelegate // 权限列表的 delegate 指针，主题切换时更新样式

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
	lastPromptTokens int // ctx bar 实时值（TurnStats → API 真实值，Tier 3 完成 → 归零）

	// Bubbletea 组件
	program        *tea.Program // 在 main 中注入，用于 goroutine → TUI 通信
	keys           keyMap
	help           help.Model
	spinner        spinner.Model  // 通用 spinner（HUD 加载指示）
	spAsst         spinner.Model  // assistant 流式前缀动画
	spThought      spinner.Model  // thought 流式前缀动画
	spTool         spinner.Model  // tool 执行中前缀动画
	ctxProgress    progress.Model // ctx 窗口进度条（bubbles progress 组件）
	lastRenderTime time.Time      // 上次 viewport 渲染时间，用于流式节流
	input          textinput.Model
	viewport       viewport.Model

	// 输入历史
	inputHistory []string // 已提交的输入，最新在前
	historyPos   int      // 当前历史位置（-1 = 不在历史导航中）
	historyDraft string   // 进入历史导航前输入框中的草稿文本

	// 双击 Esc 清空输入
	lastEscTime time.Time // 上次在空闲态按 Esc 的时间

	// 状态
	running   bool               // agent loop 正在执行中
	cancelRun context.CancelFunc // 取消当前运行的 agent loop（nil 表示无运行中 loop）
	themeMode string             // 当前主题模式: auto / dark / light
	palette   palette            // 当前配色
	width     int
	height    int
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
func newTUIModel(llmClient llm.Client, registry tool.Registry, guard permission.Guard, expander *reference.Expander, modelName string, theme string, verboseLog io.Writer, contextLimit int) *model {
	if modelName == "" {
		modelName = "deepseek-v4"
	}

	cwd, _ := os.Getwd()
	cm := ctxpkg.New(buildSystemPrompt(cwd))

	ti := textinput.New()
	ti.Prompt = "› "
	ti.Placeholder = "输入消息，Enter 发送..."
	ti.CharLimit = 2048

	// 初始尺寸设 0，首个 WindowSizeMsg 后由 resizeViewport() 接管
	vp := viewport.New(viewport.WithWidth(0), viewport.WithHeight(0))
	vp.KeyMap = viewportKeyMap()

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
		glamourRenderer: glamourR,
		keys:            defaultKeys,
		help:            help.New(),
		spinner:         sp,
		spAsst:          spAsst,
		spThought:       spThought,
		spTool:          spTool,
		ctxProgress:     cp,
		input:           ti,
		viewport:        vp,
		historyPos:      -1,
		overlay:         overlayNone,
		themeMode:       theme,
		palette:         darkPalette, // 默认，initTheme 覆盖
	}
}

// wireLoop 在 model 创建后、program 注入后调用，创建 agent loop 并注入 tuiUserResponder。
func (m *model) wireLoop() {
	m.loop = agentloop.New(m.llmClient, m.registry, agentloop.Config{
		MaxTurns:      0,
		SystemPrompt:  "",
		Guard:         m.guard,
		UserResponder: &tuiUserResponder{program: m.program, cwd: m.cwd},
		VerboseWriter: m.verboseLog,
		Compactor:     m.cm.Compactor(),
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

		headerLines, bottomLines := m.measureLayout()
		vpHeight := m.height - 1 - headerLines - 1 - bottomLines // padTop + header + gap(1)
		if vpHeight < 5 {
			vpHeight = 5
		}

		m.viewport.SetWidth(contentWidth)
		m.viewport.SetHeight(vpHeight)
		m.setViewportContent(m.viewport.AtBottom())

		m.input.SetWidth(contentWidth - 4)

		// 重建 Glamour renderer 以适配新宽度
		// 实际可用文本宽度 = viewport 宽度(m.width-4) - 前缀宽度(2) = m.width-6
		if m.glamourRenderer != nil {
			m.glamourRenderer.Close()
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

		// 任意段落处于流式时，每次 tick 都重建 viewport 内容以刷新 spinner
		// 帧。保持滚动位置不动，避免打断用户阅读。
		if m.hasStreamingPara() {
			m.refreshViewportContent()
		}

		return m, tea.Batch(cmds...)

	// ------------------------------------------------------------------
	// Agent Loop 流式事件（由 goroutine 通过 p.Send 推送）
	// ------------------------------------------------------------------
	case agentloop.StreamDelta:
		m.handleStreamDelta(msg)
		m.refreshViewportContent()
		return m, nil

	case agentloop.ToolCallStart:
		m.handleToolStart(msg)
		m.refreshViewportContent()
		return m, nil

	case agentloop.ToolCallResult:
		m.handleToolResult(msg)
		m.refreshViewportContent()
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
				Type:  paraSystem,
				State: stateDone,
				Text:  "上下文压缩完成：Tier 3 摘要已生成",
			})
			m.trimParas()
		}
		if msg.Compaction.HardLimitReached {
			msgText := "上下文已达上限（98%），后续 LLM 调用已被阻止。使用 /reset 重建会话。"
			if msg.Compaction.HardLimitReason == "tier3_failures" {
				msgText = "Tier 3 摘要连续失败已达上限，后续 LLM 调用已被阻止。使用 /reset 重建会话。"
			}
			m.paras = append(m.paras, Paragraph{
				Type:  paraSystem,
				State: stateDone,
				Text:  msgText,
			})
			m.trimParas()
		}
		return m, nil

	case agentloop.BalanceUpdate:
		if msg.Balance != nil {
			m.hudBalanceAvail = msg.Balance.IsAvailable
			m.hudBalance = formatBalance(msg.Balance)
		}
		return m, nil

	case agentloop.LoopDone:
		m.handleLoopDone(msg)
		m.updateViewportContent()
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
	} else if shouldActivatePicker(m.input.Value()) && !m.running && m.overlay == overlayNone {
		// 选择器刚关闭且 input 值未变 → 阻止立即重新触发
		if m.input.Value() != m.pickerDismissValue {
			m.activatePicker()
			cmds = append(cmds, m.startPickerScan())
		}
	}

	// viewport 更新：正常模式始终更新；权限面板活跃时同样更新以便滚动上下文
	switch m.overlay {
	case overlayNone, overlayPermission:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
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
//  1. 全局快捷键（所有状态/覆盖层下生效）：Quit, ToggleTool, ToggleThought, ToggleTheme, JumpBottom, PageUp/Down
//  2. 文件选择器活跃 → handlePickerKey
//  3. 权限面板活跃 → permList 导航 + handlePermKey
//  4. 正常模式快捷键
func (m *model) handleKeyPress(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	// =====================================================================
	// 1. 全局快捷键（所有状态/覆盖层下生效，行为高度统一）
	// =====================================================================
	switch {
	case key.Matches(msg, m.keys.Quit):
		return true, tea.Quit

	case key.Matches(msg, m.keys.ToggleTool):
		m.toggleLastTool()
		return true, nil

	case key.Matches(msg, m.keys.ToggleThought):
		m.toggleLastThought()
		return true, nil

	case key.Matches(msg, m.keys.ToggleTheme):
		m.toggleTheme()
		return true, nil

	case key.Matches(msg, m.keys.JumpBottom):
		m.viewport.GotoBottom()
		return true, nil

	case key.Matches(msg, m.keys.PageUp),
		key.Matches(msg, m.keys.PageDown):
		// 导航键始终传给 viewport，不传给 input
		return false, nil
	}

	// =====================================================================
	// 2. 文件选择器活跃时路由
	// =====================================================================
	if m.pickerVisible {
		return m.handlePickerKey(msg)
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
	// 4. 正常模式快捷键
	// =====================================================================
	switch {
	case key.Matches(msg, m.keys.Enter):
		if !m.running {
			userInput := strings.TrimSpace(m.input.Value())
			if userInput == "" {
				return true, nil
			}

			// 硬临界值阻断：上下文已达上限时直接拒绝新 prompt
			if m.cm.Compactor().LastResult().HardLimitReached {
				reason := m.cm.Compactor().LastResult().HardLimitReason
				msg := "上下文已达上限（98%），后续 LLM 调用已被阻止。使用 /reset 重建会话。"
				if reason == "tier3_failures" {
					msg = "Tier 3 摘要连续失败已达上限，后续 LLM 调用已被阻止。使用 /reset 重建会话。"
				}
				m.paras = append(m.paras, Paragraph{
					Type:  paraSystem,
					State: stateDone,
					Text:  msg,
				})
				m.updateViewportContent()
				return true, nil
			}

			m.input.Reset()
			return true, m.doTurn(userInput)
		}
		return true, nil // running 时吞掉 enter

	case key.Matches(msg, m.keys.Interrupt):
		if m.running && m.cancelRun != nil {
			m.cancelRun()
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
		// 空闲态 ↑ → 输入历史导航；否则传给 viewport
		if !m.running && m.overlay == overlayNone && !m.pickerVisible {
			if m.navigateHistoryUp() {
				return true, nil
			}
		}
		return false, nil

	case key.Matches(msg, m.keys.Down):
		// 空闲态 ↓ → 输入历史导航；否则传给 viewport
		if !m.running && m.overlay == overlayNone && !m.pickerVisible {
			if m.navigateHistoryDown() {
				return true, nil
			}
		}
		return false, nil

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

	// 其他按键（PgUp/PgDn/Tab/Ctrl+O 等）透传给 viewport，允许权限框显示时滚动查看上下文
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
	// 替换 @ 及其后的内容为 @{selectedPath}
	newValue := value[:atIdx] + "@" + selected
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
	if strings.Contains(afterAt, " ") {
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
	if maxHeight > 12 {
		maxHeight = 12
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
		// 跳过摘要头和警告行
		if strings.HasPrefix(line, "Found ") || strings.HasPrefix(line, "⚠️") || strings.HasPrefix(line, "No files") || strings.HasPrefix(line, "Searched under") {
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
// 完整结果已通过 agent loop 传递给 LLM，截断仅影响 TUI 展示。
func truncateToolResult(result string) string {
	if len(result) <= maxToolResultBytes {
		return result
	}
	return result[:maxToolResultBytes] + "\n... (output truncated)"
}

// handleLoopDone 处理循环终止。
func (m *model) handleLoopDone(ev agentloop.LoopDone) {
	m.running = false

	// 计算延迟（必须在 CompleteRun 之前，因为 CompleteRun 需要 durationMs）
	if !m.turnStartTime.IsZero() {
		m.hudLatMs = time.Since(m.turnStartTime).Milliseconds()
	}

	// 提交到 ContextManager（stats 累加 + 落盘；压缩已在 Loop 内完成）
	result := m.cm.CompleteRun(ev.Messages, m.loopPrompt, m.lastTurnPrompt, m.loopCompl, m.loopCacheHit, m.loopCacheMiss, m.loopReasoning, m.hudModel, m.hudLatMs, string(ev.Reason))

	// loop 级增量归零，准备下一个 loop
	m.loopPrompt = 0
	m.loopCompl = 0
	m.loopCacheHit = 0
	m.loopCacheMiss = 0
	m.loopReasoning = 0
	m.lastTurnPrompt = 0
	m.lastTurnCompl = 0

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

	// 非正常/正常终止都追加系统提示段落
	switch ev.Reason {
	case agentloop.ReasonMaxTurns:
		m.paras = append(m.paras, Paragraph{
			Type:  paraSystem,
			State: stateDone,
			Text:  fmt.Sprintf("已达到最大轮次限制（%d）。输入消息继续对话。", ev.Turn),
		})
	case agentloop.ReasonAborted:
		m.paras = append(m.paras, Paragraph{
			Type:  paraSystem,
			State: stateDone,
			Text:  "执行被中断。",
		})
	case agentloop.ReasonModelError:
		m.paras = append(m.paras, Paragraph{
			Type:  paraSystem,
			State: stateDone,
			Text:  fmt.Sprintf("模型错误: %v", ev.Err),
		})
	}

	// Tier 3 完成后 ctx bar 归零，等待下一个 TurnStats 用 API 真实值恢复
	if result.Compaction.Tier3SummaryDone {
		m.lastPromptTokens = 0
	}
	_ = result // 供上层使用

	// 段落数超过上限时淘汰旧段落，防止内存无限增长
	m.trimParas()

	// 异步查询余额（不影响主流程）
	if m.llmClient.SupportsBalance() {
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

// trimParas 当段落数超过 maxParas 时从头部淘汰旧段落，
// 同步清理 viewport 行缓存、段落行号索引和滚动偏移。
func (m *model) trimParas() {
	if len(m.paras) <= maxParas {
		return
	}
	remove := len(m.paras) - maxParas

	// 计算被淘汰段落在 viewport 中占的行数
	removedLines := 0
	if remove < len(m.paraLineStarts) {
		removedLines = m.paraLineStarts[remove]
	} else if len(m.paraLineStarts) > 0 {
		removedLines = len(m.cachedViewportLines)
	}

	// 淘汰旧段落
	m.paras = append([]Paragraph{}, m.paras[remove:]...)

	// 重建 paraLineStarts：偏移后续段落的行号
	newStarts := make([]int, len(m.paras))
	for i := range newStarts {
		if i+remove < len(m.paraLineStarts) {
			newStarts[i] = m.paraLineStarts[i+remove] - removedLines
		}
	}
	m.paraLineStarts = newStarts

	// 从 viewport 行缓存头部切除
	if removedLines <= len(m.cachedViewportLines) {
		m.cachedViewportLines = m.cachedViewportLines[removedLines:]
	} else {
		m.cachedViewportLines = nil
	}

	// 调整滚动偏移，防止视口跳变
	yOff := m.viewport.YOffset() - removedLines
	if yOff < 0 {
		yOff = 0
	}
	m.viewport.SetYOffset(yOff)
}

// toggleLastTool 切换最后一个 tool 段落的折叠/展开状态。
func (m *model) toggleLastTool() {
	idx := findLastTool(m.paras)
	if idx < 0 {
		return
	}
	p := &m.paras[idx]
	// 仅 shell 支持展开/折叠；diff 视图始终展开
	if p.ToolName != "shell" {
		return
	}
	switch p.State {
	case stateExpanded:
		p.State = stateDone // 回到折叠预览
	case stateDone:
		p.State = stateExpanded
	default:
		return // 流式或其他状态不允许切换
	}
	p.renderDirty = true
	m.setViewportContent(false)
}

// toggleLastThought 切换最后一个 thought 段落的折叠/展开状态。
func (m *model) toggleLastThought() {
	idx := findLastThought(m.paras)
	if idx < 0 {
		return
	}
	p := &m.paras[idx]
	switch p.State {
	case stateExpanded:
		p.State = stateCollapsed
	case stateCollapsed:
		p.State = stateExpanded
	default:
		return // 流式或其他状态不允许切换
	}
	p.renderDirty = true
	m.setViewportContent(false)
}

// ---------------------------------------------------------------------------
// Viewport 内容更新
// ---------------------------------------------------------------------------

// renderViewportAtHeight 从缓存的 viewport 内容行中取可见切片，返回精准 vpHeight 行。
// 直接切片 m.cachedViewportLines，不再经过临时 bubbles viewport，确保行数完全确定。
func (m *model) renderViewportAtHeight(vpHeight int) []string {
	if vpHeight < 1 {
		vpHeight = 1
	}

	totalLines := len(m.cachedViewportLines)
	if totalLines == 0 {
		return make([]string, vpHeight)
	}

	yOff := m.viewport.YOffset()
	if m.viewport.AtBottom() {
		yOff = max(0, totalLines-vpHeight)
	} else if yOff > totalLines-vpHeight {
		yOff = max(0, totalLines-vpHeight)
	}

	end := yOff + vpHeight
	if end > totalLines {
		end = totalLines
	}

	lines := make([]string, vpHeight)
	copy(lines, m.cachedViewportLines[yOff:end])
	return lines
}

// setViewportContent 从段落列表重建 viewport 内容，gotoBottom 控制是否跟底。
// 调用方负责决定是否跟底——流式刷新应传 atBottom，用户操作应传 true/false。
func (m *model) setViewportContent(gotoBottom bool) {
	lines, lineStarts := buildViewportContent(m.paras, m.viewportCtx(), len(m.cachedViewportLines))
	m.cachedViewportLines = lines
	m.viewport.SetContentLines(lines)
	if gotoBottom {
		m.viewport.GotoBottom()
	}
	m.paraLineStarts = lineStarts
}

// updateViewportContent 从段落列表重建 viewport 并滚到底部。
func (m *model) updateViewportContent() {
	m.setViewportContent(true)
}

// refreshViewportContent 从段落列表重建 viewport，但仅在用户处于底部时才跟底，
// 避免打断上滚阅读。流式期间节流至 ~30ms 避免高频重建。
//
// 快速路径：仅最后一个段落处于流式时，增量更新该段落行而不重建全部缓存，
// 将每帧开销从 O(总段落数) 降至 O(1)。
func (m *model) refreshViewportContent() {
	// 流式期间节流：距上次渲染不足 30ms 则跳过，spinner tick 兜底刷新
	if m.hasStreamingPara() {
		elapsed := time.Since(m.lastRenderTime)
		if elapsed < 30*time.Millisecond {
			return
		}
	}
	m.lastRenderTime = time.Now()

	// 快速路径：仅最后一个段落流式 → 增量拼接尾部段落
	// 需要 paraLineStarts 与 paras 长度一致（新增段落时可能尚未同步）
	if m.streamingIsLastOnly() && len(m.paraLineStarts) == len(m.paras) {
		atBottom := m.viewport.AtBottom()
		m.spliceLastParagraph()
		if atBottom {
			m.viewport.GotoBottom()
		}
		return
	}

	m.setViewportContent(m.viewport.AtBottom())
}

// streamingIsLastOnly 返回是否仅最后一个段落处于流式状态。
func (m *model) streamingIsLastOnly() bool {
	if len(m.paras) == 0 {
		return false
	}
	last := m.paras[len(m.paras)-1]
	return last.State == stateStreaming
}

// spliceLastParagraph 仅重建最后一个段落的渲染行并拼接到 viewport 缓存尾部，
// 避免 buildViewportContent 的全量复制。仅在 streamingIsLastOnly() 为 true 时调用。
func (m *model) spliceLastParagraph() {
	lastIdx := len(m.paras) - 1
	oldStart := m.paraLineStarts[lastIdx]

	// 渲染最后一个段落为行数组
	ctx := m.viewportCtx()
	newLines := renderSingleParagraph(&m.paras[lastIdx], ctx)

	// 计算旧段落行数
	oldEnd := len(m.cachedViewportLines)
	if lastIdx+1 < len(m.paraLineStarts) {
		oldEnd = m.paraLineStarts[lastIdx+1]
	}
	oldLen := oldEnd - oldStart
	delta := len(newLines) - oldLen

	// 原地拼接：头 + 新行 + 尾
	total := len(m.cachedViewportLines) + delta
	merged := make([]string, total)
	copy(merged, m.cachedViewportLines[:oldStart])
	copy(merged[oldStart:], newLines)
	copy(merged[oldStart+len(newLines):], m.cachedViewportLines[oldEnd:])
	m.cachedViewportLines = merged

	// 偏移后续段落的行号索引
	for i := lastIdx + 1; i < len(m.paraLineStarts); i++ {
		m.paraLineStarts[i] += delta
	}

	m.viewport.SetContentLines(m.cachedViewportLines)
}

// viewportCtx 返回 viewport 渲染所需的上下文（spinners + Glamour renderer）。
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

	m.running = true
	m.turnStartTime = time.Now()
	m.updateViewportContent()

	// 创建可取消的 context（在 goroutine 外创建，避免 race）
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelRun = cancel

	// 3. 返回一个 tea.Cmd：在 goroutine 中消费 loop.Run() channel，
	//    通过 p.Send 实时推送事件到 TUI Update。
	return func() tea.Msg {
		defer cancel()
		defer func() { m.cancelRun = nil }()

		for ev := range m.loop.Run(ctx, messagesSnapshot) {
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
// 视图
// ---------------------------------------------------------------------------

// measureLayout 测量 header 和底部固定区域的实际显示行数。
// header/footer 均不以 \n 结尾，实际行数 = lipgloss.Height + 1。
func (m *model) measureLayout() (headerLines, bottomLines int) {
	header := m.renderHeader()
	footer := m.renderFooter()
	headerLines = lipgloss.Height(header) + 1
	footerLines := lipgloss.Height(footer) + 1
	// 底部固定块：分隔线(1) + 输入框(1) + 间距(1) + footer
	bottomLines = 1 + 1 + 1 + footerLines
	return
}

func (m *model) View() tea.View {
	contentWidth := max(m.width-4, 20)

	// 1. 渲染固定元素
	header := m.renderHeader()
	separator := lipgloss.NewStyle().
		Foreground(colorMuted).
		Render(strings.Repeat("─", contentWidth))
	inputView := styleInput.Render(m.input.View())
	footer := m.renderFooter()

	// 2. 渲染权限确认覆盖层（如有，占用 viewport 空间）
	var overlayContent string
	var overlayHeight int
	if m.overlay == overlayPermission && m.permReq != nil {
		boxWidth := contentWidth
		if boxWidth > 70 {
			boxWidth = 70
		}
		overlayContent = m.renderPermOverlay(boxWidth)
		overlayHeight = strings.Count(overlayContent, "\n") + 1
	}

	// 4. 渲染文件选择器下拉列表（如有），并计算其高度
	var pickerContent string
	var pickerHeight int
	if m.pickerVisible {
		pickerContent = m.renderPickerDropdown(contentWidth)
		pickerHeight = strings.Count(pickerContent, "\n") + 1
	}

	// 5. 计算 viewport 高度（扣除 header / 权限框 / 文件选择器 / 底部固定区域）
	headerLines, bottomLines := m.measureLayout()
	vpHeight := m.height - 1 - headerLines - 1 - bottomLines // padTop + header + gap(1)
	vpHeight -= overlayHeight                                // 权限覆盖层
	vpHeight -= pickerHeight                                 // 文件选择器下拉
	if vpHeight < 5 {
		vpHeight = 5
	}

	// 6. 渲染 viewport
	vpView := strings.Join(m.renderViewportAtHeight(vpHeight), "\n")

	// 7. 纵向拼接：header → viewport → [权限框] → [文件选择器] → separator → input → footer
	parts := []string{header, "", vpView}
	if overlayContent != "" {
		parts = append(parts, overlayContent)
	}
	if pickerContent != "" {
		parts = append(parts, pickerContent)
	}
	parts = append(parts, separator, inputView, "", footer)
	mainBody := lipgloss.JoinVertical(lipgloss.Left, parts...)

	// 7. 应用外边距，去除 styleApp 底部 padding 的尾随换行
	mainContent := styleApp.Render(mainBody)
	mainContent = strings.TrimRight(mainContent, "\n")

	v := tea.NewView(mainContent)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
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
	indicatorWidth := lipgloss.Width(indicator)
	modelPart := indicator + styleFooterModel.Render(m.hudModel)
	ctxPart := m.renderCtxBarCompact()

	line1Parts := []string{modelPart, ctxPart}
	line1 := styleFooter.Width(contentWidth).Render(strings.Join(line1Parts, sep))

	// Line 2: cache + turns + messages + latency + balance（前导空格对齐 line1 spinner 缩进）
	compactingPart := m.renderCacheRate()
	turnsPart := styleFooterLabel.Render("Loop") + " " + styleFooterValue.Render(fmt.Sprintf("%d", m.hudTurns))
	messagesPart := styleFooterLabel.Render("M") + " " + styleFooterValue.Render(fmt.Sprintf("%d", m.hudMessages))
	latPart := m.renderLatency()
	balancePart := m.renderBalance()

	line2Parts := []string{compactingPart, turnsPart, messagesPart, latPart, balancePart}
	line2Content := strings.Repeat(" ", indicatorWidth) + strings.Join(line2Parts, sep)
	line2 := styleFooter.Width(contentWidth).Render(line2Content)

	// Line 3: 快捷键提示（bubbles/help 自动按宽度省略）
	// 权限面板活跃时面板内已有快捷键提示，footer 不重复显示
	m.help.SetWidth(contentWidth)
	var helpKeys []key.Binding
	switch {
	case m.overlay == overlayPermission:
		helpKeys = nil // 面板内已有 permKeys，不重复
	default:
		helpKeys = []key.Binding{
			m.keys.Enter, m.keys.Picker, m.keys.Interrupt,
			m.keys.ToggleTool, m.keys.ToggleThought, m.keys.ToggleTheme, m.keys.Quit,
		}
	}
	line3 := styleFooter.Width(contentWidth).Render(m.help.ShortHelpView(helpKeys))

	return line1 + "\n" + line2 + "\n" + line3
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

	var pctStyle lipgloss.Style
	switch {
	case pct < 50:
		pctStyle = styleCtxBarGreenFg
	case pct < 80:
		pctStyle = styleCtxBarGoldFg
	default:
		pctStyle = styleCtxBarRedFg
	}

	pctStr := pctStyle.Render(fmt.Sprintf("%.1f%% · %s/%s", pct,
		formatTokens(currentTokens), formatTokens(m.contextLimit)))

	return styleFooterLabel.Render("ctx") + " " + barStr + " " + pctStr
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
	case pct > 50:
		valStyle = styleCacheGreen
	case pct >= 25:
		valStyle = styleCacheGold
	default:
		valStyle = styleFooterValue
	}

	return label + " " + valStyle.Render(fmt.Sprintf("%d%%", pct))
}

// renderLatency 渲染延迟。
func (m *model) renderLatency() string {
	label := styleFooterLabel.Render("elap")

	// 运行中显示实时耗时
	if m.running {
		if m.turnStartTime.IsZero() {
			return label + " " + styleFooterValueMuted.Render("--")
		}
		elapsed := time.Since(m.turnStartTime).Milliseconds()
		return label + " " + styleFooterValue.Render(formatDuration(elapsed))
	}

	if m.hudLatMs == 0 {
		return label + " " + styleFooterValueMuted.Render("--")
	}

	var valStyle lipgloss.Style
	switch {
	case m.hudLatMs < 500:
		valStyle = styleFooterLatGreen
	case m.hudLatMs < 2000:
		valStyle = styleFooterLatGold
	default:
		valStyle = styleFooterLatRed
	}

	return label + " " + valStyle.Render(formatDuration(m.hudLatMs))
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
		if normalized, _ := tool.NormalizeShellCommand(argsSummary); normalized != "" {
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
		if lipgloss.HasDarkBackground(os.Stdin, os.Stdout) {
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
		if lipgloss.HasDarkBackground(os.Stdin, os.Stdout) {
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
	isDark := m.themeMode == "dark" || (m.themeMode == "auto" && lipgloss.HasDarkBackground(os.Stdin, os.Stdout))
	m.help.Styles = help.DefaultStyles(isDark)

	// 同步 input 组件样式 —— 提示符与用户消息前缀联动，placeholder 使用 muted 色
	inputStyles := m.input.Styles()
	inputStyles.Focused.Prompt = lipgloss.NewStyle().Foreground(colorUser).Bold(true)
	inputStyles.Blurred.Prompt = lipgloss.NewStyle().Foreground(colorUser).Bold(true)
	inputStyles.Focused.Placeholder = lipgloss.NewStyle().Foreground(colorMuted)
	inputStyles.Blurred.Placeholder = lipgloss.NewStyle().Foreground(colorMuted)
	m.input.SetStyles(inputStyles)

	// 同步 permList delegate 样式（若已构建）
	if m.permDelegate != nil {
		m.permDelegate.Styles = listItemStyles()
		m.permList.SetDelegate(m.permDelegate)
	}

	// 同步 pickerList delegate 样式（若已构建）
	if m.pickerDelegate != nil {
		m.pickerDelegate.Styles = listItemStyles()
		m.pickerList.SetDelegate(m.pickerDelegate)
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
	m.cachedViewportLines = nil

	// 从段落列表重建 viewport，否则 View() 中发现缓存为空会返回空白行。
	m.setViewportContent(false)
}

// runTUI 启动交互式 TUI 模式。依赖由 main() 统一初始化后传入，无需重复创建。
func runTUI(llmClient llm.Client, registry tool.Registry, guard permission.Guard, expander *reference.Expander, modelName string, theme string, verboseLog io.Writer, contextLimit int, ctxMgr *ctxpkg.ContextManager) {
	m := newTUIModel(llmClient, registry, guard, expander, modelName, theme, verboseLog, contextLimit)
	// 用外部创建的 ContextManager 替换 newTUIModel 内部创建的
	m.cm = ctxMgr
	// 恢复会话级 HUD 累积值
	m.hudCacheHit = ctxMgr.Stats().TotalCacheHitTokens
	m.hudCacheMiss = ctxMgr.Stats().TotalCacheMissTokens
	// ctx bar 初始为 0，首个 TurnStats 会用 API 精确值更新
	m.lastPromptTokens = 0
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
		fmt.Fprintf(os.Stderr, "session: %s\n", sid)
		fmt.Fprintf(os.Stderr, "  wvl --resume %s\n", sid)
	}
}
