package session

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Menfre01/waveloom/pkg/compaction"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/task"
)

// BuildVersion 由 main() 在启动时注入（来自 ldflags 或 fallback）。
// session 文件写入此版本号，用于兼容性检查。
var BuildVersion = "dev"

// sessionFile 是 session 落盘文件的顶层结构。
type sessionFile struct {
	SessionID   string              `json:"session_id"`
	Version     string              `json:"version"`
	CreatedAt   string              `json:"created_at"`
	UpdatedAt   string              `json:"updated_at"`
	Messages    []llm.Message       `json:"messages"`
	Stats       sessionStats        `json:"stats"`
	Compaction  *sessionCompaction  `json:"compaction,omitempty"`
	Tasks       []task.TaskInfo     `json:"tasks,omitempty"`
	TodoItems            []json.RawMessage   `json:"todo_items"`
	LastBackgroundCheck  string              `json:"last_background_check,omitempty"`
}

// sessionCompaction 是压缩状态的序列化形式。
type sessionCompaction struct {
	Decisions  []compaction.CompactionDecision `json:"decisions"`
	Watermark  compaction.WatermarkState       `json:"watermark"`
	Summaries  []string                        `json:"summaries,omitempty"`
	TotalTurns int                             `json:"total_turns"`
}

// sessionStats 是 Stats 的序列化形式。
type sessionStats struct {
	TotalTurns            int   `json:"total_turns"`
	TotalPromptTokens     int   `json:"total_prompt_tokens"`
	TotalCompletionTokens int   `json:"total_completion_tokens"`
	TotalCacheHitTokens   int   `json:"total_cache_hit_tokens"`
	TotalCacheMissTokens  int   `json:"total_cache_miss_tokens"`
	TotalReasoningTokens  int   `json:"total_reasoning_tokens"`
	TotalDurationMs       int64 `json:"total_duration_ms"`
	MessageCount          int   `json:"message_count"`
}

