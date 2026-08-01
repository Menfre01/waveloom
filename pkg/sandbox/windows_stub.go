package sandbox

import "fmt"

// ============================================================================
// windowsStubBackend — Windows 占位
// ============================================================================

// windowsStubBackend 在原生 Windows 上不启用沙箱。
// 与 Claude Code 同款策略:WSL2 环境走 Linux bwrap 后端。
type windowsStubBackend struct{}

func newWindowsStubBackend() *windowsStubBackend { return &windowsStubBackend{} }

// Name 返回后端名。
func (b *windowsStubBackend) Name() string { return "windows-stub" }

// Probe 探测后端可用性。
func (b *windowsStubBackend) Probe() error {
	return fmt.Errorf("sandbox: Windows is not supported (use WSL2 with the Linux bubblewrap backend); sandbox disabled")
}

// Transform 构造沙箱 argv。
func (b *windowsStubBackend) Transform(shellBin string, args []string, cfg *Config, workspace string) ([]string, error) {
	return nil, ErrSandboxUnavailable
}
