package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ---------------------------------------------------------------------------
// CLI — MCP 子命令处理
// ---------------------------------------------------------------------------

// RunMCPCommand 处理 waveloom mcp <subcommand> 命令。
// args 是 mcp 后面的参数（不含 "mcp" 本身）。
// 返回 true 表示成功，false 表示出错（错误信息已写入 stderr）。
func RunMCPCommand(args []string) bool {
	if len(args) == 0 {
		printMCPUsage()
		return false
	}

	homeDir, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()

	var ok bool
	switch args[0] {
	case "add":
		ok = runAdd(args[1:], homeDir, cwd)
	case "add-json":
		ok = runAddJSON(args[1:], homeDir, cwd)
	case "list":
		ok = runList(homeDir, cwd)
	case "get":
		ok = runGet(args[1:], homeDir, cwd)
	case "remove":
		ok = runRemove(args[1:], homeDir, cwd)
	default:
		fmt.Fprintf(os.Stderr, "Unknown mcp subcommand: %s\n\n", args[0])
		printMCPUsage()
		return false
	}
	return ok
}

func printMCPUsage() {
	fmt.Fprint(os.Stderr, `Usage: waveloom mcp <command> [args]

Commands:
  add       Add an MCP server
  add-json  Add an MCP server from JSON
  list      List configured MCP servers
  get       Show details for a server
  remove    Remove a server

Examples:
  waveloom mcp add --transport http github https://api.githubcopilot.com/mcp/
  waveloom mcp add --transport stdio db -- npx -y @bytebase/dbhub --dsn "postgresql://..."
  waveloom mcp add-json weather '{"type":"http","url":"https://api.weather.com/mcp"}'
  waveloom mcp list
  waveloom mcp get github
  waveloom mcp remove github
`)
}

// ---------------------------------------------------------------------------
// mcp add
// ---------------------------------------------------------------------------

func runAdd(args []string, homeDir, cwd string) bool {
	var transportType string
	var scope string
	var headers []string
	var envVars []string

	// 解析 flag-style 参数（在 server name 和 -- 之前）
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--transport":
			i++
			if i < len(args) {
				transportType = args[i]
			}
		case "--scope":
			i++
			if i < len(args) {
				scope = args[i]
			}
		case "--header":
			i++
			if i < len(args) {
				headers = append(headers, args[i])
			}
		case "--env":
			i++
			if i < len(args) {
				envVars = append(envVars, args[i])
			}
		default:
			// 遇到非 flag 参数，假定是 server name 或 --
			goto parseRest
		}
		i++
	}

parseRest:
	remaining := args[i:]

	if transportType == "" {
		transportType = "stdio"
	}

	switch transportType {
	case "http", "sse":
		if len(remaining) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: waveloom mcp add --transport http [--scope <s>] <name> <url>\n")
			return false
		}
		name := remaining[0]
		url := remaining[1]

		config := ServerConfig{
			Type: ServerTypeHTTP,
			URL:  url,
		}
		if len(headers) > 0 {
			config.Headers = parseHeaderFlags(headers)
		}

		return addServer(name, config, scope, homeDir, cwd)

	case "stdio":
		if len(remaining) < 3 {
			fmt.Fprintf(os.Stderr, "Usage: waveloom mcp add --transport stdio [--scope <s>] <name> -- <command> [args...]\n")
			return false
		}
		name := remaining[0]

		// 跳过 --
		if remaining[1] != "--" {
			fmt.Fprintf(os.Stderr, "Error: stdio transport requires -- before the command\n")
			return false
		}
		commandAndArgs := remaining[2:]

		config := ServerConfig{
			Type:    ServerTypeStdio,
			Command: commandAndArgs[0],
		}
		if len(commandAndArgs) > 1 {
			config.Args = commandAndArgs[1:]
		}
		if len(envVars) > 0 {
			config.Env = parseEnvFlags(envVars)
		}

		return addServer(name, config, scope, homeDir, cwd)

	default:
		fmt.Fprintf(os.Stderr, "Unknown transport type: %s (use 'http' or 'stdio')\n", transportType)
		return false
	}
}

