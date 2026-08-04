package sandbox

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// ============================================================================
// 网络模式
// ============================================================================

const (
	// NetworkModeOff 全断网(--unshare-net,默认)。
	NetworkModeOff = "off"
	// NetworkModeOn 沙箱内直连网络(v1,需显式配置凭据防护)。
	NetworkModeOn = "on"
	// NetworkModeProxy 沙箱外本地代理 + 域名过滤(v2,未实现)。
	NetworkModeProxy = "proxy"
)

// ============================================================================
// Config — settings.json 的 sandbox 段
// ============================================================================

// Config 是沙箱的完整配置。
type Config struct {
	// Enabled 是否启用沙箱。TUI 常规模式需显式 true;
	// --bypass-permissions / ACP 无交互自动激活时无需手动置 true。
	Enabled bool `json:"enabled"`

	// FailIfUnavailable 依赖缺失(bwrap 未装/能力不足)时是否硬失败。
	// false = 警告 + 降级运行;true = 拒绝执行。
	FailIfUnavailable bool `json:"failIfUnavailable"`

	// AllowUnsandboxedCommands 沙箱内命令失败时是否允许用户逃逸到沙箱外重试。
	// 默认 true。nil 表示未设置,按 true 处理。
	AllowUnsandboxedCommands *bool `json:"allowUnsandboxedCommands"`

	// ExcludedCommands 逃逸命令列表(前缀/精确/通配)。命中命令不进沙箱,
	// 但仍过权限系统,且不享受 autoAllow 二元决策。
	ExcludedCommands []string `json:"excludedCommands"`

	Network      NetworkConfig      `json:"network"`
	Filesystem   FilesystemConfig   `json:"filesystem"`
	Capabilities CapabilitiesConfig `json:"capabilities"`
	Credentials  CredentialsConfig  `json:"credentials"`
	// Env 沙箱内注入的环境变量(通用机制,不绑定任何工具)。
	// 典型用途:构建工具缓存重定向——如 go 的
	// {"GOPATH": "./.waveloom-gopath", "GOMODCACHE": "./.waveloom-gomodcache",
	//  "GOCACHE": "./.waveloom-gocache"}(./ 展开为 workspace 下可写路径),
	// npm 的 {"npm_config_cache": "./.waveloom-npm-cache"} 等。
	// 值支持路径前缀语义(~ 家目录、// 绝对、/ 绝对、./ workspace 相对);
	// 其他(裸名/URL 等)按字面量注入(如 GOPROXY)。
	// 安全:键命中凭据剥离清单(如 *_API_KEY)时被忽略——剥离优先,
	// 防止配置回填被剥离的敏感变量。
	Env map[string]string `json:"env"`
}

// NetworkConfig 网络隔离策略。
type NetworkConfig struct {
	// Mode: off / on / proxy(v2 未实现)。
	Mode string `json:"mode"`
	// AllowedDomains 域名白名单(仅 proxy 模式使用,v2)。
	AllowedDomains []string `json:"allowedDomains"`
}

// FilesystemConfig 文件系统边界。
type FilesystemConfig struct {
	// AllowWrite 额外可写路径(默认仅工作区)。
	// 路径前缀语义://abs 绝对路径、~/ 家目录、./ 或裸名 项目根。
	AllowWrite []string `json:"allowWrite"`
	// AllowRead 已废弃(2026-09:默认读遮蔽移除后无作用对象)。
	// 保留字段仅为解析旧配置并告警,不再有任何行为。
	// Deprecated: 使用 denyRead / credentials.files 显式遮蔽。
	AllowRead []string `json:"allowRead"`
	// DenyRead 遮蔽(不可读)路径。默认不遮蔽任何路径,需显式配置;
	// 推荐清单(云凭证/shell 启动文件等)见 docs/settings.md。
	DenyRead []string `json:"denyRead"`
}

// CapabilitiesConfig 内核能力配置。
type CapabilitiesConfig struct {
	// Keep --cap-drop ALL 后加回的能力列表(如 net_raw 供 ping)。
	Keep []string `json:"keep"`
}

// CredentialsConfig 凭据防护(网络 on 时强烈建议;可选加固,不阻塞启用)。
type CredentialsConfig struct {
	// Files 额外遮蔽(不可读)的凭据文件/目录。
	Files []string `json:"files"`
	// EnvVars 额外剥离的环境变量名,与默认 glob 剥离叠加。
	EnvVars []string `json:"envVars"`
}

// ============================================================================
// 默认值与解析
// ============================================================================

// DefaultConfig 返回沙箱默认配置:不启用、降级运行、允许逃逸、断网。
func DefaultConfig() *Config {
	allow := true
	return &Config{
		Enabled:                  false,
		FailIfUnavailable:        false,
		AllowUnsandboxedCommands: &allow,
		Network: NetworkConfig{
			Mode: NetworkModeOff,
		},
	}
}

// LoadConfig 从 JSON 解析沙箱配置并校验。
// 校验失败返回 error(调用方应拒绝启用,不静默降级)。
func LoadConfig(data []byte) (*Config, error) {
	cfg := DefaultConfig()
	if len(data) > 0 {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("sandbox config: invalid json: %w", err)
		}
	}
	if len(cfg.Filesystem.AllowRead) > 0 {
		// 2026-09:allowRead 废弃(默认读遮蔽移除后无作用对象)。
		// 告警并忽略——不阻断旧配置加载,但提示用户迁移到 denyRead。
		slog.Warn("sandbox config: filesystem.allowRead is deprecated and ignored; use filesystem.denyRead / credentials.files instead", "allowRead", cfg.Filesystem.AllowRead)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// AllowUnsandboxed 返回是否允许沙箱外逃逸(未设置时默认 true)。
func (c *Config) AllowUnsandboxed() bool {
	if c.AllowUnsandboxedCommands == nil {
		return true
	}
	return *c.AllowUnsandboxedCommands
}

// Validate 校验配置一致性。违规返回 error(fail-closed)。
//
// 校验规则:
//  1. network.mode 必须是 off/on/proxy(proxy 为 v2,当前拒绝)
//  2. excludedCommands 不允许空条目
//
// 注:沙箱默认不遮蔽任何路径(2026-09 决策,对齐 Claude Code / Codex)。
// 网络 on 的凭据遮蔽(credentials.files / filesystem.denyRead)为**可选加固**,
// 不阻塞启用;未显式遮蔽时由调用方提示用户
// ("网络打开后能读就能外传,配置遮蔽更安全",见 cmd/waveloom/sandbox_setup.go)。
func (c *Config) Validate() error {
	switch c.Network.Mode {
	case NetworkModeOff, NetworkModeOn:
	case NetworkModeProxy:
		return fmt.Errorf("sandbox config: network mode %q is v2 (not implemented yet)", NetworkModeProxy)
	default:
		return fmt.Errorf("sandbox config: invalid network mode %q (want off/on)", c.Network.Mode)
	}

	for _, cmd := range c.ExcludedCommands {
		if strings.TrimSpace(cmd) == "" {
			return fmt.Errorf("sandbox config: excludedCommands contains empty entry")
		}
	}
	for k, v := range c.Env {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("sandbox config: env contains empty key")
		}
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("sandbox config: env.%s has empty value", k)
		}
		if !validEnvName(k) {
			return fmt.Errorf("sandbox config: env key %q is not a valid environment variable name", k)
		}
	}

	return nil
}

// validEnvName 校验环境变量名格式(字母/下划线开头,后跟字母/数字/下划线)。
func validEnvName(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		ok := r == '_' ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(i > 0 && r >= '0' && r <= '9')
		if !ok {
			return false
		}
	}
	return true
}
