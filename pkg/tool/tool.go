package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// TypedTool[P] — 工具实现者的类型安全接口
// ---------------------------------------------------------------------------

// TypedTool 是工具实现者关心的类型安全接口。
// P 是工具的参数结构体，例如 ReadFileParams。
type TypedTool[P any] interface {
	Name() string
	Description() string
	Schema() json.RawMessage // JSON Schema for input parameters
	ConcurrentSafe() bool    // true → 可并行；false → 必须串行
	Execute(ctx context.Context, params P) (*ToolResult, error)
}

// ---------------------------------------------------------------------------
// Tool — 类型擦除后的统一接口
// ---------------------------------------------------------------------------

// Tool 是 Registry 存储和 Loop 调用的统一接口。
// 每个 TypedTool[P] 通过 Wrap() 包装为 Tool，json.Unmarshal 由 ErasedTool 统一处理。
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	ConcurrentSafe() bool
	Execute(ctx context.Context, raw json.RawMessage) (*ToolResult, error)
}

// UserInteractionTool 是可选接口，由需要阻塞式用户交互的工具实现。
// Loop 在执行前检查此接口；若返回 true，则工具不经过权限检查和普通执行路径，
// 改为通过 UserResponder 进行阻塞式交互。
type UserInteractionTool interface {
	RequiresUserInteraction() bool
}

// ToolWithPrompt 是可选接口，由需要注入使用指南的工具实现。
// Prompt() 返回工具的使用指南（When to Use / NOT / 示例等），
// 由 Loop 在启动时注入 system message，与 Description 分离。
// Description 保持简短（~30 token），Prompt 可包含详细规则。
type ToolWithPrompt interface {
	Prompt() string
}

// ---------------------------------------------------------------------------
// TypedStreamableTool[P] — 可选的流式工具接口
// ---------------------------------------------------------------------------

// TypedStreamableTool 由支持增量输出推送的工具实现。
// Wrap() 自动检测并桥接到 ErasedTool，json.Unmarshal 集中处理。
type TypedStreamableTool[P any] interface {
	SupportsStreaming() bool
	ExecuteStreaming(ctx context.Context, params P, chunkCb func(string)) (*ToolResult, error)
}

// StreamableTool 是类型擦除后的流式工具接口。
// Loop 在执行前通过 type assertion 检测此接口，决定走 streaming 路径还是普通 Execute。
type StreamableTool interface {
	SupportsStreaming() bool
	ExecuteStreaming(ctx context.Context, raw json.RawMessage, chunkCb func(string)) (*ToolResult, error)
}

// ---------------------------------------------------------------------------
// ErasedTool + Wrap — 类型擦除
// ---------------------------------------------------------------------------

// ErasedTool 包装 TypedTool[P]，实现 Tool 接口。
// 在 Execute 中统一完成 json.Unmarshal，工具实现者永远不需要手写。
type ErasedTool struct {
	name                    string
	desc                    string
	prompt                  string
	schema                  json.RawMessage
	concurrentSafe          bool
	requiresUserInteraction bool
	execute                 func(ctx context.Context, raw json.RawMessage) (*ToolResult, error)

	// streaming 支持（可选，由 Wrap 自动检测 TypedStreamableTool[P]）
	supportsStreaming bool
	executeStreaming  func(ctx context.Context, raw json.RawMessage, chunkCb func(string)) (*ToolResult, error)
}

func (e *ErasedTool) Name() string            { return e.name }
func (e *ErasedTool) Description() string     { return e.desc }
func (e *ErasedTool) Prompt() string          { return e.prompt }
func (e *ErasedTool) Schema() json.RawMessage { return e.schema }
func (e *ErasedTool) ConcurrentSafe() bool    { return e.concurrentSafe }
func (e *ErasedTool) RequiresUserInteraction() bool { return e.requiresUserInteraction }
func (e *ErasedTool) Execute(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
	return e.execute(ctx, raw)
}

