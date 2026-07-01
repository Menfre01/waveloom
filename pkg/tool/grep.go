package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Menfre01/waveloom/pkg/pathutil"
)

// ---------------------------------------------------------------------------
// Grep — 正则内容搜索
// ---------------------------------------------------------------------------

const (
	// MaxGrepMatches 是 grep 返回的最大匹配行数。
	MaxGrepMatches = 250
)

type GrepParams struct {
	Pattern         string `json:"pattern"`
	Include         string `json:"include"`
	WorkingDir      string `json:"working_dir"`
	CaseInsensitive bool   `json:"case_insensitive"`
	ContextLines    int    `json:"context_lines"`
}

type Grep struct{}

func (t *Grep) Name() string            { return "grep" }
func (t *Grep) Description() string {
	return "Search for lines matching a regular expression (RE2 syntax). " +
		"Supports glob file filtering (include parameter) and context lines. Returns up to 250 matches. " +
		"IMPORTANT: use RE2 regex syntax, not PCRE — no lookahead/lookbehind/backreferences. " +
		"If pattern returns no results, try a simpler pattern or use search_file to locate files first."
}
func (t *Grep) Schema() json.RawMessage { return grepSchema }
func (t *Grep) ConcurrentSafe() bool    { return true }

type grepMatch struct {
	file    string
	lineNum int
	text    string
}

func (t *Grep) Execute(ctx context.Context, p GrepParams) (*ToolResult, error) {
	start := time.Now()

	// ── Step 1: 编译正则 ──
	pattern := p.Pattern
	if p.CaseInsensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		msg := fmt.Sprintf("invalid regex pattern: %v", err)
		if hint := pcreHint(p.Pattern); hint != "" {
			msg += "\n" + hint
		}
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs, msg, err), nil
	}

	// ── Step 2: 搜索目录 ──
	dir := p.WorkingDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	dir, err = pathutil.ResolvePath(dir)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("invalid working_dir: %v", err), err), nil
	}

	// ── Step 3: 收集文件 ──
	files, err := collectFiles(dir, p.Include)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("error walking directory: %v", err), err), nil
	}

	// ── Step 4: 逐文件搜索 ──
	var matches []grepMatch
	truncated := false

	for i, file := range files {
		// 每 32 个文件检查一次 ctx 取消
		if i%32 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		fileMatches, err := searchInFile(file, re, p.ContextLines)
		if err != nil {
			continue
		}
		for _, m := range fileMatches {
			if len(matches) >= MaxGrepMatches {
				truncated = true
				break
			}
			matches = append(matches, m)
		}
		if truncated {
			break
		}
	}

	// ── Step 5: 格式化 ──
	duration := time.Since(start)

	// 按文件 + 行号排序
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].file != matches[j].file {
			return matches[i].file < matches[j].file
		}
		return matches[i].lineNum < matches[j].lineNum
	})

	content := formatGrepResults(matches, dir, p.Pattern, truncated, duration)

	return &ToolResult{
		Content: content,
		Meta: ToolMeta{
			LineCount: len(matches),
			Duration:  duration,
		},
	}, nil
}

// ── collectFiles ──

