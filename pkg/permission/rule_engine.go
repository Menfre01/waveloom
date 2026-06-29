package permission

import (
	"encoding/json"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Menfre01/waveloom/pkg/pathutil"
)

// ---------------------------------------------------------------------------
// RuleEngine — 规则匹配引擎
// ---------------------------------------------------------------------------

// RuleEngine 管理 allow/deny/ask 三组规则，提供按优先级顺序匹配的能力。
type RuleEngine struct {
	mu         sync.RWMutex
	allowRules []RuleEntry
	denyRules  []RuleEntry
	askRules   []RuleEntry
}

// NewRuleEngine 创建一个空的规则引擎。
func NewRuleEngine() *RuleEngine {
	return &RuleEngine{}
}

// LoadRules 加载规则（替换现有规则）。
func (re *RuleEngine) LoadRules(entries []RuleEntry) {
	re.mu.Lock()
	defer re.mu.Unlock()
	re.allowRules = nil
	re.denyRules = nil
	re.askRules = nil
	for _, e := range entries {
		re.addRuleLocked(e)
	}
}

// AddRule 追加一条规则。
func (re *RuleEngine) AddRule(entry RuleEntry) {
	re.mu.Lock()
	defer re.mu.Unlock()
	re.addRuleLocked(entry)
}

// RemoveRule 移除一条匹配的规则。
func (re *RuleEngine) RemoveRule(rule Rule, scope RuleScope) {
	re.mu.Lock()
	defer re.mu.Unlock()
	re.denyRules = removeRuleFrom(re.denyRules, rule, scope)
	re.askRules = removeRuleFrom(re.askRules, rule, scope)
	re.allowRules = removeRuleFrom(re.allowRules, rule, scope)
}

// CheckDeny 检查 deny 规则。返回 (result, found)。
func (re *RuleEngine) CheckDeny(toolName string, input json.RawMessage) (DecisionResult, bool) {
	re.mu.RLock()
	defer re.mu.RUnlock()
	return re.checkRules(re.denyRules, toolName, input, DecisionDeny, ReasonRule)
}

// CheckAsk 检查 ask 规则。返回 (result, found)。
func (re *RuleEngine) CheckAsk(toolName string, input json.RawMessage) (DecisionResult, bool) {
	re.mu.RLock()
	defer re.mu.RUnlock()
	return re.checkRules(re.askRules, toolName, input, DecisionAsk, ReasonRule)
}

// CheckAllow 检查 allow 规则。返回 (result, found)。
func (re *RuleEngine) CheckAllow(toolName string, input json.RawMessage) (DecisionResult, bool) {
	re.mu.RLock()
	defer re.mu.RUnlock()
	return re.checkRules(re.allowRules, toolName, input, DecisionAllow, ReasonRule)
}

// AllRules 返回所有规则。
func (re *RuleEngine) AllRules() []RuleEntry {
	re.mu.RLock()
	defer re.mu.RUnlock()

	var all []RuleEntry
	all = append(all, re.denyRules...)
	all = append(all, re.askRules...)
	all = append(all, re.allowRules...)
	return all
}

// ---------------------------------------------------------------------------
// 内部方法
// ---------------------------------------------------------------------------

func (re *RuleEngine) addRuleLocked(entry RuleEntry) {
	switch entry.Rule.Behavior {
	case RuleDeny:
		re.denyRules = append(re.denyRules, entry)
	case RuleAsk:
		re.askRules = append(re.askRules, entry)
	case RuleAllow:
		re.allowRules = append(re.allowRules, entry)
	}
}

// checkRules 在规则列表中查找匹配。
// 先匹配工具级规则（Pattern 为空），再匹配内容级规则（Pattern 非空）。
func (re *RuleEngine) checkRules(rules []RuleEntry, toolName string, input json.RawMessage, decision Decision, reason DecisionReason) (DecisionResult, bool) {
	// 第一遍：工具级匹配
	for _, e := range rules {
		if e.Rule.Pattern == "" && e.Rule.ToolName == toolName {
			return DecisionResult{
				Decision: decision,
				Reason:   reason,
				Rule:     FormatRule(e.Rule),
			}, true
		}
	}

	// 第二遍：内容级匹配
	for _, e := range rules {
		if e.Rule.Pattern != "" && e.Rule.ToolName == toolName {
			if matchContent(toolName, e.Rule.Pattern, input) {
				return DecisionResult{
					Decision: decision,
					Reason:   reason,
					Rule:     FormatRule(e.Rule),
				}, true
			}
		}
	}

	return DecisionResult{}, false
}

