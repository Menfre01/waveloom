// Package eval 提供 hashline 编辑模型的数据集驱动离线评估。
//
// 数据集为 JSONL 文件(每行一个 Case),runner 在内存文件系统上执行
// ParsePatch → ApplyPatch 完整路径,断言最终文件状态 / 错误类型 / 警告。
// 用途:
//   - 回归防护:把 LLM 端到端验证 skill(test-newedit)中的人工 case 固化进 CI
//   - 迭代度量:prompt / 语法版本演进时,用固定数据集量化解析与应用成功率
//   - workbench 基础:Case + RunCase 可被交互式工具(cmd/editbench)复用
package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Menfre01/waveloom/pkg/hashline"
)

// Case 是单个评估用例,对应 JSONL 数据集的一行。
type Case struct {
	// Name 用例名,要求数据集内唯一,失败时以此定位。
	Name string `json:"name"`
	// Files 是执行 patch 前的磁盘状态(path → content)。
	Files map[string]string `json:"files"`
	// SnapshotFiles 描述快照 store 状态,用于模拟 "read 过" 的前置条件:
	//   缺省(nil) → 对所有 Files 建立快照(正常 read-before-edit 流程)
	//   空 map {} → 无任何快照(读前哨兵 / 未 read 场景)
	//   显式内容  → 快照内容可与 Files 不同(模拟 read 后被外部修改)
	SnapshotFiles *map[string]string `json:"snapshot_files,omitempty"`
	// Patch 是 hashline patch 文本。独立成行(行首无缩进)的 `[path#TAG]` 占位符
	// 会被替换为该路径快照中的真实 TAG;该路径无快照时替换为 `#0000`(读前哨兵场景)。
	Patch string `json:"patch"`

	// ExpectFiles 断言执行后的最终文件内容(精确匹配)。错误场景下也可用
	// 于断言文件未被修改。
	ExpectFiles map[string]string `json:"expect_files,omitempty"`
	// ExpectErrorKind 断言至少一个 section 以该 kind 失败
	// (file_not_found / tag_mismatch / invalid_args / permission_denied)。
	ExpectErrorKind string `json:"expect_error_kind,omitempty"`
	// ExpectParseError 断言 ParsePatch 失败(此后不再执行 ApplyPatch)。
	ExpectParseError bool `json:"expect_parse_error,omitempty"`
	// ExpectWarningContains 断言至少一个 section 的 Warning 包含该子串。
	ExpectWarningContains string `json:"expect_warning_contains,omitempty"`
}

// Result 是单个用例的执行结果。
type Result struct {
	Case     *Case
	Passed   bool
	Failures []string
	Results  []hashline.SectionResult
	ParseErr error
}

func (r *Result) fail(format string, args ...any) {
	r.Failures = append(r.Failures, fmt.Sprintf(format, args...))
}

// ---------------------------------------------------------------------------
// 数据集加载
// ---------------------------------------------------------------------------

// LoadDir 加载目录下全部 .jsonl 数据集中的用例(按文件名排序,保证确定性)。
func LoadDir(dir string) ([]*Case, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read eval dir: %w", err)
	}
	var cases []*Case
	seen := make(map[string]string, len(cases))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		cs, err := LoadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			if c.Name == "" {
				return nil, fmt.Errorf("%s: eval case missing name", e.Name())
			}
			if prev, dup := seen[c.Name]; dup {
				return nil, fmt.Errorf("duplicate case name %q in %s (already in %s)", c.Name, e.Name(), prev)
			}
			seen[c.Name] = e.Name()
		}
		cases = append(cases, cs...)
	}
	return cases, nil
}

// LoadFile 加载单个 JSONL 文件。
func LoadFile(path string) ([]*Case, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	cases, err := Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cases, nil
}

// Decode 从 reader 逐行解析 JSONL。空行与 # 开头的注释行被跳过。
func Decode(r io.Reader) ([]*Case, error) {
	var cases []*Case
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNum := 0
	for sc.Scan() {
		lineNum++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var c Case
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNum, err)
		}
		cases = append(cases, &c)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return cases, nil
}

// ---------------------------------------------------------------------------
// 执行
// ---------------------------------------------------------------------------

// 仅匹配独立成行的 section 头(允许行尾空白),避免替换 patch body 中的
// [x#TAG] 字面量。限制:body 行本身顶格且恰如 section 头时仍会被替换。
var tagPlaceholder = regexp.MustCompile(`(?m)^\[([^]#]+)#TAG\][ \t]*$`)

// substituteTags 把 patch 文本中 [path#TAG] 占位符替换为 store 中的真实 TAG。
// 路径无快照时替换为 0000 — parser 要求 TAG 为 4 hex,而 0000 会命中
// ApplyPatch 的 "has not been read yet" 专用分支,正是读前哨兵类用例所需。
func substituteTags(patchText string, store *hashline.SnapshotStore) string {
	return tagPlaceholder.ReplaceAllStringFunc(patchText, func(m string) string {
		path := tagPlaceholder.FindStringSubmatch(m)[1]
		if snap, ok := store.Get(path); ok {
			return "[" + path + "#" + snap.TAG + "]"
		}
		return "[" + path + "#0000]"
	})
}

