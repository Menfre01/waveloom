package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"waveloom/pkg/lsp"
)

var lspReferencesSchema = json.RawMessage(`{
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
    "include_declaration": {
      "type": "boolean",
      "description": "是否包含定义位置（默认 true）",
      "default": true
    },
    "working_dir": {
      "type": "string",
      "description": "工作目录（可选）"
    }
  },
  "required": ["file_path", "line", "character"]
}`)

// LSPReferencesParams 是 lsp_references 工具的参数。
type LSPReferencesParams struct {
	FilePath           string `json:"file_path"`
	Line               uint32 `json:"line"`
	Character          uint32 `json:"character"`
	IncludeDeclaration *bool  `json:"include_declaration,omitempty"`
	WorkingDir         string `json:"working_dir,omitempty"`
}

// LSPReferences 查找符号的所有引用。
type LSPReferences struct{}

func (t *LSPReferences) Name() string           { return "lsp_references" }
func (t *LSPReferences) Schema() json.RawMessage { return lspReferencesSchema }
func (t *LSPReferences) ConcurrentSafe() bool    { return true }

func (t *LSPReferences) Description() string {
	return "查找符号的所有引用位置（包括定义）。返回文件路径、行号、列号列表。用于追踪依赖、影响范围分析。"
}

func (t *LSPReferences) Execute(ctx context.Context, p LSPReferencesParams) (*ToolResult, error) {
	includeDecl := true
	if p.IncludeDeclaration != nil {
		includeDecl = *p.IncludeDeclaration
	}
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

	var locations []lsp.Location
	err = LSPManager.Call(inst, "textDocument/references", lsp.ReferencesParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: lsp.PathToURI(p.FilePath)},
		Position:     lsp.Position{Line: p.Line, Character: p.Character},
		Context:      lsp.ReferencesContext{IncludeDeclaration: includeDecl},
	}, &locations)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindCommandFailed,
			fmt.Sprintf("references 查询失败: %s", err.Error()), err), nil
	}

	if len(locations) == 0 {
		return &ToolResult{Content: "未找到引用"}, nil
	}

	// 限制输出最多 100 条
	maxShow := len(locations)
	if maxShow > 100 {
		maxShow = 100
	}

	var b strings.Builder
	fmt.Fprintf(&b, "找到 %d 个引用", len(locations))
	if len(locations) > 100 {
		fmt.Fprintf(&b, "（仅显示前 100 条）")
	}
	b.WriteString(":\n\n")

	for i, loc := range locations[:maxShow] {
		fmt.Fprintf(&b, "%d. %s L%d:%d\n",
			i+1, loc.URI,
			loc.Range.Start.Line+1, loc.Range.Start.Character+1,
		)
	}
	return &ToolResult{Content: strings.TrimSpace(b.String())}, nil
}
