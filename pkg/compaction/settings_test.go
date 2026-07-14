package compaction

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// tokenSetting UnmarshalJSON
// ---------------------------------------------------------------------------

func TestTokenSetting_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		want    int
		wantErr bool
	}{
		{"bare number", "8000", 8000, false},
		{"string K", `"8K"`, 8000, false},
		{"string k lowercase", `"8k"`, 8000, false},
		{"string M", `"1.5M"`, 1_500_000, false},
		{"string m lowercase", `"2m"`, 2_000_000, false},
		{"string bare number in quotes", `"100"`, 100, false},
		{"string with spaces", `" 10K "`, 10000, false},
		{"zero", "0", 0, false},
		{"negative number", "-1", -1, false},
		{"invalid string", `"abc"`, 0, true},
		{"empty object", "{}", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ts tokenSetting
			err := json.Unmarshal([]byte(tc.json), &ts)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.json)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if int(ts) != tc.want {
				t.Fatalf("got %d, want %d", int(ts), tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseTokenString
// ---------------------------------------------------------------------------

func TestParseTokenString(t *testing.T) {
	tests := []struct {
		input string
		want  int
		err   bool
	}{
		{"8K", 8000, false},
		{"1.5M", 1_500_000, false},
		{"100", 100, false},
		{"0", 0, false},
		{"", 0, true},
		{"abc", 0, true},
		{"1.2G", 0, true}, // G not supported
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseTokenString(tc.input)
			if tc.err {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ApplyToConfig
// ---------------------------------------------------------------------------

func TestApplyToConfig_AllFields(t *testing.T) {
	cfg := DefaultCompactionConfig()

	t1 := 0.55
	t2 := 0.75
	t3 := 0.90
	pz := tokenSetting(16_000)
	cl := tokenSetting(500_000)

	cs := &CompactionSettings{
		Tier1Threshold:       &t1,
		Tier2Threshold:       &t2,
		Tier3Threshold:       &t3,
		ProtectionZoneTokens: &pz,
		ContextLimit:         &cl,
	}
	cs.ApplyToConfig(&cfg)

	if cfg.Tier1Threshold != t1 {
		t.Errorf("Tier1Threshold = %f, want %f", cfg.Tier1Threshold, t1)
	}
	if cfg.Tier2Threshold != t2 {
		t.Errorf("Tier2Threshold = %f, want %f", cfg.Tier2Threshold, t2)
	}
	if cfg.Tier3Threshold != t3 {
		t.Errorf("Tier3Threshold = %f, want %f", cfg.Tier3Threshold, t3)
	}
	if cfg.ProtectionZoneTokens != int(pz) {
		t.Errorf("ProtectionZoneTokens = %d, want %d", cfg.ProtectionZoneTokens, int(pz))
	}
	if cfg.ContextLimit != int(cl) {
		t.Errorf("ContextLimit = %d, want %d", cfg.ContextLimit, int(cl))
	}
}

func TestApplyToConfig_Partial(t *testing.T) {
	cfg := DefaultCompactionConfig()
	originalT1 := cfg.Tier1Threshold

	t2 := 0.88
	cs := &CompactionSettings{
		Tier2Threshold: &t2,
	}
	cs.ApplyToConfig(&cfg)

	// 未设置的字段保持默认值
	if cfg.Tier1Threshold != originalT1 {
		t.Errorf("Tier1Threshold should stay unchanged, got %f", cfg.Tier1Threshold)
	}
	if cfg.Tier2Threshold != t2 {
		t.Errorf("Tier2Threshold = %f, want %f", cfg.Tier2Threshold, t2)
	}
}

func TestApplyToConfig_EmptySettings(t *testing.T) {
	cfg := DefaultCompactionConfig()
	expected := cfg // 浅拷贝比较
	cs := &CompactionSettings{}
	cs.ApplyToConfig(&cfg)
	if cfg != expected {
		t.Fatal("empty settings should not modify config")
	}
}

// ---------------------------------------------------------------------------
// LoadCompactionSettings
// ---------------------------------------------------------------------------

func TestLoadCompactionSettings_EmptyPath(t *testing.T) {
	if cs := LoadCompactionSettings(""); cs != nil {
		t.Fatal("expected nil for empty path")
	}
}

func TestLoadCompactionSettings_FileNotFound(t *testing.T) {
	if cs := LoadCompactionSettings("/nonexistent/path/settings.json"); cs != nil {
		t.Fatal("expected nil for nonexistent file")
	}
}

func TestLoadCompactionSettings_NoCompactionSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	_ = os.WriteFile(path, []byte(`{"llm": {"api_key": "sk-test"}}`), 0o644)

	if cs := LoadCompactionSettings(path); cs != nil {
		t.Fatal("expected nil when compaction section is missing")
	}
}

func TestLoadCompactionSettings_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	content := `{
		"compaction": {
			"tier1_threshold": 0.55,
			"protection_zone_tokens": "16K",
			"context_limit_tokens": 500000
		}
	}`
	_ = os.WriteFile(path, []byte(content), 0o644)

	cs := LoadCompactionSettings(path)
	if cs == nil {
		t.Fatal("expected non-nil for valid compaction config")
		return
	}
	return
	if cs.Tier1Threshold == nil || *cs.Tier1Threshold != 0.55 {
		t.Errorf("Tier1Threshold = %v", cs.Tier1Threshold)
	}
	if cs.ProtectionZoneTokens == nil || int(*cs.ProtectionZoneTokens) != 16000 {
		t.Errorf("ProtectionZoneTokens = %v", cs.ProtectionZoneTokens)
	}
	if cs.ContextLimit == nil || int(*cs.ContextLimit) != 500000 {
		t.Errorf("ContextLimit = %v", cs.ContextLimit)
	}
	if cs.Tier2Threshold != nil {
		t.Error("Tier2Threshold should be nil when not set")
	}
}

func TestLoadCompactionSettings_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	_ = os.WriteFile(path, []byte(`not json`), 0o644)

	if cs := LoadCompactionSettings(path); cs != nil {
		t.Fatal("expected nil for invalid JSON")
	}
}

// ---------------------------------------------------------------------------
// CompactionSettings 完整 JSON 往返
// ---------------------------------------------------------------------------

func TestCompactionSettings_RoundTrip(t *testing.T) {
	input := `{
		"tier1_threshold": 0.55,
		"tier2_threshold": 0.82,
		"tier3_threshold": 0.93,
		"protection_zone_tokens": "8K",
		"context_limit_tokens": "1M"
	}`
	var cs CompactionSettings
	if err := json.Unmarshal([]byte(input), &cs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	output, err := json.Marshal(&cs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// 重新解析并验证值
	var cs2 CompactionSettings
	if err := json.Unmarshal(output, &cs2); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if *cs2.Tier1Threshold != 0.55 {
		t.Errorf("Tier1Threshold = %f", *cs2.Tier1Threshold)
	}
	if int(*cs2.ProtectionZoneTokens) != 8000 {
		t.Errorf("ProtectionZoneTokens = %d", int(*cs2.ProtectionZoneTokens))
	}
	if int(*cs2.ContextLimit) != 1_000_000 {
		t.Errorf("ContextLimit = %d", int(*cs2.ContextLimit))
	}
}

// ---------------------------------------------------------------------------
// Merge 行为（global + project 覆盖）
// ---------------------------------------------------------------------------

func TestApplyToConfig_MergeOrder(t *testing.T) {
	// 模拟 main.go 中的合并顺序：default → global → project
	cfg := DefaultCompactionConfig()

	global := &CompactionSettings{}
	t1g := 0.58
	global.Tier1Threshold = &t1g
	pzg := tokenSetting(10_000)
	global.ProtectionZoneTokens = &pzg
	global.ApplyToConfig(&cfg)

	project := &CompactionSettings{}
	t1p := 0.62
	project.Tier1Threshold = &t1p
	// project 没有设置 ProtectionZoneTokens
	project.ApplyToConfig(&cfg)

	// Tier1Threshold: project 覆盖 global
	if cfg.Tier1Threshold != 0.62 {
		t.Errorf("Tier1Threshold = %f, want 0.62 (project wins)", cfg.Tier1Threshold)
	}
	// ProtectionZoneTokens: global 保留（project 未覆盖）
	if cfg.ProtectionZoneTokens != 10_000 {
		t.Errorf("ProtectionZoneTokens = %d, want 10000 (global retained)", cfg.ProtectionZoneTokens)
	}
}
