package tool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── ThrottleStore 单元语义 ─────────────────────────────────────────────

func TestRegression_ThrottleStoreBackoffEscalatesAndResets(t *testing.T) {
	s := NewThrottleStore()
	host := "api.example.com"
	t0 := time.Now()

	retryAt, n := s.ReportRateLimited(host, time.Time{}, t0)
	if n != 1 {
		t.Fatalf("consecutive = %d, want 1", n)
	}
	if d := retryAt.Sub(t0); d < 1500*time.Millisecond || d > 3*time.Second {
		t.Errorf("first backoff = %v, want ~2s", d)
	}

	// 退避期内 → 短路
	if allowed, _, state, consec := s.Reserve(host, t0.Add(1*time.Second)); allowed || state != ThrottleRateLimit || consec != 1 {
		t.Errorf("Reserve in backoff: allowed=%v state=%s consec=%d, want short-circuit", allowed, state, consec)
	}

	// 第二次失败 → 指数退避 4s
	retryAt2, n2 := s.ReportRateLimited(host, time.Time{}, t0.Add(5*time.Second))
	if n2 != 2 {
		t.Fatalf("consecutive = %d, want 2", n2)
	}
	if d := retryAt2.Sub(t0.Add(5 * time.Second)); d < 3*time.Second || d > 5*time.Second {
		t.Errorf("second backoff = %v, want ~4s", d)
	}
	if allowed, _, _, _ := s.Reserve(host, t0.Add(8*time.Second)); allowed {
		t.Error("Reserve should still short-circuit at t+8s")
	}

	// 成功清零 → 允许 + 计数复位
	s.ReportSuccess(host)
	if allowed, _, _, _ := s.Reserve(host, t0.Add(30*time.Second)); !allowed {
		t.Error("Reserve after success should be allowed")
	}
	_, n3 := s.ReportRateLimited(host, time.Time{}, t0.Add(31*time.Second))
	if n3 != 1 {
		t.Errorf("consecutive after reset = %d, want 1", n3)
	}
}

func TestRegression_ThrottleStoreExternalRetryAfterUnclamped(t *testing.T) {
	s := NewThrottleStore()
	host := "slow.example.com"
	t0 := time.Now()
	ext := t0.Add(10 * time.Minute) // 服务端 Retry-After=600s(未被 10s 钳制)

	retryAt, _ := s.ReportRateLimited(host, ext, t0)
	if retryAt.Sub(t0) < 9*time.Minute {
		t.Errorf("scheduled retryAt = %v after t0, want >= 10min (external unclamped)", retryAt.Sub(t0))
	}
	if allowed, retryAt2, state, _ := s.Reserve(host, t0.Add(60*time.Second)); allowed || state != ThrottleRateLimit {
		t.Errorf("Reserve at t+60s: allowed=%v state=%s, want short-circuit", allowed, state)
	} else if retryAt2.Sub(t0) < 9*time.Minute {
		t.Errorf("Reserve retryAt = %v, want near external schedule", retryAt2.Sub(t0))
	}
}

func TestRegression_ThrottleStore403BlockedShortCircuit(t *testing.T) {
	s := NewThrottleStore()
	host := "blocked.example.com"
	t0 := time.Now()

	until, n := s.ReportBlocked(host, t0)
	if n != 1 || until.Sub(t0) < 9*time.Minute {
		t.Fatalf("blocked until = %v (n=%d), want ~10min cooldown", until.Sub(t0), n)
	}
	if allowed, _, state, _ := s.Reserve(host, t0.Add(time.Minute)); allowed || state != ThrottleBlocked {
		t.Errorf("Reserve in cooldown: allowed=%v state=%s, want blocked short-circuit", allowed, state)
	}
	// 冷却结束后放行
	if allowed, _, _, _ := s.Reserve(host, t0.Add(11*time.Minute)); !allowed {
		t.Error("Reserve after cooldown should be allowed")
	}
}

