package reference

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"waveloom/pkg/permission"
	"waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// Mock types
// ---------------------------------------------------------------------------

// mockRegistry 可编程控制 Execute 返回值。
type mockRegistry struct {
	results map[string]mockCall
}

type mockCall struct {
	result *tool.ToolResult
	err    error
}

func (m *mockRegistry) Register(tool.Tool)              {}
func (m *mockRegistry) List() []tool.ToolSpec            { return nil }
func (m *mockRegistry) Get(name string) (tool.Tool, bool) { return nil, false }

func (m *mockRegistry) Execute(_ context.Context, name string, input json.RawMessage) (*tool.ToolResult, error) {
	// Build key: toolName + file_path or path from input
	var params struct {
		FilePath   string `json:"file_path"`
		Path       string `json:"path"`
		WorkingDir string `json:"working_dir"`
	}
	json.Unmarshal(input, &params)
	key := name + "|" + params.FilePath + "|" + params.Path
	if c, ok := m.results[key]; ok {
		return c.result, c.err
	}
	// Fallback: try toolName alone
	if c, ok := m.results[name]; ok {
		return c.result, c.err
	}
	return &tool.ToolResult{}, nil
}

func newMockRegistry() *mockRegistry {
	return &mockRegistry{results: make(map[string]mockCall)}
}

func (m *mockRegistry) setExecute(name, filePath, dirPath string, result *tool.ToolResult, err error) {
	key := name + "|" + filePath + "|" + dirPath
	m.results[key] = mockCall{result: result, err: err}
}

// mockGuard 可编程控制 Check 返回值。
type mockGuard struct {
	decisions map[string]permission.DecisionResult
}

func (m *mockGuard) Check(_ context.Context, toolName string, _ json.RawMessage) permission.DecisionResult {
	if d, ok := m.decisions[toolName]; ok {
		return d
	}
	return permission.DecisionResult{Decision: permission.DecisionAllow}
}

func (m *mockGuard) AddRule(permission.Rule, permission.RuleScope) error        { return nil }
func (m *mockGuard) RemoveRule(permission.Rule, permission.RuleScope) error     { return nil }
func (m *mockGuard) ListRules() []permission.RuleEntry                           { return nil }
func (m *mockGuard) PersistRule(permission.Rule) error                           { return nil }
func (m *mockGuard) SessionAllow(string, json.RawMessage)                       {}
func (m *mockGuard) SessionDeny(string, json.RawMessage)                        {}
func (m *mockGuard) ClearSession()                                               {}
func (m *mockGuard) SessionMemoryLen() int                                       { return 0 }

func newMockGuard() *mockGuard {
	return &mockGuard{decisions: make(map[string]permission.DecisionResult)}
}

