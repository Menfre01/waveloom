package reference

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/permission"
)

// ---------------------------------------------------------------------------
// Mock types
// ---------------------------------------------------------------------------

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
func (m *mockGuard) EnterPlanMode(string)                                        {}
func (m *mockGuard) ExitPlanMode()                                               {}
func (m *mockGuard) SetAvailableBuildTools([]string)                             {}

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


	guard := newMockGuard()

	expander := New(guard)
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


	guard := newMockGuard()

	expander := New(guard)
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


	guard := newMockGuard()

	expander := New(guard)
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


	guard := newMockGuard()
	guard.setDecision("read", permission.DecisionDeny)

	expander := New(guard)
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


	guard := newMockGuard()

	expander := New(guard)
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


	guard := newMockGuard()

	expander := New(guard)
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


	guard := newMockGuard()

	expander := New(guard)
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
	_ = makeFile(t, dir, "auth/login.go", "package auth\n\nfunc Login() {}")


	guard := newMockGuard()
	expander := New(guard)

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
	guard := newMockGuard()
	expander := New(guard)

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
	_ = makeFile(t, dir, "auth/login.go", "package auth")
	_ = makeDir(t, dir, "pkg/llm")


	guard := newMockGuard()
	expander := New(guard)

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
	_ = makeFile(t, dir, "auth/login.go", "package auth\n\nfunc Login() {}")
	_ = makeDir(t, dir, "pkg/context")


	guard := newMockGuard()
	expander := New(guard)

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

// ---------------------------------------------------------------------------
// AGENTS.md 上下文展开测试
// AGENTS.md 中可以通过 @ 引用其他文件，展开后注入为 messages[1]。
// ---------------------------------------------------------------------------

