package permission

import (
	"encoding/json"
	"sync"
	"testing"
)

func TestRuleEngine_DenyPriority(t *testing.T) {
	re := NewRuleEngine()
	re.LoadRules([]RuleEntry{
		{Rule: Rule{Behavior: RuleDeny, ToolName: "bash"}, Source: SourceConfig, Scope: ScopeConfig},
		{Rule: Rule{Behavior: RuleAllow, ToolName: "bash"}, Source: SourceConfig, Scope: ScopeConfig},
	})

	result, found := re.CheckDeny("bash", nil)
	if !found {
		t.Error("CheckDeny should find deny rule")
	}
	if result.Decision != DecisionDeny {
		t.Errorf("CheckDeny decision = %s, want %s", result.Decision, DecisionDeny)
	}
}

func TestRuleEngine_ToolLevelMatch(t *testing.T) {
	re := NewRuleEngine()
	re.LoadRules([]RuleEntry{
		{Rule: Rule{Behavior: RuleAllow, ToolName: "read_file"}, Source: SourceConfig, Scope: ScopeConfig},
		{Rule: Rule{Behavior: RuleDeny, ToolName: "write_file"}, Source: SourceConfig, Scope: ScopeConfig},
	})

	// allow read_file
	result, found := re.CheckAllow("read_file", nil)
	if !found {
		t.Error("CheckAllow should find read_file allow rule")
	}
	if result.Decision != DecisionAllow {
		t.Errorf("CheckAllow read_file = %s, want %s", result.Decision, DecisionAllow)
	}

	// deny write_file
	result, found = re.CheckDeny("write_file", nil)
	if !found {
		t.Error("CheckDeny should find write_file deny rule")
	}
	if result.Decision != DecisionDeny {
		t.Errorf("CheckDeny write_file = %s, want %s", result.Decision, DecisionDeny)
	}
}

func TestRuleEngine_ContentLevelMatch(t *testing.T) {
	re := NewRuleEngine()
	re.LoadRules([]RuleEntry{
		{Rule: Rule{Behavior: RuleAllow, ToolName: "bash", Pattern: "git *"}, Source: SourceConfig, Scope: ScopeConfig},
		{Rule: Rule{Behavior: RuleDeny, ToolName: "bash", Pattern: "rm *"}, Source: SourceConfig, Scope: ScopeConfig},
	})

	// allow git commands
	input := json.RawMessage(`{"command": "git status"}`)
	result, found := re.CheckAllow("bash", input)
	if !found {
		t.Error("CheckAllow should find 'git *' allow rule")
	}
	if result.Decision != DecisionAllow {
		t.Errorf("CheckAllow git status = %s, want %s", result.Decision, DecisionAllow)
	}

	// deny rm commands
	input = json.RawMessage(`{"command": "rm file.txt"}`)
	result, found = re.CheckDeny("bash", input)
	if !found {
		t.Error("CheckDeny should find 'rm *' deny rule")
	}
	if result.Decision != DecisionDeny {
		t.Errorf("CheckDeny rm file.txt = %s, want %s", result.Decision, DecisionDeny)
	}

	// non-matching command should not find content-level rule
	input = json.RawMessage(`{"command": "ls -la"}`)
	_, found = re.CheckAllow("bash", input)
	if found {
		t.Error("CheckAllow should not match 'ls -la' against 'git *' rule")
	}
}

func TestRuleEngine_FilePathPattern(t *testing.T) {
	re := NewRuleEngine()
	re.LoadRules([]RuleEntry{
		{Rule: Rule{Behavior: RuleAllow, ToolName: "read_file", Pattern: "src/*"}, Source: SourceConfig, Scope: ScopeConfig},
	})

	// match src/ files
	input := json.RawMessage(`{"file_path": "src/main.go"}`)
	result, found := re.CheckAllow("read_file", input)
	if !found {
		t.Error("CheckAllow should match 'src/main.go' against 'src/*' pattern")
	}
	if result.Decision != DecisionAllow {
		t.Errorf("result = %s, want %s", result.Decision, DecisionAllow)
	}

	// non-matching path
	input = json.RawMessage(`{"file_path": "pkg/util.go"}`)
	_, found = re.CheckAllow("read_file", input)
	if found {
		t.Error("CheckAllow should not match 'pkg/util.go' against 'src/*' pattern")
	}
}

