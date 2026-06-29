package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/compaction"
	"github.com/Menfre01/waveloom/pkg/llm"
)

// ---------------------------------------------------------------------------
// SaveSessionToFile / LoadSessionFromFile / loadSessionFile
// ---------------------------------------------------------------------------

func TestSaveAndLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "You are helpful."},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "Hi there!"},
	}
	stats := Stats{
		TotalTurns:            3,
		TotalPromptTokens:     150,
		TotalCompletionTokens: 80,
		TotalCacheHitTokens:   120,
		TotalCacheMissTokens:  30,
		TotalReasoningTokens:  40,
		TotalDurationMs:       5000,
		MessageCount:          3,
	}

	if err := SaveSessionToFile(path, messages, stats, nil); err != nil {
		t.Fatalf("SaveSessionToFile: %v", err)
	}

	loaded, loadedStats, compData, sid, err := LoadSessionFromFile(path)
	if err != nil {
		t.Fatalf("LoadSessionFromFile: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(loaded))
	}
	if loaded[0].Content != "You are helpful." {
		t.Errorf("system message mismatch: %q", loaded[0].Content)
	}
	if loadedStats.TotalTurns != 3 {
		t.Errorf("TotalTurns mismatch: %d", loadedStats.TotalTurns)
	}
	if loadedStats.TotalCacheHitTokens != 120 {
		t.Errorf("TotalCacheHitTokens mismatch: %d", loadedStats.TotalCacheHitTokens)
	}
	if compData != nil {
		t.Error("compData should be nil when not saved")
	}
	if sid == "" {
		t.Error("session ID should not be empty")
	}
}

func TestSaveAndLoad_WithCompaction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session_cp.json")

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "s"},
		{Role: llm.RoleUser, Content: "u"},
	}
	stats := Stats{TotalTurns: 1, MessageCount: 2}

	compData := &compaction.CompactionData{
		Decisions: compaction.NewDecisionSetFromList([]compaction.CompactionDecision{
			{MsgIndex: 0, DecisionTier: 1, Action: "snip", TokensSaved: 50, AppliedAt: 1},
		}),
		Watermark: compaction.WatermarkState{
			ContextLimit: 1000000,
			Tier1Cursor:  3,
			Tier2Cursor:  3,
			Tier3Cursor:  3,
		},
		Summaries:  []string{"summary 1"},
		TotalTurns: 1,
	}

	if err := SaveSessionToFile(path, messages, stats, compData); err != nil {
		t.Fatalf("SaveSessionToFile: %v", err)
	}

	_, loadedStats, loadedComp, _, err := LoadSessionFromFile(path)
	if err != nil {
		t.Fatalf("LoadSessionFromFile: %v", err)
	}
	if loadedStats.TotalTurns != 1 {
		t.Errorf("TotalTurns mismatch: %d", loadedStats.TotalTurns)
	}
	if loadedComp == nil {
		t.Fatal("compaction data not loaded")
	}
	if len(loadedComp.Summaries) != 1 || loadedComp.Summaries[0] != "summary 1" {
		t.Errorf("summaries mismatch: %v", loadedComp.Summaries)
	}
	if loadedComp.Watermark.ContextLimit != 1000000 {
		t.Errorf("context limit mismatch: %d", loadedComp.Watermark.ContextLimit)
	}
}

func TestSave_UpdateExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "update.json")

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "first"},
	}
	stats := Stats{TotalTurns: 1, MessageCount: 1}

	// 第一次保存
	if err := SaveSessionToFile(path, messages, stats, nil); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// 读取 session ID
	_, _, _, sid1, _ := LoadSessionFromFile(path)

	// 第二次保存（模拟 Append 新的 turn）
	messages = append(messages, llm.Message{Role: llm.RoleAssistant, Content: "response"})
	stats.TotalTurns = 2
	stats.MessageCount = 2

	if err := SaveSessionToFile(path, messages, stats, nil); err != nil {
		t.Fatalf("second save: %v", err)
	}

	loaded, loadedStats, _, sid2, _ := LoadSessionFromFile(path)
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded))
	}
	if loadedStats.TotalTurns != 2 {
		t.Fatalf("expected TotalTurns=2, got %d", loadedStats.TotalTurns)
	}
	// session ID 应保持不变
	if sid1 != sid2 {
		t.Errorf("session ID changed: %q → %q", sid1, sid2)
	}
}

func TestLoadSessionFile_NotFound(t *testing.T) {
	sf, err := loadSessionFile("/nonexistent/path.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sf != nil {
		t.Error("expected nil for nonexistent file")
	}
}

func TestLoadSessionFile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := loadSessionFile(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadSessionFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := loadSessionFile(path)
	if err == nil {
		t.Fatal("expected error for empty file (invalid JSON)")
	}
}

func TestLoadSessionFromFile_NotFound(t *testing.T) {
	msgs, stats, comp, sid, err := LoadSessionFromFile("/nonexistent/path.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msgs != nil || comp != nil || sid != "" {
		t.Error("expected nil/empty for nonexistent file")
	}
	if stats.TotalTurns != 0 {
		t.Error("expected zero stats for nonexistent file")
	}
}

// ---------------------------------------------------------------------------
// RemoveSessionFile
// ---------------------------------------------------------------------------

func TestRemoveSessionFile_Exists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RemoveSessionFile(path); err != nil {
		t.Fatalf("RemoveSessionFile: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should not exist after removal")
	}
}

