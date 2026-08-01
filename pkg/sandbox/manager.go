package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// ErrSandboxUnavailable 后端不可用(未选择/探测失败)。
var ErrSandboxUnavailable = errors.New("sandbox: backend unavailable")

// ============================================================================
// backend — 平台后端接口
// ============================================================================

// Backend 是沙箱平台后端(linux bwrap / darwin seatbelt / windows stub / 测试 fake)。
// 导出以便外部注入自定义后端(如 WSL2 桥接)与测试 fake。
type Backend interface {
	// Name 返回后端名(用于日志/测试)。
	Name() string
	// Probe 探测后端可用性(能力探测)。返回 nil 表示可用。
	Probe() error
	// Transform 将 (shellBin, args...) 改写为沙箱包装后的 argv。
	// 返回的 argv[0] 是沙箱二进制。
	Transform(shellBin string, args []string, cfg *Config, workspace string) ([]string, error)
}

// ============================================================================
// SandboxManager — 沙箱管理器
// ============================================================================

// SandboxManager 管理沙箱生命周期:平台分发(Select)→ argv 改写(Transform)→
// 逃逸判定(IsExcludedForTool)。后端可插拔,测试可注入 fake 后端。
type SandboxManager struct {
	cfg       *Config
	workspace string
	backend   Backend
}

// NewManager 创建沙箱管理器(尚未选择后端,Select() 前不可用)。
func NewManager(cfg *Config, workspace string) *SandboxManager {
	return &SandboxManager{cfg: cfg, workspace: workspace}
}

// Select 根据平台选择后端并探测可用性。
// 返回 error 表示后端不可用,调用方按 failIfUnavailable 决定降级或硬失败。
func (m *SandboxManager) Select() error {
	var b Backend
	switch runtime.GOOS {
	case "linux":
		b = newBwrapBackend()
	case "darwin":
		b = newSeatbeltBackend()
	case "windows":
		b = newWindowsStubBackend()
	default:
		return fmt.Errorf("sandbox: unsupported platform %q", runtime.GOOS)
	}
	if err := b.Probe(); err != nil {
		return err
	}
	m.backend = b
	return nil
}

// SetBackend 注入后端(测试或自定义后端使用)。
// 绕过 Select 的平台分发;生产路径仍走 Select。
func (m *SandboxManager) SetBackend(b Backend) {
	m.backend = b
}

// Available 返回后端是否已选择且可用。
func (m *SandboxManager) Available() bool {
	return m.backend != nil
}

// AllowUnsandboxed 返回是否允许沙箱外逃逸(allowUnsandboxedCommands 配置)。
// Shell 工具在沙箱内命令失败时据此提示模型可逃逸重试。
func (m *SandboxManager) AllowUnsandboxed() bool {
	return m.cfg.AllowUnsandboxed()
}

// Name 返回当前后端名(未选择时返回空串)。
func (m *SandboxManager) Name() string {
	if m.backend == nil {
		return ""
	}
	return m.backend.Name()
}

// Transform 将 (shellBin, args...) 改写为沙箱包装后的 argv。
// 后端不可用时返回 ErrSandboxUnavailable。
func (m *SandboxManager) Transform(shellBin string, args []string) ([]string, error) {
	if m.backend == nil {
		return nil, ErrSandboxUnavailable
	}
	return m.backend.Transform(shellBin, args, m.cfg, m.workspace)
}

// ExtraFiles 返回 Transform 中 --file 遮蔽引用所需的额外 fd
// (调用方附加到 exec.Cmd.ExtraFiles,编号从 3 开始)。
// 无 --file 遮蔽时返回 nil。
func (m *SandboxManager) ExtraFiles() []*os.File {
	if m.backend == nil {
		return nil
	}
	if b, ok := m.backend.(interface{ ExtraFiles() []*os.File }); ok {
		return b.ExtraFiles()
	}
	return nil
}

// Run 执行沙箱包装后的命令,返回退出码与合并输出。
// 仅供需要统一执行入口的调用方使用(Shell 工具自行 exec,不经过此方法)。
func (m *SandboxManager) Run(shellBin string, args []string) (int, string, error) {
	wrapped, err := m.Transform(shellBin, args)
	if err != nil {
		return -1, "", err
	}
	cmd := exec.Command(wrapped[0], wrapped[1:]...)
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return -1, string(out), err
		}
	}
	return exitCode, string(out), nil
}

