package permission

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
)

// ---------------------------------------------------------------------------
// PermissionSettings — 配置文件结构
// ---------------------------------------------------------------------------

// PermissionSettings 对应 settings.json 中的 permissions 配置块。
type PermissionSettings struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
	Ask   []string `json:"ask"`
}

// settingsFileWithPermissions 是 settings.json 的顶层结构（包含 permissions）。
type settingsFileWithPermissions struct {
	LLM         *interface{}        `json:"llm"`
	Permissions *PermissionSettings `json:"permissions"`
}

// ---------------------------------------------------------------------------
// LoadRulesFromSettings — 从配置解析规则
// ---------------------------------------------------------------------------

// LoadRulesFromSettings 从 PermissionSettings 解析规则。
// 无效规则跳过并记录警告（不阻塞启动）。
func LoadRulesFromSettings(ps *PermissionSettings, source RuleSource) []RuleEntry {
	if ps == nil {
		return nil
	}

	var entries []RuleEntry

	// 解析 allow 规则
	for _, s := range ps.Allow {
		rule, err := ParseRule(s, RuleAllow)
		if err != nil {
			slog.Warn("skipping invalid allow rule", "rule", s, "err", err)
			continue
		}
		entries = append(entries, RuleEntry{
			Rule:   rule,
			Source: source,
			Scope:  ScopeConfig,
		})
	}

	// 解析 deny 规则
	for _, s := range ps.Deny {
		rule, err := ParseRule(s, RuleDeny)
		if err != nil {
			slog.Warn("skipping invalid deny rule", "rule", s, "err", err)
			continue
		}
		entries = append(entries, RuleEntry{
			Rule:   rule,
			Source: source,
			Scope:  ScopeConfig,
		})
	}

	// 解析 ask 规则
	for _, s := range ps.Ask {
		rule, err := ParseRule(s, RuleAsk)
		if err != nil {
			slog.Warn("skipping invalid ask rule", "rule", s, "err", err)
			continue
		}
		entries = append(entries, RuleEntry{
			Rule:   rule,
			Source: source,
			Scope:  ScopeConfig,
		})
	}

	return entries
}

// LoadRulesFromConfigFile 从 settings.json 文件加载权限规则。
func LoadRulesFromConfigFile(path string) ([]RuleEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 配置文件不存在是正常的
		}
		return nil, fmt.Errorf("reading permission config %s: %w", path, err)
	}

	var sf settingsFileWithPermissions
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("parsing permission config %s: %w", path, err)
	}

	return LoadRulesFromSettings(sf.Permissions, SourceConfig), nil
}

// LoadRulesFromConfigFiles 从全局和项目配置文件合并加载权限规则。
// 合并策略：以 (Behavior, ToolName, Pattern) 为键，项目规则覆盖全局同键规则。
// 文件不存在时静默跳过。
func LoadRulesFromConfigFiles(globalPath, projectPath string) ([]RuleEntry, error) {
	ruleMap := make(map[string]RuleEntry)

	// 先加载全局
	if globalPath != "" {
		globalRules, err := LoadRulesFromConfigFile(globalPath)
		if err != nil {
			return nil, err
		}
		for _, r := range globalRules {
			ruleMap[ruleEntryKey(r)] = r
		}
	}

	// 再加载项目（同键覆盖）
	if projectPath != "" {
		projectRules, err := LoadRulesFromConfigFile(projectPath)
		if err != nil {
			return nil, err
		}
		for _, r := range projectRules {
			ruleMap[ruleEntryKey(r)] = r
		}
	}

	result := make([]RuleEntry, 0, len(ruleMap))
	for _, r := range ruleMap {
		result = append(result, r)
	}
	return result, nil
}

// ruleEntryKey 生成规则条目的唯一键（Behavior + ToolName + Pattern）。
func ruleEntryKey(e RuleEntry) string {
	return string(e.Rule.Behavior) + ":" + e.Rule.ToolName + "\x00" + e.Rule.Pattern
}
