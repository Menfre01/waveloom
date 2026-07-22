// Package permission — 只读命令 flag 白名单（
//
// 为 Plan Mode 自动批准提供安全基础：只有使用白名单中安全 flag 的命令才能被自动批准。
// 任何不在白名单中的 flag / 组合都要求用户确认。
package permission

import (
	"strings"

	"github.com/Menfre01/waveloom/pkg/bash"
)

// ============================================================================
// FlagArgType — flag 参数类型
// ============================================================================

// FlagArgType 描述 flag 期望的参数类型。
type FlagArgType string

const (
	FlagNone   FlagArgType = "none"   // 无参数（布尔标志）
	FlagNumber FlagArgType = "number" // 数字参数
	FlagString FlagArgType = "string" // 字符串参数
	FlagEOF    FlagArgType = "EOF"    // EOF 字符串
	FlagChar   FlagArgType = "char"   // 单字符参数
	FlagBrace  FlagArgType = "{}"     // 替换字符串
)

// ============================================================================
// CommandConfig — 命令白名单配置
// ============================================================================

// CommandConfig 定义单个命令的安全 flag 白名单。
type CommandConfig struct {
	SafeFlags          map[string]FlagArgType // 安全 flag → 参数类型
	RespectsDoubleDash bool                   // 是否遵守 POSIX --（默认 true）
}

// ============================================================================
// COMMAND_ALLOWLIST
// ============================================================================