// ============================================================================
// IsExcludedForTool — 工具级逃逸判定
// ============================================================================

// IsExcludedForTool 判定工具调用是否命中逃逸命令(不进沙箱)。
// 封装工具特定 input 解析:对 bash 工具提取 command 字段 + 逐条拆分匹配;
// 非 bash 工具返回 false。agentloop 依赖此方法,不耦合工具 input schema。
func (m *SandboxManager) IsExcludedForTool(toolName string, input json.RawMessage) (bool, error) {
	// bash_subagent 与 bash 同等对待(子代理 Shell 工具,二审 High-1 修复)
	if toolName != "bash" && toolName != "shell" && toolName != "bash_subagent" {
		return false, nil
	}
	var params struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return false, fmt.Errorf("sandbox: parse %s input: %w", toolName, err)
	}
	return m.IsExcluded(params.Command), nil
}

// IsExcluded 判定命令是否命中 excludedCommands。
// 先拆分复合命令(A && B && C → 逐条)再剥离 env 包装,逐条匹配。
func (m *SandboxManager) IsExcluded(cmd string) bool {
	if len(m.cfg.ExcludedCommands) == 0 {
		return false
	}
	for _, sub := range SplitCompoundCommand(cmd) {
		sub = StripEnvWrapper(sub)
		for _, pattern := range m.cfg.ExcludedCommands {
			if matchExcludedPattern(sub, pattern) {
				return true
			}
		}
	}
	return false
}

// matchExcludedPattern 匹配单个逃逸模式:
//   - 精确匹配整条命令
//   - 以 * 结尾 → 前缀匹配(如 "git push *")
//   - 含 * → glob 匹配(如 "docker *")
func matchExcludedPattern(cmd, pattern string) bool {
	if pattern == cmd {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(cmd, strings.TrimSuffix(pattern, "*"))
	}
	if strings.Contains(pattern, "*") {
		ok, _ := path.Match(pattern, cmd)
		return ok
	}
	return false
}

// ============================================================================
// SplitCompoundCommand / StripEnvWrapper — 命令预处理
// ============================================================================

// SplitCompoundCommand 基于 mvdan.cc/sh AST 将复合命令拆为逐条子命令。
// 拆分边界:&&、||、|、|&(BinaryCmd 链)。换行/分号分隔的顶层语句也拆分。
// 解析失败时退化返回整条命令(单元素切片)。
func SplitCompoundCommand(cmd string) []string {
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(cmd), "")
	if err != nil {
		return []string{cmd}
	}
	var out []string
	for _, stmt := range prog.Stmts {
		collectSplitStmt(stmt, &out)
	}
	if len(out) == 0 {
		return []string{cmd}
	}
	return out
}

// collectSplitStmt 递归收集语句:BinaryCmd(&&/||/| 链)拆左右,其余整体保留。
func collectSplitStmt(stmt *syntax.Stmt, out *[]string) {
	switch c := stmt.Cmd.(type) {
	case *syntax.BinaryCmd:
		collectSplitStmt(c.X, out)
		collectSplitStmt(c.Y, out)
	default:
		*out = append(*out, renderStmt(stmt))
	}
}

// renderStmt 用 syntax.Printer 还原语句文本(Node 接口无 String 方法)。
func renderStmt(stmt *syntax.Stmt) string {
	var sb strings.Builder
	pr := syntax.NewPrinter()
	_ = pr.Print(&sb, stmt)
	return sb.String()
}

// StripEnvWrapper 剥离 `env [flags] VAR=val cmd ...` 的 env 包装,返回剩余命令。
// 非 env 前缀命令原样返回。
func StripEnvWrapper(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) < 2 || fields[0] != "env" {
		return cmd
	}
	i := 1
	for i < len(fields) {
		f := fields[i]
		switch {
		case f == "-u" || f == "--unset" || f == "-C" || f == "--chdir":
			i += 2 // flag + 值
		case strings.HasPrefix(f, "-"):
			i++ // 无值 flag
		case strings.Contains(f, "="):
			i++ // VAR=val 赋值
		default:
			goto done
		}
	}
done:
	if i < len(fields) {
		return strings.Join(fields[i:], " ")
	}
	return cmd
}
