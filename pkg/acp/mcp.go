package acp

import (
	"encoding/json"
	"fmt"

	"github.com/Menfre01/waveloom/pkg/mcp"
)

// parseMcpServers 将 session/new 的 mcpServers 参数(ACP v1 格式,数组)
// 转换为 mcp.ServerConfig map(server name → config)。
//
// ACP McpServer 是 discriminated union(schema.json):
//   - 无 type 字段 → stdio 变体{name, command, args[], env[]}(默认,All Agents MUST)
//   - type:"http" → {name, url, headers[]}
//   - type:"sse" → {name, url, headers[]}(Waveloom 映射为 http,对齐现有 SSE 处理)
//
// env/headers 的 {name, value} 数组转换为 map[string]string。
// 非法条目(缺 command/url、重复 name)整体报错(fail-closed)。
func parseMcpServers(raw json.RawMessage) (map[string]mcp.ServerConfig, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var servers []AcpMcpServer
	if err := json.Unmarshal(raw, &servers); err != nil {
		return nil, fmt.Errorf("mcpServers must be an array: %w", err)
	}

	configs := make(map[string]mcp.ServerConfig, len(servers))
	for _, s := range servers {
		if s.Name == "" {
			return nil, fmt.Errorf("mcpServers entry missing name")
		}
		if _, dup := configs[s.Name]; dup {
			return nil, fmt.Errorf("duplicate mcpServers entry %q", s.Name)
		}

		cfg := mcp.ServerConfig{
			Name:    s.Name,
			Env:     nameValuesToMap(s.Env),
			Headers: nameValuesToMap(s.Headers),
		}
		switch s.Type {
		case "", "stdio":
			cfg.Type = mcp.ServerTypeStdio
			cfg.Command = s.Command
			cfg.Args = s.Args
			if cfg.Command == "" {
				return nil, fmt.Errorf("mcpServers %q: stdio server requires command", s.Name)
			}
		case "http":
			cfg.Type = mcp.ServerTypeHTTP
			cfg.URL = s.URL
			if cfg.URL == "" {
				return nil, fmt.Errorf("mcpServers %q: http server requires url", s.Name)
			}
		case "sse":
			// SSE deprecated,映射为 http(对齐 Waveloom 现有处理)
			cfg.Type = mcp.ServerTypeHTTP
			cfg.URL = s.URL
			if cfg.URL == "" {
				return nil, fmt.Errorf("mcpServers %q: sse server requires url", s.Name)
			}
		default:
			return nil, fmt.Errorf("mcpServers %q: unknown type %q", s.Name, s.Type)
		}
		configs[s.Name] = cfg
	}
	return configs, nil
}

// nameValuesToMap 将 {name, value} 数组转换为 map。
func nameValuesToMap(items []AcpNameValue) map[string]string {
	if len(items) == 0 {
		return nil
	}
	m := make(map[string]string, len(items))
	for _, item := range items {
		if item.Name != "" {
			m[item.Name] = item.Value
		}
	}
	return m
}
