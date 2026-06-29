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
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// WebFetch — 获取 Web 内容
// ---------------------------------------------------------------------------

const (
	DefaultWebFetchMaxSize  = 1 << 20  // 1MB
	MaxWebFetchMaxSize      = 5 << 20  // 5MB
	DefaultWebFetchTimeoutMs = 30000   // 30s
	MaxWebFetchTimeoutMs    = 120000   // 120s
)

type WebFetchParams struct {
	URL       string `json:"url"`
	MaxSize   int    `json:"max_size"`   // 最大响应字节数（可选，默认 1MB）
	TimeoutMs int    `json:"timeout_ms"` // 超时时间（毫秒，可选，默认 30000）
}

type WebFetch struct{
	httpClient    *http.Client // 可注入的 HTTP 客户端；nil 使用默认
	skipHostCheck bool         // 跳过主机 IP 校验（仅测试用）
}

func (t *WebFetch) Name() string         { return "web_fetch" }
func (t *WebFetch) Schema() json.RawMessage { return webFetchSchema }
func (t *WebFetch) ConcurrentSafe() bool { return true }

func (t *WebFetch) Description() string {
	return strings.Join([]string{
		"Fetch content from a URL and return text. Use for consulting online docs, API references, package registries, etc.",
		"",
		"Only text-based content is supported (text/*, application/json, application/xml, application/javascript).",
		"HTML pages are automatically stripped to plain text.",
		"Binary content (images, videos, etc.) is rejected.",
		"",
		"Note: this tool only makes GET requests, and does not modify any remote resources.",
	}, "\n")
}

func (t *WebFetch) client() *http.Client {
	if t.httpClient != nil {
		return t.httpClient
	}
	return webFetchClient
}

var webFetchClient = &http.Client{
	Timeout: time.Duration(MaxWebFetchTimeoutMs) * time.Millisecond,
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

func (t *WebFetch) Execute(ctx context.Context, p WebFetchParams) (*ToolResult, error) {
	// ── Step 0: 父 context 已取消 → 提前返回 ──
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// ── Step 1: URL 校验 ──
	parsedURL, err := url.Parse(p.URL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("invalid URL: %s", p.URL), err), nil
	}
	if !t.skipHostCheck {
		if err := validateRequestURL(parsedURL); err != nil {
			return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
				fmt.Sprintf("invalid URL: %s: %s", p.URL, err.Error()), nil), nil
		}
	}

	// ── Step 2: 大小限制 ──
	maxSize := p.MaxSize
	if maxSize <= 0 {
		maxSize = DefaultWebFetchMaxSize
	}
	if maxSize > MaxWebFetchMaxSize {
		maxSize = MaxWebFetchMaxSize
	}

	// ── Step 3: 超时设置 ──
	timeoutMs := p.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = DefaultWebFetchTimeoutMs
	}
	if timeoutMs > MaxWebFetchTimeoutMs {
		timeoutMs = MaxWebFetchTimeoutMs
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// ── Step 4: 构造请求 ──
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, p.URL, nil)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("cannot create request: %v", err), err), nil
	}
	req.Header.Set("User-Agent", "Waveloom/0.1.0")
	req.Header.Set("Accept", "text/*, application/json, application/xml, application/javascript")

	// ── Step 5: 发起请求 ──
	start := time.Now()
	resp, err := t.client().Do(req)
	if err != nil {
		duration := time.Since(start)
		if reqCtx.Err() == context.DeadlineExceeded {
			return &ToolResult{
				Content: fmt.Sprintf("Request timed out after %s.\nURL: %s", formatDuration(timeout), p.URL),
				Meta:    ToolMeta{Duration: duration},
				Error: &ToolError{
					Class:   ErrorClassRecoverable,
					Kind:    ErrKindTimeout,
					Message: fmt.Sprintf("request timed out after %s", formatDuration(timeout)),
				},
			}, nil
		}
		return toolError(ErrorClassRecoverable, ErrKindCommandFailed,
			fmt.Sprintf("request failed: %v", err), err), nil
	}
	defer resp.Body.Close()

	// ── Step 6: 检查 Content-Type ──
	contentType := resp.Header.Get("Content-Type")
	if !isTextContentType(contentType) {
		return toolError(ErrorClassRecoverable, ErrKindBinaryFile,
			fmt.Sprintf("unsupported content type: %s (only text/*, application/json, application/xml, application/javascript are supported)",
				contentType), nil), nil
	}

	// ── Step 7: 读取响应体（受大小限制，分块检查 context 取消）──
	limitedReader := io.LimitReader(resp.Body, int64(maxSize)+1)
	bodyBytes, err := readHTTPBodyWithContext(reqCtx, limitedReader)
	if err != nil {
		if reqCtx.Err() != nil {
			return nil, reqCtx.Err()
		}
		return toolError(ErrorClassRecoverable, ErrKindCommandFailed,
			fmt.Sprintf("error reading response: %v", err), err), nil
	}

	truncated := len(bodyBytes) > maxSize
	if truncated {
		bodyBytes = bodyBytes[:maxSize]
	}

	duration := time.Since(start)

	// ── Step 8: HTTP 状态码检查 ──
	if resp.StatusCode >= 400 {
		preview := formatBodyPreview(bodyBytes, 500)
		return &ToolResult{
			Content: fmt.Sprintf("HTTP %d %s\nURL: %s\n\n%s",
				resp.StatusCode, resp.Status, p.URL, preview),
			Meta: ToolMeta{
				Duration:  duration,
				ByteCount: len(bodyBytes),
			},
			Error: &ToolError{
				Class:   ErrorClassRecoverable,
				Kind:    ErrKindCommandFailed,
				Message: fmt.Sprintf("HTTP %d %s", resp.StatusCode, resp.Status),
			},
		}, nil
	}

	// ── Step 9: 文本提取 ──
	bodyText := string(bodyBytes)
	if isHTMLContentType(contentType) {
		bodyText = stripHTML(bodyText)
	}

	// ── Step 10: 格式化输出 ──
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("Fetched %s  HTTP %d", p.URL, resp.StatusCode))
	buf.WriteString(fmt.Sprintf("  %s", duration.Round(time.Millisecond)))
	buf.WriteString(fmt.Sprintf("\nContent-Type: %s", contentType))
	buf.WriteString(fmt.Sprintf("\nSize: %s", formatSize(int64(len(bodyBytes)))))
	if truncated {
		buf.WriteString(fmt.Sprintf(" [truncated from > %s]", formatSize(int64(maxSize))))
	}
	buf.WriteString("\n\n")
	buf.WriteString(bodyText)

	return &ToolResult{
		Content: buf.String(),
		Meta: ToolMeta{
			Duration:  duration,
			ByteCount: len(bodyBytes),
		},
	}, nil
}

