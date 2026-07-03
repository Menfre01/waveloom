package context

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Menfre01/waveloom/pkg/compaction"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/task"
)

func TestNew_WithSystemPrompt(t *testing.T) {
	cm := New("You are a helpful assistant.")
	if cm.Stats().MessageCount != 1 {
		t.Fatalf("expected 1 message, got %d", cm.Stats().MessageCount)
	}
	msgs := cm.PrepareRun("hello")
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != llm.RoleSystem || msgs[0].Content != "You are a helpful assistant." {
		t.Fatalf("system message mismatch: %+v", msgs[0])
	}
	if msgs[1].Role != llm.RoleUser || msgs[1].Content != "hello" {
		t.Fatalf("user message mismatch: %+v", msgs[1])
	}
}

func TestNew_WithoutSystemPrompt(t *testing.T) {
	cm := New("")
	if cm.Stats().MessageCount != 0 {
		t.Fatalf("expected 0 messages, got %d", cm.Stats().MessageCount)
	}
	msgs := cm.PrepareRun("hello")
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != llm.RoleUser || msgs[0].Content != "hello" {
		t.Fatalf("user message mismatch: %+v", msgs[0])
	}
}

func TestPrepareRun_ReturnsCopy(t *testing.T) {
	cm := New("system")
	msgs1 := cm.PrepareRun("turn 1")

	// 修改返回值不应影响 ContextManager 内部状态
	msgs1[0].Content = "modified"
	_ = append(msgs1, llm.Message{Role: llm.RoleAssistant, Content: "fake"})

	// 第二次 PrepareRun 应基于原始内部状态
	msgs2 := cm.PrepareRun("turn 2")
	if msgs2[0].Content != "system" {
		t.Fatalf("internal state was mutated: content=%q", msgs2[0].Content)
	}
	if len(msgs2) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs2))
	}
}

func TestCompleteRun_ReplacesState(t *testing.T) {
	cm := New("system")
	cm.PrepareRun("hello")

	// 模拟 Loop 完成后的消息
	loopMessages := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "Hi there!"},
	}
	cm.CompleteRun(loopMessages, 100, 100, 50, 80, 20, 0, "", 0, "")

	if cm.Stats().MessageCount != 3 {
		t.Fatalf("expected 3 messages, got %d", cm.Stats().MessageCount)
	}

	// 下一轮 PrepareRun 应基于新状态
	msgs := cm.PrepareRun("next")
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}
	if msgs[2].Role != llm.RoleAssistant || msgs[2].Content != "Hi there!" {
		t.Fatalf("assistant message lost after CompleteRun: %+v", msgs[2])
	}
	if msgs[3].Role != llm.RoleUser || msgs[3].Content != "next" {
		t.Fatalf("new user message mismatch: %+v", msgs[3])
	}
}

func TestCompleteRun_StatsAccumulation(t *testing.T) {
	cm := New("system")
	cm.PrepareRun("turn 1")

	// 第一轮
	cm.CompleteRun([]llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "turn 1"},
		{Role: llm.RoleAssistant, Content: "response 1"},
	}, 50, 50, 30, 40, 10, 0, "", 0, "")

	s := cm.Stats()
	if s.TotalTurns != 1 {
		t.Fatalf("expected TotalTurns=1, got %d", s.TotalTurns)
	}
	if s.TotalPromptTokens != 50 || s.TotalCompletionTokens != 30 {
		t.Fatalf("token stats mismatch: prompt=%d comp=%d", s.TotalPromptTokens, s.TotalCompletionTokens)
	}
	if s.TotalCacheHitTokens != 40 || s.TotalCacheMissTokens != 10 {
		t.Fatalf("cache stats mismatch: hit=%d miss=%d", s.TotalCacheHitTokens, s.TotalCacheMissTokens)
	}
	if s.MessageCount != 3 {
		t.Fatalf("expected MessageCount=3, got %d", s.MessageCount)
	}

	// 第二轮
	cm.PrepareRun("turn 2")
	cm.CompleteRun([]llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "turn 1"},
		{Role: llm.RoleAssistant, Content: "response 1"},
		{Role: llm.RoleUser, Content: "turn 2"},
		{Role: llm.RoleAssistant, Content: "response 2"},
	}, 60, 60, 40, 50, 10, 0, "", 0, "")

	s = cm.Stats()
	if s.TotalTurns != 2 {
		t.Fatalf("expected TotalTurns=2, got %d", s.TotalTurns)
	}
	if s.TotalPromptTokens != 110 || s.TotalCompletionTokens != 70 {
		t.Fatalf("token stats not accumulated: prompt=%d comp=%d", s.TotalPromptTokens, s.TotalCompletionTokens)
	}
	if s.TotalCacheHitTokens != 90 || s.TotalCacheMissTokens != 20 {
		t.Fatalf("cache stats not accumulated: hit=%d miss=%d", s.TotalCacheHitTokens, s.TotalCacheMissTokens)
	}
}

