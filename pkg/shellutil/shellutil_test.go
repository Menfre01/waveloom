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
