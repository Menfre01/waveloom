package llm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// --- settings.json 结构 ---

// LLMSettings 对应 settings.json 中的顶层 llm 配置块。
// 所有 LLM Client 的构造参数均通过此结构表达，支持任意嵌套的 extra_params。
type LLMSettings struct {
	APIKey      string            `json:"api_key"`     // API Key；为空时回退到 LLM_API_KEY 环境变量
	Provider    string            `json:"provider"`    // "openai" / "deepseek"，默认 "openai"
	Model       string            `json:"model"`       // 模型名称
	BaseURL     string            `json:"base_url"`    // API 端点，留空使用默认
	Timeout     string            `json:"timeout"`     // 单次请求超时，Go Duration 格式（如 "600s"），默认 600s
	Retry       *RetrySettings    `json:"retry"`       // 重试策略，留空使用默认
	Headers     map[string]string `json:"headers"`     // 自定义 HTTP 请求头
	ExtraParams map[string]any    `json:"extra_params"` // Provider 特有参数，支持任意嵌套
}

// RetrySettings 对应 settings.json 中的 retry 配置块。
type RetrySettings struct {
	MaxRetries     int    `json:"max_retries"`     // 最大重试次数
	InitialBackoff string `json:"initial_backoff"` // 初始退避时间，Go Duration 格式
	MaxBackoff     string `json:"max_backoff"`     // 最大退避时间，Go Duration 格式
	Multiplier     float64 `json:"multiplier"`     // 退避乘数
}

// --- 顶层设置文件结构 ---

// settingsFile 是 settings.json 文件的顶层结构。
type settingsFile struct {
	LLM *LLMSettings `json:"llm"`
}

// --- 加载函数 ---

// LoadSettings 从文件路径加载 settings.json，返回 LLM 配置块。
func LoadSettings(path string) (*LLMSettings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading settings file %s: %w", path, err)
	}

	var sf settingsFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("parsing settings file %s: %w", path, err)
	}

	if sf.LLM == nil {
		return nil, fmt.Errorf("settings file %s: missing \"llm\" section", path)
	}

	return sf.LLM, nil
}

// LoadSettingsIfExists 从文件路径加载 LLM 配置，文件不存在或无 llm 段时返回 nil。
// 仅 JSON 解析错误时返回 error。
func LoadSettingsIfExists(path string) (*LLMSettings, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading settings file %s: %w", path, err)
	}

	var sf settingsFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("parsing settings file %s: %w", path, err)
	}

	return sf.LLM, nil
}

// NewClientFromSettings 从 settings.json 文件构造 Client。
func NewClientFromSettings(path string) (Client, error) {
	settings, err := LoadSettings(path)
	if err != nil {
		return nil, err
	}

	client, _, err := NewClientFromLLMSettings(settings)
	return client, err
}

// --- 合并逻辑 ---

// MergeLLMSettings 合并两个 LLMSettings，override 字段优先。
// base / override 可为 nil。返回新分配的 *LLMSettings（不修改入参）。
func MergeLLMSettings(base, override *LLMSettings) *LLMSettings {
	if base == nil && override == nil {
		return nil
	}
	if base == nil {
		return override
	}
	if override == nil {
		return base
	}

	merged := &LLMSettings{
		APIKey:   base.APIKey,
		Provider: base.Provider,
		Model:    base.Model,
		BaseURL:  base.BaseURL,
		Timeout:  base.Timeout,
		Retry:    base.Retry,
	}

	// 深拷贝 Headers
	if base.Headers != nil {
		merged.Headers = make(map[string]string, len(base.Headers))
		for k, v := range base.Headers {
			merged.Headers[k] = v
		}
	}

	// 深拷贝 ExtraParams
	if base.ExtraParams != nil {
		merged.ExtraParams = make(map[string]any, len(base.ExtraParams))
		for k, v := range base.ExtraParams {
			merged.ExtraParams[k] = v
		}
	}

	// override 字段覆盖
	if override.APIKey != "" {
		merged.APIKey = override.APIKey
	}
	if override.Provider != "" {
		merged.Provider = override.Provider
	}
	if override.Model != "" {
		merged.Model = override.Model
	}
	if override.BaseURL != "" {
		merged.BaseURL = override.BaseURL
	}
	if override.Timeout != "" {
		merged.Timeout = override.Timeout
	}
	if override.Retry != nil {
		merged.Retry = override.Retry
	}
	if override.Headers != nil {
		if merged.Headers == nil {
			merged.Headers = make(map[string]string)
		}
		for k, v := range override.Headers {
			merged.Headers[k] = v
		}
	}
	if override.ExtraParams != nil {
		if merged.ExtraParams == nil {
			merged.ExtraParams = make(map[string]any)
		}
		for k, v := range override.ExtraParams {
			merged.ExtraParams[k] = v
		}
	}

	return merged
}

