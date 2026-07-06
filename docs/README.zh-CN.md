<p align="center">
  <a href="../README.md">English</a>
  &nbsp;·&nbsp;
  <strong>简体中文</strong>
</p>

<p align="center">
  <img src="../assets/logo.svg" alt="Waveloom" width="360"/>
</p>

<p align="center">
  <a href="https://github.com/Menfre01/waveloom/releases/latest"><img src="https://img.shields.io/github/v/release/Menfre01/waveloom?style=flat-square&color=00ADD8&labelColor=161b22" alt="release"/></a>
  <a href="https://github.com/Menfre01/waveloom/actions/workflows/ci.yml"><img src="https://github.com/Menfre01/waveloom/actions/workflows/ci.yml/badge.svg?style=flat-square&labelColor=161b22" alt="CI"/></a>
  <a href="https://github.com/Menfre01/waveloom/releases"><img src="https://img.shields.io/github/downloads/Menfre01/waveloom/total?style=flat-square&color=00ADD8&label=GitHub%20downloads&labelColor=161b22" alt="downloads"/></a>
  <a href="https://go.dev"><img src="https://img.shields.io/badge/language-Go-00ADD8?style=flat-square&logo=go&logoColor=white&labelColor=161b22" alt="Go"/></a>
  <a href="https://platform.deepseek.com"><img src="https://img.shields.io/badge/DeepSeek-native-4D6BFE?style=flat-square&labelColor=161b22" alt="DeepSeek"/></a>
  <a href="../LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-8b949e?style=flat-square&labelColor=161b22" alt="license"/></a>
</p>

---

**DeepSeek 生态中最精致的终端 Code Agent。** Claude Code 级别的 TUI 打磨 — 流式推理渲染、rich diff、权限确认对话框、`@` 模糊文件选择器、`/` 命令面板 — 同时从架构层面深度优化 DeepSeek 前缀缓存。`.claude/skills/` 开箱即用。DeepSeek 缓存命中与未命中价格差高达 120 倍，Waveloom 让最长公共前缀跨轮次持续命中。

**curl 一键安装（推荐）**

```sh
curl -fsSL https://raw.githubusercontent.com/Menfre01/waveloom/main/install.sh | sh
```

**Homebrew**

```sh
brew trust menfre01/tap
brew install Menfre01/tap/waveloom
```

> 支持 macOS / Linux / Windows，AMD64 & ARM64。安装到 `~/.local/bin`，无需 sudo。

