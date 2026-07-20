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
	APIKey      string                     `json:"api_key,omitempty"` // API Key；为空时回退到 LLM_API_KEY 环境变量
	Provider    string                     `json:"provider"`          // "openai" / "deepseek" / "kimi"，默认 "deepseek"
	Model       string                     `json:"model,omitempty"`        // 模型名称（主模型）
	SubModel    string                     `json:"sub_model,omitempty"`    // subagent 默认模型
	Mode        string                     `json:"mode,omitempty"`         // "normal" / "advisor"，默认 "normal"
	BaseURL     string                     `json:"base_url,omitempty"`     // API 端点，留空使用默认
	Timeout     string                     `json:"timeout,omitempty"`      // 单次请求超时，Go Duration 格式（如 "600s"），默认 600s
	Retry       *RetrySettings             `json:"retry,omitempty"`        // 重试策略，留空使用默认
	Headers     map[string]string          `json:"headers,omitempty"`      // 自定义 HTTP 请求头
	ExtraParams map[string]any             `json:"extra_params,omitempty"` // Provider 特有参数，支持任意嵌套
	Profiles    map[string]*LLMSettings    `json:"profiles,omitempty"`     // 多 Provider 配置，以 provider 名为键
}

// IsAdvisorMode 判断当前是否启用 advisor mode。
// 条件：mode == "advisor" && SubModel 非空 && SubModel != Model。
func (s *LLMSettings) IsAdvisorMode() bool {
	return s.Mode == "advisor" && s.SubModel != "" && s.SubModel != s.Model
}

