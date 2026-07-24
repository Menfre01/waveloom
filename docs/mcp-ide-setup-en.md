# Connecting IDE MCP Server

Waveloom supports connecting to IntelliJ IDEA or VS Code MCP Server via the MCP protocol, leveraging the IDE's code indexing capabilities (symbol lookup, file search, build diagnostics, etc.) as alternatives to shell commands.

## Why connect an IDE?

| Scenario | Shell approach | IDE MCP approach |
|------|-----------|-------------|
| File search | `find` / `rg` (disk scan) | IDE index (milliseconds, auto-excludes `node_modules`) |
| Symbol lookup | `grep` (text match) | PSI/LSP precise semantic lookup |
| Current file | Requires `@` reference | IDE directly provides open file list |

## Supported features

| Feature | IntelliJ IDEA | VS Code |
|------|:---:|:---:|
| Static capability guide (tool usage hints) | ✅ | ✅ |
| Dynamic context injection (open file list) | ✅ | ❌ * |
| Project workspace matching (CWD validation) | ✅ | ✅ |

*VS Code MCP Server does not have a tool for listing all open files, but validates workspace match via `get_workspace_folders`.

## IntelliJ IDEA

**Prerequisites**: IntelliJ IDEA 2025.2+, MCP Server plugin enabled (enabled by default).

### 1. Enable MCP Server

1. Open IDEA → `Settings | Tools | MCP Server`
2. Click `Enable MCP Server`
3. In the `Manual Client Configuration` section, click `Copy Stdio Config`

### 2. Configure Waveloom

Paste the copied configuration into `.mcp.json` in your project root:

```json
{
  "mcpServers": {
    "idea": {
      "type": "stdio",
      "command": "/Applications/IntelliJ IDEA.app/Contents/MacOS/idea",
      "args": ["mcp-server"]
    }
  }
}
```

> **Note**: The `command` and `args` should match IDEA's `Copy Stdio Config` output — paths differ by platform.
>
> - macOS: `/Applications/IntelliJ IDEA.app/Contents/MacOS/idea`
> - Windows: `idea64.exe`
> - Linux: `/usr/local/bin/idea`

### 3. Verify

Start Waveloom and check for the `## IDE Integration — IntelliJ IDEA` section in the system prompt.

---

## VS Code

**Prerequisites**: VS Code + community extension `nabheet.vscode-ide-mcp`.

### 1. Install the extension

```sh
code --install-extension nabheet.vscode-ide-mcp
```

Or search and install `vscode-ide-mcp` from the VS Code Marketplace.

### 2. Configure Waveloom

Add to `.mcp.json` in your project root:

```json
{
  "mcpServers": {
    "vscode": {
      "type": "sse",
      "url": "http://127.0.0.1:9876/mcp"
    }
  }
}
```

The extension listens on `http://127.0.0.1:9876` by default — no additional configuration needed.

### 3. Verify

```sh
curl -s -X POST http://127.0.0.1:9876/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

Start Waveloom and check for the `## IDE Integration — VS Code` section in the system prompt.

---

## How it works

- **Static injection**: On first startup, Waveloom detects connected IDE types and injects tool usage guidance into the system prompt (preserving prefix cache)
- **Dynamic context**: On subsequent turns, Waveloom queries the IDE for current open files and injects them into the user message
- **MCP tool isolation**: IDE tools are registered as `mcp__<server>__<tool>`, not conflicting with Waveloom's built-in tools
