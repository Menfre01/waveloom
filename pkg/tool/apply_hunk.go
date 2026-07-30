package tool

import (
	"context"
	"fmt"
	"os"
	"strings"

	"path/filepath"

	"github.com/Menfre01/waveloom/pkg/filehistory"
)

// ---------------------------------------------------------------------------
// Hunk application engine
// ---------------------------------------------------------------------------

// HunkResult reports the outcome of applying a single hunk.
type HunkResult struct {
	File     string
	Header   string // @@ header text
	Line     int    // line where hunk was applied (1-based), 0 if failed
	Error    string // empty if success
	OldLines []string
	NewLines []string
	RawBody  string // raw hunk body (lines after @@ header), for DiffHunk construction
	// Failure diagnostics
	FileSnippet  string   // file content around expected location
	ClosestMatch string   // closest-match hint
}

// ApplyHunk applies a multi-file multi-hunk patch to the filesystem.
// readStates is consulted for conflict detection; on success, read states are updated.
func ApplyHunk(ctx context.Context, path, hunkText string, readStates *ReadStateStore) ([]HunkResult, error) {
	files := parsePatchFiles(hunkText, path)
	if len(files) == 0 {
		// 无 hunk — 返回空结果,由 edit 层处理为 "no changes needed"
		return nil, nil
	}

	var allResults []HunkResult

	for _, pf := range files {
		results := applyFileHunks(ctx, pf.path, pf.hunks, readStates)
		allResults = append(allResults, results...)
	}

	return allResults, nil
}

// ---------------------------------------------------------------------------
// Patch parsing
// ---------------------------------------------------------------------------

type patchFile struct {
	path  string
	hunks []patchHunk
}

type patchHunk struct {
	header  string // @@ line text
	pattern []string
	replace []string
	rawBody string // raw hunk body (lines between @@ header and next hunk/EOF)
}

func parsePatchFiles(text string, defaultPath string) []patchFile {
	var files []patchFile
	var current *patchFile

	lines := strings.Split(text, "\n")
	i := 0

	for i < len(lines) {
		line := strings.TrimSpace(lines[i])
		// Envelope markers — skip, don't create phantom entries
		if strings.HasPrefix(line, "*** Begin Patch") || strings.HasPrefix(line, "*** End Patch") {
			i++
			continue
		}
		if strings.HasPrefix(line, "*** Update File:") {
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Update File:"))
			if !filepath.IsAbs(path) && defaultPath != "" {
				path = filepath.Join(filepath.Dir(defaultPath), path)
			}
			files = append(files, patchFile{path: path})
			current = &files[len(files)-1]
			i++
			continue
		}
		if current == nil {
			// No *** Update File header — use default path for all hunks
			files = append(files, patchFile{path: defaultPath})
			current = &files[len(files)-1]
		}
		if strings.HasPrefix(line, "@@") {
			hunk := parseOneHunk(lines, &i)
			current.hunks = append(current.hunks, hunk)
		} else {
			i++
		}
	}

	return files
}

