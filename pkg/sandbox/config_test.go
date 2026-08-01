package sandbox

import (
	"testing"
)

func TestConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Error("Enabled = true, want false (default)")
	}
	if cfg.FailIfUnavailable {
		t.Error("FailIfUnavailable = true, want false")
	}
	if !cfg.AllowUnsandboxed() {
		t.Error("AllowUnsandboxed() = false, want true (default)")
	}
	if cfg.Network.Mode != NetworkModeOff {
		t.Errorf("Network.Mode = %q, want %q", cfg.Network.Mode, NetworkModeOff)
	}

	// 空 JSON 也走默认值
	cfg2, err := LoadConfig([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Enabled || !cfg2.AllowUnsandboxed() {
		t.Error("empty json should produce defaults")
	}
}

func TestConfigParseFull(t *testing.T) {
	data := []byte(`{
		"enabled": true,
		"failIfUnavailable": true,
		"allowUnsandboxedCommands": false,
		"excludedCommands": ["docker *", "git push *"],
		"network": {"mode": "off", "allowedDomains": ["github.com", "*.npmjs.org"]},
		"filesystem": {"denyRead": ["~/.ssh"]},
		"capabilities": {"keep": ["net_raw"]},
		"credentials": {"envVars": ["GH_TOKEN"]}
	}`)
	cfg, err := LoadConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || !cfg.FailIfUnavailable {
		t.Error("enabled/failIfUnavailable not parsed")
	}
	if cfg.AllowUnsandboxed() {
		t.Error("allowUnsandboxedCommands=false not parsed")
	}
	if len(cfg.ExcludedCommands) != 2 || cfg.ExcludedCommands[0] != "docker *" {
		t.Errorf("excludedCommands = %v", cfg.ExcludedCommands)
	}
	if len(cfg.Capabilities.Keep) != 1 || cfg.Capabilities.Keep[0] != "net_raw" {
		t.Errorf("capabilities.keep = %v", cfg.Capabilities.Keep)
	}
	// allowedDomains 解析(v2 proxy 预留,当前仅解析不生效)
	if len(cfg.Network.AllowedDomains) != 2 || cfg.Network.AllowedDomains[0] != "github.com" {
		t.Errorf("allowedDomains = %v", cfg.Network.AllowedDomains)
	}
}

func TestConfigNetworkOn_NoMaskStillValid(t *testing.T) {
	// 网络 on 缺凭据遮蔽 → 允许(遮蔽为可选加固,2025-08 决策)
	cfg, err := LoadConfig([]byte(`{"network": {"mode": "on"}}`))
	if err != nil {
		t.Fatalf("network on without explicit mask should be valid: %v", err)
	}
	if cfg.Network.Mode != NetworkModeOn {
		t.Error("network mode not parsed")
	}

	// 配置了遮蔽同样有效(加固路径)
	if _, err := LoadConfig([]byte(`{"network": {"mode": "on"}, "credentials": {"files": ["~/.ssh"]}}`)); err != nil {
		t.Errorf("with credentials.files should be valid: %v", err)
	}
}

func TestConfigInvalidModes(t *testing.T) {
	// proxy 是 v2,当前拒绝
	if _, err := LoadConfig([]byte(`{"network": {"mode": "proxy"}}`)); err == nil {
		t.Error("proxy mode should fail (v2 not implemented)")
	}
	// 未知模式
	if _, err := LoadConfig([]byte(`{"network": {"mode": "banana"}}`)); err == nil {
		t.Error("invalid mode should fail")
	}
	// 空 excludedCommands 条目
	if _, err := LoadConfig([]byte(`{"excludedCommands": ["", "docker *"]}`)); err == nil {
		t.Error("empty excludedCommands entry should fail")
	}
}

func TestConfigInvalidJSON(t *testing.T) {
	if _, err := LoadConfig([]byte(`{not json`)); err == nil {
		t.Error("invalid json should fail")
	}
}
