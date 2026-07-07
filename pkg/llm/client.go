package llm

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// httpStatusError 封装非 2xx HTTP 响应，供 ClassifyError 判断。
type httpStatusError struct {
	StatusCode int
	Body       string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

// Client 向 LLM 发送消息并返回响应。
// 重试策略、指数退避、错误分类等均在实现内部处理。
// Loop 通过此接口使用 LLM，对 Provider 差异完全无感知。
type Client interface {
	SendMessage(ctx context.Context, messages []Message, tools []ToolSpec) (*Response, error)
	SendMessageStream(ctx context.Context, messages []Message, tools []ToolSpec) (<-chan StreamingEvent, error)

	// GetBalance 查询账户余额。部分 Provider 不支持，此时返回 nil, nil。
	GetBalance(ctx context.Context) (*BalanceInfo, error)

	// SupportsBalance 返回当前 Provider 是否支持余额查询。
	SupportsBalance() bool

	// ListModels 获取 Provider 支持的模型列表。
	// 对应 GET /models（DeepSeek）或 GET /models（OpenAI）。
	ListModels(ctx context.Context) ([]ModelInfo, error)
}

// client 是 Client 接口的内部实现。
type client struct {
	config     ClientConfig
	adapter    providerAdapter
	httpClient *http.Client
}

// NewClient 根据 ClientConfig 构造 Client 实例。
func NewClient(cfg ClientConfig) (Client, error) {
	if cfg.APIKey == "" {
		return nil, &NonRetryableError{Message: "API key is required"}
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 600 * time.Second // 对齐 DeepSeek 服务端保活超时
	}
	if cfg.RetryPolicy == (RetryPolicy{}) {
		cfg.RetryPolicy = DefaultRetryPolicy()
	}

	var adapter providerAdapter
	switch cfg.Provider {
	case ProviderDeepSeek:
		adapter = newDeepSeekAdapter(cfg)
	case ProviderOpenAI:
		adapter = newOpenAIAdapter(cfg)
	case "":
		adapter = newDeepSeekAdapter(cfg)
	default:
		adapter = newOpenAIAdapter(cfg)
	}

	return newClientWithAdapter(cfg, adapter), nil
}

// newClientWithAdapter 使用指定 adapter 构造 Client（内部使用，测试可注入 spy adapter）。
func newClientWithAdapter(cfg ClientConfig, adapter providerAdapter) Client {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: cfg.Timeout,
		}
	}
	return &client{
		config:     cfg,
		adapter:    adapter,
		httpClient: httpClient,
	}
}

// SendMessage 向 LLM 发送消息并返回响应。
func (c *client) SendMessage(ctx context.Context, messages []Message, tools []ToolSpec) (*Response, error) {
	if ctx == nil {
		return nil, &NonRetryableError{Message: "context must not be nil"}
	}
	if len(messages) == 0 {
		return nil, &NonRetryableError{Message: "messages must not be empty"}
	}
	if err := validateToolNames(tools); err != nil {
		return nil, err
	}

	req, err := c.adapter.BuildRequest(ctx, messages, tools)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req = req.WithContext(ctx)

	// 设置自定义请求头
	for k, v := range c.config.Headers {
		req.Header.Set(k, v)
	}
	// 设置认证头
	key, value := c.adapter.AuthHeader()
	req.Header.Set(key, value)

	return c.sendWithRetry(ctx, req)
}

// SendMessageStream 向 LLM 发送消息并以 SSE 流式接收响应。
// 返回一个只读 channel，调用方通过 range 消费 StreamingEvent。
// Channel 在流结束或出错后自动关闭。
func (c *client) SendMessageStream(ctx context.Context, messages []Message, tools []ToolSpec) (<-chan StreamingEvent, error) {
	if ctx == nil {
		return nil, &NonRetryableError{Message: "context must not be nil"}
	}
	if len(messages) == 0 {
		return nil, &NonRetryableError{Message: "messages must not be empty"}
	}
	if err := validateToolNames(tools); err != nil {
		return nil, err
	}

	req, err := c.adapter.BuildStreamRequest(ctx, messages, tools)
	if err != nil {
		return nil, fmt.Errorf("building stream request: %w", err)
	}
	req = req.WithContext(ctx)

	// 设置自定义请求头
	for k, v := range c.config.Headers {
		req.Header.Set(k, v)
	}
	// 设置认证头
	key, value := c.adapter.AuthHeader()
	req.Header.Set(key, value)

	ch := make(chan StreamingEvent, 16)
	go c.readStream(ctx, req, ch)
	return ch, nil
}

// GetBalance 查询账户余额。部分 Provider 不支持，返回 nil, nil。
// 与 SendMessage 使用同一个 httpClient，遵循相同的超时配置。
func (c *client) GetBalance(ctx context.Context) (*BalanceInfo, error) {
	if ctx == nil {
		return nil, &NonRetryableError{Message: "context must not be nil"}
	}
	return c.adapter.GetBalance(ctx, c.httpClient)
}

