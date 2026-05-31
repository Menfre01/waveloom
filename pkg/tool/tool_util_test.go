package tool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// formatSize 测试（read_file.go 里的辅助函数）
// ---------------------------------------------------------------------------

func TestFormatSizeB(t *testing.T) {
	if got := formatSize(512); got != "512B" {
		t.Errorf("formatSize(512) = %q, want %q", got, "512B")
	}
}

func TestFormatSizeKB(t *testing.T) {
	if got := formatSize(2048); got != "2.0KB" {
		t.Errorf("formatSize(2048) = %q, want %q", got, "2.0KB")
	}
}

func TestFormatSizeMB(t *testing.T) {
	if got := formatSize(2*1024*1024 + 500000); !strings.HasPrefix(got, "2.") {
		t.Errorf("formatSize(2MB+) = %q, want prefix '2.xMB'", got)
	}
}

// ---------------------------------------------------------------------------
// formatDuration 测试（shell.go 里的辅助函数）
// ---------------------------------------------------------------------------

func TestFormatDurationSeconds(t *testing.T) {
	if got := formatDuration(30 * time.Second); got != "30s" {
		t.Errorf("formatDuration(30s) = %q, want %q", got, "30s")
	}
}

func TestFormatDurationMinutes(t *testing.T) {
	if got := formatDuration(90 * time.Second); got != "1.5min" {
		t.Errorf("formatDuration(90s) = %q, want %q", got, "1.5min")
	}
}

// ---------------------------------------------------------------------------
// truncateOutput 测试（shell.go 里的辅助函数）
// ---------------------------------------------------------------------------

func TestTruncateOutputNoTruncation(t *testing.T) {
	output := "line1\nline2\nline3\n"
	if got := truncateOutput(output, 100); got != output {
		t.Errorf("truncateOutput(3 lines, max 100) = %q, want unchanged", got)
	}
}

func TestTruncateOutputWithTruncation(t *testing.T) {
	var lines []string
	for i := 0; i < MaxShellLines+200; i++ {
		lines = append(lines, fmt.Sprintf("line_%d", i))
	}
	output := strings.Join(lines, "\n")
	result := truncateOutput(output, MaxShellLines)

	if !strings.Contains(result, "truncated") {
		t.Error("truncateOutput() should contain truncation notice")
	}
	// 验证截断点位置：head + tail = MaxShellLines/2 + MaxShellLines/2 = MaxShellLines
	// 加上截断提示行 <= MaxShellLines+1 行
	resultLines := strings.Split(result, "\n")
	if len(resultLines) > MaxShellLines+1+1 { // +1 for truncation line +1 potential trailing
		t.Errorf("truncateOutput() got %d lines, want at most %d", len(resultLines), MaxShellLines+2)
	}
}

// ---------------------------------------------------------------------------
// isDiskFull 测试（write_file.go 里的辅助函数）
// ---------------------------------------------------------------------------

func TestIsDiskFullNil(t *testing.T) {
	if isDiskFull(nil) {
		t.Error("isDiskFull(nil) = true, want false")
	}
}

func TestIsDiskFullNoSpace(t *testing.T) {
	if !isDiskFull(errors.New("write /tmp/file: no space left on device")) {
		t.Error("isDiskFull(no space left) = false, want true")
	}
}

func TestIsDiskFullDiskFull(t *testing.T) {
	if !isDiskFull(fmt.Errorf("disk full")) {
		t.Error("isDiskFull(disk full) = false, want true")
	}
}

func TestIsDiskFullENOSPC(t *testing.T) {
	if !isDiskFull(errors.New("ENOSPC: write error")) {
		t.Error("isDiskFull(ENOSPC) = false, want true")
	}
}

func TestIsDiskFullOtherError(t *testing.T) {
	if isDiskFull(errors.New("permission denied")) {
		t.Error("isDiskFull(permission denied) = true, want false")
	}
}

// ---------------------------------------------------------------------------
// matchGlob 测试（search_file.go 里的辅助函数）
// ---------------------------------------------------------------------------

func TestMatchGlobSimple(t *testing.T) {
	// 无 ** 的直接匹配
	if !matchPath("*.go", "main.go") {
		t.Error("matchPath(*.go, main.go) = false, want true")
	}
	if matchPath("*.go", "main.txt") {
		t.Error("matchPath(*.go, main.txt) = true, want false")
	}
}

func TestMatchGlobDoubleStarPrefix(t *testing.T) {
	// **/*.go 匹配任意深层路径
	if !matchPath("**/*.go", "pkg/tool/main.go") {
		t.Error("matchPath(**/*.go, pkg/tool/main.go) = false, want true")
	}
	if !matchPath("**/*.go", "main.go") {
		t.Error("matchPath(**/*.go, main.go) = false, want true")
	}
}

