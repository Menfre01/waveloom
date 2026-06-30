package skill

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/permission"
)

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

func tmpDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "waveloom-skill-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newTestLoader(homeDir, cwd string) *Loader {
	return NewLoader(cwd, homeDir, "test-session-123", "medium", nil)
}

// ---------------------------------------------------------------------------
// Frontmatter 解析测试
// ---------------------------------------------------------------------------

func TestParseFrontmatter_Basic(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "deploy", "SKILL.md"), `---
name: deploy
description: Deploy to production
---

# Deploy

Steps here.
`)
	l := newTestLoader(home, home)
	infos, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(infos))
	}
	info := infos[0]
	if info.Name != "deploy" {
		t.Errorf("name = %q, want %q", info.Name, "deploy")
	}
	if info.Description != "Deploy to production" {
		t.Errorf("description = %q, want %q", info.Description, "Deploy to production")
	}
	if !info.UserInvocable {
		t.Error("expected user-invocable by default")
	}
	if !info.ModelInvocable {
		t.Error("expected model-invocable by default")
	}
}

func TestParseFrontmatter_NoFrontmatter(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), `# Just a skill

No frontmatter here.
`)
	l := newTestLoader(home, home)
	_, err := l.Load("test", "")
	if err != nil {
		t.Fatal(err)
	}

	infos, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(infos))
	}
}

func TestParseFrontmatter_MalformedYAML(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), `---
name: [broken
---
body
`)
	l := newTestLoader(home, home)
	// 畸形 YAML 不应 panic，整个文件作为 body
	loaded, err := l.Load("test", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "body") {
		t.Error("expected body content")
	}
}

func TestParseFrontmatter_DisableModelInvocation(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "deploy", "SKILL.md"), `---
name: deploy
description: Deploy
disable-model-invocation: true
---
body
`)
	l := newTestLoader(home, home)
	infos, _ := l.List()
	if len(infos) != 1 {
		t.Fatal("expected 1 skill")
	}
	if infos[0].ModelInvocable {
		t.Error("expected model-invocable = false")
	}
}

func TestParseFrontmatter_UserInvocable(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "hidden", "SKILL.md"), `---
name: hidden
user-invocable: false
---
body
`)
	l := newTestLoader(home, home)
	infos, _ := l.List()
	if len(infos) != 1 {
		t.Fatal("expected 1 skill")
	}
	if infos[0].UserInvocable {
		t.Error("expected user-invocable = false")
	}
}

func TestParseFrontmatter_Arguments_String(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "migrate", "SKILL.md"), `---
name: migrate
arguments: "source target"
---
Migrate $source to $target
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("migrate", "React Vue")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "Migrate React to Vue") {
		t.Errorf("expected named argument substitution, got: %s", loaded.Body)
	}
}

func TestParseFrontmatter_Arguments_List(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "migrate", "SKILL.md"), `---
name: migrate
arguments: [source, target]
---
Migrate $source to $target
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("migrate", "React Vue")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "Migrate React to Vue") {
		t.Errorf("expected named argument substitution, got: %s", loaded.Body)
	}
}

func TestParseFrontmatter_WhenToUse(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "deploy", "SKILL.md"), `---
description: Deploy
when_to_use: When user wants to deploy
---
body
`)
	l := newTestLoader(home, home)
	infos, _ := l.List()
	if len(infos) != 1 {
		t.Fatal("expected 1 skill")
	}
	if !strings.Contains(infos[0].Description, "When user wants to deploy") {
		t.Errorf("description should include when_to_use, got: %s", infos[0].Description)
	}
}

func TestParseFrontmatter_AllFields(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "full-skill", "SKILL.md"), `---
name: full-skill
description: Full featured skill
when_to_use: On deploy
argument-hint: "[env]"
arguments: [env, branch]
disable-model-invocation: true
user-invocable: true
---
body
`)
	l := newTestLoader(home, home)
	infos, _ := l.List()
	if len(infos) != 1 {
		t.Fatal("expected 1 skill")
	}
	info := infos[0]
	if info.Name != "full-skill" {
		t.Errorf("name = %q", info.Name)
	}
	if info.Args != "[env]" {
		t.Errorf("args = %q", info.Args)
	}
	if !strings.Contains(info.Description, "On deploy") {
		t.Error("missing when_to_use in description")
	}
}

func TestParseFrontmatter_EmptyFrontmatter(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "empty", "SKILL.md"), "---\n---\nbody\n")
	l := newTestLoader(home, home)
	loaded, err := l.Load("empty", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "body") {
		t.Errorf("expected body content, got: %s", loaded.Body)
	}
	if strings.Contains(loaded.Body, "---") {
		t.Error("frontmatter delimiters should be stripped")
	}
}

func TestParseFrontmatter_UnclosedFrontmatter(t *testing.T) {
	home := tmpDir(t)
	// 以 --- 开头但没有闭合 --- 的前言 → 整个文件作为 body
	writeFile(t, filepath.Join(home, ".claude", "skills", "unclosed", "SKILL.md"), "---\nkey: value\nbody here\n")
	l := newTestLoader(home, home)
	loaded, err := l.Load("unclosed", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "body here") {
		t.Errorf("expected body content, got: %s", loaded.Body)
	}
}

// ---------------------------------------------------------------------------
// 发现测试
// ---------------------------------------------------------------------------

func TestDiscover_PersonalClaude(t *testing.T) {
	home := tmpDir(t)
	cwd := tmpDir(t)

	writeFile(t, filepath.Join(home, ".claude", "skills", "myskill", "SKILL.md"), `---
description: My skill
---
body
`)
	l := newTestLoader(home, cwd)
	infos, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Name != "myskill" {
		t.Errorf("expected myskill, got %v", infos)
	}
}

func TestDiscover_PersonalWaveloom(t *testing.T) {
	home := tmpDir(t)
	cwd := tmpDir(t)

	writeFile(t, filepath.Join(home, ".waveloom", "skills", "myskill", "SKILL.md"), `---
description: My skill
---
body
`)
	l := newTestLoader(home, cwd)
	infos, _ := l.List()
	if len(infos) != 1 || infos[0].Name != "myskill" {
		t.Errorf("expected myskill, got %v", infos)
	}
}

func TestDiscover_ProjectClaude(t *testing.T) {
	home := tmpDir(t)
	project := tmpDir(t)
	// 让 project 成为 git 仓库
	writeFile(t, filepath.Join(project, ".git"), "")
	cwd := filepath.Join(project, "subdir")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(project, ".claude", "skills", "project-skill", "SKILL.md"), `---
description: Project skill
---
body
`)
	l := newTestLoader(home, cwd)
	infos, _ := l.List()
	found := false
	for _, info := range infos {
		if info.Name == "project-skill" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("project skill not found in %v", infos)
	}
}