// SupportsBalance 委托给 adapter 判断。
func (c *client) SupportsBalance() bool {
	return c.adapter.SupportsBalance()
}

// ListModels 委托给 adapter 获取模型列表。
func (c *client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	if ctx == nil {
		return nil, &NonRetryableError{Message: "context must not be nil"}
	}
	return c.adapter.ListModels(ctx, c.httpClient)
}

// readStream 在后台 goroutine 中读取 SSE 流，解析增量事件并发送到 channel。
func (c *client) readStream(ctx context.Context, req *http.Request, ch chan<- StreamingEvent) {
	defer close(ch)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		ch <- StreamingEvent{Done: true, Err: &RetryableError{
			Message: fmt.Sprintf("network error: %v", err),
			Cause:   err,
		}}
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		ch <- StreamingEvent{Done: true, Err: &httpStatusError{
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}}
		return
	}

	// 当 ctx 取消时中断阻塞的流读取：关闭 body 使 scanner.Scan() 从 Read 中返回。
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()
	go func() {
		<-streamCtx.Done()
		_ = resp.Body.Close()
	}()

	scanner := bufio.NewScanner(resp.Body)
	// SSE 行可以很长（某行可能包含完整 JSON），设置足够大的 buffer
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var acc streamAccumulator
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		if data == "[DONE]" {
			ch <- acc.final()
			return
		}

		ev, err := c.adapter.ParseStreamEvent([]byte(data))
		if err != nil {
			// 跳过无法解析的 chunk，继续消费后续数据
			continue
		}
		acc.accumulate(ev)
		if ev.Delta != "" || ev.ReasoningDelta != "" {
			ch <- ev
		}

		// 快速路径：数据仍在到达但 ctx 已取消
		select {
		case <-ctx.Done():
			ch <- StreamingEvent{Done: true, Err: ctx.Err()}
			return
		default:
		}
	}

	if err := scanner.Err(); err != nil {
		// 优先报告 ctx 取消（body close 导致的读取中断），否则报告 IO 错误
		if ctxErr := ctx.Err(); ctxErr != nil {
			ch <- StreamingEvent{Done: true, Err: ctxErr}
		} else {
			ch <- StreamingEvent{Done: true, Err: &RetryableError{
				Message: fmt.Sprintf("stream read error: %v", err),
				Cause:   err,
			}}
		}
		return
	}

	// Scanner 正常结束但未收到 [DONE]（连接提前关闭）
	ch <- acc.final()
}

// streamAccumulator 在流式接收过程中累积 tool_calls。
// OpenAI/DeepSeek 流式协议中 tool_calls 以 index 分片到达，
// 同一 index 的多个 chunk 拼接成完整的 ToolCall。
type streamAccumulator struct {
	toolCallMap  map[int]*toolCallBuilder
	finishReason string
	lastUsage    *UsageInfo // 最后一帧携带的 token 用量（含缓存命中统计）
}

type toolCallBuilder struct {
	ID        string
	Name      string
	Arguments string
}

func (a *streamAccumulator) accumulate(ev StreamingEvent) {
	if ev.FinishReason != "" {
		a.finishReason = ev.FinishReason
	}
	if ev.Usage != nil {
		a.lastUsage = ev.Usage
	}
	if len(ev.ToolCalls) > 0 {
		if a.toolCallMap == nil {
			a.toolCallMap = make(map[int]*toolCallBuilder)
		}
		for _, tc := range ev.ToolCalls {
			if a.toolCallMap[tc.Index] == nil {
				a.toolCallMap[tc.Index] = &toolCallBuilder{}
			}
			b := a.toolCallMap[tc.Index]
			if tc.ID != "" {
				b.ID = tc.ID
			}
			if tc.Name != "" {
				b.Name = tc.Name
			}
			b.Arguments += tc.Arguments
		}
	}
}

func (a *streamAccumulator) final() StreamingEvent {
	ev := StreamingEvent{Done: true, FinishReason: a.finishReason, Usage: a.lastUsage}
	if a.finishReason == "" {
		ev.FinishReason = "stop"
	}
	if len(a.toolCallMap) > 0 {
		ev.ToolCalls = make([]ToolCall, 0, len(a.toolCallMap))
		for i := 0; i < len(a.toolCallMap); i++ {
			if b, ok := a.toolCallMap[i]; ok && b != nil {
				ev.ToolCalls = append(ev.ToolCalls, ToolCall{
					ID:        b.ID,
					Name:      b.Name,
					Arguments: b.Arguments,
				})
			}
		}
	}
	return ev
}

