package context

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ---------------------------------------------------------------------------
// Transcript — session 对话段落流式记录（JSONL）
// ---------------------------------------------------------------------------

// maxTranscriptLines 是 resume 时回放到 viewport 的最大段落数。
const maxTranscriptLines = 500

// TranscriptLine 是 transcript JSONL 文件中一行的结构。
type TranscriptLine struct {
	Type          string `json:"type"`                    // user / thought / assistant / tool / system / subagent
	State         string `json:"state"`                   // done / collapsed / expanded / error
	Text          string `json:"text,omitempty"`          // 文本内容
	ToolName      string `json:"name,omitempty"`          // 工具名
	ToolArgs      string `json:"args,omitempty"`          // 工具参数摘要
	ToolResult    string `json:"result,omitempty"`        // 工具输出
	ToolError     string `json:"error,omitempty"`         // 工具错误
	ToolDurMs     int64  `json:"dur_ms,omitempty"`        // 工具耗时
	ThoughtTokens int    `json:"thought_tokens,omitempty"` // thought token 数
	NotifKind     string `json:"notif,omitempty"`          // 系统通知类型: info / warn / error

	// Phase 2: subagent 结构化持久化
	SubagentType        string `json:"sa_type,omitempty"`        // subagent 类型（fork / Explore / ...）
	SubagentModel       string `json:"sa_model,omitempty"`       // 子 agent 模型名
	SubagentPrompt      string `json:"sa_prompt,omitempty"`      // 委派任务描述
	SubagentTurns       int    `json:"sa_turns,omitempty"`       // 总轮次
	SubagentPromptTok   int    `json:"sa_ptok,omitempty"`        // ↑ 输入 token
	SubagentComplTok    int    `json:"sa_ctok,omitempty"`        // ↓ 输出 token
	SubagentToolCallID  string `json:"sa_tcid,omitempty"`        // 父级 tool_call ID
	SubagentEventsJSON  string `json:"sa_events,omitempty"`      // 结构化事件列表（JSON 数组）
}

// TranscriptPath 返回给定 session 对应的 transcript 文件路径。
func TranscriptPath(sessionsDir, sessionID string) string {
	return filepath.Join(sessionsDir, sessionID+".jsonl")
}

// AppendTranscriptLine 向 transcript 文件追加一行 JSONL。
// 自动创建目录，不存在则创建文件。
func AppendTranscriptLine(path string, line TranscriptLine) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create transcript dir: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open transcript: %w", err)
	}
	defer func() { _ = f.Close() }()

	data, err := json.Marshal(line)
	if err != nil {
		return fmt.Errorf("marshal transcript line: %w", err)
	}
	data = append(data, '\n')

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write transcript: %w", err)
	}
	return nil
}

// LoadTranscriptLines 读取 transcript 文件的最后 maxLines 行。
// 文件不存在返回空切片。
func LoadTranscriptLines(path string) ([]TranscriptLine, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer func() { _ = f.Close() }()

	var all []TranscriptLine
	scanner := bufio.NewScanner(f)
	// 增大 buffer 以容纳长 tool result
	scanner.Buffer(make([]byte, 0, 256*1024), 10*1024*1024)

	for scanner.Scan() {
		var line TranscriptLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue // 跳过损坏行
		}
		// 跳过空 Type（非 transcript 格式数据，如 session message JSON）
		if line.Type == "" {
			continue
		}
		all = append(all, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan transcript: %w", err)
	}

	if len(all) > maxTranscriptLines {
		all = all[len(all)-maxTranscriptLines:]
	}
	return all, nil
}

// RemoveTranscriptFile 删除 transcript 文件。
func RemoveTranscriptFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove transcript: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Recent Sessions — 最近 session 记录，用于 --continue 和 ls
// ---------------------------------------------------------------------------

const maxRecentSessions = 10

// RecentEntry 是 recent.json 中的一条记录。
type RecentEntry struct {
	ID           string `json:"id"`
	UpdatedAt    string `json:"updated_at"`
	MessageCount int    `json:"message_count"`
}

// RecentPath 返回 recent.json 文件路径。
func RecentPath(sessionsDir string) string {
	return filepath.Join(sessionsDir, "recent.json")
}

// UpdateRecentSessions 将指定 session 加入 recent.json 头部，保留最近 maxRecentSessions 条。
// 如果 session 已存在，更新其 updated_at 和 message_count。
func UpdateRecentSessions(sessionsDir, sessionID string, messageCount int) error {
	dir := sessionsDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
	}

	path := RecentPath(sessionsDir)
	entries, _ := loadRecentEntries(path)

	// 构建新列表：当前 session 放最前
	now := time.Now().UTC().Format(time.RFC3339)
	newEntry := RecentEntry{ID: sessionID, UpdatedAt: now, MessageCount: messageCount}

	result := make([]RecentEntry, 0, len(entries)+1)
	result = append(result, newEntry)
	for _, e := range entries {
		if e.ID == sessionID {
			continue // 去重
		}
		result = append(result, e)
		if len(result) >= maxRecentSessions {
			break
		}
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal recent: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write recent: %w", err)
	}
	return nil
}

// LoadRecentSessions 读取 recent.json，返回按时间倒序的 session 列表。
// 文件不存在返回空切片。
func LoadRecentSessions(sessionsDir string) ([]RecentEntry, error) {
	return loadRecentEntries(RecentPath(sessionsDir))
}

// ContinueSessionID 返回最近一个 session 的 ID，供 --continue 使用。
// 无记录时返回空字符串。
func ContinueSessionID(sessionsDir string) (string, error) {
	entries, err := LoadRecentSessions(sessionsDir)
	if err != nil || len(entries) == 0 {
		return "", err
	}
	return entries[0].ID, nil
}

// loadRecentEntries 从文件读取 recent entries。
func loadRecentEntries(path string) ([]RecentEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read recent: %w", err)
	}
	var entries []RecentEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse recent: %w", err)
	}
	// 按 UpdatedAt 降序排序
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].UpdatedAt > entries[j].UpdatedAt
	})
	return entries, nil
}
