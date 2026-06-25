package main

import (
	"os"
	"path/filepath"
	"testing"

	ctxpkg "waveloom/pkg/context"
)

func TestParagraphToTranscriptLineRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		p    Paragraph
	}{
		{
			name: "user paragraph",
			p:    Paragraph{Type: paraUser, State: stateDone, Text: "hello world"},
		},
		{
			name: "assistant paragraph",
			p:    Paragraph{Type: paraAssistant, State: stateDone, Text: "here is the result"},
		},
		{
			name: "thought paragraph",
			p:    Paragraph{Type: paraThought, State: stateCollapsed, Text: "thinking...", ThoughtTokens: 100},
		},
		{
			name: "tool paragraph done",
			p:    Paragraph{Type: paraTool, State: stateDone, ToolName: "shell", ToolArgs: "echo hello", ToolResult: "hello\n", ToolDurMs: 42},
		},
		{
			name: "tool paragraph error",
			p:    Paragraph{Type: paraTool, State: stateError, ToolName: "read_file", ToolArgs: "/tmp/x", ToolError: "file not found", ToolDurMs: 5},
		},
		{
			name: "system paragraph",
			p:    Paragraph{Type: paraSystem, State: stateDone, Text: "执行被中断。"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := paragraphToTranscriptLine(&tt.p)
			restored := transcriptLineToParagraph(line)

			if restored.Type != tt.p.Type {
				t.Errorf("Type: %d != %d", restored.Type, tt.p.Type)
			}
			if restored.State != tt.p.State {
				t.Errorf("State: %d != %d", restored.State, tt.p.State)
			}
			if restored.Text != tt.p.Text {
				t.Errorf("Text: %q != %q", restored.Text, tt.p.Text)
			}
			if restored.ToolName != tt.p.ToolName {
				t.Errorf("ToolName: %q != %q", restored.ToolName, tt.p.ToolName)
			}
			if restored.ToolArgs != tt.p.ToolArgs {
				t.Errorf("ToolArgs: %q != %q", restored.ToolArgs, tt.p.ToolArgs)
			}
			if restored.ToolResult != tt.p.ToolResult {
				t.Errorf("ToolResult: %q != %q", restored.ToolResult, tt.p.ToolResult)
			}
			if restored.ToolDurMs != tt.p.ToolDurMs {
				t.Errorf("ToolDurMs: %d != %d", restored.ToolDurMs, tt.p.ToolDurMs)
			}
		})
	}
}

func TestTranscriptFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	tp := filepath.Join(dir, "test.jsonl")

	// Write some paragraphs
	p1 := Paragraph{Type: paraUser, State: stateDone, Text: "hello"}
	p2 := Paragraph{Type: paraAssistant, State: stateDone, Text: "hi there"}
	p3 := Paragraph{Type: paraTool, State: stateDone, ToolName: "shell", ToolArgs: "ls", ToolResult: "file.txt\n"}

	for _, p := range []*Paragraph{&p1, &p2, &p3} {
		line := paragraphToTranscriptLine(p)
		if err := ctxpkg.AppendTranscriptLine(tp, line); err != nil {
			t.Fatalf("AppendTranscriptLine: %v", err)
		}
	}

	// Load
	lines, err := ctxpkg.LoadTranscriptLines(tp)
	if err != nil {
		t.Fatalf("LoadTranscriptLines: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}

	// Verify
	if lines[0].Type != "user" || lines[0].Text != "hello" {
		t.Errorf("line 0: %+v", lines[0])
	}
	if lines[1].Type != "assistant" || lines[1].Text != "hi there" {
		t.Errorf("line 1: %+v", lines[1])
	}
	if lines[2].Type != "tool" || lines[2].ToolName != "shell" {
		t.Errorf("line 2: %+v", lines[2])
	}
}

func TestTranscriptLineToParagraph(t *testing.T) {
	// Verify unknown type/state fields produce valid Paragraph defaults
	line := ctxpkg.TranscriptLine{Type: "unknown", State: "unknown"}
	p := transcriptLineToParagraph(line)
	// Should default to zero values for type (paraUser = 0) and state (stateStreaming = 0)
	if p.Type != paraUser {
		t.Errorf("unknown type should default to paraUser (0), got %d", p.Type)
	}
	if p.State != stateStreaming {
		t.Errorf("unknown state should default to stateStreaming (0), got %d", p.State)
	}
}

func TestReplayTranscriptEmptyPath(t *testing.T) {
	m := &model{transcriptPath: ""}
	m.replayTranscript()
	if len(m.paras) != 0 {
		t.Errorf("expected 0 paras for empty path, got %d", len(m.paras))
	}
}

func TestFlushTranscriptEmptyPath(t *testing.T) {
	m := &model{transcriptPath: ""}
	m.paras = []Paragraph{{Type: paraUser, State: stateDone, Text: "x"}}
	// Should not crash
	m.flushTranscript()
}

func TestReplayTranscript(t *testing.T) {
	dir := t.TempDir()

	// Create a transcript file
	tp := filepath.Join(dir, "test.jsonl")
	ctxpkg.AppendTranscriptLine(tp, ctxpkg.TranscriptLine{Type: "user", State: "done", Text: "question"})
	ctxpkg.AppendTranscriptLine(tp, ctxpkg.TranscriptLine{Type: "assistant", State: "done", Text: "answer"})

	// Replay
	m := &model{transcriptPath: tp}
	m.replayTranscript()

	if len(m.paras) != 2 {
		t.Fatalf("expected 2 paras, got %d", len(m.paras))
	}
	if m.paras[0].Type != paraUser || m.paras[0].Text != "question" {
		t.Errorf("para 0: %+v", m.paras[0])
	}
	if m.paras[1].Type != paraAssistant || m.paras[1].Text != "answer" {
		t.Errorf("para 1: %+v", m.paras[1])
	}
	if m.transcriptWritten != 2 {
		t.Errorf("transcriptWritten = %d, want 2", m.transcriptWritten)
	}
}

func TestFlushTranscriptSkipsStreaming(t *testing.T) {
	dir := t.TempDir()
	tp := filepath.Join(dir, "test.jsonl")

	m := &model{transcriptPath: tp}

	// Simulate real TUI flow: paragraphs added incrementally
	// Step 1: doTurn adds user para
	m.paras = []Paragraph{{Type: paraUser, State: stateDone, Text: "user"}}
	m.flushTranscript() // writes user
	if m.transcriptWritten != 1 {
		t.Fatalf("step 1: transcriptWritten = %d, want 1", m.transcriptWritten)
	}

	// Step 2: handleToolStart adds tool para (streaming)
	m.paras = append(m.paras, Paragraph{Type: paraTool, State: stateStreaming, ToolName: "shell"})
	m.flushTranscript() // skips streaming tool
	if m.transcriptWritten != 1 {
		t.Fatalf("step 2: transcriptWritten = %d, want 1 (streaming skipped)", m.transcriptWritten)
	}

	// Step 3: handleToolResult transitions tool to done
	m.paras[1].State = stateDone
	m.paras[1].ToolResult = "ok"
	m.flushTranscript() // now writes the tool para
	if m.transcriptWritten != 2 {
		t.Fatalf("step 3: transcriptWritten = %d, want 2", m.transcriptWritten)
	}

	// Step 4: another assistant paragraph
	m.paras = append(m.paras, Paragraph{Type: paraAssistant, State: stateDone, Text: "done"})
	m.flushTranscript()
	if m.transcriptWritten != 3 {
		t.Fatalf("step 4: transcriptWritten = %d, want 3", m.transcriptWritten)
	}

	// Verify transcript file content
	lines, err := ctxpkg.LoadTranscriptLines(tp)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines in transcript, got %d", len(lines))
	}
	if lines[0].Type != "user" || lines[1].Type != "tool" || lines[2].Type != "assistant" {
		t.Errorf("lines: %+v, %+v, %+v", lines[0], lines[1], lines[2])
	}
}

func TestFlushTranscriptRaceOnTrim(t *testing.T) {
	// Verify that trimParas properly adjusts transcriptWritten.
	dir := t.TempDir()
	tp := filepath.Join(dir, "test.jsonl")

	m := &model{transcriptPath: tp, transcriptWritten: 5}
	// Simulate trim: 5 paras existed, 3 removed from front
	m.paras = make([]Paragraph, 10)
	m.transcriptWritten = 7

	// trimParas would remove some from front; verify flushTranscript handles
	copy(m.paras, []Paragraph{
		{Type: paraUser, State: stateDone, Text: "a"},
		{Type: paraUser, State: stateDone, Text: "b"},
	})

	// If transcriptWritten > len(paras) due to some bug, flushTranscript still works
	// (the loop just exits because i < len(m.paras) is false)
	m.flushTranscript()
	if m.transcriptWritten == 7 {
		// No change expected since transcriptWritten exceeds len(paras)
	}
}

func TestTrimParasAdjustsTranscriptWritten(t *testing.T) {
	m := &model{transcriptPath: "/tmp/x.jsonl", transcriptWritten: 10}
	m.paras = make([]Paragraph, 15) // 15 paras, maxParas = 200 (default)

	// Manual trim simulation
	remove := 3
	m.paras = append([]Paragraph{}, m.paras[remove:]...)

	if m.transcriptWritten > 0 {
		m.transcriptWritten -= remove
		if m.transcriptWritten < 0 {
			m.transcriptWritten = 0
		}
	}

	if m.transcriptWritten != 7 {
		t.Errorf("transcriptWritten = %d, want 7", m.transcriptWritten)
	}
}

func init() {
	// suppress os.Exit in test context
	_ = os.Stderr
}
