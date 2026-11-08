package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// TestRegression_ResumeUsesCurrentSystemPrompt 断言恢复会话时使用本次构建的 system prompt,
// 而非磁盘里保存的旧版本。
//
// REGRESSION: LoadFromFile 曾整体采用磁盘消息,而 session JSON 的 messages[0] 就是保存
// 当时的整份 system prompt。提示词升级后 --resume / --continue 会沿用旧文本 ——
// 新增规则与修复一条都不生效,升级形同虚设。
func TestRegression_ResumeUsesCurrentSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.json")

	stale := []llm.Message{
		{Role: llm.RoleSystem, Content: "OLD SYSTEM PROMPT"},
		{Role: llm.RoleUser, Content: "previous question"},
		{Role: llm.RoleAssistant, Content: "previous answer"},
	}
	if err := SaveSessionToFile(path, "", stale, Stats{}, nil, nil, nil, nil, time.Time{}); err != nil {
		t.Fatalf("准备历史 session: %v", err)
	}

	const currentPrompt = "CURRENT SYSTEM PROMPT"
	cm := New(currentPrompt)
	if !cm.LoadFromFile(path) {
		t.Fatal("LoadFromFile 应成功")
	}

	msgs := cm.Messages()
	if len(msgs) < 2 {
		t.Fatalf("历史消息应被恢复,实际 %d 条", len(msgs))
	}
	if msgs[0].Role != llm.RoleSystem {
		t.Fatalf("messages[0] 应为 system,实际 %q", msgs[0].Role)
	}
	if msgs[0].Content != currentPrompt {
		t.Errorf("恢复后 system prompt 未更新:\n got: %q\nwant: %q", msgs[0].Content, currentPrompt)
	}
	// 历史对话本身必须保留
	if msgs[1].Content != "previous question" {
		t.Errorf("历史 user 消息被破坏: %q", msgs[1].Content)
	}
}

// TestRegression_ResumeWithoutSystemMessageKeepsHistory 断言历史首条不是 system 时不插入、
// 不篡改,避免改变消息序列结构。
func TestRegression_ResumeWithoutSystemMessageKeepsHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nosys.json")

	history := []llm.Message{
		{Role: llm.RoleUser, Content: "question without system"},
		{Role: llm.RoleAssistant, Content: "answer"},
	}
	if err := SaveSessionToFile(path, "", history, Stats{}, nil, nil, nil, nil, time.Time{}); err != nil {
		t.Fatalf("准备历史 session: %v", err)
	}

	cm := New("CURRENT SYSTEM PROMPT")
	if !cm.LoadFromFile(path) {
		t.Fatal("LoadFromFile 应成功")
	}

	msgs := cm.Messages()
	if len(msgs) == 0 || msgs[0].Role != llm.RoleUser {
		t.Fatalf("不应为缺失 system 的历史插入消息,实际首条: %+v", msgs[0])
	}
}
