package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/pathutil"
)

func TestFindProjectRoot_WithGit(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}

	root := pathutil.FindProjectRoot(dir)
	if root != dir {
		t.Fatalf("expected %q, got %q", dir, root)
	}
}

func TestFindProjectRoot_WithGitFile(t *testing.T) {
	dir := t.TempDir()
	gitFile := filepath.Join(dir, ".git")
	if err := os.WriteFile(gitFile, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	root := pathutil.FindProjectRoot(dir)
	if root != dir {
		t.Fatalf("expected %q, got %q", dir, root)
	}
}

func TestFindProjectRoot_NoGit(t *testing.T) {
	dir := t.TempDir()
	root := pathutil.FindProjectRoot(dir)
	if root != "" {
		t.Fatalf("expected empty, got %q", root)
	}
}

func TestFindProjectRoot_Subdirectory(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "cmd", "waveloom")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	root := pathutil.FindProjectRoot(sub)
	if root != dir {
		t.Fatalf("expected %q, got %q", dir, root)
	}
}

func TestDiscoverAgentsMd_Found(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(f, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := discoverAgentsMd(dir)
	if got != f {
		t.Fatalf("expected %q, got %q", f, got)
	}
}

func TestDiscoverAgentsMd_NotFound(t *testing.T) {
	dir := t.TempDir()
	got := discoverAgentsMd(dir)
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestDiscoverAgentsMd_IsDirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "AGENTS.md")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	got := discoverAgentsMd(dir)
	if got != "" {
		t.Fatalf("expected empty for directory, got %q", got)
	}
}

func TestLoad_NoFiles(t *testing.T) {
	dir := t.TempDir()
	loader := NewLoader(dir, "")

	text, warnings, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	if text != "" {
		t.Fatalf("expected empty text, got %q", text)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestLoad_SingleFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("convention 1"), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(dir, "")
	text, warnings, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	if !strings.Contains(text, "convention 1") {
		t.Fatalf("expected content in text, got: %s", text)
	}
	if !strings.Contains(text, "<INSTRUCTIONS>") || !strings.Contains(text, "</INSTRUCTIONS>") {
		t.Fatalf("expected <INSTRUCTIONS> wrappers, got: %s", text)
	}
	if !strings.Contains(text, "## "+filepath.Join(dir, "AGENTS.md")) {
		t.Fatalf("expected ## {path} label, got: %s", text)
	}
	if !strings.Contains(text, "# AGENTS.md instructions for") {
		t.Fatalf("expected # AGENTS.md instructions for header, got: %s", text)
	}
}

func TestLoad_Hierarchical(t *testing.T) {
	dir := t.TempDir()

	// git init
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// root AGENTS.md
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("root convention"), 0o644); err != nil {
		t.Fatal(err)
	}
	// sub AGENTS.md
	sub := filepath.Join(dir, "cmd", "waveloom")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte("sub convention"), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(sub, "")
	text, warnings, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	// root should appear before sub in text
	rootIdx := strings.Index(text, "root convention")
	subIdx := strings.Index(text, "sub convention")
	if rootIdx < 0 || subIdx < 0 {
		t.Fatalf("expected both conventions, got: %s", text)
	}
	if rootIdx > subIdx {
		t.Fatalf("expected root before sub, root=%d sub=%d, text: %s", rootIdx, subIdx, text)
	}

	// both should have ## labels
	if !strings.Contains(text, "## "+filepath.Join(dir, "AGENTS.md")) {
		t.Fatalf("expected root path label, got: %s", text)
	}
	if !strings.Contains(text, "## "+filepath.Join(sub, "AGENTS.md")) {
		t.Fatalf("expected sub path label, got: %s", text)
	}
}

