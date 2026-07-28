// editbench — hashline 编辑模型基准测试工具
//
// 用法:
//   editbench -dir <数据集目录> [-filter <name>] [-json] [-v]
//
// 默认读取 pkg/hashline/eval/testdata/evals 下的全部 JSONL 用例,
// 批量执行 ParsePatch → ApplyPatch 完整路径,统计通过率。
// 用例通过 eval.Case + eval.RunCase 在内存 FS 上执行,不接触磁盘。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Menfre01/waveloom/pkg/hashline/eval"
)

type benchResult struct {
	Name    string  `json:"name"`
	Passed  bool    `json:"passed"`
	Elapsed float64 `json:"elapsed_ms"`
	Failure string  `json:"failure,omitempty"`
	Error   string  `json:"error,omitempty"`
}

func main() {
	dir := flag.String("dir", "pkg/hashline/eval/testdata/evals", "数据集目录")
	filter := flag.String("filter", "", "按用例名子串过滤")
	jsonOut := flag.Bool("json", false, "JSON 格式输出")
	verbose := flag.Bool("v", false, "显示每个 case 的详情(含 PASS)")
	flag.Parse()

	cases, err := eval.LoadDir(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载数据集失败: %v\n", err)
		os.Exit(1)
	}

	if *filter != "" {
		filtered := cases[:0]
		for _, c := range cases {
			if strings.Contains(c.Name, *filter) {
				filtered = append(filtered, c)
			}
		}
		cases = filtered
	}

	if len(cases) == 0 {
		fmt.Println("没有匹配的用例")
		os.Exit(0)
	}

	var passed, failed int
	results := make([]benchResult, 0, len(cases))

	for _, c := range cases {
		start := time.Now()
		r := eval.RunCase(c)
		elapsed := float64(time.Since(start).Microseconds()) / 1000.0

		res := benchResult{Name: c.Name, Passed: r.Passed, Elapsed: elapsed}
		if !r.Passed {
			res.Failure = strings.Join(r.Failures, "; ")
			if r.ParseErr != nil {
				res.Error = r.ParseErr.Error()
			}
			failed++
		} else {
			passed++
		}
		results = append(results, res)
	}

	if *jsonOut {
		outputJSON(results, passed, failed, *dir)
		return
	}

	outputText(results, passed, failed, *dir, *verbose)
}

func outputJSON(results []benchResult, passed, failed int, dir string) {
	summary := map[string]interface{}{
		"total":     len(results),
		"passed":    passed,
		"failed":    failed,
		"pass_rate": formatRate(passed, len(results)),
		"directory": dir,
		"results":   results,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(summary); err != nil {
		fmt.Fprintf(os.Stderr, "JSON 输出失败: %v\n", err)
		os.Exit(1)
	}
}

func outputText(results []benchResult, passed, failed int, dir string, verbose bool) {
	byFile := groupByFile(results)

	fmt.Println("editbench — hashline 编辑模型基准测试")
	fmt.Printf("数据集: %s\n", dir)
	fmt.Printf("用例数: %d\n\n", len(results))

	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)

	for _, f := range files {
		fmt.Printf("── %s ──\n", f)
		for _, r := range byFile[f] {
			mark := "✓"
			if !r.Passed {
				mark = "✗"
			}
			fmt.Printf("  %s %-50s %6.1fms", mark, r.Name, r.Elapsed)
			if !r.Passed {
				fmt.Printf("\n    %s", r.Failure)
			}
			fmt.Println()
		}
		fmt.Println()
	}

	fmt.Println(strings.Repeat("─", 60))
	fmt.Printf("Result: %d/%d passed (%s)\n", passed, len(results), formatRate(passed, len(results)))

	if failed > 0 && !verbose {
		fmt.Println("提示: 用 -v 查看失败详情, -json 导出完整报告")
	}
}

func groupByFile(results []benchResult) map[string][]benchResult {
	groups := make(map[string][]benchResult)
	for _, r := range results {
		group := r.Name
		if idx := strings.Index(r.Name, "-"); idx > 0 {
			group = r.Name[:idx]
		}
		groups[group] = append(groups[group], r)
	}
	return groups
}

func formatRate(passed, total int) string {
	if total == 0 {
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", float64(passed)/float64(total)*100)
}
