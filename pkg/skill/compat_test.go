package skill

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/permission"
)

// ===========================================================================
// Claude Code frontmatter 兼容性(回归)
// ===========================================================================

// REGRESSION: Claude Code 文档允许 allowed-tools 写成标量字符串
// (`allowed-tools: Bash(openspec:*)`,openspec CLI 生成的文件正是这种写法)。
// 旧实现把它解码进 []string 会触发 YAML 类型错误,parseSKILLmd 随即丢弃整份
// frontmatter —— name/description/when_to_use 全部静默消失,skill 在
// `/` 面板与 Available Skills 表格里只剩一个空描述(表现为"加载不出来")。
func TestRegression_AllowedToolsScalarStringKeepsFrontmatter(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "openspec-apply-change", "SKILL.md"), `---
name: openspec-apply-change
description: Implement tasks from an OpenSpec change.
when_to_use: Use when the user says "openspec apply".
allowed-tools: Bash(openspec:*)
license: MIT
metadata:
  author: openspec
  version: "1.0"
---
body
`)

	l := newTestLoader(home, home)
	infos, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 skill, got %d: %v", len(infos), infos)
	}
	info := infos[0]
	if info.Name != "openspec-apply-change" {
		t.Errorf("name = %q, want openspec-apply-change", info.Name)
	}
	if !strings.Contains(info.Description, "OpenSpec change") {
		t.Errorf("description lost: %q", info.Description)
	}
	if !strings.Contains(info.Description, "openspec apply") {
		t.Errorf("when_to_use lost: %q", info.Description)
	}
}

// REGRESSION: allowed-tools 标量形式必须真正进入 Bash 白名单,
// 否则带动态注入的 skill/command 会在 lint 阶段被拒绝加载。
func TestRegression_AllowedToolsScalarStringFeedsWhitelist(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), `---
name: test
description: scalar whitelist
allowed-tools: Bash(echo a:*, echo b:*)
---
!`+"`echo a done`"+`
`)

	l := newTestLoaderWithGuard(home, home, permission.NewGuard())
	loaded, err := l.Load("test", "")
	if err != nil {
		t.Fatalf("Load should pass lint with scalar allowed-tools, got: %v", err)
	}
	if !strings.Contains(loaded.Body, "done") {
		t.Errorf("injection not executed: %q", loaded.Body)
	}
}

// REGRESSION: 单个字段类型不符(如 description 误写成列表)时,
// 其余字段必须保留 —— 旧实现整体丢弃 frontmatter。
func TestRegression_FrontmatterTypeMismatchKeepsOtherFields(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "partial", "SKILL.md"), `---
name: partial
description:
  - line one
  - line two
when_to_use: Only for partial parsing.
argument-hint: "[scope]"
---
body
`)

	l := newTestLoader(home, home)
	infos, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(infos))
	}
	if infos[0].Name != "partial" {
		t.Errorf("name lost on partial decode: %q", infos[0].Name)
	}
	if !strings.Contains(infos[0].Description, "Only for partial parsing.") {
		t.Errorf("when_to_use lost on partial decode: %q", infos[0].Description)
	}
	if infos[0].Args != "[scope]" {
		t.Errorf("argument-hint lost on partial decode: %q", infos[0].Args)
	}
}

// REGRESSION: YAML 语法本身非法时保持旧行为(整份 frontmatter 弃用),
// 不能让降级解码掩盖真正的语法错误。
func TestRegression_FrontmatterSyntaxErrorStillDropsAll(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "broken", "SKILL.md"), `---
name: [broken
description: never parsed
---
body
`)

	l := newTestLoader(home, home)
	loaded, err := l.Load("broken", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "name: [broken") {
		t.Errorf("malformed frontmatter should be folded into body, got: %q", loaded.Body)
	}
}

// paths 同样支持标量写法(Claude Code 的 splitPathInFrontmatter 语义)。
func TestRegression_PathsScalarFormActivates(t *testing.T) {
	dir := tmpDir(t)
	skillDir := filepath.Join(dir, ".claude", "skills", "react-expert")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), `---
description: React
paths: "**/*.tsx"
---
body
`)

	l := newTestLoader(dir, dir)
	infos, _ := l.List()
	if len(infos) != 0 {
		t.Fatalf("unactivated conditional skill should not be listed, got %v", infos)
	}
	activated := l.ActivateForPaths([]string{"src/App.tsx"})
	if len(activated) != 1 || activated[0] != "react-expert" {
		t.Fatalf("scalar paths not activated: %v", activated)
	}
}

// ===========================================================================
// 嵌套 commands 目录(回归)
// ===========================================================================

// REGRESSION: openspec CLI 把 slash 命令写进 .claude/commands/opsx/*.md,
// 旧 scanCommandsDir 遇到目录直接 continue,6 个 /opsx:* 命令全部加载不出来。
// 现在嵌套目录以 ":" 连接为限定名(对齐 Claude Code)。
func TestRegression_NestedCommandsDirDiscovered(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "commands", "opsx", "apply.md"), `---
name: "OPSX: Apply"
description: "Implement tasks from an OpenSpec change (Experimental)"
allowed-tools: Bash(openspec:*)
---
implement
`)

	l := newTestLoader(home, home)
	infos, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 nested command, got %d: %v", len(infos), infos)
	}
	// 展示名 "OPSX: Apply" 含空格,不可作为标识符 → 回退路径名 opsx:apply
	if infos[0].Name != "opsx:apply" {
		t.Errorf("name = %q, want opsx:apply", infos[0].Name)
	}
	if !strings.Contains(infos[0].Description, "OpenSpec change") {
		t.Errorf("description = %q", infos[0].Description)
	}
}

