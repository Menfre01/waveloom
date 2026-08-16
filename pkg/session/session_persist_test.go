package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Menfre01/waveloom/pkg/compaction"
	"github.com/Menfre01/waveloom/pkg/filehistory"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/task"
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

	if err := SaveSessionToFile(path, "", messages, stats, nil, nil, nil, nil, time.Time{}); err != nil {
		t.Fatalf("SaveSessionToFile: %v", err)
	}

	loaded, loadedStats, compData, sid, _, _, _, _, _, _, err := LoadSessionFromFile(path)
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

// TestSaveAndLoad_Name 验证 session name 写入/读取/覆盖语义。
func TestSaveAndLoad_Name(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	messages := []llm.Message{{Role: llm.RoleUser, Content: "hello"}}

	// 首次保存:写入 name
	if err := SaveSessionToFile(path, "  修复登录 bug  ", messages, Stats{}, nil, nil, nil, nil, time.Time{}); err != nil {
		t.Fatalf("SaveSessionToFile: %v", err)
	}
	_, _, _, _, name, _, _, _, _, _, err := LoadSessionFromFile(path)
	if err != nil {
		t.Fatalf("LoadSessionFromFile: %v", err)
	}
	if name != "修复登录 bug" {
		t.Errorf("name = %q, want %q(TrimSpace 后)", name, "修复登录 bug")
	}

	// 第二次保存:name 为空 → 保留已有 name
	if err := SaveSessionToFile(path, "", messages, Stats{}, nil, nil, nil, nil, time.Time{}); err != nil {
		t.Fatalf("SaveSessionToFile(empty name): %v", err)
	}
	_, _, _, _, name2, _, _, _, _, _, err := LoadSessionFromFile(path)
	if err != nil {
		t.Fatalf("LoadSessionFromFile: %v", err)
	}
	if name2 != "修复登录 bug" {
		t.Errorf("name = %q after empty-name save, want %q(保留)", name2, "修复登录 bug")
	}

	// 第三次保存:name 非空 → 覆盖
	if err := SaveSessionToFile(path, "重构 TUI", messages, Stats{}, nil, nil, nil, nil, time.Time{}); err != nil {
		t.Fatalf("SaveSessionToFile(override): %v", err)
	}
	_, _, _, _, name3, _, _, _, _, _, err := LoadSessionFromFile(path)
	if err != nil {
		t.Fatalf("LoadSessionFromFile: %v", err)
	}
	if name3 != "重构 TUI" {
		t.Errorf("name = %q after override, want %q", name3, "重构 TUI")
	}
}

// TestSaveAndLoad_NameTruncate 验证超长 name 截断到 maxSessionNameLen。
func TestSaveAndLoad_NameTruncate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	messages := []llm.Message{{Role: llm.RoleUser, Content: "hello"}}

	long := strings.Repeat("长", maxSessionNameLen+50)
	if err := SaveSessionToFile(path, long, messages, Stats{}, nil, nil, nil, nil, time.Time{}); err != nil {
		t.Fatalf("SaveSessionToFile: %v", err)
	}
	_, _, _, _, name, _, _, _, _, _, err := LoadSessionFromFile(path)
	if err != nil {
		t.Fatalf("LoadSessionFromFile: %v", err)
	}
	if got := len([]rune(name)); got != maxSessionNameLen {
		t.Errorf("name rune len = %d, want %d", got, maxSessionNameLen)
	}
}

