package tool

import (
	_ "embed"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)
//go:embed web_fetch_prompt.md
var webFetchPrompt string

// ---------------------------------------------------------------------------
// WebFetch — 获取 Web 内容
// ---------------------------------------------------------------------------

const (
	DefaultWebFetchMaxSize  = 1 << 20  // 1MB
	MaxWebFetchMaxSize      = 5 << 20  // 5MB
	DefaultWebFetchTimeoutMs = 30000   // 30s
	MaxWebFetchTimeoutMs    = 120000   // 120s
)

// webFetchRetryBackoff 限流退避的默认等待时间(无 Retry-After 头时)。
const webFetchRetryBackoff = 2 * time.Second

// maxRateLimitWait 限流等待上限,避免长时间阻塞工具调用。
const maxRateLimitWait = 10 * time.Second

// isRateLimitedStatus 判断状态码是否属于可退避重试的限流类:
// 429 Too Many Requests / 503 Service Unavailable 恒真;
// 403 Forbidden 仅当携带 Retry-After 头(常见于网关限流,如 Cloudflare);
// 其余(404/405/500 等)不重试——404 是 URL 失效,重试无意义。
func isRateLimitedStatus(status int, header http.Header) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return true
	case http.StatusForbidden:
		return header.Get("Retry-After") != ""
	}
	return false
}