func TestCompleteRun_PreservesSystemMessage(t *testing.T) {
	cm := New("original system")
	cm.PrepareRun("hello")

	// Loop 可能使用不同的 system prompt 内容，CompleteRun 以 Loop 产出为准
	cm.CompleteRun([]llm.Message{
		{Role: llm.RoleSystem, Content: "loop system"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}, 10, 10, 5, 0, 10, 0, "", 0, "")

	msgs := cm.PrepareRun("next")
	if msgs[0].Role != llm.RoleSystem {
		t.Fatalf("system message lost after CompleteRun")
	}
	if msgs[0].Content != "loop system" {
		t.Fatalf("system content not updated: %q", msgs[0].Content)
	}
}

func TestReset_WithSystem(t *testing.T) {
	cm := New("system")
	cm.PrepareRun("hello")
	cm.CompleteRun([]llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}, 10, 10, 5, 0, 10, 0, "", 0, "")

	cm.Reset()

	if cm.Stats().MessageCount != 1 {
		t.Fatalf("expected 1 message after reset, got %d", cm.Stats().MessageCount)
	}
	msgs := cm.PrepareRun("fresh start")
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages after reset+prepare, got %d", len(msgs))
	}
	if msgs[0].Role != llm.RoleSystem {
		t.Fatalf("system message lost after reset")
	}
}

func TestReset_WithoutSystem(t *testing.T) {
	cm := New("")
	cm.PrepareRun("hello")
	cm.CompleteRun([]llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}, 10, 10, 5, 0, 10, 0, "", 0, "")

	cm.Reset()

	if cm.Stats().MessageCount != 0 {
		t.Fatalf("expected 0 messages after reset, got %d", cm.Stats().MessageCount)
	}
}

func TestReset_EmptyContextManager(t *testing.T) {
	cm := New("")
	// Reset on empty CM should not panic
	cm.Reset()
	if cm.Stats().MessageCount != 0 {
		t.Fatalf("expected 0 messages, got %d", cm.Stats().MessageCount)
	}
}

