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
	// 无空格 pattern（如 "gstack*"）：HasPrefix 直接命中
	if !MatchBashPattern("gstack-something", "gstack*") {
		t.Error(`"gstack*" should match "gstack-something" (HasPrefix)`)
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

// REGRESSION: Claude Code 允许在单个 allowed-tools 条目里用逗号声明多条前缀模式
// (如 `Bash(git branch:*, git diff:*)`)。旧实现把括号内整串当作单一 pattern,
// 导致白名单不匹配 → 带动态注入的 skill/command 被 lint 拒绝而加载不出来。
func TestRegression_ParseAllowedBashPatterns_InnerCommaSeparated(t *testing.T) {
	got := ParseAllowedBashPatterns([]string{"Bash(git branch:*, git diff:*)"})
	want := []string{"git branch:*", "git diff:*"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseAllowedBashPatterns = %v, want %v", got, want)
	}

	// 拆出的模式必须能匹配对应命令(review_diff 命令的真实用法)
	for _, cmd := range []string{"git branch --show-current", "git diff --staged"} {
		matched := false
		for _, p := range got {
			if MatchBashPattern(cmd, p) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("command %q not matched by %v", cmd, got)
		}
	}
}

func TestSplitRuleList(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"顶层逗号分隔", "Bash(git log:*), Read", []string{"Bash(git log:*)", "Read"}},
		{"括号内逗号保留", "Bash(git branch:*, git diff:*)", []string{"Bash(git branch:*, git diff:*)"}},
		{"数组方括号保留", "Bash(cmd[0,1])", []string{"Bash(cmd[0,1])"}},
		{"双引号内逗号保留", `Bash(git commit -m "a,b")`, []string{`Bash(git commit -m "a,b")`}},
		{"单引号内逗号保留", `Bash(sed -e 's/a,b/c/')`, []string{`Bash(sed -e 's/a,b/c/')`}},
		{"转义引号不结束引号", `Bash(echo "a\",b")`, []string{`Bash(echo "a\",b")`}},
		{"空项丢弃", " , Read, ,Write ", []string{"Read", "Write"}},
		{"空串", "", nil},
		{"无逗号", "Bash(npm *)", []string{"Bash(npm *)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SplitRuleList(tt.raw); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SplitRuleList(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

// REGRESSION: 引号内逗号不能被当作模式分隔符(如 `Bash(git commit -m "a,b")`),
// 否则会拆出两条死模式,原本合法的白名单在 lint 阶段被拒。
func TestRegression_ParseAllowedBashPatterns_QuotedComma(t *testing.T) {
	raw := []string{`Bash(git commit -m "a,b")`}
	got := ParseAllowedBashPatterns(raw)
	want := []string{`git commit -m "a,b"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseAllowedBashPatterns = %v, want %v", got, want)
	}
	if !MatchBashPattern(`git commit -m "a,b"`, got[0]) {
		t.Errorf("pattern %q should match its own command", got[0])
	}
}

// REGRESSION: 新增的 "xxx:*" 分支不能吞掉以 "*" 开头的既有模式。
func TestRegression_MatchBashPattern_LeadingStarPatternsPreserved(t *testing.T) {
	// "*xxx:*" 保留"包含子串"语义
	if !MatchBashPattern("git log:pretty", "*log:*") {
		t.Error("*log:* should still match by substring")
	}
	// "*:*" 保留老旧语义:命令含冒号即命中
	if !MatchBashPattern("git log:pretty", "*:*") {
		t.Error("*:* should match any command containing ':'")
	}
	if MatchBashPattern("git log --oneline", "*:*") {
		t.Error("*:* should not match a command without ':'")
	}
}

// ":" + "*" 无有效前缀 → 不放行任何命令(避免 fail-open 成 allow-all)。
func TestRegression_MatchBashPattern_EmptyColonPrefixDenies(t *testing.T) {
	if MatchBashPattern("rm -rf /", ":*") {
		t.Error("pattern \":*\" must not allow arbitrary commands")
	}
}

// 多词前缀的字面前缀匹配语义(不误命中相近命令)。
func TestMatchBashPattern_MultiWordColonStarPrefix(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{"git branch --show-current", true},
		{"git branch", true},
		{"git branchx --all", false},
		{"git branchless status", false},
		{"xx git branch -a", false},
	}
	for _, tt := range tests {
		if got := MatchBashPattern(tt.command, "git branch:*"); got != tt.want {
			t.Errorf("MatchBashPattern(%q, %q) = %v, want %v", tt.command, "git branch:*", got, tt.want)
		}
	}
}
