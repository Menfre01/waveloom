package hashline

import (
	"strings"
	"testing"
)

// TestRegression_SwapLastLineWithEmptyBody_TrailingNewline verifies that
// SWAP-ing the last line of a file with an empty body properly preserves
// the line count when the file has a trailing newline.
//
// Scenario (from E5 of end-to-end hashline test):
//   File: "line1\n\ninserted content\n" (3 lines)
//   SWAP line 3 with body [""]
//   Expected: "line1\n\n\n" (3 lines, delta=0)
//   Bug: result was "line1\n\n" (2 lines, delta=-1)
func TestRegression_SwapLastLineWithEmptyBody_TrailingNewline(t *testing.T) {
	// File with trailing newline: 3 lines
	content := "line1\n\ninserted content\n"
	lines := strings.Split(content, "\n")
	hasTrailing := strings.HasSuffix(content, "\n")

	t.Logf("Input: %q", content)
	t.Logf("Split: %q (len=%d)", lines, len(lines))
	t.Logf("HasTrailingNewline: %v", hasTrailing)
	t.Logf("countLines(input): %d", countLines(content))

	// SWAP line 3 (1-based) → empty string
	op := Op{Kind: OpSWAP, LineStart: 3, LineEnd: 3, Body: []string{""}}

	result, _, err := applyEdits(content, []Op{op})
	if err != nil {
		t.Fatalf("applyEdits failed: %v", err)
	}

	t.Logf("Result: %q", result)
	t.Logf("countLines(result): %d", countLines(result))

	oldLines := countLines(content)
	newLines := countLines(result)
	delta := newLines - oldLines

	t.Logf("LinesDelta: %d (old=%d, new=%d)", delta, oldLines, newLines)

	if delta != 0 {
		t.Errorf("REGESSION: LinesDelta should be 0, got %d", delta)
		t.Errorf("  Input lines: %d, Output lines: %d", oldLines, newLines)
		t.Errorf("  Input:  %q", content)
		t.Errorf("  Output: %q", result)
	}

	// Verify the result has exactly 3 non-split-stripped lines
	resultSplit := strings.Split(result, "\n")
	// Strip trailing empty from Split (as FormatContent does)
	if len(resultSplit) > 0 && resultSplit[len(resultSplit)-1] == "" {
		resultSplit = resultSplit[:len(resultSplit)-1]
	}
	if len(resultSplit) != 3 {
		t.Errorf("Expected 3 display lines, got %d: %q", len(resultSplit), resultSplit)
	}
}

// TestRegression_SwapLastLineWithEmptyBody_NoTrailingNewline tests the
// edge case where the file has NO trailing newline.
func TestRegression_SwapLastLineWithEmptyBody_NoTrailingNewline(t *testing.T) {
	// File WITHOUT trailing newline: 3 lines
	content := "line1\n\ninserted content"
	lines := strings.Split(content, "\n")
	hasTrailing := strings.HasSuffix(content, "\n")

	t.Logf("Input: %q", content)
	t.Logf("Split: %q (len=%d)", lines, len(lines))
	t.Logf("HasTrailingNewline: %v", hasTrailing)
	t.Logf("countLines(input): %d", countLines(content))

	// SWAP line 3 (1-based) → empty string
	op := Op{Kind: OpSWAP, LineStart: 3, LineEnd: 3, Body: []string{""}}

	result, _, err := applyEdits(content, []Op{op})
	if err != nil {
		t.Fatalf("applyEdits failed: %v", err)
	}

	t.Logf("Result: %q", result)
	t.Logf("countLines(result): %d", countLines(result))

	oldLines := countLines(content)
	newLines := countLines(result)
	delta := newLines - oldLines

	t.Logf("LinesDelta: %d (old=%d, new=%d)", delta, oldLines, newLines)

	// This should also retain line count (3→3), or at minimum not lose content.
	resultSplit := strings.Split(result, "\n")
	if len(resultSplit) > 0 && resultSplit[len(resultSplit)-1] == "" {
		resultSplit = resultSplit[:len(resultSplit)-1]
	}
	
	// After SWAP, should have 3 lines or 2 lines depending on trailing newline handling.
	// The key assertion: no data should be silently dropped.
	if len(resultSplit) < 2 {
		t.Errorf("Expected at least 2 display lines, got %d: %q", len(resultSplit), resultSplit)
	}
}
