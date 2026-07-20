# 配置参考

Waveloom 首次运行在 `.waveloom/settings.json` 生成默认配置。配置文件位置（优先级从高到低）：

1. CLI `--settings` 参数
2. `.waveloom/settings.json`（项目根目录）
3. `~/.waveloom/settings.json`（全局）

## settings.json

最简配置：

```json
{
  "llm": {
    "api_key": "sk-your-deepseek-key"
  }
}
```

### llm 配置

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `api_key` | DeepSeek API Key，为空时回退 `LLM_API_KEY` 环境变量 | — |
| `provider` | `deepseek`、`kimi` 或 `openai` | `deepseek` |
| `model` | 模型名 | `deepseek-v4-pro` |
| `sub_model` | 子代理默认模型，仅 DeepSeek 下自动配对（pro → flash） | 自动配对 |
| `base_url` | API 地址 | `https://api.deepseek.com` |
| `timeout` | 请求超时 | `600s` |
| `extra_params` | 额外参数（thinking、reasoning_effort 等） | 思考模式默认开启 |
| `retry` | 重试策略 `{"max_retries":3, "initial_backoff":"1s", "max_backoff":"30s", "multiplier":2.0}` | 默认重试策略 |
| `mode` | `"normal"` 或 `"advisor"`。Advisor 模式下主 Agent 默认用 `sub_model`（次模型）以降低 token 成本，仅在 plan mode 内临时切换为 `model`（主模型）深度推理。仅在 `sub_model` 非空且不等于 `model` 时生效。**启动时生效，运行时无法通过 `/model` 切换** | `"normal"` |
| `headers` | 自定义 HTTP 请求头 `{"X-Custom": "value"}` | — |
| `profiles` | 多 Provider 配置，以 provider 名为键（如 `"kimi"`、`"openai"`）。每个 profile 可包含 `api_key`、`model`、`sub_model`、`base_url`、`extra_params`。配合 `--provider` CLI 参数切换。Provider 无关字段（`timeout`、`retry`、`headers`、`mode`）从顶层继承 | — |

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


### permissions 配置

```json
{
  "permissions": {
    "allow": ["read_file", "web_fetch", "bash(go build *)", "bash(go test *)"],
    "deny":  ["bash(rm -rf /*)"],
    "ask":   ["write_file", "edit_file"]
  }
}
```

规则格式：`工具名` 或 `工具名(匹配模式)`，如 `bash(ls *)` 匹配所有以 `ls ` 开头的命令。

### compaction 上下文压缩配置

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `tier1_threshold` | Tier 1（Snip）触发阈值 | `0.6`（60%） |
| `tier2_threshold` | Tier 2（Prune）触发阈值 | `0.8`（80%） |
| `tier3_threshold` | Tier 3（Summarize）触发阈值 | `0.95`（95%） |
| `protection_zone_tokens` | 保护区 Token 数，支持 `"8K"` / `8000` | `8000` |
| `context_limit_tokens` | 模型上下文上限，支持 `"1M"` / `1000000` | `1000000` |


### hooks 配置

Hook 系统兼容 Claude Code Hooks 协议，在工具执行生命周期中注入外部脚本。典型用途：命令改写（如 RTK token 优化）、结果处理、事件通知。

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

#### 配置层级（优先级从高到低，同事件 merge）

1. `.claude/settings.local.json` — 本地覆盖（不提交版本控制）
2. `.waveloom/settings.json` — Waveloom 项目级
3. `.claude/settings.json` — Claude Code 项目级
4. `~/.waveloom/settings.json` — Waveloom 用户级
5. `~/.claude/settings.json` — Claude Code 用户级

#### 事件类型

| 事件 | 触发时机 | 同步/异步 | 可改写参数 |
|------|---------|----------|-----------|
| `PreToolUse` | 工具执行前 | 同步 | 是 |
| `PostToolUse` | 工具执行后 | 同步 | 是（结果） |
| `Notification` | 生命周期事件（任务开始/完成/出错） | 异步 | 否 |
| `Stop` | Agent Loop 终止 | 同步 | 否 |

#### matcher 匹配规则

