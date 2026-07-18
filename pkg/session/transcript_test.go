package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// ---------------------------------------------------------------------------
// Transcript
// ---------------------------------------------------------------------------

func TestTranscriptAppendAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	entries := MessagesToTranscriptEntries(
		[]llm.Message{
			{Role: llm.RoleUser, ID: "a1", Content: "hello"},
			{Role: llm.RoleAssistant, ID: "a2", Content: "hi there"},
			{Role: llm.RoleTool, ID: "a3", Content: "ok\n", ToolCallID: "call-1", Name: "bash"},
		},
		nil, "sid", "v1", "/cwd", "",
	)

	if err := WriteTranscriptEntries(path, entries); err != nil {
		t.Fatalf("WriteTranscriptEntries: %v", err)
	}

	loaded, err := LoadTranscriptEntries(path)
	if err != nil {
		t.Fatalf("LoadTranscriptEntries: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(loaded))
	}

	// Verify first entry is a user message
	msg0 := loaded[0].ToMessage()
	if msg0.Content != "hello" {
		t.Errorf("entry 0 content = %q, want %q", msg0.Content, "hello")
	}

	// Verify third entry is a tool result
	msg2 := loaded[2].ToMessage()
	if msg2.Content != "ok\n" {
		t.Errorf("entry 2 content = %q, want %q", msg2.Content, "ok\n")
	}
}

func TestTranscriptLoadEmpty(t *testing.T) {
	entries, err := LoadTranscriptEntries("/nonexistent/path.jsonl")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil for nonexistent file, got %v", entries)
	}
}

func TestTranscriptLoadTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.jsonl")

	// Write more than maxTranscriptEntries
	for i := 0; i < maxTranscriptEntries+100; i++ {
		entries := MessagesToTranscriptEntries(
			[]llm.Message{{Role: llm.RoleSystem, ID: "id-" + string(rune('a'+i%26)), Content: "msg"}},
			nil, "sid", "v1", "/cwd", "",
		)
		_ = AppendTranscriptEntries(path, entries)
	}

	loaded, err := LoadTranscriptEntriesTail(path)
	if err != nil {
		t.Fatalf("LoadTranscriptEntriesTail: %v", err)
	}
	if len(loaded) != maxTranscriptEntries {
		t.Errorf("expected %d entries (truncated), got %d", maxTranscriptEntries, len(loaded))
	}
}

func TestTranscriptPath(t *testing.T) {
	p := TranscriptPath(filepath.FromSlash("/tmp/sessions"), "abc-123")
	expected := filepath.FromSlash("/tmp/sessions/abc-123.jsonl")
	if p != expected {
		t.Errorf("TranscriptPath = %q, want %q", p, expected)
	}
}

// ---------------------------------------------------------------------------
// Recent Sessions
// ---------------------------------------------------------------------------

