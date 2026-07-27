// Package lsp 提供 Language Server Protocol 3.17 诊断客户端实现。
//
// 仅包含诊断所需的协议子集：
//   - initialize / initialized
//   - textDocument/didOpen / didChange / didClose
//   - textDocument/publishDiagnostics (push)
//   - shutdown / exit
package lsp

import "encoding/json"

// ---------------------------------------------------------------------------
// 基础类型
// ---------------------------------------------------------------------------

// DocumentURI 是 LSP 中的文件标识符 (file:/// 格式)。
type DocumentURI string

// Position 表示文本中的位置 (0-based line/character)。
// LSP 规定 character 为 UTF-16 code units。
type Position struct {
	Line      uint32 `json:"line"`
	Character uint32 `json:"character"`
}

// Range 表示文本中的一个区域。
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// DiagnosticSeverity 诊断严重级别。
type DiagnosticSeverity uint32

const (
	SeverityError       DiagnosticSeverity = 1
	SeverityWarning     DiagnosticSeverity = 2
	SeverityInformation DiagnosticSeverity = 3
	SeverityHint        DiagnosticSeverity = 4
)

// Diagnostic 表示一个编译诊断。
type Diagnostic struct {
	Range    Range              `json:"range"`
	Severity DiagnosticSeverity `json:"severity,omitempty"`
	Code     string             `json:"code,omitempty"`
	Source   string             `json:"source,omitempty"`
	Message  string             `json:"message"`
}

// ---------------------------------------------------------------------------
// 文档相关
// ---------------------------------------------------------------------------

// TextDocumentIdentifier 通过 URI 标识文本文档。
type TextDocumentIdentifier struct {
	URI DocumentURI `json:"uri"`
}

// VersionedTextDocumentIdentifier 带版本号的文档标识。
type VersionedTextDocumentIdentifier struct {
	URI     DocumentURI `json:"uri"`
	Version int         `json:"version"`
}

// TextDocumentItem 表示打开的文档。
type TextDocumentItem struct {
	URI        DocumentURI `json:"uri"`
	LanguageID string      `json:"languageId"`
	Version    int         `json:"version"`
	Text       string      `json:"text"`
}

// TextDocumentContentChangeEvent 文档内容变更事件。
type TextDocumentContentChangeEvent struct {
	Text string `json:"text"`
}

// ---------------------------------------------------------------------------
// Initialize
// ---------------------------------------------------------------------------

// InitializeParams 是 initialize 请求的参数。
type InitializeParams struct {
	ProcessID    int                `json:"processId"`
	RootURI      string             `json:"rootUri,omitempty"`
	Capabilities ClientCapabilities `json:"capabilities"`
}

// ClientCapabilities 声明 Client 支持的能力。
type ClientCapabilities struct {
	TextDocument *TextDocumentClientCapabilities `json:"textDocument,omitempty"`
}

// TextDocumentClientCapabilities 文本文档相关能力。
type TextDocumentClientCapabilities struct {
	Diagnostic *DiagnosticClientCapabilities `json:"diagnostic,omitempty"`
}

// DiagnosticClientCapabilities 诊断相关能力。
type DiagnosticClientCapabilities struct {
	DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
}

// InitializeResult 是 initialize 请求的响应。
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
}

// ServerCapabilities Server 声明支持的能力。
type ServerCapabilities struct {
	TextDocumentSync   TextDocumentSyncKindOrOptions `json:"textDocumentSync,omitempty"`
	DiagnosticProvider *DiagnosticOptions            `json:"diagnosticProvider,omitempty"`
}

// TextDocumentSyncKindOrOptions 兼容 LSP 3.17 中 textDocumentSync 的两种形式:
//   - TextDocumentSyncKind (数字): 0=None, 1=Full, 2=Incremental
//   - TextDocumentSyncOptions (对象): {openClose, change, ...}
type TextDocumentSyncKindOrOptions struct {
	Kind    *int
	Options *TextDocumentSyncOptions
}

// TextDocumentSyncOptions 文档同步选项。
type TextDocumentSyncOptions struct {
	OpenClose bool `json:"openClose"`
	Change    int  `json:"change"` // 1 = Full
}

func (t *TextDocumentSyncKindOrOptions) UnmarshalJSON(data []byte) error {
	// 尝试解析为数字 (TextDocumentSyncKind)
	var kind int
	if json.Unmarshal(data, &kind) == nil {
		t.Kind = &kind
		return nil
	}
	// 尝试解析为对象 (TextDocumentSyncOptions)
	var opts TextDocumentSyncOptions
	if json.Unmarshal(data, &opts) == nil {
		t.Options = &opts
		return nil
	}
	return nil
}

