# Contributing to Waveloom

感谢你的贡献！

## 快速开始

```sh
git clone git@github.com:Menfre01/waveloom.git
cd waveloom
make build
make test
```

## 开发流程

### 组件化 Wave 开发

- 任务拆分以**单个组件高内聚、组件之间低耦合**为原则
- 每个任务独立作为一个 Wave 推进，按"组件开发 → 组件测试 → 组件验收 → 逐步组装"的顺序执行
- 每个 Wave 开始前阅读 `specs/` 中对应组件的规格书

### TDD（测试驱动开发）

- Red → Green → Refactor 循环
- 先写测试，再写实现
- 修改 `pkg/` 代码后运行 `make test` 确保所有测试通过

## 项目结构

```
waveloom/
├── cmd/waveloom/          # CLI 入口 + TUI
├── pkg/
│   ├── agentloop/         # Think-Act-Observe 循环
│   ├── compaction/        # 四级水位线上下文压缩
│   ├── context/           # 跨轮次消息历史
│   ├── environment/       # 编译/运行时工具链探测
│   ├── llm/               # LLM Client（DeepSeek + OpenAI）
│   ├── memory/            # AGENTS.md 层级加载
│   ├── permission/        # 权限守门人
│   ├── reference/         # @ 文件引用展开
│   └── tool/              # 内置工具系统
├── specs/                 # 各组件设计规格书（修改前先阅读）
├── docs/                  # 文档
└── Makefile
```

## 编码规范

- 遵循 [Effective Go](https://go.dev/doc/effective_go) 和 Go 社区惯例
- 命名清晰，避免缩写
- 错误统一处理，不外泄堆栈到客户端
- 修改前阅读 `AGENTS.md` 了解项目级约定

## 常用命令

| 操作 | 命令 |
|------|------|
| 编译 | `make build` |
| 安装 | `make install` |
| 运行 | `make run` |
| 测试 | `make test` |
| 集成测试 | `make test-integration` |
| 清理 | `make clean` |

## Commit 规范

遵循 [Conventional Commits](https://www.conventionalcommits.org/zh-hans/v1.0.0/) v1.0.0：

```
<type>(<scope>): <subject>
```

- `type`: `feat` / `fix` / `refactor` / `test` / `docs` / `chore`
- `scope`: 组件或包名
- `subject`: 中文祈使句，≤72 字符

## PR 审核

- 保持 PR 小而聚焦，一个 PR 解决一个问题
- 确保 CI 通过后再请求 review
- Review 者会检查代码风格、测试覆盖、文档同步

## 参考文档

- [`docs/system-prompt.md`](./docs/system-prompt.md) — System Prompt 完整内容及设计原则
- [`docs/tool-descriptions.md`](./docs/tool-descriptions.md) — 14 个内置工具的 Schema 定义
- [`specs/`](./specs/) — 各组件设计规格书（修改前先阅读）
