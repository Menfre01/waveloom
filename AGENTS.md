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

- 遵循 Go 社区惯例（Effective Go，标准库 project layout）
- 不要过度设计，保持简单，按需重构
- 命名清晰，避免缩写
- 错误统一处理，不外泄堆栈到客户端
- 查询 API 时优先使用 `go doc` 命令，避免凭记忆猜测接口签名

## Agent 操作规范

执行代码修改任务时，必须遵守以下操作顺序，减少试错。

### 修改前（MUST DO）

1. **定位**：用 `search_file` 定位目标文件，用 `grep` 定位具体符号
2. **确认**：用 `read_file` 阅读当前文件内容（即使刚读过，编辑前必须重新确认行号和内容，尤其是缩进）
3. **基线**：用 `lsp_diagnostic` 查看文件现有诊断，建立错误/警告基线

### 修改后（MUST DO）

1. **诊断**：用 `lsp_diagnostic` 检查是否引入新错误
2. **编译**：涉及 Go 项目时运行 `make build`
3. **测试**：涉及 `pkg/` 代码时运行 `make test`

### 重构前（MUST DO）

1. `lsp_references` 确认所有引用点
2. `lsp_hover` 确认类型签名
3. 评估影响范围后再动手

### 工具调用原则

- **并行优先**：多个独立只读操作（read_file、grep、search_file、lsp_*）应在同一轮并行发起，减少等待
- **串行必须**：写操作（write_file、edit_file、shell）必须串行，且不与写操作同轮混合
- **优先专用工具**：能用 read_file/grep/search_file/ls 的场景，禁止用 shell 替代
- **edit_file 优先**：对现有文件做局部修改用 edit_file，只有创建新文件或完全覆写时才用 write_file
- `no_match` 时不要盲目重试 — 先用 read_file 重新确认 old_string 的精确内容（含缩进和空白符）
- `security_violation` 是致命错误，停止当前路径，不要尝试绕过

## 开发流程

### 组件化 Wave 开发流程

- 任务拆分以**单个组件高内聚、组件之间低耦合**为基本原则。
- 每个任务独立作为一个 **Wave** 推进，按"组件开发 → 组件测试 → 组件验收 → 逐步组装"的顺序执行。
- 每个 Wave 开始前必须产出任务规格书，明确：
  - 新增 / 修改 / 删除哪些文件；
  - 组件边界、输入输出、依赖关系和不变量；
  - 与既有组件的集成点。
- 每个 Wave 的测试内容必须明确，并以**启动 cold agent 进行测试**为验收执行方式。
- 每个 Wave 的验收标准必须明确，并以**启动 cold agent 进行 review** 为最终确认方式。
- 只有当前 Wave 的组件测试和组件验收通过后，才进入下一个 Wave 或进行组件组装。

### TDD（测试驱动开发）

- Red → Green → Refactor 循环
- 先写测试，再写实现，保持测试通过
- 测试覆盖率目标 = 100%（OS/文件系统错误路径、impossible 分支等不可模拟路径除外，97%+ 视为达标）

## 文档规范

- 涉及架构、流程、数据模型的图表统一先使用 ascii 绘制再转换为 Mermaid 语法


## 构建与测试

- 编译、安装、测试统一使用 Makefile 标准操作，**禁止直接调用 `go build` / `go install` / `go test`**：

| 操作 | 命令 |
|------|------|
| 编译 | `make build` |
| 安装 | `make install` |
| 运行 | `make run` |
| 测试 | `make test` |
| 集成测试 | `make test-integration` |
| 清理 | `make clean` |

### 注意事项

- 同一 Wave 内的子任务不应修改同一文件，避免合并冲突
- 主 agent 负责协调 Wave 边界，等待关键依赖完成后才推进
- 子任务完成时须明确列出修改的文件路径，便于整合

## 提交策略

**禁止自动提交**。任何代码修改后，无论修改范围大小、是否通过测试，均不得自动执行 `git add`、`git commit`、`git push` 或任何其他会修改 Git 历史的操作。必须等待用户明确给出提交指令（如"提交"、"commit"）后方可执行。

此规则适用于所有场景，包括但不限于：
- 修复 bug 后
- 测试通过后
- 文档更新后
- 用户说"完成了"但未明确要求提交时

## Commit 规范

遵循 [Conventional Commits](https://www.conventionalcommits.org/zh-hans/v1.0.0/) v1.0.0。

### 格式

```
<type>(<scope>): <subject>
```

- `type`: `feat` / `fix` / `refactor` / `test` / `docs` / `chore`
- `scope`: 组件或包名（`llm` / `loop` / `tool` / `perm` / `tui` / `cli` / `context` / `compaction` / `lsp` / `memory`）；允许多 scope 用 `/` 分隔
- `subject`: 中文祈使句，≤72 字符，不以句号结尾

### 示例

```
feat(loop): Run() 增加 VerboseWriter 支持
feat(tui): eventCh 增加 listenEventChannel 链路
fix(cli): 修复 os.IsNotExist 跨包装错误检测失败
refactor(llm): 移除 NewClientFromEnv
chore: 新增 Makefile
```

## Release 规范

发布 Release 时应对 commit 分类汇总，按以下粒度输出 release notes：

- **新增功能** — 列出新增的特性、模块、命令
- **修复** — 列出修复的 bug
- **重构** — 列出重大重构的模块
- **性能优化** — 列出性能相关优化

以下类型不列入 release notes：

- `docs` — 文档修改
- `chore` — 工程杂务
- `test` — 纯测试补充

不逐条罗列 commit，以用户可感知的功能变化为描述单位。

### 发布流程

1. **扫描 commit** — 从上次 tag 到 HEAD，按上述分类汇总 changelog
2. **审查 README** — 检查中英双语 README 是否需要同步新功能、新命令、新 flag
3. **文档提交** — 如有 README 修改，先 commit
4. **打 tag** — `git tag vX.Y.Z`
5. **交叉编译** — `make release`
6. **推送** — `git push origin dev && git push origin vX.Y.Z`
7. **创建 Release** — `gh release create vX.Y.Z dist/* --title "vX.Y.Z" --notes "changelog"`