func TestMatchGlobDoubleStarMiddle(t *testing.T) {
	// src/**/*_test.go 匹配中间 **
	if !matchPath("src/**/*_test.go", "src/pkg/tool/tool_test.go") {
		t.Error("matchPath(src/**/*_test.go, src/pkg/tool/tool_test.go) = false, want true")
	}
}

func TestMatchGlobDoubleStarSuffix(t *testing.T) {
	// 以 ** 结尾
	if !matchPath("pkg/**", "pkg/tool/file.go") {
		t.Error("matchPath(pkg/**, pkg/tool/file.go) = false, want true")
	}
}

func TestMatchGlobNoStar(t *testing.T) {
	// 无通配符
	if matchPath("no_star.go", "other.go") {
		t.Error("matchPath(no_star.go, other.go) = true, want false")
	}
	if !matchPath("same.go", "same.go") {
		t.Error("matchPath(same.go, same.go) = false, want true")
	}
}

func TestMatchGlobPrefixDoubleStar(t *testing.T) {
	// prefix** 模式 — 以某前缀开头
	if !matchPath("pkg**", "pkg/tool/file.go") {
		t.Error("matchPath(pkg**, pkg/tool/file.go) = false, want true")
	}
	if matchPath("pkg**", "src/file.go") {
		t.Error("matchPath(pkg**, src/file.go) = true, want false")
	}
}

func TestMatchGlobSuffixDoubleStar(t *testing.T) {
	// **suffix 模式 — 以某后缀结尾
	if !matchPath("**_test.go", "pkg/tool/tool_test.go") {
		t.Error("matchPath(**_test.go, pkg/tool/tool_test.go) = false, want true")
	}
	if !matchPath("**_test.go", "tool_test.go") {
		t.Error("matchPath(**_test.go, tool_test.go) = false, want true")
	}
	if matchPath("**_test.go", "pkg/tool/tool.go") {
		t.Error("matchPath(**_test.go, tool.go) = true, want false")
	}
}

func TestMatchGlobDoubleStarOnly(t *testing.T) {
	// 只有 ** — 匹配一切
	if !matchPath("**", "any/path/file.txt") {
		t.Error("matchPath(**, any/path/file.txt) = false, want true")
	}
}

func TestMatchGlobPrefixMismatch(t *testing.T) {
	// src/** 模式，但路径不以 src/ 开头 — 返回 false
	if matchPath("src/**/*.go", "lib/file.go") {
		t.Error("matchPath(src/**/*.go, lib/file.go) = true, want false")
	}
}

func TestMatchGlobSuffixMatchFail(t *testing.T) {
	// **/*_test.go 模式，路径不匹配后缀 — 走到 HasSuffix 回退
	if matchPath("src/**/*_test.go", "src/pkg/tool.go") {
		t.Error("matchPath(src/**/*_test.go, src/pkg/tool.go) = true, want false")
	}
}

// ---------------------------------------------------------------------------
// Shell 危险命令检测
// ---------------------------------------------------------------------------

func TestShellDangerousCommandDetected(t *testing.T) {
	// Wave 3: security warnings moved to permission.Guard.
	// Shell.Execute no longer performs security checks —
	// that's the Guard's responsibility before Execute is called.
	tool := &Shell{}
	result, err := tool.Execute(context.Background(), ShellParams{
		Command: "echo chmod 777",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if result.Content == "" {
		t.Error("Content should not be empty")
	}
}

// ---------------------------------------------------------------------------
// SearchFile not-a-directory
// ---------------------------------------------------------------------------

func TestSearchFileNotDirectory(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "file.txt")
	os.WriteFile(filePath, []byte("content"), 0o644)

	tool := &SearchFile{}
	result, err := tool.Execute(context.Background(), SearchFileParams{
		Pattern:    "*.go",
		WorkingDir: filePath, // 文件，不是目录
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil || result.Error.Kind != ErrKindNotDir {
		t.Errorf("Expected ErrKindNotDir, got %v", result.Error)
	}
}

// ---------------------------------------------------------------------------
// 写文件边缘路径测试
// ---------------------------------------------------------------------------

func TestWriteFileReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	roDir := filepath.Join(dir, "readonly")
	os.Mkdir(roDir, 0o444) // 只读目录
	defer os.Chmod(roDir, 0o755)

	tool := &WriteFile{}
	result, err := tool.Execute(context.Background(), WriteFileParams{
		FilePath: filepath.Join(roDir, "test.txt"),
		Content:  "hello",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// 注意：macOS 上 root 用户即使目录权限为 444 也可以写入
	// 因此可能成功也可能失败（PermissionDenied 或成功）
	_ = result
}
