package harness

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// 与 run.py 对齐的默认值。
const (
	DefaultMaxTurns    = 25
	DefaultToolTimeout = 5 * time.Minute
)

// RunCLI 解析参数并执行,返回进程退出码(可单测)。
// CLI 入口在 cmd/harness(参照 cmd/llmedit 先例)。
//
// 用法:harness -instances id1,id2 [-parallel N] [-fast|-full]
//   - fast(L0 行为层):宿主进程内 agent,分钟级行为信号
//   - full(L1 权威层):委托 run.py 单容器流程(官方判定)
//   - full + -testbed <venv-python>:非容器评测(agent 宿主 + 宿主 venv 判定)
func RunCLI(args []string) int {
	fs := flag.NewFlagSet("harness", flag.ContinueOnError)
	instances := fs.String("instances", "", "实例 ID 列表,逗号分隔")
	parallel := fs.Int("parallel", 4, "并发实例数")
	resultsDir := fs.String("results-dir", "", "实例数据目录(默认 eval/swebench/results)")
	settingsPath := fs.String("settings", "", "LLM settings.json 路径(默认 eval/swebench/settings.json)")
	maxTurns := fs.Int("max-turns", DefaultMaxTurns, "agent 最大轮数")
	prepare := fs.Bool("prepare", false, "实例数据缺失时回调 run.py prepare_instance")
	full := fs.Bool("full", false, "权威层:委托 run.py 单容器流程;与 -testbed 同用则走宿主判定")
	model := fs.String("model", "", "覆盖 LLM 模型(空 = settings 默认;评测对齐用 deepseek-v4-flash)")
	noSandbox := fs.Bool("no-sandbox", false, "关闭沙箱 bash 裸执行(默认 fail-closed:沙箱不可用即拒绝)")
	testbed := fs.String("testbed", "", "testbed venv python 绝对路径(如 /tmp/pylint-testbed/bin/python);非空时 bash 注入 env 激活,agent 可自测(非容器评测)")
	judgeOnly := fs.Bool("judge-only", false, "只重跑宿主判定(复用已有 model_patch,跳过 agent)")
	venvPython := fs.String("venv-python", "", "评测 venv python 路径(--full/--prepare 用)")
	swebenchDir := fs.String("swebench-dir", "", "eval/swebench 目录(--full/--prepare 用)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *instances == "" {
		fmt.Fprintln(os.Stderr, "用法: harness -instances id1,id2 [-parallel N] [-fast|-full]")
		return 2
	}
	ids := splitIDs(*instances)

	// 默认路径:相对仓库根(harness 通常从仓库根运行)。
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	swebench := *swebenchDir
	if swebench == "" {
		swebench = filepath.Join(cwd, "eval", "swebench")
	}
	results := *resultsDir
	if results == "" {
		results = filepath.Join(swebench, "results")
	}
	settings := *settingsPath
	if settings == "" {
		settings = filepath.Join(swebench, "settings.json")
	}

	cfg := &Settings{
		ResultsDir:    results,
		SettingsPath:  settings,
		MaxTurns:      *maxTurns,
		Model:         *model,
		NoSandbox:     *noSandbox,
		TestbedPython: *testbed,
		ToolTimeout:   DefaultToolTimeout,
		Prepare:       *prepare,
		JudgeOnly:     *judgeOnly,
	}

	ctx := context.Background()
	if *full {
		if *testbed != "" {
			// 非容器评测:agent 宿主跑(-testbed 注入)+ 宿主 venv 判定
			if err := runFullHost(ctx, ids, cfg, *venvPython, swebench, *parallel); err != nil {
				fmt.Fprintln(os.Stderr, "host 模式失败:", err)
				return 1
			}
			return 0
		}
		if err := runFull(ctx, ids, *venvPython, swebench, *parallel); err != nil {
			fmt.Fprintln(os.Stderr, "full 模式失败:", err)
			return 1
		}
		return 0
	}
	client, err := NewClient(cfg.SettingsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载 settings 失败:", err)
		return 1
	}
	if err := runFast(ctx, client, ids, cfg, *parallel, *venvPython, swebench); err != nil {
		fmt.Fprintln(os.Stderr, "fast 模式失败:", err)
		return 1
	}
	return 0
}

