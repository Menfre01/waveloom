package main

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// parseCLIForTest 在每次调用前重置 flag.CommandLine 并设置 os.Args。
func parseCLIForTest(args []string) CLIConfig {
	os.Args = append([]string{"waveloom"}, args...)
	return parseCLI()
}

// ---------------------------------------------------------------------------
// parseTokenLimit
// ---------------------------------------------------------------------------

func TestParseTokenLimit_Pure(t *testing.T) {
	v, err := parseTokenLimit("1048576")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 1048576 {
		t.Errorf("expected 1048576, got %d", v)
	}
}

func TestParseTokenLimit_Mega(t *testing.T) {
	v, err := parseTokenLimit("1M")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 1000000 {
		t.Errorf("expected 1000000, got %d", v)
	}
}

func TestParseTokenLimit_MegaLowercase(t *testing.T) {
	v, err := parseTokenLimit("2m")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 2000000 {
		t.Errorf("expected 2000000, got %d", v)
	}
}

func TestParseTokenLimit_Kilo(t *testing.T) {
	v, err := parseTokenLimit("200k")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 200000 {
		t.Errorf("expected 200000, got %d", v)
	}
}

func TestParseTokenLimit_KiloUppercase(t *testing.T) {
	v, err := parseTokenLimit("128K")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 128000 {
		t.Errorf("expected 128000, got %d", v)
	}
}

func TestParseTokenLimit_Empty(t *testing.T) {
	_, err := parseTokenLimit("")
	if err == nil {
		t.Fatal("expected error for empty value")
	}
}

func TestParseTokenLimit_Invalid(t *testing.T) {
	_, err := parseTokenLimit("abc")
	if err == nil {
		t.Fatal("expected error for invalid value")
	}
}

func TestParseTokenLimit_Negative(t *testing.T) {
	_, err := parseTokenLimit("-1")
	if err == nil {
		t.Fatal("expected error for negative value")
	}
}

func TestParseTokenLimit_Zero(t *testing.T) {
	_, err := parseTokenLimit("0")
	if err == nil {
		t.Fatal("expected error for zero")
	}
}

func TestParseTokenLimit_Whitespace(t *testing.T) {
	v, err := parseTokenLimit("  500k  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 500000 {
		t.Errorf("expected 500000, got %d", v)
	}
}

// ---------------------------------------------------------------------------
// resolveSettingsPaths
// ---------------------------------------------------------------------------

func TestResolveSettingsPaths_Default(t *testing.T) {
	globalPath, projectPath := resolveSettingsPaths("")
	if globalPath == "" {
		t.Error("globalPath should not be empty")
	}
	if !filepath.IsAbs(globalPath) {
		t.Errorf("globalPath should be absolute, got %q", globalPath)
	}
	if !filepath.IsAbs(projectPath) {
		t.Errorf("projectPath should be absolute, got %q", projectPath)
	}
}

func TestResolveSettingsPaths_Explicit(t *testing.T) {
	globalPath, projectPath := resolveSettingsPaths("/custom/settings.json")
	if !strings.HasSuffix(projectPath, filepath.FromSlash("/custom/settings.json")) {
		t.Errorf("expected path ending with %q, got %q", filepath.FromSlash("/custom/settings.json"), projectPath)
	}
	if globalPath == "" {
		t.Error("globalPath should not be empty")
	}
}

// ---------------------------------------------------------------------------
// buildSystemPrompt
// ---------------------------------------------------------------------------

func TestBuildSystemPrompt_ContainsCWD(t *testing.T) {
	prompt := buildSystemPrompt("/test/cwd", LocaleZhCN)
	if prompt == "" {
		t.Fatal("prompt should not be empty")
	}
	if !containsSubstr(prompt, "/test/cwd") {
		t.Error("prompt should contain /test/cwd")
	}
	if !containsSubstr(prompt, "Workspace") {
		t.Error("prompt should contain Workspace header")
	}
}

func TestBuildSystemPrompt_NonEmpty(t *testing.T) {
	prompt := buildSystemPrompt("/tmp", LocaleZhCN)
	if len(prompt) < 100 {
		t.Errorf("prompt too short: %d chars", len(prompt))
	}
}

func TestBuildSystemPrompt_LocaleZhCN_KeepsChineseInstruction(t *testing.T) {
	prompt := buildSystemPrompt("/tmp", LocaleZhCN)
	if !containsSubstr(prompt, "Communicate in Chinese when addressing the user") {
		t.Error("zh-CN prompt should contain Chinese communication instruction")
	}
}