// COMMAND_ALLOWLIST flag 白名单。
// 所有列入的 flag 仅允许读取操作，不得写入文件、执行代码或发起网络请求。
var COMMAND_ALLOWLIST = map[string]CommandConfig{
	// ── grep ──
	"grep": {
		SafeFlags: map[string]FlagArgType{
			"-e": FlagString, "--regexp": FlagString,
			"-f": FlagString, "--file": FlagString,
			"-F": FlagNone, "--fixed-strings": FlagNone,
			"-G": FlagNone, "--basic-regexp": FlagNone,
			"-E": FlagNone, "--extended-regexp": FlagNone,
			"-P": FlagNone, "--perl-regexp": FlagNone,
			"-i": FlagNone, "--ignore-case": FlagNone, "--no-ignore-case": FlagNone,
			"-v": FlagNone, "--invert-match": FlagNone,
			"-w": FlagNone, "--word-regexp": FlagNone,
			"-x": FlagNone, "--line-regexp": FlagNone,
			"-c": FlagNone, "--count": FlagNone,
			"--color": FlagString, "--colour": FlagString,
			"-L": FlagNone, "--files-without-match": FlagNone,
			"-l": FlagNone, "--files-with-matches": FlagNone,
			"-m": FlagNumber, "--max-count": FlagNumber,
			"-o": FlagNone, "--only-matching": FlagNone,
			"-q": FlagNone, "--quiet": FlagNone, "--silent": FlagNone,
			"-s": FlagNone, "--no-messages": FlagNone,
			"-b": FlagNone, "--byte-offset": FlagNone,
			"-H": FlagNone, "--with-filename": FlagNone,
			"-h": FlagNone, "--no-filename": FlagNone, "--label": FlagString,
			"-n": FlagNone, "--line-number": FlagNone,
			"-T": FlagNone, "--initial-tab": FlagNone,
			"-u": FlagNone, "--unix-byte-offsets": FlagNone,
			"-Z": FlagNone, "--null": FlagNone,
			"-z": FlagNone, "--null-data": FlagNone,
			"-A": FlagNumber, "--after-context": FlagNumber,
			"-B": FlagNumber, "--before-context": FlagNumber,
			"-C": FlagNumber, "--context": FlagNumber,
			"--group-separator": FlagString, "--no-group-separator": FlagNone,
			"-a": FlagNone, "--text": FlagNone, "--binary-files": FlagString,
			"-D": FlagString, "--devices": FlagString,
			"-d": FlagString, "--directories": FlagString,
			"--exclude": FlagString, "--exclude-from": FlagString, "--exclude-dir": FlagString,
			"--include": FlagString,
			"-r": FlagNone, "--recursive": FlagNone,
			"-R": FlagNone, "--dereference-recursive": FlagNone,
			"--line-buffered": FlagNone,
			"-U": FlagNone, "--binary": FlagNone,
			"--help": FlagNone, "-V": FlagNone, "--version": FlagNone,
		},
	},

	// ── rg (ripgrep) ──
	"rg": {
		SafeFlags: map[string]FlagArgType{
			"-e": FlagString, "--regexp": FlagString,
			"-f": FlagString, "--file": FlagString,
			"-F": FlagNone, "--fixed-strings": FlagNone,
			"-i": FlagNone, "--ignore-case": FlagNone,
			"-s": FlagNone, "--case-sensitive": FlagNone,
			"-v": FlagNone, "--invert-match": FlagNone,
			"-w": FlagNone, "--word-regexp": FlagNone,
			"-x": FlagNone, "--line-regexp": FlagNone,
			"-c": FlagNone, "--count": FlagNone,
			"-l": FlagNone, "--files-with-matches": FlagNone,
			"--files-without-match": FlagNone,
			"-o": FlagNone, "--only-matching": FlagNone,
			"-n": FlagNone, "--line-number": FlagNone,
			"-N": FlagNone, "--no-line-number": FlagNone,
			"-q": FlagNone, "--quiet": FlagNone,
			"-m": FlagNumber, "--max-count": FlagNumber,
			"-A": FlagNumber, "--after-context": FlagNumber,
			"-B": FlagNumber, "--before-context": FlagNumber,
			"-C": FlagNumber, "--context": FlagNumber,
			"-g": FlagString, "--glob": FlagString,
			"-t": FlagString, "--type": FlagString,
			"-T": FlagString, "--type-not": FlagString,
			"--type-add": FlagString, "--type-clear": FlagString,
			"-j": FlagNumber, "--threads": FlagNumber,
			"--max-depth": FlagNumber, "--min-depth": FlagNumber,
			"--max-filesize": FlagString,
			"--color": FlagString, "--colors": FlagString,
			"--no-heading": FlagNone, "--with-filename": FlagNone,
			"--no-filename": FlagNone, "--heading": FlagNone,
			"-H": FlagNone, "--hidden": FlagNone,
			"-u": FlagNone, "--unrestricted": FlagNone,
			"-L": FlagNone, "--follow": FlagNone,
			"-0": FlagNone, "--null": FlagNone,
			"--null-data": FlagNone,
			"--no-ignore": FlagNone, "--no-ignore-parent": FlagNone,
			"--no-ignore-vcs": FlagNone, "--no-require-git": FlagNone,
			"--ignore-file": FlagString,
			"--sort": FlagString, "--sortr": FlagString,
			"--crlf": FlagNone, "--no-crlf": FlagNone,
			"--trim": FlagNone, "--no-trim": FlagNone,
			"--json": FlagNone, "--stats": FlagNone,
			"-h": FlagNone, "--help": FlagNone,
			"-V": FlagNone, "--version": FlagNone,
		},
	},

	// ── find ──
	"find": {
		SafeFlags: map[string]FlagArgType{
			"-H": FlagNone, "-L": FlagNone, "-P": FlagNone,
			"-name": FlagString, "-iname": FlagString,
			"-path": FlagString, "-ipath": FlagString,
			"-regex": FlagString, "-iregex": FlagString,
			"-type": FlagString, "-xtype": FlagString,
			"-size": FlagString,
			"-maxdepth": FlagNumber, "-mindepth": FlagNumber,
			"-mtime": FlagString, "-atime": FlagString, "-ctime": FlagString,
			"-mmin": FlagString, "-amin": FlagString, "-cmin": FlagString,
			"-newer": FlagString, "-anewer": FlagString, "-cnewer": FlagString,
			"-perm": FlagString,
			"-user": FlagString, "-group": FlagString, "-nouser": FlagNone, "-nogroup": FlagNone,
			"-links": FlagString,
			"-inum": FlagString, "-samefile": FlagString,
			"-depth": FlagNone, "-d": FlagNone,
			"-prune": FlagNone,
			"-print": FlagNone, "-print0": FlagNone,
			"-ls": FlagNone, "-fls": FlagString,
			"-fprint": FlagString, "-fprint0": FlagString,
			"-printf": FlagString, "-fprintf": FlagString,
			"-help": FlagNone, "--help": FlagNone, "-version": FlagNone, "--version": FlagNone,
			// 危险：-exec/-execdir/-ok/-okdir/-delete 不在白名单中
		},
		RespectsDoubleDash: true,
	},

	// ── sort ──
	"sort": {
		SafeFlags: map[string]FlagArgType{
			"-b": FlagNone, "--ignore-leading-blanks": FlagNone,
			"-d": FlagNone, "--dictionary-order": FlagNone,
			"-f": FlagNone, "--ignore-case": FlagNone,
			"-g": FlagNone, "--general-numeric-sort": FlagNone,
			"-h": FlagNone, "--human-numeric-sort": FlagNone,
			"-i": FlagNone, "--ignore-nonprinting": FlagNone,
			"-M": FlagNone, "--month-sort": FlagNone,
			"-n": FlagNone, "--numeric-sort": FlagNone,
			"-R": FlagNone, "--random-sort": FlagNone,
			"-r": FlagNone, "--reverse": FlagNone,
			"--sort": FlagString, "-s": FlagNone, "--stable": FlagNone,
			"-u": FlagNone, "--unique": FlagNone,
			"-V": FlagNone, "--version-sort": FlagNone,
			"-z": FlagNone, "--zero-terminated": FlagNone,
			"-k": FlagString, "--key": FlagString,
			"-t": FlagString, "--field-separator": FlagString,
			"-c": FlagNone, "--check": FlagNone,
			"-C": FlagNone, "--check-char-order": FlagNone,
			"-m": FlagNone, "--merge": FlagNone,
			"-S": FlagString, "--buffer-size": FlagString,
			"--parallel": FlagNumber, "--batch-size": FlagNumber,
			"--help": FlagNone, "--version": FlagNone,
			// 危险：-o/--output 不在白名单中
		},
	},

	// ── sed ──
	"sed": {
		SafeFlags: map[string]FlagArgType{
			"-e": FlagString, "--expression": FlagString,
			"-f": FlagString, "--file": FlagString,
			"-n": FlagNone, "--quiet": FlagNone, "--silent": FlagNone,
			"-r": FlagNone, "--regexp-extended": FlagNone,
			"-E": FlagNone, "--posix": FlagNone,
			"-l": FlagNumber, "--line-length": FlagNumber,
			"-z": FlagNone, "--zero-terminated": FlagNone,
			"-s": FlagNone, "--separate": FlagNone,
			"-u": FlagNone, "--unbuffered": FlagNone,
			"--debug": FlagNone, "--help": FlagNone, "--version": FlagNone,
			// 危险：-i/--in-place 不在白名单中
		},
	},

	// ── ps ──
	"ps": {
		SafeFlags: map[string]FlagArgType{
			"-e": FlagNone, "-A": FlagNone, "-a": FlagNone,
			"-d": FlagNone, "-N": FlagNone, "--deselect": FlagNone,
			"-f": FlagNone, "-F": FlagNone, "-l": FlagNone,
			"-j": FlagNone, "-y": FlagNone,
			"-w": FlagNone, "-ww": FlagNone, "--width": FlagNumber,
			"-c": FlagNone, "-H": FlagNone, "--forest": FlagNone,
			"--headers": FlagNone, "--no-headers": FlagNone,
			"-n": FlagString, "--sort": FlagString,
			"-L": FlagNone, "-T": FlagNone, "-m": FlagNone,
			"-C": FlagString, "-G": FlagString, "-g": FlagString,
			"-p": FlagString, "--pid": FlagString,
			"-q": FlagString, "--quick-pid": FlagString,
			"-s": FlagString, "--sid": FlagString,
			"-t": FlagString, "--tty": FlagString,
			"-U": FlagString, "-u": FlagString, "--user": FlagString,
			"--help": FlagNone, "--info": FlagNone,
			"-V": FlagNone, "--version": FlagNone,
		},
		RespectsDoubleDash: true,
	},

	// ── file ──
	"file": {
		SafeFlags: map[string]FlagArgType{
			"-b": FlagNone, "--brief": FlagNone,
			"-i": FlagNone, "--mime": FlagNone, "--mime-type": FlagNone, "--mime-encoding": FlagNone,
			"--apple": FlagNone,
			"-c": FlagNone, "--check-encoding": FlagNone,
			"--exclude": FlagString, "--exclude-quiet": FlagString,
			"-0": FlagNone, "--print0": FlagNone,
			"-f": FlagString, "-F": FlagString, "--separator": FlagString,
			"--help": FlagNone, "--version": FlagNone, "-v": FlagNone,
			"-h": FlagNone, "--no-dereference": FlagNone,
			"-L": FlagNone, "--dereference": FlagNone,
			"-m": FlagString, "--magic-file": FlagString,
			"-k": FlagNone, "--keep-going": FlagNone,
			"-l": FlagNone, "--list": FlagNone,
			"-n": FlagNone, "--no-buffer": FlagNone,
			"-p": FlagNone, "--preserve-date": FlagNone,
			"-r": FlagNone, "--raw": FlagNone,
			"-s": FlagNone, "--special-files": FlagNone,
			"-z": FlagNone, "--uncompress": FlagNone,
		},
	},

	// ── base64 ──
	"base64": {
		SafeFlags: map[string]FlagArgType{
			"-d": FlagNone, "-D": FlagNone, "--decode": FlagNone,
			"-b": FlagNumber, "--break": FlagNumber,
			"-w": FlagNumber, "--wrap": FlagNumber,
			"-i": FlagString, "--input": FlagString,
			"--ignore-garbage": FlagNone,
			"-h": FlagNone, "--help": FlagNone, "--version": FlagNone,
		},
		RespectsDoubleDash: false,
	},

	// ── checksum commands ──
	"sha256sum": {SafeFlags: map[string]FlagArgType{
		"-b": FlagNone, "--binary": FlagNone, "-t": FlagNone, "--text": FlagNone,
		"-c": FlagNone, "--check": FlagNone, "--ignore-missing": FlagNone,
		"--quiet": FlagNone, "--status": FlagNone, "--strict": FlagNone,
		"-w": FlagNone, "--warn": FlagNone, "--tag": FlagNone,
		"-z": FlagNone, "--zero": FlagNone,
		"--help": FlagNone, "--version": FlagNone,
	}},

	// ── xargs ──（仅安全 flag，禁止 -i/-e）
	"xargs": {
		SafeFlags: map[string]FlagArgType{
			"-I": FlagBrace, "-n": FlagNumber, "-P": FlagNumber,
			"-L": FlagNumber, "-s": FlagNumber, "-E": FlagEOF,
			"-0": FlagNone, "-t": FlagNone, "-r": FlagNone, "-x": FlagNone, "-d": FlagChar,
		},
	},

	// ── man ──
	"man": {
		SafeFlags: map[string]FlagArgType{
			"-a": FlagNone, "--all": FlagNone,
			"-d": FlagNone, "-f": FlagNone, "--whatis": FlagNone,
			"-h": FlagNone, "-k": FlagNone, "--apropos": FlagNone,
			"-l": FlagString,
			"-w": FlagNone, "-S": FlagString, "-s": FlagString,
		},
	},

	// ── netstat ──
	"netstat": {
		SafeFlags: map[string]FlagArgType{
			"-a": FlagNone, "-L": FlagNone, "-l": FlagNone, "-n": FlagNone,
			"-f": FlagString, "-g": FlagNone, "-i": FlagNone, "-I": FlagString,
			"-s": FlagNone, "-r": FlagNone, "-m": FlagNone, "-v": FlagNone,
		},
	},

	// ── lsof ──
	"lsof": {
		SafeFlags: map[string]FlagArgType{
			"-a": FlagNone, "-b": FlagNone, "-C": FlagNone, "-l": FlagNone,
			"-n": FlagNone, "-N": FlagNone, "-O": FlagNone, "-P": FlagNone,
			"-Q": FlagNone, "-R": FlagNone, "-t": FlagNone, "-U": FlagNone,
			"-V": FlagNone, "-X": FlagNone, "-H": FlagNone, "-E": FlagNone,
			"-F": FlagNone, "-g": FlagNone, "-i": FlagNone, "-K": FlagNone,
			"-L": FlagNone, "-o": FlagNone, "-r": FlagNone, "-s": FlagNone,
			"-S": FlagNone, "-T": FlagNone, "-x": FlagNone,
			"-A": FlagString, "-c": FlagString, "-d": FlagString,
			"-e": FlagString, "-k": FlagString, "-p": FlagString, "-u": FlagString,
		},
	},

	// ── fd / fdfind ──
	"fd": {
		SafeFlags: map[string]FlagArgType{
			"-h": FlagNone, "--help": FlagNone, "-V": FlagNone, "--version": FlagNone,
			"-H": FlagNone, "--hidden": FlagNone,
			"-I": FlagNone, "--no-ignore": FlagNone,
			"--no-ignore-vcs": FlagNone, "--no-ignore-parent": FlagNone,
			"-s": FlagNone, "--case-sensitive": FlagNone,
			"-i": FlagNone, "--ignore-case": FlagNone,
			"-g": FlagNone, "--glob": FlagNone,
			"--regex": FlagNone, "-F": FlagNone, "--fixed-strings": FlagNone,
			"-a": FlagNone, "--absolute-path": FlagNone,
			"-L": FlagNone, "--follow": FlagNone,
			"-p": FlagNone, "--full-path": FlagNone,
			"-0": FlagNone, "--print0": FlagNone,
			"-d": FlagNumber, "--max-depth": FlagNumber,
			"--min-depth": FlagNumber, "--exact-depth": FlagNumber,
			"-t": FlagString, "--type": FlagString,
			"-e": FlagString, "--extension": FlagString,
			"-S": FlagString, "--size": FlagString,
			"--changed-within": FlagString, "--changed-before": FlagString,
			"-o": FlagString, "--owner": FlagString,
			"-E": FlagString, "--exclude": FlagString,
			"--ignore-file": FlagString,
			"-c": FlagString, "--color": FlagString,
			"-j": FlagNumber, "--threads": FlagNumber,
			"--max-buffer-time": FlagString, "--max-results": FlagNumber,
			"-1": FlagNone, "-q": FlagNone, "--quiet": FlagNone,
			"--show-errors": FlagNone, "--strip-cwd-prefix": FlagNone,
			"--one-file-system": FlagNone, "--prune": FlagNone,
			"--search-path": FlagString, "--base-directory": FlagString,
			"--path-separator": FlagString, "--batch-size": FlagNumber,
			"--no-require-git": FlagNone, "--hyperlink": FlagString,
		},
	},
	"fdfind": {
		SafeFlags: map[string]FlagArgType{
			"-h": FlagNone, "--help": FlagNone, "-V": FlagNone, "--version": FlagNone,
			"-H": FlagNone, "--hidden": FlagNone,
			"-I": FlagNone, "--no-ignore": FlagNone,
			"-s": FlagNone, "--case-sensitive": FlagNone,
			"-i": FlagNone, "--ignore-case": FlagNone,
			"-g": FlagNone, "--glob": FlagNone,
			"-F": FlagNone, "--fixed-strings": FlagNone,
			"-L": FlagNone, "--follow": FlagNone,
			"-p": FlagNone, "--full-path": FlagNone,
			"-0": FlagNone, "--print0": FlagNone,
			"-d": FlagNumber, "--max-depth": FlagNumber,
			"-t": FlagString, "--type": FlagString,
			"-e": FlagString, "--extension": FlagString,
			"-S": FlagString, "--size": FlagString,
			"-E": FlagString, "--exclude": FlagString,
			"-c": FlagString, "--color": FlagString,
			"-j": FlagNumber, "--threads": FlagNumber,
			"-1": FlagNone, "-q": FlagNone, "--quiet": FlagNone,
			"--prune": FlagNone, "--search-path": FlagString,
		},
	},

	"sha1sum": {SafeFlags: map[string]FlagArgType{
		"-b": FlagNone, "--binary": FlagNone, "-t": FlagNone, "--text": FlagNone,
		"-c": FlagNone, "--check": FlagNone, "--ignore-missing": FlagNone,
		"--quiet": FlagNone, "--status": FlagNone, "--strict": FlagNone,
		"-w": FlagNone, "--warn": FlagNone, "--tag": FlagNone,
		"-z": FlagNone, "--zero": FlagNone,
		"--help": FlagNone, "--version": FlagNone,
	}},
	"md5sum": {SafeFlags: map[string]FlagArgType{
		"-b": FlagNone, "--binary": FlagNone, "-t": FlagNone, "--text": FlagNone,
		"-c": FlagNone, "--check": FlagNone, "--ignore-missing": FlagNone,
		"--quiet": FlagNone, "--status": FlagNone, "--strict": FlagNone,
		"-w": FlagNone, "--warn": FlagNone, "--tag": FlagNone,
		"-z": FlagNone, "--zero": FlagNone,
		"--help": FlagNone, "--version": FlagNone,
	}},

	// ── tree ──
	"tree": {
		SafeFlags: map[string]FlagArgType{
			"-a": FlagNone, "-d": FlagNone, "-l": FlagNone, "-f": FlagNone,
			"-x": FlagNone, "-L": FlagNumber,
			"-P": FlagString, "-I": FlagString,
			"--gitignore": FlagNone, "--gitfile": FlagString,
			"--ignore-case": FlagNone, "--matchdirs": FlagNone, "--metafirst": FlagNone,
			"--prune": FlagNone, "--info": FlagNone, "--infofile": FlagString,
			"--noreport": FlagNone, "--charset": FlagString, "--filelimit": FlagNumber,
			"-q": FlagNone, "-N": FlagNone, "-Q": FlagNone,
			"-p": FlagNone, "-u": FlagNone, "-g": FlagNone,
			"-s": FlagNone, "-h": FlagNone, "--si": FlagNone, "--du": FlagNone,
			"-D": FlagNone, "--timefmt": FlagString,
			"-F": FlagNone, "--inodes": FlagNone, "--device": FlagNone,
			"-v": FlagNone, "-t": FlagNone, "-c": FlagNone, "-U": FlagNone, "-r": FlagNone,
			"--dirsfirst": FlagNone, "--filesfirst": FlagNone, "--sort": FlagString,
			"-i": FlagNone, "-A": FlagNone, "-S": FlagNone,
			"-n": FlagNone, "-C": FlagNone,
			"-X": FlagNone, "-J": FlagNone,
			"-H": FlagString, "--nolinks": FlagNone,
			"--hintro": FlagString, "--houtro": FlagString,
			"-T": FlagString, "--hyperlink": FlagNone,
			"--fromfile": FlagNone, "--fromtabfile": FlagNone, "--fflinks": FlagNone,
			"--help": FlagNone, "--version": FlagNone,
			// 危险：-o/--output, -R 不在白名单中
		},
	},

	// ── date ──
	"date": {
		SafeFlags: map[string]FlagArgType{
			"-d": FlagString, "--date": FlagString,
			"-r": FlagString, "--reference": FlagString,
			"-u": FlagNone, "--utc": FlagNone, "--universal": FlagNone,
			"-I": FlagNone, "--iso-8601": FlagString,
			"-R": FlagNone, "--rfc-email": FlagNone,
			"--rfc-3339": FlagString,
			"--debug": FlagNone, "--help": FlagNone, "--version": FlagNone,
			// 危险：-s/--set, -f/--file 不在白名单中
		},
	},

	// ── hostname ──
	"hostname": {
		SafeFlags: map[string]FlagArgType{
			"-f": FlagNone, "--fqdn": FlagNone, "--long": FlagNone,
			"-s": FlagNone, "--short": FlagNone,
			"-i": FlagNone, "--ip-address": FlagNone,
			"-I": FlagNone, "--all-ip-addresses": FlagNone,
			"-a": FlagNone, "--alias": FlagNone,
			"-d": FlagNone, "--domain": FlagNone,
			"-A": FlagNone, "--all-fqdns": FlagNone,
			"-v": FlagNone, "--verbose": FlagNone,
			"-h": FlagNone, "--help": FlagNone,
			"-V": FlagNone, "--version": FlagNone,
			// 危险：positional args 会设置 hostname
		},
	},
}

