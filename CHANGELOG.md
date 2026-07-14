# Changelog


## [v0.1.0-beta.9] — 2026-07-14

### 新增功能
- **Hashline 编辑模型**：引入 hashline read/edit/write 编辑工具替代旧 read_file/edit_file，支持 TAG 锚定编辑、SWAP/INS/DEL/REM/MV 六种操作、空 body 行保留；edit 响应自动追加编辑后上下文，LLM 可链式编辑无需重新 read
- **yarn/pnpm/clang 工具链探测**：环境探测新增 yarn、pnpm、clang 支持，覆盖更多前端和 C/C++ 项目场景
- **web_search 超时控制**：web_search 新增 `timeout_ms` 参数支持，防止搜索请求长时间阻塞
- **Skill $@ bash 兼容语法**：变量替换增加 `$@` bash 兼容语法

### 修复
- **Hashline TAG 稳定性**：TAG 内容摘要算法重构确保文件内容不变时 TAG 不变；recovery 范围不变量校验消除静默损坏风险
- **Hashline LLM 格式兼容**：容忍尾部冒号、前导空白、行尾注释、INS 无冒号、大小写混用、单引号路径等 LLM 常见格式偏差
- **Hashline 路径对齐**：修复 edit 与 read 路径不对齐导致跨 turn tag_mismatch
- **Hashline 格式混淆提示**：parseLineRange 对 `:=` 格式混淆提供友好错误提示
- **Subagent Fork 消息清理**：fork 消息构建清理孤儿 tool_calls，修复缓存命中率异常
- **Subagent 写操作追踪**：修复 hashline edit 写操作在 subagent_write_operations 中缺失的追踪
- **Subagent/Permission 安全加固**：修复 Fork boilerplate、敏感文件分类和清理指引
- **Permission 规则修复**：edit_file/write_file 规则因 normalizeToolName 缺失兼容映射导致不生效
- **Permission Bash 白名单**：前缀匹配增加命令链操作符检测，防止绕过
- **Agent Loop Todo 残留**：ReasonCompleted 前检测残留 todo，注入最后机会提醒防止列表残留
- **Memory UTF-8 处理**：非法 UTF-8 序列不再静默替换为 U+FFFD，改为保留原始内容并记录警告
- **Shellutil 后台命令检测**：IsBackgroundCommand 不再误将 `&&` 结尾的命令识别为后台命令
- **TempDir 符号链接**：os.TempDir() 替换为 pathutil.TempDir() 解决 macOS 符号链接路径不一致
- **Context/Task 持久化**：--resume 后 lastBackgroundCheck 持久化，中断任务状态恢复
- **TUI 帮助 overlay**：修复 ? 帮助 overlay 快捷键文字对比度不足

### 重构
- **工具统一重命名**：移除旧 read_file/edit_file 工具注册和 hashline 开关，read/edit/write 短名统一注册
- **TUI Logo 布局**：将 logo 从 header 移入 viewport 可滚动区域，释放固定行高
## [v0.1.0-beta.8] — 2026-07-13

### 新增功能
- **首次使用体验优化**：首次运行无配置时自动进入 setup 向导，不再报错退出；空 API Key 留在原地提示错误；保存前校验 API Key 有效性（ListModels 轻量验证）；TUI 空状态显示引导面板（/ 命令、@ 引用、⏎ 发送、示例 prompt）；LLM/网络错误人性化映射（humanizeError），不再透传原始 JSON；环境探测结果缓存 24h，PATH 变化自动失效，二次启动免等待；更新通知改为 footer 三态切换

### 修复
- **plugin lint**：修复 os.MkdirAll 返回值未检查导致的 lint errcheck 警告
- **Windows 路径兼容性**：`stripCWDPrefix`、`pathPrefixMatch`、`extractDirPrefix` 归一化路径分隔符为 `/`，使用 `filepath.ToSlash`/`IsAbs`/`Dir` 替代硬编码 `/`，修复文件选择器在 Windows 上过滤和显示异常

