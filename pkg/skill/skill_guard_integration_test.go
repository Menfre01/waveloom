package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/permission"
)

// TestSkillWhitelistPersistsAfterLoad 端到端验证：
// 1. Load() 时 !`cmd` 通过白名单执行
// 2. Load() 后白名单持续生效，后续 shell 调用也放行
// 3. 非白名单命令不受影响
// 注意：RiskNone 命令（echo/ls/cat 等）始终 ALLOW，此处使用构建工具命令验证白名单。
func TestSkillWhitelistPersistsAfterLoad(t *testing.T) {
	home := tmpDir(t)

	// 创建 SKILL.md，whitelist pattern "git *"
	skillDir := filepath.Join(home, ".claude", "skills", "my-skill")
	body := "---\ndescription: Test skill\nallowed-tools:\n  - \"Bash(git *)\"\n---\n" + "!" + "`git --version`"
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), body)

	guard := permission.NewGuard()
	l := NewLoader(home, home, "test-sid", "medium", guard)

	// 1. Load — !`cmd` 应在白名单保护下成功执行
	loaded, err := l.Load("my-skill", "")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !strings.Contains(loaded.Body, "git version") {
		t.Errorf("injected output not found in body:\n%s", loaded.Body)
	}

	// 2. Load 返回后白名单仍在 → 后续 shell 调用也放行
	output := l.runCommand("git --version", "/tmp")
	if strings.Contains(output, "skill command denied") {
		t.Errorf("whitelisted command after Load should succeed, got: %q", output)
	}

	// 3. 非白名单命令走默认策略（shell 默认 ASK → runCommand 拒绝）
	output = l.runCommand("python --version", "/tmp")
	if !strings.Contains(output, "skill command denied") {
		t.Errorf("non-whitelisted command should be denied, got: %q", output)
	}
}

// TestSkillWhitelistFullPathMatch 验证核心场景：
// pattern "gstack-update-check *" 匹配完整脚本路径，Load 时及 Load 后均放行。
func TestSkillWhitelistFullPathMatch(t *testing.T) {
	home := tmpDir(t)

	// 创建模拟脚本
	scriptDir := filepath.Join(home, ".claude", "skills", "gstack", "bin")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(scriptDir, "gstack-update-check")
	writeFile(t, scriptPath, "#!/bin/sh\necho \"gstack: no updates available\"")
	if err := os.Chmod(scriptPath, 0o755); err != nil {
		t.Fatal(err)
	}

	// 创建 SKILL.md，whitelist pattern 匹配脚本名
	skillDir := filepath.Join(home, ".claude", "skills", "gstack-check")
	body := "---\ndescription: GStack update check\nallowed-tools:\n  - \"Bash(gstack-update-check *)\"\n---\n" + "!" + "`" + scriptPath + "`"
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), body)

	guard := permission.NewGuard()
	l := NewLoader(home, home, "test-sid", "medium", guard)

	// 1. Load — !`cmd` 应在白名单保护下成功执行
	loaded, err := l.Load("gstack-check", "")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !strings.Contains(loaded.Body, "gstack: no updates available") {
		t.Errorf("expected gstack output in body, got:\n%s", loaded.Body)
	}

	// 2. Load 后白名单仍在 → 再次调用同一脚本也放行
	output := l.runCommand(scriptPath, scriptDir)
	if !strings.Contains(output, "gstack: no updates available") {
		t.Errorf("whitelisted script after Load should succeed, got: %q", output)
	}

	// 清理
	_ = os.RemoveAll(filepath.Join(home, ".claude", "skills", "gstack"))
}

// TestSkillWhitelistClearsOnNextLoad 验证：
// 加载新 skill 时旧白名单被清除，新白名单生效。
// 注意：RiskNone 命令（echo/ls/cat 等纯只读命令）始终 ALLOW，不依赖 skill 白名单。
func TestSkillWhitelistClearsOnNextLoad(t *testing.T) {
	home := tmpDir(t)

	// Skill A: whitelist "git *"
	skillDirA := filepath.Join(home, ".claude", "skills", "skill-a")
	bodyA := "---\ndescription: Skill A\nallowed-tools:\n  - \"Bash(git *)\"\n---\nbody-a"
	writeFile(t, filepath.Join(skillDirA, "SKILL.md"), bodyA)

	// Skill B: whitelist "go *"（不含 git）
	skillDirB := filepath.Join(home, ".claude", "skills", "skill-b")
	bodyB := "---\ndescription: Skill B\nallowed-tools:\n  - \"Bash(go *)\"\n---\nbody-b"
	writeFile(t, filepath.Join(skillDirB, "SKILL.md"), bodyB)

	guard := permission.NewGuard()
	l := NewLoader(home, home, "test-sid", "medium", guard)

	// 加载 Skill A → git 应被白名单放行
	_, err := l.Load("skill-a", "")
	if err != nil {
		t.Fatalf("Load skill-a failed: %v", err)
	}
	output := l.runCommand("git --version", "/tmp")
	if strings.Contains(output, "skill command denied") {
		t.Errorf("git should be whitelisted after loading skill-a, got: %q", output)
	}

	// 加载 Skill B → git 白名单应被清除，go 被放行
	_, err = l.Load("skill-b", "")
	if err != nil {
		t.Fatalf("Load skill-b failed: %v", err)
	}
	output = l.runCommand("git --version", "/tmp")
	if !strings.Contains(output, "skill command denied") {
		t.Errorf("git should NOT be whitelisted after loading skill-b, got: %q", output)
	}
	output = l.runCommand("go version", "/tmp")
	if strings.Contains(output, "skill command denied") {
		t.Errorf("go should be whitelisted after loading skill-b, got: %q", output)
	}
}
