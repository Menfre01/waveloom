// Package lsp 提供 Language Server Protocol 3.17 客户端实现。
//
// 仅包含 Waveloom 所需的协议子集：
//   - initialize / initialized
//   - textDocument/didOpen / didClose
//   - textDocument/publishDiagnostics（push）
//   - textDocument/definition / references / hover
//   - shutdown / exit
package lsp

import "encoding/json"

// ---------------------------------------------------------------------------
// 基础类型
// ---------------------------------------------------------------------------

// DocumentURI 是 LSP 中的文件标识符（file:/// 格式）。
type DocumentURI string

// Position 表示文本中的位置（0-based line/character）。
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

// Location 表示文件中的位置。
type Location struct {
	URI   DocumentURI `json:"uri"`
	Range Range       `json:"range"`
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

// MarkupContent 表示 Markdown 格式的文档内容。
type MarkupContent struct {
	Kind  string `json:"kind"` // "markdown" 或 "plaintext"
	Value string `json:"value"`
}

// Hover 表示悬浮文档信息。
type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

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

// ---------------------------------------------------------------------------
// Initialize
// ---------------------------------------------------------------------------

// InitializeParams 是 initialize 请求的参数。
type InitializeParams struct {
	ProcessID  int    `json:"processId"`
	RootURI    string `json:"rootUri,omitempty"`
	Capabilities      ClientCapabilities `json:"capabilities"`
}

// ClientCapabilities 声明 Client 支持的能力。
type ClientCapabilities struct {
	TextDocument *TextDocumentClientCapabilities `json:"textDocument,omitempty"`
}

// TextDocumentClientCapabilities 文本文档相关能力。
type TextDocumentClientCapabilities struct {
	Diagnostic *DiagnosticClientCapabilities `json:"diagnostic,omitempty"`
	Definition *DefinitionClientCapabilities `json:"definition,omitempty"`
	References *ReferencesClientCapabilities `json:"references,omitempty"`
	Hover      *HoverClientCapabilities      `json:"hover,omitempty"`
}

// DiagnosticClientCapabilities 诊断相关能力。
type DiagnosticClientCapabilities struct {
	DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
}

// DefinitionClientCapabilities 定义跳转能力。
type DefinitionClientCapabilities struct {
	DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
}

// ReferencesClientCapabilities 引用查找能力。
type ReferencesClientCapabilities struct {
	DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
}

// HoverClientCapabilities 悬浮文档能力。
type HoverClientCapabilities struct {
	DynamicRegistration    bool `json:"dynamicRegistration,omitempty"`
	ContentFormat          []string `json:"contentFormat,omitempty"` // "markdown", "plaintext"
}

// InitializeResult 是 initialize 请求的响应。
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
}

// ServerCapabilities Server 声明支持的能力。
type ServerCapabilities struct {
	TextDocumentSync *TextDocumentSyncOptions `json:"textDocumentSync,omitempty"`
	DefinitionProvider bool                    `json:"definitionProvider,omitempty"`
	ReferencesProvider bool                    `json:"referencesProvider,omitempty"`
	HoverProvider      bool                    `json:"hoverProvider,omitempty"`
	DiagnosticProvider *DiagnosticOptions      `json:"diagnosticProvider,omitempty"`
}

// TextDocumentSyncOptions 文档同步选项。
type TextDocumentSyncOptions struct {
	OpenClose bool `json:"openClose"`
	Change    int  `json:"change"` // 1 = Full
}

// DiagnosticOptions 诊断选项。
type DiagnosticOptions struct {
	Identifier string `json:"identifier"`
}

// ---------------------------------------------------------------------------
// DidOpen / DidClose
// ---------------------------------------------------------------------------

// DidOpenTextDocumentParams 是 textDocument/didOpen 通知的参数。
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// DidCloseTextDocumentParams 是 textDocument/didClose 通知的参数。
type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// ---------------------------------------------------------------------------
// Definition / References / Hover
// ---------------------------------------------------------------------------

// DefinitionParams 是 textDocument/definition 请求的参数。
type DefinitionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// ReferencesParams 是 textDocument/references 请求的参数。
type ReferencesParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      ReferencesContext       `json:"context"`
}

// ReferencesContext 引用查找上下文。
type ReferencesContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// HoverParams 是 textDocument/hover 请求的参数。
type HoverParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// ---------------------------------------------------------------------------
// PublishDiagnostics（Server → Client 通知）
// ---------------------------------------------------------------------------

// PublishDiagnosticsParams 是 textDocument/publishDiagnostics 通知的参数。
type PublishDiagnosticsParams struct {
	URI         DocumentURI  `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
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

// Notification 表示 JSON-RPC 通知（无 id，无响应）。
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}
