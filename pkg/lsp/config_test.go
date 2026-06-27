package lsp

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// ---------------------------------------------------------------------------
// DefaultServerConfigs
// ---------------------------------------------------------------------------

func TestDefaultServerConfigs_ContainsGo(t *testing.T) {
	cfgs := DefaultServerConfigs()
	cfg, ok := cfgs[".go"]
	if !ok {
		t.Fatal(".go not found in default configs")
	}
	if cfg.Command != "gopls" {
		t.Errorf("expected gopls, got %q", cfg.Command)
	}
}

func TestDefaultServerConfigs_ContainsRust(t *testing.T) {
	cfgs := DefaultServerConfigs()
	cfg, ok := cfgs[".rs"]
	if !ok {
		t.Fatal(".rs not found in default configs")
	}
	if cfg.Command != "rust-analyzer" {
		t.Errorf("expected rust-analyzer, got %q", cfg.Command)
	}
}

func TestDefaultServerConfigs_TypeScriptHasArgs(t *testing.T) {
	cfgs := DefaultServerConfigs()
	cfg, ok := cfgs[".ts"]
	if !ok {
		t.Fatal(".ts not found in default configs")
	}
	if cfg.Command != "typescript-language-server" {
		t.Errorf("expected typescript-language-server, got %q", cfg.Command)
	}
	if len(cfg.Args) != 1 || cfg.Args[0] != "--stdio" {
		t.Errorf("expected [--stdio], got %v", cfg.Args)
	}
}

func TestDefaultServerConfigs_Python(t *testing.T) {
	cfgs := DefaultServerConfigs()
	cfg, ok := cfgs[".py"]
	if !ok {
		t.Fatal(".py not found in default configs")
	}
	if cfg.Command != "pyright-langserver" {
		t.Errorf("expected pyright-langserver, got %q", cfg.Command)
	}
}

func TestDefaultServerConfigs_GoMod(t *testing.T) {
	cfgs := DefaultServerConfigs()
	if _, ok := cfgs[".mod"]; !ok {
		t.Error(".mod not found")
	}
	if _, ok := cfgs[".sum"]; !ok {
		t.Error(".sum not found")
	}
	if _, ok := cfgs[".work"]; !ok {
		t.Error(".work not found")
	}
}

// ---------------------------------------------------------------------------
// LookupServer
// ---------------------------------------------------------------------------

func TestLookupServer_DefaultGo(t *testing.T) {
	cfg := LookupServer("main.go", nil)
	if cfg == nil {
		t.Fatal("expected config for .go")
	}
	if cfg.Command != "gopls" {
		t.Errorf("expected gopls, got %q", cfg.Command)
	}
}

func TestLookupServer_DefaultRust(t *testing.T) {
	cfg := LookupServer("lib.rs", nil)
	if cfg == nil {
		t.Fatal("expected config for .rs")
	}
	if cfg.Command != "rust-analyzer" {
		t.Errorf("expected rust-analyzer, got %q", cfg.Command)
	}
}

func TestLookupServer_NoExtension(t *testing.T) {
	cfg := LookupServer("Makefile", nil)
	if cfg != nil {
		t.Error("expected nil for file without extension")
	}
}

func TestLookupServer_UnknownExtension(t *testing.T) {
	cfg := LookupServer("file.xyz", nil)
	if cfg != nil {
		t.Error("expected nil for unknown extension")
	}
}

func TestLookupServer_UserOverride(t *testing.T) {
	overrides := map[string]ServerConfig{
		".go": {Command: "custom-gopls", Args: []string{"-v"}},
	}
	cfg := LookupServer("main.go", overrides)
	if cfg == nil {
		t.Fatal("expected config for .go")
	}
	if cfg.Command != "custom-gopls" {
		t.Errorf("expected custom-gopls, got %q", cfg.Command)
	}
	if len(cfg.Args) != 1 || cfg.Args[0] != "-v" {
		t.Errorf("expected [-v], got %v", cfg.Args)
	}
}

func TestLookupServer_UserOverrideNewExt(t *testing.T) {
	overrides := map[string]ServerConfig{
		".zig": {Command: "zls"},
	}
	cfg := LookupServer("main.zig", overrides)
	if cfg == nil {
		t.Fatal("expected config for .zig via override")
	}
	if cfg.Command != "zls" {
		t.Errorf("expected zls, got %q", cfg.Command)
	}
}

// ---------------------------------------------------------------------------
// SupportedExtensions
// ---------------------------------------------------------------------------

func TestSupportedExtensions_Defaults(t *testing.T) {
	exts := SupportedExtensions(nil)
	if !exts[".go"] {
		t.Error("expected .go in supported extensions")
	}
	if !exts[".rs"] {
		t.Error("expected .rs in supported extensions")
	}
	if !exts[".py"] {
		t.Error("expected .py in supported extensions")
	}
	if exts[".xyz"] {
		t.Error("did not expect .xyz in supported extensions")
	}
}

func TestSupportedExtensions_WithOverrides(t *testing.T) {
	overrides := map[string]ServerConfig{
		".custom": {Command: "custom-lsp"},
	}
	exts := SupportedExtensions(overrides)
	if !exts[".go"] {
		t.Error("expected .go still present")
	}
	if !exts[".custom"] {
		t.Error("expected .custom from overrides")
	}
}

// ---------------------------------------------------------------------------
// LoadUserServers
// ---------------------------------------------------------------------------

func TestLoadUserServers_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	content := `{
		"lsp": {
			"servers": {
				".go": {"command": "custom-gopls", "args": ["-r", "trace"]},
				".py": {"command": "pylsp"}
			}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	servers := LoadUserServers(path)
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(servers))
	}
	if servers[".go"].Command != "custom-gopls" {
		t.Errorf("go command = %q", servers[".go"].Command)
	}
	if servers[".py"].Command != "pylsp" {
		t.Errorf("py command = %q", servers[".py"].Command)
	}
	if !reflect.DeepEqual(servers[".go"].Args, []string{"-r", "trace"}) {
		t.Errorf("go args = %v", servers[".go"].Args)
	}
}

func TestLoadUserServers_FileNotFound(t *testing.T) {
	servers := LoadUserServers("/nonexistent/settings.json")
	if servers != nil {
		t.Errorf("expected nil for nonexistent file, got %v", servers)
	}
}

func TestLoadUserServers_NoLSPSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	content := `{"llm": {"model": "test"}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	servers := LoadUserServers(path)
	if servers != nil {
		t.Errorf("expected nil when no lsp section, got %v", servers)
	}
}

func TestLoadUserServers_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}

	servers := LoadUserServers(path)
	if servers != nil {
		t.Errorf("expected nil for invalid JSON, got %v", servers)
	}
}

func TestLoadUserServers_EmptyServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	content := `{"lsp": {"servers": {}}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	servers := LoadUserServers(path)
	if len(servers) != 0 {
		t.Errorf("expected empty map, got %d entries", len(servers))
	}
}

func TestLoadUserServers_ServerWithoutArgs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	content := `{"lsp": {"servers": {".sol": {"command": "solc-lsp"}}}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	servers := LoadUserServers(path)
	if servers[".sol"].Command != "solc-lsp" {
		t.Errorf("command = %q", servers[".sol"].Command)
	}
	if servers[".sol"].Args != nil {
		t.Errorf("expected nil args, got %v", servers[".sol"].Args)
	}
}