| 语法 | 示例 | 说明 |
|------|------|------|
| 空字符串 | `""` | 匹配所有工具 |
| 精确名称 | `"Bash"` | 完全匹配工具名 |
| 前缀通配 | `"Read*"` | 匹配以 Read 开头的工具 |
| 多模式 | `"Bash\|Read"` | `\|` 分隔，匹配任一 |

#### Hook 条目字段

| 字段 | 类型 | 必需 | 说明 |
|------|------|------|------|
| `type` | string | 否 | `"command"`（默认） |
| `command` | string | 是 | 可执行脚本路径或 shell 命令 |
| `timeout` | number | 否 | 超时毫秒数，默认 30000 |

Hook 脚本通过 stdin 接收 JSON 事件上下文，通过 stdout 返回 JSON 结果。退出码 0 = 正常应用改写，1 = 透传原始参数，2 = 阻止执行。更多细节参见 [Claude Code Hooks 文档](https://code.claude.com/docs/en/hooks)。

### environment 环境工具配置

Agent 启动时自动探测可用工具链。若工具不在 PATH 中或需指定版本，可通过 `environment.tools` 配置路径，详见 [`environment.md`](./environment.md)。

### web_search 搜索配置

`web_search` 工具默认使用 DuckDuckGo 搜索引擎，无需任何配置。如需更好的搜索结果质量，通过环境变量切换到 Brave Search API：

```bash
export BRAVE_API_KEY="your-brave-api-key"
```

未配置时自动使用 DuckDuckGo。

### 工具超时配置

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `tool_timeout` | 单个工具执行超时（Go Duration 格式，如 `"10m"` / `"600s"` / `"0s"`，0 禁用） | `"5m"` |

### session 配置

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `session.dir` | 会话存储目录（相对或绝对路径）。优先级：`settings.json session.dir` > `WAVELOOM_SESSION_DIR` 环境变量 > `~/.waveloom/<project>/sessions/` | `~/.waveloom/<project>/sessions/` |

```json
{
  "session": {
    "dir": ".waveloom/sessions"
  }
}
```


### 界面配置

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `theme` | 主题模式：`auto`（自动检测终端背景）、`dark`、`light`、`darkcolorblind`、`lightcolorblind`。可通过 `/theme` 命令运行时切换并持久化 | `auto` |
| `locale` | 界面语言：`zh-CN`（中文）、`en-US`（英文）、`auto`（从 `LANG` 环境变量自动检测）。优先级：`--locale` CLI > `settings.json` > `LANG` | `auto` |

```json
{
  "theme": "dark",
  "locale": "zh-CN"
}
```

### Plan 模式配置

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `plans_directory` | Plan 文件存储目录（相对路径相对于配置文件所在目录） | `~/.waveloom/plans/` |
## CLI 参数

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `--model` | 模型名 | `deepseek-v4-pro` |
| `--system-prompt` | 自定义系统提示词 | 内置提示词 |
| `--max-turns N` | 最大轮数，0 不限制 | `0`（不限制） |
| `--context-limit 1M` | 上下文窗口大小，支持 `1M` / `200k` / 数字 | `1M` |
| `--theme auto/dark/light` | 主题，auto 自动检测终端背景 | `auto` |
| `--locale zh-CN/en-US/auto` | 界面语言，auto 从 `LANG` 环境变量检测 | `auto` |
| `--provider NAME` | 切换 LLM Provider（需在 `profiles` 中配置对应 profile） | — |
| `--log-level level` | 日志级别（error/warn/info/debug） | `info` |
| `--verbose` | 输出详细日志到 `.waveloom/waveloom.log` | 关闭 |
| `--bypass-permissions` | 跳过所有权限检查 | 关闭 |
| `--tool-timeout D` | 单个工具执行超时（Go Duration 格式，如 `10m` / `600s` / `0s`，0 禁用） | `10m` |
| `--resume ID` | 恢复指定会话 | — |
| `--continue` | 恢复最近一次会话 | — |
| `--settings PATH` | 指定配置文件路径 | `.waveloom/settings.json` |
| `--version` | 显示版本号 | — |

配置优先级：**CLI 参数 > `.waveloom/settings.json`（项目） > `~/.waveloom/settings.json`（全局）**
