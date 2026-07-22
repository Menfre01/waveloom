// Package bash 基于 mvdan.cc/sh 的 AST 提供 shell 命令分析能力。
//
// 实现 23 个安全检测器,覆盖命令结构提取、parser differential 攻击检测和通用安全审查。
package bash

import (
	"regexp"
	"strings"
	"unicode"
)

// ============================================================================
// SecurityCheck / SecurityReport — 安全检测结果
// ============================================================================

// SecurityCheck 表示单个安全检测的结果。
type SecurityCheck struct {
	Name    string // 检测器名称
	Passed  bool   // true=安全, false=需审查
	Reason  string // 不通过时的原因
	IsParse bool   // true=parser differential（解析器差异）, false=通用安全
}

// SecurityReport 汇总所有安全检查结果。
type SecurityReport struct {
	Checks   []SecurityCheck
	Command  *CommandInfo
	HasIssue bool // 是否有任何检查未通过
}

// SecurityContext 为各检测器提供预计算的命令视图。
type SecurityContext struct {
	OriginalCommand string
	BaseCommand     string
	Unquoted        string // 剥离单引号内容后的视图
	FullyUnquoted   string // 剥离单双引号内容后的视图
	KeepQuotes      string // 保留引号定界符，剥离引号内容
	CommandInfo     *CommandInfo
}

// ============================================================================
// QuoteExtraction — 多层引号剥离
// ============================================================================

type quoteExtraction struct {
	withDQ    string // 保留双引号内容，仅剥离单引号
	full      string // 剥离全部引号内容
	keepQuote string // 保留引号定界符
}

// extractQuotedContent 返回三种不同的引号剥离视图。
// 返回三种不同的引号剥离视图。
func extractQuotedContent(cmd string) quoteExtraction {
	var wDQ, full, keepQ strings.Builder
	wDQ.Grow(len(cmd))
	full.Grow(len(cmd))
	keepQ.Grow(len(cmd))

	inSQ, inDQ, escaped := false, false, false

	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]

		if escaped {
			escaped = false
			if !inSQ {
				wDQ.WriteByte(ch)
			}
			if !inSQ && !inDQ {
				full.WriteByte(ch)
				keepQ.WriteByte(ch)
			}
			continue
		}

		if ch == '\\' && !inSQ {
			escaped = true
			if !inSQ {
				wDQ.WriteByte(ch)
			}
			if !inSQ && !inDQ {
				full.WriteByte(ch)
				keepQ.WriteByte(ch)
			}
			continue
		}

		if ch == '\'' && !inDQ {
			inSQ = !inSQ
			keepQ.WriteByte(ch)
			continue
		}

		if ch == '"' && !inSQ {
			inDQ = !inDQ
			keepQ.WriteByte(ch)
			continue
		}

		if !inSQ {
			wDQ.WriteByte(ch)
		}
		if !inSQ && !inDQ {
			full.WriteByte(ch)
			keepQ.WriteByte(ch)
		}
	}

	return quoteExtraction{
		withDQ:    wDQ.String(),
		full:      full.String(),
		keepQuote: keepQ.String(),
	}
}

// buildSecurityContext 构建安全上下文。
func buildSecurityContext(raw string, ci *CommandInfo) SecurityContext {
	qe := extractQuotedContent(raw)
	return SecurityContext{
		OriginalCommand: raw,
		BaseCommand:     ci.BaseCommand,
		Unquoted:        qe.withDQ,
		FullyUnquoted:   qe.full,
		KeepQuotes:      qe.keepQuote,
		CommandInfo:     ci,
	}
}

// ============================================================================
// Audit — 完整安全审查入口
// ============================================================================

// Audit 对 shell 命令执行完整安全审查。
// 安全检测器链,按严重性从高到低排列。
func Audit(raw string) (*SecurityReport, error) {
	ci, err := Parse(raw)
	if err != nil {
		// Parse 失败时仍运行不依赖 AST 的检测器
		ci = &CommandInfo{Raw: raw}
	}

	ctx := buildSecurityContext(raw, ci)
	report := &SecurityReport{Command: ci}

	// 第一阶段：空命令 / 不完整命令（解析前检查）
	report.runCheck(checkEmpty(ctx))
	if report.HasIssue {
		return report, nil
	}
	report.runCheck(checkIncompleteCommands(ctx))
	if report.HasIssue {
		return report, nil
	}

	// 第二阶段：安全 heredoc 检测（早期 allow 路径）
	report.runCheck(checkSafeCommandSubstitution(ctx))
	// 注意：safe heredoc 返回 Passed=true 意味着"安全 heredoc 模式不存在"，
	// 不等于命令不安全。这里 HasIssue 语义例外，不提前返回。

	// 第三阶段：parser differential 检测器（IsParse=true）
	report.runCheck(checkBackslashOperators(ctx))
	report.runCheck(checkBackslashEscapedWhitespace(ctx))
	report.runCheck(checkBraceExpansion(ctx))
	report.runCheck(checkObfuscatedFlags(ctx))
	report.runCheck(checkCarriageReturn(ctx))
	report.runCheck(checkMidWordHash(ctx))
	report.runCheck(checkCommentQuoteDesync(ctx))
	report.runCheck(checkQuotedNewline(ctx))
	report.runCheck(checkUnicodeWhitespace(ctx))
	report.runCheck(checkMalformedTokenInjection(ctx))

	// 第四阶段：通用安全检测器（IsParse=false）
	report.runCheck(checkShellMetacharacters(ctx))
	report.runCheck(checkDangerousVariables(ctx))
	report.runCheck(checkDangerousPatterns(ctx))
	report.runCheck(checkRedirections(ctx))
	report.runCheck(checkNewlines(ctx))
	report.runCheck(checkIFSInjection(ctx))
	report.runCheck(checkProcEnvironAccess(ctx))
	report.runCheck(checkJqCommand(ctx))
	report.runCheck(checkGitCommit(ctx))
	report.runCheck(checkZshDangerousCommands(ctx))
	report.runCheck(checkControlCharacters(ctx))

	return report, nil
}

func (r *SecurityReport) runCheck(sc SecurityCheck) {
	r.Checks = append(r.Checks, sc)
	if !sc.Passed {
		r.HasIssue = true
	}
}

