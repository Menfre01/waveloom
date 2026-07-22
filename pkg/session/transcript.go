package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// ---------------------------------------------------------------------------
// TranscriptEntry — unified compatible JSONL entry
// ---------------------------------------------------------------------------

// maxTranscriptEntries 是 resume 时回放到 viewport 的最大条目数。
const maxTranscriptEntries = 500

// TranscriptEntry 是统一 JSONL 文件中一行的结构，。
// 同时替代旧的 TranscriptLine（TUI viewport）和裸 llm.Message（resume）。
type TranscriptEntry struct {
	ParentUUID     *string         `json:"parentUuid,omitempty"`
	UUID           string          `json:"uuid"`
	SessionID      string          `json:"sessionId"`
	Version        string          `json:"version"`
	Cwd            string          `json:"cwd"`
	GitBranch      string          `json:"gitBranch,omitempty"`
	Timestamp      string          `json:"timestamp"`
	Type           string          `json:"type"` // "user" | "assistant" | "system" | "permission-mode" | "progress" | "file-history-snapshot"
	IsSidechain    bool            `json:"isSidechain"`
	PermissionMode string          `json:"permissionMode,omitempty"`
	Subtype        string          `json:"subtype,omitempty"` // system 子类型: "turn_duration", "local_command"
	Message        json.RawMessage `json:"message"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type anthropicMessage struct {
	Role       string         `json:"role"`
	Content    []contentBlock `json:"content"`
	Model      string         `json:"model,omitempty"`
	StopReason string         `json:"stop_reason,omitempty"`
	Usage      *llm.UsageInfo `json:"usage,omitempty"`
}

// NewTranscriptEntry 将 llm.Message 转换为 TranscriptEntry，同时做 content blocks 映射。
func NewTranscriptEntry(msg llm.Message, parentUUID *string, sessionID, version, cwd, gitBranch string) TranscriptEntry {
	return TranscriptEntry{
		ParentUUID: parentUUID,
		UUID:       msg.ID,
		SessionID:  sessionID,
		Version:    version,
		Cwd:        cwd,
		GitBranch:  gitBranch,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Type:       msgRoleToEntryType(msg.Role),
		Message:    convertToContentBlocks(msg),
	}
}

// msgRoleToEntryType 将 llm.Role 转换为 TranscriptEntry.Type。
func msgRoleToEntryType(role llm.Role) string {
	switch role {
	case llm.RoleUser:
		return "user"
	case llm.RoleAssistant:
		return "assistant"
	case llm.RoleSystem:
		return "system"
	case llm.RoleTool:
		return "user" // tool_result messages → role "user" per transcript convention
	default:
		return "user"
	}
}

func convertToContentBlocks(msg llm.Message) json.RawMessage {
	var blocks []contentBlock

	switch msg.Role {
	case llm.RoleUser, llm.RoleSystem:
		if msg.Content != "" {
			blocks = append(blocks, contentBlock{Type: "text", Text: msg.Content})
		}
	case llm.RoleAssistant:
		if msg.Content != "" {
			blocks = append(blocks, contentBlock{Type: "text", Text: msg.Content})
		}
		for _, tc := range msg.ToolCalls {
			blocks = append(blocks, contentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Name,
				Input: json.RawMessage(tc.Arguments),
			})
		}
		// thinking block — DeepSeek requires reasoning_content on assistant messages with tool_calls
		if msg.ReasoningContent != "" || len(msg.ToolCalls) > 0 {
			blocks = append(blocks, contentBlock{
				Type:     "thinking",
				Thinking: msg.ReasoningContent,
			})
		}
	case llm.RoleTool:
		blocks = append(blocks, contentBlock{
			Type:      "tool_result",
			ToolUseID: msg.ToolCallID,
			Content:   msg.Content,
			Name:      msg.Name,
		})
	}

	am := anthropicMessage{
		Role:       string(msg.Role),
		Content:    blocks,
		Model:      msg.Model,
		StopReason: msg.FinishReason,
		Usage:      msg.Usage,
	}

	data, err := json.Marshal(am)
	if err != nil {
		// 回退：纯文本 content
		fallback, _ := json.Marshal(anthropicMessage{
			Role:    am.Role,
			Content: []contentBlock{{Type: "text", Text: msg.Content}},
		})
		return fallback
	}
	return data
}

// ToMessage 将 TranscriptEntry 转换回 llm.Message（反向 content blocks 映射）。
func (e TranscriptEntry) ToMessage() llm.Message {
	msg := llm.Message{
		ID: e.UUID,
	}

	// 解析 content blocks
	var am anthropicMessage
	if err := json.Unmarshal(e.Message, &am); err != nil {
		// 回退：尝试按纯文本解析
		msg.Role = entryTypeToRole(e.Type)
		msg.Content = string(e.Message)
		return msg
	}

	msg.Role = entryTypeToRole(e.Type)

	// 标记：先设默认 role，解析 content blocks 后可能覆盖
	hasToolResult := false

	// 提取 text 和 tool_call 信息
	var textParts []string
	for _, block := range am.Content {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "tool_use":
			msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: string(block.Input),
			})
		case "tool_result":
			msg.Content = block.Content
			msg.ToolCallID = block.ToolUseID
			msg.Name = block.Name
			hasToolResult = true
		case "thinking":
			msg.ReasoningContent = block.Thinking
		}
	}
	// tool_result 消息 → role 应为 tool（将其序列化为 user，反序列化时还原）
	if hasToolResult {
		msg.Role = llm.RoleTool
	}

	// 恢复 model/stop_reason/usage
	msg.Model = am.Model
	msg.FinishReason = am.StopReason
	msg.Usage = am.Usage

	if len(textParts) > 0 && msg.Role != llm.RoleTool {
		msg.Content = strings.Join(textParts, "\n")
	}
	return msg
}

func entryTypeToRole(t string) llm.Role {
	switch t {
	case "user":
		return llm.RoleUser
	case "assistant":
		return llm.RoleAssistant
	case "system":
		return llm.RoleSystem
	case "tool":
		return llm.RoleTool
	default:
		return llm.RoleUser
	}
}

// MessagesToTranscriptEntries 将 []llm.Message 批量转换为 []TranscriptEntry。
// 每条消息通过 parentUUID 建立父子链接。
// startingParentUUID 为第一条消息的父 UUID（nil 表示第一条消息无父节点）。
// 增量追加时传入上一条消息的 UUID 以保持链完整。
func MessagesToTranscriptEntries(messages []llm.Message, startingParentUUID *string, sessionID, version, cwd, gitBranch string) []TranscriptEntry {
	if len(messages) == 0 {
		return nil
	}
	entries := make([]TranscriptEntry, 0, len(messages))
	prevUUID := startingParentUUID
	for i := range messages {
		msg := messages[i]
		e := NewTranscriptEntry(msg, prevUUID, sessionID, version, cwd, gitBranch)
		entries = append(entries, e)
		prevUUID = &msg.ID
	}
	return entries
}

// ---------------------------------------------------------------------------
// TranscriptEntry JSONL I/O
// ---------------------------------------------------------------------------

// AppendTranscriptEntries 将 TranscriptEntry 列表追加到 JSONL 文件。
func AppendTranscriptEntries(path string, entries []TranscriptEntry) error {
	if len(entries) == 0 {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create jsonl dir: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open jsonl: %w", err)
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			return fmt.Errorf("encode transcript entry to jsonl: %w", err)
		}
	}
	return nil
}

// WriteTranscriptEntries 将 TranscriptEntry 列表完整写入 JSONL 文件（覆盖模式）。
func WriteTranscriptEntries(path string, entries []TranscriptEntry) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create jsonl dir: %w", err)
	}

	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create jsonl tmp: %w", err)
	}

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	writeErr := error(nil)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			writeErr = fmt.Errorf("encode transcript entry to jsonl: %w", err)
			break
		}
	}
	closeErr := f.Close()

	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return writeErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close jsonl tmp: %w", closeErr)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename jsonl: %w", err)
	}
	return nil
}

// LoadTranscriptEntries 从 JSONL 文件读取所有 TranscriptEntry。
// 文件不存在返回 nil, nil。
func LoadTranscriptEntries(path string) ([]TranscriptEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open transcript jsonl: %w", err)
	}
	defer func() { _ = f.Close() }()

	var entries []TranscriptEntry
	scanner := bufio.NewScanner(f)
	// 增大 scanner buffer：单条消息可能很大（如包含 tool 结果）
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e TranscriptEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // 跳过损坏的行
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return entries, fmt.Errorf("scan transcript jsonl: %w", err)
	}
	return entries, nil
}

// LoadTranscriptEntriesTail 读取 transcript 文件的最后 maxEntries 行。
// 文件不存在返回空切片。
func LoadTranscriptEntriesTail(path string) ([]TranscriptEntry, error) {
	all, err := LoadTranscriptEntries(path)
	if err != nil {
		return nil, err
	}
	if len(all) > maxTranscriptEntries {
		all = all[len(all)-maxTranscriptEntries:]
	}
	return all, nil
}

// ---------------------------------------------------------------------------
// Transcript 文件路径
// TranscriptPath 返回给定 session 对应的统一 transcript JSONL 文件路径。
func TranscriptPath(sessionsDir, sessionID string) string {
	return filepath.Join(sessionsDir, sessionID+".jsonl")
}

// SubagentTranscriptPath 返回 subagent 对应的 transcript JSONL 文件路径。
func SubagentTranscriptPath(sessionsDir, sessionID, agentID string) string {
	return filepath.Join(sessionsDir, sessionID, "subagents", "agent-"+agentID+".jsonl")
}

// SubagentMetaPath 返回 subagent 对应的 metadata 文件路径。
func SubagentMetaPath(sessionsDir, sessionID, agentID string) string {
	return filepath.Join(sessionsDir, sessionID, "subagents", "agent-"+agentID+".meta.json")
}

// AgentMetadata 是 subagent metadata 的持久化形式。
type AgentMetadata struct {
	AgentType        string `json:"agentType"`
	Description      string `json:"description,omitempty"`
	Model            string `json:"model,omitempty"`
	TotalTurns       int    `json:"totalTurns,omitempty"`
	PromptTokens     int    `json:"promptTokens,omitempty"`
	CompletionTokens int    `json:"completionTokens,omitempty"`
}

func SaveAgentMetadata(path string, meta AgentMetadata) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create subagent meta dir: %w", err)
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal agent meta: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadAgentMetadata 从 meta JSON 文件读取 subagent metadata。
// 文件不存在返回 nil, nil。
func LoadAgentMetadata(path string) (*AgentMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read agent meta: %w", err)
	}
	var meta AgentMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse agent meta: %w", err)
	}
	return &meta, nil
}

// RemoveTranscriptFile 删除 transcript 文件。
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
func UpdateRecentSessions(sessionsDir, sessionID string, messageCount int) error {
	dir := sessionsDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
	}

	path := RecentPath(sessionsDir)
	entries, _ := loadRecentEntries(path)

	now := time.Now().UTC().Format(time.RFC3339)
	newEntry := RecentEntry{ID: sessionID, UpdatedAt: now, MessageCount: messageCount}

	result := make([]RecentEntry, 0, len(entries)+1)
	result = append(result, newEntry)
	for _, e := range entries {
		if e.ID == sessionID {
			continue
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
func LoadRecentSessions(sessionsDir string) ([]RecentEntry, error) {
	return loadRecentEntries(RecentPath(sessionsDir))
}

// ContinueSessionID 返回最近一个 session 的 ID，供 --continue 使用。
func ContinueSessionID(sessionsDir string) (string, error) {
	entries, err := LoadRecentSessions(sessionsDir)
	if err != nil || len(entries) == 0 {
		return "", err
	}
	return entries[0].ID, nil
}

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
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].UpdatedAt > entries[j].UpdatedAt
	})
	return entries, nil
}

// ---------------------------------------------------------------------------
// SubagentEventEntry — subagent 事件 JSONL 持久化
// ---------------------------------------------------------------------------

// SubagentEventEntry 是 subagent 单次事件的序列化形式。
// 简化格式：不存储完整的 Anthropic content blocks，而是直接存储事件。
// TUI resume 时通过 buildSubagentParagraph 重建 paraSubagent 段落。
type SubagentEventEntry struct {
	Kind       int    `json:"kind"`                  // SubagentEventKind
	TextDelta  string `json:"text,omitempty"`        // SubagentText
	ToolName   string `json:"tool_name,omitempty"`   // SubagentToolStart / SubagentToolResult
	ToolArgs   string `json:"tool_args,omitempty"`   // SubagentToolStart
	ToolResult string `json:"tool_result,omitempty"` // SubagentToolResult
	ToolDurMs  int64  `json:"tool_dur_ms,omitempty"` // SubagentToolResult
	ToolError  string `json:"tool_error,omitempty"`  // SubagentToolResult
}

// WriteSubagentEvents 将 subagent 事件列表写入 JSONL 文件（覆盖模式）。
func WriteSubagentEvents(path string, events []SubagentEventEntry) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create subagent jsonl dir: %w", err)
	}
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create subagent jsonl tmp: %w", err)
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			_ = f.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("encode subagent event: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close subagent jsonl tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename subagent jsonl: %w", err)
	}
	return nil
}

// LoadSubagentEvents 从 JSONL 文件读取 subagent 事件列表。
func LoadSubagentEvents(path string) ([]SubagentEventEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open subagent jsonl: %w", err)
	}
	defer func() { _ = f.Close() }()

	var events []SubagentEventEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e SubagentEventEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		events = append(events, e)
	}
	if err := scanner.Err(); err != nil {
		return events, fmt.Errorf("scan subagent jsonl: %w", err)
	}
	return events, nil
}