// ============================================================================
// ValidateFlagsReadOnly — Plan Mode 只读 flag 验证
// ============================================================================

// FlagValidationResult 包含 flag 验证的结果。
type FlagValidationResult struct {
	Allowed bool
	Message string
}

// ValidateFlagsReadOnly 检查命令的所有 flag 是否在只读白名单中。
// 用于 Plan Mode 自动批准决策。
func ValidateFlagsReadOnly(cmd string) FlagValidationResult {
	ci := bash.ParseLenient(cmd)
	if ci == nil || ci.BaseCommand == "" {
		return FlagValidationResult{Allowed: false, Message: "cannot parse command"}
	}

	config, ok := COMMAND_ALLOWLIST[ci.BaseCommand]
	if !ok {
		return FlagValidationResult{
			Allowed: false,
			Message: "command '" + ci.BaseCommand + "' not in read-only allowlist",
		}
	}

	// 验证每个 flag
	for _, flag := range ci.Flags {
		// 先检查完整 flag 名（长标志如 -exec, -name）
		flagName := strings.SplitN(flag, "=", 2)[0]
		if _, ok := config.SafeFlags[flagName]; ok {
			continue
		}

		// 处理 -abc 组合形式（仅当 flag 为短标志组合：2-3 个小写字母）
		if !strings.HasPrefix(flag, "--") && len(flag) >= 2 && len(flag) <= 4 && isShortFlagCombo(flag[1:]) {
			for _, ch := range flag[1:] {
				single := "-" + string(ch)
				if _, ok := config.SafeFlags[single]; !ok {
					return FlagValidationResult{
						Allowed: false,
						Message: "flag '" + single + "' not in read-only allowlist for '" + ci.BaseCommand + "'",
					}
				}
			}
			continue
		}

		// 完整 flag 名不在白名单 → 拒绝
		return FlagValidationResult{
			Allowed: false,
			Message: "flag '" + flagName + "' not in read-only allowlist for '" + ci.BaseCommand + "'",
		}
	}

	return FlagValidationResult{Allowed: true}
}

// isShortFlagCombo 判断字符串是否为短标志组合（仅小写字母）。
// -la, -rn → true; -exec → false（太长，非组合）。
func isShortFlagCombo(s string) bool {
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}