func TestMultiTurnAccumulation(t *testing.T) {
	cm := New("You are a code assistant.")

	// Turn 1
	cm.PrepareRun("read main.go")
	cm.CompleteRun([]llm.Message{
		{Role: llm.RoleSystem, Content: "You are a code assistant."},
		{Role: llm.RoleUser, Content: "read main.go"},
		{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{{ID: "tc1", Name: "read_file", Arguments: `{"path":"main.go"}`}}},
		{Role: llm.RoleTool, Content: "package main\nfunc main() {}", ToolCallID: "tc1", Name: "read_file"},
		{Role: llm.RoleAssistant, Content: "The file contains a main function."},
	}, 200, 200, 100, 150, 50, 0, "", 0, "")

	// Turn 2
	msgs := cm.PrepareRun("now edit it")
	if len(msgs) != 6 {
		t.Fatalf("expected 6 messages for turn 2, got %d", len(msgs))
	}
	// 验证历史完整性
	if msgs[2].Role != llm.RoleAssistant || len(msgs[2].ToolCalls) != 1 {
		t.Fatal("tool call history lost")
	}
	if msgs[3].Role != llm.RoleTool || msgs[3].ToolCallID != "tc1" {
		t.Fatal("tool result history lost")
	}
	if msgs[4].Role != llm.RoleAssistant || msgs[4].Content != "The file contains a main function." {
		t.Fatal("assistant response history lost")
	}
	if msgs[5].Role != llm.RoleUser || msgs[5].Content != "now edit it" {
		t.Fatalf("new user message mismatch: %+v", msgs[5])
	}

	// Turn 2 complete
	cm.CompleteRun(append(msgs, llm.Message{Role: llm.RoleAssistant, Content: "Done."}), 250, 250, 80, 200, 50, 0, "", 0, "")

	s := cm.Stats()
	if s.TotalTurns != 2 {
		t.Fatalf("expected 2 turns, got %d", s.TotalTurns)
	}
	if s.TotalCacheHitTokens != 350 {
		t.Fatalf("expected cache hit=350, got %d", s.TotalCacheHitTokens)
	}
}

func TestConcurrentAccess(t *testing.T) {
	cm := New("system")
	cm.PrepareRun("initial")

	var wg sync.WaitGroup
	const goroutines = 20

	// 并发 Stats 读取
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cm.Stats()
			_ = cm.Stats().MessageCount
		}()
	}

	// 并发 PrepareRun + CompleteRun
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			msgs := cm.PrepareRun("input")
			cm.CompleteRun(msgs, 10, 10, 5, 0, 10, 0, "", 0, "")
		}(i)
	}

	wg.Wait()

	// 不应 panic，至少有一些消息累积
	if cm.Stats().MessageCount == 0 {
		t.Fatal("expected non-zero message count after concurrent access")
	}
}

func TestStats_ReturnsCopy(t *testing.T) {
	cm := New("system")
	cm.PrepareRun("hello")
	cm.CompleteRun([]llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}, 10, 10, 5, 8, 2, 0, "", 0, "")

	s := cm.Stats()
	// 修改返回的 Stats 不应影响内部
	s.TotalTurns = 999
	s.TotalPromptTokens = 999

	s2 := cm.Stats()
	if s2.TotalTurns != 1 {
		t.Fatalf("Stats() should return a copy: TotalTurns=%d", s2.TotalTurns)
	}
	if s2.TotalPromptTokens != 10 {
		t.Fatalf("Stats() should return a copy: TotalPromptTokens=%d", s2.TotalPromptTokens)
	}
}

func TestReset_ResetsStats(t *testing.T) {
	cm := New("system")
	cm.PrepareRun("hello")
	cm.CompleteRun([]llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}, 100, 100, 50, 80, 20, 0, "", 0, "")

	cm.Reset()

	s := cm.Stats()
	if s.TotalTurns != 0 {
		t.Fatalf("Stats not reset: TotalTurns=%d", s.TotalTurns)
	}
	if s.TotalPromptTokens != 0 || s.TotalCompletionTokens != 0 {
		t.Fatal("token stats not reset")
	}
	if s.TotalCacheHitTokens != 0 || s.TotalCacheMissTokens != 0 {
		t.Fatal("cache stats not reset")
	}

	// Reset 后 PrepareRun 不应携带旧历史
	msgs := cm.PrepareRun("fresh")
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages after reset+prepare, got %d", len(msgs))
	}
	if msgs[1].Content != "fresh" {
		t.Fatalf("new user message mismatch: %q", msgs[1].Content)
	}
	for _, m := range msgs {
		if m.Content == "hi" {
			t.Fatal("old assistant message leaked after reset")
		}
	}
}

