package tool

import (
	"context"
	"encoding/json"

	"github.com/Menfre01/waveloom/pkg/skill"
)

// SkillParams 是 skill 工具的参数。
type SkillParams struct {
	Name      string `json:"name"`      // skill 名称
	Arguments string `json:"arguments"` // 传入 skill 的参数（可选）
}

// SkillTool 让 LLM 可以调用用户定义的 skill。
// 实现 TypedTool[SkillParams]。
type SkillTool struct {
	loader *skill.Loader
}

// NewSkillTool 构造 SkillTool。
func NewSkillTool(loader *skill.Loader) *SkillTool {
	return &SkillTool{loader: loader}
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
	loaded, err := t.loader.Load(p.Name, p.Arguments)
	if err != nil {
		return &ToolResult{
			Error: &ToolError{
				Class:   ErrorClassRecoverable,
				Kind:    ErrKindNoResults,
				Message: "Skill not found: " + p.Name,
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
