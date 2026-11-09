package tool

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ddgFixtureHTML 是 DuckDuckGo 后端的固定响应,用于验证降级落点。
const ddgFixtureHTML = `<!DOCTYPE html>
<html><body>
<div class="result">
  <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Flocal.example%2Fpage">Local Fallback Title</a>
  <a class="result__snippet">Local fallback snippet.</a>
</div>
</body></html>`

// officialSearchMock 是官方搜索端点的测试替身:按给定状态回应并统计命中次数。
func officialSearchMock(t *testing.T, status int, body string, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != officialSearchPath {
			t.Errorf("path = %q, want %q", r.URL.Path, officialSearchPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer sk-test")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// localSearchMock 是 DuckDuckGo 后端的测试替身。
func localSearchMock(hits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		_, _ = w.Write([]byte(ddgFixtureHTML))
	}))
}

// alwaysCreds 返回一份固定可用的官方后端凭据。
func alwaysCreds() SearchCredentials {
	return func() (string, string, bool) { return "sk-test", "", true }
}

// T1:官方结果字段正确映射,非法 date 不让整批失败。
func TestWebSearchOfficial_ResultMapping(t *testing.T) {
	var officialHits, localHits atomic.Int32
	body := `{"results":[
		{"rerank_score":2.4,"title":"Alpha","url":"https://alpha.example","snippet":"snippet A",
		 "summary":"long summary A","date":"2026-06-01T00:00:00+00:00","authority":3},
		{"rerank_score":0.4,"title":"Beta","url":"https://beta.example","snippet":"snippet B",
		 "summary":"","date":"not-a-date","authority":0}
	]}`
	srv := officialSearchMock(t, http.StatusOK, body, &officialHits)
	defer srv.Close()
	local := localSearchMock(&localHits)
	defer local.Close()

	tool := &WebSearch{credentials: alwaysCreds(), officialBaseURL: srv.URL, ddgBaseURL: local.URL + "/html/?"}
	result, err := tool.Execute(context.Background(), WebSearchParams{Query: "q"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected tool error = %v", result.Error)
	}

	if got := officialHits.Load(); got != 1 {
		t.Errorf("official hits = %d, want 1", got)
	}
	if got := localHits.Load(); got != 0 {
		t.Errorf("local hits = %d, want 0 (official succeeded)", got)
	}

	wants := []string{
		"DeepSeek Search",
		"score 2.4", "authority 3", "2026-06-01T00:00:00+00:00",
		"Summary: long summary A",
		"authority 0", // 0 是合法等级,必须原样输出而非当作缺失
	}
	for _, w := range wants {
		if !strings.Contains(result.Content, w) {
			t.Errorf("content missing %q\n--- content ---\n%s", w, result.Content)
		}
	}
	// 非法 date 置空,不得让整批结果失败,也不得把原始脏值透出。
	if strings.Contains(result.Content, "not-a-date") {
		t.Errorf("invalid date leaked into output: %s", result.Content)
	}
}

// T2:官方各类失败均降级到本地,且不向调用方暴露错误。
func TestWebSearchOfficial_FallbackChain(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"out-of-balance-402", http.StatusPaymentRequired, `{"error":{"message":"insufficient balance"}}`},
		{"rate-limited-429", http.StatusTooManyRequests, `{"error":{"message":"qps exceeded"}}`},
		{"server-error-500", http.StatusInternalServerError, `{}`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var officialHits, localHits atomic.Int32
			srv := officialSearchMock(t, c.status, c.body, &officialHits)
			defer srv.Close()
			local := localSearchMock(&localHits)
			defer local.Close()

			tool := &WebSearch{credentials: alwaysCreds(), officialBaseURL: srv.URL, ddgBaseURL: local.URL + "/html/?"}
			result, err := tool.Execute(context.Background(), WebSearchParams{Query: "q"})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if result.Error != nil {
				t.Fatalf("fallback must not surface error: %v", result.Error)
			}
			if !strings.Contains(result.Content, "Local Fallback Title") {
				t.Errorf("expected local fallback content, got:\n%s", result.Content)
			}
			if !strings.Contains(result.Content, "[fallback]") {
				t.Errorf("expected fallback note, got:\n%s", result.Content)
			}
			if localHits.Load() == 0 {
				t.Error("local backend was not used")
			}
			if got := officialHits.Load(); got != 1 {
				t.Errorf("official hits = %d, want 1 (每次调用至多命中官方一次,防止计费泄漏)", got)
			}
		})
	}
}