func TestDiscover_ProjectWaveloom(t *testing.T) {
	home := tmpDir(t)
	project := tmpDir(t)
	writeFile(t, filepath.Join(project, ".git"), "")
	cwd := filepath.Join(project, "subdir")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(project, ".waveloom", "skills", "projskill", "SKILL.md"), `---
description: Project waveloom skill
---
body
`)
	l := newTestLoader(home, cwd)
	infos, _ := l.List()
	found := false
	for _, info := range infos {
		if info.Name == "projskill" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("project skill not found")
	}
}

func TestDiscover_DuplicateName(t *testing.T) {
	home := tmpDir(t)
	cwd := tmpDir(t)

	// .claude 优先于 .waveloom
	writeFile(t, filepath.Join(home, ".claude", "skills", "dup", "SKILL.md"), `---
description: Claude version
---
body
`)
	writeFile(t, filepath.Join(home, ".waveloom", "skills", "dup", "SKILL.md"), `---
description: Waveloom version
---
body
`)
	l := newTestLoader(home, cwd)
	infos, _ := l.List()

	count := 0
	for _, info := range infos {
		if info.Name == "dup" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 dup skill, got %d", count)
	}
}

func TestDiscover_NoSkillsDir(t *testing.T) {
	home := tmpDir(t)
	cwd := tmpDir(t)
	l := newTestLoader(home, cwd)
	infos, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		t.Errorf("expected 0 skills, got %d", len(infos))
	}
}

func TestDiscover_EmptySkillsDir(t *testing.T) {
	home := tmpDir(t)
	cwd := tmpDir(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	l := newTestLoader(home, cwd)
	infos, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		t.Errorf("expected 0 skills, got %d", len(infos))
	}
}

func TestDiscover_FlatCommands(t *testing.T) {
	home := tmpDir(t)
	cwd := tmpDir(t)

	writeFile(t, filepath.Join(home, ".claude", "commands", "deploy.md"), `---
description: Deploy command
---
deploy steps
`)
	l := newTestLoader(home, cwd)
	infos, _ := l.List()
	found := false
	for _, info := range infos {
		if info.Name == "deploy" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("flat command deploy.md not found in %v", infos)
	}
}

func TestDiscover_SkillOverridesCommand(t *testing.T) {
	home := tmpDir(t)
	cwd := tmpDir(t)

	// skill 目录形式
	writeFile(t, filepath.Join(home, ".claude", "skills", "deploy", "SKILL.md"), `---
description: Skill deploy
---
skill body
`)
	// command 扁平文件（同优先级目录，但 skill 优先）
	writeFile(t, filepath.Join(home, ".claude", "commands", "deploy.md"), `---
description: Command deploy
---
cmd body
`)

	l := newTestLoader(home, cwd)
	infos, _ := l.List()

	// 找到 deploy，确认只有 1 个且为 skill 版本
	count := 0
	var foundInfo SkillInfo
	for _, info := range infos {
		if info.Name == "deploy" {
			count++
			foundInfo = info
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 deploy, got %d", count)
	}
	if !strings.Contains(foundInfo.Description, "Skill deploy") {
		t.Errorf("expected skill version to win, got description: %s", foundInfo.Description)
	}
}

func TestDiscover_PersonalCommandWaveloom(t *testing.T) {
	home := tmpDir(t)
	cwd := tmpDir(t)

	writeFile(t, filepath.Join(home, ".waveloom", "commands", "release.md"), `---
description: Release command
---
release steps
`)
	l := newTestLoader(home, cwd)
	infos, _ := l.List()
	found := false
	for _, info := range infos {
		if info.Name == "release" {
			found = true
			break
		}
	}
	if !found {
		t.Error("release command not found")
	}
}

// ---------------------------------------------------------------------------
// 变量替换测试
// ---------------------------------------------------------------------------

func TestLoad_RendersVariables(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), `---
name: test
---
Fix issue $ARGUMENTS. First arg: $0. Second: $1.
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("test", "123 bug")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "Fix issue 123 bug") {
		t.Errorf("ARGUMENTS not replaced: %s", loaded.Body)
	}
	if !strings.Contains(loaded.Body, "First arg: 123") {
		t.Errorf("$0 not replaced: %s", loaded.Body)
	}
	if !strings.Contains(loaded.Body, "Second: bug") {
		t.Errorf("$1 not replaced: %s", loaded.Body)
	}
}

func TestLoad_IndexedArguments(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), `---
name: test
---
First: $ARGUMENTS[0], Second: $ARGUMENTS[1]
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("test", "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "First: hello") {
		t.Errorf("ARGUMENTS[0] not replaced: %s", loaded.Body)
	}
	if !strings.Contains(loaded.Body, "Second: world") {
		t.Errorf("ARGUMENTS[1] not replaced: %s", loaded.Body)
	}
}

func TestLoad_NamedArguments(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "migrate", "SKILL.md"), `---
name: migrate
arguments: [source, target]
---
Migrate $source to $target
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("migrate", "React Vue")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "Migrate React to Vue") {
		t.Errorf("named args not replaced: %s", loaded.Body)
	}
}

func TestLoad_PartialNamedArguments(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "greet", "SKILL.md"), `---
name: greet
arguments: [who, mood]
---
你是 $who，心情 $mood。
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("greet", "menfre")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "你是 menfre，心情 。") {
		t.Errorf("partial named args not handled correctly: %s", loaded.Body)
	}
	if strings.Contains(loaded.Body, "$mood") {
		t.Errorf("unbound $mood should be empty, not preserved: %s", loaded.Body)
	}
}

func TestLoad_SessionIDVariable(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "logger", "SKILL.md"), `---
name: logger
---
Log to ${CLAUDE_SESSION_ID}.log
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("logger", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "test-session-123.log") {
		t.Errorf("session ID not replaced: %s", loaded.Body)
	}
}

func TestLoad_WaveloomSessionIDVariable(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "logger", "SKILL.md"), `---
name: logger
---
Log to ${WAVELOOM_SESSION_ID}.log
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("logger", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "test-session-123.log") {
		t.Errorf("WAVELOOM_SESSION_ID not replaced: %s", loaded.Body)
	}
}

func TestLoad_SkillDirVariable(t *testing.T) {
	home := tmpDir(t)
	skillDir := filepath.Join(home, ".claude", "skills", "script")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), `---
name: script
---
Run ${WAVELOOM_SKILL_DIR}/helper.sh
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("script", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, skillDir+"/helper.sh") {
		t.Errorf("skill dir not replaced: %s", loaded.Body)
	}
}

func TestLoad_ClaudeSkillDirVariable(t *testing.T) {
	home := tmpDir(t)
	skillDir := filepath.Join(home, ".claude", "skills", "script")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), `---
name: script
---
Run ${CLAUDE_SKILL_DIR}/helper.sh
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("script", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, skillDir+"/helper.sh") {
		t.Errorf("CLAUDE_SKILL_DIR not replaced: %s", loaded.Body)
	}
}

func TestLoad_EffortVariable(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), `---
name: test
---
Effort: ${WAVELOOM_EFFORT}
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("test", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "Effort: medium") {
		t.Errorf("effort not replaced: %s", loaded.Body)
	}
}

func TestLoad_ClaudeEffortVariable(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), `---
name: test
---
Effort: ${CLAUDE_EFFORT}
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("test", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "Effort: medium") {
		t.Errorf("CLAUDE_EFFORT not replaced: %s", loaded.Body)
	}
}

