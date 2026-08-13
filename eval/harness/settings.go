package harness

import (
	"fmt"
	"time"

	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/prompt"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// Settings 评测运行配置,由 CLI 解析填充。
type Settings struct {
	// ResultsDir 实例数据目录(eval/swebench/results)。
	ResultsDir string
	// SettingsPath LLM settings.json 路径(评测专用:deepseek + flash + 128k)。
	SettingsPath string
	// MaxTurns agent 最大轮数(与 run.py MAX_TURNS 对齐,默认 25)。
	MaxTurns int
	// Model 覆盖 LLM client 默认模型(空 = client 默认,即 settings 的 model)。
	// run.py 评测用 deepseek-v4-flash,L0 对齐时应显式传入。
	Model string
	// NoSandbox 显式关闭沙箱(bash 裸执行)。
	// 默认 false = fail-closed:沙箱不可用时拒绝执行该实例(评测安全要求);
	// 仅在受控环境(容器内 / Linux bwrap 宿主)评估降级影响时使用。
	NoSandbox bool
	// TestbedPython testbed venv 的 python 绝对路径(如 /tmp/pylint-testbed/bin/python)
	// 或 env 根目录(如 /tmp,按 repo slug 匹配 <root>/<repo>_<repo>-testbed/bin/python)。
	// 非空时 bash 工具命令前注入 "source <venv>/bin/activate &&",agent 获得
	// 与官方 testbed 一致的自测环境(非容器评测核心)。
	TestbedPython string
	// ToolTimeout 单工具执行超时(默认 5min)。
	ToolTimeout time.Duration
	// Prepare 实例数据缺失时回调 run.py prepare_instance 补齐。
	Prepare bool
	// JudgeOnly 只重跑宿主判定(复用已有 model_patch,跳过 agent)。
	// 用于 agent 产物有效但判定环境修复后的快速重判定。
	JudgeOnly bool
}

// NewClient 从评测 settings.json 构造共享 LLM Client。
// 与 run.py 一致:单个 client 供全部实例并发使用(实测 DeepSeek 2500 并发非瓶颈)。
func NewClient(path string) (llm.Client, error) {
	return llm.NewClientFromSettings(path)
}

// BuildSystemPrompt 组装 L0 system prompt(对齐产品路径:
// prompt.Default + Workspace 节 + 工具使用指南,由 ContextManager 注入)。
//
// 与 llmedit.buildSystemPrompt 同模式;与 cmd/waveloom 主入口的差异:
// 不注入环境探测节(宿主工具链与 L1 testbed 环境不同,注入反而误导 agent)。
func BuildSystemPrompt(registry tool.Registry, repoDir string) string {
	sp := prompt.Default
	sp += fmt.Sprintf(`
## Workspace

Current working directory: %s
All file paths are resolved relative to this directory unless a working_dir is specified.
`, repoDir)
	if tp := registry.FormatToolPrompts(); tp != "" {
		sp += "\n\n" + tp
	}
	return sp
}
