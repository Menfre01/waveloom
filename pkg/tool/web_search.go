package tool

import (
	_ "embed"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/html"
)
//go:embed web_search_prompt.md
var webSearchPrompt string

// ---------------------------------------------------------------------------
// WebSearch — 搜索引擎查询
// ---------------------------------------------------------------------------

const (
	DefaultWebSearchMaxResults = 10
	MaxWebSearchMaxResults     = 20
	DefaultWebSearchTimeoutMs  = 45000  // 45s
	MaxWebSearchTimeoutMs      = 120000 // 120s
)

// AuthorityUnknown 表示结果未携带权威度信号。
// 官方语义:authority 取值 -1(unknown) 或 0–4,其中 0 是合法等级,
// 因此不能用零值表示"未知",必须用 -1 哨兵。
const AuthorityUnknown = -1

// SearchCredentials 返回官方搜索/抓取后端所需的凭据。
// ok=false 表示当前 provider 不提供该后端(如使用 Kimi/OpenAI),
// 调用方应直接走本地后端,不得发出任何官方端点请求。
//
// 设计为函数而非静态值:provider 支持运行时切换,每次 Execute 现读
// 可使切换即时生效,且 API key 不长期驻留在工具结构体里。
type SearchCredentials func() (apiKey, baseURL string, ok bool)

// OfficialCredentials 用调用方提供的 LLM 设置快照构造官方后端凭据。
// load 每次 Execute 调用一次,使 provider 运行时切换即时生效(design D1)。
//
// 仅当 provider 为 deepseek(空值视为默认 deepseek)且 API key 非空时可用;
// 其他 provider 无法使用 DeepSeek 自研搜索端点,直接返回不可用以走本地后端。
// base URL 由 load 提供:若指向自建网关且无该端点,请求会失败并自动降级。
func OfficialCredentials(load func() (apiKey, provider, baseURL string)) SearchCredentials {
	return func() (string, string, bool) {
		apiKey, provider, baseURL := load()
		if apiKey == "" {
			return "", "", false
		}
		if provider != "" && provider != "deepseek" {
			return "", "", false
		}
		return apiKey, baseURL, true
	}
}

type WebSearchParams struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"` // 返回结果数（可选，默认 10，最大 20）
	TimeoutMs  int    `json:"timeout_ms"`  // 超时时间（毫秒，可选，默认 45000，最大 120000）
}

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`

	// 以下字段仅官方后端填充,本地后端保持零值(Authority 例外,用
	// AuthorityUnknown 显式标注"信号不可用",避免与合法的 0 级混淆)。
	Score       float64 `json:"score,omitempty"`        // 重排分数 0–3,≥1.5 为官方定义的高质量结果
	Authority   int     `json:"authority,omitempty"`    // 权威度:AuthorityUnknown 或 0–4
	PublishedAt string  `json:"published_at,omitempty"` // RFC3339;空表示未知
	Summary     string  `json:"summary,omitempty"`      // 长摘要,按 D4 仅对高分前若干条填充
}

type WebSearch struct {
	httpClient      *http.Client // 可注入的 HTTP 客户端;nil 使用默认
	braveBaseURL    string       // 测试用 Brave API URL 覆盖;空则用常量
	ddgBaseURL      string       // 测试用 DDG URL 覆盖;空则用常量
	officialBaseURL string       // 测试用官方端点覆盖;空则用凭据返回的 base URL

	// credentials 为 nil 时不启用官方后端,行为与改动前完全一致。
	credentials SearchCredentials

	// officialSuspendedUntil 是官方后端的禁用截止时间(UnixNano,0 = 未禁用)。
	// WebSearch 是 ConcurrentSafe 的,必须原子访问。
	officialSuspendedUntil atomic.Int64
}

// WithCredentials 注入官方后端凭据;nil 表示不启用官方后端。
// 返回自身以便链式构造:tool.Wrap((&tool.WebSearch{}).WithCredentials(creds))。
func (t *WebSearch) WithCredentials(creds SearchCredentials) *WebSearch {
	t.credentials = creds
	return t
}

func (t *WebSearch) Name() string            { return "web_search" }

var webSearchSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "Search query — keywords, natural language, or technical terms",
      "minLength": 1
    },
    "max_results": {
      "type": "integer",
      "description": "Maximum number of results to return (default: 10, max: 20)"
    },
    "timeout_ms": {
      "type": "integer",
      "description": "Timeout in milliseconds (optional, default: 45000, max: 120000)"
    }
  },
  "required": ["query"]
}`)

