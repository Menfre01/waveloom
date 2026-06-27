package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"waveloom/pkg/lsp"
)

var lspHoverSchema = json.RawMessage(`{
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

// LSPHoverParams 是 lsp_hover 工具的参数。
type LSPHoverParams struct {
	FilePath   string `json:"file_path"`
	Line       uint32 `json:"line"`
	Character  uint32 `json:"character"`
	WorkingDir string `json:"working_dir,omitempty"`
}

// LSPHover 获取光标位置符号的类型信息和文档。
type LSPHover struct{}

func (t *LSPHover) Name() string           { return "lsp_hover" }
func (t *LSPHover) Schema() json.RawMessage { return lspHoverSchema }
func (t *LSPHover) ConcurrentSafe() bool    { return true }

func (t *LSPHover) Description() string {
	return "Get the type signature and documentation (Markdown) for a symbol at the cursor position. Use for quickly viewing API usage."
}

func (t *LSPHover) Execute(ctx context.Context, p LSPHoverParams) (*ToolResult, error) {
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

	var hover lsp.Hover
	err = LSPManager.Call(ctx, inst, "textDocument/hover", lsp.HoverParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: lsp.PathToURI(p.FilePath)},
		Position:     lsp.Position{Line: p.Line, Character: p.Character},
	}, &hover)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindCommandFailed,
			fmt.Sprintf("hover 查询失败: %s", err.Error()), err), nil
	}

	if hover.Contents.Value == "" {
		return &ToolResult{Content: "No hover information"}, nil
	}

	return &ToolResult{Content: hover.Contents.Value}, nil
}
