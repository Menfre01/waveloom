package reference

import (
	"context"
	"encoding/json"
	"fmt"

	"waveloom/pkg/permission"
	"waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	maxFileBytes  = 32 * 1024  // 单文件上限 32KB
	maxTotalBytes = 128 * 1024 // 总展开内容上限 128KB
)

// ---------------------------------------------------------------------------
// Expander
// ---------------------------------------------------------------------------

// Expander 负责解析和展开 @ 引用。
type Expander struct {
	registry tool.Registry
	guard    permission.Guard
}

// New 创建一个新的 Expander。
func New(registry tool.Registry, guard permission.Guard) *Expander {
	return &Expander{
		registry: registry,
		guard:    guard,
	}
}

// Expand 解析 userInput 中的 @ 引用，展开为实际内容，返回替换后的文本。
//
// 展开逻辑:
//  1. 扫描 userInput，提取所有 @ref
//  2. 对每个 ref，通过 os.Stat 判定是文件还是目录
//  3. 文件 → 调用 read_file 获取内容
//     目录 → 调用 ls 获取树形列表
//  4. 将 @ref 替换为 @@ 围栏块包裹的实际内容
//  5. 展开失败时保留 @ref 原文 + 追加错误标记
func (e *Expander) Expand(ctx context.Context, userInput string, cwd string) (expanded string, refs []ResolvedRef, err error) {
	// 1. Parse
	parsed := parseRefs(userInput, cwd)
	if len(parsed) == 0 {
		return userInput, nil, nil
	}

	// 2. Expand each ref
	resolved, expandErr := e.expandRefs(ctx, parsed, cwd)
	if expandErr != nil {
		return userInput, nil, expandErr
	}

	// 3. Replace
	expanded = replaceRefs(userInput, resolved, cwd)

	return expanded, resolved, nil
}

// expandRefs 对一组 Ref 执行展开，返回 ResolvedRef 列表。
func (e *Expander) expandRefs(ctx context.Context, refs []Ref, cwd string) ([]ResolvedRef, error) {
	var resolved []ResolvedRef
	totalBytes := 0

	for _, ref := range refs {
		// Check total bytes limit
		if totalBytes >= maxTotalBytes {
			// Skip remaining refs; caller can detect truncation via len(resolved) < len(refs)
			break
		}

		// Determine tool name and params
		var toolName string
		var params json.RawMessage

		switch ref.Kind {
		case KindFile:
			toolName = "read_file"
			params = json.RawMessage(fmt.Sprintf(`{"file_path": "%s"}`, ref.Path))
		case KindFolder:
			toolName = "ls"
			params = json.RawMessage(fmt.Sprintf(`{"path": "%s", "depth": 2}`, ref.Path))
		default:
			continue
		}

		// Permission check
		decision := e.guard.Check(ctx, toolName, params)
		if decision.Decision == permission.DecisionDeny || decision.Decision == permission.DecisionAsk {
			// For ask in expander (non-interactive), treat as deny
			resolved = append(resolved, ResolvedRef{
				Ref:   ref,
				Error: fmt.Sprintf("permission denied: %s", decision.Message),
			})
			continue
		}

		// Execute tool
		result, execErr := e.registry.Execute(ctx, toolName, params)
		if execErr != nil {
			resolved = append(resolved, ResolvedRef{
				Ref:   ref,
				Error: fmt.Sprintf("tool execution failed: %v", execErr),
			})
			continue
		}

		if result.IsError() {
			resolved = append(resolved, ResolvedRef{
				Ref:   ref,
				Error: result.Error.Message,
			})
			continue
		}

		// Apply single-file size limit
		content := result.Content
		byteCount := len(content)
		if byteCount > maxFileBytes {
			content = content[:maxFileBytes]
			content += "\n[truncated: file exceeds 32KB limit]"
			byteCount = len(content)
		}

		// Check total bytes
		if totalBytes+byteCount > maxTotalBytes {
			// Truncate this file's content to fit
			available := maxTotalBytes - totalBytes
			if available > 0 {
				content = content[:available]
				content += "\n[truncated: total context exceeds 128KB limit]"
				byteCount = len(content)
			} else {
				// Should not reach here due to totalBytes check at top, but be safe
				break
			}
		}

		totalBytes += byteCount

		resolved = append(resolved, ResolvedRef{
			Ref:     ref,
			Content: content,
			Bytes:   byteCount,
		})

	}

	return resolved, nil
}
