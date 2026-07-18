package tool

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ── 共享辅助函数：从旧 read_file.go 提取，被 hashline 工具和其他工具依赖 ──

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

	if suggestion := SuggestPathUnderCwd(path); suggestion != "" {
		msg += fmt.Sprintf("\nDid you mean %s?", suggestion)
		return toolError(ErrorClassRecoverable, ErrKindFileNotFound, msg, nil)
	}

	parentDir := filepath.Dir(path)
	if info, statErr := os.Stat(parentDir); statErr == nil && info.IsDir() {
		if similar := FindSimilarFile(path); similar != "" {
			msg += fmt.Sprintf("\nDid you mean %s?", similar)
			return toolError(ErrorClassRecoverable, ErrKindFileNotFound, msg, nil)
		}
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

	msg += fmt.Sprintf("\nParent directory not found: %s", parentDir)
	msg += "\nCheck the path with shell('ls')."
	return toolError(ErrorClassRecoverable, ErrKindFileNotFound, msg, nil)
}

func readFileWithContext(ctx context.Context, path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var buf bytes.Buffer
	chunk := make([]byte, 64*1024)

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
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func sortDirEntries(entries []os.DirEntry) {
	sort.Slice(entries, func(i, j int) bool {
		iDir, jDir := entries[i].IsDir(), entries[j].IsDir()
		if iDir != jDir {
			return !iDir
		}
		return entries[i].Name() < entries[j].Name()
	})
}

func suggestFileInDir(dirPath string, entries []os.DirEntry) string {
	dirName := filepath.Base(dirPath)

	var files []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e)
		}
	}
	if len(files) == 0 {
		return ""
	}

	for _, e := range files {
		name := e.Name()
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		if strings.EqualFold(base, dirName) {
			return filepath.Join(dirPath, name)
		}
	}

	entryNames := []string{"index.", "main.", "mod.", "lib."}
	for _, prefix := range entryNames {
		for _, e := range files {
			if strings.HasPrefix(strings.ToLower(e.Name()), prefix) {
				return filepath.Join(dirPath, e.Name())
			}
		}
	}

	for _, e := range files {
		if !strings.HasPrefix(e.Name(), ".") {
			return filepath.Join(dirPath, e.Name())
		}
	}

	return ""
}
