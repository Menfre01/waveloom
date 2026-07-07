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
| `provider` | `deepseek` 或 `openai` | `deepseek` |
| `model` | 模型名 | `deepseek-v4-pro` |
| `sub_model` | 子代理默认模型，仅 DeepSeek 下自动配对（pro → flash） | 自动配对 |
| `base_url` | API 地址 | `https://api.deepseek.com` |
| `timeout` | 请求超时 | `600s` |
| `extra_params` | 额外参数（thinking、reasoning_effort 等） | 思考模式默认开启 |
| `retry` | 重试策略 `{"max_retries":3, "initial_backoff":"1s", "max_backoff":"30s", "multiplier":2.0}` | 默认重试策略 |
| `headers` | 自定义 HTTP 请求头 `{"X-Custom": "value"}` | — |

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

### environment 环境工具配置

Agent 启动时自动探测可用工具链。若工具不在 PATH 中或需指定版本，可通过 `environment.tools` 配置路径，详见 [`environment.md`](./environment.md)。

### 工具超时配置

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `tool_timeout` | 单个工具执行超时（Go Duration 格式，如 `"10m"` / `"600s"` / `"0s"`，0 禁用） | `"10m"` |

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
| `--verbose` | 输出详细日志到 `.waveloom/waveloom.log` | 关闭 |
| `--bypass-permissions` | 跳过所有权限检查 | 关闭 |
| `--tool-timeout D` | 单个工具执行超时（Go Duration 格式，如 `10m` / `600s` / `0s`，0 禁用） | `10m` |
| `--resume ID` | 恢复指定会话 | — |
| `--continue` | 恢复最近一次会话 | — |
| `--settings PATH` | 指定配置文件路径 | `.waveloom/settings.json` |
| `--version` | 显示版本号 | — |

配置优先级：**CLI 参数 > `.waveloom/settings.json`（项目） > `~/.waveloom/settings.json`（全局）**
