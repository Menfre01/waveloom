# Configuration Reference

On first run, Waveloom generates a default config at `.waveloom/settings.json`. Config file locations (highest priority first):

1. CLI `--settings` flag
2. `.waveloom/settings.json` (project root)
3. `~/.waveloom/settings.json` (global)

## settings.json

Minimal config:

```json
{
  "llm": {
    "api_key": "sk-your-deepseek-key"
  }
}
```

### llm Configuration

| Field | Description | Default |
|-------|-------------|---------|
| `api_key` | DeepSeek API Key, falls back to `LLM_API_KEY` env var when empty | — |
| `provider` | `deepseek`, `kimi`, or `openai` | `deepseek` |
| `model` | Model name | `deepseek-v4-pro` |
| `base_url` | API endpoint | `https://api.deepseek.com` |
| `timeout` | Request timeout | `600s` |
| `extra_params` | Extra parameters (thinking, reasoning_effort, etc.) | Thinking mode on by default |
| `retry` | Retry policy `{"max_retries":3, "initial_backoff":"1s", "max_backoff":"30s", "multiplier":2.0}` | Default retry policy |
| `sub_model` | Sub-agent default model (explore subagent uses this model for code search and discovery, ~2x cheaper) | Auto-paired (pro → flash) |
| `profiles` | Multi-provider configuration, keyed by provider name (e.g., `"kimi"`, `"openai"`). Each profile may contain `api_key`, `model`, `sub_model`, `base_url`, `extra_params`. Used with `--provider` CLI flag. Provider-independent fields (`timeout`, `retry`, `headers`) are inherited from the top level | — |

```json
{
  "llm": {
    "provider": "deepseek",
    "profiles": {
      "kimi": {
        "api_key": "sk-your-kimi-key",
        "model": "kimi-k2",
        "base_url": "https://api.moonshot.cn/v1"
      },
      "openai": {
        "api_key": "sk-your-openai-key",
        "model": "gpt-5",
        "base_url": "https://api.openai.com/v1"
      }
    }
  }
}
```

### permissions Configuration

```json
{
  "permissions": {
    "allow": ["read_file", "web_fetch", "bash(go build *)", "bash(go test *)"],
    "deny":  ["bash(rm -rf /*)"],
    "ask":   ["write_file", "edit_file"]
  }
}
```

Rule format: `ToolName` or `ToolName(pattern)`, e.g., `bash(ls *)` matches all commands starting with `ls `.

### compaction Configuration

| Field | Description | Default |
|-------|-------------|---------|
| `tier1_threshold` | Tier 1 (Snip) trigger threshold | `0.6` (60%) |
| `tier2_threshold` | Tier 2 (Prune) trigger threshold | `0.8` (80%) |
| `tier3_threshold` | Tier 3 (Summarize) trigger threshold | `0.95` (95%) |
| `protection_zone_tokens` | Protection zone token count, supports `"8K"` / `8000` | `8000` |
| `context_limit_tokens` | Model context limit, supports `"1M"` / `1000000` | `1000000` |

### hooks Configuration

