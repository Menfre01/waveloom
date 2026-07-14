package hashline

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// Recovery — TAG 过期时的行号重映射
// ---------------------------------------------------------------------------

// RecoverResult 表示恢复尝试的结果。
type RecoverResult struct {
	Success   bool
	MappedOps []Op     // 重映射后的操作
	Warnings  []string // 警告信息
}

// MapStatus 表示快照行在当前文件中的映射状态。
type MapStatus int

const (
	MapUnchanged MapStatus = iota // 行内容未变，位置未变
	MapShifted                    // 行内容未变，位置变化
	MapModified                   // 行内容被修改
	MapDeleted                    // 行被删除
)

// LineMapping 记录快照行号到当前文件行号的映射。
type LineMapping struct {
	OldLine int       // 快照中的行号（1-based）
	NewLine int       // 当前文件中的行号（1-based，0 = 已删除）
	Status  MapStatus
}

// RecoverOps 尝试将操作的行号从快照版本重映射到当前文件版本。
// snapshot 是 TAG 对应快照的完整文件内容。
// current 是磁盘上的当前文件内容。
// ops 是原始操作列表。
//
// 使用 LCS 算法找出快照和当前文件的对应行，然后重映射操作行号。
func RecoverOps(snapshot, current string, ops []Op) *RecoverResult {
	snapLines := splitLinesStr(snapshot)
	currLines := splitLinesStr(current)

	// 计算 LCS 对齐
	lcs := computeLCS(snapLines, currLines)

	// 建立行号映射
	mappings := buildLineMappings(lcs, snapLines, currLines)

	// 重映射每个操作
	result := &RecoverResult{Success: true}
	mappedOps := make([]Op, 0, len(ops))

	for _, op := range ops {
		mapped, err := mapOp(op, mappings)
		if err != nil {
			result.Success = false
			result.Warnings = append(result.Warnings, fmt.Sprintf("cannot recover %s: %v", op.Kind, err))
			result.MappedOps = nil
			return result
		}
		mappedOps = append(mappedOps, mapped)
	}

	result.MappedOps = mappedOps
	if len(result.Warnings) > 0 {
		result.Success = false // 有警告 = 部分失败
	}
	return result
}

// mapOp 将单个操作的行号从快照映射到当前文件。
func mapOp(op Op, mappings []LineMapping) (Op, error) {
	mapped := op

	switch op.Kind {
	case OpSWAP:
		startMapping := findMapping(mappings, op.LineStart)
		endMapping := findMapping(mappings, op.LineEnd)

		if startMapping == nil || startMapping.Status == MapDeleted {
			return Op{}, fmt.Errorf("line %d deleted in current version", op.LineStart)
		}
		if endMapping == nil || endMapping.Status == MapDeleted {
			return Op{}, fmt.Errorf("line %d deleted in current version", op.LineEnd)
		}
		if startMapping.Status == MapModified || endMapping.Status == MapModified {
			return Op{}, fmt.Errorf("line %d modified in current version — conflict", op.LineStart)
		}

		// 范围校验：连续性 + 均匀性 + 每行可映射。
		if err := validateRange(mappings, op.LineStart, op.LineEnd); err != nil {
			return Op{}, err
		}

		mapped.LineStart = startMapping.NewLine
		mapped.LineEnd = endMapping.NewLine

	case OpDEL:
		startMapping := findMapping(mappings, op.LineStart)

		if startMapping == nil || startMapping.Status == MapDeleted {
			return Op{}, fmt.Errorf("line %d already deleted", op.LineStart)
		}
		if startMapping.Status == MapModified {
			return Op{}, fmt.Errorf("line %d modified in current version — conflict", op.LineStart)
		}

		mapped.LineStart = startMapping.NewLine

		if op.LineEnd != op.LineStart {
			endMapping := findMapping(mappings, op.LineEnd)
			if endMapping == nil || endMapping.Status == MapDeleted {
				return Op{}, fmt.Errorf("line %d deleted in current version", op.LineEnd)
			}
			if endMapping.Status == MapModified {
				return Op{}, fmt.Errorf("line %d modified in current version — conflict", op.LineEnd)
			}
			// 范围校验：连续性 + 均匀性 + 每行可映射。
			if err := validateRange(mappings, op.LineStart, op.LineEnd); err != nil {
				return Op{}, err
			}
			mapped.LineEnd = endMapping.NewLine
		} else {
			mapped.LineEnd = mapped.LineStart
		}

	case OpINS:
		if op.Position == "head" || op.Position == "tail" {
			return mapped, nil
		}

		refMapping := findMapping(mappings, op.RefLine)
		if refMapping == nil || refMapping.Status == MapDeleted {
			return Op{}, fmt.Errorf("INS reference line %d deleted", op.RefLine)
		}
		if refMapping.Status == MapModified {
			return Op{}, fmt.Errorf("INS reference line %d modified — conflict", op.RefLine)
		}

		mapped.RefLine = refMapping.NewLine

	default:
		// REM, MV — 不需要行号重映射
	}

	return mapped, nil
}


func findMapping(mappings []LineMapping, oldLine int) *LineMapping {
	for i := range mappings {
		if mappings[i].OldLine == oldLine {
			return &mappings[i]
		}
	}
	return nil
}

