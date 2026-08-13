package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// mockClient 模拟 LLM:首个响应请求 write 工具,之后返回纯文本结束。
// mu 保护 callCount(并行实例共享 client 场景;go test -race 需要)。
type mockClient struct {
	mu        sync.Mutex
	responses []*llm.Response
	callCount int
}

func (m *mockClient) SendMessage(ctx context.Context, messages []llm.Message, tools []llm.ToolSpec) (*llm.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := m.callCount
	m.callCount++
	if idx < len(m.responses) {
		return m.responses[idx], nil
	}
	return &llm.Response{Content: "done"}, nil
}

func (m *mockClient) SendMessageStream(ctx context.Context, messages []llm.Message, tools []llm.ToolSpec) (<-chan llm.StreamingEvent, error) {
	ch := make(chan llm.StreamingEvent, 4)
	go func() {
		defer close(ch)
		resp, _ := m.SendMessage(ctx, messages, tools)
		if resp.Content != "" {
			ch <- llm.StreamingEvent{Delta: resp.Content}
		}
		// 终态事件必须携带 ToolCalls(agentloop 在 ev.Done 时取 ev.ToolCalls)。
		ch <- llm.StreamingEvent{ToolCalls: resp.ToolCalls, Done: true}
	}()
	return ch, nil
}

func (m *mockClient) ListModels(ctx context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (m *mockClient) ProviderName() string                                    { return "mock" }
func (m *mockClient) GetBalance(ctx context.Context) (*llm.BalanceInfo, error) { return nil, nil }
func (m *mockClient) SupportsBalance() bool                                    { return false }

// makeGitRepo 创建带一个已提交文件的临时 git 仓库,返回目录路径。
func makeGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.py"), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "a.py"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "base"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// makeInstance 构造完整实例目录:repo + prompt.txt + gold_patch.diff。
func makeInstance(t *testing.T, repoDir string) *Instance {
	t.Helper()
	instDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(instDir, "prompt.txt"),
		[]byte("fix a.py"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instDir, "gold_patch.diff"),
		[]byte("+++ b/a.py\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Instance{ID: "test-inst", InstDir: instDir, RepoDir: repoDir, Prompt: "fix a.py"}
}

func TestRunFast_EndToEnd(t *testing.T) {
	// 评测 guard 路径白名单依赖 POSIX 路径语义(Windows 短路径/大小写差异
	// 导致 write 被误 deny),评测运行环境为 Linux/macOS,Windows 仅保证编译。
	if runtime.GOOS == "windows" {
		t.Skip("评测基建(fast 模式 guard 路径白名单)仅支持 Linux/macOS")
	}
	repoDir := makeGitRepo(t)
	inst := makeInstance(t, repoDir)

	// 首个响应请求 write 工具改写 a.py,随后结束。
	client := &mockClient{responses: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{
			ID:   "tc1",
			Name: "write",
			Arguments: fmt.Sprintf(`{"file_path":"%s","content":"x = 2\n"}`,
				filepath.Join(repoDir, "a.py")),
		}}},
	}}

	runner := NewL0Runner(client, &Settings{MaxTurns: 5, ToolTimeout: 30 * time.Second, NoSandbox: true})

	m := runner.RunFast(context.Background(), inst)

	if m.FirstEditTurn != 1 {
		t.Errorf("FirstEditTurn=%d, want 1", m.FirstEditTurn)
	}
	if m.Writes != 1 || m.TotalTurns < 1 {
		t.Errorf("unexpected metrics: %+v", m)
	}
	// file_overlap:agent 修改了 gold 文件 a.py
	if len(m.FileOverlap) != 1 || m.FileOverlap[0] != "a.py" {
		t.Errorf("FileOverlap=%v, want [a.py]", m.FileOverlap)
	}
	if m.OverlapRatio != 1.0 {
		t.Errorf("OverlapRatio=%v, want 1.0", m.OverlapRatio)
	}
	// 文件确实被改写
	data, err := os.ReadFile(filepath.Join(repoDir, "a.py"))
	if err != nil || string(data) != "x = 2\n" {
		t.Errorf("a.py not modified: %q err=%v", data, err)
	}
}

