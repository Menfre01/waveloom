package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Menfre01/waveloom/pkg/agentloop"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/sandbox"
	"github.com/Menfre01/waveloom/pkg/session"
	"github.com/Menfre01/waveloom/pkg/todo"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// Instance 单个评测实例的宿主侧数据(直接复用 run.py prepare 产物)。
type Instance struct {
	ID      string
	InstDir string // results/<id>
	RepoDir string // results/<id>/repo(checkout 好的 base_commit)
	Prompt  string // prompt.txt(problem_statement + 模板)
	Repo    string // 数据集 repo(如 "psf/requests"),用于按 repo 匹配 testbed env
}

// LoadInstance 从 results/<id> 加载实例数据。
// 数据由 run.py prepare_instance 生成(prompt.txt / gold_patch.diff / repo/)。
func LoadInstance(resultsDir, id string) (*Instance, error) {
	instDir := filepath.Join(resultsDir, id)
	repoDir := filepath.Join(instDir, "repo")
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
		return nil, fmt.Errorf("实例 %s 的 repo 未准备(缺 %s):先运行 run.py prepare 或 --prepare", id, filepath.Join(repoDir, ".git"))
	}
	promptBytes, err := os.ReadFile(filepath.Join(instDir, "prompt.txt"))
	if err != nil {
		return nil, fmt.Errorf("读取 prompt.txt: %w", err)
	}
	repo := ""
	if data, err := os.ReadFile(filepath.Join(instDir, "instance.json")); err == nil {
		var meta struct {
			Repo string `json:"repo"`
		}
		if json.Unmarshal(data, &meta) == nil {
			repo = meta.Repo
		}
	}
	return &Instance{
		ID:      id,
		InstDir: instDir,
		RepoDir: repoDir,
		Prompt:  string(promptBytes),
		Repo:    repo,
	}, nil
}

// PrepareViaRunPy 回调 run.py prepare_instance 补齐实例数据(幂等)。
// 依赖评测 venv(python3 路径可配置),失败返回错误供调用方提示。
func PrepareViaRunPy(venvPython, swebenchDir, id string) error {
	py := venvPython
	if py == "" {
		py = "python3"
	}
	code := fmt.Sprintf("import sys; sys.path.insert(0, %q); import run; run.prepare_instance(%q)",
		swebenchDir, id)
	cmd := exec.Command(py, "-c", code)
	cmd.Dir = swebenchDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("prepare_instance(%s) 失败: %v\n%s", id, err, out)
	}
	slog.Info("prepare_instance 完成", "instance", id)
	return nil
}

// L0Runner 驱动 L0 行为层:宿主进程内 agent 执行。
type L0Runner struct {
	client   llm.Client
	settings *Settings
}

// NewL0Runner 构造 L0 runner。工具注册在 RunFast 内按实例完成
// (per-instance registry + 注入该实例的 sandboxMgr)。
func NewL0Runner(client llm.Client, settings *Settings) *L0Runner {
	return &L0Runner{client: client, settings: settings}
}