// T2 续:402 是持续状态,同一实例的后续调用不得再次请求官方后端。
func TestWebSearchOfficial_402SuspendsForSession(t *testing.T) {
	var officialHits, localHits atomic.Int32
	srv := officialSearchMock(t, http.StatusPaymentRequired, `{}`, &officialHits)
	defer srv.Close()
	local := localSearchMock(&localHits)
	defer local.Close()

	tool := &WebSearch{credentials: alwaysCreds(), officialBaseURL: srv.URL, ddgBaseURL: local.URL + "/html/?"}
	for i := 0; i < 2; i++ {
		if _, err := tool.Execute(context.Background(), WebSearchParams{Query: "q"}); err != nil {
			t.Fatalf("call %d: Execute() error = %v", i, err)
		}
	}

	if got := officialHits.Load(); got != 1 {
		t.Errorf("official hits = %d, want 1 (402 must suspend official backend for the session)", got)
	}
	if got := localHits.Load(); got != 2 {
		t.Errorf("local hits = %d, want 2", got)
	}
}

// T3:官方成功但无匹配时不回退本地 —— 回退只会多付一次费用。
func TestWebSearchOfficial_EmptyResultsNoFallback(t *testing.T) {
	var officialHits, localHits atomic.Int32
	srv := officialSearchMock(t, http.StatusOK, `{"results":[]}`, &officialHits)
	defer srv.Close()
	local := localSearchMock(&localHits)
	defer local.Close()

	tool := &WebSearch{credentials: alwaysCreds(), officialBaseURL: srv.URL, ddgBaseURL: local.URL + "/html/?"}
	result, err := tool.Execute(context.Background(), WebSearchParams{Query: "q"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected tool error = %v", result.Error)
	}
	if got := localHits.Load(); got != 0 {
		t.Errorf("local hits = %d, want 0 (empty official result must not fall back)", got)
	}
	if !strings.Contains(result.Content, "DeepSeek Search") {
		t.Errorf("result must attribute the official backend, got:\n%s", result.Content)
	}
	if !strings.Contains(result.Content, "No results found") {
		t.Errorf("empty result must be explained, got:\n%s", result.Content)
	}
}

// T4:本地后端结果不得伪造可信度信号。
func TestWebSearchLocal_NoFabricatedSignals(t *testing.T) {
	var localHits atomic.Int32
	local := localSearchMock(&localHits)
	defer local.Close()

	tool := &WebSearch{ddgBaseURL: local.URL + "/html/?"}
	result, err := tool.Execute(context.Background(), WebSearchParams{Query: "q"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected tool error = %v", result.Error)
	}
	if localHits.Load() == 0 {
		t.Fatal("local backend was not used")
	}
	for _, banned := range []string{"score ", "authority ", "Summary:"} {
		if strings.Contains(result.Content, banned) {
			t.Errorf("local result must not carry %q, got:\n%s", banned, result.Content)
		}
	}
}

// T6:长摘要只给达到高质量阈值的前 N 条,控制上下文体积。
func TestWebSearchOfficial_SummaryQuota(t *testing.T) {
	items := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		score := 0.5 // 低于高质量阈值
		if i < officialSummaryTopN {
			score = 2.5 - float64(i)*0.1
		}
		items = append(items, fmt.Sprintf(
			`{"rerank_score":%.1f,"title":"R%d","url":"https://r%d.example","snippet":"s%d","summary":"SUMMARY-%d","authority":3}`,
			score, i, i, i, i))
	}
	body := `{"results":[` + strings.Join(items, ",") + `]}`

	var officialHits, localHits atomic.Int32
	srv := officialSearchMock(t, http.StatusOK, body, &officialHits)
	defer srv.Close()
	local := localSearchMock(&localHits)
	defer local.Close()

	tool := &WebSearch{credentials: alwaysCreds(), officialBaseURL: srv.URL, ddgBaseURL: local.URL + "/html/?"}
	result, err := tool.Execute(context.Background(), WebSearchParams{Query: "q", MaxResults: 10})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got := strings.Count(result.Content, "Summary:"); got != officialSummaryTopN {
		t.Errorf("summary count = %d, want %d\n--- content ---\n%s", got, officialSummaryTopN, result.Content)
	}
	// 低分条目的摘要不得出现(第 4 条起低于阈值)。
	if strings.Contains(result.Content, "SUMMARY-3") {
		t.Errorf("low-score result must not include summary:\n%s", result.Content)
	}
}

// T7:无凭据时不发出任何官方请求,直接走本地。
func TestWebSearchOfficial_NoCredentialsUsesLocalOnly(t *testing.T) {
	cases := []struct {
		name string
		cred SearchCredentials
	}{
		{"nil-credentials", nil},
		{"credentials-unavailable", func() (string, string, bool) { return "", "", false }},
		{"empty-api-key", func() (string, string, bool) { return "", "https://example.com", true }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var officialHits, localHits atomic.Int32
			srv := officialSearchMock(t, http.StatusOK, `{"results":[{"title":"X","url":"https://x.example","snippet":"s"}]}`, &officialHits)
			defer srv.Close()
			local := localSearchMock(&localHits)
			defer local.Close()

			tool := &WebSearch{credentials: c.cred, officialBaseURL: srv.URL, ddgBaseURL: local.URL + "/html/?"}
			result, err := tool.Execute(context.Background(), WebSearchParams{Query: "q"})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if got := officialHits.Load(); got != 0 {
				t.Errorf("official hits = %d, want 0 (no credentials must not call official endpoint)", got)
			}
			if localHits.Load() == 0 {
				t.Error("local backend was not used")
			}
			if !strings.Contains(result.Content, "Local Fallback Title") {
				t.Errorf("expected local content, got:\n%s", result.Content)
			}
		})
	}
}

