package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// ---------------------------------------------------------------------------
// LoadConfigs — 多源配置加载与合并
// ---------------------------------------------------------------------------

// LoadConfigs 从所有来源加载 MCP Server 配置并合并。
// cwd 是当前工作目录，homeDir 是用户主目录。
// 返回按名称去重合并后的配置映射（name → ServerConfig）。
func LoadConfigs(cwd, homeDir string) map[string]ServerConfig {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if homeDir == "" {
		homeDir, _ = os.UserHomeDir()
	}

	var sources []map[string]ServerConfig

	// 按优先级从低到高加载（后面的覆盖前面的同名 server）

	// 6. Claude 桌面版配置（自动发现 Claude 桌面版已安装的 MCP server）
	if homeDir != "" {
		if servers := loadClaudeDesktopConfig(homeDir); len(servers) > 0 {
			sources = append(sources, servers)
		}
	}

	// 5. ~/.claude.json → mcpServers（Claude Code 用户级）
	if homeDir != "" {
		if servers := loadClaudeJSON(homeDir, ""); len(servers) > 0 {
			sources = append(sources, servers)
		}
	}

	// 4. ~/.claude.json → projects.<cwd>（Claude Code 本地级）
	if homeDir != "" && cwd != "" {
		if servers := loadClaudeJSON(homeDir, cwd); len(servers) > 0 {
			sources = append(sources, servers)
		}
	}

	// 3. ~/.waveloom.json → mcpServers（Waveloom 用户级）
	if homeDir != "" {
		if servers := loadWaveloomJSON(homeDir, ""); len(servers) > 0 {
			sources = append(sources, servers)
		}
	}

	// 2. ~/.waveloom.json → projects.<cwd>（Waveloom 本地级）
	if homeDir != "" && cwd != "" {
		if servers := loadWaveloomJSON(homeDir, cwd); len(servers) > 0 {
			sources = append(sources, servers)
		}
	}

	// 1. .mcp.json（项目级，最高优先级）
	if cwd != "" {
		if servers := loadMCPJSON(cwd); len(servers) > 0 {
			sources = append(sources, servers)
		}
	}

	return mergeConfigs(sources)
}

// mergeConfigs 按名称合并配置，后面的覆盖前面的。返回 name → ServerConfig 映射。
func mergeConfigs(sources []map[string]ServerConfig) map[string]ServerConfig {
	merged := make(map[string]ServerConfig)
	for _, src := range sources {
		for name, cfg := range src {
			merged[name] = cfg
		}
	}

	for name, cfg := range merged {
		cfg.Name = name
		expandServerConfig(&cfg)
		merged[name] = cfg
	}

	return merged
}

// ---------------------------------------------------------------------------
// 单个来源加载
// ---------------------------------------------------------------------------

// loadMCPJSON 加载 <project>/.mcp.json。
func loadMCPJSON(projectDir string) map[string]ServerConfig {
	path := filepath.Join(projectDir, ".mcp.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg MCPConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return cfg.MCPServers
}

// loadWaveloomJSON 加载 ~/.waveloom.json。
// projectPath 为空时加载用户级（mcpServers），非空时加载本地级（projects.<projectPath>.mcpServers）。
func loadWaveloomJSON(homeDir, projectPath string) map[string]ServerConfig {
	path := filepath.Join(homeDir, ".waveloom.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	if projectPath != "" {
		// 加载 projects.<cwd>.mcpServers
		var cfg ClaudeJSONFile // 复用 Claude 格式（结构一致）
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil
		}
		if entry, ok := cfg.Projects[projectPath]; ok {
			return entry.MCPServers
		}
		return nil
	}

	// 加载顶级 mcpServers
	var cfg MCPConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return cfg.MCPServers
}

// loadClaudeDesktopConfig 加载 Claude 桌面版配置文件。
func loadClaudeDesktopConfig(homeDir string) map[string]ServerConfig {
	path := claudeDesktopConfigPath(homeDir)
	if path == "" {
		return nil
	}
	return loadFlatMCPConfig(path)
}

// claudeDesktopConfigPath 返回 Claude 桌面版配置文件路径（按平台）。
func claudeDesktopConfigPath(homeDir string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(homeDir, "AppData", "Roaming")
		}
		return filepath.Join(appData, "Claude", "claude_desktop_config.json")
	default: // linux
		return filepath.Join(homeDir, ".config", "Claude", "claude_desktop_config.json")
	}
}

// loadFlatMCPConfig 加载扁平结构的 MCP 配置文件（顶层 mcpServers 键）。
func loadFlatMCPConfig(path string) map[string]ServerConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg MCPConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return cfg.MCPServers
}