func (t *WebSearch) Schema() json.RawMessage { return webSearchSchema }
func (t *WebSearch) ConcurrentSafe() bool    { return true }

func (t *WebSearch) Description() string {
	return "Search the web and return results (title/URL/snippet; the DeepSeek backend also returns relevance score and authority). Backends by priority: DeepSeek Search (paid, when the provider is deepseek), Brave (BRAVE_API_KEY), DuckDuckGo. Rules: see system prompt ## Information Sources."
}

// Prompt 返回 web_search 使用指南和跨工具引用，由 Registry.FormatToolPrompts() 注入 C1。
// Prompt 返回使用指南，由 Registry.FormatToolPrompts() 注入 system prompt。
func (t *WebSearch) Prompt() string { return webSearchPrompt }

func (t *WebSearch) client() *http.Client {
	if t.httpClient != nil {
		return t.httpClient
	}
	// 复用 web_fetch 的 SSRF 安全客户端（共享 Dialer + 重定向校验）
	return &http.Client{
		Timeout: time.Duration(MaxWebSearchTimeoutMs) * time.Millisecond,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: 10 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return validateRequestURL(req.URL)
		},
	}
}

// ---------------------------------------------------------------------------
// 官方搜索后端(DeepSeek 自研搜索引擎,2026-10-17 开放)
// ---------------------------------------------------------------------------

const (
	// officialSearchHost 是官方端点的 host,用于 ThrottleStore 分片。
	officialSearchHost = "api.deepseek.com"
	// officialSearchPath 是官方搜索端点路径(相对 base URL)。
	officialSearchPath = "/v1/tools/search"
	// officialDefaultBaseURL 是凭据未提供 base URL 时的兜底。
	officialDefaultBaseURL = "https://api.deepseek.com"

	// officialSummaryMinScore 是官方定义的高质量阈值(≥1.5 视为高质量结果)。
	officialSummaryMinScore = 1.5
	// officialSummaryTopN 是附长摘要的条数上限(design D4:控制上下文体积)。
	officialSummaryTopN = 3
	// officialSummaryMaxRunes 是单条长摘要的截断上限。
	officialSummaryMaxRunes = 400
	// officialMaxResponseBytes 限制官方响应体读取量,防异常膨胀。
	officialMaxResponseBytes = 4 << 20

	// officialSuspendCooldown 是 402(余额不足)后的禁用冷却窗口。
	// 充值不会通知本进程,永久禁用会让用户必须重启才能恢复;冷却窗口在
	// 成本(每次窗口后至多一次探测请求)与恢复速度之间取折中。
	officialSuspendCooldown = 5 * time.Minute
)

// officialSearchResponse 是官方搜索端点的响应结构。
// Date/Authority 用指针以区分"字段缺失"与"合法零值" —— authority 的 0
// 是有效等级(0–4),不能与"未知"混为一谈。
type officialSearchResponse struct {
	Results []struct {
		RerankScore float64 `json:"rerank_score"`
		Title       string  `json:"title"`
		URL         string  `json:"url"`
		Snippet     string  `json:"snippet"`
		Summary     string  `json:"summary"`
		Date        *string `json:"date"`
		Authority   *int    `json:"authority"`
	} `json:"results"`
}

// officialSearchError 携带官方端点的 HTTP 状态,供调用方按 design D3 的表分类。
// status 为 0 表示网络层失败(连接/DNS/超时),不代表服务端状态。
type officialSearchError struct {
	status int
	detail string
}

func (e *officialSearchError) Error() string {
	if e.status == 0 {
		return "official search backend unreachable: " + e.detail
	}
	return fmt.Sprintf("official search backend HTTP %d: %s", e.status, e.detail)
}