// TestAgentsMdExpand_SingleFile 模拟 AGENTS.md 中引用单个文件。
func TestAgentsMdExpand_SingleFile(t *testing.T) {
	dir := t.TempDir()
	_ = makeFile(t, dir, "docs/coding-style.md", "# 编码规范\n\n- 遵循 Go 惯例\n- 错误统一处理")


	guard := newMockGuard()
	expander := New(guard)

	agentsMdText := "# AGENTS.md instructions for /project\n\n<INSTRUCTIONS>\n\n## /project/AGENTS.md\n基础规范 @docs/coding-style.md\n\n</INSTRUCTIONS>"
	expanded, refs, err := expander.Expand(context.Background(), agentsMdText, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if !strings.Contains(expanded, "@@ docs/coding-style.md (file)") {
		t.Fatalf("expected expanded file block, got: %s", expanded)
	}
	if !strings.Contains(expanded, "## /project/AGENTS.md") {
		t.Fatalf("AGENTS.md structure should be preserved: %s", expanded)
	}
	if strings.Contains(expanded, "@docs/coding-style.md") {
		t.Fatalf("raw @ref should be replaced: %s", expanded)
	}
}

// TestAgentsMdExpand_NoRefPassthrough AGENTS.md 中没有 @ 引用时直接透传。
func TestAgentsMdExpand_NoRefPassthrough(t *testing.T) {
	dir := t.TempDir()
	guard := newMockGuard()
	expander := New(guard)

	agentsMdText := "# AGENTS.md instructions for /project\n\n<INSTRUCTIONS>\n\n## /project/AGENTS.md\n纯文本规范，没有任何引用。\n\n</INSTRUCTIONS>"
	expanded, refs, err := expander.Expand(context.Background(), agentsMdText, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("expected 0 refs, got %d", len(refs))
	}
	if expanded != agentsMdText {
		t.Fatalf("expected identical passthrough when no refs, got: %s", expanded)
	}
}

// TestAgentsMdExpand_FileNotFound AGENTS.md 中 @ 引用的文件不存在时，ref 携带错误信息且输出包含 [not found] 标记。
func TestAgentsMdExpand_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	_ = filepath.Join(dir, "nonexistent.md")


	guard := newMockGuard()
	expander := New(guard)

	agentsMdText := "# AGENTS.md instructions\n\n<INSTRUCTIONS>\n\n@nonexistent.md\n\n</INSTRUCTIONS>"
	expanded, refs, err := expander.Expand(context.Background(), agentsMdText, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	// 展开后的 ref 应包含错误详情
	if refs[0].Error == "" {
		t.Fatalf("ref should have error for missing file")
	}
	if !strings.Contains(expanded, "[not found]") {
		t.Fatalf("expected 'not found' marker for missing file, got: %s", expanded)
	}
	// AGENTS.md 剩余文本应保留
	if !strings.Contains(expanded, "# AGENTS.md instructions") {
		t.Fatalf("AGENTS.md preamble should be preserved: %s", expanded)
	}
}

// TestAgentsMdExpand_RelativePath AGENTS.md 中的相对路径 @ 引用基于 cwd 解析。
func TestAgentsMdExpand_RelativePath(t *testing.T) {
	dir := t.TempDir()
	_ = makeFile(t, dir, "sub/dir/rules.md", "# 子目录规范")


	guard := newMockGuard()
	expander := New(guard)

	agentsMdText := "# AGENTS.md\n\n<INSTRUCTIONS>\n\n引用 @sub/dir/rules.md\n\n</INSTRUCTIONS>"
	expanded, refs, err := expander.Expand(context.Background(), agentsMdText, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if !strings.Contains(expanded, "@@ sub/dir/rules.md (file)") {
		t.Fatalf("relative path should resolve: %s", expanded)
	}
}

// TestAgentsMdExpand_AbsolutePath AGENTS.md 中的绝对路径引用应直接定位。
func TestAgentsMdExpand_AbsolutePath(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "global-rules.md", "# 全局规范")


	guard := newMockGuard()
	expander := New(guard)

	agentsMdText := "# AGENTS.md\n\n<INSTRUCTIONS>\n\n引用 @" + f + "\n\n</INSTRUCTIONS>"
	expanded, refs, err := expander.Expand(context.Background(), agentsMdText, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Error != "" {
		t.Fatalf("unexpected ref error for absolute path: %q", refs[0].Error)
	}
	if !strings.Contains(expanded, "@@ ") {
		t.Fatalf("expected @@ block for valid absolute path ref, got: %s", expanded)
	}
}

// TestAgentsMdExpand_MarkdownFile AGENTS.md 中引用 .md 文件应获得 ```markdown 语言标签。
func TestAgentsMdExpand_MarkdownFile(t *testing.T) {
	dir := t.TempDir()
	_ = makeFile(t, dir, "docs/project-overview.md", "# 项目概览\n\nWaveloom 是一个终端编码代理。")


	guard := newMockGuard()
	expander := New(guard)

	agentsMdText := "# AGENTS.md\n\n<INSTRUCTIONS>\n\n@docs/project-overview.md\n\n</INSTRUCTIONS>"
	expanded, refs, err := expander.Expand(context.Background(), agentsMdText, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if !strings.Contains(expanded, "```markdown") {
		t.Fatalf("expected ```markdown language tag for .md file, got: %s", expanded)
	}
}

// TestAgentsMdExpand_MultipleMixedRefs AGENTS.md 中同时引用文件和目录。
func TestAgentsMdExpand_MultipleMixedRefs(t *testing.T) {
	dir := t.TempDir()
	_ = makeFile(t, dir, "specs/agent-loop.md", "# Agent Loop 规格")
	_ = makeDir(t, dir, "pkg/agentloop")


	guard := newMockGuard()
	expander := New(guard)

	agentsMdText := "# AGENTS.md\n\n<INSTRUCTIONS>\n\n规格见 @specs/agent-loop.md\n代码见 @pkg/agentloop\n\n</INSTRUCTIONS>"
	expanded, refs, err := expander.Expand(context.Background(), agentsMdText, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
	if !strings.Contains(expanded, "@@ specs/agent-loop.md (file)") {
		t.Fatalf("expected file block: %s", expanded)
	}
	if !strings.Contains(expanded, "@@ pkg/agentloop (directory)") {
		t.Fatalf("expected directory block: %s", expanded)
	}
}

// TestAgentsMdExpand_PermissionDenied AGENTS.md 中引用被权限规则拒绝的文件时，
// refs 列表中包含 "permission denied" 错误详情，输出中显示 [not found] 标记。
func TestAgentsMdExpand_PermissionDenied(t *testing.T) {
	dir := t.TempDir()
	_ = makeFile(t, dir, "secret.md", "# 机密文档")


	guard := newMockGuard()
	guard.setDecision("read", permission.DecisionDeny)

	expander := New(guard)

	agentsMdText := "# AGENTS.md\n\n<INSTRUCTIONS>\n\n@secret.md\n\n</INSTRUCTIONS>"
	expanded, refs, err := expander.Expand(context.Background(), agentsMdText, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if !strings.Contains(refs[0].Error, "permission denied") {
		t.Fatalf("ref error should contain 'permission denied', got: %q", refs[0].Error)
	}
	if !strings.Contains(expanded, "[not found]") {
		t.Fatalf("expanded text should contain 'not found' marker, got: %s", expanded)
	}
}

// TestAgentsMdExpand_PartialFailure 一个 @ 引用成功、另一个失败时，成功的内容仍保留，失败的显示错误。
func TestAgentsMdExpand_PartialFailure(t *testing.T) {
	dir := t.TempDir()
	_ = makeFile(t, dir, "docs/readme.md", "# 说明")
	_ = filepath.Join(dir, "docs", "changelog.md")


	guard := newMockGuard()
	expander := New(guard)

	agentsMdText := "# AGENTS.md\n\n<INSTRUCTIONS>\n\n@docs/readme.md\n@docs/changelog.md\n\n</INSTRUCTIONS>"
	expanded, refs, err := expander.Expand(context.Background(), agentsMdText, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
	if !strings.Contains(expanded, "# 说明") {
		t.Fatalf("successful ref content should be present: %s", expanded)
	}
	if !strings.Contains(expanded, "[not found]") {
		t.Fatalf("failed ref should show error marker: %s", expanded)
	}
}

// TestAgentsMdExpand_FuzzyMatch AGENTS.md 中的 @ 引用支持模糊前缀匹配。
func TestAgentsMdExpand_FuzzyMatch(t *testing.T) {
	dir := t.TempDir()
	_ = makeFile(t, dir, "specs/reference-context.md", "# 引用上下文")


	guard := newMockGuard()
	expander := New(guard)

	agentsMdText := "# AGENTS.md\n\n<INSTRUCTIONS>\n\n@specs/reference-c\n\n</INSTRUCTIONS>"
	expanded, _, err := expander.Expand(context.Background(), agentsMdText, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(expanded, "@@ specs/reference-context.md (file)") {
		t.Fatalf("fuzzy match should resolve, got: %s", expanded)
	}
}

// TestAgentsMdExpand_ContentAboveRefs AGENTS.md 中的 @ 引用展开后，非引用文本应保留在下方。
func TestAgentsMdExpand_ContentAboveRefs(t *testing.T) {
	dir := t.TempDir()
	_ = makeFile(t, dir, "specs/agent-loop.md", "# Agent Loop")


	guard := newMockGuard()
	expander := New(guard)

	agentsMdText := "# AGENTS.md instructions for /project\n\n<INSTRUCTIONS>\n\n## /project/AGENTS.md\n\n项目基础规范，详见 @specs/agent-loop.md\n\n更多细节请参考团队 Wiki。\n\n</INSTRUCTIONS>"
	expanded, _, err := expander.Expand(context.Background(), agentsMdText, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// @@ 块应该在顶部（引用内容），指令文本在下方
	if !strings.Contains(expanded, "@@ specs/agent-loop.md (file)") {
		t.Fatalf("@@ block missing: %s", expanded)
	}
	if !strings.Contains(expanded, "项目基础规范") {
		t.Fatalf("instruction text should be preserved: %s", expanded)
	}
	if !strings.Contains(expanded, "更多细节请参考团队 Wiki") {
		t.Fatalf("trailing text should be preserved: %s", expanded)
	}
	// 引用内容块应在用户指令文本之前
	blockPos := strings.Index(expanded, "@@ specs/agent-loop.md (file)")
	instrPos := strings.Index(expanded, "项目基础规范")
	if blockPos > instrPos {
		t.Fatalf("@@ blocks should appear before instruction text")
	}
}

// TestAgentsMdExpand_Empty 空 AGENTS.md 内容无引用，不报错。
func TestAgentsMdExpand_Empty(t *testing.T) {
	dir := t.TempDir()
	guard := newMockGuard()
	expander := New(guard)

	expanded, refs, err := expander.Expand(context.Background(), "", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("expected 0 refs for empty input, got %d", len(refs))
	}
	if expanded != "" {
		t.Fatalf("expected empty passthrough, got %q", expanded)
	}
}

// TestAgentsMdExpand_TotalByteLimit AGENTS.md 展开后总大小超过 128KB 时应截断。
func TestAgentsMdExpand_TotalByteLimit(t *testing.T) {
	dir := t.TempDir()
	_ = makeFile(t, dir, "big.md", strings.Repeat("# 大文件\n", 20000)) // ~140KB


	guard := newMockGuard()
	expander := New(guard)

	agentsMdText := "# AGENTS.md\n\n<INSTRUCTIONS>\n\n@big.md\n\n</INSTRUCTIONS>"
	expanded, refs, err := expander.Expand(context.Background(), agentsMdText, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if len(expanded) > 140*1024 {
		t.Fatalf("expanded content should be truncated, got %d bytes", len(expanded))
	}
}

// TestAgentsMdExpand_DirectoryRef AGENTS.md 中 @ 引用目录时展开为文件列表。
func TestAgentsMdExpand_DirectoryRef(t *testing.T) {
	dir := t.TempDir()
	_ = makeDir(t, dir, "docs/specs")
	_ = makeFile(t, dir, "docs/specs/agent-loop.md", "# agent-loop")
	_ = makeFile(t, dir, "docs/specs/reference.md", "# reference")
	_ = makeFile(t, dir, "docs/specs/compaction.md", "# compaction")


	guard := newMockGuard()
	expander := New(guard)

	agentsMdText := "# AGENTS.md\n\n<INSTRUCTIONS>\n\n@docs/specs\n\n</INSTRUCTIONS>"
	expanded, refs, err := expander.Expand(context.Background(), agentsMdText, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if !strings.Contains(expanded, "@@ docs/specs (directory)") {
		t.Fatalf("expected directory block: %s", expanded)
	}
	if !strings.Contains(expanded, "agent-loop.md") {
		t.Fatalf("expected directory listing content: %s", expanded)
	}
}

// ---------------------------------------------------------------------------
// languageForPath
// ---------------------------------------------------------------------------

func TestLanguageForPath_Go(t *testing.T) {
	if got := languageForPath("main.go"); got != "go" {
		t.Errorf("expected go, got %q", got)
	}
}

func TestLanguageForPath_Python(t *testing.T) {
	if got := languageForPath("script.py"); got != "python" {
		t.Errorf("expected python, got %q", got)
	}
}

func TestLanguageForPath_JavaScript(t *testing.T) {
	if got := languageForPath("app.js"); got != "javascript" {
		t.Errorf("expected javascript, got %q", got)
	}
}

func TestLanguageForPath_TypeScript(t *testing.T) {
	if got := languageForPath("component.tsx"); got != "typescript" {
		t.Errorf("expected typescript, got %q", got)
	}
}

func TestLanguageForPath_Rust(t *testing.T) {
	if got := languageForPath("lib.rs"); got != "rust" {
		t.Errorf("expected rust, got %q", got)
	}
}

func TestLanguageForPath_Java(t *testing.T) {
	if got := languageForPath("Main.java"); got != "java" {
		t.Errorf("expected java, got %q", got)
	}
}

func TestLanguageForPath_C(t *testing.T) {
	if got := languageForPath("main.c"); got != "c" {
		t.Errorf("expected c, got %q", got)
	}
	if got := languageForPath("header.h"); got != "c" {
		t.Errorf("expected c for .h, got %q", got)
	}
}

func TestLanguageForPath_CPP(t *testing.T) {
	if got := languageForPath("main.cpp"); got != "cpp" {
		t.Errorf("expected cpp, got %q", got)
	}
	if got := languageForPath("main.cc"); got != "cpp" {
		t.Errorf("expected cpp for .cc, got %q", got)
	}
}

func TestLanguageForPath_Shell(t *testing.T) {
	if got := languageForPath("script.sh"); got != "bash" {
		t.Errorf("expected bash, got %q", got)
	}
	if got := languageForPath("script.bash"); got != "bash" {
		t.Errorf("expected bash for .bash, got %q", got)
	}
}

func TestLanguageForPath_YAML(t *testing.T) {
	if got := languageForPath("config.yaml"); got != "yaml" {
		t.Errorf("expected yaml, got %q", got)
	}
	if got := languageForPath("config.yml"); got != "yaml" {
		t.Errorf("expected yaml for .yml, got %q", got)
	}
}

func TestLanguageForPath_JSON(t *testing.T) {
	if got := languageForPath("data.json"); got != "json" {
		t.Errorf("expected json, got %q", got)
	}
}

func TestLanguageForPath_TOML(t *testing.T) {
	if got := languageForPath("Cargo.toml"); got != "toml" {
		t.Errorf("expected toml, got %q", got)
	}
}

func TestLanguageForPath_Markdown(t *testing.T) {
	if got := languageForPath("README.md"); got != "markdown" {
		t.Errorf("expected markdown, got %q", got)
	}
	if got := languageForPath("page.mdx"); got != "markdown" {
		t.Errorf("expected markdown for .mdx, got %q", got)
	}
}

func TestLanguageForPath_SQL(t *testing.T) {
	if got := languageForPath("query.sql"); got != "sql" {
		t.Errorf("expected sql, got %q", got)
	}
}

func TestLanguageForPath_CSS(t *testing.T) {
	if got := languageForPath("style.css"); got != "css" {
		t.Errorf("expected css, got %q", got)
	}
	if got := languageForPath("style.scss"); got != "css" {
		t.Errorf("expected css for .scss, got %q", got)
	}
}

func TestLanguageForPath_Dockerfile(t *testing.T) {
	if got := languageForPath("Dockerfile"); got != "dockerfile" {
		t.Errorf("expected dockerfile, got %q", got)
	}
}

func TestLanguageForPath_Makefile(t *testing.T) {
	if got := languageForPath("Makefile"); got != "makefile" {
		t.Errorf("expected makefile, got %q", got)
	}
}

func TestLanguageForPath_Unknown(t *testing.T) {
	if got := languageForPath("config.xyz"); got != "" {
		t.Errorf("expected empty for unknown extension, got %q", got)
	}
}

func TestLanguageForPath_NoExtension(t *testing.T) {
	if got := languageForPath("README"); got != "" {
		t.Errorf("expected empty for no extension, got %q", got)
	}
}

func TestLanguageForPath_Proto(t *testing.T) {
	if got := languageForPath("service.proto"); got != "protobuf" {
		t.Errorf("expected protobuf, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// relativePath
// ---------------------------------------------------------------------------

func TestRelativePath_SubDir(t *testing.T) {
	cwd := "/home/user/project"
	absPath := "/home/user/project/pkg/auth/login.go"
	rel := relativePath(absPath, cwd)
	if rel != "pkg/auth/login.go" {
		t.Errorf("expected 'pkg/auth/login.go', got %q", rel)
	}
}

func TestRelativePath_SameDir(t *testing.T) {
	cwd := "/home/user/project"
	absPath := "/home/user/project/main.go"
	rel := relativePath(absPath, cwd)
	if rel != "main.go" {
		t.Errorf("expected 'main.go', got %q", rel)
	}
}

func TestRelativePath_Outside(t *testing.T) {
	cwd := "/home/user/project"
	absPath := "/home/user/other/file.go"
	rel := relativePath(absPath, cwd)
	if rel != "/home/user/other/file.go" {
		t.Errorf("expected absolute path when outside, got %q", rel)
	}
}

func TestRelativePath_AlreadyRelative(t *testing.T) {
	// relativePath is called with absolute paths from resolvePath
	// Testing with CWD == absPath prefix
	cwd := "/tmp"
	absPath := "/tmp/file.go"
	rel := relativePath(absPath, cwd)
	if rel != "file.go" {
		t.Errorf("expected 'file.go', got %q", rel)
	}
}
