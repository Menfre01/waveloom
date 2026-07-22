// Package permission — 路径验证（
//
// 提供：
// - PATH_EXTRACTORS: 30+ 命令的路径提取器（per-command 参数解析）
// - -- end-of-options 处理
// - 复合命令 cd 检测
// - 安全包装器剥离（timeout/nice/nohup/env）
// - 危险删除路径检查
package permission

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Menfre01/waveloom/pkg/bash"
)

// ============================================================================
// FileOperationType
// ============================================================================

// FileOperationType 文件操作类型。
type FileOperationType string

const (
	OpRead   FileOperationType = "read"
	OpWrite  FileOperationType = "write"
	OpCreate FileOperationType = "create"
)

// ============================================================================
// PathCommand — 支持的路径命令类型
// ============================================================================

// PathCommand 表示需要路径验证的命令名。
type PathCommand string

const (
	CmdCD      PathCommand = "cd"
	CmdLS      PathCommand = "ls"
	CmdFind    PathCommand = "find"
	CmdMkdir   PathCommand = "mkdir"
	CmdTouch   PathCommand = "touch"
	CmdRm      PathCommand = "rm"
	CmdRmdir   PathCommand = "rmdir"
	CmdMv      PathCommand = "mv"
	CmdCp      PathCommand = "cp"
	CmdCat     PathCommand = "cat"
	CmdHead    PathCommand = "head"
	CmdTail    PathCommand = "tail"
	CmdSort    PathCommand = "sort"
	CmdUniq    PathCommand = "uniq"
	CmdWc      PathCommand = "wc"
	CmdCut     PathCommand = "cut"
	CmdPaste   PathCommand = "paste"
	CmdColumn  PathCommand = "column"
	CmdTr      PathCommand = "tr"
	CmdFile    PathCommand = "file"
	CmdStat    PathCommand = "stat"
	CmdDiff    PathCommand = "diff"
	CmdAwk     PathCommand = "awk"
	CmdStrings PathCommand = "strings"
	CmdHexdump PathCommand = "hexdump"
	CmdOd      PathCommand = "od"
	CmdBase64  PathCommand = "base64"
	CmdNl      PathCommand = "nl"
	CmdGrep    PathCommand = "grep"
	CmdRg      PathCommand = "rg"
	CmdSed     PathCommand = "sed"
	CmdGit     PathCommand = "git"
	CmdJq      PathCommand = "jq"
	CmdSHA256  PathCommand = "sha256sum"
	CmdSHA1    PathCommand = "sha1sum"
	CmdMD5     PathCommand = "md5sum"
)

// supportedPathCommands 是所有支持路径验证的命令集合。
var supportedPathCommands = map[string]PathCommand{
	"cd": CmdCD, "ls": CmdLS, "find": CmdFind,
	"mkdir": CmdMkdir, "touch": CmdTouch, "rm": CmdRm, "rmdir": CmdRmdir,
	"mv": CmdMv, "cp": CmdCp, "cat": CmdCat, "head": CmdHead, "tail": CmdTail,
	"sort": CmdSort, "uniq": CmdUniq, "wc": CmdWc, "cut": CmdCut,
	"paste": CmdPaste, "column": CmdColumn, "tr": CmdTr,
	"file": CmdFile, "stat": CmdStat, "diff": CmdDiff, "awk": CmdAwk,
	"strings": CmdStrings, "hexdump": CmdHexdump, "od": CmdOd,
	"base64": CmdBase64, "nl": CmdNl, "grep": CmdGrep, "rg": CmdRg,
	"sed": CmdSed, "git": CmdGit, "jq": CmdJq,
	"sha256sum": CmdSHA256, "sha1sum": CmdSHA1, "md5sum": CmdMD5,
}

// IsSupportedPathCommand 检查命令是否为路径受限命令。
func IsSupportedPathCommand(cmd string) bool {
	_, ok := supportedPathCommands[cmd]
	return ok
}

// ============================================================================
// PathExtractor / PATH_EXTRACTORS
// ============================================================================

// PathExtractor 从命令参数中提取路径列表。
type PathExtractor func(args []string) []string

