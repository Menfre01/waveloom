// Package subagent 提供子 agent 委派功能的核心类型和工具。
// AgentTool 实现位于 agent.go，事件类型在本文件。
package subagent

import "github.com/Menfre01/waveloom/pkg/agentloop"

// ---------------------------------------------------------------------------
// SubagentStart — 子 agent 开始执行
// ---------------------------------------------------------------------------

// SubagentStart 表示子 agent 开始执行。
type SubagentStart struct {
	Turn       int    // 当前 turn 序号
	ToolCallID string // 父级 agent 工具调用 ID
	AgentType  string // "fork" | "evaluate" | "Explore" | "verification"
	Prompt     string // 委派的任务描述
	InheritCtx bool   // true = fork 热启动，false = cold 冷启动
	Model      string // 子 agent 模型，空 = 继承主模型
}

func (SubagentStart) TurnEvent() {}

// ---------------------------------------------------------------------------
// SubagentEventKind — 子 agent 内部事件类型
// ---------------------------------------------------------------------------

// SubagentEventKind 区分子 agent 内部事件类型。
type SubagentEventKind int

const (
	SubagentText       SubagentEventKind = iota // agent 输出文本增量
	SubagentToolStart                            // 子 agent 开始执行工具
	SubagentToolResult                           // 子 agent 工具执行结果
)

// SubagentEvent 聚合子 agent 内部产生的一次事件。
type SubagentEvent struct {
	Turn       int
	ToolCallID string
	Kind       SubagentEventKind

	// SubagentText 时使用
	TextDelta string

	// SubagentToolStart / SubagentToolResult 时使用
	ToolName   string
	ToolArgs   string
	ToolResult string
	ToolDurMs  int64
	ToolError  string
}

func (SubagentEvent) TurnEvent() {}

// ---------------------------------------------------------------------------
// SubagentEnd — 子 agent 执行完毕
// ---------------------------------------------------------------------------

// SubagentEnd 表示子 agent 执行完毕。
type SubagentEnd struct {
	Turn             int
	ToolCallID       string
	TotalTurns       int
	PromptTokens     int // ↑ 输入 token
	CompletionTokens int // ↓ 输出 token
	DurationMs       int64
	Error            string // 非空表示异常终止
}

func (SubagentEnd) TurnEvent() {}

// Compile-time check: these types implement agentloop.TurnEvent.
var (
	_ agentloop.TurnEvent = SubagentStart{}
	_ agentloop.TurnEvent = SubagentEvent{}
	_ agentloop.TurnEvent = SubagentEnd{}
)