func TestRuleEngine_NoMatch(t *testing.T) {
	re := NewRuleEngine()
	re.LoadRules([]RuleEntry{
		{Rule: Rule{Behavior: RuleAllow, ToolName: "read_file"}, Source: SourceConfig, Scope: ScopeConfig},
	})

	// read_file has allow rule, write_file doesn't
	_, found := re.CheckAllow("write_file", nil)
	if found {
		t.Error("CheckAllow should not find rule for write_file")
	}
}

func TestRuleEngine_AddRule(t *testing.T) {
	re := NewRuleEngine()

	// Initially empty
	_, found := re.CheckAllow("read_file", nil)
	if found {
		t.Error("empty engine should not find rules")
	}

	// Add a rule
	re.AddRule(RuleEntry{
		Rule:   Rule{Behavior: RuleAllow, ToolName: "read_file"},
		Source: SourceConfig,
		Scope:  ScopeConfig,
	})

	result, found := re.CheckAllow("read_file", nil)
	if !found {
		t.Error("after AddRule, should find rule")
	}
	if result.Decision != DecisionAllow {
		t.Errorf("result = %s, want %s", result.Decision, DecisionAllow)
	}
}

func TestRuleEngine_RemoveRule(t *testing.T) {
	re := NewRuleEngine()
	rule := Rule{Behavior: RuleAllow, ToolName: "read_file"}
	re.AddRule(RuleEntry{Rule: rule, Source: SourceConfig, Scope: ScopeConfig})

	re.RemoveRule(rule, ScopeConfig)

	_, found := re.CheckAllow("read_file", nil)
	if found {
		t.Error("after RemoveRule, should not find rule")
	}
}

func TestRuleEngine_ToolLevelBeforeContentLevel(t *testing.T) {
	re := NewRuleEngine()
	re.LoadRules([]RuleEntry{
		// 内容级规则在前
		{Rule: Rule{Behavior: RuleAllow, ToolName: "bash", Pattern: "git *"}, Source: SourceConfig, Scope: ScopeConfig},
		// 工具级规则在后
		{Rule: Rule{Behavior: RuleAllow, ToolName: "bash"}, Source: SourceConfig, Scope: ScopeConfig},
	})

	// 工具级规则应优先匹配（即使内容级在数组前面）
	result, found := re.CheckAllow("bash", json.RawMessage(`{"command": "make"}`))
	if !found {
		t.Error("should find tool-level rule")
	}
	// 工具级匹配时 Rule 字段应为 "bash"（不含 pattern）
	if result.Rule != "bash" {
		t.Errorf("tool-level match: Rule = %q, want %q", result.Rule, "bash")
	}
}

func TestRuleEngine_AllRules(t *testing.T) {
	re := NewRuleEngine()
	re.LoadRules([]RuleEntry{
		{Rule: Rule{Behavior: RuleDeny, ToolName: "bash", Pattern: "rm *"}, Source: SourceConfig, Scope: ScopeConfig},
		{Rule: Rule{Behavior: RuleAsk, ToolName: "write_file"}, Source: SourceConfig, Scope: ScopeConfig},
		{Rule: Rule{Behavior: RuleAllow, ToolName: "read_file"}, Source: SourceConfig, Scope: ScopeConfig},
	})

	all := re.AllRules()
	if len(all) != 3 {
		t.Errorf("AllRules() returned %d items, want 3", len(all))
	}
}

func TestRuleEngine_ConcurrentAddAndCheck(t *testing.T) {
	re := NewRuleEngine()
	var wg sync.WaitGroup

	// 并发 AddRule
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := string(rune('a' + i%26))
			re.AddRule(RuleEntry{
				Rule:   Rule{Behavior: RuleAllow, ToolName: name},
				Source: SourceCLI,
				Scope:  ScopeSession,
			})
		}(i)
	}

	// 并发 CheckAllow
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := string(rune('a' + i%26))
			re.CheckAllow(name, nil)
		}(i)
	}

	wg.Wait()
	// 只验证不 panic
}