// officialCredentials 返回官方后端凭据;未配置时 ok=false。
func (t *WebSearch) officialCredentials() (apiKey, baseURL string, ok bool) {
	if t.credentials == nil {
		return "", "", false
	}
	apiKey, baseURL, ok = t.credentials()
	if !ok || apiKey == "" {
		return "", "", false
	}
	if baseURL == "" {
		baseURL = officialDefaultBaseURL
	}
	if t.officialBaseURL != "" {
		baseURL = t.officialBaseURL
	}
	return apiKey, baseURL, true
}

// officialCoolingDown 判断官方后端是否处于 402 冷却窗口内。
// 窗口内直接走本地后端,不发任何官方请求。
func (t *WebSearch) officialCoolingDown() bool {
	until := t.officialSuspendedUntil.Load()
	return until != 0 && time.Now().UnixNano() < until
}

// searchOfficial 调用官方自研搜索引擎。
// body 只传 query/count/need_content —— ori_question 与 localization 属 Non-goals。
func (t *WebSearch) searchOfficial(ctx context.Context, baseURL, apiKey, query string, maxResults int) ([]SearchResult, error) {
	payload, err := json.Marshal(map[string]any{
		"query":        query,
		"count":        maxResults,
		"need_content": false,
	})
	if err != nil {
		return nil, &officialSearchError{detail: "encode request: " + err.Error()}
	}

	endpoint := normalizeOfficialBaseURL(baseURL) + officialSearchPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, &officialSearchError{detail: "build request: " + err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := t.client().Do(req)
	if err != nil {
		return nil, &officialSearchError{detail: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, &officialSearchError{status: resp.StatusCode, detail: strings.TrimSpace(string(detail))}
	}

	var parsed officialSearchResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, officialMaxResponseBytes)).Decode(&parsed); err != nil {
		// 响应体不可解析属服务端异常,按当前状态交给调用方降级。
		return nil, &officialSearchError{status: resp.StatusCode, detail: "decode response: " + err.Error()}
	}

	results := make([]SearchResult, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		authority := AuthorityUnknown
		if r.Authority != nil {
			authority = *r.Authority
		}
		published := ""
		if r.Date != nil {
			published = normalizePublishedAt(*r.Date)
		}
		results = append(results, SearchResult{
			Title:       r.Title,
			URL:         r.URL,
			Snippet:     r.Snippet,
			Score:       r.RerankScore,
			Authority:   authority,
			PublishedAt: published,
			Summary:     r.Summary,
		})
	}
	return results, nil
}

// normalizeOfficialBaseURL 去掉 base URL 尾部斜杠与冗余的 /v1 段。
// 与 pkg/llm 的 normalizeDeepSeekBaseURL 保持一致:官方文档中
// https://api.deepseek.com 与 https://api.deepseek.com/v1 均为合法写法,
// 按 OpenAI 兼容惯例填 /v1 时,原样拼 /v1/tools/search 会得到 /v1/v1/...
// 永久 404 且不进 ThrottleStore(每次调用白发一次请求)。
// 仅剥路径最后一段恰为 v1 的情况,自定义前缀(如 /proxy/v1)不受影响。
func normalizeOfficialBaseURL(raw string) string {
	return strings.TrimSuffix(strings.TrimRight(raw, "/"), "/v1")
}

// normalizePublishedAt 校验官方 date 字段。
// 非法格式置空而不让整次搜索失败 —— 单条脏数据不应牺牲整批结果。
func normalizePublishedAt(raw string) string {
	if raw == "" {
		return ""
	}
	if _, err := time.Parse(time.RFC3339, raw); err != nil {
		return ""
	}
	return raw
}

