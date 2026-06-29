package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Menfre01/waveloom/pkg/pathutil"
)

// ---------------------------------------------------------------------------
// WriteFile — 写入/覆写文件
// ---------------------------------------------------------------------------

type WriteFileParams struct {
	FilePath   string `json:"file_path"`
	Content    string `json:"content"`
	WorkingDir string `json:"working_dir"` // 工作目录（可选），相对路径基于此解析
}

type WriteFile struct{}

func (t *WriteFile) Name() string            { return "write_file" }
func (t *WriteFile) Description() string {
	return "Create a new file or overwrite an existing file. Creates parent directories automatically. Use only for new files or complete overwrites; for partial edits use edit_file."
}
func (t *WriteFile) Schema() json.RawMessage { return writeFileSchema }
func (t *WriteFile) ConcurrentSafe() bool    { return false }

func (t *WriteFile) Execute(ctx context.Context, p WriteFileParams) (*ToolResult, error) {
	// ── Step 1: 路径解析 ──
	path, err := pathutil.ResolvePathWithDir(p.FilePath, p.WorkingDir)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("invalid path: %v", err), err), nil
	}

	// ── Step 2: 大小保护 ──
	if len(p.Content) > MaxWriteBytes {
		return toolError(ErrorClassFatal, ErrKindInvalidArgs,
			fmt.Sprintf("content too large (%s), max write size is %s",
				formatSize(int64(len(p.Content))), formatSize(int64(MaxWriteBytes))), nil), nil
	}

	// ── Step 3: 确定操作类型 (create vs update) ──
	oldContent := ""
	isUpdate := false

	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
				fmt.Sprintf("path is a directory, cannot write: %s", path), nil), nil
		}
		// 文件存在 — update
		oldBytes, readErr := os.ReadFile(path)
		if readErr == nil {
			oldContent = string(oldBytes)
		}
		isUpdate = true
	}

	// ── Step 4: 父目录创建 ──
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return toolError(ErrorClassFatal, ErrKindPermissionDenied,
			fmt.Sprintf("cannot create parent directory: %s", dir), err), nil
	}

	// ── Step 5: 写入 ──
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(p.Content), 0o644); err != nil {
		if isDiskFull(err) {
			return toolError(ErrorClassFatal, ErrKindDiskFull,
				fmt.Sprintf("disk full while writing: %s", path), err), nil
		}
		return toolError(ErrorClassFatal, ErrKindPermissionDenied,
			fmt.Sprintf("cannot write file: %s", path), err), nil
	}

	// ── Step 6: Diff 反馈 ──
	newLines := countLinesInContent(p.Content)
	oldLines := countLinesInContent(oldContent)

	var result strings.Builder

	if !isUpdate {
		// ── Create ──
		result.WriteString(fmt.Sprintf("Created new file: %s\n", path))
		result.WriteString(fmt.Sprintf("   Lines: %d, Size: %s\n", newLines, formatSize(int64(len(p.Content)))))
		result.WriteString(renderContentPreview(p.Content))
	} else {
		// ── Update ──
		result.WriteString(fmt.Sprintf("Updated file: %s\n", path))
		result.WriteString(fmt.Sprintf("   Lines: %d → %d (%s%d)\n",
			oldLines, newLines, changeSign(newLines-oldLines), absInt(newLines-oldLines)))
		result.WriteString(fmt.Sprintf("   Size: %s → %s\n",
			formatSize(int64(len(oldContent))), formatSize(int64(len(p.Content)))))

		// 找出变化摘要
		changeSummary := summarizeChange(oldContent, p.Content)
		if changeSummary != "" {
			result.WriteString(changeSummary)
		}

		result.WriteString(renderContentPreview(p.Content))
	}

	return &ToolResult{
		Content: result.String(),
		Meta: ToolMeta{
			FilePath:  path,
			LineCount: newLines,
			ByteCount: len(p.Content),
		},
	}, nil
}

// ── 辅助 ──

func countLinesInContent(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

func changeSign(delta int) string {
	if delta >= 0 {
		return "+"
	}
	return ""
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// renderContentPreview 返回内容的前若干行作为 LLM 校验预览。
func renderContentPreview(content string) string {
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	preview := 10
	if len(lines) < preview {
		preview = len(lines)
	}
	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("\n   --- Preview (first %d lines) ---\n", preview))
	for i := 0; i < preview; i++ {
		buf.WriteString(fmt.Sprintf("   [%d] %s\n", i+1, lines[i]))
	}
	if len(lines) > preview {
		buf.WriteString(fmt.Sprintf("   ... (%d more lines)\n", len(lines)-preview))
	}
	return buf.String()
}

// summarizeChange 提供旧→新的变化摘要。
func summarizeChange(old, new string) string {
	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(new, "\n")

	// 计算首尾共同行
	commonHead := 0
	for commonHead < len(oldLines) && commonHead < len(newLines) &&
		oldLines[commonHead] == newLines[commonHead] {
		commonHead++
	}

	commonTail := 0
	for commonTail < len(oldLines)-commonHead && commonTail < len(newLines)-commonHead &&
		oldLines[len(oldLines)-1-commonTail] == newLines[len(newLines)-1-commonTail] {
		commonTail++
	}

	linesAdded := len(newLines) - len(oldLines)
	changedRegions := len(oldLines) - commonHead - commonTail

	if changedRegions <= 0 && linesAdded == 0 {
		return "   No changes detected (content identical).\n"
	}

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("   Lines added: %d, Removed: %d, Changed: %d\n",
		maxInt(linesAdded, 0), maxInt(-linesAdded, 0), changedRegions))

	if changedRegions > 0 && changedRegions <= 5 {
		buf.WriteString("   --- Diff (old → new) ---\n")
		// 显示少量变更区域
		for i := commonHead; i < len(oldLines)-commonTail && i < commonHead+5; i++ {
			buf.WriteString(fmt.Sprintf("   - %s\n", oldLines[i]))
		}
		for i := commonHead; i < len(newLines)-commonTail && i < commonHead+5; i++ {
			buf.WriteString(fmt.Sprintf("   + %s\n", newLines[i]))
		}
	}
	return buf.String()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func isDiskFull(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no space left") ||
		strings.Contains(msg, "disk full") ||
		strings.Contains(msg, "ENOSPC")
}
