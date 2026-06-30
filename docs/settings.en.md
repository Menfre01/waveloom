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
| `provider` | `deepseek` or `openai` | `deepseek` |
| `model` | Model name | `deepseek-v4-pro` |
| `base_url` | API endpoint | `https://api.deepseek.com` |
| `timeout` | Request timeout | `600s` |
| `extra_params` | Extra parameters (thinking, reasoning_effort, etc.) | Thinking mode on by default |
| `retry` | Retry policy `{"max_retries":3, "initial_backoff":"1s", "max_backoff":"30s", "multiplier":2.0}` | Default retry policy |
| `headers` | Custom HTTP headers `{"X-Custom": "value"}` | — |

### permissions Configuration

```json
{
  "permissions": {
    "allow": ["read_file", "search_file", "grep", "ls"],
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

### environment Configuration

The agent auto-detects available toolchains at startup. For tools not in PATH or to pin a specific version, configure via `environment.tools`. See [`environment.en.md`](./environment.en.md) for details.

### Tool Timeout Configuration

| Field | Description | Default |
|-------|-------------|---------|
| `tool_timeout` | Single tool execution timeout (Go Duration format, e.g. `"10m"` / `"600s"` / `"0s"`, 0 to disable) | `"10m"` |

## CLI Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--model` | Model name | `deepseek-v4-pro` |
| `--system-prompt` | Custom system prompt | Built-in prompt |
| `--max-turns N` | Maximum turns, 0 = unlimited | `0` (unlimited) |
| `--context-limit 1M` | Context window size, supports `1M` / `200k` / raw number | `1M` |
| `--theme auto/dark/light` | Theme, auto detects terminal background | `auto` |
| `--verbose` | Log detailed output to `.waveloom/waveloom.log` | Off |
| `--bypass-permissions` | Skip all permission checks | Off |
| `--tool-timeout D` | Single tool execution timeout (Go Duration format, e.g. `10m` / `600s` / `0s`, 0 to disable) | `10m` |
| `--resume ID` | Resume a specific session | — |
| `--continue` | Resume the most recent session | — |
| `--settings PATH` | Specify config file path | `.waveloom/settings.json` |
| `--version` | Show version | — |

Priority: **CLI flags > `.waveloom/settings.json` (project) > `~/.waveloom/settings.json` (global)**
