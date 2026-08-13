package agentloop

import (
	"context"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// TestPreviewSuffixColonVariants 锁定 hasPreviewSuffix 对冒号类 Unicode 变体的
// 匹配行为:全部视觉形似冒号的变体(共 12 个)都必须视为预告后缀。
// REGRESSION: 曾仅覆盖 U+003A/U+FF1A,小冒号 U+FE55、比号 U+2236 等变体
// 结尾的预告文本被跳过,注入不触发、任务误判完成(评测实测发现漏注入)。
func TestPreviewSuffixColonVariants(t *testing.T) {
	colonVariants := []struct {
		name string
		text string
	}{
		{"ascii colon", "开始执行:"},
		{"fullwidth colon", "开始执行\uFF1A"},
		{"small colon", "开始执行\uFE55"},
		{"ratio", "开始执行\u2236"},
		{"modifier colon", "开始执行\uA789"},
		{"vertical colon", "开始执行\uFE13"},
		{"proportion", "开始执行\u2237"},
		{"armenian full stop", "开始执行\u0589"},
		{"hebrew sof pasuq", "开始执行\u05C3"},
		{"triangular colon", "开始执行\u02D0"},
		{"raised colon", "开始执行\u02F8"},
		{"greek question mark", "开始执行\u037E"},
	}
	for _, tt := range colonVariants {
		if !hasPreviewSuffix(tt.text) {
			t.Errorf("hasPreviewSuffix(%q) = false, want true", tt.text)
		}
	}
}

// TestRegression_PreviewColonVariantInjects 端到端验证:模型输出以冒号变体
// (此处用比号 U+2236,视觉形似冒号)结尾的预告文本时,[system:continue]
// 提醒正常注入,模型获得补发工具调用的机会。
// REGRESSION: 变体冒号不在 suffix 列表时注入被跳过,预告动作永不执行。
func TestRegression_PreviewColonVariantInjects(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{
			// 第一轮:比号结尾的预告文本,无工具调用
			makeTextResponse("开始执行\u2236"),
			// 第二轮:模型补发工具调用
			makeToolCallResponse("", makeToolCall("tc1", "read_file", `{"file_path":"/tmp/a.txt"}`)),
			// 第三轮:正常完成
			makeTextResponse("All done.")}}
	registry := newTestRegistry(newSuccessTool("read_file", true, "hello"))
	loop := New(client, registry, DefaultConfig())

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "run eval"}}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if finalEv.Reason != ReasonCompleted {
		t.Errorf("expected ReasonCompleted, got %s", finalEv.Reason)
	}
	reminderCount := 0
	for _, msg := range finalEv.Messages {
		if strings.Contains(msg.Content, "[system:continue]") {
			reminderCount++
		}
	}
	if reminderCount != 1 {
		t.Errorf("expected 1 [system:continue] reminder for colon variant preview, got %d", reminderCount)
	}
	if client.callCount != 3 {
		t.Errorf("expected 3 LLM calls (preview → tool → done), got %d", client.callCount)
	}
}
