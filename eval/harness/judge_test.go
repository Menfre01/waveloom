package harness

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// makeTestbedVenv 创建假 venv(python + activate)。
func makeTestbedVenv(t *testing.T) string {
	t.Helper()
	venv := t.TempDir()
	bin := filepath.Join(venv, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "activate"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "python"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(bin, "python")
}

func TestHostJudge_BuildEvalScript(t *testing.T) {
	instDir := t.TempDir()
	repoDir := filepath.Join(instDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 模拟 run.py 生成的 eval_script(含 conda 激活与 /testbed)
	script := `#!/bin/bash
set -uxo pipefail
source /opt/miniconda3/bin/activate
conda activate testbed
cd /testbed
git status
python -m pytest tests/test_x.py
`
	if err := os.WriteFile(filepath.Join(instDir, "eval_script.sh"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	inst := &Instance{ID: "t-inst", InstDir: instDir, RepoDir: repoDir}
	judge := &HostJudge{Inst: inst, TestbedPython: makeTestbedVenv(t)}

	got, err := judge.buildHostEvalScript()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if strings.Contains(out, "/opt/miniconda3") {
		t.Errorf("conda activate 未替换: %s", out)
	}
	if strings.Contains(out, "conda activate") {
		t.Errorf("conda activate 未移除: %s", out)
	}
	if !strings.Contains(out, "source "+filepath.Join(filepath.Dir(judge.TestbedPython), "activate")) {
		t.Errorf("venv activate 未注入: %s", out)
	}
	if !strings.Contains(out, "cd "+repoDir) {
		t.Errorf("/testbed 未替换为 repo: %s", out)
	}
	if !strings.Contains(out, "pytest") {
		t.Errorf("测试命令丢失: %s", out)
	}
}

func TestHostJudge_BuildEvalScriptMissing(t *testing.T) {
	instDir := t.TempDir()
	inst := &Instance{ID: "t", InstDir: instDir, RepoDir: t.TempDir()}
	judge := &HostJudge{Inst: inst, TestbedPython: makeTestbedVenv(t)}
	if _, err := judge.buildHostEvalScript(); err == nil {
		t.Fatal("expected error for missing eval_script.sh")
	}
}

// TestHostJudge_RunMissingScript eval_script 缺失时 Run 报错。
func TestHostJudge_RunMissingScript(t *testing.T) {
	instDir := t.TempDir()
	inst := &Instance{ID: "t", InstDir: instDir, RepoDir: t.TempDir()}
	judge := &HostJudge{Inst: inst, TestbedPython: makeTestbedVenv(t)}
	if err := judge.Run(context.Background()); err == nil {
		t.Fatal("expected error for missing eval_script.sh")
	}
}

// TestHostJudge_RunWithFakeEvalScript 用假 eval_script 验证 Run 流程
// (bash 执行 + verdict 调用;verdict 用假 python 记录参数)。
func TestHostJudge_RunWithFakeEvalScript(t *testing.T) {
	// Run 在 Windows 上按运行时保护直接返回错误(宿主判定依赖 bash),
	// 本测试验证 bash 执行 + verdict 调用流程,Windows 无此语义,跳过。
	if runtime.GOOS == "windows" {
		t.Skip("宿主判定依赖 bash,Windows 不支持(与 Run 的运行时保护一致)")
	}
	instDir := t.TempDir()
	repoDir := t.TempDir()
	// 假 eval_script:只 echo,不依赖 testbed 真实包
	if err := os.WriteFile(filepath.Join(instDir, "eval_script.sh"),
		[]byte("#!/bin/bash\nsource /opt/miniconda3/bin/activate\nconda activate testbed\ncd /testbed\necho fake-test-output\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 假 venv
	testbed := makeTestbedVenv(t)
	// 假 verdict python:记录调用参数
	recorder := filepath.Join(t.TempDir(), "called.txt")
	fakePy := filepath.Join(t.TempDir(), "fake-python")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + recorder + "\nexit 0\n"
	if err := os.WriteFile(fakePy, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	inst := &Instance{ID: "t", InstDir: instDir, RepoDir: repoDir}
	judge := &HostJudge{Inst: inst, TestbedPython: testbed, VenvPython: fakePy, SwebenchDir: t.TempDir()}
	if err := judge.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// container-run.log 已生成(判定日志)
	logData, err := os.ReadFile(filepath.Join(instDir, "container-run.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "fake-test-output") {
		t.Fatalf("eval script output missing from log: %s", logData)
	}
	// verdict python 被调用(collect_patch + verdict)
	called, err := os.ReadFile(recorder)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(called), "collect_patch") || !strings.Contains(string(called), "verdict") {
		t.Fatalf("run.py verdict not invoked: %s", called)
	}
	if !strings.Contains(string(called), "finalize") {
		t.Fatalf("run.py finalize(trace/归因证据)未调用: %s", called)
	}
}