The Hook system is compatible with the Claude Code Hooks protocol, injecting external scripts into the tool execution lifecycle. Typical use cases: command rewriting (e.g., RTK token optimization), result processing, event notification.

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "~/.claude/hooks/rtk-rewrite.sh",
            "timeout": 5000
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "curl -s -X POST 'http://localhost:8080/log' -d @-"
          }
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "notify-slack.sh"
          }
        ]
      }
    ]
  }
}
```

#### Configuration hierarchy (highest priority first, same-event merge)

1. `.claude/settings.local.json` — Local override (do not commit)
2. `.waveloom/settings.json` — Waveloom project-level
3. `.claude/settings.json` — Claude Code project-level
4. `~/.waveloom/settings.json` — Waveloom user-level
5. `~/.claude/settings.json` — Claude Code user-level

#### Event types

| Event | Trigger | Sync/Async | Can rewrite |
|-------|---------|-----------|-------------|
| `PreToolUse` | Before tool execution | Sync | Yes (params) |
| `PostToolUse` | After tool execution | Sync | Yes (result) |
| `Notification` | Lifecycle events (task start/complete/error) | Async | No |
| `Stop` | Agent Loop termination | Sync | No |

#### Matcher rules

| Syntax | Example | Description |
|--------|---------|-------------|
| Empty string | `""` | Match all tools |
| Exact name | `"Bash"` | Exact tool name match |
| Prefix wildcard | `"Read*"` | Match tools starting with Read |
| Multi-pattern | `"Bash\|Read"` | `\|` delimited, match any |

#### Hook entry fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | No | `"command"` (default) |
| `command` | string | Yes | Executable script path or shell command |
| `timeout` | number | No | Timeout in milliseconds, default 30000 |

Hook scripts receive JSON event context via stdin and return JSON results via stdout. Exit code 0 = apply rewrite, 1 = pass through, 2 = block execution. See [Claude Code Hooks docs](https://code.claude.com/docs/en/hooks) for details.

### environment Configuration

The agent auto-detects available toolchains at startup. For tools not in PATH or to pin a specific version, configure via `environment.tools`. See [`environment.en.md`](./environment.en.md) for details.

### lsp LSP Diagnostics Configuration

After every `edit` / `write`, LSP diagnostics run automatically. See [`lsp.en.md`](./lsp.en.md).

| Field | Description | Default |
|------|-------------|--------|
| `lsp.servers` | File extension → LSP Server config. Key is extension (e.g. `".py"`), value is `{"command": "...", "args": ["..."]}`. User-defined entries override built-in defaults | — |
| `lsp.idle_timeout_ms` | Idle timeout before server is reaped (ms) | `300000` (5 min) |

Example: adding Python and Java LSP Servers:

```json
{
  "lsp": {
    "servers": {
      ".py": { "command": "pyright-langserver", "args": ["--stdio"] },
      ".java": { "command": "jdtls" }
    }
  }
}
```

### web_search Configuration

The `web_search` tool defaults to DuckDuckGo — no configuration required. For better result quality, switch to the Brave Search API via an environment variable:

```bash
export BRAVE_API_KEY="your-brave-api-key"
```

Falls back to DuckDuckGo when not set.

### Tool Timeout Configuration

| Field | Description | Default |
|-------|-------------|---------|
| `tool_timeout` | Single tool execution timeout (Go Duration format, e.g. `"10m"` / `"600s"` / `"0s"`, 0 to disable) | `"5m"` |

### session Configuration

| Field | Description | Default |
|-------|-------------|---------|
| `session.dir` | Session storage directory (relative or absolute path). Priority: `settings.json session.dir` > `WAVELOOM_SESSION_DIR` env var > `~/.waveloom/<project>/sessions/` | `~/.waveloom/<project>/sessions/` |

```json
{
  "session": {
    "dir": ".waveloom/sessions"
  }
}
```

### UI Configuration

| Field | Description | Default |
|-------|-------------|---------|
| `theme` | Theme mode: `auto` (detect terminal background automatically), `dark`, `light`, `darkcolorblind`, `lightcolorblind`. Can be changed and persisted at runtime via `/theme` command | `auto` |
| `locale` | UI language: `zh-CN` (Chinese), `en-US` (English), `auto` (detect from `LANG` env var). Priority: `--locale` CLI > `settings.json` > `LANG` | `auto` |

```json
{
  "theme": "dark",
  "locale": "zh-CN"
}
```

### Plan Mode Configuration

| Field | Description | Default |
|-------|-------------|---------|
| `plans_directory` | Plan file storage directory (relative paths are relative to the settings file directory) | `~/.waveloom/plans/` |

### sandbox Configuration

| Field | Description | Default |
|-------|-------------|---------|
| `enabled` | Enable the sandbox in TUI mode; auto-activated by `--bypass-permissions` / one-shot / non-interactive ACP (no need to set true) | `false` |
| `failIfUnavailable` | Refuse to start when the backend is missing (e.g. bwrap not installed) | `false` |
| `allowUnsandboxedCommands` | Hint escape (add to excludedCommands) when a sandboxed command fails | `true` |
| `excludedCommands` | Escape hatch list (prefix/exact/wildcard); matching commands run unsandboxed but still pass through Guard | `[]` |
| `env` | Env vars injected inside the sandbox (tool-agnostic mechanism); values support path prefixes (`~/` home, `//`/`/` absolute, `./` workspace-relative), anything else is injected verbatim; keys matching the credential-strip list are ignored (strip wins) | `{}` |
| `network.mode` | Network policy: `off` (fully offline) / `on` (direct); `proxy` is v2, not implemented. Defaults to `on` (2025-09 decision: non-interactive entries need networked tools out of the box; exfiltration risk is mitigated by `denyRead` / `credentials.files`) | `on` |
| `network.allowedDomains` | Domain allowlist (v2 proxy placeholder, not active yet) | `[]` |
| `filesystem.allowWrite` | Extra writable paths (`//abs` absolute, `~/` home, `./` or bare name = project root); root and mask-conflicting paths are rejected | `[]` |
| `filesystem.allowRead` | **Deprecated** (2026-09): parsed only to warn and ignore; no longer has any effect. Use `denyRead` / `credentials.files` instead | `[]` |
| `filesystem.denyRead` | Masked (unreadable) paths. Nothing is masked by default — configure explicitly (recommended list below) | `[]` |
| `capabilities.keep` | Kernel capabilities re-added after `--cap-drop ALL` (e.g. `net_raw` for ping) | `[]` |
| `credentials.files` | Credential mask paths (strongly recommended when network is `on`) | `[]` |
| `credentials.envVars` | Extra env vars to strip, layered on built-in globs (`*TOKEN*` / `*_API_KEY` etc.) | `[]` |

