package hashline

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"github.com/Menfre01/waveloom/pkg/pathutil"
	"golang.org/x/text/unicode/norm"
)

type OpKind int

const (
	OpSWAP OpKind = iota
	OpDEL
	OpINS
)

func (k OpKind) String() string {
	switch k {
	case OpSWAP:
		return "SWAP"
	case OpDEL:
		return "DEL"
	case OpINS:
		return "INS"
	default:
		return "UNKNOWN"
	}
}

type Op struct {
	Kind       OpKind
	LineStart  int
	LineEnd    int
	RefLine    int
	Body       []string
	OldString  string
	ReplaceAll bool
}

type Section struct {
	Path string
	TAG  string
	Ops  []Op
}

type Patch struct{ Sections []Section }

type EditLineKind string

const (
	LineAdd    EditLineKind = "+"
	LineDel    EditLineKind = "-"
	LineCtx    EditLineKind = " "
	LineHeader EditLineKind = "@"
)

type EditLine struct {
	Kind    EditLineKind
	Content string
	OldNum  int
	NewNum  int
}

type EditHunk struct {
	OldStart, OldCount, NewStart, NewCount int
	Heading                                string
	Lines                                  []EditLine
	NoNewlineAtEOF                         bool
}

type SectionResult struct {
	Path, Op, OldTAG, NewTAG string
	LinesDelta               int
	DiffHunks                []EditHunk
	Warning                  string
	Error                    *EditError
}

type EditError struct {
	Fatal   bool
	Kind    string
	Message string
}

func (e *EditError) Error() string { return e.Message }

type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("parse error at line %d: %s", e.Line, e.Msg)
	}
	return fmt.Sprintf("parse error: %s", e.Msg)
}

// ---------------------------------------------------------------------------
// ParsePatch
// ---------------------------------------------------------------------------

