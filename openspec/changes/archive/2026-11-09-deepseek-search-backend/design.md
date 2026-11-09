# Design: deepseek-search-backend

## 上下文

`web_search` / `web_fetch` 目前是零值结构体(仅测试用字段),凭据靠 `os.Getenv("BRAVE_API_KEY")` 在 `Execute` 内现读。三处生产构造点(`main.go:415`、`acp.go:302`、`subagent/agent.go:735`)都是 `tool.Wrap(&tool.WebSearch{})`,拿不到 settings.json 里的 LLM 凭据。

`ThrottleStore` 按 host 分片(Reserve / ReportRateLimited / ReportBlocked / ReportSuccess),天然适配"多后端各自退避"。

官方端点与 LLM 同域同 key:

```
POST {base_url}/v1/tools/search   {"query","count","need_content"}
POST {base_url}/v1/tools/fetch    {"url"}
```

## 关键决策

### D1 凭据用函数式 provider,在 Execute 时读取

`WebSearch` / `WebFetch` 各增一个 `credentials func() (apiKey, baseURL string, ok bool)`;`nil` 表示不启用官方后端。

- **理由**:`/provider` 支持运行时切换,Brave 的 `os.Getenv` 也是每次现读。静态注入 key 会在切换后过期,且把密钥长期驻留结构体。
- **权衡**:tool 层多一个 nil 可空字段;换取切换即时生效与密钥不驻留。
- **否决**:构造时传 `apiKey string` —— 切换 provider 后需重建整个 registry。

### D2 后端 auto 选择,首版不增配置开关

有 DeepSeek 凭据即官方优先,否则直接走本地。

- **理由**:用户在提案评审时已明确选择"官方优先 + 本地兜底";"优先+降级"本身已是安全默认。多一个开关就多一个配置错误面。
- **风险**:想省钱的用户无法关闭。缓解:402 自动会话级禁用、输出标明付费来源、成本量级极小(≈1/4 次 LLM 调用)。
- **后续**:若实际出现成本抱怨,再增 `tools.search_backend: auto|local`,不在本 change 预留。

### D3 降级链与 ThrottleStore 分片

官方 host 与 `html.duckduckgo.com` / `api.search.brave.com` 各自独立退避,互不污染。

| 官方状态 | 处理 |
|---|---|
| 200 有 results | 返回官方结果 |
| 200 空 results | 视为无匹配,**不回退本地**(避免双倍成本),输出说明 |
| 402 余额不足 | 降级本地 + 会话级禁用官方(后续调用直接本地) |
| 429 限流 | `ReportRateLimited` 后降级本地 |
| 403 fetch 安全策略拒绝 | 降级本地 fetch |
| 404 fetch 无可用内容 | 降级本地 fetch |
| 5xx / 超时 / 连接错误 | 降级本地,不禁用 |

- **理由**:402 是持续状态(充值前不会自愈),重试纯浪费;429 是瞬时状态,靠退避窗口。
- **权衡**:会话级禁用状态需在 `WebSearch` 实例上存,不跨进程持久。可接受——重启即重新探测。

### D4 summary 体积控制

只对 `rerank_score >= 1.5` 的**前 3 条**附 `summary`,每条截断至 400 字;其余条目仅 `snippet`。

- **理由**:官方说 `summary` 600–800 字"intended to be read by a model",但 10 条全附 ≈ 8K 字,会挤占上下文且多数条目不被引用。`rerank_score >= 1.5` 是官方定义的高质量阈值。
- **权衡**:模型可能想读第 4 条以后的长摘要——仍可通过 `web_fetch` 取全文。
- **否决**:全量附 summary —— 上下文成本远超收益。

### D5 扩展 `SearchResult`,不引入新类型

加 `Score float64`、`Authority int`、`PublishedAt string`、`Summary string`,零值表示信号缺失;本地后端不填,格式化时跳过零值。

- **理由**:多一层类型转换换来的是零收益;`SearchResult` 是包内类型,无外部兼容负担。
- **风险**:`Authority` 的零值(0)与"权威度 0"(官方明确 0 是合法值)冲突。用 `AuthoritySet bool` 或 `Authority int` + `-1` 表示未知(官方定义 `-1` = unknown)。**采用后者**:与官方语义一致。

### D6 URL 校验前置单点

`validateRequestURL` 在两个后端**之前**统一调用,不复制到各后端。

- **理由**:安全校验必须是前置单点,复制到每个后端就多一处可能被遗漏或写错的实现。
- **否决**:各后端自行校验 —— 新增后端时必须记得加,是典型的回归缺口。

## 测试策略

| 编号 | 测试 | 断言 |
|---|---|---|
| T1 | 官方后端结果映射 | `rerank_score`/`authority`/`date`/`summary` 正确映射,`date` 异常格式不 panic |
| T2 | 降级链 | 402/429/500/超时各自落到本地后端,且 402 后第二次调用不再请求官方 |
| T3 | 空结果不回退 | 官方 200 + 空 results 时不产生本地请求 |
| T4 | 本地结果不伪造信号 | 本地后端结果 `Authority = -1` 且格式化不含分数 |
| T5 | URL 安全跨后端 | 私网地址、非 HTTP(S) 在两个后端都被拒绝且不发出请求 |
| T6 | summary 体积 | 10 条结果中只有符合条件的前 3 条带 summary,总输出长度受限 |
| T7 | 凭据缺失 | provider 返回 ok=false 时零官方请求 |

## 失败与降级

- 官方响应字段缺失或类型异常 → 防御性解析(缺失即零值),不因单条脏数据失败整次搜索。
- 官方 200 但 `results` 非数组 → 当作服务异常处理,降级本地。
- 本地后端也失败 → 返回合并错误,消息中包含官方失败原因(便于判断是否凭据问题)。
- 无降级路径可走的场景不存在:本地 DuckDuckGo 始终可用(仅受网络与限流影响)。

## 跨平台

纯 HTTP 调用,无文件系统与路径操作,三平台一致。测试用 `httptest` 注入 base URL,不依赖外网。
