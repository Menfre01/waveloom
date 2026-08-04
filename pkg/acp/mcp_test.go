package acp

import (
	"encoding/json"
	"testing"

	"github.com/Menfre01/waveloom/pkg/mcp"
)

// TestParseMcpServersStdio:无 type 字段 → stdio 变体(默认,All Agents MUST)。
func TestParseMcpServersStdio(t *testing.T) {
	raw := json.RawMessage(`[{
		"name": "filesystem",
		"command": "/usr/local/bin/mcp-fs",
		"args": ["--root", "/tmp"],
		"env": [{"name": "API_KEY", "value": "secret"}, {"name": "EMPTY", "value": ""}]
	}]`)
	configs, err := parseMcpServers(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cfg, ok := configs["filesystem"]
	if !ok {
		t.Fatal("missing server config")
	}
	if cfg.Type != mcp.ServerTypeStdio {
		t.Errorf("type = %q, want stdio", cfg.Type)
	}
	if cfg.Command != "/usr/local/bin/mcp-fs" || len(cfg.Args) != 2 {
		t.Errorf("command/args = %q %v", cfg.Command, cfg.Args)
	}
	if cfg.Env["API_KEY"] != "secret" || cfg.Env["EMPTY"] != "" {
		t.Errorf("env mapping = %v", cfg.Env)
	}
}

// TestParseMcpServersHTTP:type:"http" → http 变体,headers 数组 → map。
func TestParseMcpServersHTTP(t *testing.T) {
	raw := json.RawMessage(`[{
		"type": "http",
		"name": "remote",
		"url": "https://mcp.example.com/sse",
		"headers": [{"name": "Authorization", "value": "Bearer x"}]
	}]`)
	configs, err := parseMcpServers(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cfg := configs["remote"]
	if cfg.Type != mcp.ServerTypeHTTP {
		t.Errorf("type = %q, want http", cfg.Type)
	}
	if cfg.URL != "https://mcp.example.com/sse" {
		t.Errorf("url = %q", cfg.URL)
	}
	if cfg.Headers["Authorization"] != "Bearer x" {
		t.Errorf("headers mapping = %v", cfg.Headers)
	}
}

// TestParseMcpServersSSE:type:"sse" → 映射为 http(Waveloom SSE 处理对齐)。
func TestParseMcpServersSSE(t *testing.T) {
	raw := json.RawMessage(`[{"type":"sse","name":"legacy","url":"https://mcp.example.com/sse"}]`)
	configs, err := parseMcpServers(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg := configs["legacy"]; cfg.Type != mcp.ServerTypeHTTP {
		t.Errorf("sse type = %q, want http (mapped)", cfg.Type)
	}
}

func TestParseMcpServersErrors(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"not an array", `{"name":"x"}`},
		{"missing name", `[{"command":"/bin/true"}]`},
		{"stdio missing command", `[{"name":"x"}]`},
		{"http missing url", `[{"type":"http","name":"x"}]`},
		{"unknown type", `[{"type":"banana","name":"x","url":"http://x"}]`},
		{"duplicate name", `[{"name":"x","command":"/bin/a"},{"name":"x","command":"/bin/b"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseMcpServers(json.RawMessage(tc.raw)); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestParseMcpServersEmpty(t *testing.T) {
	// 空/缺省 → nil,不报错(未配置 MCP)
	configs, err := parseMcpServers(nil)
	if err != nil || configs != nil {
		t.Errorf("nil input: configs=%v err=%v, want nil/nil", configs, err)
	}
	configs, err = parseMcpServers(json.RawMessage("null"))
	if err != nil || configs != nil {
		t.Errorf("null input: configs=%v err=%v, want nil/nil", configs, err)
	}
}