// RunFast 在宿主执行单个实例的 agent 循环,返回行为指标。
// 每个实例独立 registry(含独立 Shell 实例)/ guard / sandbox / ContextManager /
// 工作目录,可安全并行;不共享可变工具状态(避免 Shell.lastCommand 竞态)。
func (r *L0Runner) RunFast(ctx context.Context, inst *Instance) *BehaviorMetrics {
	start := time.Now()
	col := newCollector(inst.ID)

	// ── 重置 repo 到干净 base_commit(对齐 run.py prepare_instance:
	// 并行重跑/脏 repo 会污染 file_overlap 与 git status 指标)──
	resetRepo(inst.RepoDir)

	// ── 沙箱:必须开启(评测安全要求),bwrap/seatbelt 包装 bash ──
	// fail-closed:后端不可用且未显式 -no-sandbox 时,拒绝执行该实例
	// (agent 的 bash 会裸跑宿主,不可信输入不允许降级)。
	var sandboxMgr *sandbox.SandboxManager
	sm := sandbox.NewManager(&sandbox.Config{
		Enabled:           true,
		FailIfUnavailable: false,
	}, inst.RepoDir)
	if err := sm.Select(); err != nil {
		if !r.settings.NoSandbox {
			col.m.Error = fmt.Sprintf("沙箱不可用(%v);评测默认 fail-closed,如需裸执行请显式 -no-sandbox", err)
			col.m.ElapsedMs = time.Since(start).Milliseconds()
			return col.m
		}
		slog.Warn("L0 -no-sandbox:bash 裸执行(仅受 guard 工作目录约束)",
			"instance", inst.ID, "err", err)
	} else {
		sandboxMgr = sm
	}

	// ── per-instance 工具注册:Shell 注入本实例 sandboxMgr(沙箱才真正生效),
	// testbed env 激活注入(非容器评测,agent 可自测)──
	testbedPython, terr := resolveTestbed(inst, r.settings.TestbedPython)
	if terr != nil {
		col.m.Error = terr.Error()
		col.m.ElapsedMs = time.Since(start).Milliseconds()
		return col.m
	}
	registry := tool.NewRegistry()
	registerEvalTools(registry, sandboxMgr, testbedPython)

	// ── 权限守门人:autoAllow 二元决策 + 工作目录白名单 ──
	// deny 规则 / RiskHigh / PathDangerous 硬拦截在二元决策下保留(fail-closed):
	// agent 的 write/edit/bash 只能作用于 repo 工作目录内。
	guard := permission.NewGuard(
		permission.WithWorkingDirs(inst.RepoDir),
	)
	guard.EnableAutoAllow()

	// ── ContextManager:system prompt 由 PrepareRun 注入(对齐 runOneShot) ──
	cm := session.New(BuildSystemPrompt(registry, inst.RepoDir))
	// 会话落盘到实例 sessions/ 目录(供 finalize 生成 trace.jsonl → 归因)
	// agentloop 每轮自动 saveToPath(.json + .jsonl),与容器方案一致。
	sessionsDir := filepath.Join(inst.InstDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		slog.Warn("创建 sessions 目录失败,trace 不可用", "instance", inst.ID, "err", err)
	} else {
		cm.SetSessionPath(filepath.Join(sessionsDir, session.NewSessionID()+".json"))
	}
	messages, _ := cm.PrepareRun(inst.Prompt)

	loopCfg := agentloop.Config{
		MaxTurns:    r.settings.MaxTurns,
		Model:       r.settings.Model,
		Guard:       guard,
		SandboxMgr:  sandboxMgr,
		Compactor:   cm.Compactor(),
		ToolTimeout: r.settings.ToolTimeout,
		TodoState:   todo.NewTodoState(),
	}
	loop := agentloop.New(r.client, registry, loopCfg)
	eventCh := loop.Run(ctx, messages)
	var finalEv agentloop.LoopDone
	for ev := range eventCh {
		col.OnEvent(ev)
		if done, ok := ev.(agentloop.LoopDone); ok {
			finalEv = done
		}
	}
	// 会话落盘:提交完整消息历史(对齐 runOneShot 的 CompleteRun)后 Save。
	// 只 Save 不 CompleteRun 会落盘 PrepareRun 时的 2 条初始消息,
	// finalize 复制到 trace.jsonl 的将是空 trace(归因不可用)。
	if finalEv.Messages != nil {
		_ = cm.CompleteRun(finalEv.Messages, 0, 0, 0, 0, 0, 0,
			r.settings.Model, 0, string(finalEv.Reason))
	}
	cm.Save()

	// ── file_overlap:model_patch(git status)∩ gold_patch ──
	col.m.ModelFiles = modelFilesFromGit(inst.RepoDir)
	if gold, err := LoadGoldFiles(inst.InstDir); err == nil {
		col.m.GoldFiles = gold
		col.m.FileOverlap = intersect(col.m.ModelFiles, gold)
		if len(gold) > 0 {
			col.m.OverlapRatio = float64(len(col.m.FileOverlap)) / float64(len(gold))
		}
	} else {
		slog.Warn("读取 gold_patch.diff 失败", "instance", inst.ID, "err", err)
	}
	col.m.ElapsedMs = time.Since(start).Milliseconds()
	return col.m
}

