# 四级水位线上下文压缩系统

## 1. 定位

Waveloom Code Agent 在 DeepSeek API 下的完整上下文压缩策略。核心目标：在几十甚至上百轮的持续执行中，**降低信噪比、防止上下文溢出**，同时最大化 DeepSeek 前缀缓存命中率。

省钱是顺带的结果。真正的目标是防止 Context Rot——当上下文塞到 70% 以上，模型的中段失忆和指令漂移开始恶化。

## 2. 架构

```
┌─ ContextManager ──────────────────────────────────────────┐
│  PrepareRun() → compactor.AdvanceTurn()                    │
│  CompleteRun() → stats + 落盘                              │
│  持有 *TieredCompactor                                     │
└───────────────────────────────────────────────────────────┘
                    │
┌─ agentloop ───────────────────────────────────────────────┐
│  Config.Compactor: Compactor (接口)                        │
│  Run() 每轮 LLM + tool 执行后调用 Compact()                │
└───────────────────────────────────────────────────────────┘
                    │
┌─ compaction ──────────────────────────────────────────────┐
│  Compactor (接口)                                          │
│    Compact(ctx, messages, contextTokens) Tick              │
│    AdvanceTurn() int                                       │
│                                                            │
│  TieredCompactor (实现)                                    │
│    状态: watermark, decisions, existingSummaries,          │
│          config, summarizer, lastResult, totalTurns        │
│    算法: applyTier1/2/3, checkHardLimit                    │
│    持久化: Snapshot() / Restore()                          │
└───────────────────────────────────────────────────────────┘
```

### 组件位置

| 组件 | 包 | 文件 |
|------|-----|------|
| `Compactor` 接口 | `pkg/compaction` | `types.go` |
| `TieredCompactor` | `pkg/compaction` | `compactor.go` |
| 四级水位线算法 | `pkg/compaction` | `compaction.go` |
| `CompactionSummarizer` | `pkg/compaction` | `summarizer.go` |
| `CompactionSettings` | `pkg/compaction` | `settings.go` |
| ContextManager 持有 | `pkg/context` | `context.go` |
| agentloop 注入 | `cmd/waveloom` | `tui.go` / `runner.go` |

## 3. 核心概念

### 四级水位线

```
                         context window (1M)
  ┌──────────────────────────────────────────────────────────────────┐
  │  ████████████████████████████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░ │
  │  ↑ 已用                       ↑ 60%   ↑ 80%   ↑ 95%            │
  │                                Tier 1  Tier 2  Tier 3            │
  │  Tier 0: < 60%  — 什么都不做                                     │
  │  Tier 1: 60-80% — Snip：工具结果差分截断（纯本地，零 API 调用）    │
  │  Tier 2: 80-95% — Prune：reasoning 清除 + 占位符替换 + 用户代码块 │
  │  Tier 3: ≥ 95%  — Summarize：LLM 增量摘要（需 API 调用）         │
  │  硬临界: ≥ 98%  — 阻止后续 LLM 调用                               │
  └──────────────────────────────────────────────────────────────────┘
```

Tier 是累积的——Tier 3 触发时会先执行 Tier 1 和 Tier 2，再做摘要。

### 单调边界

一旦对某条消息做出压缩决策，该决策在本次 session 的所有后续轮次中永远不变。实现方式：**决策表（`compactionDecisionSet`）+ 双 cursor（`Tier1Cursor` / `Tier2Cursor`）**。

```
messages:  [0] [1] [2] ... [tier2Cursor] ... [tier1Cursor] ... [protectionStartIdx] ... [N]
           ↑               ↑                   ↑                   ↑                      ↑
           system prompt    Tier 2 已处理       Tier 1 已处理        保护区起点             末尾
```

- `Tier1Cursor` / `Tier2Cursor` 记录已评估的消息索引前沿，只向前移动
- `compactionDecisionSet` 记录每条被压缩消息的决策（`snip` / `prune`），已有决策不会被覆盖为更轻的压缩
- 已越过 cursor 的消息永不重新评估

### 保护区

最近 8000 token 内的所有消息，任何 Tier 都不参与压缩。保护区起始索引 `protectionStartIdx` 每次从 messages 末尾向前累加 token 计算。

### Turn 计数

`totalTurns` 由 `TieredCompactor` 内部维护。`ContextManager.PrepareRun()` 调用 `AdvanceTurn()` 推进，与 TUI HUD 的 Loop 计数同步。写入 `CompactionDecision.AppliedAt` 用于审计。

## 4. 数据类型

### Compactor 接口

```go
type Compactor interface {
    Compact(ctx context.Context, messages *[]llm.Message, contextTokens int) Tick
    AdvanceTurn() int
}
```

