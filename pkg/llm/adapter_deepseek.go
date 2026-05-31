package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// deepSeekAdapter 实现 providerAdapter，使用 DeepSeek 的 OpenAI 兼容 API。
type deepSeekAdapter struct {
	model          string
	apiKey         string
	baseURL        string
	extraParams    map[string]any
	headers        map[string]string
	responseFormat string
}

func newDeepSeekAdapter(cfg ClientConfig) *deepSeekAdapter {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	return &deepSeekAdapter{
		model:          cfg.Model,
		apiKey:         cfg.APIKey,
		baseURL:        baseURL,
		extraParams:    cfg.ExtraParams,
		headers:        cfg.Headers,
		responseFormat: cfg.ResponseFormat,
	}
}

func (a *deepSeekAdapter) BaseURL() string {
	return a.baseURL
}

func (a *deepSeekAdapter) AuthHeader() (string, string) {
	return "Authorization", "Bearer " + a.apiKey
}

func (a *deepSeekAdapter) BuildRequest(messages []Message, tools []ToolSpec) (*http.Request, error) {
	body := a.buildRequestBody(messages, tools, false)
	return newJSONRequest(http.MethodPost, a.baseURL+"/v1/chat/completions", body)
}

func (a *deepSeekAdapter) BuildStreamRequest(messages []Message, tools []ToolSpec) (*http.Request, error) {
	body := a.buildRequestBody(messages, tools, true)
	return newJSONRequest(http.MethodPost, a.baseURL+"/v1/chat/completions", body)
}

// buildRequestBody 构造请求 body 公共逻辑。
func (a *deepSeekAdapter) buildRequestBody(messages []Message, tools []ToolSpec, stream bool) map[string]any {
	body := make(map[string]any)
	body["model"] = a.model
	body["messages"] = messages
	body["stream"] = stream

	if len(tools) > 0 {
		body["tools"] = buildToolsJSON(tools)
	}

	// 合并 ExtraParams 到 body 顶层
	for k, v := range a.extraParams {
		if k == "reasoning_effort" {
			if s, ok := v.(string); ok {
				v = mapReasoningEffort(s)
			}
		}
		body[k] = v
	}

	if a.responseFormat == "json_object" {
		body["response_format"] = map[string]string{"type": "json_object"}
	}

	return body
}

func (a *deepSeekAdapter) ParseResponse(body []byte) (*Response, error) {
	var resp deepSeekResponse
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

	// insufficient_system_resource → Retryable
	if choice.FinishReason == "insufficient_system_resource" {
		return nil, &RetryableError{
			Message:    "insufficient system resource",
			StatusCode: 200,
		}
	}

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

	// 提取 usage
	if resp.Usage != nil {
		result.Usage = &UsageInfo{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
			CacheHitTokens:   resp.Usage.PromptCacheHitTokens,
			CacheMissTokens:  resp.Usage.PromptCacheMissTokens,
		}
		if resp.Usage.CompletionTokensDetails != nil {
			result.Usage.ReasoningTokens = resp.Usage.CompletionTokensDetails.ReasoningTokens
		}
	}

	return result, nil
}

