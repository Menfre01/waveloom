package tool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Menfre01/waveloom/pkg/pathutil"
)

// ---------------------------------------------------------------------------
// ReadFile — 读取文件内容
// ---------------------------------------------------------------------------

type ReadFileParams struct {
	FilePath   string `json:"file_path"`
	Offset     int    `json:"offset"`     // 0-based: 0 = 文件第一行
	Limit      int    `json:"limit"`      // 读取行数（0 = 不限）
	WorkingDir string `json:"working_dir"` // 工作目录（可选），相对路径基于此解析
}

type ReadFile struct{}

func (t *ReadFile) Name() string         { return "read_file" }
func (t *ReadFile) Description() string {
	return "Read a file with line numbers. Supports offset and limit parameters to read partial content. " +
		"file_path must be a file, not a directory. " +
		"On directory error, pick a file from the listing — use the Did you mean suggestion if present."
}

var readFileSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "file_path": {
      "type": "string",
      "description": "File path (absolute, or relative to working_dir / workspace root). Must be a file, not a directory — use shell('ls') first to explore directories. Paths without a file extension are likely directories."
    },
    "offset": {
      "type": "integer",
      "description": "Starting line number (0-based, 0 = first line, optional)"
    },
    "limit": {
      "type": "integer",
      "description": "Number of lines to read (optional, default: all)"
    },
    "working_dir": {
      "type": "string",
      "description": "Working directory (optional)"
    }
  },
  "required": ["file_path"]
}`)

func (t *ReadFile) Schema() json.RawMessage { return readFileSchema }
func (t *ReadFile) ConcurrentSafe() bool { return true }

func (t *ReadFile) Execute(ctx context.Context, p ReadFileParams) (*ToolResult, error) {
	// ── Step 1: 路径解析 ──
	path, err := pathutil.ResolvePathWithDir(p.FilePath, p.WorkingDir)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("invalid path: %v", err), err), nil
	}

	// ── Step 2: 设备文件拦截（零 I/O）──
	if IsBlockedDevicePath(path) {
		return toolError(ErrorClassFatal, ErrKindSecurityViolation,
			fmt.Sprintf("cannot read device file: %s", path), nil), nil
	}

	// ── Step 3: 文件检查 ──
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fileNotFoundError(path), nil
		}
		return toolError(ErrorClassFatal, ErrKindPermissionDenied,
			fmt.Sprintf("cannot access file: %s", path), err), nil
	}

	if info.IsDir() {
		entries, readErr := os.ReadDir(path)
		if readErr == nil {
			// 智能排序：源码文件优先，目录放最后
			sortDirEntries(entries)

			var listing strings.Builder
			fmt.Fprintf(&listing, "Path is a directory, not a file: %s\n\n", path)

			// 尝试推测用户想要的文件
			if suggestion := suggestFileInDir(path, entries); suggestion != "" {
				fmt.Fprintf(&listing, "Did you mean %s?\n\n", suggestion)
			}

			const maxDisplay = 50
			total := len(entries)
			if total > maxDisplay {
				fmt.Fprintf(&listing, "Showing first %d of %d entries (use ls for more):\n", maxDisplay, total)
			} else {
				listing.WriteString("Contents:\n")
			}
			for i, entry := range entries {
				if i >= maxDisplay {
					fmt.Fprintf(&listing, "  ... and %d more entries\n", total-maxDisplay)
					break
				}
				name := entry.Name()
				if entry.IsDir() {
					name += "/"
				}
				fmt.Fprintf(&listing, "  %s\n", name)
			}
			return toolError(ErrorClassRecoverable, ErrKindNotDir, listing.String(), nil), nil
		}
		return toolError(ErrorClassRecoverable, ErrKindNotDir,
			fmt.Sprintf("path is a directory, not a file: %s", path), nil), nil
	}

	// ── Step 4: 二进制检测 ──
	if HasBinaryExtension(path) {
		return toolError(ErrorClassRecoverable, ErrKindBinaryFile,
			fmt.Sprintf("file appears to be a binary %s file: %s",
				strings.ToLower(strings.TrimPrefix(fileExtension(path), ".")), path), nil), nil
	}

	isBinary, err := IsBinaryByContent(path)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("cannot check file type: %v", err), err), nil
	}
	if isBinary {
		return toolError(ErrorClassRecoverable, ErrKindBinaryFile,
			fmt.Sprintf("file appears to be binary: %s", path), nil), nil
	}

	// ── Step 5: 读取内容 ──
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 小文件走快速路径（分块读取，每块检查 ctx），大文件走流式路径
	var content string
	var lineCount, totalLines int

	if info.Size() < FastPathMaxSize {
		content, lineCount, totalLines, err = readFast(ctx, path, p.Offset, p.Limit)
	} else {
		content, lineCount, totalLines, err = readStreaming(ctx, path, p.Offset, p.Limit)
	}
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindCommandFailed,
			fmt.Sprintf("error reading file: %v", err), err), nil
	}

	// ── Step 6: 格式化 ──
	if totalLines == 0 {
		content = "<system-reminder>Warning: the file exists but the contents are empty.</system-reminder>"
	} else if content == "" && p.Offset > 0 {
		content = fmt.Sprintf("<system-reminder>Warning: the file exists but is shorter than the provided offset (%d). The file has %d lines.</system-reminder>",
			p.Offset, totalLines)
	}

	return &ToolResult{
		Content: content,
		Meta: ToolMeta{
			FilePath:  path,
			LineCount: lineCount,
			ByteCount: len(content),
		},
	}, nil
}

// ── helpers ──

func toolError(class ErrorClass, kind, msg string, cause error) *ToolResult {
	return &ToolResult{Error: &ToolError{Class: class, Kind: kind, Message: msg, Cause: cause}}
}

func fileExtension(path string) string {
	for i := len(path) - 1; i >= 0 && path[i] != '/' && path[i] != '\\'; i-- {
		if path[i] == '.' {
			return path[i:]
		}
	}
	return ""
}

func fileNotFoundError(path string) *ToolResult {
	cwd, _ := os.Getwd()
	msg := fmt.Sprintf("File does not exist: %s\nCWD: %s", path, cwd)

	// 场景 1：文件名恰好在 CWD 下存在（LLM 忘了加子目录前缀）
	if suggestion := SuggestPathUnderCwd(path); suggestion != "" {
		msg += fmt.Sprintf("\nDid you mean %s?", suggestion)
		return toolError(ErrorClassRecoverable, ErrKindFileNotFound, msg, nil)
	}

	// 场景 2：父目录存在 → 在目录内找相似文件名
	parentDir := filepath.Dir(path)
	if info, statErr := os.Stat(parentDir); statErr == nil && info.IsDir() {
		if similar := FindSimilarFile(path); similar != "" {
			msg += fmt.Sprintf("\nDid you mean %s?", similar)
			return toolError(ErrorClassRecoverable, ErrKindFileNotFound, msg, nil)
		}
		// 父目录存在但没找到相似文件 → 列出目录内容帮助 LLM 定位
		entries, readErr := os.ReadDir(parentDir)
		if readErr == nil && len(entries) > 0 {
			sortDirEntries(entries)
			msg += fmt.Sprintf("\n\nFiles in %s:", parentDir)
			const maxShow = 20
			for i, e := range entries {
				if i >= maxShow {
					msg += fmt.Sprintf("\n  ... and %d more files", len(entries)-maxShow)
					break
				}
				name := e.Name()
				if e.IsDir() {
					name += "/"
				}
				msg += fmt.Sprintf("\n  %s", name)
			}
		}
		return toolError(ErrorClassRecoverable, ErrKindFileNotFound, msg, nil)
	}

	// 场景 3：父目录也不存在 → 路径整体错误，不做文件猜测
	msg += fmt.Sprintf("\nParent directory not found: %s", parentDir)
	msg += "\nCheck the path with shell('ls')."
	return toolError(ErrorClassRecoverable, ErrKindFileNotFound, msg, nil)
}

// ──────────────────────────────────────────────────────────────────────────
// Fast path — 小文件 (< 10MB)：分块读取，每块检查 context 取消。
// ──────────────────────────────────────────────────────────────────────────

func readFast(ctx context.Context, path string, offset, limit int) (content string, lineCount int, totalLines int, err error) {
	raw, err := readFileWithContext(ctx, path)
	if err != nil {
		return "", 0, 0, err
	}

	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\r")
	lines := splitLines(text)
	totalLines = len(lines)

	selected, truncated := selectRange(lines, offset, limit)
	content = strings.Join(selected, "\n")
	if truncated {
		omitted := totalLines - offset - len(selected)
		if omitted < 0 {
			omitted = 0
		}
		content += fmt.Sprintf("\n... [truncated: %d lines omitted]", omitted)
	}

	return content, len(selected), totalLines, nil
}

// readFileWithContext 分块读取文件，每 64KB 检查 ctx 是否取消。
// 用于替代 os.ReadFile，支持 context 中断。
func readFileWithContext(ctx context.Context, path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var buf bytes.Buffer
	chunk := make([]byte, 64*1024) // 64KB chunks

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := f.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}

	return buf.Bytes(), nil
}

func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	// 去除尾部的空行（由 trailing newline 产生）
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// ──────────────────────────────────────────────────────────────────────────
// Streaming path — 大文件 / 非普通文件：bufio.Scanner 流式处理，每行检查 context。
// ──────────────────────────────────────────────────────────────────────────

func readStreaming(ctx context.Context, path string, offset, limit int) (content string, lineCount int, totalLines int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, 0, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	var selected []string
	lineIdx := 0
	selectedBytes := 0
	reachedByteLimit := false

	for scanner.Scan() {
		// 每 64 行检查一次 context 取消，避免高频 syscall。
		if lineIdx%64 == 0 {
			if err := ctx.Err(); err != nil {
				return "", 0, 0, err
			}
		}

		text := scanner.Text()

		if reachedByteLimit {
			lineIdx++
			continue
		}

		if lineIdx < offset {
			lineIdx++
			continue
		}

		line := fmt.Sprintf("[%d] %s", lineIdx+1, text)
		lineIdx++

		sep := 0
		if len(selected) > 0 {
			sep = 1
		}
		if selectedBytes+sep+len(line) > MaxReadBytes {
			reachedByteLimit = true
			continue
		}

		if len(selected) > 0 {
			selectedBytes++
		}
		selectedBytes += len(line)
		selected = append(selected, line)

		if limit > 0 && len(selected) >= limit {
			reachedByteLimit = true
		}
	}

	if err := scanner.Err(); err != nil {
		return "", 0, 0, err
	}

	totalLines = lineIdx
	content = strings.Join(selected, "\n")
	if reachedByteLimit {
		omitted := totalLines - offset - len(selected)
		if omitted < 0 {
			omitted = 0
		}
		content += fmt.Sprintf("\n... [truncated: %d lines omitted]", omitted)
	}

	return content, len(selected), totalLines, nil
}

// ──────────────────────────────────────────────────────────────────────────
// selectRange — 从已分割的行数组按 offset/limit 选取，受字节预算限制
// ──────────────────────────────────────────────────────────────────────────

func selectRange(lines []string, offset, limit int) (selected []string, truncated bool) {
	used := 0

	for i, line := range lines {
		if i < offset {
			continue
		}

		formatted := fmt.Sprintf("[%d] %s", i+1, line)

		sep := 0
		if len(selected) > 0 {
			sep = 1
		}
		if used+sep+len(formatted) > MaxReadBytes {
			truncated = true
			break
		}
		if len(selected) > 0 {
			used++
		}
		used += len(formatted)
		selected = append(selected, formatted)

		if limit > 0 && len(selected) >= limit {
			if i+1 < len(lines) {
				truncated = true
			}
			break
		}
	}
	return
}

// ---------------------------------------------------------------------------
// sortDirEntries — 排序目录条目：文件在前（字母序），目录在后（字母序）
// ---------------------------------------------------------------------------

func sortDirEntries(entries []os.DirEntry) {
	sort.Slice(entries, func(i, j int) bool {
		iDir, jDir := entries[i].IsDir(), entries[j].IsDir()
		if iDir != jDir {
			return !iDir // 文件在前，目录在后
		}
		return entries[i].Name() < entries[j].Name()
	})
}

// ---------------------------------------------------------------------------
// suggestFileInDir — 推测目录下用户最可能想读的文件（语言无关）
// ---------------------------------------------------------------------------

// suggestFileInDir 在目录中寻找最可能的候选文件。
// 优先级：
//   1. 文件名（不含扩展名）与目录名相同（如 pkg/skill → skill.go / skill.py / skill.rs）
//   2. 常见入口文件名（index.* / main.* / mod.* / lib.*）
//   3. 字母序第一个非隐藏文件
// 均不满足返回 ""。
func suggestFileInDir(dirPath string, entries []os.DirEntry) string {
	dirName := filepath.Base(dirPath)

	// 收集非目录文件
	var files []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e)
		}
	}
	if len(files) == 0 {
		return ""
	}

	// 1. 文件名（去扩展名）与目录名相同（大小写不敏感）
	for _, e := range files {
		name := e.Name()
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		if strings.EqualFold(base, dirName) {
			return filepath.Join(dirPath, name)
		}
	}

	// 2. 常见入口文件名
	entryNames := []string{"index.", "main.", "mod.", "lib."}
	for _, prefix := range entryNames {
		for _, e := range files {
			if strings.HasPrefix(strings.ToLower(e.Name()), prefix) {
				return filepath.Join(dirPath, e.Name())
			}
		}
	}

	// 3. 字母序第一个非隐藏文件
	for _, e := range files {
		if !strings.HasPrefix(e.Name(), ".") {
			return filepath.Join(dirPath, e.Name())
		}
	}

	return ""
}