// PATH_EXTRACTORS per-command 路径提取器。
var PATH_EXTRACTORS = map[PathCommand]PathExtractor{
	CmdCD:      extractCD,
	CmdLS:      extractSimple,
	CmdFind:    extractFind,
	CmdMkdir:   extractSimple,
	CmdTouch:   extractSimple,
	CmdRm:      extractSimple,
	CmdRmdir:   extractSimple,
	CmdMv:      extractSimple,
	CmdCp:      extractSimple,
	CmdCat:     extractSimple,
	CmdHead:    extractSimple,
	CmdTail:    extractSimple,
	CmdSort:    extractSimple,
	CmdUniq:    extractSimple,
	CmdWc:      extractSimple,
	CmdCut:     extractSimple,
	CmdPaste:   extractSimple,
	CmdColumn:  extractSimple,
	CmdFile:    extractSimple,
	CmdStat:    extractSimple,
	CmdDiff:    extractSimple,
	CmdAwk:     extractSimple,
	CmdStrings: extractSimple,
	CmdHexdump: extractSimple,
	CmdOd:      extractSimple,
	CmdBase64:  extractSimple,
	CmdNl:      extractSimple,
	CmdSHA256:  extractSimple,
	CmdSHA1:    extractSimple,
	CmdMD5:     extractSimple,
	CmdTr:      extractTr,
	CmdGrep:    extractGrep,
	CmdRg:      extractRg,
	CmdSed:     extractSed,
	CmdJq:      extractJq,
	CmdGit:     extractGit,
}

// CommandOpType 返回命令的操作类型。
var CommandOpType = map[PathCommand]FileOperationType{
	CmdCD: OpRead, CmdLS: OpRead, CmdFind: OpRead,
	CmdMkdir: OpCreate, CmdTouch: OpCreate,
	CmdRm: OpWrite, CmdRmdir: OpWrite, CmdMv: OpWrite, CmdCp: OpWrite,
	CmdCat: OpRead, CmdHead: OpRead, CmdTail: OpRead,
	CmdSort: OpRead, CmdUniq: OpRead, CmdWc: OpRead,
	CmdCut: OpRead, CmdPaste: OpRead, CmdColumn: OpRead,
	CmdTr: OpRead, CmdFile: OpRead, CmdStat: OpRead,
	CmdDiff: OpRead, CmdAwk: OpRead, CmdStrings: OpRead,
	CmdHexdump: OpRead, CmdOd: OpRead, CmdBase64: OpRead,
	CmdNl: OpRead, CmdGrep: OpRead, CmdRg: OpRead,
	CmdSed: OpWrite, CmdGit: OpRead, CmdJq: OpRead,
	CmdSHA256: OpRead, CmdSHA1: OpRead, CmdMD5: OpRead,
}

// ============================================================================
// filterOutFlags — 通用标志过滤（含 -- 处理）
// ============================================================================

func filterOutFlags(args []string) []string {
	var result []string
	afterDD := false
	for _, arg := range args {
		if afterDD {
			result = append(result, arg)
		} else if arg == "--" {
			afterDD = true
		} else if !strings.HasPrefix(arg, "-") {
			result = append(result, arg)
		}
	}
	return result
}

// ============================================================================
// 各命令的路径提取器
// ============================================================================

func extractSimple(args []string) []string { return filterOutFlags(args) }

func extractCD(args []string) []string {
	if len(args) == 0 {
		home, _ := os.UserHomeDir()
		if home == "" {
			home = "~"
		}
		return []string{home}
	}
	return []string{strings.Join(args, " ")}
}

var findPathFlags = map[string]bool{
	"-newer": true, "-anewer": true, "-cnewer": true, "-mnewer": true,
	"-samefile": true, "-path": true, "-wholename": true,
	"-ilname": true, "-lname": true, "-ipath": true, "-iwholename": true,
}

func extractFind(args []string) []string {
	var paths []string
	foundNonGlobal, afterDD := false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if afterDD {
			paths = append(paths, arg)
			continue
		}
		if arg == "--" {
			afterDD = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if arg == "-H" || arg == "-L" || arg == "-P" {
				continue
			}
			foundNonGlobal = true
			if findPathFlags[arg] || (len(arg) == 8 && strings.HasPrefix(arg, "-newer")) {
				if i+1 < len(args) {
					paths = append(paths, args[i+1])
					i++
				}
			}
			continue
		}
		if !foundNonGlobal {
			paths = append(paths, arg)
		}
	}
	if len(paths) == 0 {
		return []string{"."}
	}
	return paths
}

func extractGrep(args []string) []string {
	fa := map[string]bool{
		"-e": true, "--regexp": true, "-f": true, "--file": true,
		"--exclude": true, "--include": true,
		"--exclude-dir": true, "--include-dir": true,
		"-m": true, "--max-count": true,
		"-A": true, "--after-context": true,
		"-B": true, "--before-context": true,
		"-C": true, "--context": true,
	}
	paths := extractPatternCmd(args, fa, nil)
	if len(paths) == 0 {
		for _, a := range args {
			if a == "-r" || a == "-R" || a == "--recursive" {
				return []string{"."}
			}
		}
	}
	return paths
}

