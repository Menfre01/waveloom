package hashline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

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

// ApplyPatch 解析后的 patch 应用到文件系统。
// store 可为 nil（无 TAG 验证时跳过 Verify，但仍可执行编辑）。
// 不同文件的 Section 并行执行；同一文件的 Section 按声明顺序串行。
func ApplyPatch(patch *Patch, fs FileSystem, store *SnapshotStore) []SectionResult {
	n := len(patch.Sections)
	results := make([]SectionResult, n)

	// ── Step 0: 跨 Section 冲突检测（同文件多 Section）──
	conflictErrors := detectCrossSectionConflicts(patch)
	for i, err := range conflictErrors {
		results[i] = SectionResult{Path: patch.Sections[i].Path, OldTAG: patch.Sections[i].TAG, Error: err}
	}

	// 预先保存每个文件路径的原始快照内容
	originalSnapshots := make(map[string]string)
	if store != nil {
		for _, sec := range patch.Sections {
			storePath := fs.ResolvePath(sec.Path)
			if _, exists := originalSnapshots[storePath]; !exists {
				if snap, ok := store.Get(storePath); ok {
					originalSnapshots[storePath] = snap.Content
				}
			}
		}
	}

	// 按文件路径分组：同一文件的 Section 保持声明顺序，不同文件并行。
	type fileGroup struct {
		path     string
		indices  []int // 该文件中非冲突 Section 的索引
	}
	groupMap := make(map[string]*fileGroup)
	groupOrder := make([]*fileGroup, 0)

	for i, sec := range patch.Sections {
		if _, isConflict := conflictErrors[i]; isConflict {
			continue
		}
		fg, exists := groupMap[sec.Path]
		if !exists {
			fg = &fileGroup{path: sec.Path}
			groupMap[sec.Path] = fg
			groupOrder = append(groupOrder, fg)
		}
		fg.indices = append(fg.indices, i)
	}

	if len(groupOrder) == 0 {
		return results
	}

	var wg sync.WaitGroup
	wg.Add(len(groupOrder))

	for _, fg := range groupOrder {
		go func(g *fileGroup) {
			defer wg.Done()
			for _, idx := range g.indices {
				results[idx] = applySection(patch.Sections[idx], fs, store, originalSnapshots)
			}
		}(fg)
	}

	wg.Wait()
	return results
}