// sendWithRetry 执行带重试的请求。
func (c *client) sendWithRetry(ctx context.Context, req *http.Request) (*Response, error) {
	var lastErr error
	maxAttempts := c.config.RetryPolicy.MaxRetries + 1

	for attempt := 0; attempt <= c.config.RetryPolicy.MaxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, &NonRetryableError{
				Message: "request cancelled",
				Cause:   err,
			}
		}

		reqClone := req.Clone(ctx)
		resp, err := c.doRequest(reqClone)
		if err == nil {
			c.emitRetryEvent(ctx, RetryEvent{
				Attempt:     attempt,
				MaxAttempts: maxAttempts,
				Error:       nil,
				Backoff:     0,
				WillRetry:   false,
				Timestamp:   time.Now(),
			})
			return resp, nil
		}

		lastErr = err

		if c.adapter.ClassifyError(err) == ErrorClassNonRetryable {
			c.emitRetryEvent(ctx, RetryEvent{
				Attempt:     attempt,
				MaxAttempts: maxAttempts,
				Error:       err,
				Backoff:     0,
				WillRetry:   false,
				Timestamp:   time.Now(),
			})
			return nil, err
		}

		if attempt == c.config.RetryPolicy.MaxRetries {
			c.emitRetryEvent(ctx, RetryEvent{
				Attempt:     attempt,
				MaxAttempts: maxAttempts,
				Error:       err,
				Backoff:     0,
				WillRetry:   false,
				Timestamp:   time.Now(),
			})
			break
		}

		wait := c.config.RetryPolicy.ComputeBackoff(attempt, err)
		c.emitRetryEvent(ctx, RetryEvent{
			Attempt:     attempt,
			MaxAttempts: maxAttempts,
			Error:       err,
			Backoff:     wait,
			WillRetry:   true,
			Timestamp:   time.Now(),
		})
		select {
		case <-ctx.Done():
			return nil, &NonRetryableError{
				Message: "request cancelled during backoff",
				Cause:   ctx.Err(),
			}
		case <-time.After(wait):
		}
	}

	return nil, &NonRetryableError{
		Message: fmt.Sprintf("retry exhausted after %d attempts", c.config.RetryPolicy.MaxRetries+1),
		Cause:   lastErr,
	}
}

// emitRetryEvent 在 OnRetry 不为 nil 时触发重试事件。
func (c *client) emitRetryEvent(ctx context.Context, ev RetryEvent) {
	if c.config.OnRetry != nil {
		c.config.OnRetry(ctx, ev)
	}
}

// doRequest 执行单次 HTTP 请求。
func (c *client) doRequest(req *http.Request) (*Response, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &RetryableError{
			Message:    fmt.Sprintf("network error: %v", err),
			StatusCode: 0,
			Cause:      err,
		}
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &RetryableError{
			Message: fmt.Sprintf("reading response body: %v", err),
			Cause:   err,
		}
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return c.adapter.ParseResponse(body)
	}

	httpErr := &httpStatusError{
		StatusCode: resp.StatusCode,
		Body:       string(body),
	}

	// 429 含 Retry-After 头时包装为 RetryableError
	if resp.StatusCode == 429 {
		if ra := parseRetryAfter(resp.Header.Get("Retry-After")); ra > 0 {
			return nil, &RetryableError{
				Message:    fmt.Sprintf("rate limited (429): %s", string(body)),
				StatusCode: 429,
				RetryAfter: ra,
				Cause:      httpErr,
			}
		}
		return nil, &RetryableError{
			Message:    fmt.Sprintf("rate limited (429): %s", string(body)),
			StatusCode: 429,
			Cause:      httpErr,
		}
	}

	return nil, httpErr
}

// parseRetryAfter 解析 Retry-After 头，支持秒数和 HTTP 日期格式。
func parseRetryAfter(s string) time.Duration {
	if s == "" {
		return 0
	}
	// 先尝试解析为秒数
	if sec, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Duration(sec) * time.Second
	}
	// 再尝试 HTTP 日期格式
	if t, err := http.ParseTime(s); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}

// validateToolNames 校验工具名：清理非法字符，检查重名和长度。
func validateToolNames(tools []ToolSpec) error {
	seen := make(map[string]bool, len(tools))
	for _, t := range tools {
		cleaned := cleanToolName(t.Name)
		if cleaned == "" || len(cleaned) > 64 {
			return &NonRetryableError{
				Message: fmt.Sprintf("invalid tool name after cleaning: %q (original: %q)", cleaned, t.Name),
			}
		}
		if seen[cleaned] {
			return &NonRetryableError{
				Message: fmt.Sprintf("duplicate tool name after cleaning: %q (original: %q)", cleaned, t.Name),
			}
		}
		seen[cleaned] = true
	}
	return nil
}

// cleanToolName 清理工具名中的非法字符，仅保留 [a-zA-Z0-9_-]。
func cleanToolName(name string) string {
	var buf strings.Builder
	buf.Grow(len(name))
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			buf.WriteRune(r)
		}
	}
	return buf.String()
}

// 确保 client 实现 Client 接口。
var _ Client = (*client)(nil)
