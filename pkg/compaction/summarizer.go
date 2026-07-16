package compaction

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// CompactionSummarizer 使用 LLM Client 执行 Tier 3 增量摘要。
// 实现 Summarizer 接口。
type CompactionSummarizer struct {
	client    llm.Client
	maxTokens int // 摘要最大输出 token 数（默认 SummaryMaxTokens）
}

// NewCompactionSummarizer 创建一个 CompactionSummarizer。
func NewCompactionSummarizer(client llm.Client, maxTokens int) *CompactionSummarizer {
	if maxTokens <= 0 {
		maxTokens = SummaryMaxTokens
	}
	return &CompactionSummarizer{client: client, maxTokens: maxTokens}
}

// Summarize 实现 Summarizer 接口。
func (s *CompactionSummarizer) Summarize(ctx context.Context, existingSummaries []string, deltaMessages []llm.Message) (content string, err error) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: FormatSummaryPrompt()},
		{Role: llm.RoleUser, Content: FormatSummaryUserMessage(existingSummaries, deltaMessages)},
	}

	resp, callErr := s.client.SendMessage(ctx, messages, nil)
	if callErr != nil {
		err = fmt.Errorf("compaction summarizer: LLM call failed: %w", callErr)
		slog.Warn("compaction summary LLM call failed", "err", err)
		return
	}

	content = strings.TrimSpace(resp.Content)
	if content == "" {
		err = fmt.Errorf("compaction summarizer: empty response")
		slog.Warn("compaction summary returned empty response")
		return
	}

	// 验证输出为合法 JSON
	if !json.Valid([]byte(extractJSON(content))) {
		err = fmt.Errorf("compaction summarizer: response is not valid JSON: %s", truncateString(content, 200))
		slog.Warn("compaction summary JSON parse failed", "err", err)
		return
	}

	return content, nil
}

// extractJSON 从模型输出中提取 JSON 片段（处理可能的 markdown 包裹）。
func extractJSON(s string) string {
	// 去除 ```json ... ``` 包裹
	if idx := strings.Index(s, "```json"); idx >= 0 {
		start := idx + len("```json")
		if end := strings.Index(s[start:], "```"); end >= 0 {
			s = s[start : start+end]
		} else {
			s = s[start:]
		}
	} else if idx := strings.Index(s, "```"); idx >= 0 {
		start := idx + len("```")
		if end := strings.Index(s[start:], "```"); end >= 0 {
			s = s[start : start+end]
		} else {
			s = s[start:]
		}
	}
	return strings.TrimSpace(s)
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
