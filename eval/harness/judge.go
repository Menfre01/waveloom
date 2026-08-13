package harness

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// HostJudge 宿主判定:agent 修复完成后,在 testbed venv 内执行官方
// eval_script 生成测试日志,再复用 run.py 的 collect_patch + verdict
// (官方 get_eval_report)写 verdict.json —— 非容器评测闭环的最后一步。
//
// 与容器方案的一致性:判定逻辑(get_eval_report / status_map 回退)完全复用
// Python 侧,Go 只做环境转换(conda activate → venv source activate)。
type HostJudge struct {
	Inst          *Instance
	TestbedPython string // testbed venv python(agent 自测与判定共用)
	VenvPython    string // 评测 venv python(含 swebench 包;可等于 TestbedPython 若已装)
	SwebenchDir   string // run.py 所在目录(sys.path 注入)
}

// Run 执行宿主判定:
//  1. eval_script.sh → 宿主版(替换 conda 激活与 /testbed 路径)
//  2. repo 内执行 → container-run.log(与 run.py verdict 读取路径一致)
//  3. 复用 run.py collect_patch + verdict → verdict.json
func (j *HostJudge) Run(ctx context.Context) error {
	script, err := j.buildHostEvalScript()
	if err != nil {
		return fmt.Errorf("构建宿主 eval_script: %w", err)
	}
	logPath := filepath.Join(j.Inst.InstDir, "container-run.log")

	// ── 2. 执行宿主版 eval_script ──
	cmd := exec.CommandContext(ctx, "bash", script)
	cmd.Dir = j.Inst.RepoDir
	logf, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("创建判定日志: %w", err)
	}
	defer logf.Close()
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Run(); err != nil {
		// eval_script 可能因测试失败非零退出(属正常判定结果),不视为错误
		slog.Info("宿主 eval_script 非零退出(判定结果待 verdict)", "instance", j.Inst.ID, "exit", err)
	}

	// ── 3. 复用 run.py collect_patch + verdict(官方 get_eval_report)──
	py := j.VenvPython
	if py == "" {
		py = j.TestbedPython
	}
	code := fmt.Sprintf(
		"import sys; sys.path.insert(0, %q); import run; run.collect_patch(%q); run.verdict(%q); from finalize import finalize; finalize(%q)",
		j.SwebenchDir, j.Inst.ID, j.Inst.ID, j.Inst.ID)
	c := exec.CommandContext(ctx, py, "-c", code)
	c.Dir = j.SwebenchDir
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("verdict 判定失败: %v\n%s", err, out)
	}
	slog.Info("宿主判定完成", "instance", j.Inst.ID)
	return nil
}

// buildHostEvalScript 读取实例 eval_script.sh,生成宿主版:
//   - "source /opt/miniconda3/bin/activate" → "source <venv>/bin/activate"
//   - "conda activate testbed" 删除(venv 无 conda)
//   - "cd /testbed" → "cd <repo_dir>"
//
// 返回脚本路径(写入实例目录,复用 run.py 生成的 eval_script.sh 为模板)。
func (j *HostJudge) buildHostEvalScript() (string, error) {
	src, err := os.ReadFile(filepath.Join(j.Inst.InstDir, "eval_script.sh"))
	if err != nil {
		return "", fmt.Errorf("读取 eval_script.sh: %w", err)
	}
	activate := fmt.Sprintf("source %s", filepath.Join(filepath.Dir(j.TestbedPython), "activate"))
	lines := strings.Split(string(src), "\n")
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "source /opt/miniconda3/bin/activate"):
			out = append(out, activate)
		case strings.HasPrefix(trimmed, "conda activate"):
			// venv 无 conda,跳过
		case strings.HasPrefix(trimmed, "cd /testbed"):
			out = append(out, "cd "+j.Inst.RepoDir)
		default:
			out = append(out, line)
		}
	}
	script := filepath.Join(j.Inst.InstDir, "eval_script_host.sh")
	if err := os.WriteFile(script, []byte(strings.Join(out, "\n")), 0o755); err != nil {
		return "", err
	}
	return script, nil
}