// RunCase 在全新的内存 FS + 快照 store 上执行单个用例。
// 每个用例相互独立,可并行执行。
func RunCase(c *Case) *Result {
	r := &Result{Case: c}

	fs := NewMemoryFS()
	for p, content := range c.Files {
		fs.Write(p, content)
	}

	store := hashline.NewStore()
	if c.SnapshotFiles == nil {
		for p, content := range c.Files {
			if _, err := store.Record(p, content); err != nil {
				r.fail("snapshot record %q: %v", p, err)
			}
		}
	} else {
		for p, content := range *c.SnapshotFiles {
			if _, err := store.Record(p, content); err != nil {
				r.fail("snapshot record %q: %v", p, err)
			}
		}
	}
	if len(r.Failures) > 0 {
		return r
	}

	patch, err := hashline.ParsePatch(substituteTags(c.Patch, store))
	if err != nil {
		r.ParseErr = err
		if !c.ExpectParseError {
			r.fail("unexpected parse error: %v", err)
		}
		r.Passed = len(r.Failures) == 0
		return r
	}
	if c.ExpectParseError {
		r.fail("expected parse error, but patch parsed successfully")
		r.Passed = false
		return r
	}

	r.Results = hashline.ApplyPatch(patch, fs, store)
	r.assert(c, fs)
	r.Passed = len(r.Failures) == 0
	return r
}

func (r *Result) assert(c *Case, fs *MemoryFS) {
	// ── 错误断言 ──
	if c.ExpectErrorKind != "" {
		found := false
		for _, res := range r.Results {
			if res.Error == nil {
				continue
			}
			if res.Error.Kind == c.ExpectErrorKind {
				found = true
			} else {
				r.fail("section %s failed with undeclared kind %q (expected %q): %s",
					res.Path, res.Error.Kind, c.ExpectErrorKind, res.Error.Message)
			}
		}
		if !found {
			r.fail("expected error kind %q, got: %s", c.ExpectErrorKind, summarizeResults(r.Results))
		}
	} else {
		for _, res := range r.Results {
			if res.Error != nil {
				r.fail("unexpected section error on %s: [%s] %s", res.Path, res.Error.Kind, res.Error.Message)
			}
		}
	}

	// ── 警告断言 ──
	if c.ExpectWarningContains != "" {
		found := false
		for _, res := range r.Results {
			if strings.Contains(res.Warning, c.ExpectWarningContains) {
				found = true
				break
			}
		}
		if !found {
			r.fail("expected warning containing %q, got: %s", c.ExpectWarningContains, summarizeWarnings(r.Results))
		}
	}

	// ── 最终文件状态断言(顺序无关,精确匹配)──
	paths := make([]string, 0, len(c.ExpectFiles))
	for p := range c.ExpectFiles {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		want := c.ExpectFiles[p]
		got, err := fs.ReadFile(p)
		if err != nil {
			r.fail("expect file %q: %v", p, err)
			continue
		}
		if got != want {
			r.fail("file %q mismatch:\n  want: %q\n  got:  %q", p, want, got)
		}
	}
}

func summarizeResults(results []hashline.SectionResult) string {
	var parts []string
	for _, res := range results {
		if res.Error != nil {
			parts = append(parts, fmt.Sprintf("%s:[%s]%s", res.Path, res.Error.Kind, res.Error.Message))
		} else {
			parts = append(parts, fmt.Sprintf("%s:ok", res.Path))
		}
	}
	if len(parts) == 0 {
		return "(no section results)"
	}
	return strings.Join(parts, ", ")
}

func summarizeWarnings(results []hashline.SectionResult) string {
	var parts []string
	for _, res := range results {
		if res.Warning != "" {
			parts = append(parts, fmt.Sprintf("%s:%q", res.Path, res.Warning))
		}
	}
	if len(parts) == 0 {
		return "(no warnings)"
	}
	return strings.Join(parts, ", ")
}

// ---------------------------------------------------------------------------
// MemoryFS — 内存文件系统(独立实现,不依赖 hashline 包内测试版本)
// ---------------------------------------------------------------------------

// MemoryFS 实现 hashline.FileSystem,path 原样作为 key(ResolvePath 不做转换)。
type MemoryFS struct {
	files map[string]string
}

func NewMemoryFS() *MemoryFS {
	return &MemoryFS{files: make(map[string]string)}
}

// Write 直接写入文件(不经过 patch 流程),用于构造初始状态。
func (fs *MemoryFS) Write(path, content string) {
	fs.files[path] = content
}

func (fs *MemoryFS) ReadFile(path string) (string, error) {
	content, ok := fs.files[path]
	if !ok {
		return "", fmt.Errorf("%w: %s", os.ErrNotExist, path)
	}
	return content, nil
}

func (fs *MemoryFS) WriteFile(path string, content string) error {
	fs.files[path] = content
	return nil
}

func (fs *MemoryFS) MkdirAll(path string) error { return nil }

func (fs *MemoryFS) Remove(path string) error {
	delete(fs.files, path)
	return nil
}

func (fs *MemoryFS) ResolvePath(path string) string { return path }
