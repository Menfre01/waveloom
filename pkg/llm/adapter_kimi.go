package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// kimiAdapter 实现 providerAdapter，使用 Kimi 的 OpenAI 兼容 API。
// Kimi API 端点已包含 /v1 前缀（https://api.kimi.com/coding/v1），
// 路径拼接方式同 OpenAI adapter。
type kimiAdapter struct {
	model          string
	apiKey         string
	baseURL        string
	extraParams    map[string]any
	headers        map[string]string
	responseFormat string
}

func newKimiAdapter(cfg ClientConfig) *kimiAdapter {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.kimi.com/coding/v1"
	}
	return &kimiAdapter{
		model:          cfg.Model,
		apiKey:         cfg.APIKey,
		baseURL:        baseURL,
		extraParams:    cfg.ExtraParams,
		headers:        cfg.Headers,
		responseFormat: cfg.ResponseFormat,
	}
}

func (a *kimiAdapter) BaseURL() string {
	return a.baseURL
}

func (a *kimiAdapter) AuthHeader() (string, string) {
	return "Authorization", "Bearer " + a.apiKey
}

func (a *kimiAdapter) BuildRequest(ctx context.Context, messages []Message, tools []ToolSpec) (*http.Request, error) {
	body := a.buildRequestBody(ctx, messages, tools, false)
	return newJSONRequest(http.MethodPost, a.baseURL+"/chat/completions", body)
}

func (a *kimiAdapter) BuildStreamRequest(ctx context.Context, messages []Message, tools []ToolSpec) (*http.Request, error) {
	body := a.buildRequestBody(ctx, messages, tools, true)
	return newJSONRequest(http.MethodPost, a.baseURL+"/chat/completions", body)
}

// kimiFixedParams Kimi K3 固定不可改的参数，传入其他值报错。
// 为避免用户从 DeepSeek 配置迁移时触发 API 400，adapter 静默过滤。
var kimiFixedParams = map[string]bool{
	"temperature":       true,
	"thinking":          true, // DeepSeek 参数，Kimi 使用 reasoning_effort
	"top_p":             true,
	"n":                 true,
	"presence_penalty":  true,
	"frequency_penalty": true,
}

// buildRequestBody 构造请求 body 公共逻辑。
func (a *kimiAdapter) buildRequestBody(ctx context.Context, messages []Message, tools []ToolSpec, stream bool) map[string]any {
	body := make(map[string]any)
	if override := ModelOverrideFromContext(ctx); override != "" {
		body["model"] = override
	} else {
		body["model"] = a.model
	}

	// Kimi K3 要求原样回传完整的 assistant message（含 reasoning_content），
	// 不剥离 reasoning。与 DeepSeek adapter 不同，此处直接传递 messages。
	body["messages"] = messages
	body["stream"] = stream

	if len(tools) > 0 {
		body["tools"] = buildToolsJSON(tools)
	}

	// Kimi K3 使用顶层 reasoning_effort；始终启用思考，当前唯一支持 "max"。
	// 如果用户通过 ExtraParams 显式传入 reasoning_effort，保留用户值；
	// 否则注入默认 "max"。
	body["reasoning_effort"] = "max"

	// 合并 ExtraParams（用户传入的 reasoning_effort 可覆盖上述默认值）
	for k, v := range a.extraParams {
		body[k] = v
	}

	// 静默过滤 Kimi 固定不可改的参数
	for k := range kimiFixedParams {
		delete(body, k)
	}

	// Kimi 流式响应默认不在最后一帧返回 usage；必须显式要求
	// include_usage 才能获取 prompt/completion/cached_tokens 等统计。
	// 这是 TUI 上 ctx 进度条与 cache 数值的来源。
	if stream {
		body["stream_options"] = map[string]any{"include_usage": true}
	}

	if a.responseFormat == "json_object" {
		body["response_format"] = map[string]string{"type": "json_object"}
	}

	return body
}

