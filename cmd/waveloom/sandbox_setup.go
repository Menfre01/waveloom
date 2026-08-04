package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"runtime"

	"github.com/Menfre01/waveloom/pkg/sandbox"
)

// settingsFileWithSandbox 是 settings.json 的顶层结构(含 sandbox 段)。
type settingsFileWithSandbox struct {
	Sandbox *json.RawMessage `json:"sandbox"`
}

// loadSandboxSection 读取配置文件中的 sandbox 段。
// 文件不存在/无 sandbox 段 → 返回 nil(使用默认配置)。
func loadSandboxSection(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var sf settingsFileWithSandbox
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, err
	}
	if sf.Sandbox == nil {
		return nil, nil
	}
	return *sf.Sandbox, nil
}

// createSandboxManager 加载沙箱配置并激活。
// networkOverride 为 --sandbox-network flag 值(off/on/空),非空时覆盖
// settings.json 的 network.mode 并重新校验(网络 on 的凭据强制校验不被绕过)。
//
// 激活判定(任一满足):
//  1. settings.json 显式 enabled: true(TUI 常规模式)
//  2. --bypass-permissions(TUI)或 one-shot / ACP 无交互入口自动激活
//     (oneshot 无条件激活,无需显式 flag——2025-09 决策,对齐 ACP)
//
// 返回 nil 表示沙箱未启用或不可用(调用方降级运行)。
// failIfUnavailable: true 且依赖缺失时返回 fatal=true(调用方拒绝启动)。
func createSandboxManager(bypassPerm bool, networkOverride, globalPath, projectPath, cwd string) (mgr *sandbox.SandboxManager, fatal bool) {
	// 合并配置:项目 sandbox 段覆盖全局(与权限规则一致)
	raw, err := loadSandboxSection(projectPath)
	if err != nil {
		slog.Warn("sandbox: project config unreadable, sandbox disabled", "path", projectPath, "err", err)
		return nil, false
	}
	if raw == nil {
		if raw, err = loadSandboxSection(globalPath); err != nil {
			slog.Warn("sandbox: global config unreadable, sandbox disabled", "path", globalPath, "err", err)
			return nil, false
		}
	}
	cfg, err := sandbox.LoadConfig(raw)
	if err != nil {
		// 配置校验失败 → fail-closed:不启用,提示用户修复
		slog.Warn("sandbox: invalid config, sandbox disabled", "error", err)
		// 五审 M2:显式启用但配置错误也应 stderr 可见(对齐 Select 失败路径)
		if cfg != nil && cfg.Enabled {
			fmt.Fprintf(os.Stderr, "⚠ sandbox: invalid config, sandbox disabled (%v)\n", err)
		}
		return nil, false
	}
	// CLI flag 覆盖网络模式(off/on),重新校验保持 fail-closed
	if networkOverride != "" {
		cfg.Network.Mode = networkOverride
		if err := cfg.Validate(); err != nil {
			slog.Warn("sandbox: invalid config after --sandbox-network override, sandbox disabled", "error", err)
			return nil, false
		}
	}
	if !cfg.Enabled && !bypassPerm {
		return nil, false
	}

	// 网络 on 的凭据遮蔽为可选加固:提示用户但不阻止。
	// 仅在沙箱实际激活(本段之后)前提示;沙箱未启用时网络模式无意义,
	// 避免默认 on + 未启用场景每次启动刷屏(二审 M4)。
	// 网络默认 on(2025-09 决策);2026-09 决策:默认不遮蔽任何路径,
	// 凭据防护由显式配置承载(推荐 denyRead / credentials.files 见 docs/settings.md)。
	if cfg.Network.Mode == sandbox.NetworkModeOn &&
		len(cfg.Credentials.Files) == 0 && len(cfg.Filesystem.DenyRead) == 0 {
		slog.Warn("sandbox: network mode on without explicit credentials.files / denyRead — network can exfiltrate unmasked user files; configure masking for stronger protection")
		// 五审 M6:slog 默认只写日志文件,TUI/one-shot/ACP 用户零感知——
		// 网络 on + 无遮蔽 = 凭据可读可外传,必须 stderr 可见(对齐 109 行先例)。
		fmt.Fprintf(os.Stderr, "⚠ sandbox: network on without credential masking (denyRead / credentials.files) — ~/.ssh, ~/.aws, settings.json etc. are readable and can be exfiltrated; configure masking for stronger protection\n")
	}

	mgr = sandbox.NewManager(cfg, cwd)
	if err := mgr.Select(); err != nil {
		// 五审 M1:原生 Windows stub 恒"不可用"是平台不支持而非环境缺陷,
		// failIfUnavailable 不应拒绝启动(否则共享 settings.json 的
		// Windows 用户无路可退)
		if cfg.FailIfUnavailable && runtime.GOOS != "windows" {
			slog.Error("sandbox: required but unavailable (failIfUnavailable=true), refusing to start", "error", err)
			return nil, true
		}
		slog.Warn("sandbox: unavailable, running unsandboxed", "error", err)
		// 三审 Medium-3:显式 enabled=true 的用户应感知沙箱未生效——
		// slog 默认只写日志文件,TUI/one-shot 无感知
		if cfg.Enabled {
			fmt.Fprintf(os.Stderr, "⚠ sandbox: enabled but unavailable, running unsandboxed (%v)\n", err)
		}
		return nil, false
	}
	slog.Info("sandbox: activated", "backend", mgr.Name())
	return mgr, false
}
