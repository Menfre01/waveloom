package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
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

func (a *deepSeekAdapter) BuildRequest(ctx context.Context, messages []Message, tools []ToolSpec) (*http.Request, error) {
	if a.effectiveModel(ctx) == ModelDeepSeekV4Flash {
		body := a.buildResponsesRequestBody(ctx, messages, tools, false)
		return newJSONRequest(http.MethodPost, a.baseURL+"/v1/responses", body)
	}
	body := a.buildChatRequestBody(ctx, messages, tools, false)
	return newJSONRequest(http.MethodPost, a.baseURL+"/v1/chat/completions", body)
}

func (a *deepSeekAdapter) BuildStreamRequest(ctx context.Context, messages []Message, tools []ToolSpec) (*http.Request, error) {
	if a.effectiveModel(ctx) == ModelDeepSeekV4Flash {
		body := a.buildResponsesRequestBody(ctx, messages, tools, true)
		return newJSONRequest(http.MethodPost, a.baseURL+"/v1/responses", body)
	}
	body := a.buildChatRequestBody(ctx, messages, tools, true)
	return newJSONRequest(http.MethodPost, a.baseURL+"/v1/chat/completions", body)
}

// effectiveModel 返回实际生效的模型名:ctx 中的 ModelOverride 优先,否则用配置模型。
func (a *deepSeekAdapter) effectiveModel(ctx context.Context) string {
	if override := ModelOverrideFromContext(ctx); override != "" {
		return override
	}
	return a.model
}

// buildChatRequestBody 构造 Chat Completions 请求 body(非 Responses API 模式)。
func (a *deepSeekAdapter) buildChatRequestBody(ctx context.Context, messages []Message, tools []ToolSpec, stream bool) map[string]any {
	body := make(map[string]any)
	body["model"] = a.effectiveModel(ctx)
	body["messages"] = stripReasoningWithoutToolCalls(messages)
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

	// per-request max_tokens 覆盖(如压缩摘要需显式输出上限,
	// 否则服务端默认上限可能截断长 JSON 输出)
	if v, ok := MaxTokensFromContext(ctx); ok {
		body["max_tokens"] = v
	}

	return body
}

// ---------------------------------------------------------------------------
// Responses API(deepseek-v4-flash)
// ---------------------------------------------------------------------------

// buildResponsesRequestBody 构造 Responses API 请求 body。
// 端点 POST /v1/responses;messages 转为 input items,首条 system 提取为 instructions。
func (a *deepSeekAdapter) buildResponsesRequestBody(ctx context.Context, messages []Message, tools []ToolSpec, stream bool) map[string]any {
	body := make(map[string]any)
	body["model"] = a.effectiveModel(ctx)

	instructions, input := buildResponsesInput(messages)
	if instructions != "" {
		body["instructions"] = instructions
	}
	body["input"] = input

	if len(tools) > 0 {
		body["tools"] = buildResponsesTools(tools)
	}
	body["stream"] = stream

	// extra_params:reasoning_effort → reasoning.effort(原始值,不经 chat 模式的
	// mapReasoningEffort 重映射——Responses API 接受 OpenAI 兼容的 low/medium/high)
	for k, v := range a.extraParams {
		switch k {
		case "reasoning_effort":
			if s, ok := v.(string); ok {
				body["reasoning"] = map[string]any{"effort": s}
			}
		case "max_tokens":
			// chat 专用参数;Responses API 用 max_output_tokens
			if n, ok := v.(int); ok && n > 0 {
				body["max_output_tokens"] = n
			}
		default:
			body[k] = v // 不支持的参数服务端静默忽略
		}
	}

	// response_format(json_object)→ text.format(Responses API 格式)
	// 注意:若 ExtraParams 同时含 "text"(如 json_schema 配置),此处覆盖,
	// 配置层 responseFormat 优先(当前仅 json_object 已实现)。
	if a.responseFormat == "json_object" {
		body["text"] = map[string]any{"format": map[string]string{"type": "json_object"}}
	}

	// per-request max_tokens 覆盖(如压缩摘要)映射为 max_output_tokens
	if v, ok := MaxTokensFromContext(ctx); ok {
		body["max_output_tokens"] = v
	}

	return body
}

