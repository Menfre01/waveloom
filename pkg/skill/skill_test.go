package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	return NewLoader(cwd, homeDir, "test-session-123", "medium")
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
	// $2 超出范围应保持字面
	if !strings.Contains(loaded.Body, "$2") {
		t.Errorf("$2 should remain as literal when only 1 arg provided: %s", loaded.Body)
	}
	// $1 也应保持字面
	if !strings.Contains(loaded.Body, "$1") {
		t.Errorf("$1 should remain as literal: %s", loaded.Body)
	}
	// $0 应被替换
	if !strings.Contains(loaded.Body, "First: onlyone") {
		t.Errorf("$0 should be replaced: %s", loaded.Body)
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
Some text !` + "`echo bad`" + ` should not execute
`)
	l := newTestLoader(home, home)
	loaded, err := l.Load("test", "")
	if err != nil {
		t.Fatal(err)
	}
	// 非行首的 !`...` 不应被执行，仍保留为字面文本
	if !strings.Contains(loaded.Body, "!`echo bad`") {
		t.Error("non-line-start !`...` should be preserved as literal text")
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
