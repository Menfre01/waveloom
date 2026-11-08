# Design: system-prompt-integrity

## 上下文

system prompt 由多个来源顺序拼接而成,`cmd/waveloom/main.go:264-288` 是主流程的组装链,`cmd/waveloom/acp.go:162` 是 ACP 入口的组装链。两链长度不同,但都以 `buildSystemPrompt()` 产出的 `prompt.Default` 为基底。

```
main.go:
  264  buildSystemPrompt()        → prompt.Default (16,209 字符)      0% ─┐
  271  + formatEnvironmentSection                                       │ MID
  275  + IDE capability guide                                           │ (0/12 遵从)
  280  + FormatSkillListing()                                           │
  287  + FormatToolPrompts()      → 工具 prompt (16,873 字符)       ~50% ─┤
                                                                        END
                                                                    100% ─┘ (10/12 遵从)

acp.go:
  162  buildSystemPrompt()        → prompt.Default 即全部
  228  SystemPrompt: systemPrompt
```

实验依据:`experiments/attention_position/README.md`,竞争条件下 END 10/12(83%)、BEGIN 2/12(17%)、MID 0/12(0%)。

## 目标

1. 消除 system prompt 的文本缺陷(拼接残留、悬空引用、分节失效、失真数字)。
2. 把行为约束放到 END 高遵从区,使"不推诿、不空谈"成为有效指令。
3. 修正压缩提示的误导性表述。

## 非目标

见 proposal 的 Non-goals。本设计不重构工具 prompt 全文,不接入官方 Search API,不调整模型与 compaction 阈值。

## 关键决策

### D1 尾部规则用独立文件 + `go:embed`,不用 Go 字符串常量

新增 `pkg/prompt/tail.md`,经 `//go:embed` 导出 `prompt.TailRules`。

- **理由**:与 `default.md` 同形态,可 diff review、可被完整性测试统一覆盖;Go 常量里的多行文本易与缩进/转义纠缠,正是本次 4 处污染的产生机制。
- **权衡**:多一个文件与一个 embed 变量;换来可测试性与可读性,值得。
- **否决**:写成 `const tailRules = "..."` —— 重蹈字符串拼接覆辙。

### D2 注入点选各入口的**最后一个**追加位置

- 主流程:`cmd/waveloom/main.go:288` 之后追加。
- ACP:`cmd/waveloom/acp.go:162` 之后追加(该链无后续注入,仍为末尾)。

- **理由**:TAIL 语义要求物理位置在绝对末尾。若把规则放进 `buildSystemPrompt()`,会被后续 `FormatToolPrompts()` 推到中段,立刻退回 0% 遵从区。
- **风险**:将来若新增注入方追加在尾部规则之后,规则会被推回中段且**无任何报错**。缓解:测试断言 `TailRules` 是 system prompt 的末尾内容(见测试策略 T5)。

### D3 完整性测试分两层放置

| 层 | 位置 | 职责 |
|---|---|---|
| 源文件自检 | `pkg/prompt/integrity_test.go` | 检查 `default.md` 与 `tail.md`:无拼接残留、`##` 前有空行、成本倍数可复核 |
| 跨包引用检查 | `pkg/tool/prompt_refs_test.go` | 检查工具描述中引用的 `## <标题>` 在 `prompt.Default` 中存在 |

- **理由**:`pkg/tool` 当前不 import `pkg/prompt`(`grep` 确认),反向 import 无循环依赖,可安全做交叉校验。
- **否决**:放 `cmd/waveloom` 集成层 —— 需要构造完整 CLI 上下文,慢且覆盖不全。

### D4 "禁止建议式收尾"只做提示词,不加 loop 层词面检测

- **理由**:实测近 40 个会话的最终回答中 **41% 含"建议"字样**,其中包含正常交付(如代码审查结论)。词面匹配必然误报,而误报会消耗 turn、制造干扰。
- **权衡**:约束偏软,依赖尾部位置提升遵从率。若 P1 落地后实测仍不达标,另案设计窄化检测(仅匹配"需要我…吗/要不要/由你定",且排除 plan mode),不在本提案内。

### D5 `240x` 改写而非删除

该句的约束对象是 **subagent 输出**(subagent 最终输出作为 tool_result 永久留在父上下文),约束本身有效。删除会丢掉这条约束,改写只修正失真比例:按定价表 `deepseek-flash` 输出 8.0 / 未命中 2.0 = **4x**、输出 / 缓存命中 0.04 = **200x**。

## 实现约束

- 纯提示词与文案改动,**不涉及文件系统路径操作**,三平台行为一致;`go:embed` 在 Linux/macOS/Windows 均按 `\n` 保留原始字节。
- 新文件 `tail.md` 必须为 UTF-8 无 BOM,行尾 LF(仓库 `.gitattributes` 已有约束)。
- `default.md` 的 4 处污染位于 .md 正文,修复即删除 `` ` + " `` 与 `` " + ` `` 残留、还原为普通行内代码。**必须逐个重读确认,不按行号批量替换**——行号会随前序修改漂移。
- TAIL 段是静态文本,不随时间/环境变化,不破坏前缀缓存(与 `main.go:273` "静态,不破坏前缀缓存"的既有约束一致)。
- 回归防护遵循 AGENTS.md:每个修复点上方或对应测试中标注 `REGRESSION:`,可测的写 `TestRegression_<简述>`。

## 测试策略

| 编号 | 测试 | 断言 |
|---|---|---|
| T1 | `TestRegression_PromptNoConcatResidue` | `prompt.Default` 与 `prompt.TailRules` 均不含 `` ` + " `` / `` " + ` `` |
| T2 | `TestRegression_PromptSectionBlankLine` | 每个 `##` / `###` 标题行前为空行或文档起始 |
| T3 | `TestRegression_ToolPromptReferencesExist` | 工具描述引用的每个 `## <标题>` 出现在 `prompt.Default` 中 |
| T4 | `TestRegression_CompactionNoFalseClaim` | 截断输出不含 `already processed`;含可重读提示 |
| T5 | `Test_TailRulesIsLastSection` | 组装完成后 `TailRules` 位于 system prompt 末尾 |
| T6 | `Test_TailRulesInjectedBothEntries` | 主流程与 ACP 两条组装链都包含 `TailRules` |

## 失败与降级

- 若 T5 在后续迭代中失败(尾部位置被新注入方破坏),说明新注入方必须**放在 `TailRules` 之前**,或把 `TailRules` 的注入统一收口到一个 assemble 函数。届时另案处理,不在本提案预留抽象。
- 若尾部规则与既有 loop 注入文案(`previewContinueText`、`todoLastChanceText`)冲突,以实测为准:先观察一轮,冲突明确再单独提案收敛注入文案。
- 文本类改动无运行时降级路径;测试失败即阻断合入。
