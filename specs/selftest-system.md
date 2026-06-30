# Self-Test System 组件规格书

## 组件定位

Self-Test System 是 Waveloom 的**元测试系统**——用 Skill 编排 Tool，以 cold agent 端到端执行的方式验证整个栈的行为正确性。

它不是 Go 单元测试的替代，而是**补充**：

| 测试层 | 执行者 | 确定可回归 | 覆盖范围 |
|--------|--------|-----------|---------|
| Go 单元测试 | `go test` | ✅ 完全确定 | 单个函数 / 单个包 |
| Go 集成测试 | `go test -tags=integration` | ✅ 完全确定 | 跨包协议对齐 |
| **Skill 集成测试**（本组件） | Cold agent 执行 skill → 消费 TurnEvent | ⚠️ 结构确定（tool 调用序列），文本模糊 | 全栈端到端（LLM → Loop → Tool → Context → Compaction → TUI 渲染） |

**核心价值：** 只有 skill 集成测试能验证"LLM 是否按 skill 指令正确编排了多 tool 调用"。Go 测试可以验证 `SkillTool.Execute()` 返回了正确的 body，但无法验证 LLM 读到 body 后是否会按指令依次调用 `grep` → `read_file` → `edit_file`。

## Sub-Agent 验证模式（核心架构洞察）

### 问题：主 Agent 自检的"上下文惯性"

当主 agent 运行到第 30 轮、消息历史膨胀到数万 token 后，让它自我检查某段代码的正确性，会面临**上下文惯性**（Context Inertia）：

| 惯性类型 | 表现 | 后果 |
|---------|------|------|
| **确认偏误** | "我刚写的代码应该没问题"——倾向于验证而非证伪 | 漏掉真实 bug |
| **锚定效应** | 早期对话中的假设持续影响后续判断 | 错误前提得不到纠正 |
| **沉没成本** | "我已经花了 20 轮修这个，不可能还有问题" | 过早宣布修复完成 |
| **注意力稀释** | 长上下文中 LLM 对最新指令的注意力权重下降 | 遗漏验证步骤 |
| **探测惯性** | 之前失败过的 tool 调用模式被再次尝试 | agent loop 的 `ConsecutiveSameError` 保护触发前已浪费多轮 |

这不是 Waveloom 实现的缺陷，而是**单 agent 长对话的固有属性**——任何 LLM 在长上下文中都会出现上述效应。

### 方案：三方角色分离

**关键实现洞察：** Sub-agent 的隔离边界是上下文，不是进程。"启动一个新的 sub-agent"并不意味着 `make build` + `os/exec`——它只是 `context.Fork(skillBody)`，在同一进程中创建一个新的 agent loop，携带空的消息历史和独立的 compaction 水位线。共享 tool.Registry、llm.Client、文件系统。零编译成本，毫秒级启动。干净的是**判断力**，不是二进制。

```
主 Agent                  Skill                   Sub-Agent
(编排者 + 裁判)           (合约)                   (执行者)
      │                      │                         │
      │  1. 需要验证 X       │                         │
      │─────────────────────→│                         │
      │  调用 skill tool     │                         │
      │                      │                         │
      │  2. 返回 Body        │                         │
      │←─────────────────────│                         │
      │  "启动 sub-agent     │                         │
      │   验证以下内容：      │                         │
      │   - 验收标准 A        │                         │
      │   - 验收标准 B        │                         │
      │   - 验收标准 C"       │                         │
      │                      │                         │
      │  3. 启动 sub-agent   │                         │
      │  (context: fork,      │                         │
      │   全新消息历史)        │                         │
      │─────────────────────────────────────────────────→│
      │                      │                         │
      │                      │   4. 执行验证            │
      │                      │   - 无上下文惯性         │
      │                      │   - 仅看到 skill body    │
      │                      │   - 冷静调用 tool        │
      │                      │                         │
      │  5. 验证结果          │                         │
      │←─────────────────────────────────────────────────│
      │  "PASS: A ✓  B ✓  C ✓"                          │
      │                                                  │
      │  6. 裁判                                          │
      │  - 全部 PASS → 验证通过                           │
      │  - 任一 FAIL → 回到修复循环                       │
```

### 三个角色的职责

| 角色 | 谁担任 | 职责 | 类比 |
|------|--------|------|------|
| **合约** | Skill（`SKILL.md`） | 定义验证范围、tool 编排步骤、验收标准 | 测试用例文档 |
| **执行者** | Sub-Agent（`context: fork`） | 严格按 skill body 执行验证，返回结构化结果 | CI runner |
| **裁判** | 主 Agent | 读取 sub-agent 结果，对照验收标准裁决 PASS/FAIL | Code reviewer |

### 为什么 Sub-Agent 比主 Agent 自检更可靠

1. **零上下文惯性**——sub-agent 的消息历史仅含 skill body + 验证过程中产生的 tool 调用。它不知道主 agent 花了 20 轮修这个 bug，不知道之前失败过 5 次——它只是一个"冷静的审计员"。

2. **合约强制**——skill body 的验收标准是铁律。主 agent 可能因为疲劳而跳过某条标准（"这条应该没问题，不查了"），sub-agent 没有"疲劳"概念。

3. **失败可重复**——如果 sub-agent 返回 FAIL，主 agent 修复后可以**再次启动一个新的 sub-agent**（全新 context），重复验证直到 PASS。每次都是干净的执行环境。

4. **可组合**——主 agent 可以在单轮中启动多个 sub-agent，分别验证不同关注点（性能、安全、代码风格），然后汇总裁判。

### 具体示例：验证 compaction 修复

