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
| `sub_model` | Sub-agent default model, auto-paired for DeepSeek (pro → flash) | Auto-paired |
| `base_url` | API endpoint | `https://api.deepseek.com` |
| `timeout` | Request timeout | `600s` |
| `extra_params` | Extra parameters (thinking, reasoning_effort, etc.) | Thinking mode on by default |
| `retry` | Retry policy `{"max_retries":3, "initial_backoff":"1s", "max_backoff":"30s", "multiplier":2.0}` | Default retry policy |
| `mode` | `"normal"` or `"advisor"`. In advisor mode the main Agent defaults to `sub_model` (secondary model) to reduce token costs, temporarily switching to `model` (primary model) inside plan mode for deep reasoning. Only effective when `sub_model` is non-empty and differs from `model`. **Startup-only, cannot be changed at runtime via `/model`** | `"normal"` |
| `headers` | Custom HTTP headers `{"X-Custom": "value"}` | — |
| `profiles` | Multi-provider configuration, keyed by provider name (e.g., `"kimi"`, `"openai"`). Each profile may contain `api_key`, `model`, `sub_model`, `base_url`, `extra_params`. Used with `--provider` CLI flag. Provider-independent fields (`timeout`, `retry`, `headers`, `mode`) are inherited from the top level | — |

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
| `--verbose` | Log detailed output to `.waveloom/waveloom.log` | Off |
| `--bypass-permissions` | Skip all permission checks | Off |
| `--tool-timeout D` | Single tool execution timeout (Go Duration format, e.g. `10m` / `600s` / `0s`, 0 to disable) | `10m` |
| `--resume ID` | Resume a specific session | — |
| `--continue` | Resume the most recent session | — |
| `--settings PATH` | Specify config file path | `.waveloom/settings.json` |
| `--version` | Show version | — |

Priority: **CLI flags > `.waveloom/settings.json` (project) > `~/.waveloom/settings.json` (global)**