// ── Content-Type helpers ──

func isTextContentType(contentType string) bool {
	// 提取 media type（去除参数如 charset）
	mediaType := strings.ToLower(contentType)
	if idx := strings.Index(mediaType, ";"); idx >= 0 {
		mediaType = mediaType[:idx]
	}
	mediaType = strings.TrimSpace(mediaType)

	switch mediaType {
	case
		"text/plain",
		"text/html",
		"text/markdown",
		"text/xml",
		"text/css",
		"text/javascript",
		"text/csv",
		"text/yaml",
		"application/json",
		"application/xml",
		"application/javascript",
		"application/xhtml+xml",
		"application/ld+json":
		return true
	default:
		return strings.HasPrefix(mediaType, "text/")
	}
}

func isHTMLContentType(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml+xml")
}

// ── stripHTML — 移除 HTML 标签，保留文本内容 ──

func stripHTML(s string) string {
	var buf bytes.Buffer
	inTag := false
	inScript := false
	inStyle := false
	tagName := ""

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if inTag {
			if ch == '>' {
				inTag = false
				// 检查是否是 <script> 或 <style> 结束标签
				if inScript && (tagName == "/script" || tagName == "script/") {
					inScript = false
				}
				if inStyle && (tagName == "/style" || tagName == "style/") {
					inStyle = false
				}
				// 检查是否是 <script> 或 <style> 开始标签
				lower := strings.ToLower(strings.TrimSpace(tagName))
				if lower == "script" {
					inScript = true
				}
				if lower == "style" {
					inStyle = true
				}
				tagName = ""
				// 添加换行在块级元素后
				if isBlockTag(lower) && !inScript && !inStyle {
					buf.WriteByte('\n')
				}
			} else {
				tagName += string(ch)
			}
			continue
		}

		if inScript || inStyle {
			if ch == '<' {
				inTag = true
				tagName = ""
			}
			continue
		}

		if ch == '<' {
			inTag = true
			tagName = ""
			continue
		}

		buf.WriteByte(ch)
	}

	// 清理多余空白
	result := buf.String()
	lines := strings.Split(result, "\n")
	var cleaned []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return strings.Join(cleaned, "\n")
}

var blockTags = map[string]bool{
	"div": true, "p": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "li": true, "tr": true,
	"section": true, "article": true, "header": true, "footer": true,
	"nav": true, "main": true, "aside": true, "br": true, "hr": true,
	"ul": true, "ol": true, "table": true, "pre": true, "blockquote": true,
	"/div": true, "/p": true, "/h1": true, "/h2": true, "/h3": true,
	"/h4": true, "/h5": true, "/h6": true, "/li": true, "/tr": true,
	"/section": true, "/article": true, "/header": true, "/footer": true,
	"/nav": true, "/main": true, "/aside": true, "/ul": true, "/ol": true,
	"/table": true, "/pre": true, "/blockquote": true,
}

func isBlockTag(tag string) bool {
	return blockTags[tag]
}

// ── formatBodyPreview — 截取响应体的前 n 字节用于错误消息 ──