```
═══════ 主 Agent 视角（Turn 32，消息历史 28000 tokens） ═══════

User: "compaction tier 3 摘要后 token 计数不准，请修复"

... 20 轮修复过程 ...

主 Agent: "我认为修复完成。让我启动验证 skill。"

→ skill("selftest/verify-compaction")
→ Skill body:

  # Verify Compaction Fix
  
  ## Instructions
  
  You are a verification sub-agent. Your task is to verify a compaction
  bug fix. You have NO context about the fix itself — only verify the
  current state.
  
  1. Read pkg/compaction/summarizer.go in full
  2. Read pkg/compaction/summarizer_test.go in full
  3. Run: go test ./pkg/compaction/ -run TestSummarizer -v -count=1
  4. Verify these invariants by reading the code (not running):
     a. TokenDiff after summary is always ≤ 0
     b. tier field is monotonically non-decreasing
     c. HardLimitReached is set before tier 4
  5. Output a structured report:
     ```
     INVARIANT_A: PASS|FAIL — <evidence>
     INVARIANT_B: PASS|FAIL — <evidence>
     INVARIANT_C: PASS|FAIL — <evidence>
     ```

═══════ Sub-Agent 视角（Turn 1，消息历史仅 skill body） ═══════

Sub-Agent: "I'll verify the compaction fix. Let me start by reading
the source."

→ read_file("pkg/compaction/summarizer.go")
→ read_file("pkg/compaction/summarizer_test.go")
→ shell("go test ./pkg/compaction/ -run TestSummarizer -v -count=1")

"Tests pass. Now checking invariants..."

"INVARIANT_A: FAIL — TokenDiff is +12 at line 347 when summary replaces
  content but the base token count wasn't recalculated after pruning.
  
 INVARIANT_B: PASS — tier progression 0→1→2→3, monotonic.
 
 INVARIANT_C: PASS — HardLimitReached check at line 289, before tier 4
  check at line 312."

═══════ 主 Agent 裁判 ═══════

主 Agent: "INVARIANT_A FAIL. Sub-agent 发现 TokenDiff 在 line 347
  有 +12 的泄漏。它不会因为'我修过了'而跳过检查——它根本不知道我修过。
  
  让我修复 line 347 的 token 重算逻辑，然后重新启动一个新 sub-agent
  验证。"

→ edit_file(...)  // 修复 line 347
→ skill("selftest/verify-compaction")  // 再次启动，全新 sub-agent

"INVARIANT_A: PASS ✓
 INVARIANT_B: PASS ✓
 INVARIANT_C: PASS ✓"

主 Agent: "三个不变量全部通过。修复确认完成。"
```

### 与现有架构的关系

```
pkg/skill/skill.go          ← Skill 作为"合约"的载体
    ↓ 依赖（P1，尚未实现）
pkg/subagent/orchestrator.go ← context: fork，启动独立 agent loop
    ↓ 依赖（P1，尚未实现）
pkg/selftest/runner.go      ← 程序化测试 harness（Wave 1 用嵌入式 loop）
    ↓ 扩展（Wave 2）
主 Agent 现场编排             ← "我需要验证 X → 写 skill → 启动 sub-agent → 裁判"
```

**三阶段演进：**

| Wave | 能力 | 验证模式 |
|------|------|---------|
| Wave 1（本 spec） | 嵌入式 agent loop + RecordingRegistry | 程序化 `go test` 驱动，预定义 skill |
| Wave 2 | Sub-Agent（`context: fork`）实现 | 主 agent 现场编排 skill → 启动 sub-agent → 裁判 |
| Wave 3 | 多 sub-agent 并行 | 主 agent 启动 3 个 sub-agent 同时验证性能/安全/风格 |

Wave 2 是真正的拐点——届时 Waveloom 可以在**运行时**对自己的修改做冷静的自我验证，而非依赖预定义的测试脚本。

## Codex 审查发现与修复（2025-06-29）

经 Codex CLI（独立 AI 审查，非 Claude）读完全部 spec 和源码后，发现 13 条具体问题。以下按严重程度逐条记录并修复 spec。

### 阻塞级

**#1 文件系统跨 sub-agent 污染** — 修复：新增不变量 13

> 能力矩阵中各 skill 顺序执行，前面 skill 的 `edit_file` / `write_file` 会永久改变磁盘状态。Sub-agent #3（code-modification）改了文件后，#5（external-info）继承的是被修改后的文件系统。"冷"的仅是消息历史——文件系统是热的。

修复方案：每个 capability test case 必须从独立临时目录启动，Setup 函数负责创建干净的 fixture。Runner 在 test case 间执行 `git checkout -- .` 或等价重置。

**#2 无结构化输出约束** — 修复：新增设计决策 + 不变量 14

> 方案要求 sub-agent 输出 "PASS/FAIL + 证据" 结构化报告，但 sub-agent 的 LLM 调用没有 `response_format: json_object` 约束。裁判解析自由文本 → 假阳性/假阴性。

修复方案：sub-agent 的 skill body 末尾强制输出 JSON 格式报告。Wave 1 harness 的 MockClient 返回预定义 JSON。Wave 2 在 sub-agent loop config 中设置 `ResponseFormat: llm.ResponseFormatJSONObject`。

**#3 验证与修复职责混淆** — 修复：重写能力矩阵

> 原 `test-verification` 能力包含 `edit_file` 步骤——sub-agent 既当审计员又当修理工。审计者编辑代码后不再冷。

修复方案：能力矩阵拆分为"纯验证 skill"（只读 tool 白名单）和"修复验证 skill"（读写 tool）。纯验证 skill 的验收标准排除 `edit_file` / `write_file`。详见下方更新后的能力矩阵。

**#5 context-compaction 能力是自指悖论** — 修复：重写为外部可验证

> 原设计让 sub-agent 自己判断 post-compaction 回答一致性，但 pre-compaction 内容已被压缩销毁。

修复方案：改为 harness 侧验证——(a) 注入预定义长消息序列触发压缩，(b) 通过 TurnStats 验证 `HasCompaction() == true` 且 tier ≥ 1，(c) 验证压缩后 ContextManager 的消息数量严格减少。不依赖 LLM 自评。

### 高风险

**#6 context.Fork 实现细节不足** — 修复：补充 ForkConfig 结构

> 原 spec 说"像创建新 ContextManager"——未定义：是否排除 AGENTS.md、是否共享 sessionPath、是否新建 Compactor。

修复方案：在集成 5 中补充 `ForkConfig` 结构——`ExcludeAgentsMd bool`、`NewSessionID string`、`NewCompactor bool`。

**#7 Permission Guard 共享污染** — 修复：新增不变量 15

> sub-agent 共享父 agent 的 `permission.Guard`，后者有 session 级别的 "don't ask again" 记忆。sub-agent 的 tool 调用可能因记忆中的 deny 而被误杀。

修复方案：sub-agent 使用独立的 Guard 实例（从同一规则集构造，但不继承 session 决策记忆）。

**#9 系统 prompt 偏见** — 修复：新增不变量 16

> sub-agent 收到与父 agent 相同的 system prompt（含 ## Available Tools 描述），工具描述带有隐含偏好。

修复方案：sub-agent 的 system prompt 中移除 `## Available Skills` 列表（仅保留工具列表），工具描述改为中性格式（名称 + 参数签名，去掉使用建议）。