func (a *kimiAdapter) ParseResponse(body []byte) (*Response, error) {
	var resp kimiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, &RetryableError{
			Message: fmt.Sprintf("malformed response: %v", err),
			Cause:   err,
		}
	}

	if len(resp.Choices) == 0 {
		return nil, &NonRetryableError{
			Message: "response has no choices",
		}
	}

	choice := resp.Choices[0]
	result := &Response{
		FinishReason:     choice.FinishReason,
		Content:          choice.Message.Content,
		ReasoningContent: choice.Message.ReasoningContent,
		Model:            resp.Model,
	}

	// 提取 tool_calls
	for _, tc := range choice.Message.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	// 提取 usage：Kimi 仅返回 cached_tokens 单一字段，不区分 hit/miss。
	// 用 prompt_tokens - cached_tokens 估算 cache miss，使 TUI 命中率显示合理。
	if resp.Usage != nil {
		cacheHit := resp.Usage.CachedTokens
		result.Usage = &UsageInfo{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
			CacheHitTokens:   cacheHit,
			CacheMissTokens:  max(0, resp.Usage.PromptTokens-cacheHit),
			// ReasoningTokens 保持 0 — Kimi 无此字段
		}
	}

	return result, nil

}

func (a *kimiAdapter) ClassifyError(err error) ErrorClass {
	// 已分类的错误直接返回
	var re *RetryableError
	if errors.As(err, &re) {
		return ErrorClassRetryable
	}
	var nre *NonRetryableError
	if errors.As(err, &nre) {
		return ErrorClassNonRetryable
	}

	// HTTP 状态码分类
	var httpErr *httpStatusError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case 429:
			return ErrorClassRetryable
		case 401, 402, 400:
			return ErrorClassNonRetryable
		default:
			if httpErr.StatusCode >= 500 {
				return ErrorClassRetryable
			}
			return ErrorClassNonRetryable
		}
	}

	// 网络错误默认可重试
	return ErrorClassRetryable
}

func (a *kimiAdapter) ParseStreamEvent(data []byte) (StreamingEvent, error) {
	var chunk kimiStreamChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return StreamingEvent{}, fmt.Errorf("parsing stream chunk: %w", err)
	}

	if len(chunk.Choices) == 0 {
		// OpenAI 兼容协议：stream_options.include_usage=true 时会在最后发送一个
		// choices 为空的额外 chunk，仅携带 usage 字段。必须解析该 usage，
		// 否则 TUI 的 ctx 进度条和 cache 数值拿不到 token 统计。
		if chunk.Usage != nil {
			cacheHit := chunk.Usage.CachedTokens
			return StreamingEvent{Usage: &UsageInfo{
				PromptTokens:     chunk.Usage.PromptTokens,
				CompletionTokens: chunk.Usage.CompletionTokens,
				TotalTokens:      chunk.Usage.TotalTokens,
				CacheHitTokens:   cacheHit,
			CacheMissTokens:  max(0, chunk.Usage.PromptTokens-cacheHit),
			}}, nil
		}
		return StreamingEvent{}, nil
	}

	choice := chunk.Choices[0]
	ev := StreamingEvent{
		Delta:          choice.Delta.Content,
		ReasoningDelta: choice.Delta.ReasoningContent,
		FinishReason:   choice.FinishReason,
		Model:          chunk.Model,
	}

	// 提取 delta 中的 tool_calls（含 index 用于累积）
	for _, tc := range choice.Delta.ToolCalls {
		ev.ToolCalls = append(ev.ToolCalls, ToolCall{
			Index:     tc.Index,
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	// 提取 usage（仅在最后一帧携带）；Kimi 仅返回 cached_tokens
	if chunk.Usage != nil {
		cacheHit := chunk.Usage.CachedTokens
		ev.Usage = &UsageInfo{
			PromptTokens:     chunk.Usage.PromptTokens,
			CompletionTokens: chunk.Usage.CompletionTokens,
			TotalTokens:      chunk.Usage.TotalTokens,
			CacheHitTokens:   cacheHit,
			CacheMissTokens:  max(0, chunk.Usage.PromptTokens-cacheHit),
		}
	}

	return ev, nil
}

// --- Kimi 响应解析结构 ---

// kimiStreamChunk SSE 流式 chunk 结构。
type kimiStreamChunk struct {
	Choices []kimiStreamChoice `json:"choices"`
	Usage   *kimiUsage         `json:"usage"` // 最后一帧携带
	Model   string             `json:"model"` // 首帧携带
}

type kimiStreamChoice struct {
	FinishReason string          `json:"finish_reason"`
	Delta        kimiStreamDelta `json:"delta"`
}

type kimiStreamDelta struct {
	Content          string                `json:"content"`
	ReasoningContent string                `json:"reasoning_content"`
	ToolCalls        []kimiStreamToolCall  `json:"tool_calls"`
}

type kimiStreamToolCall struct {
	Index    int               `json:"index"`
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Function kimiFunctionCall  `json:"function"`
}

// --- Kimi 非流式响应解析结构 ---

type kimiResponse struct {
	ID      string       `json:"id"`
	Choices []kimiChoice `json:"choices"`
	Usage   *kimiUsage   `json:"usage"`
	Model   string       `json:"model"`
}

type kimiChoice struct {
	FinishReason string      `json:"finish_reason"`
	Message      kimiMessage `json:"message"`
}

type kimiMessage struct {
	Role             string          `json:"role"`
	Content          string          `json:"content"`
	ReasoningContent string          `json:"reasoning_content"`
	ToolCalls        []kimiToolCall  `json:"tool_calls"`
}

type kimiToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function kimiFunctionCall `json:"function"`
}

type kimiFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// kimiUsage Kimi API 的 usage 结构。cached_tokens 为单一缓存命中 token 数，
// 不区分 hit/miss，也无 completion_tokens_details。
type kimiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CachedTokens     int `json:"cached_tokens"`
}

