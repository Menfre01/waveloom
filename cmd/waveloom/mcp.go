package main

import (
	"os"

	"github.com/Menfre01/waveloom/pkg/mcp"
)

// runMCPCommand 处理 waveloom mcp 子命令。
func runMCPCommand(args []string) {
	// 移除 "mcp" 前缀，传递剩余参数
	mcp.RunMCPCommand(args)
	os.Exit(0)
}
