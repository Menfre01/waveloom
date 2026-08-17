package main

// 本文件补齐以下 0 覆盖率纯逻辑函数的单元测试:
//   - i18n.go:       DetectLocale / messagesFor / resolveLocale
//   - humanize.go:   humanizeError
//   - tui_renderer.go: parseWebFetchBody / parseWebSearchBody
//   - main.go:       formatEnvironmentSection / resolveLocaleWithSettings /
//                    hookSettingsPaths / createLLMClient
//   - config.go:     printHelpWithAutoDetect
//   - tui.go:        serializeTodoItems
//   - completion.go: runCompletion(helper-process 模式,处理 os.Exit)
//   - tui_picker.go: doScanRelative / isHiddenOrBinary / extractDirPrefix /
//                    resolveTilde
//   - setup.go:      validateAPIKey / newSetupModel / buildExtraParams /
//                    renderSetupLogo

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/environment"
	"github.com/Menfre01/waveloom/pkg/todo"
)

// ---------------------------------------------------------------------------
// i18n.go — DetectLocale / messagesFor / resolveLocale
// ---------------------------------------------------------------------------

func TestDetectLocale_PriorityAndFallbacks(t *testing.T) {
	tests := []struct {
		name   string
		lcAll  string
		lang   string
		want   Locale
	}{
		{"LC_ALL zh_CN wins over LANG en_US", "zh_CN.UTF-8", "en_US.UTF-8", LocaleZhCN},
		{"LANG zh_CN with encoding suffix", "", "zh_CN.UTF-8", LocaleZhCN},
		{"LANG zh-CN hyphen form", "", "zh-CN", LocaleZhCN},
		{"LANG bare zh", "", "zh", LocaleZhCN},
		{"LANG zh_TW falls into zh_ prefix family", "", "zh_TW", LocaleZhCN},
		{"LANG zh with padding trimmed", "", "  zh-CN  ", LocaleZhCN},
		{"non-zh LC_ALL falls through to LANG zh", "C", "zh_CN", LocaleZhCN},
		{"LANG en_US", "", "en_US.UTF-8", LocaleEnUS},
		{"LANG C", "", "C", LocaleEnUS},
		{"both empty defaults to en-US", "", "", LocaleEnUS},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LC_ALL", tt.lcAll)
			t.Setenv("LANG", tt.lang)
			if got := DetectLocale(); got != tt.want {
				t.Errorf("DetectLocale() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMessagesFor(t *testing.T) {
	if got := messagesFor(LocaleZhCN); got != &zhCN {
		t.Errorf("messagesFor(zh-CN) should return zhCN instance")
	}
	if got := messagesFor(LocaleEnUS); got != &enUS {
		t.Errorf("messagesFor(en-US) should return enUS instance")
	}
	for _, unknown := range []Locale{"", "fr-FR", "xx"} {
		if got := messagesFor(unknown); got != &enUS {
			t.Errorf("messagesFor(%q) should fall back to enUS, got %p", unknown, got)
		}
	}
}

func TestResolveLocale(t *testing.T) {
	t.Run("explicit", func(t *testing.T) {
		if got := resolveLocale("zh-CN"); got != LocaleZhCN {
			t.Errorf("resolveLocale(zh-CN) = %q", got)
		}
		if got := resolveLocale("en-US"); got != LocaleEnUS {
			t.Errorf("resolveLocale(en-US) = %q", got)
		}
	})
	t.Run("auto and unknown use env detection", func(t *testing.T) {
		t.Setenv("LANG", "zh_CN.UTF-8")
		for _, raw := range []string{"auto", "", "fr-FR"} {
			if got := resolveLocale(raw); got != LocaleZhCN {
				t.Errorf("resolveLocale(%q) = %q, want zh-CN", raw, got)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// humanize.go — humanizeError
// ---------------------------------------------------------------------------

func TestHumanizeError(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantSub string
	}{
		{"nil error returns empty", "", ""},
		{"HTTP 401", "request failed: HTTP 401", "Authentication failed"},
		{"invalid_api_key", "invalid_api_key: bad credentials", "Authentication failed"},
		{"Invalid API Key", "Invalid API Key", "Authentication failed"},
		{"Authentication Fails", "Authentication Fails", "Authentication failed"},
		{"HTTP 403", "HTTP 403 Forbidden", "Access denied"},
		{"HTTP 429", "HTTP 429 Too Many Requests", "Rate limited"},
		{"rate limited", "you are rate limited", "Rate limited"},
		{"rate_limit", "rate_limit exceeded", "Rate limited"},
		{"HTTP 500", "HTTP 500", "temporary error"},
		{"HTTP 502", "HTTP 502", "temporary error"},
		{"HTTP 503", "HTTP 503", "temporary error"},
		{"HTTP 504", "HTTP 504", "temporary error"},
		{"network error", "network error", "Network error"},
		{"connection refused", "connection refused", "Network error"},
		{"no such host", "no such host", "Network error"},
		{"dial tcp", "dial tcp 1.2.3.4:443", "Network error"},
		{"i/o timeout", "i/o timeout", "Network error"},
		{"timeout", "request timeout", "timed out"},
		{"deadline exceeded", "deadline exceeded", "timed out"},
		{"context deadline exceeded", "context deadline exceeded", "timed out"},
		{"retry exhausted", "retry exhausted", "retry attempts failed"},
		{"deepseek overload", "insufficient system resource", "overloaded"},
		{"api_key is required", "api_key is required", "No API key"},
		{"API key is required", "API key is required", "No API key"},
		{"maximum context", "maximum context length", "context window"},
		{"context length", "context length exceeded", "context window"},
		{"max context", "max context", "context window"},
		{"consecutive empty responses", "consecutive empty responses", "empty responses"},
		{"unknown tool", "unknown tool: foo", "internal error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.in != "" {
				err = errors.New(tt.in)
			}
			got := humanizeError(err)
			if tt.wantSub == "" {
				if got != "" {
					t.Errorf("humanizeError(nil) = %q, want empty", got)
				}
				return
			}
			if !strings.Contains(got, tt.wantSub) {
				t.Errorf("humanizeError(%q) = %q, want substring %q", tt.in, got, tt.wantSub)
			}
		})
	}

	t.Run("fallback returns original message", func(t *testing.T) {
		if got := humanizeError(errors.New("some random failure")); got != "some random failure" {
			t.Errorf("expected original message, got %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// tui_renderer.go — parseWebFetchBody / parseWebSearchBody
// ---------------------------------------------------------------------------

func TestParseWebFetchBody(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"strips metadata header", "Fetched https://x.com  HTTP 200  1.2s\nContent-Type: text/html\n\nhello world", "hello world"},
		{"trims leading blank lines after separator", "header\n\n\n\nbody", "body"},
		{"no separator returns input unchanged", "plain output without blank line", "plain output without blank line"},
		{"empty input", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseWebFetchBody(tt.in); got != tt.want {
				t.Errorf("parseWebFetchBody(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseWebSearchBody(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"strips search header", "Search results for: \"go\"  (DuckDuckGo)  1.3s\n\n1. Result A", "1. Result A"},
		{"trims leading blank lines", "h\n\n\n1. A", "1. A"},
		{"no separator returns input unchanged", "single line result", "single line result"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseWebSearchBody(tt.in); got != tt.want {
				t.Errorf("parseWebSearchBody(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// main.go / config.go — environment section, locale resolution, hook paths
// ---------------------------------------------------------------------------

func TestFormatEnvironmentSection(t *testing.T) {
	t.Run("empty results and no overrides yield empty string", func(t *testing.T) {
		dir := t.TempDir()
		if got := formatEnvironmentSection(nil, dir, filepath.Join(dir, "no-global.json"), filepath.Join(dir, "no-proj.json")); got != "" {
			t.Errorf("expected empty section, got %q", got)
		}
	})

	t.Run("reports OS and shell plus found and not found probes", func(t *testing.T) {
		dir := t.TempDir()
		results := []environment.ProbeResult{
			{Binary: "go", Output: "go version go1.25", Found: true},
			{Binary: "dotnet", Output: "", Found: false},
		}
		got := formatEnvironmentSection(results, dir, filepath.Join(dir, "g.json"), filepath.Join(dir, "p.json"))
		for _, want := range []string{"## Environment", "- OS: ", "- Shell: ", "go", "go version go1.25", "Not found: dotnet"} {
			if !strings.Contains(got, want) {
				t.Errorf("section missing %q:\n%s", want, got)
			}
		}
	})

	t.Run("settings.json overrides are listed as configured tools", func(t *testing.T) {
		dir := t.TempDir()
		proj := filepath.Join(dir, "p.json")
		data := map[string]any{
			"environment": map[string]any{"tools": map[string]string{"go": "/opt/go/bin/go"}},
		}
		raw, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(proj, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		results := []environment.ProbeResult{
			{Binary: "go", Output: "go version go1.25", Found: true},
		}
		got := formatEnvironmentSection(results, dir, filepath.Join(dir, "g.json"), proj)
		if !strings.Contains(got, "Configured tools") || !strings.Contains(got, "/opt/go/bin/go") {
			t.Errorf("override not listed:\n%s", got)
		}
		// overridden binary must not appear under "Available tools"
		if strings.Contains(got, "Available tools") && strings.Contains(got, "go version go1.25") {
			t.Errorf("overridden probe should be excluded from available tools:\n%s", got)
		}
	})
}

func TestResolveLocaleWithSettings(t *testing.T) {
	writeSettings := func(dir, name, locale string) string {
		if locale == "" {
			return ""
		}
		p := filepath.Join(dir, name)
		raw, _ := json.Marshal(map[string]any{"locale": locale})
		if err := os.WriteFile(p, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("CLI flag wins over settings and env", func(t *testing.T) {
		dir := t.TempDir()
		proj := writeSettings(dir, "p.json", "en-US")
		t.Setenv("LANG", "zh_CN.UTF-8")
		if got := resolveLocaleWithSettings("zh-CN", proj, ""); got != LocaleZhCN {
			t.Errorf("CLI zh-CN should win, got %q", got)
		}
	})

	t.Run("project settings override global settings", func(t *testing.T) {
		dir := t.TempDir()
		proj := writeSettings(dir, "p.json", "en-US")
		glob := writeSettings(dir, "g.json", "zh-CN")
		if got := resolveLocaleWithSettings("auto", proj, glob); got != LocaleEnUS {
			t.Errorf("project en-US should win, got %q", got)
		}
	})

	t.Run("global settings used when project missing", func(t *testing.T) {
		dir := t.TempDir()
		glob := writeSettings(dir, "g.json", "zh-CN")
		if got := resolveLocaleWithSettings("auto", filepath.Join(dir, "nope.json"), glob); got != LocaleZhCN {
			t.Errorf("global zh-CN should apply, got %q", got)
		}
	})

	t.Run("falls back to env detection", func(t *testing.T) {
		t.Setenv("LANG", "zh_CN.UTF-8")
		if got := resolveLocaleWithSettings("auto", filepath.Join(t.TempDir(), "x"), filepath.Join(t.TempDir(), "y")); got != LocaleZhCN {
			t.Errorf("env detection should apply, got %q", got)
		}
	})

	t.Run("invalid JSON in settings is skipped", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "bad.json")
		if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("LANG", "en_US.UTF-8")
		if got := resolveLocaleWithSettings("auto", p, ""); got != LocaleEnUS {
			t.Errorf("should skip invalid JSON and use env, got %q", got)
		}
	})
}

func TestHookSettingsPaths(t *testing.T) {
	paths := hookSettingsPaths()
	if len(paths) != 5 {
		t.Fatalf("expected 5 paths, got %d: %v", len(paths), paths)
	}
	home, _ := os.UserHomeDir()
	wantTail := []string{
		filepath.Join(home, ".waveloom", "settings.json"),
		filepath.Join(".waveloom", "settings.json"),
	}
	gotTail := paths[len(paths)-2:]
	for i := range wantTail {
		if gotTail[i] != wantTail[i] {
			t.Errorf("paths[%d] = %q, want %q", len(paths)-2+i, gotTail[i], wantTail[i])
		}
	}
	// Waveloom 自有配置必须在最后(优先级最高)
	if !strings.Contains(paths[0], filepath.Join(".claude", "settings.json")) {
		t.Errorf("first path should be ~/.claude/settings.json, got %q", paths[0])
	}
}

func TestPrintHelpWithAutoDetect(t *testing.T) {
	t.Setenv("LANG", "zh_CN.UTF-8")
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	printHelpWithAutoDetect()
	w.Close()
	os.Stdout = old
	buf := make([]byte, 64*1024)
	n, _ := r.Read(buf)
	got := string(buf[:n])
	if !strings.Contains(got, "用法:") {
		t.Errorf("expected Chinese help output, got %q", got[:min(len(got), 200)])
	}
}

// ---------------------------------------------------------------------------
// tui.go — serializeTodoItems
// ---------------------------------------------------------------------------

func TestSerializeTodoItems(t *testing.T) {
	t.Run("nil state returns nil", func(t *testing.T) {
		if got := serializeTodoItems(nil); got != nil {
			t.Errorf("serializeTodoItems(nil) = %v, want nil", got)
		}
	})

	t.Run("empty state returns empty non-nil slice", func(t *testing.T) {
		got := serializeTodoItems(todo.NewTodoState())
		if got == nil {
			t.Fatal("expected empty non-nil slice")
		}
		if len(got) != 0 {
			t.Fatalf("expected empty slice, got %d items", len(got))
		}
	})

	t.Run("serializes snapshot items as JSON", func(t *testing.T) {
		ts := todo.NewTodoState()
		ts.Apply(todo.TodoWriteParams{Todos: []todo.TodoItem{
			{Content: "write tests", Status: "in_progress", Description: "desc"},
			{Content: "run coverage", Status: "pending"},
		}})
		raw := serializeTodoItems(ts)
		if len(raw) != 2 {
			t.Fatalf("expected 2 items, got %d", len(raw))
		}
		for i, item := range raw {
			var got todo.TodoItem
			if err := json.Unmarshal(item, &got); err != nil {
				t.Fatalf("item %d is not valid JSON: %v (%s)", i, err, item)
			}
			if got.Content == "" || got.Status == "" {
				t.Errorf("item %d missing fields: %+v", i, got)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// completion.go — runCompletion(helper-process,因为内部调用 os.Exit)
// ---------------------------------------------------------------------------

func TestRunCompletionHelper(t *testing.T) {
	if os.Getenv("GO_WANT_RUN_COMPLETION") != "1" {
		t.Skip("helper process only")
	}
	runCompletion(os.Getenv("WAVELOOM_COMPLETION_SHELL"))
}

func runCompletionChild(t *testing.T, shell string) (string, error) {
	t.Helper()
	// 注意:不能用 os.Args[0] —— 包内其他测试(TestParseCLI_*)会把
	// os.Args 重写为 ["waveloom", ...],导致此处 exec 到 PATH 上的
	// waveloom 主二进制而不是测试二进制。用 os.Executable 获取真实路径。
	bin, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(bin, "-test.run=TestRunCompletionHelper")
	cmd.Env = append(os.Environ(),
		"GO_WANT_RUN_COMPLETION=1",
		"WAVELOOM_COMPLETION_SHELL="+shell,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestRunCompletion(t *testing.T) {
	markers := map[string]string{
		"bash": "complete -F _waveloom waveloom",
		"zsh":  "#compdef waveloom",
		"fish": "complete -c waveloom -f",
	}
	for shell, marker := range markers {
		t.Run("supported "+shell, func(t *testing.T) {
			out, err := runCompletionChild(t, shell)
			if err != nil {
				t.Fatalf("runCompletion(%q) exited with error: %v\n%s", shell, err, out)
			}
			if !strings.Contains(out, marker) {
				t.Errorf("output missing marker %q:\n%s", marker, out)
			}
		})
	}

	t.Run("unsupported shell exits with code 1 and error message", func(t *testing.T) {
		out, err := runCompletionChild(t, "powershell")
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			t.Fatalf("expected exit code 1, got err=%v\n%s", err, out)
		}
		if !strings.Contains(out, "Unsupported shell: powershell") {
			t.Errorf("expected error message, got:\n%s", out)
		}
	})
}

// ---------------------------------------------------------------------------
// tui_picker.go — doScanRelative / isHiddenOrBinary / extractDirPrefix /
//                 resolveTilde
// ---------------------------------------------------------------------------

func mkfile(t *testing.T, root, rel string) string {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func pickerPaths(items []pickerItem) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it.Path] = true
	}
	return m
}

func TestDoScanRelative_Basic(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "a.go")
	mkfile(t, root, "b.txt")
	mkfile(t, root, "sub/c.md")
	mkfile(t, root, ".hidden.txt")
	mkfile(t, root, ".git/config")
	mkfile(t, root, "node_modules/pkg/index.js")
	mkfile(t, root, "archive.zip")
	mkfile(t, root, "image.png")
	mkfile(t, root, "vendor/lib/x.c")

	items := doScanRelative(context.Background(), nil, root, "")
	paths := pickerPaths(items)

	// 期望路径用 filepath.Join 构造,兼容 Windows 的 \ 分隔符
	for _, want := range []string{"a.go", "b.txt", "sub", filepath.Join("sub", "c.md")} {
		if !paths[want] {
			t.Errorf("missing expected item %q in %v", want, paths)
		}
	}
	for _, banned := range []string{".hidden.txt", ".git", "node_modules", "archive.zip", "image.png", "vendor"} {
		if paths[banned] {
			t.Errorf("item %q should be filtered out, got %v", banned, paths)
		}
	}
	// 目录推断:sub 应标记为目录
	for _, it := range items {
		if it.Path == "sub" && !it.IsDir {
			t.Error("expected sub to be a directory item")
		}
	}
}

func TestDoScanRelative_ParentDirIncludesCWD(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "waveloom")
	mkfile(t, cwd, "inner.txt")
	mkfile(t, root, "sibling.txt")

	items := doScanRelative(context.Background(), nil, cwd, "../")
	paths := pickerPaths(items)

	// CWD 本身应作为 ".." 目录候选项
	if !paths[".."] {
		t.Errorf("expected '..' (CWD itself) as candidate, got %v", paths)
	}
	// CWD 的目录候选项以 ../<basename> 形式出现
	if !paths[filepath.Join("..", "waveloom")] {
		t.Errorf("expected %q dir candidate, got %v", filepath.Join("..", "waveloom"), paths)
	}
	// CWD 内的文件被排除
	if paths["inner.txt"] {
		t.Errorf("inner.txt inside CWD should be excluded, got %v", paths)
	}
	// 兄弟文件可见(相对 CWD 以 ../ 前缀)
	if !paths[filepath.Join("..", "sibling.txt")] {
		t.Errorf("expected %q, got %v", filepath.Join("..", "sibling.txt"), paths)
	}
}

func TestDoScanRelative_EmptyDirReturnsNil(t *testing.T) {
	root := t.TempDir()
	if items := doScanRelative(context.Background(), nil, root, ""); items != nil {
		t.Errorf("expected nil for empty dir, got %v", items)
	}
}

func TestIsHiddenOrBinary(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{".hidden", true},
		{".git/config", true},
		{"node_modules", true},
		{"a/node_modules/b.js", true},
		{"__pycache__/x.pyc", true},
		{"vendor/lib.so", true},
		{"dist/bundle.js", true},
		{"build/out", true},
		{"app.exe", true},
		{"lib.dll", true},
		{"lib.dylib", true},
		{"main.o", true},
		{"lib.a", true},
		{"Main.class", true},
		{"x.pyc", true},
		{"app.jar", true},
		{"file.war", true},
		{"a.zip", true},
		{"a.tar", true},
		{"a.gz", true},
		{"a.bz2", true},
		{"a.7z", true},
		{"a.rar", true},
		{"photo.png", true},
		{"photo.JPG", true},
		{"photo.jpeg", true},
		{"x.gif", true},
		{"x.ico", true},
		{"x.pdf", true},
		{"x.woff2", true},
		{"x.ttf", true},
		{"x.eot", true},
		{"x.wasm", true},
		{"main.go", false},
		{"notes.txt", false},
		{".", false},
		{"..", false},
		{"a/b/main.go", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isHiddenOrBinary(tt.path); got != tt.want {
				t.Errorf("isHiddenOrBinary(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestExtractDirPrefix(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"main.go", ""},
		{"~/x/y", "~/x/"},
		{"~/x", "~/"},
		{"./sub/a", "./sub/"},
		{"../wav", "../"},
		{"..", ".."},
	}
	// 绝对路径用例按平台构造:Windows 上无盘符的 / 路径不是绝对路径
	if runtime.GOOS == "windows" {
		tests = append(tests, struct{ in, want string }{`C:\abs\path\f.go`, "C:/abs/path/"})
	} else {
		tests = append(tests, struct{ in, want string }{"/abs/path/f.go", "/abs/path/"})
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := extractDirPrefix(tt.in); got != tt.want {
				t.Errorf("extractDirPrefix(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestResolveTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}

	t.Run("non-tilde passes through", func(t *testing.T) {
		if got := resolveTilde("a/b"); got != "a/b" {
			t.Errorf("resolveTilde(a/b) = %q", got)
		}
	})
	t.Run("bare tilde expands to home", func(t *testing.T) {
		if got := resolveTilde("~"); got != home {
			t.Errorf("resolveTilde(~) = %q, want %q", got, home)
		}
	})
	t.Run("tilde with suffix", func(t *testing.T) {
		if got := resolveTilde("~/x/y"); got != home+"/x/y" {
			t.Errorf("resolveTilde(~/x/y) = %q, want %q", got, home+"/x/y")
		}
	})
	t.Run("unknown user falls back to input", func(t *testing.T) {
		in := "~no_such_user_zzz/x"
		if got := resolveTilde(in); got != in {
			t.Errorf("resolveTilde(%q) = %q, want unchanged", in, got)
		}
	})
	t.Run("current user tilde expands", func(t *testing.T) {
		cur, err := user.Current()
		if err != nil {
			t.Skip("cannot lookup current user")
		}
		got := resolveTilde("~" + cur.Username + "/x")
		if got != home+"/x" {
			t.Errorf("resolveTilde(~user/x) = %q, want %q", got, home+"/x")
		}
	})
}

// ---------------------------------------------------------------------------
// setup.go — validateAPIKey / newSetupModel / buildExtraParams /
//            renderSetupLogo
// ---------------------------------------------------------------------------

func TestValidateAPIKey_EmptyKeyFailsFast(t *testing.T) {
	err := validateAPIKey("deepseek", "", "https://api.deepseek.com")
	if err == nil {
		t.Fatal("expected error for empty API key")
	}
	if !strings.Contains(err.Error(), "invalid configuration") {
		t.Errorf("expected 'invalid configuration' wrapper, got %q", err)
	}
	if !strings.Contains(err.Error(), "API key is required") {
		t.Errorf("expected underlying key error, got %q", err)
	}
}

func TestNewSetupModel_Defaults(t *testing.T) {
	m := newSetupModel(LocaleZhCN)
	if m.state.prov != "deepseek" {
		t.Errorf("prov = %q, want deepseek", m.state.prov)
	}
	if m.state.theme != "auto" {
		t.Errorf("theme = %q, want auto", m.state.theme)
	}
	if m.state.locale != "zh-CN" {
		t.Errorf("locale = %q, want zh-CN", m.state.locale)
	}
	if m.state.model != "deepseek-v4-pro" {
		t.Errorf("model = %q", m.state.model)
	}
	if m.state.contextLimit != "1M" {
		t.Errorf("contextLimit = %q, want 1M", m.state.contextLimit)
	}
	if !m.state.darkBackground {
		t.Error("darkBackground should default to true")
	}
	if m.state.lc != &zhCN {
		t.Error("lc should be zhCN messages")
	}

	mEn := newSetupModel(LocaleEnUS)
	if mEn.state.lc != &enUS {
		t.Error("lc should be enUS messages")
	}
}

func TestBuildExtraParams(t *testing.T) {
	tests := []struct {
		prov string
		want map[string]any
	}{
		{"deepseek", map[string]any{"thinking": map[string]any{"type": "enabled"}, "reasoning_effort": "max"}},
		{"kimi", map[string]any{"reasoning_effort": "max"}},
		{"openai", nil},
		{"unknown", nil},
	}
	for _, tt := range tests {
		t.Run(tt.prov, func(t *testing.T) {
			m := &setupModel{state: &setupState{prov: tt.prov}}
			got := m.buildExtraParams()
			if len(got) != len(tt.want) {
				t.Fatalf("buildExtraParams(%q) = %v, want %v", tt.prov, got, tt.want)
			}
			for k, wantV := range tt.want {
				gotV, ok := got[k]
				if !ok {
					t.Errorf("missing key %q", k)
					continue
				}
				gotJSON, _ := json.Marshal(gotV)
				wantJSON, _ := json.Marshal(wantV)
				if string(gotJSON) != string(wantJSON) {
					t.Errorf("key %q = %s, want %s", k, gotJSON, wantJSON)
				}
			}
		})
	}
}

func TestRenderSetupLogo(t *testing.T) {
	t.Run("narrow width renders WAVELOOM text", func(t *testing.T) {
		got := renderSetupLogo(10)
		if !strings.Contains(got, "WAVELOOM") {
			t.Errorf("narrow logo should contain WAVELOOM, got %q", got)
		}
	})
	t.Run("wide width renders ascii art lines", func(t *testing.T) {
		got := renderSetupLogo(100)
		if len(strings.Split(got, "\n")) != len(asciiArt)+1 {
			t.Errorf("expected %d lines, got %d", len(asciiArt)+1, len(strings.Split(got, "\n")))
		}
	})
}

// ---------------------------------------------------------------------------
// main.go — createLLMClient
// ---------------------------------------------------------------------------

func TestCreateLLMClient(t *testing.T) {
	t.Run("missing settings files yield nil-settings error", func(t *testing.T) {
		t.Setenv("LLM_API_KEY", "")
		dir := t.TempDir()
		_, _, _, err := createLLMClient(filepath.Join(dir, "g.json"), filepath.Join(dir, "p.json"), "", "", LocaleEnUS)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "settings must not be nil") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("settings without api key yield key error", func(t *testing.T) {
		t.Setenv("LLM_API_KEY", "")
		dir := t.TempDir()
		p := filepath.Join(dir, "p.json")
		raw, _ := json.Marshal(map[string]any{"llm": map[string]any{"model": "m1"}})
		if err := os.WriteFile(p, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, _, err := createLLMClient(filepath.Join(dir, "g.json"), p, "", "", LocaleEnUS)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "api_key is required") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("valid settings create a client", func(t *testing.T) {
		t.Setenv("LLM_API_KEY", "")
		dir := t.TempDir()
		p := filepath.Join(dir, "p.json")
		raw, _ := json.Marshal(map[string]any{"llm": map[string]any{
			"provider": "deepseek",
			"api_key":  "sk-test",
			"model":    "deepseek-v4-pro",
			"timeout":  "10s",
		}})
		if err := os.WriteFile(p, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		client, cfg, settings, err := createLLMClient(filepath.Join(dir, "g.json"), p, "", "", LocaleEnUS)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if client == nil {
			t.Fatal("client should not be nil")
		}
		if cfg.APIKey != "sk-test" {
			t.Errorf("cfg.APIKey = %q", cfg.APIKey)
		}
		if settings == nil || settings.Model != "deepseek-v4-pro" {
			t.Errorf("settings = %+v", settings)
		}
	})
}
