package subagent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Menfre01/waveloom/pkg/permission"
)

// SecurityFinding 表示分类器发现的一个安全问题。
type SecurityFinding struct {
	Severity string // "HIGH" | "MEDIUM" | "LOW"
	Category string // "sensitive_file" | "dangerous_command" | "out_of_workspace_write"
	Detail   string
}

// classify 扫描子 agent 事件列表，检测安全风险。
// 返回空切片表示未发现问题。
func classify(events []SubagentEvent, workspaceDir string) []SecurityFinding {
	if len(events) == 0 {
		return nil
	}

	var findings []SecurityFinding

	// 从 ToolStart 事件中提取命令和文件路径。
	// ToolResult 中的实际结果用于交叉验证，但不单独扫描（避免重复报告）。
	type pendingTool struct {
		name string
		args string
	}
	var current pendingTool

	for _, ev := range events {
		switch ev.Kind {
		case SubagentToolStart:
			current = pendingTool{name: ev.ToolName, args: ev.ToolArgs}

		case SubagentToolResult:
			if current.name == "" {
				continue
			}
			switch current.name {
			case "bash_subagent":
				findings = classifyCommand(findings, current.args)
			case "read_file", "write_file", "edit_file":
				findings = classifyFile(findings, current.name, current.args, workspaceDir)
			}
			current = pendingTool{} // 消费完毕

		default:
			// SubagentText / SubagentThought 不包含可分类的操作
		}
	}

	return findings
}

// classifyCommand 检查 bash 命令的安全性。
func classifyCommand(findings []SecurityFinding, command string) []SecurityFinding {
	result := permission.CommandSafetyCheck(command)
	switch result.Level {
	case permission.RiskHigh:
		findings = append(findings, SecurityFinding{
			Severity: "HIGH",
			Category: "dangerous_command",
			Detail:   fmt.Sprintf("High-risk command detected: %s — %s", command, result.Message),
		})
	case permission.RiskMedium:
		findings = append(findings, SecurityFinding{
			Severity: "MEDIUM",
			Category: "dangerous_command",
			Detail:   fmt.Sprintf("Medium-risk command detected: %s — %s", command, result.Message),
		})
	}
	return findings
}

// classifyFile 检查文件操作的安全性。
func classifyFile(findings []SecurityFinding, toolName, filePath, workspaceDir string) []SecurityFinding {
	inWS := isWithinWorkspace(filePath, workspaceDir)

	// 工作目录外写入 → out_of_workspace_write
	if !inWS && (toolName == "write_file" || toolName == "edit_file") {
		findings = append(findings, SecurityFinding{
			Severity: "LOW",
			Category: "out_of_workspace_write",
			Detail:   fmt.Sprintf("%s wrote to path outside workspace: %s", toolName, filePath),
		})
		return findings
	}

	// 工作目录外读操作：PathSafetyCheck 会把任意外部路径标为 PathDangerous，
	// 但对于只读操作这是预期行为（读取系统头文件、配置等），不报告。
	if !inWS {
		return findings
	}

	// 工作目录内：使用完整路径安全检查
	result := permission.PathSafetyCheck(filePath, []string{workspaceDir})

	switch result.Level {
	case permission.PathDangerous:
		findings = append(findings, SecurityFinding{
			Severity: "HIGH",
			Category: "sensitive_file",
			Detail:   fmt.Sprintf("%s accessed dangerous path: %s — %s", toolName, filePath, result.Message),
		})
	case permission.PathSensitive:
		severity := "MEDIUM"
		if toolName == "write_file" || toolName == "edit_file" {
			severity = "HIGH"
		}
		findings = append(findings, SecurityFinding{
			Severity: severity,
			Category: "sensitive_file",
			Detail:   fmt.Sprintf("%s accessed sensitive path: %s — %s", toolName, filePath, result.Message),
		})
	}

	return findings
}

// isWithinWorkspace 检查路径是否在工作目录内。宽松版本——仅做前缀匹配。
func isWithinWorkspace(filePath, workspaceDir string) bool {
	if workspaceDir == "" {
		return true // 无法判断，放行
	}
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return false
	}
	absWS, err := filepath.Abs(workspaceDir)
	if err != nil {
		return true // 解析失败，放行
	}
	// 规范化路径分隔符
	absPath = filepath.Clean(absPath)
	absWS = filepath.Clean(absWS)

	rel, err := filepath.Rel(absWS, absPath)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}

// formatFindings 将 findings 格式化为注入父 LLM 的文本块。
func formatFindings(findings []SecurityFinding) string {
	if len(findings) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n\n<subagent_security_warning>\n")
	for _, f := range findings {
		fmt.Fprintf(&sb, "- [%s] %s: %s\n", f.Severity, f.Category, f.Detail)
	}
	sb.WriteString("</subagent_security_warning>")
	return sb.String()
}
