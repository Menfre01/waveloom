package main

import (
	"flag"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

// resetCommandLine 重置全局 flag.CommandLine，避免跨 parseCLI 调用重复注册 panic。
func resetCommandLine() {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
}

// parseCLIForTest 在每次调用前重置 flag.CommandLine 并设置 os.Args。
func parseCLIForTest(args []string) CLIConfig {
	resetCommandLine()
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
	if projectPath != "/custom/settings.json" {
		t.Errorf("expected /custom/settings.json, got %q", projectPath)
	}
	if globalPath == "" {
		t.Error("globalPath should not be empty")
	}
}

// ---------------------------------------------------------------------------
// setupVerboseLog
// ---------------------------------------------------------------------------

func TestSetupVerboseLog_Disabled(t *testing.T) {
	wc, err := setupVerboseLog(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wc != nil {
		t.Error("expected nil for verbose=false")
	}
}

func TestSetupVerboseLog_Enabled(t *testing.T) {
	dir := filepath.Join(".waveloom")
	origExists := false
	if _, err := os.Stat(dir); err == nil {
		origExists = true
	}

	wc, err := setupVerboseLog(true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wc == nil {
		t.Fatal("expected non-nil writer for verbose=true")
	}
	_ = wc.Close()

	logPath := filepath.Join(".waveloom", "waveloom.log")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("waveloom.log not created")
	}

	_ = os.Remove(logPath)
	_ = os.Remove(logPath + ".1")
	if !origExists {
		_ = os.Remove(dir)
	}
}

// ---------------------------------------------------------------------------
// buildSystemPrompt
// ---------------------------------------------------------------------------

func TestBuildSystemPrompt_ContainsCWD(t *testing.T) {
	prompt := buildSystemPrompt("/test/cwd")
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
	prompt := buildSystemPrompt("/tmp")
	if len(prompt) < 100 {
		t.Errorf("prompt too short: %d chars", len(prompt))
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

func TestParseCLI_Verbose(t *testing.T) {
	cfg := parseCLIForTest([]string{"--verbose", "test"})
	if !cfg.Verbose {
		t.Error("expected Verbose=true")
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

func TestParseCLI_ContextLimitDefault(t *testing.T) {
	cfg := parseCLIForTest([]string{"test"})
	if cfg.ContextLimit != 1000000 {
		t.Errorf("expected default ContextLimit=1000000, got %d", cfg.ContextLimit)
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
	if cfg.ContextLimit != 1000000 {
		t.Errorf("expected fallback ContextLimit=1000000, got %d", cfg.ContextLimit)
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
	if cfg.Verbose {
		t.Error("default Verbose should be false")
	}
	if cfg.BypassPerm {
		t.Error("default BypassPerm should be false")
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
	printHelp()
}