// handleOfficialFailure 按 design D3 的表处理官方后端失败,
// 返回给用户看的降级说明;调用方随后继续走本地后端。
func (t *WebSearch) handleOfficialFailure(throttle *ThrottleStore, err error) string {
	var oe *officialSearchError
	if !errors.As(err, &oe) {
		return "official search backend failed, fell back to local: " + err.Error()
	}

	switch {
	case oe.status == http.StatusPaymentRequired:
		// 402 在充值前不会自愈,但用户可能刚充值:用冷却窗口而非永久禁用,
		// 窗口结束后重新探测一次。
		t.officialSuspendedUntil.Store(time.Now().Add(officialSuspendCooldown).UnixNano())
		return fmt.Sprintf("official search backend out of balance (HTTP 402); paused for %s, local backend used",
			officialSuspendCooldown)
	case oe.status == http.StatusTooManyRequests:
		retryAt, consec := throttle.ReportRateLimited(officialSearchHost, time.Time{}, time.Now())
		return fmt.Sprintf("official search backend rate limited (HTTP 429, %d consecutive failures, earliest retry %s UTC); local backend used",
			consec, retryAt.UTC().Format(time.RFC3339))
	case oe.status >= http.StatusInternalServerError:
		return fmt.Sprintf("official search backend error (HTTP %d); local backend used", oe.status)
	case oe.status != 0:
		return fmt.Sprintf("official search backend returned HTTP %d; local backend used", oe.status)
	default:
		return "official search backend unreachable; local backend used: " + oe.detail
	}
}

// formatResults 渲染搜索结果。
// official=true 时输出可信度信号,并对高分的前若干条附长摘要(design D4);
// note 非空时在结果前追加一行降级/状态说明。
func (t *WebSearch) formatResults(query string, results []SearchResult, source string, start time.Time, note string, official bool) *ToolResult {
	duration := time.Since(start)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Search results for: \"%s\"  (%s)  %s\n", query, source, duration.Round(time.Millisecond))
	if note != "" {
		fmt.Fprintf(&buf, "[fallback] %s\n", note)
	}

	if len(results) == 0 {
		fmt.Fprintf(&buf, "No results found. Try different or fewer keywords, or use web_fetch with a known URL.")
	} else {
		fmt.Fprintf(&buf, "Found %d result(s)", len(results))
		if official {
			fmt.Fprintf(&buf, "  (score: relevance 0-3, >=%.1f high quality; authority: 3+ authoritative)", officialSummaryMinScore)
		}
		fmt.Fprintf(&buf, ":\n\n")

		summaryQuota := 0
		for i, r := range results {
			fmt.Fprintf(&buf, "%d. %s\n", i+1, r.Title)
			fmt.Fprintf(&buf, "   URL: %s\n", r.URL)
			if official {
				fmt.Fprintf(&buf, "   %s\n", credibilityLine(r))
			}
			fmt.Fprintf(&buf, "   %s\n", r.Snippet)
			// 长摘要只给达到高质量阈值的前 N 条:全量注入会挤占上下文,
			// 未被引用的条目可通过 web_fetch 取全文。
			if official && r.Summary != "" && r.Score >= officialSummaryMinScore && summaryQuota < officialSummaryTopN {
				summaryQuota++
				fmt.Fprintf(&buf, "   Summary: %s\n", truncateRunes(r.Summary, officialSummaryMaxRunes))
			}
			fmt.Fprintf(&buf, "\n")
		}
	}

	return &ToolResult{
		Content: buf.String(),
		Meta: ToolMeta{
			Duration:  duration,
			LineCount: len(results),
		},
	}
}

// credibilityLine 渲染官方结果的可信度标记;字段缺失时省略对应项。
func credibilityLine(r SearchResult) string {
	// 官方 API 保证 rerank_score 是 required 字段(0–3),故分数恒输出;
	// authority 允许 -1(unknown)与 0–4,必须区分"未知"与"权威度 0"。
	parts := []string{fmt.Sprintf("score %.1f", r.Score)}
	if r.Authority != AuthorityUnknown {
		parts = append(parts, fmt.Sprintf("authority %d", r.Authority))
	}
	if r.PublishedAt != "" {
		parts = append(parts, r.PublishedAt)
	}
	return strings.Join(parts, " · ")
}

// truncateRunes 按 rune 截断,超出时追加省略标记(避免截断多字节字符)。
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

