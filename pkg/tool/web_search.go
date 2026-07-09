package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// ---------------------------------------------------------------------------
// WebSearch — 搜索引擎查询
// ---------------------------------------------------------------------------

const (
	DefaultWebSearchMaxResults = 10
	MaxWebSearchMaxResults     = 20
)

type WebSearchParams struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"` // 返回结果数（可选，默认 10，最大 20）
}

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

type WebSearch struct {
	httpClient   *http.Client // 可注入的 HTTP 客户端；nil 使用默认
	braveBaseURL string       // 测试用 Brave API URL 覆盖；空则用常量
	ddgBaseURL   string       // 测试用 DDG URL 覆盖；空则用常量
}

func (t *WebSearch) Name() string            { return "web_search" }
func (t *WebSearch) Schema() json.RawMessage { return webSearchSchema }
func (t *WebSearch) ConcurrentSafe() bool    { return true }

func (t *WebSearch) Description() string {
	return strings.Join([]string{
		"Search the web and return a list of results (title, URL, snippet).",
		"Use this to find current documentation, API references, solutions, or any information not in your training data.",
		"",
		"After searching, use web_fetch to read the full content of promising URLs.",
		"",
		"Backends (auto-selected):",
		"- DuckDuckGo (default, no configuration needed)",
		"- Brave Search (set BRAVE_API_KEY environment variable for better results)",
	}, "\n")
}

func (t *WebSearch) client() *http.Client {
	if t.httpClient != nil {
		return t.httpClient
	}
	// 复用 web_fetch 的 SSRF 安全客户端（共享 Dialer + 重定向校验）
	return &http.Client{
		Timeout: 30 * time.Second,
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

	start := time.Now()

	// 自动选择后端：BRAVE_API_KEY 存在 → Brave，否则 → DuckDuckGo
	var results []SearchResult
	var source string
	var execErr error

	if braveKey := os.Getenv("BRAVE_API_KEY"); braveKey != "" {
		source = "Brave Search"
		results, execErr = t.searchBrave(ctx, p.Query, maxResults, braveKey)
	} else {
		source = "DuckDuckGo"
		results, execErr = t.searchDDG(ctx, p.Query, maxResults)
	}

	duration := time.Since(start)

	if execErr != nil {
		return &ToolResult{
			Content: fmt.Sprintf("Search failed (%s): %s\nQuery: %s", source, execErr.Error(), p.Query),
			Meta:    ToolMeta{Duration: duration},
			Error: &ToolError{
				Class:   ErrorClassRecoverable,
				Kind:    ErrKindCommandFailed,
				Message: fmt.Sprintf("search failed: %s", execErr.Error()),
				Cause:   execErr,
			},
		}, nil
	}

	// 格式化输出
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Search results for: \"%s\"  (%s)  %s\n", p.Query, source, duration.Round(time.Millisecond))
	if len(results) == 0 {
		fmt.Fprintf(&buf, "No results found.")
	} else {
		fmt.Fprintf(&buf, "Found %d result(s):\n\n", len(results))
		for i, r := range results {
			fmt.Fprintf(&buf, "%d. %s\n", i+1, r.Title)
			fmt.Fprintf(&buf, "   URL: %s\n", r.URL)
			fmt.Fprintf(&buf, "   %s\n\n", r.Snippet)
		}
	}

	return &ToolResult{
		Content: buf.String(),
		Meta: ToolMeta{
			Duration:  duration,
			LineCount: len(results),
		},
	}, nil
}

// ---------------------------------------------------------------------------
// DuckDuckGo HTML 后端
// ---------------------------------------------------------------------------

const ddgSearchURL = "https://html.duckduckgo.com/html/"

func (t *WebSearch) searchDDG(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	baseURL := t.ddgBaseURL
	if baseURL == "" {
		baseURL = ddgSearchURL
	}
	reqURL := baseURL + "?q=" + url.QueryEscape(query)

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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	bodyBytes, err := readHTTPBodyWithContext(ctx, io.LimitReader(resp.Body, 1<<20))
	if err != nil && len(bodyBytes) == 0 {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return parseDDGResults(bytes.NewReader(bodyBytes), maxResults)
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
		return nil, fmt.Errorf("brave API HTTP %d: %s", resp.StatusCode, string(body))
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