func TestLoad_MixedNamespaces(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "mixed", "SKILL.md"), `---
name: mixed
---
Claude: ${CLAUDE_SESSION_ID}, Waveloom: ${WAVELOOM_SESSION_ID}
Dir: ${CLAUDE_SKILL_DIR} = ${WAVELOOM_SKILL_DIR}
Effort: ${CLAUDE_EFFORT} = ${WAVELOOM_EFFORT}
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("mixed", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "test-session-123") {
		t.Error("session IDs should be replaced")
	}
	if !strings.Contains(loaded.Body, "medium") {
		t.Error("effort should be replaced")
	}
}

func TestLoad_ArgsAutoAppend(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "deploy", "SKILL.md"), `---
name: deploy
---
Deploy now.
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("deploy", "production")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "ARGUMENTS: production") {
		t.Errorf("ARGUMENTS not auto-appended: %s", loaded.Body)
	}
}

func TestLoad_ArgsPresent_NoAutoAppend(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "deploy", "SKILL.md"), `---
name: deploy
---
Deploy $ARGUMENTS now.
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("deploy", "production")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(loaded.Body, "ARGUMENTS:") {
		t.Error("should not auto-append ARGUMENTS when $ARGUMENTS is present in body")
	}
	if !strings.Contains(loaded.Body, "Deploy production now") {
		t.Errorf("$ARGUMENTS should be replaced: %s", loaded.Body)
	}
}

func TestLoad_EmptyArgs(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "deploy", "SKILL.md"), `---
name: deploy
---
Deploy now.
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("deploy", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(loaded.Body, "ARGUMENTS:") {
		t.Error("should not auto-append ARGUMENTS for empty args")
	}
}

// TestLoad_EmptyArgsWithPlaceholders 验证无参时所有占位符替换为空字符串。
func TestLoad_EmptyArgsWithPlaceholders(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), `---
name: test
arguments: [who, mood]
---
$ARGUMENTS|$0|$1|$ARGUMENTS[0]|$ARGUMENTS[1]|$who|$mood
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("test", "")
	if err != nil {
		t.Fatal(err)
	}
	// 所有占位符均应替换为空，只留下分隔符 "||||||"
	expected := "||||||"
	if loaded.Body != expected {
		t.Errorf("empty args should replace all placeholders with empty string: got %q", loaded.Body)
	}
	if strings.Contains(loaded.Body, "ARGUMENTS:") {
		t.Error("should not auto-append ARGUMENTS for empty args even when placeholders present")
	}
}

// TestRegression_CodeSpanProtected 验证代码片段中的 $ARGUMENTS 不被替换，
// 且不影响代码片段外的正常替换。
func TestRegression_CodeSpanProtected(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), "---\nname: test\n---\n"+
		"Deploy `$ARGUMENTS` to $ARGUMENTS now.\n")
	l := newTestLoader(home, home)
	loaded, err := l.Load("test", "production")
	if err != nil {
		t.Fatal(err)
	}
	// 代码片段内的 $ARGUMENTS 应保持字面
	if !strings.Contains(loaded.Body, "`$ARGUMENTS`") {
		t.Errorf("$ARGUMENTS in code span should remain literal: %s", loaded.Body)
	}
	// 代码片段外的 $ARGUMENTS 应被替换
	if !strings.Contains(loaded.Body, "to production now") {
		t.Errorf("$ARGUMENTS outside code span should be replaced: %s", loaded.Body)
	}
	// $ARGUMENTS 在代码片段外已出现，不应自动追加 ARGUMENTS 行
	if strings.Contains(loaded.Body, "ARGUMENTS:") {
		t.Error("should not auto-append when $ARGUMENTS is present outside code spans")
	}
}

// TestRegression_CodeSpanAutoAppend 验证仅代码片段含 $ARGUMENTS 字面时
// 仍触发自动追加（因为没有真正的 $ARGUMENTS 变量消费参数）。
func TestRegression_CodeSpanAutoAppend(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), "---\nname: test\n---\n"+
		"这个 body 中不包含 `$ARGUMENTS` 变量。\n")
	l := newTestLoader(home, home)
	loaded, err := l.Load("test", "deploy to staging")
	if err != nil {
		t.Fatal(err)
	}
	// 代码片段内的 $ARGUMENTS 应保持字面
	if !strings.Contains(loaded.Body, "`$ARGUMENTS`") {
		t.Errorf("$ARGUMENTS in code span should remain literal: %s", loaded.Body)
	}
	// 应自动追加 ARGUMENTS 行（因为 body 中无真正的 $ARGUMENTS 变量）
	if !strings.Contains(loaded.Body, "ARGUMENTS: deploy to staging") {
		t.Errorf("should auto-append ARGUMENTS when $ARGUMENTS only in code span: %s", loaded.Body)
	}
}

func TestLoad_IndexOutOfRange(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), `---
name: test
---
First: $0, Second: $1, Third: $2
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("test", "onlyone")
	if err != nil {
		t.Fatal(err)
	}
	expected := "First: onlyone, Second: , Third: "
	if loaded.Body != expected {
		t.Errorf("out-of-range index args should be empty string: got %q, want %q", loaded.Body, expected)
	}
}

// ---------------------------------------------------------------------------
// 动态注入测试
// ---------------------------------------------------------------------------

func TestLoad_DynamicInjection(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), `---
name: test
---
!` + "`echo hello world`" + `
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("test", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "hello world") {
		t.Errorf("dynamic injection not executed: %s", loaded.Body)
	}
	if strings.Contains(loaded.Body, "`echo") {
		t.Error("command placeholder should be replaced")
	}
}

func TestLoad_MultilineInjection(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), "---\nname: test\n---\n```!\necho hello\necho world\n```\n")
	l := newTestLoader(home, home)
	loaded, err := l.Load("test", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "hello\nworld") {
		t.Errorf("multiline injection not executed: %s", loaded.Body)
	}
}

func TestLoad_DynamicInjectionError(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), `---
name: test
---
!` + "`false`" + `
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("test", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "[command exited with code") {
		t.Errorf("expected error marker in body: %s", loaded.Body)
	}
}

func TestLoad_DynamicInjectionNotAtLineStart(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), `---
name: test
---
Some text!` + "`echo bad`" + ` should not execute
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("test", "")
	if err != nil {
		t.Fatal(err)
	}
	// "text!`cmd`" 中 ! 前是 't'，不匹配 → 不执行
	if !strings.Contains(loaded.Body, "!`echo bad`") {
		t.Error("non-whitespace-prefixed !`...` should be preserved as literal text")
	}
	// 确认没有被执行
	if strings.Contains(loaded.Body, "Some textbad") {
		t.Error("should not have executed the command")
	}
}

