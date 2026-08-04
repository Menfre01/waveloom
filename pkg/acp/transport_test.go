package acp

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestStdioTransportSendReceive(t *testing.T) {
	// 使用 strings.Builder 作为 writer + strings.Reader 作为 reader
	var buf strings.Builder
	r := strings.NewReader("")
	w := &buf

	transport := NewStdioTransportIO(r, w)

	// 测试 Send
	req, _ := NewRequest(1, MethodInitialize, nil)
	err := transport.Send(req)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	output := buf.String()
	if !strings.HasSuffix(output, "\n") {
		t.Error("output should end with newline")
	}
	if !json.Valid([]byte(strings.TrimSpace(output))) {
		t.Errorf("invalid JSON output: %s", output)
	}
	if strings.Contains(strings.TrimRight(output, "\n"), "\n") {
		t.Error("message should be single line")
	}
}

func TestStdioTransportReceive(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`
	r := strings.NewReader(input + "\n")
	var buf strings.Builder

	transport := NewStdioTransportIO(r, &buf)

	raw, err := transport.Receive()
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if !json.Valid(raw) {
		t.Errorf("invalid JSON: %s", raw)
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Method != "initialize" {
		t.Errorf("method = %q, want %q", req.Method, "initialize")
	}
}

func TestStdioTransportReceiveEOF(t *testing.T) {
	r := strings.NewReader("")
	var buf strings.Builder

	transport := NewStdioTransportIO(r, &buf)

	_, err := transport.Receive()
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestStdioTransportReceiveSkipsEmptyLines(t *testing.T) {
	input := "\n\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"session/new\"}\n"
	r := strings.NewReader(input)
	var buf strings.Builder

	transport := NewStdioTransportIO(r, &buf)

	raw, err := transport.Receive()
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Method != "session/new" {
		t.Errorf("method = %q, want %q", req.Method, "session/new")
	}
}

func TestStdioTransportSendEmbeddedNewline(t *testing.T) {
	r := strings.NewReader("")
	var buf strings.Builder

	transport := NewStdioTransportIO(r, &buf)

	// json.Marshal 会转义字符串内的 \n 为 \\n，所以普通 Go 值不会触发嵌行检测。
	// 测试通过实现确认：合法 Go struct 的 Send 始终成功。
	req, _ := NewRequest(1, MethodInitialize, nil)
	if err := transport.Send(req); err != nil {
		t.Fatalf("Send valid request: %v", err)
	}

	output := buf.String()
	if !strings.HasSuffix(output, "\n") {
		t.Error("output should end with newline")
	}
	if strings.Count(strings.TrimRight(output, "\n"), "\n") > 0 {
		t.Error("message should be single line")
	}
}

func TestStdioTransportSendNotification(t *testing.T) {
	var buf strings.Builder
	r := strings.NewReader("")

	transport := NewStdioTransportIO(r, &buf)

	notif, err := NewNotification(MethodSessionUpdate, SessionUpdateParams{
		SessionID: "test-session",
		Update:    json.RawMessage(`{"sessionUpdate":"agent_message_chunk"}`),
	})
	if err != nil {
		t.Fatalf("NewNotification: %v", err)
	}

	err = transport.Send(notif)
	if err != nil {
		t.Fatalf("Send notification: %v", err)
	}

	output := buf.String()
	if !json.Valid([]byte(strings.TrimSpace(output))) {
		t.Errorf("invalid notification JSON: %s", output)
	}
}

func TestStdioTransportClose(t *testing.T) {
	r := strings.NewReader("")
	var buf strings.Builder

	transport := NewStdioTransportIO(r, &buf)
	if err := transport.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestStdioTransportReceiveLineTooLong:单条消息超过 10MB 上限 → ErrLineTooLong
// (可恢复哨兵),且后续行仍可正常读取(Scanner 从超长行之后继续)——DoS 防护基础。
func TestStdioTransportReceiveLineTooLong(t *testing.T) {
	longLine := strings.Repeat("x", 10*1024*1024+1)
	input := longLine + "\n" + `{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n"
	tr := NewStdioTransportIO(strings.NewReader(input), io.Discard)

	_, err := tr.Receive()
	if !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("expected ErrLineTooLong, got %v", err)
	}

	// 超长行之后的消息仍可读取
	raw, err := tr.Receive()
	if err != nil {
		t.Fatalf("receive after too-long line: %v", err)
	}
	if !strings.Contains(string(raw), `"method":"initialize"`) {
		t.Errorf("unexpected next message: %.100s", raw)
	}
}

// TestStdioTransportReceiveLastLineNoNewline:最后一行无换行结尾仍应返回
// (与 bufio.Scanner 行为一致,EOF 有数据分支)。
func TestStdioTransportReceiveLastLineNoNewline(t *testing.T) {
	tr := NewStdioTransportIO(strings.NewReader(`{"jsonrpc":"2.0","id":9,"method":"initialize"}`), io.Discard)

	raw, err := tr.Receive()
	if err != nil {
		t.Fatalf("receive last line without newline: %v", err)
	}
	if !strings.Contains(string(raw), `"id":9`) {
		t.Errorf("unexpected content: %s", raw)
	}
}

// TestStdioTransportReceiveEmptyInput:空输入 → io.EOF(无数据分支)。
func TestStdioTransportReceiveEmptyInput(t *testing.T) {
	tr := NewStdioTransportIO(strings.NewReader(""), io.Discard)
	if _, err := tr.Receive(); err != io.EOF {
		t.Errorf("empty input: err = %v, want io.EOF", err)
	}
}
