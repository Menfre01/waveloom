package shellutil

import "testing"

func TestIsBackgroundCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"echo hello &", true},
		{"echo hello & ", true},
		{"echo hello 2>&1 &", true},
		{"npx wrangler dev --port 8794 2>&1 &", true},
		{"echo hello", false},
		{"echo foo & echo bar", false},
		{"echo hello && echo world", false},
		{"", false},
		{"&", true},
		// 多行 —— 某一行以 & 结尾
		{"npx wrangler dev --port 8787 2>&1 &\nsleep 12", true},
		{"echo hello\nsleep 100 &", true},
		{"echo hello &\necho world &", true},
		// 多行 —— 无 &
		{"echo hello\necho world", false},
		// 多行 —— & 不在行尾
		{"echo foo & echo bar\necho baz", false},
	}
	for _, tt := range tests {
		got := IsBackgroundCommand(tt.cmd)
		if got != tt.want {
			t.Errorf("IsBackgroundCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

// REGRESSION: Windows 自带 %SystemRoot%\System32\bash.exe 是 WSL 启动器而非
// Git Bash。GitHub windows-latest 的 PATH 里有它,裸 LookPath("bash.exe") 会命中,
// 未装 WSL 发行版时任何命令都返回 exit 1 且无输出 —— skill 动态注入全部静默失败。
func TestIsWSLBashLauncher(t *testing.T) {
	t.Setenv("SystemRoot", "/fake-windows")

	cases := []struct {
		name string
		path string
		want bool
	}{
		{"system32-bash", "/fake-windows/System32/bash.exe", true},
		{"case-insensitive", "/FAKE-WINDOWS/system32/BASH.EXE", true},
		{"with-dot-segments", "/fake-windows/./System32/../System32/bash.exe", true},
		{"git-bash", "/Program Files/Git/bin/bash.exe", false},
		{"unix-bash", "/bin/bash", false},
		{"empty", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isWSLBashLauncher(c.path); got != c.want {
				t.Errorf("isWSLBashLauncher(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}