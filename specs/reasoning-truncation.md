# Reasoning Content Truncation 规格书

## 组件定位

Reasoning Content Truncation 是 Waveloom 上下文压缩策略的**第二层（L0.5）**，在 Tool Result Truncation（L0）基础上，对已消费的 `reasoning_content` 进行滑动窗口清除，在不改变消息结构的前提下进一步减少上下文体积。

**核心价值：** DeepSeek 思考模式下每条 `assistant(tool_calls)` 消息携带的 `reasoning_content` 通常 50-500 tokens。LLM API 无状态——`reasoning_content` 的唯一用途是让模型"继续之前的思考"。Loop 结束后模型已产出最终回复，旧 loop 的思考链对后续 loop 的边际推理价值随时间衰减。保留它们只会挤压上下文窗口、降低前缀缓存命中率。

**实验依据（2026-06-21）：** 对 DeepSeek API 的三组对照测试证实——
- 空 `reasoning_content`（`""`）不触发 400 错误 ✅
- 删除 `reasoning_content` 字段不触发 400 错误 ✅
- API 仅校验消息结构完整性（role、tool_call_id 配对），不校验 `reasoning_content` 内容

## 范围

本阶段实现滑动窗口清除。暂不纳入：

- ❌ 基于 token 预算的动态窗口大小调整
- ❌ LLM 摘要压缩旧 reasoning（L2）
- ❌ 压缩事件日志与 session JSON 记录

## 参考来源

- DeepSeek API 文档：思考模式 —— `reasoning_content` 回传规则与多轮对话拼接策略
- 实验数据：`/tmp/test_reasoning.py`（2026-06-21）—— 三种 reasoning_content 变体对照测试

## 设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| 清除时机 | `CompleteRun` 中，Tool Result Truncation 之后 | 同 L0——Loop 已消费完整思考链并产出最终回复，此时清除对当前 loop 零影响 |
| 清除对象 | 旧 loop 中 `RoleAssistant` 消息的 `ReasoningContent` 字段 | assistant 的 `Content`（回复文本）必须保留；仅 reasoning 可安全清除 |
| 清除策略 | 滑动窗口：保留最近 N 个 loop 的 reasoning，清除更早的 | 近期 reasoning 对当前任务可能有跨 loop 推理延续价值；远期 reasoning 边际价值趋零 |
| 默认窗口 | N=1（仅保留最近 1 轮） | loop 内子 turn 连续性由同 loop 的完整 reasoning 保证；上一轮的思考链可能对当前任务有跨轮参考价值，保留一轮后清除，缓存断裂点靠后更温和 |
| 消息结构 | 仅置空 `ReasoningContent` 字段，不增删消息、不变 role、不变 tool_calls | 不改变 JSON 序列化结构；`reasoning_content` 无 `omitempty`，空值仍输出为 `""` |
| 可观测性 | 复用 `LoopRecord.TokensTruncated` 累计节省 | 与 tool 截断共用同一统计通道；后续可拆分独立指标 |
| 字段处理 | 置为 `""` 而非删除 | 保持 JSON key 存在，避免 DeepSeek API 可能的字段缺失敏感（实验虽已验证删除可行，但保留 key 更安全） |

### 为什么 N=1 是合理默认值

```
场景                                同一 loop 内         跨 loop
─────────────────────────────────────────────────────────────────
DeepSeek 协议要求 reasoning 回传      ✅ 必须               ❌ 不必须
LLM 推理延续需要前序 reasoning         ✅ 需要               ❌ 边际递减
新 user 消息是最强的上下文切换信号      N/A                   ✅ 是
```

Loop 内的子 turn 连续性有同 loop 的完整 reasoning 保证——这些不会被清除。上一轮的思考链可能对当前任务（如"先读代码 → 理解结构"的跨轮任务）有参考价值——保留一轮。再往前的 reasoning 与新 user 消息的推理连续性已很弱，可以清除。

**缓存影响：** N=1 每轮只清理一个 loop 的 reasoning，断裂点紧贴历史末尾，缓存 miss 范围小。N=0 每轮完成立即清空，断裂点在 prevLen（历史最末尾），下一轮首次请求全部 miss。

### reasoning_content 时间价值衰减模型