// HasParserDifferential 返回是否有 parser differential 类型的检测未通过。
func (r *SecurityReport) HasParserDifferential() bool {
	for _, c := range r.Checks {
		if !c.Passed && c.IsParse {
			return true
		}
	}
	return false
}

// ============================================================================
// 辅助函数
// ============================================================================

// isEscapedAt 检查 pos 位置的字符是否被奇数个反斜杠转义。
func isEscapedAt(s string, pos int) bool {
	count := 0
	for i := pos - 1; i >= 0 && s[i] == '\\'; i-- {
		count++
	}
	return count%2 == 1
}

// hasUnescapedChar 检查 s 中是否存在未转义的指定字符（单字符）。
func hasUnescapedChar(s string, ch byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++ // 跳过转义序列
			continue
		}
		if s[i] == ch {
			return true
		}
	}
	return false
}

// stripSafeRedirections 剥离安全的 I/O 重定向（如 2>&1, >/dev/null）。
// stripSafeRedirections 剥离安全重定向操作符。

// ============================================================================
// 检测器 1: checkEmpty — 空命令
// ============================================================================

func checkEmpty(ctx SecurityContext) SecurityCheck {
	if strings.TrimSpace(ctx.OriginalCommand) == "" {
		return SecurityCheck{
			Name:   "empty",
			Passed: false,
			Reason: "Empty command is safe — nothing to execute",
		}
	}
	return SecurityCheck{Name: "empty", Passed: true}
}

// ============================================================================
// 检测器 2: checkIncompleteCommands — 不完整命令片段
// ============================================================================

func checkIncompleteCommands(ctx SecurityContext) SecurityCheck {
	raw := ctx.OriginalCommand
	trimmed := strings.TrimSpace(raw)

	// Tab 开头
	if strings.HasPrefix(raw, "\t") {
		return SecurityCheck{
			Name:    "incomplete_commands",
			Passed:  false,
			Reason:  "Command appears to be an incomplete fragment (starts with tab)",
			IsParse: true,
		}
	}

	// 以 - 开头（flags 但没有命令名）
	if strings.HasPrefix(trimmed, "-") {
		return SecurityCheck{
			Name:    "incomplete_commands",
			Passed:  false,
			Reason:  "Command appears to be an incomplete fragment (starts with flags)",
			IsParse: true,
		}
	}

	// 以操作符开头（&&, ||, ;, >>, >, <）
	if reIncompleteOp.MatchString(trimmed) {
		return SecurityCheck{
			Name:    "incomplete_commands",
			Passed:  false,
			Reason:  "Command appears to be a continuation line (starts with operator)",
			IsParse: true,
		}
	}

	return SecurityCheck{Name: "incomplete_commands", Passed: true}
}

var reIncompleteOp = regexp.MustCompile(`^\s*(&&|\|\||;|>>?|<)`)

// ============================================================================
// 检测器 3: checkBackslashOperators — 反斜杠转义操作符
// ============================================================================

