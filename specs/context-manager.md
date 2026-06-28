# Context Manager 组件规格书

## 组件定位

Context Manager 是 Waveloom Code Agent 的**上下文累积层**，负责跨 Agent Loop 调用维护消息历史，
使 DeepSeek 的前缀缓存（Prefix Cache）系统能够跨轮次命中。

**核心价值：** DeepSeek API 对请求 `messages` 数组的最长公共前缀进行缓存。如果每次 Loop
调用都从零开始构造消息，缓存的命中率为 0%。Context Manager 通过保持消息历史的连续性，
使得 System Prompt + 工具定义 + 历史对话的公共前缀被复用，大幅降低 Token 消耗和延迟。

## 范围

- ✅ 消息累积（PrepareRun / CompleteRun）
- ✅ 四级水位线上下文压缩（通过内部 Compactor，详见 `specs/compaction.md`）
- ✅ AGENTS.md 注入（InjectUserInstructions）
- ✅ Session 持久化（Save / LoadFromFile / RemoveSession）
- ✅ 累计 Token 统计（Stats）
- ✅ 历史重置（Reset）

## 参考来源

- DeepSeek API 文档：Prefix Cache 基于 `messages` 数组的最长公共前缀自动工作
- Claude Code: `src/query/tokenBudget.ts` — context window management
- Codex CLI: `core/src/session/turn_context.rs` — pre-sampling compact, token limit checks

## 设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| 消息所有权 | ContextManager 持有完整历史，Loop 只读使用 | Loop 不应感知跨调用的状态管理 |
| System Prompt 管理 | ContextManager 在初始化时注入首条 system 消息 | 确保 system prompt 始终是最长公共前缀的起点 |
| 生命周期 | PrepareRun → Loop.Run → CompleteRun | 清晰的三段式，Prepare 追加 user 消息，Complete 吸收 Loop 产出 |
| 线程安全 | 内部使用 sync.RWMutex | PrepareRun/CompleteRun 在 agent goroutine，Stats 在 UI goroutine 读取 |
| 压缩集成 | ContextManager 持有 TieredCompactor | 压缩状态与消息历史生命周期一致，便于持久化 |
| AGENTS.md 注入 | 作为 messages[1] 的独立 user 消息 | 对标 Codex UserInstructions fragment；保持 system prompt 短小稳定 |
| 注入防重 | instructionsInjected 标记 | 防止 Reset 后重复注入 |

## 组件边界

### 输入
- `New(systemPrompt)` / `NewWithCompaction(systemPrompt, config, summarizer)` — 构造时注入 system prompt 和压缩配置
- `PrepareRun(userInput)` → `[]llm.Message` — 每次用户输入前调用
- `CompleteRun(messages, ...)` — 每次 Loop 结束后调用
- `Reset()` — 清空历史（Ctrl+L）
- `InjectUserInstructions(text)` — 注入 AGENTS.md

### 输出
- `PrepareRun` 返回完整消息历史（含 system prompt + 累积历史 + 新 user 消息）
- `CompleteRun` 返回 `CompleteResult`（含消息数、压缩结果、硬限标记）
- `Stats()` 返回累计 Token 统计

### 依赖
- `waveloom/pkg/llm` — `llm.Message` 类型
- `waveloom/pkg/compaction` — `TieredCompactor`、`CompactionConfig`、`Summarizer`、`CompactionData`

### 不依赖
- 不依赖 LLM Client
- 不依赖 Tool Registry
- 不依赖 Agent Loop（Loop 通过 `PrepareRun`/`CompleteRun` 解耦）

---

## 接口定义

