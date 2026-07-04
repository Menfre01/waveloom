package main

import (
	"os"
	"strings"
)

// ---------------------------------------------------------------------------
// Locale 类型
// ---------------------------------------------------------------------------

// Locale 表示界面语言。
type Locale string

const (
	LocaleZhCN Locale = "zh-CN"
	LocaleEnUS Locale = "en-US"
)

// ---------------------------------------------------------------------------
// Messages — 所有可翻译的 TUI 文案
// ---------------------------------------------------------------------------

// Messages 聚合所有 TUI 界面文案。通过 Locale 索引获取对应语言的实例。
// 命名规范：按功能区域分组，用点号连接（如 Input.Placeholder）。
// 带 % 格式化动词的字段保留 Go fmt 占位符，调用方负责 Sprintf。
type Messages struct {
	// ── Input ──────────────────────────────────────────────
	InputPlaceholder          string
	InputOtherPlaceholder     string
	InputAgentRunning         string
	InputFocusModePlaceholder string
	InputPlanModePlaceholder  string

	// ── System notifications ──────────────────────────────
	SysCompactionDone    string
	SysContextHardLimit  string
	SysSummaryFailed     string
	SysNewSessionCreated string
	SysUnknownCommand    string // 含 %s
	SysCommandFailed     string // 含 %v
	SysUpdateFailed      string
	SysUpdateInstalled   string // 含 %s
	SysSkillActivated    string // 含 %s
	SysSkillLoadFailed   string // 含 %s, %s

	// ── Loop done ─────────────────────────────────────────
	LoopCompleted   string // 含 %d, %s, %s, %s
	LoopMaxTurns    string // 含 %d, %s, %s, %s
	LoopAborted     string // 含 %s
	LoopToolTimeout string // 含 %s, %s, %s
	LoopModelError  string // 含 %s, %v
	LoopToolFatal   string // 含 %s, %v

	// ── Update ────────────────────────────────────────────
	UpdateAvailable string // 含 %s

	// ── Thought ───────────────────────────────────────────
	ThoughtThinking     string
	ThoughtComplete     string // 含 %d
	ThoughtExpandHint   string // 含 %d
	ThoughtCollapseHint string

	// ── Tool ──────────────────────────────────────────────
	ToolNotFound         string
	ToolNoInfo           string
	ToolNQuestions       string // 含 %d
	ToolQuestionDeclined string
	ToolTruncated        string
	ToolTruncatedLines   string // 含 %d
	ToolExpandAllHint    string
	ToolCollapseHint     string

	// ── Permission overlay ───────────────────────────────
	PermRequired string
	PermReason   string
	PermAllow    string
	PermAllowAll string
	PermDeny     string

	// ── Question overlay ─────────────────────────────────
	QuestionOtherOption string

	// ── Theme / Model picker ─────────────────────────────
	PickerSelectTheme  string
	PickerSelectModel  string
	PickerSelectLocale string
	PickerThemeAuto    string

	// ── Key bindings ─────────────────────────────────────
	KeyNav         string
	KeyConfirm     string
	KeyDeny        string
	KeyCancel      string
	KeyToggle      string
	KeySend        string
	KeyInterrupt   string
	KeyQuit        string
	KeyFocusNext   string
	KeyFocusPrev   string
	KeyScrollUp    string
	KeyScrollDown  string
	KeyPageUp      string
	KeyPageDown    string
	KeyToggleTheme string
	KeyJumpBottom  string
	KeyPicker      string
	KeyPaste       string

	// ── Focus separator ──────────────────────────────────
	FocusSeparatorHint string

	// ── Plan mode ─────────────────────────────────────────
	PlanEnterTitle   string
	PlanEnterDesc1   string
	PlanEnterDesc2   string
	PlanEnterConfirm string
	PlanEnterCancel  string
	PlanExitTitle    string
	PlanExitApprove  string
	PlanExitReject   string

	// ── Header ───────────────────────────────────────────
	HeaderSession string

	// ── Footer (labels only; values are computed) ────────
	FooterCtx   string
	FooterCache string
	FooterLoop  string
	FooterM     string
	FooterElap  string
	FooterBal   string

	// ── Setup wizard ─────────────────────────────────────
	SetupTitle           string
	SetupOverwriteWarn   string
	SetupStepLocale      string
	SetupStepProvider    string
	SetupStepAPIKey      string
	SetupStepModel       string
	SetupStepTheme       string
	SetupLocalePrompt    string
	SetupProviderPrompt  string
	SetupAPIKeyPrompt    string
	SetupAPIKeyEmptyWarn string
	SetupModelPrompt     string // 含 %s
	SetupThemePrompt     string
	SetupDoneTitle       string
	SetupDoneConfigSaved string
	SetupDoneReady       string
	SetupConfirmTitle    string
	SetupConfirmPrompt   string
	SetupConfirmRedo     string // 含 %d
	SetupHelpHint        string
	SetupSummaryTheme    string
	SetupSummaryLanguage string
	SetupSummaryProvider string
	SetupSummaryModel    string
	SetupSummaryAPIKey   string
	SetupConfirmSave     string
	SetupConfirmBack     string

	// ── Slash commands ────────────────────────────────────
	SlashNewDescription        string
	SlashNewCreated            string
	SlashNewFailed             string // 含 %v
	SlashModelDescription      string
	SlashModelListFailed       string // 含 %v
	SlashModelListFailedNoNet  string
	SlashModelUnknown          string // 含 %s
	SlashModelConfigReadFailed string // 含 %v
	SlashModelConfigSaveFailed string // 含 %v
	SlashModelSwitched         string // 含 %s
	SlashThemeDescription      string
	SlashLocaleDescription     string
	SlashHelpDescription       string
	SlashHelpText              string

	// ── CLI help ──────────────────────────────────────────
	HelpUsageText string

	// ── CLI stdout (--continue / --resume / ls / oneshot) ──
	CLINoAPIKeySetupHint    string
	CLIContinueSession      string // 含 %s
	CLINoRecentSession      string
	CLIResumedSession       string // 含 %s
	CLIDefaultConfigCreated string // 含 %s
	CLISetupHint            string
	CLILsNoRecent           string
	CLILsHeader             string
	CLILsRestoreHint        string

	// ── One-shot mode ──────────────────────────────────────
	OneShotHeader string // 含 %s, %s
	OneShotError  string // 含 %v

	// ── Session save on exit ──────────────────────────────
	SessionSaved      string // 含 %s
	SessionResumeHint string // 含 %s

	// ── Setup locale options ──────────────────────────────
	SetupLocaleZhCNLabel string
	SetupLocaleEnUSLabel string
}

