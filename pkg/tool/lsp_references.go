package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Menfre01/waveloom/pkg/lsp"
)

var lspReferencesSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "file_path": {
      "type": "string",
      "description": "File path (absolute, or relative to working_dir / workspace root). Must be an existing source code file, not a directory. Not for .md, .txt, .json, .yaml, or other non-code files."
    },
    "line": {
      "type": "integer",
      "description": "Line number (0-based)"
    },
    "character": {
      "type": "integer",
      "description": "Column number (0-based)"
    },
    "include_declaration": {
      "type": "boolean",
      "description": "Include the definition location (default: true)",
      "default": true
    },
    "working_dir": {
      "type": "string",
      "description": "Working directory (optional)"
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
type LSPReferences struct {
	lspProvider *LSPProvider
}

// NewLSPReferences 创建一个依赖注入的 LSPReferences 工具。
func NewLSPReferences(provider *LSPProvider) *LSPReferences {
	return &LSPReferences{lspProvider: provider}
}

func (t *LSPReferences) Name() string           { return "lsp_references" }
func (t *LSPReferences) Schema() json.RawMessage { return lspReferencesSchema }
func (t *LSPReferences) ConcurrentSafe() bool    { return true }

func (t *LSPReferences) Description() string {
	return "Find all references to a symbol (including its definition). Returns a list of file paths, lines, and columns. " +
		"Use for tracing dependencies and impact analysis. " +
		"NOTE: line and character are 0-based (first line = 0, first column = 0)."
}

func (t *LSPReferences) Execute(ctx context.Context, p LSPReferencesParams) (*ToolResult, error) {
	includeDecl := true
	if p.IncludeDeclaration != nil {
		includeDecl = *p.IncludeDeclaration
	}
	mgr := t.lspManager()
	if mgr == nil {
		return toolError(ErrorClassRecoverable, ErrKindCommandNotFound,
			"LSP not initialized", nil), nil
	}

	inst, err := mgr.GetOrCreate(p.FilePath)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindCommandNotFound,
			fmt.Sprintf("failed to start LSP server: %s", err.Error()), err), nil
	}

	if err := mgr.SyncFile(inst, p.FilePath); err != nil {
		return toolError(ErrorClassRecoverable, ErrKindCommandFailed,
			fmt.Sprintf("LSP file sync failed: %s", err.Error()), err), nil
	}

	var locations []lsp.Location
	err = mgr.Call(ctx, inst, "textDocument/references", lsp.ReferencesParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: lsp.PathToURI(p.FilePath)},
		Position:     lsp.Position{Line: p.Line, Character: p.Character},
		Context:      lsp.ReferencesContext{IncludeDeclaration: includeDecl},
	}, &locations)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindCommandFailed,
			fmt.Sprintf("references 查询失败: %s", err.Error()), err), nil
	}

	if len(locations) == 0 {
		return &ToolResult{Content: "No references found"}, nil
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

// lspManager 返回注入的 LSP Manager。
func (t *LSPReferences) lspManager() *lsp.Manager {
	return t.lspProvider.Manager
}