// validateRange 检查 [startLine, endLine] 范围内：
//  1. 所有行都可通过映射找到且未被修改/删除；
//  2. 重映射后的范围长度与原始范围一致（连续性）；
//  3. 范围内所有行的偏移量一致（均匀性，防止 LCS 歧义对齐）。
//
// 三项全部通过才视为安全的简单平移；任一项失败表示文件结构变化超出纯偏移，
// 必须拒绝以避免静默损坏。
func validateRange(mappings []LineMapping, startLine, endLine int) error {
	if startLine > endLine {
		return nil // 空范围，无需校验
	}

	startM := findMapping(mappings, startLine)
	endM := findMapping(mappings, endLine)
	if startM == nil || endM == nil {
		return fmt.Errorf("range boundary not found in mappings")
	}

	expectedDelta := startM.NewLine - startLine
	expectedLen := endLine - startLine
	actualLen := endM.NewLine - startM.NewLine

	if actualLen != expectedLen {
		return fmt.Errorf("range length mismatch: original=%d, remapped=%d — file structure changed, cannot safely remap", expectedLen, actualLen)
	}

	for line := startLine; line <= endLine; line++ {
		m := findMapping(mappings, line)
		if m == nil || m.Status == MapDeleted {
			return fmt.Errorf("line %d in range deleted in current version", line)
		}
		if m.Status == MapModified {
			return fmt.Errorf("line %d in range modified in current version — conflict", line)
		}
		actualDelta := m.NewLine - line
		if actualDelta != expectedDelta {
			return fmt.Errorf("non-uniform offset at line %d: expected delta %d, got %d — ambiguous LCS alignment", line, expectedDelta, actualDelta)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// LCS 计算
// ---------------------------------------------------------------------------

// lcsPair 表示一个 LCS 匹配对。
type lcsPair struct {
	snapIdx int // 快照中的索引（0-based）
	currIdx int // 当前文件中的索引（0-based）
}

// computeLCS 计算两个行序列的最长公共子序列。
// 返回匹配对的列表（按快照中的行顺序排列）。
func computeLCS(a, b []string) []lcsPair {
	// 标准 O(n*m) LCS
	n, m := len(a), len(b)

	// 对大文件做行哈希快速匹配降级
	if n > 5000 || m > 5000 {
		return computeFastLCS(a, b)
	}

	// 构建 DP 表
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}

	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				if dp[i-1][j] >= dp[i][j-1] {
					dp[i][j] = dp[i-1][j]
				} else {
					dp[i][j] = dp[i][j-1]
				}
			}
		}
	}

	// 回溯
	var pairs []lcsPair
	i, j := n, m
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			pairs = append(pairs, lcsPair{snapIdx: i - 1, currIdx: j - 1})
			i--
			j--
		} else if dp[i-1][j] >= dp[i][j-1] {
			i--
		} else {
			j--
		}
	}

	// 反转（现在是从后往前）
	for left, right := 0, len(pairs)-1; left < right; left, right = left+1, right-1 {
		pairs[left], pairs[right] = pairs[right], pairs[left]
	}

	return pairs
}

// computeFastLCS 大文件行哈希快速匹配（降级策略）。
func computeFastLCS(a, b []string) []lcsPair {
	// 对 b 建立行哈希 → 索引映射
	hashToIdx := make(map[string][]int)
	for i, line := range b {
		hashToIdx[line] = append(hashToIdx[line], i)
	}

	// 对 a 的每行在 b 中找匹配
	usedB := make(map[int]bool)
	var pairs []lcsPair
	for i, line := range a {
		indices, ok := hashToIdx[line]
		if !ok {
			continue
		}
		// 找第一个未使用的匹配
		for _, j := range indices {
			if !usedB[j] {
				pairs = append(pairs, lcsPair{snapIdx: i, currIdx: j})
				usedB[j] = true
				break
			}
		}
	}
	return pairs
}

// buildLineMappings 从 LCS 匹配对构建行号映射。
// 两遍扫描：第一遍基于 LCS 分类 Unchanged/Shifted/Modified；
// 第二遍将 Modified 中内容已完全消失的行精确标记为 Deleted。
func buildLineMappings(lcs []lcsPair, snapLines, currLines []string) []LineMapping {
	snapLen := len(snapLines)

	// 第一遍：基于 LCS 建立行号映射
	mappings := make([]LineMapping, snapLen)
	lcsIdx := 0

	for snapLine := 0; snapLine < snapLen; snapLine++ {
		mapping := LineMapping{OldLine: snapLine + 1}

		if lcsIdx < len(lcs) && lcs[lcsIdx].snapIdx == snapLine {
			// 该行在 LCS 中（内容未变）
			currLCSIdx := lcs[lcsIdx].currIdx
			if currLCSIdx == snapLine {
				mapping.Status = MapUnchanged
			} else {
				mapping.Status = MapShifted
			}
			mapping.NewLine = currLCSIdx + 1
			lcsIdx++
		} else {
			// 该行不在 LCS 中 → 暂时标记为 Modified，第二遍精确区分
			mapping.Status = MapModified
			mapping.NewLine = 0
		}

		mappings[snapLine] = mapping
	}

	// 第二遍：区分 MapModified（内容被改）和 MapDeleted（完全删除）。
	// 对第一遍标记为 MapModified 的行，检查其内容是否仍存在于当前文件的任意位置。
	// 仅当内容在 currLines 中完全找不到时才标记为 MapDeleted。
	if len(currLines) > 0 {
		currSet := make(map[string]bool, len(currLines))
		for _, line := range currLines {
			currSet[line] = true
		}
		for i := range mappings {
			if mappings[i].Status == MapModified {
				if !currSet[snapLines[i]] {
					mappings[i].Status = MapDeleted
				}
			}
		}
	}

	return mappings
}

// splitLinesStr 分割字符串为行。
func splitLinesStr(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	// 去除末尾空行
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