// ---------------------------------------------------------------------------
// 语言实例
// ---------------------------------------------------------------------------

var zhCN = Messages{
	// Input
	InputPlaceholder:          "输入消息, Enter 发送 · / 命令 · @ 选择文件 · Esc 中断",
	InputOtherPlaceholder:     "输入自定义答案...",
	InputAgentRunning:         "Agent 执行中... Esc 中断",
	InputFocusModePlaceholder: "段落已聚焦 · Enter 展开/折叠 · Esc 回到输入",
	InputPlanModePlaceholder:  "[Plan] 输入消息, Enter 发送 · Shift+Tab 退出",

	// System
	SysCompactionDone:    "压缩完成。",
	SysContextHardLimit:  "上下文已满（98%）。/reset 重建。",
	SysSummaryFailed:     "摘要连续失败。/reset 重建。",
	SysNewSessionCreated: "新 session 已创建。",
	SysUnknownCommand:    "未知命令: %s。输入框输入 / 查看可用命令。",
	SysCommandFailed:     "命令执行失败: %v",
	SysUpdateFailed:      "更新失败。你可以重新打开 waveloom 后重试，或手动运行 install.sh。",
	SysUpdateInstalled:   "✓ %s 已安装，重启后生效。",
	SysSkillActivated:    "已激活 Skills: %s",
	SysSkillLoadFailed:   "Skill 加载失败: %s — %s",

	// Loop
	LoopCompleted:   "完成（%d轮, %s, ↑%s, ↓%s）",
	LoopMaxTurns:    "已达最大轮次（%d轮, %s, ↑%s, ↓%s）。继续对话。",
	LoopAborted:     "已中断（%s）",
	LoopToolTimeout: "工具执行超时（%s %s）%s",
	LoopModelError:  "模型错误（%s, %v）",
	LoopToolFatal:   "工具错误（%s, %v）",

	// Update
	UpdateAvailable: "↑ %s  enter 更新 • esc 忽略",

	// Thought
	ThoughtThinking:     "思考中...",
	ThoughtComplete:     "▶ 思考完成 (%d tokens) · Enter 展开",
	ThoughtExpandHint:   "··· Enter 展开 (%d tokens)",
	ThoughtCollapseHint: "▼ Enter 折叠",

	// Tool
	ToolNotFound:         "(未找到)",
	ToolNoInfo:           "(无信息)",
	ToolNQuestions:       "(%d 问)",
	ToolQuestionDeclined: "(declined)",
	ToolTruncated:        "··· (truncated)",
	ToolTruncatedLines:   "... (truncated to %d lines)",
	ToolExpandAllHint:    "··· Enter 展开全部",
	ToolCollapseHint:     "▼ Enter 折叠",

	// Permission
	PermRequired: "▲ Permission Required",
	PermReason:   "Reason: ",
	PermAllow:    "Allow (本次放行)",
	PermAllowAll: "Always Allow (记住，不再询问)",
	PermDeny:     "Deny (本次拒绝)",

	// Question
	QuestionOtherOption: "Other...",

	// Picker
	PickerSelectTheme:  "▲ 选择主题",
	PickerSelectModel:  "▲ 选择模型",
	PickerSelectLocale: "▲ 选择界面语言",
	PickerThemeAuto:    "Auto（自动检测终端背景色）",

	// Key bindings
	KeyNav:         "导航",
	KeyConfirm:     "确认",
	KeyDeny:        "拒绝",
	KeyCancel:      "取消",
	KeyToggle:      "勾选",
	KeySend:        "发送消息",
	KeyInterrupt:   "中断 agent loop",
	KeyQuit:        "退出",
	KeyFocusNext:   "聚焦下一个可交互段落",
	KeyFocusPrev:   "聚焦上一个可交互段落",
	KeyScrollUp:    "向上滚动",
	KeyScrollDown:  "向下滚动",
	KeyPageUp:      "向上翻页",
	KeyPageDown:    "向下翻页",
	KeyToggleTheme: "切换主题 (dark/light/auto)",
	KeyJumpBottom:  "跳到底部",
	KeyPicker:      "选择文件/目录",
	KeyPaste:       "粘贴",

	// Focus separator
	FocusSeparatorHint: " ◆ 段落已聚焦 · Enter 展开/折叠 · Esc 退出 ◆ ",

	// Plan mode
	PlanEnterTitle:   "进入 Plan 模式？",
	PlanEnterDesc1:   "Agent 将探索代码库并设计实现方案，",
	PlanEnterDesc2:   "期间无法编辑源文件，方案完成后需你审批。",
	PlanEnterConfirm: "确认",
	PlanEnterCancel:  "取消",
	PlanExitTitle:    "Plan 审批",
	PlanExitApprove:  "批准",
	PlanExitReject:   "拒绝，继续修改",

	// Header
	HeaderSession: "session: ",

	// Footer labels
	FooterCtx:   "ctx",
	FooterCache: "cache",
	FooterLoop:  "Loop",
	FooterM:     "M",
	FooterElap:  "elap",
	FooterBal:   "bal",

	// Setup wizard
	SetupTitle:           "Waveloom · 首次设置",
	SetupOverwriteWarn:   "继续操作将覆盖当前的 api_key。",
	SetupStepLocale:      "Step %d/%d — 界面语言",
	SetupStepProvider:    "Step %d/%d — 选择 Provider",
	SetupStepAPIKey:      "Step %d/%d — API Key",
	SetupStepModel:       "Step %d/%d — 模型名称",
	SetupStepTheme:       "Step %d/%d — 主题",
	SetupLocalePrompt:    "请输入数字 (1-2) [默认: 1]: ",
	SetupProviderPrompt:  "请输入数字 (1-2) [默认: 1]: ",
	SetupAPIKeyPrompt:    "请输入 API Key: ",
	SetupAPIKeyEmptyWarn: "⚠️  API Key 不能为空。你可以之后设置 LLM_API_KEY 环境变量再运行 waveloom setup。",
	SetupModelPrompt:     "输入模型名 [默认: %s]: ",
	SetupThemePrompt:     "请输入数字 (1-3) [默认: 1]: ",
	SetupDoneTitle:       "设置完成！",
	SetupDoneConfigSaved: "配置已保存到 %s",
	SetupDoneReady:       "现在可以运行 waveloom 进入交互模式了。",
	SetupConfirmTitle:    "确认配置",
	SetupConfirmPrompt:   "确认以上配置？",
	SetupConfirmRedo:     "重新设置 Step %d",
	SetupHelpHint:        "↑↓ 导航   Enter 确认   Esc 回退   Ctrl+C 退出",
	SetupSummaryTheme:    "主题",
	SetupSummaryLanguage: "语言",
	SetupSummaryProvider: "Provider",
	SetupSummaryModel:    "模型",
	SetupSummaryAPIKey:   "API Key",
	SetupConfirmSave:     "Save  — 确认保存",
	SetupConfirmBack:     "Back  — 回退修改",

	// Slash commands
	SlashNewDescription:        "创建全新 session",
	SlashNewCreated:            "新 session 已创建。",
	SlashNewFailed:             "创建新 session 失败: %v",
	SlashModelDescription:      "显示或切换模型",
	SlashModelListFailed:       "无法获取模型列表: %v",
	SlashModelListFailedNoNet:  "无法获取模型列表，请检查网络连接后重试。",
	SlashModelUnknown:          "未知模型: %s。输入 /model 查看可用列表。",
	SlashModelConfigReadFailed: "读取配置失败: %v",
	SlashModelConfigSaveFailed: "保存配置失败: %v",
	SlashModelSwitched:         "模型已切换为 %s。",
	SlashThemeDescription:      "选择主题（Auto / Dark / Light）",
	SlashLocaleDescription:     "切换语言（zh-CN / en-US）",
	SlashHelpDescription:       "显示所有可用命令",
	SlashHelpText: `使用技巧:

  —— 以下仅在空闲时生效 ——
  输入 /         查看并补全命令（↑↓ 导航，Enter 确认，Tab 自动补全）
  输入 @         引用文件（↑↓ 导航，Enter 确认，Tab 深入目录）
  ↑↓              浏览输入历史
  Tab / Shift+Tab 段落间导航，Enter 展开 / 折叠
  Esc（双击）      清空输入框
  exit            退出程序

  可用命令：在输入框输入 / 即可弹出命令列表。

  —— 以下任意时刻生效 ——
  Ctrl+G          循环切换主题（dark → light → auto）
  Ctrl+E / End    跳到底部
  Ctrl+C          退出
  PgUp / PgDn     上下翻页
  Esc（运行中）     中断当前 Agent 执行

  会话结束时 session 自动保存，使用 waveloom --continue 恢复最近会话。
  单次执行：waveloom "解释这段代码"`,

	// CLI help
	HelpUsageText: `Waveloom — Code Agent CLI

用法:
  waveloom                     交互式 TUI 模式
  waveloom ls                  列出最近 sessions
  waveloom setup               首次设置向导
  waveloom completion <shell>  输出 shell 补全脚本 (bash/zsh/fish)
  waveloom "prompt"            单次执行模式
  waveloom --help              显示帮助
  waveloom --version           显示版本号

选项:
  --settings PATH         配置文件路径（项目级；全局 ~/.waveloom/settings.json 自动合并）
  --version               显示版本号
  --model NAME            LLM 模型名称
  --theme MODE            主题模式: auto（默认）/ dark / light
                          auto 自动检测终端背景色
  --locale LANG           界面语言: auto（默认）/ zh-CN / en-US
                          auto 从 LANG 环境变量自动检测
  --verbose               记录 LLM 调用和工具执行日志到 .waveloom/waveloom.log
  --max-turns N           最大 turn 数（0=无限制）
  --system-prompt TEXT    系统提示词
  --context-limit N       上下文窗口 token 上限，支持 1M / 200k / 1048576 等格式（默认: 1M）
  --bypass-permissions    跳过权限检查（CI/测试）
  --tool-timeout D         单个工具执行超时（Go Duration 格式，如 10m / 600s / 0s，0 禁用，默认 10m）
  --resume ID             恢复指定 session ID 的对话
  --continue              恢复最近一个 session 的对话

配置文件（settings.json）:
  ~/.waveloom/settings.json  用户全局配置（安全基线）
  .waveloom/settings.json    项目级配置（字段覆盖全局，权限同键覆盖全局）
  --settings PATH            显式指定项目配置文件

  llm.api_key              API Key（必填；为空时回退 LLM_API_KEY 环境变量）
  llm.provider              Provider（openai / deepseek）
  llm.model                 模型名称
  llm.base_url              API 端点
  llm.timeout               请求超时（如 "600s"）
  llm.extra_params          额外参数（如 temperature, max_tokens, thinking 等）

  permissions.allow[]       直接允许的规则
  permissions.deny[]        直接拒绝的规则
  permissions.ask[]         需用户确认的规则
                           格式: "tool_name" 或 "tool_name(pattern)"

环境变量:
  LLM_API_KEY             API Key（settings.json 未设置时的回退）
`,

	// CLI stdout
	CLINoAPIKeySetupHint:    "\n  请运行 waveloom setup 完成首次配置，或设置 LLM_API_KEY 环境变量。\n",
	CLIContinueSession:      "继续最近 session: %s\n",
	CLINoRecentSession:      "没有找到最近的 session，将创建新 session\n",
	CLIResumedSession:       "已恢复 session: %s\n",
	CLIDefaultConfigCreated: "📝 已生成默认配置文件: %s\n",
	CLISetupHint:            "   💡 运行 waveloom setup 完成首次配置，或设置 LLM_API_KEY 环境变量\n",
	CLILsNoRecent:           "没有找到最近的 session。",
	CLILsHeader:             "最近 sessions:",
	CLILsRestoreHint:        "恢复: waveloom --resume <id>  或  waveloom --continue",

	// One-shot mode
	OneShotHeader: "🤖 Waveloom (单次模式) — %s — %s\n\n",
	OneShotError:  "❌ 错误: %v\n",

	// Session save
	SessionSaved:      "已保存 session: %s\n",
	SessionResumeHint: "  恢复对话: waveloom --resume %s\n",

	// Setup locale options
	SetupLocaleZhCNLabel: "简体中文  (zh-CN)",
	SetupLocaleEnUSLabel: "English   (en-US)",
}

