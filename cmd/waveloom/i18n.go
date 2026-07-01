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

	// ── System notifications ──────────────────────────────
	SysCompactionDone     string
	SysContextHardLimit   string
	SysSummaryFailed      string
	SysNewSessionCreated  string
	SysUnknownCommand     string // 含 %s
	SysCommandFailed      string // 含 %v
	SysUpdateFailed       string
	SysUpdateInstalled    string // 含 %s
	SysSkillActivated     string // 含 %s
	SysSkillLoadFailed    string // 含 %s, %s

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
	ThoughtThinking      string
	ThoughtComplete      string // 含 %d
	ThoughtExpandHint    string // 含 %d
	ThoughtCollapseHint  string

	// ── Tool ──────────────────────────────────────────────
	ToolNotFound          string
	ToolNoInfo            string
	ToolNoHoverOutput     string
	ToolNDefinitions      string // 含 %d, %s
	ToolNReferences       string // 含 %d, %s
	ToolNQuestions        string // 含 %d
	ToolNDiagnostics      string // 含 %d, %d, %d, %d, %d, %s
	ToolQuestionDeclined  string
	ToolTruncated         string
	ToolTruncatedLines    string // 含 %d
	ToolExpandAllHint     string
	ToolCollapseHint      string

	// ── Permission overlay ───────────────────────────────
	PermRequired   string
	PermReason     string
	PermAllow      string
	PermAllowAll   string
	PermDeny       string

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
	SetupTitle         string
	SetupOverwriteWarn string
	SetupStepLocale    string
	SetupStepProvider  string
	SetupStepAPIKey    string
	SetupStepModel     string
	SetupStepTheme     string
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

	// System
	SysCompactionDone:    "压缩完成。",
	SysContextHardLimit:  "上下文已满（98%）。/reset 重建。",
	SysSummaryFailed:     "摘要连续失败。/reset 重建。",
	SysNewSessionCreated: "新 session 已创建。",
	SysUnknownCommand:    "未知命令: %s。输入 /help 查看可用命令。",
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
	UpdateAvailable: "↑ %s available — 空闲时 Enter 更新  Esc 忽略",

	// Thought
	ThoughtThinking:     "思考中...",
	ThoughtComplete:     "▶ 思考完成 (%d tokens) · Enter 展开",
	ThoughtExpandHint:   "··· Enter 展开 (%d tokens)",
	ThoughtCollapseHint: "▼ Enter 折叠",

	// Tool
	ToolNotFound:         "(未找到)",
	ToolNoInfo:           "(无信息)",
	ToolNoHoverOutput:    "无悬浮信息",
	ToolNDefinitions:     "(%d 个定义, %s)",
	ToolNReferences:      "(%d 个引用, %s)",
	ToolNQuestions:       "(%d 问)",
	ToolNDiagnostics:     "(%d 条: %dE %dW %dI %dH, %s)",
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
	FocusSeparatorHint: " ◆ 段落已聚焦  Enter 展开/折叠  Esc 退出焦点模式 ◆ ",

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
	SetupDoneConfigSaved: "配置已保存到 ~/.waveloom/settings.json",
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
}

var enUS = Messages{
	// Input
	InputPlaceholder:          "Type a message, Enter to send · / commands · @ pick files · Esc to interrupt",
	InputOtherPlaceholder:     "Type custom answer...",
	InputAgentRunning:         "Agent running... Esc to interrupt",
	InputFocusModePlaceholder: "Paragraph focused · Enter expand/collapse · Esc back to input",

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
	UpdateAvailable: "↑ %s available — when idle, Enter to update  Esc to dismiss",

	// Thought
	ThoughtThinking:     "Thinking...",
	ThoughtComplete:     "▶ Thinking done (%d tokens) · Enter to expand",
	ThoughtExpandHint:   "··· Enter to expand (%d tokens)",
	ThoughtCollapseHint: "▼ Enter to collapse",

	// Tool
	ToolNotFound:         "(not found)",
	ToolNoInfo:           "(no info)",
	ToolNoHoverOutput:    "No hover info",
	ToolNDefinitions:     "(%d definitions, %s)",
	ToolNReferences:      "(%d references, %s)",
	ToolNQuestions:       "(%d questions)",
	ToolNDiagnostics:     "(%d items: %dE %dW %dI %dH, %s)",
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
	FocusSeparatorHint: " ◆ Paragraph focused  Enter expand/collapse  Esc exit focus ◆ ",

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
	SetupDoneConfigSaved: "Config saved to ~/.waveloom/settings.json",
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
