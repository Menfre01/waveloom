package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRegression_ValidateResolvedPathDiagnostics — not-been-read 错误消息必须
// 包含解析后的路径与可操作的恢复指引。
//
// REGRESSION: 旧消息只有 "file has not been read yet — use read tool first",
// LLM 无法判断是"真没读过"还是"hunk header 路径被错误解析(双重嵌套)",
// 只能盲目 re-read 重试(实测 re-read 两次均无效)。
func TestRegression_ValidateResolvedPathDiagnostics(t *testing.T) {
	s := NewReadStateStore()
	_, reason := s.Validate("/proj/pkg/sandbox/x_test.go")
	if !strings.Contains(reason, "/proj/pkg/sandbox/x_test.go") {
		t.Errorf("reason should contain resolved path: %q", reason)
	}
	if !strings.Contains(reason, "omit the '*** Update File:' header") {
		t.Errorf("reason should hint at header omission: %q", reason)
	}
}

// TestRegression_ReadStateKeyNormalized — Record/Get/Validate/Update 的 key
// 统一 Clean 归一化,路径写法差异(./ 前缀、冗余分隔符)不导致 miss。
func TestRegression_ReadStateKeyNormalized(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "x.go")
	if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewReadStateStore()
	// 带 ./ 冗余段的路径写法 Record,Clean 路径 Validate 应命中
	s.Record(filepath.Join(dir, ".", "x.go"), "content")
	if ok, reason := s.Validate(filePath); !ok {
		t.Errorf("Validate with cleaned path should hit Record with ./ path: %s", reason)
	}
	s.Update(filePath, "content2")
	if state := s.Get(filepath.Join(dir, ".", "x.go")); state == nil || state.Content != "content2" {
		t.Error("Update/Get with different path spellings should share the normalized key")
	}
}
