# Changelog

## [v0.1.0-alpha.9] — 2026-07-02

### 新增功能
- **Plan Mode 先规划后执行工作流**：进入 Plan 模式后仅允许读写 plan 文件，源文件写保护；shell 风险分流（RiskLow 提升为 ALLOW，RiskMedium/High 不变）；审批通过后方可执行代码修改；支持 `Shift+Tab` 快捷键进入/退出；`enter_plan_mode` / `exit_plan_mode` 工具；TUI overlay 审批框；`[plan:start #xxxx]` / `[plan:end #xxxx]` 消息对追踪

### 修复
- **Shell 多行续行 JSON 转义指引**：Shell 工具 Description 增加 `\\\n` 多行命令转义示例，减少 LLM 生成无效转义 JSON 的概率

## [v0.1.0-alpha.8] — 2026-07-01

### 新增功能
- **Slash 命令中英双语**：SlashMessages 注入机制实现 slash 命令文案根据 locale 自动切换
- **not_dir 增强**：read_file 传入目录时提供 Did you mean 文件建议，blank-line 自动修正

### 修复
- **DenialTracker 熔断移除**：连续拒绝后不再封锁所有工具，每个请求独立评估（Step 1.5 移除）
- **LSP 工具 schema**：增加非代码文件软约束，减少误用

### 重构
- **组件解耦**：消除 tool / slashcommand / context / agentloop / pathutil 间编译期耦合，提升独立演进能力

## [v0.1.0-alpha.7] — 2026-07-01

### 新增功能
- **i18n 多语言支持**：zh-CN / en-US 完整双语文案，TUI 界面全覆盖，LANG 环境变量自动检测
- **--locale CLI 参数**：`auto`（默认）/ `zh-CN` / `en-US`，三级优先级：CLI > settings.json > LANG
- **/locale slash 命令**：TUI 内实时切换界面语言，立即生效并持久化到 settings.json
- **CLI --help 中英双语**：根据 locale 显示对应语言的帮助文本
- **首次设置向导重写**：Bubble Tea + huh 表单交互，集成语言/主题/Provider/模型一站式配置
- **自更新检查**：空闲时检测 GitHub Release 新版本，Enter 一键下载安装

### 修复
- 权限命令链绕过修复，风险分级扩展，DenialTracker 敏感路径接入
- Esc 中断时杀死进程组，防止长时间 bash 命令无法退出
- install.sh 移除 GitHub API 限流依赖，改用 releases/latest/download 重定向

## [v0.1.0-alpha.6] — 2026-06-30

### 新增功能
- **Skill 系统**：兼容 Claude Code Skill 格式，自动加载 `~/.claude/skills/` 目录中已有 Skill，无需任何迁移
- **Skill 白名单与条件激活**：`allowed-tools` Bash 命令白名单、`paths` 条件激活（gitignore-style glob）、Guard 权限集成
- **AskUserQuestion 选择题**：LLM 向用户发起单选/多选/Other 自定义输入/拒绝交互，TUI overlay 渲染
- **edit_file 空白归一化**：匹配唯一时自动修复空白差异，减少 LLM 重试轮次
- **edit_file 空白符降级**：no_match 诊断增强，宽松空白匹配回退

### 修复
- `--resume` 恢复时 tool_calls 反序列化丢失 Name/Arguments
- Session 恢复时空响应防护，增强反序列化完整性校验
- web_fetch HTML 实体解码、缺失 Content-Type 容错、超时返回部分内容
- 工具错误态展开/折叠渲染修复，ToolResult 为空时回退展示 ToolError
- System prompt 与工具描述间的推理空隙消除
- macOS/Linux 符号链接偏差导致路径误判

### 重构
- TUI 输入框水平滚动重构为 syncInputVisibleStart

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
- **AGENTS.md @ 引用展开**：支持 `@path/to/file` 引用外部文件，自动展开合并、去重
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
