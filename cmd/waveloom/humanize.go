package main

import "strings"

// humanizeError converts technical LLM/network errors into user-friendly messages.
// Raw errors are preserved in verbose logs; this function is for user-facing display only.
func humanizeError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()

	// 401 — authentication failure
	if strings.Contains(msg, "HTTP 401") || strings.Contains(msg, "invalid_api_key") || strings.Contains(msg, "Invalid API Key") || strings.Contains(msg, "Authentication Fails") {
		return "Authentication failed — your API key is invalid. Run waveloom setup to update it, or check your LLM_API_KEY environment variable."
	}

	// 403 — forbidden
	if strings.Contains(msg, "HTTP 403") {
		return "Access denied — your API key does not have permission for this operation. Check your account status on the provider dashboard."
	}

	// 429 — rate limit
	if strings.Contains(msg, "HTTP 429") || strings.Contains(msg, "rate limited") || strings.Contains(msg, "rate_limit") {
		return "Rate limited — the API is throttling requests. Waveloom will retry automatically; if this persists, check your usage quota."
	}

	// 5xx — server error
	if strings.Contains(msg, "HTTP 500") || strings.Contains(msg, "HTTP 502") || strings.Contains(msg, "HTTP 503") || strings.Contains(msg, "HTTP 504") {
		return "The API server encountered a temporary error. This usually resolves within a few seconds — please try again."
	}

	// Network errors
	if strings.Contains(msg, "network error") || strings.Contains(msg, "connection refused") || strings.Contains(msg, "no such host") || strings.Contains(msg, "dial tcp") || strings.Contains(msg, "i/o timeout") {
		return "Network error — cannot reach the API server. Check your internet connection and verify the API endpoint URL in settings."
	}

	// Timeout / deadline
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "context deadline exceeded") {
		return "Request timed out — the API server did not respond in time. Try again, or check your network connection."
	}

	// Retry exhausted
	if strings.Contains(msg, "retry exhausted") {
		return "All retry attempts failed — the API is currently unreachable. Check your connection, API key, and endpoint URL, then try again."
	}

	// DeepSeek overload
	if strings.Contains(msg, "insufficient system resource") {
		return "DeepSeek is currently overloaded. Waveloom will retry automatically — no action needed."
	}

	// Missing API key
	if strings.Contains(msg, "api_key is required") || strings.Contains(msg, "API key is required") {
		return "No API key configured. Run waveloom setup or set the LLM_API_KEY environment variable."
	}

	// Context length exceeded
	if strings.Contains(msg, "maximum context") || strings.Contains(msg, "context length") || strings.Contains(msg, "max context") {
		return "The conversation has exceeded the context window limit. Start a new session with /new, or the context will be automatically compressed."
	}

	// Empty responses loop
	if strings.Contains(msg, "consecutive empty responses") {
		return "The model produced several empty responses in a row. Try rephrasing your prompt or starting a new session with /new."
	}

	// Unknown tool (internal, should not normally happen)
	if strings.Contains(msg, "unknown tool") {
		return "An internal error occurred — the model referenced an unsupported tool. Please try again or restart waveloom."
	}

	// Fallback: return the original message for anything we don't recognize
	return msg
}
