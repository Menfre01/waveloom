package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"waveloom/pkg/lsp"
)

var lspDefinitionSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "file_path": {
      "type": "string",
      "description": "Absolute file path"
    },
    "line": {
      "type": "integer",
      "description": "Line number (0-based)"
    },
    "character": {
      "type": "integer",
      "description": "Column number (0-based)"
    },
    "working_dir": {
      "type": "string",
      "description": "Working directory (optional)"
    }
  },
  "required": ["file_path", "line", "character"]
}`)

// LSPDefinitionParams 是 lsp_definition 工具的参数。
type LSPDefinitionParams struct {
	FilePath   string `json:"file_path"`
	Line       uint32 `json:"line"`
	Character  uint32 `json:"character"`
	WorkingDir string `json:"working_dir,omitempty"`
}

// LSPDefinition 跳转到光标位置的符号定义。
type LSPDefinition struct{}

func (t *LSPDefinition) Name() string           { return "lsp_definition" }
func (t *LSPDefinition) Schema() json.RawMessage { return lspDefinitionSchema }
func (t *LSPDefinition) ConcurrentSafe() bool    { return true }

func (t *LSPDefinition) Description() string {
	return "Jump to the symbol definition at the cursor position. Returns file path, line, and column. Use for understanding third-party libraries, type definitions, and function signatures."
}

func (t *LSPDefinition) Execute(ctx context.Context, p LSPDefinitionParams) (*ToolResult, error) {
	if LSPManager == nil {
		return toolError(ErrorClassRecoverable, ErrKindCommandNotFound,
			"LSP not initialized", nil), nil
	}

	inst, err := LSPManager.GetOrCreate(p.FilePath)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindCommandNotFound,
			fmt.Sprintf("failed to start LSP server: %s", err.Error()), err), nil
	}

	if err := LSPManager.SyncFile(inst, p.FilePath); err != nil {
		return toolError(ErrorClassRecoverable, ErrKindCommandFailed,
			fmt.Sprintf("LSP file sync failed: %s", err.Error()), err), nil
	}

	var locations []lsp.Location
	err = LSPManager.Call(ctx, inst, "textDocument/definition", lsp.DefinitionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: lsp.PathToURI(p.FilePath)},
		Position:     lsp.Position{Line: p.Line, Character: p.Character},
	}, &locations)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindCommandFailed,
			fmt.Sprintf("definition 查询失败: %s", err.Error()), err), nil
	}

	if len(locations) == 0 {
		return &ToolResult{Content: "No definition found"}, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "找到 %d 个定义:\n\n", len(locations))
	for i, loc := range locations {
		fmt.Fprintf(&b, "%d. %s\n   L%d:%d - L%d:%d\n",
			i+1, loc.URI,
			loc.Range.Start.Line+1, loc.Range.Start.Character+1,
			loc.Range.End.Line+1, loc.Range.End.Character+1,
		)
	}
	return &ToolResult{Content: strings.TrimSpace(b.String())}, nil
}
