// Package plugin 发现并加载 Claude Code 已安装插件中的 skills/commands。
//
// 只读已有插件，不处理安装/卸载/更新。
// 跳过 agents/hooks/MCP/LSP 等 Waveloom 不支持的组件。
package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ---------------------------------------------------------------------------
// 数据结构
// ---------------------------------------------------------------------------

// InstalledPlugin 是 installed_plugins.json 中单个插件的安装条目。
type InstalledPlugin struct {
	Scope       string `json:"scope"`
	InstallPath string `json:"installPath"`
	Version     string `json:"version,omitempty"`
	ProjectPath string `json:"projectPath,omitempty"`
}

// installedPluginsFile 对应 installed_plugins.json。
// 使用 RawMessage 处理 plugins 字段以兼容 V1（object）和 V2（array）格式。
type installedPluginsFile struct {
	Version int              `json:"version"`
	Plugins json.RawMessage  `json:"plugins"`
}

// PluginManifest 对应 .claude-plugin/plugin.json 清单（只取 skills/commands 相关字段）。
type PluginManifest struct {
	Name        string   `json:"name"`
	Skills      []string `json:"skills,omitempty"`
	Commands    []string `json:"commands,omitempty"`
	Description string   `json:"description,omitempty"`
}

// claudeSettings 是 ~/.claude/settings.json 中与插件相关的字段。
type claudeSettings struct {
	EnabledPlugins map[string]any `json:"enabledPlugins"`
}

// PluginSkill 表示从插件中发现的一个 skill 路径。
type PluginSkill struct {
	PluginName    string // 所属插件名
	PluginVersion string
	SkillName     string // skill 名（目录名）
	SKILLPath     string // SKILL.md 绝对路径
}

// PluginCommand 表示从插件中发现的一个命令路径。
type PluginCommand struct {
	PluginName    string
	PluginVersion string
	CommandName   string // 文件名（不含 .md）
	MDPath        string // .md 文件绝对路径
}

// ---------------------------------------------------------------------------
// 发现
// ---------------------------------------------------------------------------

// Discover 发现所有已启用插件中的 skills 和 commands。
// 从 pluginsDir 读取 installed_plugins.json，从 claudeDir 读取 settings.json
// 判断启用状态。
func Discover(pluginsDir, claudeDir string) (skills []PluginSkill, commands []PluginCommand, err error) {
	if pluginsDir == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return nil, nil, homeErr
		}
		pluginsDir = filepath.Join(home, ".claude", "plugins")
	}
	if claudeDir == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return nil, nil, homeErr
		}
		claudeDir = filepath.Join(home, ".claude")
	}

	// 读 installed_plugins.json
	installedFile := filepath.Join(pluginsDir, "installed_plugins.json")
	data, err := os.ReadFile(installedFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil // 无插件目录，跳过
		}
		return nil, nil, fmt.Errorf("read installed plugins: %w", err)
	}

	var ipf installedPluginsFile
	if err := json.Unmarshal(data, &ipf); err != nil {
		return nil, nil, fmt.Errorf("parse installed plugins: %w", err)
	}
	// 只处理 V2 格式
	if ipf.Version != 2 || ipf.Plugins == nil {
		return nil, nil, nil
	}

	// 解码 V2 plugins（map[string][]InstalledPlugin）
	var v2Plugins map[string][]InstalledPlugin
	if err := json.Unmarshal(ipf.Plugins, &v2Plugins); err != nil {
		return nil, nil, fmt.Errorf("parse V2 plugins: %w", err)
	}

	// 读 settings.json 获取启用状态
	enabled := loadEnabledPlugins(claudeDir)

	// 遍历每个插件
	for pluginID, installations := range v2Plugins {
		if !isEnabled(enabled, pluginID) {
			continue
		}
		for _, inst := range installations {
			ps, pc := discoverPlugin(pluginID, inst)
			skills = append(skills, ps...)
			commands = append(commands, pc...)
		}
	}

	return skills, commands, nil
}

