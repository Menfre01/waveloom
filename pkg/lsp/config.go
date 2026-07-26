package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ---------------------------------------------------------------------------
// 语言配置
// ---------------------------------------------------------------------------

// ServerConfig 定义启动某个 Language Server 的命令和参数。
type ServerConfig struct {
	Command string   // 可执行文件名 (如 "gopls")
	Args    []string // 命令行参数
}

// DefaultLSPServers 返回内置的文件扩展名 → LSP Server 映射 (Tier 1)。
func DefaultLSPServers() map[string]ServerConfig {
	return map[string]ServerConfig{
		".go":  {Command: "gopls"},
		".rs":  {Command: "rust-analyzer"},
		".ts":  {Command: "typescript-language-server", Args: []string{"--stdio"}},
		".tsx": {Command: "typescript-language-server", Args: []string{"--stdio"}},
		".js":  {Command: "typescript-language-server", Args: []string{"--stdio"}},
		".jsx": {Command: "typescript-language-server", Args: []string{"--stdio"}},
		".c":   {Command: "clangd"},
		".cpp": {Command: "clangd"},
		".cc":  {Command: "clangd"},
		".cxx": {Command: "clangd"},
		".h":   {Command: "clangd"},
		".hpp": {Command: "clangd"},
	}
}

// LookupServer 根据文件扩展名查找对应的 LSP Server 配置。
// userOverrides 优先级高于默认配置。未匹配返回 nil。
func LookupServer(filePath string, userOverrides map[string]ServerConfig) *ServerConfig {
	ext := filepath.Ext(filePath)
	if ext == "" {
		return nil
	}

	// 用户覆盖优先
	if cfg, ok := userOverrides[ext]; ok {
		return &cfg
	}

	// 默认配置
	defaults := DefaultLSPServers()
	if cfg, ok := defaults[ext]; ok {
		return &cfg
	}

	return nil
}

// IsCodeFile 判断是否为可 LSP 检查的代码文件。
// 排除文本文档、数据格式等 LSP 无法提供有效诊断的文件类型。
func IsCodeFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".md", ".txt", ".csv", ".svg", ".lock", ".gitignore",
		".mod", ".sum", ".work": // Go module files — gopls handles via .go files
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// settings.json 配置加载
// ---------------------------------------------------------------------------

// lspSettings 是 settings.json 中 "lsp" 键的格式。
type lspSettings struct {
	Servers       map[string]lspServerEntry `json:"servers"`
	IdleTimeoutMs int                       `json:"idle_timeout_ms"`
}

type lspServerEntry struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// LoadUserServers 从 settings.json 加载用户自定义的 LSP Server 配置。
// 文件不存在或解析失败返回空 map。
func LoadUserServers(settingsPath string) map[string]ServerConfig {
	if settingsPath == "" {
		return nil
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil
	}

	var wrapper struct {
		LSP *lspSettings `json:"lsp"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil || wrapper.LSP == nil {
		return nil
	}

	servers := make(map[string]ServerConfig)
	for ext, entry := range wrapper.LSP.Servers {
		servers[ext] = ServerConfig(entry)
	}
	return servers
}