func TestMatchContent_ShellPrefixExactMatch(t *testing.T) {
	// "make build *" 应匹配精确命令 "make build"（无参数）
	result := matchContent("bash", "make build *", json.RawMessage(`{"command": "make build"}`))
	if !result {
		t.Error(`"make build *" should match exact command "make build"`)
	}

	// "make build *" 应匹配带参数的命令 "make build all"
	result = matchContent("bash", "make build *", json.RawMessage(`{"command": "make build all"}`))
	if !result {
		t.Error(`"make build *" should match "make build all"`)
	}

	// "make build *" 不应匹配 "make buildx"
	result = matchContent("bash", "make build *", json.RawMessage(`{"command": "make buildx"}`))
	if result {
		t.Error(`"make build *" should NOT match "make buildx"`)
	}

	// "git *" 应匹配精确命令 "git"（无参数）
	result = matchContent("bash", "git *", json.RawMessage(`{"command": "git"}`))
	if !result {
		t.Error(`"git *" should match exact command "git"`)
	}
}

func TestMatchContent_EmptyInput(t *testing.T) {
	// 空输入不应匹配任何 pattern
	result := matchContent("bash", "git *", json.RawMessage(`{}`))
	if result {
		t.Error("empty input should not match")
	}
}

func TestMatchContent_InvalidJSON(t *testing.T) {
	result := matchContent("bash", "git *", json.RawMessage(`invalid`))
	if result {
		t.Error("invalid JSON should not match")
	}
}

// ---------------------------------------------------------------------------
// CheckAsk — 直接单元测试（补充此前仅在 guard 集成测试中间接覆盖的缺口）
// ---------------------------------------------------------------------------

func TestRuleEngine_CheckAsk_ToolLevel(t *testing.T) {
	re := NewRuleEngine()
	re.LoadRules([]RuleEntry{
		{Rule: Rule{Behavior: RuleAsk, ToolName: "write_file"}, Source: SourceConfig, Scope: ScopeConfig},
	})

	result, found := re.CheckAsk("write_file", nil)
	if !found {
		t.Error("CheckAsk should find tool-level ask rule")
	}
	if result.Decision != DecisionAsk {
		t.Errorf("CheckAsk decision = %s, want %s", result.Decision, DecisionAsk)
	}
	if result.Reason != ReasonRule {
		t.Errorf("CheckAsk reason = %s, want %s", result.Reason, ReasonRule)
	}
}

func TestRuleEngine_CheckAsk_ContentLevel(t *testing.T) {
	re := NewRuleEngine()
	re.LoadRules([]RuleEntry{
		{Rule: Rule{Behavior: RuleAsk, ToolName: "bash", Pattern: "docker *"}, Source: SourceConfig, Scope: ScopeConfig},
	})

	input := json.RawMessage(`{"command": "docker build ."}`)
	result, found := re.CheckAsk("bash", input)
	if !found {
		t.Error("CheckAsk should find content-level ask rule for docker")
	}
	if result.Decision != DecisionAsk {
		t.Errorf("CheckAsk decision = %s, want %s", result.Decision, DecisionAsk)
	}

	// 不匹配的命令不应命中
	_, found = re.CheckAsk("bash", json.RawMessage(`{"command": "git status"}`))
	if found {
		t.Error("CheckAsk should not match 'git status' against 'docker *' rule")
	}
}

func TestRuleEngine_CheckAsk_NoMatch(t *testing.T) {
	re := NewRuleEngine()
	re.LoadRules([]RuleEntry{
		{Rule: Rule{Behavior: RuleAllow, ToolName: "read_file"}, Source: SourceConfig, Scope: ScopeConfig},
	})

	_, found := re.CheckAsk("read_file", nil)
	if found {
		t.Error("CheckAsk should not find ask rule when only allow rules exist")
	}
}

