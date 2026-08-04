package sandbox

import (
	"strings"
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
	"filesystem": {"allowRead": ["~/.docker/run"], "denyRead": ["~/.ssh"]},
	"capabilities": {"keep": ["net_raw"]},
	"credentials": {"envVars": ["GH_TOKEN"]},
	"env": {"GOPATH": ".waveloom-gopath", "GOPROXY": "https://proxy.golang.org,direct"}
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
	// allowRead 已废弃(2026-09):字段仍可解析(兼容旧配置)但不再生效,
	// LoadConfig 仅告警不报错(行为由 TestConfigParseFull 的 err == nil 覆盖)
	if len(cfg.Filesystem.AllowRead) != 1 || cfg.Filesystem.AllowRead[0] != "~/.docker/run" {
		t.Errorf("filesystem.allowRead (deprecated) should still parse = %v", cfg.Filesystem.AllowRead)
	}
	if len(cfg.Filesystem.DenyRead) != 1 || cfg.Filesystem.DenyRead[0] != "~/.ssh" {
		t.Errorf("filesystem.denyRead = %v", cfg.Filesystem.DenyRead)
	}
	// allowedDomains 解析(v2 proxy 预留,当前仅解析不生效)
	if len(cfg.Network.AllowedDomains) != 2 || cfg.Network.AllowedDomains[0] != "github.com" {
		t.Errorf("allowedDomains = %v", cfg.Network.AllowedDomains)
	}
	if cfg.Env["GOPATH"] != ".waveloom-gopath" || cfg.Env["GOPROXY"] != "https://proxy.golang.org,direct" {
		t.Errorf("env = %v", cfg.Env)
	}
}

func TestConfigEnvValidation(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want string // 期望错误子串,空 = 合法
	}{
		{"valid", `{"env": {"GOPATH": ".waveloom-gopath"}}`, ""},
		{"empty key", `{"env": {"": "x"}}`, "empty key"},
		{"empty value", `{"env": {"GOPATH": ""}}`, "empty value"},
		{"bad key", `{"env": {"1BAD": "x"}}`, "not a valid environment variable name"},
		{"bad key char", `{"env": {"GO PATH": "x"}}`, "not a valid environment variable name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadConfig([]byte(tc.env))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
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
	// allowRead 已废弃:空条目不再报错(告警并忽略)
	if _, err := LoadConfig([]byte(`{"filesystem": {"allowRead": [""]}}`)); err != nil {
		t.Errorf("deprecated allowRead should be ignored, got %v", err)
	}
}

func TestConfigInvalidJSON(t *testing.T) {
	if _, err := LoadConfig([]byte(`{not json`)); err == nil {
		t.Error("invalid json should fail")
	}
}
