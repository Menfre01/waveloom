package tool

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"waveloom/pkg/lsp"
)

// ---------------------------------------------------------------------------
// lsp_diagnostic
// ---------------------------------------------------------------------------

func TestLSPDiagnosticNoErrors(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not found in PATH")
	}

	dir := t.TempDir()
	writeFile(t, dir+"/go.mod", "module example\n\ngo 1.21\n")
	path := writeFile(t, dir+"/main.go", `package main

func main() {
	_ = 42
}
`)

	mgr := lsp.NewManager(lsp.WithIdleTimeout(10 * time.Second))
	defer mgr.Close()
	setManager(mgr)

	tool := &LSDiagnostic{}
	// 第一次：didOpen 触发诊断推送
	_, _ = tool.Execute(t.Context(), LSDiagnosticParams{FilePath: path})

	// 等待 gopls 异步推送诊断
	waitForDiagnostics(t, mgr, lsp.PathToURI(path), 100*time.Millisecond, 3*time.Second)

	// 第二次：从缓存读取
	result, err := tool.Execute(t.Context(), LSDiagnosticParams{FilePath: path})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	t.Logf("diagnostics: %s", result.Content)
	if !strings.Contains(result.Content, "无诊断信息") {
		t.Errorf("expected no diagnostics, got: %s", result.Content)
	}
}

func TestLSPDiagnosticWithErrors(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not found in PATH")
	}

	dir := t.TempDir()
	writeFile(t, dir+"/go.mod", "module example\n\ngo 1.21\n")
	path := writeFile(t, dir+"/main.go", `package main

func main() {
	x := 1
	y = 2
	_ = x
	_ = y
}
`)

	mgr := lsp.NewManager(lsp.WithIdleTimeout(10 * time.Second))
	defer mgr.Close()
	setManager(mgr)

	tool := &LSDiagnostic{}
	_, _ = tool.Execute(t.Context(), LSDiagnosticParams{FilePath: path})

	waitForDiagnostics(t, mgr, lsp.PathToURI(path), 100*time.Millisecond, 5*time.Second)

	result, err := tool.Execute(t.Context(), LSDiagnosticParams{FilePath: path})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	t.Logf("diagnostics: %s", result.Content)
	if !strings.Contains(result.Content, "undefined: y") {
		t.Errorf("expected 'undefined: y' in: %s", result.Content)
	}
	if !strings.Contains(result.Content, "error") {
		t.Errorf("expected 'error' severity in: %s", result.Content)
	}
}

// ---------------------------------------------------------------------------
// lsp_definition
// ---------------------------------------------------------------------------

func TestLSPDefinition(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not found in PATH")
	}

	dir := t.TempDir()
	writeFile(t, dir+"/go.mod", "module example\n\ngo 1.21\n")
	path := writeFile(t, dir+"/main.go", `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`)

	mgr := lsp.NewManager(lsp.WithIdleTimeout(10 * time.Second))
	defer mgr.Close()
	setManager(mgr)

	// 先打开文件让 gopls 加载包
	inst, err := mgr.GetOrCreate(path)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	_ = mgr.SyncFile(inst, path)
	// 等待 gopls 完成包加载
	waitForReady(t, mgr, inst, path, 2*time.Second)

	tool := &LSPDefinition{}
	result, err := tool.Execute(t.Context(), LSPDefinitionParams{
		FilePath:  path,
		Line:      5,
		Character: 7,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	t.Logf("definition: %s", result.Content)
	if !strings.Contains(result.Content, "fmt") {
		t.Errorf("expected fmt in: %s", result.Content)
	}
}

// ---------------------------------------------------------------------------
// lsp_hover
// ---------------------------------------------------------------------------

func TestLSPHover(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not found in PATH")
	}

	dir := t.TempDir()
	writeFile(t, dir+"/go.mod", "module example\n\ngo 1.21\n")
	path := writeFile(t, dir+"/main.go", `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`)

	mgr := lsp.NewManager(lsp.WithIdleTimeout(10 * time.Second))
	defer mgr.Close()
	setManager(mgr)

	inst, err := mgr.GetOrCreate(path)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	_ = mgr.SyncFile(inst, path)
	waitForReady(t, mgr, inst, path, 2*time.Second)

	tool := &LSPHover{}
	result, err := tool.Execute(t.Context(), LSPHoverParams{
		FilePath:  path,
		Line:      5,
		Character: 7,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	t.Logf("hover: %s", result.Content)
	if !strings.Contains(result.Content, "Println") {
		t.Errorf("expected Println in: %s", result.Content)
	}
}

// ---------------------------------------------------------------------------
// lsp_references
// ---------------------------------------------------------------------------

func TestLSPReferences(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not found in PATH")
	}

	dir := t.TempDir()
	writeFile(t, dir+"/go.mod", "module example\n\ngo 1.21\n")
	path := writeFile(t, dir+"/main.go", `package main

var x = 1

func main() {
	println(x)
}
`)

	mgr := lsp.NewManager(lsp.WithIdleTimeout(10 * time.Second))
	defer mgr.Close()
	setManager(mgr)

	inst, err := mgr.GetOrCreate(path)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	_ = mgr.SyncFile(inst, path)
	waitForReady(t, mgr, inst, path, 2*time.Second)

	tool := &LSPReferences{}
	result, err := tool.Execute(t.Context(), LSPReferencesParams{
		FilePath:  path,
		Line:      2,
		Character: 4,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	t.Logf("references: %s", result.Content)
	if !strings.Contains(result.Content, "引用") {
		t.Errorf("expected references in: %s", result.Content)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func setManager(mgr *lsp.Manager) {
	LSPManager = mgr
}

// waitForDiagnostics 轮询等待诊断到达缓存。
func waitForDiagnostics(t *testing.T, mgr *lsp.Manager, uri lsp.DocumentURI, interval, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		diags := mgr.Diagnostics(uri)
		if len(diags) > 0 {
			return
		}
		select {
		case <-deadline:
			t.Logf("timeout waiting for diagnostics for %s", uri)
			return
		case <-time.After(interval):
		}
	}
}

// waitForReady 等待 gopls 完成包加载（通过轮询 definition 请求）。
func waitForReady(t *testing.T, mgr *lsp.Manager, inst *lsp.ServerInstance, filePath string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		var locs []lsp.Location
		err := mgr.Call(inst, "textDocument/definition", lsp.DefinitionParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: lsp.PathToURI(filePath)},
			Position:     lsp.Position{Line: 0, Character: 0},
		}, &locs)
		if err == nil {
			return
		}
		select {
		case <-deadline:
			t.Logf("gopls not ready after %v: %v", timeout, err)
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
}
