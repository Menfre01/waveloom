package permission

import "testing"

func TestInterpretExitCode_Default(t *testing.T) {
	// 普通命令 exit 0 = 成功
	r := InterpretExitCode("ls -la", 0, "", "")
	if r.IsError {
		t.Error("ls exit 0 should not be error")
	}
	// exit 1 = 错误
	r = InterpretExitCode("ls -la", 1, "", "")
	if !r.IsError {
		t.Error("ls exit 1 should be error")
	}
}

func TestInterpretExitCode_Grep(t *testing.T) {
	// grep exit 0 = 有匹配
	r := InterpretExitCode("grep pattern file", 0, "", "")
	if r.IsError {
		t.Error("grep exit 0 should not be error")
	}
	// grep exit 1 = 无匹配 (不是错误)
	r = InterpretExitCode("grep pattern file", 1, "", "")
	if r.IsError {
		t.Error("grep exit 1 should not be error (no matches)")
	}
	if r.Message != "no matches found" {
		t.Errorf("grep exit 1 message = %q, want 'no matches found'", r.Message)
	}
	// grep exit 2 = 真实错误
	r = InterpretExitCode("grep pattern file", 2, "", "")
	if !r.IsError {
		t.Error("grep exit 2 should be error")
	}
}

func TestInterpretExitCode_Find(t *testing.T) {
	r := InterpretExitCode("find . -name '*.go'", 1, "", "")
	if r.IsError {
		t.Error("find exit 1 should not be error")
	}
	if r.Message != "some directories were inaccessible" {
		t.Errorf("find exit 1 message = %q", r.Message)
	}
	r = InterpretExitCode("find . -name '*.go'", 2, "", "")
	if !r.IsError {
		t.Error("find exit 2 should be error")
	}
}

func TestInterpretExitCode_Diff(t *testing.T) {
	r := InterpretExitCode("diff a.txt b.txt", 1, "", "")
	if r.IsError {
		t.Error("diff exit 1 should not be error")
	}
	if r.Message != "files differ" {
		t.Errorf("diff exit 1 message = %q", r.Message)
	}
}

func TestInterpretExitCode_Test(t *testing.T) {
	r := InterpretExitCode("test -f file.txt", 1, "", "")
	if r.IsError {
		t.Error("test exit 1 should not be error")
	}
	// [ 是 test 别名
	r = InterpretExitCode("[ -f file.txt ]", 1, "", "")
	if r.IsError {
		t.Error("[ exit 1 should not be error")
	}
}

func TestInterpretExitCode_Rg(t *testing.T) {
	r := InterpretExitCode("rg pattern", 1, "", "")
	if r.IsError {
		t.Error("rg exit 1 should not be error")
	}
	if r.Message != "no matches found" {
		t.Errorf("rg exit 1 message = %q", r.Message)
	}
}

func TestInterpretExitCode_Git(t *testing.T) {
	// git diff exit 1 = 有差异
	r := InterpretExitCode("git diff", 1, "diff --git", "")
	if r.IsError {
		t.Error("git diff exit 1 should not be error")
	}
	// git grep exit 1 = 无匹配
	r = InterpretExitCode("git grep pattern", 1, "", "grep")
	if r.IsError {
		t.Error("git grep exit 1 should not be error")
	}
	// git status exit 1 = 未知，默认为错误
	r = InterpretExitCode("git status", 1, "", "")
	if !r.IsError {
		t.Error("git status exit 1 should be error")
	}
}
