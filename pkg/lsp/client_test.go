package lsp

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// readMessage 单元测试
// ---------------------------------------------------------------------------

func TestReadMessage(t *testing.T) {
	input := "Content-Length: 24\r\n\r\n{\"jsonrpc\":\"2.0\",\"id\":1}"
	msg, err := readMessage(bufio.NewReader(strings.NewReader(input)))
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if msg.ID == nil || *msg.ID != 1 {
		t.Errorf("id = %v, want 1", msg.ID)
	}
	if msg.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want \"2.0\"", msg.JSONRPC)
	}
}

func TestReadMessageNotification(t *testing.T) {
	input := "Content-Length: 60\r\n\r\n{\"jsonrpc\":\"2.0\",\"method\":\"textDocument/publishDiagnostics\"}"
	msg, err := readMessage(bufio.NewReader(strings.NewReader(input)))
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if msg.ID != nil {
		t.Errorf("id = %v, want nil (notification)", msg.ID)
	}
	if msg.Method != "textDocument/publishDiagnostics" {
		t.Errorf("method = %q, want publishDiagnostics", msg.Method)
	}
}

func TestReadMessageMissingHeader(t *testing.T) {
	input := "{\"jsonrpc\":\"2.0\"}\r\n"
	_, err := readMessage(bufio.NewReader(strings.NewReader(input)))
	if err == nil {
		t.Fatal("expected error for missing Content-Length")
	}
}

func TestReadMessageIncompleteBody(t *testing.T) {
	input := "Content-Length: 100\r\n\r\nshort"
	_, err := readMessage(bufio.NewReader(strings.NewReader(input)))
	if err == nil {
		t.Fatal("expected error for incomplete body")
	}
}

// ---------------------------------------------------------------------------
// 协议类型 JSON 序列化测试
// ---------------------------------------------------------------------------

func TestPositionMarshal(t *testing.T) {
	pos := Position{Line: 10, Character: 5}
	data, err := json.Marshal(pos)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var pos2 Position
	if err := json.Unmarshal(data, &pos2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pos2.Line != 10 || pos2.Character != 5 {
		t.Errorf("roundtrip = %+v, want {Line:10 Character:5}", pos2)
	}
}

func TestDiagnosticMarshal(t *testing.T) {
	d := Diagnostic{
		Range: Range{
			Start: Position{Line: 5, Character: 0},
			End:   Position{Line: 5, Character: 10},
		},
		Severity: SeverityError,
		Message:  "expected ';', found ')'",
	}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var d2 Diagnostic
	if err := json.Unmarshal(data, &d2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d2.Message != d.Message {
		t.Errorf("message = %q, want %q", d2.Message, d.Message)
	}
	if d2.Severity != SeverityError {
		t.Errorf("severity = %d, want %d", d2.Severity, SeverityError)
	}
}

func TestLocationMarshal(t *testing.T) {
	loc := Location{
		URI:   "file:///home/user/main.go",
		Range: Range{Start: Position{1, 0}, End: Position{1, 15}},
	}
	data, _ := json.Marshal(loc)
	if !strings.Contains(string(data), "file:///home/user/main.go") {
		t.Errorf("json missing URI: %s", data)
	}
}

func TestRequestMarshal(t *testing.T) {
	req := Request{
		JSONRPC: "2.0",
		ID:      42,
		Method:  "textDocument/definition",
		Params:  json.RawMessage(`{"key":"value"}`),
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// 确认 id 是数字不是字符串
	if !strings.Contains(string(data), `"id":42`) {
		t.Errorf("id not a number: %s", data)
	}
}

func TestNotificationMarshal(t *testing.T) {
	ntf := Notification{
		JSONRPC: "2.0",
		Method:  "textDocument/didOpen",
		Params:  json.RawMessage(`{"textDocument":{}}`),
	}
	data, err := json.Marshal(ntf)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// 通知不应包含 id 字段
	if strings.Contains(string(data), `"id"`) {
		t.Errorf("notification contains id: %s", data)
	}
}

// ---------------------------------------------------------------------------
// 与 gopls 集成测试（需要 gopls 在 PATH 中）
// ---------------------------------------------------------------------------

func TestClientInitialize(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not found in PATH")
	}

	dir := t.TempDir()
	// 创建最小 Go 模块
	if err := os.WriteFile(dir+"/go.mod", []byte("module example\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rootURI := "file://" + dir
	client, err := NewClient("gopls", nil, rootURI)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	if !client.CommandRunning() {
		t.Error("command should be running after init")
	}
}

func TestClientDiagnosticNotification(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not found in PATH")
	}

	dir := t.TempDir()
	// 写入有错误的 Go 文件
	goCode := `package main

func main() {
	x := 1
	_ = x
}
`
	if err := os.WriteFile(dir+"/main.go", []byte(goCode), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/go.mod", []byte("module example\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rootURI := "file://" + dir
	client, err := NewClient("gopls", nil, rootURI)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	// 注册诊断通知处理器
	diagCh := make(chan PublishDiagnosticsParams, 1)
	client.OnNotification("textDocument/publishDiagnostics", func(raw json.RawMessage) {
		var params PublishDiagnosticsParams
		if err := json.Unmarshal(raw, &params); err != nil {
			t.Logf("unmarshal diagnostics: %v", err)
			return
		}
		diagCh <- params
	})

	// 打开文件
	fileURI := "file://" + dir + "/main.go"
	content, _ := os.ReadFile(dir + "/main.go")
	err = client.Notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        DocumentURI(fileURI),
			LanguageID: "go",
			Version:    1,
			Text:       string(content),
		},
	})
	if err != nil {
		t.Fatalf("didOpen: %v", err)
	}

	// 等待诊断推送
	select {
	case diag := <-diagCh:
		t.Logf("received %d diagnostics for %s", len(diag.Diagnostics), diag.URI)
		// gopls 可能返回 0 个（无错误）或若干诊断
	default:
		t.Log("no diagnostics received (may be clean file)")
	}
}

func TestClientDefinition(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not found in PATH")
	}

	dir := t.TempDir()
	goCode := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	if err := os.WriteFile(dir+"/main.go", []byte(goCode), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/go.mod", []byte("module example\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rootURI := "file://" + dir
	client, err := NewClient("gopls", nil, rootURI)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	fileURI := "file://" + dir + "/main.go"
	content, _ := os.ReadFile(dir + "/main.go")
	_ = client.Notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        DocumentURI(fileURI),
			LanguageID: "go",
			Version:    1,
			Text:       string(content),
		},
	})

	// 查询 fmt.Println 的定义（line 5, char 7 → "fmt.Println" 的位置）
	var locations []Location
	err = client.Call("textDocument/definition", DefinitionParams{
		TextDocument: TextDocumentIdentifier{URI: DocumentURI(fileURI)},
		Position:     Position{Line: 5, Character: 7},
	}, &locations)
	if err != nil {
		t.Fatalf("definition: %v", err)
	}

	if len(locations) == 0 {
		t.Fatal("expected at least one location for fmt.Println definition")
	}
	t.Logf("definition: %s:%d:%d", locations[0].URI, locations[0].Range.Start.Line, locations[0].Range.Start.Character)
}

