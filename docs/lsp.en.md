# LSP Diagnostics

Waveloom automatically runs LSP diagnostics after every `edit` / `write`, injecting compile errors and warnings into tool output so the LLM can catch and fix issues immediately.

## How It Works

```
edit / write completes
  → Extract changed file paths
  → Match LSP Server (by file extension)
  → Sync file content to Server (didOpen / didChange)
  → Wait for Server analysis & diagnostics push (2s timeout)
  → Format diagnostics and inject into tool output
  → LLM sees diagnostics in the next turn
```

Diagnostics appear in tool output as:

```
## LSP Diagnostics

### `pkg/lsp/pathutil.go`
L13:5: error: undefined: err
L16:2: error: undefined: abs
```

## Supported Languages

| Language | LSP Server | Probe Command | Note |
|----------|-----------|--------------|------|
| Go | `gopls` | `gopls version` | Built-in, auto-detected |
| Rust | `rust-analyzer` | `rust-analyzer --version` | Built-in, auto-detected |
| TypeScript / JavaScript | `typescript-language-server` | `typescript-language-server --version` | Built-in, requires `npm install -g` |
| C / C++ | `clangd` | `clangd --version` | Built-in, auto-detected |

Other languages are supported via `settings.json` configuration.

## Configuration

### Adding a Custom LSP Server

Add an `lsp.servers` section to `~/.waveloom/settings.json` or `.waveloom/settings.json`:

```json
{
  "lsp": {
    "servers": {
      ".py": {
        "command": "pyright-langserver",
        "args": ["--stdio"]
      },
      ".java": {
        "command": "jdtls"
      }
    },
    "idle_timeout_ms": 300000
  }
}
```

- `servers`: extension → Server config. Command must be in PATH or an absolute path.
- `idle_timeout_ms`: idle timeout before Server is reaped (default 5 minutes, optional).

### Disabling LSP

No configuration needed — Waveloom only activates LSP when a probe detects the server. To disable, simply remove the command from PATH.

## Design Decisions

- **Auto-injected, zero tool calls**: diagnostics are appended to edit/write output automatically, using no LLM tool slots
- **Silent degradation**: uninstalled, timed out, or crashed LSP Servers do not affect editing
- **Eventual consistency**: first edit of a file may return empty diagnostics (async analysis); subsequent edits use cached results
- **Safety pipeline**: diagnostic text passes through Unicode sanitization, injection scanning, external data marking, and 256KB truncation
