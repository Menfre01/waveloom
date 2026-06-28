package permission

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"waveloom/pkg/pathutil"
)

// ---------------------------------------------------------------------------
// GuardOption — 函数式选项
// ---------------------------------------------------------------------------

// GuardOption 是 NewGuard 的函数式选项。
type GuardOption func(*GuardImpl)

// WithWorkingDirs 设置允许的工作目录列表。
func WithWorkingDirs(dirs ...string) GuardOption {
	return func(g *GuardImpl) {
		g.workingDirs = dirs
	}
}

// WithBypassMode 启用 bypass 模式（CI/测试场景）。
func WithBypassMode(enabled bool) GuardOption {
	return func(g *GuardImpl) {
		g.bypassMode = enabled
	}
}

// WithRules 加载初始规则。
func WithRules(entries []RuleEntry) GuardOption {
	return func(g *GuardImpl) {
		g.ruleEngine.LoadRules(entries)
	}
}

// WithToolRiskClass 设置指定工具的风险分类。
func WithToolRiskClass(toolName string, class ToolRiskClass) GuardOption {
	return func(g *GuardImpl) {
		g.toolRiskClass[toolName] = class
	}
}

// WithProjectConfigPath 设置项目配置文件路径（用于 PersistRule 落盘）。
func WithProjectConfigPath(path string) GuardOption {
	return func(g *GuardImpl) {
		g.projectConfigPath = path
	}
}

// ---------------------------------------------------------------------------
// GuardImpl — Guard 接口的默认实现
// ---------------------------------------------------------------------------

// GuardImpl 是 Guard 接口的默认实现。
// 组合 RuleEngine + SessionMemory + DenialTracker + 工具风险分类 + bypass 模式。
type GuardImpl struct {
	ruleEngine    *RuleEngine
	sessionMemory *SessionMemory
	denialTracker *DenialTracker

	workingDirs       []string
	bypassMode        bool
	toolRiskClass     map[string]ToolRiskClass
	projectConfigPath string // settings.json 路径（落盘用）
}

// NewGuard 创建一个新的 GuardImpl。
func NewGuard(opts ...GuardOption) *GuardImpl {
	g := &GuardImpl{
		ruleEngine:    NewRuleEngine(),
		sessionMemory: NewSessionMemory(),
		denialTracker: NewDenialTracker(),
		toolRiskClass: make(map[string]ToolRiskClass),
	}

	// 内置工具风险分类
	g.toolRiskClass["read_file"] = RiskClassRead
	g.toolRiskClass["search_file"] = RiskClassRead
	g.toolRiskClass["grep"] = RiskClassRead
	g.toolRiskClass["ls"] = RiskClassRead
	g.toolRiskClass["lsp_diagnostic"] = RiskClassRead
	g.toolRiskClass["lsp_definition"] = RiskClassRead
	g.toolRiskClass["lsp_references"] = RiskClassRead
	g.toolRiskClass["lsp_hover"] = RiskClassRead
	g.toolRiskClass["web_fetch"] = RiskClassRead
	g.toolRiskClass["write_file"] = RiskClassWrite
	g.toolRiskClass["edit_file"] = RiskClassWrite
	g.toolRiskClass["shell"] = RiskClassExecute

	// 默认工作目录
	if cwd, err := os.Getwd(); err == nil {
		g.workingDirs = []string{cwd}
	}

	for _, opt := range opts {
		opt(g)
	}

	return g
}

// ---------------------------------------------------------------------------
// Check — 8 步权限检查流程
// ---------------------------------------------------------------------------

// Check 对工具调用执行权限检查，返回决策结果。
//
// 7 步检查流程（按顺序短路）：
//
//  1. deny 规则（工具级 + 内容级）→ DENY
//  2. ask 规则（工具级 + 内容级）→ ASK
//  3. 工具特有安全检查 → DENY（硬拦截，不允许任何规则覆盖）
//  4. allow 规则（工具级 + 内容级）→ ALLOW
//  5. Session 记忆 → ALLOW/DENY
//  6. Bypass 模式 → ALLOW
//  7. 默认策略（read→ALLOW, write/execute→ASK）
func (g *GuardImpl) Check(ctx context.Context, toolName string, input json.RawMessage) DecisionResult {
	// Step 1: deny 规则检查（最高优先级）
	if result, found := g.ruleEngine.CheckDeny(toolName, input); found {
		g.denialTracker.RecordDenial()
		return result
	}

	// Step 2: ask 规则检查
	if result, found := g.ruleEngine.CheckAsk(toolName, input); found {
		return result
	}

	// Step 3: 工具特有安全检查（硬拦截，在 allow 规则之前执行）
	if safetyResult := g.toolSafetyCheck(toolName, input); safetyResult.Decision != "" {
		if safetyResult.Decision == DecisionDeny {
			g.denialTracker.RecordDenial()
		}
		return safetyResult
	}

	// Step 4: allow 规则检查（工具级 + 内容级）
	if result, found := g.ruleEngine.CheckAllow(toolName, input); found {
		g.denialTracker.RecordAllow()
		return result
	}

	// Step 5: session 记忆检查
	if d, found := g.sessionMemory.Lookup(toolName, ExtractPattern(toolName, input)); found {
		if d == DecisionAllow {
			g.denialTracker.RecordAllow()
		} else {
			g.denialTracker.RecordDenial()
		}
		return DecisionResult{
			Decision: d,
			Reason:   ReasonSession,
			Message:  "decision from session memory",
		}
	}

	// Step 6: bypass 模式
	if g.bypassMode {
		return DecisionResult{
			Decision: DecisionAllow,
			Reason:   ReasonBypass,
			Message:  "bypass mode enabled",
		}
	}

	// Step 7: 默认策略
	result := g.defaultDecision(toolName)

	// 所有 ASK 决策都附带建议记住的 pattern，供 UI 层展示
	if result.Decision == DecisionAsk && result.SuggestedPattern == "" {
		result.SuggestedPattern = ExtractPattern(toolName, input)
	}

	return result
}