func applySection(sec Section, fs FileSystem, store *SnapshotStore, originalSnapshots map[string]string) SectionResult {
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
			snapContent := ""
			if orig, ok := originalSnapshots[storePath]; ok {
				snapContent = orig
			} else if snap, ok := store.Get(storePath); ok {
				snapContent = snap.Content
			}
			if snapContent != "" {
				recovery := RecoverOps(snapContent, currentContent, sec.Ops)
				if recovery.Success {
					result.Warning = fmt.Sprintf("TAG expired, auto-recovered: %v", verifyErr)
					sec.Ops = recovery.MappedOps
				} else {
					reason := "unknown"
					if len(recovery.Warnings) > 0 {
						reason = strings.Join(recovery.Warnings, "; ")
					}
					result.Error = &EditError{
						Fatal:   false,
						Kind:    "tag_mismatch",
						Message: fmt.Sprintf("TAG mismatch for %q and recovery failed (%s): the file has been modified since last read. Previous sections in this patch may have already been applied — use the post-edit context above to construct a new edit without re-reading. (%v)", sec.Path, reason, verifyErr),
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
			if len(sec.Ops) > 1 {
				result.Error = &EditError{
					Fatal:   false,
					Kind:    "invalid_args",
					Message: "MV cannot be combined with other operations in the same section. Use a separate section for MV.",
				}
				return result
			}
			return applyMV(sec, op, fs, store, currentContent)
		}
	}

	for _, op := range sec.Ops {
		if op.Kind == OpREM {
			if len(sec.Ops) > 1 {
				result.Error = &EditError{
					Fatal:   false,
					Kind:    "invalid_args",
					Message: "REM cannot be combined with other operations in the same section. Use a separate section for REM.",
				}
				return result
			}
			return applyREM(sec, fs, store)
		}
	}

	// 检测操作重叠（改进 1：在原始行号上检测，重叠时返回明确错误）
	if err := detectOverlaps(sec.Ops); err != nil {
		result.Error = &EditError{
			Fatal:   false,
			Kind:    "invalid_args",
			Message: err.Error(),
		}
		return result
	}

	// 按声明顺序应用操作，自动计算累计行偏移（改进 2）
	newContent, hunks, err := applyEdits(currentContent, sec.Ops)
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
// applyEdits — 在内存中按声明顺序应用操作，自动计算累计行偏移
// ---------------------------------------------------------------------------

type editSpan struct {
	op    Op
	start int // 0-based start line in original
	end   int // 0-based end line in original (exclusive)
}

// applyEdits 按声明顺序处理操作。每次操作后自动计算行偏移量并调整
// 后续操作的行号，LLM 无需手动计算偏移。所有操作的行号均以原始文件为基准，
// 系统在应用时按顺序累计偏移。
func applyEdits(content string, ops []Op) (string, []EditHunk, error) {
	lines := strings.Split(content, "\n")
	hasTrailingNewline := strings.HasSuffix(content, "\n")

	// 验证原始行号在范围内
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

	// 构建 editSpan（使用原始行号，用于 diff hunks 的 OldStart）
	var origSpans []editSpan
	for _, op := range ops {
		sp := opToSpan(op, lines, hasTrailingNewline)
		origSpans = append(origSpans, sp)
	}

	// 按声明顺序应用操作，使用位置感知的偏移追踪：
	// 每个操作记录 (applyPos, delta) — 仅当后续操作的操作位置 ≥ applyPos 时才受此 delta 影响。
	type posDelta struct {
		pos   int // 操作后的影响起始位置（0-based，仅 ≥pos 的行被偏移）
		delta int // 行数变化
	}
	var deltas []posDelta
	var appliedSpans []editSpan

	originalLines := lines

	for _, sp := range origSpans {
		// 计算当前操作的有效偏移：仅累加 applyPos ≤ 当前 start 的 delta
		offset := 0
		for _, pd := range deltas {
			if pd.pos <= sp.start {
				offset += pd.delta
			}
		}

		offsetSp := sp
		offsetSp.start += offset
		offsetSp.end += offset

		if offsetSp.start < 0 {
			offsetSp.start = 0
		}
		if offsetSp.end < 0 {
			offsetSp.end = 0
		}

		switch sp.op.Kind {
		case OpDEL:
			if offsetSp.end > len(lines) {
				offsetSp.end = len(lines)
			}
			if offsetSp.start < offsetSp.end {
				lines = append(lines[:offsetSp.start], lines[offsetSp.end:]...)
			}
			delta := -(offsetSp.end - offsetSp.start)
			deltas = append(deltas, posDelta{pos: offsetSp.end, delta: delta})
		case OpSWAP:
			if offsetSp.end > len(lines) {
				offsetSp.end = len(lines)
			}
			oldLen := offsetSp.end - offsetSp.start
			newLen := len(sp.op.Body)
			newPart := make([]string, 0, offsetSp.start+newLen+len(lines)-offsetSp.end)
			newPart = append(newPart, lines[:offsetSp.start]...)
			newPart = append(newPart, sp.op.Body...)
			newPart = append(newPart, lines[offsetSp.end:]...)
			lines = newPart
			deltas = append(deltas, posDelta{pos: offsetSp.end, delta: newLen - oldLen})
		case OpINS:
			bodyLines := sp.op.Body
			insertAt := offsetSp.start
			if insertAt > len(lines) {
				insertAt = len(lines)
			}
			newPart := make([]string, 0, len(lines)+len(bodyLines))
			newPart = append(newPart, lines[:insertAt]...)
			newPart = append(newPart, bodyLines...)
			newPart = append(newPart, lines[insertAt:]...)
			lines = newPart
			deltas = append(deltas, posDelta{pos: insertAt, delta: len(bodyLines)})
		}
		appliedSpans = append(appliedSpans, offsetSp)
	}


	// Normalize lines to preserve trailing newline semantics from the
	// original file. When hasTrailingNewline is true, lines must end
	// with "" (Split's marker for trailing \n). When false, lines must
	// NOT end with "" — unless a SWAP/INS at the end of the file
	// introduced a real empty last line that needs a trailing newline
	// to be representable.
	if hasTrailingNewline {
		if len(lines) == 0 || lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
	} else {
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			// The original file being empty ([""]) creates a
			// trailing "" artifact — skip, it's not a real
			// empty line introduced by an operation.
			isEmptyArtifact := len(originalLines) == 1 && originalLines[0] == ""
			if !isEmptyArtifact {
				lines = append(lines, "")
			}
		}
	}
	result := strings.Join(lines, "\n")
	hunks := buildEditHunksFromApplied(lines, origSpans, appliedSpans)
	return result, hunks, nil
}

// ApplyEditsForTest 是 applyEdits 的导出包装，仅供测试使用。
func ApplyEditsForTest(content string, ops []Op) (string, []EditHunk, error) {
	return applyEdits(content, ops)
}

// ---------------------------------------------------------------------------
// detectOverlaps — 操作重叠检测（改进 1）
// ---------------------------------------------------------------------------

// lineRange 表示操作在原始文件中的行范围（0-based）。
type lineRange struct {
	start int // 0-based, inclusive
	end   int // 0-based, exclusive
}

// detectOverlaps 检查多个操作是否在原始行号上有重叠范围。
// 重叠操作意味着 LLM 可能未考虑操作间的交互，应返回错误让其拆分为多个 edit 调用。
// 
// INS.PRE 使用零宽度插入点（RefLine-1），INS.POST 使用 RefLine 作为插入点。
// 这样 INS.PRE N 不会与 SWAP N（范围 N-1..N）重叠，让 applyEdits 的偏移计算
// 自动处理行号重映射。但 INS-to-INS 冲突（同 RefLine 上的 PRE+PRE/POST+POST/PRE+POST）
// 通过额外的同位检测捕获。
func detectOverlaps(ops []Op) error {
	for i := 0; i < len(ops); i++ {
		for j := i + 1; j < len(ops); j++ {
			ri := opRange(ops[i])
			rj := opRange(ops[j])

			// 额外的 INS-to-INS 同位检测：两个 INS 操作指向同一个 RefLine
			// 即为冲突（顺序依赖），无论 PRE/POST 组合。
			if ops[i].Kind == OpINS && ops[j].Kind == OpINS &&
				ops[i].RefLine > 0 && ops[j].RefLine > 0 &&
				ops[i].RefLine == ops[j].RefLine {
				return fmt.Errorf(
					"overlapping operations on reference line %d: %s (op %d) and %s (op %d) both target the same reference line; split overlapping ops into separate edit calls",
					ops[i].RefLine,
					ops[i].Kind, i+1, ops[j].Kind, j+1,
				)
			}

			if ri == nil || rj == nil {
				continue
			}
			if rangesOverlap(ri.start, ri.end, rj.start, rj.end) {
				return fmt.Errorf(
					"overlapping operations on lines %d-%d: %s (op %d) and %s (op %d) both touch overlapping ranges; split overlapping ops into separate edit calls",
					overlapStart(ri.start, rj.start), overlapEnd(ri.end, rj.end),
					ops[i].Kind, i+1, ops[j].Kind, j+1,
				)
			}
		}
	}
	return nil
}

func opRange(op Op) *lineRange {
	switch op.Kind {
	case OpSWAP, OpDEL:
		start := op.LineStart - 1
		end := op.LineEnd
		return &lineRange{start: start, end: end}
	case OpINS:
		if op.Position == "pre" {
			// INS.PRE 在参考行之前插入，不替换参考行本身。
			// 使用零宽度插入点：{RefLine-1, RefLine-1}，
			// 这样不会与 SWAP/DEL 在同一参考行上重叠。
			return &lineRange{start: op.RefLine - 1, end: op.RefLine - 1}
		}
		if op.Position == "post" {
			// INS.POST 在参考行之后插入，使用 {RefLine, RefLine}。
			return &lineRange{start: op.RefLine, end: op.RefLine}
		}
		// head/tail 不与任何行重叠
		return nil
	default:
		return nil
	}
}

// detectCrossSectionConflicts 检测同文件多 Section 之间的操作冲突。
// 返回冲突 Section 索引到错误信息的映射；若映射非空，ApplyPatch 对这些 Section
// 返回预构建的错误结果且不修改文件（其他文件不受影响）。无冲突返回 nil。
func detectCrossSectionConflicts(patch *Patch) map[int]*EditError {
	groups := make(map[string][]int) // path → section indices
	for i, sec := range patch.Sections {
		groups[sec.Path] = append(groups[sec.Path], i)
	}

	conflicts := make(map[int]*EditError)

	for path, indices := range groups {
		if len(indices) <= 1 {
			continue
		}

		// REM/MV + 其他操作 = 冲突（Section 1 删/移文件 → Section 2 无法操作）
		hasRemMV := false
		hasLineOps := false
		for _, si := range indices {
			for _, op := range patch.Sections[si].Ops {
				switch op.Kind {
				case OpREM, OpMV:
					hasRemMV = true
				case OpSWAP, OpDEL, OpINS:
					hasLineOps = true
				}
			}
		}
		if hasRemMV && hasLineOps {
			for _, si := range indices {
				conflicts[si] = &EditError{
					Fatal: false,
					Kind:  "invalid_args",
					Message: fmt.Sprintf(
						"cross-section conflict in %q: one section uses REM/MV while another uses line-range operations. "+
							"REM/MV and line operations on the same file cannot be combined in one patch. "+
							"Split REM/MV into its own edit call.",
						path,
					),
				}
			}
			continue
		}

		// 合并所有操作检测行范围重叠
		type opWithSrc struct {
			op       Op
			secIndex int
		}
		var allOps []opWithSrc
		for _, si := range indices {
			for _, op := range patch.Sections[si].Ops {
				if op.Kind == OpREM || op.Kind == OpMV {
					continue // 已在上面的 REM/MV 检测中处理
				}
				allOps = append(allOps, opWithSrc{op: op, secIndex: si + 1})
			}
		}

		hasOverlap := false
		var overlapDetail string
		for i := 0; i < len(allOps); i++ {
			for j := i + 1; j < len(allOps); j++ {
				ri := opRange(allOps[i].op)
				rj := opRange(allOps[j].op)

				// INS-to-INS 同位检测
				if allOps[i].op.Kind == OpINS && allOps[j].op.Kind == OpINS &&
					allOps[i].op.RefLine > 0 && allOps[j].op.RefLine > 0 &&
					allOps[i].op.RefLine == allOps[j].op.RefLine {
					hasOverlap = true
					overlapDetail = fmt.Sprintf("%s (section %d) and %s (section %d) both target reference line %d",
						allOps[i].op.Kind, allOps[i].secIndex,
						allOps[j].op.Kind, allOps[j].secIndex,
						allOps[i].op.RefLine)
					break
				}

				if ri == nil || rj == nil {
					continue
				}
				if rangesOverlap(ri.start, ri.end, rj.start, rj.end) {
					hasOverlap = true
					overlapDetail = fmt.Sprintf("%s (section %d, lines %d-%d) and %s (section %d, lines %d-%d) overlap",
						allOps[i].op.Kind, allOps[i].secIndex,
						ri.start+1, ri.end,
						allOps[j].op.Kind, allOps[j].secIndex,
						rj.start+1, rj.end)
					break
				}
			}
			if hasOverlap {
				break
			}
		}

		if hasOverlap {
			for _, si := range indices {
				conflicts[si] = &EditError{
					Fatal: false,
					Kind:  "invalid_args",
					Message: fmt.Sprintf(
						"cross-section conflict in %q: %s. "+
							"Merge overlapping operations into a single section, or split conflicting changes into separate edit calls.",
						path, overlapDetail,
					),
				}
			}
		}
	}

	if len(conflicts) == 0 {
		return nil
	}
	return conflicts
}

func rangesOverlap(aStart, aEnd, bStart, bEnd int) bool {
	return aStart < bEnd && bStart < aEnd
}

func overlapStart(a, b int) int {
	if a < b {
		return a + 1 // 回到 1-based
	}
	return b + 1
}

func overlapEnd(a, b int) int {
	if a > b {
		return a // 0-based exclusive = 1-based last
	}
	return b
}

// ---------------------------------------------------------------------------
// opToSpan / buildEditHunksFromApplied
// ---------------------------------------------------------------------------

func opToSpan(op Op, lines []string, hasTrailingNewline bool) editSpan {
	switch op.Kind {
	case OpDEL:
		start := op.LineStart - 1
		end := op.LineEnd
		return editSpan{op: op, start: start, end: end}
	case OpSWAP:
		start := op.LineStart - 1
		end := op.LineEnd
		return editSpan{op: op, start: start, end: end}
	case OpINS:
		switch op.Position {
		case "head":
			return editSpan{op: op, start: 0, end: 0}
		case "tail":
			// 空文件（split("") → [""]）退化为 head，避免前导换行
			if len(lines) == 1 && lines[0] == "" && !hasTrailingNewline {
				return editSpan{op: op, start: 0, end: 0}
			}
			end := len(lines)
			if hasTrailingNewline {
				end--
			}
			return editSpan{op: op, start: end, end: end}
		case "pre":
			start := op.RefLine - 1
			return editSpan{op: op, start: start, end: start}
		case "post":
			start := op.RefLine
			return editSpan{op: op, start: start, end: start}
		}
	}
	return editSpan{}
}

// buildEditHunksFromApplied 使用原始和应用后的 span 构建 diff hunks。
func buildEditHunksFromApplied(origLines []string, origSpans, appliedSpans []editSpan) []EditHunk {
	var hunks []EditHunk
	for i := range origSpans {
		orig := origSpans[i]
		offset := appliedSpans[i]

		switch orig.op.Kind {
		case OpDEL:
			hunk := EditHunk{
				OldStart: orig.start + 1,
				OldCount: orig.end - orig.start,
				NewStart: offset.start + 1,
				NewCount: 0,
			}
			for j := orig.start; j < orig.end && j < len(origLines); j++ {
				hunk.Lines = append(hunk.Lines, EditLine{
					Kind:    LineDel,
					Content: origLines[j],
					OldNum:  j + 1,
				})
			}
			if len(hunk.Lines) > 0 {
				hunks = append(hunks, hunk)
			}

		case OpSWAP:
			bodyLines := orig.op.Body
			hunk := EditHunk{
				OldStart: orig.start + 1,
				OldCount: orig.end - orig.start,
				NewStart: offset.start + 1,
				NewCount: len(bodyLines),
			}
			for j := orig.start; j < orig.end && j < len(origLines); j++ {
				hunk.Lines = append(hunk.Lines, EditLine{
					Kind:    LineDel,
					Content: origLines[j],
					OldNum:  j + 1,
				})
			}
			for j, bl := range bodyLines {
				hunk.Lines = append(hunk.Lines, EditLine{
					Kind:    LineAdd,
					Content: bl,
					NewNum:  offset.start + 1 + j,
				})
			}
			hunks = append(hunks, hunk)

		case OpINS:
			bodyLines := orig.op.Body
			hunk := EditHunk{
				OldStart: offset.start + 1,
				OldCount: 0,
				NewStart: offset.start + 1,
				NewCount: len(bodyLines),
			}
			for j, bl := range bodyLines {
				hunk.Lines = append(hunk.Lines, EditLine{
					Kind:    LineAdd,
					Content: bl,
					NewNum:  offset.start + 1 + j,
				})
			}
			hunks = append(hunks, hunk)
		}
	}
	return hunks
}