func TestBuildSystemPrompt_LocaleEnUS_ReplacesWithEnglish(t *testing.T) {
	prompt := buildSystemPrompt("/tmp", LocaleEnUS)
	if containsSubstr(prompt, "Communicate in Chinese") {
		t.Error("en-US prompt should NOT contain Chinese communication instruction")
	}
	if !containsSubstr(prompt, "Communicate in English when addressing the user") {
		t.Error("en-US prompt should contain English communication instruction")
	}
}

func TestBuildSystemPrompt_LocaleSwap_ExactOneReplacement(t *testing.T) {
	originalLine := "Communicate in Chinese when addressing the user; keep English code and terminal output as-is."
	replacementLine := "Communicate in English when addressing the user."

	zhCN := buildSystemPrompt("/tmp", LocaleZhCN)
	enUS := buildSystemPrompt("/tmp", LocaleEnUS)

	if c := strings.Count(zhCN, originalLine); c != 1 {
		t.Errorf("zh-CN: expected 1 occurrence of original line, got %d", c)
	}
	if c := strings.Count(enUS, originalLine); c != 0 {
		t.Errorf("en-US: expected 0 occurrences of original line, got %d", c)
	}
	if c := strings.Count(enUS, replacementLine); c != 1 {
		t.Errorf("en-US: expected 1 occurrence of replacement line, got %d", c)
	}
}

// ---------------------------------------------------------------------------
// needsSetup — partial test (no API key = true, but don't assert hard)
// ---------------------------------------------------------------------------

func TestNeedsSetup_NoConfig(t *testing.T) {
	result := needsSetup()
	_ = result // 至少不应 panic
}

// ---------------------------------------------------------------------------
// parseCLI — 所有 flag 组合
// ---------------------------------------------------------------------------

func TestParseCLI_VersionFlag(t *testing.T) {
	cfg := parseCLIForTest([]string{"--version"})
	if !cfg.ShowVersion {
		t.Error("expected ShowVersion=true")
	}
}

func TestParseCLI_HelpFlag(t *testing.T) {
	cfg := parseCLIForTest([]string{"--help"})
	if !cfg.ShowHelp {
		t.Error("expected ShowHelp=true")
	}
}

func TestParseCLI_HelpShortFlag(t *testing.T) {
	cfg := parseCLIForTest([]string{"-h"})
	if !cfg.ShowHelp {
		t.Error("expected ShowHelp=true for -h")
	}
}

func TestParseCLI_SetupFlag(t *testing.T) {
	cfg := parseCLIForTest([]string{"--setup"})
	if !cfg.Setup {
		t.Error("expected Setup=true")
	}
}

func TestParseCLI_SetupSubcommand(t *testing.T) {
	cfg := parseCLIForTest([]string{"setup"})
	if !cfg.Setup {
		t.Error("expected Setup=true for 'setup' subcommand")
	}
}

func TestParseCLI_ListSessionsSubcommand(t *testing.T) {
	cfg := parseCLIForTest([]string{"ls"})
	if !cfg.ListSessions {
		t.Error("expected ListSessions=true for 'ls' subcommand")
	}
}

func TestParseCLI_CompletionBash(t *testing.T) {
	cfg := parseCLIForTest([]string{"completion", "bash"})
	if cfg.CompletionShell != "bash" {
		t.Errorf("expected CompletionShell='bash', got %q", cfg.CompletionShell)
	}
}

func TestParseCLI_CompletionZsh(t *testing.T) {
	cfg := parseCLIForTest([]string{"completion", "zsh"})
	if cfg.CompletionShell != "zsh" {
		t.Errorf("expected CompletionShell='zsh', got %q", cfg.CompletionShell)
	}
}

func TestParseCLI_CompletionFish(t *testing.T) {
	cfg := parseCLIForTest([]string{"completion", "fish"})
	if cfg.CompletionShell != "fish" {
		t.Errorf("expected CompletionShell='fish', got %q", cfg.CompletionShell)
	}
}

func TestParseCLI_OneShot(t *testing.T) {
	cfg := parseCLIForTest([]string{"explain main.go"})
	if cfg.OneShot != "explain main.go" {
		t.Errorf("expected OneShot='explain main.go', got %q", cfg.OneShot)
	}
}

func TestParseCLI_ModelFlag(t *testing.T) {
	cfg := parseCLIForTest([]string{"--model", "deepseek-v4-flash", "hello"})
	if cfg.Model != "deepseek-v4-flash" {
		t.Errorf("expected model 'deepseek-v4-flash', got %q", cfg.Model)
	}
	if cfg.OneShot != "hello" {
		t.Errorf("expected OneShot='hello', got %q", cfg.OneShot)
	}
}

