package hashline

import (
	"strings"
	"testing"
)

// ============================================================================
// Progressive matching tests
// ============================================================================

func TestMatchContentExact(t *testing.T) {
	ok, level := matchContent("hello", "hello")
	if !ok || level != "exact" {
		t.Errorf("expected (true, exact), got (%v, %s)", ok, level)
	}
}

func TestMatchContentNFC(t *testing.T) {
	// "é" as NFC (U+00E9) vs NFD (U+0065 + U+0301)
	nfc := "r\xc3\xa9sum\xc3\xa9"
	nfd := "re\xcc\x81sume\xcc\x81"
	ok, level := matchContent(nfc, nfd)
	if !ok {
		t.Errorf("NFC fallback failed: nfc=%q nfd=%q", nfc, nfd)
	}
	if level != "nfc" {
		t.Errorf("expected 'nfc', got %q", level)
	}
}

func TestMatchContentRstrip(t *testing.T) {
	// Trailing spaces on lines should match
	fileContent := "line1  \nline2\t\n"
	oldContent := "line1\nline2\n"
	ok, level := matchContent(fileContent, oldContent)
	if !ok {
		t.Errorf("rstrip fallback failed")
	}
	if level != "rstrip" {
		t.Errorf("expected 'rstrip', got %q", level)
	}
}

func TestMatchContentTrim(t *testing.T) {
	// Leading + trailing whitespace should match after trim
	fileContent := "  line1  \n\tline2\t\n"
	oldContent := "line1\nline2\n"
	ok, level := matchContent(fileContent, oldContent)
	if !ok {
		t.Errorf("trim fallback failed")
	}
	if level != "trim" {
		t.Errorf("expected 'trim', got %q", level)
	}
}

func TestMatchContentNFKC(t *testing.T) {
	// Full-width ASCII → normal ASCII (NFKC)
	fullwidth := "\xef\xbc\xa8\xef\xbd\x85\xef\xbd\x8c\xef\xbd\x8c\xef\xbd\x8f" // "Ｈｅｌｌｏ"
	normal := "Hello"
	ok, level := matchContent(fullwidth, normal)
	if !ok {
		t.Errorf("NFKC fallback failed: fullwidth=%q normal=%q", fullwidth, normal)
	}
	if level != "nfkc" {
		t.Errorf("expected 'nfkc', got %q", level)
	}
}

func TestMatchContentNoMatch(t *testing.T) {
	ok, _ := matchContent("hello", "world")
	if ok {
		t.Error("expected no match for completely different strings")
	}
}

// ============================================================================
// CRLF normalization tests
// ============================================================================

func TestCRLFNormalizationInResolveContentOps(t *testing.T) {
	content := "line1\nline2\n"
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	// OldString with CRLF line endings
	op := Op{
		Kind:      OpSWAP,
		LineStart: 1,
		LineEnd:   2,
		OldString: "line1\r\nline2",
		Body:      []string{"replaced1", "replaced2"},
	}
	ops := []Op{op}

	err := resolveContentOps(lines, &ops)
	if err != nil {
		t.Fatalf("CRLF normalization failed: %v", err)
	}
	if ops[0].Body[0] != "replaced1" {
		t.Errorf("unexpected result: %+v", ops[0])
	}
}

func TestCRLFNormalizationMixedEndings(t *testing.T) {
	content := "a\nb\nc\n"
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	// Mixed CRLF and LF in OldString
	op := Op{
		Kind:      OpSWAP,
		LineStart: 1,
		LineEnd:   3,
		OldString: "a\r\nb\nc",
		Body:      []string{"X", "Y", "Z"},
	}
	ops := []Op{op}

	err := resolveContentOps(lines, &ops)
	if err != nil {
		t.Fatalf("mixed CRLF/LF normalization failed: %v", err)
	}
	if ops[0].Body[0] != "X" {
		t.Errorf("unexpected result: %+v", ops[0])
	}
}

// ============================================================================
// Helper function unit tests
// ============================================================================

func TestRstripLines(t *testing.T) {
	result := rstripLines("hello  \nworld\t\n")
	expected := "hello\nworld\n"
	if result != expected {
		t.Errorf("rstripLines: got %q, want %q", result, expected)
	}
}

func TestTrimLines(t *testing.T) {
	result := trimLines("  hello  \n\tworld\t\n")
	expected := "hello\nworld\n"
	if result != expected {
		t.Errorf("trimLines: got %q, want %q", result, expected)
	}
}

// ============================================================================
// Integration: progressive matching through resolveContentOps
// ============================================================================

func TestResolveContentOpsProgressiveMatching(t *testing.T) {
	// File has trailing spaces, OldString doesn't — should match via rstrip
	content := "line1  \nline2\n"
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	op := Op{
		Kind:      OpSWAP,
		LineStart: 1,
		LineEnd:   1,
		OldString: "line1", // no trailing spaces
		Body:      []string{"replaced"},
	}
	ops := []Op{op}

	err := resolveContentOps(lines, &ops)
	if err != nil {
		t.Fatalf("progressive matching (rstrip) failed through resolveContentOps: %v", err)
	}
}

func TestResolveContentOpsTrimFallback(t *testing.T) {
	content := "  line1  \n\tline2\t\n"
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	op := Op{
		Kind:      OpSWAP,
		LineStart: 1,
		LineEnd:   2,
		OldString: "line1\nline2",
		Body:      []string{"a", "b"},
	}
	ops := []Op{op}

	err := resolveContentOps(lines, &ops)
	if err != nil {
		t.Fatalf("progressive matching (trim) failed: %v", err)
	}
}
