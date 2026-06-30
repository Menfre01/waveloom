// Package permission 实现 Waveloom Code Agent 的权限与安全系统。
//
// Guard 是权限守门人的核心接口，在工具执行前拦截、评估、决策。
// 检查流程（7 步，按顺序短路）：
//
//  1. deny 规则（工具级 + 内容级）→ DENY
//  2. ask 规则（工具级 + 内容级）→ ASK
//  3. 工具特有安全检查 → DENY（硬拦截）
//  4. allow 规则（工具级 + 内容级）→ ALLOW
//  5. Session 记忆 → ALLOW/DENY
//  6. Bypass 模式 → ALLOW
//  7. 默认策略（read→ALLOW, write/execute→ASK）
package permission

import (
	"context"
	"encoding/json"
)

// ---------------------------------------------------------------------------
// Decision — 权限决策
// ---------------------------------------------------------------------------

// Decision 表示权限检查的三种结果。
type Decision string

const (
	DecisionAllow Decision = "allow" // 允许执行
	DecisionDeny  Decision = "deny"  // 拒绝执行
	DecisionAsk   Decision = "ask"   // 需要用户确认
)

// ---------------------------------------------------------------------------
// DecisionReason — 决策原因
// ---------------------------------------------------------------------------

// DecisionReason 描述决策产生的原因。
type DecisionReason string

const (
	ReasonRule        DecisionReason = "rule"          // 匹配了显式规则
	ReasonDefault     DecisionReason = "default"       // 无匹配规则，走默认策略
	ReasonSafety      DecisionReason = "safety"        // 安全检查拦截
	ReasonSession     DecisionReason = "session"       // 会话级记忆
	ReasonBypass      DecisionReason = "bypass"        // bypass 模式（测试/CI）
	ReasonBuiltinAllow DecisionReason = "builtin_allow" // 内置白名单直接放行
)

// ---------------------------------------------------------------------------
// DecisionResult — 权限决策结果
// ---------------------------------------------------------------------------

// DecisionResult 包含权限决策的完整信息。
type DecisionResult struct {
	Decision         Decision       // 决策
	Reason           DecisionReason // 原因
	Message          string         // 人类可读的解释（给 LLM 或 UI 使用）
	Rule             string         // 匹配的规则原文（如 "Bash(git:*)"），空表示无规则匹配
	SuggestedPattern string         // 若用户选择"记住"，建议记住的 pattern（如 "git add *"）；空表示无法提取
}

// ---------------------------------------------------------------------------
// Rule — 权限规则
// ---------------------------------------------------------------------------

// RuleBehavior 规则的行为。
type RuleBehavior string

const (
	RuleAllow RuleBehavior = "allow"
	RuleDeny  RuleBehavior = "deny"
	RuleAsk   RuleBehavior = "ask"
)

// RuleScope 规则的持久化范围。
type RuleScope string

const (
	ScopeSession RuleScope = "session" // 仅当前会话，进程结束消失
	ScopeConfig  RuleScope = "config"  // 写入配置文件，跨会话持久化
)

// RuleSource 规则的来源。
type RuleSource string

const (
	SourceConfig  RuleSource = "config"  // 配置文件
	SourceSession RuleSource = "session" // 会话内记忆
	SourceCLI     RuleSource = "cli"     // CLI 参数
)

// Rule 表示一条权限规则。
// 格式：ToolName 或 ToolName(pattern)
// 示例：
//
//	"read_file"            → 工具级：允许/拒绝整个 read_file
//	"Bash(git *)"          → 内容级：匹配以 "git " 开头的命令
//	"write_file(src/**)"   → 内容级：匹配 src/ 下的路径
//
// Bash(...) 自动映射为 shell(...)
type Rule struct {
	Behavior RuleBehavior
	ToolName string
	Pattern  string // 可选的内容匹配模式（glob）
}

// String 返回规则的可读表示。
func (r Rule) String() string {
	if r.Pattern != "" {
		return string(r.Behavior) + ":" + r.ToolName + "(" + r.Pattern + ")"
	}
	return string(r.Behavior) + ":" + r.ToolName
}

// RuleEntry 是带来源信息的规则。
type RuleEntry struct {
	Rule   Rule
	Source RuleSource
	Scope  RuleScope
}

// ---------------------------------------------------------------------------
// UserResponder — 用户交互接口
// ---------------------------------------------------------------------------

