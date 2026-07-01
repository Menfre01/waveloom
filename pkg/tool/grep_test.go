package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepSuccess(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n\nfunc hello() {}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b.go"), []byte("package main\n\nfunc world() {}\n"), 0o644)

	tool := &Grep{}
	result, err := tool.Execute(context.Background(), GrepParams{
		Pattern:    "func ",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, "hello") {
		t.Error("Content should contain hello")
	}
	if !contains(result.Content, "world") {
		t.Error("Content should contain world")
	}
}

func TestGrepNoResults(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n"), 0o644)

	tool := &Grep{}
	result, err := tool.Execute(context.Background(), GrepParams{
		Pattern:    "nonexistent_pattern_xyz",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// 空结果不是 error
	if result.Error != nil {
		t.Fatalf("Empty results should NOT be ToolError, got: %v", result.Error)
	}
	if !contains(result.Content, "No matches found") {
		t.Errorf("Content should indicate no results: %s", result.Content)
	}
}

func TestGrepInvalidRegex(t *testing.T) {
	tool := &Grep{}
	result, err := tool.Execute(context.Background(), GrepParams{
		Pattern: "[invalid regex",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for invalid regex")
	}
	if result.Error.Kind != ErrKindInvalidArgs {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindInvalidArgs)
	}
}

func TestGrepWithInclude(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\nconst x = 1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b.txt"), []byte("const y = 2\n"), 0o644)

	tool := &Grep{}
	result, err := tool.Execute(context.Background(), GrepParams{
		Pattern:    "const",
		Include:    "*.go",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, "a.go") {
		t.Error("Content should contain a.go")
	}
	if contains(result.Content, "b.txt") {
		t.Error("Content should not contain b.txt (filtered by include)")
	}
}

func TestGrepCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\nFUNC Hello() {}\n"), 0o644)

	tool := &Grep{}
	result, err := tool.Execute(context.Background(), GrepParams{
		Pattern:        "func",
		WorkingDir:     dir,
		CaseInsensitive: true,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, "Hello") {
		t.Error("Content should contain Hello (case insensitive match)")
	}
}

func TestGrepWithContextLines(t *testing.T) {
	dir := t.TempDir()
	content := "line1\nline2\nMATCH line3\nline4\nline5\n"
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte(content), 0o644)

	tool := &Grep{}
	result, err := tool.Execute(context.Background(), GrepParams{
		Pattern:      "MATCH",
		WorkingDir:   dir,
		ContextLines: 1,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	// 应该包含上下文行
	if !contains(result.Content, "line2") {
		t.Error("Content should contain context line2")
	}
	if !contains(result.Content, "line4") {
		t.Error("Content should contain context line4")
	}
}

func TestGrepSkipsBinary(t *testing.T) {
	dir := t.TempDir()
	// 写入二进制文件
	binaryContent := make([]byte, 512)
	for i := range binaryContent {
		if i%3 == 0 {
			binaryContent[i] = 0
		} else {
			binaryContent[i] = 'A'
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "binary.bin"), binaryContent, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "text.go"), []byte("package main\nconst found = true\n"), 0o644)

	tool := &Grep{}
	result, err := tool.Execute(context.Background(), GrepParams{
		Pattern:    "found",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, "text.go") {
		t.Error("Content should contain text.go")
	}
	if contains(result.Content, "binary.bin") {
		t.Error("Content should not contain binary.bin (binary file should be skipped)")
	}
}

func TestGrepPcreHint(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    string
	}{
		{"lookahead", `foo(?=bar)`, "lookahead/lookbehind"},
		{"negative lookahead", `foo(?!bar)`, "lookahead/lookbehind"},
		{"lookbehind", `(?<=foo)bar`, "lookahead/lookbehind"},
		{"negative lookbehind", `(?<!foo)bar`, "lookahead/lookbehind"},
		{"backreference", `(foo)\1`, "backreferences"},
		{"keep out", `foo\Kbar`, "\\K is a PCRE-only"},
		{"recursive pattern", `(?R)`, "recursive/subroutine"},
		{"subroutine pattern", `(?&name)`, "recursive/subroutine"},
		{"named group", `(?P<name>foo)`, "recursive/subroutine"},
		{"atomic group", `(?>foo)`, "atomic groups"},
		{"possessive plus", `foo++`, "possessive quantifiers"},
		{"possessive star", `foo*+`, "possessive quantifiers"},
		{"possessive question", `foo?+`, "possessive quantifiers"},
		{"valid RE2", `foo.*bar`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := pcreHint(tt.pattern)
			if tt.want == "" {
				if hint != "" {
					t.Errorf("pcreHint(%q) = %q, want empty", tt.pattern, hint)
				}
			} else {
				if !strings.Contains(hint, tt.want) {
					t.Errorf("pcreHint(%q) = %q, want containing %q", tt.pattern, hint, tt.want)
				}
			}
		})
	}
}

func TestGrepInvalidRegexReturnsHint(t *testing.T) {
	tool := &Grep{}
	result, err := tool.Execute(context.Background(), GrepParams{
		Pattern: `(?<=foo)bar`,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for PCRE lookbehind")
	}
	if !strings.Contains(result.Error.Message, "Hint:") {
		t.Errorf("PCRE error should include hint: %s", result.Error.Message)
	}
}