```go
package context

import (
    "waveloom/pkg/compaction"
    "waveloom/pkg/llm"
)

// Stats 记录跨轮次的累计统计。
type Stats struct {
    MessageCount          int   // 当前累积的消息数
    TotalTurns            int   // 累计完成的 loop 数
    TotalPromptTokens     int   // 累计输入 token
    TotalCompletionTokens int   // 累计输出 token
    TotalCacheHitTokens   int   // 累计缓存命中 token
    TotalCacheMissTokens  int   // 累计缓存未命中 token
    TotalReasoningTokens  int   // 累计思考链 token
    TotalDurationMs       int64 // 累计耗时（毫秒）
}

// ContextManager 跨 Agent Loop 调用累积消息历史。
// 所有公开方法受 RWMutex 保护，并发安全。
type ContextManager struct {
    mu                   sync.RWMutex
    messages             []llm.Message
    stats                Stats
    sessionPath          string
    compactor            *compaction.TieredCompactor
    instructionsInjected bool
}

// New 创建一个新的 ContextManager（使用默认压缩配置，无 Summarizer）。
func New(systemPrompt string) *ContextManager

// NewWithCompaction 创建一个带完整压缩配置的 ContextManager。
func NewWithCompaction(systemPrompt string, config compaction.CompactionConfig, summarizer compaction.Summarizer) *ContextManager

// Compactor 返回内部 TieredCompactor 引用（供 TUI/持久化使用）。
func (cm *ContextManager) Compactor() *compaction.TieredCompactor

// PrepareRun 追加一条 user 消息到历史，返回完整消息切片供 Loop 使用。
// 返回的切片是内部状态的副本，Loop 对返回值的修改不影响 ContextManager。
// 同时推进会话级 turn 计数（compactor.AdvanceTurn()）。
func (cm *ContextManager) PrepareRun(userInput string) []llm.Message

// CompleteResult 由 CompleteRun 返回，供上层（TUI/runner）获取本轮状态。
type CompleteResult struct {
    MessageCount     int                           // 当前消息总数
    Compaction       compaction.CompactionResult   // 本轮压缩结果
    HardLimitReached bool                          // 是否触发硬临界值
    HardLimitReason  string                        // 触发原因："usage" 或 "tier3_failures"
}

// CompleteRun 用 Loop 完成后的完整消息历史替换内部状态，并累加本轮 token 统计。
//
// promptTokens 为跨轮累加值（用于 TotalPromptTokens 统计），
// contextTokens 为末轮 API 返回的 prompt_tokens（用于上下文利用率计算）。
// model 为实际使用的模型名，reason 为终止原因，durationMs 为本轮耗时。
//
// 上下文压缩已在 Loop 内每轮执行，此处仅做状态持久化。
// 如果设置了 sessionPath 则自动落盘。
func (cm *ContextManager) CompleteRun(
    messages []llm.Message,
    promptTokens, contextTokens, completionTokens, cacheHit, cacheMiss, reasoningTokens int,
    model string, durationMs int64, reason string,
) CompleteResult

// SetSessionPath 设置 session 落盘路径。设置后每次 CompleteRun 自动保存。
func (cm *ContextManager) SetSessionPath(path string)

// Save 手动触发一次完整的 session 持久化。
func (cm *ContextManager) Save()

// LoadFromFile 从 session 文件恢复 ContextManager 的内部状态。
// 恢复后自动将 sessionPath 设为该文件路径，后续 CompleteRun 自动落盘。
// 同时恢复压缩决策表和摘要链。
func (cm *ContextManager) LoadFromFile(path string) bool

// RemoveSession 删除当前的 session 落盘文件并关闭自动落盘。
func (cm *ContextManager) RemoveSession()

// Stats 返回当前累计统计的快照（MessageCount 实时取自 len(messages)）。
func (cm *ContextManager) Stats() Stats

// Reset 清空历史并归零统计，但保留 system prompt（messages[0]）。
// 同时重置压缩状态和 instructionsInjected 标记。
func (cm *ContextManager) Reset()

// ResetCompactor 仅重置压缩状态（决策表、水位线、摘要链），不影响消息历史和统计。
func (cm *ContextManager) ResetCompactor()

// InjectUserInstructions 注入 AGENTS.md 内容作为第一条 user 消息。
// 在 system prompt（messages[0]）之后、用户实际输入之前插入。
// 若已注入过（instructionsInjected==true）则不做任何操作。
func (cm *ContextManager) InjectUserInstructions(text string)
```

---

## 核心算法

### PrepareRun

```
输入: userInput string
1. cm.mu.Lock()
2. cm.messages = append(cm.messages, Message{Role: "user", Content: userInput})
3. cm.compactor.AdvanceTurn()  // 推进会话级 turn 计数
4. snapshot := copy(cm.messages)
5. cm.mu.Unlock()
6. 返回 snapshot
```

### CompleteRun

```
输入: messages, promptTokens, contextTokens, completionTokens, cacheHit, cacheMiss,
      reasoningTokens, model, durationMs, reason
1. cm.mu.Lock()
2. validated, _ := llm.ValidateMessages(messages)
3. cm.messages = validated
4. cm.stats.TotalTurns++
5. cm.stats.TotalPromptTokens += promptTokens
6. cm.stats.TotalCompletionTokens += completionTokens
7. cm.stats.TotalCacheHitTokens += cacheHit
8. cm.stats.TotalCacheMissTokens += cacheMiss
9. cm.stats.TotalReasoningTokens += reasoningTokens
10. cm.stats.TotalDurationMs += durationMs
11. lastCompaction := cm.compactor.LastResult()
12. cm.mu.Unlock()
13. 如果 sessionPath != "" → saveToPath()
14. 返回 CompleteResult{MessageCount, Compaction, HardLimitReached, HardLimitReason}
```

### Reset

```
1. cm.mu.Lock()
2. cm.stats = Stats{}
3. cm.instructionsInjected = false
4. cm.compactor.Reset()
5. 如果 messages[0].Role == system → messages = messages[:1]；否则 messages = nil
6. cm.mu.Unlock()
```