// ResolveProfile 用当前 provider 的 profile 字段覆盖顶层字段。
// profiles 为空或无匹配 provider 时不做任何操作（向下兼容旧配置文件）。
// Profile 是 provider 专属配置，优先级高于顶层通用字段：切换 provider 后
// 顶层残留的上一 provider 的 model/base_url/api_key 必须被 profile 覆盖，
// 否则会携带错误的 base_url/model 请求新 provider（典型表现为 404）。
// Provider 无关字段（Timeout/Retry/Headers/Mode）不从 profile 读取。
// 若调用方需要保留显式指定的字段（如 CLI --model），应在 ResolveProfile 之后再赋值。
func (s *LLMSettings) ResolveProfile() {
	if s.Profiles == nil {
		return
	}
	p, ok := s.Profiles[s.Provider]
	if !ok || p == nil {
		return
	}
	if p.APIKey != "" {
		s.APIKey = p.APIKey
	}
	if p.Model != "" {
		s.Model = p.Model
	}
	if p.SubModel != "" {
		s.SubModel = p.SubModel
	}
	if p.BaseURL != "" {
		s.BaseURL = p.BaseURL
	}
	s.ExtraParams = p.ExtraParams
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

// SetModel 设置主模型，并同步更新当前 provider 的 profile（若存在）。
// 用于 /model 显式切换模型的场景：保证切换持久化后，下次启动
// ResolveProfile 时 profile 不会把顶层 Model 覆盖回旧值。
func (s *LLMSettings) SetModel(name string) {
	s.Model = name
	if p, ok := s.Profiles[s.Provider]; ok && p != nil {
		p.Model = name
	}
}

// NewClientFromSettings 从 settings.json 文件构造 Client。
func NewClientFromSettings(path string) (Client, error) {
	settings, err := LoadSettings(path)
	if err != nil {
		return nil, err
	}

	settings.ResolveProfile()
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

	// Provider 切换检测：当项目 Provider 与全局不同时，以项目配置为准，
	// 仅继承 Provider 无关的字段（Timeout、Retry、Headers）。
	// 空 Provider 与 NewClient 的默认对齐，按 deepseek 处理，避免误判切换。
	baseProvider := base.Provider
	if baseProvider == "" {
		baseProvider = string(ProviderDeepSeek)
	}
	providerChanged := override.Provider != "" && override.Provider != baseProvider

	var merged *LLMSettings
	if providerChanged {
		merged = &LLMSettings{
			APIKey:      override.APIKey,
			Provider:    override.Provider,
			Model:       override.Model,
			SubModel:    override.SubModel,
			Mode:        override.Mode,
			BaseURL:     override.BaseURL,
			Timeout:     override.Timeout,
			Retry:       override.Retry,
			ExtraParams: nil,
		}
		// Provider 无关字段：override 为空时继承 base
		if merged.Timeout == "" {
			merged.Timeout = base.Timeout
		}
		if merged.Mode == "" {
			merged.Mode = base.Mode
		}
		if merged.Retry == nil {
			merged.Retry = base.Retry
		}
		// Headers: base + override 合并（Provider 无关）
		if base.Headers != nil {
			merged.Headers = make(map[string]string, len(base.Headers))
			for k, v := range base.Headers {
				merged.Headers[k] = v
			}
		}
	} else {
		merged = &LLMSettings{
			APIKey:   base.APIKey,
			Provider: base.Provider,
			Model:    base.Model,
			SubModel: base.SubModel,
			Mode:     base.Mode,
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
		// 深拷贝 ExtraParams（同 Provider 才继承）
		if base.ExtraParams != nil {
			merged.ExtraParams = make(map[string]any, len(base.ExtraParams))
			for k, v := range base.ExtraParams {
				merged.ExtraParams[k] = v
			}
		}
	}
	if !providerChanged {
		// 同 Provider：override 标量字段覆盖 base
		if override.APIKey != "" {
			merged.APIKey = override.APIKey
		}
		if override.Provider != "" {
			merged.Provider = override.Provider
		}
		if override.Model != "" {
			merged.Model = override.Model
		}
		if override.SubModel != "" {
			merged.SubModel = override.SubModel
		}
		if override.Mode != "" {
			merged.Mode = override.Mode
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
	}
	// Headers 合并（Provider 无关，两种路径均执行）
	if override.Headers != nil {
		if merged.Headers == nil {
			merged.Headers = make(map[string]string)
		}
		for k, v := range override.Headers {
			merged.Headers[k] = v
		}
	}
	// ExtraParams 合并（Provider 无关，两种路径均执行）
	if override.ExtraParams != nil {
		if merged.ExtraParams == nil {
			merged.ExtraParams = make(map[string]any)
		}
		for k, v := range override.ExtraParams {
			merged.ExtraParams[k] = v
		}
	}


	// Profiles 合并：override 覆盖 base 同名 profile，base 独有的保留。
	// 深拷贝每个 profile，避免调用方修改 merged 时污染 base/override 的原始对象。
	if base.Profiles != nil || override.Profiles != nil {
		merged.Profiles = make(map[string]*LLMSettings)
		for k, v := range base.Profiles {
			merged.Profiles[k] = copyProfile(v)
		}
		for k, v := range override.Profiles {
			merged.Profiles[k] = copyProfile(v)
		}
	}
	return merged
}

// copyProfile 深拷贝一个 LLMSettings profile（用于 Profiles 合并）。
// 复制标量字段，并对 Headers/ExtraParams/Retry 做深拷贝。
func copyProfile(p *LLMSettings) *LLMSettings {
	if p == nil {
		return nil
	}
	cp := &LLMSettings{
		APIKey:      p.APIKey,
		Provider:    p.Provider,
		Model:       p.Model,
		SubModel:    p.SubModel,
		Mode:        p.Mode,
		BaseURL:     p.BaseURL,
		Timeout:     p.Timeout,
		ExtraParams: nil,
		Profiles:    nil,
	}
	if p.Retry != nil {
		rp := *p.Retry
		cp.Retry = &rp
	}
	if p.Headers != nil {
		cp.Headers = make(map[string]string, len(p.Headers))
		for k, v := range p.Headers {
			cp.Headers[k] = v
		}
	}
	if p.ExtraParams != nil {
		cp.ExtraParams = make(map[string]any, len(p.ExtraParams))
		for k, v := range p.ExtraParams {
			cp.ExtraParams[k] = v
		}
	}
	return cp
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
	merged.ResolveProfile()
	client, _, err := NewClientFromLLMSettings(merged)
	return client, err
}

// NewClientFromLLMSettings 从 LLMSettings 构造 Client。
// API Key 优先使用 settings.api_key，为空时回退到 LLM_API_KEY 环境变量。
// 注意：本函数不调用 ResolveProfile。若 settings 配置了 profiles，
// 调用方须先调用 ResolveProfile 再传入（需要保留显式指定字段如
// CLI --model 时，应在 ResolveProfile 之后再赋值）。
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
		SubModel: "deepseek-v4-flash",
		Mode:     "normal",
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
