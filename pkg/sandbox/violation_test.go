package sandbox

import (
	"strings"
	"testing"
)

func TestAnnotateViolations_ReadonlyWrite(t *testing.T) {
	stderr := "cp: cannot create regular file '/home/user/.gitconfig': Read-only file system"
	out := AnnotateViolations(stderr)
	if !strings.Contains(out, "<sandbox_violations>") {
		t.Error("missing <sandbox_violations> block")
	}
	if !strings.Contains(out, "write blocked: /home/user/.gitconfig (read-only filesystem)") {
		t.Errorf("missing write violation, got:\n%s", out)
	}
	if !strings.Contains(out, "</sandbox_violations>") {
		t.Error("missing closing tag")
	}
}

func TestAnnotateViolations_PermDenied(t *testing.T) {
	// macOS Seatbelt:遮蔽目录 deny access → EPERM,统一标注 read masked
	stderr := "cat: /home/user/.ssh/id_rsa: Permission denied"
	out := AnnotateViolations(stderr)
	if !strings.Contains(out, "read masked") {
		t.Errorf("missing read violation, got:\n%s", out)
	}
}

func TestAnnotateViolations_NoViolation(t *testing.T) {
	stderr := "normal output\nwarning: nothing happened"
	out := AnnotateViolations(stderr)
	if out != stderr {
		t.Errorf("no-violation stderr should be unchanged, got:\n%s", out)
	}
}

func TestAnnotateViolations_MultipleLines(t *testing.T) {
	stderr := "mkdir: cannot create directory '/etc/x': Read-only file system\n" +
		"cat: /home/user/.ssh/id_rsa: Permission denied\n"
	out := AnnotateViolations(stderr)
	if strings.Count(out, "blocked:") != 1 {
		t.Errorf("want 1 write violation, got:\n%s", out)
	}
	if strings.Count(out, "read masked (returned empty)") != 1 {
		t.Errorf("want 1 read violation, got:\n%s", out)
	}
}

func TestViolationString_EmptyPath(t *testing.T) {
	v := Violation{Kind: "write", Detail: "read-only filesystem"}
	if v.String() != "write blocked: (read-only filesystem)" {
		t.Errorf("String = %q", v.String())
	}
}

func TestAnnotateViolations_NoTrailingNewline(t *testing.T) {
	// stderr 无换行时注解块应自行补换行,不粘连
	stderr := "rm: cannot remove '/tmp/x': Read-only file system"
	out := AnnotateViolations(stderr)
	if !strings.HasPrefix(out, stderr+"\n") {
		t.Errorf("missing newline before annotation, got:\n%s", out)
	}
}
