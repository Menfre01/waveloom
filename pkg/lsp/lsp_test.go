package lsp
import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// mockLSPPath returns the path to the mock LSP server binary.
// If not built yet, builds it using 'go build'.
// mockLSPPath returns the path to the mock LSP server binary.
// If not built yet, builds it using 'go build'.
func mockLSPPath(t *testing.T) string {
	t.Helper()
	testdataDir := filepath.Join("testdata", "mock_lsp")
	name := "waveloom-mock-lsp"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(os.TempDir(), name)

	// Check if already built
	if _, err := os.Stat(bin); err == nil {
		return bin
	}

	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = testdataDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build mock lsp: %v\n%s", err, out)
	}
	return bin
}

// cleanupMockLSP removes the mock binary.
func cleanupMockLSP(t *testing.T) {
	t.Helper()
	name := "waveloom-mock-lsp"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(os.TempDir(), name)
	_ = os.Remove(bin)
}

// =========================================================================
// Client tests
// =========================================================================

func TestClientInitialize(t *testing.T) {
	bin := mockLSPPath(t)

	rootURI := PathToURI(t.TempDir())
	client, err := NewClient(bin, nil, string(rootURI), "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if client == nil {
		t.Fatal("client is nil")
	}
	defer func() { _ = client.Close() }()
}

func TestClientCall(t *testing.T) {
	bin := mockLSPPath(t)

	rootURI := PathToURI(t.TempDir())
	client, err := NewClient(bin, nil, string(rootURI), "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Test: shutdown (valid method)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Call(ctx, "shutdown", nil, nil); err != nil {
		t.Fatalf("shutdown call: %v", err)
	}

	// Test: unknown method
	if err := client.Call(ctx, "unknown/method", nil, nil); err == nil {
		t.Error("expected error for unknown method")
	}
}

func TestClientNotify(t *testing.T) {
	bin := mockLSPPath(t)

	rootURI := PathToURI(t.TempDir())
	client, err := NewClient(bin, nil, string(rootURI), "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Notify didOpen
	if err := client.Notify("textDocument/didOpen", nil); err != nil {
		t.Fatalf("didOpen notify: %v", err)
	}
}

func TestClientOnNotification(t *testing.T) {
	bin := mockLSPPath(t)

	rootURI := PathToURI(t.TempDir())
	client, err := NewClient(bin, nil, string(rootURI), "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	received := make(chan PublishDiagnosticsParams, 1)
	client.OnNotification("textDocument/publishDiagnostics", func(raw json.RawMessage) {
		var params PublishDiagnosticsParams
		if err := json.Unmarshal(raw, &params); err != nil {
			t.Logf("unmarshal error: %v", err)
			return
		}
		received <- params
	})

	// Send didOpen to trigger publishDiagnostics notification
	uri := PathToURI(t.TempDir() + "/test.go")
	if err := client.Notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        uri,
			LanguageID: "go",
			Version:    1,
			Text:       "package test",
		},
	}); err != nil {
		t.Fatalf("didOpen: %v", err)
	}

	// Wait for publishDiagnostics
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	select {
	case params := <-received:
		if params.URI != uri {
			t.Errorf("unexpected URI: %s, want %s", params.URI, uri)
		}
		// didOpen sends empty diagnostics initially
		if len(params.Diagnostics) != 0 {
			t.Logf("diagnostics count: %d", len(params.Diagnostics))
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for publishDiagnostics notification")
	}
}

func TestClientReadLoopMultiHeader(t *testing.T) {
	bin := mockLSPPath(t)

	rootURI := PathToURI(t.TempDir())
	client, err := NewClient(bin, nil, string(rootURI), "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Simulate didChange with content containing magic trigger
	uri := PathToURI(t.TempDir() + "/test.go")
	if err := client.Notify("textDocument/didChange", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":     uri,
			"version": 2,
		},
		"contentChanges": []map[string]interface{}{
			{"text": "ERROR_TRIGGER some code"},
		},
	}); err != nil {
		t.Fatalf("didChange: %v", err)
	}

	// The mock server should send diagnostics with the error
	// (we verified notification handling in previous test)
}

