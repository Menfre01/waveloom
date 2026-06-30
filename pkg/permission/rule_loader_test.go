package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRulesFromSettings_ValidConfig(t *testing.T) {
	ps := &PermissionSettings{
		Allow: []string{"read_file", "Bash(git *)", "grep"},
		Deny:  []string{"shell(rm -rf *)"},
		Ask:   []string{"write_file", "edit_file"},
	}

	entries := LoadRulesFromSettings(ps, SourceConfig)

	// 验证数量: 3 allow + 1 deny + 2 ask = 6
	if len(entries) != 6 {
		t.Fatalf("LoadRulesFromSettings returned %d entries, want 6", len(entries))
	}

	// 验证 allow 规则
	allowRules := filterByBehavior(entries, RuleAllow)
	if len(allowRules) != 3 {
		t.Errorf("allow rules = %d, want 3", len(allowRules))
	}

	// 验证 Bash → shell 映射
	foundBashMapped := false
	for _, e := range allowRules {
		if e.Rule.ToolName == "bash" && e.Rule.Pattern == "git *" {
			foundBashMapped = true
		}
	}
	if !foundBashMapped {
		t.Error("Bash(git *) should be mapped to shell(git *)")
	}

	// 验证 deny 规则
	denyRules := filterByBehavior(entries, RuleDeny)
	if len(denyRules) != 1 {
		t.Errorf("deny rules = %d, want 1", len(denyRules))
	}

	// 验证 ask 规则
	askRules := filterByBehavior(entries, RuleAsk)
	if len(askRules) != 2 {
		t.Errorf("ask rules = %d, want 2", len(askRules))
	}
}

func TestLoadRulesFromSettings_NilSettings(t *testing.T) {
	entries := LoadRulesFromSettings(nil, SourceConfig)
	if entries != nil {
		t.Errorf("nil settings should return nil, got %v", entries)
	}
}

func TestLoadRulesFromSettings_EmptyConfig(t *testing.T) {
	ps := &PermissionSettings{}
	entries := LoadRulesFromSettings(ps, SourceConfig)
	if len(entries) != 0 {
		t.Errorf("empty config should return 0 entries, got %d", len(entries))
	}
}

func TestLoadRulesFromSettings_InvalidRulesSkipped(t *testing.T) {
	ps := &PermissionSettings{
		Allow: []string{"read_file", "shell()", "valid_tool"},
	}

	entries := LoadRulesFromSettings(ps, SourceConfig)
	// "shell()" should be skipped (empty pattern)
	if len(entries) != 2 {
		t.Errorf("expected 2 valid entries (skipping 1 invalid), got %d", len(entries))
	}
}

func TestLoadRulesFromConfigFile(t *testing.T) {
	// 创建临时配置文件
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")

	config := map[string]interface{}{
		"permissions": map[string]interface{}{
			"allow": []string{"read_file", "Bash(go *)"},
			"deny":  []string{"shell(rm -rf /*)"},
			"ask":   []string{"write_file"},
		},
	}
	data, _ := json.Marshal(config)
	_ = os.WriteFile(configPath, data, 0o644)

	entries, err := LoadRulesFromConfigFile(configPath)
	if err != nil {
		t.Fatalf("LoadRulesFromConfigFile error: %v", err)
	}

	if len(entries) != 4 {
		t.Errorf("expected 4 entries, got %d", len(entries))
	}

	// 验证 Bash → shell 映射
	foundGoRule := false
	for _, e := range entries {
		if e.Rule.ToolName == "bash" && e.Rule.Pattern == "go *" {
			foundGoRule = true
		}
	}
	if !foundGoRule {
		t.Error("Bash(go *) should be mapped to bash(go *)")
	}
}

func TestLoadRulesFromConfigFile_NotExist(t *testing.T) {
	entries, err := LoadRulesFromConfigFile("/nonexistent/settings.json")
	if err != nil {
		t.Errorf("non-existent file should not error, got: %v", err)
	}
	if entries != nil {
		t.Errorf("non-existent file should return nil entries, got %v", entries)
	}
}

func TestLoadRulesFromConfigFile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	_ = os.WriteFile(configPath, []byte("invalid json"), 0o644)

	_, err := LoadRulesFromConfigFile(configPath)
	if err == nil {
		t.Error("invalid JSON should return error")
	}
}

func TestLoadRulesFromConfigFile_NoPermissionsBlock(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")

	config := map[string]interface{}{
		"llm": map[string]interface{}{
			"provider": "openai",
		},
	}
	data, _ := json.Marshal(config)
	_ = os.WriteFile(configPath, data, 0o644)

	entries, err := LoadRulesFromConfigFile(configPath)
	if err != nil {
		t.Fatalf("missing permissions block should not error, got: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("missing permissions block should return 0 entries, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// 测试辅助
// ---------------------------------------------------------------------------

func filterByBehavior(entries []RuleEntry, behavior RuleBehavior) []RuleEntry {
	var result []RuleEntry
	for _, e := range entries {
		if e.Rule.Behavior == behavior {
			result = append(result, e)
		}
	}
	return result
}