### 重构
- **System Prompt 重排**：C2 行为约束移入 C1，按注意力机制重排 section 顺序，C3c 改为 Append 策略，提升指令遵循率
- **TodoWrite 工具拆分**：新增 ToolWithPrompt 可选接口，工具可分别提供短 Description（~60 token）和 Prompt 使用指南（~1200 token），Registry 自动拼接，不侵入 system message，前缀缓存不受影响
- **Todo 提醒系统强化**：StatusSummary 被动备注改为主动指令检查点；idleTodoWrite/idleTodoReminder 阈值从 3 降为 2；todoReminderText 嵌入 staleness 计数去除忽略出口；新增 14 个回归测试覆盖提醒/注入/计数器全部路径
- **子代理-Todo 生命周期绑定**：回退 TodoState context 传播机制（删除 WithTodoState 链路），转为 C1 END prompt 引导：主代理派生子代理前设 todo 为 in_progress，返回后更新为 completed；并行子代理显式 3 轮节奏；子代理 `allAgentDisallowed` 加入 `todo_write`

## [v0.1.0-beta.7] — 2026-07-12

### 新增功能
- **Claude Code 插件兼容**：自动发现并加载已安装的 Claude Code 插件中的 skills/commands，通过 `installed_plugins.json` + `enabledPlugins` 配置管理，同名 skill 以用户手动创建优先，支持标准 skills/commands 目录和 manifest 声明的自定义路径（[#2](https://github.com/Menfre01/waveloom/issues/2)）
- **Advisor mode /model 提示**：advisor 模式下使用 `/model` 切换模型时，追加提示"切换模型不改变 normal/advisor 模式"
- **工具错误逐级退避**：advisor mode 下工具错误增加逐级退避机制，降低连续失败时的 token 浪费

## [v0.1.0-beta.6] — 2026-07-11

### 新增功能
- **Advisor Mode 双模型成本优化**：子代理 advisor 类型使用 flash 模型处理评估任务，主 agent 保留 pro 模型做深度推理，评估任务 token 成本降低约 50%
- **Overlay/Rewind TUI 增强**：overlay 面板统一铺满终端宽度消除窄边截断；rewind 消息选择器支持自适应宽度、内容截断与滚动交互

### 修复
- **TUI 持久化修复**：暗色/亮色检测改用 Bubble Tea `BackgroundColorMsg` 系统事件，修复部分终端上主题持久化静默失败
- **Plan Mode 模型切换修复**：手动进入 plan mode 时 advisor 模型未从 pro 切换为 flash 的问题
- **System Prompt 推理漏洞**：全面审计并修复 agent system prompt 中 2 处可能导致 LLM 绕过约束的推理漏洞

### 重构
- **模型配置 Settings 驱动重构**：LLM 模型配置完全由 settings.json 驱动，移除所有硬编码模型常量，用户可通过 settings 自定义任意 LLM 参数

## [v0.1.0-beta.5] — 2026-07-10

### 新增功能
- **Checkpoint/Rewind 时间旅行**：支持将对话回退到任意历史用户消息，同时恢复所有文件变更。每次编辑前自动备份原始文件至 `.waveloom/file-history/`，每轮用户消息创建检查点。提供 TUI 选择界面（消息列表 + 确认对话框），支持 Fork 模式（原 session 完整保留，不丢历史）
- **Glamour Dracula 语法高亮**：dark 主题下 Glamour Markdown 代码块从 DarkStyle 切换为 Dracula 配色，Comment / Keyword / LiteralString 等 25+ token 类型对比度大幅提升

### 修复
- **dark 主题可读性**：Gray / Muted 提亮，改善暗色终端下的文本对比度
- **HUD 布局修复**：新内容提示不再挤占 HUD 显示行；展开态宽度溢出修正；工具输出截断沿 UTF-8 rune 边界安全切割，避免多字节字符乱码
- **i18n 补全**：补全 subagent 后缀等 4 处硬编码中文，统一 Messages 国际化

### 重构
- 精简 `todo_write` 提示词，规则集中到 system prompt，降低工具描述 token 消耗

## [v0.1.0-beta.4] — 2026-07-09

### 新增功能
- **`web_search` 内置工具**：DDG（默认）+ Brave（可选）双后端搜索引擎，与 `web_fetch` 联动形成搜索→读取闭环；TUI 专用段落渲染支持参数展示、摘要预览和结果展开
- **MCP 桌面版自动发现**：自动识别 Claude 桌面版配置（macOS/Windows/Linux 三平台路径），无需手动配置即可接入已有 MCP Server

### 重构
- **`todo_write` 触发阈值优化**：触发条件从 ≥2 turns 收紧为 ≥5 turns，parallel subagents 改为 serial subagents，idleTodoReminder 从 2 调整为 3，减少小型任务滥用

## [v0.1.0-beta.3] — 2026-07-09

### 新增功能
- **色盲友好双主题**：ColorBlind 拆分为 Dark CB（深色终端）和 Light CB（浅色终端），保留蓝/橙 diff 配色，各有一套完整色板
- **主题持久化**：Ctrl+G / `/theme` 切换主题后自动保存到 settings.json，下次启动恢复
- **Glamour Markdown 全面同步主题色板**：段落正文、引用块、表格、分割线、斜体/加粗/删除线、列表符号等 12 类元素色全部与 Waveloom 主题同步
- **emoji 渲染**：支持 `:rocket:` 等短码渲染为 Unicode emoji
- **True Color 代码高亮**：Chroma 语法高亮升级为 `terminal16m`（1670 万色）
- **`?` 快捷键帮助 overlay**：按 `?` 弹出所有快捷键列表，纵向排版确保窄终端完整显示

### 修复
- 子 agent token 消耗和缓存命中率累加到主 agent HUD 统计
- Windows `splitPathParts` 盘符死循环导致 5 分钟超时
- `/new` 后欢迎提示不重现（现已忽略纯系统消息段落）
- 新内容到达提示误占渲染行导致光标上移

### 重构
- 帮助 overlay 从 FullHelpView 列布局改为 ShortHelpView 纵向渲染，消除窄终端截断
- 空状态判断逻辑泛化为忽略系统段落，后续新增系统消息不再影响欢迎提示

## [v0.1.0-beta.2] — 2026-07-08

### 新增功能
- **子代理结构化事件渲染**：TUI 展开态按事件类型差异化渲染 — thought 以 dimmed 斜体显示思考过程、tool 名绿色粗体 + args 代码色、工具输出 │ 前缀缩进；新增 `SubagentThought` 和 `SubagentToolStream` 事件类型
- **Layer 3 事后安全分类器**：子代理执行完成后自动扫描事件列表，检测危险命令（rm/chmod/sudo/shutdown 等）和敏感文件操作（.env 写入），生成 `HIGH`/`MEDIUM`/`LOW` 三级安全警告，以 `<subagent_security_warning>` XML 块注入父 LLM 结果
- **Explore 自动小模型**：`Explore` 类型子代理未指定模型时自动选用 `sub_model` 配置（如 `deepseek-v4-flash`），降低探索类任务的 token 成本
- **Footer thinking 档位显示**：模型名旁显示 `(think high)` / `(think max)`，自动从 `reasoning_effort` 配置解析，thinking 关闭时不显示
- **Subagent Transcript 持久化**：`TranscriptLine` 新增 8 个 subagent 字段（类型/模型/轮次/token/事件 JSON），支持 `--resume` 完整恢复子代理段落状态

### 修复
- `extractPath` edit_file 格式适配：从 emoji 前缀 `"✅ Edit applied to"` 改为 `"Edited file:"` 前缀解析
- `ToolCallStream` 事件 Kind 从 `SubagentToolResult` 修正为独立的 `SubagentToolStream`，避免流式 chunk 与最终结果重复渲染

### 重构
- 精简 system prompt 与 tool description，分离职责减少 token 消耗

## [v0.1.0-beta.1] — 2026-07-07

### 新增功能
- **MCP Client**：完整 MCP 客户端实现 — 连接外部 MCP Server，自动工具发现与注册，与内置工具并列显示；支持 SSE 和 stdio 传输，`mcpServers` 配置与 Claude Code `.claude.json` 兼容
- **Todo 任务列表**：完整 todo 状态管理系统 — `todo_write` 工具、TUI 侧边面板、周期性提醒、pending/in_progress/completed 三态流转；支持并行子代理多 in_progress、标题显示完成进度
- **Subagent 增强**：fork 身份注入保持调用链可追溯；evaluation/verification 冷 agent（独立视角评审、对抗验证）；模型自动切换（deepseek-v4-pro 深推理 vs flash 日常）；缓存友好消息构造最大化前缀命中
- **Todo 周期性提醒**：替代 ReminderInjected 一次性注入，todo 列表未完成时按时钟频率自动提醒 LLM

### 修复
- **MCP**：goroutine 泄漏、SSE 行解析异常、退出码错误等 9 个问题；日志默认写入 `io.Discard` 避免泄漏到 TUI
- **Agent Loop**：`resultsCh` 双重 panic、Guard nil 解引用等 4 个缺陷；残留 todo 时 `ReminderInjected` 跨 turn 未重置
- **Subagent**：`forwardEvents` 扇出通道解耦消除死锁；并发事件路由修复、中间 turn 文本裁剪、`bash_subagent` 隔离
- **Todo**：merge 模式不删除未传入项；LLM 直接替换而非逐步更新的工作流引导
- **TUI**：多行用户消息仅首行显示 `›` 前缀；`--resume` 恢复后已清空的 todolist 仍然出现；todo 面板 pending 项默认字体色
- **Windows**：`install.ps1` 自动配置 PATH 与 Git Bash `~/.bashrc`；Go module 路径适配 Windows 反斜杠

### 重构
- Todo 去掉 ID 和 merge 机制，LLM 每次传入完整列表以消除状态不一致
- Todo 移除 in_progress 单任务限制，支持并行子代理多任务同时进行
- Subagent 提取 `ensureNonEmpty` 消除 anyText 状态追踪
- 收紧 `todo_write` 触发条件减少小型任务滥用
- 强化系统提示词中 `deepseek-v4-flash` 默认推荐力度

## [v0.1.0-alpha.15] — 2026-07-06

### 新增功能
- **Subagent 委托**：新增 `agent` 工具，支持 fork 和 cold agent 两种模式，子 agent 可独立执行复杂多步任务；cold agent 冷启动（无上下文延续），适用于探索性任务

### 修复
- **Windows Git Bash 兼容性**：Shell 解释器探测优先通过 `exec.LookPath` 在 PATH 中查找 `bash.exe`，修复 Git Bash 内 setup 能跑但正常启动崩溃的问题；`resolveWindowsShell` 不再 `os.Exit(1)`，改为返回空字符串由调用方处理
- **权限规则引擎 Windows 路径适配**：`splitPath`/`matchGlob` 使用 `filepath.ToSlash` 统一归一化 `\` 分隔符，修复 Windows 下文件路径 glob 规则（如 `src/**`）无法匹配的问题
- **自更新 `os.Chmod` Windows 守卫**：`SelfUpdate` 和 `extractWaveloom` 增加 `runtime.GOOS != "windows"` 守卫，避免 Windows 上 `Chmod(0o755)` 报错阻塞更新流程
- **`/tmp` 工作目录白名单平台守卫**：`Guard` 初始化时仅 Unix 平台添加 `/tmp` 到工作目录白名单，Windows 使用 `os.TempDir()`
- **命令安全 `extractFirstToken` 适配 `\`**：增加 `\` 回退分支，确保 Windows 绝对路径命令提取正确
- **`/proc/self/fd/` 路径检查平台守卫**：增加 `runtime.GOOS != "windows"` 守卫，Windows 无 `/proc/` 文件系统

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