// SupportsStreaming 报告该工具是否支持增量输出推送。
func (e *ErasedTool) SupportsStreaming() bool { return e.supportsStreaming }

// ExecuteStreaming 执行工具并将增量输出通过 chunkCb 推送。
func (e *ErasedTool) ExecuteStreaming(ctx context.Context, raw json.RawMessage, chunkCb func(string)) (*ToolResult, error) {
	return e.executeStreaming(ctx, raw, chunkCb)
}

// Wrap 将 TypedTool[P] 包装为 Tool。
// 这是唯一的 json.Unmarshal 调用的位置 — 所有工具实现者不再需要手写反序列化。
func Wrap[P any](t TypedTool[P]) *ErasedTool {
	var requiresUI bool
	if uit, ok := any(t).(UserInteractionTool); ok {
		requiresUI = uit.RequiresUserInteraction()
	}
	var prompt string
	if twp, ok := any(t).(ToolWithPrompt); ok {
		prompt = twp.Prompt()
	}
	et := &ErasedTool{
		name:                    t.Name(),
		desc:                    t.Description(),
		prompt:                  prompt,
		schema:                  t.Schema(),
		concurrentSafe:          t.ConcurrentSafe(),
		requiresUserInteraction: requiresUI,
		execute: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			var p P
			if err := json.Unmarshal(raw, &p); err != nil {
				return &ToolResult{
					Error: &ToolError{
						Class:   ErrorClassRecoverable,
						Kind:    ErrKindInvalidArgs,
						Message: fmt.Sprintf("invalid params for %s: %v", t.Name(), err),
						Cause:   err,
					},
				}, nil
			}
			return t.Execute(ctx, p)
		},
	}

	// 自动检测 TypedStreamableTool[P]，桥接 json.Unmarshal
	if st, ok := any(t).(TypedStreamableTool[P]); ok && st.SupportsStreaming() {
		et.supportsStreaming = true
		et.executeStreaming = func(ctx context.Context, raw json.RawMessage, chunkCb func(string)) (*ToolResult, error) {
			var p P
			if err := json.Unmarshal(raw, &p); err != nil {
				return &ToolResult{
					Error: &ToolError{
						Class:   ErrorClassRecoverable,
						Kind:    ErrKindInvalidArgs,
						Message: fmt.Sprintf("invalid params for %s: %v", t.Name(), err),
						Cause:   err,
					},
				}, nil
			}
			return st.ExecuteStreaming(ctx, p, chunkCb)
		}
	}

	return et
}

// ---------------------------------------------------------------------------
// ToolResult
// ---------------------------------------------------------------------------

// ToolResult 封装工具执行结果。
type ToolResult struct {
	Content    string    // 文本输出（发送给 LLM）
	Meta       ToolMeta  // 元数据（供 Loop 和其他组件使用）
	Error      *ToolError
	ToolCallID string    // LLM 工具调用 ID，由 Loop 填充（工具实现者不感知）
}

// IsError 返回工具执行是否产生了错误。
func (r *ToolResult) IsError() bool {
	return r.Error != nil
}

// ---------------------------------------------------------------------------
// ToolMeta
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// DiffLine / DiffLineKind / DiffHunk — 统一 diff 结构化数据
// ---------------------------------------------------------------------------

// DiffLineKind 表示统一 diff 中一行的类型。
type DiffLineKind string

const (
	DiffAdd    DiffLineKind = "+" // 新增行
	DiffDel    DiffLineKind = "-" // 删除行
	DiffCtx    DiffLineKind = " " // 上下文行（未改动）
	DiffHeader DiffLineKind = "@" // hunk 头
)

// DiffLine 表示统一 diff 中的一行。
type DiffLine struct {
	Kind    DiffLineKind
	Content string // 不含前缀的实际内容
	OldNum  int    // 旧文件行号（0 = 不适用）
	NewNum  int    // 新文件行号（0 = 不适用）
}