// checkBackslashOperators 检测反斜杠转义的 shell 操作符。
// 检测反斜杠转义操作符和实际操作符节点。
//
// AST 预检：如果 mvdan/sh 解析确认命令中不存在真正的操作符节点
//（如 find -exec {} \; 中的 \; 只是 word 参数），跳过逐字符扫描，
// 消除已知误报。
func checkBackslashOperators(ctx SecurityContext) SecurityCheck {
	raw := ctx.OriginalCommand
	ci := ctx.CommandInfo

	// AST 预检：单语句、无管道的命令中 \; \| \& \<> 只是转义字面量
	if ci != nil && ci.Stmts == 1 && !ci.HasPipes && len(ci.Redirs) == 0 {
		return SecurityCheck{Name: "backslash_operator", Passed: true, IsParse: true}
	}

	inSQ, inDQ := false, false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if ch == '\\' && !inSQ {
			if !inDQ {
				if i+1 < len(raw) && strings.ContainsRune(shellOperatorChars, rune(raw[i+1])) {
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
	return SecurityCheck{Name: "backslash_operator", Passed: true, IsParse: true}
}
const shellOperatorChars = ";|&<>"

// ============================================================================
// 检测器 4: checkBackslashEscapedWhitespace — 反斜杠转义空白符
// ============================================================================

func checkBackslashEscapedWhitespace(ctx SecurityContext) SecurityCheck {
	raw := ctx.OriginalCommand
	inSQ, inDQ := false, false

	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if ch == '\\' && !inSQ {
			if !inDQ {
				if i+1 < len(raw) {
					next := raw[i+1]
					if next == ' ' || next == '\t' {
						return SecurityCheck{
							Name:    "backslash_escaped_whitespace",
							Passed:  false,
							Reason:  "Command contains backslash-escaped whitespace that could alter command parsing",
							IsParse: true,
						}
					}
				}
			}
			i++
			continue
		}
		if ch == '"' && !inSQ {
			inDQ = !inDQ
			continue
		}
		if ch == '\'' && !inDQ {
			inSQ = !inSQ
			continue
		}
	}
	return SecurityCheck{Name: "backslash_escaped_whitespace", Passed: true, IsParse: true}
}

// ============================================================================
// 检测器 5: checkBraceExpansion — 花括号展开
// ============================================================================

func checkBraceExpansion(ctx SecurityContext) SecurityCheck {
	full := ctx.FullyUnquoted

	if !strings.Contains(full, "{") {
		return SecurityCheck{Name: "brace_expansion", Passed: true, IsParse: true}
	}

	// 检查括号不匹配（} > {  → 可能有引号包裹的 { 被剥离）
	open, close := countUnescapedBraces(full)
	if open > 0 && close > open {
		return SecurityCheck{
			Name:    "brace_expansion",
			Passed:  false,
			Reason:  "Command has excess closing braces after quote stripping, indicating possible brace expansion obfuscation",
			IsParse: true,
		}
	}

	// 检查原始命令中的引号包裹的花括号（'{', '"}'）
	if open > 0 && reQuotedBrace.MatchString(ctx.OriginalCommand) {
		return SecurityCheck{
			Name:    "brace_expansion",
			Passed:  false,
			Reason:  "Command contains quoted brace character inside brace context (potential brace expansion obfuscation)",
			IsParse: true,
		}
	}

	// 深度匹配扫描：寻找 {...,...} 或 {a..z} 模式
	for i := 0; i < len(full); i++ {
		if full[i] != '{' || isEscapedAt(full, i) {
			continue
		}
		// 找到匹配的 }
		depth := 1
		closeIdx := -1
		for j := i + 1; j < len(full); j++ {
			ch := full[j]
			if ch == '{' && !isEscapedAt(full, j) {
				depth++
			} else if ch == '}' && !isEscapedAt(full, j) {
				depth--
				if depth == 0 {
					closeIdx = j
					break
				}
			}
		}
		if closeIdx < 0 {
			continue
		}
		// 在外层检查逗号或 .. 序列
		inner := 0
		for k := i + 1; k < closeIdx; k++ {
			ch := full[k]
			if ch == '{' && !isEscapedAt(full, k) {
				inner++
			} else if ch == '}' && !isEscapedAt(full, k) {
				inner--
			} else if inner == 0 {
				if ch == ',' {
					return SecurityCheck{
						Name:    "brace_expansion",
						Passed:  false,
						Reason:  "Command contains brace expansion that could alter command parsing",
						IsParse: true,
					}
				}
				if ch == '.' && k+1 < closeIdx && full[k+1] == '.' {
					return SecurityCheck{
						Name:    "brace_expansion",
						Passed:  false,
						Reason:  "Command contains brace sequence expansion that could alter command parsing",
						IsParse: true,
					}
				}
			}
		}
	}
	return SecurityCheck{Name: "brace_expansion", Passed: true, IsParse: true}
}

var reQuotedBrace = regexp.MustCompile(`['"][{}]['"]`)

func countUnescapedBraces(s string) (open, close int) {
	for i := 0; i < len(s); i++ {
		if s[i] == '{' && !isEscapedAt(s, i) {
			open++
		} else if s[i] == '}' && !isEscapedAt(s, i) {
			close++
		}
	}
	return
}

// ============================================================================
// 检测器 6: checkObfuscatedFlags — 混淆标志检测
// ============================================================================

// checkObfuscatedFlags 混淆标志检测。
// ANSI-C quoting ($'...') 和 locale quoting ($"...") 在任何情况下都需要拦截，
// 即使是 echo 命令——因为它们可以编码任意字符。
func checkObfuscatedFlags(ctx SecurityContext) SecurityCheck {
	raw := ctx.OriginalCommand
	base := ctx.BaseCommand

	// 1. ANSI-C quoting $'...' — 必须在 echo shortcut 之前检查
	if reANSIC.MatchString(raw) {
		return SecurityCheck{
			Name:    "obfuscated_flags",
			Passed:  false,
			Reason:  "Command contains ANSI-C quoting ($'...') which can hide characters",
			IsParse: true,
		}
	}

	// 2. Locale quoting $"..." — 必须在 echo shortcut 之前检查
	if reLocaleQ.MatchString(raw) {
		return SecurityCheck{
			Name:    "obfuscated_flags",
			Passed:  false,
			Reason:  "Command contains locale quoting ($\"...\") which can hide characters",
			IsParse: true,
		}
	}

	// echo 无管道操作符且无 ANSI-C/locale quoting 时是安全的
	hasOps := strings.ContainsAny(raw, "|&;")
	if base == "echo" && !hasOps {
		return SecurityCheck{Name: "obfuscated_flags", Passed: true, IsParse: true}
	}


	// 3. 空 ANSI-C/Locale 引号后跟短横
	if reEmptySpecialDash.MatchString(raw) {
		return SecurityCheck{
			Name:    "obfuscated_flags",
			Passed:  false,
			Reason:  "Command contains empty special quotes before dash (potential bypass)",
			IsParse: true,
		}
	}

	// 4. 空引号后跟短横
	if reEmptyQuoteDash.MatchString(raw) {
		return SecurityCheck{
			Name:    "obfuscated_flags",
			Passed:  false,
			Reason:  "Command contains empty quotes before dash (potential bypass)",
			IsParse: true,
		}
	}

	// 5. 同质空引号 + 带横引号（如 """-f"）
	if reHomoEmptyAdjDash.MatchString(raw) {
		return SecurityCheck{
			Name:    "obfuscated_flags",
			Passed:  false,
			Reason:  "Command contains empty quote pair adjacent to quoted dash (potential flag obfuscation)",
			IsParse: true,
		}
	}

	// 6. 词首 3+ 连续引号
	if reTripleQuote.MatchString(raw) {
		return SecurityCheck{
			Name:    "obfuscated_flags",
			Passed:  false,
			Reason:  "Command contains consecutive quote characters at word start (potential obfuscation)",
			IsParse: true,
		}
	}

	// 7. 引号内标志扫描（逐字符状态机）
	inSQ, inDQ, escaped := false, false, false
	for i := 0; i < len(raw)-1; i++ {
		ch := raw[i]
		next := raw[i+1]

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

		// 空白后跟引号 → 可能是 " 混淆
		if (ch == ' ' || ch == '\t') && (next == '\'' || next == '"' || next == '`') {
			quote := next
			j := i + 2
			inside := ""
			for j < len(raw) && raw[j] != quote {
				inside += string(raw[j])
				j++
			}
			if j < len(raw) && raw[j] == quote {
				charAfter := byte(0)
				if j+1 < len(raw) {
					charAfter = raw[j+1]
				}

				// 引号内容以 -+字母/数字 开头
				hasFlagInside := reFlagInsideQuote.MatchString(inside)
				// 引号内容为纯横线，且后跟标志延续字符
				hasFlagContinue := rePureDash.MatchString(inside) && charAfter != 0 && reFlagContinue.Match([]byte{charAfter})
				// 链式引号混淆
				hasChained := (inside == "" || rePureDash.MatchString(inside)) && charAfter != 0 &&
					(charAfter == '\'' || charAfter == '"' || charAfter == '`')

				if hasFlagInside || hasFlagContinue || hasChained {
					return SecurityCheck{
						Name:    "obfuscated_flags",
						Passed:  false,
						Reason:  "Command contains quoted characters in flag names",
						IsParse: true,
					}
				}
			}
		}
	}

	// 8. fullyUnquoted 中的空白后引号+横线
	if reUnquotedQuoteDash.MatchString(ctx.FullyUnquoted) {
		return SecurityCheck{
			Name:    "obfuscated_flags",
			Passed:  false,
			Reason:  "Command contains quoted characters in flag names",
			IsParse: true,
		}
	}

	// 9. fullyUnquoted 中的空引号+横线
	if reUnquotedEmptyQuoteDash.MatchString(ctx.FullyUnquoted) {
		return SecurityCheck{
			Name:    "obfuscated_flags",
			Passed:  false,
			Reason:  "Command contains quoted characters in flag names",
			IsParse: true,
		}
	}

	return SecurityCheck{Name: "obfuscated_flags", Passed: true, IsParse: true}
}

var (
	reANSIC               = regexp.MustCompile(`\$'[^']*'`)
	reLocaleQ             = regexp.MustCompile(`\$"[^"]*"`)
	reEmptySpecialDash    = regexp.MustCompile(`\$['"]{2}\s*-`)
	reEmptyQuoteDash      = regexp.MustCompile(`(?:^|\s)(?:''|"")+\s*-`)
	reHomoEmptyAdjDash    = regexp.MustCompile(`(?:""|'')+['"]-`)
	reTripleQuote         = regexp.MustCompile(`(?:^|\s)['"]{3,}`)
	reFlagInsideQuote     = regexp.MustCompile(`^-+[a-zA-Z0-9$\x60]`)
	rePureDash            = regexp.MustCompile(`^-+$`)
	reFlagContinue        = regexp.MustCompile(`[a-zA-Z0-9\\$\x60\-{]`)
	reUnquotedQuoteDash   = regexp.MustCompile(`\s['"\x60]-`)
	reUnquotedEmptyQuoteDash = regexp.MustCompile(`['"\x60]{2}-`)
)

// ============================================================================
// 检测器 7: checkShellMetacharacters — Shell 元字符
// ============================================================================

func checkShellMetacharacters(ctx SecurityContext) SecurityCheck {
	unquoted := ctx.Unquoted
	msg := "Command contains shell metacharacters (;, |, or &) in arguments"

	// 引号内的 ; & |
	if reQuotedMeta.MatchString(unquoted) {
		return SecurityCheck{Name: "shell_metacharacters", Passed: false, Reason: msg}
	}

	// find -name / -path / -iname 参数中的引号内 ; | &
	if reFindNameMeta.MatchString(unquoted) || reFindPathMeta.MatchString(unquoted) || reInameMeta.MatchString(unquoted) {
		return SecurityCheck{Name: "shell_metacharacters", Passed: false, Reason: msg}
	}

	// find -regex 参数中的 ; &
	if reFindRegexMeta.MatchString(unquoted) {
		return SecurityCheck{Name: "shell_metacharacters", Passed: false, Reason: msg}
	}

	return SecurityCheck{Name: "shell_metacharacters", Passed: true}
}

var (
	reQuotedMeta    = regexp.MustCompile(`(?:^|\s)["'][^"']*[;&][^"']*["'](?:\s|$)`)
	reFindNameMeta  = regexp.MustCompile(`-name\s+["'][^"']*[;|&][^"']*["']`)
	reFindPathMeta  = regexp.MustCompile(`-path\s+["'][^"']*[;|&][^"']*["']`)
	reInameMeta     = regexp.MustCompile(`-iname\s+["'][^"']*[;|&][^"']*["']`)
	reFindRegexMeta = regexp.MustCompile(`-regex\s+["'][^"']*[;&][^"']*["']`)
)

// ============================================================================
// 检测器 8: checkDangerousVariables — 危险位置的变量展开
// ============================================================================

func checkDangerousVariables(ctx SecurityContext) SecurityCheck {
	full := ctx.FullyUnquoted

	if reVarNearRedirect.MatchString(full) || reRedirectNearVar.MatchString(full) {
		return SecurityCheck{
			Name:   "dangerous_variables",
			Passed: false,
			Reason: "Command contains variables in dangerous contexts (redirections or pipes)",
		}
	}
	return SecurityCheck{Name: "dangerous_variables", Passed: true}
}

var (
	reVarNearRedirect  = regexp.MustCompile(`[<>|]\s*\$[A-Za-z_]`)
	reRedirectNearVar  = regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*\s*[|<>]`)
)

// ============================================================================
// 检测器 9: checkDangerousPatterns — 危险模式（命令替换等）
// ============================================================================

// commandSubstPatterns 命令替换模式列表。
var commandSubstPatterns = []struct {
	pattern *regexp.Regexp
	message string
}{
	{regexp.MustCompile(`<\(`), "process substitution <()"},
	{regexp.MustCompile(`>\(`), "process substitution >()"},
	{regexp.MustCompile(`=\(`), "Zsh process substitution =()"},
	{regexp.MustCompile(`(?:^|[\s;&|])=[a-zA-Z_]`), "Zsh equals expansion (=cmd)"},
	{regexp.MustCompile(`\$\(`), "$() command substitution"},
	{regexp.MustCompile(`\$\{`), "${} parameter substitution"},
	{regexp.MustCompile(`\$\[`), "$[] legacy arithmetic expansion"},
	{regexp.MustCompile(`~\[`), "Zsh-style parameter expansion"},
	{regexp.MustCompile(`\(e:`), "Zsh-style glob qualifiers"},
	{regexp.MustCompile(`\(\+`), "Zsh glob qualifier with command execution"},
	{regexp.MustCompile(`\}\s*always\s*\{`), "Zsh always block (try/always construct)"},
	{regexp.MustCompile(`<#`), "PowerShell comment syntax"},
}

func checkDangerousPatterns(ctx SecurityContext) SecurityCheck {
	unquoted := ctx.Unquoted

	// 反引号（未转义）
	if hasUnescapedChar(unquoted, '`') {
		return SecurityCheck{
			Name:   "dangerous_patterns",
			Passed: false,
			Reason: "Command contains backticks (`) for command substitution",
		}
	}

	// 命令替换模式
	for _, p := range commandSubstPatterns {
		if p.pattern.MatchString(unquoted) {
			return SecurityCheck{
				Name:   "dangerous_patterns",
				Passed: false,
				Reason: "Command contains " + p.message,
			}
		}
	}

	return SecurityCheck{Name: "dangerous_patterns", Passed: true}
}

// ============================================================================
// 检测器 10: checkRedirections — 输入/输出重定向
// ============================================================================

func checkRedirections(ctx SecurityContext) SecurityCheck {
	full := ctx.FullyUnquoted

	if strings.ContainsRune(full, '<') {
		return SecurityCheck{
			Name:   "input_redirection",
			Passed: false,
			Reason: "Command contains input redirection (<) which could read sensitive files",
		}
	}

	if strings.ContainsRune(full, '>') {
		return SecurityCheck{
			Name:   "output_redirection",
			Passed: false,
			Reason: "Command contains output redirection (>) which could write to arbitrary files",
		}
	}

	return SecurityCheck{Name: "redirections", Passed: true}
}

// ============================================================================
// 检测器 11: checkNewlines — 命令中的换行符
// ============================================================================

func checkNewlines(ctx SecurityContext) SecurityCheck {
	full := ctx.FullyUnquoted

	if !strings.ContainsAny(full, "\n\r") {
		return SecurityCheck{Name: "newlines", Passed: true}
	}

	// 检查是否为反斜杠+换行延续（安全）
	// 非白名单空格 + 反斜杠 + 换行 → 安全
	// 反之 → 可能是隐藏的命令分隔符
	if reCmdAfterNewline.MatchString(full) {
		return SecurityCheck{
			Name:   "newlines",
			Passed: false,
			Reason: "Command contains newlines that could separate multiple commands",
		}
	}

	return SecurityCheck{Name: "newlines", Passed: true}
}

var reCmdAfterNewline = regexp.MustCompile(`[^\\\s][\n\r]\s*\S`)

// ============================================================================
// 检测器 12: checkCarriageReturn — 回车符 parser differential
// ============================================================================

func checkCarriageReturn(ctx SecurityContext) SecurityCheck {
	raw := ctx.OriginalCommand

	if !strings.ContainsRune(raw, '\r') {
		return SecurityCheck{Name: "carriage_return", Passed: true, IsParse: true}
	}

	// CR 在双引号外是 parser differential
	inSQ, inDQ, escaped := false, false, false
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
		if ch == '\r' && !inDQ {
			return SecurityCheck{
				Name:    "carriage_return",
				Passed:  false,
				Reason:  "Command contains carriage return (\\r) which shell-quote and bash tokenize differently",
				IsParse: true,
			}
		}
	}
	return SecurityCheck{Name: "carriage_return", Passed: true, IsParse: true}
}

// ============================================================================
// 检测器 13: checkIFSInjection — IFS 变量注入
// ============================================================================

var reIFS = regexp.MustCompile(`\$IFS|\$\{[^}]*IFS`)

func checkIFSInjection(ctx SecurityContext) SecurityCheck {
	raw := ctx.OriginalCommand
	if reIFS.MatchString(raw) {
		return SecurityCheck{
			Name:   "ifs_injection",
			Passed: false,
			Reason: "Command contains IFS variable usage which could bypass security validation",
		}
	}
	return SecurityCheck{Name: "ifs_injection", Passed: true}
}

// ============================================================================
// 检测器 14: checkProcEnvironAccess — /proc/*/environ 访问
// ============================================================================

var reProcEnviron = regexp.MustCompile(`/proc/.*/environ`)

func checkProcEnvironAccess(ctx SecurityContext) SecurityCheck {
	raw := ctx.OriginalCommand
	if reProcEnviron.MatchString(raw) {
		return SecurityCheck{
			Name:   "proc_environ_access",
			Passed: false,
			Reason: "Command accesses /proc/*/environ which could expose sensitive environment variables",
		}
	}
	return SecurityCheck{Name: "proc_environ_access", Passed: true}
}

// ============================================================================
// 检测器 15: checkMalformedTokenInjection — 畸形 token 注入
// ============================================================================

func checkMalformedTokenInjection(ctx SecurityContext) SecurityCheck {
	raw := ctx.OriginalCommand

	// 检测命令分隔符（;, &&, ||）的存在
	hasSep := reCmdSeparator.MatchString(raw)
	if !hasSep {
		return SecurityCheck{Name: "malformed_token_injection", Passed: true, IsParse: true}
	}

	// 检测未闭合的引号（畸形 token）
	if hasUnbalancedQuotes(raw) {
		return SecurityCheck{
			Name:    "malformed_token_injection",
			Passed:  false,
			Reason:  "Command contains ambiguous syntax with command separators that could be misinterpreted",
			IsParse: true,
		}
	}

	return SecurityCheck{Name: "malformed_token_injection", Passed: true, IsParse: true}
}

var reCmdSeparator = regexp.MustCompile(`[;&]|\|\|`)

func hasUnbalancedQuotes(s string) bool {
	inSQ, inDQ, escaped := false, false, false
	for i := 0; i < len(s); i++ {
		ch := s[i]
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
		} else if ch == '"' && !inSQ {
			inDQ = !inDQ
		}
	}
	return inSQ || inDQ
}

// ============================================================================
// 检测器 16: checkUnicodeWhitespace — Unicode 空白符
// ============================================================================

func checkUnicodeWhitespace(ctx SecurityContext) SecurityCheck {
	raw := ctx.OriginalCommand

	for _, r := range raw {
		if isUnicodeWS(r) {
			return SecurityCheck{
				Name:    "unicode_whitespace",
				Passed:  false,
				Reason:  "Command contains Unicode whitespace characters that could cause parsing inconsistencies",
				IsParse: true,
			}
		}
	}
	return SecurityCheck{Name: "unicode_whitespace", Passed: true, IsParse: true}
}

// unicodeWS 匹配 Unicode 空白字符。
var unicodeWS = map[rune]bool{
	'\u00A0': true, // NO-BREAK SPACE
	'\u1680': true, // OGHAM SPACE MARK
	'\u2000': true, '\u2001': true, '\u2002': true, '\u2003': true,
	'\u2004': true, '\u2005': true, '\u2006': true, '\u2007': true,
	'\u2008': true, '\u2009': true, '\u200A': true, // EN QUAD ~ HAIR SPACE
	'\u2028': true, // LINE SEPARATOR
	'\u2029': true, // PARAGRAPH SEPARATOR
	'\u202F': true, // NARROW NO-BREAK SPACE
	'\u205F': true, // MEDIUM MATHEMATICAL SPACE
	'\u3000': true, // IDEOGRAPHIC SPACE
	'\uFEFF': true, // ZERO WIDTH NO-BREAK SPACE (BOM)
}

func isUnicodeWS(r rune) bool {
	return unicodeWS[r]
}

// ============================================================================
// 检测器 17: checkMidWordHash — 词中 # 符号
// ============================================================================

// checkMidWordHash 检测词中的 # 符号。
// Go 的 regexp 不支持 lookbehind,因此手动排除 ${# 长度语法。
func checkMidWordHash(ctx SecurityContext) SecurityCheck {
	keepQ := ctx.KeepQuotes

	// 扫描非空白字符后跟 # 的序列,排除 ${#
	for i := 0; i < len(keepQ)-1; i++ {
		if keepQ[i] == '#' {
			// 检查 # 前是否为非空白字符
			if i > 0 && !isWhitespace(keepQ[i-1]) {
				// 排除 ${# 长度语法
				if i >= 2 && keepQ[i-2] == '$' && keepQ[i-1] == '{' {
					continue
				}
				return SecurityCheck{
					Name:   "mid_word_hash",
					Passed: false,
					Reason: "Command contains mid-word # which is parsed differently by shell-quote vs bash",
					IsParse: true,
				}
			}
		}
	}
	return SecurityCheck{Name: "mid_word_hash", Passed: true, IsParse: true}
}

func isWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// ============================================================================
// 检测器 18: checkCommentQuoteDesync — 注释引号脱同步
// ============================================================================

func checkCommentQuoteDesync(ctx SecurityContext) SecurityCheck {
	raw := ctx.OriginalCommand

	inSQ, inDQ, escaped := false, false, false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]

		if escaped {
			escaped = false
			continue
		}

		if inSQ {
			if ch == '\'' {
				inSQ = false
			}
			continue
		}

		if ch == '\\' {
			escaped = true
			continue
		}

		if inDQ {
			if ch == '"' {
				inDQ = false
			}
			continue
		}

		if ch == '\'' {
			inSQ = true
			continue
		}

		if ch == '"' {
			inDQ = true
			continue
		}

		// 未引用的 # — bash 视作注释开始
		if ch == '#' {
			// 检查此行剩余部分是否含引号字符
			lineEnd := strings.IndexByte(raw[i+1:], '\n')
			comment := ""
			if lineEnd >= 0 {
				comment = raw[i+1 : i+1+lineEnd]
			} else {
				comment = raw[i+1:]
			}
			if strings.ContainsAny(comment, "'\"") {
				return SecurityCheck{
					Name:    "comment_quote_desync",
					Passed:  false,
					Reason:  "Command contains quote characters inside a # comment which can desync quote tracking",
					IsParse: true,
				}
			}
			// 跳过注释至行尾
			if lineEnd >= 0 {
				i = i + lineEnd
			} else {
				break
			}
		}
	}
	return SecurityCheck{Name: "comment_quote_desync", Passed: true, IsParse: true}
}

// ============================================================================
// 检测器 19: checkQuotedNewline — 引号内换行后跟 # 行
// ============================================================================

func checkQuotedNewline(ctx SecurityContext) SecurityCheck {
	raw := ctx.OriginalCommand

	if !strings.ContainsRune(raw, '\n') || !strings.ContainsRune(raw, '#') {
		return SecurityCheck{Name: "quoted_newline", Passed: true, IsParse: true}
	}

	inSQ, inDQ, escaped := false, false, false
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

		// 引号内的换行 → 检查下一行是否以 # 开头
		if ch == '\n' && (inSQ || inDQ) {
			lineStart := i + 1
			nextNL := strings.IndexByte(raw[lineStart:], '\n')
			lineEnd := len(raw)
			if nextNL >= 0 {
				lineEnd = lineStart + nextNL
			}
			nextLine := raw[lineStart:lineEnd]
			if strings.HasPrefix(strings.TrimSpace(nextLine), "#") {
				return SecurityCheck{
					Name:    "quoted_newline",
					Passed:  false,
					Reason:  "Command contains a quoted newline followed by a #-prefixed line, which can hide arguments from line-based permission checks",
					IsParse: true,
				}
			}
		}
	}
	return SecurityCheck{Name: "quoted_newline", Passed: true, IsParse: true}
}

// ============================================================================
// 检测器 20: checkJqCommand — jq 危险函数/标志
// ============================================================================

var reJqSystem = regexp.MustCompile(`\bsystem\s*\(`)

func checkJqCommand(ctx SecurityContext) SecurityCheck {
	if ctx.BaseCommand != "jq" {
		return SecurityCheck{Name: "jq_command", Passed: true}
	}

	raw := ctx.OriginalCommand

	// jq system() 函数
	if reJqSystem.MatchString(raw) {
		return SecurityCheck{
			Name:   "jq_command",
			Passed: false,
			Reason: "jq command contains system() function which executes arbitrary commands",
		}
	}

	// jq 危险标志
	afterJq := strings.TrimSpace(raw[2:])
	if reJqDangerousFlag.MatchString(afterJq) {
		return SecurityCheck{
			Name:   "jq_command",
			Passed: false,
			Reason: "jq command contains dangerous flags that could execute code or read arbitrary files",
		}
	}

	return SecurityCheck{Name: "jq_command", Passed: true}
}

var reJqDangerousFlag = regexp.MustCompile(`(?:^|\s)(?:-f\b|--from-file|--rawfile|--slurpfile|-L\b|--library-path)`)

// ============================================================================
// 检测器 21: checkGitCommit — Git commit 命令替换/重定向
// ============================================================================

var reGitCommit = regexp.MustCompile(`^git[ \t]+commit[ \t]+`)

func checkGitCommit(ctx SecurityContext) SecurityCheck {
	raw := ctx.OriginalCommand

	if !reGitCommit.MatchString(raw) {
		return SecurityCheck{Name: "git_commit", Passed: true}
	}

	// 反斜杠 → 交给完整验证链
	if strings.ContainsRune(raw, '\\') {
		return SecurityCheck{Name: "git_commit", Passed: true}
	}

	// 手动扫描 -m "message" 或 -m 'message'
	// reGitCommitMsg 匹配 git commit message 引用。
	quote, msgContent, remainder := extractGitCommitMsg(raw)
	if quote == 0 {
		return SecurityCheck{Name: "git_commit", Passed: true}
	}

	msg := string(msgContent)

	// 双引号内的命令替换
	if quote == '"' && reCmdSubst.MatchString(msg) {
		return SecurityCheck{
			Name:   "git_commit",
			Passed: false,
			Reason: "Git commit message contains command substitution patterns",
		}
	}

	// 消息以 - 开头(混淆标志)
	if len(msg) > 0 && msg[0] == '-' {
		return SecurityCheck{
			Name:   "git_commit",
			Passed: false,
			Reason: "Git commit message starts with dash — potential flag obfuscation",
		}
	}

	rem := string(remainder)

	// 余下部分含 shell 元字符
	if reShellMetaInRemainder.MatchString(rem) {
		return SecurityCheck{Name: "git_commit", Passed: true} // 交给完整链
	}

	// 余下部分含未引用的重定向
	if rem != "" {
		if hasUnquotedRedirect(rem) {
			return SecurityCheck{Name: "git_commit", Passed: true} // 交给完整链
		}
	}

	// 安全的 git commit
	return SecurityCheck{Name: "git_commit", Passed: true}
}

// extractGitCommitMsg 从 git commit 命令中提取 -m 后的引号内容。
// 返回 (quote, message, remainder)，quote=0 表示未找到。
// extractGitCommitMsg 从 git commit 命令中提取 -m 后的引号内容。
// 返回 (quote, message, remainder)，quote=0 表示未找到。
func extractGitCommitMsg(cmd string) (quote byte, msg string, remainder string) {
	// 跳过 "git commit "
	prefix := "git commit"
	idx := strings.Index(cmd, prefix)
	if idx < 0 {
		return 0, "", ""
	}
	rest := cmd[idx+len(prefix):]

	// 扫描到 -m
	for len(rest) > 0 {
		sp := skipSpaceStr(rest)
		rest = rest[sp:]
		if len(rest) == 0 {
			return 0, "", ""
		}
		tokenEnd := findTokenEndStr(rest)
		token := rest[:tokenEnd]
		if strings.HasPrefix(token, "-m") {
			rest = rest[tokenEnd:]
			rest = rest[skipSpaceStr(rest):]
			if len(rest) == 0 {
				return 0, "", ""
			}
			if rest[0] == '"' || rest[0] == '\'' {
				quote = rest[0]
				rest = rest[1:]
				closeIdx := findClosingQuoteStr(rest, quote)
				if closeIdx < 0 {
					return 0, "", ""
				}
				msg = rest[:closeIdx]
				remainder = rest[closeIdx+1:]
				return quote, msg, remainder
			}
			return 0, "", ""
		}
		rest = rest[tokenEnd:]
	}
	return 0, "", ""
}

func skipSpaceStr(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return i
		}
	}
	return len(s)
}

func findTokenEndStr(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			return i
		}
	}
	return len(s)
}

func findClosingQuoteStr(s string, quote byte) int {
	escaped := false
	for i := 0; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		if s[i] == '\\' && quote == '"' {
			escaped = true
			continue
		}
		if s[i] == quote {
			return i
		}
	}
	return -1
}


var (
	reCmdSubst             = regexp.MustCompile(`\$\(|\x60|\$\{`)
	reShellMetaInRemainder = regexp.MustCompile(`[;|&()\x60]|\$\(|\$\{`)
)

func hasUnquotedRedirect(s string) bool {
	inSQ, inDQ := false, false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '\'' && !inDQ {
			inSQ = !inSQ
			continue
		}
		if ch == '"' && !inSQ {
			inDQ = !inDQ
			continue
		}
		if !inSQ && !inDQ && (ch == '<' || ch == '>') {
			return true
		}
	}
	return false
}

