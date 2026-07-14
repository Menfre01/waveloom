package hashline

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Menfre01/waveloom/pkg/pathutil"
)

// ---------------------------------------------------------------------------
// Patch types
// ---------------------------------------------------------------------------

// OpKind 表示操作类型。
type OpKind int

const (
	OpSWAP OpKind = iota
	OpDEL
	OpINS
	OpREM
	OpMV
)

func (k OpKind) String() string {
	switch k {
	case OpSWAP:
		return "SWAP"
	case OpDEL:
		return "DEL"
	case OpINS:
		return "INS"
	case OpREM:
		return "REM"
	case OpMV:
		return "MV"
	default:
		return "UNKNOWN"
	}
}

// Op 表示单个编辑操作。
type Op struct {
	Kind      OpKind
	LineStart int    // 起始行号（1-based，DEL 和 SWAP 必需）
	LineEnd   int    // 结束行号（SWAP 必需，含；DEL 可选，缺省 = LineStart）
	Position  string // INS 的插入位置："head" / "tail" / "pre" / "post"
	RefLine   int    // INS pre/post 的参考行号
	Body      []string // SWAP/INS 的新内容（已去除 + 前缀的 body 行，nil 表示无 body）
	DestPath  string // MV 的目标路径
}

// Section 表示 patch 中一个文件的编辑指令。
type Section struct {
	Path string
	TAG  string
	Ops  []Op
}

// Patch 表示一个完整的 patch 文档。
type Patch struct {
	Sections []Section
}

// ---------------------------------------------------------------------------
// Result types (hashline-local, converted to tool types in edit_file_hashline)
// ---------------------------------------------------------------------------

// EditLineKind 表示 diff 行的类型（hashline 内部表示）。
type EditLineKind string

const (
	LineAdd    EditLineKind = "+"
	LineDel    EditLineKind = "-"
	LineCtx    EditLineKind = " "
	LineHeader EditLineKind = "@"
)

// EditLine 表示 diff 中的一行。
type EditLine struct {
	Kind    EditLineKind
	Content string
	OldNum  int
	NewNum  int
}

// EditHunk 表示一个 diff 块。
type EditHunk struct {
	OldStart       int
	OldCount       int
	NewStart       int
	NewCount       int
	Heading        string
	Lines          []EditLine
	NoNewlineAtEOF bool
}

// SectionResult 表示单个 Section 的应用结果。
type SectionResult struct {
	Path       string
	Op         string // "update" / "delete" / "create" / "rename"
	OldTAG     string
	NewTAG     string
	LinesDelta int
	DiffHunks  []EditHunk
	Warning    string
	Error      *EditError
}

// EditError 表示编辑过程中的错误。
type EditError struct {
	Fatal    bool   // true = 不可恢复，false = 可恢复
	Kind     string // "file_not_found", "tag_mismatch", "invalid_args", "permission_denied" ...
	Message  string
}

func (e *EditError) Error() string { return e.Message }

// ---------------------------------------------------------------------------
// Parse errors
// ---------------------------------------------------------------------------

// ParseError 表示 patch 解析错误。
type ParseError struct {
	Line int    // 出错行号（1-based，0 = 未知）
	Msg  string
}

func (e *ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("parse error at line %d: %s", e.Line, e.Msg)
	}
	return fmt.Sprintf("parse error: %s", e.Msg)
}

// ---------------------------------------------------------------------------
// ParsePatch 解析 hashline patch 文本。
// ---------------------------------------------------------------------------

// ParsePatch 解析 hashline format patch 文本，返回 Patch 结构。
func ParsePatch(text string) (*Patch, error) {
	lines := strings.Split(text, "\n")
	scanner := &patchScanner{lines: lines, pos: 0}

	// 跳过开头的 *** Begin Patch
	if err := scanner.expectMarker("*** Begin Patch"); err != nil {
		return nil, err
	}

	var sections []Section
	for scanner.pos < len(lines) {
		line := scanner.trimmed()
		if line == "" {
			scanner.pos++
			continue
		}

		// 检查结束标记
		if strings.EqualFold(line, "*** End Patch") {
			scanner.pos++
			break
		}

		// 期望文件头 [PATH#TAG]
		section, err := scanner.parseSection()
		if err != nil {
			return nil, err
		}
		sections = append(sections, section)
	}

	if len(sections) == 0 {
		return nil, &ParseError{Msg: "no sections found in patch"}
	}

	return &Patch{Sections: sections}, nil
}