func formatBodyPreview(body []byte, maxLen int) string {
	s := string(body)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + fmt.Sprintf("\n... [truncated: %d bytes]", len(s)-maxLen)
}

// ── SSRF 防护 ──

// validateRequestURL 校验请求 URL，阻止内网、回环、链接本地等地址。
func validateRequestURL(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("empty host")
	}
	return validateHost(host)
}

// validateHost 解析主机名并检查 IP 是否属于禁止范围。
// 阻止：回环、私有、链路本地、未指定、组播地址。
func validateHost(host string) error {
	// 先尝试直接解析为 IP（无 DNS 查询）
	if ip := net.ParseIP(host); ip != nil {
		return checkIPAllowed(ip)
	}

	// 主机名：DNS 解析后逐一检查所有 IP
	ips, err := net.LookupIP(host)
	if err != nil {
		// 无法解析的域名拒绝，防止 DNS rebinding 探测
		return fmt.Errorf("cannot resolve host: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("no IP addresses resolved for host")
	}
	for _, ip := range ips {
		if err := checkIPAllowed(ip); err != nil {
			return err
		}
	}
	return nil
}

// checkIPAllowed 检查单个 IP 是否允许访问。
func checkIPAllowed(ip net.IP) error {
	if ip.IsLoopback() {
		return fmt.Errorf("loopback address rejected: %s", ip)
	}
	if ip.IsPrivate() {
		return fmt.Errorf("private address rejected: %s", ip)
	}
	if ip.IsLinkLocalUnicast() {
		return fmt.Errorf("link-local address rejected: %s", ip)
	}
	if ip.IsLinkLocalMulticast() {
		return fmt.Errorf("link-local multicast address rejected: %s", ip)
	}
	if ip.IsUnspecified() {
		return fmt.Errorf("unspecified address rejected: %s", ip)
	}
	if ip.IsMulticast() {
		return fmt.Errorf("multicast address rejected: %s", ip)
	}

	// 额外检查 IPv4 特殊范围：0.0.0.0/8、127.0.0.0/8 等
	ip4 := ip.To4()
	if ip4 != nil {
		if err := checkIPv4Special(ip4); err != nil {
			return err
		}
	}
	return nil
}

// ── readHTTPBodyWithContext — 分块读取 HTTP 响应体，每 64KB 检查 context ──

// readHTTPBodyWithContext 从 reader 分块读取数据，每 64KB 检查 ctx 是否取消。
// 用于替代 io.ReadAll，支持 context 中断。
func readHTTPBodyWithContext(ctx context.Context, reader io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	chunk := make([]byte, 64*1024) // 64KB chunks

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := reader.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}

	return buf.Bytes(), nil
}

// checkIPv4Special 检查未被子网方法覆盖的 IPv4 特殊范围。
func checkIPv4Special(ip net.IP) error {
	// 0.0.0.0/8（包括 0.0.0.0）
	if ip[0] == 0 {
		return fmt.Errorf("current network address rejected: %s", ip)
	}
	// 127.0.0.0/8（回环）
	if ip[0] == 127 {
		return fmt.Errorf("loopback address rejected: %s", ip)
	}
	// 100.64.0.0/10（运营商级 NAT，RFC 6598）
	if ip[0] == 100 && ip[1] >= 64 && ip[1] <= 127 {
		return fmt.Errorf("carrier-grade NAT address rejected: %s", ip)
	}
	// 169.254.0.0/16（链路本地）
	if ip[0] == 169 && ip[1] == 254 {
		return fmt.Errorf("link-local address rejected: %s", ip)
	}
	// 192.0.2.0/24（TEST-NET-1）
	if ip[0] == 192 && ip[1] == 0 && ip[2] == 2 {
		return fmt.Errorf("test-net address rejected: %s", ip)
	}
	// 198.18.0.0/15（基准测试）— 放行，沙箱环境常用此段作为透明代理地址
	// 198.51.100.0/24（TEST-NET-2）
	if ip[0] == 198 && ip[1] == 51 && ip[2] == 100 {
		return fmt.Errorf("test-net address rejected: %s", ip)
	}
	// 203.0.113.0/24（TEST-NET-3）
	if ip[0] == 203 && ip[1] == 0 && ip[2] == 113 {
		return fmt.Errorf("test-net address rejected: %s", ip)
	}
	// 224.0.0.0/4（组播）
	if ip[0] >= 224 && ip[0] <= 239 {
		return fmt.Errorf("multicast address rejected: %s", ip)
	}
	// 240.0.0.0/4（保留）
	if ip[0] >= 240 {
		return fmt.Errorf("reserved address rejected: %s", ip)
	}
	// 255.255.255.255（广播）
	if ip[0] == 255 && ip[1] == 255 && ip[2] == 255 && ip[3] == 255 {
		return fmt.Errorf("broadcast address rejected: %s", ip)
	}
	return nil
}

