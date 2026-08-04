package acp

import (
	"context"
)

// CommandRunner 执行 ACP 场景可用的斜杠命令(由入口注入,如 cmd/waveloom 的
// acpCommandRunner)。pkg/acp 不依赖 slashcommand 包——具体命令系统由
// 入口实现并注入,保持协议层与命令系统解耦。
//
// Run 返回:
//   - handled=false → 输入不是命令(正常走 LLM)
//   - handled=true, injectedPrompt != "" → 命令加载了指令文本(skill),
//     注入 prompt 继续 LLM 处理
//   - handled=true, resultText != "" → 命令直接产出文本结果,
//     作为 agent 消息回复,不调用 LLM
type CommandRunner interface {
	Run(ctx context.Context, input string) (resultText, injectedPrompt string, handled bool)
	// AvailableCommands 返回客户端命令面板可展示的命令列表。
	AvailableCommands() []AvailableCommand
}

// sendAvailableCommands 发送 available_commands_update 通知。
func (a *adapter) sendAvailableCommands(cmds []AvailableCommand) {
	if len(cmds) == 0 {
		return
	}
	a.sendUpdate(AvailableCommandsUpdate{
		SessionUpdate:     "available_commands_update",
		AvailableCommands: cmds,
	})
}