func extractRg(args []string) []string {
	fa := map[string]bool{
		"-e": true, "--regexp": true, "-f": true, "--file": true,
		"-t": true, "--type": true, "-T": true, "--type-not": true,
		"-g": true, "--glob": true, "-m": true, "--max-count": true,
		"--max-depth": true, "-r": true, "--replace": true,
		"-A": true, "--after-context": true,
		"-B": true, "--before-context": true,
		"-C": true, "--context": true,
	}
	return extractPatternCmd(args, fa, []string{"."})
}

func extractPatternCmd(args []string, flagsWithArgs map[string]bool, defaults []string) []string {
	var paths []string
	patternFound, afterDD := false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if afterDD {
			if !patternFound {
				patternFound = true
				continue
			}
			paths = append(paths, arg)
			continue
		}
		if arg == "--" {
			afterDD = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			flag := strings.SplitN(arg, "=", 2)[0]
			if flagsWithArgs[flag] {
				patternFound = true
				if !strings.Contains(arg, "=") {
					i++
				}
			}
			continue
		}
		if !patternFound {
			patternFound = true
			continue
		}
		paths = append(paths, arg)
	}
	if len(paths) == 0 && defaults != nil {
		return defaults
	}
	return paths
}

func extractSed(args []string) []string {
	var paths []string
	skipNext, scriptFound, afterDD := false, false, false
	for i := 0; i < len(args); i++ {
		if skipNext {
			skipNext = false
			continue
		}
		arg := args[i]
		if afterDD {
			paths = append(paths, arg)
			continue
		}
		if arg == "--" {
			afterDD = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if arg == "-f" || arg == "--file" {
				if i+1 < len(args) {
					paths = append(paths, args[i+1])
					skipNext = true
				}
				scriptFound = true
			} else if arg == "-e" || arg == "--expression" {
				skipNext = true
				scriptFound = true
			} else if strings.ContainsRune(arg, 'e') || strings.ContainsRune(arg, 'f') {
				scriptFound = true
			}
			continue
		}
		if !scriptFound {
			scriptFound = true
			continue
		}
		paths = append(paths, arg)
	}
	return paths
}

func extractJq(args []string) []string {
	var paths []string
	fa := map[string]bool{
		"-e": true, "--expression": true, "-f": true, "--from-file": true,
		"--arg": true, "--argjson": true, "--slurpfile": true, "--rawfile": true,
		"--args": true, "--jsonargs": true, "-L": true, "--library-path": true,
		"--indent": true, "--tab": true,
	}
	filterFound, afterDD := false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if afterDD {
			if !filterFound {
				filterFound = true
				continue
			}
			paths = append(paths, arg)
			continue
		}
		if arg == "--" {
			afterDD = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			flag := strings.SplitN(arg, "=", 2)[0]
			if flag == "-e" || flag == "--expression" {
				filterFound = true
			}
			if fa[flag] && !strings.Contains(arg, "=") {
				i++
			}
			continue
		}
		if !filterFound {
			filterFound = true
			continue
		}
		paths = append(paths, arg)
	}
	return paths
}

func extractTr(args []string) []string {
	hasDelete := false
	for _, a := range args {
		if a == "-d" || a == "--delete" || (strings.HasPrefix(a, "-") && strings.ContainsRune(a, 'd')) {
			hasDelete = true
			break
		}
	}
	nonFlags := filterOutFlags(args)
	if hasDelete {
		if len(nonFlags) > 0 {
			return nonFlags[1:]
		}
		return nil
	}
	if len(nonFlags) >= 2 {
		return nonFlags[2:]
	}
	return nil
}

func extractGit(args []string) []string {
	if len(args) >= 1 && args[0] == "diff" {
		for _, a := range args {
			if a == "--no-index" {
				p := filterOutFlags(args[1:])
				if len(p) > 2 {
					p = p[:2]
				}
				return p
			}
		}
	}
	return nil
}

// ============================================================================
// Safe Wrapper 剥离
// ============================================================================

var safeWrappers = map[string]bool{
	"timeout": true, "nice": true, "nohup": true, "time": true, "env": true,
}

