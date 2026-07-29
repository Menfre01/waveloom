package tool

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

)

//go:embed edit_file_prompt.md
var editFilePrompt string

// ---------------------------------------------------------------------------
// EditFile — diff hunk edit tool
// ---------------------------------------------------------------------------

type EditFileParams struct {
	FilePath string `json:"file_path"`
	Hunk     string `json:"hunk"`
}

type EditFile struct{}

func (t *EditFile) Name() string        { return "edit" }
func (t *EditFile) Description() string {
	return "Edit files using unified diff hunks. Supports multi-file, multi-hunk patches."
}
func (t *EditFile) Prompt() string       { return editFilePrompt }
func (t *EditFile) ConcurrentSafe() bool { return false }

var editFileSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "file_path": {
      "type": "string",
      "description": "Primary file path. For multi-file patches, use *** Update File: headers in the hunk body."
    },
    "hunk": {
      "type": "string",
      "description": "Unified diff hunk(s). Format: @@ header\\n context\\n-old\\n+new\\n context. For multi-file, wrap each file section with *** Update File: <path>."
    }
  },
  "required": ["file_path", "hunk"]
}`)

func (t *EditFile) Schema() json.RawMessage { return editFileSchema }

func (t *EditFile) Execute(ctx context.Context, p EditFileParams) (*ToolResult, error) {
	if p.Hunk == "" {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			"hunk is required", nil), nil
	}

	results, err := ApplyHunk(p.FilePath, p.Hunk, ReadStateFromContext(ctx))
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("hunk error: %v", err), err), nil
	}

	if len(results) == 0 {
		return &ToolResult{Content: "✓ no changes needed"}, nil
	}

	var succeeded, failed int
	var buf strings.Builder

	for _, r := range results {
		if r.Error != "" {
			failed++
			fmt.Fprintf(&buf, "✗ %s: @@ %s — %s\n", r.File, r.Header, r.Error)
			if len(r.OldLines) > 0 {
				buf.WriteString("  pattern:\n")
				for _, l := range r.OldLines {
					fmt.Fprintf(&buf, "   - %s\n", l)
				}
				for _, l := range r.NewLines {
					fmt.Fprintf(&buf, "   + %s\n", l)
				}
			}
			if r.FileSnippet != "" {
				buf.WriteString("  file content:\n")
				buf.WriteString(r.FileSnippet)
			}
			if r.ClosestMatch != "" {
				buf.WriteString(r.ClosestMatch)
			}
			if r.CharDiff != "" {
				buf.WriteString(r.CharDiff)
			}
		} else {
			succeeded++
			fmt.Fprintf(&buf, "✓ %s: @@ %s — applied at line %d\n", r.File, r.Header, r.Line)
		}
	}

	total := succeeded + failed
	fmt.Fprintf(&buf, "\n%d/%d hunks succeeded", succeeded, total)
	if failed > 0 {
		fmt.Fprintf(&buf, " — re-read failed files and retry")
	}
	buf.WriteByte('\n')

	if succeeded == 0 && failed > 0 {
		return &ToolResult{
			Content: buf.String(),
			Error: &ToolError{
				Class:   ErrorClassRecoverable,
				Kind:    ErrKindInvalidArgs,
				Message: fmt.Sprintf("all %d hunks failed", failed),
			},
		}, nil
	}

	return &ToolResult{Content: buf.String()}, nil
}
