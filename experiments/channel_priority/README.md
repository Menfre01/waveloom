# Channel Priority 实验

验证 C1 (System Prompt)、C2 (Tool Definitions)、C3a (AGENTS.md) 三个静态通道之间的指令优先级。

## 实验设计

在 C1、C2、C3a 中植入互相矛盾的 `select_rule` 指令，观察 LLM 最终遵从哪个通道。

```
Token 序列: [C1][C2][C3a][C3b 查询]

C1 (system):  IMPORTANT: rule='A'
C2 (tools):   select_rule — IMPORTANT: rule='B'
C3a (user):   IMPORTANT: rule='C'
```

## 场景

| 实验 | 对抗组合 | 关键问题 |
|------|---------|---------|
| 1 | C1 vs C3a | system role vs 近因 |
| 2 | C1 vs C2 vs C3a | 三通道优先级排序 |

## 结果

| 对抗组合 | 胜出 | 机制 |
|---------|------|------|
| C1 vs C3a | C3a 100% | 近因效应 > system role |
| C1 vs C2 vs C3a | C3a 77%、C2 13%、C1 10% | 近因为主，C2 是夹层通道 |

## 关键发现

- "system prompt wins when they conflict with AGENTS.md" 被实验证伪
- C3a END 是静态通道中最强的注意力位置
- C2 夹在 C1 和 C3a 之间，是注意力黑洞

## 先决条件

- Go 1.25+
- DeepSeek API Key

## 运行

```sh
export DEEPSEEK_API_KEY=sk-...
go run ./experiments/channel_priority/
```
