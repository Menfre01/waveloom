package tool

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
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
	var diffHunks []DiffHunk

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
			if dh := parseDiffHunk(r.Header, r.RawBody, r.File); dh != nil {
				diffHunks = append(diffHunks, *dh)
			}
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

	result := &ToolResult{Content: buf.String()}
	if len(diffHunks) > 0 {
		result.Meta.DiffHunks = diffHunks
		result.Meta.FilePath = diffHunks[0].FilePath
	}
	return result, nil
}

// parseDiffHunk parses a unified diff hunk header and body into a DiffHunk.
// Returns nil if parsing fails.
func parseDiffHunk(header, body, filePath string) *DiffHunk {
	if body == "" {
		return nil
	}
	oldStart, oldCount, newStart, newCount, heading := parseHunkHeader(header)

	// Non-standard header (e.g. @@ func name or bare @@): start line
	// numbering at 1 and infer counts from the body after parsing.
	inferFromBody := oldStart == 0 && newStart == 0
	if inferFromBody {
		oldStart, newStart = 1, 1
	}

	// Normalize \r\n → \n in body before parsing
	body = strings.ReplaceAll(body, "\r\n", "\n")

	var lines []DiffLine
	oldNum := oldStart
	newNum := newStart

	for _, line := range strings.Split(body, "\n") {
		if line == "" || line == "\r" {
			continue
		}
		switch {
		case strings.HasPrefix(line, " "):
			lines = append(lines, DiffLine{
				Kind:    DiffCtx,
				Content: line[1:],
				OldNum:  oldNum,
				NewNum:  newNum,
			})
			oldNum++
			newNum++
		case strings.HasPrefix(line, "-"):
			lines = append(lines, DiffLine{
				Kind:    DiffDel,
				Content: line[1:],
				OldNum:  oldNum,
			})
			oldNum++
		case strings.HasPrefix(line, "+"):
			lines = append(lines, DiffLine{
				Kind:    DiffAdd,
				Content: line[1:],
				NewNum:  newNum,
			})
			newNum++
		default:
			// No prefix — treat as context
			lines = append(lines, DiffLine{
				Kind:    DiffCtx,
				Content: line,
				OldNum:  oldNum,
				NewNum:  newNum,
			})
			oldNum++
			newNum++
		}
	}

	if len(lines) == 0 {
		return nil
	}

	if inferFromBody {
		oldCount = oldNum - 1
		newCount = newNum - 1
		if heading == "" {
			heading = strings.TrimSpace(strings.ReplaceAll(header, "@@", ""))
		}
	} else if oldCount == 0 && newCount == 0 {
		// Standard header with omitted counts (e.g. @@ -1 +1 @@)
		oldCount = oldNum - oldStart
		newCount = newNum - newStart
	}

	return &DiffHunk{
		FilePath: filePath,
		OldStart: oldStart,
		OldCount: oldCount,
		NewStart: newStart,
		NewCount: newCount,
		Heading:  heading,
		Lines:    lines,
	}
}

// parseHunkHeader parses "@@ -oldStart[,oldCount] +newStart[,newCount] @@ [heading]".
func parseHunkHeader(header string) (oldStart, oldCount, newStart, newCount int, heading string) {
	// Find the @@ markers
	parts := strings.SplitN(header, "@@", 3)
	if len(parts) < 3 {
		return 0, 0, 0, 0, ""
	}
	rangePart := strings.TrimSpace(parts[1])
	// rangePart looks like "-1,7 +1,7" or "-1 +1"
	spaceIdx := strings.Index(rangePart, " ")
	if spaceIdx < 0 {
		return 0, 0, 0, 0, ""
	}
	oldPart := strings.TrimPrefix(rangePart[:spaceIdx], "-")
	newPart := strings.TrimPrefix(rangePart[spaceIdx+1:], "+")

	oldStart, oldCount = parseRange(oldPart)
	newStart, newCount = parseRange(newPart)

	if len(parts) > 2 {
		heading = strings.TrimSpace(parts[2])
	}
	return
}

// parseRange parses "start" or "start,count".
func parseRange(s string) (start, count int) {
	commaIdx := strings.Index(s, ",")
	if commaIdx < 0 {
		start, _ = strconv.Atoi(s)
		count = 1
		return
	}
	start, _ = strconv.Atoi(s[:commaIdx])
	count, _ = strconv.Atoi(s[commaIdx+1:])
	if count == 0 {
		count = 1
	}
	return
}
