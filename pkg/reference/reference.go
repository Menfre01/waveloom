// Package reference 实现 @ 引用语法的解析和展开。
//
// 用户在输入中使用 @path 语法引用文件或目录，
// Expander 在发送到 Agent Loop 之前将其展开为实际内容。
//
// 对标 Cursor/Copilot 的 @file/@folder 上下文引用，
// 展开在客户端确定性完成，不消耗 tool call 轮次。
package reference

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// Kind 表示引用的类型。
type Kind int

const (
	KindFile   Kind = iota // @file — 读取文件内容
	KindFolder             // @folder — 列出目录结构
)

// Ref 表示用户输入中解析出的一个 @ 引用。
type Ref struct {
	Raw  string // 原始文本，如 "@auth/login.go"
	Path string // 解析后的绝对路径
	Kind Kind   // 引用类型（文件或目录）
}

// ResolvedRef 表示一个已解析完成的引用，含展开后的内容。
type ResolvedRef struct {
	Ref
	Content string // 展开后的内容
	Bytes   int    // 内容字节数
	Error   string // 展开失败时的错误信息（空 = 成功）
}

// ---------------------------------------------------------------------------
// Regex
// ---------------------------------------------------------------------------

// atRefPattern 匹配有效 @ 引用：行首或空格后紧跟 @，捕获后续非空白字符。
// 有效: " @file.go", "@file.go"（行首）
// 无效: "email@host.com", "foo@bar"
var atRefPattern = regexp.MustCompile(`(?:^|\s)@([^\s]+)`)

// ---------------------------------------------------------------------------
// Parse
// ---------------------------------------------------------------------------

// parseRefs 从 userInput 中提取所有有效 @ 引用，解析为 []Ref。
// cwd 用于将相对路径转换为绝对路径。
func parseRefs(userInput string, cwd string) []Ref {
	matches := atRefPattern.FindAllStringSubmatch(userInput, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var refs []Ref

	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		rawPath := m[1]
		if rawPath == "" {
			continue
		}

		// Resolve absolute path
		absPath := resolvePath(rawPath, cwd)

		// Determine Kind via os.Stat; fall back to fuzzy prefix matching
		kind := KindFile
		if info, err := os.Stat(absPath); err == nil {
			if info.IsDir() {
				kind = KindFolder
			}
		} else if matchedPath, isDir, ok := fuzzyMatch(absPath); ok {
			absPath = matchedPath
			if isDir {
				kind = KindFolder
			}
		}

		// Dedup by absolute path (after fuzzy resolution)
		if seen[absPath] {
			continue
		}
		seen[absPath] = true

		refs = append(refs, Ref{
			Raw:  "@" + rawPath,
			Path: absPath,
			Kind: kind,
		})
	}

	return refs
}

// ---------------------------------------------------------------------------
// Path resolution
// ---------------------------------------------------------------------------

// resolvePath 将用户输入的路径解析为绝对路径。
// 绝对路径直接返回；相对路径基于 cwd 拼接。
func resolvePath(rawPath string, cwd string) string {
	if filepath.IsAbs(rawPath) {
		return filepath.Clean(rawPath)
	}
	return filepath.Clean(filepath.Join(cwd, rawPath))
}

// ---------------------------------------------------------------------------
// Fuzzy matching
// ---------------------------------------------------------------------------

// fuzzyMatch 在精确路径不存在时，按路径分量逐级进行最小前缀匹配。
//
// 算法：从 absPath 向上找到最近的已存在目录作为起点，然后逐级向下，
// 每个分量在当前目录中按前缀匹配条目，优先精确匹配，否则选名称最短的匹配项。
// 中间分量只匹配目录，末级分量可匹配文件或目录。
func fuzzyMatch(absPath string) (string, bool, bool) {
	// 1. 找到最近的已存在祖先目录，收集需要模糊匹配的分量
	var components []string
	searchDir := absPath

	for {
		info, err := os.Stat(searchDir)
		if err == nil && info.IsDir() {
			break
		}
		parent := filepath.Dir(searchDir)
		if parent == searchDir {
			// 已到达文件系统根且不存在
			return "", false, false
		}
		components = append([]string{filepath.Base(searchDir)}, components...)
		searchDir = parent
	}

	if len(components) == 0 {
		// 路径已精确存在 — 正常情况下 fuzzyMatch 不会被如此调用
		info, _ := os.Stat(absPath)
		return absPath, info.IsDir(), true
	}

	// 2. 从已存在目录出发，逐分量模糊匹配
	currentDir := searchDir
	for i, component := range components {
		isLast := i == len(components)-1
		matchedPath, isDir, ok := fuzzyMatchComponent(currentDir, component, !isLast)
		if !ok {
			return "", false, false
		}
		if isLast {
			return matchedPath, isDir, true
		}
		currentDir = matchedPath
	}

	return "", false, false
}

// fuzzyMatchComponent 在指定目录中按最小前缀匹配单个路径分量。
// dirsOnly=true 时只考虑目录条目（用于中间路径分量）。
func fuzzyMatchComponent(dir, prefix string, dirsOnly bool) (string, bool, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false, false
	}

	// 按类型过滤
	var candidates []os.DirEntry
	for _, e := range entries {
		if dirsOnly && !e.IsDir() {
			continue
		}
		candidates = append(candidates, e)
	}

	if len(candidates) == 0 {
		return "", false, false
	}

	// 1) 精确匹配优先
	for _, e := range candidates {
		if e.Name() == prefix {
			return filepath.Join(dir, e.Name()), e.IsDir(), true
		}
	}

	// 2) 大小写敏感前缀匹配
	var matches []os.DirEntry
	for _, e := range candidates {
		if strings.HasPrefix(e.Name(), prefix) {
			matches = append(matches, e)
		}
	}

	// 3) 无匹配时尝试大小写不敏感
	if len(matches) == 0 {
		prefixLower := strings.ToLower(prefix)
		for _, e := range candidates {
			if strings.HasPrefix(strings.ToLower(e.Name()), prefixLower) {
				matches = append(matches, e)
			}
		}
	}

	if len(matches) == 0 {
		return "", false, false
	}

	// 最小前缀匹配：选名称最短者
	best := matches[0]
	for _, m := range matches[1:] {
		if len(m.Name()) < len(best.Name()) {
			best = m
		}
	}

	return filepath.Join(dir, best.Name()), best.IsDir(), true
}

