package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Menfre01/waveloom/pkg/pathutil"
)

// ---------------------------------------------------------------------------
// SearchFile — Glob 文件搜索
// ---------------------------------------------------------------------------

const (
	// MaxSearchResults 是 search_file 返回的最大文件数。
	MaxSearchResults = 100
)

type SearchFileParams struct {
	Pattern    string `json:"pattern"`
	WorkingDir string `json:"working_dir"`
}

type SearchFile struct{}

func (t *SearchFile) Name() string            { return "search_file" }
func (t *SearchFile) Description() string     { return "Search for file names using glob patterns. Supports ** recursive matching (e.g. **/*.go, src/**/*_test.go). Returns up to 100 files." }
func (t *SearchFile) Schema() json.RawMessage { return searchFileSchema }
func (t *SearchFile) ConcurrentSafe() bool    { return true }

func (t *SearchFile) Execute(ctx context.Context, p SearchFileParams) (*ToolResult, error) {
	start := time.Now()

	// ── Step 1: 确定搜索目录 ──
	dir := p.WorkingDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	dir, err := pathutil.ResolvePath(dir)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("invalid working_dir: %v", err), err), nil
	}

	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return toolError(ErrorClassRecoverable, ErrKindFileNotFound,
				fmt.Sprintf("directory not found: %s\nCWD: %s", dir, cwd()), err), nil
		}
		return toolError(ErrorClassFatal, ErrKindPermissionDenied,
			fmt.Sprintf("cannot access directory: %s", dir), err), nil
	}
	if !info.IsDir() {
		return toolError(ErrorClassRecoverable, ErrKindNotDir,
			fmt.Sprintf("path is not a directory: %s", dir), nil), nil
	}

	// ── Step 2: 遍历匹配 ──
	var matches []string
	truncated := false

	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return nil
		}
		if d.IsDir() && ShouldSkipDir(d.Name()) {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}

		if matchPath(p.Pattern, rel) {
			if len(matches) >= MaxSearchResults {
				truncated = true
				return filepath.SkipAll // 停止遍历
			}
			matches = append(matches, rel)
		}
		return nil
	})

	// ── Step 3: 格式化输出 ──
	duration := time.Since(start)
	content := formatSearchResults(p.Pattern, dir, matches, truncated, duration)

	return &ToolResult{
		Content: content,
		Meta: ToolMeta{
			FilePath:  p.Pattern,
			LineCount: len(matches),
			Duration:  duration,
		},
	}, nil
}

// ── matchPath ──

func matchPath(pattern, path string) bool {
	// 1. 直接匹配文件名
	if matched, _ := filepath.Match(pattern, filepath.Base(path)); matched {
		return true
	}

	// 2. 含 ** 的路径匹配
	if strings.Contains(pattern, "**") {
		return matchDoubleStar(pattern, path)
	}

	return false
}

// matchDoubleStar 实现 ** 递归匹配。
// 策略：将 pattern 按 **/ 分割，前缀必须匹配路径开头，后缀匹配剩余部分。
func matchDoubleStar(pattern, path string) bool {
	// 以 **/ 分割
	parts := strings.Split(pattern, "**/")

	switch {
	case len(parts) == 1:
		// 没有 **/ — 可能是 "prefix**" 或 "**suffix" 或 "**"
		clean := strings.ReplaceAll(pattern, "**", "")
		if clean == "" {
			return true // "**" 匹配一切
		}
		if strings.HasPrefix(pattern, "**") {
			// "**/suffix" 的 "/" 在分割中已处理，这里处理 "**suffix"
			return strings.HasSuffix(path, clean)
		}
		// "prefix**" → path 以 prefix 开头即可
		return strings.HasPrefix(path, clean)

	default:
		// 标准 "prefix/**/suffix" 或 "**/suffix" 或 "prefix/**"
		prefix := parts[0]
		suffix := parts[len(parts)-1]

		// 前缀检查
		if prefix != "" {
			if !strings.HasPrefix(path, prefix) {
				return false
			}
			path = path[len(prefix):]
		}

		// 后缀检查 — 对剩余路径做 glob 匹配
		if suffix != "" {
			// 去掉后缀头部的 /（如 **/*.go → "*.go"）
			suffix = strings.TrimPrefix(suffix, string(filepath.Separator))

			// 检查路径中是否某处匹配此后缀
			// 1) 直接文件名匹配
			if matched, _ := filepath.Match(suffix, filepath.Base(path)); matched {
				return true
			}
			// 2) HasSuffix 回退
			if strings.HasSuffix(path, suffix) {
				return true
			}
			// 3) 任一中间路径分量匹配（如 **/waveloom* 匹配 cmd/waveloom/main.go）
			for _, comp := range strings.Split(path, string(filepath.Separator)) {
				if matched, _ := filepath.Match(suffix, comp); matched {
					return true
				}
			}
			return false
		}

		return true // "prefix/**" → 前缀匹配就行
	}
}

// ── formatSearchResults ──

func formatSearchResults(pattern, dir string, matches []string, truncated bool, duration time.Duration) string {
	var buf strings.Builder

	// 排序
	sort.Strings(matches)

	if len(matches) == 0 {
		buf.WriteString(fmt.Sprintf("No files matching %q found in %s.", pattern, relOrDir(dir)))
		// 添加搜索范围提示
		buf.WriteString(fmt.Sprintf("\nSearched under: %s", dir))
		return buf.String()
	}

	buf.WriteString(fmt.Sprintf("Found %d file(s) matching %q in %s (%s):",
		len(matches), pattern, relOrDir(dir), duration.Round(time.Millisecond)))

	if truncated {
		buf.WriteString(fmt.Sprintf("\nResults truncated to %d. Use a more specific pattern (e.g. path/**/*.go) to narrow results.",
			MaxSearchResults))
	}

	buf.WriteByte('\n')
	for _, m := range matches {
		buf.WriteString(m)
		buf.WriteByte('\n')
	}

	return buf.String()
}

func relOrDir(dir string) string {
	cwd, err := os.Getwd()
	if err == nil {
		if rel, err := filepath.Rel(cwd, dir); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return dir
}

func cwd() string {
	d, _ := os.Getwd()
	return d
}
