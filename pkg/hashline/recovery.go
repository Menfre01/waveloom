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
	MappedOps []Op       // 重映射后的操作
	Warnings  []string   // 警告信息
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
	mappings := buildLineMappings(lcs, len(snapLines), len(currLines))

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
			mapped.LineEnd = endMapping.NewLine
		} else {
			mapped.LineEnd = mapped.LineStart
		}

	case OpINS:
		if op.Position == "head" || op.Position == "tail" {
			// INS.HEAD / INS.TAIL 不依赖行号，无需重映射
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
func buildLineMappings(lcs []lcsPair, snapLen, currLen int) []LineMapping {
	// 初始化所有快照行 → 映射
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
			// 该行不在 LCS 中 → 被修改或删除
			mapping.Status = MapModified
			mapping.NewLine = 0
		}

		mappings[snapLine] = mapping
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