func TestCompleteRun_NilMessages(t *testing.T) {
	cm := New("system")
	cm.PrepareRun("hello")

	cm.CompleteRun(nil, 0, 0, 0, 0, 0, 0, "", 0, "")

	if cm.Stats().MessageCount != 0 {
		t.Fatalf("expected 0 messages after nil CompleteRun, got %d", cm.Stats().MessageCount)
	}
}

func TestCompleteRun_EmptyMessages(t *testing.T) {
	cm := New("system")
	cm.PrepareRun("hello")

	cm.CompleteRun([]llm.Message{}, 0, 0, 0, 0, 0, 0, "", 0, "")

	if cm.Stats().MessageCount != 0 {
		t.Fatalf("expected 0 messages after empty CompleteRun, got %d", cm.Stats().MessageCount)
	}

	msgs := cm.PrepareRun("next")
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message after empty CompleteRun, got %d", len(msgs))
	}
	if msgs[0].Role != llm.RoleUser || msgs[0].Content != "next" {
		t.Fatalf("user message mismatch: %+v", msgs[0])
	}
}

func TestInjectUserInstructions_InsertsAfterSystem(t *testing.T) {
	cm := New("system prompt")
	cm.InjectUserInstructions("AGENTS.md content")

	msgs := cm.PrepareRun("hello")
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages (system, instructions, user), got %d", len(msgs))
	}
	if msgs[0].Role != llm.RoleSystem {
		t.Fatalf("messages[0] should be system, got %s", msgs[0].Role)
	}
	if msgs[0].Content != "system prompt" {
		t.Fatalf("messages[0] content mismatch: %q", msgs[0].Content)
	}
	if msgs[1].Role != llm.RoleUser || msgs[1].Content != "AGENTS.md content" {
		t.Fatalf("messages[1] should be user instructions, got role=%s content=%q", msgs[1].Role, msgs[1].Content)
	}
	if msgs[2].Role != llm.RoleUser || msgs[2].Content != "hello" {
		t.Fatalf("messages[2] should be user input, got role=%s content=%q", msgs[2].Role, msgs[2].Content)
	}
}

func TestInjectUserInstructions_NoSystem(t *testing.T) {
	cm := New("")
	cm.InjectUserInstructions("AGENTS.md content")

	if cm.Stats().MessageCount != 0 {
		t.Fatalf("expected 0 messages when no system prompt, got %d", cm.Stats().MessageCount)
	}
}

func TestInjectUserInstructions_EmptyText(t *testing.T) {
	cm := New("system")
	cm.InjectUserInstructions("")

	if cm.Stats().MessageCount != 1 {
		t.Fatalf("expected 1 message (system only), got %d", cm.Stats().MessageCount)
	}
}

func TestInjectUserInstructions_ResetPreservesInstructions(t *testing.T) {
	cm := New("system")
	cm.InjectUserInstructions("AGENTS.md content")

	// Reset should keep system prompt + instructions
	cm.Reset()
	cm.InjectUserInstructions("AGENTS.md content reloaded")

	msgs := cm.PrepareRun("hello")
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages after reset+reload, got %d", len(msgs))
	}
	if msgs[1].Content != "AGENTS.md content reloaded" {
		t.Fatalf("instructions not reloaded: %q", msgs[1].Content)
	}
}

func TestInjectUserInstructions_Idempotent(t *testing.T) {
	cm := New("system")
	cm.InjectUserInstructions("first injection")
	cm.InjectUserInstructions("second injection")

	msgs := cm.PrepareRun("hello")
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	// 第二次注入应被忽略
	if msgs[1].Content != "first injection" {
		t.Fatalf("expected first injection preserved, got %q", msgs[1].Content)
	}
}

// ---------------------------------------------------------------------------
// NewWithCompaction / Compactor
// ---------------------------------------------------------------------------

func TestNewWithCompaction_WithSystemPrompt(t *testing.T) {
	cm := NewWithCompaction("system", compaction.DefaultCompactionConfig(), nil)
	if cm.Stats().MessageCount != 1 {
		t.Fatalf("expected 1 message, got %d", cm.Stats().MessageCount)
	}
}

