package permission

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Menfre01/waveloom/pkg/pathutil"
)

// ---------------------------------------------------------------------------
// GuardOption — 函数式选项
// ---------------------------------------------------------------------------

// GuardOption 是 NewGuard 的函数式选项。
type GuardOption func(*GuardImpl)

// WithWorkingDirs 设置允许的工作目录列表（覆盖默认值）。
func WithWorkingDirs(dirs ...string) GuardOption {
	return func(g *GuardImpl) {
		g.workingDirs = dirs
	}
}

// WithExtraWorkingDirs 追加额外允许的工作目录（保留默认值 cwd + /tmp + os.TempDir）。
func WithExtraWorkingDirs(dirs ...string) GuardOption {
	return func(g *GuardImpl) {
		g.workingDirs = append(g.workingDirs, dirs...)
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
	builtinAllow      map[string]bool   // 内置白名单工具，Check() 第 0 步直接放行
	projectConfigPath string            // settings.json 路径（落盘用）

	// skillBashPatterns 当前正在加载的 skill 的 Bash 白名单（来自 allowed-tools）。
	// 在 shellSafetyCheck 中，白名单命令优先放行，不触发高危拦截。
	// 由 skill.Loader 通过 SetSkillBashWhitelist / ClearSkillBashWhitelist 管理。
	skillBashPatterns []string

	// planMode 为 true 时，write_file/edit_file 仅放行 planFilePath，shell RiskLow→ALLOW。
	planMode     bool
	planFilePath string

	// availableBuildTools 是环境探测到的构建工具列表（如 "go", "node", "npm"）。
	// plan 模式下这些工具的 shell 命令从 RiskLow 提升为 ALLOW。
	availableBuildTools map[string]bool
}

// SetSkillBashWhitelist 设置当前 skill 的 Bash 白名单模式。
// 白名单命令会绕过 shellSafetyCheck 的高危拦截。
func (g *GuardImpl) SetSkillBashWhitelist(patterns []string) {
	g.skillBashPatterns = patterns
}

// ClearSkillBashWhitelist 清空 skill 白名单。
func (g *GuardImpl) ClearSkillBashWhitelist() {
	g.skillBashPatterns = nil
}

// EnterPlanMode 启用 plan 模式路径白名单 + shell RiskLow→ALLOW。
func (g *GuardImpl) EnterPlanMode(planFilePath string) {
	g.planMode = true
	g.planFilePath = planFilePath
}

// ExitPlanMode 恢复正常模式。
func (g *GuardImpl) ExitPlanMode() {
	g.planMode = false
	g.planFilePath = ""
}

// SetAvailableBuildTools 注入环境探测到的构建工具列表（用于 plan 模式 RiskLow→ALLOW）。
func (g *GuardImpl) SetAvailableBuildTools(tools []string) {
	g.availableBuildTools = make(map[string]bool, len(tools))
	for _, t := range tools {
		g.availableBuildTools[t] = true
	}
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
	g.toolRiskClass["read"] = RiskClassRead
	g.toolRiskClass["web_fetch"] = RiskClassRead
	g.toolRiskClass["write_file"] = RiskClassWrite
	g.toolRiskClass["write"] = RiskClassWrite
	g.toolRiskClass["edit_file"] = RiskClassWrite
	g.toolRiskClass["edit"] = RiskClassWrite
	g.toolRiskClass["bash"] = RiskClassExecute
	g.toolRiskClass["kill_background_task"] = RiskClassSafe

	// 内置白名单：无需权限检查，直接放行
	g.builtinAllow = map[string]bool{
		"ask_user_question": true,
		"skill":             true, // 用户显式安装/调用的 skill，不受权限拦截
		"enter_plan_mode":   true,
		"exit_plan_mode":    true,
		"agent":             true, // 子 agent 委托：父已 bypass，子能力是父的子集
	}

	// 默认工作目录：项目根目录 + /tmp（Unix）+ 系统临时目录
	g.workingDirs = make([]string, 0, 3)
	if cwd, err := os.Getwd(); err == nil {
		g.workingDirs = append(g.workingDirs, cwd)
	}
	if runtime.GOOS != "windows" {
		g.workingDirs = append(g.workingDirs, "/tmp")
	}
	g.workingDirs = append(g.workingDirs, pathutil.TempDir())

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
// 8 步检查流程（按顺序短路）：
//
//  0. 内置白名单 → ALLOW
//  1. deny 规则（工具级 + 内容级）→ DENY
//  2. ask 规则（工具级 + 内容级）→ ASK
//  2.5 Skill Bash 白名单（shell 工具且命令匹配）→ ALLOW（绕过 Step 3 高危拦截）
//  3. 工具特有安全检查 → DENY（硬拦截，不允许规则覆盖）
//  4. allow 规则（工具级 + 内容级）→ ALLOW
//  5. Session 记忆 → ALLOW/DENY
//  6. Bypass 模式 → ALLOW
//  7. 默认策略（read→ALLOW, write/execute→ASK）
func (g *GuardImpl) Check(ctx context.Context, toolName string, input json.RawMessage) DecisionResult {
	// Step 0: 内置白名单 — 直接放行，不经过规则/安全检查/默认策略
	if g.builtinAllow[toolName] {
		slog.Debug("perm step0 builtin allow", "tool", toolName)
		g.denialTracker.RecordAllow()
		return DecisionResult{
			Decision: DecisionAllow,
			Reason:   ReasonBuiltinAllow,
			Message:  "built-in always-allow tool",
		}
	}

	// Step 1: deny 规则检查（最高优先级）
	if result, found := g.ruleEngine.CheckDeny(toolName, input); found {
		slog.Info("perm step1 rule deny", "tool", toolName, "pattern", result.Rule)
		g.denialTracker.RecordDenial()
		return result
	}

	// Step 2: ask 规则检查
	if result, found := g.ruleEngine.CheckAsk(toolName, input); found {
		return result
	}

	// Step 2.5: Skill Bash 白名单 — 在安全检查之前放行
	// skill 的 allowed-tools 声明了该 skill 需要的命令，
	// 白名单命令绕过 Step 3 的高危硬拦截，但不绕过 deny 规则（Step 1）。
	// 白名单由 Loader 在加载 skill 时注册，持续到下一个 skill 加载。
	if toolName == "bash" && len(g.skillBashPatterns) > 0 {
		var params struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(input, &params) == nil {
			for _, pattern := range g.skillBashPatterns {
				if MatchBashPattern(params.Command, pattern) {
					g.denialTracker.RecordAllow()
					return DecisionResult{
						Decision: DecisionAllow,
						Reason:   ReasonBuiltinAllow,
						Message:  "skill bash whitelist",
					}
				}
			}
		}
	}

	// Step 3: 工具特有安全检查（硬拦截，在 allow 规则之前执行）
	if safetyResult := g.toolSafetyCheck(toolName, input); safetyResult.Decision != "" {
		if safetyResult.Decision == DecisionDeny {
			slog.Info("perm step3 security deny", "tool", toolName, "message", safetyResult.Message)
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
		slog.Debug("perm step5 memory", "tool", toolName, "decision", d)
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
	if result.Decision == DecisionAsk {
		slog.Info("perm default ask", "tool", toolName)
	}

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
// RiskNone → 直接 ALLOW（纯只读命令，零风险）；
// RiskLow → passthrough（构建工具，后续规则/默认策略决定）；
// RiskHigh → DENY（高危命令硬拦截）。
func (g *GuardImpl) shellSafetyCheck(input json.RawMessage) DecisionResult {
	var params struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return DecisionResult{}
	}

	// Skill Bash 白名单优先：白名单命令直接放行，不触发高危拦截
	for _, pattern := range g.skillBashPatterns {
		if MatchBashPattern(params.Command, pattern) {
			return DecisionResult{
				Decision: DecisionAllow,
				Reason:   ReasonBuiltinAllow,
				Message:  "skill bash whitelist",
			}
		}
	}

	cmdCheck := CommandSafetyCheck(params.Command)

	switch cmdCheck.Level {
	case RiskNone:
		// 纯只读命令：跳过后续规则/默认策略，直接放行
		return DecisionResult{
			Decision: DecisionAllow,
			Reason:   ReasonSafety,
			Message:  cmdCheck.Message,
		}

	case RiskLow:
		// plan 模式：构建工具（RiskLow）直接 ALLOW，无需逐条确认
		if g.planMode {
			return DecisionResult{
				Decision: DecisionAllow,
				Reason:   ReasonSafety,
				Message:  fmt.Sprintf("plan mode: low-risk command allowed: %s", cmdCheck.Pattern),
			}
		}
		// 非 plan 模式：passthrough，交给后续规则/默认策略
		return DecisionResult{}

	case RiskHigh:
		return DecisionResult{
			Decision: DecisionDeny,
			Reason:   ReasonSafety,
			Message:  fmt.Sprintf("dangerous command blocked: %s", cmdCheck.Pattern),
			Rule:     cmdCheck.Pattern,
		}

	default:
		// RiskMedium → passthrough，交给后续规则/默认策略
		return DecisionResult{}
	}
}

// fileSafetyCheck 对文件工具执行路径安全检查。
// 仅返回 deny（危险路径 write 操作硬拦截），不返回 ask。
// plan 模式下 write 操作仅放行 plan 文件路径。
func (g *GuardImpl) fileSafetyCheck(input json.RawMessage, isWriteOp bool) DecisionResult {
	filePath, _ := extractFilePath(input)
	if filePath == "" {
		return DecisionResult{}
	}

	// plan 模式：仅允许写入 plan 文件
	if g.planMode && isWriteOp {
		resolved, _ := pathutil.ResolvePathWithDir(filePath, "")
		planResolved, _ := pathutil.ResolvePathWithDir(g.planFilePath, "")
		if resolved != planResolved {
			return DecisionResult{
				Decision: DecisionDeny,
				Reason:   ReasonSafety,
				Message:  fmt.Sprintf("plan mode: only writes to %s are allowed", g.planFilePath),
			}
		}
		// plan 文件 → 直接 ALLOW，不经过后续规则/默认策略
		return DecisionResult{
			Decision: DecisionAllow,
			Reason:   ReasonSafety,
			Message:  "plan mode: write to plan file allowed",
		}
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
	case RiskClassSafe:
		return DecisionResult{
			Decision: DecisionAllow,
			Reason:   ReasonDefault,
			Message:  "safe internal tool: default allow",
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
	case "bash":
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

	case "ask_user_question":
		// ask_user_question 不需要内容级 pattern —— 它不操作文件/命令，
		// 且权限检查恒为 ask（requiresUserInteraction=true），
		// 没有 allow/deny/session 记忆的概念。
		return ""

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

// extractFilePath 从工具输入中提取文件路径和 working_dir。
func extractFilePath(input json.RawMessage) (path, workingDir string) {
	var params struct {
		FilePath   string `json:"file_path"`
		Path       string `json:"path"`
		Patch      string `json:"patch"`
		WorkingDir string `json:"working_dir"`
	}
	if json.Unmarshal(input, &params) != nil {
		return "", ""
	}
	path = params.FilePath
	if path == "" {
		path = params.Path
	}
	if path == "" && params.Patch != "" {
		// edit: 从 patch 文本中提取第一个 [PATH#TAG] 中的路径
		path = extractPathFromPatch(params.Patch)
	}
	if path == "" {
		path = params.WorkingDir
	}
	return path, params.WorkingDir
}

// extractPathFromPatch 从 hashline patch 文本中提取第一个 [PATH#TAG] 的路径部分。
func extractPathFromPatch(patch string) string {
	idx := strings.Index(patch, "[")
	if idx < 0 {
		return ""
	}
	rest := patch[idx+1:]
	end := strings.IndexByte(rest, ']')
	if end < 0 {
		return ""
	}
	header := rest[:end]
	hashIdx := strings.LastIndex(header, "#")
	if hashIdx < 0 {
		return header // 无 TAG，整段当作路径
	}
	return header[:hashIdx]
}
