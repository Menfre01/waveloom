# HUD 与 ContextManager 解耦 规格书

> **状态：✅ 已实现** — `CompleteResult` 已返回，`EstimateCurrentTokens` / `CalibrateEstimatedTokens` / `MessageCount()` 已删除，TUI 通过 `lastTurnPrompt` + `lastTurnCompl` 自行计算。

## 定位

TUI 底部 HUD（Footer）展示：模型名、上下文窗口使用量、缓存命中率、Loop 数、消息数、延迟、余额。
当前部分数据通过 TUI 直接调用 `cm.EstimateCurrentTokens()`、`cm.CalibrateEstimatedTokens()`、
`cm.Stats().TotalTurns`、`cm.MessageCount()` 获取，形成 TUI → cm 的读取耦合。

本规格通过 **CompleteRun 返回值 + 删除公开读取方法** 消除耦合。

---

## ctx bar 数据流

```
TurnStats (每轮) → 只记录最新 Prompt/Completion 到 lastTurnPrompt/lastTurnCompl
                   ctx bar 不变（loop 中不跳动）
                            │
   LoopDone ─────────────────┤
                            │
          CompleteRun 压缩 → 返回 CompleteResult {MessageCount, TokensCompact}
                            │
          TUI: lastPromptTokens = lastTurnPrompt + lastTurnCompl - TokensCompact
                ctx bar 一次性更新为压缩后值
```

---

## 变更：ContextManager

### 新增类型

```go
type CompleteResult struct {
    MessageCount  int // 当前消息总数
    TokensCompact int // 本轮压缩/截断节省的 token 估算值
}
```

### 签名变更

```
CompleteRun(...) 返回值 int → CompleteResult
```

### 删除

| 方法 | 原因 |
|------|------|
| `EstimateCurrentTokens()` | 不再暴露 |
| `CalibrateEstimatedTokens()` | 不再校准 |
| `MessageCount()` | 由 `Stats().MessageCount` 替代 |

---

## 变更：TUI

### 新增字段

```go
lastTurnPrompt int // 本 loop 最后一个 TurnStats 的 PromptTokens
lastTurnCompl  int // 本 loop 最后一个 TurnStats 的 CompletionTokens
```

### 删除字段

`ctxCalibrated bool`

### TurnStats handler

```go
case agentloop.TurnStats:
    // 记录最新一轮的 API 精确值，不更新 ctx bar
    if msg.PromptTokens > 0 {
        m.lastTurnPrompt = msg.PromptTokens
        m.lastTurnCompl  = msg.CompletionTokens
    }
    // loop 增量累加、hudCache 累加 不变
```

### doTurn

```go
m.hudTurns++      // 前置递增，不依赖 cm
m.cm.PrepareRun() // 不再调 EstimateCurrentTokens
```

### handleLoopDone

```go
result := m.cm.CompleteRun(...)

// ctx bar 一次性更新
m.lastPromptTokens = m.lastTurnPrompt + m.lastTurnCompl - result.TokensCompact

// HUD
m.hudMessages = result.MessageCount

// 归零
m.loopPrompt = 0; m.loopCompl = 0; ...
m.lastTurnPrompt = 0; m.lastTurnCompl = 0
```

### runTUI 恢复

```go
m.lastPromptTokens = 0  // 首个 LoopDone 更新
m.hudCacheHit/Miss = ctxMgr.Stats()  // 保留
```

---

## HUD 数据来源汇总

| 字段 | 来源 | 更新时机 |
|------|------|----------|
| `hudModel` | `TurnStats.Model` | 每 turn |
| `hudCacheHit/Miss` | `TurnStats.CacheHitTokens/MissTokens` | 每 turn 累加 |
| `hudBalance` | `BalanceUpdate.Balance` | 首次 loop 启动 |
| `hudTurns` | TUI 自增 | doTurn 开头 |
| `hudMessages` | `CompleteResult.MessageCount` | LoopDone |
| `lastPromptTokens` | `lastTurnPrompt + lastTurnCompl - TokensCompact` | LoopDone |
| `hudLatMs` | `time.Since(turnStartTime)` | LoopDone |

---

## Wave 范围

| 文件 | 操作 |
|------|------|
| `pkg/context/context.go` | 新增 `CompleteResult`；`CompleteRun` 返回 `CompleteResult`；删除 `EstimateCurrentTokens`、`CalibrateEstimatedTokens`、`MessageCount` |
| `pkg/context/context_test.go` | 适配 API 变更 |
| `cmd/waveloom/tui.go` | 新增 `lastTurnPrompt`/`lastTurnCompl`；删除 `ctxCalibrated`；移除所有 cm 读取调用 |
| `cmd/waveloom/runner.go` | `CompleteRun` 返回值适配 |
| `specs/hud.md` | 本文件 |
