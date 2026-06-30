package permission

import (
	"fmt"
	"sync"
	"testing"
)

func TestSessionMemory_ToolLevelRemember(t *testing.T) {
	sm := NewSessionMemory()

	sm.Remember("write_file", "", DecisionAllow)

	d, found := sm.Lookup("write_file", "")
	if !found {
		t.Error("Lookup(write_file, '') should find tool-level memory")
	}
	if d != DecisionAllow {
		t.Errorf("Lookup(write_file, '') = %s, want %s", d, DecisionAllow)
	}
}

func TestSessionMemory_ContentLevelRemember(t *testing.T) {
	sm := NewSessionMemory()

	sm.Remember("bash", "git *", DecisionAllow)

	d, found := sm.Lookup("bash", "git *")
	if !found {
		t.Error("Lookup(shell, 'git *') should find content-level memory")
	}
	if d != DecisionAllow {
		t.Errorf("Lookup(shell, 'git *') = %s, want %s", d, DecisionAllow)
	}
}

func TestSessionMemory_ContentLevelTakesPrecedence(t *testing.T) {
	sm := NewSessionMemory()

	// 先设工具级为 deny
	sm.Remember("bash", "", DecisionDeny)
	// 再设内容级为 allow
	sm.Remember("bash", "git *", DecisionAllow)

	// 查找 "git *" 应返回内容级 allow（优先于工具级 deny）
	d, found := sm.Lookup("bash", "git *")
	if !found {
		t.Error("should find memory")
	}
	if d != DecisionAllow {
		t.Errorf("Lookup(shell, 'git *') = %s, want %s (content-level takes precedence)", d, DecisionAllow)
	}

	// 查找 "make" 没有内容级记忆，应返回工具级 deny
	d, found = sm.Lookup("bash", "make")
	if !found {
		t.Error("should find tool-level memory")
	}
	if d != DecisionDeny {
		t.Errorf("Lookup(shell, 'make') = %s, want %s (falls back to tool-level)", d, DecisionDeny)
	}
}

func TestSessionMemory_NotFound(t *testing.T) {
	sm := NewSessionMemory()

	d, found := sm.Lookup("write_file", "")
	if found {
		t.Error("empty memory should not find anything")
	}
	if d != "" {
		t.Errorf("not-found decision = %q, want empty", d)
	}
}

func TestSessionMemory_Len(t *testing.T) {
	sm := NewSessionMemory()

	if sm.Len() != 0 {
		t.Errorf("initial Len = %d, want 0", sm.Len())
	}

	sm.Remember("write_file", "", DecisionAllow)
	if sm.Len() != 1 {
		t.Errorf("after 1 insert, Len = %d, want 1", sm.Len())
	}

	sm.Remember("bash", "git *", DecisionAllow)
	if sm.Len() != 2 {
		t.Errorf("after 2 inserts, Len = %d, want 2", sm.Len())
	}
}

func TestSessionMemory_Clear(t *testing.T) {
	sm := NewSessionMemory()

	sm.Remember("write_file", "", DecisionAllow)
	sm.Remember("bash", "git *", DecisionAllow)
	sm.Clear()

	if sm.Len() != 0 {
		t.Errorf("after Clear, Len = %d, want 0", sm.Len())
	}

	_, found := sm.Lookup("write_file", "")
	if found {
		t.Error("after Clear, Lookup should not find anything")
	}
}

func TestSessionMemory_Entries(t *testing.T) {
	sm := NewSessionMemory()
	sm.Remember("write_file", "", DecisionAllow)
	sm.Remember("bash", "git *", DecisionDeny)

	entries := sm.Entries()
	if len(entries) != 2 {
		t.Fatalf("Entries() returned %d items, want 2", len(entries))
	}

	// 验证来源和范围
	for _, e := range entries {
		if e.Source != SourceSession {
			t.Errorf("Entry.Source = %s, want %s", e.Source, SourceSession)
		}
		if e.Scope != ScopeSession {
			t.Errorf("Entry.Scope = %s, want %s", e.Scope, ScopeSession)
		}
	}
}