// ============================================================================
// 检测器 22: checkZshDangerousCommands — Zsh 危险命令
// ============================================================================

// zshDangerousCommands Zsh 特有危险命令集合。
var zshDangerousCommands = map[string]bool{
	"zmodload":  true,
	"emulate":   true,
	"sysopen":   true,
	"sysread":   true,
	"syswrite":  true,
	"sysseek":   true,
	"zpty":      true,
	"ztcp":      true,
	"zsocket":   true,
	"mapfile":   true,
	"zf_rm":     true,
	"zf_mv":     true,
	"zf_ln":     true,
	"zf_chmod":  true,
	"zf_chown":  true,
	"zf_mkdir":  true,
	"zf_rmdir":  true,
	"zf_chgrp":  true,
}

// zshPrecommandModifiers Zsh 前置命令修饰符。
var zshPrecommandModifiers = map[string]bool{
	"command":   true,
	"builtin":   true,
	"noglob":    true,
	"nocorrect": true,
}

func checkZshDangerousCommands(ctx SecurityContext) SecurityCheck {
	raw := ctx.OriginalCommand

	// 从原始命令中提取基础命令名（跳过 env 赋值和 Zsh 修饰符）
	tokens := strings.Fields(strings.TrimSpace(raw))
	baseCmd := ""
	for _, t := range tokens {
		// 跳过环境变量赋值
		if reEnvAssign.MatchString(t) {
			continue
		}
		// 跳过 Zsh 前置修饰符
		if zshPrecommandModifiers[t] {
			continue
		}
		baseCmd = t
		break
	}

	if zshDangerousCommands[baseCmd] {
		return SecurityCheck{
			Name:   "zsh_dangerous_command",
			Passed: false,
			Reason: "Command uses Zsh builtin: " + baseCmd + " — this can bypass security checks",
		}
	}
	return SecurityCheck{Name: "zsh_dangerous_command", Passed: true}
}