// --- Kimi 余额查询 ---

// kimiBalanceResponse Kimi /v1/users/me/balance 响应结构。
type kimiBalanceResponse struct {
	Code   int              `json:"code"`
	Data   kimiBalanceData  `json:"data"`
	SCode  string           `json:"scode"`
	Status bool             `json:"status"`
}

type kimiBalanceData struct {
	AvailableBalance float64 `json:"available_balance"`
	VoucherBalance   float64 `json:"voucher_balance"`
	CashBalance      float64 `json:"cash_balance"`
}

// GetBalance 查询 Kimi 账户余额。
// 端点: GET /v1/users/me/balance（baseURL 已含 /v1）
func (a *kimiAdapter) GetBalance(ctx context.Context, httpClient *http.Client) (*BalanceInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"/users/me/balance", nil)
	if err != nil {
		return nil, fmt.Errorf("creating balance request: %w", err)
	}
	key, value := a.AuthHeader()
	req.Header.Set(key, value)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("balance request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading balance response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("balance request HTTP %d: %s", resp.StatusCode, string(body))
	}

	var br kimiBalanceResponse
	if err := json.Unmarshal(body, &br); err != nil {
		return nil, fmt.Errorf("parsing balance response: %w", err)
	}

	// 映射：available_balance → TotalBalance, voucher_balance → GrantedBalance,
	// cash_balance → ToppedUpBalance。Kimi 仅支持人民币。
	result := &BalanceInfo{
		IsAvailable: br.Data.AvailableBalance > 0,
		BalanceInfos: []CurrencyBalance{{
			Currency:        "CNY",
			TotalBalance:    fmt.Sprintf("%.5f", br.Data.AvailableBalance),
			GrantedBalance:  fmt.Sprintf("%.5f", br.Data.VoucherBalance),
			ToppedUpBalance: fmt.Sprintf("%.5f", br.Data.CashBalance),
		}},
	}
	return result, nil
}

// SupportsBalance Kimi 支持余额查询。
func (a *kimiAdapter) SupportsBalance() bool { return true }

// ListModels 通过 Kimi API 获取可用模型列表。
// 端点: GET {baseURL}/models
func (a *kimiAdapter) ListModels(ctx context.Context, httpClient *http.Client) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("creating list models request: %w", err)
	}
	key, value := a.AuthHeader()
	req.Header.Set(key, value)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list models request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading list models response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list models HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Object string      `json:"object"`
		Data   []ModelInfo `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing list models response: %w", err)
	}

	return result.Data, nil
}