func parseOneHunk(lines []string, i *int) patchHunk {
	h := patchHunk{}
	// Parse @@ header
	h.header = strings.TrimSpace(lines[*i])
	*i++
	bodyStart := *i

	for *i < len(lines) {
		line := lines[*i]
		// Stop at next @@ or *** Update File or *** End Patch
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "@@") || strings.HasPrefix(trimmed, "*** Update File:") {
			break
		}
		if strings.HasPrefix(trimmed, "*** End Patch") {
			break
		}

		if len(line) == 0 {
			// Handle bare empty lines (no space prefix).
			// Strict unified diff requires a space prefix, but LLMs and humans
			// naturally use blank lines for visual separation.
			//
			// Strategy: scan forward through consecutive empty lines. If the
			// run ends at a boundary/EOF, all are trailing artifacts → skip.
			// If the run ends at a content line, all are intra-body → context.
			emptyEnd := *i
			for emptyEnd < len(lines) && len(lines[emptyEnd]) == 0 {
				emptyEnd++
			}
			if emptyEnd >= len(lines) || isHunkBoundary(lines[emptyEnd]) {
				// Trailing empty run — skip all, let the loop see the boundary next
				*i = emptyEnd
				continue
			} else {
				// Intra-body empty run — treat all as context
				for *i < emptyEnd {
					h.pattern = append(h.pattern, "")
					h.replace = append(h.replace, "")
					*i++
				}
				continue
			}
		}
		switch line[0] {
		case ' ':
			h.pattern = append(h.pattern, line[1:])
			h.replace = append(h.replace, line[1:])
		case '-':
			h.pattern = append(h.pattern, line[1:])
		case '+':
			h.replace = append(h.replace, line[1:])
		default:
			// Treat as context
			h.pattern = append(h.pattern, line)
			h.replace = append(h.replace, line)
		}
		*i++
	}
	// Store raw hunk body (lines between @@ header and next hunk/EOF)
	h.rawBody = strings.Join(lines[bodyStart:*i], "\n")
	return h
}

// isHunkBoundary reports whether a line marks the end of a hunk body
// (next hunk header, file marker, or envelope marker).
// NOTE: Keep in sync with the break conditions in parseOneHunk's for-loop
// (lines 120-125). Any new boundary marker added there must be added here too.
func isHunkBoundary(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "@@") ||
		strings.HasPrefix(trimmed, "*** Update File:") ||
		strings.HasPrefix(trimmed, "*** End Patch")
}

// ---------------------------------------------------------------------------
// File-level hunk application
// ---------------------------------------------------------------------------

func applyFileHunks(ctx context.Context, filePath string, hunks []patchHunk, readStates *ReadStateStore) []HunkResult {
	var results []HunkResult

	if readStates != nil {
		ok, reason := readStates.Validate(filePath)
		if !ok {
			for _, h := range hunks {
				results = append(results, HunkResult{
					File:    filePath,
					Header:  h.header,
					Error:   reason,
					RawBody: h.rawBody,
				})
			}
			return results
		}
	}

	// ── FileHistory tracking: backup file before modification, so rewind can restore ──
	if fh := filehistory.FromContext(ctx); fh != nil {
		if msgID := filehistory.MessageIDFromContext(ctx); msgID != "" {
			if sd := filehistory.SessionDirFromContext(ctx); sd != "" {
				fh.TrackEdit(filePath, msgID, sd)
			}
		}
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		for _, h := range hunks {
			results = append(results, HunkResult{
				File:    filePath,
				Header:  h.header,
				Error:   fmt.Sprintf("cannot read file: %v", err),
				RawBody: h.rawBody,
			})
		}
		return results
	}

	fileLines := strings.Split(string(data), "\n")
	lastMatch := 0
	lineOffset := 0

	for _, h := range hunks {
		matchStart := seekHunk(fileLines, h.pattern, lastMatch)
		if matchStart < 0 {
			diagnostics := buildHunkDiagnostics(filePath, fileLines, h)
			results = append(results, HunkResult{
				File:         filePath,
				Header:       h.header,
				Error:        "hunk not found",
				OldLines:     h.pattern,
				NewLines:     h.replace,
				RawBody:      h.rawBody,
				FileSnippet:  diagnostics.fileSnippet,
				ClosestMatch: diagnostics.closestMatch,
			})
			continue
		}

		// Apply the hunk
		var newLines []string
		newLines = append(newLines, fileLines[:matchStart]...)
		newLines = append(newLines, h.replace...)
		newLines = append(newLines, fileLines[matchStart+len(h.pattern):]...)

		oldLen := len(h.pattern)
		newLen := len(h.replace)
		lineOffset += newLen - oldLen
		lastMatch = matchStart + newLen

		fileLines = newLines
		results = append(results, HunkResult{
			File:     filePath,
			Header:   h.header,
			Line:     matchStart + 1,
			OldLines: h.pattern,
			NewLines: h.replace,
			RawBody:  h.rawBody,
		})

	}

	// Write file
	newContent := normalizeFileContent(strings.Join(fileLines, "\n"))
	if err := os.WriteFile(filePath, []byte(newContent), 0o644); err != nil {
		// Mark remaining results as failed
		for i := len(results); i < len(hunks); i++ {
			// Already have results for all processed hunks
		}
		return results
	}

	// Update readState
	if readStates != nil {
		readStates.Update(filePath, newContent)
	}

	return results
}

