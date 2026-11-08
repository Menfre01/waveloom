# Tasks: system-prompt-integrity

## Wave 1 — 文本内容修复(无代码改动)

同一 Wave 内各任务修改不同文件,可并行。

### 1.1 修 `pkg/prompt/default.md` 的拼接残留

- [x] 逐个重读确认 4 处污染位置,:75 与 :157–159 全部命中
- [x] 删除 `` ` + " `` 与 `` " + ` `` 残留,还原为普通行内代码
- [x] 验证:grep 零匹配

### 1.2 补 `default.md` 分节与缺失章节

- [x] `## Coding Scenarios` 与 `## Bug Investigation` 之前各补空行
- [x] 新增 `## Information Sources` 节(:54),覆盖:未知 URL 先搜后取、已知 URL 直接取、优先一手来源、记录文档日期、证据不足时明说
- [x] 验证:`## Information Sources` 在 :54;全文件标题前空行断言通过

### 1.3 改写 `default.md` 的成本提示

- [x] 改为真实比例并写明比较对象:`4x a cache-miss input token and 200x a cache-hit input token (deepseek-flash list price)`
- [x] 保留 subagent 输出约束,只改数字与比较对象
- [x] 验证:`TestRegression_PricingClaimMatchesTable` 由定价表复现 8.0/2.0 与 8.0/0.04

### 1.4 新建 `pkg/prompt/tail.md`

- [x] 三类 MUST 规则:决策边界、禁止建议式收尾、动作完整性
- [x] UTF-8 无 BOM、LF 行尾,25 行
- [x] 验证:内容与 `specs/prompt/instruction-placement/spec.md` 的 Requirement 对应

### 1.5 收窄 `pkg/tool/ask_user_question_prompt.md`

- [x] 删除 "Get decisions on implementation choices" 与 "Offer choices to the user about what direction to take"
- [x] 替换为:仅确认不可逆操作、跨项目边界、显著成本、与用户已给要求冲突的方向变更
- [x] 新增一条:项目内实现选择(命名、文件布局、复用哪个既有 helper、如何组织改动)自行决定,不得提问
- [x] 保留 "Only ask when you cannot reasonably infer..." 约束
- [x] 验证:grep "implementation choices" / "Offer choices" 零匹配

### 1.6 同步 `docs/system-prompt.md`

- [x] 同步 1.1–1.3 的全部改动
- [x] 更新头部声明:补充 `tail.md` 说明(由两入口在组装链末尾追加,不在镜像内)
- [x] 结构导读表补 `Information Sources` 行
- [x] **额外发现并修复**:镜像块原本漏了 `## Tool Use Discipline` 整节(1616 字符),"严格一致"的声明早已失真;已补回并在导读表加对应行
- [x] 验证:python 逐字节比对镜像块与 `default.md`,16809 = 16809,`True`

## Wave 2 — 代码接入与回归防护(依赖 Wave 1)

### 2.1 导出尾部规则

- [x] `pkg/prompt/default.go` 增加 `//go:embed tail.md` + `var TailRules string`,并注明"其后不得再追加内容"的依据
- [x] 验证:`go test ./pkg/prompt/` 编译通过,`TailRules` 非空

### 2.2 主流程注入尾部规则

- [x] `cmd/waveloom/main.go` 在 `FormatToolPrompts()` 之后追加 `prompt.TailRules`
- [x] 更新原注释(原注释称"Coding Scenarios 等关键决策内容在前",与实验结论相反)
- [x] 加 import `pkg/prompt`
- [x] 验证:`make build` 通过

### 2.3 ACP 入口注入尾部规则

- [x] `cmd/waveloom/acp.go` 在 `buildSystemPrompt()` 之后追加同一段规则
- [x] 加 import `pkg/prompt`
- [x] 验证:`make build` 通过

### 2.4 修 `pkg/compaction` 的误导性截断文案

- [x] 三处删除 "full result already processed by Agent",改为 `removed from context; re-run or re-read if you need them`
- [x] 每处加 `// REGRESSION:` 注释
- [x] 验证:`go test ./pkg/compaction/` 通过(既有 `omitted` 断言不受影响)

### 2.5 回归防护测试