var reEnvAssign = regexp.MustCompile(`^[A-Za-z_]\w*=`)

// ============================================================================
// 检测器 23: checkControlCharacters — 控制字符
// ============================================================================

func checkControlCharacters(ctx SecurityContext) SecurityCheck {
	raw := ctx.OriginalCommand

	for _, r := range raw {
		if isControlChar(r) {
			return SecurityCheck{
				Name:    "control_characters",
				Passed:  false,
				Reason:  "Command contains control characters that can hide command content",
				IsParse: true,
			}
		}
	}
	return SecurityCheck{Name: "control_characters", Passed: true, IsParse: true}
}

// isControlChar 检查是否为不可打印控制字符（排除常见空白符）。
func isControlChar(r rune) bool {
	if r >= 0x20 && r <= 0x7E {
		return false // 可打印 ASCII
	}
	if r == '\t' || r == '\n' || r == '\r' {
		return false // 已在其他检测器中处理
	}
	if r > 127 {
		return false // 非 ASCII（Unicode WS 在 checkUnicodeWhitespace 处理）
	}
	// 0x00–0x1F 或 0x7F (DEL) → 控制字符
	return r < 0x20 || r == 0x7F
}

// ============================================================================
// 检测器 24: checkSafeCommandSubstitution — 安全 heredoc 检测
// ============================================================================

