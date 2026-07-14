package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// maskKey
// ---------------------------------------------------------------------------

func TestMaskKey_Short(t *testing.T) {
	if got := maskKey("abc"); got != "***" {
		t.Errorf("expected '***', got %q", got)
	}
}

func TestMaskKey_Exactly8(t *testing.T) {
	if got := maskKey("12345678"); got != "********" {
		t.Errorf("expected '********', got %q", got)
	}
}

func TestMaskKey_Long(t *testing.T) {
	got := maskKey("sk-abcdefghij-12345678")
	if !strings.HasPrefix(got, "sk-a") || !strings.HasSuffix(got, "5678") {
		t.Errorf("expected sk-a****...****5678, got %q", got)
	}
	if len(got) != len("sk-abcdefghij-12345678") {
		t.Errorf("expected same length, got %d vs %d", len(got), len("sk-abcdefghij-12345678"))
	}
}

// ---------------------------------------------------------------------------
// abs
// ---------------------------------------------------------------------------

func TestAbs_Positive(t *testing.T) {
	if got := abs(5); got != 5 {
		t.Errorf("abs(5) = %d", got)
	}
}

func TestAbs_Negative(t *testing.T) {
	if got := abs(-3); got != 3 {
		t.Errorf("abs(-3) = %d", got)
	}
}

func TestAbs_Zero(t *testing.T) {
	if got := abs(0); got != 0 {
		t.Errorf("abs(0) = %d", got)
	}
}

// ---------------------------------------------------------------------------
// renderSummary
// ---------------------------------------------------------------------------

func TestRenderSummary_AllFields(t *testing.T) {
	s := &setupState{
		theme:  "dark",
		locale: "zh-CN",
		prov:   "deepseek",
		model:  "deepseek-v4-pro",
		apiKey: "sk-test1234",
		lc:     &zhCN,
	}
	got := renderSummary(s)
	for _, want := range []string{"dark", "zh-CN", "deepseek", "deepseek-v4-pro", "sk-t***1234"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in summary, got:\n%s", want, got)
		}
	}
}

func TestRenderSummary_English(t *testing.T) {
	s := &setupState{
		theme:  "auto",
		locale: "en-US",
		prov:   "openai",
		model:  "gpt-4o",
		apiKey: "sk-1234567890abcdef",
		lc:     &enUS,
	}
	got := renderSummary(s)
	for _, want := range []string{"Theme", "Language", "Provider", "Model", "API Key"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in summary, got:\n%s", want, got)
		}
	}
}

// ---------------------------------------------------------------------------
// renderBannerLine
// ---------------------------------------------------------------------------

func TestRenderBannerLine_ContainsText(t *testing.T) {
	got := renderBannerLine(&zhCN)
	if !strings.Contains(got, zhCN.SetupOverwriteWarn) {
		t.Errorf("expected overwrite warning, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// renderSetupHelp
// ---------------------------------------------------------------------------

func TestRenderSetupHelp_ContainsHint(t *testing.T) {
	got := renderSetupHelp(&zhCN)
	if !strings.Contains(got, "导航") {
		t.Errorf("expected Chinese help text, got %q", got)
	}
	gotEn := renderSetupHelp(&enUS)
	if !strings.Contains(gotEn, "navigate") {
		t.Errorf("expected English help text, got %q", gotEn)
	}
}

// ---------------------------------------------------------------------------
// setupHuhTheme
// ---------------------------------------------------------------------------

func TestSetupHuhTheme_ReturnsNonNil(t *testing.T) {
	theme := setupHuhTheme()
	if theme == nil {
		t.Fatal("expected non-nil theme")
		return
	}
	styles := theme.Theme(true) // dark
	if styles == nil {
		t.Fatal("expected non-nil styles")
	}
}

// ---------------------------------------------------------------------------
// writeFullSetup
// ---------------------------------------------------------------------------

func TestWriteFullSetup_CreatesNew(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	err := writeFullSetup(path, nil, "zh-CN", "dark")
	if err != nil {
		t.Fatalf("writeFullSetup: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg["locale"] != "zh-CN" {
		t.Errorf("locale = %v", cfg["locale"])
	}
	if cfg["theme"] != "dark" {
		t.Errorf("theme = %v", cfg["theme"])
	}
}

func TestWriteFullSetup_PreservesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// 写入已有配置
	existing := map[string]any{
		"custom_field": "keep",
		"locale":       "en-US",
	}
	data, _ := json.MarshalIndent(existing, "", "    ")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	// 覆盖 locale
	err := writeFullSetup(path, nil, "zh-CN", "auto")
	if err != nil {
		t.Fatalf("writeFullSetup: %v", err)
	}

	var cfg map[string]any
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if cfg["custom_field"] != "keep" {
		t.Error("custom_field should be preserved")
	}
	if cfg["locale"] != "zh-CN" {
		t.Errorf("locale = %v", cfg["locale"])
	}
}