// patchScanner 是 patch 文本的行扫描器。
type patchScanner struct {
	lines []string
	pos   int
}

func (s *patchScanner) currentLine() int {
	return s.pos + 1
}

func (s *patchScanner) trimmed() string {
	for s.pos < len(s.lines) {
		line := strings.TrimSpace(s.lines[s.pos])
		if line != "" {
			return line
		}
		s.pos++
	}
	return ""
}

func (s *patchScanner) rawLine() string {
	if s.pos >= len(s.lines) {
		return ""
	}
	return s.lines[s.pos]
}

func (s *patchScanner) expectMarker(marker string) error {
	line := s.trimmed()
	if !strings.EqualFold(line, marker) {
		return &ParseError{
			Line: s.currentLine(),
			Msg:  fmt.Sprintf("expected %q, got %q", marker, line),
		}
	}
	s.pos++
	return nil
}

// parseSection 解析一个 [PATH#TAG] 块及其后续操作。
func (s *patchScanner) parseSection() (Section, error) {
	line := s.trimmed()
	lineNum := s.currentLine()

	if !strings.HasPrefix(line, "[") || !strings.Contains(line, "#") {
		return Section{}, &ParseError{
			Line: lineNum,
			Msg:  fmt.Sprintf("expected [PATH#TAG], got %q", line),
		}
	}

	idxEnd := strings.IndexByte(line, ']')
	if idxEnd < 0 {
		return Section{}, &ParseError{
			Line: lineNum,
			Msg:  fmt.Sprintf("unclosed section header: %q", line),
		}
	}

	header := line[1:idxEnd]
	hashIdx := strings.LastIndex(header, "#")
	if hashIdx < 0 || hashIdx == len(header)-1 {
		return Section{}, &ParseError{
			Line: lineNum,
			Msg:  fmt.Sprintf("invalid section header (missing TAG): %q", line),
		}
	}

	path := header[:hashIdx]
	tag := header[hashIdx+1:]
	if tag == "" || len(tag) != 4 {
		return Section{}, &ParseError{
			Line: lineNum,
			Msg:  fmt.Sprintf("invalid TAG: %q (must be 4 hex chars)", tag),
		}
	}

	s.pos++ // consume header

	// 解析操作
	var ops []Op
	for s.pos < len(s.lines) {
		trimmed := strings.TrimSpace(s.rawLine())

		if trimmed == "" {
			s.pos++
			continue
		}

		// 下一个 section 或结束标记 → 停止
		if strings.HasPrefix(trimmed, "[") || strings.EqualFold(trimmed, "*** End Patch") {
			break
		}

		// 检查操作头
		op, err := s.parseOp()
		if err != nil {
			return Section{}, err
		}
		ops = append(ops, op)
	}

	if len(ops) == 0 {
		return Section{}, &ParseError{
			Line: lineNum,
			Msg:  fmt.Sprintf("no operations in section [%s#%s]", path, tag),
		}
	}

	return Section{Path: path, TAG: tag, Ops: ops}, nil
}

// normalizeOpLine 对操作行做 LLM 兼容性规范化：
// - 剥离行尾注释（# ... 和 // ...）
// - 修复 INS. PRE / INS. POST / INS. HEAD / INS. TAIL（dot 后多余空格）
func normalizeOpLine(line string) string {
	// 剥离行尾注释（空格 + # 或 // 开始的后缀）
	line = stripTrailingComment(line)

	// 修复 dot 后多余空格：INS. PRE → INS.PRE 等
	line = strings.Replace(line, "INS. PRE", "INS.PRE", 1)
	line = strings.Replace(line, "INS. POST", "INS.POST", 1)
	line = strings.Replace(line, "INS. HEAD", "INS.HEAD", 1)
	line = strings.Replace(line, "INS. TAIL", "INS.TAIL", 1)

	return line
}

// stripTrailingComment 剥离操作行行尾注释。模式：空格 + # 或 //。
func stripTrailingComment(line string) string {
	if idx := strings.Index(line, " //"); idx >= 0 {
		return strings.TrimSpace(line[:idx])
	}
	if idx := strings.Index(line, " #"); idx > 0 {
		return strings.TrimSpace(line[:idx])
	}
	return line
}