// ---------------------------------------------------------------------------
// Diagnostics
// ---------------------------------------------------------------------------

type hunkDiagnostics struct {
	fileSnippet  string
	closestMatch string
}

func buildHunkDiagnostics(filePath string, fileLines []string, h patchHunk) hunkDiagnostics {
	var d hunkDiagnostics

	// Find the best matching position for the pattern in the file.
	bestPos := findBestHunkPosition(fileLines, h.pattern)

	// Layer 1: file snippet around best match position
	snippetStart := bestPos - 3
	if snippetStart < 0 {
		snippetStart = 0
	}
	snippetEnd := bestPos + len(h.pattern) + 3
	if snippetEnd > len(fileLines) {
		snippetEnd = len(fileLines)
	}
	var b strings.Builder
	for i := snippetStart; i < snippetEnd; i++ {
		fmt.Fprintf(&b, "   %d: %s\n", i+1, fileLines[i])
	}
	d.fileSnippet = b.String()

	// Layer 2: per-line diagnostics at the best match position
	if len(h.pattern) > 0 && bestPos >= 0 && bestPos+len(h.pattern) <= len(fileLines) {
		var cb strings.Builder
		for j, p := range h.pattern {
			fl := fileLines[bestPos+j]
			if p == fl {
				continue
			}
			dist := levenshteinDist(p, fl)
			fmt.Fprintf(&cb, "  Line %d: -%s\n", bestPos+j+1, p)
			fmt.Fprintf(&cb, "  Line %d: +%s\n", bestPos+j+1, fl)
			if dist > 0 && dist <= 5 {
				cb.WriteString(charDiff(p, fl))
			}
		}
		// Always report layer diagnostics — even when all lines match exactly,
		// the hunk may have been missed because lastMatch overshot the position.
		cb.WriteString(diagnoseMatchLayer(fileLines, h.pattern, bestPos))
		d.closestMatch = cb.String()
	}

	return d
}

// findBestHunkPosition finds the file line best matching the first pattern line.
// Uses progressive matching (exact → rstrip → trim → unicode), falling back to Levenshtein.
func findBestHunkPosition(fileLines, pattern []string) int {
	if len(pattern) == 0 {
		return 0
	}
	firstPattern := pattern[0]
	firstTrim := strings.TrimSpace(firstPattern)
	firstUnicode := normalizeUnicode2(firstTrim)

	// Pass 1: find pattern[0] via progressive matching
	for i, l := range fileLines {
		if l == firstPattern ||
			strings.TrimRight(l, " \t\r") == strings.TrimRight(firstPattern, " \t\r") ||
			strings.TrimSpace(l) == firstTrim ||
			normalizeUnicode2(strings.TrimSpace(l)) == firstUnicode {
			return i
		}
	}

	// Pass 2: best Levenshtein match
	bestDist := -1
	bestIdx := 0
	for i, l := range fileLines {
		d := levenshteinDist(firstTrim, strings.TrimSpace(l))
		if bestDist < 0 || d < bestDist {
			bestDist = d
			bestIdx = i
		}
	}
	return bestIdx
}

