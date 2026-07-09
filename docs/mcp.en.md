# MCP (Model Context Protocol)

Waveloom includes a full MCP client that connects to external MCP servers and auto-discovers their tools.

## Configuration Sources

Waveloom loads MCP server configs from multiple sources on startup, merged by priority (higher overrides lower):

| Priority | Source | Description |
|----------|--------|-------------|
| 6 (lowest) | Claude Desktop | Auto-discover MCP servers installed in Claude Desktop |
| 5 | `~/.claude.json` → `mcpServers` | Claude Code user-level config |
| 4 | `~/.claude.json` → `projects.<cwd>` | Claude Code project-level config |
| 3 | `~/.waveloom.json` → `mcpServers` | Waveloom user-level config |
| 2 | `~/.waveloom.json` → `projects.<cwd>` | Waveloom project-level config |
| 1 (highest) | `.mcp.json` | Project root config file |

> **Note**: Same-name servers are overridden by higher-priority configs. E.g. if both `.mcp.json` and Claude Desktop define `pencil`, the `.mcp.json` config wins.

## CLI Management

```bash
# Add a stdio server (local CLI program)
waveloom mcp add --transport stdio [--scope user|local] <name> -- <command> [args...]

# Add an HTTP server (remote service)
waveloom mcp add --transport http [--scope user|local] <name> <url>

# Add from JSON (batch or complex config)
waveloom mcp add-json <name> '<json>'

# List all configured servers
waveloom mcp list

# Show details for a server
waveloom mcp get <name>

# Remove a server
waveloom mcp remove <name>
```

### Examples

```bash
# Add local Pencil design tool (stdio)
waveloom mcp add --transport stdio --scope local pencil -- \
  /Applications/Pencil.app/Contents/Resources/app.asar.unpacked/out/mcp-server-darwin-arm64 \
  --app desktop

# Add a remote HTTP server
waveloom mcp add --transport http --scope user my-api https://api.example.com/mcp

# With custom headers
waveloom mcp add --transport http --header "Authorization: Bearer token123" my-api https://api.example.com/mcp

# With environment variables
waveloom mcp add --transport stdio --env "NODE_ENV=production" my-tool -- npx my-mcp-server
```

### scope Parameter

| Value | Written to | Scope |
|-------|-----------|-------|
| `user` (default) | `~/.waveloom.json` | Global, all projects |
| `local` | `.mcp.json` | Current project only |

## Config File Format

### `.mcp.json` / `~/.waveloom.json`

```json
{
    "mcpServers": {
        "pencil": {
            "type": "stdio",
            "command": "/path/to/mcp-server",
            "args": ["--app", "desktop"],
            "env": {
                "NODE_ENV": "production"
            }
        },
        "my-api": {
            "type": "http",
            "url": "https://api.example.com/mcp",
            "headers": {
                "Authorization": "Bearer token123"
            }
        }
    }
}
```

### `~/.claude.json` (Claude Code compatible)

```json
{
    "mcpServers": {
        "github": {
            "type": "http",
            "url": "https://api.githubcopilot.com/mcp/"
        }
    },
    "projects": {
        "/path/to/project": {
            "mcpServers": {
                "local-server": {
                    "type": "stdio",
                    "command": "node",
                    "args": ["server.js"]
                }
            }
        }
    }
}
```

## Environment Variable Expansion

`${VAR}` and `${VAR:-default}` in config values are automatically expanded:

```json
{
    "type": "http",
    "url": "https://api.example.com/mcp",
    "headers": {
        "Authorization": "Bearer ${MCP_API_KEY}"
    }
}
```

## Registration & Tool Naming

- After connecting, tools are discovered via the `tools/list` protocol
- Each tool is registered in Waveloom's global tool registry with the naming pattern: `mcp__<server>__<tool>`
- E.g. Pencil's `batch_design` → `mcp__pencil__batch_design`
- The LLM sees all tools (built-in + MCP) in every request and chooses which to invoke

## Troubleshooting

```bash
# View MCP connection logs
waveloom --verbose "test" 2>&1 | grep "\[mcp\]"

# Verify a server is registered
waveloom mcp list

# Inspect a specific server
waveloom mcp get <name>
```

Common errors:

| Error | Cause | Fix |
|-------|-------|-----|
| `executable file not found` | Command path doesn't exist or not in `$PATH` | Use absolute path or install the command |
| `connection refused` | HTTP server not running or wrong port | Check URL and port |
| `4xx/5xx` | Auth failure or server internal error | Check headers/token or server logs |
