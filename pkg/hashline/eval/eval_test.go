package eval

import (
	"os"
	"strings"
	"testing"
)

// TestEvalCases 数据集驱动的主测试:遍历 testdata/evals 下全部 JSONL case。
// 新增用例只需向数据集追加一行,无需改动本文件。
func TestEvalCases(t *testing.T) {
	cases, err := LoadDir("testdata/evals")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("no eval cases found in testdata/evals")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			r := RunCase(c)
			for _, f := range r.Failures {
				t.Error(f)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// runner 自身单测
// ---------------------------------------------------------------------------

func TestDecodeSkipsCommentsAndBlankLines(t *testing.T) {
	input := `# comment

{"name": "a", "files": {}, "patch": "p"}
   # indented comment
{"name": "b", "files": {}, "patch": "p"}
`
	cases, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(cases) != 2 || cases[0].Name != "a" || cases[1].Name != "b" {
		t.Fatalf("unexpected cases: %+v", cases)
	}
}

func TestDecodeInvalidJSON(t *testing.T) {
	_, err := Decode(strings.NewReader("not json\n"))
	if err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("expected line-numbered error, got: %v", err)
	}
}

func TestLoadDirNotExist(t *testing.T) {
	_, err := LoadDir("testdata/does-not-exist")
	if err == nil {
		t.Fatal("expected error for missing dir")
	}
}

func TestLoadDirRejectsDuplicateNames(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/a.jsonl", `{"name": "dup", "files": {}, "patch": "p"}`+"\n")
	writeFile(t, dir+"/b.jsonl", `{"name": "dup", "files": {}, "patch": "p"}`+"\n")
	_, err := LoadDir(dir)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got: %v", err)
	}
}

func TestLoadDirRejectsMissingName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/a.jsonl", `{"files": {}, "patch": "p"}`+"\n")
	_, err := LoadDir(dir)
	if err == nil || !strings.Contains(err.Error(), "missing name") {
		t.Fatalf("expected missing-name error, got: %v", err)
	}
}

func TestRunCaseParseErrorExpected(t *testing.T) {
	c := &Case{
		Name:             "parse-error",
		Files:            map[string]string{"f.go": "x\n"},
		Patch:            "not a patch at all",
		ExpectParseError: true,
	}
	if r := RunCase(c); !r.Passed {
		t.Fatalf("expected pass, failures: %v", r.Failures)
	}
}

func TestRunCaseParseErrorUnexpected(t *testing.T) {
	c := &Case{
		Name:  "parse-error-unexpected",
		Files: map[string]string{"f.go": "x\n"},
		Patch: "not a patch at all",
	}
	r := RunCase(c)
	if r.Passed {
		t.Fatal("expected failure for unexpected parse error")
	}
	if r.ParseErr == nil {
		t.Fatal("ParseErr should be recorded")
	}
}

func TestRunCaseParseErrorMissing(t *testing.T) {
	c := &Case{
		Name:             "parse-error-missing",
		Files:            map[string]string{"f.go": "x\n"},
		Patch:            "*** Begin Patch\n[f.go#TAG]\nDEL 1\n*** End Patch",
		ExpectParseError: true,
	}
	if r := RunCase(c); r.Passed {
		t.Fatal("expected failure when patch parses but parse error was expected")
	}
}

func TestSubstituteTags(t *testing.T) {
	c := &Case{
		Name:  "tag-subst",
		Files: map[string]string{"f.go": "a\nb\n"},
		Patch: "*** Begin Patch\n[f.go#TAG]\nDEL 1\n*** End Patch",
		// TAG 占位符被替换后 DEL 生效,文件只剩 b
		ExpectFiles: map[string]string{"f.go": "b\n"},
	}
	if r := RunCase(c); !r.Passed {
		t.Fatalf("failures: %v", r.Failures)
	}
}

