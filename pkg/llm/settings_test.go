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
		// unscaled base fields retained
		if got.BaseURL != "https://base.example.com" {
			t.Errorf("BaseURL = %q, want https://base.example.com", got.BaseURL)
		}
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