func TestClientHover(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not found in PATH")
	}

	dir := t.TempDir()
	goCode := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	if err := os.WriteFile(dir+"/main.go", []byte(goCode), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/go.mod", []byte("module example\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rootURI := "file://" + dir
	client, err := NewClient("gopls", nil, rootURI)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	fileURI := "file://" + dir + "/main.go"
	content, _ := os.ReadFile(dir + "/main.go")
	_ = client.Notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        DocumentURI(fileURI),
			LanguageID: "go",
			Version:    1,
			Text:       string(content),
		},
	})

	// 查询 Println 的 hover 信息
	var hover Hover
	err = client.Call("textDocument/hover", HoverParams{
		TextDocument: TextDocumentIdentifier{URI: DocumentURI(fileURI)},
		Position:     Position{Line: 5, Character: 7},
	}, &hover)
	if err != nil {
		t.Fatalf("hover: %v", err)
	}

	if hover.Contents.Value == "" {
		t.Fatal("expected non-empty hover content")
	}
	t.Logf("hover: %s", hover.Contents.Value)
}

func TestClientDiagnosticWithError(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not found in PATH")
	}

	dir := t.TempDir()
	// 故意写入有编译错误的代码
	goCode := `package main

func main() {
	x := 1
	y = 2
	_ = x
	_ = y
}
`
	if err := os.WriteFile(dir+"/main.go", []byte(goCode), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/go.mod", []byte("module example\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rootURI := "file://" + dir
	client, err := NewClient("gopls", nil, rootURI)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	diagCh := make(chan PublishDiagnosticsParams, 1)
	client.OnNotification("textDocument/publishDiagnostics", func(raw json.RawMessage) {
		var params PublishDiagnosticsParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return
		}
		select {
		case diagCh <- params:
		default:
		}
	})

	fileURI := "file://" + dir + "/main.go"
	content, _ := os.ReadFile(dir + "/main.go")
	_ = client.Notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        DocumentURI(fileURI),
			LanguageID: "go",
			Version:    1,
			Text:       string(content),
		},
	})

	// 诊断推送是异步的，等待一小段时间
	var diag PublishDiagnosticsParams
	select {
	case diag = <-diagCh:
	case <-time.After(3 * time.Second):
	}

	if len(diag.Diagnostics) == 0 {
		t.Error("expected at least one diagnostic for broken code")
	}
	for _, d := range diag.Diagnostics {
		t.Logf("diagnostic: L%d:%d [%s] %s", d.Range.Start.Line, d.Range.Start.Character, severityString(d.Severity), d.Message)
	}
}

func severityString(s DiagnosticSeverity) string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityInformation:
		return "info"
	case SeverityHint:
		return "hint"
	default:
		return "unknown"
	}
}