### Tick

```go
type Tick struct {
    Tier                     int
    HardLimitReached         bool
    HardLimitReason          string
    MessagesPruned           int
    MessagesSnipped          int
    TokensSaved              int
    Tier3SummaryDone         bool
    UsageRatio               float64
    ContextTokens            int
    ContextLimit             int
    MessageCount             int
    Tier3ConsecutiveFailures int
}
```

### CompactionDecision（有序集合，按 MsgIndex 升序）

```go
type CompactionDecision struct {
    MsgIndex      int    `json:"msg_index"`
    DecisionTier  int    `json:"decision_tier"`
    Action        string `json:"action"` // "snip" | "prune"
    TokensSaved   int    `json:"tokens_saved"`
    AppliedAt     int    `json:"applied_at"`
}

type compactionDecisionSet []CompactionDecision  // 按 MsgIndex 升序
```

操作：
- `canApply(msgIndex, action) bool` — O(log N) 二分查找，snip 可升级为 prune，不可降级
- `upsert(d)` — 有序插入或替换

### WatermarkState

```go
type WatermarkState struct {
    CurrentTier     int
    UsageRatio      float64
    LastUsageTokens int
    ContextLimit    int
    Tier1Cursor     int
    Tier2Cursor     int
    Tier3Cursor     int
    Tier3ConsecutiveFailures int
}
```

### Summarizer 接口

```go
type Summarizer interface {
    Summarize(ctx context.Context, existingSummaries []string, deltaMessages []llm.Message) (string, error)
}
```

## 5. 四级水位线算法

### Compact() 内部流程

```
1. 计算 usageRatio = contextTokens / ContextLimit（使用 API 真实 prompt_tokens）
2. 确定 tier（<60% → 0, <80% → 1, <95% → 2, ≥95% → 3）
3. checkHardLimit（≥98% 或 Tier3 连续失败 ≥2）
4. 计算 protectionStartIdx
5. Tier 1 snip（如 tier ≥ 1）— 扫描 [tier1Cursor, protectionStartIdx)
6. Tier 2 prune（如 tier ≥ 2）— 扫描 [tier2Cursor, protectionStartIdx)
7. Tier 3 summarize（如 tier ≥ 3）— 扫描 [tier3Cursor, protectionStartIdx)
```

### Tier 1: Snip（60-80%）

纯本地，零 API 调用。

| 工具 | 最大行数 | head | tail |
|------|---------|------|------|
| `read_file` | 200 | 150 | 10 |
| `shell` | 60 | 20 | 30 |
| `grep` | 60 | 50 | 0 |
| `web_fetch` | 200 | 150 | 10 |

`ls` / `search_file` / `edit_file` / `write_file` 不截断。未知工具不截断。

### Tier 2: Prune（80-95%）

纯本地，零 API 调用。处理三类消息：

| 角色 | 操作 |
|------|------|
| `assistant` | `reasoning_content` → 置空 |
| `tool` (read_file/shell/grep/web_fetch) | `content` → 占位符 `[tool call 输出已被压缩]` |
| `user` | code fence 内 >50 行的块 → 占位符 `[粘贴的内容已被压缩（原始: >50 行）]` |

`ls` / `search_file` / `edit_file` / `write_file` 不压缩。

用户消息中 fence 外的自然语言指令原样保留。code fence 检测通过反引号计数匹配开关（3 个进入，≥3 个退出，关 ≥ 开）。

### Tier 3: Summarize（≥95%）

需 LLM 调用。

1. 收集 `[tier3Cursor, protectionStartIdx)` 内的消息作为 delta
2. 构造 LLM 请求：system = `FormatSummaryPrompt()`，user = `FormatSummaryUserMessage(existingSummaries, delta)`
3. LLM 产出结构化 JSON 摘要（progress/pending/pitfalls/constraints）
4. 删除 delta 消息，将摘要作为 user 消息追加
5. 清空 decisions（旧索引已失效）
6. 重置三个 cursor 到摘要消息之后

**摘要 JSON 格式**：

```json
{
  "progress": {
    "summary": "<200字中文进展概述>",
    "files": [{"path": "...", "action": "created|modified|deleted|read", "why": "变更意图"}]
  },
  "pending": ["未完成任务"],
  "pitfalls": [{"problem": "遇到的问题", "solution": "解决方案"}],
  "constraints": "必须遵守的约束"
}
```

**摘要链**：每次 Tier 3 产出独立摘要追加到链末尾，不重写历史摘要。LLM 收到全部已有摘要作为上下文参考，但只产出本阶段增量。

**JSON 模式**：摘要 Client 独立创建，设置 `ClientConfig.ResponseFormat = "json_object"`，API 层保证合法 JSON 输出。

