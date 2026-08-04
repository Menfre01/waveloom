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
| `base_url` | API 地址 | `https://api.deepseek.com` |
| `timeout` | 请求超时 | `600s` |
| `extra_params` | 额外参数（thinking、reasoning_effort 等） | 思考模式默认开启 |
| `retry` | 重试策略 `{"max_retries":3, "initial_backoff":"1s", "max_backoff":"30s", "multiplier":2.0}` | 默认重试策略 |
| `sub_model` | 子代理默认模型(exporer 子代理使用此模型执行代码搜索和发现,约 2 倍便宜) | 自动配对(pro → flash) |
| `profiles` | 多 Provider 配置,以 provider 名为键(如 `"kimi"`、`"openai"`)。每个 profile 可包含 `api_key`、`model`、`sub_model`、`base_url`、`extra_params`。配合 `--provider` CLI 参数切换。Provider 无关字段(`timeout`、`retry`、`headers`)从顶层继承 | — |

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
    "allow": ["read", "web_fetch", "bash(go build *)", "bash(go test *)"],
    "deny":  ["bash(rm -rf /*)"],
    "ask":   ["write", "edit"]
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

Agent 启动时自动探测可用工具链。若工具不在 PATH 中或需指定版本,可通过 `environment.tools` 配置路径,详见 [`environment.md`](./environment.md)。

### lsp LSP 诊断配置

`edit` / `write` 操作后自动运行 LSP 诊断,详见 [`lsp.md`](./lsp.md)。

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `lsp.servers` | 文件扩展名 → LSP Server 配置。Key 为扩展名(如 `".py"`),Value 为 `{"command": "...", "args": ["..."]}`。用户自定义覆盖内置默认 | — |
| `lsp.idle_timeout_ms` | Server 空闲回收超时(毫秒) | `300000`(5 分钟) |

示例:添加 Python 和 Java 的 LSP Server:

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
| `plans_directory` | Plan 文件存储目录(相对路径相对于配置文件所在目录) | `~/.waveloom/plans/` |

### sandbox 沙箱配置

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `enabled` | TUI 常规模式是否启用沙箱;`--bypass-permissions` / ACP 无交互自动激活,无需置 true | `false` |
| `failIfUnavailable` | 后端缺失(bwrap 未装)时是否拒绝启动 | `false` |
| `allowUnsandboxedCommands` | 沙箱内命令失败时是否提示逃生(加入 excludedCommands) | `true` |
| `excludedCommands` | 逃逸命令列表(前缀/精确/通配),命中不进沙箱(裸跑),但权限仍受 Guard 约束 | `[]` |
| `env` | 沙箱内注入的环境变量(通用机制,不绑定任何工具);值支持路径前缀(`~/` 家目录、`//`/`/` 绝对、`./` workspace 相对),其他按字面量注入;键命中凭据剥离清单时忽略(剥离优先) | `{}` |
| `network.mode` | 网络策略:`off`(全断网)/ `on`(直连);`proxy` 为 v2 未实现 | `off` |
| `network.allowedDomains` | 域名白名单(v2 proxy 预留,当前不生效) | `[]` |
| `filesystem.allowWrite` | 额外可写路径(`//abs` 绝对、`~/` 家目录、`./` 或裸名项目根);根目录与遮蔽路径冲突时拒绝 | `[]` |
| `filesystem.allowRead` | **已废弃**(2026-09):配置中出现仅告警并忽略,不再生效;凭据防护统一用 `denyRead` / `credentials.files` | `[]` |
| `filesystem.denyRead` | 遮蔽(不可读)路径。默认不遮蔽任何路径,需显式配置(推荐清单见下文) | `[]` |
| `capabilities.keep` | `--cap-drop ALL` 后加回的内核能力(如 `net_raw` 供 ping) | `[]` |
| `credentials.files` | 凭据遮蔽路径(网络 on 时强烈建议配置) | `[]` |
| `credentials.envVars` | 额外剥离的环境变量,与内置 glob(`*TOKEN*` / `*_API_KEY` 等)叠加 | `[]` |

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

### 沙箱内环境变量注入(`env`)

`sandbox.env` 是**工具无关**的通用机制:沙箱内命令启动时注入指定环境变量,典型用途是把构建工具缓存重定向到 workspace 可写区——只读根下宿主缓存(如 `~/go/pkg/mod`)不可写,go/npm/cargo 等工具写入会失败或降级告警。

- **值路径语义**:`./` → workspace 相对(`"./.waveloom-gomodcache"` → `<项目根>/.waveloom-gomodcache`);`~/` → 家目录;`//` 或 `/` → 绝对路径;其余(URL 等)按字面量注入(如 `GOPROXY`)
- **go 示例**:`GOPATH` / `GOMODCACHE` / `GOCACHE` 指到 workspace 后,首次沙箱内构建需网络下载依赖(一次性,缓存持久),之后离线可构建;宿主构建不受影响
- **npm/cargo 等**:同理配 `npm_config_cache` / `CARGO_HOME` / `PIP_CACHE_DIR` 等
- **安全**:键命中凭据剥离(内置 glob `*TOKEN*` / `*_API_KEY` 等或 `credentials.envVars`)时**忽略注入**,剥离优先——防止配置把被剥离的敏感变量回填进沙箱

### 遮蔽策略(2026-09:默认不遮蔽)

沙箱默认**不遮蔽任何路径**(`~/.ssh`、`~/.aws`、钥匙串等均可读),对齐 Claude Code / Codex。
凭据防护由 `filesystem.denyRead` / `credentials.files` 显式配置;网络 `on` 且未配置时启动警告
("网络打开后能读就能外传,配置遮蔽更安全")。

**固定内置遮蔽**(防写/防逃逸,不可配置移除):

| 路径 | 作用 |
|------|------|
| `<项目根>/.git/hooks` | 防持久化注入(hooks 被执行,写入即逃逸);Linux tmpfs 空覆盖,Seatbelt deny read+write |
| `/var/run/docker.sock` | 防逃逸(docker 可挂载宿主根);macOS 另有 `~/.docker/run/docker.sock` |

**推荐凭据遮蔽配置**(网络 `on` 强烈建议,按需增减):

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

macOS 额外建议追加:`~/Library/Keychains`、`~/Library/HTTPStorages`、
`~/Library/Cookies`、`~/Library/Application Support/Google/Chrome`、
`.../Firefox`、`.../Microsoft/Edge`(钥匙串/cookie/浏览器会话,网络 on 时可读走外传)。

> 注:环境变量剥离(内置 glob:`*TOKEN*` / `*_API_KEY` / `AWS_*` / `GH_*` 等)为独立防线,始终生效,与路径遮蔽配置无关。

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
| `--log-level level` | 日志级别(error/warn/info/debug) | `info` |
| `--bypass-permissions` | 无交互入口(one-shot/ACP)下 ASK → ALLOW,保留 deny 规则与高危硬拦截;TUI 交互模式维持弹窗 | 关闭 |
| `--sandbox-network off/on` | 沙箱网络模式,覆盖 `settings.json` 的 `network.mode`(on 建议配置凭据遮蔽) | 取配置 |
| `--tool-timeout D` | 单个工具执行超时(Go Duration 格式,如 `10m` / `600s` / `0s`,0 禁用) | `5m` |
| `--resume ID` | 恢复指定会话 | — |
| `--continue` | 恢复最近一次会话 | — |
| `--settings PATH` | 指定配置文件路径 | `.waveloom/settings.json` |
| `--version` | 显示版本号 | — |

配置优先级：**CLI 参数 > `.waveloom/settings.json`（项目） > `~/.waveloom/settings.json`（全局）**