var enUS = Messages{
	InputPlaceholder:          "Type a message, Enter to send · / commands · @ pick files · Esc to interrupt",
	InputOtherPlaceholder:     "Type custom answer...",
	InputAgentRunning:         "Agent running... Esc to interrupt",
	InputFocusModePlaceholder: "Paragraph focused · Enter expand/collapse · Esc back to input",
	InputPlanModePlaceholder:  "[Plan] Type a message, Enter to send · Shift+Tab to exit",

	// System
	SysCompactionDone:    "Compaction complete.",
	SysContextHardLimit:  "Context full (98%). /reset to rebuild.",
	SysSummaryFailed:     "Summary failed repeatedly. /reset to rebuild.",
	SysNewSessionCreated: "New session created.",
	SysUnknownCommand:    "Unknown command: %s. Type /help to see available commands.",
	SysCommandFailed:     "Command failed: %v",
	SysUpdateFailed:      "Update failed. Reopen waveloom to retry, or run install.sh manually.",
	SysUpdateInstalled:   "✓ %s installed, restart to take effect.",
	SysSkillActivated:    "Skills activated: %s",
	SysSkillLoadFailed:   "Skill load failed: %s — %s",

	// Loop
	LoopCompleted:   "Done (%d turns, %s, ↑%s, ↓%s)",
	LoopMaxTurns:    "Max turns reached (%d turns, %s, ↑%s, ↓%s). Continue.",
	LoopAborted:     "Aborted (%s)",
	LoopToolTimeout: "Tool timeout (%s %s) %s",
	LoopModelError:  "Model error (%s, %v)",
	LoopToolFatal:   "Tool error (%s, %v)",

	// Update
	UpdateAvailable: "↑ %s  enter update • esc dismiss",

	// Thought
	ThoughtThinking:     "Thinking...",
	ThoughtComplete:     "▶ Thinking done (%d tokens) · Enter to expand",
	ThoughtExpandHint:   "··· Enter to expand (%d tokens)",
	ThoughtCollapseHint: "▼ Enter to collapse",

	// Tool
	ToolNotFound:         "(not found)",
	ToolNoInfo:           "(no info)",
	ToolNQuestions:       "(%d questions)",
	ToolQuestionDeclined: "(declined)",
	ToolTruncated:        "··· (truncated)",
	ToolTruncatedLines:   "... (truncated to %d lines)",
	ToolExpandAllHint:    "··· Enter to expand all",
	ToolCollapseHint:     "▼ Enter to collapse",

	// Permission
	PermRequired: "▲ Permission Required",
	PermReason:   "Reason: ",
	PermAllow:    "Allow (this time)",
	PermAllowAll: "Always Allow (remember)",
	PermDeny:     "Deny (this time)",

	// Question
	QuestionOtherOption: "Other...",

	// Picker
	PickerSelectTheme:  "▲ Select Theme",
	PickerSelectModel:  "▲ Select Model",
	PickerSelectLocale: "▲ Select Language",
	PickerThemeAuto:    "Auto (detect terminal background)",

	// Key bindings
	KeyNav:         "Navigate",
	KeyConfirm:     "Confirm",
	KeyDeny:        "Deny",
	KeyCancel:      "Cancel",
	KeyToggle:      "Toggle",
	KeySend:        "Send message",
	KeyInterrupt:   "Interrupt agent loop",
	KeyQuit:        "Quit",
	KeyFocusNext:   "Focus next interactive paragraph",
	KeyFocusPrev:   "Focus previous interactive paragraph",
	KeyScrollUp:    "Scroll up",
	KeyScrollDown:  "Scroll down",
	KeyPageUp:      "Page up",
	KeyPageDown:    "Page down",
	KeyToggleTheme: "Toggle theme (dark/light/auto)",
	KeyJumpBottom:  "Jump to bottom",
	KeyPicker:      "Pick file/directory",
	KeyPaste:       "Paste",

	// Focus separator
	FocusSeparatorHint: " ◆ Paragraph focused · Enter expand/collapse · Esc exit ◆ ",

	// Plan mode
	PlanEnterTitle:   "Enter plan mode?",
	PlanEnterDesc1:   "Agent will explore the codebase and design an approach,",
	PlanEnterDesc2:   "source edits are blocked until you approve the plan.",
	PlanEnterConfirm: "Confirm",
	PlanEnterCancel:  "Cancel",
	PlanExitTitle:    "Plan Approval",
	PlanExitApprove:  "Approve",
	PlanExitReject:   "Reject, continue editing",

	// Header
	HeaderSession: "session: ",

	// Footer labels
	FooterCtx:   "ctx",
	FooterCache: "cache",
	FooterLoop:  "Loop",
	FooterM:     "M",
	FooterElap:  "elap",
	FooterBal:   "bal",

	// Setup wizard
	SetupTitle:           "Waveloom · First-time Setup",
	SetupOverwriteWarn:   "This will overwrite the existing api_key.",
	SetupStepLocale:      "Step %d/%d — Language",
	SetupStepProvider:    "Step %d/%d — Select Provider",
	SetupStepAPIKey:      "Step %d/%d — API Key",
	SetupStepModel:       "Step %d/%d — Model Name",
	SetupStepTheme:       "Step %d/%d — Theme",
	SetupLocalePrompt:    "Enter number (1-2) [default: 1]: ",
	SetupProviderPrompt:  "Enter number (1-2) [default: 1]: ",
	SetupAPIKeyPrompt:    "Enter API Key: ",
	SetupAPIKeyEmptyWarn: "⚠️  API Key cannot be empty. You can set LLM_API_KEY environment variable and run waveloom setup again.",
	SetupModelPrompt:     "Enter model name [default: %s]: ",
	SetupThemePrompt:     "Enter number (1-3) [default: 1]: ",
	SetupDoneTitle:       "Setup Complete!",
	SetupDoneConfigSaved: "Config saved to %s",
	SetupDoneReady:       "You can now run waveloom to start the interactive mode.",
	SetupConfirmTitle:    "Confirm Settings",
	SetupConfirmPrompt:   "Confirm the settings above?",
	SetupConfirmRedo:     "Redo Step %d",
	SetupHelpHint:        "↑↓ navigate   Enter confirm   Esc back   Ctrl+C quit",
	SetupSummaryTheme:    "Theme",
	SetupSummaryLanguage: "Language",
	SetupSummaryProvider: "Provider",
	SetupSummaryModel:    "Model",
	SetupSummaryAPIKey:   "API Key",
	SetupConfirmSave:     "Save",
	SetupConfirmBack:     "Back",

	// Slash commands
	SlashNewDescription:        "Create new session",
	SlashNewCreated:            "New session created.",
	SlashNewFailed:             "Failed to create session: %v",
	SlashModelDescription:      "Show or switch model",
	SlashModelListFailed:       "Unable to fetch model list: %v",
	SlashModelListFailedNoNet:  "Unable to fetch model list. Please check your network and retry.",
	SlashModelUnknown:          "Unknown model: %s. Type /model to see available models.",
	SlashModelConfigReadFailed: "Failed to read config: %v",
	SlashModelConfigSaveFailed: "Failed to save config: %v",
	SlashModelSwitched:         "Model switched to %s.",
	SlashThemeDescription:      "Select theme (Auto / Dark / Light)",
	SlashLocaleDescription:     "Switch language (zh-CN / en-US)",
	SlashHelpDescription:       "Show all available commands",
	SlashHelpText: `Usage tips:

  —— Idle only ——
  Type /         View and complete commands (↑↓ navigate, Enter confirm, Tab autocomplete)
  Type @         Reference files (↑↓ navigate, Enter confirm, Tab dive into directories)
  ↑↓              Browse input history
  Tab / Shift+Tab Navigate between paragraphs, Enter expand / collapse
  Esc (double)    Clear input
  exit            Exit program

  Available commands: type / in the input to see the command list.

  —— Anytime ——
  Ctrl+G          Cycle theme (dark → light → auto)
  Ctrl+E / End    Jump to bottom
  Ctrl+C          Quit
  PgUp / PgDn     Page up / down
  Esc (running)   Interrupt current agent execution

  Sessions are auto-saved on exit. Use waveloom --continue to resume.`,

	// CLI help
	HelpUsageText: `Waveloom — Code Agent CLI

Usage:
  waveloom                     Interactive TUI mode
  waveloom ls                  List recent sessions
  waveloom setup               First-time setup wizard
  waveloom completion <shell>  Output shell completion script (bash/zsh/fish)
  waveloom "prompt"            Single-shot execution mode
  waveloom --help              Show help
  waveloom --version           Show version

Options:
  --settings PATH         Config file path (project-level; global ~/.waveloom/settings.json auto-merged)
  --version               Show version
  --model NAME            LLM model name
  --theme MODE            Theme mode: auto (default) / dark / light
                          auto detects terminal background color
  --locale LANG           Interface language: auto (default) / zh-CN / en-US
                          auto detects from LANG environment variable
  --verbose               Log LLM calls and tool execution to .waveloom/waveloom.log
  --max-turns N           Max turns (0=unlimited)
  --system-prompt TEXT    System prompt
  --context-limit N       Context window token limit, supports 1M / 200k / 1048576 etc. (default: 1M)
  --bypass-permissions    Skip permission checks (CI/testing)
  --tool-timeout D         Single tool execution timeout (Go Duration format, e.g. 10m / 600s / 0s, 0 disables, default 10m)
  --resume ID             Resume session by ID
  --continue              Resume the most recent session

Configuration (settings.json):
  ~/.waveloom/settings.json  Global user config (security baseline)
  .waveloom/settings.json    Project-level config (fields override global, permissions replace by key)
  --settings PATH            Explicitly specify project config file

  llm.api_key              API key (required; falls back to LLM_API_KEY env var)
  llm.provider              Provider (openai / deepseek)
  llm.model                 Model name
  llm.base_url              API endpoint
  llm.timeout               Request timeout (e.g. "600s")
  llm.extra_params          Extra parameters (e.g. temperature, max_tokens, thinking)

  permissions.allow[]       Rules to allow directly
  permissions.deny[]        Rules to deny directly
  permissions.ask[]         Rules requiring user confirmation
                            Format: "tool_name" or "tool_name(pattern)"

Environment variables:
  LLM_API_KEY            API key (fallback when not set in settings.json)
`,

	// CLI stdout
	CLINoAPIKeySetupHint:    "\n  Run waveloom setup to complete initial configuration, or set LLM_API_KEY environment variable.\n",
	CLIContinueSession:      "Continuing most recent session: %s\n",
	CLINoRecentSession:      "No recent session found, creating new session\n",
	CLIResumedSession:       "Resumed session: %s\n",
	CLIDefaultConfigCreated: "Default config created: %s\n",
	CLISetupHint:            "   💡 Run waveloom setup to complete configuration, or set LLM_API_KEY\n",
	CLILsNoRecent:           "No recent sessions found.",
	CLILsHeader:             "Recent sessions:",
	CLILsRestoreHint:        "Restore: waveloom --resume <id>  or  waveloom --continue",

	// One-shot mode
	OneShotHeader: "🤖 Waveloom (oneshot) — %s — %s\n\n",
	OneShotError:  "❌ Error: %v\n",

	// Session save
	SessionSaved:      "Session saved: %s\n",
	SessionResumeHint: "  Resume: waveloom --resume %s\n",

	// Setup locale options
	SetupLocaleZhCNLabel: "简体中文  (zh-CN)",
	SetupLocaleEnUSLabel: "English   (en-US)",
}

