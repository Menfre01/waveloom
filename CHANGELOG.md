# Changelog

## [v0.1.0-alpha.14] — 2026-07-04

### 新增功能
- **退避机制重构**：引入 Tool+Kind 双重键退避跟踪，三段式渐进警告（3/5/8 次），loop 间跨 turn 持久化退避状态，减少同类错误无意义重试

### 修复
- **@ 文件选择器巨型目录无响应**：`filepath.WalkDir` 遍历超大目录时不截断、实时显示进度，绝对路径搜索不再超时
- **@ ../ 路径基准错误**：`doScanRelative` 解析 `../` 相对路径时 CWD 基准修复，确保兄弟目录搜索结果正确
- **Windows CI 测试失败**：`relativizePaths` 单元测试硬编码 Unix 路径，在 Windows 平台 `filepath.IsAbs` 无盘符判定为假导致 `filepath.Rel` 异常，修复为跨平台绝对路径构造

### 重构
- **@ 选择器跨平台兼容**：用 `filepath.WalkDir` 替代 `find` 外部命令，Windows / Linux / Darwin 三平台统一搜索逻辑

## [v0.1.0-alpha.13] — 2026-07-04

### 修复
- **@ 父目录搜索不显示当前项目**：`doScanRelative` 将 CWD 目录项 prepend 到候选项开头，避免被 500 条截断丢弃；同时修复 `../waveloom/` 解析回 CWD 后 display 前缀丢失导致无法继续搜索子文件
- **@ / / 选择器排序优化**：prefix/substr 组内按匹配位置升序（越左越优先），非连续子串排最后；`/` 命令选择器同样策略
- **expander `ls` 伪工具清理**：文件与目录引用统一走 `read_file` 权限检查，消除对已删除 `ls` 工具的引用

## [v0.1.0-alpha.12] — 2026-07-04

### 新增功能
- **多行输入框**：输入框从单行改为多行 textarea，固定 2 行高度，内容超出自动折行；第一行显示 `›` 前缀，后续行缩进对齐；终端原生 real cursor 替代 ANSI 虚拟光标；布局动态计算行数，避免 HUD 被挤压
- **Windows 平台支持**：完整支持 Git Bash 集成与 Windows 平台工具链
- **RiskClassSafe 安全分级**：`kill_background_task` 默认放行，减少不必要权限确认

### 修复
- **流式输出换行抖动**：新增 `wrapLineStable` 硬截断替代 word-wrap，流式期间换行位置由列号唯一决定，不随文字增长漂移；覆盖 assistant/thought/tool 三条流式路径
- **错误颜色区分**：Recoverable 错误显示金色、Fatal 错误显示红色，原全部显示红色
- **/clear 别名搜索与技能刷新**：命令选择器支持别名模糊搜索；Session reset 后重新注册技能命令

## [v0.1.0-alpha.11] — 2026-07-03

### 新增功能
- **后台命令完整支持**：`ShellParams` 增加 `run_in_background` 显式参数；`&` 向后兼容（单行 `&` → 剥离后后台执行，多行 `&` → 前景 + log 提示）；`Execute`/`ExecuteStreaming` 共享文件 fd 输出消除 SIGPIPE；`task.Registry` 后台任务注册、状态追踪、退出码记录；`kill_background_task` SIGKILL 进程组终止；跨 turn 注入 `<background-task>` 通知；Skill execShell 后台命令不卡死

### 修复
- **权限子串误伤修复**：`sh -c`/`bash -c` 等 10 条双 keyword 内联执行模式添加 `FirstTokenOnly`，防止路径/flag 子串命中导致误判 RiskHigh；权限测试覆盖率提升至 95%

## [v0.1.0-alpha.10] — 2026-07-03

### 新增功能
- **Shell 流式输出**：长命令（如 `make build`、`npm install`）输出逐行实时推送到 TUI，无需等待命令结束即可看到执行进度
- **@ 文件选择器增强**：支持 `../` 兄弟目录、绝对路径和 `~/` 外部目录搜索，跨项目引用文件更便捷
- **权限规则 glob 增强**：`matchGlob` 支持 `**` 递归路径匹配

### 修复
- **后台命令管道泄漏导致 TUI 卡死**：`bash -c "command &"` 不再冻结 TUI。后台进程自动重定向到临时日志文件；`ExecuteStreaming` `wg.Wait()` 和 `executeToolCalls` 并发工具等待均增加超时保护，三层防护确保 TUI 任何情况下不永久卡死
- **权限安全增强**：补充高危命令拦截模式（提权、内联执行），扩大安全命令白名单（grep/find/echo/mkdir 及构建工具），首 token 精确匹配防路径子串误伤，邻接匹配消除 AND 误报
- **edit_file Unicode 归一化**：增加 Unicode 归一化和行号前缀自动修复降级，减少 LLM 因不可见字符差异导致的 no_match 重试
- **Shell Description 优化**：单行命令硬约束，移除多行续行教程，减少 LLM 生成无效 JSON 的概率

### 重构
- **移除 LSP 模块**：精简 grep/search_file/ls 工具，工具集从 13 个收敛至 9 个核心工具，代码验证统一通过构建工具完成，降低复杂度
- **全面国际化**：System Prompt 根据 locale 动态切换中英文，CLI 输出全面双语化

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
