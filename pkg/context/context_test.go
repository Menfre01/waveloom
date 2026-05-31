package context

import (
	"sync"
	"testing"

	"waveloom/pkg/llm"
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
	msgs1 = append(msgs1, llm.Message{Role: llm.RoleAssistant, Content: "fake"})

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
