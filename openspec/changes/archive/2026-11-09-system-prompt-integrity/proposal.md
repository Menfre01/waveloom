# Proposal: system-prompt-integrity

## Why

用户反馈两类可感知劣化:模型"降智"(该做的动作省掉)与"留小问题不改"(自己知道解决办法、不需用户拍板却只给建议)。根因不在模型能力,而在系统提示词的**内容有损坏**与**组装顺序与注意力分布错配**。

项目自己的实验给出了量化依据:`experiments/attention_position/README.md` 12 次试验中,竞争条件下 END 位置指令胜出 10/12(83%)、BEGIN 2/12(17%)、**MID 0/12(0%,完全被忽略)**。而当前 `default.md`(16,209 字符)占 system prompt(~36K 字符)的 0%–45%,其 `Coding Scenarios`(:73)、`File Operations`、`Quality Gates`、`Task Coordination`(:160)等规则全部落在 MID 区;真正被遵从的 END 区(50%–100%)由工具 prompt(16,873 字符)占据,其中 `ask_user_question_prompt.md` 明确写着 "Get decisions on implementation choices as you work" 与 "Offer choices to the user about what direction to take"。

结果:模型最听得进"去问用户",最听不进"别偷懒、要验证、要建 todo"。

叠加因素:DeepSeek 官方 2026-10-17 公告 V4.1-Flash-1017 "we also reduced the model's task runtime and output length"——模型侧主动缩短输出,而我方提示词 `default.md:8` 的 "Be concise. Strip filler, narration, and enthusiastic fluff." 再压一次,双向压缩把执行与验证动作一并省掉。

## What Changes

### 用户可感知

- **不再把实现决策抛给用户**:`ask_user_question` 的用途从"实现选择也问"收窄为"仅在不可逆、跨边界(改项目外文件/凭据/网络出口)、或成本决策时问",其余由 agent 自主执行。
- **不再以"建议"收尾**:本轮发现的可修问题要么当场修掉、要么落 todo 追踪;禁止"建议你后续……"式交付。仅 plan mode 例外(规划产物天然不含实施)。
- **截断后重新读取而不是"以为已读"**:压缩/截断的提示文案如实告知内容已丢弃,模型不会因误导性文案而基于不存在的记忆继续推理。

### 内部修复(用户不可直接感知,但决定上面三项能否成立)

- 修 `pkg/prompt/default.md` 中 4 处 Go 拼接语法污染(:75、:157、:158、:159),形态为字面的 `` ` + "`…`" + ` ``,即源码字符串拼接片段误写入 Markdown,经 `go:embed` 原样进入 system prompt。
- 补齐 `default.md` 分节:`## Coding Scenarios`(:73)与上一节之间缺空行,Markdown 分节失效。
- 补 `## Information Sources` 节:`pkg/tool/web_search_prompt.md` 与 `web_fetch` 的 Description 引用了该节,但它**在 `default.md` 中根本不存在**(grep 无匹配),搜索纪律条款悬空。
- 改写 `default.md:123` 的成本提示:`Output tokens are 240x the cost of cached input tokens.` 是拿输出价对比**缓存命中价**(pro: 27.0/0.30 ≈ 90x,flash: 8.0/0.04 = 200x);对比**未命中**仅 4x。该句约束对象是 subagent 输出(其输出永久留在父上下文),约束有效但比例失真,改写为真实比例而非删除。
- 修 `pkg/compaction/compaction.go:437` 的截断文案:现文案 `"[... N lines omitted — full result already processed by Agent]"` 声称内容"已被 Agent 处理",实际已永久丢弃。
- 在 `cmd/waveloom/main.go:287` 之后追加 TAIL 段(真正的 END 高遵从区),承载决策边界、禁止建议式收尾、动作完整性三类 MUST 级规则;**不在 `default.md` 中部新增规则**(MID 遵从率 0%,改了无效)。

### 明确不做

- 不改默认模型。官方 2026-10-17 基准显示 V4.1-Flash-1017 在 DeepSWE 77.0 / Agents' Last Exam 35.4 / Automation-Bench 60.5 等超过 V4-Pro 的 62.7 / 25.7 / 31.8,且价格低 3.4–4.5 倍,继续用 `deepseek-flash` 是正确选择。
- 不改 `reasoning_effort` 默认值。官方 Thinking Mode 文档明确 "Thinking mode is enabled by default, with the default effort being high"。
- 不改 compaction 阈值。实测本会话水位 10%、148 个历史会话峰值 64.8%,均未触发 Tier1(45%)。

