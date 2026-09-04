package agentloop

import (
	"context"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// TestRunBackgroundCompletionNoticeInjectedAfterToolResult 验证 turn 内后台
// 任务完成通知:工具 step 执行后、模型下一次调用前,通知以 user 消息注入,
// 消息序列为 user → assistant(tool_calls) → tool(result) → user(notice)
// → assistant(text)。
func TestRunBackgroundCompletionNoticeInjectedAfterToolResult(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("", makeToolCall("tc1", "read_file", `{"file_path":"/tmp/a.txt"}`)),
			makeTextResponse("done reading"),
		}}
	readTool := newSuccessTool("read_file", true, "hello")
	registry := newTestRegistry(readTool)

	cfg := DefaultConfig()
	calls := 0
	cfg.BackgroundCompletions = func() string {
		calls++
		if calls == 1 {
			return "<background-notifications>\n" +
				"<background-task id=\"bg1\" command=\"sleep 5\" exit_code=\"0\" log=\"/tmp/x.log\">completed</background-task>\n" +
				"</background-notifications>"
		}
		return ""
	}
	loop := New(client, registry, cfg)

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "read /tmp/a.txt"}}))
	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if finalEv.Reason != ReasonCompleted {
		t.Fatalf("expected ReasonCompleted, got %s", finalEv.Reason)
	}
	if calls == 0 {
		t.Fatal("BackgroundCompletions never invoked")
	}
	// 消息序列:user → assistant(tool_calls) → tool(result) → user(notice) → assistant(text)
	if len(finalEv.Messages) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(finalEv.Messages))
	}
	notice := finalEv.Messages[3]
	if notice.Role != llm.RoleUser {
		t.Errorf("Messages[3].Role = %q, want user(notice)", notice.Role)
	}
	if !strings.Contains(notice.Content, `id="bg1"`) {
		t.Errorf("Messages[3].Content missing bg1 notice, got %q", notice.Content)
	}
	if finalEv.Messages[4].Role != llm.RoleAssistant {
		t.Errorf("Messages[4].Role = %q, want assistant(终答)", finalEv.Messages[4].Role)
	}
}

// TestRunBackgroundCompletionNilConfig 验证 BackgroundCompletions 为 nil 时
// 不注入任何额外消息(默认路径不受影响)。
func TestRunBackgroundCompletionNilConfig(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("", makeToolCall("tc1", "read_file", `{"file_path":"/tmp/a.txt"}`)),
			makeTextResponse("done"),
		}}
	readTool := newSuccessTool("read_file", true, "hello")
	registry := newTestRegistry(readTool)

	finalEv := drainEvents(New(client, registry, DefaultConfig()).Run(context.Background(),
		[]llm.Message{{Role: llm.RoleUser, Content: "read"}}))
	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if len(finalEv.Messages) != 4 {
		t.Fatalf("expected 4 messages without notifier, got %d", len(finalEv.Messages))
	}
}