func (t *WebSearch) Execute(ctx context.Context, p WebSearchParams) (*ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if strings.TrimSpace(p.Query) == "" {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			"query is required for web_search", nil), nil
	}

	maxResults := p.MaxResults
	if maxResults <= 0 {
		maxResults = DefaultWebSearchMaxResults
	}
	if maxResults > MaxWebSearchMaxResults {
		maxResults = MaxWebSearchMaxResults
	}

	// 超时设置
	timeoutMs := p.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = DefaultWebSearchTimeoutMs
	}
	if timeoutMs > MaxWebSearchTimeoutMs {
		timeoutMs = MaxWebSearchTimeoutMs
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()

	// 官方后端优先(design D2):有凭据且本会话未被停用则先试,
	// 失败按 D3 分类降级到本地,不使整个调用失败。
	var officialNote string
	if apiKey, baseURL, ok := t.officialCredentials(); ok && !t.officialCoolingDown() {
		offThrottle := ThrottleStoreFromContext(ctx)
		if allowed, retryAt, _, consec := offThrottle.Reserve(officialSearchHost, time.Now()); !allowed {
			officialNote = fmt.Sprintf("official search backend in backoff (%d consecutive failures, earliest retry %s UTC); local backend used",
				consec, retryAt.UTC().Format(time.RFC3339))
		} else {
			// 官方尝试使用独立预算:若共用 reqCtx,官方超时会耗尽整笔预算,
			// 本地降级接手时 ctx 已过期而必然失败 —— 等于没有降级。
			// 固定取一半,保证任何 timeout 下都留出本地降级的余量。
			offCtx, offCancel := context.WithTimeout(ctx, timeout/2)
			officialResults, officialErr := t.searchOfficial(offCtx, baseURL, apiKey, p.Query, maxResults)
			offCancel()
			if officialErr == nil {
				// 200 且 results 为空视为"无匹配",不回退本地(design D3):
				// 回退只会多付一笔费用,结果依然是空。
				offThrottle.ReportSuccess(officialSearchHost)
				return t.formatResults(p.Query, officialResults, "DeepSeek Search", start, "", true), nil
			}
			officialNote = t.handleOfficialFailure(offThrottle, officialErr)
		}
	}

	// 自动选择后端:BRAVE_API_KEY 存在 → Brave,否则 → DuckDuckGo
	var results []SearchResult
	var source string
	var execErr error

	// 后端选择 + 会话级限流短路(退避/封锁期内不发请求)
	key := ddgSearchHost
	source = "DuckDuckGo"
	braveKey := os.Getenv("BRAVE_API_KEY")
	if braveKey != "" {
		key = braveSearchHost
		source = "Brave Search"
	}
	// localFail 把官方后端失败原因并入本地失败结果。
	// REGRESSION: 官方 402/429 降级后若本地也失败,原输出只剩本地错误 ——
	// 用户看到 "DuckDuckGo timeout" 而完全不知道官方后端因余额不足被跳过,
	// 恰恰是最需要诊断信息的场景(实测:mock 402 + DDG 不可达时 tool_result
	// 里看不到任何 402 痕迹)。
	localFail := func(result *ToolResult) *ToolResult {
		if result == nil || officialNote == "" {
			return result
		}
		result.Content += "\n[official-backend] " + officialNote
		if result.Error != nil {
			result.Error.Message += "; official backend: " + officialNote
		}
		return result
	}

	throttle := ThrottleStoreFromContext(ctx)
	if allowed, retryAt, state, consec := throttle.Reserve(key, time.Now()); !allowed {
		kind := ErrKindRateLimited
		stateName := "限流退避中"
		if state == ThrottleBlocked {
			kind = ErrKindBlocked
			stateName = "策略封锁冷却中"
		}
		return localFail(&ToolResult{
			Content: fmt.Sprintf("Search blocked (%s)\nQuery: %s", stateName, p.Query),
			Meta:    ToolMeta{Duration: 0},
			Error: &ToolError{
				Class:   ErrorClassRecoverable,
				Kind:    kind,
				Message: fmt.Sprintf("search backend %s %s(连续失败 %d 次),最早可重试 %s(UTC)。到点前重试大概率仍失败——稍后再搜、换查询,或配置 BRAVE_API_KEY 切换后端",
					key, stateName, consec, retryAt.UTC().Format(time.RFC3339)),
			},
		}), nil
	}

	if braveKey != "" {
		results, execErr = t.searchBrave(reqCtx, p.Query, maxResults, braveKey)
	} else {
		results, execErr = t.searchDDG(reqCtx, p.Query, maxResults)
	}

	duration := time.Since(start)

	if execErr != nil {
		// 限流/封锁分类:登记会话级 ThrottleStore 并产出可执行语义文案。
		if hs, ok := execErr.(*searchStatusError); ok &&
			(hs.status == 202 || hs.status == http.StatusTooManyRequests ||
				hs.status == http.StatusServiceUnavailable || hs.status == http.StatusForbidden) {
			now := time.Now()
			if hs.status == http.StatusForbidden {
				until, consec := throttle.ReportBlocked(key, now)
				msg := fmt.Sprintf("search backend %s blocked (HTTP 403)。连续失败 %d 次,冷却至 %s(UTC)。重试同一查询大概率仍失败——换查询、稍后再搜,或配置 BRAVE_API_KEY 切换后端", source, consec, until.UTC().Format(time.RFC3339))
				if hs.detail != "" {
					msg += " server response: " + hs.detail
				}
				return localFail(&ToolResult{
					Content: fmt.Sprintf("Search failed (%s): HTTP %d 策略封锁\nQuery: %s", source, hs.status, p.Query),
					Meta:    ToolMeta{Duration: duration},
					Error: &ToolError{
						Class:   ErrorClassRecoverable,
						Kind:    ErrKindBlocked,
						Message: msg,
					},
				}), nil
			}
			var ext time.Time
			if hs.retryAfter > 0 {
				ext = now.Add(hs.retryAfter)
			}
			retryAt, consec := throttle.ReportRateLimited(key, ext, now)
			verb := "限流"
			if hs.status == 202 {
				verb = "反爬/结果未就绪"
			}
			msg := fmt.Sprintf("search backend %s %s (HTTP %d)。连续失败 %d 次,最早可重试 %s(UTC)。稍后重试、换查询,或配置 BRAVE_API_KEY 切换后端", source, verb, hs.status, consec, retryAt.UTC().Format(time.RFC3339))
			if hs.detail != "" {
				msg += " server response: " + hs.detail
			}
			return localFail(&ToolResult{
				Content: fmt.Sprintf("Search failed (%s): HTTP %d\nQuery: %s", source, hs.status, p.Query),
				Meta:    ToolMeta{Duration: duration},
				Error: &ToolError{
					Class:   ErrorClassRecoverable,
					Kind:    ErrKindRateLimited,
					Message: msg,
				},
			}), nil
		}
		// 检测 context 超时
		if reqCtx.Err() == context.DeadlineExceeded {
			return localFail(&ToolResult{
				Content: fmt.Sprintf("Search timed out after %s.\nQuery: %s  (%s)", formatDuration(timeout), p.Query, source),
				Meta:    ToolMeta{Duration: duration},
				Error: &ToolError{
					Class:   ErrorClassRecoverable,
					Kind:    ErrKindTimeout,
					Message: fmt.Sprintf("search timed out after %s. Try increasing timeout_ms or using fewer keywords", formatDuration(timeout)),
				},
			}), nil
		}
		return localFail(&ToolResult{
			Content: fmt.Sprintf("Search failed (%s): %s\nQuery: %s", source, execErr.Error(), p.Query),
			Meta:    ToolMeta{Duration: duration},
			Error: &ToolError{
				Class:   ErrorClassRecoverable,
				Kind:    ErrKindCommandFailed,
				Message: fmt.Sprintf("search failed: %s. Check your network or try web_fetch with a known URL", execErr.Error()),
				Cause:   execErr,
			},
		}), nil
	}

	// 成功:清零会话级限流状态
	throttle.ReportSuccess(key)

	// 本地后端不提供可信度信号:显式标注"未知"而不是留 0 ——
	// authority 的 0 是合法等级,零值会被误读为"权威度 0"。
	for i := range results {
		results[i].Authority = AuthorityUnknown
	}
	return t.formatResults(p.Query, results, source, start, officialNote, false), nil
}