## Capabilities

### New Capabilities

- `prompt/system-prompt-integrity` — system prompt 文本完整性:不得含源码拼接残留、引用的 Markdown 节必须存在、分节合法、数字断言与实际定价一致。
- `prompt/instruction-placement` — 指令放置规则:MUST 级行为约束必须位于 END 高遵从区;MID 区不承载行为约束;工具 prompt 与 `default.md` 的职责边界明确。
- `compaction/truncation-transparency` — 上下文改写/截断必须如实告知模型内容已丢失,禁止暗示"已处理"。

### Modified Capabilities

无。`openspec/specs/` 当前为空,本项目首次建立规格资产。

## Non-goals

- **不解决"模型能力不足"**:本提案的前提是模型能力足够,问题在提示词侧。若改造后仍不达标,应另开提案做 A/B 评估(`eval/` 目录已有基础设施),不在本提案内调模型。
- **不重构工具 prompt 全文**:`edit_file_prompt.md`(8,059 字符,占工具 prompt 48%)与 JSON schema 高度重叠,压缩空间明确,但属于独立的文本重构,另行提案。
- **不接入官方 Search & Fetch API**:DeepSeek 2026-10-17 开放 `POST /v1/tools/search` 与 `/v1/tools/fetch`($0.75/1000 成功请求,QPS 100/25),是独立价值项;本提案只补 `## Information Sources` 节的搜索纪律,不换后端。
- **不调整子代理模型选择**:Explore 当前锁 flash(`pkg/subagent/agent.go:493`),影响证据质量,但改变它牵动成本模型,另行提案。
- **不修改 `previewContinueText` / `todoLastChanceText` 等 loop 注入文案**:它们是上一代提示词的补丁,是否与新的尾部规则冲突,待 P1 落地后按实测再定。

## Impact

### 受影响文件

| 文件 | 变更 |
|------|------|
| `pkg/prompt/default.md` | 修 4 处污染、补分节空行、补 `## Information Sources` 节、改写 :123 成本提示 |
| `docs/system-prompt.md` | 同一批污染同步(该文档自称与 `default.md` 严格一致) |
| `pkg/compaction/compaction.go` | :437 截断文案改为如实告知 |
| `pkg/tool/ask_user_question_prompt.md` | 用途第 3/4 条收窄为不可逆/跨边界/成本决策 |
| `cmd/waveloom/main.go` | :287 之后追加 TAIL 段注入 |

### 行为影响

- **system prompt 长度**:TAIL 段净增约 600–900 字符,低于 `default.md` 的 4%;相对 36K 总量可忽略。
- **前缀缓存**:TAIL 段是 system prompt 末尾的**静态**内容,只影响一次全量写入,不破坏后续轮次的前缀缓存(与 `main.go:273` "静态,不破坏前缀缓存"的既有做法一致)。
- **测试**:需补断言防止污染回潮(检测 ` + "`" + ` + "`" + ` 拼接残留、引用节存在性);`compaction` 文案变更需同步现有断言。
- **兼容性**:纯提示词与文案改动,无 API、配置、协议变更;三平台无差异(不涉及文件系统路径操作)。

### 风险

- **TAIL 段可能与其他尾部工具 prompt 竞争注意力**:工具 prompt 已有 16,873 字符,新增规则置于其**之后**(绝对末尾),按近因效应应获得更高遵从;若实测无效,需考虑前移工具 prompt 或压缩 `edit_file_prompt.md`。
- **"禁止建议式收尾"存在误报面**:实测近 40 个会话的最终回答中 41% 含"建议"字样,其中有正常交付。故本提案**只改提示词措辞,不加 loop 层词面检测**;若后续要加,必须收窄到"需要我…吗/要不要/由你定"并排除 plan mode。
- **`ask_user_question` 收窄可能让 agent 在真正需要用户决策时硬撑**:通过保留"不可逆/跨边界/成本"三类例外覆盖该风险。