```
价值
  │
  │ ████
  │ ██████
  │ ████████
  │ ██████░░░░
  │ ████░░░░░░░
  │ ██░░░░░░░░░░░
  │ █░░░░░░░░░░░░░░
  └──────────────────→ 时间（loop 序号）
     L0  L1  L2  L3  L4
     当前 loop
```

- L0（当前 loop）：完整保留——Loop 内子 turn 依赖
- L1-L2（最近 1-2 个 loop）：可选保留——多步任务可能需要
- L3+（更早 loop）：建议清除——与当前任务几乎无关

## 组件边界

### 输入

- `messages []llm.Message` — 完整消息历史
- `prevLen int` — 本轮新增消息的起始索引（`prevLen` 之前的消息属于旧 loop）
- `keepLoops int` — 保留 reasoning 的最近 loop 数（0 = 全部清除）

### 输出

- `messages []llm.Message` — ReasoningContent 已清除的消息历史（原地修改）
- `bytesSaved int` — 清除的 `reasoning_content` 累计字节数

### 依赖

- 无外部依赖（仅标准库）
- 不依赖 LLM Client、Tool Registry、Agent Loop

### 不依赖

- 不依赖 Tool Result Truncation（独立操作，但共用 `CompleteRun` 执行窗口）

## 接口定义

```go
// loopRange 记录一个 loop 在 messages 数组中的索引区间。
//
// startIdx 是该 loop 第一条消息在 messages 中的索引（含），
// endIdx 是下一条不属于该 loop 的消息索引（不含，即下一 loop 的 startIdx 或 len(messages)）。
type loopRange struct {
    loop     int // loop 序号（1-based）
    startIdx int // 起始索引（含）
    endIdx   int // 结束索引（不含）
}

// clearReasoningInRange 清除 messages[start:end] 中所有 assistant 消息的
// ReasoningContent，返回清除的估算 token 数（使用 estimatedTokensFromContent，
// 与 L0 Tool Result Truncation 保持一致）。
func clearReasoningInRange(messages []llm.Message, start, end int) int
```

## 核心算法——增量索引清理

不扫描整个 messages 数组。ContextManager 维护 `loopRanges []loopRange`，在每轮 `CompleteRun` 时：

1. 记录本轮 loop 的 `[startIdx, endIdx)`
2. 若已记录的 loop 数量超过 `reasoningKeepLoops`，清理最早一轮的 reasoning 并移除其 range

```
CompleteRun(messages, ...):

    prevLen = len(cm.messages)           // 本轮 loop 的消息在 messages 中的起始索引
    currentLoop = cm.stats.TotalTurns + 1

    // ... L0: Tool Result Truncation（现有逻辑）...

    validated, ok := llm.ValidateMessages(messages)
    cm.messages = validated
    newLen := len(validated)             // 本轮 loop 的结束索引

    // L0.5: 记录本轮 loop 的索引范围
    cm.loopRanges = append(cm.loopRanges, loopRange{
        loop:     currentLoop,
        startIdx: prevLen,
        endIdx:   newLen,
    })

    // 若超出保留窗口，清理最早一轮
    for len(cm.loopRanges) > cm.reasoningKeepLoops {
        oldest := cm.loopRanges[0]
        bytes := clearReasoningInRange(cm.messages, oldest.startIdx, oldest.endIdx)
        loopRec.TokensTruncated += bytes / 4
        cm.loopRanges = cm.loopRanges[1:]
    }

    // ... 落盘 ...
```

**为什么是增量而非全量：**

| 方式 | 每轮 O(n) | 说明 |
|------|----------|------|
| 全量扫描 | O(全部 messages) | 每轮遍历整个 history 找旧 loop 的 assistant 消息 |
| 增量索引 | O(该轮 messages) | 只处理恰好滑出窗口的那一轮，索引 O(1) 定位 |

### 示例：keepLoops=3 的完整流程

```
Loop 1 完成:
  loopRanges = [{loop:1, start:0, end:120}]
  len = 1 ≤ 3 → 不清理

Loop 2 完成:
  loopRanges = [{loop:1, start:0, end:120}, {loop:2, start:120, end:245}]
  len = 2 ≤ 3 → 不清理

Loop 3 完成:
  loopRanges = [{loop:1, ...}, {loop:2, ...}, {loop:3, start:245, end:380}]
  len = 3 ≤ 3 → 不清理

Loop 4 完成:
  loopRanges = [{1,...}, {2,...}, {3,...}, {4, start:380, end:510}]
  len = 4 > 3 → 清理 Loop 1: clearReasoningInRange(messages, 0, 120)
  loopRanges = [{2,...}, {3,...}, {4,...}]

Loop 5 完成:
  loopRanges = [{2,...}, {3,...}, {4,...}, {5, start:510, end:650}]
  len = 4 > 3 → 清理 Loop 2: clearReasoningInRange(messages, 120, 245)
  loopRanges = [{3,...}, {4,...}, {5,...}]
```