// DiffHunk 表示一个 diff 块（一段连续的变更 + 上下文）。
type DiffHunk struct {
	OldStart int        // 旧文件起始行号（1-based）
	OldCount int        // 旧文件覆盖行数
	NewStart int        // 新文件起始行号（1-based）
	NewCount int        // 新文件覆盖行数
	Heading  string     // hunk 头部函数上下文（如 "func main() {"）
	Lines    []DiffLine

	// NoNewlineAtEOF 表示 hunk 末尾的旧文件或新文件不以换行结尾。
	// 渲染时输出 "\ No newline at end of file" 标记（符合 POSIX unified diff 规范）。
	NoNewlineAtEOF bool
}

// Stats 返回该 hunk 的增删统计。
func (h DiffHunk) Stats() (add, del int) {
	for _, l := range h.Lines {
		switch l.Kind {
		case DiffAdd:
			add++
		case DiffDel:
			del++
		}
	}
	return
}

// ---------------------------------------------------------------------------
// ToolMeta
// ---------------------------------------------------------------------------

// ToolMeta 携带结构化元数据。
type ToolMeta struct {
	Duration  time.Duration // 执行耗时
	FilePath  string        // 操作涉及的文件路径（如有）
	ExitCode  int           // shell 命令退出码（-1 表示不适用）
	LineCount int           // 输出行数
	ByteCount int           // 输出字节数

	// BackgroundTaskID 后台任务 ID（非空表示命令在后台执行）。
	BackgroundTaskID string
	// LogPath 输出日志路径（文件 fd 模式下的输出文件路径）。
	LogPath string

	// DiffHunks 为 edit_file / write_file 等工具提供的结构化 diff，供 TUI 渲染带行号的统一 diff 视图。
	// nil 表示不适用（非编辑类工具或发生错误）。
	DiffHunks []DiffHunk
}

// ---------------------------------------------------------------------------
// ToolError
// ---------------------------------------------------------------------------

// ErrorClass 区分错误的可恢复性。
type ErrorClass int

const (
	ErrorClassRecoverable ErrorClass = iota // LLM 可以自行修正
	ErrorClassFatal                         // 必须终止
)

// ToolError 封装工具执行错误。
type ToolError struct {
	Class   ErrorClass // 分类
	Kind    string     // "file_not_found", "permission_denied", "invalid_args" ...
	Message string     // 人类可读描述，会返回给 LLM
	Cause   error      // 原始 error，不对外暴露
}

func (e *ToolError) Error() string { return e.Message }
func (e *ToolError) Unwrap() error { return e.Cause }

// 预定义错误 Kind

// Recoverable — LLM 可以修正
const (
	ErrKindFileNotFound          = "file_not_found"
	ErrKindNoResults             = "no_results"
	ErrKindInvalidArgs           = "invalid_args"
	ErrKindCommandFailed         = "command_failed"
	ErrKindCommandNotFound       = "command_not_found"
	ErrKindCommandPermission     = "command_permission_denied"
	ErrKindTimeout               = "timeout"
	ErrKindNotDir                = "not_dir"
	ErrKindBinaryFile            = "binary_file"
	ErrKindMultipleMatch         = "multiple_matches"
	ErrKindNoMatch               = "no_match"
	ErrKindLargeFile             = "large_file"
)

// Fatal — 不可恢复
const (
	ErrKindPermissionDenied = "permission_denied"
	ErrKindDiskFull         = "disk_full"
	ErrKindUnknownTool      = "unknown_tool"
	ErrKindSecurityViolation = "security_violation"
)

// ---------------------------------------------------------------------------
// ToolSpec
// ---------------------------------------------------------------------------

// ToolSpec 是 Tool 的轻量描述，发送给 LLM 做 function calling。
// 字段和 JSON tag 对齐 OpenAI Chat Completions / DeepSeek API 的 tools[].function 格式。
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema 参数定义
	Prompt      string          `json:"-"`          // 工具使用指南（注入 system message，不进入 function description）
}