// TestRegression_InlineInjectionAfterText 对标 Claude Code：
// 行中 ! 前为空白字符时，触发动态注入。
// 例如 "当前时间: !`date '+%H:%M:%S'`" 中 ! 前是空格 → 应执行。
func TestRegression_InlineInjectionAfterText(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), `---
name: test
---
当前时间: !` + "`echo 2025-01-01`" + `
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("test", "")
	if err != nil {
		t.Fatal(err)
	}
	// 行中空白前缀的 !`cmd` 应被执行
	if !strings.Contains(loaded.Body, "当前时间: 2025-01-01") {
		t.Errorf("whitespace-prefixed inline injection should be executed, got: %s", loaded.Body)
	}
	// 不应残留命令原文
	if strings.Contains(loaded.Body, "!`") {
		t.Error("command placeholder should be replaced")
	}
}

// ===========================================================================
// 动态注入 Lint 测试（Guard 存在时解析阶段校验白名单）
// ===========================================================================

func TestLintInjections_NoWhitelist_Rejected(t *testing.T) {
	// Guard 存在 + body 有注入 + 无 allowed-tools → Load 应失败
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), `---
name: test
---
!`+"`echo hello`"+`
`)
	guard := permission.NewGuard()
	l := newTestLoaderWithGuard(home, home, guard)
	_, err := l.Load("test", "")
	if err == nil {
		t.Fatal("expected Load to fail: skill has injection but no allowed-tools whitelist")
	}
	if !strings.Contains(err.Error(), "no allowed-tools Bash whitelist") {
		t.Errorf("error should mention missing whitelist, got: %v", err)
	}
}

func TestLintInjections_UnmatchedCommand_Rejected(t *testing.T) {
	// Guard 存在 + body 有注入 + allowed-tools 不覆盖 → Load 应失败
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), `---
name: test
allowed-tools:
  - "Bash(echo *)"
---
!`+"`date '+%Y-%m-%d'`"+`
`)
	guard := permission.NewGuard()
	l := newTestLoaderWithGuard(home, home, guard)
	_, err := l.Load("test", "")
	if err == nil {
		t.Fatal("expected Load to fail: date command not whitelisted")
	}
	if !strings.Contains(err.Error(), "is not covered by allowed-tools") {
		t.Errorf("error should mention uncovered command, got: %v", err)
	}
}

func TestLintInjections_MatchedCommand_Passes(t *testing.T) {
	// Guard 存在 + body 有注入 + allowed-tools 覆盖 → Load 成功
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), `---
name: test
allowed-tools:
  - "Bash(echo *)"
  - "Bash(date *)"
---
!`+"`echo hello`"+`
`)
	guard := permission.NewGuard()
	l := newTestLoaderWithGuard(home, home, guard)
	loaded, err := l.Load("test", "")
	if err != nil {
		t.Fatalf("Load should succeed with matched whitelist, got: %v", err)
	}
	if !strings.Contains(loaded.Body, "hello") {
		t.Errorf("injection not executed: %s", loaded.Body)
	}
}

func TestLintInjections_BareBash_AllAllowed(t *testing.T) {
	// allowed-tools: ["Bash"] 无 pattern → 所有命令放行
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), `---
name: test
allowed-tools:
  - "Bash"
---
!`+"`echo all-good`"+`
`)
	guard := permission.NewGuard()
	l := newTestLoaderWithGuard(home, home, guard)
	loaded, err := l.Load("test", "")
	if err != nil {
		t.Fatalf("bare Bash should allow all commands, got: %v", err)
	}
	if !strings.Contains(loaded.Body, "all-good") {
		t.Errorf("injection not executed: %s", loaded.Body)
	}
}

func TestLintInjections_MultilineWithGuard_ProperWhitelist(t *testing.T) {
	// Guard 存在 + 多行注入 + 完整白名单 → Load 成功
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), "---\nname: test\nallowed-tools:\n  - \"Bash(echo *)\"\n  - \"Bash(uname *)\"\n  - \"Bash(df *)\"\n  - \"Bash(tail *)\"\n---\n```!\necho \"=== info ===\"\nuname -s\ndf -h / | tail -1\n```\n")
	guard := permission.NewGuard()
	l := newTestLoaderWithGuard(home, home, guard)
	loaded, err := l.Load("test", "")
	if err != nil {
		t.Fatalf("multiline with full whitelist should pass lint, got: %v", err)
	}
	if !strings.Contains(loaded.Body, "=== info ===") {
		t.Errorf("multiline injection not executed: %s", loaded.Body)
	}
}

func TestLintInjections_MultilinePartialWhitelist_Rejected(t *testing.T) {
	// Guard 存在 + 多行注入 + 白名单只覆盖部分命令 → Load 应失败
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), "---\nname: test\nallowed-tools:\n  - \"Bash(echo *)\"\n---\n```!\necho hello\nuname -s\n```\n")
	guard := permission.NewGuard()
	l := newTestLoaderWithGuard(home, home, guard)
	_, err := l.Load("test", "")
	if err == nil {
		t.Fatal("expected Load to fail: uname not whitelisted")
	}
	if !strings.Contains(err.Error(), "uname") {
		t.Errorf("error should mention uname command, got: %v", err)
	}
}

func TestLintInjections_NoGuard_SkipsLint(t *testing.T) {
	// Guard 为 nil 时 lint 跳过 → 注入正常执行（测试/开发模式）
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), `---
name: test
---
!`+"`echo no-guard-ok`"+`
`)
	l := newTestLoader(home, home) // nil guard
	loaded, err := l.Load("test", "")
	if err != nil {
		t.Fatalf("no-guard loader should skip lint, got: %v", err)
	}
	if !strings.Contains(loaded.Body, "no-guard-ok") {
		t.Errorf("injection should execute when guard is nil: %s", loaded.Body)
	}
}

// ---------------------------------------------------------------------------
// Shell 参数解析测试
// ---------------------------------------------------------------------------

func TestShellSplit_Basic(t *testing.T) {
	args := shellSplit(`hello world`)
	if len(args) != 2 || args[0] != "hello" || args[1] != "world" {
		t.Errorf("got %v", args)
	}
}

func TestShellSplit_Quoting(t *testing.T) {
	args := shellSplit(`"production staging" main`)
	if len(args) != 2 || args[0] != "production staging" || args[1] != "main" {
		t.Errorf("got %v", args)
	}
}

func TestShellSplit_Empty(t *testing.T) {
	args := shellSplit("")
	if len(args) != 0 {
		t.Errorf("expected empty, got %v", args)
	}
}

// ---------------------------------------------------------------------------
// 转义测试
// ---------------------------------------------------------------------------

