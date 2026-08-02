package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestLoadSandboxSection_MissingFile 配置文件不存在 → nil(使用默认配置)。
func TestLoadSandboxSection_MissingFile(t *testing.T) {
	raw, err := loadSandboxSection(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if raw != nil {
		t.Errorf("missing file should return nil, got %s", raw)
	}
}

// TestLoadSandboxSection_NoSandboxKey 无 sandbox 段 → nil。
func TestLoadSandboxSection_NoSandboxKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(path, []byte(`{"permissions": {"allow": []}}`), 0o644)
	raw, err := loadSandboxSection(path)
	if err != nil {
		t.Fatal(err)
	}
	if raw != nil {
		t.Errorf("no sandbox key should return nil, got %s", raw)
	}
}

// TestLoadSandboxSection_InvalidJSON 配置文件 JSON 损坏 → error。
func TestLoadSandboxSection_InvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(path, []byte(`{broken`), 0o644)
	if _, err := loadSandboxSection(path); err == nil {
		t.Error("invalid json should return error")
	}
}

// TestLoadSandboxSection_HasSandbox 有 sandbox 段 → 返回原始内容。
func TestLoadSandboxSection_HasSandbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(path, []byte(`{"sandbox": {"enabled": true}}`), 0o644)
	raw, err := loadSandboxSection(path)
	if err != nil {
		t.Fatal(err)
	}
	if raw == nil || string(raw) != `{"enabled": true}` {
		t.Errorf("raw = %s", raw)
	}
}

// TestCreateSandboxManager_NotEnabled 未启用且非 bypass → nil(沙箱不激活)。
func TestCreateSandboxManager_NotEnabled(t *testing.T) {
	if mgr, fatal := createSandboxManager(false, "", "", "", t.TempDir()); mgr != nil || fatal {
		t.Error("default disabled sandbox should return nil")
	}
}

// TestCreateSandboxManager_InvalidConfig 配置校验失败 → nil(fail-closed)。
func TestCreateSandboxManager_InvalidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	// proxy 是 v2 未实现 → 校验拒绝
	_ = os.WriteFile(path, []byte(`{"sandbox": {"enabled": true, "network": {"mode": "proxy"}}}`), 0o644)
	if mgr, fatal := createSandboxManager(false, "", "", path, t.TempDir()); mgr != nil || fatal {
		t.Error("invalid config should disable sandbox")
	}
}

// TestCreateSandboxManager_UnreadableConfig 配置不可读 → nil。
func TestCreateSandboxManager_UnreadableConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(path, []byte(`{bad`), 0o644)
	if mgr, fatal := createSandboxManager(false, "", "", path, t.TempDir()); mgr != nil || fatal {
		t.Error("unreadable config should disable sandbox")
	}
}

// TestCreateSandboxManager_BypassActivates --bypass-permissions 激活沙箱
// (真实平台探测;后端不可用的环境自动跳过)。
func TestCreateSandboxManager_BypassActivates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows stub always unavailable")
	}
	mgr, fatal := createSandboxManager(true, "", "", "", t.TempDir())
	if fatal {
		t.Fatal("bypass activation should not be fatal (failIfUnavailable defaults false)")
	}
	if mgr == nil {
		t.Skip("sandbox backend unavailable on this machine (bwrap/seatbelt missing)")
	}
	if !mgr.Available() {
		t.Error("bypass should activate sandbox")
	}
	if mgr.AllowUnsandboxed() != true {
		t.Error("default allowUnsandboxed should be true")
	}
}

// TestCreateSandboxManager_EnabledExplicit 显式 enabled: true 激活(非 bypass)。
func TestCreateSandboxManager_EnabledExplicit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows stub always unavailable")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(path, []byte(`{"sandbox": {"enabled": true}}`), 0o644)
	mgr, fatal := createSandboxManager(false, "", "", path, t.TempDir())
	if fatal {
		t.Fatal("enabled without failIfUnavailable should not be fatal")
	}
	if mgr == nil {
		t.Skip("sandbox backend unavailable on this machine (bwrap/seatbelt missing)")
	}
	if !mgr.Available() {
		t.Error("enabled:true should activate sandbox")
	}
}

// TestCreateSandboxManager_FailIfUnavailableFatal failIfUnavailable + 后端
// 不可用 → fatal=true(调用方拒绝启动)。
func TestCreateSandboxManager_FailIfUnavailableFatal(t *testing.T) {
	if runtime.GOOS == "windows" {
		// 实现含 Windows 特例:stub 恒不可用是平台不支持而非环境缺陷,
		// failIfUnavailable 不拒绝启动(见 sandbox_setup.go Select 失败分支)。
		t.Skip("failIfUnavailable is not fatal on Windows by design")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(path, []byte(`{"sandbox": {"enabled": true, "failIfUnavailable": true}}`), 0o644)
	// PATH 置空 → LookPath 必然失败 → Select 失败 → fatal
	t.Setenv("PATH", t.TempDir())
	mgr, fatal := createSandboxManager(false, "", "", path, t.TempDir())
	if !fatal {
		t.Error("failIfUnavailable + unavailable should be fatal")
	}
	if mgr != nil {
		t.Error("fatal path should return nil manager")
	}
}

// TestCreateSandboxManager_NetworkOverrideOn_WithoutCredentials
// --sandbox-network on 未配置凭据遮蔽 → 允许启用(遮蔽为可选加固,2025-08 决策)。
func TestCreateSandboxManager_NetworkOverrideOn_WithoutCredentials(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows stub always unavailable")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(path, []byte(`{"sandbox": {"enabled": true}}`), 0o644)
	mgr, fatal := createSandboxManager(false, "on", "", path, t.TempDir())
	if fatal {
		t.Fatal("should not be fatal")
	}
	if mgr == nil {
		t.Skip("sandbox backend unavailable on this machine")
	}
	// 覆盖生效:on 模式 → argv 无 --unshare-net
	argv, err := mgr.Transform("bash", []string{"-c", "true"})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range argv {
		if a == "--unshare-net" {
			t.Error("network override on should not include --unshare-net")
		}
	}
}

// TestCreateSandboxManager_NetworkOverrideOn_WithCredentials
// --sandbox-network on + 凭据遮蔽 → 覆盖生效(激活路径,后端不可用则跳过)。
func TestCreateSandboxManager_NetworkOverrideOn_WithCredentials(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows stub always unavailable")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(path, []byte(`{
		"sandbox": {
			"enabled": true,
			"network": {"mode": "off"},
			"credentials": {"files": ["~/.ssh"]}
		}
	}`), 0o644)
	mgr, fatal := createSandboxManager(false, "on", "", path, t.TempDir())
	if fatal {
		t.Fatal("should not be fatal")
	}
	if mgr == nil {
		t.Skip("sandbox backend unavailable on this machine")
	}
	// 覆盖生效的验证:通过 Transform 产出确认无 --unshare-net(仅 macOS/Linux 后端)
	argv, err := mgr.Transform("bash", []string{"-c", "true"})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range argv {
		if a == "--unshare-net" {
			t.Error("network override on should not include --unshare-net")
		}
	}
}

// TestCreateSandboxManager_NetworkOverrideInvalid flag 值非法 → 拒绝。
func TestCreateSandboxManager_NetworkOverrideInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(path, []byte(`{"sandbox": {"enabled": true, "credentials": {"files": ["~/.ssh"]}}}`), 0o644)
	if mgr, fatal := createSandboxManager(false, "banana", "", path, t.TempDir()); mgr != nil || fatal {
		t.Error("invalid network override should be rejected")
	}
}