// toolSafetyCheck 对工具执行类型专属的安全检查。
// 返回空的 Decision 表示 passthrough（无意见）。
func (g *GuardImpl) toolSafetyCheck(toolName string, input json.RawMessage) DecisionResult {
	riskClass := g.toolRiskClass[toolName]

	switch riskClass {
	case RiskClassExecute:
		return g.shellSafetyCheck(input)

	case RiskClassWrite, RiskClassRead:
		return g.fileSafetyCheck(input, riskClass == RiskClassWrite)

	default:
		// 未知工具，无安全检查
		return DecisionResult{}
	}
}

// shellSafetyCheck 对 shell 工具的命令执行安全检查。
// 仅返回 deny（高危命令硬拦截），不返回 ask。
func (g *GuardImpl) shellSafetyCheck(input json.RawMessage) DecisionResult {
	var params struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return DecisionResult{}
	}

	cmdCheck := CommandSafetyCheck(params.Command)

	switch cmdCheck.Level {
	case RiskHigh:
		return DecisionResult{
			Decision: DecisionDeny,
			Reason:   ReasonSafety,
			Message:  fmt.Sprintf("dangerous command blocked: %s", cmdCheck.Pattern),
			Rule:     cmdCheck.Pattern,
		}

	default:
		// RiskLow/RiskMedium → passthrough，交给后续规则/默认策略
		return DecisionResult{}
	}
}

// fileSafetyCheck 对文件工具执行路径安全检查。
// 仅返回 deny（危险路径 write 操作硬拦截），不返回 ask。
func (g *GuardImpl) fileSafetyCheck(input json.RawMessage, isWriteOp bool) DecisionResult {
	filePath, _ := extractFilePath(input)
	if filePath == "" {
		return DecisionResult{}
	}

	pathCheck := PathSafetyCheck(filePath, g.workingDirs)

	// 仅在工作目录外 + write 操作时 deny
	if pathCheck.Level == PathDangerous && isWriteOp {
		return DecisionResult{
			Decision: DecisionDeny,
			Reason:   ReasonSafety,
			Message:  pathCheck.Message,
		}
	}

	// 其他情况（safe/sensitive read/write）→ passthrough
	return DecisionResult{}
}

// defaultDecision 返回默认策略决策。
func (g *GuardImpl) defaultDecision(toolName string) DecisionResult {
	riskClass := g.toolRiskClass[toolName]

	switch riskClass {
	case RiskClassRead:
		return DecisionResult{
			Decision: DecisionAllow,
			Reason:   ReasonDefault,
			Message:  "read-only tool: default allow",
		}
	case RiskClassWrite, RiskClassExecute:
		return DecisionResult{
			Decision: DecisionAsk,
			Reason:   ReasonDefault,
			Message:  fmt.Sprintf("tool %q requires confirmation", toolName),
		}
	default:
		// 未知工具默认 ask
		return DecisionResult{
			Decision: DecisionAsk,
			Reason:   ReasonDefault,
			Message:  fmt.Sprintf("unknown tool %q: requires confirmation", toolName),
		}
	}
}

// ---------------------------------------------------------------------------
// AddRule / RemoveRule / ListRules
// ---------------------------------------------------------------------------

// AddRule 追加一条规则。
// scope=Session → 仅当前会话；scope=Config → 写入配置文件。
func (g *GuardImpl) AddRule(rule Rule, scope RuleScope) error {
	var source RuleSource

	switch scope {
	case ScopeSession:
		source = SourceSession
	case ScopeConfig:
		source = SourceConfig
	}

	g.ruleEngine.AddRule(RuleEntry{
		Rule:   rule,
		Source: source,
		Scope:  scope,
	})

	return nil
}

