package tool

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ---------------------------------------------------------------------------
// 常量
// ---------------------------------------------------------------------------

const (
	// MaxReadBytes 是 read_file 单次返回的最大字节数。
	// 100KB ≈ 30K tokens（英文代码），对于绝大多数源文件足够。
	MaxReadBytes = 100 << 10 // 100KB

	// FastPathMaxSize 是小文件快速路径的阈值。
	// 小于此值的普通文件一次 ReadFile 读完，避免逐行 Scan 的异步开销。
	FastPathMaxSize = 10 << 20 // 10MB

	MaxWriteBytes  = 500 << 10 // 500KB — write_file 拒绝写入超过此大小的内容
	MaxShellOutput = 100 << 10 // 100KB — shell 输出截断阈值
	MaxShellLines  = 3000      // shell 输出最大行数
	MaxLineBytes   = 4096      // shell 输出单行最大字节数，超长行截断
)

// ---------------------------------------------------------------------------
// 跳过的目录
// ---------------------------------------------------------------------------

var skipDirs = map[string]bool{
	".git":         true,
	".svn":         true,
	".hg":          true,
	"node_modules": true,
	".claude":      true,
	"__pycache__":  true,
	".DS_Store":    true,
}

// ---------------------------------------------------------------------------
// 设备文件黑名单 — 读取这些路径会永远阻塞（无 EOF）
// ---------------------------------------------------------------------------

var blockedDevicePaths = map[string]bool{
	"/dev/zero":    true,
	"/dev/random":  true,
	"/dev/urandom": true,
	"/dev/full":    true,
	"/dev/stdin":   true,
	"/dev/tty":     true,
	"/dev/console": true,
	"/dev/stdout":  true,
	"/dev/stderr":  true,
}

func init() {
	if runtime.GOOS == "windows" {
		blockedDevicePaths[`\\.\NUL`] = true
		blockedDevicePaths[`\\.\CON`] = true
		blockedDevicePaths[`\\.\CONIN$`] = true
		blockedDevicePaths[`\\.\CONOUT$`] = true
		blockedDevicePaths[`\\.\PhysicalDrive0`] = true
		blockedDevicePaths[`\\.\PhysicalDrive1`] = true
		blockedDevicePaths["NUL"] = true
		blockedDevicePaths["CON"] = true
		blockedDevicePaths["PRN"] = true
		blockedDevicePaths["AUX"] = true
	}
}

