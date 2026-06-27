// Package compaction — 四级水位线上下文压缩系统。
//
// 核心目标：在几十甚至上百轮的持续执行中，降低信噪比、防止上下文溢出，
// 同时最大化 DeepSeek 前缀缓存命中率。
//
// 四级水位线：
//   - Tier 0 (< 60%): 什么都不做
//   - Tier 1 (60-80%): Snip — 工具结果差分截断（纯本地，零 API 调用）
//   - Tier 2 (80-95%): Prune — reasoning 清除 + 占位符替换 + 用户代码块压缩（纯本地）
//   - Tier 3 (≥ 95%): Summarize — LLM 增量摘要（需 API 调用）
//   - 硬临界值 (≥ 98%): 阻止后续 LLM 调用
//
// 单调边界保证：一旦对某条消息做出压缩决策，该决策在本次 session 的所有后续轮次中永远不变。
// 通过 compactionDecisionSet + 双 cursor（Tier1Cursor / Tier2Cursor）实现。
package compaction

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"waveloom/pkg/llm"
)

// ---------------------------------------------------------------------------
// 常量
// ---------------------------------------------------------------------------

const (
	// 水位线阈值（通过 CompactionConfig 可覆盖）
	DefaultTier1Threshold = 0.60 // 60% — 预防性维护甜点
	DefaultTier2Threshold = 0.80 // 80% — 危险线
	DefaultTier3Threshold = 0.95 // 95% — 最后防线，触发 LLM 增量摘要

	// 保护区大小
	DefaultProtectionZoneTokens = 8000 // 最近 8000 token 不参与压缩

	// 窗口上限缓冲
	ContextLimitBuffer = 0.98 // 在 98% 处硬截断，留 2% 缓冲

	// 默认上下文窗口大小（DeepSeek V4）
	DefaultContextLimit = 1_000_000

	// Tier 3 摘要最大输出 token
	SummaryMaxTokens = 2000

	// Tier 3 连续失败上限
	MaxTier3ConsecutiveFailures = 2
)

// ---------------------------------------------------------------------------
// 压缩决策
// ---------------------------------------------------------------------------

// CompactionDecision 记录对单条消息的一次压缩决策。
// 按消息绝对索引持久化，实现单调边界保证。
type CompactionDecision struct {
	MsgIndex     int    `json:"msg_index"`     // messages 数组中的绝对索引
	DecisionTier int    `json:"decision_tier"` // 做出决策时的 tier（1/2/3）
	Action       string `json:"action"`        // "snip" | "prune"
	TokensSaved  int    `json:"tokens_saved"`  // 本次决策节省的估算 token 数
	AppliedAt    int    `json:"applied_at"`    // 决策生效的 loop 序号（TotalTurns）
}

// compactionDecisionSet 以 MsgIndex 严格升序排列的压缩决策集合。
// 通过二分查找实现 O(log N) 的 canApply 操作。
// 零值为 nil，表示空集合。
type compactionDecisionSet []CompactionDecision

// canApply 判断是否可以对指定消息应用压缩操作。
// 返回 true 表示可以执行；false 表示已有更强决策保护。
func (ds compactionDecisionSet) canApply(msgIndex int, action string) bool {
	idx := sort.Search(len(ds), func(i int) bool {
		return ds[i].MsgIndex >= msgIndex
	})
	if idx >= len(ds) || ds[idx].MsgIndex != msgIndex {
		return true // 无现有决策 → 可以执行
	}
	existing := ds[idx]
	// 决策强度：prune > snip
	// snip 可升级为 prune；无记录表示未处理，可执行 snip 或 prune
	if action == "prune" && existing.Action == "snip" {
		return true // snip 可以升级为 prune
	}
	return false // 不允许降级或同级别重复
}

// upsert 插入或更新一条决策，维持 MsgIndex 升序不变。
func (ds *compactionDecisionSet) upsert(d CompactionDecision) {
	idx := sort.Search(len(*ds), func(i int) bool {
		return (*ds)[i].MsgIndex >= d.MsgIndex
	})
	if idx < len(*ds) && (*ds)[idx].MsgIndex == d.MsgIndex {
		// 替换已有记录
		(*ds)[idx] = d
	} else {
		// 插入新记录
		*ds = append(*ds, CompactionDecision{})
		copy((*ds)[idx+1:], (*ds)[idx:])
		(*ds)[idx] = d
	}
}

