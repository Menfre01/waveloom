package tool

import (
	"context"
	"encoding/json"
	"strings"
)

// SkillLoadResult 是 SkillExecutor 加载 skill 后的结果。
type SkillLoadResult struct {
	Body    string // 渲染后的 body（变量已替换、!`cmd` 已执行、附属文件清单已追加）
	DirPath string // SKILL.md 所在目录
}

// SkillExecutor 加载并渲染 skill。
// skill 包实现此接口，tool 包通过接口消费，消除 tool → skill 的编译期依赖。
type SkillExecutor interface {
	Load(name, args string) (*SkillLoadResult, error)
}

// SkillParams 是 skill 工具的参数。
type SkillParams struct {
	Name      string `json:"name"`      // skill 名称
	Arguments string `json:"arguments"` // 传入 skill 的参数（可选）
}

// SkillTool 让 LLM 可以调用用户定义的 skill。
// 实现 TypedTool[SkillParams]。
type SkillTool struct {
	executor SkillExecutor
}

// NewSkillTool 构造 SkillTool。
func NewSkillTool(executor SkillExecutor) *SkillTool {
	return &SkillTool{executor: executor}
}

func (t *SkillTool) Name() string        { return "skill" }
func (t *SkillTool) Description() string {
	return "Invoke a user-defined skill. Use this when a task matches an available skill's description. Call with skill name and optional arguments."
}
func (t *SkillTool) ConcurrentSafe() bool { return false }

func (t *SkillTool) Schema() json.RawMessage {
	return jsonSkillSchema
}

func (t *SkillTool) Execute(ctx context.Context, p SkillParams) (*ToolResult, error) {
	loaded, err := t.executor.Load(p.Name, p.Arguments)
	if err != nil {
		msg := "Skill load failed: " + p.Name + " — " + err.Error()
		// 区分“skill 不存在”和“加载失败（如白名单拦截）”
		if strings.Contains(err.Error(), "skill not found") {
			msg = "Skill not found: " + p.Name
		}
		return &ToolResult{
			Error: &ToolError{
				Class:   ErrorClassRecoverable,
				Kind:    ErrKindNoResults,
				Message: msg,
				Cause:   err,
			},
		}, nil
	}

	return &ToolResult{
		Content: loaded.Body,
		Meta: ToolMeta{
			FilePath:  loaded.DirPath,
			LineCount: countLines(loaded.Body),
			ByteCount: len(loaded.Body),
		},
	}, nil
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := 1
	for _, ch := range s {
		if ch == '\n' {
			n++
		}
	}
	return n
}

var jsonSkillSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "name": {
      "type": "string",
      "description": "The skill name (e.g., 'deploy', 'summarize-changes')"
    },
    "arguments": {
      "type": "string",
      "description": "Optional arguments to pass to the skill"
    }
  },
  "required": ["name"]
}`)
