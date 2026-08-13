package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/agentloop"
)

// ---- GoldFilesFromPatch ----

func TestGoldFilesFromPatch(t *testing.T) {
	patch := `diff --git a/setup.cfg b/setup.cfg
--- a/setup.cfg
+++ b/setup.cfg
@@ -1,3 +1,4 @@
 old
+new
diff --git a/src/appdirs.py b/src/appdirs.py
--- a/src/appdirs.py
+++ b/src/appdirs.py
@@ -10,2 +10,3 @@
 x
+y
diff --git a/deleted.py b/deleted.py
--- a/deleted.py
+++ /dev/null
@@ -1,2 +0,0 @@
-removed
`
	got := GoldFilesFromPatch(patch)
	want := []string{"setup.cfg", "src/appdirs.py", "deleted.py"}
	if len(got) != len(want) {
		t.Fatalf("got %d files %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("files[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestGoldFilesFromPatch_DedupeAndNoPrefix(t *testing.T) {
	patch := `+++ b/x.py
+++ x.py
+++ b/x.py
`
	got := GoldFilesFromPatch(patch)
	if len(got) != 1 || got[0] != "x.py" {
		t.Fatalf("dedupe failed: %v", got)
	}
}

func TestGoldFilesFromPatch_Empty(t *testing.T) {
	if got := GoldFilesFromPatch(""); len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
	if got := GoldFilesFromPatch("--- a/x.py\n@@ -1 +1 @@\nno plus headers\n"); len(got) != 0 {
		t.Fatalf("no +++ headers should yield empty, got %v", got)
	}
}

// ---- LoadGoldFiles ----

func TestLoadGoldFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gold_patch.diff"),
		[]byte("+++ b/a.py\n+++ b/b/c.py\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := LoadGoldFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0] != "a.py" || files[1] != "b/c.py" {
		t.Fatalf("unexpected files: %v", files)
	}

	if _, err := LoadGoldFiles(t.TempDir()); err == nil {
		t.Fatal("expected error for missing gold_patch.diff")
	}
}

// ---- looksLikeTestCommand ----

func TestLooksLikeTestCommand(t *testing.T) {
	cases := []struct {
		args string
		want bool
	}{
		{"python -m pytest", true},
		{"python3.11 -m pytest", true},
		{"pytest tests/test_x.py -x", true},
		{"python3 -m pytest -q", true},
		{"tox", true},
		{"tox -e py39", true},
		{"python -m unittest", true},
		{"make test", true},
		{"cd /tmp && pytest .", true},
		{"cd /tmp\necho done\npytest .", true},
		{"python -c 'print(1)'", false},
		{"git status", false},
		{"pip install pytest", false}, // 安装非运行
		{"echo pytest", false},        // 参数位置不在命令起点
	}
	for _, c := range cases {
		if got := looksLikeTestCommand(c.args); got != c.want {
			t.Errorf("looksLikeTestCommand(%q)=%v, want %v", c.args, got, c.want)
		}
	}
}

// ---- metricsCollector ----

func TestMetricsCollector_FirstEditTurnAndCounts(t *testing.T) {
	c := newCollector("inst-1")
	c.OnEvent(agentloop.ToolCallStart{Turn: 1, ToolCallID: "t1", ToolCallName: "read", Arguments: `{"file_path":"a.py"}`})
	c.OnEvent(agentloop.ToolCallResult{Turn: 1, ToolCallID: "t1", ToolCallName: "read"})
	c.OnEvent(agentloop.ToolCallStart{Turn: 2, ToolCallID: "t2", ToolCallName: "edit", Arguments: `{"file_path":"a.py"}`})
	c.OnEvent(agentloop.ToolCallResult{Turn: 2, ToolCallID: "t2", ToolCallName: "edit"})
	c.OnEvent(agentloop.ToolCallStart{Turn: 3, ToolCallID: "t3", ToolCallName: "bash", Arguments: `{"command":"pytest"}`})
	c.OnEvent(agentloop.ToolCallResult{Turn: 3, ToolCallID: "t3", ToolCallName: "bash"})
	c.OnEvent(agentloop.LoopDone{Turn: 3, Reason: agentloop.ReasonMaxTurns})

	m := c.m
	if m.FirstEditTurn != 2 {
		t.Errorf("FirstEditTurn=%d, want 2", m.FirstEditTurn)
	}
	if m.Edits != 1 || m.Reads != 1 || m.BashCalls != 1 {
		t.Errorf("counts wrong: %+v", m)
	}
	if m.TestAttempts != 1 {
		t.Errorf("TestAttempts=%d, want 1", m.TestAttempts)
	}
	if m.TotalTurns != 3 || m.TerminalReason != "max_turns" {
		t.Errorf("LoopDone not recorded: %+v", m)
	}
	if len(m.ToolSequence) != 3 || m.ToolSequence[0] != "read" {
		t.Errorf("ToolSequence wrong: %v", m.ToolSequence)
	}
}

func TestMetricsCollector_WriteCountsAsFirstEdit(t *testing.T) {
	c := newCollector("inst-2")
	c.OnEvent(agentloop.ToolCallStart{Turn: 1, ToolCallID: "w1", ToolCallName: "write", Arguments: `{}`})
	c.OnEvent(agentloop.ToolCallResult{Turn: 1, ToolCallID: "w1", ToolCallName: "write"})
	if c.m.FirstEditTurn != 1 || c.m.Writes != 1 {
		t.Fatalf("write not counted: %+v", c.m)
	}
}

func TestMetricsCollector_BashErrorsAndDenied(t *testing.T) {
	c := newCollector("inst-3")
	c.OnEvent(agentloop.ToolCallStart{Turn: 1, ToolCallID: "b1", ToolCallName: "bash", Arguments: `{"command":"pytest"}`})
	c.OnEvent(agentloop.ToolCallResult{Turn: 1, ToolCallID: "b1", ToolCallName: "bash", Error: "exit 1", ErrorKind: "command_failed"})
	c.OnEvent(agentloop.ToolCallStart{Turn: 2, ToolCallID: "b2", ToolCallName: "write", Arguments: `{}`})
	c.OnEvent(agentloop.ToolCallResult{Turn: 2, ToolCallID: "b2", ToolCallName: "write", Denied: true})

	m := c.m
	if m.BashErrors != 1 {
		t.Errorf("BashErrors=%d, want 1", m.BashErrors)
	}
	// 错误 bash 不算测试尝试(未成功执行)
	if m.TestAttempts != 0 {
		t.Errorf("TestAttempts=%d, want 0 (bash errored)", m.TestAttempts)
	}
	if m.DeniedCalls != 1 {
		t.Errorf("DeniedCalls=%d, want 1", m.DeniedCalls)
	}
}

// ---- intersect ----

func TestIntersect(t *testing.T) {
	a := []string{"b.py", "a.py", "c.py"}
	b := []string{"a.py", "d.py"}
	got := intersect(a, b)
	if len(got) != 1 || got[0] != "a.py" {
		t.Fatalf("intersect=%v, want [a.py]", got)
	}
	if got := intersect(nil, b); len(got) != 0 {
		t.Fatalf("nil left should yield empty, got %v", got)
	}
}

// ---- LoadInstance / splitIDs ----

func TestLoadInstance_MissingRepo(t *testing.T) {
	results := t.TempDir()
	instDir := filepath.Join(results, "x")
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadInstance(results, "x"); err == nil {
		t.Fatal("expected error for missing repo")
	} else if !strings.Contains(err.Error(), "repo") {
		t.Fatalf("error should mention repo: %v", err)
	}
}

func TestSplitIDs(t *testing.T) {
	got := splitIDs(" a,b ,,c ")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("splitIDs=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitIDs=%v, want %v", got, want)
		}
	}
	if len(splitIDs("")) != 0 {
		t.Fatal("empty input should yield empty")
	}
}

// TestGoldFilesFromPatch_PlusPlusNoPath +++ 行无路径时跳过(正则不匹配分支)。
func TestGoldFilesFromPatch_PlusPlusNoPath(t *testing.T) {
	if got := GoldFilesFromPatch("+++\n"); len(got) != 0 {
		t.Fatalf("bare +++ should be skipped, got %v", got)
	}
}