func TestNewWithCompaction_WithoutSystemPrompt(t *testing.T) {
	cm := NewWithCompaction("", compaction.DefaultCompactionConfig(), nil)
	if cm.Stats().MessageCount != 0 {
		t.Fatalf("expected 0 messages, got %d", cm.Stats().MessageCount)
	}
}

func TestCompactor_ReturnsNonNil(t *testing.T) {
	cm := New("system")
	if cm.Compactor() == nil {
		t.Fatal("Compactor() returned nil")
	}
}

// ---------------------------------------------------------------------------
// Session path
// ---------------------------------------------------------------------------

func TestSetSessionPath_SessionPath(t *testing.T) {
	cm := New("system")
	if cm.SessionPath() != "" {
		t.Fatalf("expected empty path, got %q", cm.SessionPath())
	}

	cm.SetSessionPath("/tmp/test-session.json")
	if cm.SessionPath() != "/tmp/test-session.json" {
		t.Fatalf("expected /tmp/test-session.json, got %q", cm.SessionPath())
	}

	// 清空
	cm.SetSessionPath("")
	if cm.SessionPath() != "" {
		t.Fatalf("expected empty path after clear, got %q", cm.SessionPath())
	}
}

func TestSessionID_FromPath(t *testing.T) {
	cm := New("system")
	cm.SetSessionPath("/tmp/sessions/abc-123-def.json")
	id := cm.SessionID()
	if id != "abc-123-def" {
		t.Fatalf("expected abc-123-def, got %q", id)
	}
}

func TestSessionID_EmptyPath(t *testing.T) {
	cm := New("system")
	if cm.SessionID() != "" {
		t.Fatalf("expected empty session ID, got %q", cm.SessionID())
	}
}

func TestSessionID_NoExtension(t *testing.T) {
	cm := New("system")
	cm.SetSessionPath("/tmp/sessions/noext")
	id := cm.SessionID()
	if id != "noext" {
		t.Fatalf("expected 'noext', got %q", id)
	}
}

// ---------------------------------------------------------------------------
// Save / LoadFromFile / RemoveSession
// ---------------------------------------------------------------------------

func TestSave_WithoutPath(t *testing.T) {
	cm := New("system")
	cm.PrepareRun("hello")
	// 未设置 path 的 Save 应静默返回，不 panic
	cm.Save()
}

func TestSave_WithPath(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/session.json"
	cm := New("system")
	cm.PrepareRun("hello")
	cm.CompleteRun([]llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}, 10, 10, 5, 0, 10, 0, "", 0, "")

	cm.SetSessionPath(path)
	cm.Save()

	// 验证文件存在
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("session file not created")
	}
}

func TestLoadFromFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/session.json"

	// 创建并保存
	cm1 := New("system prompt")
	cm1.InjectUserInstructions("AGENTS.md rules")
	cm1.PrepareRun("hello")
	cm1.CompleteRun([]llm.Message{
		{Role: llm.RoleSystem, Content: "system prompt"},
		{Role: llm.RoleUser, Content: "AGENTS.md rules"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi there"},
	}, 50, 50, 30, 40, 10, 0, "", 100, "done")

	cm1.SetSessionPath(path)
	cm1.Save()

	// 加载到新 CM
	cm2 := New("unused")
	if !cm2.LoadFromFile(path) {
		t.Fatal("LoadFromFile returned false")
	}

	if cm2.SessionPath() != path {
		t.Fatalf("session path not set after load: %q", cm2.SessionPath())
	}

	s := cm2.Stats()
	if s.TotalTurns != 1 {
		t.Fatalf("expected TotalTurns=1, got %d", s.TotalTurns)
	}
	if s.TotalPromptTokens != 50 {
		t.Fatalf("expected TotalPromptTokens=50, got %d", s.TotalPromptTokens)
	}
	if s.MessageCount != 4 {
		t.Fatalf("expected MessageCount=4, got %d", s.MessageCount)
	}
}

