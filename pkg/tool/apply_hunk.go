package tool

import (
	"fmt"
	"os"
	"strings"
	"github.com/Menfre01/waveloom/pkg/hashline"
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
	// Failure diagnostics
	FileSnippet  string   // file content around expected location
	ClosestMatch string   // closest-match hint
	CharDiff     string   // character-level diff (if distance <= 3)
}

// ApplyHunk applies a multi-file multi-hunk patch to the filesystem.
// readStates is consulted for conflict detection; on success, read states are updated.
func ApplyHunk(path, hunkText string, readStates *ReadStateStore) ([]HunkResult, error) {
	files := parsePatchFiles(hunkText, path)
	if len(files) == 0 {
		return nil, fmt.Errorf("no hunks found in patch")
	}

	var allResults []HunkResult

	for _, pf := range files {
		results := applyFileHunks(pf.path, pf.hunks, readStates)
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
	oldTxt  string // original hunk text for diagnostics
}

func parsePatchFiles(text string, defaultPath string) []patchFile {
	var files []patchFile
	var current *patchFile

	lines := strings.Split(text, "\n")
	i := 0

	for i < len(lines) {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "*** Update File:") {
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Update File:"))
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
	h.oldTxt = h.header + "\n"
	*i++

	for *i < len(lines) {
		line := lines[*i]
		// Stop at next @@ or *** Update File
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "@@") || strings.HasPrefix(trimmed, "*** Update File:") {
			break
		}
		if strings.HasPrefix(trimmed, "*** End Patch") {
			break
		}
		h.oldTxt += line + "\n"

		if len(line) == 0 {
			*i++
			continue
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
	return h
}

// ---------------------------------------------------------------------------
// File-level hunk application
// ---------------------------------------------------------------------------

func applyFileHunks(filePath string, hunks []patchHunk, readStates *ReadStateStore) []HunkResult {
	var results []HunkResult

	if readStates != nil {
		ok, reason := readStates.Validate(filePath)
		if !ok {
			for _, h := range hunks {
				results = append(results, HunkResult{
					File:  filePath,
					Header: h.header,
					Error: reason,
				})
			}
			return results
		}
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		for _, h := range hunks {
			results = append(results, HunkResult{
				File:  filePath,
				Header: h.header,
				Error: fmt.Sprintf("cannot read file: %v", err),
			})
		}
		return results
	}

	fileLines := strings.Split(string(data), "\n")
	lastMatch := 0
	lineOffset := 0

	for hi, h := range hunks {
		matchStart := seekHunk(fileLines, h.pattern, lastMatch)
		if matchStart < 0 {
			diagnostics := buildHunkDiagnostics(filePath, fileLines, h, lastMatch)
			results = append(results, HunkResult{
				File:         filePath,
				Header:       h.header,
				Error:        "hunk not found",
				OldLines:     h.pattern,
				NewLines:     h.replace,
				FileSnippet:  diagnostics.fileSnippet,
				ClosestMatch: diagnostics.closestMatch,
				CharDiff:     diagnostics.charDiff,
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
		})

		_ = hi
	}

	// Write file
	newContent := hashline.NormalizeFileContent(strings.Join(fileLines, "\n"))
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
	charDiff     string
}

func buildHunkDiagnostics(filePath string, fileLines []string, h patchHunk, searchStart int) hunkDiagnostics {
	var d hunkDiagnostics

	// Layer 1: file snippet around expected location
	snippetStart := searchStart - 3
	if snippetStart < 0 {
		snippetStart = 0
	}
	snippetEnd := searchStart + len(h.pattern) + 3
	if snippetEnd > len(fileLines) {
		snippetEnd = len(fileLines)
	}
	var b strings.Builder
	for i := snippetStart; i < snippetEnd; i++ {
		fmt.Fprintf(&b, "   %d: %s\n", i+1, fileLines[i])
	}
	d.fileSnippet = b.String()

	// Layer 2: closest-match (Levenshtein per line, top-3)
	if len(h.pattern) > 0 {
		query := h.pattern[0]
		type candidate struct {
			line int
			dist int
		}
		var best [3]candidate
		for i := range best {
			best[i] = candidate{line: -1, dist: 9999}
		}
		for i, l := range fileLines {
			d := levenshteinDist(query, l)
			if d == 0 {
				continue
			}
			for j := range best {
				if d < best[j].dist {
					for k := 2; k > j; k-- {
						best[k] = best[k-1]
					}
					best[j] = candidate{line: i + 1, dist: d}
					break
				}
			}
		}
		if best[0].line > 0 {
			var cb strings.Builder
			for _, c := range best {
				if c.line < 0 {
					break
				}
				marker := "→"
				fmt.Fprintf(&cb, "  %s Line %d: %s (distance=%d)\n", marker, c.line, fileLines[c.line-1], c.dist)
				// Layer 3: char diff for closest match
				if c.dist > 0 && c.dist <= 3 {
					cb.WriteString(charDiff(query, fileLines[c.line-1]))
				}
			}
			d.closestMatch = cb.String()
		}
	}

	return d
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
	var result strings.Builder
	result.WriteString("    old: ")
	result.WriteString(a)
	result.WriteByte('\n')
	result.WriteString("    new: ")
	result.WriteString(b)
	result.WriteByte('\n')
	result.WriteString("          ")
	for i := 0; i < len(ra) && i < len(rb); i++ {
		if ra[i] != rb[i] {
			result.WriteByte('^')
		} else {
			result.WriteByte(' ')
		}
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
		case '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212':
			return '-'
		case '\u2018', '\u2019', '\u201A', '\u201B':
			return '\''
		case '\u201C', '\u201D', '\u201E', '\u201F':
			return '"'
		case '\u00A0', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006',
			'\u2007', '\u2008', '\u2009', '\u200A', '\u202F', '\u205F', '\u3000':
			return ' '
		}
		return r
	}, s)
}
