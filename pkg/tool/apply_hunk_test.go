package tool

import (
	"strings"
	"testing"
)

func TestSeekExact(t *testing.T) {
	lines := strings.Split("package main\n\nfunc greet() string {\n\treturn \"hello\"\n}\n", "\n")
	pattern := []string{"func greet() string {", "\treturn \"hello\"", "}"}
	idx := seekExact(lines, pattern, 0)
	if idx != 2 {
		t.Fatalf("expected idx=2, got %d", idx)
	}
}

func TestSeekExactNotFound(t *testing.T) {
	lines := strings.Split("package main\n\nfunc main() {}\n", "\n")
	pattern := []string{"func greet() string {"}
	idx := seekExact(lines, pattern, 0)
	if idx >= 0 {
		t.Fatalf("expected not found, got idx=%d", idx)
	}
}

func TestSeekRstrip(t *testing.T) {
	lines := strings.Split("func greet() string {   \n\treturn \"hello\"\t\t\n}\n", "\n")
	pattern := []string{"func greet() string {", "\treturn \"hello\"", "}"}
	idx := seekRstrip(lines, pattern, 0)
	if idx != 0 {
		t.Fatalf("expected idx=0, got %d", idx)
	}
}

func TestParsePatchFilesDefaultPath(t *testing.T) {
	text := "@@ func greet\n func greet() string {\n-    return \"hello\"\n+    return \"hello, \" + name\n }\n"
	files := parsePatchFiles(text, "main.go")
	if len(files) != 1 || files[0].path != "main.go" || len(files[0].hunks) != 1 {
		t.Fatalf("got %d files, path=%q, hunks=%d", len(files), files[0].path, len(files[0].hunks))
	}
}

func TestParsePatchFilesMultiFile(t *testing.T) {
	text := "*** Update File: main.go\n@@ func greet\n \n*** Update File: helper.go\n@@ func helper\n \n"
	files := parsePatchFiles(text, "")
	if len(files) != 2 || files[0].path != "main.go" || files[1].path != "helper.go" {
		t.Fatalf("got %d files, paths: %q, %q", len(files), files[0].path, files[1].path)
	}
}