// 官方端点不可达(连接失败)时同样降级,且不把错误抛给调用方。
func TestWebSearchOfficial_UnreachableFallsBack(t *testing.T) {
	var localHits atomic.Int32
	local := localSearchMock(&localHits)
	defer local.Close()

	// 关闭的 server 得到一个连接必失败的 URL。
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	tool := &WebSearch{credentials: alwaysCreds(), officialBaseURL: deadURL, ddgBaseURL: local.URL + "/html/?"}
	result, err := tool.Execute(context.Background(), WebSearchParams{Query: "q"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unreachable official backend must degrade, not error: %v", result.Error)
	}
	if !strings.Contains(result.Content, "Local Fallback Title") {
		t.Errorf("expected local fallback content, got:\n%s", result.Content)
	}
	if !strings.Contains(result.Content, "[fallback]") {
		t.Errorf("expected fallback note, got:\n%s", result.Content)
	}
}

// 官方超时必须仍能降级本地。
// REGRESSION: 原实现与本地共用 reqCtx —— 官方超时耗尽整笔预算后,本地降级
// 接手时 ctx 已过期,降级必然失败并返回 "search timed out",超时路径上等于
// 没有降级。官方改为独立取 timeout/2 后不再饿死本地。
func TestWebSearchOfficial_TimeoutStillFallsBack(t *testing.T) {
	var officialHits, localHits atomic.Int32
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		officialHits.Add(1)
		// 挂住直到客户端放弃;兜底 timer 是必需的 —— 客户端超时取消不一定会
		// 传播到 handler 的 ctx,无兜底时 httptest.Server.Close 会等 handler
		// 返回而死锁。
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer slow.Close()
	local := localSearchMock(&localHits)
	defer local.Close()

	tool := &WebSearch{credentials: alwaysCreds(), officialBaseURL: slow.URL, ddgBaseURL: local.URL + "/html/?"}
	result, err := tool.Execute(context.Background(), WebSearchParams{Query: "q", TimeoutMs: 1200})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("official timeout must degrade to local, got error: %v", result.Error)
	}
	if !strings.Contains(result.Content, "Local Fallback Title") {
		t.Errorf("expected local fallback content, got:\n%s", result.Content)
	}
	if officialHits.Load() == 0 {
		t.Error("official backend should have been attempted first")
	}
}