// SaveSessionToFile 将消息历史和统计信息序列化写入指定文件。
// 使用原子写入：先写临时文件，再 rename。
// compaction 为 nil 时不写入压缩状态。
// lastBackgroundCheck 为后台任务上次检查时间（零值时保留已有值）。
func SaveSessionToFile(path string, messages []llm.Message, stats Stats, compData *compaction.CompactionData, todoItems []json.RawMessage, lastBackgroundCheck time.Time) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}

	// 如果已存在旧文件，保留 session_id 和 created_at
	var sf sessionFile
	existing, err := loadSessionFile(path)
	if err == nil && existing != nil {
		sf.SessionID = existing.SessionID
		sf.CreatedAt = existing.CreatedAt
		sf.Version = existing.Version
	} else {
		sf.SessionID = NewSessionID()
		sf.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		sf.Version = version()
	}

	sf.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	sf.Messages = messages
	sf.Stats = sessionStats{
		TotalTurns:            stats.TotalTurns,
		TotalPromptTokens:     stats.TotalPromptTokens,
		TotalCompletionTokens: stats.TotalCompletionTokens,
		TotalCacheHitTokens:   stats.TotalCacheHitTokens,
		TotalCacheMissTokens:  stats.TotalCacheMissTokens,
		TotalReasoningTokens:  stats.TotalReasoningTokens,
		TotalDurationMs:       stats.TotalDurationMs,
		MessageCount:          stats.MessageCount,
	}

	if compData != nil {
		decisions := compaction.DecisionSetToList(compData.Decisions)
		sf.Compaction = &sessionCompaction{
			Decisions:  decisions,
			Watermark:  compData.Watermark,
			Summaries:  compData.Summaries,
			TotalTurns: compData.TotalTurns,
		}
	}

	if !lastBackgroundCheck.IsZero() {
		sf.LastBackgroundCheck = lastBackgroundCheck.UTC().Format(time.RFC3339)
	} else if existing != nil && existing.LastBackgroundCheck != "" {
		sf.LastBackgroundCheck = existing.LastBackgroundCheck
	}

	// 复制一份避免直接引用 Registry 内部指针
	list := task.DefaultRegistry.List()
	sf.Tasks = make([]task.TaskInfo, len(list))
	for i, t := range list {
		sf.Tasks[i] = *t
	}

	if todoItems != nil {
		sf.TodoItems = todoItems
	} else if existing != nil && len(existing.TodoItems) > 0 {
		// 从未设置过 todo（nil）→ 保留已有数据
		sf.TodoItems = existing.TodoItems
	}

	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write session tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename session file: %w", err)
	}
	return nil
}
// LoadSessionFromFile 从指定文件读取并返回消息历史、统计信息、压缩数据、session ID、后台任务列表和上次后台检查时间。
// 文件不存在返回 nil, ..., nil, "", nil, time.Time{}, nil；格式无效返回 error。
//
// 加载优先级：
//  1. 若同目录存在 .jsonl 文件，优先从 JSONL 加载消息（增量恢复）
//  2. 否则从 JSON 文件的 Messages 字段加载（兼容旧格式）
func LoadSessionFromFile(path string) ([]llm.Message, Stats, *compaction.CompactionData, string, []task.TaskInfo, []json.RawMessage, time.Time, error) {
	sf, err := loadSessionFile(path)
	if err != nil {
		return nil, Stats{}, nil, "", nil, nil, time.Time{}, err
	}
	if sf == nil {
		return nil, Stats{}, nil, "", nil, nil, time.Time{}, nil
	}

	// 优先从 JSONL 加载消息
	messages := sf.Messages
	jlPath := jsonlPathForJSON(path)
	if jlMessages, jlErr := loadMessagesFromJSONL(jlPath); jlErr == nil && len(jlMessages) > 0 {
		messages = jlMessages
	}

	stats := Stats{
		TotalTurns:            sf.Stats.TotalTurns,
		TotalPromptTokens:     sf.Stats.TotalPromptTokens,
		TotalCompletionTokens: sf.Stats.TotalCompletionTokens,
		TotalCacheHitTokens:   sf.Stats.TotalCacheHitTokens,
		TotalCacheMissTokens:  sf.Stats.TotalCacheMissTokens,
		TotalReasoningTokens:  sf.Stats.TotalReasoningTokens,
		TotalDurationMs:       sf.Stats.TotalDurationMs,
		MessageCount:          sf.Stats.MessageCount,
	}

	var compData *compaction.CompactionData
	if sf.Compaction != nil {
		decisions := compaction.NewDecisionSetFromList(sf.Compaction.Decisions)
		compData = &compaction.CompactionData{
			Decisions:  decisions,
			Watermark:  sf.Compaction.Watermark,
			Summaries:  sf.Compaction.Summaries,
			TotalTurns: sf.Compaction.TotalTurns,
		}
	}


	var lastBackgroundCheck time.Time
	if sf.LastBackgroundCheck != "" {
		if t, parseErr := time.Parse(time.RFC3339, sf.LastBackgroundCheck); parseErr == nil {
			lastBackgroundCheck = t
		}
	}

	return messages, stats, compData, sf.SessionID, sf.Tasks, sf.TodoItems, lastBackgroundCheck, nil
}

// loadSessionFile 读取并解析 session 文件。文件不存在返回 nil, nil。
func loadSessionFile(path string) (*sessionFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read session file: %w", err)
	}

	var sf sessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("parse session file: %w", err)
	}
	return &sf, nil
}

// RemoveSessionFile 删除 session 文件。
func RemoveSessionFile(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove session file: %w", err)
	}
	return nil
}

// ResolveSessionDir 根据项目路径返回 session 存储目录。
//
// 优先级：
//  1. override 非空 — 绝对路径直接使用，相对路径基于 cwd 解析
//  2. 环境变量 WAVELOOM_SESSION_DIR
//  3. 默认：~/.waveloom/<project-slug>/sessions/
func ResolveSessionDir(cwd string, override string) (string, error) {
	if override != "" {
		if filepath.IsAbs(override) {
			return override, nil
		}
		absCwd, err := filepath.Abs(cwd)
		if err != nil {
			return "", fmt.Errorf("resolve absolute cwd: %w", err)
		}
		// 相对路径追加项目前缀，与 home 目录默认行为一致
		// 例：override=".waveloom/sessions", cwd=/path/to/waveloom → /path/to/waveloom/.waveloom/sessions/waveloom/
		return filepath.Join(absCwd, override, projectSlug(absCwd)), nil
	}
	if dir := os.Getenv("WAVELOOM_SESSION_DIR"); dir != "" {
		return dir, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}

	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve absolute cwd: %w", err)
	}

	slug := projectSlug(absCwd)
	return filepath.Join(homeDir, ".waveloom", slug, "sessions"), nil
}

