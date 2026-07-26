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
	cm := session.New("test")
	cm.SetSessionPath(filepath.Join(dir, "test.json"))
	sid := cm.SessionID()
	// 使用正确的路径:sessionDir/<sid>/subagents/ 与 loadSubagentTranscripts 一致
	subPath := session.SubagentTranscriptPath(dir, sid, "ag1")
	subEntries := session.MessagesToTranscriptEntries([]llm.Message{
		{Role: llm.RoleAssistant, ID: "sa1", Content: "Found", ToolCalls: []llm.ToolCall{{ID: "st1", Name: "grep", Arguments: `{"pattern":"func"}`}}},
		{Role: llm.RoleTool, ID: "st2", Content: "ok", ToolCallID: "st1", Name: "grep"},
	}, nil, "sid", "v1", "/cwd", "")
	for i := range subEntries { subEntries[i].IsSidechain = true }
	if err := session.WriteTranscriptEntries(subPath, subEntries); err != nil { t.Fatal(err) }
	metaPath := session.SubagentMetaPath(dir, sid, "ag1")
	_ = session.SaveAgentMetadata(metaPath, session.AgentMetadata{AgentType: "Explore", Description: "search task"})
	m := &model{transcriptPath: tp, sessionDir: dir, cm: cm}
	m.replayTranscript()
	if len(m.paras) != 2 { t.Fatalf("got %d", len(m.paras)) }
	// 验证 subagent 段落已被正确去重(原地替换)且包含事件
	var subPara *Paragraph
	for i := range m.paras {
		if m.paras[i].Type == paraSubagent && m.paras[i].SubagentToolCallID == "ag1" {
			subPara = &m.paras[i]
			break
		}
	}
	if subPara == nil {
		t.Fatal("subagent paragraph not found")
	}
	if len(subPara.SubagentEvents) == 0 {
		t.Fatal("subagent paragraph has no events — dedup/replacement likely failed")
	}
	if subPara.SubagentType != "Explore" {
		t.Errorf("SubagentType = %q, want Explore", subPara.SubagentType)
	}
	if subPara.SubagentPrompt != "search task" {
		t.Errorf("SubagentPrompt = %q, want 'search task'", subPara.SubagentPrompt)
	}
}
// TestReplayTranscriptWithSubagentsTruncated 测试 transcript 截断场景:
// 主 transcript 中 assistant 消息(含 agent tool call)被截断,仅剩 tool_result,
// 验证 fallback 创建 paraSubagent(而非 paraTool),后续 loadSubagentTranscripts
// 通过 SubagentToolCallID 匹配并原地替换。
func TestReplayTranscriptWithSubagentsTruncated(t *testing.T) {
	dir := t.TempDir()
	tp := filepath.Join(dir, "test.jsonl")
	// 模拟截断后的主 transcript:只有 user + tool_result(无 assistant 占位)
	entries := session.MessagesToTranscriptEntries([]llm.Message{
		{Role: llm.RoleUser, ID: "u1", Content: "search"},
		// 注意:没有 assistant 消息(agent tool call 被截断)
		{Role: llm.RoleTool, ID: "t1", Content: "found result", ToolCallID: "ag1", Name: "agent"},
	}, nil, "sid", "v1", "/cwd", "")
	if err := session.WriteTranscriptEntries(tp, entries); err != nil { t.Fatal(err) }
	cm := session.New("test")
	cm.SetSessionPath(filepath.Join(dir, "test.json"))
	sid := cm.SessionID()
	subPath := session.SubagentTranscriptPath(dir, sid, "ag1")
	subEntries := session.MessagesToTranscriptEntries([]llm.Message{
		{Role: llm.RoleAssistant, ID: "sa1", Content: "Found", ToolCalls: []llm.ToolCall{{ID: "st1", Name: "grep", Arguments: `{"pattern":"func"}`}}},
		{Role: llm.RoleTool, ID: "st2", Content: "ok", ToolCallID: "st1", Name: "grep"},
	}, nil, "sid", "v1", "/cwd", "")
	for i := range subEntries { subEntries[i].IsSidechain = true }
	if err := session.WriteTranscriptEntries(subPath, subEntries); err != nil { t.Fatal(err) }
	metaPath := session.SubagentMetaPath(dir, sid, "ag1")
	_ = session.SaveAgentMetadata(metaPath, session.AgentMetadata{AgentType: "fork", Description: "truncated"})
	m := &model{transcriptPath: tp, sessionDir: dir, cm: cm}
	m.replayTranscript()
	// 预期:user + subagent(被 fallback paraSubagent → findSubagentPara 原地替换为 enriched)
	if len(m.paras) != 2 { t.Fatalf("got %d paras, want 2", len(m.paras)) }
	var subPara *Paragraph
	for i := range m.paras {
		if m.paras[i].Type == paraSubagent && m.paras[i].SubagentToolCallID == "ag1" {
			subPara = &m.paras[i]
			break
		}
	}
	if subPara == nil {
		t.Fatal("subagent paragraph not found in truncated replay")
	}
	if len(subPara.SubagentEvents) == 0 {
		t.Fatal("subagent paragraph has no events — dedup/replacement failed in truncated scenario")
	}
	if subPara.SubagentType != "fork" {
		t.Errorf("SubagentType = %q, want fork", subPara.SubagentType)
	}
}
// TestReplayTranscriptWithSubagentsFullyTruncated 测试完全截断场景:
// 主 transcript 中既无 assistant(agent tool call)也无 tool_result,
// subagent transcript 文件存在但找不到任何匹配 → 应静默丢弃,不追加到末尾。
func TestReplayTranscriptWithSubagentsFullyTruncated(t *testing.T) {
	dir := t.TempDir()
	tp := filepath.Join(dir, "test.jsonl")
	// 主 transcript 只有一条不相关的 user 消息
	entries := session.MessagesToTranscriptEntries([]llm.Message{
		{Role: llm.RoleUser, ID: "u1", Content: "something else"},
		{Role: llm.RoleAssistant, ID: "a1", Content: "ok"},
	}, nil, "sid", "v1", "/cwd", "")
	if err := session.WriteTranscriptEntries(tp, entries); err != nil { t.Fatal(err) }
	cm := session.New("test")
	cm.SetSessionPath(filepath.Join(dir, "test.json"))
	sid := cm.SessionID()
	// 磁盘上有 subagent transcript,但 viewport 中无对应段落
	subPath := session.SubagentTranscriptPath(dir, sid, "orphan")
	subEntries := session.MessagesToTranscriptEntries([]llm.Message{
		{Role: llm.RoleAssistant, ID: "sa1", Content: "orphan output"},
	}, nil, "sid", "v1", "/cwd", "")
	for i := range subEntries { subEntries[i].IsSidechain = true }
	if err := session.WriteTranscriptEntries(subPath, subEntries); err != nil { t.Fatal(err) }
	m := &model{transcriptPath: tp, sessionDir: dir, cm: cm}
	m.replayTranscript()
	// 应只有 user + assistant,无 subagent 段落被追加
	if len(m.paras) != 2 { t.Fatalf("got %d paras, want 2 (orphan subagent should be discarded)", len(m.paras)) }
	for _, p := range m.paras {
		if p.Type == paraSubagent {
			t.Fatal("orphan subagent paragraph should not appear in viewport")
		}
	}
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
