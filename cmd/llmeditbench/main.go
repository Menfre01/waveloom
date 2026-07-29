// llmeditbench — Layer 3 LLM-in-the-loop 编辑可用性基准测试
//
// 用法:
//   llmeditbench -task <name>        跑单个任务
//   llmeditbench -all                 跑全部试点任务
//   llmeditbench -task <name> -n 5   跑 5 次取平均
//
// 依赖 ~/.waveloom/settings.json 中的 LLM 配置(provider/model/api_key)。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Menfre01/waveloom/eval/llmedit"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/tool"
)

func main() {
	taskName := flag.String("task", "", "任务名(子串匹配)")
	all := flag.Bool("all", false, "跑全部试点任务")
	repeat := flag.Int("n", 1, "每个任务重复次数")
	taskDir := flag.String("tasks", "eval/llmedit/testdata/tasks", "任务目录")
	provider := flag.String("provider", "", "LLM provider(覆盖 settings)")
	model := flag.String("model", "", "LLM model(覆盖 settings)")
	flag.Parse()

	if *taskName == "" && !*all {
		fmt.Fprintln(os.Stderr, "用法: llmeditbench -task <name> 或 -all")
		os.Exit(1)
	}

	// 1. 加载 LLM 配置
	home, _ := os.UserHomeDir()
	globalSettings, _ := llm.LoadSettingsIfExists(home + "/.waveloom/settings.json")
	projectSettings, _ := llm.LoadSettingsIfExists(".waveloom/settings.json")
	merged := llm.MergeLLMSettings(globalSettings, projectSettings)
	if merged == nil {
		fmt.Fprintln(os.Stderr, "未找到 LLM 配置(检查 ~/.waveloom/settings.json)")
		os.Exit(1)
	}
	merged.ResolveProfile()
	if *provider != "" {
		merged.Provider = *provider
	}
	if *model != "" {
		merged.Model = *model
	}

	client, _, settings, err := llm.NewClientFromLLMSettings(merged)
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建 LLM 客户端失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Provider: %s  Model: %s\n\n", settings.Provider, settings.Model)

	// 2. 注册工具
	registry := tool.NewRegistry()
	llmedit.RegisterEditTools(registry)

	// 3. 构建 system prompt(edit 使用指南)
	sysPrompt := buildEditSystemPrompt()
	runner := llmedit.NewRunner(client, registry, sysPrompt)
	runner.MaxTurns = 10
	runner.Model = settings.Model

	// 4. 加载任务
	tasks, err := llmedit.LoadTasks(*taskDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载任务失败: %v\n", err)
		os.Exit(1)
	}

	if !*all {
		filtered := filterTasks(tasks, *taskName)
		if len(filtered) == 0 {
			fmt.Fprintf(os.Stderr, "未匹配任务: %s\n", *taskName)
			os.Exit(1)
		}
		tasks = filtered
	}

	// 5. 执行
	ctx := context.Background()
	totalStart := time.Now()

	for _, task := range tasks {
		fmt.Printf("── %s (%s) ──\n", task.Name, task.Type)
		fmt.Printf("   指令: %s\n\n", task.Instruction)

		for i := 0; i < *repeat; i++ {
			if *repeat > 1 {
				fmt.Printf("  [run %d/%d] ", i+1, *repeat)
			}

			runStart := time.Now()
			result := runner.Run(ctx, task)
			elapsed := time.Since(runStart).Milliseconds()

			printResult(result, elapsed)
		}
	}

	fmt.Printf("\n总耗时: %s\n", time.Since(totalStart).Round(time.Millisecond))
}

func printResult(result *llmedit.RunResult, elapsedMs int64) {
	if result.Error != "" {
		fmt.Printf("✗ 错误: %s (%dms)\n", result.Error, elapsedMs)
		return
	}

	m := result.Metrics
	s := result.Score

	status := "✓"
	if !s.Passed {
		status = "✗"
	}
	fmt.Printf("%s 通过:%v 编译:%v 编辑距离:%d  turn:%d edit:%d parse:%.0f%% old:%.0f%% 盲编:%d tag误:%d 警告:%d 响应:%d (%dms)\n",
		status, s.Passed, s.CompileOK, s.EditDistance,
		m.TotalTurns, m.TotalEdits, m.ParseRate, m.OldSentinelRate,
		m.BlindEdits, m.TAGMismatches, m.WarningsCount, m.WarningsResponded,
		elapsedMs)

	if !s.Passed {
		for path, fs := range s.FileResults {
			if !fs.ExactMatch {
				fmt.Printf("    %s: 编辑距离=%d\n", path, fs.EditDistance)
				fmt.Printf("    got:  %q\n", truncate(fs.Got, 100))
				fmt.Printf("    want: %q\n", truncate(fs.Want, 100))
			}
		}
	}
}

func filterTasks(tasks []*llmedit.Task, name string) []*llmedit.Task {
	var result []*llmedit.Task
	for _, t := range tasks {
		if strings.Contains(t.Name, name) {
			result = append(result, t)
		}
	}
	return result
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func buildEditSystemPrompt() string {
	return `You are a coding agent. Use the read/edit/write tools to complete the task.

## Edit File (Hashline)

` + "`read` → TAG + line numbers → `edit` with TAG + line numbers. Never reproduce old code — only TAG, line numbers, and new content." + `

### Operations

SWAP N.=M      Replace lines N–M with body. Use %OLD ... %NEW sentinel.
DEL N.=M       Delete lines N–M.
INS.PRE N:     Insert before line N.  INS.POST N: after line N.
INS.HEAD:      Insert at file start.  INS.TAIL: at file end.

### Sentinel format (preferred)

SWAP 2.=2
%OLD
func main() {
%NEW
func run() {

### Same-file multi-section rule

When editing the same file with multiple sections in one patch, every SWAP MUST include %OLD — otherwise the entire patch is rejected.

### ⚠️ Warning handling

If the edit response contains "⚠️ SKIPPING VERIFICATION" — re-read the file and retry with %OLD.

Complete the task with minimal turns. Do not commit code.`
}
