// llmedit — Layer 3 LLM-in-the-loop 编辑可用性评估 CLI
//
// 用法:
//   go run ./cmd/llmedit/ -provider deepseek -model deepseek-v4-pro
//   go run ./cmd/llmedit/ -n 5          每个任务重复 5 次取平均
//
// API key 查找顺序:
//   1. -api-key 参数
//   2. {PROVIDER}_API_KEY 环境变量 (如 DEEPSEEK_API_KEY)
//   3. LLM_API_KEY 环境变量
//   4. 项目根目录 .env 文件中的对应变量
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Menfre01/waveloom/eval/llmedit"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/tool"
)

func apiKeyFromEnv(provider string) string {
	keys := []string{
		strings.ToUpper(provider) + "_API_KEY",
		provider + "_API_KEY",
		"LLM_API_KEY",
	}
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func main() {
	loadDotEnv()

	provider := flag.String("provider", "deepseek", "LLM provider")
	apiKeyFlag := flag.String("api-key", "", "API key")
	model := flag.String("model", "deepseek-v4-pro", "模型名称")
	baseURL := flag.String("base-url", "", "自定义 API 端点")
	taskDir := flag.String("dir", "eval/llmedit/testdata/tasks", "任务目录")
	taskFilter := flag.String("task", "", "任务名过滤(子串匹配,为空跑全部)")
	repeat := flag.Int("n", 1, "每个任务重复次数,>1 时输出汇总统计")
	verbose := flag.Bool("v", false, "详细输出每个 turn 的工具调用")
	flag.Parse()

	apiKey := *apiKeyFlag
	if apiKey == "" {
		apiKey = apiKeyFromEnv(*provider)
	}
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "请设置 -api-key 或对应的环境变量 (如 DEEPSEEK_API_KEY)")
		os.Exit(1)
	}

	cfg := llm.ClientConfig{
		Provider: llm.ProviderType(*provider),
		APIKey:   apiKey,
		Model:    *model,
		BaseURL:  *baseURL,
		Timeout:  5 * time.Minute,
	}
	client, err := llm.NewClient(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建 LLM 客户端失败: %v\n", err)
		os.Exit(1)
	}

	tasks, err := llmedit.LoadTasks(*taskDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载任务失败: %v\n", err)
		os.Exit(1)
	}

	registry := tool.NewRegistry()
	llmedit.RegisterEditTools(registry)
	runner := llmedit.NewRunner(client, registry, "")
	runner.Verbose = *verbose

	fmt.Printf("=== llmedit: Layer 3 编辑可用性评估 ===\n")
	fmt.Printf("Provider: %s  Model: %s\n", *provider, *model)
	fmt.Printf("任务数: %d\n", len(tasks))
	if *repeat > 1 {
		fmt.Printf("重复: %d 次\n", *repeat)
	}
	fmt.Println()

	ctx := context.Background()
	if *taskFilter != "" {
		var filtered []*llmedit.Task
		for _, t := range tasks {
			if strings.Contains(t.Name, *taskFilter) {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) == 0 {
			fmt.Fprintf(os.Stderr, "未匹配任务: %s\n", *taskFilter)
			os.Exit(1)
		}
		tasks = filtered
	}

	type agg struct {
		total, passed, edits, writes    int
		parseRate, blind, tagMis, warn  float64
		turns, dist                     float64
	}
	aggResults := make(map[string]*agg)
	for _, t := range tasks {
		aggResults[t.Name] = &agg{}
	}

	for run := 0; run < *repeat; run++ {
		if *repeat > 1 {
			fmt.Printf("\n── 第 %d/%d 轮 ──\n", run+1, *repeat)
		}
		br := runner.RunBatch(ctx, tasks)
		for i, res := range br.Results {
			label := fmt.Sprintf("[%d/%d] %s", i+1, len(tasks), res.Task.Name)
			if res.Error != "" {
				fmt.Printf("%s — ERROR: %s\n", label, res.Error)
				continue
			}
			m := res.Metrics
			status := "✗"
			if res.Score.Passed {
				status = "✓"
			}
			fmt.Printf("%s — %s  edits=%d writes=%d parse=%.0f%% blind=%d tagMis=%d warn=%d turns=%d dist=%d\n",
				label, status, m.TotalEdits, m.WriteCount, m.ParseRate,
				m.BlindEdits, m.TAGMismatches, m.WarningsCount,
				m.TotalTurns, m.EditDistance)
			if *verbose && res.VerboseOutput != "" {
				fmt.Println(res.VerboseOutput)
			}

			a := aggResults[res.Task.Name]
			a.total++
			if res.Score.Passed {
				a.passed++
			}
			a.edits += m.TotalEdits
			a.writes += m.WriteCount
			a.parseRate += m.ParseRate
			a.blind += float64(m.BlindEdits)
			a.tagMis += float64(m.TAGMismatches)
			a.warn += float64(m.WarningsCount)
			a.turns += float64(m.TotalTurns)
			a.dist += float64(m.EditDistance)
		}
	}

	if *repeat > 1 {
		fmt.Println()
		fmt.Println("═══════════════════════════════════════════════════════════════")
		fmt.Printf("  汇总统计 (每任务 N=%d)\n", *repeat)
		fmt.Println("═══════════════════════════════════════════════════════════════")
		fmt.Printf("%-22s %6s %6s %6s %7s %7s %6s %6s %6s\n",
			"任务", "通过率", "均edit", "均turn", "均dist", "均parse", "均盲编", "均tag误", "均warn")
		fmt.Println("─────────────────────────────────────────────────────────────")
		totalPassed, totalRuns := 0, 0
		for _, t := range tasks {
			a := aggResults[t.Name]
			n := float64(a.total)
			if n == 0 {
				continue
			}
			pct := float64(a.passed) / n * 100
			fmt.Printf("%-22s %5.0f%%  %5.1f  %5.1f  %6.1f  %6.0f%% %5.1f  %5.1f  %5.1f\n",
				t.Name, pct,
				float64(a.edits)/n, a.turns/n, a.dist/n,
				a.parseRate/n, a.blind/n, a.tagMis/n, a.warn/n)
			totalPassed += a.passed
			totalRuns += a.total
		}
		fmt.Println("─────────────────────────────────────────────────────────────")
		if totalRuns > 0 {
			fmt.Printf("%-22s %5.0f%%\n", "总计", float64(totalPassed)/float64(totalRuns)*100)
		}
		fmt.Println("═══════════════════════════════════════════════════════════════")
	}
}

// loadDotEnv 从项目根目录加载 .env 文件。
// 不覆盖已存在的环境变量。
func loadDotEnv() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}

	f, err := os.Open(filepath.Join(dir, ".env"))
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
}