```json
{
  "sandbox": {
    "enabled": false,
    "excludedCommands": ["docker *"],
    "env": {
      "GOPATH": "./.waveloom-gopath",
      "GOMODCACHE": "./.waveloom-gomodcache",
      "GOCACHE": "./.waveloom-gocache",
      "GOPROXY": "https://proxy.golang.org,direct"
    },
    "network": { "mode": "off" },
    "filesystem": { "allowWrite": ["~/.cache"], "denyRead": ["~/.aws"] },
    "credentials": { "files": ["~/.ssh"], "envVars": ["GH_TOKEN"] }
  }
}
```

### Env Injection Inside the Sandbox (`env`)

`sandbox.env` is a **tool-agnostic** mechanism: the listed env vars are injected when commands start inside the sandbox. Its typical use is redirecting build-tool caches into the writable workspace area — under the read-only root, host caches (e.g. `~/go/pkg/mod`) are not writable, so go/npm/cargo would fail or degrade with warnings.

- **Value path semantics**: `./` → workspace-relative (`"./.waveloom-gomodcache"` → `<project root>/.waveloom-gomodcache`); `~/` → home; `//` or `/` → absolute; anything else (URLs etc.) is injected verbatim (e.g. `GOPROXY`)
- **Go example**: pointing `GOPATH` / `GOMODCACHE` / `GOCACHE` at the workspace makes the first in-sandbox build download dependencies over the network (one-time, cached persistently), after which offline builds work; host builds are unaffected
- **npm/cargo etc.**: configure `npm_config_cache` / `CARGO_HOME` / `PIP_CACHE_DIR` the same way
- **Security**: keys matching the credential-strip rules (built-in globs `*TOKEN*` / `*_API_KEY` etc. or `credentials.envVars`) are **ignored** — strip wins, preventing the config from re-injecting stripped secrets into the sandbox

### Masking Strategy (2026-09: nothing masked by default)

