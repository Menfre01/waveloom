# Waveloom

终端编码代理（Go 实现），帮助用户编写、重构、调试和探索代码。

## 项目概要

- **语言**：Go 1.25+
- **LLM**:DeepSeek(默认)/ Kimi / OpenAI,通过 `llm.Client` 接口 + adapter 适配,支持 `/provider` 运行时切换
- **TUI**：Bubble Tea v2 + Glamour Markdown 渲染 + Lipgloss 样式
- **LSP**:`edit`/`write` 后自动运行 LSP diagnostics 验证(内置 gopls / rust-analyzer / typescript-language-server / clangd),未安装时静默跳过;构建工具(go build / npx tsc / cargo build / make)兜底
- **构建**：`make build` / `make test` / `make run`

```
cmd/waveloom/    CLI 入口(main, config, runner, tui)
pkg/
  agentloop/     Think-Act-Observe 循环(Run → <-chan TurnEvent)
  bash/          Shell 命令 AST 解析与危险命令安全检测
  compaction/    四级水位线上下文压缩(Snip/Prune/Summarize)
  environment/   工具链探测
  filehistory/   文件历史备份、快照、回退
  hook/          Hook 系统(PreToolUse/PostToolUse 等事件,settings.json 配置外部脚本)
  llm/           LLM Client(DeepSeek/Kimi/OpenAI adapter、流式、重试)
  logging/       日志
  lsp/           LSP 诊断客户端(edit/write 后自动验证)
  mcp/           MCP 客户端(配置、传输、工具代理)
  memory/        AGENTS.md 层级加载
  pathutil/      路径工具
  permission/    权限守门人(规则引擎、路径/命令安全)
  plugin/        插件发现
  pricing/       LLM token 计费(CNY/USD 双币种、模型级增量计价)
  reference/     @ 文件引用展开
  session/       跨轮次消息历史与持久化(PrepareRun / CompleteRun,JSONL)
  shellutil/     Shell 命令处理共享实用函数(供 tool/skill 复用)
  skill/         Skill 系统(.claude/skills/ 与 .waveloom/skills/ 双路径加载)
  slashcommand/  / 命令面板
  subagent/      子代理(Fork/Cold/Explore)
  task/          后台任务管理
  todo/          Todo 状态管理
  tool/          工具系统(内置工具,TypedTool[P] 泛型接口)
  tuitest/       TUI 测试辅助(Bubble Tea v2 model 测试)
specs/           各组件规格书(修改前先阅读;内部文档,不纳入公开仓库)
```

## 编码规范

- **跨平台兼容**:所有代码必须同时兼容 Windows / Linux / Darwin 三平台:
  - 文件系统操作优先使用 `filepath.WalkDir`、`os.ReadDir` 等标准库,禁止直接调用外部命令(如 `find`、`ls`、`dir`)
  - 路径拼接必须使用 `filepath.Join`,分隔符使用 `filepath.Separator`,禁止硬编码 `/` 或 `\`
  - 外部 API 调用前确认第三方包是否声明了跨平台支持,必要时用 `runtime.GOOS` 条件编译

## 代码审查

- 完成较大的代码改动(涉及 3+ 文件或 50+ 行变更)后,**自动**启动代码审查,审查维度包括:逻辑正确性、跨平台兼容、边界条件、安全风险
- 审查完成后将结果直接反馈给用户,无需用户主动要求

## 开发流程

### Wave 开发

- 任务拆分以单个组件高内聚、组件之间低耦合为原则，每个任务拆为独立 Wave，按"组件开发 → 测试 → 验收 → 组装"推进
- Wave 开始前产出规格书(文件清单、组件边界/依赖/不变量、集成点),完成后执行测试和 review
- 相互独立的 Wave 使用 subagent 并行执行;有依赖关系的 Wave 由主 agent 串行推进,等待关键依赖完成
- 并行安全约束:同一 Wave 内不修改同一文件;子任务完成时列出修改文件路径,供主 agent 汇总

### TDD

- Red → Green → Refactor；测试覆盖率 ≥97%（排除 OS/文件系统不可模拟路径）

### Bug 修复回归防护

- 每个 Bug 修复**必须**附加回归防护：
  - **可测**：编写 `TestRegression_<简述>`，断言命中根因
  - **不可测**：修复点上方加 `// REGRESSION: <根因>。无法单测：<理由>`
