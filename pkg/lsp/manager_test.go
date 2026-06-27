package lsp

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// ManagerOption functions
// ---------------------------------------------------------------------------

func TestWithUserServers(t *testing.T) {
	servers := map[string]ServerConfig{
		".go": {Command: "custom-gopls"},
	}
	m := NewManager(WithUserServers(servers))
	if len(m.userServers) != 1 {
		t.Fatalf("expected 1 user server, got %d", len(m.userServers))
	}
	if m.userServers[".go"].Command != "custom-gopls" {
		t.Errorf("expected custom-gopls, got %q", m.userServers[".go"].Command)
	}
}

func TestWithIdleTimeout(t *testing.T) {
	m := NewManager(WithIdleTimeout(10 * time.Minute))
	if m.idleTimeout != 10*time.Minute {
		t.Errorf("expected 10m, got %v", m.idleTimeout)
	}
}

func TestWithLogger(t *testing.T) {
	logger := log.New(os.Stderr, "[test] ", 0)
	m := NewManager(WithLogger(logger))
	if m.logger != logger {
		t.Error("logger not set")
	}
}

func TestNewManager_Defaults(t *testing.T) {
	m := NewManager()
	if m.idleTimeout != 5*time.Minute {
		t.Errorf("expected default 5m idle timeout, got %v", m.idleTimeout)
	}
	if m.userServers == nil {
		t.Error("userServers should be initialized")
	}
	if m.instances == nil {
		t.Error("instances should be initialized")
	}
	if m.ctx == nil {
		t.Error("context should be set")
	}
	m.Close()
}

func TestNewManager_CombinedOptions(t *testing.T) {
	servers := map[string]ServerConfig{".py": {Command: "pylsp"}}
	logger := log.New(os.Stderr, "[lsp] ", 0)
	m := NewManager(
		WithUserServers(servers),
		WithIdleTimeout(2*time.Minute),
		WithLogger(logger),
	)
	if m.userServers[".py"].Command != "pylsp" {
		t.Error("user servers not applied")
	}
	if m.idleTimeout != 2*time.Minute {
		t.Error("idle timeout not applied")
	}
	if m.logger != logger {
		t.Error("logger not applied")
	}
	m.Close()
}

// ---------------------------------------------------------------------------
// serverState.String
// ---------------------------------------------------------------------------

