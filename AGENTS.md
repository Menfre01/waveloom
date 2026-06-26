# Waveloom


## 编码规范

- 遵循 Go 社区惯例（Effective Go，标准库 project layout）
- 不要过度设计，保持简单，按需重构
- 命名清晰，避免缩写
- 错误统一处理，不外泄堆栈到客户端
- 查询 API 时优先使用 `go doc` 命令，避免凭记忆猜测接口签名

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
- **禁止自动提交**：任何代码修改后不得自动执行 `git add` / `git commit`，必须等待用户明确指示方可提交

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
