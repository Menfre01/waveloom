package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// Registry 接口
// ---------------------------------------------------------------------------

// Registry 管理所有已注册的工具。
type Registry interface {
	Register(t Tool) // 注册工具;重复名称会 panic(编程错误)
	List() []ToolSpec
	Get(name string) (Tool, bool)
	Execute(ctx context.Context, name string, input json.RawMessage) (*ToolResult, error)
	IsStreamable(name string) bool
	ExecuteStreaming(ctx context.Context, name string, input json.RawMessage, chunkCb func(string)) (*ToolResult, error)
	// FormatToolPrompts 返回所有 ToolWithPrompt 工具的 C1 使用指南。
	// 由 system prompt 构建器调用,实现“注册什么工具就注入什么指南”的按需组装。
	FormatToolPrompts() string
}

// ---------------------------------------------------------------------------
// 默认实现
// ---------------------------------------------------------------------------

// registry 是 Registry 接口的默认实现。
// 支持分层(可选 parent):child 的 Get/List 合并父级,本地注册 shadow 父级同名工具。
// 所有方法并发安全(MCP 工具由异步 goroutine 注册,与 Loop 并发读取)。
type registry struct {
	mu     sync.RWMutex
	parent Registry // 可选父级(共享内置工具);nil 为根注册表
	tools  map[string]Tool
	specs  []ToolSpec // 本地预构建的 ToolSpec 列表
}

// NewRegistry 创建一个空的根注册表。
func NewRegistry() *registry {
	return &registry{
		tools: make(map[string]Tool),
	}
}

// NewChildRegistry 创建共享父级的分层注册表。
// 用途:per-session 隔离(如 ACP 每个 session 的 MCP 工具只对自身可见;
// session 关闭时丢弃 child 即天然反注册,无需修改父级)。
// shadow 语义:本地注册与父级同名的工具时,List 以本地为准(父级同名条目被遮蔽)。
func NewChildRegistry(parent Registry) Registry {
	return &registry{
		parent: parent,
		tools:  make(map[string]Tool),
	}
}

// Register 接受 Tool(即 ErasedTool),由外部通过 Wrap() 构造。
// 本地重复名称会 panic(编程错误);与父级同名 → shadow(允许)。
func (r *registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name()]; exists {
		panic(fmt.Sprintf("tool %q already registered", t.Name()))
	}
	r.tools[t.Name()] = t
	desc := t.Description()
	spec := ToolSpec{
		Name:        t.Name(),
		Parameters:  t.Schema(),
	}
	if twp, ok := t.(ToolWithPrompt); ok {
		spec.Prompt = twp.Prompt()
	}
	spec.Description = desc
	r.specs = append(r.specs, spec)
}

// List 返回所有可见工具的 ToolSpec 列表(本地 + 父级,本地 shadow 父级同名)。
func (r *registry) List() []ToolSpec {
	r.mu.RLock()
	localSpecs := r.specs
	r.mu.RUnlock()

	if r.parent == nil {
		return localSpecs
	}

	// 合并:本地优先(本地 shadow 父级同名),父级工具追加在后
	merged := make([]ToolSpec, 0, len(localSpecs)+8)
	seen := make(map[string]bool, len(localSpecs))
	for _, spec := range localSpecs {
		seen[spec.Name] = true
		merged = append(merged, spec)
	}
	for _, spec := range r.parent.List() {
		if seen[spec.Name] {
			continue // 被本地 shadow
		}
		merged = append(merged, spec)
	}
	return merged
}

// FormatToolPrompts 返回所有 ToolWithPrompt 工具的 C1 使用指南,
// 由 system prompt 构建器按需注入。仅收集 spec.Prompt 非空的工具。
func (r *registry) FormatToolPrompts() string {
	var parts []string
	for _, spec := range r.List() {
		if spec.Prompt == "" {
			continue
		}
		parts = append(parts, spec.Prompt)
	}
	return strings.Join(parts, "\n\n")
}

// Get 按名查找工具(本地优先,parent 兜底)。
func (r *registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	t, ok := r.tools[name]
	r.mu.RUnlock()
	if ok {
		return t, true
	}
	if r.parent != nil {
		return r.parent.Get(name)
	}
	return nil, false
}

// Execute 查找并执行指定工具。
// 未知工具名返回 error;工具级错误通过 ToolResult.Error 返回。
func (r *registry) Execute(ctx context.Context, name string, input json.RawMessage) (*ToolResult, error) {
	tool, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("tool %q not registered", name)
	}
	// 参数校验 + json.Unmarshal 由 ErasedTool 内部完成
	return tool.Execute(ctx, input)
}

// IsStreamable 报告指定工具是否支持增量输出推送。
func (r *registry) IsStreamable(name string) bool {
	tool, ok := r.Get(name)
	if !ok {
		return false
	}
	if st, ok := tool.(StreamableTool); ok {
		return st.SupportsStreaming()
	}
	return false
}

// ExecuteStreaming 执行支持流式输出的工具,增量输出通过 chunkCb 推送。
func (r *registry) ExecuteStreaming(ctx context.Context, name string, input json.RawMessage, chunkCb func(string)) (*ToolResult, error) {
	tool, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("tool %q not registered", name)
	}
	st, ok := tool.(StreamableTool)
	if !ok || !st.SupportsStreaming() {
		return nil, fmt.Errorf("tool %q does not support streaming", name)
	}
	return st.ExecuteStreaming(ctx, input, chunkCb)
}