func TestServerStateString(t *testing.T) {
	tests := []struct {
		state serverState
		want  string
	}{
		{stateNew, "new"},
		{stateStarting, "starting"},
		{stateReady, "ready"},
		{stateCrashed, "crashed"},
		{stateClosed, "closed"},
		{serverState(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("state(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// GetOrCreate — error paths
// ---------------------------------------------------------------------------

func TestGetOrCreate_NoExtension(t *testing.T) {
	m := NewManager()
	defer m.Close()

	_, err := m.GetOrCreate("Makefile")
	if err == nil {
		t.Fatal("expected error for file without extension")
	}
	if !strings.Contains(err.Error(), "no file extension") {
		t.Errorf("expected 'no file extension' error, got %q", err.Error())
	}
}

func TestGetOrCreate_UnknownExtension(t *testing.T) {
	m := NewManager()
	defer m.Close()

	_, err := m.GetOrCreate("file.unknown_ext_xyz")
	if err == nil {
		t.Fatal("expected error for unknown extension")
	}
	if !strings.Contains(err.Error(), "no LSP server configured") {
		t.Errorf("expected 'no LSP server configured' error, got %q", err.Error())
	}
}

func TestGetOrCreate_GoNoGopls(t *testing.T) {
	// gopls may not be installed; test error path
	m := NewManager()
	defer m.Close()

	_, err := m.GetOrCreate("test.go")
	if err != nil {
		// 预期：gopls 未安装时返回 "not found in PATH"
		if !strings.Contains(err.Error(), "start") && !strings.Contains(err.Error(), "not found") {
			t.Logf("unexpected error (may indicate gopls is actually installed): %v", err)
		}
	}
	// 如果 gopls 安装了，GetOrCreate 可能成功 — 清理
	m.Close()
}

// ---------------------------------------------------------------------------
// Diagnostics — no instances
// ---------------------------------------------------------------------------

func TestDiagnostics_NoInstances(t *testing.T) {
	m := NewManager()
	defer m.Close()

	diags := m.Diagnostics("file:///nonexistent.go")
	if diags != nil {
		t.Error("expected nil diagnostics when no instances")
	}
}

// ---------------------------------------------------------------------------
// Close
// ---------------------------------------------------------------------------

func TestClose_EmptyManager(t *testing.T) {
	m := NewManager()
	m.Close() // 不应 panic

	// 二次 Close 应安全
	m.Close()
}

func TestClose_HasInstances(t *testing.T) {
	m := NewManager()
	// 手动插入一个 fake instance（不启动真实 server，state != ready）
	inst := &ServerInstance{
		ext:      ".test",
		cfg:      ServerConfig{Command: "nonexistent"},
		state:    stateCrashed, // 非 ready 状态，Close 不会尝试 close nil client
		lastUsed: time.Now(),
	}
	m.instances[".test"] = inst

	m.Close()

	// 确认 instance 被移除
	if len(m.instances) != 0 {
		t.Errorf("expected 0 instances after close, got %d", len(m.instances))
	}
}

// ---------------------------------------------------------------------------
// PathToURI
// ---------------------------------------------------------------------------

func TestPathToURI(t *testing.T) {
	uri := PathToURI("/home/user/project/main.go")
	if !strings.HasPrefix(string(uri), "file://") {
		t.Errorf("expected file:// prefix, got %q", uri)
	}
	if !strings.HasSuffix(string(uri), "/main.go") {
		t.Errorf("expected /main.go suffix, got %q", uri)
	}
}

func TestPathToURI_Relative(t *testing.T) {
	uri := PathToURI("main.go")
	if !strings.HasPrefix(string(uri), "file://") {
		t.Errorf("expected file:// prefix for relative path, got %q", uri)
	}
}

// ---------------------------------------------------------------------------
// extToLanguageID
// ---------------------------------------------------------------------------

func TestExtToLanguageID(t *testing.T) {
	tests := []struct {
		ext  string
		want string
	}{
		{".go", "go"},
		{".mod", "go.mod"},
		{".sum", "go.sum"},
		{".work", "go.work"},
		{".rs", "rust"},
		{".ts", "typescript"},
		{".tsx", "typescript"},
		{".js", "javascript"},
		{".jsx", "javascript"},
		{".py", "python"},
		{".pyi", "python"},
		{".rb", "rb"},
		{".zig", "zig"},
	}
	for _, tt := range tests {
		if got := extToLanguageID(tt.ext); got != tt.want {
			t.Errorf("extToLanguageID(%q) = %q, want %q", tt.ext, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// LoadUserServers — integration with Manager
// ---------------------------------------------------------------------------

func TestLoadUserServers_Integration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	content := `{"lsp": {"servers": {".go": {"command": "echo"}}}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	servers := LoadUserServers(path)
	if servers[".go"].Command != "echo" {
		t.Errorf("expected echo, got %q", servers[".go"].Command)
	}
}

// ---------------------------------------------------------------------------
// ServerInstance state transitions (without real server)
// ---------------------------------------------------------------------------

func TestServerInstance_DefaultState(t *testing.T) {
	inst := &ServerInstance{
		ext:      ".go",
		cfg:      ServerConfig{Command: "gopls"},
		state:    stateNew,
		lastUsed: time.Now(),
	}

	inst.stateMu.RLock()
	s := inst.state
	inst.stateMu.RUnlock()

	if s != stateNew {
		t.Errorf("expected stateNew, got %v", s)
	}
}

// ---------------------------------------------------------------------------
// GetOrCreate with context cancellation
// ---------------------------------------------------------------------------

func TestGetOrCreate_CancelledContext(t *testing.T) {
	m := NewManager()
	defer m.Close()

	// Manager context is still valid; cancellation doesn't affect lookup
	// but if gopls is not installed, the error path is tested
	_, err := m.GetOrCreate("nonexistent_file.noext")
	if err == nil {
		t.Fatal("expected error for no extension file")
	}
}

// ---------------------------------------------------------------------------
// Call error path: server not ready
// ---------------------------------------------------------------------------

func TestCall_ServerNotReady(t *testing.T) {
	m := NewManager()
	defer m.Close()

	inst := &ServerInstance{
		ext:      ".go",
		state:    stateNew, // not ready
		lastUsed: time.Now(),
	}

	err := m.Call(context.Background(), inst, "textDocument/definition", nil, nil)
	if err == nil {
		t.Fatal("expected error when server not ready")
	}
	if !strings.Contains(err.Error(), "not ready") {
		t.Errorf("expected 'not ready' error, got %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// SyncFile error: file not found
// ---------------------------------------------------------------------------

func TestSyncFile_NotFound(t *testing.T) {
	m := NewManager()
	defer m.Close()

	inst := &ServerInstance{
		ext:      ".go",
		state:    stateReady,
		lastUsed: time.Now(),
	}

	err := m.SyncFile(inst, "/nonexistent/file.go")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
	if !strings.Contains(err.Error(), "read file") {
		t.Errorf("expected 'read file' error, got %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// SyncFile: server not ready
// ---------------------------------------------------------------------------

func TestSyncFile_ServerNotReady(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	if err := os.WriteFile(path, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager()
	defer m.Close()

	inst := &ServerInstance{
		ext:      ".go",
		state:    stateNew, // not ready
		lastUsed: time.Now(),
	}

	err := m.SyncFile(inst, path)
	if err == nil {
		t.Fatal("expected error when server not ready")
	}
	if !strings.Contains(err.Error(), "not ready") {
		t.Errorf("expected 'not ready' error, got %q", err.Error())
	}
}