### InjectUserInstructions

```
1. cm.mu.Lock()
2. 如果 instructionsInjected 或 text 为空 → return
3. 在 messages[0] 和 messages[1:] 之间插入 Message{Role: "user", Content: text}
4. instructionsInjected = true
5. cm.mu.Unlock()
```

---

## 集成点

### TUI 模式（`cmd/waveloom/tui.go`）

```go
// 初始化
ctxMgr := ctxpkg.NewWithCompaction(systemPrompt, compactionConfig, summarizer)
ctxMgr.InjectUserInstructions(agentsMdText)

// 每轮对话
messages := ctxMgr.PrepareRun(userInput)
ch := loop.Run(ctx, messages)
// ... TUI 消费 TurnEvent ...
result := ctxMgr.CompleteRun(finalEv.Messages,
    runPromptTokens, runContextTokens, runComplTokens,
    runCacheHit, runCacheMiss, runReasoningTokens,
    model, durationMs, reason)
```

### 单次模式（`cmd/waveloom/runner.go`）

```go
expandedInput, _, _ := expander.Expand(ctx, userInput, cwd)
messages := ctxMgr.PrepareRun(expandedInput)
ch := loop.Run(ctx, messages)
// ... drain events ...
ctxMgr.CompleteRun(...)
```

---

## 缓存命中原理

DeepSeek 的前缀缓存按以下规则工作：

```
请求 1: messages = [system, user:"读 a.go", assistant:"...", tool:"..."]
         → 服务端缓存整个 messages 数组

请求 2: messages = [system, user:"读 a.go", assistant:"...", tool:"...", user:"改第3行"]
         → 前 4 条消息与前一次请求完全相同 → 前缀缓存命中 4 条
         → 只处理新增的 user:"改第3行"

请求 3: messages = [system, user:"读 b.go"]  
         → 仅 system 消息与前缀匹配 → 缓存命中 1 条
```

**关键不变量：** `cm.messages[0]` 永远是 system 消息。AGENTS.md 在 `messages[1]` 同样稳定不变。
只要 system prompt 和 AGENTS.md 不变，每次请求至少命中 2 条消息的缓存。

---

## 状态图

```
                    ┌──────────┐
                    │   New    │
                    │ (system) │
                    └────┬─────┘
                         │
                         │ InjectUserInstructions
                         ▼
                    ┌──────────────┐
                    │ +AGENTS.md   │  (messages[1])
                    └────┬─────────┘
                         │
          ┌──────────────┼──────────────┐
          │              │              │
          ▼              ▼              ▼
    ┌──────────┐   ┌──────────┐   ┌──────────┐
    │PrepareRun│   │  Stats   │   │  Reset   │
    │ +user msg│   │ (只读)   │   │ → system │
    │AdvanceTurn│  └──────────┘   │ +inject  │
    └────┬─────┘                  └──────────┘
         │
         ▼
    ┌──────────┐
    │Loop.Run  │  (Loop 使用返回的 messages，内部追加 assistant + tool)
    └────┬─────┘
         │
         ▼
    ┌──────────┐
    │CompleteRun│  (替换内部状态 + 累加统计 + 自动落盘)
    └────┬─────┘
         │
         └──→ 回到 PrepareRun 等待下一轮
```

---

## 不变量

1. **System 消息在首条：** `cm.messages[0].Role == RoleSystem`，确保 system prompt 始终是公共前缀起点
2. **AGENTS.md 在 messages[1]：** 注入后固定在 system 之后、用户输入之前
3. **消息顺序：** System → User(AGENTS.md) → User(input) → Assistant → Tool → ... 严格遵守
4. **CompleteRun 幂等替换：** 每次调用 CompleteRun 完全替换内部状态，不追加
5. **PrepareRun 副本隔离：** 返回的是副本，Loop 的修改不影响 ContextManager
6. **Reset 保留 system：** Reset 后 messages 回到 `[system]` 状态，instructionsInjected 重置
7. **线程安全：** 所有公开方法受 RWMutex 保护
8. **压缩生命周期一致：** compactor 状态随 session 持久化/恢复

---

## 文件清单

| 文件 | 说明 |
|------|------|
| `pkg/context/context.go` | ContextManager 实现 + CompleteResult + InjectUserInstructions |
| `pkg/context/context_test.go` | 单元测试 |
| `pkg/context/session_persist.go` | Session JSON 持久化（Save / Load / Remove） |
| `pkg/context/session_persist_test.go` | 持久化测试 |
| `pkg/context/transcript.go` | Session 列表（RecentEntry / UpdateRecentSessions） |
| `pkg/context/transcript_test.go` | Transcript 测试 |
| `specs/context-manager.md` | 本规格书 |
