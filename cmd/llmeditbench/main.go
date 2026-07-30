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

	client, cfg, err := llm.NewClientFromLLMSettings(merged)
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建 LLM 客户端失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Provider: %s  Model: %s\n\n", cfg.Provider, cfg.Model)

	// 2. 注册工具
	registry := tool.NewRegistry()
	llmedit.RegisterEditTools(registry)

	// 3. 构建 system prompt(edit 使用指南)
	sysPrompt := buildEditSystemPrompt()
	runner := llmedit.NewRunner(client, registry, sysPrompt)
	runner.MaxTurns = 10
	runner.Model = cfg.Model

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
	fmt.Printf("%s 通过:%v 编译:%v 编辑距离:%d  turn:%d edit:%d parse:%.0f%% 盲编:%d 匹配失败:%d (%dms)\n",
		status, s.Passed, s.CompileOK, s.EditDistance,
		m.TotalTurns, m.TotalEdits, m.ParseRate,
		m.BlindEdits, m.HunkMatchFailures,
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
	return `You are a coding agent. Use read/edit/write tools to complete the task.

## Edit — Unified Diff Hunk Format

` + "`read`" + ` the file first, then ` + "`edit`" + ` with unified diff hunks.
Each hunk uses @@ header with context lines, - for removal, + for addition.

### Hunk format

@@ [optional context label]
 context line
-old line to remove
+new line to add
 context line

### Rules

- Always ` + "`read`" + ` before ` + "`edit`" + `/` + "`write`" + ` — the tool requires read state.
- For single-file edits: include multiple @@ hunks in one ` + "`edit`" + ` call.
- For multi-file edits: use ` + "`*** Update File: <path>`" + ` header before each file's hunks.
- Include enough context lines around each change for unique matching.
- If a hunk fails to apply: re-read the file, adjust context, retry.

Complete the task with minimal turns.`
}
