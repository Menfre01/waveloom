// Package hook 实现 Hooks 系统。
//
// 支持的事件类型（一期）：
// - PreToolUse：工具执行前改写参数
// - PostToolUse：工具执行后改写结果
// - Notification：异步事件通知
// - Stop：loop 终止通知
//
// 配置格式兼容 settings.json 的 hooks 字段。现有 hook 脚本无需修改即可在 Waveloom 中使用。
package hook

import "encoding/json"

// ── 事件类型 ──

// EventType 表示 hook 事件类型。
type EventType string

const (
	EventPreToolUse       EventType = "PreToolUse"
	EventPostToolUse      EventType = "PostToolUse"
	EventNotification     EventType = "Notification"
	EventStop             EventType = "Stop"
	EventUserPromptSubmit EventType = "UserPromptSubmit"
	EventSessionStart     EventType = "SessionStart"
	EventSessionEnd       EventType = "SessionEnd"
	EventSubagentStop     EventType = "SubagentStop"
	EventPreCompact       EventType = "PreCompact"
)

// ── Hook 配置 ──

// HookConfig 是单个 hook 条目的配置。
// settings.json 中 hooks 字段的单个条目。
type HookConfig struct {
	Matcher string     `json:"matcher,omitempty"` // 匹配器（空=全部）
	Hooks   []HookItem `json:"hooks"`
}

// HookItem 是单个 hook 的执行定义。
type HookItem struct {
	Type    string `json:"type,omitempty"`    // "command"（默认）
	Command string `json:"command"`           // 脚本路径或 shell 命令
	Timeout int    `json:"timeout,omitempty"` // 超时毫秒数，默认 30000
}
type EventContext struct {
	SessionID        string          `json:"session_id"`
	TranscriptPath   string          `json:"transcript_path"`
	Cwd              string          `json:"cwd"`
	PermissionMode   string          `json:"permission_mode,omitempty"`
	HookEventName    string          `json:"hook_event_name"`
	ToolName         string          `json:"tool_name,omitempty"`
	ToolUseID        string          `json:"tool_use_id,omitempty"`        // PreToolUse: tool call ID
	ToolInput        json.RawMessage `json:"tool_input,omitempty"`         // PreToolUse: 工具参数
	ToolResponse     json.RawMessage `json:"tool_response,omitempty"`      // PostToolUse: {content, exitCode}
	NotificationType string          `json:"notification_type,omitempty"`   // Notification
	Message          string          `json:"message,omitempty"`             // Notification/Stop
	StopHookActive   *bool           `json:"stop_hook_active,omitempty"`   // Stop: 前一个 Stop hook 是否已激活
}

// ToolResponseContent 是 PostToolUse 的 tool_response 内容。
type ToolResponseContent struct {
	Content  string `json:"content"`
	ExitCode int    `json:"exitCode"`
}

// ── Hook 结果 ──

// HookOutput 是 hook 脚本 stdout 返回的 JSON 结构。
// 同时支持 hookSpecificOutput（新格式）和 legacy decision/reason（旧格式）。
type HookOutput struct {
	// Common fields（所有 hook 事件均可返回）
	Continue       *bool  `json:"continue,omitempty"`
	StopReason     string `json:"stopReason,omitempty"`
	SuppressOutput *bool  `json:"suppressOutput,omitempty"`

	// Legacy format（PreToolUse / UserPromptSubmit / Stop）
	Decision string `json:"decision,omitempty"` // "approve" | "block"
	Reason   string `json:"reason,omitempty"`

	// Preferred format
	HookSpecificOutput HookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

// HookSpecificOutput 是 hook 的特定输出（推荐格式）。
type HookSpecificOutput struct {
	HookEventName              string          `json:"hookEventName"`
	PermissionDecision         string          `json:"permissionDecision,omitempty"`         // "allow" / "deny" / "ask"
	PermissionDecisionReason   string          `json:"permissionDecisionReason,omitempty"`
	UpdatedInput               json.RawMessage `json:"updatedInput,omitempty"`               // PreToolUse：改写后的参数（Waveloom 扩展）
	UpdatedResult              string          `json:"updatedResult,omitempty"`              // PostToolUse：改写后的结果（Waveloom 扩展）
	AdditionalContext          string          `json:"additionalContext,omitempty"`          // SessionStart/UserPromptSubmit：注入上下文
}

// HookResult 是 hook 执行后的处理结果。
type HookResult struct {
	ModifiedInput  json.RawMessage // PreToolUse：改写后的 tool_input
	ModifiedResult string          // PostToolUse：改写后的 result
	Denied         bool            // 是否被拒绝执行
	DenyReason     string          // 拒绝原因
	ShouldStop     bool            // Stop hook：是否阻止终止
	StopReason     string          // Stop hook：停止原因
}

// ── 默认值 ──

const DefaultHookTimeoutMs = 30000

// ── 配置加载 ──

// SettingsFile 是 settings.json 中 hooks 相关的结构。
type SettingsFile struct {
	Hooks map[string][]HookConfig `json:"hooks"`
}

// LoadFromSettings 从 settings.json 内容解析 hooks 配置，按事件类型分组。
// 返回的 map key 为事件类型字符串（如 "PreToolUse"），value 为该事件的配置列表。
func LoadFromSettings(raw []byte) (map[EventType][]HookConfig, error) {
	var sf SettingsFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return nil, err
	}

	result := make(map[EventType][]HookConfig)
	for key, configs := range sf.Hooks {
		et := EventType(key)
		result[et] = configs
	}
	return result, nil
}

// MergeConfigs 合并多个 hooks 配置源（后出现的覆盖先出现的）。
// 注意：同事件类型采用整体替换语义（非 append），// 即 .waveloom/settings.json 中的 PreToolUse 配置会完全替换 .claude/settings.json 中的 PreToolUse 配置。
// 如需在多个配置源中追加 hook，请在同一个文件中列出所有 hook 条目。
func MergeConfigs(sources ...map[EventType][]HookConfig) map[EventType][]HookConfig {
	result := make(map[EventType][]HookConfig)
	for _, src := range sources {
		for et, configs := range src {
			result[et] = configs
		}
	}
	return result
}