// IsBlockedDevicePath 检查路径是否为阻塞设备文件。
func IsBlockedDevicePath(path string) bool {
	if blockedDevicePaths[path] {
		return true
	}
	// Windows 保留文件名（大小写不敏感，仅取 basename 比较）
	if runtime.GOOS == "windows" && isWindowsReservedName(filepath.Base(path)) {
		return true
	}
	// Windows \\.\ 前缀设备路径
	if runtime.GOOS == "windows" && strings.HasPrefix(path, `\\.\`) {
		return true
	}
	// Linux: /proc/self/fd/0,1,2 是 stdio 别名
	if runtime.GOOS != "windows" && strings.HasPrefix(path, "/proc/") &&
		(strings.HasSuffix(path, "/fd/0") ||
			strings.HasSuffix(path, "/fd/1") ||
			strings.HasSuffix(path, "/fd/2")) {
		return true
	}
	return false
}

// windowsReservedNames 是 Windows 保留文件名（大小写不敏感）。
// 这些文件名在任何目录下都是设备，不可作为普通文件读取。
var windowsReservedNames = map[string]bool{
	"nul":  true,
	"con":  true,
	"prn":  true,
	"aux":  true,
	"com1": true,
	"com2": true,
	"com3": true,
	"com4": true,
	"lpt1": true,
	"lpt2": true,
	"lpt3": true,
}

func isWindowsReservedName(base string) bool {
	// 去掉扩展名再比较（如 "nul.txt" 在 Windows 上也是 NUL 设备）
	name := strings.TrimSuffix(strings.ToLower(base), filepath.Ext(base))
	return windowsReservedNames[name]
}

// ---------------------------------------------------------------------------
// 已知二进制扩展名 — 零 I/O 成本快速拒绝
// ---------------------------------------------------------------------------

var binaryExtensions = map[string]bool{
	".o":     true,
	".a":     true,
	".so":    true,
	".dylib": true,
	".dll":   true,
	".exe":   true,
	".bin":   true,
	".dat":   true,
	".class": true,
	".pyc":   true,
	".pyo":   true,
	".pyd":   true,
	".wasm":  true,
	".zip":   true,
	".tar":   true,
	".gz":    true,
	".bz2":   true,
	".xz":    true,
	".7z":    true,
	".rar":   true,
	".jpg":   true,
	".jpeg":  true,
	".png":   true,
	".gif":   true,
	".bmp":   true,
	".ico":   true,
	".webp":  true,
	".mp3":   true,
	".mp4":   true,
	".avi":   true,
	".mov":   true,
	".wav":   true,
	".flac":  true,
	".ttf":   true,
	".otf":   true,
	".woff":  true,
	".woff2": true,
	".eot":   true,
	".pdf":   true,
}

// HasBinaryExtension 通过扩展名判断文件是否为已知二进制格式。
func HasBinaryExtension(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return binaryExtensions[ext]
}

// ---------------------------------------------------------------------------
// IsWithinDir — 项目边界检查
// ---------------------------------------------------------------------------

func IsWithinDir(path, dir string) bool {
	evalPath, errPath := filepath.EvalSymlinks(path)
	evalDir, errDir := filepath.EvalSymlinks(dir)

	// If path resolution fails, also skip dir resolution to keep both
	// in the same namespace.  This avoids false negatives when path
	// does not exist yet but dir is rooted under a symlink (e.g. /tmp
	// → /private/tmp on macOS), which would cause filepath.Rel to
	// fail on the mismatched prefixes.
	if errPath != nil {
		evalPath = filepath.Clean(path)
		evalDir = filepath.Clean(dir)
	} else if errDir != nil {
		evalDir = filepath.Clean(dir)
	}

	rel, err := filepath.Rel(evalDir, evalPath)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..") && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ---------------------------------------------------------------------------
// IsBinaryByContent — 基于内容检测二进制（前 512 字节中 null 占比 > 30%）
// ---------------------------------------------------------------------------

func IsBinaryByContent(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return false, err
	}
	if n == 0 {
		return false, nil
	}

	nullCount := 0
	for i := 0; i < n; i++ {
		if buf[i] == 0 {
			nullCount++
		}
	}
	return float64(nullCount)/float64(n) > 0.30, nil
}

// ---------------------------------------------------------------------------
// IsBinaryFile — 两层检测：先扩展名（零 I/O），再内容
// ---------------------------------------------------------------------------

func IsBinaryFile(path string) (bool, error) {
	if HasBinaryExtension(path) {
		return true, nil
	}
	return IsBinaryByContent(path)
}

// ---------------------------------------------------------------------------
// ShouldSkipDir — 隐藏目录跳过
// ---------------------------------------------------------------------------

func ShouldSkipDir(name string) bool {
	return skipDirs[name] || strings.HasPrefix(name, ".")
}

// ---------------------------------------------------------------------------
// FindSimilarFile — 在目标文件的父目录中查找相似文件名（仅当父目录存在时调用）。
// 返回相对路径（优先相对于 CWD）；未找到足够相似的返回 ""。
// 阈值：max(3, len(name)/4)，避免把无关文件当"相似"建议。
func FindSimilarFile(targetPath string) string {
	dir := filepath.Dir(targetPath)
	name := filepath.Base(targetPath)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	// 收紧阈值：至少需要 75% 以上字符匹配
	threshold := len(name) / 4
	if threshold < 3 {
		threshold = 3
	}
	// 但阈值不能超过文件名长度的 60%，否则只要名字稍长就什么都能匹配
	maxThreshold := len(name) * 6 / 10
	if threshold > maxThreshold {
		threshold = maxThreshold
	}
	if threshold < 1 {
		threshold = 1
	}

	var best string
	bestDist := threshold + 1

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		d := editDistance(name, e.Name())
		if d < bestDist {
			bestDist = d
			best = e.Name()
		}
	}
	if best == "" {
		return ""
	}

	// 返回相对路径（优先相对于 CWD）
	fullPath := filepath.Join(dir, best)
	cwd, _ := os.Getwd()
	if rel, err := filepath.Rel(cwd, fullPath); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return fullPath
}

// editDistance 计算两个字符串的 Levenshtein 距离（简化版，仅用于短字符串）。
func editDistance(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// 保证 a 是较短的字符串
	if len(a) > len(b) {
		a, b = b, a
	}

	prev := make([]int, len(a)+1)
	curr := make([]int, len(a)+1)
	for i := 0; i <= len(a); i++ {
		prev[i] = i
	}

	for j := 1; j <= len(b); j++ {
		curr[0] = j
		for i := 1; i <= len(a); i++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			curr[i] = min3(prev[i]+1, curr[i-1]+1, prev[i-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(a)]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// ---------------------------------------------------------------------------
// SuggestPathUnderCwd — 检查目标文件名是否存在于 CWD 下
// ---------------------------------------------------------------------------

func SuggestPathUnderCwd(targetPath string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	name := filepath.Base(targetPath)
	candidate := filepath.Join(cwd, name)
	if _, err := os.Stat(candidate); err == nil {
		rel, _ := filepath.Rel(cwd, candidate)
		return rel
	}
	return ""
}

// ---------------------------------------------------------------------------
// EstimateTokens — 粗略估算 token 消耗
// 参考 DeepSeek: 1 英文字符 ≈ 0.3 token，1 中文字符 ≈ 0.6 token
// ---------------------------------------------------------------------------

func EstimateTokens(s string) int {
	ascii := 0
	nonASCII := 0
	for _, r := range s {
		if r < 128 {
			ascii++
		} else {
			nonASCII++
		}
	}
	return int(float64(ascii)*0.3 + float64(nonASCII)*0.6)
}

// ---------------------------------------------------------------------------
// formatSize — 格式化文件大小
// ---------------------------------------------------------------------------

func formatSize(size int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
	)
	switch {
	case size >= MB:
		return fmt.Sprintf("%.1fMB", float64(size)/float64(MB))
	case size >= KB:
		return fmt.Sprintf("%.1fKB", float64(size)/float64(KB))
	default:
		return fmt.Sprintf("%dB", size)
	}
}