// ---------------------------------------------------------------------------
// 水位线状态
// ---------------------------------------------------------------------------

// WatermarkState 记录当前水位线状态。
type WatermarkState struct {
	CurrentTier     int     `json:"current_tier"`      // 当前触发的 tier（0-3）
	UsageRatio      float64 `json:"usage_ratio"`       // 当前上下文利用率（基于 API 真实 usage）
	LastUsageTokens int     `json:"last_usage_tokens"` // 上一轮的 total_tokens（来自 API usage）
	ContextLimit    int     `json:"context_limit"`     // 模型上下文上限
	Tier1Cursor     int     `json:"tier1_cursor"`      // Tier 1 已处理的 messages 索引前沿
	Tier2Cursor     int     `json:"tier2_cursor"`      // Tier 2 已处理的 messages 索引前沿
	Tier3Cursor     int     `json:"tier3_cursor"`      // Tier 3 摘要已覆盖的 messages 索引前沿

	// Tier 3 连续失败计数
	Tier3ConsecutiveFailures int `json:"tier3_consecutive_failures"`
}

// CompactionConfig 压缩系统可配置参数。
type CompactionConfig struct {
	Tier1Threshold       float64 // Tier 1 触发阈值（默认 0.60）
	Tier2Threshold       float64 // Tier 2 触发阈值（默认 0.80）
	Tier3Threshold       float64 // Tier 3 触发阈值（默认 0.95）
	ProtectionZoneTokens int     // 保护区 token 数（默认 8000）
	ContextLimit         int     // 模型上下文上限（默认 1_000_000）
}

// DefaultCompactionConfig 返回默认压缩配置。
func DefaultCompactionConfig() CompactionConfig {
	return CompactionConfig{
		Tier1Threshold:       DefaultTier1Threshold,
		Tier2Threshold:       DefaultTier2Threshold,
		Tier3Threshold:       DefaultTier3Threshold,
		ProtectionZoneTokens: DefaultProtectionZoneTokens,
		ContextLimit:         DefaultContextLimit,
	}
}

// normalize 对零值字段用默认常量兜底，返回规范化后的配置。
func (c CompactionConfig) normalize() CompactionConfig {
	if c.Tier1Threshold <= 0 {
		c.Tier1Threshold = DefaultTier1Threshold
	}
	if c.Tier2Threshold <= 0 {
		c.Tier2Threshold = DefaultTier2Threshold
	}
	if c.Tier3Threshold <= 0 {
		c.Tier3Threshold = DefaultTier3Threshold
	}
	if c.ProtectionZoneTokens <= 0 {
		c.ProtectionZoneTokens = DefaultProtectionZoneTokens
	}
	if c.ContextLimit <= 0 {
		c.ContextLimit = DefaultContextLimit
	}
	return c
}

// Summarizer 执行 LLM 增量摘要。
// 由调用方注入具体的 LLM 调用实现。
type Summarizer interface {
	// Summarize 基于已有摘要链和增量消息产出本阶段摘要。
	// existingSummaries 为之前所有摘要的 JSON 字符串（不可变），
	// deltaMessages 为本阶段新增的消息。
	// 返回 JSON 格式的结构化摘要字符串。
	Summarize(ctx context.Context, existingSummaries []string, deltaMessages []llm.Message) (string, error)
}

// ---------------------------------------------------------------------------
// CompactionResult — 压缩操作结果
// ---------------------------------------------------------------------------

// CompactionResult 记录一次 CompleteRun 中压缩操作的结果。
type CompactionResult struct {
	Tier               int    // 本次触发的最高 tier
	HardLimitReached   bool   // 是否触发硬临界值（≥ 98% 或 Tier 3 连续失败）
	HardLimitReason    string // 触发原因："usage" 或 "tier3_failures"
	MessagesPruned     int    // 被 prune 的消息数
	MessagesSnipped    int    // 被 snip 的消息数
	TokensSaved        int    // 估算节省 token 数
	Tier3SummaryDone   bool   // Tier 3 摘要是否成功执行
	Tier3Error         error  // Tier 3 失败时的错误（nil 表示成功或未执行）
	ProtectionStartIdx int    // 保护区起始索引（供外部 Tier 3 分步执行）
}