func TestRecentUpdateAndLoad(t *testing.T) {
	dir := t.TempDir()

	if err := UpdateRecentSessions(dir, "session-1", 5); err != nil {
		t.Fatalf("UpdateRecentSessions: %v", err)
	}
	if err := UpdateRecentSessions(dir, "session-2", 10); err != nil {
		t.Fatalf("UpdateRecentSessions: %v", err)
	}

	entries, err := LoadRecentSessions(dir)
	if err != nil {
		t.Fatalf("LoadRecentSessions: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// 最近的在最前
	if entries[0].ID != "session-2" {
		t.Errorf("first entry = %q, want %q", entries[0].ID, "session-2")
	}
	if entries[1].ID != "session-1" {
		t.Errorf("second entry = %q, want %q", entries[1].ID, "session-1")
	}
}

func TestRecentDeduplication(t *testing.T) {
	dir := t.TempDir()

	_ = UpdateRecentSessions(dir, "session-1", 5)
	_ = UpdateRecentSessions(dir, "session-1", 8) // 更新同一个

	entries, _ := LoadRecentSessions(dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after dedup, got %d", len(entries))
	}
	if entries[0].MessageCount != 8 {
		t.Errorf("MessageCount = %d, want 8", entries[0].MessageCount)
	}
}

func TestRecentMaxEntries(t *testing.T) {
	dir := t.TempDir()

	for i := 0; i < maxRecentSessions+5; i++ {
		id := "session-" + string(rune('a'+i%26))
		_ = UpdateRecentSessions(dir, id, 0)
	}

	entries, _ := LoadRecentSessions(dir)
	if len(entries) > maxRecentSessions {
		t.Errorf("expected at most %d entries, got %d", maxRecentSessions, len(entries))
	}
}

func TestRecentLoadEmpty(t *testing.T) {
	entries, err := LoadRecentSessions("/nonexistent/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil for nonexistent dir, got %v", entries)
	}
}

func TestContinueSessionID(t *testing.T) {
	dir := t.TempDir()

	// 空目录 → 空
	id, err := ContinueSessionID(dir)
	if err != nil || id != "" {
		t.Fatalf("expected empty, got %q (err=%v)", id, err)
	}

	_ = UpdateRecentSessions(dir, "last-session", 3)
	id, err = ContinueSessionID(dir)
	if err != nil || id != "last-session" {
		t.Fatalf("expected 'last-session', got %q (err=%v)", id, err)
	}
}

func TestRecentPath(t *testing.T) {
	p := RecentPath(filepath.FromSlash("/tmp/sessions"))
	want := filepath.FromSlash("/tmp/sessions/recent.json")
	if p != want {
		t.Errorf("RecentPath = %q, want %q", p, want)
	}
}

func TestRemoveTranscript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	// 创建文件
	entries := MessagesToTranscriptEntries(
		[]llm.Message{{Role: llm.RoleUser, ID: "x", Content: "x"}},
		nil, "sid", "v1", "/cwd", "",
	)
	if err := WriteTranscriptEntries(path, entries); err != nil {
		t.Fatal(err)
	}

	// 删除
	if err := RemoveTranscriptFile(path); err != nil {
		t.Fatalf("RemoveTranscriptFile: %v", err)
	}

	// 确认已删除
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should not exist after removal")
	}

	// 删除不存在的文件应静默成功
	if err := RemoveTranscriptFile(path); err != nil {
		t.Fatalf("RemoveTranscriptFile on nonexistent: %v", err)
	}
}

func TestTranscriptLoad_CorruptedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.jsonl")

	// 写入混合数据：有效行 + 损坏行 + 有效行
	e1 := NewTranscriptEntry(llm.Message{Role: llm.RoleUser, ID: "a1", Content: "line1"}, nil, "sid", "v1", "/cwd", "")
	e2 := NewTranscriptEntry(llm.Message{Role: llm.RoleAssistant, ID: "a2", Content: "line3"}, nil, "sid", "v1", "/cwd", "")

	data1, _ := json.Marshal(e1)
	data2, _ := json.Marshal(e2)
	raw := string(data1) + "\nthis is not json\n" + string(data2) + "\n"
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := LoadTranscriptEntries(path)
	if err != nil {
		t.Fatalf("LoadTranscriptEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 valid entries (corrupt skipped), got %d", len(entries))
	}

	msg0 := entries[0].ToMessage()
	if msg0.Content != "line1" {
		t.Errorf("entry0 content = %q, want %q", msg0.Content, "line1")
	}
	msg1 := entries[1].ToMessage()
	if msg1.Content != "line3" {
		t.Errorf("entry1 content = %q, want %q", msg1.Content, "line3")
	}
}

func TestAppendTranscriptEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "append.jsonl")

	// Write initial entries
	initial := MessagesToTranscriptEntries(
		[]llm.Message{{Role: llm.RoleUser, ID: "a1", Content: "first"}},
		nil, "sid", "v1", "/cwd", "",
	)
	if err := WriteTranscriptEntries(path, initial); err != nil {
		t.Fatal(err)
	}

	// Append more
	more := MessagesToTranscriptEntries(
		[]llm.Message{{Role: llm.RoleAssistant, ID: "a2", Content: "second"}},
		nil, "sid", "v1", "/cwd", "",
	)
	if err := AppendTranscriptEntries(path, more); err != nil {
		t.Fatal(err)
	}

	loaded, _ := LoadTranscriptEntries(path)
	if len(loaded) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(loaded))
	}
}

// ── P2 测试：content blocks 转换全覆盖 + fallback + parentUUID 链 ──