**Windows** 需要安装 [Git for Windows](https://git-scm.com/downloads/win)。打开 PowerShell 运行：

```powershell
powershell -c "irm https://raw.githubusercontent.com/Menfre01/waveloom/main/install.ps1 | iex"
```

> [!TIP]
> **Windows 上推荐使用 [WSL2](https://learn.microsoft.com/zh-cn/windows/wsl/install) 获得最佳体验。** 在 WSL2 内安装 Linux 版本，无需 Git Bash 转发层，终端渲染更流畅，shell 命令性能更佳。
>
> 选择 Git Bash？Waveloom 依赖 `bash.exe`，cmd 和 PowerShell 不支持。安装完成后，**打开 Git Bash** 执行下方命令。若找不到 `waveloom`，将 `%USERPROFILE%\.local\bin` 加入 Windows 系统 PATH（安装脚本已自动处理）。

安装后配置 Key 即可开始：

```sh
waveloom setup
waveloom
```

> [!IMPORTANT]
> API Key 直连 DeepSeek / OpenAI，代码不经过第三方。写文件和执行命令前需要你确认。

<p align="center">
  <img src="../assets/demo.gif" alt="Waveloom Demo" width="900"/>
</p>

---

## 和其他工具相比？

| | Waveloom | Claude Code | Reasonix |
|---|---|---|---|
| Skill 格式 | 开箱即用：`.claude/skills/` SKILL.md，9/15 个 frontmatter 字段（`$ARGUMENTS`、`paths`、`` !`cmd` `` 注入等） | 原生 SKILL.md + commands | 6/15 字段，Skill 无变量替换（仅 commands 支持） |
| 缓存设计 | DeepSeek 前缀匹配：四级水位线（Snip → Prune → Summarize），压缩后字节永不变化 | Anthropic `cache_control`：`cache_edits` API，System Prompt 含动态段 | DeepSeek 前缀匹配：四级（notice → snip → compact → force），`session.Replace()` 触发 rewrite 版本号 |
| 上下文压缩 | 单调不变式 — `compactionDecisionSet` + 双游标，每条消息只压缩一次 | 每轮独立压缩，无持久性保证 | 前缀字节跨压缩保留，但无逐消息决策追踪 |
| Plan 模式 | Guard 限制只写 plan 文件，构建工具自动放行 | 权限层全局阻止写入，富交互审批 UI | `planmode.Policy` + bash/MCP 信任门；注入 Marker 字符串；无 plan 文件 |
| 子代理 | Fork（继承上下文）/ Cold（裁剪工具集）/ Explore（只读） | Fork + Cold + In-process + Coordinator（tmux 拉起） | `task` 工具嵌套 agent，后台任务通过 job manager |
| 运行时 | Go 单二进制 ~18MB，零依赖 | Node.js | Go 二进制 + Desktop 应用，外部 plugin 宿主 |
| 权限模型 | 8 步决策管线，3 级命令安全分类（RiskNone/RiskLow/RiskHigh） | 8 源规则合并 + LLM 分类器自动审批 | Policy + Approver，9 阶段执行管线，shellsafe readOnly 检测 |
| TUI 打磨 | 流式推理、rich diff、权限对话框、`@` 模糊选择器、`/` 面板、i18n、主题切换 — Claude Code 同级 | 原生 TUI（Ink/React），标杆水平 | 基础 TUI，功能可用但无 diff/语法高亮打磨 |

**选 Waveloom 如果**：用 DeepSeek、已有 `.claude/skills/`、想要 Claude Code 体验但不想白烧缓存未命中费用。  
**选 Claude Code 如果**：用 Anthropic API、需要 MCP + coordinator 模式、重度依赖 Claude 生态。  
**选 Reasonix 如果**：需要桌面 GUI、飞书/微信/QQ Bot 集成、或 LSP 代码分析。

---

## 为什么选择 TUI

**Waveloom 是唯一达到 Claude Code 级终端交互打磨的 DeepSeek 原生 Agent。** 流式推理 + 语法高亮、rich diff、权限确认对话框、`@` 模糊文件选择器、`/` 命令面板、主题切换、中英双语。大多数 DeepSeek Agent 的 TUI 只是 bare minimum — 纯文本流、无交互设计。跑一下就知道差距。

---

## 功能亮点

- **前缀缓存深度优化** — System Prompt 固定，消息只在末尾追加，四级水位线压缩后字节永不变化，最大公共前缀持续命中
- **权限安全模型** — 三级决策（allow / deny / ask），规则引擎支持模式匹配，写操作和命令执行需要你确认
- **会话持久恢复** — 关闭终端几天后 `waveloom --continue` 回来，Agent 记得所有上下文接着工作
- **Plan 模式** — 先规划后执行的二阶段工作流：探索设计 → 审批 → 编码。`Shift+Tab` 一键进入/退出，Guard 写保护拦截。
- **10 个内置工具** — `read_file` / `write_file` / `edit_file` / `shell` / `web_fetch` / `ask_user_question` / `enter_plan_mode` / `exit_plan_mode` / `skill` / `agent`
- **i18n 多语言** — 完整中英双语界面，`--locale` CLI 参数 / `/locale` 命令，LANG 环境变量自动检测

---

## 常见问题

**Q: 怎么切换模型？**  
输入 `/model` 选择，或 `waveloom --model deepseek-v4-flash`。

**Q: API Key 安全吗？**  
Key 存储在本地 `~/.waveloom/`，直连 DeepSeek / OpenAI，不经过任何第三方服务器。

**Q: 怎么切换语言？**  
输入 `/locale` 切换中英文界面，或 `waveloom --locale en-US`。设置自动保存到 `settings.json`。

**Q: 支持哪些语言？**  
Waveloom 适用于任何文本项目。代码验证使用各语言原生构建工具（`go build`、`npx tsc`、`cargo build`、`make` 等），无需安装 LSP Server。

---

## 文档

| 文档 | 内容 |
|------|------|
| [`usage`](./usage.md) | 交互模式、快捷键、Skill 系统 |
| [`install`](./install.md) | Homebrew / curl / 源码构建 / Shell 补全 |
| [`settings`](./settings.md) | API Key、模型、超时、压缩水位线 |
| [`prefix-cache`](./prefix-cache.md) | DeepSeek 缓存原理、四级水位线 |
| [`environment`](./environment.md) | 工具链探测 |
| [`faq`](./faq.md) | 常见问题 |

---

## 开发

Go 1.25+，`make build` / `make test`。项目结构及贡献指南详见 [`CONTRIBUTING.md`](../CONTRIBUTING.md)。

---

---

Apache License 2.0 © 2026 Waveloom Contributors