func splitIDs(s string) []string {
	var out []string
	for _, id := range strings.Split(s, ",") {
		id = strings.TrimSpace(id)
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

// runFullHost 非容器权威评测:agent 宿主进程内跑(-testbed 注入,可自测),
// 修复后宿主 venv 跑官方 eval_script + get_eval_report 判定(verdict.json)。
func runFullHost(ctx context.Context, ids []string, cfg *Settings, venvPython, swebenchDir string, parallel int) error {
	client, err := NewClient(cfg.SettingsPath)
	if err != nil {
		return fmt.Errorf("加载 settings(%s): %w", cfg.SettingsPath, err)
	}
	runner := NewL0Runner(client, cfg)

	// 实例加载(--prepare 时补齐缺失数据)
	var instances []*Instance
	for _, id := range ids {
		inst, err := LoadInstance(cfg.ResultsDir, id)
		if err != nil && cfg.Prepare {
			if perr := PrepareViaRunPy(venvPython, swebenchDir, id); perr != nil {
				return fmt.Errorf("%v", perr)
			}
			inst, err = LoadInstance(cfg.ResultsDir, id)
		}
		if err != nil {
			return fmt.Errorf("%v(可用 -prepare 或先运行 run.py)", err)
		}
		instances = append(instances, inst)
	}

	// 阶段 1:agent 宿主执行(可并行;judge-only 时跳过)
	results := make([]*BehaviorMetrics, 0, len(instances))
	if !cfg.JudgeOnly {
		sem := make(chan struct{}, parallel)
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, inst := range instances {
			wg.Add(1)
			sem <- struct{}{}
			go func(inst *Instance) {
				defer wg.Done()
				defer func() { <-sem }()
				m := runner.RunFast(ctx, inst)
				mu.Lock()
				results = append(results, m)
				mu.Unlock()
				fmt.Printf("[host] %s: 首edit轮=%d turns=%d overlap=%.0f%% (%d/%d) %.0fs\n",
					inst.ID, m.FirstEditTurn, m.TotalTurns,
					m.OverlapRatio*100, len(m.FileOverlap), len(m.GoldFiles),
					float64(m.ElapsedMs)/1000)
			}(inst)
		}
		wg.Wait()
	} else {
		fmt.Println("[host] judge-only:跳过 agent,仅重跑宿主判定")
	}

	// 阶段 2:宿主 venv 判定(可并行)
	for _, inst := range instances {
		testbedPy, err := resolveTestbed(inst, cfg.TestbedPython)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[host] %s testbed 解析失败: %v\n", inst.ID, err)
			continue
		}
		judge := &HostJudge{
			Inst:          inst,
			TestbedPython: testbedPy,
			VenvPython:    venvPython,
			SwebenchDir:   swebenchDir,
		}
		if err := judge.Run(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "[host] %s 判定失败: %v\n", inst.ID, err)
		}
	}

	out := struct {
		Mode      string             `json:"mode"`
		Results   []*BehaviorMetrics `json:"results"`
		Generated string             `json:"generated_at"`
	}{Mode: "host", Results: results, Generated: time.Now().Format(time.RFC3339)}
	data, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(data))
	return nil
}

// runFast L0 行为层:并行在宿主进程内执行 agent。
// 工具注册按实例在 RunFast 内完成(per-instance registry,注入该实例 sandboxMgr)。
func runFast(ctx context.Context, client llm.Client, ids []string, cfg *Settings, parallel int, venvPython, swebenchDir string) error {
	if parallel <= 0 {
		parallel = 1
	}

	// 实例加载(--prepare 时补齐缺失数据)
	var instances []*Instance
	for _, id := range ids {
		inst, err := LoadInstance(cfg.ResultsDir, id)
		if err != nil && cfg.Prepare {
			if perr := PrepareViaRunPy(venvPython, swebenchDir, id); perr != nil {
				return fmt.Errorf("%v", perr)
			}
			inst, err = LoadInstance(cfg.ResultsDir, id)
		}
		if err != nil {
			return fmt.Errorf("%v(可用 -prepare 或先运行 run.py)", err)
		}
		instances = append(instances, inst)
	}

	runner := NewL0Runner(client, cfg)
	sem := make(chan struct{}, parallel)
	var mu sync.Mutex
	results := make([]*BehaviorMetrics, 0, len(instances))

	var wg sync.WaitGroup
	for _, inst := range instances {
		wg.Add(1)
		sem <- struct{}{}
		go func(inst *Instance) {
			defer wg.Done()
			defer func() { <-sem }()
			m := runner.RunFast(ctx, inst)
			mu.Lock()
			results = append(results, m)
			mu.Unlock()
			fmt.Printf("[fast] %s: 首edit轮=%d turns=%d overlap=%.0f%% (%d/%d) %.0fs\n",
				inst.ID, m.FirstEditTurn, m.TotalTurns,
				m.OverlapRatio*100, len(m.FileOverlap), len(m.GoldFiles),
				float64(m.ElapsedMs)/1000)
		}(inst)
	}
	wg.Wait()

	// 输出 JSON 汇总(供脚本/对比分析消费)
	out := struct {
		Mode      string             `json:"mode"`
		Results   []*BehaviorMetrics `json:"results"`
		Generated string             `json:"generated_at"`
	}{Mode: "fast", Results: results, Generated: time.Now().Format(time.RFC3339)}
	data, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(data))
	return nil
}

// runFull L1 权威层:委托 run.py 单容器流程
// (docker 命令组装与 verdict/finalize 均在 Python 侧,避免重复实现官方判定)。
func runFull(ctx context.Context, ids []string, venvPython, swebenchDir string, parallel int) error {
	py := venvPython
	if py == "" {
		py = "python3"
	}
	args := append([]string{filepath.Join(swebenchDir, "run.py")}, ids...)
	args = append(args, "--parallel", fmt.Sprintf("%d", parallel))
	cmd := exec.CommandContext(ctx, py, args...)
	cmd.Dir = swebenchDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