func TestRegression_ThrottleStoreNilSafe(t *testing.T) {
	var s *ThrottleStore
	now := time.Now()
	if allowed, _, _, _ := s.Reserve("h", now); !allowed {
		t.Error("nil store must always allow")
	}
	if retryAt, n := s.ReportRateLimited("h", now.Add(time.Minute), now); retryAt.IsZero() || n < 1 {
		t.Error("nil store ReportRateLimited must return sane schedule")
	}
	s.ReportBlocked("h", now)
	s.ReportSuccess("h")
}

func TestRegression_ThrottleStoreConcurrent(t *testing.T) {
	s := NewThrottleStore()
	hosts := []string{"a.example.com", "b.example.com", "c.example.com"}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			host := hosts[g%len(hosts)]
			now := time.Now()
			for i := 0; i < 200; i++ {
				s.Reserve(host, now)
				if i%3 == 0 {
					s.ReportRateLimited(host, time.Time{}, now)
				}
				if i%7 == 0 {
					s.ReportSuccess(host)
				}
			}
		}(g)
	}
	wg.Wait()
}

// ── web_fetch 集成 ─────────────────────────────────────────────────────

func TestRegression_WebFetchRateLimitShortCircuit(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Retry-After", "600") // 服务端要求等待 10 分钟
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	tool := &WebFetch{skipHostCheck: true}
	s := NewThrottleStore()
	ctx := WithThrottleStore(context.Background(), s)

	// 第一次:单次请求后不再工具内重试(RA 超上限),错误带调度信息
	result, err := tool.Execute(ctx, WebFetchParams{URL: server.URL + "/a"})
	if err != nil || result.Error == nil {
		t.Fatalf("Execute() err=%v result.Error=%v, want rate-limit error", err, result.Error)
	}
	if result.Error.Kind != ErrKindRateLimited {
		t.Errorf("Kind = %v, want rate_limited", result.Error.Kind)
	}
	if !strings.Contains(result.Error.Message, "连续失败") || !strings.Contains(result.Error.Message, "UTC") {
		t.Errorf("message should carry consecutive count & retry time: %q", result.Error.Message)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1 (no in-tool retry for 600s RA)", requests)
	}

	// 第二次:退避期内直接短路,零网络请求
	result2, err := tool.Execute(ctx, WebFetchParams{URL: server.URL + "/a"})
	if err != nil || result2.Error == nil {
		t.Fatalf("Execute#2 err=%v result.Error=%v", err, result2.Error)
	}
	if result2.Error.Kind != ErrKindRateLimited {
		t.Errorf("Kind#2 = %v, want rate_limited", result2.Error.Kind)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want still 1 (short-circuited before network)", requests)
	}
}