// RemoveRule 移除一条规则。
func (g *GuardImpl) RemoveRule(rule Rule, scope RuleScope) error {
	g.ruleEngine.RemoveRule(rule, scope)
	return nil
}

// ListRules 列出当前生效的所有规则。
func (g *GuardImpl) ListRules() []RuleEntry {
	configRules := g.ruleEngine.AllRules()
	sessionRules := g.sessionMemory.Entries()

	all := make([]RuleEntry, 0, len(configRules)+len(sessionRules))
	all = append(all, configRules...)
	all = append(all, sessionRules...)
	return all
}

// ---------------------------------------------------------------------------
// 便捷方法
// ---------------------------------------------------------------------------

// SessionAllow 将当前工具的权限决策记入 session 记忆。
func (g *GuardImpl) SessionAllow(toolName string, input json.RawMessage) {
	pattern := ExtractPattern(toolName, input)
	g.sessionMemory.Remember(toolName, pattern, DecisionAllow)
}

// SessionDeny 将当前工具的拒绝决策记入 session 记忆。
func (g *GuardImpl) SessionDeny(toolName string, input json.RawMessage) {
	pattern := ExtractPattern(toolName, input)
	g.sessionMemory.Remember(toolName, pattern, DecisionDeny)
}

// PersistRule 将规则落盘到项目配置文件。
func (g *GuardImpl) PersistRule(rule Rule) error {
	if g.projectConfigPath == "" {
		return nil
	}
	return PersistRuleToConfig(g.projectConfigPath, rule)
}

// SessionMemory 返回内部的 SessionMemory（用于外部清理等操作）。
func (g *GuardImpl) SessionMemory() *SessionMemory {
	return g.sessionMemory
}

// ClearSession 清空当前 session 记忆。
func (g *GuardImpl) ClearSession() {
	g.sessionMemory.Clear()
}

// SessionMemoryLen 返回当前 session 记忆中的条目数。
func (g *GuardImpl) SessionMemoryLen() int {
	return g.sessionMemory.Len()
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

// ExtractPattern 从工具输入中提取内容级 pattern。
// shell → 归一化 cd 前缀后返回完整命令（精确匹配）。
//   归一化确保 "cd /a && go test" 和 "cd /b && go test" 产生相同 pattern。
//   例: "cd /app && go test ./..." → "go test ./..."
// 文件工具 → 归一化为绝对路径，确保相对路径和绝对路径的 session 记忆互通。
func ExtractPattern(toolName string, input json.RawMessage) string {
	switch toolName {
	case "shell":
		var params struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(input, &params) != nil {
			return ""
		}
		// 归一化：剥离 "cd <path> &&" 前缀，返回完整归一化命令
		normalized, _ := pathutil.NormalizeShellCommand(params.Command)
		return normalized

	case "web_fetch":
		var params struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(input, &params) != nil || params.URL == "" {
			return ""
		}
		return params.URL

	default:
		path, workingDir := extractFilePath(input)
		if path == "" {
			return ""
		}
		// 优先使用 working_dir 归一化为绝对路径（对齐工具实际执行时的解析逻辑）
		resolved, err := pathutil.ResolvePathWithDir(path, workingDir)
		if err == nil {
			return resolved
		}
		// 回退：filepath.Abs
		abs, err := filepath.Abs(path)
		if err != nil {
			return path
		}
		return filepath.Clean(abs)
	}
}

// stringsJoin 用指定分隔符连接字符串切片。
func stringsJoin(elems []string, sep string) string {
	if len(elems) == 0 {
		return ""
	}
	n := len(sep) * (len(elems) - 1)
	for _, e := range elems {
		n += len(e)
	}
	b := make([]byte, 0, n)
	for i, e := range elems {
		if i > 0 {
			b = append(b, sep...)
		}
		b = append(b, e...)
	}
	return string(b)
}

// extractFilePath 从工具输入中提取文件路径和 working_dir。
func extractFilePath(input json.RawMessage) (path, workingDir string) {
	var params struct {
		FilePath   string `json:"file_path"`
		Path       string `json:"path"`
		WorkingDir string `json:"working_dir"`
	}
	if json.Unmarshal(input, &params) != nil {
		return "", ""
	}
	path = params.FilePath
	if path == "" {
		path = params.Path
	}
	if path == "" {
		path = params.WorkingDir
	}
	return path, params.WorkingDir
}

// splitFields 按空格分割字符串，返回非空 fields。
func splitFields(s string) []string {
	var result []string
	for _, f := range splitBySpace(s) {
		if f != "" {
			result = append(result, f)
		}
	}
	return result
}

// splitBySpace 简单的空格分割。
func splitBySpace(s string) []string {
	var fields []string
	current := ""
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if current != "" {
				fields = append(fields, current)
				current = ""
			}
		} else {
			current += string(r)
		}
	}
	if current != "" {
		fields = append(fields, current)
	}
	return fields
}