// ---------------------------------------------------------------------------
// CompactionData — 持久化快照
// ---------------------------------------------------------------------------

// CompactionData 保存压缩系统的完整状态（用于持久化和恢复）。
type CompactionData struct {
	Decisions  compactionDecisionSet
	Watermark  WatermarkState
	Summaries  []string
	TotalTurns int
}

// NewDecisionSetFromList 从 CompactionDecision 列表构造决策表。
// 列表会被按 MsgIndex 排序以确保集合不变式。
func NewDecisionSetFromList(list []CompactionDecision) compactionDecisionSet {
	sort.Slice(list, func(i, j int) bool {
		return list[i].MsgIndex < list[j].MsgIndex
	})
	return compactionDecisionSet(list)
}

// DecisionSetToList 将决策表转为 CompactionDecision 列表（已按 MsgIndex 升序）。
func DecisionSetToList(ds compactionDecisionSet) []CompactionDecision {
	return []CompactionDecision(ds)
}

// ---------------------------------------------------------------------------
// 工具差异化截断策略
// ---------------------------------------------------------------------------

// truncationStrategy 定义一个工具的截断参数。
// maxLines 为 0 表示不截断。
// maxLineChars 为 0 表示不限制单行长度。
// maxTotalChars 为 0 表示不限制总字符数。
type truncationStrategy struct {
	maxLines      int
	headLines     int
	tailLines     int
	maxLineChars  int // 单行最大字符数，超出则截断该行
	maxTotalChars int // 总内容最大字符数，超出则整体截断
}

// toolTruncationStrategies 定义所有已知工具的 Tier 1 截断策略。
var toolTruncationStrategies = map[string]truncationStrategy{
	"read_file":      {200, 150, 10, 4000, 30000},
	"shell":          {60, 20, 30, 2000, 12000},
	"grep":           {60, 50, 0, 2000, 15000},
	"web_fetch":      {200, 150, 10, 4000, 30000},
	"ls":             {100, 80, 10, 2000, 10000},
	"search_file":    {80, 60, 10, 2000, 10000},
	"lsp_diagnostic": {60, 40, 10, 2000, 8000},
	"lsp_references": {60, 40, 10, 2000, 8000},
}

// truncatableTools Tier 2 中可替换为占位符的工具集合。
var truncatableTools = map[string]bool{
	"read_file":      true,
	"shell":          true,
	"grep":           true,
	"web_fetch":      true,
	"ls":             true,
	"search_file":    true,
	"lsp_diagnostic": true,
	"lsp_references": true,
}

// ---------------------------------------------------------------------------
// Token 估算（仅用于内部排序和保护区计算，不用于触发判断）
// ---------------------------------------------------------------------------

// estimatedTokensFromContent 估算文本的 token 数。
// 中文字符 ~0.6 tokens/char，英文/ASCII ~0.3 tokens/char。
// 误差 30-50%，仅用于相对大小比较，不用于触发判断。
func estimatedTokensFromContent(s string) int {
	if s == "" {
		return 0
	}
	var tokens float64
	for _, r := range s {
		if r <= 0x7F {
			tokens += 0.3
		} else if utf8.RuneLen(r) > 1 {
			tokens += 0.6
		} else {
			tokens += 0.3
		}
	}
	return int(tokens) + 1
}

// estimatedTokensFromMessage 估算单条消息序列化后的 token 数。
// 直接将 Message 序列化为 JSON（与 API 实际发送格式一致），
// 对完整 JSON 字符串做 token 估算，自动覆盖所有字段及 JSON 结构开销。
//
// 此估算仅用于保护区计算和内部排序（相对大小），不用于 Tier 触发判断。
func estimatedTokensFromMessage(msg llm.Message) int {
	data, err := json.Marshal(msg)
	if err != nil {
		// 回退：逐字段手动估算
		return estimatedTokensFromMessageFallback(msg)
	}
	return estimatedTokensFromContent(string(data))
}