### 边界情况

**keepLoops=0（全部清除）：**

```
Loop 1 完成:
  loopRanges = [{1, start:0, end:120}]
  len = 1 > 0 → 清理 Loop 1: clearReasoningInRange(messages, 0, 120)
  loopRanges = []   ← 空

Loop 2 完成:
  loopRanges = [{2, start:120, end:245}]
  len = 1 > 0 → 清理 Loop 2: clearReasoningInRange(messages, 120, 245)
  loopRanges = []
```

注意：keepLoops=0 时，本轮完成后自己的 reasoning 也被清空。这符合预期——下一轮不需要上一轮的思考链。

**LoadFromFile 恢复：**

从 session JSON 恢复时，`loopRanges` 不会持久化（session JSON 不记录索引范围）。恢复后 `loopRanges` 为空，后续新 loop 从头累积。由于恢复后再启动的 loop 也会按策略清理，不会出现历史 reasoning 永远残留的问题。如需精确恢复，可从 `loops[]` 中的 `turn` 序号和 `messages` 数组长度反推，但首版不实现。

**未知 loop range：**

如果 loopRanges 为空（恢复场景或首轮），`len(loopRanges) > keepLoops` 为 false，不执行清理。后续每轮正常累积。

## 集成点

### `pkg/context/context.go` — `CompleteRun`

在现有 Tool Result Truncation 循环之后、`ValidateMessages` 之后插入：

```go
func (cm *ContextManager) CompleteRun(messages []llm.Message, ...) int {
    cm.mu.Lock()

    prevLen := len(cm.messages)       // 本轮新增消息的起始索引
    currentLoop := cm.stats.TotalTurns + 1

    // --- L0: Tool Result Truncation（现有逻辑）---
    // ... 遍历 messages[prevLen:] 截断 tool 消息 ...

    // 存储替换
    validated, ok := llm.ValidateMessages(messages)
    cm.messages = validated
    newLen := len(validated)           // 本轮结束后的消息总数

    // --- L0.5: Reasoning Content Truncation（新增）---
    // 记录本轮 loop 的索引范围
    if cm.reasoningKeepLoops >= 0 && newLen > prevLen {
        cm.loopRanges = append(cm.loopRanges, loopRange{
            loop:     currentLoop,
            startIdx: prevLen,
            endIdx:   newLen,
        })

        // 清理滑出窗口的旧 loop
        for len(cm.loopRanges) > cm.reasoningKeepLoops {
            oldest := cm.loopRanges[0]
            tokens := clearReasoningInRange(cm.messages, oldest.startIdx, oldest.endIdx)
            loopRec.TokensTruncated += tokens
            cm.loopRanges = cm.loopRanges[1:]
        }
    }

    // --- 后续：统计累加 + 落盘（现有逻辑）---
    ...
}
```

### `pkg/context/context.go` — `ContextManager` 结构体

```go
type ContextManager struct {
    mu                 sync.RWMutex
    messages           []llm.Message
    stats              Stats
    sessionPath        string
    reasoningKeepLoops int         // 保留 reasoning 的最近 loop 数（0 = 全部清除）
    loopRanges         []loopRange // 各 loop 在 messages 中的索引区间
}
```

### 对 `main.go` / `tui.go` / `runner.go` 的影响

无。`CompleteRun` 内部行为变化，对外接口不变。

## 状态图

```
LoopDone → CompleteRun(messages)
    │
    ├── L0: truncateToolResult() → 截断 tool 消息 Content
    │       收集 TruncationRecord[]
    │
    ├── ValidateMessages() → validated
    ├── cm.messages = validated        ← 记录本轮 endIdx
    │
    ├── L0.5: cm.loopRanges.append({loop, startIdx, endIdx})
    │       for len(loopRanges) > keepLoops:
    │           clearReasoningInRange(oldest.startIdx, oldest.endIdx)
    │           loopRanges = loopRanges[1:]          ← 增量清理
    │
    └── 统计累加 + 落盘 saveToPath()
```

