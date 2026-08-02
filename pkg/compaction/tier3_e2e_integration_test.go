//go:build integration

package compaction

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// loadRealSession 加载真实 session 的消息与 watermark。
// 文件缺失/不可读时跳过(本机验证用)。
func loadRealSession(t *testing.T) ([]llm.Message, WatermarkState) {
	t.Helper()
	path := os.Getenv("WAVELOOM_TEST_SESSION")
	if path == "" {
		path = "/Users/menfre/Workbench/waveloom/.waveloom/sessions/waveloom/3b3c061c-7f91-d255-22c0-f553c17a6770.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("session 文件不可读(跳过): %v", err)
	}
	var d struct {
		Messages []llm.Message `json:"messages"`
		Compaction struct {
			Watermark WatermarkState `json:"watermark"`
		} `json:"compaction"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatalf("session 解析失败: %v", err)
	}
	return d.Messages, d.Compaction.Watermark
}

// TestTier3_E2E_RealSession_At95Percent 真实端到端验证:
// 真实 session(2564 条消息,90.9% 水位,tier3_cursor=2 从未推进的缺陷现场)
// + 构造 96% 水位 → CompactMessages 完整流程 + 真实 LLM 摘要。
// 断言压缩被正常驱动:摘要成功、消息收缩、游标推进、summaries 追加、配对完整。
func TestTier3_E2E_RealSession_At95Percent(t *testing.T) {
	client := loadSummaryClient(t)
	s := NewCompactionSummarizer(client, 0)

	messages, wm := loadRealSession(t)
	if len(messages) < 2000 {
		t.Skipf("session 消息过少(%d),跳过", len(messages))
	}
	oldLen := len(messages)
	lastOriginal := messages[len(messages)-1] // 保护区最后一条(压缩后应保留)

	// 从 session watermark 恢复(保留 tier3_cursor=2 的缺陷现场)
	watermark := wm
	decisions := compactionDecisionSet{}
	existingSummaries := []string{}

	// 96% 水位(> 95% 阈值)驱动完整压缩
	result := CompactMessages(
		context.Background(), &messages,
		960_000, // 96% × 1M
		&watermark, &decisions,
		105, // session 的 total_turns
		DefaultCompactionConfig(),
		s, &existingSummaries,
	)

	if result.Tier != 3 {
		t.Fatalf("96%% 水位应触发 Tier 3,实际 Tier %d", result.Tier)
	}
	if !result.Tier3SummaryDone {
		t.Fatalf("Tier 3 摘要应成功: %+v", result)
	}

	// 1. 消息收缩:delta 删除 + 摘要插入
	if len(messages) >= oldLen {
		t.Fatalf("压缩后消息数应下降: old=%d new=%d", oldLen, len(messages))
	}
	// 1b. 保护区保留(REGRESSION:applyTier3 曾静默删除保护区)
	if len(messages) <= 4 {
		t.Fatalf("保护区消息应保留在摘要之后,实际仅 %d 条", len(messages))
	}
	if lastMsg := messages[len(messages)-1]; lastMsg.Role != lastOriginal.Role {
		t.Fatalf("原最后一条消息(保护区)应保留,实际末条 role=%s", lastMsg.Role)
	}
	t.Logf("消息数: %d → %d (-%d)", oldLen, len(messages), oldLen-len(messages))

	// 2. 摘要通知插入
	foundNotif := false
	for _, m := range messages {
		if strings.Contains(m.Content, "[system:compaction]") {
			foundNotif = true
			break
		}
	}
	if !foundNotif {
		t.Fatal("压缩后应包含 [system:compaction] 通知消息")
	}

	// 3. 游标推进(摘要之后)
	if watermark.Tier3Cursor <= 2 {
		t.Fatalf("Tier3Cursor 应推进,实际 %d", watermark.Tier3Cursor)
	}
	t.Logf("Tier3Cursor: 2 → %d", watermark.Tier3Cursor)

	// 4. summaries 链追加且为合法 JSON
	if len(existingSummaries) != 1 {
		t.Fatalf("summaries 应追加 1 条,实际 %d", len(existingSummaries))
	}
	if !json.Valid([]byte(extractJSON(existingSummaries[0]))) {
		t.Fatalf("摘要不是合法 JSON: %.200s", existingSummaries[0])
	}
	t.Logf("摘要长度: %d 字符", len(existingSummaries[0]))

	// 5. 配对完整性(红线防线):压缩后无孤儿 tool_calls/tool
	if repaired := validateAfterCompaction(&messages); repaired > 0 {
		t.Errorf("压缩后存在 %d 条配对问题消息(红线防线)", repaired)
	}

	// 6. 失败计数清零
	if watermark.Tier3ConsecutiveFailures != 0 {
		t.Fatalf("成功后 Tier3ConsecutiveFailures 应清零,实际 %d", watermark.Tier3ConsecutiveFailures)
	}

	// 7. 硬限解除
	if result.HardLimitReached {
		t.Fatal("摘要成功后不应处于硬限状态")
	}
}
