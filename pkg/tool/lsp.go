package tool

import "waveloom/pkg/lsp"

// LSPManager 是全局 LSP Server 管理器，由 main 初始化后设置。
var LSPManager *lsp.Manager