// matchContent 使用 glob 匹配工具输入内容。
// shell 工具: pattern 匹配 command 字段
// 文件工具: pattern 匹配 file_path 字段，路径预先归一化为绝对路径
func matchContent(toolName, pattern string, input json.RawMessage) bool {
	var target string
	var workingDir string

	switch toolName {
	case "shell":
		var params struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(input, &params) != nil {
			return false
		}
		// 归一化：剥离 "cd <path> &&" 前缀，使 pattern 匹配更稳定
		target, _ = pathutil.NormalizeShellCommand(params.Command)

	case "web_fetch":
		var params struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(input, &params) != nil || params.URL == "" {
			return false
		}
		target = params.URL

	default:
		// 文件工具：尝试多个可能的路径字段
		var params struct {
			FilePath   string `json:"file_path"`
			Path       string `json:"path"`
			WorkingDir string `json:"working_dir"`
		}
		if json.Unmarshal(input, &params) != nil {
			return false
		}
		target = params.FilePath
		if target == "" {
			target = params.Path
		}
		if target == "" {
			target = params.WorkingDir
		}
		workingDir = params.WorkingDir
	}

	if target == "" {
		return false
	}

	// 对 shell 命令，支持前缀匹配（"git *" 匹配 "git status" 和 "git"）
	if toolName == "shell" {
		if strings.HasSuffix(pattern, "*") {
			prefix := strings.TrimRight(strings.TrimSuffix(pattern, "*"), " ")
			// target 必须精确等于 prefix（无参数），或以 prefix + " " 开头（有参数），
			// 防止 "git *" 误匹配 "gitfoo"。
			if target == prefix || strings.HasPrefix(target, prefix+" ") {
				return true
			}
		}
		// 也尝试 glob 匹配
		matched, _ := path.Match(pattern, target)
		return matched
	}

	// 对文件路径，归一化后使用 glob 匹配
	// 保留原始 target 用于相对路径 pattern 的匹配
	originalTarget := target

	// 优先使用 working_dir 将 target 解析为绝对路径（对齐工具实际执行时的解析逻辑）
	resolved, err := pathutil.ResolvePathWithDir(target, workingDir)
	if err == nil {
		target = resolved
	} else {
		// 回退：仍尝试 filepath.Abs
		absTarget, err := filepath.Abs(target)
		if err == nil {
			target = filepath.Clean(absTarget)
		}
	}

	// 策略 1: 原始 pattern 匹配原始 target（相对路径 ↔ 相对路径）
	if matched, _ := path.Match(pattern, originalTarget); matched {
		return true
	}

	// 策略 2: 原始 pattern 匹配归一化后的绝对 target
	if target != originalTarget {
		if matched, _ := path.Match(pattern, target); matched {
			return true
		}
	}

	// 策略 3: 若 pattern 不含 glob 字符，将 pattern 也归一化为绝对路径后精确比较
	if !strings.ContainsAny(pattern, "*?[") {
		absPattern, err := filepath.Abs(pattern)
		if err == nil {
			if filepath.Clean(absPattern) == target {
				return true
			}
		}
	}

	// 策略 4: 匹配文件名（适用于 "*.go" 等仅匹配文件名的 pattern）
	if matched, _ := path.Match(pattern, filepath.Base(target)); matched {
		return true
	}

	return false
}

// removeRuleFrom 从规则列表中移除匹配的规则。
func removeRuleFrom(rules []RuleEntry, rule Rule, scope RuleScope) []RuleEntry {
	var result []RuleEntry
	for _, e := range rules {
		if e.Rule.ToolName == rule.ToolName && e.Rule.Pattern == rule.Pattern && e.Scope == scope {
			continue
		}
		result = append(result, e)
	}
	return result
}
