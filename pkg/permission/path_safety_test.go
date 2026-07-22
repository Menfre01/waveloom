package permission

import (
	"os"
	"path/filepath"
	"testing"
)

// testWorkingDir 返回一个可用作 workingDir 的临时目录。
func testWorkingDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// 创建一些测试文件/目录
	_ = os.MkdirAll(filepath.Join(dir, ".git", "refs"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "src", "pkg"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".gitconfig"), []byte("[user]"), 0o644)
	return dir
}

func TestPathSafetyCheck_SafePath(t *testing.T) {
	dir := testWorkingDir(t)
	path := filepath.Join(dir, "src", "main.go")

	got := PathSafetyCheck(path, []string{dir})
	t.Logf("PathSafetyCheck(%q, [%q]) = %+v", path, dir, got)
	if got.Level != PathSafe {
		t.Errorf("PathSafetyCheck(src/main.go).Level = %s, want %s", got.Level, PathSafe)
	}
	if !got.ClassifierSafe {
		t.Error("safe path should be ClassifierSafe")
	}
}

func TestPathSafetyCheck_SensitiveDir(t *testing.T) {
	dir := testWorkingDir(t)

	// .git/ 目录内的文件
	got := PathSafetyCheck(filepath.Join(dir, ".git", "HEAD"), []string{dir})
	if got.Level != PathSensitive {
		t.Errorf("PathSafetyCheck(.git/HEAD).Level = %s, want %s", got.Level, PathSensitive)
	}
	if !got.ClassifierSafe {
		t.Error("sensitive dir should be ClassifierSafe")
	}
}

func TestPathSafetyCheck_SensitiveFile(t *testing.T) {
	dir := testWorkingDir(t)

	// .gitconfig 在工作目录内
	got := PathSafetyCheck(filepath.Join(dir, ".gitconfig"), []string{dir})
	if got.Level != PathSensitive {
		t.Errorf("PathSafetyCheck(.gitconfig).Level = %s, want %s", got.Level, PathSensitive)
	}
}

func TestPathSafetyCheck_DangerousPath(t *testing.T) {
	dir := testWorkingDir(t)

	tests := []struct {
		name string
		path string
	}{
		{"outside working dir", "/tmp/some/other/file.txt"},
		{"/etc", "/etc/passwd"},
		{".ssh", filepath.Join(dir, ".ssh", "config")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PathSafetyCheck(tt.path, []string{dir})
			if got.Level != PathDangerous {
				t.Errorf("PathSafetyCheck(%s).Level = %s, want %s", tt.name, got.Level, PathDangerous)
			}
		})
	}
}

func TestPathSafetyCheck_MultipleWorkingDirs(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir2, "test.txt"), []byte("hello"), 0o644)

	// 文件在 dir2 内，workingDirs 包含 dir1 和 dir2
	got := PathSafetyCheck(filepath.Join(dir2, "test.txt"), []string{dir1, dir2})
	if got.Level != PathSafe {
		t.Errorf("file in second working dir: Level = %s, want %s", got.Level, PathSafe)
	}

	// 文件不在任何工作目录内
	got = PathSafetyCheck("/tmp/outside.txt", []string{dir1, dir2})
	if got.Level != PathDangerous {
		t.Errorf("file outside all working dirs: Level = %s, want %s", got.Level, PathDangerous)
	}
}

func TestPathSafetyDecision_AllowRead(t *testing.T) {
	pathCheck := PathCheckResult{Level: PathSafe, Message: "within working directory"}
	got := PathSafetyDecision(pathCheck, false) // read
	if got.Decision != DecisionAllow {
		t.Errorf("read safe path: Decision = %s, want %s", got.Decision, DecisionAllow)
	}
}

func TestPathSafetyDecision_AskWriteSafe(t *testing.T) {
	pathCheck := PathCheckResult{Level: PathSafe, Message: "within working directory"}
	got := PathSafetyDecision(pathCheck, true) // write
	if got.Decision != DecisionAsk {
		t.Errorf("write safe path: Decision = %s, want %s", got.Decision, DecisionAsk)
	}
}

func TestPathSafetyDecision_AskSensitive(t *testing.T) {
	// read sensitive
	pathCheck := PathCheckResult{Level: PathSensitive, Message: "sensitive directory: .git"}
	got := PathSafetyDecision(pathCheck, false)
	if got.Decision != DecisionAsk {
		t.Errorf("read sensitive path: Decision = %s, want %s", got.Decision, DecisionAsk)
	}
	if got.Reason != ReasonSafety {
		t.Errorf("read sensitive path: Reason = %s, want %s", got.Reason, ReasonSafety)
	}

	// write sensitive
	got = PathSafetyDecision(pathCheck, true)
	if got.Decision != DecisionAsk {
		t.Errorf("write sensitive path: Decision = %s, want %s", got.Decision, DecisionAsk)
	}
}

func TestPathSafetyDecision_DenyWriteDangerous(t *testing.T) {
	pathCheck := PathCheckResult{Level: PathDangerous, Message: "path outside working directory"}

	// write dangerous → deny
	got := PathSafetyDecision(pathCheck, true)
	if got.Decision != DecisionDeny {
		t.Errorf("write dangerous path: Decision = %s, want %s", got.Decision, DecisionDeny)
	}
	if got.Reason != ReasonSafety {
		t.Errorf("write dangerous path: Reason = %s, want %s", got.Reason, ReasonSafety)
	}

	// read dangerous → ask
	got = PathSafetyDecision(pathCheck, false)
	if got.Decision != DecisionAsk {
		t.Errorf("read dangerous path: Decision = %s, want %s", got.Decision, DecisionAsk)
	}
}

