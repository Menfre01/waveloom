package tool

import (
	"context"
	"encoding/json"
	"strings"
)

// ---------------------------------------------------------------------------
// AskUserQuestion — LLM 向用户发起选择题式交互决策
// ---------------------------------------------------------------------------

// QuestionOption 是选择题的单个选项。
type QuestionOption struct {
	Label       string `json:"label"`       // 显示文本，1-5 words
	Description string `json:"description"` // 选项解释
}

// Question 是单个选择题。
type Question struct {
	Question    string           `json:"question"`    // 完整问题，以 ? 结尾
	Header      string           `json:"header"`      // 简短标签，≤12 chars
	Options     []QuestionOption `json:"options"`     // 2-4 项，label 唯一
	MultiSelect bool             `json:"multiSelect"` // 是否多选，默认 false
}

// AskUserQuestionParams 是 ask_user_question 工具的参数。
type AskUserQuestionParams struct {
	Questions []Question `json:"questions"` // 1-4 个问题
}

// askUserQuestionSchema 是 ask_user_question 的 JSON Schema。
var askUserQuestionSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "questions": {
      "type": "array",
      "minItems": 1,
      "maxItems": 4,
      "items": {
        "type": "object",
        "properties": {
          "question": {
            "type": "string",
            "description": "The complete question to ask the user. Should be clear, specific, and end with a question mark."
          },
          "header": {
            "type": "string",
            "maxLength": 12,
            "description": "Very short label displayed as a chip/tag. Examples: 'Auth method', 'Library', 'Approach'."
          },
          "options": {
            "type": "array",
            "minItems": 2,
            "maxItems": 4,
            "items": {
              "type": "object",
              "properties": {
                "label": {
                  "type": "string",
                  "description": "The display text for this option (1-5 words). Append '(Recommended)' if this is the suggested choice."
                },
                "description": {
                  "type": "string",
                  "description": "Explanation of what this option means or what will happen if chosen."
                }
              },
              "required": ["label", "description"]
            }
          },
          "multiSelect": {
            "type": "boolean",
            "default": false,
            "description": "Set to true to allow multiple selections (for non-mutually-exclusive choices)."
          }
        },
        "required": ["question", "header", "options"]
      }
    }
  },
  "required": ["questions"]
}`)

// AskUserQuestion 实现 TypedTool[AskUserQuestionParams]。
// 实际的用户交互由 Agent Loop 通过 TurnEvent + reply channel 完成，
// 工具自身的 Execute 仅在校验后返回占位结果（Loop 层会替换为实际答案）。
type AskUserQuestion struct{}

func (t *AskUserQuestion) Name() string            { return "ask_user_question" }
func (t *AskUserQuestion) ConcurrentSafe() bool     { return false }
func (t *AskUserQuestion) RequiresUserInteraction() bool { return true }

func (t *AskUserQuestion) Description() string {
	return strings.TrimSpace(`
Ask the user one or more multiple-choice questions to gather preferences,
clarify ambiguity, or make decisions during execution. Use this tool when
you need to:

1. Gather user preferences or requirements
2. Clarify ambiguous instructions
3. Get decisions on implementation choices as you work
4. Offer choices to the user about what direction to take

Usage notes:
- Users will always be able to select "Other" to provide custom text input
- Use multiSelect: true to allow multiple answers for a question
- Put "(Recommended)" at the end of the label for the suggested option
- Question texts must be unique; option labels must be unique within each question

Do NOT use this tool to ask "is my plan ready?" or "should I proceed?" —
use exit_plan_mode for plan approval.
`)
}

func (t *AskUserQuestion) Schema() json.RawMessage { return askUserQuestionSchema }

// Execute 执行工具。
// 正常的用户交互流程由 Agent Loop 在 executeToolCalls 中拦截处理，
// 此方法仅在 Loop 未正确拦截时作为兜底返回。
func (t *AskUserQuestion) Execute(ctx context.Context, params AskUserQuestionParams) (*ToolResult, error) {
	// Validate params: question texts must be unique
	seen := make(map[string]bool, len(params.Questions))
	for _, q := range params.Questions {
		if seen[q.Question] {
			return &ToolResult{
				Error: &ToolError{
					Class:   ErrorClassRecoverable,
					Kind:    ErrKindInvalidArgs,
					Message: "question texts must be unique",
				},
			}, nil
		}
		seen[q.Question] = true

		// Validate options: labels must be unique within each question
		optSeen := make(map[string]bool, len(q.Options))
		for _, o := range q.Options {
			if optSeen[o.Label] {
				return &ToolResult{
					Error: &ToolError{
						Class:   ErrorClassRecoverable,
						Kind:    ErrKindInvalidArgs,
						Message: "option labels must be unique within each question",
					},
				}, nil
			}
			optSeen[o.Label] = true
		}
	}

	// If we reach here, the tool was called without Loop mediation (shouldn't happen).
	// Return a recoverable error so LLM can handle it gracefully.
	return &ToolResult{
		Error: &ToolError{
			Class:   ErrorClassRecoverable,
			Kind:    "user_declined",
			Message: "ask_user_question requires interactive TUI session",
		},
	}, nil
}