// TestConvertToContentBlocks_AllRoles 验证 llm.Message → Anthropic content blocks 映射。
func TestConvertToContentBlocks_AllRoles(t *testing.T) {
	tests := []struct {
		name string
		msg  llm.Message
	}{
		{"user", llm.Message{Role: llm.RoleUser, ID: "u1", Content: "hello"}},
		{"system", llm.Message{Role: llm.RoleSystem, ID: "s1", Content: "system prompt"}},
		{"assistant_text_only", llm.Message{Role: llm.RoleAssistant, ID: "a1", Content: "result is 42"}},
		{"assistant_tool_calls_only", llm.Message{Role: llm.RoleAssistant, ID: "a2", ToolCalls: []llm.ToolCall{
			{ID: "tc1", Name: "bash", Arguments: `{"command":"ls"}`},
		}}},
		{"assistant_mixed", llm.Message{Role: llm.RoleAssistant, ID: "a3", Content: "let me run", ToolCalls: []llm.ToolCall{
			{ID: "tc2", Name: "read", Arguments: `{"path":"f.txt"}`},
			{ID: "tc3", Name: "bash", Arguments: `{"command":"cat f.txt"}`},
		}}},
		{"tool", llm.Message{Role: llm.RoleTool, ID: "t1", Content: "file content\n", ToolCallID: "tc2", Name: "read"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := NewTranscriptEntry(tt.msg, nil, "sid", "v1", "/cwd", "")
			if entry.UUID != tt.msg.ID {
				t.Errorf("UUID = %q, want %q", entry.UUID, tt.msg.ID)
			}
			if entry.Message == nil {
				t.Fatal("Message is nil")
			}

			// 回环验证：ToMessage 应正确还原
			restored := entry.ToMessage()
			if restored.Role != tt.msg.Role {
				t.Errorf("role = %v, want %v", restored.Role, tt.msg.Role)
			}
			if tt.msg.Role != llm.RoleAssistant || tt.msg.Content != "" {
				if restored.Content != tt.msg.Content {
					t.Errorf("content = %q, want %q", restored.Content, tt.msg.Content)
				}
			}
			if len(restored.ToolCalls) != len(tt.msg.ToolCalls) {
				t.Errorf("tool calls = %d, want %d", len(restored.ToolCalls), len(tt.msg.ToolCalls))
			}
			if tt.msg.ToolCallID != "" && restored.ToolCallID != tt.msg.ToolCallID {
				t.Errorf("tool_call_id = %q, want %q", restored.ToolCallID, tt.msg.ToolCallID)
			}
		})
	}
}

// TestToMessage_Fallback 验证损坏 JSON 时的回退路径（不 panic）。
func TestToMessage_Fallback(t *testing.T) {
	e := TranscriptEntry{
		UUID:   "bad-msg",
		Type:   "assistant",
		Message: json.RawMessage(`{invalid json definitely not valid{{{{`),
	}
	msg := e.ToMessage()
	if msg.Role != llm.RoleAssistant {
		t.Errorf("role = %v, want assistant", msg.Role)
	}
}

// TestParentUUIDChain 验证 MessagesToTranscriptEntries 的 parentUUID 链。
func TestParentUUIDChain(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, ID: "id-1", Content: "first"},
		{Role: llm.RoleAssistant, ID: "id-2", Content: "second"},
		{Role: llm.RoleUser, ID: "id-3", Content: "third"},
	}

	t.Run("nil starting parent", func(t *testing.T) {
		entries := MessagesToTranscriptEntries(msgs, nil, "sid", "v1", "/cwd", "")
		if len(entries) != 3 {
			t.Fatalf("expected 3 entries, got %d", len(entries))
		}
		if entries[0].ParentUUID != nil {
			t.Error("entry[0].ParentUUID should be nil")
		}
		if entries[1].ParentUUID == nil || *entries[1].ParentUUID != "id-1" {
			t.Errorf("entry[1].ParentUUID = %v, want id-1", entries[1].ParentUUID)
		}
		if entries[2].ParentUUID == nil || *entries[2].ParentUUID != "id-2" {
			t.Errorf("entry[2].ParentUUID = %v, want id-2", entries[2].ParentUUID)
		}
	})

	t.Run("with starting parent", func(t *testing.T) {
		parent := "parent-000"
		entries := MessagesToTranscriptEntries(msgs, &parent, "sid", "v1", "/cwd", "")
		if len(entries) != 3 {
			t.Fatalf("expected 3 entries, got %d", len(entries))
		}
		if entries[0].ParentUUID == nil || *entries[0].ParentUUID != "parent-000" {
			t.Errorf("entry[0].ParentUUID = %v, want parent-000", entries[0].ParentUUID)
		}
		if entries[1].ParentUUID == nil || *entries[1].ParentUUID != "id-1" {
			t.Errorf("entry[1].ParentUUID = %v, want id-1", entries[1].ParentUUID)
		}
	})

	t.Run("empty messages", func(t *testing.T) {
		entries := MessagesToTranscriptEntries(nil, nil, "sid", "v1", "/cwd", "")
		if entries != nil {
			t.Errorf("expected nil, got %d entries", len(entries))
		}
	})
}

