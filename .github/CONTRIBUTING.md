# 贡献指南

感谢你考虑为 Waveloom 贡献代码！在开始之前，请先阅读以下内容。

## 行为准则

- 保持友善和专业
- 尊重不同观点和经历
- 建设性地给出和接受反馈

## 如何贡献

### 报告 Bug

1. 在 [Issues](https://github.com/Menfre01/waveloom/issues) 中搜索，确认未被报告过
2. 使用 **Bug Report** 模板提交
3. 提供复现步骤、期望行为、实际行为、环境信息（OS、终端、wvl 版本）

### 请求新功能

1. 在 Issues 中搜索类似请求
2. 使用 **Feature Request** 模板提交
3. 描述使用场景和期望的解决方案

### 提交代码

1. **Fork** 本仓库
2. **创建分支**：以 `feat/`、`fix/`、`refactor/` 为前缀
3. **编写代码**：
   - 遵循 Go 社区惯例（Effective Go）
   - 先写测试，再写实现（TDD 循环）
   - 测试覆盖率目标 97%+
4. **编译测试**：`make build && make test`
5. **Commit**：遵循 [Conventional Commits](https://www.conventionalcommits.org/zh-hans/v1.0.0/)
   ```
   <type>(<scope>): <subject>
   ```
   示例：`feat(tool): 新增 web_fetch 工具` / `fix(context): 修复 60% 水位线触发过早`
6. **发起 Pull Request**：
   - 描述修改内容和原因
   - 关联相关 Issue（如 `Closes #42`）
   - 确保 CI 通过

## 开发流程

Waveloom 采用**组件化 Wave 开发流程**，详见 [AGENTS.md](../AGENTS.md)：

- 每个任务以单个组件为单位，高内聚、低耦合
- 每个 Wave 产出任务规格书 → 测试 → 验收
- 同一 Wave 内避免多人修改同一文件

## 项目结构

```
waveloom/
├── cmd/waveloom/          # 入口 + TUI
├── pkg/
│   ├── agentloop/         # Think-Act-Observe 循环
│   ├── context/           # 上下文累积 + 四级水位线压缩
│   ├── llm/               # LLM API 封装
│   ├── memory/            # AGENTS.md 层级加载
│   ├── permission/        # 权限守门人
│   ├── reference/         # @ 文件引用展开
│   └── tool/              # 内置工具
├── specs/                 # 各组件设计规格书
├── docs/                  # 文档
└── Makefile
```

## 联系方式

- [GitHub Issues](https://github.com/Menfre01/waveloom/issues) — Bug 和功能请求
- [GitHub Discussions](https://github.com/Menfre01/waveloom/discussions) — 一般讨论和 Q&A
