package sandbox

import (
	"regexp"
	"strings"
)

// ============================================================================
// Violation — 沙箱违规反馈
// ============================================================================

// Violation 描述一次沙箱拦截,以 <sandbox_violations> 块追加到命令 stderr,
// 让模型感知失败原因(避免反复重试),而不是静默失败。
type Violation struct {
	Kind   string // write / read / path
	Path   string // 被拦截的路径(尽力提取,可能为空)
	Detail string // 人类可读的拦截原因
}

// String 返回单行违规描述。
func (v Violation) String() string {
	switch v.Kind {
	case "write":
		return "write blocked: " + describePath(v.Path, v.Detail)
	case "read":
		return "read masked (returned empty): " + describePath(v.Path, v.Detail)
	case "path":
		return "path masked (returned ENOENT): " + describePath(v.Path, v.Detail)
	default:
		return v.Kind + ": " + describePath(v.Path, v.Detail)
	}
}

func describePath(p, detail string) string {
	if p == "" {
		return "(" + detail + ")"
	}
	return p + " (" + detail + ")"
}

// ============================================================================
// stderr 违规识别(v1:模式匹配)
// ============================================================================

var (
	// EROFS:只读根上写文件(bwrap --ro-bind / /)
	reReadonly = regexp.MustCompile(`(?mi)^(.*?)(?:Read-only file system|read-only filesystem)`)
	// EPERM:遮蔽目录 deny access(macOS Seatbelt;Linux 下权限受限)
	rePermDenied = regexp.MustCompile(`(?mi)^(.*?)(?:permission denied|Operation not permitted)`)
	// 路径 token:错误行中形如 /path/to/file 的片段
	rePath = regexp.MustCompile(`(/[A-Za-z0-9_./~-]+)`)
)

// collectViolations 从 stderr 文本中识别沙箱拦截行。
// 已知遮蔽场景:
//   - EROFS → 写被只读根拦截(write blocked)
//   - EPERM → 遮蔽路径读/写被拒(平台差异:Linux 目录遮蔽为 tmpfs 空覆盖,
//     macOS Seatbelt 为 deny access → EPERM,统一标注 read masked)
func collectViolations(stderr string) []Violation {
	var out []Violation
	for _, line := range strings.Split(stderr, "\n") {
		switch {
		case reReadonly.MatchString(line):
			out = append(out, Violation{
				Kind:   "write",
				Path:   extractPath(line),
				Detail: "read-only filesystem",
			})
		case rePermDenied.MatchString(line):
			// 区分读写:EPERM 可能是写操作被只读根拒绝(touch/rm/mv 等),
			// 统一标 read 会误导模型
			kind := "read"
			detail := "masked by sandbox"
			if isWriteErrorLine(line) {
				kind = "write"
				detail = "read-only filesystem (sandbox)"
			}
			out = append(out, Violation{
				Kind:   kind,
				Path:   extractPath(line),
				Detail: detail,
			})
		}
	}
	return out
}

// isWriteErrorLine 启发式判断错误行是否属于写操作:
// 行首工具名(touch/rm/mv/cp/mkdir/ln)或包含写语义关键词。
func isWriteErrorLine(line string) bool {
	lower := strings.ToLower(line)
	for _, tool := range []string{"touch:", "rm:", "mv:", "cp:", "mkdir:", "ln:", "chmod:", "chown:", "cannot create", "cannot remove", "write error"} {
		if strings.Contains(lower, tool) {
			return true
		}
	}
	return false
}

// extractPath 从错误行中提取第一个路径 token。
func extractPath(line string) string {
	m := rePath.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	return m[1]
}

// AnnotateViolations 识别 stderr 中的沙箱拦截并追加 <sandbox_violations> 块。
// 无违规时原样返回。
func AnnotateViolations(stderr string) string {
	violations := collectViolations(stderr)
	if len(violations) == 0 {
		return stderr
	}
	var sb strings.Builder
	sb.WriteString(stderr)
	if !strings.HasSuffix(stderr, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("\n<sandbox_violations>\n")
	for _, v := range violations {
		sb.WriteString(v.String())
		sb.WriteString("\n")
	}
	sb.WriteString("</sandbox_violations>\n")
	return sb.String()
}