func TestParseCLI_MaxTurns(t *testing.T) {
	cfg := parseCLIForTest([]string{"--max-turns", "5", "test"})
	if cfg.MaxTurns != 5 {
		t.Errorf("expected MaxTurns=5, got %d", cfg.MaxTurns)
	}
}

func TestParseCLI_BypassPermissions(t *testing.T) {
	cfg := parseCLIForTest([]string{"--bypass-permissions", "test"})
	if !cfg.BypassPerm {
		t.Error("expected BypassPerm=true")
	}
}

func TestParseCLI_LogLevelDefault(t *testing.T) {
	cfg := parseCLIForTest([]string{"test"})
	if cfg.LogLevel != "info" {
		t.Errorf("expected default LogLevel=info, got %q", cfg.LogLevel)
	}
}

func TestParseCLI_LogLevelDebug(t *testing.T) {
	cfg := parseCLIForTest([]string{"--log-level", "debug", "test"})
	if cfg.LogLevel != "debug" {
		t.Errorf("expected LogLevel=debug, got %q", cfg.LogLevel)
	}
}

func TestParseCLI_Continue(t *testing.T) {
	cfg := parseCLIForTest([]string{"--continue"})
	if !cfg.ContinueSession {
		t.Error("expected ContinueSession=true")
	}
}

func TestParseCLI_Resume(t *testing.T) {
	cfg := parseCLIForTest([]string{"--resume", "abc-123-def"})
	if cfg.ResumeSessionID != "abc-123-def" {
		t.Errorf("expected ResumeSessionID='abc-123-def', got %q", cfg.ResumeSessionID)
	}
}

func TestParseCLI_Name(t *testing.T) {
	cfg := parseCLIForTest([]string{"--name", "修复登录 bug"})
	if cfg.SessionName != "修复登录 bug" {
		t.Errorf("expected SessionName='修复登录 bug', got %q", cfg.SessionName)
	}
}

func TestParseCLI_NameDefaultEmpty(t *testing.T) {
	cfg := parseCLIForTest([]string{"test"})
	if cfg.SessionName != "" {
		t.Errorf("expected default SessionName='', got %q", cfg.SessionName)
	}
}

func TestParseCLI_ThemeDefault(t *testing.T) {
	cfg := parseCLIForTest([]string{"test"})
	if cfg.Theme != "auto" {
		t.Errorf("expected theme='auto', got %q", cfg.Theme)
	}
}

func TestParseCLI_ThemeDark(t *testing.T) {
	cfg := parseCLIForTest([]string{"--theme", "dark", "test"})
	if cfg.Theme != "dark" {
		t.Errorf("expected theme='dark', got %q", cfg.Theme)
	}
}

func TestParseCLI_ThemeInvalid(t *testing.T) {
	cfg := parseCLIForTest([]string{"--theme", "invalid", "test"})
	if cfg.Theme != "auto" {
		t.Errorf("expected theme='auto' for invalid, got %q", cfg.Theme)
	}
}

func TestParseCLI_ThemeDarkColorBlind(t *testing.T) {
	cfg := parseCLIForTest([]string{"--theme", "darkcolorblind", "test"})
	if cfg.Theme != "darkcolorblind" {
		t.Errorf("expected theme='darkcolorblind', got %q", cfg.Theme)
	}
}

func TestParseCLI_ThemeLightColorBlind(t *testing.T) {
	cfg := parseCLIForTest([]string{"--theme", "lightcolorblind", "test"})
	if cfg.Theme != "lightcolorblind" {
		t.Errorf("expected theme='lightcolorblind', got %q", cfg.Theme)
	}
}

func TestParseCLI_ContextLimitDefault(t *testing.T) {
	cfg := parseCLIForTest([]string{"test"})
	// 未指定 → 0,由 main.go 从 settings 的 compaction.context_limit_tokens 回退
	// (再默认 1M),保证 HUD 显示与压缩阈值一致
	if cfg.ContextLimit != 0 {
		t.Errorf("expected ContextLimit=0 (unset, resolved by main), got %d", cfg.ContextLimit)
	}
}

func TestParseCLI_ContextLimitCustom(t *testing.T) {
	cfg := parseCLIForTest([]string{"--context-limit", "200k", "test"})
	if cfg.ContextLimit != 200000 {
		t.Errorf("expected ContextLimit=200000, got %d", cfg.ContextLimit)
	}
}