The sandbox masks **no paths by default** (`~/.ssh`, `~/.aws`, keychains, etc. are all
readable), aligned with Claude Code / Codex. Credential protection is configured
explicitly via `filesystem.denyRead` / `credentials.files`; when network is `on` without
any mask, a startup warning is emitted ("network can exfiltrate unmasked files; configure
masking for stronger protection").

**Fixed built-in masks** (write-protection / escape prevention, cannot be removed):

| Path | Purpose |
|------|---------|
| `<project root>/.git/hooks` | Persistent-injection guard (hooks execute on write — escape); Linux: tmpfs overlay, Seatbelt: deny read+write |
| `/var/run/docker.sock` | Escape prevention (docker can mount host root); macOS also masks `~/.docker/run/docker.sock` |

**Recommended credential masking config** (strongly advised when network is `on`):

```json
{
  "sandbox": {
    "network": { "mode": "on" },
    "filesystem": {
      "denyRead": [
        "~/.waveloom/settings.json", "~/.git-credentials", "~/.config/git/credentials",
        "~/.bashrc", "~/.bash_profile", "~/.profile", "~/.zshrc", "~/.zshenv",
        "~/.npmrc", "~/.netrc", "~/.docker/config.json",
        "~/.config/gh/hosts.yml", "~/.mcp.json", "~/.claude/settings.json",
        "~/.aws", "~/.ssh", "~/.kube/config", "~/.config/gcloud", "~/.gnupg",
        "~/.pgpass", "~/.config/containers/auth.json", "~/.env",
        ".waveloom/settings.json", ".env"
      ]
    },
    "credentials": {
      "files": ["~/.ssh", "~/.aws/credentials"],
      "envVars": ["GH_TOKEN", "NPM_TOKEN", "AWS_ACCESS_KEY_ID"]
    }
  }
}
```

macOS: additionally consider `~/Library/Keychains`, `~/Library/HTTPStorages`,
`~/Library/Cookies`, `~/Library/Application Support/Google/Chrome`, `.../Firefox`,
`.../Microsoft/Edge` (keychains / cookies / browser sessions can be read and exfiltrated
when network is on).

> Note: env var stripping (built-in globs: `*TOKEN*` / `*_API_KEY` / `AWS_*` / `GH_*` etc.)
> is an independent defense that is always active, regardless of path masking config.

## CLI Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--model` | Model name | `deepseek-v4-pro` |
| `--system-prompt` | Custom system prompt | Built-in prompt |
| `--max-turns N` | Maximum turns, 0 = unlimited | `0` (unlimited) |
| `--context-limit 1M` | Context window size, supports `1M` / `200k` / raw number | `1M` |
| `--theme auto/dark/light` | Theme, auto detects terminal background | `auto` |
| `--locale zh-CN/en-US/auto` | UI language, auto detects from `LANG` env var | `auto` |
| `--provider NAME` | Switch LLM provider (requires matching profile in `profiles`) | — |
| `--log-level level` | Log level (error/warn/info/debug) | `info` |
| `--bypass-permissions` | one-shot (direct terminal input) / ACP: ASK → ALLOW by default (binary decision), keeping deny rules and high-risk hard blocks; one-shot **piped input** requires this flag, otherwise write/bash degrade to deny; TUI: enables the binary decision (no more prompt dialogs) | Off (default on for one-shot terminal input / ACP) |
| `--sandbox-network off/on` | Sandbox network mode, overrides `network.mode` in `settings.json` (on: credential masking recommended) | From config (default `on`) |
| `--tool-timeout D` | Single tool execution timeout (Go Duration format, e.g. `10m` / `600s` / `0s`, 0 to disable) | `5m` |
| `--resume ID` | Resume a specific session | — |
| `--continue` | Resume the most recent session | — |
| `--settings PATH` | Specify config file path | `.waveloom/settings.json` |
| `--version` | Show version | — |

Priority: **CLI flags > `.waveloom/settings.json` (project) > `~/.waveloom/settings.json` (global)**