func stripSafeWrappers(cmd string) string {
	tokens := strings.Fields(strings.TrimSpace(cmd))
	if len(tokens) == 0 {
		return cmd
	}
	i := 0
	for i < len(tokens) && strings.Contains(tokens[i], "=") {
		i++
	}
	for i < len(tokens) && safeWrappers[tokens[i]] {
		i++
		if i < len(tokens) && strings.HasPrefix(tokens[i], "-") {
			i++
		} else if i < len(tokens) && isNumericStr(tokens[i]) {
			i++
		}
		if i < len(tokens) && tokens[i] == "--" {
			i++
		}
	}
	if i >= len(tokens) {
		return cmd
	}
	return strings.Join(tokens[i:], " ")
}

func isNumericStr(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ============================================================================
// 复合命令 cd 检测
// ============================================================================


// ============================================================================
// 危险删除路径
// ============================================================================

var dangerousRemovalPaths = []string{
	string(filepath.Separator),
	filepath.Join(string(filepath.Separator), "etc"),
	filepath.Join(string(filepath.Separator), "usr"),
	filepath.Join(string(filepath.Separator), "boot"),
	filepath.Join(string(filepath.Separator), "dev"),
	filepath.Join(string(filepath.Separator), "proc"),
	filepath.Join(string(filepath.Separator), "sys"),
	filepath.Join(string(filepath.Separator), "bin"),
	filepath.Join(string(filepath.Separator), "sbin"),
	filepath.Join(string(filepath.Separator), "lib"),
	filepath.Join(string(filepath.Separator), "System"),
	filepath.Join(string(filepath.Separator), "home"),
	filepath.Join(string(filepath.Separator), "root"),
	filepath.Join(string(filepath.Separator), "var"),
	filepath.Join(string(filepath.Separator), "opt"),
	filepath.Join(string(filepath.Separator), "tmp"),
}

func isDangerousRemoval(absPath string) bool {
	clean := filepath.Clean(absPath)
	for _, dp := range dangerousRemovalPaths {
		if clean == dp || clean == dp+string(filepath.Separator) {
			return true
		}
	}
	return false
}

// ============================================================================
// PathValidationResult / ValidatePathCommand
// ============================================================================

// PathValidationResult 包含路径验证的结果。
type PathValidationResult struct {
	Allowed     bool
	Blocked     bool
	Message     string
	BlockedPath string
	OpType      FileOperationType
}

// ValidatePathCommand 验证命令的路径安全性。
func ValidatePathCommand(cmd, cwd string, workingDirs []string, compoundHasCd bool) PathValidationResult {
	stripped := stripSafeWrappers(cmd)
	ci := bash.ParseLenient(stripped)
	if ci == nil || ci.BaseCommand == "" {
		return PathValidationResult{Allowed: true}
	}
	pc, ok := supportedPathCommands[ci.BaseCommand]
	if !ok {
		return PathValidationResult{Allowed: true}
	}
	if (pc == CmdMv || pc == CmdCp) && hasFlagsInArgs(ci.Args) {
		return PathValidationResult{
			Allowed: false,
			Message: string(pc) + " with flags requires manual approval to ensure path safety",
		}
	}
	opType := CommandOpType[pc]

	// sed 只读检测：sed -n '10p' file → 降级为 read 操作
	if pc == CmdSed && IsSedReadOnly(stripped) {
		opType = OpRead
	}

	if compoundHasCd && opType != OpRead {
		return PathValidationResult{
			Allowed: false,
			Message: "Commands that change directories and perform write operations require explicit approval",
		}
	}

	extractor := PATH_EXTRACTORS[pc]
	paths := extractor(ci.Args)
	for _, path := range paths {
		resolved := resolvePath(path, cwd)
		if (pc == CmdRm || pc == CmdRmdir) && isDangerousRemoval(resolved) {
			return PathValidationResult{
				Allowed: false, Blocked: true, BlockedPath: resolved,
				Message: "Dangerous " + string(pc) + " operation detected: '" + resolved + "'",
				OpType:  opType,
			}
		}
		check := PathSafetyCheck(resolved, workingDirs)
		if check.Level == PathDangerous {
			return PathValidationResult{
				Allowed: false, BlockedPath: resolved,
				Message: check.Message, OpType: opType,
			}
		}
	}
	return PathValidationResult{Allowed: true, OpType: opType}
}

func resolvePath(path, cwd string) string {
	if strings.HasPrefix(path, "~") {
		home, _ := os.UserHomeDir()
		if home != "" {
			path = filepath.Join(home, path[1:])
		}
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(cwd, path))
}

func hasFlagsInArgs(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			return true
		}
	}
	return false
}

// String 返回 PathCommand 的字符串表示。
func (pc PathCommand) String() string { return string(pc) }
