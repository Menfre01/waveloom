package hashline

import (
	"fmt"
	"strings"
)

// FormatContent 将文件内容格式化为 hashline 输出。
// 返回 [PATH#TAG] 头 + N:CONTENT 行。
func FormatContent(path string, tag string, content string, offset, limit int) string {
	lines := strings.Split(content, "\n")
	// 去除末尾空行（由 trailing newline 产生）
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	totalLines := len(lines)

	// 空文件
	if totalLines == 0 {
		return fmt.Sprintf("[%s#%s]\n", path, tag)
	}

	// 选择可见行
	start := offset
	if start < 0 {
		start = 0
	}
	end := totalLines
	if limit > 0 {
		end = start + limit
	}
	if end > totalLines {
		end = totalLines
	}
	if start >= totalLines {
		return fmt.Sprintf("[%s#%s]\n<system-reminder>Warning: the file exists but is shorter than the provided offset (%d). The file has %d lines.</system-reminder>",
			path, tag, offset, totalLines)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[%s#%s]\n", path, tag)

	for i := start; i < end; i++ {
		fmt.Fprintf(&b, "%d:%s\n", i+1, lines[i])
	}

	// 截断提示
	if end < totalLines {
		omitted := totalLines - end
		fmt.Fprintf(&b, "... [truncated: %d lines omitted]", omitted)
	}

	return b.String()
}
