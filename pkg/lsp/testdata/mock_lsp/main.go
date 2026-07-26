package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// mock_lsp implements a minimal LSP 3.17 server for testing.
// Reads JSON-RPC from stdin, writes responses/notifications to stdout.

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      *int        `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error,omitempty"`
}

type notification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

func main() {
	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)

	for {
		msg, err := readMessage(reader)
		if err != nil {
			return
		}

		var req request
		if err := json.Unmarshal(msg, &req); err != nil {
			continue
		}

		// Handle notifications first
		if req.ID == nil {
			switch req.Method {
			case "exit":
				return
			case "textDocument/didOpen":
				handleDidOpen(writer, req.Params)
			case "textDocument/didChange":
				handleDidChange(writer, req.Params)
			case "initialized":
				// no-op
			}
			continue
		}

		// Handle requests
		switch req.Method {
		case "initialize":
			result := map[string]interface{}{
				"capabilities": map[string]interface{}{
					"textDocumentSync": map[string]interface{}{
						"openClose": true,
						"change":    1,
					},
					"diagnosticProvider": map[string]interface{}{
						"identifier": "mock",
					},
				},
			}
			sendResponse(writer, req.ID, result)

		case "shutdown":
			sendResponse(writer, req.ID, nil)

		default:
			sendError(writer, req.ID, -32601, fmt.Sprintf("Method not found: %s", req.Method))
		}
	}
}

func handleDidOpen(writer *bufio.Writer, raw json.RawMessage) {
	var params struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	json.Unmarshal(raw, &params)
	sendDiagnostics(writer, params.TextDocument.URI, []interface{}{})
}

func handleDidChange(writer *bufio.Writer, raw json.RawMessage) {
	var params struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
	}
	json.Unmarshal(raw, &params)
	var diags []interface{}
	if len(params.ContentChanges) > 0 && strings.Contains(params.ContentChanges[0].Text, "ERROR_TRIGGER") {
		diags = append(diags, map[string]interface{}{
			"range": map[string]interface{}{
				"start": map[string]interface{}{"line": 0, "character": 0},
				"end":   map[string]interface{}{"line": 0, "character": 5},
			},
			"severity": 1,
			"message":  "mock error: ERROR_TRIGGER found",
		})
	}
	sendDiagnostics(writer, params.TextDocument.URI, diags)
}

func readMessage(reader *bufio.Reader) ([]byte, error) {
	var contentLength int
	hasCL := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length:") {
			contentLength, _ = strconv.Atoi(strings.TrimPrefix(line, "Content-Length: "))
			hasCL = true
		}
	}
	if !hasCL || contentLength <= 0 {
		return nil, fmt.Errorf("invalid Content-Length")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

func writeMessage(writer *bufio.Writer, data []byte) {
	fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n", len(data))
	writer.Write(data)
	writer.Flush()
}

func sendResponse(writer *bufio.Writer, id *int, result interface{}) {
	resp := response{JSONRPC: "2.0", ID: id, Result: result}
	data, _ := json.Marshal(resp)
	writeMessage(writer, data)
}

func sendError(writer *bufio.Writer, id *int, code int, message string) {
	resp := response{JSONRPC: "2.0", ID: id, Error: map[string]interface{}{"code": code, "message": message}}
	data, _ := json.Marshal(resp)
	writeMessage(writer, data)
}

func sendDiagnostics(writer *bufio.Writer, uri string, diags []interface{}) {
	notif := notification{
		JSONRPC: "2.0",
		Method:  "textDocument/publishDiagnostics",
		Params: map[string]interface{}{
			"uri":         uri,
			"diagnostics": diags,
		},
	}
	data, _ := json.Marshal(notif)
	writeMessage(writer, data)
}
