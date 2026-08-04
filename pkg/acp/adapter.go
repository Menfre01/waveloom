package acp

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/Menfre01/waveloom/pkg/agentloop"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// Adapter — TurnEvent → ACP session/update 通知
// ---------------------------------------------------------------------------

type adapter struct {
	sessionID        string
	messageID        string // 正文 chunk 的稳定消息 ID
	thoughtMessageID string // 思考链 chunk 的独立消息 ID(避免客户端聚合时混入正文)
	sendFn           func(msg any) error
	contextLimit     uint64 // 上下文窗口容量 token 数(usage_update size 字段);0 = 未知 → 回退消息数

	// 本轮 TurnStats 累积(供 handler 提交 CompleteRun;usage_update 的
	// used 取当轮上下文大小,与 TUI HUD 同源——官方语义为
	// "Tokens currently in context")。
	promptTokens     int
	completionTokens int
	cacheHitTokens   int
	cacheMissTokens  int
	reasoningTokens  int
	messageCount     int
}

func newAdapter(sessionID string, sendFn func(msg any) error) *adapter {
	return newAdapterWithContextLimit(sessionID, sendFn, 0)
}

// newAdapterWithContextLimit 创建适配器并指定上下文窗口容量。
// contextLimit 用于 usage_update 的 size 字段(官方语义:
// "Total context window size in tokens")。
func newAdapterWithContextLimit(sessionID string, sendFn func(msg any) error, contextLimit int) *adapter {
	return &adapter{
		sessionID:        sessionID,
		messageID:        llm.NewMessageID(),
		thoughtMessageID: llm.NewMessageID(),
		sendFn:           sendFn,
		contextLimit:     uint64(max(0, contextLimit)),
	}
}

// adapterStats 是本轮 prompt 的 token/消息累积统计。
type adapterStats struct {
	PromptTokens     int
	CompletionTokens int
	CacheHitTokens   int
	CacheMissTokens  int
	ReasoningTokens  int
	MessageCount     int
}

// Stats 返回本轮消费的 TurnStats 累积值。
func (a *adapter) Stats() adapterStats {
	return adapterStats{
		PromptTokens:     a.promptTokens,
		CompletionTokens: a.completionTokens,
		CacheHitTokens:   a.cacheHitTokens,
		CacheMissTokens:  a.cacheMissTokens,
		ReasoningTokens:  a.reasoningTokens,
		MessageCount:     a.messageCount,
	}
}

func (a *adapter) consumeEvents(ctx context.Context, ch <-chan agentloop.TurnEvent) (agentloop.LoopDone, bool) {
	for {
		select {
		case <-ctx.Done():
			// REGRESSION: 不能在此立即返回——agentloop.Loop 的契约保证取消后
			// 仍会先发送携带真实 Messages 的 LoopDone 再关闭通道(loop.go Run)。
			// 若提前返回,真实历史会被丢弃,调用方 CompleteRun(nil) 会用空历史
			// 整体替换会话上下文(数据损坏)。转入纯消费循环等待真实 LoopDone。
			for ev := range ch {
				if e, ok := ev.(agentloop.LoopDone); ok {
					return e, true
				}
			}
			// 通道关闭仍未收到 LoopDone(契约被违反的防御路径):返回合成值,
			// 调用方必须检查 bool 跳过 CompleteRun。
			return agentloop.LoopDone{Reason: agentloop.ReasonCompleted}, false
		case ev, ok := <-ch:
			if !ok {
				return agentloop.LoopDone{Reason: agentloop.ReasonCompleted}, false
			}
			switch e := ev.(type) {
			case agentloop.StreamDelta:
				a.handleStreamDelta(e)
			case agentloop.ToolCallStart:
				a.handleToolCallStart(e)
			case agentloop.ToolCallStream:
				a.handleToolCallStream(e)
			case agentloop.ToolCallResult:
				a.handleToolCallResult(e)
			case agentloop.TurnStats:
				a.handleTurnStats(e)
			case agentloop.PlanModeEnter:
				a.handlePlanModeEnter(e)
			case agentloop.PlanModeExit:
				a.handlePlanModeExit(e)
			case agentloop.LoopDone:
				return e, true
			default:
				slog.Debug("acp: unknown event type", "type", ev)
			}
		}
	}
}