// base URL 尾部带 /v1 时不得拼出 /v1/v1/...。
// REGRESSION: 原实现直接 TrimSuffix(baseURL,"/") + "/v1/tools/search";用户按
// OpenAI 兼容惯例填 https://api.deepseek.com/v1 会得到永久 404,而 404 不进
// ThrottleStore,每次调用都白发一次请求。现复用与 LLM adapter 一致的归一化。
func TestWebSearchOfficial_BaseURLWithV1Suffix(t *testing.T) {
	var officialHits, localHits atomic.Int32
	srv := officialSearchMock(t, http.StatusOK,
		`{"results":[{"rerank_score":2.0,"title":"T","url":"https://t.example","snippet":"s"}]}`, &officialHits)
	defer srv.Close()
	local := localSearchMock(&localHits)
	defer local.Close()

	// officialSearchMock 会断言请求路径等于 officialSearchPath,拼出 /v1/v1 即失败。
	tool := &WebSearch{credentials: alwaysCreds(), officialBaseURL: srv.URL + "/v1", ddgBaseURL: local.URL + "/html/?"}
	result, err := tool.Execute(context.Background(), WebSearchParams{Query: "q"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected tool error = %v", result.Error)
	}
	if got := officialHits.Load(); got != 1 {
		t.Errorf("official hits = %d, want 1", got)
	}
	if localHits.Load() != 0 {
		t.Errorf("local hits = %d, want 0 (官方应成功,不该降级)", localHits.Load())
	}
	if !strings.Contains(result.Content, "DeepSeek Search") {
		t.Errorf("expected official backend attribution, got:\n%s", result.Content)
	}
}

// 官方 402 且本地也失败时,输出必须带官方失败原因。
// REGRESSION: 原实现只在降级**成功**路径展示 [fallback],本地失败路径完全丢失
// 官方原因。实测(mock 402 + DDG 不可达)tool_result 只剩 "DuckDuckGo timeout",
// 用户会把"余额不足"误诊为网络问题。
func TestWebSearchOfficial_LocalFailureCarriesOfficialReason(t *testing.T) {
	var officialHits atomic.Int32
	srv := officialSearchMock(t, http.StatusPaymentRequired, `{"error":{"message":"insufficient balance"}}`, &officialHits)
	defer srv.Close()

	// 本地后端指向已关闭的 server:连接必被拒绝,快速失败。
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	tool := &WebSearch{credentials: alwaysCreds(), officialBaseURL: srv.URL, ddgBaseURL: deadURL + "/html/?"}
	result, err := tool.Execute(context.Background(), WebSearchParams{Query: "q", TimeoutMs: 3000})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatalf("本地也失败时应返回错误结果,got:\n%s", result.Content)
	}

	combined := result.Content + "\n" + result.Error.Message
	if !strings.Contains(result.Content, "[official-backend]") {
		t.Errorf("本地失败结果必须带官方后端说明,got:\n%s", result.Content)
	}
	if !strings.Contains(combined, "402") {
		t.Errorf("官方 402 原因丢失,content=%q message=%q", result.Content, result.Error.Message)
	}
	if got := officialHits.Load(); got != 1 {
		t.Errorf("official hits = %d, want 1", got)
	}
}

// OfficialCredentials 只对 deepseek(或未指定)provider 启用官方后端。
func TestOfficialCredentials_ProviderFilter(t *testing.T) {
	cases := []struct {
		name     string
		apiKey   string
		provider string
		wantOK   bool
	}{
		{"deepseek-with-key", "sk-x", "deepseek", true},
		{"empty-provider-defaults-deepseek", "sk-x", "", true},
		{"kimi-rejected", "sk-x", "kimi", false},
		{"openai-rejected", "sk-x", "openai", false},
		{"empty-key-rejected", "", "deepseek", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			creds := OfficialCredentials(func() (string, string, string) {
				return c.apiKey, c.provider, "https://api.deepseek.com"
			})
			_, _, ok := creds()
			if ok != c.wantOK {
				t.Errorf("ok = %v, want %v", ok, c.wantOK)
			}
		})
	}
}