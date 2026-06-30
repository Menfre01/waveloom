# Waveloom

终端编码代理（Go 实现），帮助用户编写、重构、调试和探索代码。

## 项目概要

- **语言**：Go 1.25+
- **LLM**：DeepSeek V4（默认）/ OpenAI，通过 `llm.Client` 接口适配
- **TUI**：Bubble Tea v2 + Glamour Markdown 渲染 + Lipgloss 样式
- **LSP**：gopls（Go 语言服务器），支持 diagnostic / definition / references / hover
- **构建**：`make build` / `make test` / `make run`

```
cmd/waveloom/    CLI 入口（main, config, runner, tui）
pkg/
  agentloop/     Think-Act-Observe 循环（Run → <-chan TurnEvent）
  llm/           LLM Client（DeepSeek + OpenAI 适配、流式、重试）
  tool/          工具系统（12 个内置工具，TypedTool[P] 泛型接口）
  permission/    权限守门人（规则引擎、路径/命令安全）
  context/       跨轮次消息历史（PrepareRun / CompleteRun）
  compaction/    四级水位线上下文压缩（Snip/Prune/Summarize）
  lsp/           LSP Client
  environment/   工具链探测
  memory/        AGENTS.md 层级加载
  reference/     @ 文件引用展开
specs/           各组件规格书（修改前先阅读）
```

## 编码规范

- 遵循 Go 社区惯例（Effective Go，标准库 project layout），不过度设计，命名清晰避免缩写
- 错误统一处理，不外泄堆栈到客户端；查询 API 优先用 `go doc`

## Agent 操作规范

### 修改前后检查清单

| 阶段 | 操作 |
|------|------|
| 修改前 | `search_file` / `grep` 定位 → `read_file` 确认行号和内容 → `lsp_diagnostic` 建基线 |
| 修改后 | `lsp_diagnostic` 查新错误 → `make build` 编译 → `make test`（涉及 `pkg/` 时） |
| 重构前 | `lsp_references` + `lsp_hover` → 评估影响范围 |

### 工具调用原则

- **独立只读操作并行**（read_file、grep、search_file、lsp_*），写操作串行
- **优先专用工具**：能用 read_file/grep/search_file/ls 的场景，禁止用 shell 替代
- **局部修改用 edit_file**，新建或完全覆写才用 write_file
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

Release notes 以用户可感知的功能变化为描述单位，分类汇总：

- **新增功能** — 新特性、模块、命令
- **修复** — Bug 修复
- **重构** — 重大模块重构
- **性能优化** — 性能相关

`docs` / `chore` / `test` 类型不列入。

发布由 GitHub Actions 自动完成（tag push `v*` → `.github/workflows/release.yml`）。

手动步骤（release workflow 之前完成）：

1. **汇总 changelog** — 从上次 tag 到 HEAD 扫描 commit，按分类汇总，更新 `CHANGELOG.md` 和 `CHANGELOG.en.md`
2. **审查 README** — 检查 `README.md` 和 `docs/README.en.md` 是否需要同步新功能
3. **审查双语文档** — 检查 `CONTRIBUTING` / `SECURITY` / `docs/` 下中英双语是否同步
4. **文档提交** — 如有文档修改，先 commit（类型 `docs`）
5. **打 tag 并推送** — `git tag vX.Y.Z && git push origin dev && git push origin vX.Y.Z`