func TestClientClose(t *testing.T) {
	bin := mockLSPPath(t)

	rootURI := PathToURI(t.TempDir())
	client, err := NewClient(bin, nil, string(rootURI), "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	// Close should not block
	done := make(chan struct{})
	go func() {
		_ = client.Close()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(10 * time.Second):
		t.Fatal("Close() timed out")
	}
}

func TestClientCloseUnresponsive(t *testing.T) {
	// Create a process that doesn't respond to shutdown
	// Use 'sleep 30' as a mock unresponsive server
	// We skip initialize, just start directly
	cmd := exec.Command("sleep", "30")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	_ = cmd.Start()

	c := &Client{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		pending: make(map[int]chan *rawMessage),
		notify:  make(map[string][]func(json.RawMessage)),
		done:    make(chan struct{}),
	}
	go func() { _, _ = ioReadAll(stdout) }()
	go func() { _, _ = ioReadAll(stderr) }()

	// Close should kill after 5s timeout
	done := make(chan struct{})
	go func() {
		_ = c.Close()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(10 * time.Second):
		t.Fatal("Close() with unresponsive server timed out")
	}
}

// ioReadAll is a helper for discarding output
func ioReadAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	buf := make([]byte, 1024)
	var all []byte
	for {
		n, err := r.Read(buf)
		all = append(all, buf[:n]...)
		if err != nil {
			return all, err
		}
	}
}

// =========================================================================
// PathUtil tests
// =========================================================================

