package tool

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Menfre01/waveloom/pkg/lsp"
	"github.com/Menfre01/waveloom/pkg/pathutil"
)

//go:embed read_file_prompt.md
var readFilePrompt string

// ---------------------------------------------------------------------------
// ReadFile — 读取文件内容,返回带行号的文件内容
// ---------------------------------------------------------------------------

type ReadFileParams struct {
	FilePath     string `json:"file_path"`     // 与 read_file 一致
	Offset       int    `json:"offset"`        // 0-based: 0 = 文件第一行
	Limit        int    `json:"limit"`         // 读取行数(0 = 不限)
	Pattern      string `json:"pattern"`       // 可选:在文件中定位子串,窗口显示第一个匹配 ±context_lines
	ContextLines int    `json:"context_lines"` // 匹配行上下各显示的行数(默认 5,最大 50)
	WorkingDir   string `json:"working_dir"`   // 工作目录(可选)
	Outline      bool   `json:"outline"`       // 返回符号大纲而非文件内容
}

// largeReadHintBytes 大文件软提示门限:超过该字节数且未使用定向参数
// (pattern/limit)时,read 输出头部提示定向读取,避免全量内容膨胀上下文。
const largeReadHintBytes = 100 * 1024 // 100KB

// repeatedReadHintBytes 重复 read 提示门限:大文件内容未变时提示定向读取;
// 小文件重复 read 成本低(缓存命中率高),不提示避免噪音。
const repeatedReadHintBytes = 50 * 1024 // 50KB

type ReadFile struct{}

func (t *ReadFile) Name() string { return "read" }

func (t *ReadFile) Description() string {
	return "Read a file with line numbers for editing. Rules: see system prompt ## File Operations."
}

// Prompt 返回 read 工具使用指南,由 Registry.FormatToolPrompts() 注入 system prompt。
func (t *ReadFile) Prompt() string { return readFilePrompt }

var readFileHashlineSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "file_path": {
      "type": "string",
      "description": "File path (absolute, or relative to working_dir / workspace root). Must be a file, not a directory — use shell('ls') first to explore directories. Paths without a file extension are likely directories."
    },
    "offset": {
      "type": "integer",
      "description": "Without pattern: starting line number (0-based, 0 = first line, optional). With pattern: match index (0-based) to page through matches."
    },
    "limit": {
      "type": "integer",
      "description": "Number of lines to read (optional, default: all)"
    },
    "pattern": {
      "type": "string",
      "description": "Optional substring to locate in the file. When present, the output centers on the first match ±context_lines, eliminating a separate grep call. Use offset/limit to page through additional matches."
    },
    "context_lines": {
      "type": "integer",
      "description": "Lines of context above and below each match (default: 5, max: 50). 0 is treated as default. Only meaningful with pattern. For match-line-only, use limit=1."
    },
    "working_dir": {
      "type": "string",
      "description": "Working directory (optional)"
    },
    "outline": {
      "type": "boolean",
      "description": "Set to true to return a symbol outline (function/type/variable names with line numbers) instead of full file content. Uses LSP when available, falls back to regex for unsupported file types. Preferred for exploring unfamiliar files before reading full content."
    }
  },
  "required": ["file_path"]
}`)

func (t *ReadFile) Schema() json.RawMessage { return readFileHashlineSchema }

func (t *ReadFile) ConcurrentSafe() bool { return true }

func (t *ReadFile) Execute(ctx context.Context, p ReadFileParams) (*ToolResult, error) {
	// ── Step 1: 路径解析 ──
	path, err := pathutil.ResolvePathWithDir(p.FilePath, p.WorkingDir)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("invalid path: %v", err), err), nil
	}

	// ── Step 2: 设备文件拦截 ──
	if IsBlockedDevicePath(path) {
		return toolError(ErrorClassFatal, ErrKindSecurityViolation,
			fmt.Sprintf("cannot read device file: %s", path), nil), nil
	}

	// ── Step 3: 文件检查 ──
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fileNotFoundError(path), nil
		}
		return toolError(ErrorClassFatal, ErrKindPermissionDenied,
			fmt.Sprintf("cannot access file: %s", path), err), nil
	}

	if info.IsDir() {
		entries, readErr := os.ReadDir(path)
		if readErr == nil {
			sortDirEntries(entries)
			var listing strings.Builder
			fmt.Fprintf(&listing, "Path is a directory, not a file: %s\n\n", path)
			if suggestion := suggestFileInDir(path, entries); suggestion != "" {
				fmt.Fprintf(&listing, "Did you mean %s?\n\n", suggestion)
			}
			const maxDisplay = 50
			total := len(entries)
			if total > maxDisplay {
				fmt.Fprintf(&listing, "Showing first %d of %d entries (use ls for more):\n", maxDisplay, total)
			} else {
				listing.WriteString("Contents:\n")
			}
			for i, entry := range entries {
				if i >= maxDisplay {
					fmt.Fprintf(&listing, "  ... and %d more entries\n", total-maxDisplay)
					break
				}
				name := entry.Name()
				if entry.IsDir() {
					name += "/"
				}
				fmt.Fprintf(&listing, "  %s\n", name)
			}
			return toolError(ErrorClassRecoverable, ErrKindNotDir, listing.String(), nil), nil
		}
		return toolError(ErrorClassRecoverable, ErrKindNotDir,
			fmt.Sprintf("path is a directory, not a file: %s", path), nil), nil
	}

	// ── Step 4: 大文件门限 ──
	// 在读取任何内容之前先检查文件大小。read 需要完整文件内容
	// 存入 read state store,超大文件(>10MB)可能导致 OOM。
	const maxReadBytes = 10 * 1024 * 1024 // 10MB
	if info.Size() > maxReadBytes {
		s := fmt.Sprintf("%.1fMB", float64(info.Size())/(1024*1024))
		return toolError(ErrorClassRecoverable, ErrKindLargeFile,
			fmt.Sprintf("file too large for read (%s > 10MB): %s. Use shell tools to both read (head/tail/grep) and edit (sed/awk) large files.", s, path), nil), nil
	}

	// ── Step 5: 二进制检测 ──
	if HasBinaryExtension(path) {
		return toolError(ErrorClassRecoverable, ErrKindBinaryFile,
			fmt.Sprintf("file appears to be a binary %s file: %s",
				strings.ToLower(strings.TrimPrefix(fileExtension(path), ".")), path), nil), nil
	}

	isBinary, err := IsBinaryByContent(path)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("cannot check file type: %v", err), err), nil
	}
	if isBinary {
		return toolError(ErrorClassRecoverable, ErrKindBinaryFile,
			fmt.Sprintf("file appears to be binary: %s", path), nil), nil
	}

	// ── Step 5.5: Outline mode — fetch symbol index via LSP or regex ──
	if p.Outline {
		return t.executeOutline(ctx, path)
	}

	// ── Step 6: 读取文件内容 ──

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	fullContent, _, totalLines, err := readFullFile(ctx, path)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("failed to read file: %v", err), err), nil
	}

	// ── Step 6: 记录读取状态供 edit 冲突检测 + 重复 read 提示 ──
	// 大文件(>50KB)内容与上次 read 相同时提示定向读取——历史扫描发现
	// 34 次"5 事件内重读同文件"的浪费模式。小文件重复 read 成本低不提示,
	// 避免噪音(99% 缓存命中下 read 本身廉价)。
	// 已使用定向参数(pattern/limit)的读取不提示,避免自相矛盾的噪音。
	var repeatHint string
	if rs := ReadStateFromContext(ctx); rs != nil {
		if prev := rs.Get(path); prev != nil && prev.Content == fullContent &&
			info.Size() > repeatedReadHintBytes && p.Pattern == "" && p.Limit == 0 {
			repeatHint = "<system-reminder>File unchanged since your last read. If you need a specific section, use pattern or offset/limit instead of re-reading the whole file.</system-reminder>\n\n"
		}
		rs.Record(path, fullContent)
	}

	// ── Step 7: pattern 匹配(可选)──
	// pattern 选择显示窗口:匹配行 ±ContextLines。
	var matchFooter string
	if p.Pattern != "" && totalLines > 0 {
		lines := strings.Split(fullContent, "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}

		var matches []int
		for i, line := range lines {
			if strings.Contains(line, p.Pattern) {
				matches = append(matches, i) // 0-based
			}
		}
		// ContextLines 为 0 时使用默认值 5(Go int 零值无法区分"未传"和"传 0")。
		// 想要仅显示匹配行本身:传入 limit=1。
		ctxLines := p.ContextLines
		if ctxLines <= 0 {
			ctxLines = 5
		}
		if ctxLines > 50 {
			ctxLines = 50
		}

		if len(matches) == 0 {
			matchFooter = fmt.Sprintf("\n<system-reminder>Pattern %q not found in file. Check pattern spelling. Showing full file.</system-reminder>", p.Pattern)
		} else {
			// offset 复用为匹配索引(0-based),limit 约束显示行数
			requestedIdx := p.Offset // 保存原始值,用于后续钳位提示
			matchIdx := requestedIdx
			if matchIdx < 0 {
				matchIdx = 0
			}
			if matchIdx >= len(matches) {
				matchIdx = len(matches) - 1
			}

			matchLine := matches[matchIdx]
			start := matchLine - ctxLines
			if start < 0 {
				start = 0
			}
			end := matchLine + ctxLines + 1
			if end > totalLines {
				end = totalLines
			}
			if p.Limit > 0 && start+p.Limit < end {
				end = start + p.Limit
			}

			p.Offset = start
			p.Limit = end - start

			matchFooter = fmt.Sprintf("\n<system-reminder>Match %d of %d for %q at line %d (%d lines shown).",
				matchIdx+1, len(matches), p.Pattern, matchLine+1, p.Limit)
			if requestedIdx > 0 && requestedIdx >= len(matches) {
				matchFooter += fmt.Sprintf(" Requested match %d, clamped to %d.", requestedIdx+1, matchIdx+1)
			}
			if len(matches) > 1 {
				matchFooter += " Use offset=N to page through matches."
			}
			matchFooter += "</system-reminder>"
		}
	}

	// ── Step 7.5: 大文件软提示 ──
	// 全量读取大文件会膨胀上下文(历史扫描:250KB 文件被全量 read 多次)。
	// 文件 >100KB 且未使用定向参数时,输出头部提示 pattern/offset。
	// 不阻断:edit 场景仍需完整 read state 供冲突检测与 hunk 构造。
	var sizeHint string
	if info.Size() > largeReadHintBytes && p.Pattern == "" && p.Limit == 0 && !p.Outline {
		sizeHint = fmt.Sprintf("<system-reminder>File is large (%s). If you only need part of it, use pattern or offset/limit for targeted reads.</system-reminder>\n\n",
			formatSize(info.Size()))
	}

	// ── Step 8: 格式化输出 ──
	content := sizeHint + repeatHint + formatReadOutput(path, fullContent, p.Offset, p.Limit)
	content += "\n<system-reminder>Line numbers are 1-based current positions. Empty lines appear as `N:` (no content after colon). Trailing whitespace is preserved in read output — the edit engine matches with progressive tolerance (exact → trailing whitespace ignored → leading+trailing ignored → unicode normalize), so minor whitespace drift won't cause failures. When constructing edit hunks, use `@@` headers with `-` for removal, `+` for addition, and space prefix for context lines. Brace-only lines like `}` are real content with line numbers — include them when they belong inside a hunk.</system-reminder>"
	if totalLines > 0 {
		displayedLines := totalLines
		if p.Limit > 0 && p.Offset+p.Limit < totalLines {
			displayedLines = p.Limit
		}
		if p.Offset > 0 || displayedLines < totalLines {
			lines := splitLines(fullContent)
			content += "\n" + formatFileIndex(lines)
		}
	}
	if matchFooter != "" {
		content += matchFooter
	}

	if totalLines == 0 {
		content = fmt.Sprintf("[%s]\n<system-reminder>Warning: the file exists but the contents are empty.</system-reminder>", path)
	}

	lineCount := totalLines
	if p.Limit > 0 && p.Limit < totalLines {
		lineCount = p.Limit
	}
	if p.Offset > 0 && p.Offset < totalLines {
		remaining := totalLines - p.Offset
		if p.Limit == 0 || p.Limit > remaining {
			lineCount = remaining
		}
	}

	return &ToolResult{
		Content: content,
		Meta: ToolMeta{
			FilePath:  path,
			LineCount: lineCount,
			ByteCount: len(content),
		},
	}, nil
}

// readFullFile 读取完整文件内容,不做截断。
// 返回:文件文本、实际行数、总行数、错误。
func readFullFile(ctx context.Context, path string) (content string, lineCount int, totalLines int, err error) {
	raw, err := readFileWithContext(ctx, path)
	if err != nil {
		return "", 0, 0, err
	}

	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\r")
	lines := splitLines(text)
	totalLines = len(lines)

	return text, totalLines, totalLines, nil
}

// formatReadOutput 将文件内容格式化为 [path] 头 + N:CONTENT 行。
func formatReadOutput(path string, content string, offset, limit int) string {
	lines := strings.Split(content, "\n")
	// 去除末尾空行(由 trailing newline 产生)
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	totalLines := len(lines)

	// 空文件
	if totalLines == 0 {
		return fmt.Sprintf("[%s]\n", path)
	}

	// 选择可见行
	start := offset
	if start < 0 {
		start = 0
	}
	end := totalLines
	if limit > 0 {
		end = start + limit
	}
	if end > totalLines {
		end = totalLines
	}
	if start >= totalLines {
		return fmt.Sprintf("[%s]\n<system-reminder>Warning: the file exists but is shorter than the provided offset (%d). The file has %d lines.</system-reminder>",
			path, offset, totalLines)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[%s]\n", path)

	for i := start; i < end; i++ {
		if lines[i] == "" {
			fmt.Fprintf(&b, "%d:\n", i+1)
		} else {
			fmt.Fprintf(&b, "%d:%s\n", i+1, lines[i])
		}
	}

	// 截断提示
	if end < totalLines {
		omitted := totalLines - end
		fmt.Fprintf(&b, "... [truncated: %d lines omitted]", omitted)
	}

	return b.String()
}

// outline regex patterns — compiled once at package init, reused across calls.
var (
	outlineGoPatterns = []symbolPattern{
		{re: regexp.MustCompile(`^func\s+(?:\([^)]*\)\s+)?([^\s(]+)`), kind: "function"},
		{re: regexp.MustCompile(`^type\s+(\S+)\s+struct`), kind: "struct"},
		{re: regexp.MustCompile(`^type\s+(\S+)\s+interface`), kind: "interface"},
		{re: regexp.MustCompile(`^type\s+(\S+)`), kind: "type"},
	}
	outlineRustPatterns = []symbolPattern{
		{re: regexp.MustCompile(`^pub\s+fn\s+([^\s(]+)`), kind: "function"},
		{re: regexp.MustCompile(`^fn\s+([^\s(]+)`), kind: "function"},
		{re: regexp.MustCompile(`^pub\s+struct\s+(\S+)`), kind: "struct"},
		{re: regexp.MustCompile(`^struct\s+(\S+)`), kind: "struct"},
		{re: regexp.MustCompile(`^pub\s+enum\s+(\S+)`), kind: "enum"},
		{re: regexp.MustCompile(`^enum\s+(\S+)`), kind: "enum"},
		{re: regexp.MustCompile(`^pub\s+trait\s+(\S+)`), kind: "interface"},
		{re: regexp.MustCompile(`^trait\s+(\S+)`), kind: "interface"},
		{re: regexp.MustCompile(`^pub\s+type\s+(\S+)`), kind: "type"},
		{re: regexp.MustCompile(`^type\s+(\S+)`), kind: "type"},
	}
	outlineTSPatterns = []symbolPattern{
		{re: regexp.MustCompile(`^(?:export\s+)?(?:async\s+)?function\s+([^\s(]+)`), kind: "function"},
		{re: regexp.MustCompile(`^(?:export\s+)?class\s+(\S+)`), kind: "class"},
		{re: regexp.MustCompile(`^(?:export\s+)?(?:const|let|var)\s+(\S+)`), kind: "variable"},
		{re: regexp.MustCompile(`^(?:export\s+)?interface\s+(\S+)`), kind: "interface"},
		{re: regexp.MustCompile(`^(?:export\s+)?type\s+(\S+)`), kind: "type"},
	}
	outlinePyPatterns = []symbolPattern{
		{re: regexp.MustCompile(`^def\s+([^\s(]+)`), kind: "function"},
		{re: regexp.MustCompile(`^class\s+(\S+)`), kind: "class"},
	}
	outlineCPatterns = []symbolPattern{
		{re: regexp.MustCompile(`^\w[\w:*&<>\s]+\s+(?:(\w+)::)?(\w+)\s*\(`), kind: "function"},
		{re: regexp.MustCompile(`^(?:struct|class|enum)\s+(\w+)`), kind: "type"},
	}
	outlineGenericPatterns = []symbolPattern{
		{re: regexp.MustCompile(`^(?:func|fn|def)\s+([^\s(]+)`), kind: "function"},
		{re: regexp.MustCompile(`^(?:class|struct|enum|interface|type|module|trait)\s+(\S+)`), kind: "type"},
	}
	outlineShellPatterns = []symbolPattern{
		{re: regexp.MustCompile(`^(?:function\s+)?(\w+)\s*(?:\(\s*\)|\{)`), kind: "function"},
	}
	outlineMakefilePatterns = []symbolPattern{
		{re: regexp.MustCompile(`^\.PHONY:\s*(.*)`), kind: "phony"},
		{re: regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_./-]*)\s*:\s*(?:.*;.*)?`), kind: "target"},
	}
	outlineMarkdownPatterns = []symbolPattern{
		{re: regexp.MustCompile(`^(#{1,6})\s+(.+)`), kind: "heading"},
	}
	outlineRubyPatterns = []symbolPattern{
		{re: regexp.MustCompile(`^def\s+([^\s(]+)`), kind: "function"},
		{re: regexp.MustCompile(`^class\s+(\S+)`), kind: "class"},
		{re: regexp.MustCompile(`^module\s+(\S+)`), kind: "module"},
	}
)

// outlineSymbol is a single symbol entry for the outline output.
type outlineSymbol struct {
	name string
	kind string
	line uint32 // 1-based
}

// executeOutline returns a symbol outline for the given file path.
// Uses LSP textDocument/documentSymbol when a language server is available,
// falls back to regex-based symbol extraction.
func (t *ReadFile) executeOutline(ctx context.Context, path string) (*ToolResult, error) {
	// Try LSP first
	if symbols := lspOutline(ctx, path); len(symbols) > 0 {
		return &ToolResult{
			Content: formatOutlineOutput(path, symbols),
			Meta:    ToolMeta{FilePath: path},
		}, nil
	}

	// Fall back to regex
	symbols, err := regexOutline(path)
	if err != nil {
		return &ToolResult{
			Content: fmt.Sprintf("Failed to read %s: %v", path, err),
			Meta:    ToolMeta{FilePath: path},
		}, nil
	}
	if len(symbols) == 0 {
		return &ToolResult{
			Content: fmt.Sprintf("No symbols found in %s (not a recognized code file or empty).", path),
			Meta:    ToolMeta{FilePath: path},
		}, nil
	}
	return &ToolResult{
		Content: formatOutlineOutput(path, symbols),
		Meta:    ToolMeta{FilePath: path},
	}, nil
}

// lspOutline fetches symbols via LSP textDocument/documentSymbol.
func lspOutline(ctx context.Context, path string) []outlineSymbol {
	mgr := lsp.LSPManagerFromContext(ctx)
	if mgr == nil {
		return nil
	}

	docSymbols := mgr.DocumentSymbols(ctx, path)
	if len(docSymbols) == 0 {
		return nil
	}

	return flattenSymbols(docSymbols)
}

// flattenSymbols recursively flattens a DocumentSymbol tree into a flat list.
func flattenSymbols(symbols []lsp.DocumentSymbol) []outlineSymbol {
	var result []outlineSymbol
	for _, s := range symbols {
		result = append(result, outlineSymbol{
			name: s.Name,
			kind: lsp.SymbolKindLabel(s.Kind),
			line: s.SelectionRange.Start.Line + 1, // LSP 0-based → 1-based
		})
		if len(s.Children) > 0 {
			result = append(result, flattenSymbols(s.Children)...)
		}
	}
	return result
}

// regexOutline extracts symbols using regex patterns based on file extension.
// Returns an error for I/O failures; returns empty slice for unrecognized or empty files.
func regexOutline(path string) ([]outlineSymbol, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	ext := filepath.Ext(path)
	var patterns []symbolPattern
	switch {
	case ext == ".go":
		patterns = outlineGoPatterns
	case ext == ".rs":
		patterns = outlineRustPatterns
	case ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx":
		patterns = outlineTSPatterns
	case ext == ".py":
		patterns = outlinePyPatterns
	case ext == ".rb":
		patterns = outlineRubyPatterns
	case ext == ".sh" || ext == ".bash" || ext == ".zsh":
		patterns = outlineShellPatterns
	case ext == ".md" || ext == ".markdown":
		patterns = outlineMarkdownPatterns
	case ext == ".yml" || ext == ".yaml" || ext == ".json":
		return nil, nil // data files — skip scanning
	case ext == ".c" || ext == ".cpp" || ext == ".cc" || ext == ".cxx" || ext == ".h" || ext == ".hpp":
		patterns = outlineCPatterns
	case ext == "" && isMakefileName(filepath.Base(path)):
		patterns = outlineMakefilePatterns
	default:
		patterns = outlineGenericPatterns
	}

	lines := strings.Split(string(raw), "\n")
	var symbols []outlineSymbol
	for lineIdx, line := range lines {
		trimmed := strings.TrimSpace(line)
		for _, p := range patterns {
			m := p.re.FindStringSubmatch(trimmed)
			if m == nil {
				continue
			}
			name := m[len(m)-1] // last capture group is the symbol name
			symbols = append(symbols, outlineSymbol{
				name: name,
				kind: p.kind,
				line: uint32(lineIdx + 1), // 1-based
			})
			break // only match first pattern per line
		}
	}
	return symbols, nil
}

type symbolPattern struct {
	re   *regexp.Regexp
	kind string
}

// isMakefileName checks whether a base file name is a Makefile variant.
func isMakefileName(name string) bool {
	return name == "Makefile" || name == "makefile" || name == "GNUmakefile"
}
// formatOutlineOutput formats the symbol list for display.
func formatOutlineOutput(path string, symbols []outlineSymbol) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d Symbols in %s:\n", len(symbols), path)
	for _, s := range symbols {
		fmt.Fprintf(&b, "  %-24s %-12s L%d\n", s.name, s.kind, s.line)
	}
	return b.String()
}