func TestParseCLI_ContextLimitInvalid(t *testing.T) {
	cfg := parseCLIForTest([]string{"--context-limit", "xyz", "test"})
	if cfg.ContextLimit != 0 {
		t.Errorf("expected ContextLimit=0 (fallback to settings), got %d", cfg.ContextLimit)
	}
}

func TestParseCLI_SettingsPath(t *testing.T) {
	cfg := parseCLIForTest([]string{"--settings", "/custom/path.json", "test"})
	if cfg.SettingsPath != "/custom/path.json" {
		t.Errorf("expected SettingsPath='/custom/path.json', got %q", cfg.SettingsPath)
	}
}

func TestParseCLI_SystemPrompt(t *testing.T) {
	cfg := parseCLIForTest([]string{"--system-prompt", "You are helpful", "test"})
	if cfg.SystemPrompt != "You are helpful" {
		t.Errorf("expected SystemPrompt='You are helpful', got %q", cfg.SystemPrompt)
	}
}

func TestParseCLI_DefaultValues(t *testing.T) {
	cfg := parseCLIForTest([]string{"test"})
	if cfg.MaxTurns != 0 {
		t.Errorf("default MaxTurns should be 0, got %d", cfg.MaxTurns)
	}
	if cfg.Model != "" {
		t.Errorf("default Model should be empty, got %q", cfg.Model)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("default LogLevel should be info, got %q", cfg.LogLevel)
	}
	if cfg.BypassPerm {
		t.Error("default BypassPerm should be false")
	}
}

// REGRESSION: prompt 在前的命令格式("waveloom 'prompt' --flag ...")——
// Go flag 包在首个非 flag 参数处停止解析,循环解析修复前 prompt 后的
// flag 全部失效(评测 runner 调用格式实测受影响,2026-08-10)。
func TestParseCLI_FlagsAfterPrompt(t *testing.T) {
	cfg := parseCLIForTest([]string{"fix the bug", "--max-turns", "25", "--no-sandbox", "--bypass-permissions", "--context-limit", "128k"})
	if cfg.OneShot != "fix the bug" {
		t.Errorf("expected OneShot='fix the bug', got %q", cfg.OneShot)
	}
	if cfg.MaxTurns != 25 {
		t.Errorf("expected MaxTurns=25, got %d", cfg.MaxTurns)
	}
	if !cfg.NoSandbox {
		t.Error("expected NoSandbox=true")
	}
	if !cfg.BypassPerm {
		t.Error("expected BypassPerm=true")
	}
	if cfg.ContextLimit != 128000 {
		t.Errorf("expected ContextLimit=128000, got %d", cfg.ContextLimit)
	}
}

// REGRESSION: 多个位置参数与 flag 交错时,位置参数按序收集。
func TestParseCLI_InterleavedPositional(t *testing.T) {
	cfg := parseCLIForTest([]string{"prompt-a", "--max-turns", "3", "prompt-b"})
	// 单次模式取第一个位置参数为 prompt,后续位置参数不影响解析
	if cfg.OneShot != "prompt-a" {
		t.Errorf("expected OneShot='prompt-a', got %q", cfg.OneShot)
	}
	if cfg.MaxTurns != 3 {
		t.Errorf("expected MaxTurns=3, got %d", cfg.MaxTurns)
	}
}

// TestParseCLI_NoSandboxFlag --no-sandbox 在 prompt 前解析正常。
func TestParseCLI_NoSandboxFlag(t *testing.T) {
	cfg := parseCLIForTest([]string{"--no-sandbox", "test"})
	if !cfg.NoSandbox {
		t.Error("expected NoSandbox=true")
	}
}

// REGRESSION: 子命令后的参数原样保留给子命令,不被主 flag 集解析
// (重构前 flag.Parse 在子命令名处停止;循环解析不能吞掉 acp 的 --session-dir)。
func TestParseCLI_SubcommandArgsPreserved(t *testing.T) {
	// acp 子命令在 parseCLI 内直接调用 runACP 并退出,无法在单测中执行;
	// 这里验证 completion 子命令(解析逻辑共享,不进 runACP)。
	cfg := parseCLIForTest([]string{"completion", "bash"})
	if cfg.CompletionShell != "bash" {
		t.Errorf("expected CompletionShell='bash', got %q", cfg.CompletionShell)
	}
	// mcp 子命令参数应保留到 runMCPCommand——parseCLI 内直接执行无法断言,
	// 以 setup/ls 无参数子命令验证不 panic 即可。
	_ = parseCLIForTest([]string{"ls"})
}