// ---------------------------------------------------------------------------
// DuckDuckGo HTML 后端
// ---------------------------------------------------------------------------

const ddgSearchURL = "https://html.duckduckgo.com/html/"

// 后端限流状态 key(与请求 host 解耦:测试 server 与生产 host 共用同一 key,
// 节流状态按后端而非瞬时域名统计)。
const (
	ddgSearchHost   = "html.duckduckgo.com"
	braveSearchHost = "api.search.brave.com"
)

// searchStatusError 由后端返回的 HTTP 状态错误;Execute 据此分类
// 限流(202/429/503)与封锁(403),登记会话级 ThrottleStore 并产出语义文案。
type searchStatusError struct {
	status     int
	retryAfter time.Duration // Retry-After 原始时长(未钳制);0 表示无
	detail     string        // 后端响应体摘要(诊断用;限流/封锁路径不展示原文)
}

func (e *searchStatusError) Error() string {
	if e.detail != "" {
		return fmt.Sprintf("HTTP %d: %s", e.status, e.detail)
	}
	return fmt.Sprintf("HTTP %d", e.status)
}

// webSearchRetryBackoff 搜索限流退避的默认等待时间。
const webSearchRetryBackoff = 1 * time.Second

// isDDGRateLimited 判断 DuckDuckGo 状态码是否可退避重试:
// 202(DDG 反爬/异步结果未就绪,历史失败主因)、429、503。
// 403 不重试(封禁语义,重试无意义)。
func isDDGRateLimited(status int) bool {
	switch status {
	case 202, http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return true
	}
	return false
}

