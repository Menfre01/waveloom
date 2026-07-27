package lsp

import "context"

type lspManagerKey struct{}

// WithLSPManager injects the LSP Manager into ctx for tool-level access.
func WithLSPManager(ctx context.Context, mgr *Manager) context.Context {
	return context.WithValue(ctx, lspManagerKey{}, mgr)
}

// LSPManagerFromContext extracts the LSP Manager from ctx.
// Returns nil if not present.
func LSPManagerFromContext(ctx context.Context) *Manager {
	mgr, _ := ctx.Value(lspManagerKey{}).(*Manager)
	return mgr
}
