# Agent Loop 组件规格书

## 组件定位

Agent Loop 是 Waveloom Code Agent 的**心脏**，负责实现 Think-Act-Observe 循环。
它是连接 LLM Client 和 Tool System 的编排器，在每个 turn 中：
1. 组装上下文，调用 LLM（Think）— 优先流式调用，失败回退非流式
2. 解析响应，执行工具（Act）— 含权限检查，并发/串行分流
3. 收集结果，更新状态（Observe）
4. 执行压缩（Compaction）— 四级水位线，纯本地/LLM 增量摘要
5. 推送 TurnStats（含压缩结果）
6. 判断是否继续或终止

## 设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| 循环结构 | 单层 `for` 循环 | 最简，无嵌套 goroutine 开销，测试容易 |
| 事件推送 | `<-chan TurnEvent` | channel 解耦 Loop 和消费者（CLI / Server / 测试） |
| LLM 调用 | 流式优先，失败回退非流式 | 流式提供逐字 TUI 体验，非流式做可靠性兜底 |
| 工具并发 | 按 `ConcurrentSafe()` 分流 | 并发安全工具用 `sync.WaitGroup` 并行，非安全工具串行 |
| 错误处理 | Fatal 终止 / Recoverable 返回 LLM | LLM 根据错误反馈自行修正，无 loop 层面重试限制 |
| LLM 重试 | 指数退避 | 放在 Client 层，Loop 不关心重试细节 |
| 权限检查 | Guard 接口注入 | Loop 不关心权限策略，Guard 返回 allow/deny/ask 三种决策 |
| 用户交互 | UserResponder 接口注入 | ask 决策时由调用方提供交互实现 |
| TurnStats 时机 | 压缩完成后推送 | 合并 LLM token 用量 + CompactionInfo，每轮一次更新 |
| 余额查询 | Loop 外异步 goroutine | 不影响主流程，每次 loop 完成后查询 |

## 组件边界

### 输入
- `context.Context` — 取消/超时信号
- `Config` — 不可变配置
- `[]Message` — 初始消息历史

### 输出
- `<-chan TurnEvent` — 事件流，channel 在 loop 终止后关闭。

### 依赖（接口，非具体实现）
- `llm.Client` — LLM 调用
- `tool.Registry` — 工具查找和执行
- `compaction.Compactor` — 上下文压缩（nil → 跳过）
- `permission.Guard` — 权限检查（nil → 跳过）
- `permission.UserResponder` — ask 决策时的用户交互（nil → ask 降级 deny）

---

## 事件类型（TurnEvent）

### StreamDelta — 流式文本增量

```go
type StreamDelta struct {
    Turn           int    // 当前 turn 序号（1-based）
    ContentDelta   string // 增量回复文本
    ReasoningDelta string // 增量思考链
}
```

### ToolCallStart — 工具调用开始

```go
type ToolCallStart struct {
    Turn         int
    ToolCallID   string
    ToolCallName string
    Arguments    string
}
```

### ToolCallResult — 工具执行结果

```go
type ToolCallResult struct {
    Turn         int
    ToolCallID   string
    ToolCallName string
    Result       string
    Error        string
    ErrorKind    string
    DurationMs   int64
    Denied       bool
    DiffHunks    []tool.DiffHunk
}
```

### TurnStats — Token 统计 + 压缩结果（合并推送）

```go
type TurnStats struct {
    Turn             int
    Model            string
    PromptTokens     int
    CompletionTokens int
    CacheHitTokens   int
    CacheMissTokens  int
    ReasoningTokens  int
    MessageCount     int

    Compaction CompactionInfo  // 压缩结果（每轮必定携带，无压缩时为零值）
}

type CompactionInfo struct {
    TokensSaved              int
    Tier                     int     // 0/1/2/3
    SummaryDone              bool
    HardLimitReached         bool
    HardLimitReason          string
    UsageRatio               float64
    Tier3ConsecutiveFailures int
}

func (c CompactionInfo) HasCompaction() bool {
    return c.Tier > 0 && c.TokensSaved > 0
}
```

### BalanceUpdate — 余额更新

```go
type BalanceUpdate struct {
    Balance *llm.BalanceInfo
}
```

每次 loop 完成后由 TUI 异步查询并通过 `program.Send()` 推送。

### LoopDone — 循环终止

```go
type LoopDone struct {
    Turn     int
    Reason   TerminalReason
    Err      error
    Messages []llm.Message
}
```

---

## 核心类型

### Config

```go
type Config struct {
    MaxTurns      int                  // 最大 turn 数，0 表示无限制
    SystemPrompt  string               // 系统提示词
    Guard         permission.Guard     // 权限守门人，nil → 跳过
    UserResponder permission.UserResponder // ask 决策交互，nil → ask 降级 deny
    VerboseWriter io.Writer            // 非 nil 时输出调试明细
    Compactor     compaction.Compactor // 上下文压缩，nil → 跳过
}
```

### LoopState

```go
type LoopState struct {
    Messages          []llm.Message
    TurnCount         int
    ConsecutiveEmpty  int  // 连续空响应计数，>3 时 abort
}
```

### TerminalReason

