// Package sandbox 实现 Waveloom 的 OS 级执行隔离层。
//
// 与 pkg/permission(应用层决策)互补:权限系统回答"能不能跑",沙箱回答"跑了能碰什么"。
package sandbox

import "context"

// ============================================================================
// SandboxStatus — per-command 沙箱状态(契约文件)
// ============================================================================

// SandboxStatus 描述本条命令的沙箱包装状态。
//
// 由 agentloop 在每次工具调用前注入 context,供 Shell 工具(是否 bwrap
// 包装)读取;Guard 的二元决策不读此状态(2025-08 决策:bypass 即二元决策,
// 与沙箱无关——三审 Medium-2 澄清)。必须保持零依赖,防止 sandbox 包与
// permission/tool/agentloop 之间产生循环导入。
//
// 关键语义:逃逸命令(excludedCommands 命中)不进沙箱 → Active=false → 裸跑,
// 权限决策不受影响(逃逸命令同样享受二元决策)。
type SandboxStatus struct {
	Active bool   // true = 本条命令将被沙箱包装
	Reason string // 状态说明(如 "excluded: docker *" / "sandbox unavailable")
}

// sandboxCtxKey 是 context 中 SandboxStatus 的私有 key。
type sandboxCtxKey struct{}

// WithSandboxStatus 将沙箱状态注入 context。
// 未注入 context 时读取方得到 Active=false(安全默认,fail-closed)。
func WithSandboxStatus(ctx context.Context, s SandboxStatus) context.Context {
	return context.WithValue(ctx, sandboxCtxKey{}, s)
}

// SandboxStatusFrom 从 context 读取沙箱状态。
// 未注入(或非沙箱工具)时返回 {Active: false},调用方按"不在沙箱内"处理。
func SandboxStatusFrom(ctx context.Context) SandboxStatus {
	if ctx == nil {
		return SandboxStatus{}
	}
	if s, ok := ctx.Value(sandboxCtxKey{}).(SandboxStatus); ok {
		return s
	}
	return SandboxStatus{}
}