func collectFiles(dir, include string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && ShouldSkipDir(d.Name()) {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		if include != "" {
			if matched, _ := filepath.Match(include, d.Name()); !matched {
				return nil
			}
		}
		// 跳过已知二进制扩展名（零 I/O）
		if HasBinaryExtension(path) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	return files, err
}

// ── searchInFile ──

type ringEntry struct {
	num  int
	text string
}

func searchInFile(path string, re *regexp.Regexp, contextLines int) ([]grepMatch, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	if contextLines <= 0 {
		// 无上下文 → 流式匹配
		var matches []grepMatch
		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			if re.MatchString(scanner.Text()) {
				matches = append(matches, grepMatch{
					file: path, lineNum: lineNum, text: scanner.Text(),
				})
			}
		}
		return matches, scanner.Err()
	}

	// 有上下文 → 滑动窗口
	var matches []grepMatch
	scanner := bufio.NewScanner(f)
	ring := make([]ringEntry, 0, contextLines)
	pendingAfter := 0
	lastEmitted := -1
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		if re.MatchString(line) {
			for _, entry := range ring {
				if entry.num > lastEmitted {
					matches = append(matches, grepMatch{file: path, lineNum: entry.num, text: "  " + entry.text})
					lastEmitted = entry.num
				}
			}
			matches = append(matches, grepMatch{file: path, lineNum: lineNum, text: "> " + line})
			lastEmitted = lineNum
			pendingAfter = contextLines
			ring = ring[:0]
		} else if pendingAfter > 0 {
			matches = append(matches, grepMatch{file: path, lineNum: lineNum, text: "  " + line})
			lastEmitted = lineNum
			pendingAfter--
		} else {
			if len(ring) >= contextLines {
				ring = ring[1:]
			}
			ring = append(ring, ringEntry{num: lineNum, text: line})
		}
	}
	return matches, scanner.Err()
}

// ── formatGrepResults ──

func formatGrepResults(matches []grepMatch, dir string, pattern string, truncated bool, duration time.Duration) string {
	var buf strings.Builder

	if len(matches) == 0 {
		fmt.Fprintf(&buf, "No matches found for %q in %s.\n", pattern, relOrDir(dir))
		fmt.Fprintf(&buf, "Searched under: %s", dir)
		return buf.String()
	}

	fmt.Fprintf(&buf, "Found %d match(es) for %q in %s (%s):",
		len(matches), pattern, relOrDir(dir), duration.Round(time.Millisecond))

	if truncated {
		fmt.Fprintf(&buf, "\nResults truncated to %d. Narrow the pattern or use include to reduce results.",
			MaxGrepMatches)
	}

	// 按文件分组输出
	var currentFile string
	for _, m := range matches {
		rel, _ := filepath.Rel(dir, m.file)
		if rel == "" {
			rel = m.file
		}

		if rel != currentFile {
			currentFile = rel
			fmt.Fprintf(&buf, "\n── %s ──", rel)
		}
		fmt.Fprintf(&buf, "\n  %d: %s", m.lineNum, m.text)
	}
	buf.WriteByte('\n')

	return buf.String()
}

// pcreHint detects common PCRE-only constructs in a regex pattern and returns
// a hint for converting to RE2-compatible syntax.
// Like edit_file's line-number-prefix detection, this catches a frequent LLM mistake
// before the user has to diagnose it from a raw regex compile error.
func pcreHint(pattern string) string {
	// Lookahead / lookbehind — the most common PCRE mistake
	if strings.Contains(pattern, "(?=") || strings.Contains(pattern, "(?!") ||
		strings.Contains(pattern, "(?<=") || strings.Contains(pattern, "(?<!") {
		return "Hint: lookahead/lookbehind are not supported in RE2. Use a simpler pattern or match and filter in a second step."
	}
	// Backreferences like \1, \2
	for i := 1; i <= 9; i++ {
		if strings.Contains(pattern, fmt.Sprintf("\\%d", i)) {
			return "Hint: backreferences are not supported in RE2. Use a repeated subpattern or match-and-filter approach."
		}
	}
	// \K (keep out)
	if strings.Contains(pattern, `\K`) {
		return "Hint: \\K is a PCRE-only feature. Use a capture group and reference the group instead."
	}
	// Recursive / subroutine patterns
	if strings.Contains(pattern, "(?R)") || strings.Contains(pattern, "(?&") || strings.Contains(pattern, "(?P<") {
		return "Hint: recursive/subroutine patterns are not supported in RE2. Simplify to a bounded repetition."
	}
	// Atomic groups
	if strings.Contains(pattern, "(?>") {
		return "Hint: atomic groups are not supported in RE2. Remove the ?> prefix from the group."
	}
	// Possessive quantifiers
	if strings.Contains(pattern, "++") || strings.Contains(pattern, "*+") || strings.Contains(pattern, "?+") {
		return "Hint: possessive quantifiers are not supported in RE2. Remove the + after the quantifier."
	}
	return ""
}