// UserChoice 用户的选择结果。
type UserChoice struct {
	Decision      Decision  // allow 或 deny
	RememberScope RuleScope // "" → 不记住；ScopeSession → session 内记住；ScopeConfig → 持久化到配置文件
	Feedback      string    // 可选的用户反馈文本
}

// UserResponder 处理 ask 决策的用户交互。
// 具体实现由 CLI/UI 层提供，Guard 本身不感知 UI。
type UserResponder interface {
	// AskUser 向用户请求决策。
	AskUser(ctx context.Context, toolName string, input json.RawMessage, result DecisionResult) UserChoice

	// AnswerQuestion 向用户发起选择题交互。
	// 阻塞直到用户回答完毕或拒绝。返回 nil, nil 表示用户拒绝。
	AnswerQuestion(ctx context.Context, questions []QuestionPrompt) ([]QuestionResponse, error)
}

// ---------------------------------------------------------------------------
// QuestionPrompt / QuestionResponse — 选择题类型
// ---------------------------------------------------------------------------

// QuestionPrompt 是向用户展示的单个选择题。
type QuestionPrompt struct {
	Question    string                `json:"question"`    // 完整问题，以 ? 结尾
	Header      string                `json:"header"`      // 简短标签，≤12 chars
	Options     []QuestionOptionPrompt `json:"options"`     // 2-4 项，label 唯一
	MultiSelect bool                  `json:"multiSelect"` // 是否多选，默认 false
}

// QuestionOptionPrompt 是选择题的单个选项。
type QuestionOptionPrompt struct {
	Label       string `json:"label"`       // 显示文本，1-5 words
	Description string `json:"description"` // 选项解释
}

// QuestionResponse 是用户对单个问题的回答。
type QuestionResponse struct {
	Question string   `json:"question"` // 问题文本（与 QuestionPrompt.Question 对应）
	Answers  []string `json:"answers"`  // 选中的选项 label 列表；单选时为 1 个元素
}

// ---------------------------------------------------------------------------
// 风险分级
// ---------------------------------------------------------------------------

// PathSafetyLevel 路径的风险等级。
type PathSafetyLevel string

const (
	PathSafe      PathSafetyLevel = "safe"      // 工作目录内，安全
	PathSensitive PathSafetyLevel = "sensitive"  // .git/、shell rc 等，需确认
	PathDangerous PathSafetyLevel = "dangerous"  // 工作目录外 + 敏感文件，高风险
)

// CommandRiskLevel 命令的风险等级。
type CommandRiskLevel string

const (
	RiskLow    CommandRiskLevel = "low"    // 只读/查询命令
	RiskMedium CommandRiskLevel = "medium" // 写入/修改命令
	RiskHigh   CommandRiskLevel = "high"   // 危险命令模式匹配
)

// ToolRiskClass 工具的风险分类。
type ToolRiskClass string

const (
	RiskClassRead    ToolRiskClass = "read"    // 只读：read_file, grep, search_file, ls
	RiskClassWrite   ToolRiskClass = "write"   // 写入：write_file, edit_file
	RiskClassExecute ToolRiskClass = "execute" // 执行：shell
)

// ---------------------------------------------------------------------------
// Guard — 核心接口
// ---------------------------------------------------------------------------

// Guard 是权限守门人的核心接口。
type Guard interface {
	// Check 对工具调用执行权限检查，返回决策结果。
	// 不负责用户交互——如果返回 ask，调用方需通过 UserResponder 获取用户决策。
	Check(ctx context.Context, toolName string, input json.RawMessage) DecisionResult

	// AddRule 追加一条规则到内存（RuleEngine）。
	AddRule(rule Rule, scope RuleScope) error

	// RemoveRule 移除一条规则。
	RemoveRule(rule Rule, scope RuleScope) error

	// ListRules 列出当前生效的规则。
	ListRules() []RuleEntry

	// PersistRule 将规则落盘到项目配置文件（settings.json）。
	// 仅 ScopeConfig 规则需要落盘。
	PersistRule(rule Rule) error

	// SessionAllow 将当前工具的允许决策记入 SessionMemory。
	SessionAllow(toolName string, input json.RawMessage)

	// SessionDeny 将当前工具的拒绝决策记入 SessionMemory。
	SessionDeny(toolName string, input json.RawMessage)

	// ClearSession 清空当前 session 记忆（用于 /reset 等场景）。
	ClearSession()

	// SessionMemoryLen 返回当前 session 记忆中的条目数。
	SessionMemoryLen() int
}
