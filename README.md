<p align="center">
  <a href="./docs/README.en.md">English</a>
  &nbsp;·&nbsp;
  <strong>简体中文</strong>
</p>

<p align="center">
  <img src="./assets/logo.svg" alt="Waveloom" width="360"/>
</p>

<p align="center">
  <a href="https://github.com/Menfre01/waveloom/releases/latest"><img src="https://img.shields.io/github/v/release/Menfre01/waveloom?style=flat-square&color=00ADD8&labelColor=161b22" alt="release"/></a>
  <a href="https://github.com/Menfre01/waveloom/actions/workflows/ci.yml"><img src="https://github.com/Menfre01/waveloom/actions/workflows/ci.yml/badge.svg?style=flat-square&labelColor=161b22" alt="CI"/></a>
  <a href="https://github.com/Menfre01/waveloom/releases"><img src="https://img.shields.io/github/downloads/Menfre01/waveloom/total?style=flat-square&color=00ADD8&label=GitHub%20downloads&labelColor=161b22" alt="downloads"/></a>
  <a href="https://go.dev"><img src="https://img.shields.io/badge/language-Go-00ADD8?style=flat-square&logo=go&logoColor=white&labelColor=161b22" alt="Go"/></a>
  <a href="https://platform.deepseek.com"><img src="https://img.shields.io/badge/DeepSeek-native-4D6BFE?style=flat-square&labelColor=161b22" alt="DeepSeek"/></a>
  <a href="./LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-8b949e?style=flat-square&labelColor=161b22" alt="license"/></a>
</p>

---

**专为 DeepSeek 前缀缓存深度优化的终端 Code Agent。** 操作习惯贴近 Claude Code，已有 Skill 零迁移。DeepSeek 缓存命中与未命中的价格差高达 120 倍，Waveloom 从架构层面确保 System Prompt 和消息前缀稳定不变，让最长公共前缀持续命中缓存。

**Homebrew（推荐）**

```sh
brew trust menfre01/tap
brew install Menfre01/tap/waveloom
```

**curl 一键安装**

```sh
curl -fsSL https://raw.githubusercontent.com/Menfre01/waveloom/main/install.sh | sh
```

> 支持 macOS / Linux，AMD64 & ARM64。安装到 `~/.local/bin`，无需 sudo。

安装后配置 Key 即可开始：

```sh
waveloom setup
waveloom
```

> [!IMPORTANT]
> API Key 直连 DeepSeek / OpenAI，代码不经过第三方。写文件和执行命令前需要你确认。

<p align="center">
  <img src="./assets/demo.gif" alt="Waveloom Demo" width="900"/>
</p>

---

## 和 Claude Code 有什么区别？

| | Waveloom | Claude Code |
|---|---|---|
| 缓存设计 | 围绕 DeepSeek 前缀匹配：System Prompt 固定、消息追加、原地压缩 | 围绕 Anthropic `cache_control`：System Prompt 含动态段、压缩替换消息 |
| 上下文压缩 | 原地修改，前缀字节稳定 | 摘要替换消息 |
| 运行时 | 单二进制 ~17MB | Node.js |

**选 Waveloom 如果**：用 DeepSeek、在意 API 费用、已有 Claude Code Skill、需要零依赖单二进制  
**选 Claude Code 如果**：用 Anthropic API、需要 MCP、重度依赖 Claude 生态

---

## 功能亮点

- **前缀缓存深度优化** — System Prompt 固定，消息只在末尾追加，四级水位线压缩后字节永不变化，最大公共前缀持续命中
- **LSP 原生集成** — Agent 主动调用 `lsp_diagnostic` / `lsp_definition` / `lsp_references` / `lsp_hover`，像你一样理解代码
- **权限安全模型** — 三级决策（allow / deny / ask），规则引擎支持模式匹配，写操作和命令执行需要你确认
- **会话持久恢复** — 关闭终端几天后 `waveloom --continue` 回来，Agent 记得所有上下文接着工作
- **14 个内置工具** — `read_file` / `edit_file` / `grep` / `shell` / `web_fetch` / `ask_user_question` / `skill` / LSP 系列，Agent 自主调用
- **i18n 多语言** — 完整中英双语界面，`--locale` CLI 参数 / `/locale` 命令 / `settings.json` 持久化，LANG 环境变量自动检测
- **TUI 交互** — `@` 引用文件 / `@` 文件选择器 / `/` 命令面板 / `/locale` 切换语言 / `Tab` 段落导航 / `Ctrl+G` 主题切换

---

## 常见问题

**Q: 怎么切换模型？**  
输入 `/model` 选择，或 `waveloom --model deepseek-v4-flash`。

**Q: API Key 安全吗？**  
Key 存储在本地 `~/.waveloom/`，直连 DeepSeek / OpenAI，不经过任何第三方服务器。

**Q: 怎么切换语言？**  
输入 `/locale` 切换中英文界面，或 `waveloom --locale en-US`。设置自动保存到 `settings.json`。

**Q: 支持哪些语言？**  
LSP 原生支持 Go（内置 gopls 集成）。任何有 LSP Server 的语言均可使用，纯文本项目也能用 `read_file` / `edit_file` / `grep` 等基础工具。

---

## 文档

| 文档 | 内容 |
|------|------|
| [`usage`](./docs/usage.md) | 交互模式、快捷键、Skill 系统 |
| [`install`](./docs/install.md) | Homebrew / curl / 源码构建 / Shell 补全 |
| [`settings`](./docs/settings.md) | API Key、模型、超时、压缩水位线 |
| [`prefix-cache`](./docs/prefix-cache.md) | DeepSeek 缓存原理、四级水位线 |
| [`environment`](./docs/environment.md) | LSP Server、工具链探测 |
| [`faq`](./docs/faq.md) | 常见问题 |

---

## 开发

Go 1.25+，`make build` / `make test`。项目结构及贡献指南详见 [`CONTRIBUTING.md`](./CONTRIBUTING.md)。

---

Apache License 2.0 © 2026 Waveloom Contributors
