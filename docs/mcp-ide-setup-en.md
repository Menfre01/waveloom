# Connecting IDE MCP Servers

Waveloom can connect to IntelliJ IDEA or VS Code MCP Servers via the MCP protocol, leveraging IDE code indexing capabilities (symbol lookup, file search, build diagnostics, etc.) as an alternative to shell commands.

## Why connect an IDE?

| Scenario | Shell approach | IDE MCP approach |
|----------|---------------|-----------------|
| File search | `find` / `rg` (disk scan) | IDE index (milliseconds, auto-excludes `node_modules`) |
| Symbol lookup | `grep` (text match) | PSI / LSP precise semantic lookup |
| Build verification | `go build` / `make` | IDE incremental compilation + diagnostics |
| Current file | Requires `@` reference | IDE provides open file list directly |

## Supported features

| Feature | IntelliJ IDEA | VS Code |
|---------|:---:|:---:|
| Static capability guide (tool usage hints) | ✅ | ✅ |
| Dynamic context injection (open files) | ✅ | ❌ [1] |
| Project workspace matching (CWD validation) | ✅ | ✅ [2] |

[1] VS Code MCP Server does not expose a reliable "list open editors" tool.  
[2] Static guide only; dynamic open-file injection does not apply.

---

## IntelliJ IDEA

**Prerequisites**: IntelliJ IDEA 2025.2+, MCP Server plugin enabled (default).

### 1. Enable MCP Server

1. Open IDEA → `Settings | Tools | MCP Server`
2. Click `Enable MCP Server`
3. In the `Manual Client Configuration` section, click `Copy Stdio Config`

### 2. Configure Waveloom

Paste the copied configuration into your project's `.mcp.json`:

```json
{
  "mcpServers": {
    "idea": {
      "type": "stdio",
      "command": "/path/to/idea",
      "args": ["mcp-server"]
    }
  }
}
```

> **Note**: The `command` and `args` should match IDEA's `Copy Stdio Config` output. Paths vary by platform:
>
> - macOS: `/Applications/IntelliJ IDEA.app/Contents/MacOS/idea`
> - Windows: `idea64.exe`
> - Linux: `/usr/local/bin/idea`

### 3. Verify

Start Waveloom and check that the system prompt contains the `## IDE Integration — IntelliJ IDEA` section.

---

## VS Code

**Prerequisites**: VS Code + the community extension `nabheet.vscode-ide-mcp`.

### 1. Install the extension

```sh
code --install-extension nabheet.vscode-ide-mcp
```

Or install `vscode-ide-mcp` from the VS Code Marketplace.

### 2. Configure Waveloom

Add to your project's `.mcp.json`:

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

The extension listens on `http://127.0.0.1:9876` by default. No additional configuration is required.

### 3. Verify

```sh
curl -s -X POST http://127.0.0.1:9876/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

Start Waveloom and check that the system prompt contains the `## IDE Integration — VS Code` section.

---

## How it works

- **Static injection**: On startup, Waveloom detects connected IDE types and injects tool usage guidance into the system prompt (prefix-cache safe).
- **Dynamic context**: Each turn, Waveloom queries the IDE for the current open file list and injects it into the user message — no system prompt modification, preserving the prefix cache.
- **MCP tool isolation**: IDE tools are registered as `mcp__<server>__<tool>`, avoiding conflicts with Waveloom built-in tools.
