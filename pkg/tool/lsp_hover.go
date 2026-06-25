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
      "description": "文件绝对路径"
    },
    "line": {
      "type": "integer",
      "description": "行号（0-based）"
    },
    "character": {
      "type": "integer",
      "description": "列号（0-based）"
    },
    "working_dir": {
      "type": "string",
      "description": "工作目录（可选）"
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
	return "获取光标位置符号的类型签名、文档注释（Markdown 格式）。用于快速查看 API 使用方式。"
}

func (t *LSPHover) Execute(ctx context.Context, p LSPHoverParams) (*ToolResult, error) {
	if LSPManager == nil {
		return toolError(ErrorClassRecoverable, ErrKindCommandNotFound,
			"LSP 未初始化", nil), nil
	}

	inst, err := LSPManager.GetOrCreate(p.FilePath)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindCommandNotFound,
			fmt.Sprintf("无法启动 LSP Server: %s", err.Error()), err), nil
	}

	if err := LSPManager.SyncFile(inst, p.FilePath); err != nil {
		return toolError(ErrorClassRecoverable, ErrKindCommandFailed,
			fmt.Sprintf("LSP 文件同步失败: %s", err.Error()), err), nil
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
		return &ToolResult{Content: "无悬浮信息"}, nil
	}

	return &ToolResult{Content: hover.Contents.Value}, nil
}