- 同一代码区域累积 ≥3 条 → 视为脆弱模块，优先重构而非继续修补

## 构建与测试

**禁止直接调用 `go build` / `go install`**,统一使用项目构建系统:

| 操作 | 命令 | | 测试范围 | 命令 |
|------|------|---|----------|------|
| 编译 | `make build` | | 单文件/单包 | `go test ./pkg/<name>/ -run TestXxx` 或 `go test ./pkg/<name>/` |
| 安装 | `make install` | | 多包/跨包 | `make test` |
| 运行 | `make run` | | 集成测试 | `make test-integration` |
| 清理 | `make clean` | | | |

修改 `pkg/` 或 `cmd/` 后,运行中的 TUI 不会自动重载新二进制,需重启生效。

## 文档规范

- 架构/流程/数据模型绘图优先使用 Mermaid

## 提交策略

**禁止自动提交**。必须等待用户明确给出指令（如"提交"、"commit"）后方可执行 `git add` / `git commit` / `git push`。

## Commit 规范

[Conventional Commits](https://www.conventionalcommits.org/zh-hans/v1.0.0/) v1.0.0：

```
<type>(<scope>): <subject>
```

- `type`: `feat` / `fix` / `refactor` / `test` / `docs` / `chore`
- `scope`: 包名(`llm` / `loop` / `tool` / `tui` / `session` / `compaction` / `lsp` / `pricing` / ...),多 scope 用 `/` 分隔
- `subject`: 中文祈使句，≤72 字符，不以句号结尾

```
feat(loop): Run() 增加 VerboseWriter 支持
fix(session): ToolCall UnmarshalJSON 缺失导致 --resume 加载时 tool_calls 丢失
```

## Release 规范

**发布前置校验**（必须全部通过后方可继续发布流程）：

```sh
make build && make test && make lint
```

任一失败 → 先修复，再重新走校验。

Release notes 以用户可感知的功能变化为描述单位，分类汇总：

- **新增功能** — 新特性、模块、命令
- **修复** — Bug 修复
- **重构** — 重大模块重构
- **性能优化** — 性能相关

`docs` / `chore` / `test` 类型不列入。

**Release body 格式**：主体为中文 changelog 分类汇总，末尾追加英文 changelog 锚点，方便英文用户查看：

```
## [vX.Y.Z] — YYYY-MM-DD

### 新增功能
- ...

### 修复
- ...

### 重构
- ...

---

📝 [Changelog (English)](https://github.com/Menfre01/waveloom/blob/dev/CHANGELOG.en.md)
```

发布由 GitHub Actions 自动完成（tag push `v*` → `.github/workflows/release.yml`）。

手动步骤（release workflow 之前完成）：

1. **汇总 changelog** — 从上次 tag 到 HEAD 扫描 commit，按分类汇总，更新 `CHANGELOG.md` 和 `CHANGELOG.en.md`；`CHANGELOG.md` 中每个版本条目末尾必须包含英文 changelog 锚点（格式见上文 Release body 格式）
2. **核对日期** — 检查 `CHANGELOG.md` 和 `CHANGELOG.en.md` 中新版本的日期是否为当天日期（`date '+%Y-%m-%d'`），防止日期偏移
3. **核对英文锚点** — 检查 `CHANGELOG.md` 中新版本条目末尾是否包含英文 changelog 锚点（搜索 `📝 [Changelog (English)]`），确保 Release body 末尾有英文入口
4. **审查 Windows 兼容性** — 检查本次变更涉及的代码是否存在平台依赖问题：
   - 路径拼接是否使用 `filepath.Join`，无硬编码 `/` 或 `\`
   - 文件遍历优先使用 `filepath.WalkDir` / `os.ReadDir`，无外部命令
   - 新增依赖是否声明跨平台支持
   - Git diff 中新增的 `/` 分隔符确认是 Go 导入路径（安全）而非文件系统路径
5. **审查 README** — 检查 `README.md` 和 `docs/README.en.md` 是否需要同步新功能
6. **审查双语文档** — 检查 `CONTRIBUTING` / `SECURITY` / `docs/` 下中英双语是否同步
7. **文档提交** — 如有文档修改，先 commit（类型 `docs`）
8. **打 tag 并推送** — `git tag vX.Y.Z && git push origin dev && git push origin vX.Y.Z`
