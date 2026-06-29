package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Menfre01/waveloom/pkg/pathutil"
)

// ---------------------------------------------------------------------------
// Ls — 列出目录内容
// ---------------------------------------------------------------------------

const (
	// MaxListEntries 是 ls 返回的最大条目数。
	MaxListEntries = 200
)

type LsParams struct {
	Path       string `json:"path"`
	Depth      int    `json:"depth"`
	WorkingDir string `json:"working_dir"` // 工作目录（可选），相对路径基于此解析
}

type Ls struct{}

func (t *Ls) Name() string            { return "ls" }
func (t *Ls) Description() string     { return "List files and subdirectories in a directory. Directories are suffixed with /. Supports recursive depth control (depth parameter, default 1)." }
func (t *Ls) Schema() json.RawMessage { return lsSchema }
func (t *Ls) ConcurrentSafe() bool    { return true }

func (t *Ls) Execute(ctx context.Context, p LsParams) (*ToolResult, error) {
	start := time.Now()

	// ── 路径解析 ──
	path := p.Path
	workingDir := p.WorkingDir
	if path == "" {
		if workingDir != "" {
			path = workingDir
		} else {
			path, _ = os.Getwd()
		}
	}
	path, err := pathutil.ResolvePathWithDir(path, workingDir)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("invalid path: %v", err), err), nil
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return toolError(ErrorClassRecoverable, ErrKindFileNotFound,
				fmt.Sprintf("path not found: %s\nCWD: %s", path, cwd()), err), nil
		}
		return toolError(ErrorClassFatal, ErrKindPermissionDenied,
			fmt.Sprintf("cannot access path: %s", path), err), nil
	}

	if !info.IsDir() {
		return toolError(ErrorClassRecoverable, ErrKindNotDir,
			fmt.Sprintf("path is not a directory: %s", path), nil), nil
	}

	// ── 深度设置 ──
	depth := p.Depth
	if depth <= 0 {
		depth = 1
	}

	// ── 递归遍历 ──
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var buf strings.Builder
	state := &listState{buf: &buf, maxDepth: depth}

	listDir(path, "", 0, state)

	// ── 格式化 ──
	content := buf.String()
	duration := time.Since(start)

	// 摘要头
	var header string
	if content == "" {
		content = fmt.Sprintf("(empty directory: %s)\n", relOrDir(path))
		header = content
	} else {
		header = fmt.Sprintf("Listed %s (%d entries, %s):\n",
			relOrDir(path), state.count, duration.Round(time.Millisecond))
	}

	if state.truncated {
		header += fmt.Sprintf("Truncated at %d entries. Use depth or a more specific path.\n", MaxListEntries)
	}

	return &ToolResult{
		Content: header + content,
		Meta: ToolMeta{
			FilePath:  path,
			LineCount: state.count,
			Duration:  duration,
		},
	}, nil
}

type listState struct {
	buf       *strings.Builder
	maxDepth  int
	count     int
	truncated bool
}

func listDir(path, prefix string, currentDepth int, s *listState) {
	if currentDepth >= s.maxDepth || s.truncated {
		return
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		s.buf.WriteString(fmt.Sprintf("%s(error: cannot read directory)\n", prefix))
		return
	}

	sort.Slice(entries, func(i, j int) bool {
		iDir := entries[i].IsDir()
		jDir := entries[j].IsDir()
		if iDir != jDir {
			return iDir && !jDir
		}
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		name := entry.Name()

		if ShouldSkipDir(name) {
			continue
		}

		if s.count >= MaxListEntries {
			s.truncated = true
			return
		}
		s.count++

		if entry.IsDir() {
			s.buf.WriteString(fmt.Sprintf("%s%s/\n", prefix, name))
			childPath := path + string(os.PathSeparator) + name
			listDir(childPath, prefix+"  ", currentDepth+1, s)
		} else {
			s.buf.WriteString(fmt.Sprintf("%s%s\n", prefix, name))
		}
	}
}
