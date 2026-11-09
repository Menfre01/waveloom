package tool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const fetchFixtureHTML = `<!DOCTYPE html><html><body><h1>Local Page</h1><p>local content</p></body></html>`

// officialFetchMock 是官方抓取端点的测试替身。
func officialFetchMock(t *testing.T, status int, body string, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != officialFetchPath {
			t.Errorf("path = %q, want %q", r.URL.Path, officialFetchPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer sk-test")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// localFetchMock 是本地抓取后端的测试替身。
func localFetchMock(hits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		_, _ = w.Write([]byte(fetchFixtureHTML))
	}))
}

// 官方抓取成功时直接返回 Markdown,不再走本地。
func TestWebFetchOfficial_Success(t *testing.T) {
	var officialHits, localHits atomic.Int32
	body := `{"title":"Official Title","content":"# Heading\n\nofficial markdown body"}`
	srv := officialFetchMock(t, http.StatusOK, body, &officialHits)
	defer srv.Close()
	local := localFetchMock(&localHits)
	defer local.Close()

	tool := &WebFetch{skipHostCheck: true, credentials: alwaysCreds(), officialBaseURL: srv.URL}
	result, err := tool.Execute(context.Background(), WebFetchParams{URL: local.URL + "/page"})
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
	for _, w := range []string{"DeepSeek Fetch", "Official Title", "official markdown body", "text/markdown"} {
		if !strings.Contains(result.Content, w) {
			t.Errorf("content missing %q\n--- content ---\n%s", w, result.Content)
		}
	}
}

// 官方返回无可用内容(404)时降级本地,不得直接判定 URL 不可访问。
func TestWebFetchOfficial_NoContentFallsBack(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"not-found-404", http.StatusNotFound, `{"error":{"message":"no extractable markdown"}}`},
		{"blocked-403", http.StatusForbidden, `{"error":{"message":"blocked by security policy"}}`},
		{"empty-content", http.StatusOK, `{"title":"","content":""}`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var officialHits, localHits atomic.Int32
			srv := officialFetchMock(t, c.status, c.body, &officialHits)
			defer srv.Close()
			local := localFetchMock(&localHits)
			defer local.Close()

			tool := &WebFetch{skipHostCheck: true, credentials: alwaysCreds(), officialBaseURL: srv.URL}
			result, err := tool.Execute(context.Background(), WebFetchParams{URL: local.URL + "/page"})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if result.Error != nil {
				t.Fatalf("fallback must not surface error: %v", result.Error)
			}
			if localHits.Load() == 0 {
				t.Error("local backend was not used")
			}
			if !strings.Contains(result.Content, "Local Page") {
				t.Errorf("expected local content, got:\n%s", result.Content)
			}
			if !strings.Contains(result.Content, "[fallback]") {
				t.Errorf("expected fallback note, got:\n%s", result.Content)
			}
		})
	}
}

// 402 后本会话不再尝试官方抓取。
func TestWebFetchOfficial_402SuspendsForSession(t *testing.T) {
	var officialHits, localHits atomic.Int32
	srv := officialFetchMock(t, http.StatusPaymentRequired, `{}`, &officialHits)
	defer srv.Close()
	local := localFetchMock(&localHits)
	defer local.Close()

	tool := &WebFetch{skipHostCheck: true, credentials: alwaysCreds(), officialBaseURL: srv.URL}
	for i := 0; i < 2; i++ {
		if _, err := tool.Execute(context.Background(), WebFetchParams{URL: local.URL + "/page"}); err != nil {
			t.Fatalf("call %d: Execute() error = %v", i, err)
		}
	}

	if got := officialHits.Load(); got != 1 {
		t.Errorf("official hits = %d, want 1 (402 must suspend official backend)", got)
	}
	if got := localHits.Load(); got != 2 {
		t.Errorf("local hits = %d, want 2", got)
	}
}

// T5:URL 安全校验在两个后端之前统一生效,危险 URL 不会发出任何请求。
func TestWebFetchURLGuard_RejectsUnsafeTargets(t *testing.T) {
	unsafe := []string{
		"http://127.0.0.1/private",
		"http://localhost/admin",
		"http://10.0.0.1/internal",
		"file:///etc/passwd",
	}

	for _, target := range unsafe {
		t.Run(target, func(t *testing.T) {
			var officialHits, localHits atomic.Int32
			srv := officialFetchMock(t, http.StatusOK, `{"title":"X","content":"y"}`, &officialHits)
			defer srv.Close()
			local := localFetchMock(&localHits)
			defer local.Close()

			// 不设 skipHostCheck:走真实校验路径。
			tool := &WebFetch{credentials: alwaysCreds(), officialBaseURL: srv.URL}
			result, err := tool.Execute(context.Background(), WebFetchParams{URL: target})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if result.Error == nil {
				t.Fatalf("unsafe URL %q must be rejected, got content:\n%s", target, result.Content)
			}
			if got := officialHits.Load(); got != 0 {
				t.Errorf("official backend must not be called for unsafe URL, hits = %d", got)
			}
			if got := localHits.Load(); got != 0 {
				t.Errorf("local backend must not be called for unsafe URL, hits = %d", got)
			}
		})
	}
}

// 无凭据时零官方请求,直接走本地。
func TestWebFetchOfficial_NoCredentialsUsesLocalOnly(t *testing.T) {
	var officialHits, localHits atomic.Int32
	srv := officialFetchMock(t, http.StatusOK, `{"title":"X","content":"y"}`, &officialHits)
	defer srv.Close()
	local := localFetchMock(&localHits)
	defer local.Close()

	tool := &WebFetch{skipHostCheck: true, officialBaseURL: srv.URL}
	result, err := tool.Execute(context.Background(), WebFetchParams{URL: local.URL + "/page"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := officialHits.Load(); got != 0 {
		t.Errorf("official hits = %d, want 0 (no credentials)", got)
	}
	if localHits.Load() == 0 {
		t.Error("local backend was not used")
	}
	if strings.Contains(result.Content, "DeepSeek Fetch") {
		t.Errorf("result must not attribute the official backend:\n%s", result.Content)
	}
}
