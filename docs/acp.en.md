# ACP (Agent Client Protocol)

Waveloom implements the [Agent Client Protocol (ACP) v1](https://agentclientprotocol.com) as an
**Agent**, speaking JSON-RPC 2.0 over stdio (line-delimited messages) to ACP clients such as Zed.
The implementation aligns with the official `agent-client-protocol schema/v1/schema.json` and
negotiates `protocolVersion: 1`.

This document is **Waveloom's ACP compatibility list**: what is declared, what is implemented, and
which official capabilities are not supported. Clients should check this list before integrating.

## Starting the agent

```bash
waveloom acp [options]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--session-dir` | `~/.waveloom/acp-sessions` | Directory for ACP session persistence |
| `--settings` | auto-detected | Explicit path to settings.json |
| `--model` / `--provider` | settings | Override LLM model / provider |
| `--context-limit` | settings → 1M | Context window size in tokens (`1M`/`200k` supported) |
| `--log-level` | `info` | Log level (error/warn/info/debug, written to `~/.waveloom/logs`) |

> ACP is a non-interactive entry point: the sandbox auto-activates (even with
> `sandbox.enabled=false`) and permissions use binary decisions (see below).

## Request methods (Client → Agent)

| Method | Support | Notes |
|--------|:---:|-------|
| `initialize` | ✅ | Negotiates `protocolVersion: 1`; handshake gate — all other methods require it first |
| `session/new` | ✅ | `cwd` + `mcpServers` (stdio/http/sse variants, see MCP section) |
| `session/prompt` | ✅ | text / resource / resource_link content blocks; serial per session — a concurrent prompt returns `-32001` |
| `session/cancel` | ✅ | Cancels the active prompt; no response on success (implemented with notification semantics); invalid params / missing session still return an error; the race with prompt startup is covered by a `cancelPending` flag |
| `$/cancel_request` | ✅ | LSP-style cancellation by requestId (notification, no response); unknown requestId is silently ignored |
| `session/close` | ✅ | Cancels the active prompt, stops per-session MCP, removes from registry |
| `session/list` | ✅ | Returns in-memory + disk-persisted sessions |
| `session/load` | ✅ | Restores a session from disk and replays message history (as session/update notifications) |
| `session/resume` | ✅ | Restores a session from disk without replay |
| `session/delete` | ✅ | Removes from registry + deletes the session file |
| `session/request_permission` | ❌ | Agent → Client direction, **not implemented** (no permission confirmation channel in ACP, see Permissions) |

## Capabilities declared in `initialize`

| Capability | Declared | Notes |
|------------|:---:|-------|
| `agentCapabilities.loadSession` | `true` | `session/load` and `session/resume` supported |
| `promptCapabilities.image` | `false` | Image input not supported |
| `promptCapabilities.audio` | `false` | Audio input not supported |
| `promptCapabilities.embeddedContext` | `true` | Embedded resource blocks and resource_link file reads supported |
| `mcpCapabilities.http` | `true` | `session/new` mcpServers accept `type:"http"` |
| `mcpCapabilities.sse` | `true` | `type:"sse"` is mapped to http (matching Waveloom's existing SSE handling) |
| `mcpCapabilities.stdio` | implicit | ACP v1 requires stdio for All Agents MUST (no declaration field); supported |
| `sessionCapabilities` | resume/close/list/delete | Fully declared |
| `auth` / `authMethods` | empty | No authentication flow; `-32000 AuthRequired` never triggered |
| `agentInfo` | `waveloom` + build version | Implementation identification |

## Notifications (Agent → Client, `session/update` variants)

| Variant | Support | Notes |
|---------|:---:|-------|
| `available_commands_update` | ✅ | Sent after `session/new` with the slash-command palette (see below) |
| `session_info_update` | ✅ | Title sent on the first prompt (truncated to 60 chars) |
| `user_message_chunk` | ✅ | Echoes the user message (with the actual text after resource/resource_link expansion) |
| `agent_message_chunk` | ✅ | Streaming reply (stable messageId) |
| `agent_thought_chunk` | ✅ | Reasoning chain (separate messageId, never mixed with the reply) |
| `plan` | ✅ | Plan entries (defensive mapping; the plan tool is not registered in ACP mode, so it does not occur) |
| `tool_call` | ✅ | Tool start: kind/status/title (with argument description, e.g. `bash: ls -la`)/rawInput/content |
| `tool_call_update` | ✅ | Tool streaming and result: status, content (including diff blocks), locations |
| `usage_update` | ✅ | `used` (compaction-aware current context tokens) / `size` (window capacity); `cost` not populated |

## Tool calls

### ToolKind mapping

| Waveloom tool | ACP ToolKind |
|---------------|--------------|
| `read` | `read` |
| `edit` / `write` | `edit` |
| `bash` | `execute` |
| `web_search` | `search` |
| `web_fetch` | `fetch` |
| `agent` | `think` |
| others | `other` |

### tool_call content items

| Type | Support | Notes |
|------|:---:|-------|
| `content` | ✅ | Text content blocks |
| `diff` | ✅ | edit/write DiffHunks rebuilt as `{path, oldText, newText}` |
| `terminal` | ❌ | Never produced |
| `locations` | ✅ | Target files for edit/write (deduplicated per file, first hunk's start line) |

### Tools registered in ACP mode

`read` / `edit` / `write` / `bash` (background allowed) / `web_fetch` / `web_search` /
`kill_background_task` / `skill` (LLM-invocable) / `agent` (subagent) / `todo_create` / `todo_update`

**Not registered** (interactive tools that depend on a UserResponder would always fail in
non-interactive ACP mode; excluded at the schema level so the LLM never proposes them):
`ask_user_question` / `enter_plan_mode` / `exit_plan_mode`

### Slash commands

ACP mode registers `/help`, `/model`, `/provider`, `/skill` (user-invocable skills).
TUI-overlay commands (`theme`/`locale`/`rewind`/`new`) are not registered.

## Error codes

| Code | Meaning | Notes |
|------|---------|-------|
| `-32700` | Parse error | Oversized lines are recoverable: an error is returned and the loop continues (DoS protection) |
| `-32600` | Invalid request | Includes "initialize required" violations |
| `-32601` | Method not found | |
| `-32602` | Invalid params | |
| `-32603` | Internal error | |
| `-32000` | AuthRequired | Aligned with official schema; never triggered (authMethods empty) |
| `-32001` | SessionBusy | **Waveloom-specific** (official -32001 is unused): concurrent prompt on the same session / duplicate load |
| `-32002` | ResourceNotFound | Aligned with official schema: session not found |

## Behavioral conventions

- **Permissions (binary decisions)**: ACP v1 has no permission-confirmation protocol, so the entry
  point auto-enables `EnableAutoAllow` — the Guard operates in binary mode (ASK → ALLOW; only
  DENY/ALLOW). Deny rules, RiskHigh and PathDangerous hard blocks are preserved (fail-closed baseline).
- **Sandbox**: auto-activated in non-interactive mode (even if disabled in config); if the backend is
  unavailable it warns and degrades by default (binary decisions are unaffected); with
  `failIfUnavailable: true` configured it refuses to start when the backend is unavailable
  (Windows is exempt — platform non-support does not block startup).
- **Context compaction**: same source as the TUI (four-tier watermark compaction);
  `usage_update.size` = context window capacity, `used` = current context tokens (compaction-aware:
  subtracts TokensSaved after compaction, resets to 0 after a Tier-3 summary).
- **Session persistence**: one JSON file per session, written after each completed prompt;
  `session/load` replays history, `session/resume` does not.
- **MaxTurns**: unlimited in ACP mode.
- **Security boundaries**: `sessionId` is restricted to `[A-Za-z0-9_-]` and ≤64 chars (path-traversal
  protection); `resource_link` accepts only `file://` or absolute paths and must resolve inside the
  workspace (prevents arbitrary files from entering the LLM context).
- **MCP**: `mcpServers` is only carried by `session/new` (v1 semantics); stdio/http/sse variants are
  supported; invalid entries (missing command/url, duplicate names) fail the whole request
  (fail-closed); load/resume carry no mcpServers parameter, so restored sessions have no MCP tools.

## Not implemented / degraded

| Official capability | Status | Notes |
|---------------------|:---:|-------|
| Image/audio prompt input | ❌ | Declared `false` in promptCapabilities |
| `session/request_permission` | ❌ | No permission UI; binary decisions instead |
| `usage_update.cost` | ❌ | Field never populated |
| Authentication flow | ❌ | `authMethods` is an empty array |
| tool_call `terminal` content items | ❌ | Never produced |
| `session/cancel` success response | ⚠️ | Implemented with notification semantics (no response on success); error paths still return errors |

## References

- Official protocol spec: <https://agentclientprotocol.com> (schema v1)
- Implementation source: `pkg/acp/` (server / handler / transport / adapter / mcp)
- Entry points: `cmd/waveloom/acp.go`, `cmd/waveloom/acp_command.go`