func TestLoadFromFile_NotFound(t *testing.T) {
	cm := New("system")
	if cm.LoadFromFile("/nonexistent/path.json") {
		t.Fatal("LoadFromFile should return false for nonexistent file")
	}
}

func TestLoadFromFile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/bad.json"
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	cm := New("system")
	if cm.LoadFromFile(path) {
		t.Fatal("LoadFromFile should return false for invalid JSON")
	}
}

func TestLoadFromFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/empty.json"
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	cm := New("system")
	if cm.LoadFromFile(path) {
		t.Fatal("LoadFromFile should return false for empty file")
	}
}

func TestRemoveSession(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/session.json"
	cm := New("system")
	cm.PrepareRun("hello")
	cm.CompleteRun([]llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}, 10, 10, 5, 0, 10, 0, "", 0, "")

	cm.SetSessionPath(path)
	cm.Save()

	// 确认文件存在
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("session file not created before remove")
	}

	cm.RemoveSession()

	// 确认路径已清空
	if cm.SessionPath() != "" {
		t.Fatal("session path not cleared after remove")
	}

	// 确认文件已删除
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("session file still exists after remove")
	}
}

func TestRemoveSession_NoPath(t *testing.T) {
	cm := New("system")
	// 无 path 的 RemoveSession 不应 panic
	cm.RemoveSession()
}

func TestCompleteRun_AutoSave(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/autosave.json"
	cm := New("system")
	cm.SetSessionPath(path)
	cm.PrepareRun("hello")

	cm.CompleteRun([]llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}, 10, 10, 5, 0, 10, 0, "", 0, "")

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("session file not auto-saved on CompleteRun")
	}
}

func TestPrepareRun_BackgroundNotification(t *testing.T) {
	task.DefaultRegistry.Reset()
	defer task.DefaultRegistry.Reset()

	now := time.Now()
	task.DefaultRegistry.Register("completed-1", &task.TaskInfo{
		ID: "completed-1", PID: 1, Command: "make build",
		LogPath: "/tmp/bg.log", Status: task.TaskCompleted,
		StartTime: now, CompletedTime: now.Add(10*time.Millisecond), ExitCode: 0,
	})
	task.DefaultRegistry.Register("running-1", &task.TaskInfo{
		ID: "running-1", PID: 2, Command: "npx wrangler dev",
		LogPath: "/tmp/running.log", Status: task.TaskRunning,
		StartTime: now, ExitCode: -1,
	})

	cm := New("system")
	cm.PrepareRun("first turn") // 设置 lastBackgroundCheck

	// 注册新完成的任务
	task.DefaultRegistry.Register("completed-2", &task.TaskInfo{
		ID: "completed-2", PID: 3, Command: "sleep 1",
		LogPath: "/tmp/sleep.log", Status: task.TaskCompleted,
		StartTime: time.Now(), CompletedTime: time.Now(), ExitCode: 0,
	})

	msgs := cm.PrepareRun("second turn")

	// system + 首次 user + notification user + 第二次 user
	if len(msgs) < 3 {
		t.Fatalf("expected at least 3 messages, got %d", len(msgs))
	}

	notifMsg := msgs[len(msgs)-2]
	if notifMsg.Role != llm.RoleUser {
		t.Errorf("notification should be user role, got %s", notifMsg.Role)
	}
	// 应包含刚完成的任务
	if !strings.Contains(notifMsg.Content, "completed-2") {
		t.Errorf("notification should mention completed-2: %s", notifMsg.Content)
	}
	// 应包含正在运行的任务
	if !strings.Contains(notifMsg.Content, "running-1") {
		t.Errorf("notification should mention running running-1: %s", notifMsg.Content)
	}
	if !strings.Contains(notifMsg.Content, `status="running"`) {
		t.Errorf("notification should have running status: %s", notifMsg.Content)
	}
}