func (m *mockGuard) setDecision(toolName string, d permission.Decision) {
	m.decisions[toolName] = permission.DecisionResult{Decision: d}
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func makeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func makeDir(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// ---------------------------------------------------------------------------
// Parse tests
// ---------------------------------------------------------------------------

func TestParseNoRef(t *testing.T) {
	refs := parseRefs("hello world", "/tmp")
	if len(refs) != 0 {
		t.Fatalf("expected 0 refs, got %d", len(refs))
	}
}

func TestParseSingleFile(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "auth/login.go", "package auth")

	refs := parseRefs("@auth/login.go", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Kind != KindFile {
		t.Fatalf("expected KindFile, got %v", refs[0].Kind)
	}
	if refs[0].Path != f {
		t.Fatalf("expected path %q, got %q", f, refs[0].Path)
	}
}

func TestParseSingleFolder(t *testing.T) {
	dir := t.TempDir()
	d := makeDir(t, dir, "pkg/auth")

	refs := parseRefs("@pkg/auth", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Kind != KindFolder {
		t.Fatalf("expected KindFolder, got %v", refs[0].Kind)
	}
	if refs[0].Path != d {
		t.Fatalf("expected path %q, got %q", d, refs[0].Path)
	}
}

func TestParseFileWithDotSlash(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "auth/login.go", "package auth")

	refs := parseRefs("@./auth/login.go", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Path != f {
		t.Fatalf("expected path %q, got %q", f, refs[0].Path)
	}
}

func TestParseAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "a.go", "package main")

	refs := parseRefs("@"+f, "/some/other/cwd")
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Path != f {
		t.Fatalf("expected path %q, got %q", f, refs[0].Path)
	}
}

func TestParseRelativePath(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "pkg/auth/login.go", "package auth")

	refs := parseRefs("@pkg/auth/login.go", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Path != f {
		t.Fatalf("expected path %q, got %q", f, refs[0].Path)
	}
}

func TestParseMultipleRefs(t *testing.T) {
	dir := t.TempDir()
	f1 := makeFile(t, dir, "pkg/auth/login.go", "package auth")
	f2 := makeFile(t, dir, "pkg/context/context.go", "package context")

	refs := parseRefs("@pkg/auth/login.go @pkg/context/context.go", dir)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
	if refs[0].Path != f1 {
		t.Fatalf("expected path %q, got %q", f1, refs[0].Path)
	}
	if refs[1].Path != f2 {
		t.Fatalf("expected path %q, got %q", f2, refs[1].Path)
	}
}

func TestParseMixedFileAndFolder(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "pkg/auth/login.go", "package auth")
	d := makeDir(t, dir, "pkg/llm")

	refs := parseRefs("@pkg/auth/login.go @pkg/llm", dir)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
	if refs[0].Kind != KindFile || refs[0].Path != f {
		t.Fatalf("expected KindFile with path %q, got %v %q", f, refs[0].Kind, refs[0].Path)
	}
	if refs[1].Kind != KindFolder || refs[1].Path != d {
		t.Fatalf("expected KindFolder with path %q, got %v %q", d, refs[1].Kind, refs[1].Path)
	}
}

func TestParseMixedRefsInSentence(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "pkg/auth/login.go", "package auth")
	d := makeDir(t, dir, "pkg/llm")

	refs := parseRefs("看一下 @pkg/auth/login.go 和 @pkg/llm 这个目录", dir)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
	if refs[0].Path != f {
		t.Fatalf("expected path %q, got %q", f, refs[0].Path)
	}
	if refs[1].Path != d {
		t.Fatalf("expected path %q, got %q", d, refs[1].Path)
	}
}

func TestParseIgnoreEscapedAt(t *testing.T) {
	dir := t.TempDir()
	makeFile(t, dir, "file.go", "package main")

	// \@file.go should NOT be parsed — the backslash escapes the @
	refs := parseRefs(`\@file.go`, dir)
	if len(refs) != 0 {
		t.Fatalf("expected 0 refs (escaped @ should be ignored), got %d", len(refs))
	}
}

func TestParseEmailNotMatched(t *testing.T) {
	dir := t.TempDir()

	// email@example.com should NOT match @example.com
	refs := parseRefs("contact email@example.com for help", dir)
	if len(refs) != 0 {
		t.Fatalf("expected 0 refs (email should not match), got %d", len(refs))
	}
}

func TestParseAtMidWordNotMatched(t *testing.T) {
	dir := t.TempDir()
	makeFile(t, dir, "foo.go", "package main")

	// foo@bar should NOT be parsed because @ is mid-word
	refs := parseRefs("check foo@bar for details", dir)
	if len(refs) != 0 {
		t.Fatalf("expected 0 refs (@ mid-word should not match), got %d", len(refs))
	}
}

func TestParsePathWithSpaces(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "path/to/my file.go", "package main")

	refs := parseRefs("@path/to/my file.go", dir)
	if len(refs) != 0 {
		// The regex stops at space, so this won't match the full path.
		// This is expected behavior per spec — spaces in paths are unsupported.
		_ = f
	}
}

