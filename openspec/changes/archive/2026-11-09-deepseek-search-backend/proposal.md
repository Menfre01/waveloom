# Proposal: deepseek-search-backend

## Why

DeepSeek 于 **2026-10-17** 开放自研搜索引擎的独立 REST 端点(官方 Change Log 原文:"Opening up the in-house search API"):

- `POST https://api.deepseek.com/v1/tools/search`
- `POST https://api.deepseek.com/v1/tools/fetch`

官方描述该引擎"indexes hundreds of billions of web pages, supports multilingual search... filters SEO spam, GEO manipulation, and low-quality AIGC"。返回字段包含 `rerank_score`(0–3,≥1.5 优质)、`authority`(0–4,≥3 权威)、`date`、`summary`(600–800 字,明确标注"intended to be read by a model")。

我方现状与差距:

| 维度 | 现状 | 官方 API |
|---|---|---|
| 后端 | DuckDuckGo HTML 抓取(`web_search.go:279`),或 Brave(`:518`,需 `BRAVE_API_KEY`) | 自研引擎,服务端重排 |
| 重排信号 | 无,依赖 HTML 结构与顺序 | `rerank_score` + `authority` + `date` |
| 失败表现 | 抓取页面结构变动即解析失败;本次会话实测 `html.duckduckgo.com` **TCP timeout 直接报错** | HTTP 状态码语义明确(402/429/5xx),`results` 空数组表示无匹配 |
| 抓取结果 | HTML → 文本抽取,需自行清理 | `/v1/tools/fetch` 直接返回 Markdown |
| 计费 | 免费(不可控) | $0.75 / 1000 次成功请求,失败不计费 |
| QPS | 未知,靠退避猜 | Search 100 / Fetch 25,账号级 |

v0.9.0 曾调整 web_search,但那次针对的是 **Responses API 内置 web_search 被官方回收**(兼容性明细列为"忽略"),与本次开放的**独立 REST 端点**是两件不同的事。当前代码对 `tools/search` / `tools/fetch` **零引用**(`grep` 无匹配)。

## What Changes

### 用户可感知

- **搜索结果带可信度信号**:模型拿到的不再只是 title/URL/snippet,还包含重排分数、权威度与发布时间,能判断"这条来源是否值得引用"。
- **搜索失败时不再干等或直接报错**:官方端点不可用(无 key / 余额不足 / 限流 / 超时)时自动回落现有 DuckDuckGo / Brave,搜索能力不会因单一后端故障而整体失效。
- **引入付费调用**:每次成功搜索/抓取约 $0.00075(≈0.0054 元),失败不计费。会话统计与日志需体现是否走了付费后端,避免成本黑箱。
- **抓取内容更干净**:`/v1/tools/fetch` 直接返回 Markdown,省掉 HTML→文本的启发式抽取。

### 内部

- `WebSearch` / `WebFetch` 增加后端选择与凭据注入能力,现有零值构造方式保留(不注入时行为不变)。
- `SearchResult` 扩展字段,承载重排分数、权威度、发布时间与长摘要。
- 三处构造点(`main.go:415`、`acp.go:302`、`subagent/agent.go:735`)注入凭据提供者;测试构造点不需要。
- 降级链与 `ThrottleStore` 按 host 分片的限流模型衔接:官方端点与 DuckDuckGo 各自独立退避,互不污染。

## Capabilities

### New Capabilities

- `tool/web-search-backend` — 搜索后端的选择优先级、降级链、结果字段映射、限流与计费可见性。
- `tool/web-fetch-backend` — 抓取后端的选择优先级、降级链、内容类型与体积校验。

### Modified Capabilities

无。`openspec/specs/` 现有 3 个 capability 均与提示词/压缩相关,不涉及工具后端。

## Non-goals

- **不改 Responses API 的内置 web_search 处理**:那条路径(历史 `web_search_call` 回传)保持现状,与本次独立端点无关。
- **不改 `ThrottleStore` 的退避算法**:已有指数退避、Retry-After、403 封锁冷却经实测有效,本次只是把新 host 接进去。
- **不做搜索结果缓存**:跨轮复用会引入过期判断与语义匹配成本,另行评估。
- **首版不实现 `ori_question` / `localization` 参数**:前者需要原始提问上下文(当前工具入参只有 query),后者按 IP 本地化对编码场景意义有限。
- **不放松 `web_fetch` 的 URL 安全校验**:现有 SSRF 防护(`validateRequestURL`、私网地址拒绝)在官方后端路径上同样保留,不因换后端而放行。
- **不做跨 provider 搜索**:官方端点只接受 DeepSeek API key。使用 Kimi/OpenAI 作为 LLM provider 的会话,搜索仍走本地后端。

## Impact

### 受影响文件

| 文件 | 变更 |
|------|------|
| `pkg/tool/web_search.go` | 新增官方后端实现、后端选择与降级、`SearchResult` 字段扩展、输出格式化 |
| `pkg/tool/web_fetch.go` | 新增官方 fetch 后端、降级链、保留 URL 校验与体积限制 |
| `pkg/tool/web_search_prompt.md` | 描述更新(后端顺序、可信度字段含义) |
| `cmd/waveloom/main.go` | 构造点注入凭据提供者 |
| `cmd/waveloom/acp.go` | 同上 |
| `pkg/subagent/agent.go` | 同上 |
| `pkg/tool/web_search_test.go` | 新增官方后端单测、降级链测试、结果映射测试 |

### 配置

复用现有 `llm.api_key` 与 `llm.base_url`(官方搜索端点与 LLM 同域同 key),**不新增必填配置**。是否需要显式开关见 design 的 D2。

### 成本

| 场景 | 估算 |
|---|---|
| 单次搜索 | $0.00075 ≈ 0.0054 元 |
| 单次抓取 | $0.00075 ≈ 0.0054 元 |
| 典型会话(5 次搜索 + 3 次抓取) | ≈ 0.043 元 |
| 对比一次 flash LLM 调用(10K 未命中输入) | ≈ 0.02 元 |

搜索成本约为一次 LLM 调用的 1/4;会话内搜索次数通常是个位数,总量可控。

### 风险

- **端点稳定性未知**:开放仅 3 周,长尾故障模式(如 `results` 字段缺失、`date` 格式异常)需防御性解析。
- **付费端点误用**:若模型在一次 turn 内反复搜索,成本会累积。需在输出中回传"本次走了付费后端"的信号,让模型与用户都能看见。
- **`summary` 体积**:10 条 × 600–800 字 ≈ 8K 字,直接全量注入会挤占上下文。输出策略见 design 的 D4。
- **跨 provider 不可用**:非 DeepSeek provider 的会话无法使用该端点,降级路径必须无感。