### 硬临界值

| 触发条件 | 原因 |
|----------|------|
| `usageRatio ≥ 0.98` | 上下文窗口即将溢出 |
| `Tier3ConsecutiveFailures ≥ 2` | LLM 摘要连续失败 |

触发后 Tier 1/2 继续执行，但 Tier 3 被跳过，Loop 收到 `HardLimitReached=true` 终止。

### 截断/占位符示例

Tier 1（read_file，500 行）：
```
package main                                    ← head[0]
import ( "fmt" ...)                             ← head[1:5]

[... 省略 340 行 — 完整结果已由 Agent 处理]

func (m *model) renderFooter() string {         ← tail[0]
    return m.styles.footer.Render("...")        ← tail[1:9]
}
```

Tier 2（tool read_file）：
```
[tool call 输出已被压缩] 工具 read_file 的输出已被压缩（原始: 500 行, ~3500 tokens）
```

Tier 2（user 代码块）：
```
帮我修复这个 bug，日志如下：

[粘贴的内容已被压缩（原始: >50 行）]

错误码是 ENOENT。
```

## 6. 调用时序

```
PrepareRun() → AdvanceTurn()
    │
agentloop.Run():
    │── LLM call → TurnStats
    │── tool 执行 → 追加 tool 消息
    │── Compact(contextTokens=50000) → Tier 0
    │── LLM call → TurnStats
    │── tool 执行
    │── Compact(contextTokens=65000) → Tier 1 snip
    │── ...
    │── Compact(contextTokens=960000) → Tier 3 summarize
    │── Compact(HardLimitReached=true) → LoopDone
    │
CompleteRun() → stats 累加 + 落盘
```

## 7. 持久化

```go
type CompactionData struct {
    Decisions  compactionDecisionSet
    Watermark  WatermarkState
    Summaries  []string
    TotalTurns int
}
```

- `Snapshot()` → `CompactionData`，随 session JSON 落盘
- `Restore(data)` → 恢复压缩状态、cursor、决策表

## 8. 配置

```json
{
  "compaction": {
    "tier1_threshold": 0.60,
    "tier2_threshold": 0.80,
    "tier3_threshold": 0.95,
    "protection_zone_tokens": "8K",
    "context_limit_tokens": "1M"
  }
}
```

合并顺序：`DefaultCompactionConfig() → global settings → project settings`（项目优先）。

Token 计数字段支持 `8000`（裸数字）或 `"8K"` / `"1.5M"`（字符串）。

## 9. 不变量

| 不变量 | 保证机制 |
|--------|----------|
| 决策单调性 | `canApply()` — snip → prune 可升级，不可降级 |
| cursor 单调推进 | `Tier1Cursor`/`Tier2Cursor` 只增不减 |
| 保护区不可侵犯 | `findProtectionStartIdx` 每次基于最新 messages 计算 |
| Tier 累积执行 | Tier N 触发时 Tier 1..N-1 先执行完毕 |
| decisions 清空 | Tier 3 后 cursor 全部重置，旧决策无消费者，直接清空 |
| 摘要链不可变 | 每次 Tier 3 追加独立摘要，不重写历史 |
| Token 计量精确 | 触发判断用 API 真实 `prompt_tokens`，估算仅用于内部排序 |
| System prompt 永不变 | `messages[0]` 任何 tier 不修改 |
| AGENTS.md 永不变 | `messages[1]` 任何 tier 不修改 |
| 空 reasoning 安全 | 置空 `""` 而非删除字段 |
| 硬临界阻断 | ≥98% 或连续 2 次 Tier 3 失败后阻止后续 LLM 调用 |

## 10. 测试覆盖

| 测试文件 | 覆盖内容 |
|----------|---------|
| `compactor_test.go` | Tier 0/1/2/3、硬限、单调性、保护区、Snapshot/Restore、Reset |
| `compaction_test.go` | `canApply`/`upsert`、`truncateByStrategy`、`checkHardLimit`、`findProtectionStartIdx`、`estimatedTokensFromContent`、`FormatSummaryPrompt`/`FormatSummaryUserMessage` |
| `settings_test.go` | `tokenSetting`、`ApplyToConfig`、`LoadCompactionSettings`、JSON 往返、Merge 顺序 |
| `summarizer_test.go` | `CompactionSummarizer` mock 测试（成功/空响应/非法 JSON/markdown 包裹/网络错误） |
| `summarizer_integration_test.go` | 真实 DeepSeek API 端到端（基本摘要/摘要链继承/空 delta/JSON 模式/超长输入） |
| `compressUserCodeBlocks` | 8 个用例覆盖无 fence、小 fence、大 fence、嵌套反引号、未闭合、多 fence 等 |