// parseOp 解析一个操作及可选的 body 行。
// 对操作行做 LLM 兼容性规范化后再解析。
func (s *patchScanner) parseOp() (Op, error) {
	trimmed := strings.TrimSpace(s.rawLine())
	normalized := normalizeOpLine(trimmed)
	lineNum := s.currentLine()

	upper := strings.ToUpper(normalized)

	switch {
	case strings.HasPrefix(upper, "SWAP "):
		return s.parseSwapOp(normalized, lineNum)
	case strings.HasPrefix(upper, "DEL "):
		return s.parseDelOp(normalized, lineNum)
	case strings.HasPrefix(upper, "INS.PRE "):
		return s.parseInsOp(normalized, "pre", lineNum)
	case strings.HasPrefix(upper, "INS.POST "):
		return s.parseInsOp(normalized, "post", lineNum)
	case strings.HasPrefix(upper, "INS.HEAD"):
		return s.parseInsHeadTailOp(normalized, "head", lineNum)
	case strings.HasPrefix(upper, "INS.TAIL"):
		return s.parseInsHeadTailOp(normalized, "tail", lineNum)
	case strings.HasPrefix(upper, "REM"):
		s.pos++ // consume REM line
		return Op{Kind: OpREM}, nil
	case strings.HasPrefix(upper, "MV "):
		return s.parseMvOp(normalized, lineNum)
	default:
		return Op{}, &ParseError{
			Line: lineNum,
			Msg:  fmt.Sprintf("unknown operation: %q", trimmed),
		}
	}
}

func (s *patchScanner) parseSwapOp(line string, lineNum int) (Op, error) {
	rest := line[5:] // after "SWAP "
	hasBody := strings.HasSuffix(strings.TrimSpace(rest), ":")

	rest = strings.TrimSuffix(strings.TrimSpace(rest), ":")
	rest = strings.TrimSpace(rest)

	start, end, err := parseLineRange(rest)
	if err != nil {
		return Op{}, &ParseError{Line: lineNum, Msg: fmt.Sprintf("invalid SWAP range: %v", err)}
	}

	s.pos++ // consume op header

	var body []string
	if hasBody {
		body = s.readBody()
	}

	return Op{Kind: OpSWAP, LineStart: start, LineEnd: end, Body: body}, nil
}

func (s *patchScanner) parseDelOp(line string, lineNum int) (Op, error) {
	rest := line[4:] // after "DEL "
	rest = strings.TrimSuffix(strings.TrimSpace(rest), ":")
	rest = strings.TrimSpace(rest)

	start, end, err := parseLineRange(rest)
	if err != nil {
		return Op{}, &ParseError{Line: lineNum, Msg: fmt.Sprintf("invalid DEL range: %v", err)}
	}

	s.pos++ // consume op header
	return Op{Kind: OpDEL, LineStart: start, LineEnd: end}, nil
}

func (s *patchScanner) parseInsOp(line string, position string, lineNum int) (Op, error) {
	prefix := "INS." + strings.ToUpper(position) + " "
	rest := line[len(prefix):]
	rest = strings.TrimSuffix(strings.TrimSpace(rest), ":")
	rest = strings.TrimSpace(rest)

	n, err := parseSingleLine(rest)
	if err != nil {
		return Op{}, &ParseError{Line: lineNum, Msg: fmt.Sprintf("invalid INS.%s line number: %v", position, err)}
	}

	s.pos++ // consume op header
	body := s.readBody()

	return Op{Kind: OpINS, Position: position, RefLine: n, Body: body}, nil
}

func (s *patchScanner) parseInsHeadTailOp(line string, position string, lineNum int) (Op, error) {
	s.pos++ // consume op header
	body := s.readBody()
	return Op{Kind: OpINS, Position: position, Body: body}, nil
}

func (s *patchScanner) parseMvOp(line string, lineNum int) (Op, error) {
	rest := strings.TrimSpace(line[3:])
	if rest == "" {
		return Op{}, &ParseError{Line: lineNum, Msg: "MV requires a destination path"}
	}
	// 剥离双引号或单引号（LLM 可能使用任一种）
	if (strings.HasPrefix(rest, `"`) && strings.HasSuffix(rest, `"`)) ||
		(strings.HasPrefix(rest, "'") && strings.HasSuffix(rest, "'")) {
		rest = rest[1 : len(rest)-1]
	}
	s.pos++
	return Op{Kind: OpMV, DestPath: rest}, nil
}