// projectSlug 将项目绝对路径转换为可读的目录名。
// 直接使用项目目录名，简洁且具备可读性。
// 例：/Users/menfre/Workbench/waveloom → waveloom
func projectSlug(absPath string) string {
	return filepath.Base(absPath)
}

// NewSessionID 生成 16 字节随机标识符，格式为 8-4-4-4-12 hex 字符。
// 例：a1b2c3d4-e5f6-a7b8-c9d0-e1f2a3b4c5d6
func NewSessionID() string {
	b := make([]byte, 16)
	mustReadRandom(b)
	s := hex.EncodeToString(b)
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}

// version 返回当前程序版本（写入 session 文件，用于兼容性检查）。
func version() string {
	return BuildVersion
}

// --- JSONL transcript 持久化 ---

// jsonlPathForJSON 从 .json 路径推导对应的消息 JSONL 路径。
// 例：session-abc123.json → session-abc123.messages.jsonl
// 使用独立扩展名避免与 transcript JSONL（session-abc123.jsonl）冲突。
func jsonlPathForJSON(jsonPath string) string {
	return strings.TrimSuffix(jsonPath, ".json") + ".messages.jsonl"
}

// appendMessagesToJSONL 将消息逐条追加到 JSONL 文件。
// 每条消息序列化为一行 JSON，以 \n 分隔。
func appendMessagesToJSONL(path string, messages []llm.Message) error {
	if len(messages) == 0 {
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
	for _, msg := range messages {
		if err := enc.Encode(msg); err != nil {
			return fmt.Errorf("encode message to jsonl: %w", err)
		}
	}
	return nil
}

// loadMessagesFromJSONL 从 JSONL 文件读取所有消息。
// 文件不存在返回 nil, nil。
func loadMessagesFromJSONL(path string) ([]llm.Message, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open jsonl: %w", err)
	}
	defer func() { _ = f.Close() }()

	var messages []llm.Message
	scanner := bufio.NewScanner(f)
	// 增大 scanner buffer：单条消息可能很大（如包含 tool 结果）
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg llm.Message
		if err := json.Unmarshal(line, &msg); err != nil {
			// 跳过损坏的行（不阻塞整个恢复流程）
			slog.Warn("jsonl skip malformed line", "err", err)
			continue
		}
		messages = append(messages, msg)
	}
	if err := scanner.Err(); err != nil {
		return messages, fmt.Errorf("scan jsonl: %w", err)
	}
	return messages, nil
}

// writeMessagesToJSONL 将消息完整写入 JSONL 文件（覆盖模式，用于 fork session）。
func writeMessagesToJSONL(path string, messages []llm.Message) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create jsonl dir: %w", err)
	}

	// 原子写入：先写临时文件，再 rename
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create jsonl tmp: %w", err)
	}

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	writeErr := error(nil)
	for _, msg := range messages {
		if err := enc.Encode(msg); err != nil {
			writeErr = fmt.Errorf("encode message to jsonl: %w", err)
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

// --- settings.json session 配置 ---

// sessionSettingsFile 是 settings.json 中 session 块的顶层结构。
type sessionSettingsFile struct {
	Session *sessionSettings `json:"session"`
}

// sessionSettings 对应 settings.json 中的 session 配置块。
type sessionSettings struct {
	Dir string `json:"dir"` // session 存储目录（相对或绝对路径）
}

// LoadSessionDir 从 settings.json 文件读取 session 目录配置。
// 文件不存在或缺少 session 块时返回空字符串。
func LoadSessionDir(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var sf sessionSettingsFile
	if err := json.Unmarshal(data, &sf); err != nil || sf.Session == nil {
		return ""
	}
	return sf.Session.Dir
}


