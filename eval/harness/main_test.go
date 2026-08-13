package harness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// TestRunFast_Parallel 验证 runFast 并行调度 + JSON 汇总输出。
// 使用 mock client,实例为真实临时 git 仓库。
func TestRunFast_Parallel(t *testing.T) {
	resultsDir := t.TempDir()
	client := &mockClient{responses: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{
			ID: "tc1", Name: "write",
			Arguments: `{"file_path":"x","content":"y"}`,
		}}},
	}}

	// 构造 2 个实例
	var ids []string
	for _, id := range []string{"inst-a", "inst-b"} {
		repoDir := filepath.Join(resultsDir, id, "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repoDir, "f.py"), []byte("x=1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(resultsDir, id, "prompt.txt"), []byte("fix"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(resultsDir, id, "gold_patch.diff"),
			[]byte("+++ b/f.py\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// git init 使 repo 可 diff
		for _, args := range [][]string{{"init", "-q"}, {"add", "f.py"}} {
			if out, err := exec.Command("git", append([]string{"-C", repoDir}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		ids = append(ids, id)
	}

	cfg := &Settings{ResultsDir: resultsDir, MaxTurns: 5, ToolTimeout: 30 * time.Second, NoSandbox: true}
	err := runFast(context.Background(), client, ids, cfg, 2, "", "")
	if err != nil {
		t.Fatal(err)
	}
}

// TestRunFast_MissingInstance 实例未准备时报错。
func TestRunFast_MissingInstance(t *testing.T) {
	resultsDir := t.TempDir()
	cfg := &Settings{ResultsDir: resultsDir, MaxTurns: 5, ToolTimeout: 30 * time.Second, NoSandbox: true}
	err := runFast(context.Background(), &mockClient{}, []string{"ghost"}, cfg, 1, "", "")
	if err == nil {
		t.Fatal("expected error for missing instance")
	}
}

// TestRunFull_ArgAssembly runFull 委托 run.py,参数正确。
func TestRunFull_ArgAssembly(t *testing.T) {
	// 用可执行脚本伪造 python,记录 argv。
	scriptDir := t.TempDir()
	recorder := filepath.Join(scriptDir, "recorded.args")
	script := filepath.Join(scriptDir, "fake-python")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + recorder + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	swebenchDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(swebenchDir, "run.py"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := runFull(context.Background(), []string{"inst-1", "inst-2"}, script, swebenchDir, 3)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(recorder)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Fields(string(data))
	wantArgs := []string{
		filepath.Join(swebenchDir, "run.py"), "inst-1", "inst-2", "--parallel", "3",
	}
	if len(args) != len(wantArgs) {
		t.Fatalf("args=%v, want %v", args, wantArgs)
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Fatalf("args[%d]=%q, want %q (all: %v)", i, args[i], wantArgs[i], args)
		}
	}
}

// TestNewClient 验证 settings 加载:非法路径报错,合法路径成功。
func TestNewClient(t *testing.T) {
	if _, err := NewClient(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("expected error for missing settings")
	}

	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	content := `{
	  "llm": {
	    "provider": "deepseek",
	    "model": "deepseek-v4-flash",
	    "api_key": "sk-test"
	  }
	}`
	if err := os.WriteFile(settingsPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if client == nil {
		t.Fatal("client should not be nil")
	}
}

// TestRunFast_WithPrepare 缺 repo 时 --prepare 走 PrepareViaRunPy(失败路径)。
func TestRunFast_WithPrepare(t *testing.T) {
	resultsDir := t.TempDir()
	instDir := filepath.Join(resultsDir, "p-inst")
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &Settings{ResultsDir: resultsDir, MaxTurns: 5, ToolTimeout: 30 * time.Second, NoSandbox: true, Prepare: true}
	err := runFast(context.Background(), &mockClient{}, []string{"p-inst"}, cfg, 1,
		filepath.Join(t.TempDir(), "no-python"), t.TempDir())
	if err == nil {
		t.Fatal("expected error: prepare via missing python fails")
	}
}

// TestBuildSystemPrompt 验证 system prompt 包含 workspace 与工具指南。
func TestBuildSystemPrompt(t *testing.T) {
	r := tool.NewRegistry()
	registerEvalTools(r, nil, "")
	sp := BuildSystemPrompt(r, "/tmp/repo")
	if !strings.Contains(sp, "/tmp/repo") {
		t.Error("system prompt should contain workspace path")
	}
	if !strings.Contains(sp, "## Workspace") {
		t.Error("system prompt should contain Workspace section")
	}
	if !strings.Contains(sp, "## Read File") {
		t.Error("system prompt should contain tool prompts (read)")
	}
}

var _ = time.Second // 保留 time 导入占位

// TestRunFull_DefaultPython runFull 在未指定 venv-python 时用 python3(报错路径)。
func TestRunFull_DefaultPython(t *testing.T) {
	swebenchDir := t.TempDir()
	// run.py 不存在 → python3 执行报错
	err := runFull(context.Background(), []string{"inst-1"}, "", swebenchDir, 1)
	if err == nil {
		t.Fatal("expected error: run.py missing")
	}
}

// TestLoadInstance_MissingPrompt prompt.txt 缺失时报错。
func TestLoadInstance_MissingPrompt(t *testing.T) {
	resultsDir := t.TempDir()
	instDir := filepath.Join(resultsDir, "no-prompt")
	if err := os.MkdirAll(filepath.Join(instDir, "repo", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadInstance(resultsDir, "no-prompt"); err == nil {
		t.Fatal("expected error for missing prompt.txt")
	}
}

// TestRunFast_MissingGold 实例缺 gold_patch.diff 时指标仍产出(空 overlap)。
func TestRunFast_MissingGold(t *testing.T) {
	repoDir := makeGitRepo(t)
	instDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(instDir, "prompt.txt"), []byte("fix"), 0o644); err != nil {
		t.Fatal(err)
	}
	inst := &Instance{ID: "no-gold", InstDir: instDir, RepoDir: repoDir, Prompt: "fix"}
	runner := NewL0Runner(&mockClient{}, &Settings{MaxTurns: 5, ToolTimeout: 30 * time.Second, NoSandbox: true})
	m := runner.RunFast(context.Background(), inst)
	if m.FileOverlap != nil || len(m.GoldFiles) != 0 {
		t.Fatalf("gold missing should yield empty overlap: %+v", m)
	}
}

// TestRunCLI 验证 CLI 参数解析与退出码。
func TestRunCLI(t *testing.T) {
	// 缺 -instances → 退出码 2
	if code := RunCLI([]string{}); code != 2 {
		t.Errorf("empty args code=%d, want 2", code)
	}
	// 非法 flag → 2
	if code := RunCLI([]string{"-bogus"}); code != 2 {
		t.Errorf("bogus flag code=%d, want 2", code)
	}
	// full 模式 + 不存在 venv → 1
	code := RunCLI([]string{"-instances", "i1", "-full",
		"-venv-python", filepath.Join(t.TempDir(), "nope"),
		"-swebench-dir", t.TempDir()})
	if code != 1 {
		t.Errorf("full with bad venv code=%d, want 1", code)
	}
	// fast 模式 + 不存在 settings → 1
	code = RunCLI([]string{"-instances", "i1",
		"-settings", filepath.Join(t.TempDir(), "nope.json"),
		"-results-dir", t.TempDir()})
	if code != 1 {
		t.Errorf("fast with bad settings code=%d, want 1", code)
	}
}

// TestRunCLI_FastSuccess fast 模式成功路径(真实临时 settings + 实例)。
func TestRunCLI_FastSuccess(t *testing.T) {
	resultsDir := t.TempDir()
	id := "cli-ok"
	instDir := filepath.Join(resultsDir, id)
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 不创建 repo/.git → LoadInstance 报错路径(见注释)
	if err := os.WriteFile(filepath.Join(instDir, "prompt.txt"), []byte("fix"), 0o644); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"llm":{"provider":"deepseek","model":"m","api_key":"k"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// repo 非 git 仓库 → LoadInstance 报错路径,退出码 1
	code := RunCLI([]string{"-instances", id, "-settings", settingsPath, "-results-dir", resultsDir})
	if code != 1 {
		t.Errorf("non-git repo code=%d, want 1", code)
	}
}

// TestGoldFilesFromPatch_DevNullNoOld +++ /dev/null 且无 --- 行时跳过。
func TestGoldFilesFromPatch_DevNullNoOld(t *testing.T) {
	if got := GoldFilesFromPatch("+++ /dev/null\n"); len(got) != 0 {
		t.Fatalf("orphan /dev/null should be skipped, got %v", got)
	}
	// --- 行提供回退路径
	if got := GoldFilesFromPatch("--- a/del.py\n+++ /dev/null\n"); len(got) != 1 || got[0] != "del.py" {
		t.Fatalf("dev/null with old file should yield del.py, got %v", got)
	}
}

// TestModelFilesFromGit_NotARepo .git 存在但不是 git 仓库时返回空。
func TestModelFilesFromGit_NotARepo(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if files := modelFilesFromGit(dir); len(files) != 0 {
		t.Fatalf("non-repo should yield empty, got %v", files)
	}
}

// TestRunCLI_FullSuccess full 模式成功路径(有效 venv python + run.py)。
func TestRunCLI_FullSuccess(t *testing.T) {
	swebenchDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(swebenchDir, "run.py"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakePy := filepath.Join(t.TempDir(), "python")
	if err := os.WriteFile(fakePy, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	code := RunCLI([]string{"-instances", "i1", "-full", "-venv-python", fakePy, "-swebench-dir", swebenchDir})
	if code != 0 {
		t.Errorf("full success code=%d, want 0", code)
	}
}

// TestRunFullHost_MissingInstance 非容器权威评测缺 repo 时报错。
func TestRunFullHost_MissingInstance(t *testing.T) {
	resultsDir := t.TempDir()
	cfg := &Settings{
		ResultsDir:    resultsDir,
		SettingsPath:  filepath.Join(t.TempDir(), "settings.json"),
		MaxTurns:      5,
		ToolTimeout:   30 * time.Second,
		NoSandbox:     true,
		TestbedPython: filepath.Join(t.TempDir(), "venv", "bin", "python"),
	}
	// settings.json 不存在 → NewClient 报错
	err := runFullHost(context.Background(), []string{"ghost"}, cfg, "", t.TempDir(), 1)
	if err == nil {
		t.Fatal("expected error for missing settings")
	}
}

// TestRunFullHost_RepoMissing settings 有效但 repo 未准备时报错。
func TestRunFullHost_RepoMissing(t *testing.T) {
	resultsDir := t.TempDir()
	instDir := filepath.Join(resultsDir, "h-ghost")
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"llm":{"provider":"deepseek","model":"m","api_key":"k"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Settings{
		ResultsDir:    resultsDir,
		SettingsPath:  settingsPath,
		MaxTurns:      5,
		ToolTimeout:   30 * time.Second,
		NoSandbox:     true,
		TestbedPython: filepath.Join(t.TempDir(), "venv", "bin", "python"),
	}
	err := runFullHost(context.Background(), []string{"h-ghost"}, cfg, "", t.TempDir(), 1)
	if err == nil {
		t.Fatal("expected error for missing repo")
	}
}

// TestRunCLI_HostBranch -full + -testbed 走宿主分支(缺 settings → 1)。
func TestRunCLI_HostBranch(t *testing.T) {
	code := RunCLI([]string{"-instances", "i1", "-full", "-testbed", "/tmp/x/bin/python",
		"-settings", filepath.Join(t.TempDir(), "nope.json")})
	if code != 1 {
		t.Errorf("host branch with bad settings code=%d, want 1", code)
	}
}