func TestSubstituteTagsNoSnapshotBecomes0000(t *testing.T) {
	c := &Case{
		Name:            "tag-no-snapshot",
		Files:           map[string]string{"f.go": "a\n"},
		SnapshotFiles:   ptr(map[string]string{}),
		Patch:           "*** Begin Patch\n[f.go#TAG]\nDEL 1\n*** End Patch",
		ExpectErrorKind: "tag_mismatch",
		ExpectFiles:     map[string]string{"f.go": "a\n"},
	}
	if r := RunCase(c); !r.Passed {
		t.Fatalf("failures: %v", r.Failures)
	}
}

func TestRunCaseWarningAssertion(t *testing.T) {
	// 行号 SWAP 缺省 %OLD → warning "⚠️ SKIPPING VERIFICATION"
	c := &Case{
		Name:                  "warning-no-old",
		Files:                 map[string]string{"f.go": "a\n"},
		Patch:                 "*** Begin Patch\n[f.go#TAG]\nSWAP 1.=1\nb\n*** End Patch",
		ExpectWarningContains: "⚠️ SKIPPING VERIFICATION",
		ExpectFiles:           map[string]string{"f.go": "b\n"},
	}
	if r := RunCase(c); !r.Passed {
		t.Fatalf("failures: %v", r.Failures)
	}

	c2 := &Case{
		Name:                  "warning-missing",
		Files:                 map[string]string{"f.go": "a\n"},
		Patch:                 "*** Begin Patch\n[f.go#TAG]\nDEL 1\n*** End Patch",
		ExpectWarningContains: "nonexistent warning",
		ExpectFiles:           map[string]string{"f.go": ""},
	}
	if r := RunCase(c2); r.Passed {
		t.Fatal("expected failure for missing warning")
	}
}

func TestRunCaseUnexpectedSectionError(t *testing.T) {
	c := &Case{
		Name:  "unexpected-error",
		Files: map[string]string{"f.go": "a\n"},
		// DEL 超出文件行数范围 → invalid_args,但 case 未声明 expect_error_kind
		Patch: "*** Begin Patch\n[f.go#TAG]\nDEL 99\n*** End Patch",
	}
	r := RunCase(c)
	if r.Passed {
		t.Fatal("expected failure for undeclared section error")
	}
	found := false
	for _, f := range r.Failures {
		if strings.Contains(f, "unexpected section error") {
			found = true
		}
	}
	if !found {
		t.Fatalf("failures should mention unexpected section error: %v", r.Failures)
	}
}

func TestMemoryFSBasicOps(t *testing.T) {
	fs := NewMemoryFS()
	if _, err := fs.ReadFile("nope"); err == nil {
		t.Fatal("expected not-exist error")
	}
	fs.Write("a", "1")
	if got, _ := fs.ReadFile("a"); got != "1" {
		t.Fatalf("got %q", got)
	}
	if err := fs.WriteFile("a", "2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := fs.ReadFile("a"); got != "2" {
		t.Fatalf("got %q", got)
	}
	if err := fs.MkdirAll("x/y"); err != nil {
		t.Fatal(err)
	}
	if err := fs.Remove("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.ReadFile("a"); err == nil {
		t.Fatal("expected not-exist after Remove")
	}
	if got := fs.ResolvePath(" p "); got != " p " {
		t.Fatalf("ResolvePath should be identity, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func ptr(m map[string]string) *map[string]string { return &m }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSubstituteTagsIgnoresBodyLiteral 锁定:patch body 中的 [x#TAG] 字面量
// (非独立 section 头行)不被替换,避免静默篡改期望内容。
// 注意:body 内容行 TrimSpace 后不能以 [ 开头(patcher bodyTerminators 限制),
// 因此用行内位置的字面量做回归。
func TestSubstituteTagsIgnoresBodyLiteral(t *testing.T) {
	c := &Case{
		Name:  "tag-body-literal",
		Files: map[string]string{"f.go": "a\n"},
		// body 含行内 [x#TAG] 字面量 → 原样写入文件
		Patch:       "*** Begin Patch\n[f.go#TAG]\nINS.POST 1:\nnote: [x#TAG] end\n*** End Patch",
		ExpectFiles: map[string]string{"f.go": "a\nnote: [x#TAG] end\n"},
	}
	if r := RunCase(c); !r.Passed {
		t.Fatalf("failures: %v", r.Failures)
	}
}