func (a *adapter) sendUpdate(updateContent any) {
	updateJSON, err := json.Marshal(updateContent)
	if err != nil {
		slog.Error("acp: marshal update content", "err", err)
		return
	}
	notifParams := SessionUpdateParams{
		SessionID: a.sessionID,
		Update:    json.RawMessage(updateJSON),
	}
	notif, err := NewNotification(MethodSessionUpdate, notifParams)
	if err != nil {
		slog.Error("acp: marshal session/update", "err", err)
		return
	}
	if err := a.sendFn(notif); err != nil {
		slog.Error("acp: send session/update", "err", err)
	}
}

// sendUserMessage 回显用户消息(Zed 会话显示 agent 实际处理的 prompt)。
// 在命令注入/skill 展开前发送原始 userText。
func (a *adapter) sendUserMessage(text string) {
	if text == "" {
		return
	}
	a.sendUpdate(ContentChunk{
		SessionUpdate: "user_message_chunk",
		MessageID:     llm.NewMessageID(),
		Content:       ContentBlock{Type: "text", Text: text},
	})
}

// sendSessionInfo 发送 session 元数据更新(标题等)。
func (a *adapter) sendSessionInfo(title string) {
	update := SessionInfoUpdate{SessionUpdate: "session_info_update"}
	if title != "" {
		update.Title = &title
	}
	a.sendUpdate(update)
}

// ---------------------------------------------------------------------------
// StreamDelta → ContentChunk (agent_message_chunk)
// ---------------------------------------------------------------------------

func (a *adapter) handleStreamDelta(e agentloop.StreamDelta) {
	if e.ContentDelta != "" {
		a.sendUpdate(ContentChunk{
			SessionUpdate: "agent_message_chunk",
			MessageID:     a.messageID,
			Content: ContentBlock{
				Type: "text",
				Text: e.ContentDelta,
			},
		})
	}
	if e.ReasoningDelta != "" {
		// 思考链使用标准 agent_thought_chunk 变体 + 独立 messageId
		// (v1 SessionUpdate 枚举),不与正文混用 messageId。
		a.sendUpdate(ContentChunk{
			SessionUpdate: "agent_thought_chunk",
			MessageID:     a.thoughtMessageID,
			Content: ContentBlock{
				Type: "text",
				Text: e.ReasoningDelta,
			},
		})
	}
}

// ---------------------------------------------------------------------------
// PlanModeEnter/Exit → PlanUpdate
// ---------------------------------------------------------------------------

func (a *adapter) handlePlanModeEnter(e agentloop.PlanModeEnter) {
	// PlanEntry 必填 content/priority/status(v1 schema);status 枚举
	// 仅 pending/in_progress/completed。
	a.sendUpdate(PlanUpdate{
		SessionUpdate: "plan",
		Entries: []PlanEntry{
			{Content: e.PlanFile, Priority: "high", Status: "in_progress"},
		},
	})
}

func (a *adapter) handlePlanModeExit(e agentloop.PlanModeExit) {
	// v1 PlanEntryStatus 无 "rejected":未批准时以 completed 结束计划环节
	// (plan 工具在 ACP 不注册,该事件实际不产生,防御性映射)。
	status := "completed"
	a.sendUpdate(PlanUpdate{
		SessionUpdate: "plan",
		Entries: []PlanEntry{
			{Content: e.Plan, Priority: "high", Status: status},
		},
	})
}

// ---------------------------------------------------------------------------
// ToolCallStart / Stream / Result → ToolCallUpdate
// ---------------------------------------------------------------------------

