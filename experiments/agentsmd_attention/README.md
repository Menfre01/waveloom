# AGENTS.md 注意力位置实验

验证 AGENTS.md（messages[1], role=user）内部不同位置的指令遵从度差异，并与 C1 System Prompt 内部注意力分布对比。

## 实验设计

```
┌─ messages[0] (system) ────────────────────────────────┐
│ [角色定义]                                              │
│ 中性 System Prompt — 不含 select_rule 指令              │
└───────────────────────────────────────────────────────┘

┌─ messages[1] (user) — AGENTS.md ──────────────────────┐
│ IMPORTANT: rule='A'   ← BEGIN                        │
│                                                       │
│ ~1500 token filler（构建命令、测试规范、编码规则…）     │
│                                                       │
│ IMPORTANT: rule='B'   ← MID（嵌入 filler 之间）        │
│                                                       │
│ ~1500 token filler（代理规则、发布流程…）               │
│                                                       │
│ IMPORTANT: rule='C'   ← END                           │
└───────────────────────────────────────────────────────┘

┌─ messages[2] (user) ──────────────────────────────────┐
│ "Call select_rule with the correct rule value."       │
└───────────────────────────────────────────────────────┘
```

## 对比维度

| 维度 | C1 实验 (attention_position) | AGENTS.md 实验 (本实验) |
|------|------------------------------|------------------------|
| 测试目标 | C1 System Prompt（messages[0]） | AGENTS.md（messages[1]） |
| 角色 | system | user |
| 竞争指令位置 | C1 内部的 BEGIN/MID/END | AGENTS.md 内部的 BEGIN/MID/END |
| System Prompt | 含竞争指令 | **中性**（不含 select_rule 指令） |
| 回答假设 | 近因效应主导 C1 内部注意力 | 近因效应是否同样主导 user-role 消息内部注意力 |

## 假设

- H1: AGENTS.md 也呈现近因效应（END > BEGIN > MID）
- H2: 近因效应强度可能与 C1 不同（user role vs system role 的注意力权重可能不同）
- H3: AGENTS.md END（紧接用户查询）可能获得比 C1 END 更强的近因优势

## 先决条件

- Go 1.25+
- DeepSeek API Key

## 运行

```sh
export DEEPSEEK_API_KEY=sk-...
go run ./experiments/agentsmd_attention/
```

## 结果

待运行。