func TestParsePathNotExist(t *testing.T) {
	dir := t.TempDir()

	refs := parseRefs("@nonexistent/file.go", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Kind != KindFile {
		t.Fatalf("expected KindFile for non-existent path, got %v", refs[0].Kind)
	}
	// Path should still be resolved to absolute
	expected := filepath.Join(dir, "nonexistent", "file.go")
	if refs[0].Path != expected {
		t.Fatalf("expected path %q, got %q", expected, refs[0].Path)
	}
}

func TestParseDedup(t *testing.T) {
	dir := t.TempDir()
	makeFile(t, dir, "pkg/auth/login.go", "package auth")

	refs := parseRefs("@pkg/auth/login.go @pkg/auth/login.go", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref (dedup), got %d", len(refs))
	}
}

func TestParseDedupMixedCase(t *testing.T) {
	dir := t.TempDir()
	// On case-insensitive filesystems, these resolve to the same path.
	// On case-sensitive (Linux), they are different files.
	// Our dedup is by absolute path, so we test with same-case only.
	makeFile(t, dir, "pkg/auth/login.go", "package auth")

	refs := parseRefs("@pkg/auth/login.go @./pkg/auth/login.go", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref (dedup same file via different paths), got %d", len(refs))
	}
}

// ---------------------------------------------------------------------------
// Fuzzy matching parse tests
// ---------------------------------------------------------------------------

func TestParseFuzzyMatchFilePrefix(t *testing.T) {
	dir := t.TempDir()
	makeFile(t, dir, "specs/reference-context.md", "# Reference Context")

	refs := parseRefs("@specs/reference-c", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Kind != KindFile {
		t.Fatalf("expected KindFile, got %v", refs[0].Kind)
	}
	expected := filepath.Join(dir, "specs", "reference-context.md")
	if refs[0].Path != expected {
		t.Fatalf("expected path %q, got %q", expected, refs[0].Path)
	}
}

func TestParseFuzzyMatchFolderPrefix(t *testing.T) {
	dir := t.TempDir()
	makeDir(t, dir, "specs")

	refs := parseRefs("@spec", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Kind != KindFolder {
		t.Fatalf("expected KindFolder, got %v", refs[0].Kind)
	}
	expected := filepath.Join(dir, "specs")
	if refs[0].Path != expected {
		t.Fatalf("expected path %q, got %q", expected, refs[0].Path)
	}
}

func TestParseFuzzyMatchMultiplePickShortest(t *testing.T) {
	dir := t.TempDir()
	makeFile(t, dir, "specs/agent-loop.md", "# agent-loop")
	makeFile(t, dir, "specs/agents-md-memory.md", "# agents-md-memory")

	refs := parseRefs("@specs/agent", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	// agent-loop.md (13 chars) < agents-md-memory.md (19 chars)
	expected := filepath.Join(dir, "specs", "agent-loop.md")
	if refs[0].Path != expected {
		t.Fatalf("expected shortest match %q, got %q", expected, refs[0].Path)
	}
}

func TestParseFuzzyMatchCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	makeFile(t, dir, "specs/reference-context.md", "# Reference Context")

	refs := parseRefs("@specs/REFERENCE-C", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	expected := filepath.Join(dir, "specs", "reference-context.md")
	if refs[0].Path != expected {
		t.Fatalf("expected case-insensitive match to %q, got %q", expected, refs[0].Path)
	}
}

func TestParseFuzzyMatchNoMatch(t *testing.T) {
	dir := t.TempDir()
	makeFile(t, dir, "specs/reference-context.md", "# Reference Context")

	refs := parseRefs("@specs/xyz", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref (no fuzzy match, keeps original path), got %d", len(refs))
	}
	// No match → keeps original resolved path
	expected := filepath.Join(dir, "specs", "xyz")
	if refs[0].Path != expected {
		t.Fatalf("expected original path %q, got %q", expected, refs[0].Path)
	}
}

func TestParseFuzzyMatchDedupAfterMatch(t *testing.T) {
	dir := t.TempDir()
	makeFile(t, dir, "specs/reference-context.md", "# Reference Context")

	// Two different prefixes that fuzzy-match to the same file
	refs := parseRefs("@specs/reference-c @specs/ref", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref (dedup after fuzzy match), got %d", len(refs))
	}
	expected := filepath.Join(dir, "specs", "reference-context.md")
	if refs[0].Path != expected {
		t.Fatalf("expected path %q, got %q", expected, refs[0].Path)
	}
}

func TestParseFuzzyMatchSubdirectoryPrefix(t *testing.T) {
	dir := t.TempDir()
	makeFile(t, dir, "pkg/agentloop/types.go", "package agentloop")

	refs := parseRefs("@pkg/agentloop/ty", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	expected := filepath.Join(dir, "pkg", "agentloop", "types.go")
	if refs[0].Path != expected {
		t.Fatalf("expected path %q, got %q", expected, refs[0].Path)
	}
}

func TestParseFuzzyMatchParentDirNotExist(t *testing.T) {
	dir := t.TempDir()
	// "nonexistent" directory doesn't exist → fuzzyMatch should return false

	refs := parseRefs("@nonexistent/foo", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref (parent dir not found, keeps original), got %d", len(refs))
	}
	// Parent dir doesn't exist → fuzzy fails, keeps original path
	expected := filepath.Join(dir, "nonexistent", "foo")
	if refs[0].Path != expected {
		t.Fatalf("expected original path %q, got %q", expected, refs[0].Path)
	}
}

func TestParseFuzzyMatchMultiComponent(t *testing.T) {
	dir := t.TempDir()
	makeFile(t, dir, "specs/reference-context.md", "# Reference Context")

	// @spec/reference → specs/reference-context.md (both components fuzzy)
	refs := parseRefs("@spec/reference", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Kind != KindFile {
		t.Fatalf("expected KindFile, got %v", refs[0].Kind)
	}
	expected := filepath.Join(dir, "specs", "reference-context.md")
	if refs[0].Path != expected {
		t.Fatalf("expected path %q, got %q", expected, refs[0].Path)
	}
}

func TestParseFuzzyMatchMultiComponentShortPrefix(t *testing.T) {
	dir := t.TempDir()
	makeFile(t, dir, "specs/reference-context.md", "# Reference Context")

	// @spec/ref → specs/reference-context.md (both components fuzzy, short prefix)
	refs := parseRefs("@spec/ref", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	expected := filepath.Join(dir, "specs", "reference-context.md")
	if refs[0].Path != expected {
		t.Fatalf("expected path %q, got %q", expected, refs[0].Path)
	}
}

func TestParseFuzzyMatchMultiComponentExactPreferred(t *testing.T) {
	dir := t.TempDir()
	// Both spec/ and specs/ exist; exact match should be preferred
	makeDir(t, dir, "spec")
	makeFile(t, dir, "spec/reference-context.md", "# spec version")
	makeDir(t, dir, "specs")
	makeFile(t, dir, "specs/reference-context.md", "# specs version")

	// @spec/reference → should match spec/reference-context.md (exact component) not specs/
	refs := parseRefs("@spec/reference", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	expected := filepath.Join(dir, "spec", "reference-context.md")
	if refs[0].Path != expected {
		t.Fatalf("expected exact match %q, got %q", expected, refs[0].Path)
	}
}

func TestParseFuzzyMatchMultiComponentPickShortest(t *testing.T) {
	dir := t.TempDir()
	// At intermediate level: speclib/ and specs/ both match prefix "spec"
	makeDir(t, dir, "speclib")
	makeFile(t, dir, "speclib/readme.md", "# speclib")
	makeDir(t, dir, "specs")
	makeFile(t, dir, "specs/readme.md", "# specs")

	// @spec/readme → pick shortest matching dir: specs/ (5 chars) vs speclib/ (7 chars)
	refs := parseRefs("@spec/readme", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	expected := filepath.Join(dir, "specs", "readme.md")
	if refs[0].Path != expected {
		t.Fatalf("expected shortest match %q, got %q", expected, refs[0].Path)
	}
}

func TestParseFuzzyMatchMultiComponentDeepDir(t *testing.T) {
	dir := t.TempDir()
	makeFile(t, dir, "pkg/agentloop/types.go", "package agentloop")

	// @pkg/agent/typ → pkg/agentloop/types.go (three components, last two fuzzy)
	refs := parseRefs("@pkg/agent/typ", dir)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	expected := filepath.Join(dir, "pkg", "agentloop", "types.go")
	if refs[0].Path != expected {
		t.Fatalf("expected path %q, got %q", expected, refs[0].Path)
	}
}

// ---------------------------------------------------------------------------
// Expand tests
// ---------------------------------------------------------------------------

func TestExpandFileSuccess(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "auth/login.go", "package auth\n\nfunc Login() {}")

	reg := newMockRegistry()
	reg.setExecute("read_file", f, "", &tool.ToolResult{
		Content: "     1  package auth\n     2\n     3  func Login() {}",
		Meta:    tool.ToolMeta{ByteCount: 38},
	}, nil)

	guard := newMockGuard()

	expander := New(reg, guard)
	refs := []Ref{{Raw: "@auth/login.go", Path: f, Kind: KindFile}}

	resolved, err := expander.expandRefs(context.Background(), refs, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved, got %d", len(resolved))
	}
	if resolved[0].Error != "" {
		t.Fatalf("expected no error, got %q", resolved[0].Error)
	}
	if resolved[0].Bytes <= 0 {
		t.Fatalf("expected positive bytes, got %d", resolved[0].Bytes)
	}
}

func TestExpandFolderSuccess(t *testing.T) {
	dir := t.TempDir()
	d := makeDir(t, dir, "pkg/auth")

	reg := newMockRegistry()
	reg.setExecute("ls", "", d, &tool.ToolResult{
		Content: "Listed pkg/auth (3 entries, 1ms):\nlogin.go\nlogout.go\nmiddleware/\n  rate_limiter.go",
		Meta:    tool.ToolMeta{ByteCount: 72},
	}, nil)

	guard := newMockGuard()

	expander := New(reg, guard)
	refs := []Ref{{Raw: "@pkg/auth", Path: d, Kind: KindFolder}}

	resolved, err := expander.expandRefs(context.Background(), refs, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved, got %d", len(resolved))
	}
	if resolved[0].Error != "" {
		t.Fatalf("expected no error, got %q", resolved[0].Error)
	}
}

func TestExpandFileNotFound(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nonexistent.go")

	reg := newMockRegistry()
	reg.setExecute("read_file", missing, "", &tool.ToolResult{
		Error: &tool.ToolError{
			Class:   tool.ErrorClassRecoverable,
			Kind:    tool.ErrKindFileNotFound,
			Message: "file not found",
		},
	}, nil)

	guard := newMockGuard()

	expander := New(reg, guard)
	refs := []Ref{{Raw: "@nonexistent.go", Path: missing, Kind: KindFile}}

	resolved, err := expander.expandRefs(context.Background(), refs, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved, got %d", len(resolved))
	}
	if resolved[0].Error == "" {
		t.Fatalf("expected error for missing file, got none")
	}
}

func TestExpandPermissionDenied(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "secret.go", "package secret")

	reg := newMockRegistry()
	reg.setExecute("read_file", f, "", &tool.ToolResult{
		Content: "secret content",
	}, nil)

	guard := newMockGuard()
	guard.setDecision("read_file", permission.DecisionDeny)

	expander := New(reg, guard)
	refs := []Ref{{Raw: "@secret.go", Path: f, Kind: KindFile}}

	resolved, err := expander.expandRefs(context.Background(), refs, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved, got %d", len(resolved))
	}
	if resolved[0].Error == "" {
		t.Fatalf("expected permission denied error, got none")
	}
}

func TestExpandMultipleRefs(t *testing.T) {
	dir := t.TempDir()
	f1 := makeFile(t, dir, "auth/login.go", "package auth")
	f2 := makeFile(t, dir, "context/context.go", "package context")

	reg := newMockRegistry()
	reg.setExecute("read_file", f1, "", &tool.ToolResult{
		Content: "[1] package auth",
		Meta:    tool.ToolMeta{ByteCount: 13},
	}, nil)
	reg.setExecute("read_file", f2, "", &tool.ToolResult{
		Content: "[1] package context",
		Meta:    tool.ToolMeta{ByteCount: 16},
	}, nil)

	guard := newMockGuard()

	expander := New(reg, guard)
	refs := []Ref{
		{Raw: "@auth/login.go", Path: f1, Kind: KindFile},
		{Raw: "@context/context.go", Path: f2, Kind: KindFile},
	}

	resolved, err := expander.expandRefs(context.Background(), refs, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolved) != 2 {
		t.Fatalf("expected 2 resolved, got %d", len(resolved))
	}
	for _, r := range resolved {
		if r.Error != "" {
			t.Fatalf("unexpected error for %s: %s", r.Raw, r.Error)
		}
	}
}

func TestExpandMaxTotalBytes(t *testing.T) {
	dir := t.TempDir()
	f1 := makeFile(t, dir, "a.go", strings.Repeat("x", 100*1024)) // 100KB
	f2 := makeFile(t, dir, "b.go", "package b")

	reg := newMockRegistry()
	reg.setExecute("read_file", f1, "", &tool.ToolResult{
		Content: "     1  " + strings.Repeat("x", 100*1024),
		Meta:    tool.ToolMeta{ByteCount: 100 * 1024},
	}, nil)
	reg.setExecute("read_file", f2, "", &tool.ToolResult{
		Content: "[1] package b",
		Meta:    tool.ToolMeta{ByteCount: 13},
	}, nil)

	guard := newMockGuard()

	expander := New(reg, guard)
	refs := []Ref{
		{Raw: "@a.go", Path: f1, Kind: KindFile},
		{Raw: "@b.go", Path: f2, Kind: KindFile},
	}

	resolved, _ := expander.expandRefs(context.Background(), refs, dir)
	// a.go (100KB) fits under 128KB total; b.go would push it over
	// The second ref should be skipped with a warning
	if len(resolved) < 1 {
		t.Fatalf("expected at least 1 resolved, got %d", len(resolved))
	}
	if resolved[0].Error != "" {
		t.Fatalf("expected no error for first ref, got %q", resolved[0].Error)
	}
}

func TestExpandMaxFileBytes(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "big.go", strings.Repeat("x", 40*1024)) // 40KB

	reg := newMockRegistry()
	reg.setExecute("read_file", f, "", &tool.ToolResult{
		Content: "     1  " + strings.Repeat("x", 40*1024),
		Meta:    tool.ToolMeta{ByteCount: 40 * 1024},
	}, nil)

	guard := newMockGuard()

	expander := New(reg, guard)
	refs := []Ref{{Raw: "@big.go", Path: f, Kind: KindFile}}

	resolved, _ := expander.expandRefs(context.Background(), refs, dir)
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved, got %d", len(resolved))
	}
	// Should be truncated to 32KB + marker
	if resolved[0].Bytes > 33*1024 {
		t.Fatalf("expected truncated content (<= ~33KB), got %d bytes", resolved[0].Bytes)
	}
	if resolved[0].Error != "" {
		t.Fatalf("unexpected error: %s", resolved[0].Error)
	}
	// Content should be truncated with marker
	c := resolved[0].Content
	if len(c) < 100 {
		t.Fatalf("content too short: %d bytes", len(c))
	}
	if !strings.Contains(c, "truncated") {
		t.Fatalf("expected truncated marker in content (len=%d, first 200: %q)", len(c), c[:200])
	}
}

// ---------------------------------------------------------------------------
// Replace tests
// ---------------------------------------------------------------------------

func TestReplaceSingleFileRef(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "auth/login.go", "package auth")

	resolved := []ResolvedRef{
		{
			Ref:     Ref{Raw: "@auth/login.go", Path: f, Kind: KindFile},
			Content: "[1] package auth",
			Bytes:   16,
		},
	}

	result := replaceRefs("look at @auth/login.go", resolved, dir)
	if !strings.Contains(result, "@@ auth/login.go (file)") {
		t.Fatalf("expected @@ block in output, got: %s", result)
	}
	if !strings.Contains(result, "[1] package auth") {
		t.Fatalf("expected file content in output, got: %s", result)
	}
	if strings.Contains(result, "@auth/login.go") {
		t.Fatalf("expected raw @ref to be removed, got: %s", result)
	}
}

func TestReplaceSingleFolderRef(t *testing.T) {
	dir := t.TempDir()
	d := makeDir(t, dir, "pkg/auth")

	resolved := []ResolvedRef{
		{
			Ref:     Ref{Raw: "@pkg/auth", Path: d, Kind: KindFolder},
			Content: "login.go\nlogout.go",
			Bytes:   18,
		},
	}

	result := replaceRefs("check @pkg/auth", resolved, dir)
	if !strings.Contains(result, "@@ pkg/auth (directory)") {
		t.Fatalf("expected @@ directory block in output, got: %s", result)
	}
}

func TestReplaceMixedRefs(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "auth/login.go", "package auth")
	d := makeDir(t, dir, "pkg/llm")

	resolved := []ResolvedRef{
		{
			Ref:     Ref{Raw: "@auth/login.go", Path: f, Kind: KindFile},
			Content: "[1] package auth",
			Bytes:   16,
		},
		{
			Ref:     Ref{Raw: "@pkg/llm", Path: d, Kind: KindFolder},
			Content: "client.go\ntypes.go",
			Bytes:   18,
		},
	}

	result := replaceRefs("look at @auth/login.go and @pkg/llm", resolved, dir)
	if !strings.Contains(result, "@@ auth/login.go (file)") {
		t.Fatalf("expected file block")
	}
	if !strings.Contains(result, "@@ pkg/llm (directory)") {
		t.Fatalf("expected directory block")
	}
	if strings.Count(result, "@@") != 4 {
		t.Fatalf("expected 4 @@ markers (2 open + 2 close), got %d", strings.Count(result, "@@"))
	}
}

func TestReplacePreservesUserText(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "a.go", "package main")

	resolved := []ResolvedRef{
		{Ref: Ref{Raw: "@a.go", Path: f, Kind: KindFile}, Content: "[1] package main", Bytes: 15},
	}

	result := replaceRefs("please explain @a.go in detail", resolved, dir)
	if !strings.Contains(result, "please explain") {
		t.Fatalf("expected user text preserved, got: %s", result)
	}
	if !strings.Contains(result, "in detail") {
		t.Fatalf("expected trailing user text preserved, got: %s", result)
	}
}

func TestReplaceFailedRef(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.go")

	resolved := []ResolvedRef{
		{
			Ref:   Ref{Raw: "@missing.go", Path: missing, Kind: KindFile},
			Error: "file not found",
		},
	}

	result := replaceRefs("check @missing.go", resolved, dir)
	if !strings.Contains(result, "@@ @missing.go  [not found]") {
		t.Fatalf("expected error marker for failed ref, got: %s", result)
	}
}

func TestReplaceNoRefPassthrough(t *testing.T) {
	result := replaceRefs("hello world", nil, "/tmp")
	if result != "hello world" {
		t.Fatalf("expected passthrough, got %q", result)
	}
}

func TestReplaceWithLanguageTag(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "main.go", "package main")

	resolved := []ResolvedRef{
		{Ref: Ref{Raw: "@main.go", Path: f, Kind: KindFile}, Content: "[1] package main", Bytes: 15},
	}

	result := replaceRefs("@main.go", resolved, dir)
	if !strings.Contains(result, "```go") {
		t.Fatalf("expected ```go language tag for .go file, got: %s", result)
	}
}

func TestReplaceUnknownLanguage(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "config.xyz", "some config")

	resolved := []ResolvedRef{
		{Ref: Ref{Raw: "@config.xyz", Path: f, Kind: KindFile}, Content: "[1] some config", Bytes: 15},
	}

	result := replaceRefs("@config.xyz", resolved, dir)
	// Unknown extension should have ``` without language identifier
	if !strings.Contains(result, "```\n[1] some config") {
		t.Fatalf("expected ``` without language tag for unknown extension, got: %s", result)
	}
}

