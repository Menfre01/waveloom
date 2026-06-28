package agentloop

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadToolTimeout_FileNotExists(t *testing.T) {
	d, ok, err := LoadToolTimeout("/nonexistent/path/settings.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected ok=false for nonexistent file")
	}
	if d != 0 {
		t.Errorf("expected zero duration, got %v", d)
	}
}

func TestLoadToolTimeout_EmptyPath(t *testing.T) {
	d, ok, err := LoadToolTimeout("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected ok=false for empty path")
	}
	if d != 0 {
		t.Errorf("expected zero duration, got %v", d)
	}
}

func TestLoadToolTimeout_ValidValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	writeFile(t, path, `{"tool_timeout": "5m30s"}`)

	d, ok, err := LoadToolTimeout(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if d != 5*time.Minute+30*time.Second {
		t.Errorf("expected 5m30s, got %v", d)
	}
}

func TestLoadToolTimeout_Seconds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	writeFile(t, path, `{"tool_timeout": "90s"}`)

	d, ok, err := LoadToolTimeout(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if d != 90*time.Second {
		t.Errorf("expected 90s, got %v", d)
	}
}

func TestLoadToolTimeout_Zero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	writeFile(t, path, `{"tool_timeout": "0s"}`)

	d, ok, err := LoadToolTimeout(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for explicit 0s")
	}
	if d != 0 {
		t.Errorf("expected 0, got %v", d)
	}
}

func TestLoadToolTimeout_EmptyField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// 文件存在但无 tool_timeout 字段
	writeFile(t, path, `{"other_field": "value"}`)

	d, ok, err := LoadToolTimeout(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected ok=false when field is missing")
	}
	if d != 0 {
		t.Errorf("expected zero duration, got %v", d)
	}
}

func TestLoadToolTimeout_InvalidFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	writeFile(t, path, `{"tool_timeout": "not-a-duration"}`)

	_, _, err := LoadToolTimeout(path)
	if err == nil {
		t.Fatal("expected error for invalid duration format")
	}
}

func TestLoadToolTimeout_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	writeFile(t, path, `{invalid json`)

	_, _, err := LoadToolTimeout(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadToolTimeout_IgnoresOtherFields(t *testing.T) {
	// 验证 tool_timeout 与其他 settings.json 字段共存
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	writeFile(t, path, `{
		"llm": {"provider": "deepseek"},
		"tool_timeout": "2m",
		"compaction": {"tier": 1}
	}`)

	d, ok, err := LoadToolTimeout(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if d != 2*time.Minute {
		t.Errorf("expected 2m, got %v", d)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
}