```go
const (
    ReasonCompleted   TerminalReason = "completed"     // LLM 给出最终答案
    ReasonMaxTurns    TerminalReason = "max_turns"     // 达到 MaxTurns
    ReasonAborted     TerminalReason = "aborted"       // ctx 被取消
    ReasonModelError  TerminalReason = "model_error"   // LLM 调用失败 / 压缩硬限 / 连续空响应
    ReasonToolFatal   TerminalReason = "tool_fatal"    // 工具返回致命错误
)
```

### Compactor 接口

```go
type Compactor interface {
    Compact(ctx context.Context, messages *[]llm.Message, contextTokens int) Tick
    AdvanceTurn() int
}
```

`totalTurns` 由 ContextManager 通过 `AdvanceTurn()` 推进，Compactor 内部读取。

---

## 核心算法

### Run() 每轮流程

```
1. Context 取消检查
2. THINK: 流式调用 LLM → 暂存 usage（不推送 TurnStats）
   - 流中非 cancel 错误 → 回退非流式 SendMessage
   - 流中 ctx.Canceled / DeadlineExceeded → ReasonAborted
3. 过滤无效 tool_calls（空 ID/Name，未知工具）
4. 防御空响应（注入占位 "(empty response)"，ConsecutiveEmpty++；不含 reasoning_content）
5. 追加 assistant 消息（reasoning_content 仅 tool_calls 场景保留），TurnCount++
6. 无 tool calls → 有内容 → ReasonCompleted / 空 → 继续或 abort(>3次)
7. ACT + OBSERVE: 执行工具（并发/串行），追加 tool 消息
8. 压缩 + 推送 TurnStats（合并 CompactionInfo）
   - HardLimitReached → LoopDone(ReasonModelError)
9. 回到步骤 1
```

### TurnStats 推送时机

```
LLM 返回 → 暂存 Usage ──→ 工具执行 ──→ 压缩 ──→ 推送 TurnStats
                                                    ↑
                                        一次性携带 token + CompactionInfo
```

TUI 通过 `Compaction.HasCompaction()` 判断 ctx bar 使用 API 真实值还是估算值。

---

## 工具执行并发

LLM 返回多个 tool call 时，先按并发安全分类，再分批执行：

```
并发组（ConcurrentSafe=true）:
  每工具独立权限检查 → 放行者收集
  sync.WaitGroup 并行执行
  推送 ToolCallStart / ToolCallResult

串行组（ConcurrentSafe=false）:
  逐工具 权限检查 → 执行 → 推送
```

消息追加顺序按原始 ToolCall 顺序，不按执行结束顺序。

---

## 错误处理

| 错误分类 | 行为 |
|---------|------|
| `ErrorClassRecoverable` | 错误消息返回给 LLM 修正，无重试限制 |
| `ErrorClassFatal` | 直接终止 → `ReasonToolFatal` |
| Tool 返回 `(nil, nil)` | 并发/串行路径均生成 Fatal → `ReasonToolFatal` |
| LLM 连续空响应 >3 次 | `ReasonModelError` |
| LLM 调用失败（重试耗尽） | `ReasonModelError`（或 `ReasonAborted`，若 ctx 过期） |
| 流式错误 → 回退非流式成功 | 继续正常流程 |
| 流式错误 → 回退非流式也失败 | `ReasonModelError` |
| 权限 deny | 构造拒绝消息返回给 LLM，loop 继续 |
| ask 无 UserResponder | 自动降级 deny |
| 压缩 HardLimitReached | `ReasonModelError` |

---

## 不变量

1. **消息顺序**：System → User → Assistant → Tool → ... 严格遵守
2. **Turn 计数**：每次调用 LLM 后 +1
3. **终止互斥**：每个 Run 有且仅有一个 `LoopDone`
4. **错误不丢上下文**：即使因错误终止，`LoopDone.Messages` 仍包含已执行的操作历史
5. **Context 优先**：每次迭代开始先检查 `ctx.Err()`
6. **并发安全**：ConcurrentSafe 工具并行，非安全工具串行
7. **权限不可跳过**：Guard 非 nil 时，每个工具调用前必须 `Check()`
8. **TurnStats 唯一**：每轮 LLM 后最多推送一次 TurnStats（压缩完成后）
9. **tool 消息配对**：即使执行出错，每个 tool call 都有对应的 tool 消息

---

## 实现文件

| 文件 | 说明 |
|------|------|
| `pkg/agentloop/types.go` | TurnEvent 接口 + 全部事件类型 + CompactionInfo + LoopDone |
| `pkg/agentloop/loop.go` | Loop 实现（核心循环 + 压缩集成 + 工具执行 + 权限检查） |
| `pkg/agentloop/loop_test.go` | 51 个单元/集成测试（mock 注入，覆盖全部退出路径） |

## 测试覆盖

| 退出路径 | 测试 |
|----------|------|
| `ReasonCompleted` | 8+ tests |
| `ReasonMaxTurns` | 2 tests |
| `ReasonAborted` | context cancel, deadline exceeded |
| `ReasonModelError` | LLM 错误, 空响应 >3, 硬限, 流错误回退失败 |
| `ReasonToolFatal` | 致命工具错误, nil 结果, 并发执行错误 |

压缩集成：`HasCompaction` / `NoCompaction` / `HardLimitReached` / `Tier3SummaryDone`