func (a *deepSeekAdapter) ClassifyError(err error) ErrorClass {
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

// mapReasoningEffort 将 OpenAI 兼容值映射为 DeepSeek 支持的值。
// 官方文档：low/medium → high，xhigh → max。
func mapReasoningEffort(effort string) string {
	switch effort {
	case "low", "medium":
		return "high"
	case "xhigh":
		return "max"
	default:
		return effort
	}
}

func (a *deepSeekAdapter) ParseStreamEvent(data []byte) (StreamingEvent, error) {
	var chunk deepSeekStreamChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return StreamingEvent{}, fmt.Errorf("parsing stream chunk: %w", err)
	}

	if len(chunk.Choices) == 0 {
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

	// 提取 usage（仅在最后一帧携带）
	if chunk.Usage != nil {
		ev.Usage = &UsageInfo{
			PromptTokens:     chunk.Usage.PromptTokens,
			CompletionTokens: chunk.Usage.CompletionTokens,
			TotalTokens:      chunk.Usage.TotalTokens,
			CacheHitTokens:   chunk.Usage.PromptCacheHitTokens,
			CacheMissTokens:  chunk.Usage.PromptCacheMissTokens,
		}
		if chunk.Usage.CompletionTokensDetails != nil {
			ev.Usage.ReasoningTokens = chunk.Usage.CompletionTokensDetails.ReasoningTokens
		}
	}

	return ev, nil
}

// --- DeepSeek 响应解析结构 ---

// deepSeekStreamChunk SSE 流式 chunk 结构。
type deepSeekStreamChunk struct {
	Choices []deepSeekStreamChoice `json:"choices"`
	Usage   *deepSeekUsage         `json:"usage"` // 最后一帧携带
	Model   string                 `json:"model"` // 首帧携带
}

type deepSeekStreamChoice struct {
	FinishReason string             `json:"finish_reason"`
	Delta        deepSeekStreamDelta `json:"delta"`
}

type deepSeekStreamDelta struct {
	Content          string               `json:"content"`
	ReasoningContent string               `json:"reasoning_content"`
	ToolCalls        []deepSeekStreamToolCall `json:"tool_calls"`
}

type deepSeekStreamToolCall struct {
	Index    int                    `json:"index"`
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function deepSeekFunctionCall   `json:"function"`
}

// --- DeepSeek 非流式响应解析结构 (原) ---

type deepSeekResponse struct {
	Choices []deepSeekChoice `json:"choices"`
	Usage   *deepSeekUsage   `json:"usage"`
	Model   string           `json:"model"`
}

type deepSeekChoice struct {
	FinishReason string            `json:"finish_reason"`
	Message      deepSeekMessage   `json:"message"`
}

type deepSeekMessage struct {
	Role             string               `json:"role"`
	Content          string               `json:"content"`
	ReasoningContent string               `json:"reasoning_content"`
	ToolCalls        []deepSeekToolCall   `json:"tool_calls"`
}

type deepSeekToolCall struct {
	ID       string                  `json:"id"`
	Type     string                  `json:"type"`
	Function deepSeekFunctionCall    `json:"function"`
}

type deepSeekFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type deepSeekUsage struct {
	PromptTokens          int                          `json:"prompt_tokens"`
	CompletionTokens      int                          `json:"completion_tokens"`
	TotalTokens           int                          `json:"total_tokens"`
	PromptCacheHitTokens  int                          `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int                          `json:"prompt_cache_miss_tokens"`
	CompletionTokensDetails *deepSeekCompletionDetails `json:"completion_tokens_details"`
}

type deepSeekCompletionDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// --- DeepSeek 余额查询 ---

// deepSeekBalanceResponse DeepSeek /user/balance 响应结构。
type deepSeekBalanceResponse struct {
	IsAvailable  bool                       `json:"is_available"`
	BalanceInfos []deepSeekCurrencyBalance  `json:"balance_infos"`
}

type deepSeekCurrencyBalance struct {
	Currency        string `json:"currency"`
	TotalBalance    string `json:"total_balance"`
	GrantedBalance  string `json:"granted_balance"`
	ToppedUpBalance string `json:"topped_up_balance"`
}

// GetBalance 查询 DeepSeek 账户余额。
// 端点: GET /user/balance
func (a *deepSeekAdapter) GetBalance(ctx context.Context, httpClient *http.Client) (*BalanceInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"/user/balance", nil)
	if err != nil {
		return nil, fmt.Errorf("creating balance request: %w", err)
	}
	key, value := a.AuthHeader()
	req.Header.Set(key, value)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("balance request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading balance response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("balance request HTTP %d: %s", resp.StatusCode, string(body))
	}

	var br deepSeekBalanceResponse
	if err := json.Unmarshal(body, &br); err != nil {
		return nil, fmt.Errorf("parsing balance response: %w", err)
	}

	result := &BalanceInfo{
		IsAvailable:  br.IsAvailable,
		BalanceInfos: make([]CurrencyBalance, len(br.BalanceInfos)),
	}
	for i, b := range br.BalanceInfos {
		result.BalanceInfos[i] = CurrencyBalance{
			Currency:        b.Currency,
			TotalBalance:    b.TotalBalance,
			GrantedBalance:  b.GrantedBalance,
			ToppedUpBalance: b.ToppedUpBalance,
		}
	}
	return result, nil
}

// SupportsBalance DeepSeek 支持余额查询。
func (a *deepSeekAdapter) SupportsBalance() bool { return true }
