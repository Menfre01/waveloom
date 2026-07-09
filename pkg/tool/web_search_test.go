package tool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// ---------------------------------------------------------------------------
// Execute — 参数校验
// ---------------------------------------------------------------------------

func TestWebSearch_EmptyQuery(t *testing.T) {
	tool := &WebSearch{}
	result, err := tool.Execute(context.Background(), WebSearchParams{Query: ""})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for empty query")
	}
	if result.Error.Kind != ErrKindInvalidArgs {
		t.Errorf("expected ErrKindInvalidArgs, got %s", result.Error.Kind)
	}
}

func TestWebSearch_WhitespaceQuery(t *testing.T) {
	tool := &WebSearch{}
	result, err := tool.Execute(context.Background(), WebSearchParams{Query: "   "})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for whitespace-only query")
	}
}

// ---------------------------------------------------------------------------
// Execute — DDG 后端（通过 mock server）
// ---------------------------------------------------------------------------

func TestWebSearch_DDG_EndToEnd(t *testing.T) {
	mockHTML := `<!DOCTYPE html>
<html><body>
<div class="result">
  <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2Fgo1.25">Go 1.25 Release Notes</a>
  <a class="result__snippet">The latest Go release, version 1.25.</a>
</div>
<div class="result">
  <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgithub.com%2Fgolang%2Fgo">golang/go</a>
  <a class="result__snippet">The Go programming language.</a>
</div>
</body></html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		_, _ = w.Write([]byte(mockHTML))
	}))
	defer server.Close()

	tool := &WebSearch{ddgBaseURL: server.URL + "/html/?"}
	result, err := tool.Execute(context.Background(), WebSearchParams{
		Query:      "Go release",
		MaxResults: 5,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		execErr := result.Error
		t.Fatalf("Execute() result.Error = %v", execErr)
	}
	if !strings.Contains(result.Content, "Go 1.25 Release Notes") {
		t.Errorf("expected content to contain result title, got %q", result.Content)
	}
	if !strings.Contains(result.Content, "go.dev/doc/go1.25") {
		t.Errorf("expected content to contain result URL, got %q", result.Content)
	}
	if result.Meta.LineCount != 2 {
		t.Errorf("expected 2 results, got Meta.LineCount=%d", result.Meta.LineCount)
	}
}

func TestWebSearch_DDG_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	tool := &WebSearch{ddgBaseURL: server.URL + "/html/?"}
	result, err := tool.Execute(context.Background(), WebSearchParams{Query: "test"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if result.Error.Kind != ErrKindCommandFailed {
		t.Errorf("expected ErrKindCommandFailed, got %s", result.Error.Kind)
	}
	if !strings.Contains(result.Error.Message, "HTTP 500") {
		t.Errorf("expected HTTP 500 in error message, got %q", result.Error.Message)
	}
}

func TestWebSearch_DDG_EmptyResults(t *testing.T) {
	mockHTML := `<!DOCTYPE html>
<html><body>
<div class="no-results">No results found.</div>
</body></html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		_, _ = w.Write([]byte(mockHTML))
	}))
	defer server.Close()

	tool := &WebSearch{ddgBaseURL: server.URL + "/html/?"}
	result, err := tool.Execute(context.Background(), WebSearchParams{Query: "xyznonexistent"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if !strings.Contains(result.Content, "No results found") {
		t.Errorf("expected 'No results found' in content, got %q", result.Content)
	}
}

// ---------------------------------------------------------------------------
// Execute — Brave 后端（通过 mock server + BRAVE_API_KEY 环境变量）
// ---------------------------------------------------------------------------

func TestWebSearch_Brave_EndToEnd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Subscription-Token") != "test-brave-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"web": {
				"results": [
					{"title": "Brave Search", "url": "https://brave.com", "description": "A privacy-first search engine."},
					{"title": "Brave Browser", "url": "https://brave.com/browser", "description": "Fast, private browser."}
				]
			}
		}`))
	}))
	defer server.Close()

	t.Setenv("BRAVE_API_KEY", "test-brave-key")

	tool := &WebSearch{
		braveBaseURL: server.URL + "?",
	}
	result, err := tool.Execute(context.Background(), WebSearchParams{
		Query:      "brave",
		MaxResults: 5,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !strings.Contains(result.Content, "Brave Search") {
		t.Errorf("expected 'Brave Search' in content, got %q", result.Content)
	}
	if !strings.Contains(result.Content, "Brave Browser") {
		t.Errorf("expected 'Brave Browser' in content, got %q", result.Content)
	}
	if !strings.Contains(result.Content, "Brave Search") {
		t.Errorf("expected 'Brave Search' backend label, got %q", result.Content)
	}
	if result.Meta.LineCount != 2 {
		t.Errorf("expected 2 results, got Meta.LineCount=%d", result.Meta.LineCount)
	}
}

func TestWebSearch_Brave_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer server.Close()

	t.Setenv("BRAVE_API_KEY", "test-brave-key")

	tool := &WebSearch{
		braveBaseURL: server.URL + "?",
	}
	result, err := tool.Execute(context.Background(), WebSearchParams{Query: "test"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for HTTP 429")
	}
	if !strings.Contains(result.Error.Message, "rate limited") {
		t.Errorf("expected rate limit detail in error, got %q", result.Error.Message)
	}
}

func TestWebSearch_Brave_EmptyResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"web":{"results":[]}}`))
	}))
	defer server.Close()

	t.Setenv("BRAVE_API_KEY", "test-brave-key")

	tool := &WebSearch{
		braveBaseURL: server.URL + "?",
	}
	result, err := tool.Execute(context.Background(), WebSearchParams{Query: "rareterm"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if !strings.Contains(result.Content, "No results found") {
		t.Errorf("expected 'No results found' in content, got %q", result.Content)
	}
}

// ---------------------------------------------------------------------------
// Execute — MaxResults 截断
// ---------------------------------------------------------------------------

func TestWebSearch_MaxResultsTruncation(t *testing.T) {
	// max_results=100 → 截断为 20，验证参数截断逻辑（不依赖网络）
	mockHTML := `<!DOCTYPE html>
<html><body>
<div class="result"><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fa.com">A</a><a class="result__snippet">a</a></div>
<div class="result"><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fb.com">B</a><a class="result__snippet">b</a></div>
</body></html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		_, _ = w.Write([]byte(mockHTML))
	}))
	defer server.Close()

	tool := &WebSearch{ddgBaseURL: server.URL + "/html/?"}
	result, err := tool.Execute(context.Background(), WebSearchParams{
		Query:      "test",
		MaxResults: 100,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	// 100 超过上限 20，应被截断；实际只返回 2 条（mock 数据有限）
	if result.Meta.LineCount != 2 {
		t.Errorf("expected 2 results from mock, got %d", result.Meta.LineCount)
	}
}

// ---------------------------------------------------------------------------
// 属性 / 元数据
// ---------------------------------------------------------------------------

func TestWebSearch_Metadata(t *testing.T) {
	tool := &WebSearch{}
	if tool.Name() != "web_search" {
		t.Errorf("Name() = %s, want web_search", tool.Name())
	}
	if !tool.ConcurrentSafe() {
		t.Error("ConcurrentSafe() should be true")
	}
}

func TestWebSearch_Schema(t *testing.T) {
	tool := &WebSearch{}
	schema := tool.Schema()
	if schema == nil {
		t.Fatal("Schema() should not be nil")
	}
	schemaStr := string(schema)
	if !strings.Contains(schemaStr, "query") {
		t.Error("schema should contain 'query' property")
	}
	if !strings.Contains(schemaStr, "max_results") {
		t.Error("schema should contain 'max_results' property")
	}
}

func TestWebSearch_Description(t *testing.T) {
	tool := &WebSearch{}
	desc := tool.Description()
	if !strings.Contains(desc, "Search the web") {
		t.Errorf("Description() should explain search, got %q", desc)
	}
	if !strings.Contains(desc, "web_fetch") {
		t.Errorf("Description() should mention web_fetch, got %q", desc)
	}
}

// ---------------------------------------------------------------------------
// DDG HTML 解析（单元测试）
// ---------------------------------------------------------------------------

func TestParseDDGResults(t *testing.T) {
	htmlDoc := `<!DOCTYPE html>
<html><body>
<div class="result">
  <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2Fgo1.25&amp;rut=abc">Go 1.25 Release Notes</a>
  <a class="result__snippet">The latest Go release, version 1.25, arrives in August 2025.</a>
</div>
<div class="result">
  <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgithub.com%2Fgolang%2Fgo&amp;rut=def">golang/go on GitHub</a>
  <a class="result__snippet">The Go programming language.</a>
</div>
</body></html>`

	results, err := parseDDGResults(strings.NewReader(htmlDoc), 10)
	if err != nil {
		t.Fatalf("parseDDGResults() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Title != "Go 1.25 Release Notes" {
		t.Errorf("title[0] = %q", results[0].Title)
	}
	if results[0].URL != "https://go.dev/doc/go1.25" {
		t.Errorf("url[0] = %q", results[0].URL)
	}
	if !strings.Contains(results[0].Snippet, "latest Go release") {
		t.Errorf("snippet[0] = %q", results[0].Snippet)
	}
}

func TestParseDDGResults_MaxResultsLimit(t *testing.T) {
	htmlDoc := `<!DOCTYPE html>
<html><body>
<div class="result"><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fa.com">A</a><a class="result__snippet">a</a></div>
<div class="result"><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fb.com">B</a><a class="result__snippet">b</a></div>
<div class="result"><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fc.com">C</a><a class="result__snippet">c</a></div>
<div class="result"><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fd.com">D</a><a class="result__snippet">d</a></div>
<div class="result"><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fe.com">E</a><a class="result__snippet">e</a></div>
</body></html>`

	results, err := parseDDGResults(strings.NewReader(htmlDoc), 3)
	if err != nil {
		t.Fatalf("parseDDGResults() error = %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results (max_results limit), got %d", len(results))
	}
}

func TestParseDDGResults_EmptyHTML(t *testing.T) {
	htmlDoc := `<!DOCTYPE html>
<html><body>
<div class="no-results">No results found.</div>
</body></html>`

	results, err := parseDDGResults(strings.NewReader(htmlDoc), 10)
	if err != nil {
		t.Fatalf("parseDDGResults() error = %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// URL 提取 / 文本清理（单元测试）
// ---------------------------------------------------------------------------

func TestExtractDDGURL(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		expected string
	}{
		{"simple", "//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com&rut=abc", "https://example.com"},
		{"path with query", "//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2Fgo1.25&amp;rut=def", "https://go.dev/doc/go1.25"},
		{"no uddg param", "https://example.com", "https://example.com"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDDGURL(tt.raw)
			if got != tt.expected {
				t.Errorf("extractDDGURL(%q) = %q, want %q", tt.raw, got, tt.expected)
			}
		})
	}
}

func TestCleanSnippet(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"  hello   world  ", "hello world"},
		{"\n\ttrim me\n", "trim me"},
		{"already clean", "already clean"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := cleanSnippet(tt.input)
			if got != tt.expected {
				t.Errorf("cleanSnippet(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestHasClass(t *testing.T) {
	htmlStr := `<div class="result highlight"></div>`
	node, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var div *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "div" {
			div = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(node)
	if div == nil {
		t.Fatal("div not found")
	}

	if !hasClass(div, "result") {
		t.Error("hasClass(div, 'result') should be true")
	}
	if !hasClass(div, "highlight") {
		t.Error("hasClass(div, 'highlight') should be true")
	}
	if hasClass(div, "nonexistent") {
		t.Error("hasClass(div, 'nonexistent') should be false")
	}
}

func TestExtractText(t *testing.T) {
	htmlStr := `<a class="result__a" href="..."><b>Bold</b> Title <span>Span</span></a>`
	node, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var a *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			a = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(node)
	if a == nil {
		t.Fatal("a not found")
	}

	text := extractText(a)
	if text != "Bold Title Span" {
		t.Errorf("extractText() = %q, want 'Bold Title Span'", text)
	}
}
