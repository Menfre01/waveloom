package slashcommand

// SlashMessages 包含所有 slash 命令需要的可翻译文案。
// TUI 侧在构造命令时从当前 locale 的 Messages 填入，locale 切换时原地更新字段值。
type SlashMessages struct {
	// ── /new ──
	NewDescription string
	NewCreated     string
	NewFailed      string // 含 %v

	// ── /model ──
	ModelDescription      string
	ModelListFailed       string // 含 %v
	ModelListFailedNoNet  string
	ModelUnknown          string // 含 %s
	ModelConfigReadFailed string // 含 %v
	ModelConfigSaveFailed string // 含 %v
	ModelSwitched         string // 含 %s

	// ── /theme ──
	ThemeDescription string

	// ── /locale ──
	LocaleDescription string

	// ── /help ──
	HelpDescription string
	HelpText        string // /help 执行时返回的完整帮助文本
}