func (t *WebSearch) searchDDG(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	baseURL := t.ddgBaseURL
	if baseURL == "" {
		baseURL = ddgSearchURL
	}
	reqURL := baseURL + "?q=" + url.QueryEscape(query)

	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("User-Agent", "Waveloom/0.1.0")
		req.Header.Set("Accept", "text/html")

		resp, err := t.client().Do(req)
		if err != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}

		// 限流类状态码(202/429/503)→ 退避重试一次(解析 Retry-After,
		// 服务端要求等待超工具上限时不再重试,直接交给 Execute 分类)
		if attempt == 0 && isDDGRateLimited(resp.StatusCode) {
			_ = resp.Body.Close()
			wait := webSearchRetryBackoff
			if raw := rawRetryAfterDelay(resp.Header); raw > 0 {
				if raw > throttleMaxInToolWait {
					return nil, &searchStatusError{status: resp.StatusCode, retryAfter: raw}
				}
				wait = raw
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
			continue
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			return nil, &searchStatusError{
				status:     resp.StatusCode,
				retryAfter: rawRetryAfterDelay(resp.Header),
			}
		}

		bodyBytes, err := readHTTPBodyWithContext(ctx, io.LimitReader(resp.Body, 1<<20))
		if err != nil && len(bodyBytes) == 0 {
			return nil, fmt.Errorf("read response: %w", err)
		}

		return parseDDGResults(bytes.NewReader(bodyBytes), maxResults)
	}
	return nil, &searchStatusError{status: http.StatusTooManyRequests} // 不可达:循环最多 2 次
}