// reHeredocInSubst 匹配 $(cat <<... 模式。
var reHeredocInSubst = regexp.MustCompile(`\$\(.*<<`)

func checkSafeCommandSubstitution(ctx SecurityContext) SecurityCheck {
	raw := ctx.OriginalCommand
	if !reHeredocInSubst.MatchString(raw) {
		return SecurityCheck{Name: "safe_heredoc", Passed: true}
	}

	// 检测安全 heredoc 模式：$(cat <<'DELIM'...DELIM)
	if isSafeHeredoc(raw) {
		return SecurityCheck{
			Name:   "safe_heredoc",
			Passed: true,
			Reason: "Safe command substitution: cat with quoted/escaped heredoc delimiter",
		}
	}

	return SecurityCheck{Name: "safe_heredoc", Passed: true}
}

// isSafeHeredoc 检测 $(cat <<'DELIM'\n...\nDELIM\n) 形式的安全 heredoc。
func isSafeHeredoc(cmd string) bool {
	re := regexp.MustCompile(`\$\(cat[ \t]*<<(-?)[ \t]*(?:'+([A-Za-z_]\w*)'+|\\([A-Za-z_]\w*))`)
	matches := re.FindAllStringSubmatchIndex(cmd, -1)
	if len(matches) == 0 {
		return false
	}

	type hRange struct{ start, end int }
	var ranges []hRange

	for _, m := range matches {
		operatorEnd := m[1]
		afterOp := cmd[operatorEnd:]

		nl := strings.IndexByte(afterOp, '\n')
		if nl < 0 {
			continue
		}
		// 开行末尾只能有空白
		if !reOnlyWS.MatchString(afterOp[:nl]) {
			continue
		}

		// 提取定界符
		rawDelim := ""
		for g := 2; g < len(m); g += 2 {
			if m[g] >= 0 {
				rawDelim = cmd[m[g]:m[g+1]]
				break
			}
		}
		delim := strings.Trim(rawDelim, "'\"")

		bodyStart := operatorEnd + nl + 1
		body := cmd[bodyStart:]
		bodyLines := strings.Split(body, "\n")

		// 找行首匹配的定界符
		found := false
		for li, line := range bodyLines {
			if line == delim {
				// 下一行必须以 ) 开头
				if li+1 < len(bodyLines) && reOpenParen.MatchString(bodyLines[li+1]) {
					// 计算绝对位置
					endPos := bodyStart + len(strings.Join(bodyLines[:li+1], "\n")) + 1
					parenPos := strings.IndexByte(cmd[endPos:], ')')
					if parenPos >= 0 {
						ranges = append(ranges, hRange{m[0], endPos + parenPos + 1})
						found = true
					}
				} else if reInlineParen.MatchString(line[len(delim):]) {
					// 内联形式：DELIM)
					endPos := bodyStart + len(strings.Join(bodyLines[:li], "\n"))
					if li > 0 {
						endPos++
					}
					parenIdx := strings.IndexByte(cmd[endPos:], ')')
					if parenIdx >= 0 {
						ranges = append(ranges, hRange{m[0], endPos + parenIdx + 1})
						found = true
					}
				}
				break
			}
		}
		if !found {
			return false
		}
	}

	// 检测嵌套匹配
	for i := range ranges {
		for j := range ranges {
			if i == j {
				continue
			}
			if ranges[j].start > ranges[i].start && ranges[j].start < ranges[i].end {
				return false
			}
		}
	}

	// 剥离所有 heredoc 后检查余下内容
	remaining := cmd
	sortRanges := make([]hRange, len(ranges))
	copy(sortRanges, ranges)
	// 逆序排序
	for i := 0; i < len(sortRanges)-1; i++ {
		for j := i + 1; j < len(sortRanges); j++ {
			if sortRanges[j].start > sortRanges[i].start {
				sortRanges[i], sortRanges[j] = sortRanges[j], sortRanges[i]
			}
		}
	}
	for _, r := range sortRanges {
		remaining = remaining[:r.start] + remaining[r.end:]
	}

	// 余下内容必须是安全字符
	if !reSafeChars.MatchString(remaining) {
		return false
	}

	// 余下的 heredoc 剥离后的命令也需通过安全审查
	trimmed := strings.TrimSpace(remaining)
	if trimmed != "" {
		subReport, _ := Audit(remaining)
		if subReport != nil && subReport.HasIssue {
			return false
		}
	}

	return true
}

