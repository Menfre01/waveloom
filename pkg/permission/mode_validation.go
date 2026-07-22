// Package permission — 模式感知权限（对标 Claude Code modeValidation.ts）
package permission

import "github.com/Menfre01/waveloom/pkg/bash"

// ============================================================================
// PermissionMode — 权限模式
// ============================================================================

// PermissionMode 表示当前会话的权限模式。
type PermissionMode string

const (
	ModeDefault         PermissionMode = "default"
	ModeAcceptEdits     PermissionMode = "acceptEdits"
	ModeBypass          PermissionMode = "bypassPermissions"
	ModeDontAsk         PermissionMode = "dontAsk"
	ModePlan            PermissionMode = "plan"
)

// ============================================================================
// AcceptEdits 自动批准命令
// ============================================================================

// acceptEditsCommands 是 acceptEdits 模式下可自动批准的文件系统命令。
var acceptEditsCommands = map[string]bool{
	"mkdir": true,
	"touch": true,
	"rm":    true,
	"rmdir": true,
	"mv":    true,
	"cp":    true,
	"sed":   true,
}

// DecisionNone 表示当前检查不适用，交给下一层权限检查。
const DecisionNone Decision = ""

// ============================================================================
// CheckPermissionMode — 模式感知权限决策
// ============================================================================

// ModeDecision 包含模式感知的权限决策结果。
type ModeDecision struct {
	Decision Decision
	Reason   string
}

// CheckPermissionMode 根据当前模式和命令返回模式感知的权限决策。
//
// 返回：
//   - DecisionAllow: 当前模式允许自动批准
//   - DecisionAsk: 需要用户确认
//   - DecisionNone: 当前模式不适用（交给常规权限检查）
func CheckPermissionMode(cmd string, mode PermissionMode) ModeDecision {
	switch mode {
	case ModeBypass, ModeDontAsk:
		// 这些模式在主流权限检查中处理
		return ModeDecision{Decision: DecisionNone}

	case ModeAcceptEdits:
		return checkAcceptEditsMode(cmd)

	case ModePlan:
		return checkPlanMode(cmd)
	}

	return ModeDecision{Decision: DecisionNone}
}

func checkAcceptEditsMode(cmd string) ModeDecision {
	ci := bash.ParseLenient(cmd)
	if ci == nil || ci.BaseCommand == "" {
		return ModeDecision{Decision: DecisionNone}
	}

	if acceptEditsCommands[ci.BaseCommand] {
		return ModeDecision{
			Decision: DecisionAllow,
			Reason:   "acceptEdits mode: auto-allow filesystem operation '" + ci.BaseCommand + "'",
		}
	}

	return ModeDecision{Decision: DecisionNone}
}

func checkPlanMode(cmd string) ModeDecision {
	ci := bash.ParseLenient(cmd)
	if ci == nil || ci.BaseCommand == "" {
		return ModeDecision{Decision: DecisionNone}
	}

	// Plan Mode: 仅允许配置了只读白名单的命令自动批准
	result := ValidateFlagsReadOnly(cmd)
	if result.Allowed {
		return ModeDecision{
			Decision: DecisionAllow,
			Reason:   "plan mode: all flags in read-only allowlist for '" + ci.BaseCommand + "'",
		}
	}

	// 白名单不通过 → 交给常规权限检查
	return ModeDecision{Decision: DecisionNone}
}
