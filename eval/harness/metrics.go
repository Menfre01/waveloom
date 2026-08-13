package harness

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Menfre01/waveloom/pkg/agentloop"
)

// BehaviorMetrics L0 行为层指标(与归因体系对齐)。
// 全部从 TurnEvent 事件流直接消费,无需事后解析 trace。
type BehaviorMetrics struct {
	InstanceID    string   `json:"instance_id"`
	FirstEditTurn int      `json:"first_edit_turn"` // 0 = 未发生任何 edit/write
	TotalTurns    int      `json:"total_turns"`
	Edits         int      `json:"edits"` // edit 工具调用数
	Writes        int      `json:"writes"`
	Reads         int      `json:"reads"`
	BashCalls     int      `json:"bash_calls"`
	BashErrors    int      `json:"bash_errors"` // bash 返回错误(含权限拒绝)
	DeniedCalls   int      `json:"denied_calls"` // 权限被拒的调用
	TestAttempts  int      `json:"test_attempts"` // pytest/tox 等测试命令尝试次数
	ToolSequence  []string `json:"tool_sequence"` // 工具名序列
	GoldFiles     []string `json:"gold_files"`     // gold_patch.diff 修改文件集
	ModelFiles    []string `json:"model_files"`    // agent 实际修改文件集(git status)
	FileOverlap   []string `json:"file_overlap"`   // ModelFiles ∩ GoldFiles
	OverlapRatio  float64  `json:"overlap_ratio"`  // |∩| / |gold|(0 当 gold 为空)
	TerminalReason string  `json:"terminal_reason,omitempty"`
	Error          string  `json:"error,omitempty"` // 非空 = 实例未执行(如沙箱不可用 fail-closed)
	ElapsedMs      int64   `json:"elapsed_ms"`
}

// metricsCollector 消费 TurnEvent 流,累计行为指标。
type metricsCollector struct {
	m *BehaviorMetrics
	// ToolCallID → Arguments(ToolCallStart 记录,ToolCallResult 消费)
	pendingArgs map[string]string
}

func newCollector(instanceID string) *metricsCollector {
	return &metricsCollector{
		m:           &BehaviorMetrics{InstanceID: instanceID},
		pendingArgs: make(map[string]string),
	}
}

// OnEvent 处理单个 TurnEvent(在 loop.Run 事件循环中调用)。
func (c *metricsCollector) OnEvent(ev agentloop.TurnEvent) {
	switch e := ev.(type) {
	case agentloop.ToolCallStart:
		c.recordTool(e.ToolCallName, e.Turn)
		c.pendingArgs[e.ToolCallID] = e.Arguments
	case agentloop.ToolCallResult:
		c.recordResult(e)
	case agentloop.LoopDone:
		c.m.TotalTurns = e.Turn
		c.m.TerminalReason = string(e.Reason)
	}
}

func (c *metricsCollector) recordTool(name string, turn int) {
	c.m.ToolSequence = append(c.m.ToolSequence, name)
	switch name {
	case "edit":
		c.m.Edits++
		if c.m.FirstEditTurn == 0 {
			c.m.FirstEditTurn = turn
		}
	case "write":
		c.m.Writes++
		if c.m.FirstEditTurn == 0 {
			c.m.FirstEditTurn = turn
		}
	case "read":
		c.m.Reads++
	case "bash":
		c.m.BashCalls++
	}
}

func (c *metricsCollector) recordResult(e agentloop.ToolCallResult) {
	if e.Denied {
		c.m.DeniedCalls++
	}
	if e.ToolCallName != "bash" {
		return
	}
	args := c.pendingArgs[e.ToolCallID]
	delete(c.pendingArgs, e.ToolCallID)
	if e.Error != "" {
		c.m.BashErrors++
		return
	}
	// 仅统计成功执行的测试尝试(bash 参数含 pytest/tox 调用)。
	if looksLikeTestCommand(extractBashCommand(args)) {
		c.m.TestAttempts++
	}
}

// extractBashCommand 从 bash 工具参数 JSON 中提取 command 字段
// (Arguments 为 {"command":"...","working_dir":...} 结构)。
func extractBashCommand(args string) string {
	var p struct {
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(args), &p) == nil {
		return p.Command
	}
	return args
}

// looksLikeTestCommand 判断 bash 命令是否包含测试命令调用。
// 覆盖常见形式:python -m pytest / python3.11 -m pytest / pytest / tox /
// make test / python -m unittest;支持多行命令(&&/;/|/换行分隔)。
var testCmdRE = regexp.MustCompile(
	`(^|[;&|\n]\s*)(python\d*(?:\.\d+)?\s+-m\s+)?(pytest|tox|unittest)\b|(^|[;&|\n]\s*)make\s+test\b`)

func looksLikeTestCommand(args string) bool {
	return testCmdRE.MatchString(args)
}

// ---- file_overlap ----

// goldPatchFilesRE 匹配 unified diff 的 "+++ b/<path>" 头。
var goldPatchFilesRE = regexp.MustCompile(`^\+\+\+\s+(?:[ab]/)?(.+)$`)

// goldPatchOldFileRE 匹配 unified diff 的 "--- a/<path>" 头(+++ /dev/null 时回退)。
var goldPatchOldFileRE = regexp.MustCompile(`^---\s+(?:[ab]/)?(.+)$`)

// GoldFilesFromPatch 从 gold_patch.diff 文本解析修改文件集。
// 格式统一为仓库相对路径(去掉 a/ b/ 前缀)。
// 删除文件的 diff 头为 "+++ /dev/null",回退取 "--- a/<path>" 行路径。
func GoldFilesFromPatch(patch string) []string {
	var files []string
	seen := make(map[string]bool)
	sc := bufio.NewScanner(strings.NewReader(patch))
	lastOld := ""
	for sc.Scan() {
		line := sc.Text()
		if m := goldPatchOldFileRE.FindStringSubmatch(line); m != nil {
			lastOld = strings.TrimSpace(m[1])
			continue
		}
		if !strings.HasPrefix(line, "+++") {
			continue
		}
		m := goldPatchFilesRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		p := strings.TrimSpace(m[1])
		if p == "/dev/null" {
			p = lastOld // 删除文件:仍视为修改了该文件
		}
		if p == "" {
			continue
		}
		if !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}
	return files
}

// LoadGoldFiles 读取实例目录下的 gold_patch.diff 并解析文件集。
func LoadGoldFiles(instDir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(instDir, "gold_patch.diff"))
	if err != nil {
		return nil, err
	}
	return GoldFilesFromPatch(string(data)), nil
}
