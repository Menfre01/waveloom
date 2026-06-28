package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// openAIAdapter 实现 providerAdapter，使用标准 OpenAI Chat Completions 协议。
// 同时作为所有未识别 ProviderType 的默认兜底 adapter。
type openAIAdapter struct {
	model          string
	apiKey         string
	baseURL        string
	extraParams    map[string]any
	headers        map[string]string
	responseFormat string
}

func newOpenAIAdapter(cfg ClientConfig) *openAIAdapter {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &openAIAdapter{
		model:          cfg.Model,
		apiKey:         cfg.APIKey,
		baseURL:        baseURL,
		extraParams:    cfg.ExtraParams,
		headers:        cfg.Headers,
		responseFormat: cfg.ResponseFormat,
	}
}

func (a *openAIAdapter) BaseURL() string {
	return a.baseURL
}

func (a *openAIAdapter) AuthHeader() (string, string) {
	return "Authorization", "Bearer " + a.apiKey
}

func (a *openAIAdapter) BuildRequest(messages []Message, tools []ToolSpec) (*http.Request, error) {
	body := a.buildRequestBody(messages, tools, false)
	return newJSONRequest(http.MethodPost, a.baseURL+"/chat/completions", body)
}

func (a *openAIAdapter) BuildStreamRequest(messages []Message, tools []ToolSpec) (*http.Request, error) {
	body := a.buildRequestBody(messages, tools, true)
	return newJSONRequest(http.MethodPost, a.baseURL+"/chat/completions", body)
}

// buildRequestBody 构造请求 body 公共逻辑。
func (a *openAIAdapter) buildRequestBody(messages []Message, tools []ToolSpec, stream bool) map[string]any {
	body := make(map[string]any)
	body["model"] = a.model
	body["messages"] = messages
	body["stream"] = stream

	if len(tools) > 0 {
		body["tools"] = buildToolsJSON(tools)
	}

	// 合并 ExtraParams 到 body 顶层
	for k, v := range a.extraParams {
		body[k] = v
	}

	if a.responseFormat == "json_object" {
		body["response_format"] = map[string]string{"type": "json_object"}
	}

	return body
}

func (a *openAIAdapter) ParseResponse(body []byte) (*Response, error) {
	var resp openAIResponse
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
		FinishReason: choice.FinishReason,
		Model:        resp.Model,
	}

	// 提取 content
	result.Content = choice.Message.Content

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
		}
	}

	return result, nil
}

func (a *openAIAdapter) ClassifyError(err error) ErrorClass {
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
		case 401, 403, 404, 400:
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

// buildToolsJSON 将 []ToolSpec 转为 OpenAI tools 格式。
func buildToolsJSON(tools []ToolSpec) []map[string]any {
	result := make([]map[string]any, len(tools))
	for i, t := range tools {
		result[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
			},
		}
	}
	return result
}

// newJSONRequest 创建一个 JSON HTTP 请求。
// 使用 bytes.NewReader 设置 body，自动支持 GetBody 用于重试。
func newJSONRequest(method, url string, body map[string]any) (*http.Request, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling request body: %w", err)
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (a *openAIAdapter) ParseStreamEvent(data []byte) (StreamingEvent, error) {
	var chunk openAIStreamChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return StreamingEvent{}, fmt.Errorf("parsing stream chunk: %w", err)
	}

	if len(chunk.Choices) == 0 {
		return StreamingEvent{}, nil
	}

	choice := chunk.Choices[0]
	ev := StreamingEvent{
		Delta:        choice.Delta.Content,
		FinishReason: choice.FinishReason,
		Model:        chunk.Model,
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
		}
	}

	return ev, nil
}

// --- OpenAI 响应解析结构 ---

// openAIStreamChunk SSE 流式 chunk 结构。
type openAIStreamChunk struct {
	Choices []openAIStreamChoice `json:"choices"`
	Usage   *openAIUsage         `json:"usage"`
	Model   string               `json:"model"` // 首帧携带
}

type openAIStreamChoice struct {
	FinishReason string            `json:"finish_reason"`
	Delta        openAIStreamDelta `json:"delta"`
}

type openAIStreamDelta struct {
	Content   string                 `json:"content"`
	ToolCalls []openAIStreamToolCall `json:"tool_calls"`
}

type openAIStreamToolCall struct {
	Index    int                  `json:"index"`
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function openAIFunctionCall   `json:"function"`
}

// --- OpenAI 非流式响应解析结构 (原) ---

type openAIResponse struct {
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage"`
	Model   string         `json:"model"`
}

type openAIChoice struct {
	FinishReason string          `json:"finish_reason"`
	Message      openAIMessage   `json:"message"`
}

type openAIMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	ToolCalls []openAIToolCall `json:"tool_calls"`
}

type openAIToolCall struct {
	ID       string              `json:"id"`
	Type     string              `json:"type"`
	Function openAIFunctionCall  `json:"function"`
}

type openAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// GetBalance OpenAI 不支持余额查询接口，返回 nil, nil。
func (a *openAIAdapter) GetBalance(ctx context.Context, httpClient *http.Client) (*BalanceInfo, error) {
	return nil, nil
}

// SupportsBalance OpenAI 不支持余额查询。
func (a *openAIAdapter) SupportsBalance() bool { return false }

// ListModels 通过 OpenAI API 获取可用模型列表。
// 端点: GET {baseURL}/models
func (a *openAIAdapter) ListModels(ctx context.Context, httpClient *http.Client) ([]ModelInfo, error) {
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
	defer resp.Body.Close()

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