// TestLoadSessionFromFile_LegacyNoName 验证旧 session 文件(无 name 字段)兼容:name 为空。
func TestLoadSessionFromFile_LegacyNoName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.json")
	// 手工构造旧格式 JSON(无 name 字段)
	data := `{
  "session_id": "legacy-1",
  "version": "dev",
  "created_at": "2025-01-01T00:00:00Z",
  "updated_at": "2025-01-01T00:00:00Z",
  "messages": [{"role": "user", "content": "hi"}],
  "stats": {"message_count": 1}
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, name, _, _, _, _, _, err := LoadSessionFromFile(path)
	if err != nil {
		t.Fatalf("LoadSessionFromFile: %v", err)
	}
	if name != "" {
		t.Errorf("legacy name = %q, want empty", name)
	}
}

func TestSaveAndLoad_RoundTripWithToolCalls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session_tc.json")

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "You are helpful."},
		{Role: llm.RoleUser, Content: "read a file"},
		{
			Role:             llm.RoleAssistant,
			ReasoningContent: "I need to read the file.",
			ToolCalls: []llm.ToolCall{
				{ID: "call_1", Name: "read_file", Arguments: `{"path":"/tmp/test"}`},
				{ID: "call_2", Name: "grep", Arguments: `{"pattern":"foo"}`},
			},
		},
		{Role: llm.RoleTool, ToolCallID: "call_1", Name: "read_file", Content: "file contents here"},
		{Role: llm.RoleTool, ToolCallID: "call_2", Name: "grep", Content: "no matches"},
		{Role: llm.RoleAssistant, Content: "The file contains..."},
	}
	stats := Stats{TotalTurns: 1, TotalPromptTokens: 500, TotalCompletionTokens: 200, MessageCount: 6}

	if err := SaveSessionToFile(path, "", messages, stats, nil, nil, nil, nil, time.Time{}); err != nil {
		t.Fatalf("SaveSessionToFile: %v", err)
	}

	loaded, _, _, _, _, _, _, _, _, _, err := LoadSessionFromFile(path)
	if err != nil {
		t.Fatalf("LoadSessionFromFile: %v", err)
	}
	if len(loaded) != 6 {
		t.Fatalf("expected 6 messages, got %d", len(loaded))
	}

	// 验证 assistant 消息的 tool_calls 完整恢复
	asstMsg := loaded[2]
	if asstMsg.Role != llm.RoleAssistant {
		t.Fatalf("expected assistant at index 2, got %s", asstMsg.Role)
	}
	if asstMsg.ReasoningContent != "I need to read the file." {
		t.Errorf("ReasoningContent = %q, want %q", asstMsg.ReasoningContent, "I need to read the file.")
	}
	if len(asstMsg.ToolCalls) != 2 {
		t.Fatalf("expected 2 ToolCalls, got %d", len(asstMsg.ToolCalls))
	}
	if asstMsg.ToolCalls[0].ID != "call_1" {
		t.Errorf("ToolCalls[0].ID = %q, want call_1", asstMsg.ToolCalls[0].ID)
	}
	if asstMsg.ToolCalls[0].Name != "read_file" {
		t.Errorf("ToolCalls[0].Name = %q, want read_file", asstMsg.ToolCalls[0].Name)
	}
	if asstMsg.ToolCalls[0].Arguments != `{"path":"/tmp/test"}` {
		t.Errorf("ToolCalls[0].Arguments = %q, want %q", asstMsg.ToolCalls[0].Arguments, `{"path":"/tmp/test"}`)
	}
	if asstMsg.ToolCalls[1].ID != "call_2" {
		t.Errorf("ToolCalls[1].ID = %q, want call_2", asstMsg.ToolCalls[1].ID)
	}
	if asstMsg.ToolCalls[1].Name != "grep" {
		t.Errorf("ToolCalls[1].Name = %q, want grep", asstMsg.ToolCalls[1].Name)
	}
	if asstMsg.ToolCalls[1].Arguments != `{"pattern":"foo"}` {
		t.Errorf("ToolCalls[1].Arguments = %q, want %q", asstMsg.ToolCalls[1].Arguments, `{"pattern":"foo"}`)
	}

	// 验证 tool 消息完整
	tool1 := loaded[3]
	if tool1.Role != llm.RoleTool || tool1.ToolCallID != "call_1" || tool1.Content != "file contents here" {
		t.Errorf("tool1 mismatch: role=%s tool_call_id=%s content=%s", tool1.Role, tool1.ToolCallID, tool1.Content)
	}
	tool2 := loaded[4]
	if tool2.Role != llm.RoleTool || tool2.ToolCallID != "call_2" || tool2.Content != "no matches" {
		t.Errorf("tool2 mismatch: role=%s tool_call_id=%s content=%s", tool2.Role, tool2.ToolCallID, tool2.Content)
	}

	// 验证最终 assistant 消息
	final := loaded[5]
	if final.Content != "The file contains..." {
		t.Errorf("final content = %q", final.Content)
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
		TotalTurns: 5,
	}

	if err := SaveSessionToFile(path, "", messages, stats, compData, nil, nil, nil, time.Time{}); err != nil {
		t.Fatalf("SaveSessionToFile: %v", err)
	}

	_, loadedStats, loadedComp, _, _, _, _, _, _, _, err := LoadSessionFromFile(path)
	if err != nil {
		t.Fatalf("LoadSessionFromFile: %v", err)
	}
	if loadedStats.TotalTurns != 1 {
		t.Errorf("TotalTurns mismatch: %d", loadedStats.TotalTurns)
	}
	if loadedComp == nil {
		t.Fatal("compaction data not loaded")
		return
	}
	if loadedComp.TotalTurns != 5 {
		t.Errorf("CompactionData.TotalTurns mismatch: %d, want 5", loadedComp.TotalTurns)
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
	if err := SaveSessionToFile(path, "", messages, stats, nil, nil, nil, nil, time.Time{}); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// 读取 session ID
	_, _, _, sid1, _, _, _, _, _, _, _ := LoadSessionFromFile(path)

	// 第二次保存（模拟 Append 新的 turn）
	messages = append(messages, llm.Message{Role: llm.RoleAssistant, Content: "response"})
	stats.TotalTurns = 2
	stats.MessageCount = 2

	if err := SaveSessionToFile(path, "", messages, stats, nil, nil, nil, nil, time.Time{}); err != nil {
		t.Fatalf("second save: %v", err)
	}

	loaded, loadedStats, _, sid2, _, _, _, _, _, _, _ := LoadSessionFromFile(path)
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
	msgs, stats, comp, sid, _, tasks, _, _, _, _, err := LoadSessionFromFile("/nonexistent/path.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msgs != nil || comp != nil || sid != "" || tasks != nil {
		t.Error("expected nil/empty for nonexistent file")
	}
	if stats.TotalTurns != 0 {
		t.Error("expected zero stats for nonexistent file")
	}
}

func TestSessionPersist_WithTasks(t *testing.T) {
	task.DefaultRegistry.Reset()
	defer task.DefaultRegistry.Reset()

	now := time.Now()
	task.DefaultRegistry.Register("t1", &task.TaskInfo{
		ID: "t1", PID: 1, Command: "cmd1", Status: task.TaskRunning,
		StartTime: now,
	})
	task.DefaultRegistry.Register("t2", &task.TaskInfo{
		ID: "t2", PID: 2, Command: "cmd2", Status: task.TaskCompleted,
		StartTime: now, CompletedTime: now, ExitCode: 0,
	})

	path := filepath.Join(t.TempDir(), "test-session.json")

	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "test"},
	}
	if err := SaveSessionToFile(path, "", msgs, Stats{TotalTurns: 1}, nil, nil, nil, nil, time.Time{}); err != nil {
		t.Fatalf("SaveSessionToFile: %v", err)
	}

	loaded, loadedStats, _, _, _, loadedTasks, _, _, _, _, err := LoadSessionFromFile(path)
	if err != nil {
		t.Fatalf("LoadSessionFromFile: %v", err)
	}
	if len(loaded) != 1 {
		t.Errorf("expected 1 message, got %d", len(loaded))
	}
	if loadedStats.TotalTurns != 1 {
		t.Errorf("TotalTurns = %d, want 1", loadedStats.TotalTurns)
	}
	if len(loadedTasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(loadedTasks))
	}

	found := map[string]bool{}
	for _, ti := range loadedTasks {
		found[ti.ID] = true
		if ti.ID == "t2" && ti.Status != task.TaskCompleted {
			t.Errorf("t2 status: %s, want completed", ti.Status)
		}
	}
	if !found["t1"] || !found["t2"] {
		t.Errorf("missing tasks in loaded data: %v", found)
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
	if !strings.HasSuffix(dir, "sessions") || !strings.Contains(dir, filepath.FromSlash("myproject/sessions")) {
		t.Errorf("expected sessions dir, got %q", dir)
	}
}

func TestResolveSessionDir_OverrideAbsolute(t *testing.T) {
	dir, err := ResolveSessionDir(filepath.FromSlash("/tmp/cwd"), filepath.FromSlash("/custom/sessions"))
	if err != nil {
		t.Fatalf("ResolveSessionDir: %v", err)
	}
	if !strings.Contains(dir, filepath.FromSlash("/custom/sessions")) {
		t.Errorf("expected path containing %q, got %q", filepath.FromSlash("/custom/sessions"), dir)
	}
}

func TestResolveSessionDir_OverrideRelative(t *testing.T) {
	cwdSlash := filepath.FromSlash("/tmp/myproject")
	dir, err := ResolveSessionDir(cwdSlash, filepath.FromSlash(".waveloom/sessions"))
	if err != nil {
		t.Fatalf("ResolveSessionDir: %v", err)
	}
	if !strings.HasSuffix(dir, cwdSlash) && !strings.Contains(dir, cwdSlash) {
		t.Errorf("expected path under cwd, got %q", dir)
	}
}

func TestResolveSessionDir_EnvVar(t *testing.T) {
	_ = os.Setenv("WAVELOOM_SESSION_DIR", "/env/sessions")
	defer func() { _ = os.Unsetenv("WAVELOOM_SESSION_DIR") }()

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

// ── P0 测试：统一 JSONL 加载优先级 ──

// TestLoadSessionFromFile_JSONLPriority 验证 JSONL 优先于 JSON Messages 字段加载。
func TestLoadSessionFromFile_JSONLPriority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")

	// 先写 JSON（含 3 条消息）和一条 JSONL（含 5 条不同的消息）
	jsonMessages := []llm.Message{
		{Role: llm.RoleUser, ID: "json-1", Content: "json msg 1"},
		{Role: llm.RoleAssistant, ID: "json-2", Content: "json msg 2"},
		{Role: llm.RoleUser, ID: "json-3", Content: "json msg 3"},
	}
	if err := SaveSessionToFile(path, "", jsonMessages, Stats{}, nil, nil, nil, nil, time.Time{}); err != nil {
		t.Fatalf("SaveSessionToFile: %v", err)
	}

	// 写统一 JSONL（含 5 条不同的消息）
	sid := strings.TrimSuffix(filepath.Base(path), ".json")
	jlPath := TranscriptPath(filepath.Dir(path), sid)
	jlMessages := []llm.Message{
		{Role: llm.RoleUser, ID: "jl-1", Content: "jsonl msg 1"},
		{Role: llm.RoleAssistant, ID: "jl-2", Content: "jsonl msg 2"},
		{Role: llm.RoleUser, ID: "jl-3", Content: "jsonl msg 3"},
		{Role: llm.RoleAssistant, ID: "jl-4", Content: "jsonl msg 4"},
		{Role: llm.RoleUser, ID: "jl-5", Content: "jsonl msg 5"},
	}
	entries := MessagesToTranscriptEntries(jlMessages, nil, sid, version(), "/cwd", "")
	if err := WriteTranscriptEntries(jlPath, entries); err != nil {
		t.Fatalf("WriteTranscriptEntries: %v", err)
	}

	loaded, _, _, _, _, _, _, _, _, _, err := LoadSessionFromFile(path)
	if err != nil {
		t.Fatalf("LoadSessionFromFile: %v", err)
	}
	if len(loaded) != 5 {
		t.Fatalf("expected 5 messages from JSONL, got %d", len(loaded))
	}
	if loaded[0].Content != "jsonl msg 1" {
		t.Errorf("first message = %q, want %q", loaded[0].Content, "jsonl msg 1")
	}
}

// TestSaveSessionToFile_WithPlanModeAndFileHistory 验证 planMode 和 fileHistory 正确持久化。
func TestSaveSessionToFile_WithPlanModeAndFileHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")

	planMode := &sessionPlanMode{Active: true, PlanFile: "/tmp/test-plan.md"}
	fh := &filehistory.SnapshotData{
		Snapshots:    []filehistory.SnapshotEntry{},
		TrackedFiles: []string{"test.go"},
		SnapshotSeq:  3,
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, ID: "m1", Content: "hello"},
	}
	if err := SaveSessionToFile(path, "", messages, Stats{}, nil, nil, planMode, fh, time.Time{}); err != nil {
		t.Fatalf("SaveSessionToFile: %v", err)
	}

	_, _, _, _, _, _, _, loadedPlan, loadedFH, _, err := LoadSessionFromFile(path)
	if err != nil {
		t.Fatalf("LoadSessionFromFile: %v", err)
	}
	if loadedPlan == nil {
		t.Fatal("expected planMode to be loaded")
	}
	if !loadedPlan.Active || loadedPlan.PlanFile != "/tmp/test-plan.md" {
		t.Errorf("planMode = %+v, want active=true, planFile=/tmp/test-plan.md", loadedPlan)
	}
	if loadedFH == nil {
		t.Fatal("expected fileHistory to be loaded")
	}
	if loadedFH.SnapshotSeq != 3 || len(loadedFH.TrackedFiles) != 1 {
		t.Errorf("fileHistory seq=%d files=%v, want seq=3 files=[test.go]", loadedFH.SnapshotSeq, loadedFH.TrackedFiles)
	}
}

// TestRegression_HTMLUnescapeInToolCallArgs 验证 TranscriptEntry round-trip 后
// tool call arguments 中的 & 和 > 等 HTML 特殊字符不被转义为 \u0026 / \u003e。
// 根因: convertToContentBlocks 曾使用 json.Marshal(EscapeHTML=true) 序列化
// anthropicMessage,导致 json.RawMessage(Input) 内的字符串被 HTML 编码,round-trip 后
// extractField 提取到转义后的字面量,NormalizeShellCommand 无法匹配 cd && 前缀。
func TestRegression_HTMLUnescapeInToolCallArgs(t *testing.T) {
	args := `{"command":"cd /tmp && make build 2>&1"}`
	msg := llm.Message{
		ID:   "msg-1",
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{
			{ID: "call-1", Name: "bash", Arguments: args},
		},
	}

	entry := NewTranscriptEntry(msg, nil, "sid", "1.0", "/cwd", "main")
	restored := entry.ToMessage()

	if len(restored.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(restored.ToolCalls))
	}
	if restored.ToolCalls[0].Arguments != args {
		t.Errorf("tool call arguments mismatch after round-trip:\n  want: %s\n  got:  %s", args, restored.ToolCalls[0].Arguments)
	}

	// 额外检测:确保 JSONL 写入的原始字节也不含 \u0026
	entries := MessagesToTranscriptEntries([]llm.Message{msg}, nil, "sid", "1.0", "/cwd", "main")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	raw := string(entries[0].Message)
	if strings.Contains(raw, `\u0026`) {
		t.Errorf("JSONL Message field contains \\u0026 escape: %s", raw)
	}
	if strings.Contains(raw, `\u003e`) {
		t.Errorf("JSONL Message field contains \\u003e escape: %s", raw)
	}
}
