package session

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/task"
)

// TestRegression_PollBackgroundCompletionsInTurn 验证 turn 内后台任务完成
// 轮询:
//  1. 首次轮询报告 completed/failed,过滤 interrupted(kill 路径模型已可见);
//  2. 游标推进后二次轮询为空;
//  3. 与 PrepareRun 共享游标:turn 内已送达的完成不会在下一轮重复注入。
func TestRegression_PollBackgroundCompletionsInTurn(t *testing.T) {
	task.DefaultRegistry.Reset()
	defer task.DefaultRegistry.Reset()
	now := time.Now()
	reg := task.DefaultRegistry
	reg.Register("t-completed", &task.TaskInfo{
		ID: "t-completed", Command: "sleep 5", Status: task.TaskCompleted,
		CompletedTime: now, ExitCode: 0,
	})
	reg.Register("t-interrupted", &task.TaskInfo{
		ID: "t-interrupted", Command: "sleep 9", Status: task.TaskInterrupted,
		CompletedTime: now, ExitCode: 0,
	})
	reg.Register("t-failed", &task.TaskInfo{
		ID: "t-failed", Command: "badcmd", Status: task.TaskFailed,
		CompletedTime: now, ExitCode: 1,
	})

	cm := New("sys")
	notice := cm.PollBackgroundCompletions()
	if !strings.Contains(notice, `id="t-completed"`) {
		t.Errorf("notice missing completed task: %q", notice)
	}
	if !strings.Contains(notice, `id="t-failed"`) {
		t.Errorf("notice missing failed task: %q", notice)
	}
	if strings.Contains(notice, "t-interrupted") {
		t.Errorf("notice must exclude interrupted task: %q", notice)
	}

	if again := cm.PollBackgroundCompletions(); again != "" {
		t.Errorf("second poll should be empty (cursor advanced), got %q", again)
	}

	// turn 内已消费的完成,PrepareRun 不得重复注入
	msgs, _ := cm.PrepareRun("hi")
	for _, m := range msgs {
		if strings.Contains(m.Content, "background-notifications") {
			t.Errorf("PrepareRun re-reported consumed completion: %q", m.Content)
		}
	}
}

// TestRegression_ToolErrorStatsAccumulateAndPersist 验证工具失败计数
// (AddToolError)跨调用累加、Stats() 快照隔离(外部修改不污染内部)、
// 随会话 .json 落盘并在 LoadFromFile 后完整恢复。
func TestRegression_ToolErrorStatsAccumulateAndPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")

	cm := New("sys")
	cm.SetSessionPath(path)
	cm.AddToolError("rate_limited")
	cm.AddToolError("rate_limited")
	cm.AddToolError("blocked")
	cm.AddToolError("") // 空 kind 忽略
	cm.AddModelError()
	cm.AddModelError()

	snap := cm.Stats()
	if snap.ToolErrors["rate_limited"] != 2 || snap.ToolErrors["blocked"] != 1 {
		t.Fatalf("ToolErrors = %v, want rate_limited:2 blocked:1", snap.ToolErrors)
	}
	if snap.ModelErrors != 2 {
		t.Fatalf("ModelErrors = %d, want 2", snap.ModelErrors)
	}
	// 快照隔离:修改返回的 map 不得污染内部统计
	snap.ToolErrors["rate_limited"] = 99
	if got := cm.Stats().ToolErrors["rate_limited"]; got != 2 {
		t.Errorf("Stats() snapshot is not isolated: got rate_limited=%d, want 2", got)
	}

	cm.Save()

	cm2 := New("")
	if !cm2.LoadFromFile(path) {
		t.Fatal("LoadFromFile failed")
	}
	restored := cm2.Stats().ToolErrors
	if restored["rate_limited"] != 2 || restored["blocked"] != 1 {
		t.Errorf("restored ToolErrors = %v, want rate_limited:2 blocked:1", restored)
	}
	if restoredModel := cm2.Stats().ModelErrors; restoredModel != 2 {
		t.Errorf("restored ModelErrors = %d, want 2", restoredModel)
	}
}