func TestLoad_GlobalPlusHierarchical(t *testing.T) {
	homeDir := t.TempDir()

	// global AGENTS.md
	waveloomDir := filepath.Join(homeDir, ".waveloom")
	if err := os.MkdirAll(waveloomDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(waveloomDir, "AGENTS.md"), []byte("global convention"), 0o644); err != nil {
		t.Fatal(err)
	}

	// project
	projectDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(projectDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), []byte("project convention"), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(projectDir, homeDir)
	text, warnings, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	globalIdx := strings.Index(text, "global convention")
	projectIdx := strings.Index(text, "project convention")
	if globalIdx < 0 || projectIdx < 0 {
		t.Fatalf("expected both conventions, got: %s", text)
	}
	if globalIdx > projectIdx {
		t.Fatalf("expected global before project, global=%d project=%d", globalIdx, projectIdx)
	}

	if !strings.Contains(text, "~/.waveloom/AGENTS.md") {
		t.Fatalf("expected ~ shorthand for global path, got: %s", text)
	}
}

func TestLoad_GlobalMemory(t *testing.T) {
	homeDir := t.TempDir()
	waveloomDir := filepath.Join(homeDir, ".waveloom")
	if err := os.MkdirAll(waveloomDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(waveloomDir, "AGENTS.md"), []byte("global"), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(t.TempDir(), homeDir)
	text, _, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "global") {
		t.Fatalf("expected global content, got: %s", text)
	}
	if !strings.Contains(text, "~/.waveloom/AGENTS.md") {
		t.Fatalf("expected ~ shorthand, got: %s", text)
	}
}

func TestLoad_MaxBytesTruncation(t *testing.T) {
	dir := t.TempDir()
	// Create a file larger than maxBytes
	big := strings.Repeat("x", maxBytes+100)
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(dir, "")
	text, warnings, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) == 0 {
		t.Fatal("expected truncation warning, got none")
	}
	if !strings.Contains(text, "截断") && !strings.Contains(text, "truncat") {
		t.Fatalf("expected truncation notice in text, got: %s", text)
	}
}

func TestLoad_InvalidUtf8(t *testing.T) {
	dir := t.TempDir()
	// Write invalid UTF-8: 0xff is never valid
	data := []byte("hello\xffworld")
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(dir, "")
	_, warnings, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) == 0 {
		t.Fatal("expected UTF-8 warning, got none")
	}
}

func TestLoad_ReadError(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(f, []byte("content"), 0o000); err != nil {
		t.Fatal(err)
	}
	// Make file unreadable
	if err := os.Chmod(f, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(f, 0o644) }()

	loader := NewLoader(dir, "")
	text, warnings, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	if text != "" {
		t.Fatalf("expected empty text, got %q", text)
	}
	if len(warnings) == 0 {
		t.Fatal("expected warning for unreadable file, got none")
	}
}

func TestLoad_UnreadableDir_IsSkipped(t *testing.T) {
	// When a directory in the chain exists but has no AGENTS.md,
	// it should be silently skipped — not cause errors.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Only root has AGENTS.md
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("root"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create an intermediate dir without AGENTS.md
	mid := filepath.Join(dir, "pkg")
	if err := os.Mkdir(mid, 0o755); err != nil {
		t.Fatal(err)
	}
	// CWD has AGENTS.md
	leaf := filepath.Join(dir, "pkg", "llm")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "AGENTS.md"), []byte("leaf"), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(leaf, "")
	text, warnings, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) > 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if !strings.Contains(text, "root") || !strings.Contains(text, "leaf") {
		t.Fatalf("expected both root and leaf, got: %s", text)
	}
	// mid directory (pkg) has no AGENTS.md → should not appear
	midLabel := "## " + filepath.Join(dir, "pkg", "AGENTS.md")
	if strings.Contains(text, midLabel) {
		t.Fatalf("expected no label for mid dir without AGENTS.md, got: %s", text)
	}
}

func TestDirChain(t *testing.T) {
	root := "/a"
	leaf := "/a/b/c"
	chain := dirChain(root, leaf)
	expected := []string{"/a", "/a/b", "/a/b/c"}
	if len(chain) != len(expected) {
		t.Fatalf("expected %d dirs, got %d: %v", len(expected), len(chain), chain)
	}
	for i, d := range expected {
		if chain[i] != d {
			t.Fatalf("chain[%d]: expected %q, got %q", i, d, chain[i])
		}
	}
}