func TestLoad_EscapedDollar(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "test", "SKILL.md"), `---
name: test
---
Price: \$10.00, arg: $0
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("test", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "Price: $10.00") {
		t.Errorf("escaped dollar not restored: %s", loaded.Body)
	}
	if !strings.Contains(loaded.Body, "arg: hello") {
		t.Errorf("$0 should be replaced: %s", loaded.Body)
	}
}

// ---------------------------------------------------------------------------
// Skill 不存在测试
// ---------------------------------------------------------------------------

func TestLoad_SkillNotFound(t *testing.T) {
	td := tmpDir(t)
	l := newTestLoader(td, td)
	_, err := l.Load("nonexistent", "")
	if err == nil {
		t.Error("expected error for nonexistent skill")
	}
}

// ---------------------------------------------------------------------------
// 附属文件测试
// ---------------------------------------------------------------------------

func TestLoad_SupportingFiles(t *testing.T) {
	home := tmpDir(t)
	skillDir := filepath.Join(home, ".claude", "skills", "with-files")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), `---
name: with-files
---
Main body
`)
	writeFile(t, filepath.Join(skillDir, "reference.md"), "# Reference")
	writeFile(t, filepath.Join(skillDir, "examples", "sample.md"), "# Example")

	l := newTestLoader(home, home)
	loaded, err := l.Load("with-files", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "## Supporting files") {
		t.Error("expected supporting files section")
	}
	if !strings.Contains(loaded.Body, "reference.md") {
		t.Error("expected reference.md in listing")
	}
	if !strings.Contains(loaded.Body, "examples/sample.md") {
		t.Error("expected examples/sample.md in listing")
	}
}

func TestLoad_NoSupportingFiles(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "skills", "no-files", "SKILL.md"), `---
name: no-files
---
Body only
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("no-files", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(loaded.Body, "## Supporting files") {
		t.Error("should not have supporting files section")
	}
}

// ---------------------------------------------------------------------------
// 扁平 command 文件测试
// ---------------------------------------------------------------------------

