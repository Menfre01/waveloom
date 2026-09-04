package session

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Menfre01/waveloom/pkg/compaction"
	"github.com/Menfre01/waveloom/pkg/filehistory"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/task"
)

// BuildVersion 由 main() 在启动时注入（来自 ldflags 或 fallback）。
// session 文件写入此版本号，用于兼容性检查。
var BuildVersion = "dev"

// sessionFile 是 session 落盘文件的顶层结构。
type sessionFile struct {
	SessionID            string              `json:"session_id"`
	Name                 string              `json:"name,omitempty"` // 展示用名称(--name 设置,ls 显示)
	Version              string              `json:"version"`
	CreatedAt            string              `json:"created_at"`
	UpdatedAt            string              `json:"updated_at"`
	Messages             []llm.Message       `json:"messages"`
	Stats                sessionStats        `json:"stats"`
	Compaction           *sessionCompaction  `json:"compaction,omitempty"`
	Tasks                []task.TaskInfo     `json:"tasks,omitempty"`
	TodoItems            []json.RawMessage   `json:"todo_items"`
	LastBackgroundCheck  string              `json:"last_background_check,omitempty"`
	PlanMode             *sessionPlanMode    `json:"plan_mode,omitempty"`
	FileHistory          *filehistory.SnapshotData `json:"file_history,omitempty"`
}

// sessionCompaction 是压缩状态的序列化形式。
type sessionCompaction struct {
	Decisions  []compaction.CompactionDecision `json:"decisions"`
	Watermark  compaction.WatermarkState       `json:"watermark"`
	Summaries  []string                        `json:"summaries,omitempty"`
	TotalTurns int                             `json:"total_turns"` // 会话级 turn 计数(一次 Run = 一个 turn);json tag 保留兼容旧 session
}

// sessionPlanMode 是 plan mode 状态的序列化形式。
type sessionPlanMode struct {
	Active   bool   `json:"active"`
	PlanFile string `json:"plan_file"`
}

// sessionStats 是 Stats 的序列化形式。

type sessionStats struct {
	TotalTurns            int     `json:"total_turns"` // 同上:磁盘格式兼容
	TotalPromptTokens     int     `json:"total_prompt_tokens"`
	TotalCompletionTokens int     `json:"total_completion_tokens"`
	TotalCacheHitTokens   int     `json:"total_cache_hit_tokens"`
	TotalCacheMissTokens  int     `json:"total_cache_miss_tokens"`
	TotalReasoningTokens  int     `json:"total_reasoning_tokens"`
	TotalDurationMs       int64   `json:"total_duration_ms"`
	MessageCount          int     `json:"message_count"`
	TotalCost             float64 `json:"total_cost"`
	ToolErrors            map[string]int `json:"tool_errors,omitempty"`
	ModelErrors           int     `json:"model_errors,omitempty"`
}