- [x] T1 `pkg/prompt/integrity_test.go` — `TestRegression_PromptNoConcatResidue`
- [x] T2 同文件 — `TestRegression_PromptSectionBlankLine`
- [x] 同文件 — `TestRegression_PricingClaimMatchesTable`
- [x] T3 `pkg/tool/prompt_refs_test.go` — `TestRegression_ToolPromptReferencesExist`
- [x] T4 `pkg/compaction/truncation_transparency_test.go` — `TestRegression_CompactionNoFalseClaim`
- [x] T5 `cmd/waveloom/system_prompt_test.go` — `Test_TailRulesIsLastSection`
- [x] T6 同文件 — `Test_TailRulesInjectedBothEntries`
- [x] 验证:全部 PASS

### 2.6 代码审查发现项(改动完成后自动审查)

审查结论:文本层修复到位,但"尾部规则必然存在"这一前提在恢复会话路径上不成立。

- [x] 🔴 **恢复会话路径丢失尾部规则(致命)** — `pkg/session/context.go` 的 `cm.messages = messages` 整体采用磁盘消息,而 session JSON 的 `messages[0]` 保存的就是当时的整份 system prompt;`--resume` / `--continue` / ACP `loadSessionFromDisk` 全走这里,升级后旧会话沿用旧 prompt,本次修复一条都不生效。
  - 修法:`ContextManager` 增加 `systemPrompt` 字段(构造时保存),`LoadFromFile` 在校验前用它覆盖 `messages[0].Content`;仅替换已有 system 消息,不插入。
  - 回归防护:`pkg/session/resume_prompt_test.go` 两个用例(有 system 消息时替换;无 system 消息时不插入)。
- [x] 🔴 **`Quality Gates` 与尾部规则正面冲突** — `default.md:43` "If stuck, explain the bottleneck and propose next steps" 是模型可援引的合法"建议式收尾"出口。改为 "If blocked, state the blocker and what you need — do not offer follow-ups as a substitute for finishing."(docs 镜像已同步)
- [x] 🟡 **`tail.md` 边界例外** — 原文 "crossing the project boundary (files outside it)" 与 `## Workspace` 的 "not a boundary" 冲突,会让用户点名过的外部路径也被反问。补 "unless the user explicitly named that path"。
- [x] 🟡 **compaction 恢复路径措辞** — "re-read" 对 bash 输出不成立(需重跑)。改为 "re-run or re-read"。
- [x] 🟡 **`--system-prompt` 语义变化** — 该 flag 名义上是"整体替换",但 `TailRules` 仍会追加。属预期(尾部规则是行为契约,不随自定义提示词关闭),在此记录,flag 帮助文本未改。
- [ ] 🟡 **新文件等待 `git add`** — `pkg/prompt/tail.md`、5 个测试文件、`openspec/` 仍为 untracked。`//go:embed tail.md` 依赖该文件,提交遗漏会导致他人 checkout 编译失败。
- 🟡 **未采纳的两条及理由**:
  - *抽 `assembleSystemPrompt` 替代源码级断言*:design D2 明确不预留抽象。当前静态断言已能捕获"顺序颠倒"与"漏注入入口";若将来收口为函数,再改为行为断言。
  - *定价测试只断言数量级*:`default.md` 文案已显式限定 "(deepseek-flash list price)",钉死 flash 自洽;换模型时不改文案则测试失败,正是期望的防护。

## Wave 3 — 验收

- [x] `make build && make test && make lint` 全绿(build ✓;全量包测试无失败 ✓;golangci-lint 0 issues) — 审查修复后重跑
- [x] `openspec validate system-prompt-integrity --strict` 通过
- [x] **实测一轮**(真实 key,1 turn / 25 steps):项目内可逆操作直接执行、跨两包扫全、真跑 `make build` + `go test`、结尾零"建议/需要我"句式;system prompt 51,801 字符,tail 在 95% 处且为末尾,prefix cache 命中 96% — 见会话 `73068587`
- [x] 对比改造前后基线:1114 个历史会话中 79 个(7%)最终回答含"建议/需要我"类句式;改造后本次 0 处命中(单样本,统计力有限,需后续累积)
- [x] CHANGELOG 汇总 —— **有意不在本 change 内执行**:项目 Release 规范把 changelog 汇总划归发版流程(从上次 tag 到 HEAD 扫描 commit 后统一归类),单个 change 归档时汇总会与之冲突。本 change 的用户可感知变化(自主执行边界、禁止建议式收尾、截断文案诚实化)已记录在 proposal 的 What Changes 中,发版时据此提取。

## 约束提醒

- 禁止直接 `go build`,统一 `make build`
- 修改 `pkg/` 或 `cmd/` 后运行中的 TUI 不会热重载,需重启验证
- 不自动提交;等待用户明确指令