// diagnoseMatchLayer reports which matching layer would have passed or came closest,
// helping the LLM understand whether the issue is whitespace, Unicode, or content.
func diagnoseMatchLayer(fileLines, pattern []string, pos int) string {
	if pos < 0 || pos+len(pattern) > len(fileLines) || len(pattern) == 0 {
		return ""
	}

	exactOk, rstripOk, trimOk, unicodeOk := true, true, true, true
	for j, p := range pattern {
		fl := fileLines[pos+j]
		if fl != p {
			exactOk = false
		}
		if strings.TrimRight(fl, " \t\r") != strings.TrimRight(p, " \t\r") {
			rstripOk = false
		}
		if strings.TrimSpace(fl) != strings.TrimSpace(p) {
			trimOk = false
		}
		if normalizeUnicode2(strings.TrimSpace(fl)) != normalizeUnicode2(strings.TrimSpace(p)) {
			unicodeOk = false
		}
	}

	switch {
	case exactOk:
		return fmt.Sprintf("  (pattern exists at line %d — search may have started after it; re-read with more context lines and retry edit — do NOT use python/sed/bash)\n", pos+1)
	case rstripOk:
		return "  (matches after stripping trailing whitespace)\n"
	case trimOk:
		return "  (matches after trimming leading+trailing whitespace)\n"
	case unicodeOk:
		return "  (matches after Unicode normalization — check for invisible/double-width characters)\n"
	default:
		// Compute per-layer distance to find the closest
		exactDist, rstripDist, trimDist, unicodeDist := 0, 0, 0, 0
		for j, p := range pattern {
			fl := fileLines[pos+j]
			exactDist += levenshteinDist(fl, p)
			rstripDist += levenshteinDist(
				strings.TrimRight(fl, " \t\r"),
				strings.TrimRight(p, " \t\r"))
			trimDist += levenshteinDist(strings.TrimSpace(fl), strings.TrimSpace(p))
			unicodeDist += levenshteinDist(
				normalizeUnicode2(strings.TrimSpace(fl)),
				normalizeUnicode2(strings.TrimSpace(p)))
		}
		layer, dist := "exact", exactDist
		if rstripDist < dist {
			layer, dist = "rstrip", rstripDist
		}
		if trimDist < dist {
			layer, dist = "trim", trimDist
		}
		if unicodeDist < dist {
			layer, dist = "unicode", unicodeDist
		}
		return fmt.Sprintf("  (closest layer: %s, remaining distance=%d)\n", layer, dist)
	}
}