func TestRunFast_NoEditMetrics(t *testing.T) {
	repoDir := makeGitRepo(t)
	inst := makeInstance(t, repoDir)
	client := &mockClient{} // 无工具调用,直接结束

	runner := NewL0Runner(client, &Settings{MaxTurns: 5, ToolTimeout: 30 * time.Second, NoSandbox: true})

	m := runner.RunFast(context.Background(), inst)
	if m.FirstEditTurn != 0 {
		t.Errorf("FirstEditTurn=%d, want 0 (no edit)", m.FirstEditTurn)
	}
	if len(m.FileOverlap) != 0 {
		t.Errorf("FileOverlap=%v, want empty", m.FileOverlap)
	}
}

func TestModelFilesFromGit(t *testing.T) {
	repoDir := makeGitRepo(t)
	// 修改文件后应命中
	if err := os.WriteFile(filepath.Join(repoDir, "a.py"), []byte("x = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := modelFilesFromGit(repoDir)
	if len(files) != 1 || files[0] != "a.py" {
		t.Fatalf("modelFilesFromGit=%v, want [a.py]", files)
	}

	// 无修改时为空
	clean := makeGitRepo(t)
	if files := modelFilesFromGit(clean); len(files) != 0 {
		t.Fatalf("clean repo should yield empty, got %v", files)
	}

	// repo 不存在时返回空(不 panic)
	if files := modelFilesFromGit(filepath.Join(t.TempDir(), "nonexistent")); len(files) != 0 {
		t.Fatalf("nonexistent repo should yield empty, got %v", files)
	}
}

func TestModelFilesFromGit_Untracked(t *testing.T) {
	// git status --porcelain 需包含新建文件(agent 新建 = gold 命中)
	repoDir := makeGitRepo(t)
	if err := os.WriteFile(filepath.Join(repoDir, "new.py"), []byte("y = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := modelFilesFromGit(repoDir)
	if len(files) != 1 || files[0] != "new.py" {
		t.Fatalf("untracked should be included: %v", files)
	}
}

func TestLoadInstance_Success(t *testing.T) {
	resultsDir := t.TempDir()
	instDir := filepath.Join(resultsDir, "swe-inst")
	if err := os.MkdirAll(filepath.Join(instDir, "repo", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instDir, "prompt.txt"), []byte("fix"), 0o644); err != nil {
		t.Fatal(err)
	}
	inst, err := LoadInstance(resultsDir, "swe-inst")
	if err != nil {
		t.Fatal(err)
	}
	if inst.ID != "swe-inst" || inst.Prompt != "fix" {
		t.Fatalf("unexpected instance: %+v", inst)
	}
	if inst.RepoDir != filepath.Join(instDir, "repo") {
		t.Fatalf("RepoDir=%s", inst.RepoDir)
	}
}

func TestPrepareViaRunPy_MissingPython(t *testing.T) {
	err := PrepareViaRunPy(filepath.Join(t.TempDir(), "no-such-python"),
		t.TempDir(), "inst")
	if err == nil {
		t.Fatal("expected error for missing python")
	}
}

// TestPrepareViaRunPy_Success 用可执行脚本伪造 python 验证成功路径与参数。
func TestPrepareViaRunPy_Success(t *testing.T) {
	// 伪造 python 为 #! 脚本,Windows 无法 exec 脚本文件(评测运行环境
	// 为 Linux/macOS,Windows 仅保证编译)。
	if runtime.GOOS == "windows" {
		t.Skip("评测基建(fake-python exec)仅支持 Linux/macOS")
	}
	scriptDir := t.TempDir()
	recorder := filepath.Join(scriptDir, "recorded.args")
	script := filepath.Join(scriptDir, "fake-python")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + recorder + "\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	swebenchDir := t.TempDir()
	if err := PrepareViaRunPy(script, swebenchDir, "inst-1"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(recorder)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "prepare_instance") {
		t.Fatalf("prepare_instance not invoked: %s", data)
	}
	if !strings.Contains(string(data), "inst-1") {
		t.Fatalf("instance id not passed: %s", data)
	}
}

func TestRegisterEvalTools(t *testing.T) {
	r := tool.NewRegistry()
	registerEvalTools(r, nil, "")
	for _, name := range []string{"read", "edit", "write", "bash", "web_fetch", "web_search", "kill_background_task", "todo_create", "todo_update"} {
		if _, ok := r.Get(name); !ok {
			t.Errorf("tool %s not registered", name)
		}
	}
}

func TestExtractBashCommand(t *testing.T) {
	if got := extractBashCommand(`{"command":"pytest -q"}`); got != "pytest -q" {
		t.Errorf("extractBashCommand=%q", got)
	}
	if got := extractBashCommand(`not-json`); got != "not-json" {
		t.Errorf("non-json should pass through, got %q", got)
	}
}

var _ = json.Marshal // 保留 encoding/json 导入占位

// TestRunFast_SandboxFailClosed 默认(fail-closed)沙箱不可用时拒绝执行。
// 沙箱探测依赖环境(本机 seatbelt 不可用),此处模拟不可用场景:
// 直接验证 NoSandbox=false 时返回 Error,不启动 agent 循环。
func TestRunFast_SandboxFailClosed(t *testing.T) {
	repoDir := makeGitRepo(t)
	inst := makeInstance(t, repoDir)
	client := &mockClient{}
	// 沙箱探测经 exec.LookPath(bwrap/seatbelt),清空 PATH 强制探测失败,
	// 不依赖主机环境(CI 装有 bubblewrap 时原生探测会成功导致假失败)。
	t.Setenv("PATH", "/nonexistent")
	// NoSandbox=false(默认)→ 沙箱探测失败 → Error 指标,不执行 agent
	runner := NewL0Runner(client, &Settings{MaxTurns: 5, ToolTimeout: 30 * time.Second})
	m := runner.RunFast(context.Background(), inst)
	if m.Error == "" {
		t.Fatal("expected fail-closed error when sandbox unavailable")
	}
	if m.TotalTurns != 0 {
		t.Fatalf("agent should not run when sandbox unavailable, turns=%d", m.TotalTurns)
	}
}

// TestRunFast_NoSandboxExplicit 显式 -no-sandbox 时降级执行(不报 Error)。
func TestRunFast_NoSandboxExplicit(t *testing.T) {
	repoDir := makeGitRepo(t)
	inst := makeInstance(t, repoDir)
	client := &mockClient{}
	runner := NewL0Runner(client, &Settings{MaxTurns: 5, ToolTimeout: 30 * time.Second, NoSandbox: true})
	m := runner.RunFast(context.Background(), inst)
	if m.Error != "" {
		t.Fatalf("no-sandbox should degrade without error, got: %s", m.Error)
	}
}

// TestResolveTestbed 验证 per-repo env 匹配逻辑。
func TestResolveTestbed(t *testing.T) {
	// 空 → 不注入
	got, err := resolveTestbed(&Instance{ID: "x", Repo: "psf/requests"}, "")
	if err != nil || got != "" {
		t.Fatalf("empty testbed should return empty, got %q err=%v", got, err)
	}
	// venv python 文件路径 → 原样
	py := makeTestbedVenv(t)
	got, err = resolveTestbed(&Instance{ID: "x", Repo: "psf/requests"}, py)
	if err != nil || got != py {
		t.Fatalf("file testbed should pass through, got %q err=%v", got, err)
	}
	// 目录 + repo 匹配:按 slug 找 <root>/<repo>-testbed/bin/python
	root := t.TempDir()
	envDir := filepath.Join(root, "psf_requests-testbed", "bin")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakePy := filepath.Join(envDir, "python")
	if err := os.WriteFile(fakePy, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = resolveTestbed(&Instance{ID: "x", Repo: "psf/requests"}, root)
	if err != nil || got != fakePy {
		t.Fatalf("repo dir match failed, got %q err=%v", got, err)
	}
	// 目录 + 无 repo 信息 → 报错
	_, err = resolveTestbed(&Instance{ID: "x"}, root)
	if err == nil {
		t.Fatal("expected error for missing repo info")
	}
	// 目录 + env 不存在 → 报错
	_, err = resolveTestbed(&Instance{ID: "x", Repo: "unknown/repo"}, root)
	if err == nil {
		t.Fatal("expected error for missing env")
	}
}
