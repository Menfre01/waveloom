package agentloop

import (
	"context"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
)

func TestRunContentAndToolCallsTogether(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("Let me check that for you.", makeToolCall("tc1", "read_file", `{"file_path":"/tmp/a.txt"}`)),
			makeTextResponse("The file contains: hello world"),
		},
	}
	readTool := newSuccessTool("read_file", true, "hello world")
	registry := newTestRegistry(readTool)
	loop := New(client, registry, DefaultConfig())

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "read /tmp/a.txt"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if finalEv.Reason != ReasonCompleted {
		t.Errorf("expected ReasonCompleted, got %s", finalEv.Reason)
	}
	if finalEv.Turn != 2 {
		t.Errorf("expected 2 turns, got %d", finalEv.Turn)
	}

	asstMsg := finalEv.Messages[1]
	if asstMsg.Role != llm.RoleAssistant {
		t.Errorf("expected assistant, got %s", asstMsg.Role)
	}
	if asstMsg.Content != "Let me check that for you." {
		t.Errorf("expected content preserved, got %q", asstMsg.Content)
	}
	if len(asstMsg.ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(asstMsg.ToolCalls))
	}

	toolMsg := finalEv.Messages[2]
	if toolMsg.Role != llm.RoleTool {
		t.Errorf("expected tool role, got %s", toolMsg.Role)
	}
	if toolMsg.Content != "[tool_result from read_file]\nhello world" {
		t.Errorf("expected tool result, got %s", toolMsg.Content)
	}

	lastMsg := finalEv.Messages[3]
	if lastMsg.Role != llm.RoleAssistant {
		t.Errorf("expected assistant, got %s", lastMsg.Role)
	}
	if lastMsg.Content != "The file contains: hello world" {
		t.Errorf("unexpected final content: %s", lastMsg.Content)
	}
}
