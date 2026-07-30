# Edit 工具评估体系

三层递进评估 Waveloom edit 工具的引擎正确性和 LLM 可用性。

## 快速开始

```sh
# Layer 1: 离线确定性 eval（CI 回归防线）
go test ./pkg/hashline/eval/ -run TestEvalCases

# Layer 2: 基准测试 CLI（批量跑数据集）
go run ./cmd/editbench/
go run ./cmd/editbench/ -filter "atomic"    # 只看某一类
go run ./cmd/editbench/ -json               # JSON 输出

# Layer 3: LLM-in-the-loop 可用性评估（真实 LLM 驱动）
LLM_API_KEY=sk-xxx go run ./cmd/llmedit/ -provider deepseek -model deepseek-chat
```

## 三层架构

```
┌──────────────────────────────────────────────┐
│ Layer 3: LLM-in-the-loop eval                 │
│ cmd/llmedit — 真实 LLM → agent loop → edit    │
│ 14 个指标 → 判分 → 报告                        │
├──────────────────────────────────────────────┤
│ Layer 2: 基准测试 CLI                          │
│ cmd/editbench — 批量跑数据集,统计通过率        │
├──────────────────────────────────────────────┤
│ Layer 1: 离线确定性 eval                       │
│ pkg/hashline/eval — JSONL 数据集 + 内存执行    │
└──────────────────────────────────────────────┘
```

## Layer 1: 离线确定性 eval

**用途**：ParsePatch → ApplyPatch 引擎正确性回归。加 case = 追加一行 JSONL。零磁盘 IO，CI 中可跑。

**数据集**：`pkg/hashline/eval/testdata/evals/*.jsonl`（32 个 case, 9 类场景）

| 数据集 | 场景 |
|---|---|
| `progressive.jsonl` | rstrip / trim / NFC 归一化 / 负向不匹配 |
| `crlf.jsonl` | Windows CRLF → LF 归一化 |
| `read_sentinel.jsonl` | 读前哨兵：未 read / 正常 / 快照过期 |
| `body_escape.jsonl` | legacy body `+` 前缀剥离 + `\+` 转义 |
| `atomicity.jsonl` | 同文件多 section 原子合并 / 回滚 / 缺 %OLD 拒绝 |
| `recovery.jsonl` | TAG 过期 → LCS 重映射成功 / 冲突失败 |
| `dirty_patch.jsonl` | 大小写 / 行尾注释 / 空格变体 / legacy 语法 |
| `multi_file.jsonl` | 跨文件成功 / 部分成功 |
| `edge_cases.jsonl` | INS.TAIL / DEL 全文件 / SWAP ALL / 空文件 / 超范围 |

**新增 case**：在对应 jsonl 追加一行 JSON：

```json
{
  "name": "my-case",
  "files": {"main.go": "package main\nfunc main() {}\n"},
  "snapshot_files": {},
  "patch": "*** Begin Patch\n[main.go#TAG]\nSWAP 1.=1\n%OLD\npackage main\n%NEW\npackage app\n*** End Patch",
  "expect_files": {"main.go": "package app\nfunc main() {}\n"},
  "expect_error_kind": "invalid_args",
  "expect_warning_contains": "子串"
}
```

## Layer 2: 基准测试 CLI

```sh
go run ./cmd/editbench/                       # 跑全量
go run ./cmd/editbench/ -filter "atomic"      # 只看原子性
go run ./cmd/editbench/ -filter "recovery"    # 只看恢复
go run ./cmd/editbench/ -json | jq '.pass_rate'
```

## Layer 3: LLM-in-the-loop 可用性评估

### 前置条件

- API Key（DeepSeek / Kimi / OpenAI）
- 5 个试点任务已就绪（`eval/llmedit/testdata/tasks/`）

### 运行

```sh
# DeepSeek
LLM_API_KEY=sk-xxx go run ./cmd/llmedit/ -provider deepseek -model deepseek-chat

# Kimi
LLM_API_KEY=sk-xxx go run ./cmd/llmedit/ -provider kimi -model kimi-latest

# OpenAI
LLM_API_KEY=sk-xxx go run ./cmd/llmedit/ -provider openai -model gpt-4o

# 指定任务目录
go run ./cmd/llmedit/ -dir /path/to/tasks
```

### 输出示例

```
[1/5] rename-function — pass=true parse=100% 1stPass=100% blindEdits=0 tagMis=0 warn=0 turns=1 dist=0
[2/5] add-parameter — pass=true parse=100% 1stPass=100% blindEdits=0 tagMis=0 warn=0 turns=2 dist=0
...

{
  "total": 5,
  "passed": 4,
  "failed": 1,
  "errors": 0,
  "pass_rate": 80.0,
  "elapsed_ms": 45230.5
}
```

### 14 个评估指标

**L1 — patch 生成质量**（评估 prompt + 模型）

| 指标 | 含义 |
|---|---|
| Parse 成功率 | LLM 生成的 patch 被正确解析的比例 |
| %OLD 覆盖率 | SWAP 操作提供 %OLD sentinel 的比例 |
| 语法选择分布 | sentinel vs legacy 的使用偏好 |

**L2 — 执行正确性**（评估 LLM+引擎协作）

| 指标 | 含义 |
|---|---|
| 首次通过率 | 第一次 edit 后文件 byte-level 完全正确 |
| 首次通过归因 | 2×2 混淆矩阵：TAG 问题 vs patch 问题 |
| 编辑副作用率 | patch 意外改了目标范围之外的行 |
| TAG mismatch 率 | edit 返回 tag_mismatch 的频率 |
| Warning 响应率 | 看到 ⚠️ 后下一 turn 是否 re-read |
| 盲编率 | edit 前未 read 文件的比例 |
| 放弃率 | LLM 中途停止/报错退出的比例 |

**L3 — 任务最终结果**

| 指标 | 含义 |
|---|---|
| 任务成功率 | 编译通过 + byte-level 文件一致性 |
| 编辑距离 | 最终文件与 gold 的 diff edit distance |
| 收敛性 | 编辑距离随 turn 数的变化趋势 |
| 平均 turn 数 | 完成任务所需的 read→edit 循环次数 |

### 评估环境保证

- **双盲**：LLM 看到的只是自然语言指令,与真实用户请求完全一致
- **隔离**：工作目录为 OS 临时目录,与项目源码零交集
- **一致**：system prompt 与生产环境同源（`pkg/prompt/default.txt`），唯一差异是无 AGENTS.md
- **受限**：只注册 read/edit/write 工具,无法执行 shell 命令

### 新增评估任务

在 `eval/llmedit/testdata/tasks/` 下创建 JSON 文件：

```json
{
  "name": "extract-function",
  "instruction": "把 main 函数中的循环体提取为独立的 process 函数",
  "type": "multi-line",
  "files": {
    "main.go": "package main\n\nfunc main() {\n\tfor i := 0; i < 10; i++ {\n\t\tprintln(i)\n\t}\n}\n"
  },
  "gold": {
    "main.go": "package main\n\nfunc process() {\n\tfor i := 0; i < 10; i++ {\n\t\tprintln(i)\n\t}\n}\n\nfunc main() {\n\tprocess()\n}\n"
  }
}
```

`instruction` 是 LLM 看到的唯一用户消息（双盲），`gold` 是期望的最终文件内容。
