# Tasks: deepseek-search-backend

## Wave 1 — 搜索后端(仅 `pkg/tool/web_search.go`)

### 1.1 实现官方搜索后端与降级链

- [x] `SearchResult` 扩展 `Score` / `Authority` / `PublishedAt` / `Summary`(`Authority` 用 `AuthorityUnknown = -1` 表示未知,与官方 `-1 = unknown` 语义一致,不用 0)
- [x] `WebSearch` 增 `credentials SearchCredentials` 与 `officialSuspendedUntil atomic.Int64`(nil / 0 时行为不变)
- [x] `searchOfficial`:POST `{base_url}/v1/tools/search`,body 只传 `query` / `count` / `need_content=false`;`Date` / `Authority` 用指针区分"缺失"与"合法零值";`date` 非法格式置空不 panic;`results` 非数组按服务异常降级
- [x] `Execute` 后端选择:有凭据且未在冷却期 → 官方;否则本地(保留 Brave → DuckDuckGo)
- [x] 降级按 D3:200 空结果不回退;402 进冷却窗口并降级;429 走 `ReportRateLimited` 后降级;5xx / 超时 / 连接错误降级不禁用
- [x] 输出:本地 `Authority = AuthorityUnknown` 且不输出分数;官方输出 `score` / `authority` / `date`;`summary` 只给 `score >= 1.5` 的前 3 条并截断 400 字
- [x] 输出标注实际后端(`DeepSeek Search` / `Brave Search` / `DuckDuckGo`)
- [x] 验证:`make build`

### 1.2 搜索后端测试

`pkg/tool/web_search_official_test.go`(不改既有用例):

- [x] T1 结果映射:字段正确落入;非法 `date` 不 panic 且不透出脏值;`authority: 0` 原样输出
- [x] T2 降级链:402 / 429 / 500 各自降级,且不向调用方暴露错误;每 case 断言 `officialHits == 1`
- [x] T2 续 402:第二次调用不再命中官方
- [x] T3 空结果不回退:官方 200 + `results: []` 时本地零请求
- [x] T4 本地不伪造:`Authority == AuthorityUnknown`,输出不含 score/authority/Summary
- [x] T6 summary 配额:10 条中只有前 3 条达到阈值并带 summary,低分条目不出现
- [x] T7 凭据缺失:nil / `ok=false` / 空 key 三种形态下官方零命中
- [x] 官方不可达(连接失败)降级
- [x] 验证:`go test ./pkg/tool/ -run "TestWebSearchOfficial|TestWebSearchLocal"`

## Wave 2 — 抓取后端(仅 `pkg/tool/web_fetch.go`)

### 2.1 实现官方抓取后端

- [x] `WebFetch` 增同形态 `credentials` 与 `officialSuspendedUntil`
- [x] `fetchOfficial`:POST `{base_url}/v1/tools/fetch`,解析 `title` / `content`(已是 Markdown)
- [x] URL 校验前置单点:官方尝试插在 Step 1 校验与本地限流之后,`validateRequestURL` 不复制
- [x] 降级:403 / 404 / 429 / 5xx / 超时 / 空 content → 本地
- [x] 体积上限沿用本地实现,超限截断并告知
- [x] 本地兜底时输出附 `[fallback]` 说明官方为何未生效
- [x] 验证:`make build`

### 2.2 抓取后端测试

- [x] 官方成功:返回 Markdown + 标题,本地零命中
- [x] 404 / 403 / 空 content 三种情况降级本地
- [x] T5 URL 安全跨后端:`127.0.0.1` / `localhost` / `10.0.0.1` / `file://` 在两个后端都零请求
- [x] 无凭据时零官方请求
- [x] 验证:`go test ./pkg/tool/ -run "TestWebFetchOfficial|TestWebFetchURLGuard"`

## Wave 3 — 凭据注入与文案

- [x] `pkg/tool`:导出 `OfficialCredentials(load func() (apiKey, provider, baseURL string)) SearchCredentials` 与 `WithCredentials`(链式构造,字段保持私有)
- [x] `cmd/waveloom/main.go`:构造点注入,非 deepseek 或无 key 时返回不可用
- [x] `cmd/waveloom/acp.go`:同上
- [x] `pkg/subagent/agent.go`:同上(`a.Settings.LoadLLM()`)
- [x] `pkg/tool/web_search_prompt.md`:后端顺序、可信度字段含义、`[fallback]` 语义
- [x] `WebSearch.Description()` 同步(原描述只列 DuckDuckGo/Brave)
- [x] 验证:`make build`

## Wave 3.5 — 代码审查发现项

审查结论:计费与并发无实质问题;降级链在**超时路径**上不成立(已被测试复现),另有 base URL 归一化缺失。

