package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

// ---------------------------------------------------------------------------
// Registry 接口
// ---------------------------------------------------------------------------

// Registry 管理所有已注册的工具。
type Registry interface {
	Register(t Tool) // 注册工具；重复名称会 panic（编程错误）
	List() []ToolSpec
	Get(name string) (Tool, bool)
	Execute(ctx context.Context, name string, input json.RawMessage) (*ToolResult, error)
}

// ---------------------------------------------------------------------------
// 默认实现
// ---------------------------------------------------------------------------

// registry 是 Registry 接口的默认实现。
type registry struct {
	tools map[string]Tool // 工具名 → ErasedTool（实现 Tool 接口）
	specs []ToolSpec      // 预构建的 ToolSpec 列表（避免每次 List 重建）
}

// NewRegistry 创建一个空的 Registry。
func NewRegistry() *registry {
	return &registry{
		tools: make(map[string]Tool),
	}
}

// Register 接受 Tool（即 ErasedTool），由外部通过 Wrap() 构造。
// 重复名称会 panic（编程错误）。
func (r *registry) Register(t Tool) {
	if _, exists := r.tools[t.Name()]; exists {
		panic(fmt.Sprintf("tool %q already registered", t.Name()))
	}
	r.tools[t.Name()] = t
	r.specs = append(r.specs, ToolSpec{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters:  t.Schema(),
	})
}

// List 返回所有已注册工具的 ToolSpec 列表。
func (r *registry) List() []ToolSpec {
	return r.specs
}

// Get 按名查找工具。
func (r *registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Execute 查找并执行指定工具。
// 未知工具名返回 error；工具级错误通过 ToolResult.Error 返回。
func (r *registry) Execute(ctx context.Context, name string, input json.RawMessage) (*ToolResult, error) {
	tool, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("tool %q not registered", name)
	}
	// 参数校验 + json.Unmarshal 由 ErasedTool 内部完成
	return tool.Execute(ctx, input)
}

// ---------------------------------------------------------------------------
// NewDefaultRegistry — 注册所有内置工具
// ---------------------------------------------------------------------------

// NewDefaultRegistry 创建包含所有内置工具的 Registry。
func NewDefaultRegistry() Registry {
	r := NewRegistry()
	r.Register(Wrap(&ReadFile{}))
	r.Register(Wrap(&WriteFile{}))
	r.Register(Wrap(&EditFile{}))
	r.Register(Wrap(&Shell{}))
	r.Register(Wrap(&WebFetch{}))
	r.Register(Wrap(&AskUserQuestion{}))
	r.Register(Wrap(&EnterPlanMode{}))
	r.Register(Wrap(&ExitPlanMode{}))
	return r
}