// TestRegression_EventRowsPersistAndCursorUnaffected 验证:
//  1. RecordSystemEvent 的事件随 Save 落盘(type=event),且先于消息行;
//  2. LoadFromFile 恢复后 jsonlMessageCount 只计消息行——事件行不得让
//     增量追加退化为永久全量重写(否则事件被抹除、消息永不追加)。
func TestRegression_EventRowsPersistAndCursorUnaffected(t *testing.T) {
	task.DefaultRegistry.Reset() // 清空残留任务,避免 PrepareRun 注入后台通知消息
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	sid := "s"

	cm := New("sys-prompt")
	cm.SetSessionPath(path)
	cm.RecordSystemEvent(EventModelError, "error", map[string]any{"steps": 3, "ms": 1200})
	cm.Save()

	entries, err := LoadTranscriptEntries(TranscriptPath(dir, sid))
	if err != nil {
		t.Fatalf("LoadTranscriptEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("rows = %d, want 2 (event + system prompt)", len(entries))
	}
	if entries[0].Type != TranscriptEventType || entries[0].Subtype != EventModelError {
		t.Fatalf("row[0] = type %q subtype %q, want event/%s", entries[0].Type, entries[0].Subtype, EventModelError)
	}
	if entries[0].Level != "error" {
		t.Errorf("row[0].Level = %q, want error", entries[0].Level)
	}
	var payload map[string]any
	if err := json.Unmarshal(entries[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["steps"] != float64(3) {
		t.Errorf("payload steps = %v, want 3", payload["steps"])
	}
	if !isMessageRow(entries[1]) || entries[1].Type != "system" {
		t.Fatalf("row[1] should be the system-prompt message row, got type %q", entries[1].Type)
	}

	// 模拟 --resume:重新加载后再追加一轮。游标若被事件行污染(旧 bug:
	// jsonlMessageCount = len(全部行) = 2),下一轮 n=2 既不重写也不追加,
	// 新事件与 user 消息都会丢失。
	cm2 := New("")
	if !cm2.LoadFromFile(path) {
		t.Fatal("LoadFromFile failed")
	}
	cm2.RecordSystemEvent(EventToolTimeout, "warn", map[string]any{"tool": "bash", "ms": 30000})
	cm2.PrepareRun("hi")
	cm2.Save()

	entries, err = LoadTranscriptEntries(TranscriptPath(dir, sid))
	if err != nil {
		t.Fatalf("LoadTranscriptEntries: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("rows = %d, want 4 (event, system, event, user)", len(entries))
	}
	var msgRows, evRows int
	for _, e := range entries {
		if e.Type == TranscriptEventType {
			evRows++
		} else if isMessageRow(e) {
			msgRows++
		}
	}
	if evRows != 2 || msgRows != 2 {
		t.Fatalf("evRows = %d, msgRows = %d, want 2/2 (无重复重写)", evRows, msgRows)
	}
	if entries[3].Type != "user" {
		t.Errorf("row[3].Type = %q, want user(增量追加未失效)", entries[3].Type)
	}
}

// TestRegression_RewritePreservesEventRows 验证 forceRewrite(压缩/修复)时
// 已落盘事件行被保留,新事件与消息按序重写,不重复不丢失。
func TestRegression_RewritePreservesEventRows(t *testing.T) {
	dir := t.TempDir()
	jl := TranscriptPath(dir, "s")

	oldMsg := []llm.Message{{Role: llm.RoleSystem, Content: "sys"}, {Role: llm.RoleUser, Content: "old"}}
	oldEv := TranscriptEntry{
		UUID: llm.NewMessageID(), Type: TranscriptEventType, Subtype: EventCompaction,
		Timestamp: "2026-09-04T00:00:00Z",
	}
	all := append([]TranscriptEntry{oldEv}, MessagesToTranscriptEntries(oldMsg, nil, "s", "v1", "/cwd", "")...)
	if err := WriteTranscriptEntries(jl, all); err != nil {
		t.Fatalf("WriteTranscriptEntries: %v", err)
	}

	newMsg := []llm.Message{{Role: llm.RoleSystem, Content: "sys(compacted)"}}
	pending := TranscriptEntry{
		UUID: llm.NewMessageID(), Type: TranscriptEventType, Subtype: EventBackgroundTask,
		Timestamp: "2026-09-04T00:01:00Z",
	}
	if eventsOK, messagesOK := syncJSONLToFile(jl, "s", "/cwd", newMsg, 2, true, []TranscriptEntry{pending}); !eventsOK || !messagesOK {
		t.Fatalf("syncJSONLToFile rewrite failed (eventsOK=%v messagesOK=%v)", eventsOK, messagesOK)
	}

	entries, err := LoadTranscriptEntries(jl)
	if err != nil {
		t.Fatalf("LoadTranscriptEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("rows = %d, want 3 (old event + new event + compacted system)", len(entries))
	}
	if entries[0].Subtype != EventCompaction || entries[1].Subtype != EventBackgroundTask {
		t.Errorf("row order = [%s, %s], want [compaction, background_task]", entries[0].Subtype, entries[1].Subtype)
	}
	if entries[2].Type != "system" {
		t.Errorf("row[2].Type = %q, want system(压缩后消息)", entries[2].Type)
	}
}

// TestRegression_EventBufferDropSummary 验证缓冲上限:超出部分被丢弃,
// 并以一条 events_dropped 汇总事件补记,不无限膨胀。
func TestRegression_EventBufferDropSummary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	sid := "s"

	cm := New("sys")
	cm.SetSessionPath(path)
	for i := 0; i < maxPendingEvents+2; i++ {
		cm.RecordSystemEvent(EventTurnDuration, "info", map[string]any{"i": i})
	}
	cm.Save()

	entries, err := LoadTranscriptEntries(TranscriptPath(dir, sid))
	if err != nil {
		t.Fatalf("LoadTranscriptEntries: %v", err)
	}
	// 128 条保留 + 1 条 drop 汇总 + system 消息行
	if len(entries) != maxPendingEvents+2 {
		t.Fatalf("rows = %d, want %d", len(entries), maxPendingEvents+2)
	}
	dropped := 0
	for _, e := range entries {
		if e.Subtype == EventDropped {
			var p map[string]any
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				t.Fatalf("unmarshal dropped payload: %v", err)
			}
			dropped = int(p["count"].(float64))
		}
	}
	if dropped != 2 {
		t.Errorf("dropped count = %d, want 2", dropped)
	}
}
