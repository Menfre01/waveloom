package bash

import (
	"strings"
)

// SecurityCheck 表示 shell 命令的安全检测结果。
type SecurityCheck struct {
	Name    string // 检测器名称
	Passed  bool   // true=安全，false=需要审查
	Reason  string // 不通过时的原因
	IsParse bool   // true=parser differential（解析器差异），false=通用安全
}

// SecurityReport 汇总所有安全检查的结果。
type SecurityReport struct {
	Checks   []SecurityCheck
	Command  *CommandInfo
	HasIssue bool // 是否有任何检查未通过
}

// Audit 对 shell 命令执行完整安全审查。
// 对标 Claude Code bashSecurity.ts 的 validators 链。
func Audit(raw string) (*SecurityReport, error) {
	ci, err := Parse(raw)
	if err != nil {
		return nil, err
	}

	report := &SecurityReport{Command: ci}

	report.runCheck(checkBackslashOperators(ci))
	report.runCheck(checkBraceExpansion(ci))
	report.runCheck(checkObfuscatedFlags(ci))

	return report, nil
}

func (r *SecurityReport) runCheck(sc SecurityCheck) {
	r.Checks = append(r.Checks, sc)
	if !sc.Passed {
		r.HasIssue = true
	}
}

// ============================================================================
// 检测器 1: Backslash-escaped Shell Operators
// ============================================================================

const shellOperators = ";|&<>"

func checkBackslashOperators(ci *CommandInfo) SecurityCheck {
	raw := ci.Raw
	inSQ, inDQ := false, false

	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if ch == '\\' && !inSQ {
			if !inDQ {
				if i+1 < len(raw) && strings.ContainsRune(shellOperators, rune(raw[i+1])) {
					return SecurityCheck{
						Name:    "backslash_operator",
						Passed:  false,
						Reason:  "Command contains a backslash before a shell operator which can hide command structure",
						IsParse: true,
					}
				}
			}
			i++
			continue
		}
		if ch == '\'' && !inDQ {
			inSQ = !inSQ
		}
		if ch == '"' && !inSQ {
			inDQ = !inDQ
		}
	}
	return SecurityCheck{Name: "backslash_operator", Passed: true}
}

// ============================================================================
// 检测器 2: Brace Expansion
// ============================================================================

func checkBraceExpansion(ci *CommandInfo) SecurityCheck {
	raw := ci.Raw
	if !strings.Contains(raw, "{") || !strings.Contains(raw, ",") {
		return SecurityCheck{Name: "brace_expansion", Passed: true}
	}

	inSQ, inDQ, escaped := false, false, false
	hasBrace, hasComma := false, false

	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && !inSQ {
			escaped = true
			continue
		}
		if ch == '\'' && !inDQ {
			inSQ = !inSQ
			continue
		}
		if ch == '"' && !inSQ {
			inDQ = !inDQ
			continue
		}
		if inSQ || inDQ {
			continue
		}
		if ch == '{' {
			hasBrace = true
		}
		if ch == ',' {
			hasComma = true
		}
	}

	if hasBrace && hasComma {
		return SecurityCheck{
			Name:    "brace_expansion",
			Passed:  false,
			Reason:  "Command contains brace expansion that could alter command parsing",
			IsParse: true,
		}
	}
	return SecurityCheck{Name: "brace_expansion", Passed: true}
}

// ============================================================================
// 检测器 3: Obfuscated Flags
// ============================================================================

func checkObfuscatedFlags(ci *CommandInfo) SecurityCheck {
	raw := ci.Raw

	// 1. 引号拼接标志：空格 + 引号 + 短横 →
	//    检测 "-X / '-X / ""-X 组合
	inSQ, inDQ, escaped := false, false, false
	for i := 0; i < len(raw)-3; i++ {
		ch := raw[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && !inSQ {
			escaped = true
			continue
		}
		if ch == '\'' && !inDQ {
			inSQ = !inSQ
			continue
		}
		if ch == '"' && !inSQ {
			inDQ = !inDQ
			continue
		}
		if inSQ || inDQ {
			continue
		}
		if (ch == ' ' || ch == '\t') && (raw[i+1] == '\'' || raw[i+1] == '"') {
			// 空格后跟引号 → 检查引号内容是否以 - 开头（混淆标志）
			quote := raw[i+1]
			if raw[i+2] == '-' {
				return SecurityCheck{
					Name:    "obfuscated_flags",
					Passed:  false,
					Reason:  "Command contains quoted dash before flag — potential flag obfuscation",
					IsParse: true,
				}
			}
			// 空引号后跟 - → ""-X 模式
			if i+3 < len(raw) && raw[i+2] == quote && raw[i+3] == '-' {
				return SecurityCheck{
					Name:    "obfuscated_flags",
					Passed:  false,
					Reason:  "Command contains empty quote followed by dash — potential flag obfuscation",
					IsParse: true,
				}
			}
		}
	}

	// 2. ANSI-C quoting $'...'
	if strings.Contains(raw, "$\x27") {
		return SecurityCheck{
			Name:    "obfuscated_flags",
			Passed:  false,
			Reason:  "Command contains ANSI-C quoting ($'...') which can hide characters",
			IsParse: true,
		}
	}

	// 3. Locale quoting $"..."
	if strings.Contains(raw, "$\"") {
		return SecurityCheck{
			Name:    "obfuscated_flags",
			Passed:  false,
			Reason:  "Command contains locale quoting ($\"...\") which can hide characters",
			IsParse: true,
		}
	}

	return SecurityCheck{Name: "obfuscated_flags", Passed: true}
}
