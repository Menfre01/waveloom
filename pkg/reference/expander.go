package reference

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"waveloom/pkg/permission"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	maxFileBytes  = 32 * 1024  // 单文件上限 32KB
	maxTotalBytes = 128 * 1024 // 总展开内容上限 128KB
)

// ---------------------------------------------------------------------------
// Expander
// ---------------------------------------------------------------------------

// Expander 负责解析和展开 @ 引用。
type Expander struct {
	guard permission.Guard
}

// New 创建一个新的 Expander。
func New(guard permission.Guard) *Expander {
	return &Expander{
		guard: guard,
	}
}

// Expand 解析 userInput 中的 @ 引用，展开为实际内容，返回替换后的文本。
//
// 展开逻辑:
//  1. 扫描 userInput，提取所有 @ref
//  2. 对每个 ref，通过 os.Stat 判定是文件还是目录
//  3. 文件 → 直接读取内容
//     目录 → 列出目录结构
//  4. 将 @ref 替换为 @@ 围栏块包裹的实际内容
//  5. 展开失败时保留 @ref 原文 + 追加错误标记
func (e *Expander) Expand(ctx context.Context, userInput string, cwd string) (expanded string, refs []ResolvedRef, err error) {
	// 1. Parse
	parsed := parseRefs(userInput, cwd)
	if len(parsed) == 0 {
		return userInput, nil, nil
	}

	// 2. Expand each ref
	resolved, expandErr := e.expandRefs(ctx, parsed, cwd)
	if expandErr != nil {
		return userInput, nil, expandErr
	}

	// 3. Replace
	expanded = replaceRefs(userInput, resolved, cwd)

	return expanded, resolved, nil
}

// expandRefs 对一组 Ref 执行展开，返回 ResolvedRef 列表。
func (e *Expander) expandRefs(ctx context.Context, refs []Ref, cwd string) ([]ResolvedRef, error) {
	var resolved []ResolvedRef
	totalBytes := 0

	for _, ref := range refs {
		// Check total bytes limit
		if totalBytes >= maxTotalBytes {
			break
		}

		// Permission check — 使用对应的工具名以匹配权限规则
		var toolName string
		var params json.RawMessage
		switch ref.Kind {
		case KindFile:
			toolName = "read_file"
			params = json.RawMessage(fmt.Sprintf(`{"file_path": "%s"}`, ref.Path))
		case KindFolder:
			toolName = "ls"
			params = json.RawMessage(fmt.Sprintf(`{"path": "%s", "depth": 2}`, ref.Path))
		default:
			continue
		}

		decision := e.guard.Check(ctx, toolName, params)
		if decision.Decision == permission.DecisionDeny || decision.Decision == permission.DecisionAsk {
			resolved = append(resolved, ResolvedRef{
				Ref:   ref,
				Error: fmt.Sprintf("permission denied: %s", decision.Message),
			})
			continue
		}

		// Direct file I/O
		var content string
		var readErr error

		switch ref.Kind {
		case KindFile:
			content, readErr = readFileContent(ref.Path)
		case KindFolder:
			content, readErr = listDirContent(ref.Path, 2)
		}

		if readErr != nil {
			resolved = append(resolved, ResolvedRef{
				Ref:   ref,
				Error: readErr.Error(),
			})
			continue
		}

		// Apply single-file size limit
		byteCount := len(content)
		if byteCount > maxFileBytes {
			content = content[:maxFileBytes]
			content += "\n[truncated: file exceeds 32KB limit]"
			byteCount = len(content)
		}

		// Check total bytes
		if totalBytes+byteCount > maxTotalBytes {
			available := maxTotalBytes - totalBytes
			if available > 0 {
				content = content[:available]
				content += "\n[truncated: total context exceeds 128KB limit]"
				byteCount = len(content)
			} else {
				break
			}
		}

		totalBytes += byteCount

		resolved = append(resolved, ResolvedRef{
			Ref:     ref,
			Content: content,
			Bytes:   byteCount,
		})
	}

	return resolved, nil
}

// readFileContent 读取文件内容。文件不存在时返回错误。
func readFileContent(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("file not found: %s", path)
	}
	return string(data), nil
}

// listDirContent 列出目录内容（带深度限制）。
func listDirContent(root string, maxDepth int) (string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("directory not found: %s", root)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", root)
	}

	var b strings.Builder
	printDir(&b, root, "", 0, maxDepth)
	return strings.TrimSpace(b.String()), nil
}

// printDir 递归打印目录树。
func printDir(b *strings.Builder, dir string, prefix string, depth int, maxDepth int) {
	if depth > maxDepth {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// Sort: directories first, then files, alphabetically within each group
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})

	for i, entry := range entries {
		isLast := i == len(entries)-1
		connector := "├── "
		childPrefix := prefix + "│   "
		if isLast {
			connector = "└── "
			childPrefix = prefix + "    "
		}

		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		fmt.Fprintf(b, "%s%s%s\n", prefix, connector, name)

		if entry.IsDir() && depth < maxDepth {
			printDir(b, filepath.Join(dir, entry.Name()), childPrefix, depth+1, maxDepth)
		}
	}
}