// ---------------------------------------------------------------------------
// Locale 查询
// ---------------------------------------------------------------------------

// messagesFor 返回指定 locale 对应的 Messages 实例。
// 不支持的语言回退到 en-US。
func messagesFor(loc Locale) *Messages {
	switch loc {
	case LocaleZhCN:
		return &zhCN
	case LocaleEnUS:
		return &enUS
	default:
		return &enUS
	}
}

// ---------------------------------------------------------------------------
// 语言检测
// ---------------------------------------------------------------------------

// DetectLocale 从环境变量检测用户语言偏好。
// 优先级：LC_ALL > LANG > 默认 en-US。
// 仅识别 zh_CN / zh-CN / zh 系列为简体中文，其余回退英语。
func DetectLocale() Locale {
	for _, env := range []string{"LC_ALL", "LANG"} {
		val := os.Getenv(env)
		if val == "" {
			continue
		}
		normalized := strings.ToLower(strings.TrimSpace(val))
		// zh_CN.UTF-8 → zh_CN; zh-CN → zh-cn
		if strings.HasPrefix(normalized, "zh_cn") || strings.HasPrefix(normalized, "zh-cn") ||
			normalized == "zh" || strings.HasPrefix(normalized, "zh_") {
			return LocaleZhCN
		}
	}
	return LocaleEnUS
}

// resolveLocale 将 CLI --locale 参数解析为 Locale 值。
// "auto" → 自动检测，其余直接映射。
func resolveLocale(raw string) Locale {
	switch raw {
	case "zh-CN":
		return LocaleZhCN
	case "en-US":
		return LocaleEnUS
	case "auto", "":
		return DetectLocale()
	default:
		return DetectLocale()
	}
}