// readBody 读取以 + 开头的 body 行，返回去除前缀后的行列表（nil 表示无 body 行）。
// \+ 开头的行会被转义：去掉反斜杠，保留后面的 + 作为字面量内容。
// 容忍 LLM 在 + 前加空白字符，跳过 body 内部的空行。
func (s *patchScanner) readBody() []string {
	var bodyLines []string
	for s.pos < len(s.lines) {
		raw := s.rawLine()
		trimmed := strings.TrimLeft(raw, " \t")

		// 跳过空行（LLM 可能在 body 行之间插入空行）
		if raw == "" || trimmed == "" {
			s.pos++
			continue
		}

		if strings.HasPrefix(trimmed, `\+`) {
			// 转义：\+ 开头 → 字面量 + 开头的内容
			content := trimmed[1:] // 去掉 \，保留 + 及后续内容
			bodyLines = append(bodyLines, content)
			s.pos++
		} else if strings.HasPrefix(trimmed, "+") {
			content := trimmed[1:]
			bodyLines = append(bodyLines, content)
			s.pos++
		} else {
			break
		}
	}
	return bodyLines
}

// parseLineRange 解析 "N.=M" 或 "N" 行号格式。
func parseLineRange(s string) (start, end int, err error) {
	s = strings.TrimSpace(s)

	if idx := strings.Index(s, "."); idx >= 0 {
		after := s[idx+1:]
		if strings.HasPrefix(after, "=") {
			startStr := strings.TrimSpace(s[:idx])
			endStr := strings.TrimSpace(after[1:])
			start, err = parseSingleLine(startStr)
			if err != nil {
				return 0, 0, err
			}
			end, err = parseSingleLine(endStr)
			if err != nil {
				return 0, 0, err
			}
			if end < start {
				return 0, 0, fmt.Errorf("end line %d < start line %d", end, start)
			}
			return start, end, nil
		}
	}

	// Friendly hint: detect := confusion (用户写了 N:=M 而非 N.=M)
	if idx := strings.Index(s, ":="); idx >= 0 {
		left := strings.TrimSpace(s[:idx])
		right := strings.TrimSuffix(strings.TrimSpace(s[idx+2:]), ":")
		return 0, 0, fmt.Errorf("invalid range %q: did you mean %s.=%s? (SWAP/DEL ranges use N.=M format, not N:=M)", s, left, right)
	}

	start, err = parseSingleLine(s)
	if err != nil {
		return 0, 0, err
	}
	return start, start, nil
}

