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
      "description": "文件绝对路径"
    },
    "working_dir": {
      "type": "string",
      "description": "工作目录（可选，用于 LSP Server 项目上下文）"
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
	return "获取指定文件的诊断信息（编译错误、警告、lint 提示）。返回按严重级别分组的结果，包含文件、行号、列号、消息。"
}

func (t *LSDiagnostic) Execute(ctx context.Context, p LSDiagnosticParams) (*ToolResult, error) {
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

	uri := lsp.PathToURI(p.FilePath)
	diags := LSPManager.Diagnostics(uri)

	return &ToolResult{Content: formatDiagnostics(diags)}, nil
}

func formatDiagnostics(diags []lsp.Diagnostic) string {
	if len(diags) == 0 {
		return "✓ 无诊断信息"
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
	fmt.Fprintf(&b, "诊断结果：%d 条 (%d 错误, %d 警告, %d 信息, %d 提示)\n\n",
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