// NewClientFromMergedSettings 合并全局和项目配置文件构造 Client。
// 项目配置字段覆盖全局。若均无 llm 段则返回错误。
func NewClientFromMergedSettings(globalPath, projectPath string) (Client, error) {
	globalSettings, err := LoadSettingsIfExists(globalPath)
	if err != nil {
		return nil, err
	}
	projectSettings, err := LoadSettingsIfExists(projectPath)
	if err != nil {
		return nil, err
	}

	merged := MergeLLMSettings(globalSettings, projectSettings)
	if merged == nil {
		return nil, &NonRetryableError{Message: "no valid settings found (neither global nor project config has \"llm\" section)"}
	}
	client, _, err := NewClientFromLLMSettings(merged)
	return client, err
}

// NewClientFromLLMSettings 从 LLMSettings 构造 Client。
// API Key 优先使用 settings.api_key，为空时回退到 LLM_API_KEY 环境变量。
func NewClientFromLLMSettings(settings *LLMSettings) (Client, ClientConfig, error) {
	if settings == nil {
		return nil, ClientConfig{}, &NonRetryableError{Message: "settings must not be nil"}
	}

	apiKey := settings.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("LLM_API_KEY")
	}
	if apiKey == "" {
		return nil, ClientConfig{}, &NonRetryableError{Message: "api_key is required (set in settings.json or LLM_API_KEY env var)"}
	}

	timeout, err := parseDuration(settings.Timeout, 600*time.Second)
	if err != nil {
		return nil, ClientConfig{}, &NonRetryableError{Message: fmt.Sprintf("invalid timeout: %v", err)}
	}

	retryPolicy := DefaultRetryPolicy()
	if settings.Retry != nil {
		retryPolicy = parseRetrySettings(settings.Retry)
	}

	cfg := ClientConfig{
		Provider:    ProviderType(settings.Provider),
		APIKey:      apiKey,
		Model:       settings.Model,
		BaseURL:     settings.BaseURL,
		ExtraParams: settings.ExtraParams,
		RetryPolicy: retryPolicy,
		Timeout:     timeout,
		Headers:     settings.Headers,
	}

	client, err := NewClient(cfg)
	if err != nil {
		return nil, ClientConfig{}, err
	}
	return client, cfg, nil
}

// DefaultSettings 返回推荐的默认 LLM 配置（DeepSeek + 思考模式）。
func DefaultSettings() *LLMSettings {
	return &LLMSettings{
		Provider: "deepseek",
		Model:    "deepseek-v4-pro",
		BaseURL:  "https://api.deepseek.com",
		Timeout:  "600s",
		Retry: &RetrySettings{
			MaxRetries:     3,
			InitialBackoff: "1s",
			MaxBackoff:     "30s",
			Multiplier:     2.0,
		},
		ExtraParams: map[string]any{
			"thinking":         map[string]any{"type": "enabled"},
			"reasoning_effort": "max",
		},
	}
}

// WriteDefaultSettings 将默认配置文件写入指定路径。
// 自动创建父目录。如果文件已存在则不做任何操作并返回 nil。
func WriteDefaultSettings(path string) error {
	// 检查文件是否已存在
	if _, err := os.Stat(path); err == nil {
		return nil // 已存在，不覆盖
	}

	return writeSettingsFile(path, DefaultSettings())
}

// WriteSettingsFile 将给定的 LLMSettings 写入配置文件。
// 自动创建父目录，覆盖已有文件。
func WriteSettingsFile(path string, settings *LLMSettings) error {
	return writeSettingsFile(path, settings)
}

// writeSettingsFile 内部写入逻辑。
func writeSettingsFile(path string, settings *LLMSettings) error {
	// 确保父目录存在
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	sf := settingsFile{LLM: settings}
	data, err := json.MarshalIndent(sf, "", "    ")
	if err != nil {
		return fmt.Errorf("marshaling settings: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing settings to %s: %w", path, err)
	}

	return nil
}

// parseDuration 解析 duration 字符串，返回默认值当为空。
func parseDuration(s string, defaultVal time.Duration) (time.Duration, error) {
	if s == "" {
		return defaultVal, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	return d, nil
}

// parseRetrySettings 从 RetrySettings 构造 RetryPolicy，缺失字段使用默认值。
func parseRetrySettings(rs *RetrySettings) RetryPolicy {
	if rs == nil {
		return DefaultRetryPolicy()
	}
	policy := DefaultRetryPolicy()

	if rs.MaxRetries > 0 {
		policy.MaxRetries = rs.MaxRetries
	}
	if rs.Multiplier > 0 {
		policy.Multiplier = rs.Multiplier
	}

	if d, err := parseDuration(rs.InitialBackoff, 0); err == nil && d > 0 {
		policy.InitialBackoff = d
	}
	if d, err := parseDuration(rs.MaxBackoff, 0); err == nil && d > 0 {
		policy.MaxBackoff = d
	}

	return policy
}