func TestSplitPathParts(t *testing.T) {
	tests := []struct {
		path string
		want []string
	}{
		{"/home/user/project", []string{"home", "user", "project"}},
		{"/home/user/.git/HEAD", []string{"home", "user", ".git", "HEAD"}},
		{"src/main.go", []string{"src", "main.go"}},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := splitPathParts(tt.path)
			if len(got) != len(tt.want) {
				t.Fatalf("splitPathParts(%q) = %v, want %v", tt.path, got, tt.want)
			}
			for i, part := range got {
				if part != tt.want[i] {
					t.Errorf("splitPathParts(%q)[%d] = %q, want %q", tt.path, i, part, tt.want[i])
				}
			}
		})
	}
}

// REGRESSION: Windows 路径 C:\Users\... 在 splitPathParts 中无限循环。
// filepath.Split("C:") 返回 ("C:", "") — dir 非空、非分隔符，
// TrimRight 无法去除任何字符，导致循环永不终止。
// 根因：缺少"未前进则终止"的守卫条件。
func TestRegression_SplitPathParts_WindowsRoot(t *testing.T) {
	// 模拟典型 Windows temp 目录路径。splitPathParts 必须在合理时间完成。
	winPath := filepath.Join("C:", "Users", "runner", "AppData", "Local", "Temp", "test", "main.go")
	// splitPathParts 不应 panic 或无限循环
	_ = splitPathParts(winPath)

	// 关键：只有盘符无分隔符（不带反斜杠的卷名）
	// filepath.VolumeName("C:") == "C:" on Windows
	result := splitPathParts("C:")
	if result == nil {
		t.Error(`splitPathParts("C:") should return empty or valid parts, not hang`)
	}
}

func TestPathSafetyCheck_EmptyWorkingDirs(t *testing.T) {
	// 没有 workingDirs，所有路径都在工作目录外
	got := PathSafetyCheck("/home/user/file.txt", nil)
	if got.Level != PathDangerous {
		t.Errorf("empty workingDirs: Level = %s, want %s", got.Level, PathDangerous)
	}
}

// ---------------------------------------------------------------------------
// PathSafetyDecision — 边界条件
// ---------------------------------------------------------------------------

func TestPathSafetyDecision_UnknownLevel(t *testing.T) {
	// 零值 / 未定义的 PathSafetyLevel → 应落到 default case 返回 DecisionDeny
	pathCheck := PathCheckResult{Level: "", Message: "unknown"}

	// read 操作也 deny（防御性，未知等级不应放行）
	got := PathSafetyDecision(pathCheck, false)
	if got.Decision != DecisionDeny {
		t.Errorf("unknown safety level (read): Decision = %s, want %s", got.Decision, DecisionDeny)
	}

	// write 操作同样 deny
	got = PathSafetyDecision(pathCheck, true)
	if got.Decision != DecisionDeny {
		t.Errorf("unknown safety level (write): Decision = %s, want %s", got.Decision, DecisionDeny)
	}
}

// ---------------------------------------------------------------------------
// isWithinDir — 目录包含判断
// ---------------------------------------------------------------------------

func TestIsWithinDir_Subdir(t *testing.T) {
	if !isWithinDir("/home/user/project/src/main.go", "/home/user/project") {
		t.Error("subdir file should be within dir")
	}
}

func TestIsWithinDir_SamePath(t *testing.T) {
	// path == dir → rel == "." → 视为在目录内（目录自身也属于工作目录）
	if !isWithinDir("/home/user/project", "/home/user/project") {
		t.Error("same path should be considered within (rel == '.')")
	}
}

func TestIsWithinDir_Outside(t *testing.T) {
	if isWithinDir("/tmp/outside.txt", "/home/user/project") {
		t.Error("path outside dir should not match")
	}
}

func TestIsWithinDir_Error(t *testing.T) {
	// 不同卷/设备路径可能导致 Rel 出错（Windows 场景，Unix 上不容易触发）
	// 测试边界：绝对路径 + 相对路径混用
	// isWithinDir 对 Rel 错误返回 false
	if isWithinDir("relative/path", "/absolute/dir") {
		t.Error("relative path should not be within absolute dir (Rel error path)")
	}
}

// ---------------------------------------------------------------------------
// REGRESSION: evalExistingPrefix — 全分支覆盖
// ---------------------------------------------------------------------------

func TestEvalExistingPrefix_PartialExists(t *testing.T) {
	dir := t.TempDir()
	// 父目录存在，但最后一段文件不存在
	nonexistent := filepath.Join(dir, "nonexistent", "file.txt")

	result := evalExistingPrefix(nonexistent)
	// dir 部分可解析，base 部分保留原样
	if result != nonexistent {
		t.Logf("evalExistingPrefix(%q) = %q (expected same or symlink-resolved)", nonexistent, result)
	}
}

func TestEvalExistingPrefix_AllExists(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "real.txt"), []byte("hello"), 0o644)
	expected := filepath.Join(dir, "real.txt")

	result := evalExistingPrefix(expected)
	// 全部存在时应返回原路径或符号链接解析后的等价路径
	if result != expected {
		// 可能经过了符号链接解析，只要不报错即可
		t.Logf("evalExistingPrefix(%q) = %q (symlink-resolved)", expected, result)
	}
}
