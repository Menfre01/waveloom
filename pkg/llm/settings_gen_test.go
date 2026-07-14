package llm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultSettings(t *testing.T) {
	s := DefaultSettings()
	if s == nil {
		t.Fatal("DefaultSettings returned nil")
		return
	}
	if s.Provider != "deepseek" {
		t.Errorf("Provider = %q, want %q", s.Provider, "deepseek")
	}
	if s.Model != "deepseek-v4-pro" {
		t.Errorf("Model = %q, want %q", s.Model, "deepseek-v4-pro")
	}
	if s.BaseURL != "https://api.deepseek.com" {
		t.Errorf("BaseURL = %q, want %q", s.BaseURL, "https://api.deepseek.com")
	}
	if s.ExtraParams == nil {
		t.Fatal("ExtraParams is nil")
	}
	if s.ExtraParams["reasoning_effort"] != "max" {
		t.Errorf("reasoning_effort = %v, want max", s.ExtraParams["reasoning_effort"])
	}
}

func TestWriteDefaultSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// 写入默认配置
	if err := WriteDefaultSettings(path); err != nil {
		t.Fatalf("WriteDefaultSettings: %v", err)
	}

	// 验证文件存在
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("settings.json not created: %v", err)
	}

	// 验证内容可解析
	settings, err := LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings of generated file: %v", err)
	}
	if settings == nil {
		t.Fatal("settings is nil")
		return
	}
	if settings.Provider != "deepseek" {
		t.Errorf("Provider = %q, want %q", settings.Provider, "deepseek")
	}

	// api_key 不应在默认配置中（应由 env 提供）
	if settings.APIKey != "" {
		t.Errorf("default settings should not contain api_key, got %q", settings.APIKey)
	}

	// 再次写入不应覆盖
	if err := WriteDefaultSettings(path); err != nil {
		t.Fatalf("WriteDefaultSettings again: %v", err)
	}

	// 格式验证：合法 JSON
	var raw map[string]any
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("generated file is not valid JSON: %v", err)
	}
}