// DiagnosticOptions 诊断选项。
type DiagnosticOptions struct {
	Identifier string `json:"identifier"`
}

// ---------------------------------------------------------------------------
// DidOpen / DidChange / DidClose
// ---------------------------------------------------------------------------

// DidOpenTextDocumentParams 是 textDocument/didOpen 通知的参数。
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// DidChangeTextDocumentParams 是 textDocument/didChange 通知的参数。
type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier   `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent   `json:"contentChanges"`
}

// DidCloseTextDocumentParams 是 textDocument/didClose 通知的参数。
type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// ---------------------------------------------------------------------------
// PublishDiagnostics (Server → Client 通知)
// ---------------------------------------------------------------------------

// PublishDiagnosticsParams 是 textDocument/publishDiagnostics 通知的参数。
type PublishDiagnosticsParams struct {
	URI         DocumentURI  `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// ---------------------------------------------------------------------------
// DocumentSymbol — textDocument/documentSymbol
// ---------------------------------------------------------------------------

// SymbolKind is the LSP SymbolKind enum.
type SymbolKind uint32

const (
	SymbolKindFile          SymbolKind = 1
	SymbolKindModule        SymbolKind = 2
	SymbolKindNamespace     SymbolKind = 3
	SymbolKindPackage       SymbolKind = 4
	SymbolKindClass         SymbolKind = 5
	SymbolKindMethod        SymbolKind = 6
	SymbolKindProperty      SymbolKind = 7
	SymbolKindField         SymbolKind = 8
	SymbolKindConstructor   SymbolKind = 9
	SymbolKindEnum          SymbolKind = 10
	SymbolKindInterface     SymbolKind = 11
	SymbolKindFunction      SymbolKind = 12
	SymbolKindVariable      SymbolKind = 13
	SymbolKindConstant      SymbolKind = 14
	SymbolKindString        SymbolKind = 15
	SymbolKindNumber        SymbolKind = 16
	SymbolKindBoolean       SymbolKind = 17
	SymbolKindArray         SymbolKind = 18
	SymbolKindObject        SymbolKind = 19
	SymbolKindKey           SymbolKind = 20
	SymbolKindNull          SymbolKind = 21
	SymbolKindEnumMember    SymbolKind = 22
	SymbolKindStruct        SymbolKind = 23
	SymbolKindEvent         SymbolKind = 24
	SymbolKindOperator      SymbolKind = 25
	SymbolKindTypeParameter SymbolKind = 26
)

// symbolKindLabels maps SymbolKind to human-readable labels.
var symbolKindLabels = map[SymbolKind]string{
	SymbolKindFile:          "file",
	SymbolKindModule:        "module",
	SymbolKindNamespace:     "namespace",
	SymbolKindPackage:       "package",
	SymbolKindClass:         "class",
	SymbolKindMethod:        "method",
	SymbolKindProperty:      "property",
	SymbolKindField:         "field",
	SymbolKindConstructor:   "constructor",
	SymbolKindEnum:          "enum",
	SymbolKindInterface:     "interface",
	SymbolKindFunction:      "function",
	SymbolKindVariable:      "variable",
	SymbolKindConstant:      "constant",
	SymbolKindString:        "string",
	SymbolKindNumber:        "number",
	SymbolKindBoolean:       "boolean",
	SymbolKindArray:         "array",
	SymbolKindObject:        "object",
	SymbolKindKey:           "key",
	SymbolKindNull:          "null",
	SymbolKindEnumMember:    "enumMember",
	SymbolKindStruct:        "struct",
	SymbolKindEvent:         "event",
	SymbolKindOperator:      "operator",
	SymbolKindTypeParameter: "typeParameter",
}

// SymbolKindLabel returns a human-readable label for the given SymbolKind.
func SymbolKindLabel(k SymbolKind) string {
	if label, ok := symbolKindLabels[k]; ok {
		return label
	}
	return "unknown"
}

// DocumentSymbol represents a symbol in a document (LSP DocumentSymbol).
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           SymbolKind       `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// DocumentSymbolParams is the parameter for textDocument/documentSymbol request.
type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// ---------------------------------------------------------------------------
// JSON-RPC 2.0 消息
// ---------------------------------------------------------------------------

// Request 表示 JSON-RPC 请求。
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response 表示 JSON-RPC 成功响应。
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

// ResponseError JSON-RPC 错误信息。
type ResponseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Notification 表示 JSON-RPC 通知 (无 id, 无响应)。
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}