func (a *adapter) handleToolCallStart(e agentloop.ToolCallStart) {
	kind := ToolKind(e.ToolCallName)
	// title 是 Zed 卡片的主显示文本(label)——生态惯例携带参数描述
	// (如 "bash: ls -la");content 作为补充(展开/其他客户端)。
	desc := toolCallArgText(e.ToolCallName, e.Arguments)
	title := e.ToolCallName
	if desc != "" {
		title = e.ToolCallName + ": " + desc
	}
	update := ToolCallUpdate{
		SessionUpdate: "tool_call",
		ToolCallID:    e.ToolCallID,
		Kind:          kind,
		Status:        "pending",
		Title:         title,
		RawInput:      json.RawMessage(e.Arguments),
	}
	// 参数描述放 content(客户端 UI 展示用——rawInput 是机器字段,主流客户端
	// 如 Zed 不渲染)。bash → command 文本;其他工具 → 原始参数 JSON。
	if desc != "" {
		update.Content = []ToolCallContentItem{{
			Type:    "content",
			Content: &ContentBlock{Type: "text", Text: desc},
		}}
	}
	a.sendUpdate(update)
}

// toolCallArgText 提取工具参数的人类可读描述(用于 tool_call 的 title/content)。
// 每个工具提取主要参数,避免 Zed 卡片显示 raw JSON。
func toolCallArgText(toolName, args string) string {
	if args == "" || args == "{}" || args == "null" {
		return ""
	}
	var parts []string
	switch toolName {
	case "bash":
		var p struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(args), &p) == nil && p.Command != "" {
			return p.Command
		}
	case "read":
		var p struct {
			FilePath string `json:"file_path"`
			Pattern  string `json:"pattern"`
		}
		if json.Unmarshal([]byte(args), &p) == nil {
			if p.FilePath != "" {
				parts = append(parts, p.FilePath)
			}
			if p.Pattern != "" {
				parts = append(parts, "pattern: "+p.Pattern)
			}
		}
	case "edit", "write":
		var p struct {
			FilePath string `json:"file_path"`
		}
		if json.Unmarshal([]byte(args), &p) == nil && p.FilePath != "" {
			parts = append(parts, p.FilePath)
		}
	case "web_search":
		var p struct {
			Query string `json:"query"`
		}
		if json.Unmarshal([]byte(args), &p) == nil && p.Query != "" {
			return p.Query
		}
	case "web_fetch":
		var p struct {
			URL string `json:"url"`
		}
		if json.Unmarshal([]byte(args), &p) == nil && p.URL != "" {
			return p.URL
		}
	case "todo_create", "todo_update":
		var p struct {
			Todos []struct {
				Content string `json:"content"`
			} `json:"todos"`
		}
		if json.Unmarshal([]byte(args), &p) == nil && len(p.Todos) > 0 {
			var items []string
			for _, t := range p.Todos {
				items = append(items, t.Content)
			}
			parts = append(parts, strings.Join(items, "; "))
		}
	case "agent":
		var p struct {
			Description string `json:"description"`
			Prompt      string `json:"prompt"`
		}
		if json.Unmarshal([]byte(args), &p) == nil {
			if p.Description != "" {
				parts = append(parts, p.Description)
			}
			if p.Prompt != "" {
				parts = append(parts, truncateArg(p.Prompt, 60))
			}
		}
	case "skill":
		var p struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		if json.Unmarshal([]byte(args), &p) == nil && p.Name != "" {
			parts = append(parts, p.Name)
			if p.Arguments != "" {
				parts = append(parts, truncateArg(p.Arguments, 40))
			}
		}
	case "kill_background_task":
		var p struct {
			TaskID string `json:"task_id"`
		}
		if json.Unmarshal([]byte(args), &p) == nil && p.TaskID != "" {
			return p.TaskID
		}
	}
	if len(parts) == 0 {
		return args // 无法提取:回退原始 JSON
	}
	return strings.Join(parts, " | ")
}