func TestSessionMemory_ConcurrentSafety(t *testing.T) {
	sm := NewSessionMemory()
	var wg sync.WaitGroup

	// 并发写入
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sm.Remember("bash", string(rune('a'+i%26))+"*", DecisionAllow)
		}(i)
	}

	// 并发读取
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sm.Lookup("bash", string(rune('a'+i%26))+"*")
		}(i)
	}

	wg.Wait()
	// 只验证不 panic
}

// ---------------------------------------------------------------------------
// 回归测试：shell 命令 prefix 模糊匹配
// ---------------------------------------------------------------------------

func TestSessionMemory_ShellPrefixMatch_StoredExactLookupWithArgs(t *testing.T) {
	sm := NewSessionMemory()
	// 用户批准 "make build" 并记住
	sm.Remember("bash", "make build", DecisionAllow)

	// 后续 LLM 调用 "make build 2>&1" → 应通过 prefix 匹配命中
	d, found := sm.Lookup("bash", "make build 2>&1")
	if !found {
		t.Error("shell prefix match: 'make build' should match 'make build 2>&1'")
	}
	if d != DecisionAllow {
		t.Errorf("shell prefix match: Decision = %s, want %s", d, DecisionAllow)
	}
}

func TestSessionMemory_ShellPrefixMatch_StoredWithArgsLookupExact(t *testing.T) {
	sm := NewSessionMemory()
	// 用户批准 "make build 2>&1" 并记住
	sm.Remember("bash", "make build 2>&1", DecisionAllow)

	// 后续 LLM 调用 "make build" → 应通过 prefix 匹配命中（双向匹配）
	d, found := sm.Lookup("bash", "make build")
	if !found {
		t.Error("shell prefix match: 'make build 2>&1' should match 'make build' (bidirectional)")
	}
	if d != DecisionAllow {
		t.Errorf("shell prefix match: Decision = %s, want %s", d, DecisionAllow)
	}
}

func TestSessionMemory_ShellPrefixMatch_NoMatchDifferentCommand(t *testing.T) {
	sm := NewSessionMemory()
	sm.Remember("bash", "make build", DecisionAllow)

	// 不同命令不应匹配
	_, found := sm.Lookup("bash", "git status")
	if found {
		t.Error("shell prefix match: 'make build' should NOT match 'git status'")
	}
}

func TestSessionMemory_ShellPrefixMatch_ExactMatchStillWorks(t *testing.T) {
	sm := NewSessionMemory()
	sm.Remember("bash", "go test ./...", DecisionAllow)

	d, found := sm.Lookup("bash", "go test ./...")
	if !found {
		t.Error("exact match should still work")
	}
	if d != DecisionAllow {
		t.Errorf("exact match: Decision = %s, want %s", d, DecisionAllow)
	}
}

func TestShellPrefixFuzzyMatch(t *testing.T) {
	tests := []struct {
		a, b string
		match bool
	}{
		{"make build", "make build 2>&1", true},
		{"make build 2>&1", "make build", true},
		{"make build", "make build", true},
		{"git status", "git push", false},
		{"make", "make build", true},
		{"make build", "make", true},
		{"go test", "go test ./...", true},
		{"go test ./...", "go test", true},
		{"docker compose up", "docker compose up -d", true},
		{"docker compose", "docker ps", false},
		{"", "ls", false},
		{"ls", "", false},
		{"", "", true},
	}

	for _, tt := range tests {
		name := fmt.Sprintf("%q_vs_%q", tt.a, tt.b)
		t.Run(name, func(t *testing.T) {
			got := shellPrefixFuzzyMatch(tt.a, tt.b)
			if got != tt.match {
				t.Errorf("shellPrefixFuzzyMatch(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.match)
			}
		})
	}
}