// resolveTestbed 按实例 repo 解析 testbed venv python 路径。
// - testbed 为空 → 返回空(不注入 env,L0 行为层)
// - testbed 为 venv python 文件路径 → 原样使用(单 env 全实例)
// - testbed 为目录(env 根)→ 按 repo 匹配 <root>/<repo>-testbed/bin/python
//   (build_testbed.sh 的 per-repo 命名约定;repo 未知时无法匹配)
func resolveTestbed(inst *Instance, testbed string) (string, error) {
	if testbed == "" {
		return "", nil
	}
	if info, err := os.Stat(testbed); err == nil && !info.IsDir() {
		return testbed, nil // 直接是 venv python 文件
	}
	// 目录语义:per-repo env
	if inst.Repo == "" {
		return "", fmt.Errorf("实例 %s 无 repo 信息,无法匹配 per-repo testbed(可传 venv python 文件路径)", inst.ID)
	}
	slug := strings.ReplaceAll(inst.Repo, "/", "_")
	candidate := filepath.Join(testbed, slug+"-testbed", "bin", "python")
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("testbed env 不存在: %s(先运行 eval/swebench/build_testbed.sh %s)", candidate, inst.ID)
	}
	return candidate, nil
}

// resetRepo 将评测 repo 重置为干净 base_commit(git checkout -- . + 清理
// untracked),对齐 run.py prepare_instance 的幂等重置语义。
// 失败仅警告(防御:agent 前一轮可能破坏 git 状态,不阻断评测)。
func resetRepo(repoDir string) {
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
		return
	}
	for _, args := range [][]string{
		{"checkout", "-q", "--", "."},
		{"clean", "-fd", "-q"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			slog.Warn("resetRepo 失败", "repo", repoDir, "args", args, "err", err, "out", string(out))
		}
	}
}

// modelFilesFromGit 收集 agent 实际修改/新建的文件集。
// 用 git status --porcelain(含 untracked 新文件;git diff --name-only 不含)。
// 防御:repo 被 agent 破坏(git 缺失)时返回空集。
func modelFilesFromGit(repoDir string) []string {
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
		return nil
	}
	cmd := exec.Command("git", "-C", repoDir, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		slog.Warn("git status --porcelain 失败", "repo", repoDir, "err", err)
		return nil
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 4 { // "XY path" 至少 4 字符(XY + 空格 + 1 字符路径)
			continue
		}
		path := strings.TrimSpace(line[2:])
		// rename 条目格式 "R  old -> new",取新路径
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = path[idx+4:]
		}
		if path != "" {
			files = append(files, path)
		}
	}
	return files
}

// intersect 返回两个有序文件集(仓库相对路径)的交集。
func intersect(a, b []string) []string {
	setB := make(map[string]bool, len(b))
	for _, f := range b {
		setB[f] = true
	}
	var out []string
	for _, f := range a {
		if setB[f] {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// registerEvalTools 注册评测工具集(对齐 one-shot 裁剪:
// 不注册交互工具 ask/plan mode;subagent 不注册,避免父上下文污染)。
// sandboxMgr 注入 Shell 工具(沙箱包装 bash);nil = 该实例沙箱不可用。
// testbedPython 非空时 bash 注入 testbed env 激活(非容器评测,agent 可自测)。
func registerEvalTools(r tool.Registry, sandboxMgr *sandbox.SandboxManager, testbedPython string) {
	r.Register(tool.Wrap(&tool.ReadFile{}))
	r.Register(tool.Wrap(&tool.EditFile{}))
	r.Register(tool.Wrap(&tool.WriteFile{}))
	shell := &tool.Shell{AllowBg: true, SandboxMgr: sandboxMgr}
	if testbedPython != "" {
		r.Register(tool.Wrap(NewTestbedShell(shell, testbedPython)))
	} else {
		r.Register(tool.Wrap(shell))
	}
	r.Register(tool.Wrap(&tool.WebFetch{}))
	r.Register(tool.Wrap(&tool.WebSearch{}))
	r.Register(tool.Wrap(&tool.KillBackgroundTask{}))
	r.Register(tool.Wrap(&tool.TodoCreate{}))
	r.Register(tool.Wrap(&tool.TodoUpdate{}))
}