// estimatedTokensFromMessageFallback 是 estimatedTokensFromMessage 的回退路径，
// 仅在 json.Marshal 失败时使用（理论上不会发生）。
func estimatedTokensFromMessageFallback(msg llm.Message) int {
	tokens := estimatedTokensFromContent(msg.Content)
	tokens += estimatedTokensFromContent(msg.ReasoningContent)
	tokens += estimatedTokensFromContent(msg.ToolCallID)
	tokens += estimatedTokensFromContent(msg.Name)
	for _, tc := range msg.ToolCalls {
		tokens += estimatedTokensFromContent(tc.ID)
		tokens += estimatedTokensFromContent(tc.Name)
		tokens += estimatedTokensFromContent(tc.Arguments)
	}
	tokens += 20 // JSON 结构开销
	return tokens
}

// ---------------------------------------------------------------------------
// 保护区计算
// ---------------------------------------------------------------------------

// findProtectionStartIdx 从 messages 末尾向前累加 token 估算，直到累计 ≥ protectionTokens。
// 返回保护区起始索引（含）。若整个 messages 都不足 protectionTokens，返回 0。
func findProtectionStartIdx(messages []llm.Message, protectionTokens int) int {
	if len(messages) == 0 {
		return 0
	}

	accumulated := 0
	for i := len(messages) - 1; i >= 0; i-- {
		accumulated += estimatedTokensFromMessage(messages[i])
		if accumulated >= protectionTokens {
			return i
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// Tier 1: Snip — 工具结果截断
// ---------------------------------------------------------------------------

// applyTier1 对 messages[tier1Cursor:protectionStartIdx) 范围内的 tool 消息执行截断。
// 推进 tier1Cursor 到 protectionStartIdx。
// 返回被 snip 的消息数和估算节省的 token 数。
func applyTier1(
	messages []llm.Message,
	decisions *compactionDecisionSet,
	tier1Cursor *int,
	protectionStartIdx int,
	totalTurns int,
) (snipped int, tokensSaved int) {
	scanStart := *tier1Cursor
	scanEnd := protectionStartIdx

	if scanStart >= scanEnd {
		return 0, 0
	}

	// 确保索引在有效范围
	if scanStart < 2 {
		scanStart = 2
	}
	if scanEnd > len(messages) {
		scanEnd = len(messages)
	}

	for i := scanStart; i < scanEnd; i++ {
		msg := &messages[i]
		if msg.Role != llm.RoleTool {
			continue
		}
		if !decisions.canApply(i, "snip") {
			continue
		}

		strategy, ok := toolTruncationStrategies[msg.Name]
		if !ok || strategy.maxLines == 0 {
			continue // 不截断的工具
		}

		originalContent := msg.Content
		truncated, didTruncate := truncateByStrategy(originalContent, strategy)
		if !didTruncate {
			continue
		}

		beforeTokens := estimatedTokensFromContent(originalContent)
		afterTokens := estimatedTokensFromContent(truncated)
		saved := beforeTokens - afterTokens
		if saved < 0 {
			saved = 0
		}

		msg.Content = truncated
		decisions.upsert(CompactionDecision{
			MsgIndex:     i,
			DecisionTier: 1,
			Action:       "snip",
			TokensSaved:  saved,
			AppliedAt:    totalTurns,
		})
		snipped++
		tokensSaved += saved
	}

	*tier1Cursor = scanEnd
	return snipped, tokensSaved
}

// truncateByStrategy 按截断策略处理内容。
// 三级截断：行数截断 → 总字符截断 → 单行字符截断。
// 返回截断后的内容和是否实际触发截断。
func truncateByStrategy(content string, s truncationStrategy) (string, bool) {
	if content == "" {
		return content, false
	}

	lines := strings.Split(content, "\n")

	// 1. 行数截断（优先，最语义化）
	if s.maxLines > 0 && len(lines) > s.maxLines && len(lines) > s.headLines+s.tailLines+10 {
		head := strings.Join(lines[:s.headLines], "\n")
		omitted := len(lines) - s.headLines - s.tailLines

		if s.tailLines > 0 {
			tail := strings.Join(lines[len(lines)-s.tailLines:], "\n")
			result := fmt.Sprintf("%s\n\n[... 省略 %d 行 — 完整结果已由 Agent 处理]\n\n%s", head, omitted, tail)
			return result, true
		}

		result := fmt.Sprintf("%s\n\n[... 省略 %d 行]\n", head, omitted)
		return result, true
	}

	// 2. 总字符截断 — 在换行边界处切断以保持可读性
	if s.maxTotalChars > 0 && len(content) > s.maxTotalChars {
		cutPoint := s.maxTotalChars
		if idx := strings.LastIndex(content[:cutPoint], "\n"); idx > cutPoint/2 {
			cutPoint = idx
		}
		content = content[:cutPoint] + fmt.Sprintf(
			"\n[... 内容截断: %d → %d 字符 — 完整结果已由 Agent 处理]",
			len(content), cutPoint,
		)
		return content, true
	}

	// 3. 单行字符截断 — 处理超长单行（如 minified JSON、base64 blob）
	didTruncate := false
	if s.maxLineChars > 0 {
		for i, line := range lines {
			if len(line) > s.maxLineChars {
				lines[i] = line[:s.maxLineChars] + fmt.Sprintf(
					"... [行截断: %d → %d 字符]", len(line), s.maxLineChars,
				)
				didTruncate = true
			}
		}
	}

	if didTruncate {
		return strings.Join(lines, "\n"), true
	}

	return content, false
}

// ---------------------------------------------------------------------------
// Tier 2: Prune — reasoning 清除 + 占位符替换 + 用户代码块压缩
// ---------------------------------------------------------------------------

// applyTier2 对 messages[tier2Cursor:protectionStartIdx) 范围内的消息执行激进压缩。
// 推进 tier2Cursor 到 protectionStartIdx。
func applyTier2(
	messages []llm.Message,
	decisions *compactionDecisionSet,
	tier2Cursor *int,
	protectionStartIdx int,
	totalTurns int,
) (pruned int, tokensSaved int) {
	scanStart := *tier2Cursor
	scanEnd := protectionStartIdx

	if scanStart >= scanEnd {
		return 0, 0
	}

	if scanStart < 2 {
		scanStart = 2
	}
	if scanEnd > len(messages) {
		scanEnd = len(messages)
	}

	for i := scanStart; i < scanEnd; i++ {
		msg := &messages[i]

		switch msg.Role {
		case llm.RoleAssistant:
			// 清空 reasoning_content
			if msg.ReasoningContent != "" && decisions.canApply(i, "prune") {
				saved := estimatedTokensFromContent(msg.ReasoningContent)
				msg.ReasoningContent = ""
				decisions.upsert(CompactionDecision{
					MsgIndex:     i,
					DecisionTier: 2,
					Action:       "prune",
					TokensSaved:  saved,
					AppliedAt:    totalTurns,
				})
				pruned++
				tokensSaved += saved
			}

		case llm.RoleTool:
			if !truncatableTools[msg.Name] {
				continue // ls / search_file / edit_file / write_file 不动
			}
			if !decisions.canApply(i, "prune") {
				continue
			}

			originalContent := msg.Content
			placeholder := formatToolPlaceholder(msg.Name, originalContent)
			saved := estimatedTokensFromContent(originalContent) - estimatedTokensFromContent(placeholder)
			if saved < 0 {
				saved = 0
			}

			msg.Content = placeholder
			decisions.upsert(CompactionDecision{
				MsgIndex:     i,
				DecisionTier: 2,
				Action:       "prune",
				TokensSaved:  saved,
				AppliedAt:    totalTurns,
			})
			pruned++
			tokensSaved += saved

		case llm.RoleUser:
			if !decisions.canApply(i, "prune") {
				continue
			}
			newContent, didCompress := compressUserCodeBlocks(msg.Content)
			if didCompress {
				saved := estimatedTokensFromContent(msg.Content) - estimatedTokensFromContent(newContent)
				if saved < 0 {
					saved = 0
				}
				msg.Content = newContent
				decisions.upsert(CompactionDecision{
					MsgIndex:     i,
					DecisionTier: 2,
					Action:       "prune",
					TokensSaved:  saved,
					AppliedAt:    totalTurns,
				})
				pruned++
				tokensSaved += saved
			}
		}
	}

	*tier2Cursor = scanEnd
	return pruned, tokensSaved
}

// formatToolPlaceholder 为已被压缩的 tool 结果生成占位符。
func formatToolPlaceholder(toolName, originalContent string) string {
	lines := strings.Count(originalContent, "\n") + 1
	chars := len(originalContent)
	tokens := estimatedTokensFromContent(originalContent)
	return fmt.Sprintf("[tool call output compressed] Output of tool %s has been compressed (original: %d lines, %d chars, ~%d tokens)",
		toolName, lines, chars, tokens)
}

// compressUserCodeBlocks 压缩 user 消息中 >50 行的 code fence 内容，
// 以及 code fence 中单行超过 maxCodeLineChars 的超长行。
// 返回压缩后的内容和是否实际发生了压缩。
func compressUserCodeBlocks(content string) (string, bool) {
	// 快速路径：无 code fence
	if !strings.Contains(content, "```") {
		return content, false
	}

	const maxCodeLineChars = 2000

	var result strings.Builder
	result.Grow(len(content))

	lines := strings.Split(content, "\n")
	inFence := false
	fenceLines := 0
	openingBacktickCount := 0 // 进入 fence 时的连续反引号数，用于匹配关闭
	didCompress := false

	for _, line := range lines {
		bt := countLeadingBackticks(line)
		if bt >= 3 {
			if !inFence {
				// 进入 fence
				inFence = true
				fenceLines = 0
				openingBacktickCount = bt
			} else if bt >= openingBacktickCount {
				// 退出 fence（关闭反引号数 >= 开启数）
				inFence = false
				if fenceLines > 50 {
					didCompress = true
				}
			}
			// bt >= 3 but bt < openingBacktickCount: fence 内容，不切换状态
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		if inFence {
			fenceLines++
			if fenceLines <= 50 {
				if len(line) > maxCodeLineChars {
					result.WriteString(line[:maxCodeLineChars])
					result.WriteString(fmt.Sprintf("... [单行截断: %d → %d 字符]", len(line), maxCodeLineChars))
					result.WriteString("\n")
					didCompress = true
				} else {
					result.WriteString(line)
					result.WriteString("\n")
				}
			} else if fenceLines == 51 {
				// 插入占位符，跳过后续 fence 行
				result.WriteString("[pasted content compressed (original: >50 lines)]\n")
			}
			// fenceLines > 51: 跳过，不写入
		} else {
			result.WriteString(line)
			result.WriteString("\n")
		}
	}

	// 未闭合 fence：将已收集的 fence 内容按压缩规则处理完毕
	if inFence && fenceLines > 50 {
		didCompress = true
	}

	// 保留原内容末尾换行符语义
	raw := result.String()
	if len(content) > 0 && content[len(content)-1] == '\n' {
		return raw, didCompress
	}
	return strings.TrimRight(raw, "\n"), didCompress
}

// countLeadingBackticks 返回行首连续反引号数量。
func countLeadingBackticks(s string) int {
	n := 0
	for _, r := range s {
		if r == '`' {
			n++
		} else {
			break
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// Tier 3: Summarize — LLM 增量摘要
// ---------------------------------------------------------------------------

// summarizableRoles Tier 3 摘要中包含的消息角色。
var summarizableRoles = map[llm.Role]bool{
	llm.RoleUser:      true,
	llm.RoleAssistant: true,
	llm.RoleTool:      true,
}

// applyTier3 对 messages[tier3Cursor:protectionStartIdx) 执行 LLM 增量摘要。
// 删除 delta 消息，将新摘要追加到摘要链末尾。
// 重置三个 cursor。
func applyTier3(
	ctx context.Context,
	messages *[]llm.Message,
	decisions *compactionDecisionSet,
	watermark *WatermarkState,
	protectionStartIdx int,
	summarizer Summarizer,
	existingSummaries *[]string,
) (bool, error) {
	tier3Cursor := watermark.Tier3Cursor
	scanEnd := protectionStartIdx

	if tier3Cursor >= scanEnd {
		return false, nil // 无待摘要消息
	}

	if tier3Cursor < 2 {
		tier3Cursor = 2
	}

	// 收集 delta 消息
	delta := make([]llm.Message, 0, scanEnd-tier3Cursor)
	for i := tier3Cursor; i < scanEnd; i++ {
		msg := (*messages)[i]
		if summarizableRoles[msg.Role] {
			// 已被 Tier 2 prune 的 tool 消息使用简化内容
			delta = append(delta, msg)
		}
	}

	if len(delta) == 0 {
		return false, nil
	}

	// 调用 LLM 摘要
	summary, err := summarizer.Summarize(ctx, *existingSummaries, delta)
	if err != nil {
		watermark.Tier3ConsecutiveFailures++
		return false, err
	}

	// 成功：重置失败计数
	watermark.Tier3ConsecutiveFailures = 0

	// 删除 delta 范围内的消息
	oldLen := len(*messages)
	newMessages := make([]llm.Message, 0, tier3Cursor+1+(oldLen-scanEnd))
	newMessages = append(newMessages, (*messages)[:tier3Cursor]...)

	// 追加摘要作为 user 消息
	summaryMsg := llm.Message{
		Role:    llm.RoleUser,
		Content: summary,
	}
	newMessages = append(newMessages, summaryMsg)

	// 追加保护区及之后的消息
	newMessages = append(newMessages, (*messages)[scanEnd:]...)

	*messages = newMessages

	// 追加摘要到摘要链
	*existingSummaries = append(*existingSummaries, summary)

	// Tier 3 重建了消息数组，此前所有 decisions 对新索引均无效，直接清空。
	*decisions = compactionDecisionSet{}

	// 重置三个 cursor
	newCursor := tier3Cursor + 1 // 摘要消息之后
	watermark.Tier1Cursor = newCursor
	watermark.Tier2Cursor = newCursor
	watermark.Tier3Cursor = newCursor

	return true, nil
}

// ---------------------------------------------------------------------------
// 硬临界值检查
// ---------------------------------------------------------------------------

// checkHardLimit 检查是否达到硬临界值（98% 或 Tier 3 连续失败 2 次）。
// 返回 true 表示已达硬限制，后续 LLM 调用应被阻止。
func checkHardLimit(usageRatio float64, tier3ConsecutiveFailures int) (bool, string) {
	if usageRatio >= ContextLimitBuffer {
		return true, "usage"
	}
	if tier3ConsecutiveFailures >= MaxTier3ConsecutiveFailures {
		return true, "tier3_failures"
	}
	return false, ""
}

// ---------------------------------------------------------------------------
// 完整压缩流程
// ---------------------------------------------------------------------------

// CompactMessages 对消息历史执行完整四级水位线压缩。
//
// 参数：
//   - messages: 当前完整消息历史（原地修改）
//   - contextTokens: 当前上下文实际 token 数（末轮 API 返回的 prompt_tokens，非跨轮累加值）
//   - watermark: 当前水位线状态（原地更新）
//   - decisions: 当前压缩决策表（原地更新）
//   - totalTurns: 累计完成的 loop 数
//   - config: 压缩配置（阈值等）
//   - summarizer: Tier 3 摘要执行器（nil 时 Tier 3 降级跳过）
//   - existingSummaries: 已有摘要链（Tier 3 成功时追加）
//
// 返回 CompactionResult。
func CompactMessages(
	ctx context.Context,
	messages *[]llm.Message,
	contextTokens int,
	watermark *WatermarkState,
	decisions *compactionDecisionSet,
	totalTurns int,
	config CompactionConfig,
	summarizer Summarizer,
	existingSummaries *[]string,
) CompactionResult {
	result := CompactionResult{}

	if watermark.ContextLimit <= 0 {
		watermark.ContextLimit = config.ContextLimit
	}

	// 1. 计算上下文利用率（使用末轮 API 返回的 prompt_tokens，而非跨轮累加值）
	watermark.LastUsageTokens = contextTokens
	watermark.UsageRatio = float64(contextTokens) / float64(watermark.ContextLimit)

	// 2. 确定当前 tier
	var tier int
	switch {
	case watermark.UsageRatio >= config.Tier3Threshold:
		tier = 3
	case watermark.UsageRatio >= config.Tier2Threshold:
		tier = 2
	case watermark.UsageRatio >= config.Tier1Threshold:
		tier = 1
	default:
		tier = 0
	}
	watermark.CurrentTier = tier

	// 3. 硬临界值检查
	if reached, reason := checkHardLimit(watermark.UsageRatio, watermark.Tier3ConsecutiveFailures); reached {
		result.HardLimitReached = true
		result.HardLimitReason = reason
		// 仍继续执行 Tier 1/2（非 LLM）并计算 ProtectionStartIdx，
		// 确保 tier3_failures 触发的硬限仍能给 Tier 3 一次恢复机会
		protectionStartIdx := findProtectionStartIdx(*messages, config.ProtectionZoneTokens)
		result.ProtectionStartIdx = protectionStartIdx
		if tier >= 1 {
			snipped, saved := applyTier1(*messages, decisions, &watermark.Tier1Cursor, protectionStartIdx, totalTurns)
			result.MessagesSnipped = snipped
			result.TokensSaved += saved
		}
		if tier >= 2 {
			pruned, saved := applyTier2(*messages, decisions, &watermark.Tier2Cursor, watermark.Tier1Cursor, totalTurns)
			result.MessagesPruned = pruned
			result.TokensSaved += saved
		}
		result.Tier = tier
		return result
	}

	// 4. 计算保护区起始索引
	protectionStartIdx := findProtectionStartIdx(*messages, config.ProtectionZoneTokens)
	result.ProtectionStartIdx = protectionStartIdx

	// 5. Tier 1（如 tier ≥ 1）
	if tier >= 1 {
		snipped, saved := applyTier1(*messages, decisions, &watermark.Tier1Cursor, protectionStartIdx, totalTurns)
		result.MessagesSnipped = snipped
		result.TokensSaved += saved
	}

	// 6. Tier 2（如 tier ≥ 2）
	if tier >= 2 {
		pruned, saved := applyTier2(*messages, decisions, &watermark.Tier2Cursor, protectionStartIdx, totalTurns)
		result.MessagesPruned = pruned
		result.TokensSaved += saved
	}

	// 7. Tier 3（如 tier ≥ 3）
	if tier >= 3 && summarizer != nil {
		done, err := applyTier3(ctx, messages, decisions, watermark, protectionStartIdx, summarizer, existingSummaries)
		if err != nil {
			// Tier 3 失败不中断，记录后继续
			result.Tier = tier
			result.Tier3Error = err
			return result
		}
		if done {
			result.Tier3SummaryDone = true
		}
	}

	result.Tier = tier
	return result
}

// ---------------------------------------------------------------------------
// 摘要格式化工具
// ---------------------------------------------------------------------------

// FormatSummaryPrompt 返回 Tier 3 摘要的系统提示词。
func FormatSummaryPrompt() string {
	return `你是一个专业的开发会话交班记录员。你的任务是基于已有的摘要链和最新的对话增量，
产出一份结构化的 JSON 摘要，供接手同事快速了解当前进展。

## 输出格式

严格输出以下 JSON 结构（不要包含其他文本）：

{
  "progress": {
    "summary": "<200 字的中文进展概述>",
    "files": [
      {"path": "相对路径", "action": "created|modified|deleted|read", "why": "变更意图（一句话）"}
    ]
  },
  "pending": ["未完成任务 1", "未完成任务 2"],
  "pitfalls": [
    {"problem": "遇到的问题", "solution": "解决方案"}
  ],
  "constraints": "接下来必须遵守的约束（用户偏好、禁止事项、环境限制）"
}

## 规则

- progress.summary：用 <200 字描述本轮做了什么，聚焦关键决策。
- progress.files：列出涉及的每个文件，用 "why" 说明变更意图而非文件内容（内容可以通过 read_file 获取）。
- pending：列出明确未完成的任务，空数组写 []。
- pitfalls：从失败中提炼的经验，空数组写 []。
- constraints：继承已有摘要的 constraints，追加本轮发现的新约束。
- 不要重写已有摘要的内容，仅产出本阶段的增量进展。`
}

// FormatSummaryUserMessage 构造 Tier 3 摘要请求的 user 消息。
func FormatSummaryUserMessage(existingSummaries []string, deltaMessages []llm.Message) string {
	var b strings.Builder

	if len(existingSummaries) > 0 {
		b.WriteString("## 已有摘要链（不可修改）\n\n")
		for i, s := range existingSummaries {
			b.WriteString(fmt.Sprintf("### Summary %d\n\n```json\n%s\n```\n\n", i+1, s))
		}
		b.WriteString("---\n\n")
	}

	b.WriteString("## 本轮增量消息\n\n")
	for _, msg := range deltaMessages {
		role := string(msg.Role)
		content := msg.Content
		// 截断超长内容以控制摘要输入
		if len(content) > 2000 {
			content = content[:2000] + "\n... [内容已截断]"
		}
		b.WriteString(fmt.Sprintf("### [%s]\n\n%s\n\n", role, content))
	}

	return b.String()
}
