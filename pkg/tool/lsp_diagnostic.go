package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"waveloom/pkg/lsp"
)

var lspDiagnosticSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "file_path": {
      "type": "string",
      "description": "Absolute file path"
    },
    "working_dir": {
      "type": "string",
      "description": "Working directory (optional, for LSP server project context)"
    }
  },
  "required": ["file_path"]
}`)

// LSDiagnosticParams 是 lsp_diagnostic 工具的参数。
type LSDiagnosticParams struct {
	FilePath   string `json:"file_path"`
	WorkingDir string `json:"working_dir,omitempty"`
}

// LSDiagnostic 获取指定文件的 LSP 诊断信息。
type LSDiagnostic struct{}

func (t *LSDiagnostic) Name() string           { return "lsp_diagnostic" }
func (t *LSDiagnostic) Schema() json.RawMessage { return lspDiagnosticSchema }
func (t *LSDiagnostic) ConcurrentSafe() bool    { return true }

func (t *LSDiagnostic) Description() string {
	return "Get diagnostics (compile errors, warnings, lint hints) for a file. Returns results grouped by severity, including file, line, column, and message."
}

func (t *LSDiagnostic) Execute(ctx context.Context, p LSDiagnosticParams) (*ToolResult, error) {
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

	uri := lsp.PathToURI(p.FilePath)
	diags := LSPManager.Diagnostics(uri)

	return &ToolResult{Content: formatDiagnostics(diags)}, nil
}

func formatDiagnostics(diags []lsp.Diagnostic) string {
	if len(diags) == 0 {
		return "✓ No diagnostics"
	}

	var (
		errors   int
		warnings int
		infos    int
		hints    int
	)
	for _, d := range diags {
		switch d.Severity {
		case lsp.SeverityError:
			errors++
		case lsp.SeverityWarning:
			warnings++
		case lsp.SeverityInformation:
			infos++
		case lsp.SeverityHint:
			hints++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d diagnostics (%d errors, %d warnings, %d info, %d hints)\n\n",
		len(diags), errors, warnings, infos, hints)

	for _, d := range diags {
		fmt.Fprintf(&b, "L%d:%d: %s: %s\n",
			d.Range.Start.Line+1,
			d.Range.Start.Character+1,
			severityPrefix(d.Severity),
			d.Message,
		)
	}
	return strings.TrimSpace(b.String())
}

func severityPrefix(s lsp.DiagnosticSeverity) string {
	switch s {
	case lsp.SeverityError:
		return "error"
	case lsp.SeverityWarning:
		return "warning"
	case lsp.SeverityInformation:
		return "info"
	case lsp.SeverityHint:
		return "hint"
	default:
		return "unknown"
	}
}
