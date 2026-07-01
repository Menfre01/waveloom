package permission

import (
	"reflect"
	"testing"
)

// ---------------------------------------------------------------------------
// MatchBashPattern — 全分支覆盖
// ---------------------------------------------------------------------------

func TestMatchBashPattern_Wildcard(t *testing.T) {
	// "*" 匹配任意命令
	if !MatchBashPattern("anything", "*") {
		t.Error(`"*" should match anything`)
	}
	// "" 等价于 "*"
	if !MatchBashPattern("anything", "") {
		t.Error(`"" should match anything`)
	}
}

func TestMatchBashPattern_Exact(t *testing.T) {
	if !MatchBashPattern("git status", "git status") {
		t.Error("exact match should work")
	}
	if MatchBashPattern("git status", "git push") {
		t.Error("should not match different command")
	}
}

func TestMatchBashPattern_Prefix(t *testing.T) {
	// "git *" → 前缀匹配
	if !MatchBashPattern("git status", "git *") {
		t.Error(`"git *" should match "git status" (prefix with space boundary)`)
	}
	// 精确匹配无参数
	if !MatchBashPattern("git", "git *") {
		t.Error(`"git *" should match "git" (exact without args)`)
	}
	// 词边界包含匹配: "sudo git status" 中 "git" 前后有空格
	if !MatchBashPattern("sudo git status", "git *") {
		t.Error(`"git *" should match "sudo git status" (word-boundary contains)`)
	}
	// 不匹配：gitfoo 中 "git" 无词边界
	if MatchBashPattern("gitfoo", "git *") {
		t.Error(`"git *" should NOT match "gitfoo" (no word boundary)`)
	}
	// 路径边界包含匹配: "/gstack-update-check" 由 / 分隔
	if !MatchBashPattern("~/.claude/skills/gstack-update-check", "gstack-update-check *") {
		t.Error(`"gstack-update-check *" should match "~/.claude/.../gstack-update-check" (path-boundary contains)`)
	}
	// 无空格 pattern（如 "gstack*"）：前缀失败后回退包含匹配
	if !MatchBashPattern("~/.claude/skills/gstack-update-check", "gstack*") {
		t.Error(`"gstack*" should match via prefix + contains fallback`)
	}
}

func TestMatchBashPattern_Suffix(t *testing.T) {
	if !MatchBashPattern("some/path/build.sh", "*.sh") {
		t.Error(`"*.sh" should match "build.sh" (suffix)`)
	}
	if MatchBashPattern("build.sh.bak", "*.sh") {
		t.Error(`"*.sh" should NOT match "build.sh.bak"`)
	}
}

func TestMatchBashPattern_Contains(t *testing.T) {
	if !MatchBashPattern("some/long/path/git/status", "*git*") {
		t.Error(`"*git*" should match path containing git`)
	}
	if MatchBashPattern("foo", "*git*") {
		t.Error(`"*git*" should NOT match "foo"`)
	}
}

func TestMatchBashPattern_DoubleStar(t *testing.T) {
	// "**" 等价于 "*"
	if !MatchBashPattern("anything", "**") {
		t.Error(`"**" should match anything (equivalent to "*")`)
	}
}

func TestMatchBashPattern_Whitespace(t *testing.T) {
	// 模式含前后空格
	if !MatchBashPattern("git status", "  git *  ") {
		t.Error("whitespace around pattern should be trimmed")
	}
}

// ---------------------------------------------------------------------------
// ParseAllowedBashPatterns — 全分支覆盖
// ---------------------------------------------------------------------------

func TestParseAllowedBashPatterns_BashWithPattern(t *testing.T) {
	raw := []string{"Bash(git *)", "Bash(go test *)"}
	got := ParseAllowedBashPatterns(raw)
	want := []string{"git *", "go test *"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseAllowedBashPatterns = %v, want %v", got, want)
	}
}

func TestParseAllowedBashPatterns_BashWithoutPattern(t *testing.T) {
	// "Bash" 无括号 → 全放行
	got := ParseAllowedBashPatterns([]string{"Bash"})
	want := []string{"*"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseAllowedBashPatterns = %v, want %v", got, want)
	}
}

func TestParseAllowedBashPatterns_Mixed(t *testing.T) {
	raw := []string{"Bash(git *)", "read_file", "Bash", "grep"}
	got := ParseAllowedBashPatterns(raw)
	want := []string{"git *", "*"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseAllowedBashPatterns = %v, want %v", got, want)
	}
}

func TestParseAllowedBashPatterns_Empty(t *testing.T) {
	got := ParseAllowedBashPatterns(nil)
	if len(got) != 0 {
		t.Errorf("ParseAllowedBashPatterns(nil) = %v, want empty", got)
	}

	got = ParseAllowedBashPatterns([]string{})
	if len(got) != 0 {
		t.Errorf("ParseAllowedBashPatterns([]) = %v, want empty", got)
	}
}

func TestParseAllowedBashPatterns_BashEmptyPattern(t *testing.T) {
	// "Bash()" 空括号 → 跳过
	got := ParseAllowedBashPatterns([]string{"Bash()", "Bash(git *)"})
	want := []string{"git *"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseAllowedBashPatterns = %v, want %v", got, want)
	}
}

func TestParseAllowedBashPatterns_WhitespaceInPattern(t *testing.T) {
	got := ParseAllowedBashPatterns([]string{"  Bash(  git *  )  "})
	want := []string{"git *"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseAllowedBashPatterns = %v, want %v", got, want)
	}
}
