package permission

import (
	"encoding/json"
	"os"
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
	// 向后兼容：hashline 工具也匹配旧工具名的规则
	toolNames := compatToolNames(toolName)

	// 第一遍：工具级匹配
	for _, name := range toolNames {
		for _, e := range rules {
			if e.Rule.Pattern == "" && e.Rule.ToolName == name {
				return DecisionResult{
					Decision: decision,
					Reason:   reason,
					Rule:     FormatRule(e.Rule),
				}, true
			}
		}
	}

	// 第二遍：内容级匹配
	for _, name := range toolNames {
		for _, e := range rules {
			if e.Rule.Pattern != "" && e.Rule.ToolName == name {
				if matchContent(toolName, e.Rule.Pattern, input) {
					return DecisionResult{
						Decision: decision,
						Reason:   reason,
						Rule:     FormatRule(e.Rule),
					}, true
				}
			}
		}
	}

	return DecisionResult{}, false
}

// compatToolNames 返回应匹配的工具名列表（含向后兼容的旧名）。
func compatToolNames(toolName string) []string {
	switch toolName {
	case "edit_file_hashline":
		return []string{toolName, "edit_file"}
	case "read_file_hashline":
		return []string{toolName, "read_file"}
	default:
		return []string{toolName}
	}
}

// matchGlob 是对 path.Match 的增强，额外支持 ** 递归匹配。
// ** 匹配零个或多个路径组件（跨越 / 边界）。
// 不含 ** 的 pattern 直接委托给 path.Match，行为完全兼容。
// target 会先归一化为 / 分隔符，确保 Windows 下 \ 路径能正确匹配。
func matchGlob(pattern, target string) (bool, error) {
	// 归一化：将 Windows \ 转为 /，使 path.Match 和 splitPath 行为一致
	if strings.ContainsRune(target, '\\') {
		target = filepath.ToSlash(target)
	}
	if !strings.Contains(pattern, "**") {
		return path.Match(pattern, target)
	}
	patSegs := splitPath(pattern)
	tgtSegs := splitPath(target)
	return matchSegments(patSegs, tgtSegs), nil
}

// splitPath 将路径按 / 分割，保留空段（如绝对路径开头的空字符串）。
// Windows 路径中的 \ 会先归一化为 /，确保跨平台 glob 匹配一致性。
func splitPath(p string) []string {
	if p == "" {
		return nil
	}
	return strings.Split(filepath.ToSlash(p), "/")
}

// matchSegments 递归匹配 pattern 段与 target 段，** 可跨越零或多个段。
func matchSegments(pat, tgt []string) bool {
	// 两方都耗尽 → 匹配成功
	if len(pat) == 0 && len(tgt) == 0 {
		return true
	}

	// pattern 耗尽但 target 还有 → 不匹配
	if len(pat) == 0 {
		return false
	}

	// target 耗尽：剩余 pattern 必须全是 **
	if len(tgt) == 0 {
		for _, s := range pat {
			if s != "**" {
				return false
			}
		}
		return true
	}

	seg := pat[0]
	rest := pat[1:]

	if seg == "**" {
		// ** 匹配零个组件 → 跳过 **
		if matchSegments(rest, tgt) {
			return true
		}
		// ** 匹配一个组件 → 消费一个 target 段，保持 **
		if matchSegments(pat, tgt[1:]) {
			return true
		}
		return false
	}

	// 普通段：用 path.Match 比对
	matched, _ := path.Match(seg, tgt[0])
	if !matched {
		return false
	}
	return matchSegments(rest, tgt[1:])
}

// matchContent 使用 glob 匹配工具输入内容。
// shell 工具: pattern 匹配 command 字段
// 文件工具: pattern 匹配 file_path 字段，路径预先归一化为绝对路径
func matchContent(toolName, pattern string, input json.RawMessage) bool {
	var target string
	var workingDir string

	switch toolName {
	case "bash":
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
			Patch      string `json:"patch"`
			WorkingDir string `json:"working_dir"`
		}
		if json.Unmarshal(input, &params) != nil {
			return false
		}
		target = params.FilePath
		if target == "" {
			target = params.Path
		}
		if target == "" && params.Patch != "" {
			// edit_file_hashline: 从 patch 中提取 [PATH#TAG] 的路径
			target = extractPathFromPatch(params.Patch)
		}
		if target == "" {
			target = params.WorkingDir
		}
		workingDir = params.WorkingDir
	}

	if target == "" {
		return false
	}

	// 对 bash 命令，支持前缀匹配（"git *" 匹配 "git status" 和 "git"）
	if toolName == "bash" {
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
	if matched, _ := matchGlob(pattern, originalTarget); matched {
		return true
	}

	// 策略 2: 原始 pattern 匹配归一化后的绝对 target
	if target != originalTarget {
		if matched, _ := matchGlob(pattern, target); matched {
			return true
		}
	}

	// 策略 2.5: 若 pattern 是相对路径（不以 / 开头），将绝对 target 转为
	// 相对 CWD 的路径后再匹配，使得 "pkg/**" 能匹配
	// "/Users/x/project/pkg/tool/foo.go"。
	if !filepath.IsAbs(pattern) && target != "" {
		if cwd, err := os.Getwd(); err == nil {
			if relPath, err := filepath.Rel(cwd, target); err == nil {
				if matched, _ := matchGlob(pattern, relPath); matched {
					return true
				}
			}
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
	if matched, _ := matchGlob(pattern, filepath.Base(target)); matched {
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