## 不变量

1. **消息数量不变**
2. **消息角色不变**：role 字段原样保持
3. **tool_call_id 不变**：tool 消息与 assistant.tool_calls 的配对完整
4. **assistant.Content 不变**：回复文本完整保留
5. **tool.Content 不变**（已有截断除外）
6. **system/user 消息不变**
7. **保留窗口内的 reasoning 不变**：最近 `reasoningKeepLoops` 个 loop 的 reasoning 完整保留
8. **空 reasoning 安全**：`ReasoningContent` 已非空时置空，已空的不重复操作
9. **session JSON 兼容**：`reasoning_content` 无 omitempty，空值序列化为 `""`
10. **幂等**：同一条消息的 `ReasoningContent` 仅被清空一次（loopRange 被移除后不再重复处理）
11. **索引有效性**：`loopRanges[i].startIdx < loopRanges[i].endIdx`，相邻 range 的 `endIdx == 下一 range 的 startIdx`
12. **loopRanges 长度 ≤ reasoningKeepLoops**：每次 CompleteRun 后保证
13. **Token 估算一致**：`clearReasoningInRange` 使用 `estimatedTokensFromContent`（中文字符 0.6 / 英文字符 0.3），与 L0 tool truncation 的 `TokensOmitted` 计算同源

## 测试计划

### 单元测试 (`pkg/context/context_test.go`)

**clearReasoningInRange：**

1. **TestClearReasoningInRange_Empty** — 空切片，返回 0
2. **TestClearReasoningInRange_NoAssistant** — 区间内无 assistant 消息，返回 0
3. **TestClearReasoningInRange_ClearsReasoning** — 区间内的 assistant.ReasoningContent 被清空
4. **TestClearReasoningInRange_SkipsAlreadyEmpty** — ReasoningContent 已为空的不重复计数
5. **TestClearReasoningInRange_ReturnsTokens** — 返回的 token 数使用 estimatedTokensFromContent 估算（与 L0 一致）
6. **TestClearReasoningInRange_OutOfBounds** — start/end 越界保护

**增量索引逻辑：**

7. **TestLoopRange_KeepLoopsZero_ClearsImmediately** — keepLoops=0，每轮完成后自己的 reasoning 被清空，loopRanges 保持为空
8. **TestLoopRange_KeepLoopsOne** — keepLoops=1，Loop 2 完成后清理 Loop 1
9. **TestLoopRange_KeepLoopsThree** — keepLoops=3，Loop 4 完成后清理 Loop 1；Loop 5 完成后清理 Loop 2
10. **TestLoopRange_NoMessagesInLoop** — 空 loop（newLen == prevLen）不记录 range，不触发清理
11. **TestLoopRange_IndexCorrectness** — 验证 loopRange 的 startIdx/endIdx 与实际消息位置一致

### 手动验证

```bash
# 启动 waveloom，完成 2 个 loop 后检查 session JSON
# 验证 Loop 1 的 assistant(tool_calls) 消息 reasoning_content 为 ""
# 验证 Loop 2 的 assistant(tool_calls) 消息 reasoning_content 非空
cat ~/.waveloom/waveloom/sessions/<session-id>.json | jq '.messages[] | {role, reasoning_content, tool_calls}'
```

## 文件清单

| 操作 | 文件 | 说明 |
|------|------|------|
| 修改 | `pkg/context/context.go` | 新增 `loopRange` 结构体、`clearReasoningInRange`；`toolTruncationStrategies` 增加激进策略组；`ContextManager` 增加 `reasoningKeepLoops` + `loopRanges`；`CompleteRun` 集成增量清理 + 耦合二次压缩 |
| 修改 | `pkg/context/context_test.go` | 新增 clearReasoningInRange 单元测试 + 激进策略测试 + CompleteRun 集成测试 |
| 新增 | `specs/reasoning-truncation.md` | 本规格书 |

## 与 L0 Tool Result Truncation 的关系