func TestRegression_NestedCommandsDirLoad(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "commands", "opsx", "apply.md"), `---
description: nested load
---
nested body
`)

	l := newTestLoader(home, home)
	loaded, err := l.Load("opsx:apply", "")
	if err != nil {
		t.Fatalf("Load(opsx:apply) failed: %v", err)
	}
	if !strings.Contains(loaded.Body, "nested body") {
		t.Errorf("body = %q", loaded.Body)
	}
}

func TestRegression_DeeplyNestedCommandsDir(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".waveloom", "commands", "team", "release", "hotfix.md"), `---
description: deep nested
---
deep
`)

	l := newTestLoader(home, home)
	infos, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, info := range infos {
		if info.Name == "team:release:hotfix" {
			found = true
		}
	}
	if !found {
		t.Fatalf("deeply nested command not discovered: %v", infos)
	}
	if _, err := l.Load("team:release:hotfix", ""); err != nil {
		t.Errorf("Load(team:release:hotfix) failed: %v", err)
	}
}

// 隐藏目录不参与扫描(与 scanSupportingFiles 一致)。
func TestRegression_HiddenCommandsDirSkipped(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "commands", ".hidden", "secret.md"), `---
description: hidden
---
hidden
`)

	l := newTestLoader(home, home)
	infos, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		t.Errorf("hidden dir should be skipped, got %v", infos)
	}
}

// REGRESSION: commands 目录本身是以 symlink 安装的
// (如 ~/.claude/commands -> ~/dotfiles/commands)。filepath.WalkDir 的根节点按
// Lstat 处理不会自动跟随,必须先 EvalSymlinks 否则整目录命令全部丢失。
func TestRegression_CommandsRootSymlinkFollowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires admin/dev-mode on Windows")
	}

	home := tmpDir(t)
	realDir := filepath.Join(home, "dotfiles", "commands")
	writeFile(t, filepath.Join(realDir, "deploy.md"), `---
description: symlinked root
---
deploy body
`)
	writeFile(t, filepath.Join(realDir, "opsx", "apply.md"), `---
description: nested under symlinked root
---
apply body
`)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, filepath.Join(home, ".claude", "commands")); err != nil {
		t.Fatal(err)
	}

	l := newTestLoader(home, home)
	infos, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool, len(infos))
	for _, info := range infos {
		names[info.Name] = true
	}
	if !names["deploy"] || !names["opsx:apply"] {
		t.Fatalf("commands under symlinked root not discovered: %v", names)
	}
}

// Load 的名称直接参与路径拼接,必须拒绝分隔符与上跳段。
func TestRegression_LoadRejectsUnsafeName(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, "outside.md"), "outside body")

	l := newTestLoader(home, home)
	for _, name := range []string{"", "../outside", "..", "a/b", `a\b`} {
		if _, err := l.Load(name, ""); err == nil {
			t.Errorf("Load(%q) should fail", name)
		}
	}
}

// ===========================================================================
// 名称辅助函数
// ===========================================================================

func TestCommandNameFromRel(t *testing.T) {
	tests := []struct {
		name string
		rel  string
		want string
	}{
		{"扁平", "apply.md", "apply"},
		{"一层嵌套", filepath.Join("opsx", "apply.md"), "opsx:apply"},
		{"两层嵌套", filepath.Join("team", "release", "hotfix.md"), "team:release:hotfix"},
		{"非 md", "notes.txt", "notes.txt"},
		{"空串", "", ""},
		{"上跳段", filepath.Join("..", "escape.md"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := commandNameFromRel(tt.rel); got != tt.want {
				t.Errorf("commandNameFromRel(%q) = %q, want %q", tt.rel, got, tt.want)
			}
		})
	}
}

func TestCommandRelPath(t *testing.T) {
	tests := []struct {
		name   string
		want   string
		wantOK bool
	}{
		{"opsx:apply", filepath.Join("opsx", "apply"), true},
		{"team:release:hotfix", filepath.Join("team", "release", "hotfix"), true},
		{"apply", "", false},
		{"opsx:", "", false},
		{"opsx:..", "", false},
		{"opsx:.", "", false},
		{":apply", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := commandRelPath(tt.name)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("commandRelPath(%q) = (%q, %v), want (%q, %v)", tt.name, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestIsSafeSkillName(t *testing.T) {
	safe := []string{"deploy", "opsx:apply", "team:release:hotfix", "openspec-apply-change"}
	for _, name := range safe {
		if !isSafeSkillName(name) {
			t.Errorf("isSafeSkillName(%q) = false, want true", name)
		}
	}
	unsafe := []string{"", ".", "..", "a/b", `a\b`, "a/../b", "opsx:", ":apply", "a::b"}
	for _, name := range unsafe {
		if isSafeSkillName(name) {
			t.Errorf("isSafeSkillName(%q) = true, want false", name)
		}
	}
}

func TestIsValidSkillName(t *testing.T) {
	valid := []string{"deploy", "opsx:apply", "openspec-apply-change"}
	for _, name := range valid {
		if !isValidSkillName(name) {
			t.Errorf("isValidSkillName(%q) = false, want true", name)
		}
	}
	invalid := []string{"", "OPSX: Apply", "two words", "a/b", `a\b`, "tab\tname"}
	for _, name := range invalid {
		if isValidSkillName(name) {
			t.Errorf("isValidSkillName(%q) = true, want false", name)
		}
	}
}