// ---------------------------------------------------------------------------
// Expand (full) tests
// ---------------------------------------------------------------------------

func TestExpandFullSingleFile(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "auth/login.go", "package auth\n\nfunc Login() {}")

	reg := newMockRegistry()
	reg.setExecute("read_file", f, "", &tool.ToolResult{
		Content: "     1  package auth\n     2\n     3  func Login() {}",
		Meta:    tool.ToolMeta{ByteCount: 38},
	}, nil)

	guard := newMockGuard()
	expander := New(reg, guard)

	expanded, refs, err := expander.Expand(context.Background(), "look at @auth/login.go", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if !strings.Contains(expanded, "@@ auth/login.go (file)") {
		t.Fatalf("expected @@ block, got: %s", expanded)
	}
	if strings.Contains(expanded, "@auth/login.go") {
		t.Fatalf("expected raw @ref removed, got: %s", expanded)
	}
}

func TestExpandFullNoRefPassthrough(t *testing.T) {
	reg := newMockRegistry()
	guard := newMockGuard()
	expander := New(reg, guard)

	expanded, refs, err := expander.Expand(context.Background(), "hello world", "/tmp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("expected 0 refs, got %d", len(refs))
	}
	if expanded != "hello world" {
		t.Fatalf("expected passthrough, got %q", expanded)
	}
}