func TestRemoveSessionFile_NotFound(t *testing.T) {
	err := RemoveSessionFile("/nonexistent/file.json")
	if err != nil {
		t.Fatalf("unexpected error for nonexistent file: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ResolveSessionDir
// ---------------------------------------------------------------------------

func TestResolveSessionDir_Default(t *testing.T) {
	// 不设置 override 和环境变量 → 使用 ~/.waveloom/<project>/sessions/
	cwd := "/Users/test/myproject"
	dir, err := ResolveSessionDir(cwd, "")
	if err != nil {
		t.Fatalf("ResolveSessionDir: %v", err)
	}
	if !strings.Contains(dir, ".waveloom") {
		t.Errorf("expected .waveloom in path, got %q", dir)
	}
	if !strings.Contains(dir, "myproject") {
		t.Errorf("expected project name in path, got %q", dir)
	}
	if !strings.HasSuffix(dir, "sessions") || !strings.Contains(dir, "myproject/sessions") {
		t.Errorf("expected sessions dir, got %q", dir)
	}
}

func TestResolveSessionDir_OverrideAbsolute(t *testing.T) {
	dir, err := ResolveSessionDir("/tmp/cwd", "/custom/sessions")
	if err != nil {
		t.Fatalf("ResolveSessionDir: %v", err)
	}
	if dir != "/custom/sessions" {
		t.Errorf("expected /custom/sessions, got %q", dir)
	}
}

func TestResolveSessionDir_OverrideRelative(t *testing.T) {
	dir, err := ResolveSessionDir("/tmp/myproject", ".waveloom/sessions")
	if err != nil {
		t.Fatalf("ResolveSessionDir: %v", err)
	}
	if !strings.HasPrefix(dir, "/tmp/myproject") {
		t.Errorf("expected path under cwd, got %q", dir)
	}
}

func TestResolveSessionDir_EnvVar(t *testing.T) {
	os.Setenv("WAVELOOM_SESSION_DIR", "/env/sessions")
	defer os.Unsetenv("WAVELOOM_SESSION_DIR")

	dir, err := ResolveSessionDir("/tmp/cwd", "")
	if err != nil {
		t.Fatalf("ResolveSessionDir: %v", err)
	}
	if dir != "/env/sessions" {
		t.Errorf("expected /env/sessions, got %q", dir)
	}
}

// ---------------------------------------------------------------------------
// projectSlug
// ---------------------------------------------------------------------------

func TestProjectSlug(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/Users/test/myproject", "myproject"},
		{"/home/user/go/src/github.com/org/repo", "repo"},
		{"/tmp", "tmp"},
	}
	for _, tt := range tests {
		got := projectSlug(tt.path)
		if got != tt.want {
			t.Errorf("projectSlug(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// NewSessionID
// ---------------------------------------------------------------------------

func TestNewSessionID_Format(t *testing.T) {
	id := NewSessionID()
	// 格式: 8-4-4-4-12 hex（36 chars）
	if len(id) != 36 {
		t.Errorf("expected 36 chars, got %d: %q", len(id), id)
	}
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Errorf("expected 5 parts, got %d: %q", len(parts), id)
	}
	if len(parts[0]) != 8 || len(parts[1]) != 4 || len(parts[2]) != 4 || len(parts[3]) != 4 || len(parts[4]) != 12 {
		t.Errorf("unexpected segment lengths: %v", parts)
	}
}

func TestNewSessionID_Unique(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := NewSessionID()
		if ids[id] {
			t.Fatalf("duplicate ID: %s", id)
		}
		ids[id] = true
	}
}

// ---------------------------------------------------------------------------
// LoadSessionDir
// ---------------------------------------------------------------------------

func TestLoadSessionDir_ValidSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	content := `{"session": {"dir": "/custom/sessions"}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	result := LoadSessionDir(path)
	if result != "/custom/sessions" {
		t.Errorf("expected /custom/sessions, got %q", result)
	}
}

func TestLoadSessionDir_NoSessionBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	content := `{"llm": {"model": "test"}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	result := LoadSessionDir(path)
	if result != "" {
		t.Errorf("expected empty, got %q", result)
	}
}

func TestLoadSessionDir_FileNotFound(t *testing.T) {
	result := LoadSessionDir("/nonexistent/settings.json")
	if result != "" {
		t.Errorf("expected empty for nonexistent file, got %q", result)
	}
}

func TestLoadSessionDir_EmptyPath(t *testing.T) {
	result := LoadSessionDir("")
	if result != "" {
		t.Errorf("expected empty for empty path, got %q", result)
	}
}

func TestLoadSessionDir_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := LoadSessionDir(path)
	if result != "" {
		t.Errorf("expected empty for invalid JSON, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// version
// ---------------------------------------------------------------------------

func TestVersion_ReturnsBuildVersion(t *testing.T) {
	orig := BuildVersion
	defer func() { BuildVersion = orig }()

	BuildVersion = "v1.2.3"
	if v := version(); v != "v1.2.3" {
		t.Errorf("expected v1.2.3, got %q", v)
	}
}

func TestVersion_Default(t *testing.T) {
	orig := BuildVersion
	defer func() { BuildVersion = orig }()

	BuildVersion = ""
	if v := version(); v != "" {
		t.Errorf("expected empty, got %q", v)
	}
}