var (
	reOnlyWS       = regexp.MustCompile(`^[ \t]*$`)
	reOpenParen    = regexp.MustCompile(`^[ \t]*\)`)
	reInlineParen  = regexp.MustCompile(`^[ \t]*\)`)
	reSafeChars    = regexp.MustCompile(`^[a-zA-Z0-9 \t"'.\-/_@=,:+~]*$`)
)

// ============================================================================
// IsBashSecurityCheckForMisparsing 判断 SecurityCheck 是否为 parser differential 类型。
// 检测是否需要进行误解析安全检查。
// ============================================================================

// IsMisparsingCheck 检查 SecurityCheck 是否为解析器差异检测。
func IsMisparsingCheck(sc SecurityCheck) bool {
	return sc.IsParse
}

// ============================================================================
// 便捷函数：判断命令是否有 parser differential 问题
// ============================================================================

// HasParserDifferential 快速检查命令是否存在 parser differential 问题。
func HasParserDifferential(raw string) bool {
	ci, err := Parse(raw)
	if err != nil {
		ci = &CommandInfo{Raw: raw}
	}
	ctx := buildSecurityContext(raw, ci)

	checks := []func(SecurityContext) SecurityCheck{
		checkBackslashOperators,
		checkBackslashEscapedWhitespace,
		checkBraceExpansion,
		checkObfuscatedFlags,
		checkCarriageReturn,
		checkMidWordHash,
		checkCommentQuoteDesync,
		checkQuotedNewline,
		checkUnicodeWhitespace,
		checkMalformedTokenInjection,
		checkControlCharacters,
	}

	for _, fn := range checks {
		sc := fn(ctx)
		if !sc.Passed && sc.IsParse {
			return true
		}
	}
	return false
}

// Helper init function to ensure unicode category is used.
var _ = unicode.Cf // 确保 unicode 包被使用（sanitize 层用到）