**#12 裁判无法访问 tool trace** — 修复：skill body 要求输出 trace

> 当"主 agent 作为 LLM 裁判"时，只看到 sub-agent 自报文本，无法访问 RecordingRegistry 的实际 tool 调用记录。

修复方案：sub-agent 的输出 JSON 要求包含 `tool_calls` 字段（RecordingRegistry 数据由 harness 格式化注入）。裁判可交叉验证自报的 `result` 与实际的 `tool_calls`。

### 边缘条件

以下在现有设计中已覆盖或 Wave 1 无需处理：

| # | 问题 | 处理 |
|---|------|------|
| #4 | 无 sub-agent turn 预算 | Runner 强制 `MaxTurns: 15`（已加入设计决策） |
| #8 | rate limit 无调度 | Wave 1 用 mock LLM（CI）+ 真实 LLM（本地串行），不涉及 rate limit 竞争。P1 标记 |
| #10 | LLM 503 传播 | `LoopErr != nil` → test case 标记 SKIP（不变量 #8 已覆盖） |
| #11 | sub-agent 发散 | JSON 输出的 `tool_calls` 字段使 harness 可对比期望序列与实际序列 |
| #13 | 动态注入同义反复 | 改为 harness 侧验证：`loader.Load()` 前后 body 差异对比，不依赖 LLM |

## 能力矩阵（Codex #3 修复后）

能力集拆分为两个类别：**纯验证**（只读 tool，审计者角色）和**修复验证**（读写 tool，需独立 skill）。纯验证 skill 的 tool 白名单排除 `edit_file` / `write_file` / 破坏性 `shell`。

### 纯验证能力（只读白名单）

```
能力集                    Tool 编排                         验收标准（JSON 结构化输出）
──────────────────────────────────────────────────────────────────────────────────
code-understanding      grep → read_file                   · grep 命中数 ≥ 1
  (代码理解)              → lsp_definition                  · lsp_definition 跳转到正确行
                        → lsp_references                  · lsp_references 返回列表非空

external-info           web_fetch → read_file              · web_fetch 返回 HTTP 200
  (外部信息)              → grep                            · fetch 内容中目标信息可被 grep 命中

permission-boundary     shell(危险命令)                     · Guard 返回 deny
  (权限边界)              → 验证拒绝消息                     · ToolResult.Denied == true
                                                            · 拒绝消息包含命令名

context-compaction      [注入预定义长消息序列]                · TurnStats.HasCompaction() == true
  (上下文压缩)            → 触发压缩                         · TurnStats.Compaction.Tier ≥ 1
                        → 读取 TurnStats                   · 压缩后消息数量 < 压缩前
                                                            [harness 侧验证，非 LLM 自评]

dynamic-injection       loader.Load(skill, args)           · 渲染后 body 包含命令输出
  (动态注入)              → 对比渲染前后 body                 · `!command` 块被替换为实际输出
                        [harness 侧验证]                     · 命令退出码 0 时无 error 标记
```

### 修复验证能力（读写 tool，独立 skill）

```
能力集                    Tool 编排                         验收标准
──────────────────────────────────────────────────────────────────────────────────
code-modification       read_file → edit_file              · edit_file 后 lsp_diagnostic 无新增 error
  (代码修改)              → lsp_diagnostic                  · shell("go build") 退出码 0
                        → shell("go build")

test-verification       shell("go test -count=1")          · 测试退出码 = 0
  (测试验证)              → [若 FAIL] read_file(失败日志)     · 若 FAIL，日志定位到具体行
                        → [修复循环外] edit_file → 重新测试
                        [修复逻辑在独立修复 skill 中]

file-operations         write_file(fixture)                · read_file 回读内容完全一致
  (文件操作)              → read_file(回读)                  · search_file 可发现新文件
                        → search_file(验证存在)              · 二进制一致性（byte-level）
```

### 全量扫掠

```
full-capability-sweep:
  1. 依次执行每个纯验证能力（独立临时目录）
  2. 依次执行每个修复验证能力（独立临时目录，包含 fixture 准备 + 验证 + git reset）
  3. 汇总输出：
     {
       "sweep_result": {
         "total": 8,
         "pass": N,
         "fail": M,
         "skip": K,
         "tool_coverage": "12/12",
         "failures": [{ "capability": "...", "evidence": "..." }]
       }
     }
  4. 覆盖矩阵：每个 tool 在哪些能力中被验证，盲区自动标记
```

## 参考来源