// loadClaudeJSON 加载 ~/.claude.json。
// projectPath 为空时加载用户级（mcpServers），非空时加载本地级（projects.<projectPath>.mcpServers）。
func loadClaudeJSON(homeDir, projectPath string) map[string]ServerConfig {
	path := filepath.Join(homeDir, ".claude.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var cfg ClaudeJSONFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}

	if projectPath != "" {
		if entry, ok := cfg.Projects[projectPath]; ok {
			return entry.MCPServers
		}
		return nil
	}

	return cfg.MCPServers
}

// ---------------------------------------------------------------------------
// 配置管理 — 添加和删除 server
// ---------------------------------------------------------------------------

// AddServerToWaveloomJSON 向 ~/.waveloom.json 添加一个 MCP Server 配置。
// scope: "local" → projects.<cwd>.mcpServers; "user" → mcpServers
func AddServerToWaveloomJSON(homeDir, cwd, scope string, name string, config ServerConfig) error {
	path := filepath.Join(homeDir, ".waveloom.json")

	// 读取或创建
	var cfg ClaudeJSONFile // 复用结构
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if cfg.Projects == nil {
		cfg.Projects = make(map[string]ClaudeJSONProjectEntry)
	}
	if cfg.MCPServers == nil {
		cfg.MCPServers = make(map[string]ServerConfig)
	}

	switch scope {
	case "user":
		cfg.MCPServers[name] = config
	case "local":
		entry := cfg.Projects[cwd]
		if entry.MCPServers == nil {
			entry.MCPServers = make(map[string]ServerConfig)
		}
		entry.MCPServers[name] = config
		cfg.Projects[cwd] = entry
	default:
		return fmt.Errorf("unknown scope %q", scope)
	}

	// 写入
	data, err = json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}

// AddServerToMCPJSON 向 .mcp.json 添加一个 MCP Server 配置（project scope）。
func AddServerToMCPJSON(projectDir, name string, config ServerConfig) error {
	path := filepath.Join(projectDir, ".mcp.json")

	var cfg MCPConfigFile
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if cfg.MCPServers == nil {
		cfg.MCPServers = make(map[string]ServerConfig)
	}

	cfg.MCPServers[name] = config

	data, err = json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}

// RemoveServer 从所有配置来源中删除指定名称的 server。
func RemoveServer(homeDir, cwd, name string) error {
	removed := false

	// .mcp.json
	if cwd != "" {
		if err := removeFromMCPJSON(cwd, name); err == nil {
			removed = true
		}
	}

	// ~/.waveloom.json
	if homeDir != "" {
		if err := removeFromWaveloomJSON(homeDir, cwd, name); err == nil {
			removed = true
		}
	}

	if !removed {
		return fmt.Errorf("server %q not found in any configuration", name)
	}
	return nil
}

func removeFromMCPJSON(projectDir, name string) error {
	path := filepath.Join(projectDir, ".mcp.json")
	return removeServerFromFile(path, name, false, "")
}

func removeFromWaveloomJSON(homeDir, cwd, name string) error {
	path := filepath.Join(homeDir, ".waveloom.json")
	return removeServerFromFile(path, name, true, cwd)
}

func removeServerFromFile(path, name string, hasProjects bool, cwd string) error {
	if hasProjects {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var cfg ClaudeJSONFile
		if err := json.Unmarshal(data, &cfg); err != nil {
			return err
		}

		found := false
		if _, ok := cfg.MCPServers[name]; ok {
			delete(cfg.MCPServers, name)
			found = true
		}
		if cwd != "" {
			if entry, ok := cfg.Projects[cwd]; ok {
				if _, ok := entry.MCPServers[name]; ok {
					delete(entry.MCPServers, name)
					cfg.Projects[cwd] = entry
					found = true
				}
			}
		}
		if !found {
			return fmt.Errorf("not found")
		}

		data, err = json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(path, data, 0644)
	}

	// 简单 .mcp.json
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg MCPConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	if _, ok := cfg.MCPServers[name]; !ok {
		return fmt.Errorf("not found")
	}
	delete(cfg.MCPServers, name)
	data, err = json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// ListServerConfigs 返回所有来源的 server 配置汇总（未合并）。
func ListServerConfigs(homeDir, cwd string) map[string][]ServerConfigSource {
	result := make(map[string][]ServerConfigSource)

	if cwd != "" {
		for name, cfg := range loadMCPJSON(cwd) {
			expandServerConfig(&cfg)
			result[name] = append(result[name], ServerConfigSource{Source: ".mcp.json", Config: cfg})
		}
	}
	if homeDir != "" {
		for name, cfg := range loadWaveloomJSON(homeDir, cwd) {
			expandServerConfig(&cfg)
			result[name] = append(result[name], ServerConfigSource{Source: "~/.waveloom.json (local)", Config: cfg})
		}
		for name, cfg := range loadWaveloomJSON(homeDir, "") {
			expandServerConfig(&cfg)
			result[name] = append(result[name], ServerConfigSource{Source: "~/.waveloom.json (user)", Config: cfg})
		}
		for name, cfg := range loadClaudeJSON(homeDir, cwd) {
			expandServerConfig(&cfg)
			result[name] = append(result[name], ServerConfigSource{Source: "~/.claude.json (local)", Config: cfg})
		}
		for name, cfg := range loadClaudeJSON(homeDir, "") {
			expandServerConfig(&cfg)
			result[name] = append(result[name], ServerConfigSource{Source: "~/.claude.json (user)", Config: cfg})
		}
	}

	return result
}

// ServerConfigSource 记录配置来源。
type ServerConfigSource struct {
	Source string       `json:"source"`
	Config ServerConfig `json:"config"`
}

// ---------------------------------------------------------------------------
// 环境变量展开
// ---------------------------------------------------------------------------

// expandServerConfig 原地展开 ServerConfig 中所有字段的环境变量。
func expandServerConfig(cfg *ServerConfig) {
	cfg.Command = expandEnv(cfg.Command)
	cfg.URL = expandEnv(cfg.URL)
	for i, arg := range cfg.Args {
		cfg.Args[i] = expandEnv(arg)
	}
	for k, v := range cfg.Env {
		cfg.Env[k] = expandEnv(v)
	}
	for k, v := range cfg.Headers {
		cfg.Headers[k] = expandEnv(v)
	}
}

var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// expandEnv 展开字符串中的 ${VAR} 和 ${VAR:-default}。
func expandEnv(s string) string {
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		inner := match[2 : len(match)-1] // 去掉 ${ 和 }
		if idx := strings.Index(inner, ":-"); idx >= 0 {
			name := inner[:idx]
			def := inner[idx+2:]
			if val, ok := os.LookupEnv(name); ok {
				return val
			}
			return def
		}
		return os.Getenv(inner)
	})
}