// ---------------------------------------------------------------------------
// Replace
// ---------------------------------------------------------------------------

// replaceRefs 将原始输入中的 @ 引用替换为展开后的 @@ 围栏块。
// 引用内容块放在消息顶部，原始指令文本（去除 @ref 后）放在下方。
func replaceRefs(userInput string, resolved []ResolvedRef, cwd string) string {
	// 1. Remove all @ref tokens from userInput and capture remaining text fragments
	cleaned := removeAtRefs(userInput)

	// 2. Build context blocks from resolved refs
	var blocks []string
	for _, r := range resolved {
		blocks = append(blocks, formatRefBlock(r, cwd))
	}

	// 3. Assemble: context blocks + separator + user instruction
	if len(blocks) == 0 {
		return cleaned
	}

	contextPart := strings.Join(blocks, "\n\n")
	cleaned = strings.TrimSpace(cleaned)

	if cleaned == "" {
		return contextPart
	}

	return contextPart + "\n\n" + cleaned
}

// removeAtRefs 从输入中移除所有 @ref token，返回清理后的文本。
// 策略：用正则找到每个 @ref 的位置，提取 ref 之间的文本片段。
func removeAtRefs(userInput string) string {
	// Use FindAllStringSubmatchIndex to get positions
	indices := atRefPattern.FindAllStringSubmatchIndex(userInput, -1)
	if len(indices) == 0 {
		return userInput
	}

	var result strings.Builder
	lastEnd := 0

	for _, idx := range indices {
		// idx[0] = start of full match, idx[1] = end of full match
		// The full match includes the preceding space or line-start anchor.
		// We want to preserve the space before @, so we capture from lastEnd to idx[1]-len(@ref)
		fullEnd := idx[1]

		// Write text before the @, preserving the preceding space/line-start
		// leadingText computed below using atPos for correct @-boundary

		// The @ref itself starts at the @ character. Find it.
		// The pattern "(?:^|\\s)@([^\\s]+)" captures:
		//   group 0: full match including leading space/^
		//   group 1: just the path (without @)
		// So idx[2] = start of group 1 (the path), idx[3] = end of group 1
		atPos := idx[2] - 1 // position of @ character (group 1 start minus the @)
		leadingText := userInput[lastEnd:atPos]

		result.WriteString(leadingText)
		lastEnd = fullEnd
	}

	// Write remaining text after last ref
	if lastEnd < len(userInput) {
		result.WriteString(userInput[lastEnd:])
	}

	return strings.TrimSpace(result.String())
}

// formatRefBlock 将一个 ResolvedRef 格式化为 @@ 围栏块。
func formatRefBlock(r ResolvedRef, cwd string) string {
	relPath := filepath.ToSlash(relativePath(r.Path, cwd))

	if r.Error != "" {
		return fmt.Sprintf("@@ %s  [not found]\n@@", r.Raw)
	}

	switch r.Kind {
	case KindFile:
		lang := languageForPath(r.Path)
		if lang != "" {
			return fmt.Sprintf("@@ %s (file)\n```%s\n%s\n```\n@@", relPath, lang, r.Content)
		}
		return fmt.Sprintf("@@ %s (file)\n```\n%s\n```\n@@", relPath, r.Content)

	case KindFolder:
		return fmt.Sprintf("@@ %s (directory)\n```\n%s\n```\n@@", relPath, r.Content)

	default:
		return fmt.Sprintf("@@ %s\n```\n%s\n```\n@@", relPath, r.Content)
	}
}

// relativePath 返回相对于 cwd 的路径，若不可相对化则返回原路径。
func relativePath(absPath string, cwd string) string {
	rel, err := filepath.Rel(cwd, absPath)
	if err != nil {
		return absPath
	}
	// Prefer relative path when it's cleaner
	if !strings.HasPrefix(rel, "..") {
		return rel
	}
	return absPath
}

// ---------------------------------------------------------------------------
// Language mapping
// ---------------------------------------------------------------------------

// languageForPath 根据文件扩展名返回语言标识符，用于 markdown 代码块标注。
// 无匹配时返回空字符串。
func languageForPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	base := filepath.Base(path)

	// Filename-based matches (no extension or special names)
	switch base {
	case "Dockerfile":
		return "dockerfile"
	case "Makefile":
		return "makefile"
	}

	// Extension-based matches
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js":
		return "javascript"
	case ".jsx":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "typescript"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".c", ".h":
		return "c"
	case ".cpp", ".hpp", ".cc", ".hh", ".cxx", ".hxx":
		return "cpp"
	case ".sh", ".bash":
		return "bash"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	case ".toml":
		return "toml"
	case ".md", ".mdx":
		return "markdown"
	case ".sql":
		return "sql"
	case ".proto":
		return "protobuf"
	case ".css", ".scss", ".less":
		return "css"
	case ".html", ".htm":
		return "html"
	case ".xml":
		return "xml"
	}

	return ""
}