func addServer(name string, config ServerConfig, scope, homeDir, cwd string) bool {
	if scope == "" {
		scope = "local"
	}

	var err error
	switch scope {
	case "project":
		err = AddServerToMCPJSON(cwd, name, config)
	case "local", "user":
		err = AddServerToWaveloomJSON(homeDir, cwd, scope, name, config)
	default:
		fmt.Fprintf(os.Stderr, "Unknown scope: %s (use 'local', 'project', or 'user')\n", scope)
		return false
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error adding server: %v\n", err)
		return false
	}
	fmt.Printf("Added MCP server %q (scope: %s)\n", name, scope)
	return true
}

// ---------------------------------------------------------------------------
// mcp add-json
// ---------------------------------------------------------------------------

func runAddJSON(args []string, homeDir, cwd string) bool {
	var scope string

	i := 0
	for i < len(args) {
		if args[i] == "--scope" {
			i++
			if i < len(args) {
				scope = args[i]
			}
		} else {
			break
		}
		i++
	}

	remaining := args[i:]
	if len(remaining) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: waveloom mcp add-json [--scope <s>] <name> '<json>'\n")
		return false
	}

	name := remaining[0]
	jsonStr := remaining[1]

	var config ServerConfig
	if err := json.Unmarshal([]byte(jsonStr), &config); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing JSON: %v\n", err)
		return false
	}

	return addServer(name, config, scope, homeDir, cwd)
}

// ---------------------------------------------------------------------------
// mcp list
// ---------------------------------------------------------------------------

func runList(homeDir, cwd string) bool {
	configs := ListServerConfigs(homeDir, cwd)
	if len(configs) == 0 {
		fmt.Println("No MCP servers configured.")
		return true
	}

	for name, sources := range configs {
		fmt.Printf("%s:\n", name)
		for _, src := range sources {
			cfg := src.Config
			switch cfg.Type {
			case ServerTypeStdio, "":
				fmt.Printf("  [%s] stdio: %s %s\n", src.Source, cfg.Command, strings.Join(cfg.Args, " "))
			case ServerTypeHTTP, ServerTypeSSE:
				fmt.Printf("  [%s] http: %s\n", src.Source, cfg.URL)
			}
			if len(cfg.Env) > 0 {
				fmt.Printf("    env: %v\n", cfg.Env)
			}
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// mcp get
// ---------------------------------------------------------------------------

func runGet(args []string, homeDir, cwd string) bool {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: waveloom mcp get <name>\n")
		return false
	}
	name := args[0]

	sources := ListServerConfigs(homeDir, cwd)[name]
	if len(sources) == 0 {
		fmt.Printf("Server %q not found.\n", name)
		return false
	}

	fmt.Printf("%s:\n", name)
	for _, src := range sources {
		cfg := src.Config
		fmt.Printf("  Source: %s\n", src.Source)
		fmt.Printf("  Type: %s\n", cfg.Type)
		switch cfg.Type {
		case ServerTypeStdio, "":
			fmt.Printf("  Command: %s\n", cfg.Command)
			fmt.Printf("  Args: %v\n", cfg.Args)
		case ServerTypeHTTP, ServerTypeSSE:
			fmt.Printf("  URL: %s\n", cfg.URL)
			fmt.Printf("  Headers: %v\n", cfg.Headers)
		}
		if len(cfg.Env) > 0 {
			fmt.Printf("  Env: %v\n", cfg.Env)
		}
		if cfg.Timeout > 0 {
			fmt.Printf("  Timeout: %dms\n", cfg.Timeout)
		}
		fmt.Println()
	}
	return true
}

// ---------------------------------------------------------------------------
// mcp remove
// ---------------------------------------------------------------------------

func runRemove(args []string, homeDir, cwd string) bool {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: waveloom mcp remove <name>\n")
		return false
	}
	name := args[0]

	if err := RemoveServer(homeDir, cwd, name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return false
	}
	fmt.Printf("Removed MCP server %q\n", name)
	return true
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

// parseHeaderFlags 解析 "Key: Value" 格式的 header 标志。
func parseHeaderFlags(headers []string) map[string]string {
	result := make(map[string]string, len(headers))
	for _, h := range headers {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) == 2 {
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return result
}

// parseEnvFlags 解析 "KEY=value" 格式的环境变量标志。
func parseEnvFlags(envVars []string) map[string]string {
	result := make(map[string]string, len(envVars))
	for _, e := range envVars {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		}
	}
	return result
}
