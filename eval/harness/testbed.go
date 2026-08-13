package harness

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Menfre01/waveloom/pkg/tool"
)

// testbedShell 包装 tool.Shell,在每条 bash 命令前注入 testbed env 激活,
// 使宿主 agent 拥有与官方 testbed 一致的自测环境(非容器评测核心)。
//
// 背景:规格书 P0-1 要求 agent 与官方测试同环境(pylint-4661 案例)。
// 容器只是环境载体之一;宿主 conda/uv venv 同样可提供 testbed 环境,
// 通过 bash 前缀 source activate 注入,agent 即可 pytest 自测。
type testbedShell struct {
	*tool.Shell
	activate string // "source <venv>/bin/activate"
}

// NewTestbedShell 构造 testbed 注入 shell。testbedPython 为 venv 内 python
// 绝对路径(如 /tmp/pylint-testbed/bin/python),由它推导 activate 脚本路径。
func NewTestbedShell(inner *tool.Shell, testbedPython string) *testbedShell {
	return &testbedShell{
		Shell:    inner,
		activate: filepath.Join(filepath.Dir(testbedPython), "activate"),
	}
}

// Execute 注入激活前缀后转调底层 Shell。
func (t *testbedShell) Execute(ctx context.Context, p tool.ShellParams) (*tool.ToolResult, error) {
	p.Command = fmt.Sprintf("source %s && %s", t.activate, p.Command)
	return t.Shell.Execute(ctx, p)
}

// ExecuteStreaming 同 Execute,但保留流式输出(Shell 为 TypedStreamableTool,
// Wrap 会检测此方法,缺失则流式降级为普通 Execute)。
func (t *testbedShell) ExecuteStreaming(ctx context.Context, p tool.ShellParams, chunkCb func(string)) (*tool.ToolResult, error) {
	p.Command = fmt.Sprintf("source %s && %s", t.activate, p.Command)
	return t.Shell.ExecuteStreaming(ctx, p, chunkCb)
}