// retryAfterDuration 解析 Retry-After 头(秒或 HTTP-date),无效时返回 fallback。
func retryAfterDuration(header http.Header, fallback time.Duration) time.Duration {
	ra := header.Get("Retry-After")
	if ra == "" {
		return fallback
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil && secs >= 0 {
		// 先钳制再转 Duration:超大秒数(>9.2e9)直接乘会溢出为负,
		// time.After(负值)立即返回导致"立即重试"而非等待回退。
		if secs > int(maxRateLimitWait.Seconds()) {
			return maxRateLimitWait
		}
		d := time.Duration(secs) * time.Second
		if d > maxRateLimitWait {
			return maxRateLimitWait
		}
		return d
	}
	// HTTP-date 格式(极少见),解析失败回退
	if t, err := http.ParseTime(ra); err == nil {
		d := time.Until(t)
		if d > 0 && d < maxRateLimitWait {
			return d
		}
	}
	return fallback
}

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

var webFetchSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "url": {
      "type": "string",
      "description": "URL to fetch (http/https only)"
    },
    "max_size": {
      "type": "integer",
      "description": "Maximum response size in bytes (optional, default: 1MB, max: 5MB)"
    },
    "timeout_ms": {
      "type": "integer",
      "description": "Timeout in milliseconds (optional, default: 30000, max: 120000)"
    }
  },
  "required": ["url"]
}`)

func (t *WebFetch) Schema() json.RawMessage { return webFetchSchema }
func (t *WebFetch) ConcurrentSafe() bool { return true }

func (t *WebFetch) Description() string {
	return "Fetch content from a URL and return text (HTML stripped to plain text). Only text/*, JSON, XML, JavaScript. Rules: see system prompt ## Information Sources."
}

// Prompt 返回 web_fetch 使用指南和跨工具引用，由 Registry.FormatToolPrompts() 注入 C1。
// Prompt 返回使用指南，由 Registry.FormatToolPrompts() 注入 system prompt。
func (t *WebFetch) Prompt() string { return webFetchPrompt }

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

	// ── Step 5: 发起请求(限流类状态码自动退避重试一次)──
	start := time.Now()
	var (
		resp             *http.Response
		bodyBytes        []byte
		truncated        bool
		timeoutTruncated bool
		retried          bool
		readErr          error
	)
	// 会话级限流检查:退避/封锁期内直接短路,不发网络请求。
	throttle := ThrottleStoreFromContext(ctx)
	host := parsedURL.Host
	if allowed, retryAt, state, consec := throttle.Reserve(host, time.Now()); !allowed {
		kind := ErrKindRateLimited
		stateName := "限流退避中"
		if state == ThrottleBlocked {
			kind = ErrKindBlocked
			stateName = "策略封锁冷却中"
		}
		return toolError(ErrorClassRecoverable, kind,
			fmt.Sprintf("%s %s(连续失败 %d 次),最早可重试 %s(UTC)。到点前重试同一 URL 大概率仍失败——先处理其他任务或换源",
				host, stateName, consec, retryAt.UTC().Format(time.RFC3339)), nil), nil
	}
	// throttleUntil/throttleConsec 由限流响应登记,供最终错误文案携带调度信息。
	var throttleUntil time.Time
	var throttleConsec int
	for attempt := 0; ; attempt++ {
		// 每次尝试使用独立超时预算:退避等待(select 于父 ctx)不消耗请求
		// 超时——原实现共享一个 reqCtx,Retry-After 等待会吃光预算导致
		// 重试必然误报 timeout。尝试至多 2 次,defer 在函数返回时统一释放。
		attemptCtx, attemptCancel := context.WithTimeout(ctx, timeout)
		defer attemptCancel()
		req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, p.URL, nil)
		if err != nil {
			return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
				fmt.Sprintf("cannot create request: %v", err), err), nil
		}
		req.Header.Set("User-Agent", "Waveloom/0.1.0")
		req.Header.Set("Accept", "text/*, application/json, application/xml, application/javascript")

		resp, err = t.client().Do(req)
		if err != nil {
			duration := time.Since(start)
			if attemptCtx.Err() == context.DeadlineExceeded {
				return &ToolResult{
					Content: fmt.Sprintf("Request timed out after %s.\nURL: %s", formatDuration(timeout), p.URL),
					Meta:    ToolMeta{Duration: duration},
					Error: &ToolError{
						Class:   ErrorClassRecoverable,
						Kind:    ErrKindTimeout,
						Message: fmt.Sprintf("request timed out after %s. Increase timeout_ms or try a lighter URL", formatDuration(timeout)),
					},
				}, nil
			}
			return toolError(ErrorClassRecoverable, ErrKindCommandFailed,
				fmt.Sprintf("request failed: %v. Check the URL and your network, or try web_search to find the resource", err), err), nil
		}

		// 读取响应体(受大小限制,分块检查 context 取消)。限流判定在
		// Content-Type 检查前:限流响应可能非 text content-type,先判定
		// 重试可避免误入 binary 分支。
		limitedReader := io.LimitReader(resp.Body, int64(maxSize)+1)
		bodyBytes, readErr = readHTTPBodyWithContext(attemptCtx, limitedReader)
		_ = resp.Body.Close()

		// 大小截断
		truncated = len(bodyBytes) > maxSize
		if truncated {
			bodyBytes = bodyBytes[:maxSize]
		}

		// 超时截断:保留已读取的部分内容
		timeoutTruncated = false
		if readErr != nil {
			if len(bodyBytes) == 0 || attemptCtx.Err() != context.DeadlineExceeded {
				if attemptCtx.Err() != nil {
					return nil, attemptCtx.Err()
				}
				return toolError(ErrorClassRecoverable, ErrKindCommandFailed,
					fmt.Sprintf("error reading response: %v", readErr), readErr), nil
			}
			timeoutTruncated = true
		}

		// 限流类状态码(429/503,403+Retry-After)→ 向会话级 store 登记调度
		// (Retry-After 未钳制,供后续调用短路);工具内仅当等待 ≤10s 时退避
		// 重试一次——服务端要求更长等待时直接交给模型决策,不空耗 turn。
		if attempt == 0 && isRateLimitedStatus(resp.StatusCode, resp.Header) {
			wait := retryAfterDuration(resp.Header, webFetchRetryBackoff)
			raw := rawRetryAfterDelay(resp.Header)
			var ext time.Time
			if raw > 0 {
				ext = time.Now().Add(raw)
			}
			throttleUntil, throttleConsec = throttle.ReportRateLimited(host, ext, time.Now())
			if raw > throttleMaxInToolWait {
				break
			}
			retried = true
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
			continue
		}
		break
	}
	duration := time.Since(start)

	// ── Step 6: 检查 Content-Type ──
	contentType := resp.Header.Get("Content-Type")
	if !isTextContentType(contentType) {
		return toolError(ErrorClassRecoverable, ErrKindBinaryFile,
			fmt.Sprintf("unsupported content type: %s (only text/*, application/json, application/xml, application/javascript are supported). Use web_search to find a text-friendly version of this resource",
				contentType), nil), nil
	}

	// ── Step 8: HTTP 状态码检查 ──
	if resp.StatusCode >= 400 {
		preview := formatBodyPreview(bodyBytes, 500)
		// 403 无 Retry-After → 策略封锁(非限流):登记有界冷却并明示重试无意义
		if resp.StatusCode == http.StatusForbidden && resp.Header.Get("Retry-After") == "" {
			until, consec := throttle.ReportBlocked(host, time.Now())
			msg := fmt.Sprintf("HTTP 403 %s — 目标站策略封锁(非限流头)。连续失败 %d 次,冷却至 %s(UTC)。重试同一 URL 无意义——换源、换抓取方式,或冷却结束后再试",
				resp.Status, consec, until.UTC().Format(time.RFC3339))
			return &ToolResult{
				Content: fmt.Sprintf("HTTP %d %s\nURL: %s\n\n%s",
					resp.StatusCode, resp.Status, p.URL, preview),
				Meta: ToolMeta{
					Duration:  duration,
					ByteCount: len(bodyBytes),
				},
				Error: &ToolError{
					Class:   ErrorClassRecoverable,
					Kind:    ErrKindBlocked,
					Message: msg,
				},
			}, nil
		}
		// 限流类(429/503/403+RA):错误携带会话级连续次数与最早可重试时间
		if isRateLimitedStatus(resp.StatusCode, resp.Header) {
			verb := ""
			if retried {
				verb = "重试一次仍"
			}
			msg := fmt.Sprintf("HTTP %d %s (%s限流)。连续失败 %d 次,最早可重试 %s(UTC)。到点前重试同一 URL 大概率仍失败——先处理其他任务或换源",
				resp.StatusCode, resp.Status, verb, throttleConsec, throttleUntil.UTC().Format(time.RFC3339))
			return &ToolResult{
				Content: fmt.Sprintf("HTTP %d %s\nURL: %s\n\n%s",
					resp.StatusCode, resp.Status, p.URL, preview),
				Meta: ToolMeta{
					Duration:  duration,
					ByteCount: len(bodyBytes),
				},
				Error: &ToolError{
					Class:   ErrorClassRecoverable,
					Kind:    ErrKindRateLimited,
					Message: msg,
				},
			}, nil
		}
		msg := fmt.Sprintf("HTTP %d %s. Use web_search to find an alternative URL if the page is unavailable", resp.StatusCode, resp.Status)
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
				Message: msg,
			},
		}, nil

	}
	// 成功响应:清零会话级限流状态(连续失败计数/退避/封锁)。
	throttle.ReportSuccess(host)
	// ── Step 9: 文本提取 ──
	bodyText := string(bodyBytes)
	if isHTMLContentType(contentType) {
		bodyText = stripHTML(bodyText)
	}

	// ── Step 10: 格式化输出 ──
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Fetched %s  HTTP %d", p.URL, resp.StatusCode)
	fmt.Fprintf(&buf, "  %s", duration.Round(time.Millisecond))
	fmt.Fprintf(&buf, "\nContent-Type: %s", contentType)
	fmt.Fprintf(&buf, "\nSize: %s", formatSize(int64(len(bodyBytes))))
	if truncated {
		fmt.Fprintf(&buf, " [truncated from > %s]", formatSize(int64(maxSize)))
	}
	if timeoutTruncated {
		fmt.Fprintf(&buf, " [truncated: %v]", readErr)
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
	// 缺失 Content-Type 头时按 text/plain 容错处理
	if contentType == "" {
		return true
	}
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
	return html.UnescapeString(strings.Join(cleaned, "\n"))
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
// ctx 取消时返回已读取的部分数据，超时场景不浪费已下载内容。
func readHTTPBodyWithContext(ctx context.Context, reader io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	chunk := make([]byte, 64*1024) // 64KB chunks

	for {
		if err := ctx.Err(); err != nil {
			return buf.Bytes(), err
		}
		n, readErr := reader.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return buf.Bytes(), readErr
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

