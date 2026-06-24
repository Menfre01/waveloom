<p align="right"><a href="./docs/README.en.md">English</a></p>

<p align="center">
  <img src="./docs/logo.svg" alt="Waveloom" width="420"/>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/language-Go-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go"/>
  <img src="https://img.shields.io/badge/DeepSeek-native-4D6BFE?style=flat-square" alt="DeepSeek"/>
  <img src="https://img.shields.io/badge/license-Apache%202.0-8b949e?style=flat-square" alt="license"/>
  <img src="https://img.shields.io/badge/TUI-Bubble%20Tea-5fafd7?style=flat-square" alt="Bubble Tea"/>
  <img src="https://img.shields.io/badge/status-alpha-d4a76a?style=flat-square" alt="alpha"/>
</p>

---

**Waveloom** 是为 **DeepSeek 前缀缓存定制的终端 Code Agent**（纯 Go）。它利用 DeepSeek 的前缀缓存机制，通过固定的 System Prompt 起点、跨轮累积的消息历史和压缩后永不变化的字节内容，将上下文缓存命中率推高到 **95-99%**，让输入 Token 成本降至未命中价格的 **1/50 ~ 1/120**。

你用自然语言描述需求，Agent 在终端里读取代码、分析逻辑、编辑文件、执行命令——每一次写入和命令执行都先征求你的同意。首推 `deepseek-v4-flash` 和 `deepseek-v4-pro`，同时兼容 OpenAI 接口。

---

## 为什么选择 Waveloom

