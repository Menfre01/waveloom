package session

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// Transcript
// ---------------------------------------------------------------------------

func TestTranscriptAppendAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	lines := []TranscriptLine{
		{Type: "user", State: "done", Text: "hello"},
		{Type: "assistant", State: "done", Text: "hi there"},
		{Type: "tool", State: "done", ToolName: "bash", ToolArgs: "echo ok", ToolResult: "ok\n"},
	}

	for _, l := range lines {
		if err := AppendTranscriptLine(path, l); err != nil {
			t.Fatalf("AppendTranscriptLine: %v", err)
		}
	}

	loaded, err := LoadTranscriptLines(path)
	if err != nil {
		t.Fatalf("LoadTranscriptLines: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(loaded))
	}
	if loaded[0].Text != "hello" {
		t.Errorf("line 0 text = %q, want %q", loaded[0].Text, "hello")
	}
	if loaded[2].ToolName != "bash" {
		t.Errorf("line 2 tool = %q, want %q", loaded[2].ToolName, "bash")
	}
}

func TestTranscriptLoadEmpty(t *testing.T) {
	lines, err := LoadTranscriptLines("/nonexistent/path.jsonl")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lines != nil {
		t.Errorf("expected nil for nonexistent file, got %v", lines)
	}
}

func TestTranscriptMaxLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.jsonl")

	// Write more than maxTranscriptLines
	for i := 0; i < maxTranscriptLines+100; i++ {
		_ = AppendTranscriptLine(path, TranscriptLine{Type: "system", State: "done", Text: "msg"})
	}

	loaded, err := LoadTranscriptLines(path)
	if err != nil {
		t.Fatalf("LoadTranscriptLines: %v", err)
	}
	if len(loaded) != maxTranscriptLines {
		t.Errorf("expected %d lines (truncated), got %d", maxTranscriptLines, len(loaded))
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
	if err := AppendTranscriptLine(path, TranscriptLine{Type: "user", State: "done", Text: "x"}); err != nil {
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
	data := []byte(
		`{"type":"user","state":"done","text":"line1"}` + "\n" +
			`this is not json` + "\n" +
			`{"type":"assistant","state":"done","text":"line3"}` + "\n",
	)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	lines, err := LoadTranscriptLines(path)
	if err != nil {
		t.Fatalf("LoadTranscriptLines: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 valid lines (corrupt skipped), got %d", len(lines))
	}
	if lines[0].Text != "line1" {
		t.Errorf("line0 text = %q, want %q", lines[0].Text, "line1")
	}
	if lines[1].Text != "line3" {
		t.Errorf("line1 text = %q, want %q", lines[1].Text, "line3")
	}
}
