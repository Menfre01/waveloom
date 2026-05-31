# Context Manager 组件规格书

## 组件定位

Context Manager 是 Waveloom Code Agent 的**上下文累积层**，负责跨 Agent Loop 调用维护消息历史，
使 DeepSeek 的前缀缓存（Prefix Cache）系统能够跨轮次命中。

**核心价值：** DeepSeek API 对请求 `messages` 数组的最长公共前缀进行缓存。如果每次 Loop
调用都从零开始构造消息，缓存的命中率为 0%。Context Manager 通过保持消息历史的连续性，
使得 System Prompt + 工具定义 + 历史对话的公共前缀被复用，大幅降低 Token 消耗和延迟。

## 范围

本阶段实现核心功能——**消息累积**。暂不纳入：

- ❌ Token 预算控制与动态阈值压缩
- ❌ 智能裁剪（保留最近 N 轮）
- ❌ 消息去重/冗余移除
- ❌ LLM 摘要压缩（L2）

已独立实施：

- ✅ Tool Result Truncation（L0）— 详见 `specs/tool-result-truncation.md`

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
| Loop 兼容 | Loop 检测到 messages[0].Role==system 时跳过注入 | 无需修改 Loop 代码 |

## 组件边界

### 输入
- `New(systemPrompt)` — 构造时注入 system prompt
- `PrepareRun(userInput)` → `[]llm.Message` — 每次用户输入前调用
- `CompleteRun(messages, stats)` — 每次 Loop 结束后调用
- `Reset()` — 清空历史（Ctrl+L 或 /clear）

### 输出
- `PrepareRun` 返回完整消息历史（含 system prompt + 累积历史 + 新 user 消息）
- `Stats()` 返回累计 Token 统计

### 依赖
- `waveloom/pkg/llm` — `llm.Message` 类型

### 不依赖
- 不依赖 LLM Client
- 不依赖 Tool Registry
- 不依赖 Agent Loop（Loop 通过 `PrepareRun`/`CompleteRun` 解耦）

---

## 接口定义

```go
package context

import "waveloom/pkg/llm"

// ContextManager 跨 Agent Loop 调用累积消息历史，
// 使 DeepSeek 前缀缓存系统能够跨轮次命中。
type ContextManager struct {
    mu       sync.RWMutex
    messages []llm.Message
    stats    Stats
}

// Stats 记录跨轮次的累计统计。
type Stats struct {
    MessageCount         int // 当前累积的消息数
    TotalTurns           int // 累计完成的 turn 数
    TotalPromptTokens    int // 累计输入 token
    TotalCompletionTokens int // 累计输出 token
    TotalCacheHitTokens  int // 累计缓存命中 token
    TotalCacheMissTokens int // 累计缓存未命中 token
}

// New 创建一个新的 ContextManager。
// systemPrompt 作为 messages[0] 注入，确保它始终是公共前缀的起点。
func New(systemPrompt string) *ContextManager

// PrepareRun 追加一条 user 消息到历史，返回完整消息切片供 Loop 使用。
// 返回的切片是内部状态的快照副本，Loop 对返回值的修改不影响 ContextManager。
func (cm *ContextManager) PrepareRun(userInput string) []llm.Message

// CompleteRun 用 Loop 完成后的完整消息历史替换内部状态，
// 并累加本轮 token 统计。
func (cm *ContextManager) CompleteRun(messages []llm.Message, promptTokens, completionTokens, cacheHit, cacheMiss int)

// Stats 返回当前累计统计的快照。
func (cm *ContextManager) Stats() Stats

// Reset 清空历史但保留 system prompt（messages[0]）。
func (cm *ContextManager) Reset()
```

---

## 核心算法

### PrepareRun

```
输入: userInput string
1. cm.mu.Lock()
2. cm.messages = append(cm.messages, Message{Role: "user", Content: userInput})
3. snapshot := copy(cm.messages)
4. cm.mu.Unlock()
5. 返回 snapshot
```

**为什么返回副本而非引用：** Loop 内部会修改 slice（append assistant/tool 消息）。
ContextManager 不应看到这些中间状态——只有 Loop 成功完成后才通过 `CompleteRun` 提交。

### CompleteRun

```
输入: messages []Message, promptTokens, completionTokens, cacheHit, cacheMiss int
1. cm.mu.Lock()
2. cm.messages = messages  // 替换为 Loop 完成后的完整历史
3. cm.stats.TotalTurns++
4. cm.stats.TotalPromptTokens += promptTokens
5. cm.stats.TotalCompletionTokens += completionTokens
6. cm.stats.TotalCacheHitTokens += cacheHit
7. cm.stats.TotalCacheMissTokens += cacheMiss
8. cm.stats.MessageCount = len(messages)
9. cm.mu.Unlock()
```

### Reset