// REGRESSION: "--" 终止符之后全部为位置参数,不再解析为 flag。
func TestParseCLI_DoubleDashTerminator(t *testing.T) {
	cfg := parseCLIForTest([]string{"--", "prompt", "--version"})
	if cfg.OneShot != "prompt" {
		t.Errorf("expected OneShot='prompt', got %q", cfg.OneShot)
	}
	if cfg.ShowVersion {
		t.Error("--version after -- should NOT be parsed as flag")
	}
}

// REGRESSION: flag 以 "=" 形式出现(prompt 之后)。
func TestParseCLI_FlagEqualsFormAfterPrompt(t *testing.T) {
	cfg := parseCLIForTest([]string{"fix bug", "--no-sandbox=true", "--max-turns=7"})
	if cfg.OneShot != "fix bug" {
		t.Errorf("expected OneShot='fix bug', got %q", cfg.OneShot)
	}
	if !cfg.NoSandbox {
		t.Error("expected NoSandbox=true")
	}
	if cfg.MaxTurns != 7 {
		t.Errorf("expected MaxTurns=7, got %d", cfg.MaxTurns)
	}
}

// REGRESSION: 同一 flag 出现多次,末次生效(与单次 flag.Parse 行为一致)。
func TestParseCLI_DuplicateFlagLastWins(t *testing.T) {
	cfg := parseCLIForTest([]string{"--model", "a", "prompt", "--model", "b"})
	if cfg.Model != "b" {
		t.Errorf("expected Model='b' (last wins), got %q", cfg.Model)
	}
	if cfg.OneShot != "prompt" {
		t.Errorf("expected OneShot='prompt', got %q", cfg.OneShot)
	}
}

// ---------------------------------------------------------------------------
// isPiped
// ---------------------------------------------------------------------------

func TestIsPiped_NotPiped(t *testing.T) {
	// 测试环境通常不是管道 — 只验证不 panic
	_ = isPiped()
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// colorHex
// ---------------------------------------------------------------------------

func TestColorHex_Black(t *testing.T) {
	c := color.RGBA{R: 0, G: 0, B: 0, A: 255}
	h := colorHex(c)
	if h != "#000000" {
		t.Errorf("expected #000000, got %q", h)
	}
}

func TestColorHex_White(t *testing.T) {
	c := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	h := colorHex(c)
	if h != "#ffffff" {
		t.Errorf("expected #ffffff, got %q", h)
	}
}

func TestColorHex_Red(t *testing.T) {
	c := color.RGBA{R: 255, G: 0, B: 0, A: 255}
	h := colorHex(c)
	if h != "#ff0000" {
		t.Errorf("expected #ff0000, got %q", h)
	}
}

// ---------------------------------------------------------------------------
// fileItem / permItem 接口方法
// ---------------------------------------------------------------------------

func TestFileItem_Title(t *testing.T) {
	f := fileItem{display: "main.go", isDir: false}
	if f.Title() != "main.go" {
		t.Errorf("expected 'main.go', got %q", f.Title())
	}
}

func TestFileItem_Description(t *testing.T) {
	f := fileItem{display: "main.go", isDir: false}
	if f.Description() != "" {
		t.Errorf("expected empty description, got %q", f.Description())
	}
}

func TestFileItem_FilterValue(t *testing.T) {
	f := fileItem{display: "main.go", isDir: false}
	if f.FilterValue() != "main.go" {
		t.Errorf("expected 'main.go', got %q", f.FilterValue())
	}
}

func TestFileItem_Dir(t *testing.T) {
	f := fileItem{display: "pkg/", isDir: true}
	if !f.isDir {
		t.Error("expected isDir=true")
	}
	if f.Title() != "pkg/" {
		t.Errorf("expected 'pkg/', got %q", f.Title())
	}
}

func TestPermItem_FilterValue(t *testing.T) {
	p := permItem{title: "Allow", description: "Permit action", choice: permAllow}
	if p.FilterValue() != "Allow" {
		t.Errorf("expected 'Allow', got %q", p.FilterValue())
	}
}

func TestPermItem_TitleDescription(t *testing.T) {
	p := permItem{title: "Deny", description: "Block action", choice: permDeny}
	if p.Title() != "Deny" {
		t.Errorf("expected 'Deny', got %q", p.Title())
	}
	if p.Description() != "Block action" {
		t.Errorf("expected 'Block action', got %q", p.Description())
	}
}

// ---------------------------------------------------------------------------
// printHelp — 验证不 panic
// ---------------------------------------------------------------------------

func TestPrintHelp_NoPanic(t *testing.T) {
	// printHelp 写入 os.Stderr，仅验证不 panic
	printHelp(LocaleEnUS)
	printHelp(LocaleZhCN)
}