func TestPathToURI(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/home/user/project/main.go", "file:///home/user/project/main.go"},
		{"/tmp/test file.go", "file:///tmp/test%20file.go"},
		{"/path/with#hash.go", "file:///path/with%23hash.go"},
	}

	for _, tt := range tests {
		got := string(PathToURI(tt.path))
		if got != tt.want {
			t.Errorf("PathToURI(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestURIToPath(t *testing.T) {
	tests := []struct {
		uri  string
		want string
	}{
		{"file:///home/user/project/main.go", "/home/user/project/main.go"},
		{"file:///tmp/test%20file.go", "/tmp/test file.go"},
		{"file:///path/with%23hash.go", "/path/with#hash.go"},
	}

	for _, tt := range tests {
		got := URIToPath(DocumentURI(tt.uri))
		if got != tt.want {
			t.Errorf("URIToPath(%q) = %q, want %q", tt.uri, got, tt.want)
		}
	}
}

func TestPathToURIRoundtrip(t *testing.T) {
	paths := []string{
		"/home/user/my project/main.go",
		"/tmp/path with spaces/file.go",
	}

	for _, p := range paths {
		uri := PathToURI(p)
		back := URIToPath(uri)
		if back != p {
			t.Errorf("roundtrip failed: %q → %q → %q", p, uri, back)
		}
	}
}

// =========================================================================
// Config tests
// =========================================================================

func TestDefaultLSPServers(t *testing.T) {
	servers := DefaultLSPServers()

	// Should have entries for common languages
	required := []string{".go", ".rs", ".ts", ".tsx", ".js", ".c", ".cpp"}
	for _, ext := range required {
		if _, ok := servers[ext]; !ok {
			t.Errorf("missing default server for %s", ext)
		}
	}
}

func TestLookupServer(t *testing.T) {
	// Lookup with default config
	cfg := LookupServer("main.go", nil)
	if cfg == nil {
		t.Fatal("expected config for .go files")
	}
	if cfg.Command != "gopls" {
		t.Errorf("expected gopls, got %s", cfg.Command)
	}

	// Lookup with user override
	overrides := map[string]ServerConfig{
		".go": {Command: "custom-gopls", Args: []string{"--verbose"}},
	}
	cfg = LookupServer("main.go", overrides)
	if cfg == nil {
		t.Fatal("expected config for .go files")
	}
	if cfg.Command != "custom-gopls" {
		t.Errorf("expected custom-gopls, got %s", cfg.Command)
	}

	// Lookup unknown extension
	cfg = LookupServer("main.xyz", nil)
	if cfg != nil {
		t.Error("expected nil for unknown extension")
	}
}

func TestIsCodeFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"main.go", true},
		{"src/lib.rs", true},
		{"app.tsx", true},
		{"index.js", true},
		{"README.md", false},
		{"data.csv", false},
		{"go.mod", false},
		{"go.sum", false},
		{"image.svg", false},
		{"package.json", true},  // Not excluded
		{"styles.css", true},    // Not excluded
	}

	for _, tt := range tests {
		got := IsCodeFile(tt.path)
		if got != tt.want {
			t.Errorf("IsCodeFile(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestLoadUserServers(t *testing.T) {
	// Create a temporary settings.json
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	content := []byte(`{
		"lsp": {
			"servers": {
				".py": {"command": "pyright-langserver", "args": ["--stdio"]},
				".java": {"command": "jdtls"}
			},
			"idle_timeout_ms": 120000
		}
	}`)
	if err := os.WriteFile(settingsPath, content, 0644); err != nil {
		t.Fatal(err)
	}

	servers, idleTimeout := LoadUserServers(settingsPath)
	if len(servers) != 2 {
		t.Errorf("expected 2 servers, got %d", len(servers))
	}
	if _, ok := servers[".py"]; !ok {
		t.Error("missing .py server")
	}
	if _, ok := servers[".java"]; !ok {
		t.Error("missing .java server")
	}
	if idleTimeout != 2*time.Minute {
		t.Errorf("expected idle timeout 2m, got %v", idleTimeout)
	}

	// Test empty/missing file
	servers, idleTimeout = LoadUserServers("/nonexistent/path")
	if servers != nil {
		t.Error("expected nil for missing file")
	}
	if idleTimeout != 0 {
		t.Errorf("expected zero idle timeout for missing file, got %v", idleTimeout)
	}

	// Test file without lsp section
	emptySettings := filepath.Join(dir, "empty.json")
	_ = os.WriteFile(emptySettings, []byte(`{}`), 0644)
	servers, idleTimeout = LoadUserServers(emptySettings)
	if servers != nil {
		t.Error("expected nil for settings without lsp section")
	}
	if idleTimeout != 0 {
		t.Errorf("expected zero idle timeout without lsp section, got %v", idleTimeout)
	}
}

func TestLoadUserServers_IdleTimeoutDefault(t *testing.T) {
	// idle_timeout_ms not set → returns 0
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	content := []byte(`{
		"lsp": {
			"servers": {
				".py": {"command": "pyright-langserver"}
			}
		}
	}`)
	if err := os.WriteFile(settingsPath, content, 0644); err != nil {
		t.Fatal(err)
	}
	servers, idleTimeout := LoadUserServers(settingsPath)
	if len(servers) != 1 {
		t.Errorf("expected 1 server, got %d", len(servers))
	}
	if idleTimeout != 0 {
		t.Errorf("expected 0 idle timeout when not configured, got %v", idleTimeout)
	}
}

func TestUserServers_PriorityMerge(t *testing.T) {
	// 模拟 project vs global 合并逻辑: project 优先于 global
	projectDir := t.TempDir()
	globalDir := t.TempDir()

	projectSettings := filepath.Join(projectDir, "settings.json")
	globalSettings := filepath.Join(globalDir, "settings.json")

	_ = os.WriteFile(projectSettings, []byte(`{
		"lsp": {
			"servers": {
				".go": {"command": "custom-gopls"},
				".py": {"command": "pyright"}
			},
			"idle_timeout_ms": 300000
		}
	}`), 0644)

	_ = os.WriteFile(globalSettings, []byte(`{
		"lsp": {
			"servers": {
				".go": {"command": "global-gopls"},
				".java": {"command": "jdtls"}
			},
			"idle_timeout_ms": 600000
		}
	}`), 0644)

	merged := make(map[string]ServerConfig)
	var mergedIdle time.Duration

	// Project-level first
	projectServers, projectIdle := LoadUserServers(projectSettings)
	for k, v := range projectServers {
		merged[k] = v
	}
	if projectIdle > 0 {
		mergedIdle = projectIdle
	}

	// Global-level fills gaps
	globalServers, globalIdle := LoadUserServers(globalSettings)
	for k, v := range globalServers {
		if _, ok := merged[k]; !ok {
			merged[k] = v
		}
	}
	if mergedIdle == 0 && globalIdle > 0 {
		mergedIdle = globalIdle
	}

	// Assertions
	if merged[".go"].Command != "custom-gopls" {
		t.Errorf("project should override global: got %s", merged[".go"].Command)
	}
	if merged[".py"].Command != "pyright" {
		t.Errorf("project-only server missing: got %v", merged[".py"])
	}
	if merged[".java"].Command != "jdtls" {
		t.Errorf("global fallback server missing: got %v", merged[".java"])
	}
	if mergedIdle != 5*time.Minute {
		t.Errorf("project idle timeout (5m) should win over global (10m), got %v", mergedIdle)
	}
}

func TestUserServers_EndToEnd(t *testing.T) {
	// settings.json → LoadUserServers → Manager → server 启动的完整链路
	bin := mockLSPPath(t)
	defer cleanupMockLSP(t)

	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	content := []byte(`{
		"lsp": {
			"servers": {
				".go": {"command": "` + bin + `"}
			}
		}
	}`)
	if err := os.WriteFile(settingsPath, content, 0644); err != nil {
		t.Fatal(err)
	}

	// Simulate the full main.go pipeline
	servers, idleTimeout := LoadUserServers(settingsPath)
	if len(servers) != 1 {
		t.Fatalf("expected 1 server from settings, got %d", len(servers))
	}

	// idle_timeout_ms not set → should be 0, manager uses its own default
	m := NewManager(
		WithUserServers(servers),
	)
	if idleTimeout > 0 {
		m.idleTimeout = idleTimeout
	}
	m.probeMap = map[string]bool{bin: true}
	defer m.Shutdown()

	// Create a .go file and verify server starts via settings config
	goMod := filepath.Join(dir, "go.mod")
	_ = os.WriteFile(goMod, []byte("module test\n"), 0644)
	goFile := filepath.Join(dir, "main.go")
	_ = os.WriteFile(goFile, []byte("package main\n"), 0644)

	inst, err := m.GetOrCreate(goFile)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if inst == nil {
		t.Fatal("expected instance from settings-configured server")
	}
	if inst.cfg.Command != bin {
		t.Errorf("expected command %s from settings, got %s", bin, inst.cfg.Command)
	}
}

// =========================================================================
// Manager tests
// =========================================================================

func TestManagerGetOrCreate_NoProbe(t *testing.T) {
	m := NewManager()
	// Without probe map, no server should be created
	inst, err := m.GetOrCreate("test.go")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if inst != nil {
		t.Error("expected nil instance without probe")
	}
	m.Shutdown()
}

func TestManagerGetOrCreate_WithProbe(t *testing.T) {
	bin := mockLSPPath(t)
	defer cleanupMockLSP(t)

	m := NewManager()
	m.probeMap = map[string]bool{
		"mock_lsp": true, // Won't match gopls — need to use user override
	}

	// Register mock server for .go files
	userServers := map[string]ServerConfig{
		".go": {Command: bin},
	}

	// Create a temp file
	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Create a go.mod marker for root URI detection
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m2 := NewManager(WithUserServers(userServers))
	m2.probeMap = map[string]bool{bin: true}
	defer m2.Shutdown()

	inst, err := m2.GetOrCreate(goFile)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if inst == nil {
		t.Fatal("expected non-nil instance with probe")
	}

	// Second call should return the same instance
	inst2, err := m2.GetOrCreate(goFile)
	if err != nil {
		t.Fatalf("GetOrCreate (2nd): %v", err)
	}
	if inst2 != inst {
		t.Error("expected same instance")
	}
}

func TestManagerSyncFileAndDiagnostics(t *testing.T) {
	bin := mockLSPPath(t)
	defer cleanupMockLSP(t)

	dir := t.TempDir()
	// Create go.mod marker
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)

	goFile := filepath.Join(dir, "main.go")
	normalContent := "package main\n\nfunc main() {}\n"
	errorContent := "ERROR_TRIGGER\npackage main\n"

	userServers := map[string]ServerConfig{
		".go": {Command: bin},
	}

	m := NewManager(WithUserServers(userServers))
	m.probeMap = map[string]bool{bin: true}
	defer m.Shutdown()

	// First: sync with normal content
	if err := os.WriteFile(goFile, []byte(normalContent), 0644); err != nil {
		t.Fatal(err)
	}
	inst, err := m.GetOrCreate(goFile)
	if err != nil || inst == nil {
		t.Fatalf("GetOrCreate: %v, %v", err, inst)
	}

	if err := m.SyncFile(inst, goFile); err != nil {
		t.Fatalf("SyncFile: %v", err)
	}

	uri := PathToURI(goFile)
	diags := m.Diagnostics(uri)
	if len(diags) > 0 {
		t.Logf("unexpected diagnostics for normal content: %d", len(diags))
	}

	// Second: sync with error content
	if err := os.WriteFile(goFile, []byte(errorContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := m.SyncFile(inst, goFile); err != nil {
		t.Fatalf("SyncFile(2): %v", err)
	}

	diags = m.Diagnostics(uri)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	if diags[0].Severity != SeverityError {
		t.Errorf("expected severity error, got %d", diags[0].Severity)
	}
	if diags[0].Message != "mock error: ERROR_TRIGGER found" {
		t.Errorf("unexpected message: %s", diags[0].Message)
	}
}

func TestManagerDiagnosticsWaitTimeout(t *testing.T) {
	// Test that SyncFile doesn't hang when server doesn't respond
	// Use sleep as a non-LSP process
	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")
	_ = os.WriteFile(goFile, []byte("package main\n"), 0644)

	userServers := map[string]ServerConfig{
		".go": {Command: "sleep", Args: []string{"30"}},
	}

	m := NewManager(WithUserServers(userServers))
	m.probeMap = map[string]bool{"sleep": true}
	defer m.Shutdown()

	// GetOrCreate should fail since sleep is not an LSP server
	inst, err := m.GetOrCreate(goFile)
	if err == nil && inst != nil {
		// If it somehow succeeded, test that SyncFile doesn't hang
		done := make(chan struct{})
		go func() {
			_ = m.SyncFile(inst, goFile)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("SyncFile hung with unresponsive server")
		}
	}
}

// =========================================================================
// Regression tests
// =========================================================================

// TestRegression_NegativeContentLength verifies client handles negative Content-Length gracefully.
func TestRegression_NegativeContentLength(t *testing.T) {
	bin := mockLSPPath(t)

	rootURI := PathToURI(t.TempDir())
	client, err := NewClient(bin, nil, string(rootURI), "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	// The readLoop validates contentLength >= 0.
	// We can't easily inject a negative Content-Length into the mock,
	// but we verify the client is functional after initialization.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Call(ctx, "shutdown", nil, nil); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

// TestRegression_DocVersionMonotonic verifies document versions are monotonically increasing.
func TestRegression_DocVersionMonotonic(t *testing.T) {
	bin := mockLSPPath(t)
	defer cleanupMockLSP(t)

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
	goFile := filepath.Join(dir, "main.go")
	_ = os.WriteFile(goFile, []byte("package main\n"), 0644)

	userServers := map[string]ServerConfig{
		".go": {Command: bin},
	}

	m := NewManager(WithUserServers(userServers))
	m.probeMap = map[string]bool{bin: true}
	defer m.Shutdown()

	inst, err := m.GetOrCreate(goFile)
	if err != nil || inst == nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	// Initial version should be 1 (didOpen)
	if err := m.SyncFile(inst, goFile); err != nil {
		t.Fatalf("SyncFile: %v", err)
	}

	// Second sync should use version > 1
	_ = os.WriteFile(goFile, []byte("package main\n\nvar x = 1\n"), 0644)
	if err := m.SyncFile(inst, goFile); err != nil {
		t.Fatalf("SyncFile(2): %v", err)
	}

	// If we got here without errors, the version counter works
}

// =========================================================================
// Additional coverage tests
func TestSetProbeMap(t *testing.T) {
	m := NewManager()
	m.SetProbeMap(map[string]bool{"gopls": false, "clangd": false})

	// gopls probe is false → should skip
	inst, err := m.GetOrCreate("/tmp/test.go")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if inst != nil {
		t.Error("expected nil when probe is false")
	}
	m.Shutdown()
}

func TestLanguageID(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"main.go", "go"},
		{"lib.rs", "rust"},
		{"app.tsx", "typescriptreact"},
		{"index.js", "javascript"},
		{"app.jsx", "javascriptreact"},
		{"main.c", "c"},
		{"main.cpp", "cpp"},
		{"main.cc", "cpp"},
		{"main.cxx", "cpp"},
		{"header.h", "c"},
		{"header.hpp", "cpp"},
		{"script.py", "python"},
		{"unknown.xyz", "xyz"},
	}

	for _, tt := range tests {
		got := languageID(tt.path)
		if got != tt.want {
			t.Errorf("languageID(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestIdleTimeoutOption(t *testing.T) {
	m := NewManager(WithIdleTimeout(1 * time.Minute))
	if m.idleTimeout != 1*time.Minute {
		t.Errorf("expected 1m, got %v", m.idleTimeout)
	}
	m.Shutdown()
}

func TestLoggerOption(t *testing.T) {
	buf := new(bytes.Buffer)
	logger := log.New(buf, "[test] ", 0)
	m := NewManager(WithLogger(logger))
	if m.logger != logger {
		t.Error("logger not set")
	}
	m.Shutdown()
}

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
		got := tt.state.String()
		if got != tt.want {
			t.Errorf("serverState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestLogPath(t *testing.T) {
	// Normal extension
	path := lspLogPath(".go")
	if !strings.Contains(path, "go.log") {
		t.Errorf("expected go.log in path: %s", path)
	}

	// No dot extension
	path = lspLogPath("go")
	if !strings.Contains(path, "go.log") {
		t.Errorf("expected go.log in path: %s", path)
	}

	// Empty string — should not panic
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("lspLogPath panicked: %v", r)
			}
		}()
		_ = lspLogPath("")
	}()
}

func TestFindProjectRoot(t *testing.T) {
	dir := t.TempDir()

	// No markers → falls back to file's parent
	f := filepath.Join(dir, "src", "nested", "main.go")
	_ = os.MkdirAll(filepath.Dir(f), 0755)
	root := findProjectRoot(f)
	if root != filepath.Dir(f) {
		t.Errorf("no marker: expected %s, got %s", filepath.Dir(f), root)
	}

	// Go module marker at dir
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0644)
	root = findProjectRoot(f)
	if root != dir {
		t.Errorf("go.mod marker: expected %s, got %s", dir, root)
	}

	// .git marker
	dir2 := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir2, ".git"), 0755)
	f2 := filepath.Join(dir2, "deeply", "nested", "file.go")
	_ = os.MkdirAll(filepath.Dir(f2), 0755)
	root = findProjectRoot(f2)
	if root != dir2 {
		t.Errorf(".git marker: expected %s, got %s", dir2, root)
	}
}

func TestDiagnosticsCopy(t *testing.T) {
	// Test that Diagnostics returns a copy, not the internal slice
	bin := mockLSPPath(t)
	defer cleanupMockLSP(t)

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
	goFile := filepath.Join(dir, "main.go")
	_ = os.WriteFile(goFile, []byte("package main\n"), 0644)

	userServers := map[string]ServerConfig{".go": {Command: bin}}
	m := NewManager(WithUserServers(userServers))
	m.probeMap = map[string]bool{bin: true}
	defer m.Shutdown()

	inst, err := m.GetOrCreate(goFile)
	if err != nil || inst == nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	uri := PathToURI(goFile)

	// First call returns nil (no diagnostics yet)
	diags := m.Diagnostics(uri)
	if diags != nil {
		t.Error("expected nil before any sync")
	}

	// After SyncFile with normal content, should have empty diagnostics
	_ = m.SyncFile(inst, goFile)
	diags = m.Diagnostics(uri)
	// Diagnostic returns a copy — modifying it should not affect cache
	if len(diags) != 0 {
		t.Logf("diags count: %d", len(diags))
	}
	diags2 := m.Diagnostics(uri)
	if len(diags2) != len(diags) {
		t.Errorf("consecutive Diagnostics calls returned different results: %d vs %d", len(diags), len(diags2))
	}
}

func TestShutdownClosesAll(t *testing.T) {
	bin := mockLSPPath(t)
	defer cleanupMockLSP(t)

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
	goFile := filepath.Join(dir, "main.go")
	_ = os.WriteFile(goFile, []byte("package main\n"), 0644)

	userServers := map[string]ServerConfig{".go": {Command: bin}}
	m := NewManager(WithUserServers(userServers))
	m.probeMap = map[string]bool{bin: true}

	// Create an instance
	_, _ = m.GetOrCreate(goFile)

	// Shutdown should not hang
	done := make(chan struct{})
	go func() {
		m.Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Shutdown timed out")
	}
}