- Waveloom 自身架构：`pkg/agentloop/`（TurnEvent channel）、`pkg/tool/`（Registry 接口）、`pkg/skill/`（Loader）
- 讨论记录：[Skill 编排 tool 做 self-test 的可行性分析](#)（本 spec 基于该讨论整理）
- Claude Code: 无直接对应——Claude Code 的测试以单元测试为主，未提供 skill 级集成测试框架
- 业界实践：Linux `selftests/`（内核自测）、Go `testing/quick`（基于属性的模糊测试）——灵感来源但形态不同

## 首版范围（P0）

- ✅ 测试 skill 约定：`.waveloom/skills/selftest/<name>/SKILL.md`，`disable-model-invocation: true`
- ✅ 测试 harness：`pkg/selftest/runner.go`，嵌入 agent loop，消费 TurnEvent channel
- ✅ Tool call recorder：wrap `tool.Registry`，记录所有 tool 调用及其参数/结果
- ✅ 断言类型：
  - `ToolCalled(name, argsPattern)` — 某 tool 被调用且参数匹配模式
  - `ToolSequence(names...)` — tool 调用顺序断言
  - `FileExists(path, contentPattern)` — 文件系统副作用
  - `NoFatalError()` — 无致命错误
  - `TurnCount(maxN)` — 在 N 轮内完成
- ✅ Cold agent 执行：每个 test case 构造独立 `agentloop.Config` + 新鲜 `llm.Client`
- ✅ 集成到 `make test-dogfood`（新 target）
- ✅ 测试结果输出：每个 case 的 tool 调用 trace + pass/fail + 失败时 dump 消息历史
- ✅ 结构化输出格式：sub-agent skill body 末尾强制 JSON 报告（`result` + `evidence` + `tool_calls`），Go harness 用 `encoding/json` 反序列化（Codex #2）
- ✅ 文件系统隔离：每个 test case 独立临时目录，case 间 `git checkout -- .` 重置（Codex #1）
- ✅ 验证/修复分离：纯验证 skill 排除 `edit_file` / `write_file`（Codex #3）
- ✅ sub-agent turn 硬上限：`Loop.Config.MaxTurns = 15`（Codex #4）

### P1 延后

- 子进程 cold agent（`os/exec` 启动独立 waveloom 二进制）—— Wave 1 用嵌入式 loop
- **Sub-Agent 验证模式**（`context: fork`）—— 主 agent 现场编排 skill → 启动 sub-agent → 裁判。依赖 `pkg/subagent/` 实现（含 `ForkConfig` 的全部字段）
- rate-limit-aware 调度器 —— Wave 1 用 mock LLM（CI）+ 真实 LLM（本地串行），不涉及 rate limit 竞争（Codex #8）
- 断言 DSL（YAML/JSON 声明式测试用例，无需写 Go 代码）
- 断言 DSL（YAML/JSON 声明式测试用例，无需写 Go 代码）
- 测试 skill 附属文件自动 fixture（`fixtures/` 目录自动挂载为 skill 附属文件）
- 并发 test case 执行
- 多 sub-agent 并行验证（性能/安全/风格三个 sub-agent 同时跑）
- 测试覆盖率报告（tool 覆盖率 vs 注册 tool 总数）
- 回归告警（与上次基准对比 tool 调用序列变化）

### 不纳入

- LLM 输出文本的精确断言（不可确定，不属于本系统范围）
- 对非 Waveloom 项目的通用测试框架（本系统专为 Waveloom 自身 dogfooding 设计）
- 性能 benchmark（属于 `/benchmark` skill 范围）

## 设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| 执行方式 | 嵌入式 agent loop（`agentloop.Run`） | 快速、可精确拦截 tool 调用、与 `go test` 集成；Wave 2 再加子进程模式 |
| 断言目标 | Tool 调用序列 + 文件副作用，**非** LLM 输出文本 | LLM 文本非确定；tool 调用是 agent 行为的客观记录 |
| 测试 skill 位置 | `.waveloom/skills/selftest/`（项目级） | 不污染个人 skill 空间；`disable-model-invocation: true` 确保正常使用中不可见 |
| Tool 拦截方式 | `tool.Registry` 装饰器（`RecordingRegistry`） | 不侵入现有 tool 实现；对 Loop 透明 |
| LLM Provider | 测试专用环境变量 `WAVELOOM_TEST_PROVIDER` | 避免消耗生产 API quota；可指向 mock server |
| 测试隔离 | 每个 test case 独立 `t.Run`，临时目录 | 文件系统副作用隔离；并行安全 |
| 失败输出 | Dump 完整消息历史 + tool trace | 方便定位 LLM "为什么没调用某个 tool" |
| 超时 | 单 case 120s，单 tool 30s | 留足 LLM 响应时间，防止网络波动误杀 |
| 执行模式（Wave 1） | 嵌入式 agent loop（`agentloop.Run`） | 快速、可精确拦截、与 `go test` 集成 |
| 执行模式（Wave 2） | Sub-Agent（`context: fork`），主 agent 现场编排 | 无上下文惯性，主 agent 担任裁判角色 |
| 三方角色 | Skill = 合约，Sub-Agent = 执行者，主 Agent = 裁判 | 关注点分离，每个角色只有单一职责 |
| 文件系统隔离 | 每个 capability test case 独立临时目录 + `git checkout -- .` 重置 | 防止前面 skill 的 `edit_file` 污染后续 skill（Codex #1） |
| 结构化输出 | sub-agent 输出 JSON，含 `result` + `evidence` + `tool_calls` 字段 | 消除自由文本解析的假阳性/假阴性（Codex #2） |
| 验证/修复分离 | 纯验证 skill 排除 `edit_file` / `write_file`；修复 skill 独立 | 审计者不可同时是修理工（Codex #3） |
| sub-agent 系统 prompt | 不含 `## Available Skills`，工具描述中性化 | 避免系统 prompt 偏见影响 sub-agent 选 tool（Codex #9） |
| sub-agent turn 预算 | `Loop.Config.MaxTurns = 15`（硬上限） | 防止 sub-agent 死循环燃烧 token（Codex #4） |
| Permission Guard | sub-agent 使用独立 Guard 实例，不继承 session 决策记忆 | 防止父 agent 的 "don't ask again" 误杀 sub-agent tool 调用（Codex #7） |
| context-compaction 验证 | harness 侧验证：TurnStats.HasCompaction + 消息数减少 + tier ≥ 1 | 消除 LLM 自评的循环依赖（Codex #5） |
| 动态注入验证 | harness 侧：`loader.Load()` 前后 body 差异对比 | 消除 sub-agent 看到预渲染 body 的同义反复（Codex #13） |

## 组件边界

### 输入

- `t *testing.T` — Go 测试上下文
- `skillName string` — 要执行的测试 skill 名称
- `skillArgs string` — 传入 skill 的参数（可选）
- `llm.Client` — LLM 客户端（由测试环境提供，支持 mock）
- `*skill.Loader` — skill 加载器（指向测试 fixture 目录）
- `tool.Registry` — 工具注册表（生产工具 + RecordingRegistry 包装）

### 输出

- `*TestResult` — 结构化测试结果（pass/fail + trace + 消息历史）
- `error` — 仅在 harness 自身错误时非 nil（如 skill not found）

### 依赖（接口，非具体实现）

- `agentloop.Config` + `agentloop.Run` — agent 循环
- `llm.Client` — LLM 调用
- `tool.Registry` — 工具注册表（RecordingRegistry 装饰）
- `skill.Loader` — skill 发现和加载
- `context.Context` — 取消/超时控制

### 不纳入本组件

- LLM mock server（由测试基础设施提供，不属于本组件）
- 测试 skill 编写指南（属于 `docs/` 文档）
- CI 集成（属于 `.github/workflows/`）

---

## 接口定义

> 所有类型定义在 `pkg/selftest/`。
> 本包仅依赖 `pkg/agentloop`、`pkg/tool`、`pkg/skill`、`pkg/llm`、标准库 `testing`。

### TestCase — 单个测试用例

```go
// TestCase 定义一个 skill 集成测试用例。
type TestCase struct {
    Name      string // 测试名称（对应 t.Run）
    SkillName string // 要执行的 skill 名称
    SkillArgs string // 传入 skill 的参数（可选）
    Setup     func(t *testing.T, dir string) // 在临时目录中准备 fixture 文件（可选）
    Assert    func(t *testing.T, trace *Trace) // 断言逻辑
    Timeout   time.Duration // 单 case 超时（0 = 默认 120s）
}
```

### Trace — 一次执行的全量记录

```go
// Trace 记录一次 agent loop 执行的完整轨迹。
type Trace struct {
    SkillName    string              // 执行的 skill
    SkillArgs    string              // skill 参数
    ToolCalls    []RecordedToolCall  // 按时间顺序排列的 tool 调用
    TurnCount    int                 // 总轮数
    TerminalReason agentloop.TerminalReason // 终止原因
    LoopErr      error               // 异常终止时的错误
    Messages     []llm.Message       // 完整消息历史
    Duration     time.Duration       // 执行耗时
}

// RecordedToolCall 记录单次 tool 调用。
type RecordedToolCall struct {
    Turn       int           // 所在 turn
    ToolCallID string        // tool call ID
    Name       string        // 工具名
    Arguments  string        // JSON 参数
    Result     string        // 输出文本（成功时）
    Error      string        // 错误信息（失败时）
    ErrorClass string        // 错误分类（recoverable / fatal）
    DurationMs int64         // 执行耗时
}
```

### Runner — 测试执行器

```go
// Runner 在嵌入式 agent loop 中执行 skill 测试用例。
type Runner struct {
    Client     llm.Client     // LLM 客户端
    Loader     *skill.Loader  // skill 加载器
    Registry   tool.Registry  // 基础工具注册表（Runner 内部包装 RecordingRegistry）
    SystemPrompt string       // system prompt（含 skill listing）
}

// NewRunner 创建 Runner。
func NewRunner(client llm.Client, loader *skill.Loader, registry tool.Registry, systemPrompt string) *Runner

// Run 执行一个测试用例。
// 步骤：
//  1. 创建临时目录作为 CWD
//  2. 调用 Setup 准备 fixture 文件
//  3. 构造 RecordingRegistry 包装 registry
//  4. 调用 loader.Load(skillName, skillArgs) 获取 body
//  5. 构造 agentloop.Config（含 recordingRegistry、临时 CWD）
//  6. 构造初始消息（system prompt + AGENTS.md + skill body 注入）
//  7. 调用 agentloop.Run → 消费 TurnEvent channel
//  8. 收集 Trace
//  9. 调用 Assert 断言
func (r *Runner) Run(t *testing.T, tc TestCase)
```

### RecordingRegistry — Tool 调用拦截器

```go
// RecordingRegistry 包装 tool.Registry，记录所有 tool 调用。
// 对 agent loop 完全透明——它只是代理到真实 Registry 并记录元数据。
type RecordingRegistry struct {
    inner    tool.Registry
    mu       sync.Mutex
    calls    []RecordedToolCall
}

// NewRecordingRegistry 创建 RecordingRegistry。
func NewRecordingRegistry(inner tool.Registry) *RecordingRegistry

// Registry 接口实现（代理 + 记录）
func (r *RecordingRegistry) Execute(ctx context.Context, name string, paramsJSON string) (*tool.ToolResult, error)
func (r *RecordingRegistry) List() []tool.ToolSpec
func (r *RecordingRegistry) Lookup(name string) (tool.Tool, bool)

// Calls 返回记录的调用列表（按时间顺序）。
func (r *RecordingRegistry) Calls() []RecordedToolCall
```

---

## 核心算法

### 1. TestCase 执行流程

```
输入: TestCase{SkillName, SkillArgs, Setup, Assert}
输出: pass / fail（通过 t.Errorf 报告）

1. tmpDir := t.TempDir()

2. 若 tc.Setup != nil:
     tc.Setup(t, tmpDir)         // 准备 fixture 文件（如 .go 文件、.md 文件）

3. recordingReg := NewRecordingRegistry(r.Registry)

4. loaded, err := r.Loader.Load(tc.SkillName, tc.SkillArgs)
   若 err → t.Fatalf("skill not found: %s: %v", tc.SkillName, err)

5. messages := []llm.Message{
       {Role: "system", Content: r.SystemPrompt},
       {Role: "user",   Content: loaded.Body},     // skill body 作为 user 消息注入
   }

6. ctx, cancel := context.WithTimeout(context.Background(), tc.Timeout)
   defer cancel()

7. cfg := agentloop.Config{
       SystemPrompt: "",                    // 已在 messages[0] 中
       MaxTurns:     20,                    // 硬上限，防止死循环
       ToolTimeout:  30 * time.Second,
   }

8. events := agentloop.Run(ctx, cfg, recordingReg, r.Client, messages)

9. trace := &Trace{SkillName: tc.SkillName, SkillArgs: tc.SkillArgs}
   开始时间 := time.Now()

10. 消费事件循环：
    for event := range events {
        switch e := event.(type) {
        case agentloop.ToolCallResult:
            trace.ToolCalls = append(trace.ToolCalls, RecordedToolCall{
                Turn:       e.Turn,
                ToolCallID: e.ToolCallID,
                Name:       e.ToolCallName,
                Arguments:  "...",  // 从 recordingReg 中匹配
                Result:     e.Result,
                Error:      e.Error,
                ErrorClass: e.ErrorKind,
                DurationMs: e.DurationMs,
            })
        case agentloop.TurnStats:
            trace.TurnCount = e.Turn
        case agentloop.LoopDone:
            trace.TerminalReason = e.Reason
            trace.LoopErr = e.Err
            trace.Messages = e.Messages
        }
    }

11. trace.Duration = time.Since(开始时间)

12. 从 recordingReg.Calls() 补全 Arguments 字段

13. tc.Assert(t, trace)
```

### 2. RecordingRegistry 实现

```
输入: inner tool.Registry
输出: *RecordingRegistry

Execute(ctx, name, paramsJSON):
  1. 开始时间 := time.Now()
  2. result, err := r.inner.Execute(ctx, name, paramsJSON)
  3. r.mu.Lock()
     r.calls = append(r.calls, RecordedToolCall{
         Name:       name,
         Arguments:  paramsJSON,
         Result:     result.Content（若 err == nil && result.Error == nil）,
         Error:      result.Error.Message（若 result.Error != nil）,
         ErrorClass: result.Error.Class（若 result.Error != nil）,
         DurationMs: time.Since(开始时间).Milliseconds(),
     })
     r.mu.Unlock()
  4. 返回 result, err

List():
  返回 r.inner.List()

Lookup(name):
  返回 r.inner.Lookup(name)
```

### 3. 常用断言辅助函数

```go
// AssertToolCalled 断言某个 tool 被调用过（至少一次）。
func AssertToolCalled(t *testing.T, trace *Trace, name string) {
    for _, c := range trace.ToolCalls {
        if c.Name == name {
            return
        }
    }
    t.Errorf("expected tool %q to be called, but it wasn't.\nTool calls: %s", name, formatToolCalls(trace.ToolCalls))
}

// AssertToolSequence 断言 tool 调用序列包含给定的子序列（允许中间有其他调用）。
func AssertToolSequence(t *testing.T, trace *Trace, names ...string) {
    idx := 0
    for _, c := range trace.ToolCalls {
        if idx < len(names) && c.Name == names[idx] {
            idx++
        }
    }
    if idx < len(names) {
        t.Errorf("expected tool sequence %v, got up to index %d\nTool calls: %s", names, idx, formatToolCalls(trace.ToolCalls))
    }
}

// AssertNoFatalError 断言没有致命错误。
func AssertNoFatalError(t *testing.T, trace *Trace) {
    for _, c := range trace.ToolCalls {
        if c.ErrorClass == "fatal" {
            t.Errorf("unexpected fatal error in tool %q: %s", c.Name, c.Error)
        }
    }
}

// AssertToolNotCalled 断言某个 tool 未被调用（如权限检查）。
func AssertToolNotCalled(t *testing.T, trace *Trace, name string) {
    for _, c := range trace.ToolCalls {
        if c.Name == name {
            t.Errorf("expected tool %q NOT to be called, but it was", name)
        }
    }
}

// AssertTurnCount 断言在 N 轮内完成。
func AssertTurnCount(t *testing.T, trace *Trace, maxTurns int) {
    if trace.TurnCount > maxTurns {
        t.Errorf("expected ≤%d turns, got %d", maxTurns, trace.TurnCount)
    }
}
```

---

## 三方集成

### 集成 1：Makefile（`make test-dogfood`）

```
.PHONY: test-dogfood
test-dogfood:
	@echo "=== Skill Integration Tests (dogfooding) ==="
	go test ./pkg/selftest/ -v -timeout 600s -count=1
```

`-count=1` 禁用 test cache，确保每次执行都真正调用 LLM。

### 集成 2：测试 skill 目录约定

测试 skill 统一放在项目级 `.waveloom/skills/selftest/` 下：

```
.waveloom/skills/selftest/
├── basic-tool-chain/
│   └── SKILL.md              # 验证：grep → read_file → edit_file 链
├── error-recovery/
│   └── SKILL.md              # 验证：file_not_found → LLM 自主修正
├── file-roundtrip/
│   └── SKILL.md              # 验证：write_file → read_file 内容一致
├── skill-in-skill/
│   └── SKILL.md              # 验证：skill 工具调用另一个 skill
├── dynamic-injection/
│   └── SKILL.md              # 验证：!`command` 动态注入正确执行
├── permission-boundary/
│   └── SKILL.md              # 验证：Guard 拦截危险命令
├── compaction-trigger/
│   └── SKILL.md              # 验证：长对话触发压缩后继续正确
└── multi-turn-plan/
    └── SKILL.md              # 验证：规划→执行→验证 多轮流程
```

每个测试 skill 的 frontmatter 约定：

```yaml
---
description: <简短描述，仅用于文档，不注入 system prompt>
disable-model-invocation: true   # 禁止 LLM 在日常使用中调用
user-invocable: false            # 仅由测试 harness 程序化调用
---
```

### 集成 3：LLM Client 配置

测试 harness 通过环境变量获取 LLM 配置：

```
WAVELOOM_TEST_PROVIDER=deepseek    # deepseek / openai / mock
WAVELOOM_TEST_API_KEY=sk-xxx      # API key
WAVELOOM_TEST_BASE_URL=...        # 可选，自定义 base URL
WAVELOOM_TEST_MODEL=...           # 可选，覆盖默认模型
```

若 `WAVELOOM_TEST_PROVIDER=mock`，使用 `llm.MockClient`（需新增），返回预定义响应，用于 CI 环境中无需真实 LLM 的快速验证。

### 集成 4：CI（`.github/workflows/`）

```yaml
# 在 test.yaml 中新增 job
test-dogfood:
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: '1.25'
    - name: Dogfood tests (mock LLM)
      run: WAVELOOM_TEST_PROVIDER=mock make test-dogfood
```

CI 中使用 mock LLM 以保持确定性和速度。真实 LLM 测试由开发者本地按需运行。

### 集成 5：Sub-Agent 编排器（Wave 2）

当 `context: fork` 子代理执行能力就绪后，Self-Test System 的"程序化 harness"模式可升级为"主 agent 现场编排"模式。

```
主 Agent 运行时自检流程
  │
  ├─ 1. 主 agent 判断需要验证某修改
  │     触发条件示例：
  │     - 刚完成一轮多文件编辑
  │     - 用户说 "检查一下有没有遗漏"
  │     - compaction 触发后想确认一致性
  │
  ├─ 2. 主 agent 现场构造（或使用预定义）验证 skill
  │     skill body 必须包含：
  │     - agent: subagent          ← frontmatter，触发 context fork
  │     - 明确的 tool 编排步骤
  │     - 结构化的验收标准
  │     - 期望的输出格式
  │
  ├─ 3. 主 agent 调用 skill tool → skill body 返回
  │
  ├─ 4. 主 agent 启动 sub-agent（context: fork）
  │     sub-agent 获得：
  │     - 全新的 agent loop
  │     - 消息历史仅含 skill body
  │     - 继承主 agent 的 tool registry（含 RecordingRegistry）
  │     - 独立的 compaction 水位线
  │
  ├─ 5. sub-agent 执行
  │     - 严格按 skill body 的编排步骤调用 tool
  │     - 产生结构化验收报告（PASS/FAIL + 证据）
  │     - agent loop 终止，返回结果
  │
  ├─ 6. 主 agent 读取 sub-agent 输出
  │     - 解析 PASS/FAIL 标记
  │     - 交叉验证（可启动另一个 sub-agent 复核）
  │
  └─ 7. 主 agent 裁决
        - 全部 PASS → 验证通过，继续主流程
        - 任一 FAIL → 根据证据修复，回到步骤 2
```

**新增接口（`pkg/subagent/`）：**

```go
// ForkConfig 定义 sub-agent 上下文的分叉参数。
// 每个字段对应 Codex 审查发现的一条边界条件。
type ForkConfig struct {
    // SkillBody 是 sub-agent 的初始 user 消息。
    SkillBody string

    // ExcludeAgentsMd 为 true 时不注入 AGENTS.md。
    // Sub-agent 不应看到项目编码规范——这会产生偏见（Codex #9）。
    ExcludeAgentsMd bool

    // NewSessionID 是 sub-agent 的独立 session ID。
    // 不继承父 sessionPath，防止子 loop 的 compaction 写坏父 session 持久化文件（Codex #6）。
    NewSessionID string

    // NewCompactor 为 true 时创建独立的 TieredCompactor。
    // Sub-agent 的 compaction 水位线必须从 0 开始（Codex #6）。
    NewCompactor bool

    // IndependentGuard 为 true 时从同一规则集构造新 Guard 实例。
    // 不继承父 agent 的 AskDecision 缓存（Codex #7）。
    IndependentGuard bool

    // SystemPrompt 覆盖默认 system prompt。
    // Sub-agent 的 system prompt 不含 skill listing 和工具使用建议（Codex #9）。
    SystemPrompt string

    // MaxTurns 是 sub-agent loop 的最大轮数。
    // 0 表示使用默认值 15（Codex #4）。
    MaxTurns int

    // ResponseFormat 强制 LLM 输出 JSON。
    // Sub-agent 的输出必须可被 Go harness 或主 agent 解析（Codex #2）。
    ResponseFormat llm.ResponseFormat
}

// Orchestrator 管理 sub-agent 的生命周期。
// Sub-agent 在同一进程中以独立 agent loop（新 goroutine + 新消息历史）运行，
// 不需要编译新二进制或启动子进程。
// 隔离的是上下文（消息历史、compaction 水位线、turnCount），
// 共享的是 tool.Registry、llm.Client、文件系统。
type Orchestrator interface {
    // Spawn 启动一个 sub-agent，传入 skill body 作为唯一上下文。
    // 返回 result channel，主 agent 等待 sub-agent 完成后读取。
    Spawn(ctx context.Context, skillBody string, registry tool.Registry) (<-chan SubAgentResult, error)

    // SpawnParallel 并行启动多个 sub-agent。
    SpawnParallel(ctx context.Context, tasks []SubAgentTask) (<-chan SubAgentResult, error)
}

type SubAgentResult struct {
    SkillName string
    Output    string            // sub-agent 的最终回复
    Trace     *Trace            // sub-agent 的 tool 调用轨迹
    Duration  time.Duration
}
```

**与 Wave 1 的关系：**

| | Wave 1（本 spec 实现） | Wave 2（Sub-Agent 就绪后） |
|---|---|---|
| 谁启动验证 | `go test` 程序化启动 | 主 agent 运行时自主启动 |
| skill 来源 | 预定义在 `.waveloom/skills/selftest/` | 预定义 + 主 agent 现场构造 |
| 执行者 | 嵌入式 agent loop（同进程） | Sub-agent（`context: fork`，独立 loop） |
| 断言 | Go 代码（`AssertToolCalled` 等） | 主 agent 解析 sub-agent 的结构化输出 |
| 上下文 | 干净（初始消息仅为 skill body） | 干净（初始消息仅为 skill body） |
| 适用场景 | CI / 预提交 / 回归 | 运行时自检 / 修复后验证 |

两种模式共享核心基础设施：`RecordingRegistry`、`Trace`、skill 合约格式。Wave 1 的 harness 本身也可以用 Wave 2 的主 agent + sub-agent 来验证——真正的 dogfooding 闭环。

---

## 启动流程（整合视图）

```
go test ./pkg/selftest/
  │
  ├─ 1. 构造 skill.Loader
  │     loader := skill.NewLoader(cwd, homeDir, sessionID, "medium")
  │     // 扫描 .waveloom/skills/selftest/ 下的测试 skill
  │
  ├─ 2. 构造 system prompt（不含 skill listing——测试 skill 都是 disable-model-invocation）
  │
  ├─ 3. 构造 llm.Client（根据 WAVELOOM_TEST_PROVIDER 环境变量）
  │
  ├─ 4. 构造 tool.Registry（生产工具注册）
  │
  ├─ 5. 构造 Runner
  │     runner := NewRunner(client, loader, registry, systemPrompt)
  │
  ├─ 6. 对每个 TestCase：
  │     t.Run(tc.Name, func(t *testing.T) {
  │         runner.Run(t, tc)
  │     })
  │
  └─ 7. 输出汇总
```

---

## 文件清单

| 操作 | 文件 | 说明 |
|------|------|------|
| 新增 | `pkg/selftest/runner.go` | Runner 类型 + Run 方法 + Trace 收集逻辑 |
| 新增 | `pkg/selftest/recorder.go` | RecordingRegistry 实现 |
| 新增 | `pkg/selftest/assertion.go` | 断言辅助函数（AssertToolCalled 等） |
| 新增 | `pkg/selftest/runner_test.go` | Runner 自身的单元测试（用 mock LLM） |
| 新增 | `.waveloom/skills/selftest/basic-tool-chain/SKILL.md` | 首个测试 skill |
| 新增 | `.waveloom/skills/selftest/error-recovery/SKILL.md` | 错误恢复测试 skill |
| 新增 | `.waveloom/skills/selftest/file-roundtrip/SKILL.md` | 文件读写往返测试 skill |
| 修改 | `Makefile` | 新增 `test-dogfood` target |
| 新增 | `specs/selftest-system.md` | 本规格书 |

### Wave 2 扩展（依赖 `pkg/subagent/` 实现）

| 操作 | 文件 | 说明 |
|------|------|------|
| 新增 | `pkg/subagent/orchestrator.go` | Sub-agent 生命周期管理（Spawn / SpawnParallel） |
| 新增 | `pkg/subagent/orchestrator_test.go` | Orchestrator 单元测试 |
| 修改 | `pkg/skill/skill.go` | frontmatter 新增 `agent` 字段（`subagent` / `inline`） |
| 修改 | `pkg/selftest/runner.go` | 支持 SubAgentOrchestrator 注入，从程序化模式升级到运行时模式 |

---

## 不变量

1. **测试 skill 不可在日常使用中可见**：`disable-model-invocation: true` + `user-invocable: false` 确保仅 harness 可调用
2. **每个 test case 独立临时目录**：文件系统副作用完全隔离，无测试间交叉污染
3. **RecordingRegistry 不改变 tool 行为**：纯代理模式，对 Loop 和 LLM 完全透明
4. **断言仅针对 tool 调用和文件副作用**：不对 LLM 输出文本做精确断言（不可确定）
5. **超时是硬性约束**：单 case 120s 超时后 context 取消，agent loop 终止，记录为失败
6. **失败时 dump 完整上下文**：消息历史 + tool trace 完整输出，支持事后分析
7. **测试 skill 不注入 system prompt**：`FormatSkillListing` 排除 `disable-model-invocation` 的 skill（对标 Claude Code）
8. **LLM 错误不计为 harness 错误**：LLM 返回 5xx 时 test case 标记为 SKIP（非 FAIL），避免网络波动误报
9. **不支持并行 test case**（Wave 1）：LLM API rate limit 约束，串行执行更稳定
10. **Sub-agent 上下文完全隔离**（Wave 2）：sub-agent 的消息历史仅含 skill body + tool 调用，不继承主 agent 的消息历史、不共享 compaction 水位线
11. **Skill 作为不可变合约**（Wave 2）：同一 skill 被主 agent 多次启动 sub-agent 执行时，每次都是全新 context——skill body 是唯一的不变量
12. **主 agent 是唯一裁判**（Wave 2）：sub-agent 只输出结构化报告（PASS/FAIL + 证据），不自行裁决。裁决权在主 agent
13. **文件系统在 capability 间隔离**（Wave 1）：每个 test case 从独立临时目录启动，case 间执行 `git checkout -- .` 重置。禁止跨 case 的文件系统状态泄漏（Codex #1）
14. **sub-agent 输出必须是结构化 JSON**（Wave 1/2）：包含 `result`（PASS/FAIL）、`evidence`（每项验收标准的通过/失败及证据）、`tool_calls`（实际调用的 tool 名称和参数列表）。Go harness 用 `encoding/json` 反序列化验证；Wave 2 中主 agent 解析 JSON 而非自由文本（Codex #2/#12）
15. **sub-agent 使用独立 Permission Guard**（Wave 2）：从同一规则集构造但不继承父 agent 的 session 决策记忆（`AskDecision` 缓存）。`UserResponder` 为 nil 时 ask 降级 deny 的行为是显式设计——sub-agent 不应触发用户交互（Codex #7）
16. **sub-agent 的 system prompt 不含 skill listing 和使用建议**（Wave 2）：仅保留 `## Available Tools`（名称 + 参数签名，中性格式）。移除 `## Available Skills` 和工具使用建议（Codex #9）
17. **验证 skill 与修复 skill 不可为同一 skill**（Wave 1/2）：纯验证 skill 的 tool 白名单排除 `edit_file` / `write_file` / 破坏性 `shell`。修复逻辑独立为单独的修复 skill（Codex #3）

---

## 测试计划

### Harness 自身单元测试（`pkg/selftest/runner_test.go`）

1. **TestRecordingRegistry_RecordsCalls** — RecordingRegistry 正确记录 tool 调用
2. **TestRecordingRegistry_ProxiesCorrectly** — RecordingRegistry 代理结果与真实 Registry 一致
3. **TestRecordingRegistry_ConcurrentSafe** — 并发 Execute 调用不丢记录
4. **TestRunner_SkillNotFound** — skill 不存在时 Runner.Run 正确失败
5. **TestRunner_MockLLM_SingleTool** — mock LLM 返回单个 tool call → Trace 包含该调用
6. **TestRunner_MockLLM_ToolChain** — mock LLM 返回多个 tool call → Trace 记录序列
7. **TestRunner_Timeout** — context 超时后 Runner 正确终止
8. **TestAssertToolCalled_Pass** — 断言通过场景
9. **TestAssertToolCalled_Fail** — 断言失败场景
10. **TestAssertToolSequence_Subsequence** — 子序列匹配（中间允许其他调用）
11. **TestAssertNoFatalError_Pass** — 无 fatal 错误时通过
12. **TestAssertNoFatalError_Fail** — 有 fatal 错误时失败

### 测试 skill 验收（端到端，需真实 LLM）

13. **TestDogfood_BasicToolChain** — `grep` → `read_file` → `edit_file` 序列
14. **TestDogfood_ErrorRecovery** — `read_file` 失败（file_not_found）→ LLM 调用 `search_file` 修正
15. **TestDogfood_FileRoundtrip** — `write_file` → `read_file` 内容一致
16. **TestDogfood_SkillInSkill** — skill A 调用 skill B（通过 skill tool）
17. **TestDogfood_DynamicInjection** — `!`command`` 输出正确替换
18. **TestDogfood_PermissionBoundary** — Guard deny → LLM 收到拒绝消息 → 未绕过
19. **TestDogfood_MultiTurnPlan** — 3+ turn 规划-执行-验证流程

---

## 示例：一个完整的测试 skill

### `.waveloom/skills/selftest/basic-tool-chain/SKILL.md`

```markdown
---
description: Verify grep → read_file → edit_file tool chain
disable-model-invocation: true
user-invocable: false
---

# Basic Tool Chain Test

## Instructions

1. Use grep to search for "TODO" in all .go files under the current directory
2. For each file found, use read_file to read the full content
3. If any file contains "TODO", use edit_file to replace "TODO" with "DONE"
4. After all edits, output a summary of what was changed

## Acceptance Criteria

- grep was called before read_file
- read_file was called before edit_file
- No fatal errors occurred
- The task completed within 5 turns
```

### 对应的 Go test case

```go
func TestDogfood_BasicToolChain(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping dogfood test in short mode")
    }

    runner := newTestRunner(t)

    runner.Run(t, TestCase{
        Name:      "BasicToolChain",
        SkillName: "selftest/basic-tool-chain",
        Setup: func(t *testing.T, dir string) {
            // 准备包含 TODO 的 .go 文件
            content := `package main

// TODO: implement this function
func main() {
    // TODO: add arguments parsing
    println("hello")
}
`
            os.WriteFile(filepath.Join(dir, "main.go"), []byte(content), 0o644)
        },
        Assert: func(t *testing.T, trace *Trace) {
            AssertToolSequence(t, trace, "grep", "read_file", "edit_file")
            AssertNoFatalError(t, trace)
            AssertTurnCount(t, trace, 5)
        },
    })
}
```
