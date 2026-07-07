package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/Menfre01/waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// MCPToolProxy — 将 MCP 工具适配为 Waveloom TypedTool
// ---------------------------------------------------------------------------

// MCPToolProxy 将 MCP Server 的单个工具适配为 Waveloom 的 TypedTool[map[string]any]。
// 工具名格式: mcp__<server_name>__<tool_name>
type MCPToolProxy struct {
	client     *Client
	serverName string
	toolDef    ToolDef
}

// NewMCPToolProxy 创建一个 MCP 工具代理。
func NewMCPToolProxy(client *Client, toolDef ToolDef) *MCPToolProxy {
	return &MCPToolProxy{
		client:     client,
		serverName: client.ServerName(),
		toolDef:    toolDef,
	}
}

// Name 返回 Waveloom 工具名（带 mcp__ 前缀）。
func (p *MCPToolProxy) Name() string {
	return fmt.Sprintf("mcp__%s__%s", sanitizeName(p.serverName), sanitizeName(p.toolDef.Name))
}

// Description 返回工具描述。
func (p *MCPToolProxy) Description() string {
	desc := p.toolDef.Description
	if desc == "" {
		desc = fmt.Sprintf("MCP tool %q from server %q", p.toolDef.Name, p.serverName)
	}
	// 标注来源 server
	return fmt.Sprintf("[MCP:%s] %s", p.serverName, desc)
}

// Schema 返回 MCP 工具定义的 inputSchema。
func (p *MCPToolProxy) Schema() json.RawMessage {
	return p.toolDef.InputSchema
}

// ConcurrentSafe 返回 true，MCP 工具默认可并行调用。
func (p *MCPToolProxy) ConcurrentSafe() bool {
	return true
}

// Execute 将参数转发到 MCP Server，返回执行结果。
func (p *MCPToolProxy) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
	startTime := time.Now()

	mcpResult, err := p.client.CallTool(ctx, p.toolDef.Name, params)
	if err != nil {
		return &tool.ToolResult{
			Content: fmt.Sprintf("MCP tool %q error: %v", p.toolDef.Name, err),
			Meta:    tool.ToolMeta{Duration: time.Since(startTime)},
			Error: &tool.ToolError{
				Class:   tool.ErrorClassRecoverable,
				Kind:    tool.ErrKindCommandFailed,
				Message: fmt.Sprintf("MCP tool %q: %v", p.toolDef.Name, err),
				Cause:   err,
			},
		}, nil
	}

	result := toToolResult(mcpResult)
	result.Meta.Duration = time.Since(startTime)
	return result, nil
}

// ---------------------------------------------------------------------------
// 名称清理
// ---------------------------------------------------------------------------

// sanitizeName 清理名称中的非法字符，仅保留 [a-zA-Z0-9_.-]。
// 对标 MCP 工具名规范（允许点号）。
func sanitizeName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	result := b.String()
	// 去除连续下划线
	for strings.Contains(result, "__") {
		result = strings.ReplaceAll(result, "__", "_")
	}
	// 去除首尾下划线
	result = strings.Trim(result, "_")
	if result == "" {
		return "unknown"
	}
	return result
}