| 维度 | Waveloom 的做法 | 为什么重要 |
|------|----------------|-----------|
| **终端原生 TUI** | 基于 [Bubble Tea](https://github.com/charmbracelet/bubbletea) v2 + [Glamour](https://github.com/charmbracelet/glamour) Markdown 渲染 + [Lipgloss](https://github.com/charmbracelet/lipgloss) 样式引擎 | 流式渲染 thought / text / tool 输出，支持折叠展开，不是"黑盒聊天"，全程透明可审查 |
| **DeepSeek 前缀缓存优化** | System Prompt 固定为 `messages[0]`，消息历史跨轮累积不重置，压缩后字节永不变化 | 最大公共前缀持续命中，缓存命中价格仅为未命中的 **1/50 ~ 1/120**（V4-Flash：命中 ¥0.02/M，未命中 ¥1/M；V4-Pro：命中 ¥0.025/M，未命中 ¥3/M） |
| **四级水位线上下文压缩** | 60% → Snip（工具结果截断）、80% → Prune（reasoning 清除 + 占位符）、95% → Summarize（LLM 增量摘要）、98% → 硬截断 | 自动管理百万 Token 上下文窗口，长对话不丢关键信息，不留垃圾，不发生 Context Rot |
| **LSP 原生集成** | 内置 LSP Client，Agent 可主动调用 `lsp_diagnostic` / `lsp_definition` / `lsp_references` / `lsp_hover` | Agent 像你一样理解代码——跳转定义、查找引用、查看类型签名，不是盲人摸象 |
| **权限安全模型** | 三级决策（allow / deny / ask），规则引擎支持 `shell(git *)` 等模式匹配，支持 CI `--bypass-permissions` | 你始终握有最终决定权，写文件和命令执行不会静默发生 |
| **单二进制部署** | 纯 Go，零运行时依赖，预编译二进制 ~15MB | `curl` 一行命令安装，macOS / Linux AMD64 & ARM64 全支持 |

---

## 安装

依赖：[DeepSeek API Key](https://platform.deepseek.com/api_keys)。

### 预编译二进制（推荐）

无需 Go 环境，下载即用。前往 [Releases](https://github.com/Menfre01/waveloom/releases/latest) 下载对应平台的 `wvl`。

> `/usr/local/bin` 需要 sudo 权限，或改用 `~/.local/bin`（见下方备选方案）。

**macOS (ARM64 — Apple Silicon)**

```sh
sudo curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/wvl_darwin_arm64.tar.gz | sudo tar -xz -C /usr/local/bin wvl
```

**macOS (AMD64 — Intel)**

```sh
sudo curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/wvl_darwin_amd64.tar.gz | sudo tar -xz -C /usr/local/bin wvl
```

**Linux (AMD64)**

```sh
sudo curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/wvl_linux_amd64.tar.gz | sudo tar -xz -C /usr/local/bin wvl
```

**Linux (ARM64)**

```sh
sudo curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/wvl_linux_arm64.tar.gz | sudo tar -xz -C /usr/local/bin wvl
```

> 没有 `/usr/local/bin` 写入权限？安装到 `~/.local/bin`：
> ```sh
> mkdir -p ~/.local/bin
> curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/wvl_darwin_arm64.tar.gz | tar -xz -C ~/.local/bin wvl
> export PATH="$HOME/.local/bin:$PATH"  # 建议加到 ~/.bashrc 或 ~/.zshrc
> ```
>
> macOS 首次运行若提示"无法验证开发者"，执行：
> ```sh
> xattr -d com.apple.quarantine /usr/local/bin/wvl
> ```

### 从源码构建

前置条件：**Go 1.25+**。

```sh
git clone https://github.com/Menfre01/waveloom.git
cd waveloom && make install
# wvl 安装到 $HOME/go/bin，确保该路径在 PATH 中：
export PATH=$HOME/go/bin:$PATH
```

### 更新

**预编译二进制**：重新执行安装命令，覆盖旧版本即可。

**从源码构建**：

```sh
cd waveloom && git pull && make install
```

### 首次配置

```sh
# 交互式引导（只需一次）
wvl setup
# → 选择 Provider → 输入 API Key → 选择模型 → 完成

# 或跳过配置，直接用环境变量：
LLM_API_KEY=sk-... wvl
```

> **只用一次**：不想进 TUI？`wvl "帮我写一个 HTTP server 的单元测试"`

---

## Agent 能做什么

Waveloom 内置以下工具，Agent 根据任务自主调用：

| 工具 | 能力 |
|------|------|
| `read_file` | 读取文件内容 |
| `write_file` | 创建或覆盖文件 |
| `edit_file` | 精确替换文件中某段内容 |
| `grep` | 在代码库中搜索匹配的行 |
| `search_file` | 按文件名模式查找文件 |
| `ls` | 列出目录内容 |
| `shell` | 执行任意 Shell 命令 |
| `web_fetch` | 获取在线文档、API 参考 |
| `lsp_diagnostic` | 获取文件编译错误和 lint 提示 |
| `lsp_definition` | 跳转到符号定义 |
| `lsp_references` | 查找符号的所有引用位置 |
| `lsp_hover` | 获取符号类型签名和文档 |

典型场景：给你写单元测试、重构一个模块、排查 bug、解释某段代码的设计意图、添加新功能。

---

## 使用方式

### 交互模式

```sh
wvl
```

进入 TUI 后，像聊天一样打字，Enter 发送。Agent 会自主调用工具来读文件、搜代码、编辑、跑测试。

<p align="center">
  <img src="./docs/tui.png" alt="Waveloom 截图" width="720"/>
</p>

每行开头的字符告诉你**谁在说话**：

| 前缀 | 角色 | 含义 |
|------|------|------|
| `›` | 你 | 你的消息，蓝色 |
| `·` / spinner | Assistant | AI 的回复，绿色，支持 Markdown 渲染 |
| `·` / spinner | Thought | AI 的思考过程，灰色，完成后自动折叠为一句话（`Ctrl+T` 展开） |
| `•` / spinner | 工具 | AI 的操作（读文件、写文件、跑命令），绿=成功 / 红=失败 |

**快捷键**：

| 按键 | 作用 |
|------|------|
| `Enter` | 发送消息 |
| `Esc` | 中断正在运行的 Agent |
| `↑` `↓` / `PgUp` `PgDn` | 滚动对话历史 |
| `Ctrl+E` / `End` | 跳到底部 |
| `Ctrl+T` | 展开/折叠最近一个 thought |
| `Ctrl+O` | 展开/折叠最近一个 tool 输出 |
| `Ctrl+G` | 切换主题（dark / light / auto） |
| `Ctrl+V` | 粘贴 |
| `Ctrl+C` | 退出 |

**底部状态栏**显示：当前模型、上下文用量（进度条）、缓存命中率、Loop 轮数、耗时、余额。

### 单次执行

```sh
wvl "解释 pkg/llm/client.go 的设计"
wvl --model deepseek-v4-flash "给 UserService 写单元测试"
echo "review pkg/llm/ 下的代码" | wvl
```

### 引用文件

在输入框里打 `@`，会弹出文件选择器，支持模糊过滤（前缀 > 子串匹配），`Tab` 进入子目录。选中的文件内容会自动注入到消息上下文。

```
帮我优化 @pkg/auth/login.go 的错误处理逻辑
```

---

## 权限安全

Agent 执行写操作或 Shell 命令前会经过权限检查。每个工具调用产生三种决策之一：

- **允许（allow）**：直接放行（只读操作默认允许）
- **拒绝（deny）**：硬拦截（如 `rm -rf /`）
- **询问（ask）**：弹出确认框，你来决定

<p align="center">
  <img src="./docs/permission.png" alt="权限确认框" width="560"/>
</p>

在 `settings.json` 中配置权限规则：

```json
{
  "permissions": {
    "allow": ["read_file", "search_file", "grep", "ls"],
    "deny":  ["shell(rm -rf /*)"],
    "ask":   ["write_file", "edit_file"]
  }
}
```

规则格式：`工具名` 或 `工具名(匹配模式)`，如 `shell(git *)` 匹配所有以 `git ` 开头的命令。

CI / 自动化场景可用 `--bypass-permissions` 跳过所有检查。

---

## 配置

### settings.json

Waveloom 首次运行会在 `.waveloom/settings.json` 生成默认配置。最简配置只需要 `api_key`：

```json
{
  "llm": {
    "api_key": "sk-your-deepseek-key"
  }
}
```

完整的 `llm` 配置项（均有默认值，按需覆盖）：

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `api_key` | DeepSeek API Key，为空时回退 `LLM_API_KEY` 环境变量 | — |
| `provider` | `deepseek` 或 `openai` | `deepseek` |
| `model` | 模型名 | `deepseek-v4-flash` |
| `base_url` | API 地址 | `https://api.deepseek.com` |
| `timeout` | 请求超时 | `600s` |
| `extra_params` | 额外参数（thinking、reasoning_effort 等） | 思考模式默认开启 |

配置优先级：**CLI 参数 > `.waveloom/settings.json`（项目） > `~/.waveloom/settings.json`（全局）**

### CLI 参数

| 参数 | 说明 |
|------|------|
| `--model` | 模型名 |
| `--system-prompt` | 自定义系统提示词 |
| `--max-turns N` | 最大轮数，0 不限制 |
| `--context-limit 1M` | 上下文窗口大小，支持 `1M` / `200k` / 数字 |
| `--theme auto/dark/light` | 主题，auto 自动检测终端背景 |
| `--verbose` | 输出详细日志到 `.waveloom/wvl.log` |
| `--bypass-permissions` | 跳过所有权限检查 |
| `--resume ID` | 恢复指定会话 |
| `--settings PATH` | 指定配置文件路径 |

---

## 上下文管理与前缀缓存

DeepSeek 的前缀缓存机制：每次请求时，API 从 `messages[0]` 开始逐条比对，找到与上次请求的最长公共前缀。命中部分按缓存价计费，未命中部分按标准价计费。**缓存命中与未命中的输入价格差异巨大**——以 V4-Flash 为例，命中 ¥0.02/M tokens，未命中 ¥1/M tokens，相差 **50 倍**；V4-Pro 则达到 **120 倍**（¥0.025 vs ¥3/M tokens）。

Waveloom 针对这一机制做了系统性的优化：

1. **System Prompt 固定为 `messages[0]`**：无论对话多长，第一条消息始终不变，确保公共前缀的起点稳定。
2. **消息历史跨轮累积不重置**：每轮对话追加到历史末尾，不做"每轮只传当前问题"的短视优化。这样前 N-1 轮的内容都是第 N 轮请求的前缀。
3. **四级水位线压缩（Tier 0-3）**：当上下文利用率上升时，分级压缩历史消息。关键在于——**压缩后的字节内容永不变化**。一旦某条消息被截断或替换为占位符，它在后续所有轮次中保持完全相同的字节表示，前缀缓存持续命中。
4. **单调边界保证**：压缩决策表（`compactionDecisionSet`）+ 双 cursor 机制确保每条消息只被压缩一次，不会反复修改导致缓存失效。

```
                         context window (1M)
  ┌──────────────────────────────────────────────────────────────────┐
  │  ████████████████████████████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░ │
  │  ↑ 已用                       ↑ 60%   ↑ 80%   ↑ 95%            │
  │                                Tier 1  Tier 2  Tier 3            │
  │  Tier 0: < 60%  — 什么都不做                                     │
  │  Tier 1: 60-80% — Snip：工具结果差分截断（纯本地，零 API 调用）    │
  │  Tier 2: 80-95% — Prune：reasoning 清除 + 占位符替换              │
  │  Tier 3: ≥ 95%  — Summarize：LLM 增量摘要（需 API 调用）         │
  │  硬临界: ≥ 98%  — 阻止后续 LLM 调用                               │
  └──────────────────────────────────────────────────────────────────┘
```

缓存命中率通常在 **95-99%**，意味着 100 万 Token 的上下文窗口中，实际按标准价计费的只有 1-5 万 Token。这不是偶然——是架构设计的结果。

> 详见 [`specs/compaction.md`](./specs/compaction.md) —— 上下文压缩的完整设计。

---

## 开发

```sh
make build       # 编译 → bin/wvl
make install     # 安装 → $HOME/go/bin/wvl
make test        # 测试
```

```
waveloom/
├── cmd/waveloom/          # 入口 + TUI
├── pkg/
│   ├── agentloop/         # Think-Act-Observe 循环
│   ├── context/           # 上下文累积 + 四级水位线压缩
│   ├── llm/               # LLM API 封装
│   ├── memory/            # AGENTS.md 层级加载
│   ├── permission/        # 权限守门人
│   ├── reference/         # @ 文件引用展开
│   └── tool/              # 内置工具
├── specs/                 # 各组件设计规格书
├── docs/                  # 文档
└── Makefile
```

---

Apache License 2.0