// truncateArg 截断长参数,保持 title 简短。
func truncateArg(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

func (a *adapter) handleToolCallStream(e agentloop.ToolCallStream) {
	a.sendUpdate(ToolCallUpdate{
		// 后续更新必须用 tool_call_update 变体(v1:ToolCall 变体 title 必填)
		SessionUpdate: "tool_call_update",
		ToolCallID:    e.ToolCallID,
		Status:        "in_progress",
		Content: []ToolCallContentItem{
			{Type: "content", Content: &ContentBlock{Type: "text", Text: e.Chunk}},
		},
	})
}

func (a *adapter) handleToolCallResult(e agentloop.ToolCallResult) {
	status := "completed"
	if e.IsError() {
		status = "failed"
	}
	update := ToolCallUpdate{
		SessionUpdate: "tool_call_update",
		ToolCallID:    e.ToolCallID,
		Status:        status,
	}
	if e.Result != "" {
		update.Content = append(update.Content, ToolCallContentItem{
			Type:    "content",
			Content: &ContentBlock{Type: "text", Text: e.Result},
		})
	}
	if e.Error != "" {
		update.Content = append(update.Content, ToolCallContentItem{
			Type:    "content",
			Content: &ContentBlock{Type: "text", Text: "Error: " + e.Error},
		})
	}
	// diff 内容块(v1 结构:{type:"diff", path, oldText, newText}),
	// 由 DiffHunks 的 context/delete → oldText、context/add → newText 重建。
	for _, h := range e.DiffHunks {
		oldText, newText := rebuildDiffText(h)
		if h.FilePath == "" && oldText == "" && newText == "" {
			continue
		}
		update.Content = append(update.Content, ToolCallContentItem{
			Type:    "diff",
			Path:    h.FilePath,
			OldText: oldText,
			NewText: newText,
		})
	}
	// locations:文件跟随(客户端"正在操作的文件"点击跳转,官方
	// "Following the Agent" 功能)。edit/write 的 DiffHunks 提供
	// FilePath + 行号;同文件多 hunk 去重,取首个 hunk 的起始行。
	seen := make(map[string]bool)
	for _, h := range e.DiffHunks {
		if h.FilePath == "" || seen[h.FilePath] {
			continue
		}
		seen[h.FilePath] = true
		loc := ToolCallLocation{Path: h.FilePath}
		if h.OldStart > 0 {
			loc.Line = h.OldStart
		}
		update.Locations = append(update.Locations, loc)
	}
	a.sendUpdate(update)
}

// rebuildDiffText 从 DiffHunk 行数据重建 oldText/newText。
func rebuildDiffText(h tool.DiffHunk) (oldText, newText string) {
	var oldLines, newLines []string
	for _, l := range h.Lines {
		switch l.Kind {
		case tool.DiffAdd:
			newLines = append(newLines, l.Content)
		case tool.DiffDel:
			oldLines = append(oldLines, l.Content)
		case tool.DiffCtx:
			oldLines = append(oldLines, l.Content)
			newLines = append(newLines, l.Content)
		}
	}
	return strings.Join(oldLines, "\n"), strings.Join(newLines, "\n")
}

// ---------------------------------------------------------------------------
// TurnStats → UsageUpdateContent
// ---------------------------------------------------------------------------

func (a *adapter) handleTurnStats(e agentloop.TurnStats) {
	a.promptTokens += e.PromptTokens
	a.completionTokens += e.CompletionTokens
	a.cacheHitTokens += e.CacheHitTokens
	a.cacheMissTokens += e.CacheMissTokens
	a.reasoningTokens += e.ReasoningTokens
	a.messageCount = e.MessageCount
	// used = 当前上下文 token(与 TUI HUD 的 ctx bar 同逻辑):
	// 当轮 PromptTokens;有压缩 → 减 TokensSaved 估算;Tier 3 摘要后 → 0。
	used := uint64(max(0, e.PromptTokens))
	if e.Compaction.HasCompaction() {
		used = uint64(max(0, e.PromptTokens-e.Compaction.TokensSaved))
	}
	if e.Compaction.SummaryDone {
		used = 0
	}
	// size = 上下文窗口总容量 token(官方语义);未配置(0)时回退消息数
	size := uint64(max(0, e.MessageCount))
	if a.contextLimit > 0 {
		size = a.contextLimit
	}
	a.sendUpdate(UsageUpdateContent{
		SessionUpdate: "usage_update",
		Used:          used,
		Size:          size,
	})
}
