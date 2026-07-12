# Waveloom

终端编码代理（Go 实现），帮助用户编写、重构、调试和探索代码。

## 项目概要

- **语言**：Go 1.25+
- **LLM**：DeepSeek V4（默认）/ OpenAI，通过 `llm.Client` 接口适配
- **TUI**：Bubble Tea v2 + Glamour Markdown 渲染 + Lipgloss 样式
- **LSP**：已移除。代码验证统一通过构建工具（go build / npx tsc / cargo build / make）完成。
- **构建**：`make build` / `make test` / `make run`

```
cmd/waveloom/    CLI 入口（main, config, runner, tui）
pkg/
  agentloop/     Think-Act-Observe 循环（Run → <-chan TurnEvent）
  compaction/    四级水位线上下文压缩（Snip/Prune/Summarize）
  context/       跨轮次消息历史（PrepareRun / CompleteRun）
  environment/   工具链探测
  llm/           LLM Client（DeepSeek + OpenAI 适配、流式、重试）
  mcp/           MCP 客户端（配置、传输、工具代理）
  memory/        AGENTS.md 层级加载
  pathutil/      路径工具
  permission/    权限守门人（规则引擎、路径/命令安全）
  reference/     @ 文件引用展开
  shellutil/     Shell 工具
  skill/         Skill 系统（.claude/skills/ 加载执行）
  slashcommand/  / 命令面板
  subagent/      子代理（Fork/Cold/Explore）
  task/          后台任务管理
  todo/          Todo 状态管理
  tool/          工具系统（内置工具，TypedTool[P] 泛型接口）
specs/           各组件规格书（修改前先阅读）
```

## 编码规范

- 遵循 Go 社区惯例（Effective Go，标准库 project layout），不过度设计，命名清晰避免缩写
- 错误统一处理，不外泄堆栈到客户端；查询 API 优先用 `go doc`
- **跨平台兼容**：所有代码必须同时兼容 Windows / Linux / Darwin 三平台：
  - 文件系统操作优先使用 `filepath.WalkDir`、`os.ReadDir` 等标准库，禁止直接调用外部命令（如 `find`、`ls`、`dir`）
  - 路径拼接必须使用 `filepath.Join`，分隔符使用 `filepath.Separator`，禁止硬编码 `/` 或 `\`
  - 外部 API 调用前确认第三方包是否声明了跨平台支持，必要时用 `runtime.GOOS` 条件编译

## Agent 操作规范

### 修改前后检查清单

| 阶段 | 操作 |
|------|------|
| 修改前 | shell('find . -name "*.go"') / shell('grep -rn "pattern" .') 定位 → read_file 确认行号和内容 |
| 修改后 | 构建验证 → make build 编译 → make test（涉及 pkg/ 时） |
| 重构前 | shell('grep -rn ...') → read_file → 评估影响范围 |

### 工具调用原则

- **独立只读操作并行**（read_file），写操作串行
- **局部修改用 edit_file**，新建或完全覆写才用 write_file
- **edit_file 铁律**：old_string 必须精确匹配文件当前内容（缩进、空行、标点完全一致）。可靠来源：2 轮内 read_file 返回且期间无其他编辑。不可靠：记忆、跨多轮的旧 read、期间有编辑的旧 read。不确定时宁可多读一次，浪费一次调用好过 no_match 循环。
- `no_match` → 不要盲目重试，先 read_file 确认 old_string 精确内容（含缩进），再重试
- `security_violation` → 致命错误，停止当前路径

## 开发流程

### Wave 开发

- 任务拆分以单个组件高内聚、组件之间低耦合为原则，每个任务拆为独立 Wave，按"组件开发 → 测试 → 验收 → 组装"推进
- Wave 开始前产出规格书（文件清单、组件边界/依赖/不变量、集成点），完成后由 cold agent 执行测试和 review
- 主 agent 负责协调 Wave 边界，等待关键依赖完成后才推进；同一 Wave 内不应修改同一文件，子任务完成时列出修改文件路径

### TDD

- Red → Green → Refactor；测试覆盖率 ≥97%（排除 OS/文件系统不可模拟路径）

### Bug 修复回归防护

- 每个 Bug 修复**必须**附加回归防护：
  - **可测**：编写 `TestRegression_<简述>`，断言命中根因
  - **不可测**：修复点上方加 `// REGRESSION: <根因>。无法单测：<理由>`
- 同一代码区域累积 ≥3 条 → 视为脆弱模块，优先重构而非继续修补

## 构建与测试

**禁止直接调用 `go build` / `go install`**，统一使用 Makefile：

| 操作 | 命令 | | 测试范围 | 命令 |
|------|------|---|----------|------|
| 编译 | `make build` | | 单文件/单包 | `go test ./pkg/<name>/ -run TestXxx` 或 `go test ./pkg/<name>/` |
| 安装 | `make install` | | 多包/跨包 | `make test` |
| 运行 | `make run` | | 集成测试 | `make test-integration` |
| 清理 | `make clean` | | | |

## 文档规范

- 架构/流程/数据模型先用 ASCII 绘制，再转换为 Mermaid

## 提交策略

**禁止自动提交**。必须等待用户明确给出指令（如"提交"、"commit"）后方可执行 `git add` / `git commit` / `git push`。

## Commit 规范

[Conventional Commits](https://www.conventionalcommits.org/zh-hans/v1.0.0/) v1.0.0：

```
<type>(<scope>): <subject>
```

- `type`: `feat` / `fix` / `refactor` / `test` / `docs` / `chore`
- `scope`: 包名（`llm` / `loop` / `tool` / `tui` / `context` / `compaction` / …），多 scope 用 `/` 分隔
- `subject`: 中文祈使句，≤72 字符，不以句号结尾

```
feat(loop): Run() 增加 VerboseWriter 支持
fix(context): ToolCall UnmarshalJSON 缺失导致 --resume 加载时 tool_calls 丢失
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

1. **汇总 changelog** — 从上次 tag 到 HEAD 扫描 commit，按分类汇总，更新 `CHANGELOG.md` 和 `CHANGELOG.en.md`
2. **核对日期** — 检查 `CHANGELOG.md` 和 `CHANGELOG.en.md` 中新版本的日期是否为当天日期（`date '+%Y-%m-%d'`），防止日期偏移
3. **审查 Windows 兼容性** — 检查本次变更涉及的代码是否存在平台依赖问题：
   - 路径拼接是否使用 `filepath.Join`，无硬编码 `/` 或 `\`
   - 文件遍历优先使用 `filepath.WalkDir` / `os.ReadDir`，无外部命令
   - 新增依赖是否声明跨平台支持
   - Git diff 中新增的 `/` 分隔符确认是 Go 导入路径（安全）而非文件系统路径
4. **审查 README** — 检查 `README.md` 和 `docs/README.en.md` 是否需要同步新功能
5. **审查双语文档** — 检查 `CONTRIBUTING` / `SECURITY` / `docs/` 下中英双语是否同步
6. **文档提交** — 如有文档修改，先 commit（类型 `docs`）
7. **打 tag 并推送** — `git tag vX.Y.Z && git push origin dev && git push origin vX.Y.Z`
