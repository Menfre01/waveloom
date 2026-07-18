package llm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadSettings(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	content := `{
		"llm": {
			"provider": "deepseek",
			"model": "deepseek-v4-pro",
			"base_url": "https://api.deepseek.com",
			"timeout": "60s",
			"retry": {
				"max_retries": 5,
				"initial_backoff": "2s",
				"max_backoff": "60s",
				"multiplier": 3.0
			},
			"headers": {
				"X-Custom": "test"
			},
			"extra_params": {
				"thinking": {"type": "enabled"},
				"reasoning_effort": "max",
				"max_tokens": 8192
			}
		}
	}`

	if err := os.WriteFile(settingsPath, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	settings, err := LoadSettings(settingsPath)
	if err != nil {
		t.Fatalf("LoadSettings returned error: %v", err)
	}

	if settings.Provider != "deepseek" {
		t.Errorf("Provider = %q, want %q", settings.Provider, "deepseek")
	}
	if settings.Model != "deepseek-v4-pro" {
		t.Errorf("Model = %q, want %q", settings.Model, "deepseek-v4-pro")
	}
	if settings.BaseURL != "https://api.deepseek.com" {
		t.Errorf("BaseURL = %q, want %q", settings.BaseURL, "https://api.deepseek.com")
	}
	if settings.Timeout != "60s" {
		t.Errorf("Timeout = %q, want %q", settings.Timeout, "60s")
	}

	// 验证 Retry 块
	if settings.Retry == nil {
		t.Fatal("Retry is nil")
	}
	if settings.Retry.MaxRetries != 5 {
		t.Errorf("MaxRetries = %d, want 5", settings.Retry.MaxRetries)
	}
	if settings.Retry.InitialBackoff != "2s" {
		t.Errorf("InitialBackoff = %q, want %q", settings.Retry.InitialBackoff, "2s")
	}
	if settings.Retry.Multiplier != 3.0 {
		t.Errorf("Multiplier = %f, want 3.0", settings.Retry.Multiplier)
	}

	// 验证 Headers
	if settings.Headers["X-Custom"] != "test" {
		t.Errorf("Headers[X-Custom] = %q, want %q", settings.Headers["X-Custom"], "test")
	}

	// 验证 ExtraParams（重点：嵌套对象）
	if settings.ExtraParams == nil {
		t.Fatal("ExtraParams is nil")
	}
	if settings.ExtraParams["reasoning_effort"] != "max" {
		t.Errorf("reasoning_effort = %v, want %q", settings.ExtraParams["reasoning_effort"], "max")
	}
	if settings.ExtraParams["max_tokens"] != float64(8192) {
		t.Errorf("max_tokens = %v (%T), want float64(8192)", settings.ExtraParams["max_tokens"], settings.ExtraParams["max_tokens"])
	}
	thinking, ok := settings.ExtraParams["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("thinking type = %T, want map[string]any", settings.ExtraParams["thinking"])
	}
	if thinking["type"] != "enabled" {
		t.Errorf("thinking.type = %v, want %q", thinking["type"], "enabled")
	}
}

func TestLoadSettingsMissingLLMSection(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	content := `{"other_section": {"key": "value"}}`
	if err := os.WriteFile(settingsPath, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := LoadSettings(settingsPath)
	if err == nil {
		t.Fatal("expected error for missing llm section")
	}
}

func TestLoadSettingsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	if err := os.WriteFile(settingsPath, []byte(`{invalid json`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := LoadSettings(settingsPath)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadSettingsFileNotFound(t *testing.T) {
	_, err := LoadSettings("/nonexistent/path/settings.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadSettingsMinimal(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	content := `{
		"llm": {
			"provider": "openai",
			"model": "gpt-4o-mini"
		}
	}`

	if err := os.WriteFile(settingsPath, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	settings, err := LoadSettings(settingsPath)
	if err != nil {
		t.Fatalf("LoadSettings returned error: %v", err)
	}

	if settings.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", settings.Provider, "openai")
	}
	if settings.Model != "gpt-4o-mini" {
		t.Errorf("Model = %q, want %q", settings.Model, "gpt-4o-mini")
	}
	// 未配置的字段应为零值
	if settings.BaseURL != "" {
		t.Errorf("BaseURL = %q, want empty", settings.BaseURL)
	}
	if settings.Retry != nil {
		t.Errorf("Retry = %v, want nil", settings.Retry)
	}
}

func TestNewClientFromLLMSettings(t *testing.T) {
	settings := &LLMSettings{
		APIKey:      "sk-from-settings",
		Provider:    "deepseek",
		Model:       "deepseek-v4-pro",
		BaseURL:     "https://api.deepseek.com",
		Timeout:     "90s",
		ExtraParams: map[string]any{
			"thinking":         map[string]any{"type": "enabled"},
			"reasoning_effort": "high",
			"max_tokens":       4096,
		},
	}

	c, _, err := NewClientFromLLMSettings(settings)
	if err != nil {
		t.Fatalf("NewClientFromLLMSettings returned error: %v", err)
	}

	cl := c.(*client)
	if cl.config.APIKey != "sk-from-settings" {
		t.Errorf("APIKey = %q, want %q", cl.config.APIKey, "sk-from-settings")
	}
	if cl.config.Provider != ProviderDeepSeek {
		t.Errorf("Provider = %q, want %q", cl.config.Provider, ProviderDeepSeek)
	}
	if cl.config.Model != "deepseek-v4-pro" {
		t.Errorf("Model = %q, want %q", cl.config.Model, "deepseek-v4-pro")
	}
	if cl.config.Timeout != 90*time.Second {
		t.Errorf("Timeout = %v, want 90s", cl.config.Timeout)
	}

	// 验证 ExtraParams 透传（含嵌套对象）
	if cl.config.ExtraParams["thinking"] == nil {
		t.Fatal("ExtraParams[thinking] is nil")
	}
}

func TestNewClientFromLLMSettingsMissingAPIKey(t *testing.T) {
	_ = os.Unsetenv("LLM_API_KEY")

	settings := &LLMSettings{
		Provider: "openai",
		Model:    "gpt-4o",
	}

	_, _, err := NewClientFromLLMSettings(settings)
	if err == nil {
		t.Fatal("expected error for missing api_key")
		return
	}
	if !strings.Contains(err.Error(), "api_key is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNewClientFromLLMSettingsAPIKeyFallbackToEnv(t *testing.T) {
	_ = os.Setenv("LLM_API_KEY", "sk-from-env")
	defer func() { _ = os.Unsetenv("LLM_API_KEY") }()

	// api_key 字段为空 → 回退到环境变量
	settings := &LLMSettings{
		Provider: "openai",
		Model:    "gpt-4o",
	}

	c, _, err := NewClientFromLLMSettings(settings)
	if err != nil {
		t.Fatalf("NewClientFromLLMSettings returned error: %v", err)
	}

	cl := c.(*client)
	if cl.config.APIKey != "sk-from-env" {
		t.Errorf("APIKey = %q, want %q (env fallback)", cl.config.APIKey, "sk-from-env")
	}
}

func TestNewClientFromLLMSettingsAPIKeyFromSettingsPriority(t *testing.T) {
	_ = os.Setenv("LLM_API_KEY", "sk-from-env")
	defer func() { _ = os.Unsetenv("LLM_API_KEY") }()

	// settings.api_key 优先于环境变量
	settings := &LLMSettings{
		APIKey:   "sk-from-settings",
		Provider: "openai",
		Model:    "gpt-4o",
	}

	c, _, err := NewClientFromLLMSettings(settings)
	if err != nil {
		t.Fatalf("NewClientFromLLMSettings returned error: %v", err)
	}

	cl := c.(*client)
	if cl.config.APIKey != "sk-from-settings" {
		t.Errorf("APIKey = %q, want %q (settings priority over env)", cl.config.APIKey, "sk-from-settings")
	}
}

func TestNewClientFromLLMSettingsNil(t *testing.T) {
	_, _, err := NewClientFromLLMSettings(nil)
	if err == nil {
		t.Fatal("expected error for nil settings")
	}
}

func TestNewClientFromLLMSettingsDefaultRetry(t *testing.T) {
	settings := &LLMSettings{
		APIKey:   "sk-test",
		Provider: "openai",
		Model:    "gpt-4o",
	}

	c, _, err := NewClientFromLLMSettings(settings)
	if err != nil {
		t.Fatalf("NewClientFromLLMSettings returned error: %v", err)
	}

	cl := c.(*client)
	d := DefaultRetryPolicy()
	if cl.config.RetryPolicy.MaxRetries != d.MaxRetries {
		t.Errorf("MaxRetries = %d, want %d (default)", cl.config.RetryPolicy.MaxRetries, d.MaxRetries)
	}
	if cl.config.RetryPolicy.InitialBackoff != d.InitialBackoff {
		t.Errorf("InitialBackoff = %v, want %v (default)", cl.config.RetryPolicy.InitialBackoff, d.InitialBackoff)
	}
}

func TestNewClientFromLLMSettingsPartialRetry(t *testing.T) {
	settings := &LLMSettings{
		APIKey:   "sk-test",
		Provider: "openai",
		Model:    "gpt-4o",
		Retry: &RetrySettings{
			MaxRetries: 10,
		},
	}

	c, _, err := NewClientFromLLMSettings(settings)
	if err != nil {
		t.Fatalf("NewClientFromLLMSettings returned error: %v", err)
	}

	cl := c.(*client)
	if cl.config.RetryPolicy.MaxRetries != 10 {
		t.Errorf("MaxRetries = %d, want 10", cl.config.RetryPolicy.MaxRetries)
	}
	d := DefaultRetryPolicy()
	if cl.config.RetryPolicy.InitialBackoff != d.InitialBackoff {
		t.Errorf("InitialBackoff = %v, want %v (default)", cl.config.RetryPolicy.InitialBackoff, d.InitialBackoff)
	}
	if cl.config.RetryPolicy.Multiplier != d.Multiplier {
		t.Errorf("Multiplier = %f, want %f (default)", cl.config.RetryPolicy.Multiplier, d.Multiplier)
	}
}

func TestNewClientFromLLMSettingsInvalidTimeout(t *testing.T) {
	settings := &LLMSettings{
		APIKey:   "sk-test",
		Provider: "openai",
		Model:    "gpt-4o",
		Timeout:  "not-a-duration",
	}

	_, _, err := NewClientFromLLMSettings(settings)
	if err == nil {
		t.Fatal("expected error for invalid timeout")
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input       string
		defaultVal  time.Duration
		want        time.Duration
		wantErr     bool
	}{
		{"", 600 * time.Second, 600 * time.Second, false},
		{"30s", 0, 30 * time.Second, false},
		{"5m", 0, 5 * time.Minute, false},
		{"1h30m", 0, 90 * time.Minute, false},
		{"invalid", 0, 0, true},
	}

	for _, tt := range tests {
		got, err := parseDuration(tt.input, tt.defaultVal)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseDuration(%q, %v): expected error", tt.input, tt.defaultVal)
			}
		} else {
			if err != nil {
				t.Errorf("parseDuration(%q, %v): unexpected error: %v", tt.input, tt.defaultVal, err)
			}
			if got != tt.want {
				t.Errorf("parseDuration(%q, %v) = %v, want %v", tt.input, tt.defaultVal, got, tt.want)
			}
		}
	}
}

func TestParseRetrySettings(t *testing.T) {
	t.Run("nil returns defaults", func(t *testing.T) {
		got := parseRetrySettings(nil)
		d := DefaultRetryPolicy()
		if got != d {
			t.Errorf("parseRetrySettings(nil) = %+v, want %+v", got, d)
		}
	})

	t.Run("empty returns defaults", func(t *testing.T) {
		got := parseRetrySettings(&RetrySettings{})
		d := DefaultRetryPolicy()
		if got != d {
			t.Errorf("parseRetrySettings(empty) = %+v, want %+v", got, d)
		}
	})

	t.Run("full override", func(t *testing.T) {
		got := parseRetrySettings(&RetrySettings{
			MaxRetries:     10,
			InitialBackoff: "3s",
			MaxBackoff:     "120s",
			Multiplier:     3.5,
		})
		if got.MaxRetries != 10 {
			t.Errorf("MaxRetries = %d, want 10", got.MaxRetries)
		}
		if got.InitialBackoff != 3*time.Second {
			t.Errorf("InitialBackoff = %v, want 3s", got.InitialBackoff)
		}
		if got.MaxBackoff != 120*time.Second {
			t.Errorf("MaxBackoff = %v, want 120s", got.MaxBackoff)
		}
		if got.Multiplier != 3.5 {
			t.Errorf("Multiplier = %f, want 3.5", got.Multiplier)
		}
	})
}

func TestNewClientFromSettingsFileNotFound(t *testing.T) {
	_, err := NewClientFromSettings("/nonexistent/path/settings.json")
	if err == nil {
		t.Fatal("expected error for missing settings file")
	}
}

func TestNewClientFromSettingsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	content := `{
		"llm": {
			"api_key": "sk-from-json-file",
			"provider": "deepseek",
			"model": "deepseek-v4-pro",
			"base_url": "https://api.deepseek.com",
			"timeout": "45s",
			"retry": {
				"max_retries": 7,
				"initial_backoff": "3s",
				"max_backoff": "90s",
				"multiplier": 4.0
			},
			"extra_params": {
				"thinking": {"type": "enabled"},
				"reasoning_effort": "max",
				"response_format": {"type": "json_object"},
				"top_logprobs": 5
			}
		}
	}`

	if err := os.WriteFile(settingsPath, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	c, err := NewClientFromSettings(settingsPath)
	if err != nil {
		t.Fatalf("NewClientFromSettings returned error: %v", err)
	}

	cl := c.(*client)

	if cl.config.APIKey != "sk-from-json-file" {
		t.Errorf("APIKey = %q, want %q", cl.config.APIKey, "sk-from-json-file")
	}
	// 验证基本字段
	if cl.config.Provider != ProviderDeepSeek {
		t.Errorf("Provider = %q, want %q", cl.config.Provider, ProviderDeepSeek)
	}
	if cl.config.Model != "deepseek-v4-pro" {
		t.Errorf("Model = %q, want %q", cl.config.Model, "deepseek-v4-pro")
	}
	if cl.config.Timeout != 45*time.Second {
		t.Errorf("Timeout = %v, want 45s", cl.config.Timeout)
	}

	// 验证 RetryPolicy
	if cl.config.RetryPolicy.MaxRetries != 7 {
		t.Errorf("MaxRetries = %d, want 7", cl.config.RetryPolicy.MaxRetries)
	}
	if cl.config.RetryPolicy.InitialBackoff != 3*time.Second {
		t.Errorf("InitialBackoff = %v, want 3s", cl.config.RetryPolicy.InitialBackoff)
	}
	if cl.config.RetryPolicy.MaxBackoff != 90*time.Second {
		t.Errorf("MaxBackoff = %v, want 90s", cl.config.RetryPolicy.MaxBackoff)
	}
	if cl.config.RetryPolicy.Multiplier != 4.0 {
		t.Errorf("Multiplier = %f, want 4.0", cl.config.RetryPolicy.Multiplier)
	}

	// 验证 ExtraParams（含嵌套对象）
	thinking, ok := cl.config.ExtraParams["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("thinking type = %T, want map[string]any", cl.config.ExtraParams["thinking"])
	}
	if thinking["type"] != "enabled" {
		t.Errorf("thinking.type = %v, want enabled", thinking["type"])
	}

	if cl.config.ExtraParams["reasoning_effort"] != "max" {
		t.Errorf("reasoning_effort = %v, want max", cl.config.ExtraParams["reasoning_effort"])
	}

	respFmt, ok := cl.config.ExtraParams["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format type = %T, want map[string]any", cl.config.ExtraParams["response_format"])
	}
	if respFmt["type"] != "json_object" {
		t.Errorf("response_format.type = %v, want json_object", respFmt["type"])
	}

	if cl.config.ExtraParams["top_logprobs"] != float64(5) {
		t.Errorf("top_logprobs = %v (%T), want float64(5)", cl.config.ExtraParams["top_logprobs"], cl.config.ExtraParams["top_logprobs"])
	}
}

// 确保 settingsFile 结构可被 json.Unmarshal 正确解析
func TestSettingsFileJSONRoundTrip(t *testing.T) {
	original := settingsFile{
		LLM: &LLMSettings{
			Provider: "deepseek",
			Model:    "deepseek-v4-pro",
			ExtraParams: map[string]any{
				"thinking":         map[string]any{"type": "enabled"},
				"reasoning_effort": "high",
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var restored settingsFile
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if restored.LLM.Provider != "deepseek" {
		t.Errorf("Provider = %q, want deepseek", restored.LLM.Provider)
	}

	thinking, ok := restored.LLM.ExtraParams["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Errorf("thinking lost after round-trip: %v", restored.LLM.ExtraParams["thinking"])
	}
}

func TestLoadSettingsIfExists(t *testing.T) {
	dir := t.TempDir()

	t.Run("file exists with llm section", func(t *testing.T) {
		path := filepath.Join(dir, "exists.json")
		content := `{"llm": {"provider": "deepseek", "model": "deepseek-v4-flash"}}`
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		s, err := LoadSettingsIfExists(path)
		if err != nil {
			t.Fatalf("LoadSettingsIfExists: %v", err)
		}
		if s == nil {
			t.Fatal("expected non-nil settings")
			return
		}
		if s.Provider != "deepseek" {
			t.Errorf("Provider = %q, want %q", s.Provider, "deepseek")
		}
	})

	t.Run("file does not exist returns nil", func(t *testing.T) {
		s, err := LoadSettingsIfExists(filepath.Join(dir, "nonexistent.json"))
		if err != nil {
			t.Fatalf("LoadSettingsIfExists: %v", err)
		}
		if s != nil {
			t.Error("expected nil for nonexistent file")
		}
	})

	t.Run("empty path returns nil", func(t *testing.T) {
		s, err := LoadSettingsIfExists("")
		if err != nil {
			t.Fatalf("LoadSettingsIfExists: %v", err)
		}
		if s != nil {
			t.Error("expected nil for empty path")
		}
	})

	t.Run("missing llm section returns nil", func(t *testing.T) {
		path := filepath.Join(dir, "no_llm.json")
		content := `{"other": {"key": "value"}}`
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		s, err := LoadSettingsIfExists(path)
		if err != nil {
			t.Fatalf("LoadSettingsIfExists: %v", err)
		}
		if s != nil {
			t.Error("expected nil when llm section missing")
		}
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		path := filepath.Join(dir, "invalid.json")
		if err := os.WriteFile(path, []byte(`{bad`), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		_, err := LoadSettingsIfExists(path)
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
}

func TestMergeLLMSettings(t *testing.T) {
	t.Run("both nil returns nil", func(t *testing.T) {
		got := MergeLLMSettings(nil, nil)
		if got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})

	t.Run("base nil returns override", func(t *testing.T) {
		override := &LLMSettings{Provider: "deepseek", Model: "v4"}
		got := MergeLLMSettings(nil, override)
		if got != override {
			t.Error("expected override to be returned when base is nil")
		}
	})

	t.Run("override nil returns base", func(t *testing.T) {
		base := &LLMSettings{Provider: "openai", Model: "gpt-4o"}
		got := MergeLLMSettings(base, nil)
		if got != base {
			t.Error("expected base to be returned when override is nil")
		}
	})

	t.Run("override overrides scalar fields", func(t *testing.T) {
		base := &LLMSettings{
			APIKey:   "base-key",
			Provider: "openai",
			Model:    "gpt-4o",
			BaseURL:  "https://base.example.com",
			Timeout:  "60s",
		}
		override := &LLMSettings{
			APIKey:   "override-key",
			Provider: "deepseek",
			Model:    "deepseek-v4-flash",
			BaseURL:  "https://override.example.com",
		}

		got := MergeLLMSettings(base, override)
		if got.APIKey != "override-key" {
			t.Errorf("APIKey = %q, want override-key", got.APIKey)
		}
		if got.Provider != "deepseek" {
			t.Errorf("Provider = %q, want deepseek", got.Provider)
		}
		if got.Model != "deepseek-v4-flash" {
			t.Errorf("Model = %q, want deepseek-v4-flash", got.Model)
		}
		// Provider 切换：BaseURL 来自 override，不从 base 继承
		if got.BaseURL != "https://override.example.com" {
			t.Errorf("BaseURL = %q, want https://override.example.com", got.BaseURL)
		}
		// Provider 无关字段：Timeout 从 base 继承
		if got.Timeout != "60s" {
			t.Errorf("Timeout = %q, want 60s", got.Timeout)
		}
	})

	t.Run("merge headers shallow merge", func(t *testing.T) {
		base := &LLMSettings{
			Headers: map[string]string{"X-A": "a", "X-B": "b"},
		}
		override := &LLMSettings{
			Headers: map[string]string{"X-B": "new-b", "X-C": "c"},
		}

		got := MergeLLMSettings(base, override)
		if got.Headers["X-A"] != "a" {
			t.Errorf("X-A = %q, want a", got.Headers["X-A"])
		}
		if got.Headers["X-B"] != "new-b" {
			t.Errorf("X-B = %q, want new-b", got.Headers["X-B"])
		}
		if got.Headers["X-C"] != "c" {
			t.Errorf("X-C = %q, want c", got.Headers["X-C"])
		}
	})

	t.Run("merge extra_params shallow merge", func(t *testing.T) {
		base := &LLMSettings{
			ExtraParams: map[string]any{"temperature": 0.7, "max_tokens": 4096},
		}
		override := &LLMSettings{
			ExtraParams: map[string]any{"temperature": 0.3, "top_p": 0.9},
		}

		got := MergeLLMSettings(base, override)
		if got.ExtraParams["temperature"] != 0.3 {
			t.Errorf("temperature = %v, want 0.3", got.ExtraParams["temperature"])
		}
		if got.ExtraParams["max_tokens"] != 4096 {
			t.Errorf("max_tokens = %v, want 4096", got.ExtraParams["max_tokens"])
		}
		if got.ExtraParams["top_p"] != 0.9 {
			t.Errorf("top_p = %v, want 0.9", got.ExtraParams["top_p"])
		}
	})

	t.Run("override retry replaces entirely", func(t *testing.T) {
		base := &LLMSettings{
			Retry: &RetrySettings{MaxRetries: 3, InitialBackoff: "1s"},
		}
		override := &LLMSettings{
			Retry: &RetrySettings{MaxRetries: 10},
		}

		got := MergeLLMSettings(base, override)
		if got.Retry.MaxRetries != 10 {
			t.Errorf("MaxRetries = %d, want 10", got.Retry.MaxRetries)
		}
		if got.Retry.InitialBackoff != "" {
			t.Errorf("InitialBackoff = %q, want empty (replaced entirely)", got.Retry.InitialBackoff)
		}
	})

	t.Run("base and override independence", func(t *testing.T) {
		base := &LLMSettings{
			Headers: map[string]string{"X-A": "a"},
		}
		override := &LLMSettings{
			Headers: map[string]string{"X-B": "b"},
		}

		got := MergeLLMSettings(base, override)
		// mutating got should not affect base or override
		got.Headers["X-C"] = "c"

		if base.Headers["X-C"] == "c" {
			t.Error("mutating merged result affected base")
		}
		if override.Headers["X-C"] == "c" {
			t.Error("mutating merged result affected override")
		}
	})

	t.Run("override headers and extra_params with nil base maps", func(t *testing.T) {
		base := &LLMSettings{
			Provider: "openai",
			// Headers and ExtraParams are nil
		}
		override := &LLMSettings{
			Headers:     map[string]string{"X-New": "value"},
			ExtraParams: map[string]any{"key": "val"},
		}

		got := MergeLLMSettings(base, override)
		if got.Headers == nil {
			t.Fatal("Headers should be non-nil after merge")
		}
		if got.Headers["X-New"] != "value" {
			t.Errorf("X-New = %q, want value", got.Headers["X-New"])
		}
		if got.ExtraParams == nil {
			t.Fatal("ExtraParams should be non-nil after merge")
		}
		if got.ExtraParams["key"] != "val" {
			t.Errorf("ExtraParams[key] = %v, want val", got.ExtraParams["key"])
		}
	})

	t.Run("provider change drops base extra_params", func(t *testing.T) {
		base := &LLMSettings{
			Provider:    "deepseek",
			ExtraParams: map[string]any{"thinking": map[string]any{"type": "enabled"}},
		}
		override := &LLMSettings{
			Provider:    "kimi",
			ExtraParams: map[string]any{"reasoning_effort": "max"},
		}

		got := MergeLLMSettings(base, override)
		if got.Provider != "kimi" {
			t.Errorf("Provider = %q, want kimi", got.Provider)
		}
		// base 的 ExtraParams（thinking）在 Provider 切换后应被清空
		if _, exists := got.ExtraParams["thinking"]; exists {
			t.Error("thinking from base should be dropped when provider changes")
		}
		// override 的 ExtraParams 应保留
		if got.ExtraParams["reasoning_effort"] != "max" {
			t.Errorf("reasoning_effort = %v, want max", got.ExtraParams["reasoning_effort"])
		}
	})

	t.Run("same provider preserves base extra_params", func(t *testing.T) {
		base := &LLMSettings{
			Provider:    "deepseek",
			ExtraParams: map[string]any{"thinking": map[string]any{"type": "enabled"}},
		}
		override := &LLMSettings{
			Model:       "deepseek-v4-flash",
			ExtraParams: map[string]any{"reasoning_effort": "max"},
		}

		got := MergeLLMSettings(base, override)
		// 同 Provider 不切换，base ExtraParams 应保留
		if _, exists := got.ExtraParams["thinking"]; !exists {
			t.Error("thinking from base should be preserved when provider unchanged")
		}
		// override ExtraParams 合并覆盖
		if got.ExtraParams["reasoning_effort"] != "max" {
			t.Errorf("reasoning_effort = %v, want max", got.ExtraParams["reasoning_effort"])
		}
	})

	t.Run("profiles merge override covers base", func(t *testing.T) {
		base := &LLMSettings{
			Profiles: map[string]*LLMSettings{
				"deepseek": {Model: "base-ds-model"},
				"kimi":     {Model: "base-kimi-model"},
			},
		}
		override := &LLMSettings{
			Profiles: map[string]*LLMSettings{
				"deepseek": {Model: "override-ds-model"},
				"openai":   {Model: "override-oai-model"},
			},
		}

		got := MergeLLMSettings(base, override)
		if got.Profiles == nil {
			t.Fatal("Profiles should not be nil")
		}
		// override 覆盖 base 同名
		if got.Profiles["deepseek"].Model != "override-ds-model" {
			t.Errorf("deepseek.Model = %q, want override-ds-model", got.Profiles["deepseek"].Model)
		}
		// base 独有的保留
		if got.Profiles["kimi"].Model != "base-kimi-model" {
			t.Errorf("kimi.Model = %q, want base-kimi-model", got.Profiles["kimi"].Model)
		}
		// override 独有的加入
		if got.Profiles["openai"].Model != "override-oai-model" {
			t.Errorf("openai.Model = %q, want override-oai-model", got.Profiles["openai"].Model)
		}
	})

	t.Run("profiles nil base", func(t *testing.T) {
		override := &LLMSettings{
			Profiles: map[string]*LLMSettings{
				"kimi": {Model: "kimi-k3"},
			},
		}
		got := MergeLLMSettings(nil, override)
		if got.Profiles["kimi"].Model != "kimi-k3" {
			t.Errorf("kimi.Model = %q, want kimi-k3", got.Profiles["kimi"].Model)
		}
	})
}


func TestResolveProfile(t *testing.T) {
	t.Run("profiles nil no-op", func(t *testing.T) {
		s := &LLMSettings{Provider: "kimi", Model: "keep-me"}
		s.ResolveProfile()
		if s.Model != "keep-me" {
			t.Errorf("Model = %q, want keep-me", s.Model)
		}
	})

	t.Run("no matching provider no-op", func(t *testing.T) {
		s := &LLMSettings{
			Provider: "kimi",
			Model:    "keep-me",
			Profiles: map[string]*LLMSettings{
				"deepseek": {Model: "ds-model"},
			},
		}
		s.ResolveProfile()
		if s.Model != "keep-me" {
			t.Errorf("Model = %q, want keep-me (no matching profile)", s.Model)
		}
	})

	t.Run("fills empty fields from profile", func(t *testing.T) {
		s := &LLMSettings{
			Provider: "kimi",
			Profiles: map[string]*LLMSettings{
				"kimi": {Model: "kimi-k3", BaseURL: "https://api.moonshot.cn/v1"},
			},
		}
		s.ResolveProfile()
		if s.Model != "kimi-k3" {
			t.Errorf("Model = %q, want kimi-k3", s.Model)
		}
		if s.BaseURL != "https://api.moonshot.cn/v1" {
			t.Errorf("BaseURL = %q", s.BaseURL)
		}
	})

	t.Run("profile overrides already-set fields", func(t *testing.T) {
		s := &LLMSettings{
			Provider: "kimi",
			Model:    "stale-model",
			Profiles: map[string]*LLMSettings{
				"kimi": {Model: "kimi-k3", SubModel: "kimi-flash"},
			},
		}
		s.ResolveProfile()
		if s.Model != "kimi-k3" {
			t.Errorf("Model = %q, want kimi-k3 (profile overrides top-level)", s.Model)
		}
		if s.SubModel != "kimi-flash" {
			t.Errorf("SubModel = %q, want kimi-flash", s.SubModel)
		}
	})

	t.Run("empty profile values do not overwrite", func(t *testing.T) {
		s := &LLMSettings{
			Provider: "kimi",
			Model:    "existing-model",
			Profiles: map[string]*LLMSettings{
				"kimi": {Model: ""},
			},
		}
		s.ResolveProfile()
		if s.Model != "existing-model" {
			t.Errorf("Model = %q, want existing-model (empty profile field should not overwrite)", s.Model)
		}
	})
}

// REGRESSION: 切换 provider 后顶层残留的上一 provider 的 model/base_url/api_key
// 必须被目标 profile 覆盖。旧实现顶层优先（仅顶层为空才从 profile 填充），
// 导致携带 kimi 的 base_url 请求 deepseek 端点
// （https://api.moonshot.cn/v1/v1/chat/completions）返回 404。
func TestRegression_ProfileOverridesStaleTopLevelOnProviderSwitch(t *testing.T) {
	t.Run("resolve profile on single settings", func(t *testing.T) {
		s := &LLMSettings{
			Provider: "deepseek", // 从 kimi 切换而来，顶层字段残留 kimi 配置
			APIKey:   "sk-kimi-key",
			Model:    "kimi-k3",
			BaseURL:  "https://api.moonshot.cn/v1",
			Profiles: map[string]*LLMSettings{
				"kimi": {
					APIKey:  "sk-kimi-key",
					Model:   "kimi-k3",
					BaseURL: "https://api.moonshot.cn/v1",
				},
				"deepseek": {
					APIKey:  "sk-deepseek-key",
					Model:   "deepseek-v4-pro",
					BaseURL: "https://api.deepseek.com",
				},
			},
		}
		s.ResolveProfile()
		if s.APIKey != "sk-deepseek-key" {
			t.Errorf("APIKey = %q, want sk-deepseek-key", s.APIKey)
		}
		if s.Model != "deepseek-v4-pro" {
			t.Errorf("Model = %q, want deepseek-v4-pro", s.Model)
		}
		if s.BaseURL != "https://api.deepseek.com" {
			t.Errorf("BaseURL = %q, want https://api.deepseek.com", s.BaseURL)
		}
	})

	t.Run("merged client uses profile of switched provider", func(t *testing.T) {
		dir := t.TempDir()
		globalPath := filepath.Join(dir, "global.json")
		projectPath := filepath.Join(dir, "project.json")

		global := `{"llm": {"api_key": "sk-kimi-key", "provider": "kimi", "model": "kimi-k3", "base_url": "https://api.moonshot.cn/v1"}}`
		project := `{"llm": {"provider": "deepseek", "profiles": {"deepseek": {"api_key": "sk-deepseek-key", "model": "deepseek-v4-pro", "base_url": "https://api.deepseek.com"}}}}`
		if err := os.WriteFile(globalPath, []byte(global), 0644); err != nil {
			t.Fatalf("WriteFile global: %v", err)
		}
		if err := os.WriteFile(projectPath, []byte(project), 0644); err != nil {
			t.Fatalf("WriteFile project: %v", err)
		}

		c, err := NewClientFromMergedSettings(globalPath, projectPath)
		if err != nil {
			t.Fatalf("NewClientFromMergedSettings: %v", err)
		}
		cl := c.(*client)
		if cl.config.Provider != ProviderDeepSeek {
			t.Errorf("Provider = %q, want deepseek", cl.config.Provider)
		}
		if cl.config.APIKey != "sk-deepseek-key" {
			t.Errorf("APIKey = %q, want sk-deepseek-key", cl.config.APIKey)
		}
		if cl.config.Model != "deepseek-v4-pro" {
			t.Errorf("Model = %q, want deepseek-v4-pro", cl.config.Model)
		}
		if cl.config.BaseURL != "https://api.deepseek.com" {
			t.Errorf("BaseURL = %q, want https://api.deepseek.com", cl.config.BaseURL)
		}
	})
}

// REGRESSION: /model 显式切换模型必须同步写入当前 provider 的 profile。
// 否则重启后 ResolveProfile 用 profile 旧值覆盖顶层，切换在重启后静默回退。
func TestRegression_SetModelSyncsProfile(t *testing.T) {
	s := &LLMSettings{
		Provider: "deepseek",
		Model:    "deepseek-v4-pro",
		Profiles: map[string]*LLMSettings{
			"deepseek": {Model: "deepseek-v4-pro"},
		},
	}
	s.SetModel("deepseek-v4-flash")
	if s.Model != "deepseek-v4-flash" {
		t.Errorf("Model = %q, want deepseek-v4-flash", s.Model)
	}
	if s.Profiles["deepseek"].Model != "deepseek-v4-flash" {
		t.Errorf("profile Model = %q, want deepseek-v4-flash", s.Profiles["deepseek"].Model)
	}
	// 模拟重启后的解析：显式选择的模型不被 profile 覆盖回退
	s.ResolveProfile()
	if s.Model != "deepseek-v4-flash" {
		t.Errorf("after ResolveProfile Model = %q, want deepseek-v4-flash", s.Model)
	}

	// 无匹配 profile 时仅设置顶层，不得 panic
	s2 := &LLMSettings{Provider: "kimi"}
	s2.SetModel("kimi-k3")
	if s2.Model != "kimi-k3" {
		t.Errorf("Model = %q, want kimi-k3", s2.Model)
	}
}

// REGRESSION: provider 切换时不继承 base 的 APIKey。
// 上一 provider 的凭据发往新 provider 既是 401，也是凭据跨域泄露。
func TestRegression_ProviderSwitchDoesNotInheritAPIKey(t *testing.T) {
	base := &LLMSettings{Provider: "kimi", APIKey: "sk-kimi-key", Timeout: "600s"}
	override := &LLMSettings{Provider: "deepseek"}
	got := MergeLLMSettings(base, override)
	if got.APIKey != "" {
		t.Errorf("APIKey = %q, want empty (must not inherit previous provider's key)", got.APIKey)
	}
	// Provider 无关字段仍继承
	if got.Timeout != "600s" {
		t.Errorf("Timeout = %q, want 600s", got.Timeout)
	}
}

// REGRESSION: 旧格式（无 profiles）下顶层字段必须保持可用，不破坏兼容性。
func TestRegression_OldFormatWithoutProfilesUsesTopLevel(t *testing.T) {
	s := &LLMSettings{
		Provider: "deepseek",
		APIKey:   "sk-old",
		Model:    "deepseek-old-model",
		BaseURL:  "https://api.deepseek.com",
	}
	s.ResolveProfile()
	if s.APIKey != "sk-old" {
		t.Errorf("APIKey = %q, want sk-old", s.APIKey)
	}
	if s.Model != "deepseek-old-model" {
		t.Errorf("Model = %q, want deepseek-old-model", s.Model)
	}
	if s.BaseURL != "https://api.deepseek.com" {
		t.Errorf("BaseURL = %q, want https://api.deepseek.com", s.BaseURL)
	}
}

// REGRESSION: 当前 provider 的 profile 只提供部分字段时，其余字段 fallback 到顶层。
// 确保用户可以在顶层放一个通用 key，profile 只放 model 等差异字段。
func TestRegression_ProfilePartialFieldsFallbackToTopLevel(t *testing.T) {
	s := &LLMSettings{
		Provider: "deepseek",
		APIKey:   "sk-top",
		Model:    "deepseek-top",
		BaseURL:  "https://top.example.com",
		Profiles: map[string]*LLMSettings{
			"deepseek": {Model: "deepseek-profile"},
		},
	}
	s.ResolveProfile()
	// profile 提供的 Model 覆盖顶层
	if s.Model != "deepseek-profile" {
		t.Errorf("Model = %q, want deepseek-profile", s.Model)
	}
	// profile 未提供的字段保留顶层 fallback
	if s.APIKey != "sk-top" {
		t.Errorf("APIKey = %q, want sk-top (should fallback to top-level)", s.APIKey)
	}
	if s.BaseURL != "https://top.example.com" {
		t.Errorf("BaseURL = %q, want https://top.example.com (should fallback to top-level)", s.BaseURL)
	}
}
func TestNewClientFromMergedSettings(t *testing.T) {
	dir := t.TempDir()

	t.Run("merge global and project", func(t *testing.T) {
		globalPath := filepath.Join(dir, "global.json")
		projectPath := filepath.Join(dir, "project.json")

		global := `{"llm": {"api_key": "global-key", "provider": "openai", "model": "gpt-4o"}}`
		project := `{"llm": {"api_key": "project-key", "model": "gpt-4o-mini"}}`

		if err := os.WriteFile(globalPath, []byte(global), 0644); err != nil {
			t.Fatalf("WriteFile global: %v", err)
		}
		if err := os.WriteFile(projectPath, []byte(project), 0644); err != nil {
			t.Fatalf("WriteFile project: %v", err)
		}

		c, err := NewClientFromMergedSettings(globalPath, projectPath)
		if err != nil {
			t.Fatalf("NewClientFromMergedSettings: %v", err)
		}

		cl := c.(*client)
		if cl.config.APIKey != "project-key" {
			t.Errorf("APIKey = %q, want project-key", cl.config.APIKey)
		}
		if cl.config.Model != "gpt-4o-mini" {
			t.Errorf("Model = %q, want gpt-4o-mini", cl.config.Model)
		}
		if cl.config.Provider != ProviderOpenAI {
			t.Errorf("Provider = %q, want openai", cl.config.Provider)
		}
	})

	t.Run("project only", func(t *testing.T) {
		projectPath := filepath.Join(dir, "project_only.json")
		project := `{"llm": {"api_key": "sk-project", "provider": "deepseek", "model": "deepseek-v4-flash"}}`
		if err := os.WriteFile(projectPath, []byte(project), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		c, err := NewClientFromMergedSettings(filepath.Join(dir, "no_global.json"), projectPath)
		if err != nil {
			t.Fatalf("NewClientFromMergedSettings: %v", err)
		}

		cl := c.(*client)
		if cl.config.APIKey != "sk-project" {
			t.Errorf("APIKey = %q, want sk-project", cl.config.APIKey)
		}
	})

	t.Run("neither has llm section returns error", func(t *testing.T) {
		globalPath := filepath.Join(dir, "no_llm_global.json")
		projectPath := filepath.Join(dir, "no_llm_project.json")

		if err := os.WriteFile(globalPath, []byte(`{}`), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if err := os.WriteFile(projectPath, []byte(`{"other": {}}`), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		_, err := NewClientFromMergedSettings(globalPath, projectPath)
		if err == nil {
			t.Fatal("expected error when neither config has llm section")
		}
	})
}

func TestWriteDefaultSettingsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// First write should succeed
	if err := WriteDefaultSettings(path); err != nil {
		t.Fatalf("first WriteDefaultSettings: %v", err)
	}

	// Read back and verify
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Second write should be a no-op (file exists)
	if err := WriteDefaultSettings(path); err != nil {
		t.Fatalf("second WriteDefaultSettings: %v", err)
	}

	// Content should be unchanged
	data2, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != string(data2) {
		t.Error("WriteDefaultSettings modified an existing file")
	}
}

// REGRESSION: SubModel 不再自动配对，完全由配置决定。
// 当 SubModel 为空时，NewClientFromLLMSettings 不应对其赋值。
func TestNewClientFromLLMSettings_SubModelNotAutoPaired(t *testing.T) {
	settings := &LLMSettings{
		Provider: "deepseek",
		Model:    "deepseek-v4-pro",
		APIKey:   "sk-test-key-for-submodel-check",
	}
	// SubModel 未配置时应保持为空，不再自动配对
	if settings.SubModel != "" {
		t.Errorf("SubModel should remain empty when not configured, got %q", settings.SubModel)
	}
}

// REGRESSION: SubModel 由配置显式指定，不应被覆盖。
func TestSubModel_RespectsExplicitConfig_Flash(t *testing.T) {
	settings := &LLMSettings{
		Provider: "deepseek",
		Model:    "deepseek-v4-flash",
		SubModel: "custom-flash-model",
	}
	if settings.SubModel != "custom-flash-model" {
		t.Errorf("SubModel should respect explicit config, got %q", settings.SubModel)
	}
}

// REGRESSION: SubModel 由配置显式指定，不应被覆盖。
func TestSubModel_RespectsExplicitConfig_Pro(t *testing.T) {
	settings := &LLMSettings{
		Provider: "deepseek",
		Model:    "deepseek-v4-pro",
		SubModel: "custom-sub-model",
	}
	if settings.SubModel != "custom-sub-model" {
		t.Errorf("SubModel should respect explicit config, got %q", settings.SubModel)
	}
}

// REGRESSION: non-DeepSeek provider — SubModel 可自由配置，无强制逻辑。
func TestSubModel_NonDeepSeek_FreeConfig(t *testing.T) {
	settings := &LLMSettings{
		Provider: "openai",
		Model:    "gpt-4o",
		SubModel: "gpt-4o-mini",
	}
	if settings.SubModel != "gpt-4o-mini" {
		t.Errorf("SubModel should respect explicit config for any provider, got %q", settings.SubModel)
	}
}

func TestAdvisorMode_IsAdvisorMode(t *testing.T) {
	tests := []struct {
		name     string
		settings *LLMSettings
		want     bool
	}{
		{
			name:     "Mode=advisor, SubModel=flash, Model=pro → true",
			settings: &LLMSettings{Mode: "advisor", SubModel: "flash", Model: "pro"},
			want:     true,
		},
		{
			name:     "Mode=normal, SubModel=flash, Model=pro → false",
			settings: &LLMSettings{Mode: "normal", SubModel: "flash", Model: "pro"},
			want:     false,
		},
		{
			name:     "Mode=advisor, SubModel=empty, Model=pro → false",
			settings: &LLMSettings{Mode: "advisor", SubModel: "", Model: "pro"},
			want:     false,
		},
		{
			name:     "Mode=advisor, SubModel=pro, Model=pro → false (same model)",
			settings: &LLMSettings{Mode: "advisor", SubModel: "pro", Model: "pro"},
			want:     false,
		},
		{
			name:     "Mode=default, SubModel=flash, Model=pro → false",
			settings: &LLMSettings{Mode: "", SubModel: "flash", Model: "pro"},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.settings.IsAdvisorMode()
			if got != tt.want {
				t.Errorf("IsAdvisorMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAdvisorMode_MergeLLMSettings_PreservesMode(t *testing.T) {
	t.Run("global Mode=advisor, project Mode=empty → merged Mode=advisor", func(t *testing.T) {
		global := &LLMSettings{Mode: "advisor"}
		project := &LLMSettings{Mode: ""}
		merged := MergeLLMSettings(global, project)
		if merged.Mode != "advisor" {
			t.Errorf("merged.Mode = %q, want %q", merged.Mode, "advisor")
		}
	})

	t.Run("global Mode=normal, project Mode=advisor → merged Mode=advisor (project overrides)", func(t *testing.T) {
		global := &LLMSettings{Mode: "normal"}
		project := &LLMSettings{Mode: "advisor"}
		merged := MergeLLMSettings(global, project)
		if merged.Mode != "advisor" {
			t.Errorf("merged.Mode = %q, want %q", merged.Mode, "advisor")
		}
	})

	t.Run("both Mode=empty → merged Mode=empty", func(t *testing.T) {
		global := &LLMSettings{Mode: ""}
		project := &LLMSettings{Mode: ""}
		merged := MergeLLMSettings(global, project)
		if merged.Mode != "" {
			t.Errorf("merged.Mode = %q, want empty", merged.Mode)
		}
	})
}

func TestAdvisorMode_DefaultSettings_Mode(t *testing.T) {
	settings := DefaultSettings()
	if settings.Mode != "normal" {
		t.Errorf("DefaultSettings().Mode = %q, want %q", settings.Mode, "normal")
	}
}
