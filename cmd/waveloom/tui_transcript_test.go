package main

import (
	"path/filepath"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/session"
)

func TestTranscriptEntryToParagraph(t *testing.T) {
	tests := []struct {
		name   string
		entry  session.TranscriptEntry
		expect []Paragraph
	}{
		{name: "user", entry: makeEntry(llm.Message{Role: llm.RoleUser, ID: "a1", Content: "hello"}), expect: []Paragraph{{Type: paraUser, State: stateDone, Text: "hello"}}},
		{name: "assistant", entry: makeEntry(llm.Message{Role: llm.RoleAssistant, ID: "a2", Content: "hi"}), expect: []Paragraph{{Type: paraAssistant, State: stateDone, Text: "hi"}}},
		{name: "system skipped", entry: makeEntry(llm.Message{Role: llm.RoleSystem, ID: "s1", Content: "sys"}), expect: nil},
		{name: "AGENTS.md skipped", entry: makeEntry(llm.Message{Role: llm.RoleUser, ID: "u1", Content: "# AGENTS.md test"}), expect: nil},
		{name: "tool calls", entry: makeEntry(llm.Message{Role: llm.RoleAssistant, ID: "a3", Content: "run", ToolCalls: []llm.ToolCall{{ID: "tc1", Name: "bash", Arguments: `{"command":"ls"}`}}}),
			expect: []Paragraph{{Type: paraAssistant, State: stateDone, Text: "run"}, {Type: paraTool, State: stateDone, ToolName: "bash", ToolArgs: "ls"}}},
		{name: "thought", entry: makeEntry(llm.Message{Role: llm.RoleAssistant, ID: "a4", ReasoningContent: "thinking...", Content: "answer"}),
			expect: []Paragraph{{Type: paraThought, State: stateCollapsed, Text: "thinking...", ThoughtTokens: 3}, {Type: paraAssistant, State: stateDone, Text: "answer"}}},
		{name: "agent", entry: makeEntry(llm.Message{Role: llm.RoleAssistant, ID: "a5", ToolCalls: []llm.ToolCall{{ID: "ag1", Name: "agent", Arguments: `{"description":"search"}`}}}),
			expect: []Paragraph{{Type: paraSubagent, State: stateDone, SubagentToolCallID: "ag1", ToolName: "agent", ToolArgs: "search"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var paras []Paragraph
			transcriptEntryToParagraph(tt.entry, &paras)
			if len(paras) != len(tt.expect) {
				t.Fatalf("got %d paras, want %d", len(paras), len(tt.expect))
			}
			for i := range paras {
				if paras[i].Type != tt.expect[i].Type { t.Errorf("Type: %d!=%d", paras[i].Type, tt.expect[i].Type) }
				if paras[i].Text != tt.expect[i].Text { t.Errorf("Text: %q!=%q", paras[i].Text, tt.expect[i].Text) }
				if paras[i].ToolName != tt.expect[i].ToolName { t.Errorf("ToolName: %q!=%q", paras[i].ToolName, tt.expect[i].ToolName) }
				if paras[i].ToolArgs != tt.expect[i].ToolArgs { t.Errorf("ToolArgs: %q!=%q", paras[i].ToolArgs, tt.expect[i].ToolArgs) }
			}
		})
	}
}

func makeEntry(msg llm.Message) session.TranscriptEntry {
	return session.NewTranscriptEntry(msg, nil, "sid", "v1", "/cwd", "")
}

func TestReplayTranscriptEmptyPath(t *testing.T) {
	m := &model{transcriptPath: ""}
	m.replayTranscript()
	if len(m.paras) != 0 { t.Errorf("expected 0 paras") }
}

func TestReplayTranscript(t *testing.T) {
	dir := t.TempDir()
	tp := filepath.Join(dir, "test.jsonl")
	entries := session.MessagesToTranscriptEntries([]llm.Message{
		{Role: llm.RoleUser, ID: "u1", Content: "q"},
		{Role: llm.RoleAssistant, ID: "a1", Content: "a"},
	}, nil, "sid", "v1", "/cwd", "")
	if err := session.WriteTranscriptEntries(tp, entries); err != nil { t.Fatal(err) }
	m := &model{transcriptPath: tp, cm: session.New("test")}
	m.replayTranscript()
	if len(m.paras) != 2 { t.Fatalf("got %d", len(m.paras)) }
}

func TestFlushTranscriptIsNoop(t *testing.T) {
	m := &model{transcriptPath: ""}
	m.paras = []Paragraph{{Type: paraUser, State: stateDone, Text: "x"}}
	m.flushTranscript()
}

func TestReplayTranscriptWithToolCalls(t *testing.T) {
	dir := t.TempDir()
	tp := filepath.Join(dir, "test.jsonl")
	entries := session.MessagesToTranscriptEntries([]llm.Message{
		{Role: llm.RoleUser, ID: "u1", Content: "run ls"},
		{Role: llm.RoleAssistant, ID: "a1", Content: "Running", ToolCalls: []llm.ToolCall{{ID: "tc1", Name: "bash", Arguments: `{"command":"ls"}`}}},
		{Role: llm.RoleTool, ID: "t1", Content: "ok", ToolCallID: "tc1", Name: "bash"},
	}, nil, "sid", "v1", "/cwd", "")
	if err := session.WriteTranscriptEntries(tp, entries); err != nil { t.Fatal(err) }
	m := &model{transcriptPath: tp, cm: session.New("test")}
	m.replayTranscript()
	if len(m.paras) != 3 { t.Fatalf("got %d", len(m.paras)) }
}

func TestReplayTranscriptWithSubagents(t *testing.T) {
	dir := t.TempDir()
	tp := filepath.Join(dir, "test.jsonl")
	entries := session.MessagesToTranscriptEntries([]llm.Message{
		{Role: llm.RoleUser, ID: "u1", Content: "search"},
		{Role: llm.RoleAssistant, ID: "a1", ToolCalls: []llm.ToolCall{{ID: "ag1", Name: "agent", Arguments: `{"description":"search"}`}}},
	}, nil, "sid", "v1", "/cwd", "")
	if err := session.WriteTranscriptEntries(tp, entries); err != nil { t.Fatal(err) }

	subDir := filepath.Join(dir, "subagents")
	subPath := filepath.Join(subDir, "agent-ag1.jsonl")
	subEntries := session.MessagesToTranscriptEntries([]llm.Message{
		{Role: llm.RoleAssistant, ID: "sa1", Content: "Found", ToolCalls: []llm.ToolCall{{ID: "st1", Name: "grep", Arguments: `{"pattern":"func"}`}}},
		{Role: llm.RoleTool, ID: "st2", Content: "ok", ToolCallID: "st1", Name: "grep"},
	}, nil, "sid", "v1", "/cwd", "")
	for i := range subEntries { subEntries[i].IsSidechain = true }
	if err := session.WriteTranscriptEntries(subPath, subEntries); err != nil { t.Fatal(err) }

	cm := session.New("test")
	cm.SetSessionPath(filepath.Join(dir, "test.json"))
	metaPath := filepath.Join(dir, cm.SessionID(), "subagents", "agent-ag1.meta.json")
	_ = session.SaveAgentMetadata(metaPath, session.AgentMetadata{AgentType: "Explore"})

	m := &model{transcriptPath: tp, sessionDir: dir, cm: cm}
	m.replayTranscript()
	if len(m.paras) != 2 { t.Fatalf("got %d", len(m.paras)) }
	// subagent paragraph exists and is deduped
}

func TestBuildSubagentParagraph(t *testing.T) {
	entries := session.MessagesToTranscriptEntries([]llm.Message{
		{Role: llm.RoleAssistant, ID: "sa1", Content: "Found", ToolCalls: []llm.ToolCall{{ID: "st1", Name: "grep", Arguments: `{"pattern":"func"}`}}},
		{Role: llm.RoleTool, ID: "st2", Content: "ok", ToolCallID: "st1", Name: "grep"},
	}, nil, "sid", "v1", "/cwd", "")
	m := &model{sessionDir: t.TempDir(), cm: session.New("test")}
	para := m.buildSubagentParagraph("agent-1", entries)
	if para.Type != paraSubagent { t.Error("wrong type") }
	if len(para.SubagentEvents) != 3 { t.Fatalf("got %d events", len(para.SubagentEvents)) }
}

func TestFindSubagentPara(t *testing.T) {
	m := &model{paras: []Paragraph{
		{Type: paraUser, Text: "hello"},
		{Type: paraSubagent, SubagentToolCallID: "agent-1", Text: "sub"},
	}}
	if p := m.findSubagentPara("agent-1"); p == nil { t.Fatal("not found") }
	if p := m.findSubagentPara("agent-2"); p != nil { t.Fatal("should be nil") }
}