- [x] 🔴 **超时路径降级失效** — 官方尝试与本地共用 `reqCtx`,官方超时耗尽整笔预算,本地接手时 ctx 已过期,返回 `search timed out` 而非降级结果,直接违反 spec「超时 → MUST 在同一调用内降级」。修法:官方尝试改用独立预算 `timeout/2`,任何 timeout 下都留出本地余量。回归防护:`TestWebSearchOfficial_TimeoutStillFallsBack`
- [x] 🔴 **base URL 尾部 `/v1` 未归一化** — 用户按 OpenAI 惯例填 `https://api.deepseek.com/v1` 会拼出 `/v1/v1/tools/search` 永久 404,而 404 不进 `ThrottleStore`,每次调用白发一次请求。修法:新增 `normalizeOfficialBaseURL`,与 `pkg/llm` 的 `normalizeDeepSeekBaseURL` 保持一致。回归防护:`TestWebSearchOfficial_BaseURLWithV1Suffix`
- [x] 🟡 **402 永不复位** — 原实现"会话级禁用",用户充值后必须重启才能恢复。改为 **5 分钟冷却窗口**(`officialSuspendCooldown`):窗口内直接走本地,窗口后重新探测一次,成本至多多一次探测请求
- [x] 🟡 **`Description()` 未提官方/付费后端** — 已改为按优先级列出三个后端并标注 DeepSeek 为 paid,与 prompt.md 及 spec「付费可观测」对齐
- [x] 🟡 **测试未断言计费泄漏** — `FallbackChain` 与 `NoContentFallsBack` 采集了 `officialHits` 却从不断言;已补 `officialHits == 1`
- [x] 🟡 **`OfficialCredentials` 零覆盖** — 补 `TestOfficialCredentials_ProviderFilter`(deepseek / 空 provider / kimi / openai / 空 key 五形态)
- [x] 🟡 **测试缺超时用例** — 正是这个缺口掩盖了 🔴1;已补,并加兜底 timer 避免 `httptest.Server.Close` 与 handler 死锁
- [x] 🟡 **`credibilityLine` 恒输出 score** — 经核对官方 API Reference,`rerank_score` 是 **required** 字段(0–3),故分数恒输出是正确行为;已加注释说明,避免后人误改为"缺失即省略"
- [x] 🟡 **本地也失败时未合并官方失败原因** — 初判"记录理由不修",后续真机验证推翻该判断:**后果比预判严重**。用 mock 同时扮演 LLM 与官方端点(402)+ 真实 DDG(本机不可达)复现:tool_result 只剩 `DuckDuckGo timeout`,完全看不到 402,用户会把"余额不足"误诊为网络问题。已修:`web_search` 5 条失败 return、`web_fetch` 6 处统一经 `localFail` 包装,同时写入 `Content`(给模型读)与 `Error.Message`(给程序判断)。回归防护:`TestWebSearchOfficial_LocalFailureCarriesOfficialReason`。修复后同一场景输出同时含 DDG timeout 与 `[official-backend] ... (HTTP 402)`

### 审查确认无问题项

- **计费**:两个工具单次调用至多命中官方端点 1 次;`web_fetch` 的 retry 循环只在本地分支内,不重入官方;DDG 内部重试只碰 DDG
- **并发**:`credentials` 仅构造期写、`officialSuspendedUntil` 纯原子、无读-改-写;`ThrottleStore` 有 RWMutex;`-race` 通过
- **SSRF**:官方端点固定,用户 target URL 经 `validateRequestURL` 后放进 body;我方不连接目标,Authorization 只发往 baseURL,无凭据外泄。这是策略闸门而非防线,真正的解析面在官方服务端

## Wave 4 — 验收

- [x] `make build && make test && make lint` 全绿(build ✓;全量包测试无失败 ✓;golangci-lint 0 issues)
- [x] `openspec validate deepseek-search-backend --strict` 通过
- [x] **真实调用验证**(2026-11-09,TUI 重启后实测):`web_search` → `(DeepSeek Search)` 417ms,5 条结果全部带 `score`/`authority`/日期;前 3 条(score ≥1.5)带 Summary、第 4/5 条无 Summary,配额策略按预期生效。`web_fetch` → `(DeepSeek Fetch)` 233ms,`text/markdown` 3.9KB,标题解析正确、内容为干净 Markdown
- [x] 降级验证:6 条失败路径由单测覆盖(402/429/500/超时/不可达/空结果);真实环境未复现 —— 需临时改 base_url,而 settings.json 在沙箱内不可写,留待后续
- [x] 成本核对:官方中文文档标注 **5 元 / 千次**(英文文档 $0.75/1000,按 7.2 汇率折算一致),单次 ≈ 0.005 元,与 proposal 估算(0.0054)吻合,无需修正
- [x] 归档前确认遗留项说明:所有审查项均已处理,无悬空任务;唯一"环境未复现"的是真实环境降级(settings.json 沙箱内不可写),已由 6 条单测 + 1 次 mock 全链路真机验证覆盖

## 约束提醒

- 禁止直接 `go build`,统一 `make build`
- 三处构造点改动后运行中的 TUI 不会热重载,需重启验证
- 不自动提交;等待用户明确指令
- 付费端点:新增调用路径必须被输出里的后端标注覆盖,不得出现"走了付费但没标"