// buildResponsesInput 将内部 Message 列表转为 Responses API input items。
// 返回 (instructions, input):首条 system 消息提取为 instructions 字段,
// 其余消息转为平级 input items(与 OpenAI Responses 协议一致)。
func buildResponsesInput(messages []Message) (string, []any) {
	var input []any
	instructions := ""
	for i, m := range messages {
		switch m.Role {
		case RoleSystem:
			if i == 0 && instructions == "" {
				instructions = m.Content
				continue
			}
			input = append(input, map[string]any{
				"role":    "system",
				"content": []any{map[string]any{"type": "input_text", "text": m.Content}},
			})
		case RoleUser:
			input = append(input, map[string]any{
				"role":    "user",
				"content": []any{map[string]any{"type": "input_text", "text": m.Content}},
			})
		case RoleAssistant:
			input = append(input, map[string]any{
				"role":    "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": m.Content}},
			})
			// 有 tool_calls 时回传 reasoning(明文归并到相邻 assistant 消息,
			// 与不带 tool_calls 时省略 reasoning 的策略一致:减少冗余 token)
			if len(m.ToolCalls) > 0 {
				if m.ReasoningContent != "" {
					// REGRESSION: 原实现发 summary[summary_text],但 DeepSeek
					// 文档明确不支持 summary,回传的思维链被服务端忽略。
					// 正确格式为 content[reasoning_text]。
					input = append(input, map[string]any{
						"type": "reasoning",
						"content": []any{
							map[string]any{"type": "reasoning_text", "text": m.ReasoningContent},
						},
					})
				}
				for _, tc := range m.ToolCalls {
					input = append(input, map[string]any{
						"type":      "function_call",
						"call_id":   tc.ID,
						"name":      tc.Name,
						"arguments": tc.Arguments,
					})
				}
			}
			// 服务端 web_search 输出 item 原样回传(多轮对话恢复搜索结果上下文)
			for _, wsc := range m.WebSearchCalls {
				item := map[string]any{"type": "web_search_call", "id": wsc.ID}
				// Status 为空时省略字段(resume 恢复的旧 JSONL 可能无 status;
				// OpenAI 协议中 status 可选,避免发送空字符串被服务端拒绝)
				if wsc.Status != "" {
					item["status"] = wsc.Status
				}
				// Action 原样透传(文档要求"原样回传",服务端据此恢复搜索上下文;
				// json.RawMessage 序列化时内联原始 JSON)
				// "null" 字节守卫:避免显式 null 序列化为 "action":null 发送
				if len(wsc.Action) > 0 && string(wsc.Action) != "null" {
					item["action"] = wsc.Action
				}
				input = append(input, item)
			}
		case RoleTool:
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": m.ToolCallID,
				"output":  m.Content,
			})
		}
	}
	return instructions, input
}

// buildResponsesTools 将内部 ToolSpec 列表转为 Responses API tools 声明。
// web_search 本地工具转为服务端内置工具声明 {type: "web_search"}(服务端自动执行,
// 不暴露 function schema),其余工具转为 function 声明。
func buildResponsesTools(tools []ToolSpec) []any {
	var result []any
	hasWebSearch := false
	for _, t := range tools {
		if t.Name == "web_search" {
			hasWebSearch = true
			continue
		}
		result = append(result, map[string]any{
			"type":        "function",
			"name":        t.Name,
			"description": t.Description,
			"parameters":  t.Parameters,
		})
	}
	if hasWebSearch {
		result = append(result, map[string]any{"type": "web_search"})
	}
	return result
}