func TestRuleEngine_CheckAsk_FilePathPattern(t *testing.T) {
	re := NewRuleEngine()
	re.LoadRules([]RuleEntry{
		{Rule: Rule{Behavior: RuleAsk, ToolName: "write_file", Pattern: "*.md"}, Source: SourceConfig, Scope: ScopeConfig},
	})

	input := json.RawMessage(`{"file_path": "README.md"}`)
	result, found := re.CheckAsk("write_file", input)
	if !found {
		t.Error("CheckAsk should match 'README.md' against '*.md' pattern")
	}
	if result.Decision != DecisionAsk {
		t.Errorf("CheckAsk decision = %s, want %s", result.Decision, DecisionAsk)
	}
}

// ---------------------------------------------------------------------------
// REGRESSION: matchContent — web_fetch 和 working_dir 路径解析
// ---------------------------------------------------------------------------

func TestMatchContent_WebFetch(t *testing.T) {
	// web_fetch URL pattern 匹配
	result := matchContent("web_fetch", "https://example.com/*", json.RawMessage(`{"url": "https://example.com/page"}`))
	if !result {
		t.Error("web_fetch URL pattern should match")
	}

	// 不匹配
	result = matchContent("web_fetch", "https://example.com/*", json.RawMessage(`{"url": "https://other.com/page"}`))
	if result {
		t.Error("web_fetch URL pattern should NOT match different domain")
	}

	// 空 URL
	result = matchContent("web_fetch", "https://*", json.RawMessage(`{"url": ""}`))
	if result {
		t.Error("empty URL should not match")
	}
}

func TestMatchContent_WorkingDirPathResolution(t *testing.T) {
	// 通过 working_dir 将相对路径解析为绝对路径后匹配
	result := matchContent("read_file", "/tmp/test/*.go", json.RawMessage(`{"file_path": "test/main.go", "working_dir": "/tmp"}`))
	if !result {
		t.Error("relative path with working_dir should resolve and match absolute pattern")
	}
}

func TestMatchContent_PathFieldFallback(t *testing.T) {
	// 使用 "path" 字段（非 file_path）
	result := matchContent("ls", "src/*", json.RawMessage(`{"path": "src/main.go"}`))
	if !result {
		t.Error("'path' field should be used when 'file_path' is empty")
	}
}

func TestMatchContent_RemoveRuleFrom_OtherScope(t *testing.T) {
	re := NewRuleEngine()
	rule := Rule{Behavior: RuleAllow, ToolName: "read_file"}
	// session scope rule
	re.AddRule(RuleEntry{Rule: rule, Source: SourceSession, Scope: ScopeSession})
	// 移除 config scope 同名规则 — 不同 scope 不应被移除
	re.RemoveRule(rule, ScopeConfig)
	all := re.AllRules()
	if len(all) != 1 {
		t.Errorf("removing rule with different scope should not affect other: got %d rules, want 1", len(all))
	}
}

// ---------------------------------------------------------------------------
// ** — 递归 glob 匹配
// ---------------------------------------------------------------------------

