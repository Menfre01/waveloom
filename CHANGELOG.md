# Changelog

## [v0.1.0-alpha.5] — 2026-06-29

### 新增功能
- **Shell 补全**：`waveloom completion <bash|zsh|fish>`，输出对应 shell 补全脚本
- **Homebrew 安装支持**：`brew install Menfre01/tap/waveloom`

### 重构
- 二进制重命名 `wvl` → `waveloom`，Go module 路径迁移至 `github.com/Menfre01/waveloom`
- 日志文件 `.waveloom/wvl.log` → `.waveloom/waveloom.log`

### 工程
- 新增 `release.yml`：tag 推送触发交叉编译、GitHub Release 创建、Homebrew 公式同步
- 新增 `ci.yml`：push/PR 触发 build / test / lint / cross-compile
- 新增社区文件：CODEOWNERS、PR 模板、SECURITY、CONTRIBUTING、CHANGELOG、NOTICE
- 双语文档 CONTRIBUTING / SECURITY / CHANGELOG 中英同步
- Issue 模板重构（bug report / feature request）
- 删除 CLAUDE.md（内容已由 AGENTS.md 取代）

## [v0.1.0-alpha.4] — 2025-07-09

### 新增功能
- **Slash 命令系统**：输入 `/` 弹出命令选择器，支持 /new /model /theme /help，↑↓ 导航、Enter 确认、Tab 补全
- **ToolTimeout 超时保护**：可配置单工具执行超时（CLI `--tool-timeout` / settings.json `tool_timeout`），防止工具永久阻塞

### 修复
- diff_view 严格遵循 POSIX/GNU unified diff 规范
- HUD footer 颜色阈值调整（elap/cache 指示器）

### 重构
- 提取 `pathutil` 包，统一路径安全检查逻辑
- LSP Client 依赖注入重构
- LLM 交互文本中译英（Schema / Description / 错误消息 / 占位符），提升 DeepSeek 前缀缓存命中率

## [v0.1.0-alpha.3] — 2025-07-02

### 新增功能
- **AGENTS.md @ 引用展开**：对标 Claude Code 子文件拆分，支持 `@path/to/file` 引用外部文件，自动展开合并、去重
- **三级截断机制**：工具结果截断策略升级（行数→总字符数→单行长度），code fence 超长行保护

### 修复
- `replace_all` 场景下 hunk 合并与跨 hunk 行号偏移修正
- DiffAdd 行号改用 NewNum，修复自增行行号显示错误

### 重构
- TUI 通知文案精简、footer 布局调整（latency/balance 顺序调换）

### 其他
- Footer 新增 elap 延迟显示
- 安装路径从 `/usr/local/bin` 改为 `~/.local/bin`，无需 sudo

## [v0.1.0-alpha.2] — 2025-06-27

### 新增功能
- **Tab/Enter 聚焦交互**：替代 Ctrl+O/Ctrl+T，Tab 在可交互段落间导航，Enter 展开/折叠

### 修复
- 折叠预览和展开态改用包装后行数截断，防止超长单行撑满 viewport

## [v0.1.0-alpha.1] — 2025-06-20

### 新增功能
- `--model` CLI 参数覆盖配置文件模型选择
- TUI 支持 `--max-turns` 和 `--bypass-permissions` 参数

## [v0.0.3] — 2025-06-15

### 新增功能
- **会话管理**：transcript 回放、recent.json 会话记录、`--continue` 和 `ls` 命令
- **setup 子命令**：首次配置向导，引导用户填写 API Key
- **默认模型切换**：deepseek-v4-pro 作为默认模型
- `--version` 选项，统一版本号注入

### 修复
- IME 输入残影修复
- 工具执行期间会话卡死和探测死循环修复
- 无工具调用时压缩统计缺失修复

### 重构
- 移除 viewport 组件，改为手动滚动控制