// SaveSessionToFile 将消息历史、统计信息、plan mode 和文件历史序列化写入指定文件。
// 使用原子写入:先写临时文件,再 rename。
// compaction 为 nil 时不写入压缩状态。
// planMode 为 nil 时不写入 plan mode 状态(保留已有值)。
// fileHistory 为 nil 时不写入文件历史(保留已有值)。
// lastBackgroundCheck 为后台任务上次检查时间(零值时保留已有值)。
// name 为展示用 session 名称(非空时覆盖已有值,空时保留)。
func SaveSessionToFile(path string, name string, messages []llm.Message, stats Stats, compData *compaction.CompactionData, todoItems []json.RawMessage, planMode *sessionPlanMode, fileHistory *filehistory.SnapshotData, lastBackgroundCheck time.Time) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}

	// 如果已存在旧文件,保留 session_id 和 created_at
	var sf sessionFile
	existing, err := loadSessionFile(path)
	if err == nil && existing != nil {
		sf.SessionID = existing.SessionID
		sf.Name = existing.Name
		sf.CreatedAt = existing.CreatedAt
		sf.Version = existing.Version
	} else {
		sf.SessionID = NewSessionID()
		sf.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		sf.Version = version()
	}
	if n := normalizeSessionName(name); n != "" {
		sf.Name = n
	}

	sf.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	sf.Messages = messages
	// REGRESSION: 调用方(Save/saveToPath)拷贝 cm.stats 原始字段,
	// 其 MessageCount 从未被赋值而恒为 0;落盘前以实际消息数为准统一回填,
	// 与 Stats() 访问器的语义(len(cm.messages))保持一致。
	stats.MessageCount = len(messages)
	sf.Stats = sessionStats{
		TotalTurns:            stats.TotalTurns,
		TotalPromptTokens:     stats.TotalPromptTokens,
		TotalCompletionTokens: stats.TotalCompletionTokens,
		TotalCacheHitTokens:   stats.TotalCacheHitTokens,
		TotalCacheMissTokens:  stats.TotalCacheMissTokens,
		TotalReasoningTokens:  stats.TotalReasoningTokens,
		TotalDurationMs:       stats.TotalDurationMs,
		MessageCount:          stats.MessageCount,
		TotalCost:             stats.TotalCost,
		ToolErrors:            stats.ToolErrors,
		ModelErrors:           stats.ModelErrors,
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

	// Plan mode 状态
	if planMode != nil {
		sf.PlanMode = planMode
	} else if existing != nil && existing.PlanMode != nil {
		sf.PlanMode = existing.PlanMode
	}

	// FileHistory 状态
	if fileHistory != nil {
		sf.FileHistory = fileHistory
	} else if existing != nil && existing.FileHistory != nil {
		sf.FileHistory = existing.FileHistory
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
// LoadSessionFromFile 从指定文件读取并返回消息历史、统计信息、压缩数据、session ID、
// 后台任务列表、todo items、plan mode、file history 和上次后台检查时间。
// 文件不存在返回 nil, ..., nil;格式无效返回 error。
//
// 消息来源:JSON 文件的 Messages 字段(压缩后的权威上下文)。
// JSONL 仅为增量事件流水(TUI 回放/审计),不作为 LLM 上下文源 ——
// 压缩只更新 JSON 中的消息内容,JSONL 旧条目保留压缩前原文;
// 若 resume 优先加载 JSONL,会把未压缩原文重新喂给 LLM,导致上下文翻倍。
func LoadSessionFromFile(path string) ([]llm.Message, Stats, *compaction.CompactionData, string, string, []task.TaskInfo, []json.RawMessage, *sessionPlanMode, *filehistory.SnapshotData, time.Time, error) {
	sf, err := loadSessionFile(path)
	if err != nil {
		return nil, Stats{}, nil, "", "", nil, nil, nil, nil, time.Time{}, err
	}
	if sf == nil {
		return nil, Stats{}, nil, "", "", nil, nil, nil, nil, time.Time{}, nil
	}

	messages := sf.Messages

	stats := Stats{
		TotalTurns:            sf.Stats.TotalTurns,
		TotalPromptTokens:     sf.Stats.TotalPromptTokens,
		TotalCompletionTokens: sf.Stats.TotalCompletionTokens,
		TotalCacheHitTokens:   sf.Stats.TotalCacheHitTokens,
		TotalCacheMissTokens:  sf.Stats.TotalCacheMissTokens,
		TotalReasoningTokens:  sf.Stats.TotalReasoningTokens,
		TotalDurationMs:       sf.Stats.TotalDurationMs,
		MessageCount:          sf.Stats.MessageCount,
		TotalCost:             sf.Stats.TotalCost,
		ToolErrors:            sf.Stats.ToolErrors,
		ModelErrors:           sf.Stats.ModelErrors,
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

	return messages, stats, compData, sf.SessionID, sf.Name, sf.Tasks, sf.TodoItems, sf.PlanMode, sf.FileHistory, lastBackgroundCheck, nil
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

// maxSessionNameLen 是 session name 的最大长度(rune 数),防止 recent.json 膨胀。
const maxSessionNameLen = 64

// normalizeSessionName 规范化 session name:TrimSpace + 截断到 maxSessionNameLen。
// 同时过滤 C0 控制字符(换行/制表符/ANSI 转义等)、DEL 与 C1 控制字符,
// 防止破坏 ls 表格输出或注入终端控制序列。
// 空 name 返回空字符串。name 仅作展示,不参与文件路径与恢复匹配。
func normalizeSessionName(name string) string {
	n := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, name)
	n = strings.TrimSpace(n)
	if n == "" {
		return ""
	}
	runes := []rune(n)
	if len(runes) > maxSessionNameLen {
		return string(runes[:maxSessionNameLen])
	}
	return n
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