// ── Wave 1 测试：ReasoningContent + ToolName 回环 ──

// TestReasoningContent_RoundTrip 验证 reasoning_content 通过内容块完整往返。
func TestReasoningContent_RoundTrip(t *testing.T) {
	tests := []struct {
		name             string
		msg              llm.Message
		wantReasoningOut string
	}{
		{
			name: "assistant with tool_calls and reasoning",
			msg: llm.Message{
				Role:             llm.RoleAssistant,
				ID:               "a1",
				Content:          "Let me run that",
				ReasoningContent: "I need to read the file first",
				ToolCalls: []llm.ToolCall{
					{ID: "tc1", Name: "bash", Arguments: `{"command":"ls"}`},
				},
			},
			wantReasoningOut: "I need to read the file first",
		},
		{
			name: "assistant with tool_calls and empty reasoning",
			msg: llm.Message{
				Role:             llm.RoleAssistant,
				ID:               "a2",
				ReasoningContent: "",
				ToolCalls: []llm.ToolCall{
					{ID: "tc2", Name: "read", Arguments: `{"path":"f.txt"}`},
				},
			},
			wantReasoningOut: "",
		},
		{
			name: "assistant without tool_calls with reasoning",
			msg: llm.Message{
				Role:             llm.RoleAssistant,
				ID:               "a3",
				Content:          "Hello!",
				ReasoningContent: "Simple greeting",
			},
			wantReasoningOut: "Simple greeting",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := NewTranscriptEntry(tt.msg, nil, "sid", "v1", "/cwd", "")
			restored := entry.ToMessage()
			if restored.ReasoningContent != tt.wantReasoningOut {
				t.Errorf("ReasoningContent = %q, want %q", restored.ReasoningContent, tt.wantReasoningOut)
			}
		})
	}
}

// TestToolName_RoundTrip 验证工具消息的 Name 字段通过内容块完整往返。
func TestToolName_RoundTrip(t *testing.T) {
	msg := llm.Message{
		Role:       llm.RoleTool,
		ID:         "t1",
		Content:    "file contents\n",
		ToolCallID: "call-bash",
		Name:       "bash",
	}
	entry := NewTranscriptEntry(msg, nil, "sid", "v1", "/cwd", "")
	restored := entry.ToMessage()
	if restored.Role != llm.RoleTool {
		t.Errorf("Role = %v, want %v", restored.Role, llm.RoleTool)
	}
	if restored.Name != "bash" {
		t.Errorf("Name = %q, want %q", restored.Name, "bash")
	}
	if restored.Content != "file contents\n" {
		t.Errorf("Content = %q, want %q", restored.Content, "file contents\n")
	}
	if restored.ToolCallID != "call-bash" {
		t.Errorf("ToolCallID = %q, want %q", restored.ToolCallID, "call-bash")
	}
}

// TestSubagentTranscriptPath 验证 subagent 路径生成。
func TestSubagentTranscriptPath(t *testing.T) {
	path := SubagentTranscriptPath("/sessions", "sid-123", "agent-abc")
	expected := filepath.Join("/sessions", "sid-123", "subagents", "agent-agent-abc.jsonl")
	if path != expected {
		t.Errorf("path = %q, want %q", path, expected)
	}
}

// TestAgentMetadata_RoundTrip 验证 AgentMetadata 持久往返。
func TestAgentMetadata_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-x.meta.json")

	meta := AgentMetadata{
		AgentType:   "Explore",
		Description: "Search for Go files",
	}
	if err := SaveAgentMetadata(path, meta); err != nil {
		t.Fatalf("SaveAgentMetadata: %v", err)
	}
	loaded, err := LoadAgentMetadata(path)
	if err != nil {
		t.Fatalf("LoadAgentMetadata: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected metadata, got nil")
	}
	if loaded.AgentType != "Explore" {
		t.Errorf("AgentType = %q, want %q", loaded.AgentType, "Explore")
	}
	if loaded.Description != "Search for Go files" {
		t.Errorf("Description = %q, want %q", loaded.Description, "Search for Go files")
	}
}