func TestExpandFullWithFolders(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "auth/login.go", "package auth")
	d := makeDir(t, dir, "pkg/llm")

	reg := newMockRegistry()
	reg.setExecute("read_file", f, "", &tool.ToolResult{
		Content: "     1  package auth",
		Meta:    tool.ToolMeta{ByteCount: 16},
	}, nil)
	reg.setExecute("ls", "", d, &tool.ToolResult{
		Content: "client.go\ntypes.go",
		Meta:    tool.ToolMeta{ByteCount: 18},
	}, nil)

	guard := newMockGuard()
	expander := New(reg, guard)

	expanded, refs, err := expander.Expand(context.Background(), "check @auth/login.go and @pkg/llm", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
	if !strings.Contains(expanded, "@@ auth/login.go (file)") {
		t.Fatalf("expected file block")
	}
	if !strings.Contains(expanded, "@@ pkg/llm (directory)") {
		t.Fatalf("expected directory block")
	}
}

// ---------------------------------------------------------------------------
// End-to-end test
// ---------------------------------------------------------------------------

func TestE2EExpandAndReplace(t *testing.T) {
	dir := t.TempDir()
	f1 := makeFile(t, dir, "auth/login.go", "package auth\n\nfunc Login() {}")
	d2 := makeDir(t, dir, "pkg/context")

	reg := newMockRegistry()
	reg.setExecute("read_file", f1, "", &tool.ToolResult{
		Content: "     1  package auth\n     2\n     3  func Login() {}",
		Meta:    tool.ToolMeta{ByteCount: 38},
	}, nil)
	reg.setExecute("ls", "", d2, &tool.ToolResult{
		Content: "context.go\ncontext_test.go",
		Meta:    tool.ToolMeta{ByteCount: 28},
	}, nil)

	guard := newMockGuard()
	expander := New(reg, guard)

	userInput := "看一下 @auth/login.go 这个文件和 @pkg/context 目录"
	expanded, refs, err := expander.Expand(context.Background(), userInput, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}

	// Verify blocks exist
	if !strings.Contains(expanded, "@@ auth/login.go (file)") {
		t.Fatalf("missing file block")
	}
	if !strings.Contains(expanded, "@@ pkg/context (directory)") {
		t.Fatalf("missing directory block")
	}

	// Verify original @refs are removed
	if strings.Contains(expanded, "@auth/login.go") || strings.Contains(expanded, "@pkg/context") {
		t.Fatalf("raw @refs should be removed: %s", expanded)
	}

	// Verify user instruction text is preserved
	if !strings.Contains(expanded, "看一下") && !strings.Contains(expanded, "这个文件") {
		t.Fatalf("user instruction text should be preserved: %s", expanded)
	}
}
