// Package permission — 破坏性命令警告（对标 Claude Code destructiveCommandWarning.ts）
//
// 提供信息性警告显示，不改变权限逻辑或自动批准行为。
package permission

import "regexp"

// ============================================================================
// DestructivePattern — 破坏性命令模式
// ============================================================================

// DestructivePattern 描述一个需要警告用户的破坏性命令模式。
type DestructivePattern struct {
	Pattern *regexp.Regexp
	Warning string
}

// ============================================================================
// destructivePatterns — 破坏性模式列表
// ============================================================================

var destructivePatterns = []DestructivePattern{
	// ── Git 破坏性操作 ──
	{regexp.MustCompile(`\bgit\s+reset\s+--hard\b`), "Note: may discard uncommitted changes"},
	{regexp.MustCompile(`\bgit\s+push\b[^;&|\n]*[ \t](--force|--force-with-lease|-f)\b`), "Note: may overwrite remote history"},
	{regexp.MustCompile(`\bgit\s+clean\b.*-[a-zA-Z]*f`), "Note: may permanently delete untracked files"},
	{regexp.MustCompile(`\bgit\s+checkout\s+(--\s+)?\.[ \t]*($|[;&|\n])`), "Note: may discard all working tree changes"},
	{regexp.MustCompile(`\bgit\s+restore\s+(--\s+)?\.[ \t]*($|[;&|\n])`), "Note: may discard all working tree changes"},
	{regexp.MustCompile(`\bgit\s+stash[ \t]+(drop|clear)\b`), "Note: may permanently remove stashed changes"},
	{regexp.MustCompile(`\bgit\s+branch\s+(-D[ \t]|--delete\s+--force|--force\s+--delete)\b`), "Note: may force-delete a branch"},
	{regexp.MustCompile(`\bgit\s+(commit|push|merge)\b[^;&|\n]*--no-verify\b`), "Note: may skip safety hooks"},
	{regexp.MustCompile(`\bgit\s+commit\b[^;&|\n]*--amend\b`), "Note: may rewrite the last commit"},

	// ── 文件删除 ──
	{regexp.MustCompile(`(^|[;&|\n]\s*)rm\s+-[a-zA-Z]*[rR][a-zA-Z]*f|(^|[;&|\n]\s*)rm\s+-[a-zA-Z]*f[a-zA-Z]*[rR]`), "Note: may recursively force-remove files"},
	{regexp.MustCompile(`(^|[;&|\n]\s*)rm\s+-[a-zA-Z]*[rR]`), "Note: may recursively remove files"},
	{regexp.MustCompile(`(^|[;&|\n]\s*)rm\s+-[a-zA-Z]*f`), "Note: may force-remove files"},

	// ── 数据库 ──
	{regexp.MustCompile(`(?i)\b(DROP|TRUNCATE)\s+(TABLE|DATABASE|SCHEMA)\b`), "Note: may drop or truncate database objects"},
	{regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+\w+[ \t]*(;|"|'|\n|$)`), "Note: may delete all rows from a database table"},
	{regexp.MustCompile(`\bkubectl\s+delete\b`), "Note: may delete Kubernetes resources"},
	{regexp.MustCompile(`\bterraform\s+destroy\b`), "Note: may destroy Terraform infrastructure"},
	{regexp.MustCompile(`\bdocker\s+(rm|rmi|system\s+prune)\b`), "Note: may remove Docker resources"},
	{regexp.MustCompile(`\bdocker\s+compose\s+down\b`), "Note: may stop and remove Docker containers"},

	// ── 磁盘 / 文件系统 ──
	{regexp.MustCompile(`\bshred\b`), "Note: may securely delete files beyond recovery"},
	{regexp.MustCompile(`\bmkfs\.`), "Note: may format a filesystem"},
	{regexp.MustCompile(`\bdd\s+if=`), "Note: may write raw data to disk"},

	// ── 系统 ──
	{regexp.MustCompile(`\bshutdown\b`), "Note: may shut down the system"},
	{regexp.MustCompile(`\breboot\b`), "Note: may reboot the system"},
	{regexp.MustCompile(`\bchmod\s+777\b`), "Note: may set world-writable permissions"},
	{regexp.MustCompile(`\bchown\b`), "Note: may change file ownership"},
	{regexp.MustCompile(`\bcrontab\b`), "Note: may modify scheduled tasks"},
	{regexp.MustCompile(`\biptables\b`), "Note: may modify firewall rules"},
}

// ============================================================================
// GetDestructiveWarning — 获取破坏性命令警告
// ============================================================================

// GetDestructiveWarning 检查命令是否匹配已知的破坏性模式，
// 返回人类可读的警告字符串，或空字符串（无警告）。
func GetDestructiveWarning(cmd string) string {
	for _, dp := range destructivePatterns {
		if dp.Pattern.MatchString(cmd) {
			return dp.Warning
		}
	}
	return ""
}