// parseDDGResults 从 DuckDuckGo HTML 响应中提取搜索结果。
// 使用 golang.org/x/net/html 解析，按 class 名宽松匹配。
func parseDDGResults(r io.Reader, maxResults int) ([]SearchResult, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("parse HTML: %w", err)
	}

	var results []SearchResult
	var current *SearchResult
	var inResult, inSnippet bool
	var stopped bool
	var snippetBuf bytes.Buffer

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if stopped {
			return
		}

		if n.Type == html.ElementNode {
			// 检测 result 容器
			if hasClass(n, "result") && !hasClass(n, "result--") {
				// 新的结果块开始 → 保存上一个
				if current != nil && current.URL != "" {
					results = append(results, *current)
					if len(results) >= maxResults {
						stopped = true
						return
					}
				}
				current = &SearchResult{}
				inResult = true
			}

			if inResult && current != nil {
				// 标题链接
				if n.Data == "a" && hasClass(n, "result__a") {
					current.Title = extractText(n)
					for _, attr := range n.Attr {
						if attr.Key == "href" {
							current.URL = extractDDGURL(attr.Val)
						}
					}
				}
				// 摘要
				if n.Data == "a" && hasClass(n, "result__snippet") {
					inSnippet = true
					snippetBuf.Reset()
				}
			}
		}

		if n.Type == html.TextNode && inSnippet {
			snippetBuf.WriteString(n.Data)
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}

		if n.Type == html.ElementNode && inSnippet && n.Data == "a" && hasClass(n, "result__snippet") {
			inSnippet = false
			if current != nil {
				current.Snippet = cleanSnippet(snippetBuf.String())
			}
		}
	}

	walk(doc)

	// 保存最后一个结果
	if current != nil && current.URL != "" && len(results) < maxResults {
		results = append(results, *current)
	}

	return results, nil
}

// extractDDGURL 从 DDG 的重定向 URL 中提取实际目标 URL。
// DDG 格式: //duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com&rut=...
func extractDDGURL(raw string) string {
	if !strings.Contains(raw, "uddg=") {
		return raw
	}
	// 尝试作为相对 URL 解析
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	target := u.Query().Get("uddg")
	if target == "" {
		return raw
	}
	decoded, err := url.QueryUnescape(target)
	if err != nil {
		return target
	}
	return decoded
}

// cleanSnippet 清理摘要文本：trim 空白、合并多空格。
func cleanSnippet(s string) string {
	s = strings.TrimSpace(s)
	// 合并连续空白
	parts := strings.Fields(s)
	return strings.Join(parts, " ")
}

// hasClass 检查 HTML 节点是否包含指定的 CSS class。
func hasClass(n *html.Node, class string) bool {
	for _, attr := range n.Attr {
		if attr.Key == "class" {
			classes := strings.Fields(attr.Val)
			for _, c := range classes {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

// extractText 递归提取节点内的所有文本内容。
func extractText(n *html.Node) string {
	var buf bytes.Buffer
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			buf.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(buf.String())
}

// ---------------------------------------------------------------------------
// Brave Search API 后端
// ---------------------------------------------------------------------------

const braveSearchURL = "https://api.search.brave.com/res/v1/web/search"

// braveWebResponse 是 Brave Search API 的 JSON 响应结构（仅提取需要的字段）。
type braveWebResponse struct {
	Web *braveWebResults `json:"web"`
}

type braveWebResults struct {
	Results []braveResult `json:"results"`
}

type braveResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

func (t *WebSearch) searchBrave(ctx context.Context, query string, maxResults int, apiKey string) ([]SearchResult, error) {
	baseURL := t.braveBaseURL
	if baseURL == "" {
		baseURL = braveSearchURL
	}
	reqURL := fmt.Sprintf("%s?q=%s&count=%d", baseURL, url.QueryEscape(query), maxResults)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("X-Subscription-Token", apiKey)

	resp, err := t.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

    	if resp.StatusCode != http.StatusOK {
    		body, _ := readHTTPBodyWithContext(ctx, io.LimitReader(resp.Body, 4096))
    		detail := ""
    		if len(body) > 0 {
    			if len(body) > 300 {
    				body = body[:300]
    			}
    			detail = string(body)
    		}
    		return nil, &searchStatusError{
    			status:     resp.StatusCode,
    			retryAfter: rawRetryAfterDelay(resp.Header),
    			detail:     detail,
    		}
    	}

	bodyBytes, err := readHTTPBodyWithContext(ctx, io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var braveResp braveWebResponse
	if err := json.Unmarshal(bodyBytes, &braveResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var results []SearchResult
	if braveResp.Web != nil {
		for _, r := range braveResp.Web.Results {
			results = append(results, SearchResult{
				Title:   r.Title,
				URL:     r.URL,
				Snippet: r.Description,
			})
		}
	}

	return results, nil
}
