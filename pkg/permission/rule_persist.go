package permission

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// PersistRuleToConfig 将规则追加到项目 settings.json 中。
// 使用原子写入，保留文件中其他配置字段不变（llm、session 等）。
func PersistRuleToConfig(configPath string, rule Rule) error {
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	// 用 map[string]json.RawMessage 反序列化，保留所有顶层字段
	var raw map[string]json.RawMessage
	data, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read config file: %w", err)
		}
		// 文件不存在：初始化空 map
		raw = make(map[string]json.RawMessage)
	} else {
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parse config file: %w", err)
		}
	}

	// 读取现有 permissions（可能为空）
	var perms PermissionSettings
	if rawPerms, ok := raw["permissions"]; ok && len(rawPerms) > 0 {
		if err := json.Unmarshal(rawPerms, &perms); err != nil {
			return fmt.Errorf("parse permissions section: %w", err)
		}
	}

	// 将规则序列化为字符串 "tool_name(pattern)" 或 "tool_name"
	ruleStr := FormatRule(rule)

	// 去重：检查是否已有相同或更宽泛的规则覆盖
	switch rule.Behavior {
	case RuleAllow:
		if containsRule(perms.Allow, rule.ToolName, rule.Pattern) {
			return nil // 已有覆盖，不重复写入
		}
		perms.Allow = append(perms.Allow, ruleStr)
	case RuleDeny:
		if containsRule(perms.Deny, rule.ToolName, rule.Pattern) {
			return nil
		}
		perms.Deny = append(perms.Deny, ruleStr)
	default:
		// ask 规则暂不支持运行时添加
		return nil
	}

	// 将修改后的 permissions 序列化回 raw map
	permsJSON, err := marshalNoEscape(perms)
	if err != nil {
		return fmt.Errorf("marshal permissions: %w", err)
	}
	raw["permissions"] = permsJSON

	// 序列化整个文件并原子写入
	out, err := marshalIndentNoEscape(raw, "", "    ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	tmpPath := configPath + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0o644); err != nil {
		return fmt.Errorf("write config tmp: %w", err)
	}
	if err := os.Rename(tmpPath, configPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename config file: %w", err)
	}
	return nil
}

// containsRule 检查规则列表中是否已有覆盖 toolName + pattern 的规则。
// 如果已有工具级规则（pattern=""）或完全相同的 pattern，视为已覆盖。
func containsRule(rules []string, toolName, pattern string) bool {
	for _, r := range rules {
		parsed, err := ParseRule(r, RuleAllow) // Behavior 无关，只解析 ToolName+Pattern
		if err != nil {
			continue
		}
		// 工具级规则覆盖所有内容级
		if parsed.ToolName == toolName && parsed.Pattern == "" {
			return true
		}
		// 精确匹配
		if parsed.ToolName == toolName && parsed.Pattern == pattern {
			return true
		}
	}
	return false
}

// marshalNoEscape 将 v 序列化为 JSON，禁用 HTML 转义（&、<、> 保持原样）。
// 与 json.Marshal 行为一致：不追加换行。
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// Encode 会追加 \n，去掉以保持与 json.Marshal 兼容
	b := buf.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b, nil
}

// marshalIndentNoEscape 同 json.MarshalIndent，但禁用 HTML 转义。
// 与 json.MarshalIndent 行为一致：不追加换行。
func marshalIndentNoEscape(v any, prefix, indent string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent(prefix, indent)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// Encode 会追加 \n，去掉以保持与 json.MarshalIndent 兼容
	b := buf.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b, nil
}
