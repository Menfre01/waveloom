# Attention Position 实验

验证 System Prompt（C1）内部不同位置的指令遵从度差异。

## 实验设计

在同一份 ~4250 token 的 System Prompt 中，BEGIN、MID、END 三个位置各埋入一条调用指令，但给出互相矛盾的参数值（A/B/C）。LLM 只能选一个 — 选中最多值的位置注意力最强。

```
┌─ System Prompt ──────────────────────────────────────┐
│ [角色定义]                                            │
│ IMPORTANT: rule='A'   ← BEGIN                       │
│                                                      │
│ ~1500 token filler（编码规范、工具参考、发布流程…）    │
│                                                      │
│ IMPORTANT: rule='B'   ← MID（嵌入 filler 之间）       │
│                                                      │
│ ~1500 token filler（代理类型、并行策略、计划模式…）    │
│                                                      │
│ IMPORTANT: rule='C'   ← END                          │
└──────────────────────────────────────────────────────┘
```

包含控制组：每个位置单独测试（无竞争），确认 LLM 能独立遵从。

## 结果

4 轮共 12 次试验：

```
位置    | 胜出次数 | 胜率  | 模式
--------|---------|-------|-------------------
END     | 10/12   | 83%   | 近因效应 (recency)
BEGIN   |  2/12   | 17%   | 偶尔胜出，不稳定
MID     |  0/12   | 0%    | 完全被忽略

控制组（无竞争）:
  BEGIN only: ✅   MID only: ✅   END only: ✅

结论:
  → END 位置（最接近 messages）注意力最强 — 近因效应
  → MID 在竞争中被完全忽略（lost in the middle）
  → MUST 级规则应放 C1 开头或结尾，不能埋在中部

## 先决条件

- Go 1.25+
- DeepSeek API Key

## 运行

```sh
export DEEPSEEK_API_KEY=sk-...
go run ./experiments/attention_position/
```

## 对规格书的影响

C1 System Prompt 的注意力分布不均匀：
- 开头的规则遵从概率最高
- 中部的规则在存在冲突时可能被忽略
- MUST 级规则应放在 C1 开头，不能埋在中间

参见 `specs/prompt-architecture.md`。
