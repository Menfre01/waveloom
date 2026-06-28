package tool

import "waveloom/pkg/lsp"

// LSPProvider 是 LSP 工具的依赖接口，用于替代原先的全局变量。
// 所有 LSP 工具通过此接口获取 Manager 实例，支持依赖注入。
type LSPProvider struct {
	Manager *lsp.Manager
}

// NewLSPProvider 创建一个新的 LSPProvider。
func NewLSPProvider(m *lsp.Manager) *LSPProvider {
	return &LSPProvider{Manager: m}
}