func ParsePatch(text string) (*Patch, error) {
	lines := strings.Split(text, "\n")
	scanner := &patchScanner{lines: lines, pos: 0}
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
		if strings.EqualFold(line, "*** End Patch") {
			scanner.pos++
			break
		}
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

type patchScanner struct {
	lines []string
	pos   int
}

func (s *patchScanner) currentLine() int { return s.pos + 1 }
func (s *patchScanner) rawLine() string {
	if s.pos >= len(s.lines) {
		return ""
	}
	return s.lines[s.pos]
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
func (s *patchScanner) expectMarker(marker string) error {
	line := s.trimmed()
	if !strings.EqualFold(line, marker) {
		return &ParseError{Line: s.currentLine(), Msg: fmt.Sprintf("expected %q, got %q", marker, line)}
	}
	s.pos++
	return nil
}

func (s *patchScanner) parseSection() (Section, error) {
	line := s.trimmed()
	lineNum := s.currentLine()
	if !strings.HasPrefix(line, "[") || !strings.Contains(line, "#") {
		return Section{}, &ParseError{Line: lineNum, Msg: fmt.Sprintf("expected [PATH#TAG], got %q", line)}
	}
	idxEnd := strings.IndexByte(line, ']')
	if idxEnd < 0 {
		return Section{}, &ParseError{Line: lineNum, Msg: "unclosed section header"}
	}
	header := line[1:idxEnd]
	hashIdx := strings.LastIndex(header, "#")
	if hashIdx < 0 || hashIdx == len(header)-1 {
		return Section{}, &ParseError{Line: lineNum, Msg: "invalid section header (missing TAG)"}
	}
	path := header[:hashIdx]
	tag := header[hashIdx+1:]
	if tag == "" || len(tag) != 4 {
		return Section{}, &ParseError{Line: lineNum, Msg: fmt.Sprintf("invalid TAG: %q (must be 4 hex chars)", tag)}
	}
	s.pos++

	var ops []Op
	for s.pos < len(s.lines) {
		trimmed := strings.TrimSpace(s.rawLine())
		if trimmed == "" {
			s.pos++
			continue
		}
		if strings.HasPrefix(trimmed, "[") || strings.EqualFold(trimmed, "*** End Patch") {
			break
		}
		op, err := s.parseOp()
		if err != nil {
			return Section{}, err
		}
		ops = append(ops, op)
	}
	if len(ops) == 0 {
		return Section{}, &ParseError{Line: lineNum, Msg: fmt.Sprintf("no operations in section [%s#%s]", path, tag)}
	}
	return Section{Path: path, TAG: tag, Ops: ops}, nil
}

func normalizeOpLine(line string) string {
	// Normalize legacy INS.PRE / INS.POST / INS.HEAD / INS.TAIL spacing variants
	for _, variant := range []string{"INS. PRE", "INS.  PRE", "INS. POST", "INS.  POST", "INS. HEAD", "INS.  HEAD", "INS. TAIL", "INS.  TAIL"} {
		canonical := strings.ReplaceAll(variant, " ", "")
		if idx := strings.Index(line, variant); idx >= 0 {
			line = line[:idx] + canonical + line[idx+len(variant):]
			break
		}
	}
	return line
}

// stripTrailingComment removes trailing comments (// or #) from an operation line,
// but only outside of quoted strings to avoid corrupting SWAP old-text content.
func stripTrailingComment(line string) string {
	inQuote := false
	var quoteChar byte
	for i := 0; i < len(line); i++ {
		if !inQuote {
			if line[i] == '"' || line[i] == '\'' {
				inQuote = true
				quoteChar = line[i]
				continue
			}
			if line[i] == '/' && i+1 < len(line) && line[i+1] == '/' {
				return strings.TrimSpace(line[:i])
			}
			if line[i] == '#' && i > 0 {
				return strings.TrimSpace(line[:i])
			}
		} else {
			if line[i] == '\\' && i+1 < len(line) {
				i++
				continue
			}
			if line[i] == quoteChar {
				inQuote = false
			}
		}
	}
	return line
}

func (s *patchScanner) parseOp() (Op, error) {
	raw := s.rawLine()
	trimmed := strings.TrimSpace(raw)
	// 去除行尾注释（跳过引号内内容）
	cleaned := stripTrailingComment(trimmed)
	normalized := normalizeOpLine(cleaned)
	lineNum := s.currentLine()
	upper := strings.ToUpper(normalized)

	switch {
	case upper == "SWAP" || strings.HasPrefix(upper, "SWAP "):
		return s.parseSwapOp(normalized, lineNum)
	case strings.HasPrefix(upper, "DEL "):
		return s.parseDelOp(normalized, lineNum)
	case strings.HasPrefix(upper, "INS.PRE "):
		return s.parseInsLegacy(normalized, "pre", lineNum)
	case strings.HasPrefix(upper, "INS.POST "):
		return s.parseInsLegacy(normalized, "post", lineNum)
	case strings.HasPrefix(upper, "INS.HEAD"):
		rest := strings.TrimSuffix(strings.TrimSpace(normalized), ":")
		if rest != "INS.HEAD" {
			return Op{}, &ParseError{Line: lineNum, Msg: "INS.HEAD does not accept arguments — use INS.HEAD: alone, or INS 0:"}
		}
		s.pos++
		body, err := s.readBodyInContext(false)
		if err != nil {
			return Op{}, err
		}
		if len(body) == 0 {
			return Op{}, &ParseError{Line: lineNum, Msg: "INS.HEAD requires body lines — add content after the operation"}
		}
		return Op{Kind: OpINS, RefLine: 0, Body: body}, nil
	case strings.HasPrefix(upper, "INS.TAIL"):
		rest := strings.TrimSuffix(strings.TrimSpace(normalized), ":")
		if rest != "INS.TAIL" {
			return Op{}, &ParseError{Line: lineNum, Msg: "INS.TAIL does not accept arguments — use INS.TAIL: alone, or INS N: with N ≥ file length"}
		}
		s.pos++
		body, err := s.readBodyInContext(false)
		if err != nil {
			return Op{}, err
		}
		if len(body) == 0 {
			return Op{}, &ParseError{Line: lineNum, Msg: "INS.TAIL requires body lines — add content after the operation"}
		}
		return Op{Kind: OpINS, RefLine: -1, Body: body}, nil
	case strings.HasPrefix(upper, "INS "):
		return s.parseInsOp(normalized, lineNum)
	case strings.HasPrefix(upper, "REM"):
		return Op{}, &ParseError{Line: lineNum, Msg: "REM is no longer supported — use bash: rm <file>"}
	case strings.HasPrefix(upper, "MV "):
		return Op{}, &ParseError{Line: lineNum, Msg: "MV is no longer supported — use bash: mv <src> <dst>"}
	default:
		return Op{}, &ParseError{Line: lineNum, Msg: fmt.Sprintf("unknown operation: %q", trimmed)}
	}
}

func (s *patchScanner) parseSwapOp(line string, lineNum int) (Op, error) {
	rest := ""
	if len(line) > 5 {
		rest = strings.TrimSpace(line[5:])
	}
	upperRest := strings.ToUpper(rest)
	replaceAll := false
	if strings.HasPrefix(upperRest, "ALL ") || upperRest == "ALL" {
		replaceAll = true
		if len(rest) > 4 {
			rest = strings.TrimSpace(rest[4:])
		} else {
			rest = ""
		}
	}
	// Legacy: SWAP "text": or SWAP ALL "text": — inline quoted old content
	if strings.HasPrefix(rest, `"`) || strings.HasPrefix(rest, "'") {
		quote := rest[0]
		oldStr, remainder, err := s.readQuotedString(rest[1:], quote, lineNum)
		if err != nil {
			return Op{}, &ParseError{Line: lineNum, Msg: fmt.Sprintf("invalid SWAP content: %v", err)}
		}
		remainder = strings.TrimSpace(remainder)
		hasBody := strings.HasPrefix(remainder, ":")
		s.pos++ // consume closing-quote line
		var body []string
		if hasBody {
			body, err = s.readBody()
			if err != nil {
				return Op{}, err
			}
		}
		return Op{Kind: OpSWAP, OldString: oldStr, ReplaceAll: replaceAll, Body: body}, nil
	}
	// Parse optional line range: SWAP N.=M ...
	numEnd := 0
	for numEnd < len(rest) && (rest[numEnd] >= '0' && rest[numEnd] <= '9' || rest[numEnd] == '.' || rest[numEnd] == '=') {
		numEnd++
	}
	hasLineNumbers := numEnd > 0
	var start, end int
	if hasLineNumbers {
		rangePart := strings.TrimSpace(rest[:numEnd])
		var err error
		start, end, err = parseLineRange(rangePart)
		if err != nil {
			return Op{}, &ParseError{Line: lineNum, Msg: fmt.Sprintf("invalid SWAP range: %v", err)}
		}
		if start < 1 || end < 1 {
			return Op{}, &ParseError{Line: lineNum, Msg: "SWAP line numbers must be >= 1"}
		}
		// Legacy: SWAP N.=M "text": — inline quoted old content with line numbers
		restAfterRange := strings.TrimSpace(rest[numEnd:])
		if strings.HasPrefix(restAfterRange, `"`) || strings.HasPrefix(restAfterRange, "'") {
			quote := restAfterRange[0]
			oldStr, remainder, err := s.readQuotedString(restAfterRange[1:], quote, lineNum)
			if err != nil {
				return Op{}, &ParseError{Line: lineNum, Msg: fmt.Sprintf("invalid SWAP content: %v", err)}
			}
			remainder = strings.TrimSpace(remainder)
			hasBody := strings.HasPrefix(remainder, ":")
			s.pos++
			var body []string
			if hasBody {
				body, err = s.readBody()
				if err != nil {
					return Op{}, err
				}
			}
			return Op{Kind: OpSWAP, LineStart: start, LineEnd: end, OldString: oldStr, ReplaceAll: replaceAll, Body: body}, nil
		}
	}
	// Sentinel mode: %OLD ... %NEW ...  (or body-only for line-number mode without verification)
	s.pos++ // consume the SWAP line
	oldStr, body, err := s.readSentinelBlock(lineNum, hasLineNumbers)
	if err != nil {
		return Op{}, err
	}
	if !hasLineNumbers && oldStr == "" {
		return Op{}, &ParseError{Line: lineNum, Msg: "SWAP without line numbers requires %OLD — add a sentinel block or use SWAP \"text\":"}
	}
	return Op{Kind: OpSWAP, LineStart: start, LineEnd: end, OldString: oldStr, ReplaceAll: replaceAll, Body: body}, nil
}

func (s *patchScanner) parseDelOp(line string, lineNum int) (Op, error) {
	rest := strings.TrimSpace(line[4:])
	rest = strings.TrimSuffix(rest, ":")
	start, end, err := parseLineRange(strings.TrimSpace(rest))
	if err != nil {
		return Op{}, &ParseError{Line: lineNum, Msg: fmt.Sprintf("invalid DEL range: %v", err)}
	}
	if start < 1 || end < 1 {
		return Op{}, &ParseError{Line: lineNum, Msg: "DEL line numbers must be >= 1"}
	}
	s.pos++
	return Op{Kind: OpDEL, LineStart: start, LineEnd: end}, nil
}

func (s *patchScanner) parseInsOp(line string, lineNum int) (Op, error) {
	rest := strings.TrimSpace(line[4:])
	rest = strings.TrimSuffix(strings.TrimSpace(rest), ":")
	n, err := parseSingleLine(strings.TrimSpace(rest))
	if err != nil {
		return Op{}, &ParseError{Line: lineNum, Msg: fmt.Sprintf("invalid INS line number: %v", err)}
	}
	s.pos++
	body, err := s.readBody()
	if err != nil {
		return Op{}, err
	}
	if len(body) == 0 {
		return Op{}, &ParseError{Line: lineNum, Msg: "INS requires body lines — add content after the operation"}
	}
	return Op{Kind: OpINS, RefLine: n, Body: body}, nil
}

func (s *patchScanner) parseInsLegacy(line string, position string, lineNum int) (Op, error) {
	prefix := "INS." + strings.ToUpper(position) + " "
	rest := line[len(prefix):]
	rest = strings.TrimSuffix(strings.TrimSpace(rest), ":")
	n, err := parseSingleLine(strings.TrimSpace(rest))
	if err != nil {
		return Op{}, &ParseError{Line: lineNum, Msg: fmt.Sprintf("invalid INS.%s line number: %v", position, err)}
	}
	s.pos++
	body, err := s.readBody()
	if err != nil {
		return Op{}, err
	}
	if len(body) == 0 {
		return Op{}, &ParseError{Line: lineNum, Msg: "INS requires body lines — add content after the operation"}
	}
	refLine := n
	if position == "pre" && n > 0 {
		refLine = n - 1
	}
	return Op{Kind: OpINS, RefLine: refLine, Body: body}, nil
}

// readQuotedString 从 patch 中读取可能跨多行的引用字符串。
// start 是开引号之后的第一行内容。quote 是引号字符。
// s.pos 应指向开引号所在的行。返回时 s.pos 停在闭引号所在行。
func (s *patchScanner) readQuotedString(start string, quote byte, startLine int) (string, string, error) {
	var buf strings.Builder
	remaining := start

	for {
		for i := 0; i < len(remaining); i++ {
			if remaining[i] == '\\' && i+1 < len(remaining) {
				i++
				if quote == '\'' {
					// Single-quote mode: \\ -> \\, \\' -> ', \\t -> tab, \\n -> newline
					// Everything else stays literal - ideal for copy-paste from read output.
					switch remaining[i] {
					case '\\':
						buf.WriteByte('\\')
					case '\'':
						buf.WriteByte('\'')
					case 't':
						buf.WriteByte('\t')
					case 'n':
						buf.WriteByte('\n')
					default:
						buf.WriteByte('\\')
						buf.WriteByte(remaining[i])
					}
				} else {
					// Double-quote: full escape processing
					switch remaining[i] {
					case 'n':
						buf.WriteByte('\n')
					case 't':
						buf.WriteByte('\t')
					case 'r':
						buf.WriteByte('\r')
					case '\\':
						buf.WriteByte('\\')
					default:
						if remaining[i] == quote {
							buf.WriteByte(quote)
						} else {
							buf.WriteByte('\\')
							buf.WriteByte(remaining[i])
						}
					}
				}
				continue
			}
			if remaining[i] == quote {
				if buf.Len() == 0 {
					return "", "", fmt.Errorf("empty quoted string")
				}
				return buf.String(), remaining[i+1:], nil
			}
			buf.WriteByte(remaining[i])
		}
		// 当前行未找到闭引号 → 添加换行，读取下一行
		buf.WriteByte('\n')
		s.pos++
		if s.pos >= len(s.lines) {
			return "", "", &ParseError{Line: startLine, Msg: fmt.Sprintf("unclosed quoted string — missing closing %c", quote)}
		}
		remaining = s.rawLine()
	}
}

var bodyTerminators = []string{"SWAP ", "DEL ", "INS.PRE ", "INS.POST ", "INS.HEAD", "INS.TAIL", "INS "}

func isBodyTerminator(line string) bool {
	upper := strings.ToUpper(strings.TrimSpace(line))
	for _, prefix := range bodyTerminators {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return strings.HasPrefix(upper, "[") || strings.HasPrefix(upper, "***")
}

func (s *patchScanner) readBody() ([]string, error) {
	return s.readBodyInContext(false)
}

// readBodyInContext reads body lines. When inSentinel is true, unindented
// %OLD/%NEW stop reading (they act as sentinel delimiters). When false,
// %OLD/%NEW are treated as literal content.
func (s *patchScanner) readBodyInContext(inSentinel bool) ([]string, error) {
	var bodyLines []string
	for s.pos < len(s.lines) {
		raw := s.rawLine()
		trimmed := strings.TrimLeft(raw, " 	")
		if raw == "" || trimmed == "" {
			s.pos++
			continue
		}
		// \+ escape: body content that looks like an operation (SWAP/DEL/INS).
		// Strips only the backslash, preserving indentation and the + character.
		if idx := strings.Index(raw, `\+`); idx >= 0 {
			unescaped := raw[:idx] + raw[idx+1:] // remove just the \
			bodyLines = append(bodyLines, unescaped)
			s.pos++
			continue
		}
		// \%OLD and \%NEW escapes: literal %OLD / %NEW at column 0.
		// Needed when body content contains unindented sentinel keywords.
		if strings.HasPrefix(raw, `\%OLD`) {
			bodyLines = append(bodyLines, "%OLD"+raw[5:])
			s.pos++
			continue
		}
		if strings.HasPrefix(raw, `\%NEW`) {
			bodyLines = append(bodyLines, "%NEW"+raw[5:])
			s.pos++
			continue
		}
		if inSentinel && isSentinel(raw) {
			break
		}
		if isBodyTerminator(trimmed) {
			break
		}
		// Strip leading '+' body marker (patch format convention).
		// + escape at column 0 is handled above (lines 547–552) and
		// preserves literal '+' — those lines won't reach here.
		if len(raw) > 0 && raw[0] == '+' {
			raw = raw[1:]
		}
		bodyLines = append(bodyLines, raw)
		s.pos++
	}
	return bodyLines, nil
}

// isSentinel reports whether line is exactly %OLD or %NEW at column 0.
func isSentinel(line string) bool {
	return line == "%OLD" || line == "%NEW"
}

// readSentinelBlock reads optional %OLD ... %NEW ... sentinel content for SWAP.
// Only unindented %OLD/%NEW act as delimiters; indented ones are literal content.
// Returns oldStr (joined old lines, "" if no %OLD sentinel) and body.
func (s *patchScanner) readSentinelBlock(lineNum int, hasLineNumbers bool) (oldStr string, body []string, err error) {
	for s.pos < len(s.lines) {
		raw := s.rawLine()
		if raw == "" {
			s.pos++
			continue
		}
		if raw == "%OLD" {
			s.pos++ // consume sentinel
			var oldLines []string
			for s.pos < len(s.lines) {
				raw2 := s.rawLine()
				if raw2 == "%NEW" {
					s.pos++ // consume sentinel
					break
				}
				oldLines = append(oldLines, raw2)
				s.pos++
			}
			if len(oldLines) == 0 {
				oldStr = ""
			} else {
				oldStr = strings.Join(oldLines, "\n")
			}
			body, err = s.readBodyInContext(true)
			if err != nil {
				return "", nil, err
			}
			return oldStr, body, nil
		}
		// No sentinel — body starts here (line-number mode without verification)
		if hasLineNumbers {
			body, err = s.readBodyInContext(false)
			if err != nil {
				return "", nil, err
			}
			return "", body, nil
		}
		// SWAP without line numbers and no sentinel — caller handles error
		return "", nil, nil
	}
	return "", nil, nil
}

func parseLineRange(s string) (int, int, error) {
	s = strings.TrimSpace(s)
	if idx := strings.Index(s, "."); idx >= 0 {
		after := s[idx+1:]
		if strings.HasPrefix(after, "=") {
			start, err := parseSingleLine(strings.TrimSpace(s[:idx]))
			if err != nil {
				return 0, 0, err
			}
			end, err := parseSingleLine(strings.TrimSpace(after[1:]))
			if err != nil {
				return 0, 0, err
			}
			if end < start {
				return 0, 0, fmt.Errorf("end line %d < start line %d", end, start)
			}
			return start, end, nil
		}
	}
	if idx := strings.Index(s, ":="); idx >= 0 {
		left := strings.TrimSpace(s[:idx])
		right := strings.TrimSuffix(strings.TrimSpace(s[idx+2:]), ":")
		return 0, 0, fmt.Errorf("invalid range %q: did you mean %s.=%s?", s, left, right)
	}
	start, err := parseSingleLine(s)
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
	if n < 0 {
		return 0, fmt.Errorf("line number must be >= 0: %q", s)
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// FileSystem
// ---------------------------------------------------------------------------

type FileSystem interface {
	ReadFile(string) (string, error)
	WriteFile(string, string) error
	MkdirAll(string) error
	Remove(string) error
	ResolvePath(string) string
}

type OSFS struct{ WorkingDir string }

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
// ApplyPatch
// ---------------------------------------------------------------------------

func ApplyPatch(patch *Patch, fs FileSystem, store *SnapshotStore) []SectionResult {
	n := len(patch.Sections)
	results := make([]SectionResult, n)
	conflictErrors := detectCrossSectionConflicts(patch)
	for i, err := range conflictErrors {
		results[i] = SectionResult{Path: patch.Sections[i].Path, OldTAG: patch.Sections[i].TAG, Error: err}
	}
	originalSnapshots := make(map[string]string)
	if store != nil {
		for _, sec := range patch.Sections {
			storePath := fs.ResolvePath(sec.Path)
			if _, exists := originalSnapshots[storePath]; !exists {
				if snap, ok := store.Get(storePath); ok && snap.TAG == sec.TAG {
					originalSnapshots[storePath] = snap.Content
				}
			}
		}
	}
	type fileGroup struct {
		path    string
		indices []int
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
			if len(g.indices) == 1 {
				results[g.indices[0]] = applySection(patch.Sections[g.indices[0]], fs, store, originalSnapshots)
			} else {
				applySectionGroupAtomic(results, g.indices, patch.Sections, fs, store, originalSnapshots)
			}
		}(fg)
	}
	wg.Wait()
	return results
}

// matchContent applies progressive matching strategies (exact → NFC → rstrip → trim → NFKC).
// Returns (matched, matchLevel). matchLevel is "exact" or the fallback strategy name.
func matchContent(actual, expected string) (bool, string) {
	if actual == expected {
		return true, "exact"
	}
	if norm.NFC.String(actual) == norm.NFC.String(expected) {
		return true, "nfc"
	}
	if rstripLines(actual) == rstripLines(expected) {
		return true, "rstrip"
	}
	if trimLines(actual) == trimLines(expected) {
		return true, "trim"
	}
	if norm.NFKC.String(actual) == norm.NFKC.String(expected) {
		return true, "nfkc"
	}
	return false, ""
}
// rstripLines trims trailing whitespace from each line.
func rstripLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	return strings.Join(lines, "\n")
}
// trimLines trims leading and trailing whitespace from each line.
func trimLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return strings.Join(lines, "\n")
}
func resolveContentOps(lines []string, ops *[]Op) error {
	content := strings.Join(lines, "\n")
	var resolved []Op
	for _, op := range *ops {
		if op.Kind != OpSWAP || op.OldString == "" {
			resolved = append(resolved, op)
			continue
		}

		// 混合模式: 行号内容优先校验
		if op.LineStart > 0 && op.LineEnd > 0 {
			if op.LineStart > len(lines) || op.LineEnd > len(lines) {
				return fmt.Errorf("SWAP: lines %d.=%d out of range (file has %d lines)", op.LineStart, op.LineEnd, len(lines))
			}
			// Normalize CRLF in OldString — LLM may paste with Windows line endings.
			op.OldString = strings.ReplaceAll(op.OldString, "\r\n", "\n")
			actualLines := strings.Join(lines[op.LineStart-1:op.LineEnd], "\n")
			matched, _ := matchContent(actualLines, op.OldString)
			if !matched {
				fileLines := strings.Split(actualLines, "\n")
				specLines := strings.Split(op.OldString, "\n")
				firstDiff := 0
				for firstDiff < len(fileLines) && firstDiff < len(specLines) && fileLines[firstDiff] == specLines[firstDiff] {
					firstDiff++
				}
				fileLine := ""
				specLine := ""
				if firstDiff < len(fileLines) {
					fileLine = fileLines[firstDiff]
				}
				if firstDiff < len(specLines) {
					specLine = specLines[firstDiff]
				}
				// If the lines look identical, show a hex dump so the user can
				// spot invisible differences (NFC vs NFD, zero-width chars, BOM).
				hexHint := ""
				if fileLine == specLine && fileLine != "" {
					hexHint = fmt.Sprintf("\n  (visually identical — possible encoding difference)\n    file hex:     %x\n    specified hex: %x",
						[]byte(fileLine), []byte(specLine))
				}
				return fmt.Errorf("SWAP: content mismatch at lines %d.=%d\n  file has %d lines, you specified %d lines\n  first diff at line %d:\n    file:     %q\n    specified: %q%s\n  hint: check for leading/trailing newlines, trailing whitespace, or off-by-one",
					op.LineStart, op.LineEnd,
					len(fileLines), len(specLines),
					firstDiff+1, fileLine, specLine, hexHint)
			}
			resolved = append(resolved, Op{Kind: OpSWAP, LineStart: op.LineStart, LineEnd: op.LineEnd, Body: op.Body})
			continue
		}
		// 纯内容模式: 唯一匹配→执行, 多个匹配→报错带行号
		var positions []int
		searchFrom := 0
		for {
			idx := strings.Index(content[searchFrom:], op.OldString)
			if idx < 0 {
				break
			}
			positions = append(positions, searchFrom+idx)
			searchFrom += idx + len(op.OldString)
		}
		if len(positions) == 0 {
			return fmt.Errorf("SWAP: %q not found in file — check exact whitespace and spelling", op.OldString)
		}
		if !op.ReplaceAll && len(positions) > 1 {
			var lineHints []string
			for _, byteOff := range positions {
				lineHints = append(lineHints, fmt.Sprintf("%d", strings.Count(content[:byteOff], "\n")+1))
			}
			return fmt.Errorf("SWAP: %q found %d times at lines %s — add line numbers, e.g. SWAP N.=M %q", op.OldString, len(positions), strings.Join(lineHints, ", "), op.OldString) //nolint:staticcheck
		}
		for _, byteOff := range positions {
			startLine := strings.Count(content[:byteOff], "\n") + 1
			endLine := startLine + strings.Count(op.OldString, "\n")
			// Trailing \n in OldString is a line separator, not an extra line.
			if strings.HasSuffix(op.OldString, "\n") && endLine > startLine {
				endLine--
			}
			resolved = append(resolved, Op{Kind: OpSWAP, LineStart: startLine, LineEnd: endLine, Body: op.Body})
		}
	}
	*ops = resolved
	return nil
}

func normalizeInsOps(lines []string, ops *[]Op) {
	for i := range *ops {
		if (*ops)[i].Kind != OpINS {
			continue
		}
		if (*ops)[i].RefLine == -1 {
			(*ops)[i].LineStart = len(lines)
			// Empty file: INS.TAIL is identical to INS.HEAD (avoids leading newline).
			if len(lines) == 1 && lines[0] == "" {
				(*ops)[i].LineStart = 0
			}
		} else {
			(*ops)[i].LineStart = (*ops)[i].RefLine
			// Clamp: INS N 超过文件行数 → 插入到文件末尾 (len(lines) 是去除尾空行的行数)
			if (*ops)[i].LineStart > len(lines) {
				(*ops)[i].LineStart = len(lines)
			}
		}
	}
}
func applySection(sec Section, fs FileSystem, store *SnapshotStore, originalSnapshots map[string]string) SectionResult {
	storePath := fs.ResolvePath(sec.Path)
	result := SectionResult{Path: sec.Path, OldTAG: sec.TAG}
	currentContent, err := fs.ReadFile(sec.Path)
	if err != nil {
		if os.IsNotExist(err) {
			result.Error = &EditError{Fatal: false, Kind: "file_not_found", Message: fmt.Sprintf("file not found: %s", sec.Path)}
		} else {
			result.Error = &EditError{Fatal: true, Kind: "permission_denied", Message: err.Error()}
		}
		return result
	}
	if store != nil {
		_, verifyErr := store.Verify(storePath, sec.TAG, currentContent)
		if verifyErr != nil {
			snapContent := ""
			if orig, ok := originalSnapshots[storePath]; ok {
				snapContent = orig
			} else if snap, ok := store.Get(storePath); ok && snap.TAG == sec.TAG {
				snapContent = snap.Content
			}
			if snapContent != "" {
				recovery := RecoverOps(snapContent, currentContent, sec.Ops)
				if recovery.Success {
					result.Warning = fmt.Sprintf("TAG expired, auto-recovered: %v", verifyErr)
					if recovery.RemapSummary != "" {
						result.Warning += " (" + recovery.RemapSummary + ")"
					}
					sec.Ops = recovery.MappedOps
				} else {
					reason := "unknown"
					if len(recovery.Warnings) > 0 {
						reason = strings.Join(recovery.Warnings, "; ")
					}
					result.Error = &EditError{Fatal: false, Kind: "tag_mismatch", Message: fmt.Sprintf("TAG mismatch + recovery failed (%s): re-read.", reason)}
					return result
				}
			} else {
				result.Error = &EditError{Fatal: false, Kind: "tag_mismatch", Message: "TAG mismatch: re-read."}
				return result
			}
		}
	}
		// Normalize Windows line endings before splitting.
	currentContent = strings.ReplaceAll(currentContent, "\r\n", "\n")
	lines := strings.Split(currentContent, "\n")
	if strings.HasSuffix(currentContent, "\n") && len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	// Check for missing %OLD before resolveContentOps strips OldString.
	hasMissingOld := false
	for _, op := range sec.Ops {
		if op.Kind == OpSWAP && op.LineStart > 0 && op.OldString == "" {
			hasMissingOld = true
			break
		}
	}
	if err := resolveContentOps(lines, &sec.Ops); err != nil {
		result.Error = &EditError{Fatal: false, Kind: "invalid_args", Message: err.Error()}
		return result
	}
	if hasMissingOld {
		result.Warning = "No %OLD provided — content verification skipped. TAG-only safety."
	}
	normalizeInsOps(lines, &sec.Ops)
	if err := detectOverlaps(sec.Ops); err != nil {
		result.Error = &EditError{Fatal: false, Kind: "invalid_args", Message: err.Error()}
		return result
	}
	newContent, hunks, err := applyEdits(currentContent, sec.Ops)
	if err != nil {
		result.Error = &EditError{Fatal: false, Kind: "invalid_args", Message: err.Error()}
		return result
	}
	if err := fs.WriteFile(sec.Path, newContent); err != nil {
		result.Error = &EditError{Fatal: true, Kind: "permission_denied", Message: err.Error()}
		return result
	}
	newTAG := sec.TAG
	if store != nil {
		newTAG = store.Update(storePath, newContent)
	}
	result.Op = "update"
	result.NewTAG = newTAG
	result.LinesDelta = countLines(newContent) - countLines(currentContent)
	result.DiffHunks = hunks
	return result
}

func applySectionGroupAtomic(results []SectionResult, indices []int, sections []Section, fs FileSystem, store *SnapshotStore, originalSnapshots map[string]string) {
	firstSec := sections[indices[0]]
	storePath := fs.ResolvePath(firstSec.Path)
	currentContent, readErr := fs.ReadFile(firstSec.Path)
	if readErr != nil {
		for _, idx := range indices {
			results[idx] = SectionResult{Path: sections[idx].Path, OldTAG: sections[idx].TAG, Error: &EditError{Fatal: false, Kind: "file_not_found", Message: "file not found"}}
		}
		return
	}
	type validatedSection struct {
		idx     int
		ops     []Op
		warning string
	}
	var validated []validatedSection
	var allOps []Op
	for _, idx := range indices {
		mappedOps, warning, err := validateTAGAndRecover(sections[idx], storePath, currentContent, store, originalSnapshots)
		if err != nil {
			for _, si := range indices {
				results[si] = SectionResult{Path: sections[si].Path, OldTAG: sections[si].TAG, Error: &EditError{Fatal: false, Kind: "tag_mismatch", Message: fmt.Sprintf("All changes to %q rejected: %s. Re-read.", firstSec.Path, err.Error())}}
			}
			return
		}
		validated = append(validated, validatedSection{idx: idx, ops: mappedOps, warning: warning})
		allOps = append(allOps, mappedOps...)
	}
	if len(allOps) == 0 {
		for _, idx := range indices {
			results[idx] = SectionResult{Path: sections[idx].Path, OldTAG: sections[idx].TAG, Error: &EditError{Fatal: false, Kind: "invalid_args", Message: "no valid ops"}}
		}
		return
	}
		// Normalize Windows line endings before splitting.
	currentContent = strings.ReplaceAll(currentContent, "\r\n", "\n")
	lines := strings.Split(currentContent, "\n")
	if strings.HasSuffix(currentContent, "\n") && len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if err := resolveContentOps(lines, &allOps); err != nil {
		for _, idx := range indices {
			results[idx] = SectionResult{Path: sections[idx].Path, OldTAG: sections[idx].TAG, Error: &EditError{Fatal: false, Kind: "invalid_args", Message: err.Error()}}
		}
		return
	}
	normalizeInsOps(lines, &allOps)
	if err := detectOverlaps(allOps); err != nil {
		for _, idx := range indices {
			results[idx] = SectionResult{Path: sections[idx].Path, OldTAG: sections[idx].TAG, Error: &EditError{Fatal: false, Kind: "invalid_args", Message: err.Error()}}
		}
		return
	}
	newContent, allHunks, err := applyEdits(currentContent, allOps)
	if err != nil {
		for _, idx := range indices {
			results[idx] = SectionResult{Path: sections[idx].Path, OldTAG: sections[idx].TAG, Error: &EditError{Fatal: false, Kind: "invalid_args", Message: err.Error()}}
		}
		return
	}
	if err := fs.WriteFile(firstSec.Path, newContent); err != nil {
		for _, idx := range indices {
			results[idx] = SectionResult{Path: sections[idx].Path, OldTAG: sections[idx].TAG, Error: &EditError{Fatal: true, Kind: "permission_denied", Message: err.Error()}}
		}
		return
	}
	newTAG := sections[indices[0]].TAG
	if store != nil {
		newTAG = store.Update(storePath, newContent)
	}
	totalDelta := countLines(newContent) - countLines(currentContent)
	opOffset := 0
	for _, vs := range validated {
		sectionHunks := extractHunksForOps(allHunks, opOffset, len(vs.ops))
		opOffset += len(vs.ops)
		results[vs.idx] = SectionResult{Path: firstSec.Path, OldTAG: sections[vs.idx].TAG, NewTAG: newTAG, Op: "update", LinesDelta: totalDelta, DiffHunks: sectionHunks, Warning: vs.warning}
	}
}

func validateTAGAndRecover(sec Section, storePath, currentContent string, store *SnapshotStore, originalSnapshots map[string]string) ([]Op, string, error) {
	if store == nil {
		return sec.Ops, "", nil
	}
	_, verifyErr := store.Verify(storePath, sec.TAG, currentContent)
	if verifyErr == nil {
		return sec.Ops, "", nil
	}
	snapContent := ""
	if orig, ok := originalSnapshots[storePath]; ok {
		snapContent = orig
	} else if snap, ok := store.Get(storePath); ok && snap.TAG == sec.TAG {
		snapContent = snap.Content
	}
	if snapContent == "" {
		return nil, "", fmt.Errorf("TAG mismatch: no snapshot")
	}
	recovery := RecoverOps(snapContent, currentContent, sec.Ops)
	if !recovery.Success {
		reason := "unknown"
		if len(recovery.Warnings) > 0 {
			reason = strings.Join(recovery.Warnings, "; ")
		}
		return nil, "", fmt.Errorf("recovery failed (%s)", reason)
	}
	warning := fmt.Sprintf("TAG expired, auto-recovered: %v", verifyErr)
	if recovery.RemapSummary != "" {
		warning += " (" + recovery.RemapSummary + ")"
	}
	return recovery.MappedOps, warning, nil
}

func extractHunksForOps(allHunks []EditHunk, offset, count int) []EditHunk {
	if offset >= len(allHunks) {
		return nil
	}
	end := offset + count
	if end > len(allHunks) {
		end = len(allHunks)
	}
	return allHunks[offset:end]
}

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
// applyEdits
// ---------------------------------------------------------------------------

type editSpan struct {
	op         Op
	start, end int
}

func applyEdits(content string, ops []Op) (string, []EditHunk, error) {
	lines := strings.Split(content, "\n")
	hasTrailingNewline := strings.HasSuffix(content, "\n")
	for _, op := range ops {
		switch op.Kind {
		case OpSWAP, OpDEL:
			if op.LineEnd > len(lines) {
				return "", nil, fmt.Errorf("line %d out of range (file has %d lines)", op.LineEnd, len(lines))
			}
		case OpINS:
			if op.LineStart > len(lines) {
				return "", nil, fmt.Errorf("INS line %d out of range (file has %d lines)", op.LineStart, len(lines))
			}
		}
	}
	var origSpans []editSpan
	for _, op := range ops {
		origSpans = append(origSpans, opToSpan(op, lines, hasTrailingNewline))
	}
	type posDelta struct{ pos, delta int }
	var deltas []posDelta
	var appliedSpans []editSpan
	originalLines := make([]string, len(lines))
	copy(originalLines, lines)
	for _, sp := range origSpans {
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
			deltas = append(deltas, posDelta{pos: offsetSp.end, delta: -(offsetSp.end - offsetSp.start)})
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
	// Preserve the original file's trailing newline convention:
	// if the original ended with \n, ensure the result does too;
	// otherwise, let the edits determine the trailing state.
	if hasTrailingNewline {
		if len(lines) == 0 || lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n"), buildEditHunksFromApplied(originalLines, origSpans, appliedSpans), nil
}

func ApplyEditsForTest(content string, ops []Op) (string, []EditHunk, error) {
	return applyEdits(content, ops)
}

// ---------------------------------------------------------------------------
// overlap / cross-section
// ---------------------------------------------------------------------------

type lineRange struct{ start, end int }

func detectOverlaps(ops []Op) error {
	for i := 0; i < len(ops); i++ {
		for j := i + 1; j < len(ops); j++ {
			ri, rj := opRange(ops[i]), opRange(ops[j])
			if ri == nil || rj == nil {
				continue
			}
			if rangesOverlap(ri.start, ri.end, rj.start, rj.end) {
				return fmt.Errorf("overlapping operations on lines %d-%d: %s (op %d) and %s (op %d); split",
					overlapStart(ri.start, rj.start), overlapEnd(ri.end, rj.end), ops[i].Kind, i+1, ops[j].Kind, j+1)
			}
		}
	}
	return nil
}

func opRange(op Op) *lineRange {
	switch op.Kind {
	case OpSWAP:
		if op.LineStart == 0 && op.OldString != "" {
			return nil
		} // 纯内容模式,无行号范围
		return &lineRange{start: op.LineStart - 1, end: op.LineEnd}
	case OpDEL:
		return &lineRange{start: op.LineStart - 1, end: op.LineEnd}
	case OpINS:
		// INS ops may have LineStart set (after normalizeInsOps) or only RefLine (raw).
		// Fall back to RefLine when LineStart is unset.
		line := op.LineStart
		if line == 0 {
			line = op.RefLine
		}
		// INS.TAIL (RefLine=-1) can't overlap with known line numbers.
		if line < 0 {
			return nil
		}
		return &lineRange{start: line, end: line}
	default:
		return nil
	}
}

func detectCrossSectionConflicts(patch *Patch) map[int]*EditError {
	groups := make(map[string][]int)
	for i, sec := range patch.Sections {
		groups[sec.Path] = append(groups[sec.Path], i)
	}
	conflicts := make(map[int]*EditError)
	for path, indices := range groups {
		if len(indices) <= 1 {
			continue
		}
		type opWithSrc struct {
			op       Op
			secIndex int
		}
		var allOps []opWithSrc
		for _, si := range indices {
			for _, op := range patch.Sections[si].Ops {
				allOps = append(allOps, opWithSrc{op: op, secIndex: si + 1})
			}
		}
		hasOverlap := false
		var detail string
		for i := 0; i < len(allOps); i++ {
			for j := i + 1; j < len(allOps); j++ {
				ri, rj := opRange(allOps[i].op), opRange(allOps[j].op)
				if ri == nil || rj == nil {
					continue
				}
				if rangesOverlap(ri.start, ri.end, rj.start, rj.end) {
					hasOverlap = true
					detail = fmt.Sprintf("%s (section %d, lines %d-%d) and %s (section %d, lines %d-%d) overlap",
						allOps[i].op.Kind, allOps[i].secIndex, ri.start+1, ri.end,
						allOps[j].op.Kind, allOps[j].secIndex, rj.start+1, rj.end)
					break
				}
			}
			if hasOverlap {
				break
			}
		}
		if hasOverlap {
			for _, si := range indices {
				conflicts[si] = &EditError{Fatal: false, Kind: "invalid_args", Message: fmt.Sprintf("cross-section conflict in %q: %s.", path, detail)}
			}
		}
	}
	if len(conflicts) == 0 {
		return nil
	}
	return conflicts
}

func rangesOverlap(aStart, aEnd, bStart, bEnd int) bool { return aStart < bEnd && bStart < aEnd }
func overlapStart(a, b int) int {
	if a < b {
		return a + 1
	}
	return b + 1
}
func overlapEnd(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// opToSpan / hunks
// ---------------------------------------------------------------------------

func opToSpan(op Op, lines []string, hasTrailingNewline bool) editSpan {
	switch op.Kind {
	case OpDEL:
		return editSpan{op: op, start: op.LineStart - 1, end: op.LineEnd}
	case OpSWAP:
		return editSpan{op: op, start: op.LineStart - 1, end: op.LineEnd}
	case OpINS:
		start := op.LineStart
		// Fall back to RefLine only when it's a positive (non-special) value.
		// normalizeInsOps may set LineStart=0 for INS.TAIL on empty files;
		// RefLine=-1 / 0 are INS.TAIL / INS.HEAD specials that shouldn't override.
		if start == 0 && op.RefLine > 0 {
			start = op.RefLine
		}
		return editSpan{op: op, start: start, end: start}
	}
	return editSpan{}
}

const hunkContextLines = 2

func appendContextBefore(lines []EditLine, origLines []string, spanStart int) []EditLine {
	ctxStart := spanStart - hunkContextLines
	if ctxStart < 0 {
		ctxStart = 0
	}
	for j := ctxStart; j < spanStart && j < len(origLines); j++ {
		lines = append(lines, EditLine{Kind: LineCtx, Content: origLines[j], OldNum: j + 1})
	}
	return lines
}

func appendContextAfter(lines []EditLine, origLines []string, spanEnd int) []EditLine {
	ctxEnd := spanEnd + hunkContextLines
	if ctxEnd > len(origLines) {
		ctxEnd = len(origLines)
	}
	for j := spanEnd; j < ctxEnd; j++ {
		lines = append(lines, EditLine{Kind: LineCtx, Content: origLines[j], OldNum: j + 1})
	}
	return lines
}

func buildEditHunksFromApplied(origLines []string, origSpans, appliedSpans []editSpan) []EditHunk {
	var hunks []EditHunk
	for i := range origSpans {
		orig, offset := origSpans[i], appliedSpans[i]
		switch orig.op.Kind {
		case OpDEL:
			hunk := EditHunk{OldStart: orig.start + 1, OldCount: orig.end - orig.start, NewStart: offset.start + 1, NewCount: 0}
			hunk.Lines = appendContextBefore(hunk.Lines, origLines, orig.start)
			for j := orig.start; j < orig.end && j < len(origLines); j++ {
				hunk.Lines = append(hunk.Lines, EditLine{Kind: LineDel, Content: origLines[j], OldNum: j + 1})
			}
			hunk.Lines = appendContextAfter(hunk.Lines, origLines, orig.end)
			if len(hunk.Lines) > 0 {
				hunks = append(hunks, hunk)
			}
		case OpSWAP:
			bodyLines := orig.op.Body
			hunk := EditHunk{OldStart: orig.start + 1, OldCount: orig.end - orig.start, NewStart: offset.start + 1, NewCount: len(bodyLines)}
			hunk.Lines = appendContextBefore(hunk.Lines, origLines, orig.start)
			for j := orig.start; j < orig.end && j < len(origLines); j++ {
				hunk.Lines = append(hunk.Lines, EditLine{Kind: LineDel, Content: origLines[j], OldNum: j + 1})
			}
			for j, bl := range bodyLines {
				hunk.Lines = append(hunk.Lines, EditLine{Kind: LineAdd, Content: bl, NewNum: offset.start + 1 + j})
			}
			hunk.Lines = appendContextAfter(hunk.Lines, origLines, orig.end)
			hunks = append(hunks, hunk)
		case OpINS:
			bodyLines := orig.op.Body
			hunk := EditHunk{OldStart: offset.start + 1, OldCount: 0, NewStart: offset.start + 1, NewCount: len(bodyLines)}
			hunk.Lines = appendContextBefore(hunk.Lines, origLines, orig.start)
			for j, bl := range bodyLines {
				hunk.Lines = append(hunk.Lines, EditLine{Kind: LineAdd, Content: bl, NewNum: offset.start + 1 + j})
			}
			hunk.Lines = appendContextAfter(hunk.Lines, origLines, orig.start)
			hunks = append(hunks, hunk)
		}
	}
	return hunks
}
