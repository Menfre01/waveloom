# Tool Cache 实验

验证 DeepSeek API 中 tools 字段的两个关键行为：

| 问题 | 结论 |
|------|------|
| tools 是否计入 input tokens？ | ✅ 是，全额计费 |
| tools 是否参与前缀缓存？ | ✅ 是，与 system prompt 构成同一缓存前缀 |

## 先决条件

- Go 1.25+
- DeepSeek API Key（环境变量 `DEEPSEEK_API_KEY` 或 `~/.waveloom/settings.json`）

## 运行

```sh
# 方式 1: 环境变量
export DEEPSEEK_API_KEY=sk-...
go run ./experiments/tool_cache/

# 方式 2: 使用 Waveloom 已有配置
go run ./experiments/tool_cache/
```

## 预期输出

```
══════════════════════════════════════════════════════════════
实验 1: tools 是否计入 input tokens
══════════════════════════════════════════════════════════════

► Call A: 不带 tools
  [A] prompt_tokens=20  ...
► Call B: 带 2 个 tools
  [B] prompt_tokens=~400  ...
  差值: ~380 tokens
  ✅ 结论: tools 计入 input tokens

══════════════════════════════════════════════════════════════
实验 2: tools 是否参与前缀缓存
══════════════════════════════════════════════════════════════

► Call 1: 建立缓存前缀 [sys][user][2 tools]
  [1] prompt_tokens=~400  cache_hit=0  cache_miss=~400
► Call 2: 相同前缀 + 追加新消息
  [2] cache_hit=~400  cache_miss=~40
  命中率: ~90%
  ✅ 结论: tools 参与前缀缓存
```

## 注意

- 实验 1 使用不同的用户消息避免缓存共享混淆结果
- 两次调用之间有 1 秒间隔等待缓存落盘
- 如果短时间内重复运行，前一次缓存的残留可能导致实验 2 Call 1 出现非零 cache_hit
- 使用 `deepseek-v4-flash` 以降低实验成本

## 实验方法

详细设计参见 `main.go` 头部的注释。