| 维度 | L0: Tool Result Truncation | L0.5: Reasoning Content Truncation |
|------|---------------------------|-----------------------------------|
| 截断对象 | `RoleTool.Content` | `RoleAssistant.ReasoningContent` |
| 截断策略 | 按工具类型区分（head+tail+省略标记） | 滑动窗口（按 loop 保留/清除） |
| 触发条件 | 工具输出超阈值 | 消息位于保留窗口之外 |
| 可观测性 | `TruncationRecord` → session.compactions | `LoopRecord.TokensTruncated` |
| 执行顺序 | 先 | 后 |
| 相互依赖 | 无 | 耦合：L0.5 清理 reasoning 时缓存已断，可触发 L0 对该 loop 执行更激进的二次压缩（见下方） |

## 缓存断裂的"免费午餐"——耦合二次压缩

### 核心洞察

当 L0.5 清空某 loop 的 reasoning_content 时，**该 loop 在 messages 中的字节偏移已发生变化**——从该 loop 的 assistant 消息往后，全部是 cache miss。既然缓存已经断了，同一 loop 内的 tool result 保留 160 行和保留 1 行对计费没区别（都是 miss），但对上下文窗口有巨大差异。

**参考来源：** Claude Code 的 time-based microcompact 利用相同原理——当判定缓存已过期时，直接将 tool result 替换为占位符 `[Old tool result content cleared]`。

### 策略：L0 双阈值

```
Tool Result Truncation 策略表扩展：

工具      默认策略（缓存热，L0）          激进策略（缓存断，L0.5 触发）
────────  ─────────────────────────────  ──────────────────────────────────
read_file  head(150) + tail(10) + 标记    head(10) + tail(0) + 标记
shell      head(20) + tail(30) + 标记    head(5) + tail(5) + 标记，或占位符
grep       head(50) + 标记               head(10) + 标记，或占位符
ls        不截断                          不截断（输出小）
...       ...                            ...
```

### 算法

```
clearReasoningInRange(msg, type) 对 loop 内每条 tool 消息按激进阈值重新截断。
此函数与 L0 的 truncateToolResult 共用底层机制，仅策略参数不同：

   1. 清空该 range 内所有 assistant 的 ReasoningContent
   2. 对该 range 内所有 tool 消息按激进策略重新截断:
      - 查找对应的激进策略（如 read_file: {10, 10, 0}）
      - 若 content 已经过 L0 截断，解析省略标记还原近似的行数信息
      - 按激进策略重新生成截断内容，替换 Content
      - 累计节省的估算 token 数
   3. 返回累计节省 token
```

### 为什么不是纯占位符

激进策略保留**最小结构**（如 read_file 的 package 声明行 + import 第一行），而不是一步到位占位符：

```
占位符: "[已消费]"
  → 如果 LLM 需要知道文件里有什么类型，必须重新 read_file (~3000 tokens)

激进 head(10): 
  package main
  import (
      "fmt"
  ...
  → LLM 仍可看到 package + import，可能避免一次额外 tool call
```

保留 10 行 head（vs 占位符的 1 行）换来的是避免 3000 token 的额外 API 调用。参照 L0 规格书中[占位符 vs 截断](#为什么不是占位符)的论证。

### 递归清理的安全性

L0.5 激进压缩后的 tool result 可能再次匹配省略标记 `[... 省略 ...`。由于清理后的内容远小于阈值，不会触发二次截断——满足幂等性。

### 对现有 L0 的影响

L0 的 `truncateToolResult` 在 `CompleteRun` 中先于 L0.5 执行。它对本轮新增 tool 消息按默认策略截断，收集 `TruncationRecord`。L0.5 的激进压缩仅对 `clearReasoningInRange` 内的旧消息操作——两者在消息范围上不重叠，无冲突。

## 后续扩展

### L1 — 可配置窗口大小

- 通过 `settings.json` 的 `compaction.reasoning_keep_loops` 配置窗口大小
- 通过 `wvl --reasoning-keep-loops=N` CLI 参数覆盖
- 默认 N=0，提供 `--reasoning-keep-loops=-1`（保留全部）用于调试

### L1.5 — 动态窗口

- Token 预算感知：窗口大小随 `contextLimit` 动态缩放
- 滞回控制：触发阈值 85% vs 恢复阈值 60%

### TurnAnalytics 集成

- `TurnRecord` 增加 `ReasoningBytesSaved` 字段
- 跨 loop 趋势分析：reasoning 节省量 vs 回复质量变化
- 边际收益递减检测：当 reasoning 节省量持续下降 → 提示可进一步缩小窗口