func TestMatchGlob_DoubleStar_Recursive(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		target  string
		want    bool
	}{
		// ** 在末尾：匹配目录下所有文件和子目录
		{"dir all files", "src/**", "src/main.go", true},
		{"dir nested file", "src/**", "src/sub/pkg/util.go", true},
		{"dir itself", "src/**", "src", true},
		{"dir with trailing slash", "src/**", "src/", true},
		{"wrong dir", "src/**", "pkg/main.go", false},
		{"parent dir no match", "src/**", "srcraft/main.go", false},

		// ** 在开头：匹配任意深度的文件
		{"any depth file", "**/main.go", "main.go", true},
		{"one level deep", "**/main.go", "cmd/main.go", true},
		{"deep nested", "**/main.go", "a/b/c/main.go", true},
		{"wrong filename", "**/main.go", "a/b/c/notmain.go", false},

		// ** 在中间：匹配任意中间路径
		{"mid double star", "src/**/test", "src/test", true},
		{"mid with one level", "src/**/test", "src/pkg/test", true},
		{"mid with deep path", "src/**/test", "src/a/b/c/test", true},
		{"mid no match", "src/**/test", "src/pkg/notest", false},

		// ** 匹配零个组件
		{"zero component match", "src/**/file.go", "src/file.go", true},
		{"zero component mid", "a/**/b/**/c", "a/b/c", true},

		// ** 与普通 glob 组合
		{"glob with double star", "src/**/*.go", "src/main.go", true},
		{"glob nested", "src/**/*.go", "src/sub/pkg/util.go", true},
		{"glob no match extension", "src/**/*.go", "src/readme.md", false},

		// 多个 **
		{"multiple double stars", "src/**/test/**/*_test.go", "src/pkg/test/integration/foo_test.go", true},
		{"multiple no match", "src/**/test/**/*_test.go", "src/pkg/bench/foo_bench.go", false},

		// 仅 **
		{"only double star", "**", "anything/at/all.go", true},
		{"only double star empty", "**", "", true},

		// 绝对路径与 **
		{"absolute with double star", "/usr/local/**", "/usr/local/bin/foo", true},
		{"absolute no match", "/usr/local/**", "/usr/remote/bin/foo", false},

		// 兼容：不含 ** 的 pattern 行为不变
		{"no double star - exact", "src/main.go", "src/main.go", true},
		{"no double star - wildcard", "src/*.go", "src/main.go", true},
		{"no double star - single char", "src/?.go", "src/a.go", true},
		{"no double star - char class", "src/[a-z].go", "src/x.go", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := matchGlob(tt.pattern, tt.target)
			if err != nil {
				t.Fatalf("matchGlob(%q, %q) error: %v", tt.pattern, tt.target, err)
			}
			if got != tt.want {
				t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.target, got, tt.want)
			}
		})
	}
}

func TestRuleEngine_DoubleStar_PathRule(t *testing.T) {
	re := NewRuleEngine()
	re.LoadRules([]RuleEntry{
		{Rule: Rule{Behavior: RuleAllow, ToolName: "write_file", Pattern: "src/**"}, Source: SourceConfig, Scope: ScopeConfig},
	})

	// 直接子文件
	input := json.RawMessage(`{"file_path": "src/main.go"}`)
	result, found := re.CheckAllow("write_file", input)
	if !found {
		t.Error("'src/**' should match 'src/main.go'")
	}
	if result.Decision != DecisionAllow {
		t.Errorf("decision = %s, want %s", result.Decision, DecisionAllow)
	}

	// 深层嵌套
	input = json.RawMessage(`{"file_path": "src/a/b/c/deep.go"}`)
	_, found = re.CheckAllow("write_file", input)
	if !found {
		t.Error("'src/**' should match 'src/a/b/c/deep.go'")
	}

	// 不匹配的目录
	input = json.RawMessage(`{"file_path": "pkg/main.go"}`)
	_, found = re.CheckAllow("write_file", input)
	if found {
		t.Error("'src/**' should NOT match 'pkg/main.go'")
	}
}

func TestRuleEngine_DoubleStar_EditFilePathRule(t *testing.T) {
	re := NewRuleEngine()
	re.LoadRules([]RuleEntry{
		{Rule: Rule{Behavior: RuleAllow, ToolName: "edit_file", Pattern: "/Users/menfre/Workbench/waveloom/**"}, Source: SourceConfig, Scope: ScopeConfig},
	})

	// 匹配项目下的任意文件
	input := json.RawMessage(`{"file_path": "/Users/menfre/Workbench/waveloom/cmd/waveloom/tui.go"}`)
	result, found := re.CheckAllow("edit_file", input)
	if !found {
		t.Error("absolute '**' should match file at any depth")
	}
	if result.Decision != DecisionAllow {
		t.Errorf("decision = %s, want %s", result.Decision, DecisionAllow)
	}

	// 不匹配项目外的文件
	input = json.RawMessage(`{"file_path": "/Users/menfre/OtherProject/main.go"}`)
	_, found = re.CheckAllow("edit_file", input)
	if found {
		t.Error("absolute '**' should NOT match different project")
	}
}