func levenshteinDist(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min3(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func charDiff(a, b string) string {
	ra, rb := []rune(a), []rune(b)
	// Find first position where strings differ
	firstDiff := 0
	for firstDiff < len(ra) && firstDiff < len(rb) && ra[firstDiff] == rb[firstDiff] {
		firstDiff++
	}

	// Identical
	if firstDiff == len(ra) && firstDiff == len(rb) {
		return ""
	}

	var result strings.Builder
	result.WriteString("    old: ")
	result.WriteString(a)
	result.WriteByte('\n')
	result.WriteString("    new: ")
	result.WriteString(b)
	result.WriteByte('\n')
	result.WriteString("         ")
	// Single ^ marker at first differing character (count bytes, not runes, for alignment)
	markerPos := len(string(ra[:firstDiff]))
	for i := 0; i < markerPos; i++ {
		result.WriteByte(' ')
	}
	result.WriteByte('^')
	if firstDiff >= len(ra) {
		result.WriteString(" (old ends, new continues)")
	} else if firstDiff >= len(rb) {
		result.WriteString(" (new ends, old continues)")
	}
	result.WriteByte('\n')
	return result.String()
}

// ---------------------------------------------------------------------------
// 4-layer progressive matching
// ---------------------------------------------------------------------------
func seekHunk(fileLines, pattern []string, startFrom int) int {
	if len(pattern) == 0 || len(pattern) > len(fileLines) {
		return -1
	}
	searchStart := startFrom
	if searchStart < 0 || searchStart > len(fileLines)-len(pattern) {
		searchStart = 0
	}
	if idx := seekExact(fileLines, pattern, searchStart); idx >= 0 {
		return idx
	}
	if idx := seekRstrip(fileLines, pattern, searchStart); idx >= 0 {
		return idx
	}
	if idx := seekTrim(fileLines, pattern, searchStart); idx >= 0 {
		return idx
	}
	if idx := seekUnicode(fileLines, pattern, searchStart); idx >= 0 {
		return idx
	}
	return -1
}
func seekExact(lines, pattern []string, start int) int {
	for i := start; i <= len(lines)-len(pattern); i++ {
		match := true
		for j, p := range pattern {
			if lines[i+j] != p {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
func seekRstrip(lines, pattern []string, start int) int {
	for i := start; i <= len(lines)-len(pattern); i++ {
		match := true
		for j, p := range pattern {
			if strings.TrimRight(lines[i+j], " \t\r") != strings.TrimRight(p, " \t\r") {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
func seekTrim(lines, pattern []string, start int) int {
	for i := start; i <= len(lines)-len(pattern); i++ {
		match := true
		for j, p := range pattern {
			if strings.TrimSpace(lines[i+j]) != strings.TrimSpace(p) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
func seekUnicode(lines, pattern []string, start int) int {
	for i := start; i <= len(lines)-len(pattern); i++ {
		match := true
		for j, p := range pattern {
			if normalizeUnicode2(strings.TrimSpace(lines[i+j])) != normalizeUnicode2(strings.TrimSpace(p)) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
func normalizeUnicode2(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		// Invisible characters — drop them (return -1).
		// These are matching poison: they break hunk alignment but are invisible to users.
		case '\u00AD', '\u200B', '\u200C', '\u200D', '\u200E', '\u200F',
			'\u2028', '\u2029', '\u2060', '\uFEFF':
			return -1
		// Fullwidth punctuation → ASCII (U+FF01–U+FF5E offset 0xFEE0).
		// Skips fullwidth letters (U+FF21–U+FF3A, U+FF41–U+FF5A) and digits (U+FF10–U+FF19)
		// since those are semantically distinct in CJK contexts.
		case '\uFF01', '\uFF02', '\uFF03', '\uFF04', '\uFF05', '\uFF06', '\uFF07',
			'\uFF08', '\uFF09', '\uFF0A', '\uFF0B', '\uFF0C', '\uFF0D', '\uFF0E', '\uFF0F':
			return r - 0xFEE0
		case '\uFF1A', '\uFF1B', '\uFF1C', '\uFF1D', '\uFF1E', '\uFF1F', '\uFF20':
			return r - 0xFEE0
		case '\uFF3B', '\uFF3C', '\uFF3D', '\uFF3E', '\uFF3F', '\uFF40':
			return r - 0xFEE0
		case '\uFF5B', '\uFF5C', '\uFF5D', '\uFF5E':
			return r - 0xFEE0
		// CJK Compatibility Forms (vertical presentation) → ASCII
		case '\uFE35', '\uFE59':
			return '('
		case '\uFE36', '\uFE5A':
			return ')'
		case '\uFE37', '\uFE5B':
			return '{'
		case '\uFE38', '\uFE5C':
			return '}'
		case '\uFE39', '\uFE3B', '\uFE47', '\uFE5D':
			return '['
		case '\uFE3A', '\uFE3C', '\uFE48', '\uFE5E':
			return ']'
		case '\uFE3D', '\uFE3E', '\uFE41', '\uFE42', '\uFE43', '\uFE44':
			return '"'
		case '\uFE3F':
			return '<'
		case '\uFE40':
			return '>'
		case '\uFE30':
			return ':'
		case '\uFE31', '\uFE32', '\uFE33', '\uFE34':
			return '|'
		case '\uFE45', '\uFE46':
			return '?'
		case '\uFE4D', '\uFE4E', '\uFE4F':
			return '_'
		// Small Form Variants → ASCII
		case '\uFE50', '\uFE51':
			return ','
		case '\uFE52':
			return '.'
		case '\uFE54':
			return ';'
		case '\uFE55':
			return ':'
		case '\uFE56':
			return '?'
		case '\uFE57':
			return '!'
		case '\uFE5F':
			return '#'
		case '\uFE60':
			return '&'
		case '\uFE61':
			return '*'
		case '\uFE62':
			return '+'
		case '\uFE64':
			return '<'
		case '\uFE65':
			return '>'
		case '\uFE66':
			return '='
		case '\uFE68':
			return '\\'
		case '\uFE69':
			return '$'
		case '\uFE6A':
			return '%'
		case '\uFE6B':
			return '@'
		// CJK punctuation → ASCII
		case '\u3001':
			return ','
		case '\u3002':
			return '.'
		// CJK brackets → ASCII
		case '\u3008', '\u300A':
			return '<'
		case '\u3009', '\u300B':
			return '>'
		case '\u300C', '\u300E':
			return '"'
		case '\u300D', '\u300F':
			return '"'
		case '\u3010', '\u3016':
			return '['
		case '\u3011', '\u3017':
			return ']'
		case '\u3014':
			return '('
		case '\u3015':
			return ')'
		// CJK quotation marks
		case '\u301D', '\u301E', '\u301F':
			return '"'
		// CJK wave dash
		case '\u301C', '\u3030':
			return '~'
		// Fullwidth white parentheses (same block as fullwidth punctuation)
		case '\uFF5F':
			return '('
		case '\uFF60':
			return ')'
		case '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212':
			return '-'
		// Small form dashes
		case '\uFE58', '\uFE63':
			return '-'
		// Two-em / three-em dash → - (single char; multi-char not supported by strings.Map)
		case '\u2E3A', '\u2E3B':
			return '-'
		// Hyphen bullet → -
		case '\u2043':
			return '-'
		// Superscript/subscript operators → ASCII
		case '\u207A', '\u208A':
			return '+'
		case '\u207B', '\u208B':
			return '-'
		case '\u207C', '\u208C':
			return '='
		case '\u207D', '\u208D':
			return '('
		case '\u207E', '\u208E':
			return ')'
		// Asterisk operator → *
		case '\u2217':
			return '*'
		// Ratio → :
		case '\u2236':
			return ':'
		// Ellipsis → .
		case '\u2026':
			return '.'
		// Bullet → -
		case '\u2022':
			return '-'
		// Guillemets → "
		case '\u00AB', '\u00BB':
			return '"'
		// Middle dot → .
		case '\u00B7':
			return '.'
		// Prime / double prime → ' / "
		case '\u2032':
			return '\''
		case '\u2033':
			return '"'
		// Single guillemets → '
		case '\u2039', '\u203A':
			return '\''
		// Fraction slash → /
		case '\u2044':
			return '/'
		// Halfwidth Katakana punctuation
		case '\uFF61':
			return '.'
		case '\uFF62', '\uFF63':
			return '"'
		case '\uFF64':
			return ','
		case '\uFF65':
			return '\u00B7' // · (middle dot, keep as-is since no ASCII equivalent)
		case '\u2018', '\u2019', '\u201A', '\u201B':
			return '\''
		case '\u201C', '\u201D', '\u201E', '\u201F':
			return '"'
		// Additional space variants
		case '\u2000', '\u2001':
			return ' '
		// Tilde operator → ~
		case '\u223C':
			return '~'
		case '\u00A0', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006',
			'\u2007', '\u2008', '\u2009', '\u200A', '\u202F', '\u205F', '\u3000':
			return ' '
		}
		return r
	}, s)
}

// normalizeFileContent normalizes file content before writing.
// Rules: \r\n → \n, collapses 3+ blank lines to 2, strips trailing whitespace.
func normalizeFileContent(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	return strings.Join(lines, "\n")
}