func TestLoad_FlatCommandFile(t *testing.T) {
	home := tmpDir(t)
	writeFile(t, filepath.Join(home, ".claude", "commands", "greet.md"), `---
description: Greeting command
---
Hello $ARGUMENTS!
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("greet", "world")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Body, "Hello world!") {
		t.Errorf("flat command not loaded: %s", loaded.Body)
	}
	if strings.Contains(loaded.Body, "## Supporting files") {
		t.Error("flat file should not have supporting files")
	}
}

// ---------------------------------------------------------------------------
// FormatSkillListing 测试
// ---------------------------------------------------------------------------

func TestFormatSkillListing(t *testing.T) {
	home := tmpDir(t)
	cwd := tmpDir(t)

	writeFile(t, filepath.Join(home, ".claude", "skills", "deploy", "SKILL.md"), `---
description: Deploy to production
---
body
`)
	writeFile(t, filepath.Join(home, ".claude", "skills", "hidden", "SKILL.md"), `---
description: Hidden skill
disable-model-invocation: true
---
body
`)

	l := newTestLoader(home, cwd)
	listing := l.FormatSkillListing()

	if !strings.Contains(listing, "/deploy") {
		t.Error("expected /deploy in listing")
	}
	if !strings.Contains(listing, "Deploy to production") {
		t.Error("expected description in listing")
	}
	if strings.Contains(listing, "/hidden") {
		t.Error("disable-model-invocation skill should not appear")
	}
}

func TestFormatSkillListing_Empty(t *testing.T) {
	home := tmpDir(t)
	cwd := tmpDir(t)
	l := newTestLoader(home, cwd)
	listing := l.FormatSkillListing()
	if listing != "" {
		t.Errorf("expected empty, got: %s", listing)
	}
}

func TestFormatSkillListing_TruncatesDescription(t *testing.T) {
	home := tmpDir(t)
	cwd := tmpDir(t)

	longDesc := strings.Repeat("x", 2000)
	writeFile(t, filepath.Join(home, ".claude", "skills", "verbose", "SKILL.md"), `---
description: `+longDesc+`
---
body
`)

	l := newTestLoader(home, cwd)
	listing := l.FormatSkillListing()

	if !strings.Contains(listing, "/verbose") {
		t.Error("expected /verbose in listing")
	}
	// description 应被截断到 1536 字符，不应出现完整 2000 字符
	if strings.Contains(listing, strings.Repeat("x", 1600)) {
		t.Error("description should be truncated to 1536 chars")
	}
}

func TestFormatSkillListing_EmptyDescription(t *testing.T) {
	home := tmpDir(t)
	cwd := tmpDir(t)

	writeFile(t, filepath.Join(home, ".claude", "skills", "nodesc", "SKILL.md"), `---
name: nodesc
---
body
`)

	l := newTestLoader(home, cwd)
	listing := l.FormatSkillListing()
	if !strings.Contains(listing, "/nodesc") {
		t.Error("expected /nodesc in listing")
	}
	if !strings.Contains(listing, "| - |") || !strings.Contains(listing, "| /nodesc | - |") {
		t.Errorf("expected dash placeholder for empty description, got: %s", listing)
	}
}

// ---------------------------------------------------------------------------
// Reload 测试
// ---------------------------------------------------------------------------

func TestReload(t *testing.T) {
	home := tmpDir(t)
	cwd := tmpDir(t)
	l := newTestLoader(home, cwd)

	// 初始为空
	infos, _ := l.Reload()
	if len(infos) != 0 {
		t.Errorf("expected 0 skills, got %d", len(infos))
	}

	// 添加 skill 后重新扫描
	writeFile(t, filepath.Join(home, ".claude", "skills", "newskill", "SKILL.md"), `---
description: New
---
body
`)
	infos, _ = l.Reload()
	found := false
	for _, info := range infos {
		if info.Name == "newskill" {
			found = true
			break
		}
	}
	if !found {
		t.Error("new skill not found after reload")
	}
}

// ===========================================================================
// allowed-tools / 白名单 / 权限测试
// ===========================================================================

func TestParseAllowedBashPatterns_BashWithPattern(t *testing.T) {
	patterns := permission.ParseAllowedBashPatterns([]string{"Bash(git *)", "Bash(npm *)"})
	if len(patterns) != 2 {
		t.Fatalf("expected 2 patterns, got %d: %v", len(patterns), patterns)
	}
	if patterns[0] != "git *" {
		t.Errorf("patterns[0] = %q, want %q", patterns[0], "git *")
	}
	if patterns[1] != "npm *" {
		t.Errorf("patterns[1] = %q, want %q", patterns[1], "npm *")
	}
}

func TestParseAllowedBashPatterns_BashBare(t *testing.T) {
	patterns := permission.ParseAllowedBashPatterns([]string{"Bash"})
	if len(patterns) != 1 || patterns[0] != "*" {
		t.Errorf("expected [*], got %v", patterns)
	}
}

func TestParseAllowedBashPatterns_Mixed(t *testing.T) {
	patterns := permission.ParseAllowedBashPatterns([]string{"Bash(git *)", "Read", "Bash", "Write"})
	if len(patterns) != 2 {
		t.Fatalf("expected 2 bash patterns, got %d: %v", len(patterns), patterns)
	}
	if patterns[0] != "git *" {
		t.Errorf("patterns[0] = %q", patterns[0])
	}
	if patterns[1] != "*" {
		t.Errorf("patterns[1] = %q", patterns[1])
	}
}

func TestParseAllowedBashPatterns_Empty(t *testing.T) {
	patterns := permission.ParseAllowedBashPatterns(nil)
	if len(patterns) != 0 {
		t.Errorf("expected 0, got %d", len(patterns))
	}
	patterns = permission.ParseAllowedBashPatterns([]string{"Read", "Write"})
	if len(patterns) != 0 {
		t.Errorf("expected 0, got %d", len(patterns))
	}
}

func TestMatchBashPattern_Exact(t *testing.T) {
	if !permission.MatchBashPattern("git status", "git status") {
		t.Error("exact match should pass")
	}
	if permission.MatchBashPattern("git status", "git push") {
		t.Error("different exact command should not match")
	}
}

func TestMatchBashPattern_PrefixWildcard(t *testing.T) {
	if !permission.MatchBashPattern("git status", "git *") {
		t.Error("git * should match git status")
	}
	if !permission.MatchBashPattern("git push origin main", "git *") {
		t.Error("git * should match git push origin main")
	}
	if permission.MatchBashPattern("npm test", "git *") {
		t.Error("git * should not match npm test")
	}
}

func TestMatchBashPattern_WildcardAll(t *testing.T) {
	if !permission.MatchBashPattern("any command", "*") {
		t.Error("* should match any command")
	}
	if !permission.MatchBashPattern("any command", "") {
		t.Error("empty should match any command")
	}
}

func TestMatchBashPattern_SuffixWildcard(t *testing.T) {
	// *xxx 后缀匹配：命令以 pattern 去掉前导 * 的字符串结尾
	if !permission.MatchBashPattern("~/.claude/skills/gstack/bin/gstack-update-check", "*gstack-update-check") {
		t.Error("*gstack-update-check should match path ending with gstack-update-check")
	}
	if !permission.MatchBashPattern("/usr/local/bin/mycheck", "*mycheck") {
		t.Error("*mycheck should match path ending with mycheck")
	}
	if !permission.MatchBashPattern("mycheck", "*mycheck") {
		t.Error("*mycheck should match exact 'mycheck'")
	}
	if permission.MatchBashPattern("/path/to/other", "*mycheck") {
		t.Error("*mycheck should not match path ending with other")
	}
	// 后缀匹配不跨越路径分隔符的情况（仍然匹配，因为是纯字符串后缀）
	if !permission.MatchBashPattern("/foo/bar", "*bar") {
		t.Error("*bar should match /foo/bar")
	}
}

func TestMatchBashPattern_ContainsWildcard(t *testing.T) {
	if !permission.MatchBashPattern("~/.claude/skills/gstack/bin/gstack-update-check", "*gstack*") {
		t.Error("*gstack* should match path containing gstack")
	}
	if !permission.MatchBashPattern("/usr/local/bin/myapp", "*myapp*") {
		t.Error("*myapp* should match path containing myapp")
	}
	if permission.MatchBashPattern("/usr/local/bin/other", "*myapp*") {
		t.Error("*myapp* should not match path without myapp")
	}
	// 包含匹配也可用于中间片段
	if !permission.MatchBashPattern("npm run test -- --coverage", "*run test*") {
		t.Error("*run test* should match npm run test command")
	}
}

func TestMatchBashPattern_PrefixFallbackContains(t *testing.T) {
	// 核心场景: "gstack-update-check *" 应能匹配完整路径
	if !permission.MatchBashPattern("~/.claude/skills/gstack/bin/gstack-update-check", "gstack-update-check *") {
		t.Error("prefix pattern gstack-update-check * should fallback-contains-match the full path")
	}
	// 前缀匹配仍然优先
	if !permission.MatchBashPattern("git status", "git *") {
		t.Error("git * should prefix-match git status")
	}
	// 前缀失败但包含成功
	if !permission.MatchBashPattern("sudo git status", "git *") {
		t.Error("git * should fallback-contains-match sudo git status")
	}
	// 精确不匹配且不包含
	if permission.MatchBashPattern("npm test", "git *") {
		t.Error("git * should not match npm test")
	}
}

func TestMatchBashPattern_EdgeCases(t *testing.T) {
	// 前导空格
	if !permission.MatchBashPattern("  git status", "git *") {
		t.Error("leading spaces should still match")
	}
	// 空命令
	if !permission.MatchBashPattern("", "*") {
		t.Error("empty command with * should match")
	}
}

func TestIsShellAllowed(t *testing.T) {
	// isShellAllowed 已归一化到 permission.MatchBashPattern + Guard.shellSafetyCheck，
	// 此处直接测试 pattern 匹配逻辑（与 Guard 中 shellSafetyCheck 使用的同一函数）。
	patterns := []string{"git *", "go test"}

	tests := []struct {
		cmd     string
		allowed bool
	}{
		{"git status", true},
		{"git push origin main", true},
		{"go test", true},
		{"go test ./...", false}, // 精确匹配 go test，不匹配 go test ./...
		{"npm install", false},
	}
	for _, tt := range tests {
		got := false
		for _, p := range patterns {
			if permission.MatchBashPattern(tt.cmd, p) {
				got = true
				break
			}
		}
		if got != tt.allowed {
			t.Errorf("MatchBashPattern(%q) = %v, want %v", tt.cmd, got, tt.allowed)
		}
	}

	// * 兜底放行
	if !permission.MatchBashPattern("rm -rf /", "*") {
		t.Error("* should allow everything")
	}
	if !permission.MatchBashPattern("any command", "*") {
		t.Error("* should allow everything")
	}
}

// ===========================================================================
// 条件 skill (paths) 测试
// ===========================================================================

func TestIsConditional_True(t *testing.T) {
	dir := tmpDir(t)
	skillDir := filepath.Join(dir, ".claude", "skills", "react-expert")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), `---
description: React expert
paths:
  - "**/*.tsx"
  - "**/*.ts"
---
body
`)
	l := newTestLoader(dir, dir)
	if !l.isConditional(filepath.Join(skillDir, "SKILL.md")) {
		t.Error("expected conditional skill with paths")
	}
}

func TestIsConditional_False(t *testing.T) {
	dir := tmpDir(t)
	skillDir := filepath.Join(dir, ".claude", "skills", "normal-skill")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), `---
description: Normal skill
---
body
`)
	l := newTestLoader(dir, dir)
	if l.isConditional(filepath.Join(skillDir, "SKILL.md")) {
		t.Error("skill without paths should not be conditional")
	}
}

func TestIsConditional_EmptyPaths(t *testing.T) {
	dir := tmpDir(t)
	skillDir := filepath.Join(dir, ".claude", "skills", "empty-paths")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), `---
description: Has empty paths
paths: []
---
body
`)
	l := newTestLoader(dir, dir)
	// empty list = 无有效 paths
	if l.isConditional(filepath.Join(skillDir, "SKILL.md")) {
		t.Error("skill with empty paths should not be conditional")
	}
}

func TestStoreConditional(t *testing.T) {
	dir := tmpDir(t)
	skillDir := filepath.Join(dir, ".claude", "skills", "cond-skill")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), `---
description: Cond
paths:
  - "*.go"
---
body
`)
	l := newTestLoader(dir, dir)
	l.storeConditional(filepath.Join(skillDir, "SKILL.md"), "cond-skill")

	if _, exists := l.conditionalSkills["cond-skill"]; !exists {
		t.Fatal("expected conditional skill to be stored")
	}
	if l.conditionalSkills["cond-skill"].DirPath != filepath.Join(skillDir, "SKILL.md") {
		t.Error("expected filePath in DirPath field")
	}
}

func TestStoreConditional_AlreadyActivated(t *testing.T) {
	dir := tmpDir(t)
	skillDir := filepath.Join(dir, ".claude", "skills", "cond-skill")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), `---
description: Cond
paths:
  - "*.go"
---
body
`)
	l := newTestLoader(dir, dir)
	l.activatedConditionalNames["cond-skill"] = true
	l.storeConditional(filepath.Join(skillDir, "SKILL.md"), "cond-skill")

	if _, exists := l.conditionalSkills["cond-skill"]; exists {
		t.Error("already-activated skill should not be stored")
	}
}

func TestActivateForPaths_Matching(t *testing.T) {
	dir := tmpDir(t)
	skillDir := filepath.Join(dir, ".claude", "skills", "react-expert")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), `---
description: React
paths:
  - "**/*.tsx"
---
body
`)
	l := newTestLoader(dir, dir)
	l.storeConditional(filepath.Join(skillDir, "SKILL.md"), "react-expert")

	activated := l.ActivateForPaths([]string{"src/App.tsx"})
	if len(activated) != 1 || activated[0] != "react-expert" {
		t.Fatalf("expected react-expert activated, got %v", activated)
	}
	if !l.activatedConditionalNames["react-expert"] {
		t.Error("expected activatedConditionalNames to include react-expert")
	}
	if _, exists := l.conditionalSkills["react-expert"]; exists {
		t.Error("activated skill should be removed from conditionalSkills")
	}
}

func TestActivateForPaths_NotMatching(t *testing.T) {
	dir := tmpDir(t)
	skillDir := filepath.Join(dir, ".claude", "skills", "react-expert")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), `---
description: React
paths:
  - "**/*.tsx"
---
body
`)
	l := newTestLoader(dir, dir)
	l.storeConditional(filepath.Join(skillDir, "SKILL.md"), "react-expert")

	activated := l.ActivateForPaths([]string{"README.md"})
	if len(activated) != 0 {
		t.Errorf("expected no activation, got %v", activated)
	}
	if l.activatedConditionalNames["react-expert"] {
		t.Error("unmatched skill should not be activated")
	}
}

func TestActivateForPaths_EmptyInputs(t *testing.T) {
	l := newTestLoader("/tmp", "/tmp")
	// No conditional skills → empty
	if act := l.ActivateForPaths([]string{"foo.go"}); len(act) != 0 {
		t.Error("expected no activation with empty conditional map")
	}
	// Empty file paths → skip
	l.conditionalSkills["test"] = &LoadedSkill{DirPath: "/nonexistent"}
	if act := l.ActivateForPaths(nil); len(act) != 0 {
		t.Error("expected no activation with nil filePaths")
	}
}

func TestMatchAnyPath_DirectMatch(t *testing.T) {
	if !matchAnyPath([]string{"src/App.tsx"}, []string{"**/*.tsx"}) {
		t.Error("glob should match")
	}
}

func TestMatchAnyPath_BaseMatch(t *testing.T) {
	if !matchAnyPath([]string{"/abs/path/App.tsx"}, []string{"*.tsx"}) {
		t.Error("base name match should work")
	}
}

func TestMatchAnyPath_SuffixMatch(t *testing.T) {
	// pattern "src/**" matches any suffix starting with "src/"
	if !matchAnyPath([]string{"deeply/nested/src/foo"}, []string{"src/**"}) {
		t.Error("suffix prefix match should work for src/foo")
	}
}

func TestMatchAnyPath_NoMatch(t *testing.T) {
	if matchAnyPath([]string{"README.md"}, []string{"*.tsx", "*.ts"}) {
		t.Error("should not match unrelated patterns")
	}
}

func TestMatchAnyPath_MultiplePatterns(t *testing.T) {
	patterns := []string{"**/*.tsx", "**/*.ts", "**/*.jsx", "**/*.js"}
	if !matchAnyPath([]string{"src/App.tsx"}, patterns) {
		t.Error("first pattern should match")
	}
	if !matchAnyPath([]string{"lib/utils.js"}, patterns) {
		t.Error("last pattern should match")
	}
}

// ===========================================================================
// Frontmatter Paths 解析测试
// ===========================================================================

func TestFrontmatter_PathsField(t *testing.T) {
	dir := tmpDir(t)
	skillFile := filepath.Join(dir, "SKILL.md")
	writeFile(t, skillFile, `---
description: Conditional
paths:
  - "**/*.go"
  - "src/**"
---
body
`)
	fm, _, _, _, err := parseSKILLmd(skillFile, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(fm.Paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(fm.Paths))
	}
	if fm.Paths[0] != "**/*.go" || fm.Paths[1] != "src/**" {
		t.Errorf("paths not parsed correctly: %v", fm.Paths)
	}
}

// ===========================================================================
// List 排除条件 skill 测试
// ===========================================================================

func TestList_ExcludesConditionalSkills(t *testing.T) {
	home := tmpDir(t)
	cwd := tmpDir(t)
	l := newTestLoader(home, cwd)

	// 无条件 skill
	writeFile(t, filepath.Join(home, ".claude", "skills", "normal", "SKILL.md"), `---
description: Normal skill
---
body
`)
	// 条件 skill（有 paths）
	writeFile(t, filepath.Join(home, ".claude", "skills", "react", "SKILL.md"), `---
description: React
paths:
  - "*.tsx"
---
body
`)

	infos, err := l.List()
	if err != nil {
		t.Fatal(err)
	}

	normalFound := false
	reactFound := false
	for _, info := range infos {
		if info.Name == "normal" {
			normalFound = true
		}
		if info.Name == "react" {
			reactFound = true
		}
	}
	if !normalFound {
		t.Error("normal skill should appear in List")
	}
	if reactFound {
		t.Error("conditional skill should NOT appear in List before activation")
	}

	// 验证 conditional skill 被存储
	if _, exists := l.conditionalSkills["react"]; !exists {
		t.Error("conditional skill should be stored internally")
	}
}

func TestList_IncludesActivatedConditionalSkills(t *testing.T) {
	home := tmpDir(t)
	cwd := tmpDir(t)
	l := newTestLoader(home, cwd)

	writeFile(t, filepath.Join(home, ".claude", "skills", "react", "SKILL.md"), `---
description: React
paths:
  - "*.tsx"
---
body
`)

	// 第一次 List：条件 skill 被存储但不出现
	infos, _ := l.List()
	for _, info := range infos {
		if info.Name == "react" {
			t.Fatal("conditional skill should not appear initially")
		}
	}

	// 手动标记已激活
	l.activatedConditionalNames["react"] = true
	delete(l.conditionalSkills, "react")

	// Reload 后应出现
	infos, _ = l.Reload()
	found := false
	for _, info := range infos {
		if info.Name == "react" {
			found = true
			break
		}
	}
	if !found {
		t.Error("activated conditional skill should appear after Reload")
	}
}

// ===========================================================================
// Guard 集成测试（runCommand 权限路径）
// ===========================================================================

type testGuard struct {
	decision      permission.Decision
	reason        string
	skillPatterns []string
}

func (g *testGuard) Check(_ context.Context, toolName string, input json.RawMessage) permission.DecisionResult {
	// skill 白名单优先：匹配则直接放行（模拟 GuardImpl.shellSafetyCheck）
	if toolName == "bash" && len(g.skillPatterns) > 0 {
		var params struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(input, &params) == nil {
			for _, p := range g.skillPatterns {
				if permission.MatchBashPattern(params.Command, p) {
					return permission.DecisionResult{Decision: permission.DecisionAllow, Reason: permission.ReasonBuiltinAllow, Message: "skill whitelist"}
				}
			}
		}
	}
	return permission.DecisionResult{
		Decision: g.decision,
		Message:  g.reason,
	}
}

func (g *testGuard) SetSkillBashWhitelist(patterns []string) {
	g.skillPatterns = patterns
}

func (g *testGuard) ClearSkillBashWhitelist() {
	g.skillPatterns = nil
}

func (g *testGuard) AddRule(_ permission.Rule, _ permission.RuleScope) error  { return nil }
func (g *testGuard) RemoveRule(_ permission.Rule, _ permission.RuleScope) error { return nil }
func (g *testGuard) ListRules() []permission.RuleEntry                           { return nil }
func (g *testGuard) PersistRule(_ permission.Rule) error                         { return nil }
func (g *testGuard) SessionAllow(_ string, _ json.RawMessage)                    {}
func (g *testGuard) SessionDeny(_ string, _ json.RawMessage)                     {}
func (g *testGuard) ClearSession()                                               {}
func (g *testGuard) SessionMemoryLen() int                                       { return 0 }

func newTestLoaderWithGuard(homeDir, cwd string, guard permission.Guard) *Loader {
	return NewLoader(cwd, homeDir, "test-session-123", "medium", guard)
}

func TestRunCommand_GuardDeny(t *testing.T) {
	g := &testGuard{decision: permission.DecisionDeny, reason: "not allowed"}
	l := newTestLoaderWithGuard("/tmp", "/tmp", g)
	output := l.runCommand("echo hello", "/tmp")
	if !strings.Contains(output, "skill command denied") {
		t.Errorf("expected deny message, got: %q", output)
	}
	if !strings.Contains(output, "not allowed") {
		t.Errorf("expected reason in deny message, got: %q", output)
	}
}

func TestRunCommand_GuardAsk_Denied(t *testing.T) {
	g := &testGuard{decision: permission.DecisionAsk, reason: ""}
	l := newTestLoaderWithGuard("/tmp", "/tmp", g)
	output := l.runCommand("echo hello", "/tmp")
	if !strings.Contains(output, "skill command denied") {
		t.Errorf("expected deny for ask decision, got: %q", output)
	}
}

func TestRunCommand_GuardAllow_Executes(t *testing.T) {
	g := &testGuard{decision: permission.DecisionAllow}
	l := newTestLoaderWithGuard("/tmp", "/tmp", g)
	output := l.runCommand("echo success", "/tmp")
	if !strings.Contains(output, "success") {
		t.Errorf("expected command output, got: %q", output)
	}
}

func TestRunCommand_NoGuard_Executes(t *testing.T) {
	l := newTestLoader("/tmp", "/tmp")
	output := l.runCommand("echo no-guard", "/tmp")
	if !strings.Contains(output, "no-guard") {
		t.Errorf("expected command output without guard, got: %q", output)
	}
}

func TestRunCommand_WhitelistBypassGuard(t *testing.T) {
	// 白名单命令即使 Guard Deny 也应放行（通过 SetSkillBashWhitelist 注册）
	g := &testGuard{decision: permission.DecisionDeny, reason: "blocked"}
	l := newTestLoaderWithGuard("/tmp", "/tmp", g)
	l.setGuardSkillWhitelist([]string{"echo *"})
	output := l.runCommand("echo whitelisted", "/tmp")
	l.clearGuardSkillWhitelist()
	if !strings.Contains(output, "whitelisted") {
		t.Errorf("whitelisted command should bypass guard, got: %q", output)
	}
}

func TestRunCommand_GuardNil_Executes(t *testing.T) {
	// nil guard 应直接执行
	l := NewLoader("/tmp", "/tmp", "s", "medium", nil)
	output := l.runCommand("echo nil-guard", "/tmp")
	if !strings.Contains(output, "nil-guard") {
		t.Errorf("expected output with nil guard, got: %q", output)
	}
}

// ===========================================================================
// scanCommandsDir 条件 command 测试
// ===========================================================================

func TestScanCommandsDir_Conditional(t *testing.T) {
	home := tmpDir(t)
	cmdDir := filepath.Join(home, ".claude", "commands")
	writeFile(t, filepath.Join(cmdDir, "react-expert.md"), `---
description: React
paths:
  - "*.tsx"
---
body
`)
	l := newTestLoader(home, home)
	infos := l.scanCommandsDir(cmdDir, 0, make(map[string]bool))
	if len(infos) != 0 {
		t.Errorf("conditional command should not appear: got %d infos", len(infos))
	}
	if _, exists := l.conditionalSkills["react-expert"]; !exists {
		t.Error("conditional command should be stored")
	}
}

func TestScanCommandsDir_ActivatedConditional(t *testing.T) {
	home := tmpDir(t)
	cmdDir := filepath.Join(home, ".claude", "commands")
	writeFile(t, filepath.Join(cmdDir, "react-expert.md"), `---
description: React
paths:
  - "*.tsx"
---
body
`)
	l := newTestLoader(home, home)
	l.activatedConditionalNames["react-expert"] = true

	infos := l.scanCommandsDir(cmdDir, 0, make(map[string]bool))
	found := false
	for _, info := range infos {
		if info.Name == "react-expert" {
			found = true
		}
	}
	if !found {
		t.Error("activated conditional command should appear")
	}
}

// ===========================================================================
// isConditional 边界
// ===========================================================================

func TestIsConditional_FileNotFound(t *testing.T) {
	l := newTestLoader("/tmp", "/tmp")
	if l.isConditional("/path/does/not/exist/SKILL.md") {
		t.Error("non-existent file should not be conditional")
	}
}

func TestIsConditional_MalformedFrontmatter(t *testing.T) {
	dir := tmpDir(t)
	skillFile := filepath.Join(dir, "SKILL.md")
	writeFile(t, skillFile, `---
description: [unclosed
---
body
`)
	l := newTestLoader(dir, dir)
	if l.isConditional(skillFile) {
		t.Error("malformed frontmatter should not be conditional")
	}
}
