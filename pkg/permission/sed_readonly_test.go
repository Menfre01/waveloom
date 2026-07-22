package permission

import "testing"

// ============================================================================
// IsSedReadOnly
// ============================================================================

func TestIsSedReadOnly_PrintOnly(t *testing.T) {
	// sed -n '10p' file → 只读
	if !IsSedReadOnly("sed -n '10p' file.txt") {
		t.Error("sed -n '10p' should be read-only")
	}
	// sed -n '1,5p' file
	if !IsSedReadOnly("sed -n '1,5p' file.txt") {
		t.Error("sed -n '1,5p' should be read-only")
	}
}

func TestIsSedReadOnly_DeleteFromOutput(t *testing.T) {
	// sed '/pattern/d' file → 只读（d 删除输出行，不改文件）
	if !IsSedReadOnly("sed '/pattern/d' file.txt") {
		t.Error("sed '/pattern/d' should be read-only")
	}
}

func TestIsSedReadOnly_InPlaceWrite(t *testing.T) {
	// sed -i 's/a/b/' file → 写操作
	if IsSedReadOnly("sed -i 's/a/b/' file.txt") {
		t.Error("sed -i should NOT be read-only")
	}
	// sed --in-place 's/a/b/' file
	if IsSedReadOnly("sed --in-place 's/a/b/' file.txt") {
		t.Error("sed --in-place should NOT be read-only")
	}
}

func TestIsSedReadOnly_SubstituteWithoutInPlace(t *testing.T) {
	// sed 's/a/b/' file (无 -i) → 输出到 stdout，仍是写操作（s 命令）
	if IsSedReadOnly("sed 's/a/b/' file.txt") {
		t.Error("sed 's/a/b/' without -n should NOT be read-only")
	}
}

func TestIsSedReadOnly_Append(t *testing.T) {
	// sed '/pattern/a text' → a 命令是写操作
	if IsSedReadOnly("sed '/pattern/a new line' file.txt") {
		t.Error("sed 'a' command should NOT be read-only")
	}
}

func TestIsSedReadOnly_Insert(t *testing.T) {
	// sed '/pattern/i text' → i 命令是写操作
	if IsSedReadOnly("sed '/pattern/i new line' file.txt") {
		t.Error("sed 'i' command should NOT be read-only")
	}
}

func TestIsSedReadOnly_Change(t *testing.T) {
	// sed '/pattern/c text' → c 命令是写操作
	if IsSedReadOnly("sed '/pattern/c new line' file.txt") {
		t.Error("sed 'c' command should NOT be read-only")
	}
}

func TestIsSedReadOnly_UnknownCommand(t *testing.T) {
	// 未知命令 → 保守：不是只读
	if IsSedReadOnly("sed 'x' file.txt") {
		t.Error("unknown sed command should NOT be read-only")
	}
}

func TestIsSedReadOnly_EmptyExpression(t *testing.T) {
	if IsSedReadOnly("sed file.txt") {
		t.Error("sed without expression should NOT be read-only")
	}
}

func TestIsSedReadOnly_QuotedExpression(t *testing.T) {
	// 双引号内命令
	if !IsSedReadOnly("sed -n \"10p\" file.txt") {
		t.Error("sed -n \"10p\" should be read-only")
	}
}

func TestIsSedReadOnly_NotSedCommand(t *testing.T) {
	if IsSedReadOnly("grep pattern file") {
		t.Error("non-sed command should NOT be identified as sed")
	}
}

// ============================================================================
// tokenizeSed
// ============================================================================

func TestTokenizeSed(t *testing.T) {
	tests := []struct {
		cmd    string
		expect []string
	}{
		{"sed -n '10p' file.txt", []string{"sed", "-n", "10p", "file.txt"}},
		{"sed -e 's/a/b/' file.txt", []string{"sed", "-e", "s/a/b/", "file.txt"}},
		{`sed -n "10p" file.txt`, []string{"sed", "-n", "10p", "file.txt"}},
	}
	for _, tc := range tests {
		got := tokenizeSed(tc.cmd)
		if len(got) != len(tc.expect) {
			t.Errorf("tokenizeSed(%q) = %v (len=%d), want %v (len=%d)", tc.cmd, got, len(got), tc.expect, len(tc.expect))
			continue
		}
		for i := range got {
			if got[i] != tc.expect[i] {
				t.Errorf("tokenizeSed(%q)[%d] = %q, want %q", tc.cmd, i, got[i], tc.expect[i])
			}
		}
	}
}
