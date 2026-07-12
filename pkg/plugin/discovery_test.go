package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscover_NoPluginsDir(t *testing.T) {
	tmp := t.TempDir()
	skills, cmds, err := Discover(
		filepath.Join(tmp, "nonexistent"),
		filepath.Join(tmp, ".claude"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}
	if len(cmds) != 0 {
		t.Errorf("expected 0 commands, got %d", len(cmds))
	}
}

func TestDiscover_PluginWithSkills(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	pluginsDir := filepath.Join(tmp, ".claude", "plugins")

	// 创建 installed_plugins.json
	installDir := filepath.Join(pluginsDir, "cache", "test-mkt", "my-plugin", "1.0.0")
	mustWriteJSON(t, filepath.Join(pluginsDir, "installed_plugins.json"), map[string]any{
		"version": 2,
		"plugins": map[string][]map[string]any{
			"my-plugin@test-mkt": {
				{
					"scope":       "user",
					"installPath": installDir,
					"version":     "1.0.0",
				},
			},
		},
	})

	// 创建 settings.json（启用插件）
	mustWriteJSON(t, filepath.Join(claudeDir, "settings.json"), map[string]any{
		"enabledPlugins": map[string]any{
			"my-plugin@test-mkt": true,
		},
	})

	// 创建 plugin.json
	mustWriteJSON(t, filepath.Join(installDir, ".claude-plugin", "plugin.json"), map[string]any{
		"name":        "my-plugin",
		"description": "Test plugin",
		"version":     "1.0.0",
	})

	// 创建 skill
	skillDir := filepath.Join(installDir, "skills", "test-skill")
	os.MkdirAll(skillDir, 0o755)
	mustWriteFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: test-skill\ndescription: Test\n---\n\nHello")

	// 创建 command
	cmdDir := filepath.Join(installDir, "commands")
	os.MkdirAll(cmdDir, 0o755)
	mustWriteFile(t, filepath.Join(cmdDir, "test-cmd.md"), "---\ndescription: Test cmd\n---\n\nContent")

	skills, cmds, err := Discover(pluginsDir, claudeDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].SkillName != "test-skill" {
		t.Errorf("expected skill name test-skill, got %s", skills[0].SkillName)
	}
	if skills[0].PluginName != "my-plugin" {
		t.Errorf("expected plugin name my-plugin, got %s", skills[0].PluginName)
	}
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	if cmds[0].CommandName != "test-cmd" {
		t.Errorf("expected command name test-cmd, got %s", cmds[0].CommandName)
	}
}

func TestDiscover_DisabledPlugin(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	pluginsDir := filepath.Join(tmp, ".claude", "plugins")

	installDir := filepath.Join(pluginsDir, "cache", "mkt", "disabled-plug", "1.0.0")
	mustWriteJSON(t, filepath.Join(pluginsDir, "installed_plugins.json"), map[string]any{
		"version": 2,
		"plugins": map[string][]map[string]any{
			"disabled-plug@mkt": {
				{
					"scope":       "user",
					"installPath": installDir,
					"version":     "1.0.0",
				},
			},
		},
	})
	mustWriteJSON(t, filepath.Join(claudeDir, "settings.json"), map[string]any{
		"enabledPlugins": map[string]any{
			"disabled-plug@mkt": false,
		},
	})
	skillDir := filepath.Join(installDir, "skills", "doomed")
	os.MkdirAll(skillDir, 0o755)
	mustWriteFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: doomed\n---\n\nNope")

	skills, cmds, err := Discover(pluginsDir, claudeDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills for disabled plugin, got %d", len(skills))
	}
	if len(cmds) != 0 {
		t.Errorf("expected 0 commands for disabled plugin, got %d", len(cmds))
	}
}

func TestDiscover_PluginMissingFromSettings(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	pluginsDir := filepath.Join(tmp, ".claude", "plugins")

	installDir := filepath.Join(pluginsDir, "cache", "mkt", "orphan", "1.0.0")
	mustWriteJSON(t, filepath.Join(pluginsDir, "installed_plugins.json"), map[string]any{
		"version": 2,
		"plugins": map[string][]map[string]any{
			"orphan@mkt": {
				{
					"scope":       "user",
					"installPath": installDir,
					"version":     "1.0.0",
				},
			},
		},
	})
	// 空 settings.json（无 enabledPlugins）
	mustWriteJSON(t, filepath.Join(claudeDir, "settings.json"), map[string]any{})
	skillDir := filepath.Join(installDir, "skills", "ghost")
	os.MkdirAll(skillDir, 0o755)
	mustWriteFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: ghost\n---\n\nBoo")

	skills, _, _ := Discover(pluginsDir, claudeDir)
	if len(skills) != 0 {
		t.Errorf("expected 0 skills for orphans, got %d", len(skills))
	}
}

func TestDiscover_V1Format(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	pluginsDir := filepath.Join(tmp, ".claude", "plugins")

	// V1 格式：plugins 是 map[string]object（不是 array）
	mustWriteJSON(t, filepath.Join(pluginsDir, "installed_plugins.json"), map[string]any{
		"version": 1,
		"plugins": map[string]map[string]any{
			"old@mkt": {
				"installPath": "/some/path",
				"version":     "0.1",
			},
		},
	})

	skills, cmds, err := Discover(pluginsDir, claudeDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills for V1, got %d", len(skills))
	}
	if len(cmds) != 0 {
		t.Errorf("expected 0 commands for V1, got %d", len(cmds))
	}
}

func TestDiscover_ManifestExtraSkillsDir(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	pluginsDir := filepath.Join(tmp, ".claude", "plugins")

	installDir := filepath.Join(pluginsDir, "cache", "mkt", "extra", "1.0.0")
	mustWriteJSON(t, filepath.Join(pluginsDir, "installed_plugins.json"), map[string]any{
		"version": 2,
		"plugins": map[string][]map[string]any{
			"extra@mkt": {
				{
					"scope":       "user",
					"installPath": installDir,
					"version":     "1.0.0",
				},
			},
		},
	})
	mustWriteJSON(t, filepath.Join(claudeDir, "settings.json"), map[string]any{
		"enabledPlugins": map[string]any{
			"extra@mkt": true,
		},
	})
	// manifest 声明额外 skills 目录
	mustWriteJSON(t, filepath.Join(installDir, ".claude-plugin", "plugin.json"), map[string]any{
		"name":   "extra",
		"skills": []string{"./extra-skills"},
	})
	extraDir := filepath.Join(installDir, "extra-skills", "bonus")
	os.MkdirAll(extraDir, 0o755)
	mustWriteFile(t, filepath.Join(extraDir, "SKILL.md"), "---\nname: bonus\n---\n\nExtra")

	skills, _, err := Discover(pluginsDir, claudeDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill from extra dir, got %d", len(skills))
	}
	if skills[0].SkillName != "bonus" {
		t.Errorf("expected bonus, got %s", skills[0].SkillName)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustWriteJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, path, string(data))
}

// ── 补充测试：覆盖 untested 路径 ──

func TestDiscover_InvalidJSON(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	pluginsDir := filepath.Join(tmp, ".claude", "plugins")
	mustWriteFile(t, filepath.Join(pluginsDir, "installed_plugins.json"), "not json")

	_, _, err := Discover(pluginsDir, claudeDir)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestDiscover_InvalidV2PluginsFormat(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	pluginsDir := filepath.Join(tmp, ".claude", "plugins")
	// V2 但 plugins 字段不是 map[string]array
	mustWriteJSON(t, filepath.Join(pluginsDir, "installed_plugins.json"), map[string]any{
		"version": 2,
		"plugins": map[string]string{"foo": "bar"}, // invalid: string not array
	})
	mustWriteJSON(t, filepath.Join(claudeDir, "settings.json"), map[string]any{
		"enabledPlugins": map[string]any{"foo@bar": true},
	})

	_, _, err := Discover(pluginsDir, claudeDir)
	if err == nil {
		t.Error("expected error for invalid V2 plugins format, got nil")
	}
}

func TestDiscover_NoManifest(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	pluginsDir := filepath.Join(tmp, ".claude", "plugins")

	installDir := filepath.Join(pluginsDir, "cache", "mkt", "noman", "1.0.0")
	mustWriteJSON(t, filepath.Join(pluginsDir, "installed_plugins.json"), map[string]any{
		"version": 2,
		"plugins": map[string][]map[string]any{
			"noman@mkt": {{"scope": "user", "installPath": installDir, "version": "1.0.0"}},
		},
	})
	mustWriteJSON(t, filepath.Join(claudeDir, "settings.json"), map[string]any{
		"enabledPlugins": map[string]any{"noman@mkt": true},
	})
	// 不创建 plugin.json — readManifest 会失败

	skills, cmds, err := Discover(pluginsDir, claudeDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills from plugin without manifest, got %d", len(skills))
	}
	if len(cmds) != 0 {
		t.Errorf("expected 0 commands from plugin without manifest, got %d", len(cmds))
	}
}

func TestDiscover_PluginNameFallback(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	pluginsDir := filepath.Join(tmp, ".claude", "plugins")

	installDir := filepath.Join(pluginsDir, "cache", "mkt", "noname", "1.0.0")
	mustWriteJSON(t, filepath.Join(pluginsDir, "installed_plugins.json"), map[string]any{
		"version": 2,
		"plugins": map[string][]map[string]any{
			"noname@mkt": {{"scope": "user", "installPath": installDir, "version": "1.0.0"}},
		},
	})
	mustWriteJSON(t, filepath.Join(claudeDir, "settings.json"), map[string]any{
		"enabledPlugins": map[string]any{"noname@mkt": true},
	})
	// plugin.json 必须有 name 字段为空 → 回退到 pluginID
	mustWriteJSON(t, filepath.Join(installDir, ".claude-plugin", "plugin.json"), map[string]any{
		"description": "no name",
	})
	skillDir := filepath.Join(installDir, "skills", "orphan")
	os.MkdirAll(skillDir, 0o755)
	mustWriteFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: orphan\n---\n\nHello")

	skills, _, err := Discover(pluginsDir, claudeDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	// PluginName 应为 "noname@mkt"（回退到 pluginID）
	if skills[0].PluginName != "noname@mkt" {
		t.Errorf("expected PluginName fallback to 'noname@mkt', got %s", skills[0].PluginName)
	}
}

func TestDiscover_NonDirEntryInSkills(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	pluginsDir := filepath.Join(tmp, ".claude", "plugins")

	installDir := filepath.Join(pluginsDir, "cache", "mkt", "junk", "1.0.0")
	mustWriteJSON(t, filepath.Join(pluginsDir, "installed_plugins.json"), map[string]any{
		"version": 2,
		"plugins": map[string][]map[string]any{
			"junk@mkt": {{"scope": "user", "installPath": installDir, "version": "1.0.0"}},
		},
	})
	mustWriteJSON(t, filepath.Join(claudeDir, "settings.json"), map[string]any{
		"enabledPlugins": map[string]any{"junk@mkt": true},
	})
	mustWriteJSON(t, filepath.Join(installDir, ".claude-plugin", "plugin.json"), map[string]any{
		"name": "junk",
	})
	// 在 skills/ 下放一个文件（不是目录）— 应跳过
	os.MkdirAll(filepath.Join(installDir, "skills"), 0o755)
	mustWriteFile(t, filepath.Join(installDir, "skills", "notadir.txt"), "hello")

	skills, _, err := Discover(pluginsDir, claudeDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills with only non-dir entries, got %d", len(skills))
	}
}

func TestDiscover_NonMDFileInCommands(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	pluginsDir := filepath.Join(tmp, ".claude", "plugins")

	installDir := filepath.Join(pluginsDir, "cache", "mkt", "nonmd", "1.0.0")
	mustWriteJSON(t, filepath.Join(pluginsDir, "installed_plugins.json"), map[string]any{
		"version": 2,
		"plugins": map[string][]map[string]any{
			"nonmd@mkt": {{"scope": "user", "installPath": installDir, "version": "1.0.0"}},
		},
	})
	mustWriteJSON(t, filepath.Join(claudeDir, "settings.json"), map[string]any{
		"enabledPlugins": map[string]any{"nonmd@mkt": true},
	})
	mustWriteJSON(t, filepath.Join(installDir, ".claude-plugin", "plugin.json"), map[string]any{
		"name": "nonmd",
	})
	// commands/ 下放 .txt 文件而非 .md
	os.MkdirAll(filepath.Join(installDir, "commands"), 0o755)
	mustWriteFile(t, filepath.Join(installDir, "commands", "test.txt"), "not a command")

	_, cmds, err := Discover(pluginsDir, claudeDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmds) != 0 {
		t.Errorf("expected 0 commands with only non-.md files, got %d", len(cmds))
	}
}

func TestDiscover_HookPluginNoSkills(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	pluginsDir := filepath.Join(tmp, ".claude", "plugins")

	// 只有 LSP 配置的插件（无 skills/commands 目录）
	installDir := filepath.Join(pluginsDir, "cache", "mkt", "lsp-only", "1.0.0")
	mustWriteJSON(t, filepath.Join(pluginsDir, "installed_plugins.json"), map[string]any{
		"version": 2,
		"plugins": map[string][]map[string]any{
			"lsp-only@mkt": {{"scope": "user", "installPath": installDir, "version": "1.0.0"}},
		},
	})
	mustWriteJSON(t, filepath.Join(claudeDir, "settings.json"), map[string]any{
		"enabledPlugins": map[string]any{"lsp-only@mkt": true},
	})
	mustWriteJSON(t, filepath.Join(installDir, ".claude-plugin", "plugin.json"), map[string]any{
		"name": "lsp-only",
	})
	// 不创建 skills/ commands/ 目录

	skills, cmds, err := Discover(pluginsDir, claudeDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills from LSP-only plugin, got %d", len(skills))
	}
	if len(cmds) != 0 {
		t.Errorf("expected 0 commands from LSP-only plugin, got %d", len(cmds))
	}
}

func TestDiscover_EmptyPathsReturnsNoError(t *testing.T) {
	// 空路径触发 os.UserHomeDir()，通常会有 home 目录但没有 .claude/plugins
	skills, cmds, err := Discover("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 结果取决于实际机器是否安装了插件
	_ = skills
	_ = cmds
}