// discoverPlugin 发现单个插件安装条目中的 skills/commands。
func discoverPlugin(pluginID string, inst InstalledPlugin) (skills []PluginSkill, commands []PluginCommand) {
	// 读 plugin.json
	manifestPath := filepath.Join(inst.InstallPath, ".claude-plugin", "plugin.json")
	manifest, err := readManifest(manifestPath)
	if err != nil {
		return nil, nil
	}

	pluginName := manifest.Name
	if pluginName == "" {
		pluginName = pluginID
	}

	// ── Skills ──
	// 标准 skills/ 目录
	standardSkills := filepath.Join(inst.InstallPath, "skills")
	if entries, err := os.ReadDir(standardSkills); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillPath := filepath.Join(standardSkills, entry.Name(), "SKILL.md")
			if _, err := os.Stat(skillPath); err == nil {
				skills = append(skills, PluginSkill{
					PluginName:    pluginName,
					PluginVersion: inst.Version,
					SkillName:     entry.Name(),
					SKILLPath:     skillPath,
				})
			}
		}
	}

	// manifest 声明的额外 skills 目录
	for _, skillDir := range manifest.Skills {
		dir := resolveRelPath(inst.InstallPath, skillDir)
		if entries, err := os.ReadDir(dir); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				skillPath := filepath.Join(dir, entry.Name(), "SKILL.md")
				if _, err := os.Stat(skillPath); err == nil {
					skills = append(skills, PluginSkill{
						PluginName:    pluginName,
						PluginVersion: inst.Version,
						SkillName:     entry.Name(),
						SKILLPath:     skillPath,
					})
				}
			}
		}
	}

	// ── Commands ──
	// 标准 commands/ 目录
	standardCommands := filepath.Join(inst.InstallPath, "commands")
	if entries, err := os.ReadDir(standardCommands); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := trimMDExt(entry.Name())
			if name == "" {
				continue
			}
			cmdPath := filepath.Join(standardCommands, entry.Name())
			commands = append(commands, PluginCommand{
				PluginName:    pluginName,
				PluginVersion: inst.Version,
				CommandName:   name,
				MDPath:        cmdPath,
			})
		}
	}

	// manifest 声明的额外 commands
	for _, cmdPath := range manifest.Commands {
		absPath := resolveRelPath(inst.InstallPath, cmdPath)
		if info, err := os.Stat(absPath); err == nil && !info.IsDir() {
			name := trimMDExt(filepath.Base(absPath))
			if name != "" {
				commands = append(commands, PluginCommand{
					PluginName:    pluginName,
					PluginVersion: inst.Version,
					CommandName:   name,
					MDPath:        absPath,
				})
			}
		}
	}

	return skills, commands
}

// ---------------------------------------------------------------------------
// settings.json 启用状态
// ---------------------------------------------------------------------------

// loadEnabledPlugins 从 settings.json 读取 enabledPlugins 映射。
func loadEnabledPlugins(claudeDir string) map[string]any {
	settingsFile := filepath.Join(claudeDir, "settings.json")
	data, err := os.ReadFile(settingsFile)
	if err != nil {
		return nil
	}
	var cs claudeSettings
	if err := json.Unmarshal(data, &cs); err != nil {
		return nil
	}
	return cs.EnabledPlugins
}

// isEnabled 检查插件是否启用（settings.json 中 value 为 truthy 值）。
// 插件 ID 不在 enabledPlugins 中视为禁用。
// 注意：superpowers 可能在 enabledPlugins 中为 false。
func isEnabled(enabled map[string]any, pluginID string) bool {
	if enabled == nil {
		return false
	}
	v, ok := enabled[pluginID]
	if !ok {
		return false
	}
	// 处理 bool / string / nil
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val == "true" || val == "1"
	default:
		return val != nil
	}
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

// readManifest 读取并解析 plugin.json。
func readManifest(path string) (*PluginManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m PluginManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// resolveRelPath 解析相对于 base 的相对路径。
// 去掉 ./ 前缀后拼接。
func resolveRelPath(base, rel string) string {
	if len(rel) > 2 && rel[:2] == "./" {
		rel = rel[2:]
	}
	return filepath.Join(base, rel)
}

// trimMDExt 去掉 .md 后缀，非 .md 后缀返回空字符串。
func trimMDExt(name string) string {
	if len(name) >= 3 && name[len(name)-3:] == ".md" {
		return name[:len(name)-3]
	}
	return ""
}