func TestRegression_WebFetch403BlockedShortCircuit(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	tool := &WebFetch{skipHostCheck: true}
	s := NewThrottleStore()
	ctx := WithThrottleStore(context.Background(), s)

	result, err := tool.Execute(ctx, WebFetchParams{URL: server.URL + "/x"})
	if err != nil || result.Error == nil {
		t.Fatalf("Execute() err=%v result.Error=%v", err, result.Error)
	}
	if result.Error.Kind != ErrKindBlocked {
		t.Errorf("Kind = %v, want blocked", result.Error.Kind)
	}
	if !strings.Contains(result.Error.Message, "策略封锁") {
		t.Errorf("message should say policy blocked: %q", result.Error.Message)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	// 冷却期内第二次调用:零网络
	result2, _ := tool.Execute(ctx, WebFetchParams{URL: server.URL + "/x"})
	if result2.Error == nil || result2.Error.Kind != ErrKindBlocked {
		t.Errorf("Kind#2 = %v, want blocked", result2.Error.Kind)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want still 1 (blocked short-circuit)", requests)
	}
}

func TestRegression_WebFetchSuccessResetsThrottle(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	host := u.Host

	tool := &WebFetch{skipHostCheck: true}
	s := NewThrottleStore()
	ctx := WithThrottleStore(context.Background(), s)

	// 第一次:429(RA=1s)→ 工具内退避重试一次 → 成功。成功必须清零状态。
	if result, err := tool.Execute(ctx, WebFetchParams{URL: server.URL + "/x"}); err != nil || result.Error != nil {
		t.Fatalf("Execute failed after in-tool retry: %v %v (requests=%d)", err, result.Error, requests)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2 (429 + retry)", requests)
	}
	if allowed, _, _, _ := s.Reserve(host, time.Now()); !allowed {
		t.Error("host should be allowed after success (counters reset)")
	}
	// 状态已清零:第三次调用直接放行(无退避期短路线)
	if result, err := tool.Execute(ctx, WebFetchParams{URL: server.URL + "/x"}); err != nil || result.Error != nil {
		t.Fatalf("Execute after reset failed: %v %v", err, result.Error)
	}
	if requests != 3 {
		t.Errorf("requests = %d, want 3 (no short-circuit after reset)", requests)
	}
}

// ── web_search 集成 ────────────────────────────────────────────────────

func TestRegression_WebSearchRateLimitedAndShortCircuit(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusAccepted) // DDG 反爬 202
	}))
	defer server.Close()

	tool := &WebSearch{ddgBaseURL: server.URL + "/html/?"}
	s := NewThrottleStore()
	ctx := WithThrottleStore(context.Background(), s)

	result, err := tool.Execute(ctx, WebSearchParams{Query: "test", MaxResults: 5})
	if err != nil || result.Error == nil {
		t.Fatalf("Execute() err=%v result.Error=%v", err, result.Error)
	}
	if result.Error.Kind != ErrKindRateLimited {
		t.Errorf("Kind = %v, want rate_limited", result.Error.Kind)
	}
	if !strings.Contains(result.Error.Message, "连续失败") {
		t.Errorf("message should carry consecutive count: %q", result.Error.Message)
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2 (202 + in-tool retry)", requests)
	}
	// 退避期内第二次搜索:零网络
	before := requests
	if result2, _ := tool.Execute(ctx, WebSearchParams{Query: "test", MaxResults: 5}); result2.Error == nil || result2.Error.Kind != ErrKindRateLimited {
		t.Errorf("Kind#2 = %v, want rate_limited", result2.Error.Kind)
	}
	if requests != before {
		t.Errorf("requests = %d, want %d (short-circuited)", requests, before)
	}
}

func TestRegression_WebSearch403BlockedShortCircuit(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	tool := &WebSearch{ddgBaseURL: server.URL + "/html/?"}
	s := NewThrottleStore()
	ctx := WithThrottleStore(context.Background(), s)

	result, err := tool.Execute(ctx, WebSearchParams{Query: "test", MaxResults: 5})
	if err != nil || result.Error == nil {
		t.Fatalf("Execute() err=%v result.Error=%v", err, result.Error)
	}
	if result.Error.Kind != ErrKindBlocked {
		t.Errorf("Kind = %v, want blocked", result.Error.Kind)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1 (403 must not retry)", requests)
	}
	before := requests
	if result2, _ := tool.Execute(ctx, WebSearchParams{Query: "test", MaxResults: 5}); result2.Error == nil || result2.Error.Kind != ErrKindBlocked {
		t.Errorf("Kind#2 = %v, want blocked", result2.Error.Kind)
	}
	if requests != before {
		t.Errorf("requests = %d, want %d (blocked short-circuit)", requests, before)
	}
}

// TestRegression_RetryUsesFreshTimeoutBudget 验证限流退避后的重试拥有独立的
// 超时预算:原实现共享一个 reqCtx,退避 sleep 会吃光 timeout(如 400ms 预算 +
// 2s 退避 → 重试必然误报 timeout);现每次尝试独立 WithTimeout。
func TestRegression_RetryUsesFreshTimeoutBudget(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		time.Sleep(250 * time.Millisecond) // 慢响应,逼近 400ms 预算
		if requests == 1 {
			w.WriteHeader(http.StatusTooManyRequests) // 无 Retry-After → 2s 退避
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	tool := &WebFetch{skipHostCheck: true}
	result, err := tool.Execute(context.Background(), WebFetchParams{
		URL:       server.URL + "/slow-limited",
		TimeoutMs: 400, // 小于 2s 退避等待:共享预算的实现会在重试前超时
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("retry should succeed with a fresh timeout budget, got error kind=%s msg=%q", result.Error.Kind, result.Error.Message)
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2 (429 + successful retry)", requests)
	}
}