func (a *deepSeekAdapter) ParseResponse(body []byte) (*Response, error) {
	if isResponsesResponse(body) {
		return a.parseResponsesResponse(body)
	}
	return a.parseChatResponse(body)
}

// isResponsesResponse 探测响应是否为 Responses API 格式(非流式)。
// Responses 响应带 "object": "response",chat 响应带 "choices"。
func isResponsesResponse(body []byte) bool {
	var probe struct {
		Object  string `json:"object"`
		Choices *[]any `json:"choices"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return false
	}
	return probe.Object == "response" && probe.Choices == nil
}

// parseResponsesResponse 解析 Responses API 非流式响应。
// output items: message(文本) / function_call(工具调用) / reasoning(思考链) /
// web_search_call(服务端自动执行,忽略)。
func (a *deepSeekAdapter) parseResponsesResponse(body []byte) (*Response, error) {
	var resp responsesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, &RetryableError{
			Message: fmt.Sprintf("malformed responses response: %v", err),
			Cause:   err,
		}
	}

	if resp.Status == "failed" {
		msg := "responses status failed"
		if resp.Error != nil && resp.Error.Message != "" {
			msg = resp.Error.Message
		}
		return nil, &NonRetryableError{Message: msg}
	}

	result := &Response{
		Model:        resp.Model,
		FinishReason: "stop",
	}
	// REGRESSION: 原实现一律映射 length,内容过滤截断会被误报为长度截断。
	// DeepSeek 的 incomplete_details.reason 区分 max_output_tokens / content_filter。
	if resp.Status == "incomplete" {
		if resp.IncompleteDetails != nil && resp.IncompleteDetails.Reason == "content_filter" {
			result.FinishReason = "content_filter"
		} else {
			result.FinishReason = "length"
		}
	}

	var textParts []string
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" {
					textParts = append(textParts, part.Text)
				}
			}
		case "function_call":
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:        item.CallID,
				Name:      item.Name,
				Arguments: item.Arguments,
			})
		case "reasoning":
			// REGRESSION: 原实现只读 summary[summary_text],但 DeepSeek 文档
			// 承诺不生成 summary,reasoning 明文思维链放在 content[reasoning_text]
			// 中,导致非流式调用 ReasoningContent 恒为空(流式 reasoning_text.delta
			// 不受影响)。两者都解析以兼容 OpenAI 变体。
			// content 优先(DeepSeek 实际结构);content 为空时回退 summary
			// (OpenAI 变体),避免两处同时存在时重复拼接。
			fromContent := false
			for _, part := range item.Content {
				if part.Type == "reasoning_text" {
					fromContent = true
					result.ReasoningContent += part.Text
				}
			}
			if !fromContent {
				for _, part := range item.Summary {
					if part.Type == "summary_text" {
						result.ReasoningContent += part.Text
					}
				}
			}
		case "web_search_call":
			// 服务端已自动执行搜索并注入上下文,item 需原样回传
			// (下一轮恢复搜索结果上下文)
		}
	}
	result.WebSearchCalls = responsesWebSearchCalls(resp.Output)
	result.Content = strings.Join(textParts, "")
	if len(result.ToolCalls) > 0 {
		result.FinishReason = "tool_calls"
	}

	if resp.Usage != nil {
		hit, miss := responsesCacheTokens(resp.Usage)
		result.Usage = &UsageInfo{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.TotalTokens,
			CacheHitTokens:   hit,
			CacheMissTokens:  miss,
		}
		if resp.Usage.OutputTokensDetails != nil {
			result.Usage.ReasoningTokens = resp.Usage.OutputTokensDetails.ReasoningTokens
		}
	}

	return result, nil
}

func (a *deepSeekAdapter) parseChatResponse(body []byte) (*Response, error) {
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
	if isResponsesStreamEvent(data) {
		return a.parseResponsesStreamEvent(data)
	}
	return a.parseChatStreamEvent(data)
}

// isResponsesStreamEvent 探测 SSE 行是否为 Responses API 事件。
// Responses 事件带 "type": "response.*";chat chunk 无 type 字段。
func isResponsesStreamEvent(data []byte) bool {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return strings.HasPrefix(probe.Type, "response.")
}

// parseResponsesStreamEvent 解析 Responses API SSE 事件。
// 事件列表(仅处理影响输出的类型,其余忽略):
//   - response.output_text.delta → 文本增量
//   - response.reasoning_text.delta → 思考链增量
//   - response.output_item.added(function_call) → 工具调用 ID/Name(output_index 关联)
//   - response.function_call_arguments.delta → 参数增量(output_index 关联)
//   - response.function_call_arguments.done → 完整参数(覆盖累积)
//   - response.web_search_call.* → 服务端搜索状态(TUI 进度展示)
//   - response.completed / incomplete / failed → 终态事件(Done=true)
func (a *deepSeekAdapter) parseResponsesStreamEvent(data []byte) (StreamingEvent, error) {
	var ev responsesStreamEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return StreamingEvent{}, fmt.Errorf("parsing responses stream event: %w", err)
	}

	switch ev.Type {
	case "response.output_text.delta":
		return StreamingEvent{Delta: ev.Delta}, nil

	case "response.reasoning_text.delta":
		return StreamingEvent{ReasoningDelta: ev.Delta}, nil

	case "response.output_item.added":
		if ev.Item.Type == "function_call" {
			return StreamingEvent{ToolCalls: []ToolCall{{
				Index: ev.OutputIndex,
				ID:    ev.Item.CallID,
				Name:  ev.Item.Name,
			}}}, nil
		}
		return StreamingEvent{}, nil

	case "response.function_call_arguments.delta":
		return StreamingEvent{ToolCalls: []ToolCall{{
			Index:     ev.OutputIndex,
			Arguments: ev.Delta,
		}}}, nil

	case "response.function_call_arguments.done":
		// REGRESSION: done 事件携带完整 arguments 时应覆盖 delta 累积;
		// 但若服务端返回空 arguments(异常),跳过覆盖防止清空已累积的参数。
		if ev.Arguments == "" {
			return StreamingEvent{}, nil
		}
		return StreamingEvent{
			ToolCalls:       []ToolCall{{Index: ev.OutputIndex, Arguments: ev.Arguments}},
			ToolCallReplace: true,
		}, nil

	case "response.web_search_call.in_progress":
		return StreamingEvent{WebSearchStatus: "in_progress", WebSearchCallID: ev.Item.ID}, nil

	case "response.web_search_call.searching":
		return StreamingEvent{WebSearchStatus: "searching", WebSearchCallID: ev.Item.ID}, nil

	case "response.web_search_call.completed":
		// 防御性解析 OpenAI 兼容的 search_queries(DeepSeek 文档未承诺,
		// 若返回则透传给 TUI 展示真实搜索词;为空时上层回退通用文案)
		return StreamingEvent{
			WebSearchStatus:  "completed",
			WebSearchCallID:  ev.Item.ID,
			WebSearchQueries: ev.Item.SearchQueries,
		}, nil

	case "response.completed":
		// 注意:此处仅填充 WebSearchCalls(终态事件内嵌完整 output 列表);
		// ToolCalls 依赖流式增量事件(response.output_item.added +
		// function_call_arguments.delta/done)累积,与 chat 模式一致。
		return StreamingEvent{
			Done:           true,
			FinishReason:   "stop",
			Usage:          responsesUsageToInfo(ev.Response.Usage),
			Model:          ev.Response.Model,
			WebSearchCalls: responsesWebSearchCalls(ev.Response.Output),
		}, nil

	case "response.incomplete":
		// REGRESSION: 原实现一律映射 length,内容过滤截断会被误报为长度截断。
		reason := "length"
		if ev.Response.IncompleteDetails != nil && ev.Response.IncompleteDetails.Reason == "content_filter" {
			reason = "content_filter"
		}
		return StreamingEvent{
			Done:           true,
			FinishReason:   reason,
			Usage:          responsesUsageToInfo(ev.Response.Usage),
			Model:          ev.Response.Model,
			WebSearchCalls: responsesWebSearchCalls(ev.Response.Output),
		}, nil

	case "response.failed":
		msg := "responses stream failed"
		if ev.Response.Error != nil && ev.Response.Error.Message != "" {
			msg = ev.Response.Error.Message
		}
		return StreamingEvent{
			Done:  true,
			Model: ev.Response.Model,
			Err:   &NonRetryableError{Message: msg},
		}, nil

	case "response.created":
		// 首帧携带 model
		return StreamingEvent{Model: ev.Response.Model}, nil

	default:
		// response.in_progress / content_part.* / output_item.done /
		// output_text.done / reasoning_text.done 等事件不影响输出,忽略
		return StreamingEvent{}, nil
	}
}

// responsesCacheTokens 提取 Responses API usage 的缓存命中/未命中 token 数。
// REGRESSION: DeepSeek Responses API 的 input_tokens_details 仅返回 cached_tokens
// (与 Chat Completions API 的 prompt_cache_hit_tokens/prompt_cache_miss_tokens
// 不同),缺失的 cache_miss_tokens 直接解析为 0,会导致 TUI 缓存命中率虚高为
// 100% 且输入费用低估。未命中数按 input_tokens - cached_tokens 推导
// (OpenAI 语义:input_tokens = cached_tokens + 未缓存输入);
// 服务端字段有效时优先使用。
func responsesCacheTokens(u *responsesUsage) (hit, miss int) {
	if u == nil {
		return 0, 0
	}
	hit = u.InputTokensDetails.CachedTokens
	miss = u.InputTokensDetails.CacheMissTokens
	if miss <= 0 && u.InputTokens > hit {
		miss = u.InputTokens - hit
	}
	return hit, miss
}

// responsesUsageToInfo 将 Responses API usage 转为内部 UsageInfo。
func responsesUsageToInfo(u *responsesUsage) *UsageInfo {
	if u == nil {
		return nil
	}
	hit, miss := responsesCacheTokens(u)
	info := &UsageInfo{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.TotalTokens,
		CacheHitTokens:   hit,
		CacheMissTokens:  miss,
	}
	if u.OutputTokensDetails != nil {
		info.ReasoningTokens = u.OutputTokensDetails.ReasoningTokens
	}
	return info
}

// responsesWebSearchCalls 从 output items 中提取 web_search_call item。
// 服务端搜索动作已在服务端执行并注入上下文,客户端仅需原样回传
// (OpenAI 协议:{"type": "web_search_call", "id": ..., "status": ...,
// "action": {...}})。
func responsesWebSearchCalls(output []responsesItem) []WebSearchCall {
	var calls []WebSearchCall
	for _, item := range output {
		if item.Type == "web_search_call" {
			if item.ID == "" {
				// 防御:ID 缺失的 item 无法回传配对,丢弃并告警保证可观测
				slog.Warn("web_search_call item without id dropped", "status", item.Status)
				continue
			}
			calls = append(calls, WebSearchCall{ID: item.ID, Status: item.Status, Action: item.Action})
		}
	}
	return calls
}

func (a *deepSeekAdapter) parseChatStreamEvent(data []byte) (StreamingEvent, error) {
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

// --- Responses API 解析结构 (deepseek-v4-flash) ---

// responsesResponse 是 Responses API 非流式响应结构(仅提取需要的字段)。
type responsesResponse struct {
	Object            string                    `json:"object"`
	Status            string                    `json:"status"` // completed / incomplete / failed
	Output            []responsesItem           `json:"output"`
	Usage             *responsesUsage           `json:"usage"`
	Model             string                    `json:"model"`
	Error             *responsesAPIError        `json:"error"`
	IncompleteDetails *responsesIncompleteDetails `json:"incomplete_details"`
}

// responsesIncompleteDetails 响应不完整的原因详情。
type responsesIncompleteDetails struct {
	Reason string `json:"reason"` // max_output_tokens / content_filter
}

// responsesItem 是 Responses API 输出/输入 item 的通用结构。
// 不同类型使用不同字段:message(role+content)、function_call(call_id+name+arguments)、
// function_call_output(call_id+output)、reasoning(content/summary)、
// web_search_call(id+status+action)。
type responsesItem struct {
	Type      string             `json:"type"`
	ID        string             `json:"id"`
	Role      string             `json:"role"`
	Content   []responsesContent `json:"content"`
	CallID    string             `json:"call_id"`
	Name      string             `json:"name"`
	Arguments string             `json:"arguments"`
	Output    string             `json:"output"`
	Summary   []responsesContent `json:"summary"`
	Status    string             `json:"status"`
	// Action 是 web_search_call item 的搜索动作描述(search/open_page/find_in_page),
	// 原样透传供多轮回传
	Action json.RawMessage `json:"action"`
	// SearchQueries 是 web_search_call item 的实际搜索词(OpenAI 兼容
	// search_queries 字段;DeepSeek 未在文档承诺,防御性解析)
	SearchQueries []string `json:"search_queries"`
}

// responsesContent 是 Responses API 内容块结构。
// type 取值: input_text / output_text / reasoning_text / summary_text。
type responsesContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// responsesUsage 是 Responses API 的 token 用量结构。
type responsesUsage struct {
	InputTokens         int                     `json:"input_tokens"`
	OutputTokens        int                     `json:"output_tokens"`
	TotalTokens         int                     `json:"total_tokens"`
	InputTokensDetails  responsesInputDetails   `json:"input_tokens_details"`
	OutputTokensDetails *responsesOutputDetails `json:"output_tokens_details"`
}

type responsesInputDetails struct {
	CachedTokens    int `json:"cached_tokens"`
	CacheMissTokens int `json:"cache_miss_tokens"`
}

type responsesOutputDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type responsesAPIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// responsesStreamEvent 是 Responses API 流式 SSE 事件的通用结构。
// 不同事件类型使用不同字段:output_text.delta 用 Delta,
// function_call_arguments.done 用 Arguments,completed 用 Response。
type responsesStreamEvent struct {
	Type           string            `json:"type"`
	Delta          string            `json:"delta"`
	Arguments      string            `json:"arguments"`
	ItemID         string            `json:"item_id"`
	ContentIndex   int               `json:"content_index"`
	SequenceNumber int               `json:"sequence_number"`
	OutputIndex    int               `json:"output_index"`
	Item           responsesItem     `json:"item"`
	Response       responsesResponse `json:"response"`
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
	defer func() { _ = resp.Body.Close() }()

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
		result.BalanceInfos[i] = CurrencyBalance(b)
	}
	return result, nil
}

// SupportsBalance DeepSeek 支持余额查询。
func (a *deepSeekAdapter) SupportsBalance() bool { return true }

// ListModels 通过 DeepSeek API 获取可用模型列表。
// 端点: GET /models
func (a *deepSeekAdapter) ListModels(ctx context.Context, httpClient *http.Client) ([]ModelInfo, error) {
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

// stripReasoningWithoutToolCalls 从无 tool_calls 的 assistant 消息中移除 ReasoningContent。
// DeepSeek 仅要求带 tool_calls 的 assistant 回传 reasoning_content，无 tool_calls 时
// 回传会增加不必要的 token 消耗。JSONL 中的原始消息不受影响（返回新切片）。
func stripReasoningWithoutToolCalls(messages []Message) []Message {
	cleaned := make([]Message, len(messages))
	for i, m := range messages {
		cleaned[i] = m
		if m.Role == RoleAssistant && len(m.ToolCalls) == 0 && m.ReasoningContent != "" {
			cleaned[i].ReasoningContent = ""
		}
	}
	return cleaned
}