```
1. cm.mu.Lock()
2. if len(cm.messages) > 0 && cm.messages[0].Role == "system" {
3.     cm.messages = cm.messages[:1]  // 保留 system prompt
4. } else {
5.     cm.messages = nil
6. }
7. cm.mu.Unlock()
```

---

## 集成点

### CLI 单次模式（`cmd/waveloom/runner.go`）

```go
// 单次模式接入 ContextManager，确保消息历史跨轮次累积
messages := cm.PrepareRun(userInput)
ch := loop.Run(ctx, messages)
// ... drain events ...
cm.CompleteRun(finalEv.Messages, runPromptTokens, runComplTokens, runCacheHit, runCacheMiss)
```

### Server 模式（将来）

每个 Session 持有独立的 `ContextManager` 实例。`session/create` 时通过 `New(systemPrompt)` 创建，`run/start` 时调用 `PrepareRun(userInput)` 获取历史，`loopDone` 时调用 `CompleteRun` 提交。

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

**关键不变量：** `cm.messages[0]` 永远是 system 消息。只要 system prompt 不变，
每次请求至少命中 1 条消息的缓存。对话越长，历史部分命中越多，节省的 Token 越多。

---

## 状态图

```
                    ┌──────────┐
                    │   New    │
                    │ (system) │
                    └────┬─────┘
                         │
          ┌──────────────┼──────────────┐
          │              │              │
          ▼              ▼              ▼
    ┌──────────┐   ┌──────────┐   ┌──────────┐
    │PrepareRun│   │  Stats   │   │  Reset   │
    │ +user msg│   │ (只读)   │   │ → system │
    └────┬─────┘   └──────────┘   └──────────┘
         │
         ▼
    ┌──────────┐
    │Loop.Run  │  (Loop 使用返回的 messages，内部追加 assistant + tool)
    └────┬─────┘
         │
         ▼
    ┌──────────┐
    │CompleteRun│  (用 Loop 产出的完整 messages 替换内部状态)
    └────┬─────┘
         │
         └──→ 回到 PrepareRun 等待下一轮
```

---

## 不变量

1. **System 消息在首条：** `cm.messages[0].Role == RoleSystem`，确保 system prompt 始终是公共前缀起点
2. **消息顺序：** System → User → Assistant → Tool → User → ... 严格遵守（由 Loop 保证，ContextManager 透传）
3. **CompleteRun 幂等替换：** 每次调用 CompleteRun 完全替换内部状态，不追加
4. **PrepareRun 副本隔离：** 返回的是副本，Loop 的修改不影响 ContextManager
5. **Reset 保留 system：** Reset 后 messages 回到 `[system]` 状态，而非空
6. **线程安全：** 所有公开方法受 RWMutex 保护

---

## 测试计划

1. **TestNew** — 构造后 messages 为 `[system]`，stats 为零值
2. **TestPrepareRun** — 首次调用返回 `[system, user:input]`
3. **TestPrepareRunMultipleTurns** — 多轮 PrepareRun+CompleteRun 后消息正确累积
4. **TestCompleteRun** — 替换内部状态，stats 正确累加
5. **TestCompleteRunPreservesSystem** — CompleteRun 后 messages[0] 仍为 system
6. **TestReset** — Reset 后 messages 回到 `[system]`
7. **TestResetEmptyHistory** — 无 system 时的 Reset（不应 panic）
8. **TestPrepareRunReturnsCopy** — 修改返回值不影响 ContextManager 内部状态
9. **TestStatsSnapshot** — Stats() 返回副本，修改不影响内部
10. **TestConcurrentAccess** — 并发的 PrepareRun + Stats 不触发 race

---

## 文件清单

| 操作 | 文件 | 说明 |
|------|------|------|
| 新增 | `pkg/context/context.go` | ContextManager 实现 |
| 新增 | `pkg/context/context_test.go` | 单元测试 |
| 新增 | `specs/context-manager.md` | 本规格书 |
| 修改 | `cmd/waveloom/runner.go` | 单次模式接入 ContextManager |
| 修改 | `cmd/waveloom/main.go` | 创建 ContextManager 实例并注入 |

---

## 后续扩展（Wave 6+）

当单次会话消息累积到接近上下文窗口上限时，需引入压缩机制：

```
压缩策略（从轻到重）:
  1. Token 计数监控 — 在 CompleteRun 中检查累计 token 是否接近限制
  2. 警告阈值 — ContextUsage > 80% 时通过 Stats 暴露警告标志
  3. 手动清空 — 用户通过 /clear 触发 Reset()
  4. 自动压缩 — 将旧 tool result 替换为 LLM 生成的摘要（需 LLM 调用，成本高）
  5. 硬截断 — 保留最近 N 轮 + System Prompt，丢弃旧轮次
```