func parseSingleLine(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty line number")
	}
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("invalid line number: %q", s)
		}
		n = n*10 + int(ch-'0')
	}
	if n < 1 {
		return 0, fmt.Errorf("line number must be >= 1: %q", s)
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// FileSystem — 抽象文件读写
// ---------------------------------------------------------------------------

// FileSystem 抽象文件读写，方便测试。
type FileSystem interface {
	ReadFile(path string) (string, error)
	WriteFile(path string, content string) error
	MkdirAll(path string) error
	Remove(path string) error
	ResolvePath(path string) string
}

// OSFS 是真实文件系统的 FileSystem 实现。
type OSFS struct {
	WorkingDir string
}

func (fs *OSFS) ReadFile(path string) (string, error) {
	fullPath, err := pathutil.ResolvePathWithDir(path, fs.WorkingDir)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (fs *OSFS) WriteFile(path string, content string) error {
	fullPath, err := pathutil.ResolvePathWithDir(path, fs.WorkingDir)
	if err != nil {
		return err
	}
	return os.WriteFile(fullPath, []byte(content), 0o644)
}

func (fs *OSFS) MkdirAll(path string) error {
	fullPath, err := pathutil.ResolvePathWithDir(path, fs.WorkingDir)
	if err != nil {
		return err
	}
	return os.MkdirAll(fullPath, 0o755)
}

func (fs *OSFS) Remove(path string) error {
	fullPath, err := pathutil.ResolvePathWithDir(path, fs.WorkingDir)
	if err != nil {
		return err
	}
	return os.Remove(fullPath)
}

func (fs *OSFS) ResolvePath(path string) string {
	fullPath, err := pathutil.ResolvePathWithDir(path, fs.WorkingDir)
	if err != nil {
		return path
	}
	return fullPath
}

// ---------------------------------------------------------------------------
// ApplyPatch 应用 patch 到文件系统。
// ---------------------------------------------------------------------------

// ApplyPatch 解析后的 patch 应用到文件系统。
// store 可为 nil（无 TAG 验证时跳过 Verify，但仍可执行编辑）。
func ApplyPatch(patch *Patch, fs FileSystem, store *SnapshotStore) []SectionResult {
	var results []SectionResult

	for _, sec := range patch.Sections {
		result := applySection(sec, fs, store)
		results = append(results, result)
	}

	return results
}

func applySection(sec Section, fs FileSystem, store *SnapshotStore) SectionResult {
	// storePath 是解析后的绝对路径，用于 store 操作（Verify / Get / Update）。
	// fs.ReadFile 内部自行解析路径，但 store 的 key 必须是绝对路径才能
	// 与 ReadFileHashline 中 Record 的路径对齐（Record 也会解析为绝对路径）。
	storePath := fs.ResolvePath(sec.Path)

	result := SectionResult{
		Path:   sec.Path,
		OldTAG: sec.TAG,
	}

	currentContent, err := fs.ReadFile(sec.Path)
	if err != nil {
		if os.IsNotExist(err) {
			if len(sec.Ops) == 1 && sec.Ops[0].Kind == OpREM {
				result.Op = "delete"
				result.NewTAG = sec.TAG
				return result
			}
			result.Error = &EditError{
				Fatal:   false,
				Kind:    "file_not_found",
				Message: fmt.Sprintf("file not found: %s (use write_file to create it first)", sec.Path),
			}
			return result
		}
		result.Error = &EditError{
			Fatal:   true,
			Kind:    "permission_denied",
			Message: fmt.Sprintf("cannot read file: %s: %v", sec.Path, err),
		}
		return result
	}

	if store != nil {
		_, verifyErr := store.Verify(storePath, sec.TAG, currentContent)
		if verifyErr != nil {
			// TAG 不匹配 → 尝试 Recovery
			if snap, ok := store.Get(storePath); ok {
				recovery := RecoverOps(snap.Content, currentContent, sec.Ops)
				if recovery.Success {
					// Recovery 成功：使用重映射后的操作
					result.Warning = fmt.Sprintf("TAG expired, auto-recovered: %v", verifyErr)
					sec.Ops = recovery.MappedOps
				} else {
					// Recovery 失败：返回错误
					result.Error = &EditError{
						Fatal:   false,
						Kind:    "tag_mismatch",
						Message: fmt.Sprintf("TAG mismatch for %q and recovery failed: the file has been modified since last read. Re-read the file with read_file_hashline and retry. (%v)", sec.Path, verifyErr),
					}
					return result
				}
			} else {
				result.Error = &EditError{
					Fatal:   false,
					Kind:    "tag_mismatch",
					Message: fmt.Sprintf("TAG mismatch for %q: the file has been modified since last read. Re-read the file with read_file_hashline and retry. (%v)", sec.Path, verifyErr),
				}
				return result
			}
		}
	}

	for _, op := range sec.Ops {
		if op.Kind == OpMV {
			return applyMV(sec, op, fs, store, currentContent)
		}
	}

	for _, op := range sec.Ops {
		if op.Kind == OpREM {
			return applyREM(sec, fs, store)
		}
	}

	sortedOps := sortOps(sec.Ops)

	newContent, hunks, err := applyEdits(currentContent, sortedOps)
	if err != nil {
		result.Error = &EditError{
			Fatal:   false,
			Kind:    "invalid_args",
			Message: err.Error(),
		}
		return result
	}

	if err := fs.WriteFile(sec.Path, newContent); err != nil {
		result.Error = &EditError{
			Fatal:   true,
			Kind:    "permission_denied",
			Message: fmt.Sprintf("cannot write file: %s: %v", sec.Path, err),
		}
		return result
	}

	newTAG := sec.TAG
	if store != nil {
		newTAG = store.Update(storePath, newContent)
	}

	oldLines := countLines(currentContent)
	newLines := countLines(newContent)

	result.Op = "update"
	result.NewTAG = newTAG
	result.LinesDelta = newLines - oldLines
	result.DiffHunks = hunks

	return result
}

func applyMV(sec Section, op Op, fs FileSystem, store *SnapshotStore, currentContent string) SectionResult {
	result := SectionResult{
		Path:   sec.Path,
		OldTAG: sec.TAG,
		Op:     "rename",
	}

	destPath := op.DestPath

	destDir := filepath.Dir(destPath)
	if err := fs.MkdirAll(destDir); err != nil {
		result.Error = &EditError{
			Fatal:   true,
			Kind:    "permission_denied",
			Message: fmt.Sprintf("cannot create directory for rename: %v", err),
		}
		return result
	}

	if err := fs.WriteFile(destPath, currentContent); err != nil {
		result.Error = &EditError{
			Fatal:   true,
			Kind:    "permission_denied",
			Message: fmt.Sprintf("cannot write destination file: %v", err),
		}
		return result
	}

	if err := fs.Remove(sec.Path); err != nil {
		result.Error = &EditError{
			Fatal:   true,
			Kind:    "permission_denied",
			Message: fmt.Sprintf("cannot remove source file: %v", err),
		}
		return result
	}

	newTAG := sec.TAG
	if store != nil {
		newTAG = store.Update(fs.ResolvePath(destPath), currentContent)
	}

	result.NewTAG = newTAG
	result.Path = destPath
	return result
}

func applyREM(sec Section, fs FileSystem, store *SnapshotStore) SectionResult {
	result := SectionResult{
		Path:   sec.Path,
		OldTAG: sec.TAG,
		Op:     "delete",
	}

	if err := fs.Remove(sec.Path); err != nil {
		result.Error = &EditError{
			Fatal:   true,
			Kind:    "permission_denied",
			Message: fmt.Sprintf("cannot remove file: %v", err),
		}
		return result
	}

	result.NewTAG = sec.TAG
	return result
}

// countLines 统计文本行数。
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// ---------------------------------------------------------------------------
// applyEdits — 在内存中应用排序后的操作
// ---------------------------------------------------------------------------

type editSpan struct {
	op    Op
	start int // 0-based start line in original
	end   int // 0-based end line in original (exclusive)
}

func applyEdits(content string, ops []Op) (string, []EditHunk, error) {
	lines := strings.Split(content, "\n")
	hasTrailingNewline := strings.HasSuffix(content, "\n")

	// 验证行号
	for _, op := range ops {
		if op.Kind == OpSWAP || op.Kind == OpDEL {
			if op.LineEnd > len(lines) {
				return "", nil, fmt.Errorf("line %d out of range (file has %d lines)", op.LineEnd, len(lines))
			}
		}
		if op.Kind == OpINS && (op.Position == "pre" || op.Position == "post") {
			if op.RefLine > len(lines) {
				return "", nil, fmt.Errorf("INS reference line %d out of range (file has %d lines)", op.RefLine, len(lines))
			}
		}
	}

	var spans []editSpan
	for _, op := range ops {
		switch op.Kind {
		case OpDEL:
			start := op.LineStart - 1
			end := op.LineEnd
			spans = append(spans, editSpan{op: op, start: start, end: end})
		case OpSWAP:
			start := op.LineStart - 1
			end := op.LineEnd
			spans = append(spans, editSpan{op: op, start: start, end: end})
		case OpINS:
			switch op.Position {
			case "head":
				spans = append(spans, editSpan{op: op, start: 0, end: 0})
			case "tail":
				end := len(lines)
				if hasTrailingNewline {
					end--
				}
				spans = append(spans, editSpan{op: op, start: end, end: end})
			case "pre":
				start := op.RefLine - 1
				spans = append(spans, editSpan{op: op, start: start, end: start})
			case "post":
				start := op.RefLine
				spans = append(spans, editSpan{op: op, start: start, end: start})
			}
		}
	}

	hunks := buildEditHunks(lines, spans)

	// 前向迭代（spans 已按行号降序排列）
	for _, sp := range spans {
		switch sp.op.Kind {
		case OpDEL:
			if sp.end > len(lines) {
				sp.end = len(lines)
			}
			lines = append(lines[:sp.start], lines[sp.end:]...)
		case OpSWAP:
			if sp.end > len(lines) {
				sp.end = len(lines)
			}
			bodyLines := sp.op.Body
			newPart := make([]string, 0, sp.start+len(bodyLines)+len(lines)-sp.end)
			newPart = append(newPart, lines[:sp.start]...)
			newPart = append(newPart, bodyLines...)
			newPart = append(newPart, lines[sp.end:]...)
			lines = newPart
		case OpINS:
			bodyLines := sp.op.Body
			insertAt := sp.start
			if insertAt > len(lines) {
				insertAt = len(lines)
			}
			newPart := make([]string, 0, len(lines)+len(bodyLines))
			newPart = append(newPart, lines[:insertAt]...)
			newPart = append(newPart, bodyLines...)
			newPart = append(newPart, lines[insertAt:]...)
			lines = newPart
		}
	}

	result := strings.Join(lines, "\n")
	return result, hunks, nil
}


func buildEditHunks(origLines []string, spans []editSpan) []EditHunk {
	var hunks []EditHunk

	for _, sp := range spans {
		switch sp.op.Kind {
		case OpDEL:
			hunk := EditHunk{
				OldStart: sp.start + 1,
				OldCount: sp.end - sp.start,
				NewStart: sp.start + 1,
				NewCount: 0,
			}
			for i := sp.start; i < sp.end && i < len(origLines); i++ {
				hunk.Lines = append(hunk.Lines, EditLine{
					Kind:    LineDel,
					Content: origLines[i],
					OldNum:  i + 1,
				})
			}
			if len(hunk.Lines) > 0 {
				hunks = append(hunks, hunk)
			}

		case OpSWAP:
			bodyLines := sp.op.Body

			hunk := EditHunk{
				OldStart: sp.start + 1,
				OldCount: sp.end - sp.start,
				NewStart: sp.start + 1,
				NewCount: len(bodyLines),
			}
			for i := sp.start; i < sp.end && i < len(origLines); i++ {
				hunk.Lines = append(hunk.Lines, EditLine{
					Kind:    LineDel,
					Content: origLines[i],
					OldNum:  i + 1,
				})
			}
			for j, bl := range bodyLines {
				hunk.Lines = append(hunk.Lines, EditLine{
					Kind:    LineAdd,
					Content: bl,
					NewNum:  sp.start + 1 + j,
				})
			}
			hunks = append(hunks, hunk)

		case OpINS:
			bodyLines := sp.op.Body

			insertAt := sp.start + 1
			if sp.op.Position == "tail" {
				insertAt = len(origLines) + 1
			}

			hunk := EditHunk{
				OldStart: insertAt,
				OldCount: 0,
				NewStart: insertAt,
				NewCount: len(bodyLines),
			}
			for j, bl := range bodyLines {
				hunk.Lines = append(hunk.Lines, EditLine{
					Kind:    LineAdd,
					Content: bl,
					NewNum:  insertAt + j,
				})
			}
			hunks = append(hunks, hunk)
		}
	}

	return hunks
}

// ---------------------------------------------------------------------------
// sortOps — 按行号降序排列，前向迭代避免行号漂移
// ---------------------------------------------------------------------------

func sortOps(ops []Op) []Op {
	if len(ops) <= 1 {
		return ops
	}

	sorted := make([]Op, len(ops))
	copy(sorted, ops)

	sort.SliceStable(sorted, func(i, j int) bool {
		li := opLineNum(sorted[i])
		lj := opLineNum(sorted[j])
		if li != lj {
			return li > lj
		}
		return opPriority(sorted[i]) < opPriority(sorted[j])
	})

	return sorted
}

func opPriority(op Op) int {
	switch op.Kind {
	case OpDEL, OpREM:
		return 1
	case OpSWAP:
		return 2
	case OpINS:
		return 3
	case OpMV:
		return 4
	default:
		return 5
	}
}

func opLineNum(op Op) int {
	switch op.Kind {
	case OpSWAP, OpDEL:
		return op.LineStart
	case OpINS:
		if op.Position == "head" {
			return 0
		}
		if op.Position == "tail" {
			return 1 << 30
		}
		return op.RefLine
	default:
		return 0
	}
}
