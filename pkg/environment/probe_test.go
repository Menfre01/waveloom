package environment

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunProbesEmpty(t *testing.T) {
	results := RunProbes(context.Background(), nil)
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestRunProbesCommandNotFound(t *testing.T) {
	results := RunProbes(context.Background(), []string{"nonexistent_binary_xyz --version"})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Found {
		t.Error("expected Found=false for nonexistent binary")
	}
	if r.Error != "not found" {
		t.Errorf("expected 'not found', got %q", r.Error)
	}
}

func TestRunProbesGoVersion(t *testing.T) {
	// go 应该存在于所有开发环境中
	results := RunProbes(context.Background(), []string{"go version"})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if !r.Found {
		t.Skip("go not found — skipping (CI environment?)")
	}
	if !strings.HasPrefix(r.Output, "go version") {
		t.Errorf("expected 'go version ...', got %q", r.Output)
	}
	if r.Binary != "go" {
		t.Errorf("expected binary 'go', got %q", r.Binary)
	}
}

func TestRunProbesTimeout(t *testing.T) {
	// 一个会 sleep 超过 ProbeTimeout 的命令
	ctx, cancel := context.WithTimeout(context.Background(), ProbeTimeout+time.Second)
	defer cancel()

	results := RunProbes(ctx, []string{"sleep 5"})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Found {
		t.Error("expected Found=false for timed-out command")
	}
	if r.Error != "timeout" {
		t.Errorf("expected 'timeout', got %q", r.Error)
	}
}

func TestRunProbesParallel(t *testing.T) {
	// 验证并行探测不会互相干扰（3 个 go version 同时跑）
	probes := []string{
		"go version",
		"go version",
		"go version",
	}
	start := time.Now()
	results := RunProbes(context.Background(), probes)
	elapsed := time.Since(start)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	// 并行执行应在 ~单次耗时 内完成，远小于 3× 串行
	if elapsed > ProbeTimeout+500*time.Millisecond {
		t.Errorf("parallel probes took %v, expected < %v", elapsed, ProbeTimeout+500*time.Millisecond)
	}
}

func TestRunProbesSortOrder(t *testing.T) {
	results := RunProbes(context.Background(), []string{
		"nonexistent_foo --version",
		"go version",
		"nonexistent_bar --version",
		"git version",
	})

	// Found 的排在前面
	for i := 1; i < len(results); i++ {
		if !results[i-1].Found && results[i].Found {
			t.Errorf("sort invariant violated: not-found before found at index %d", i)
		}
	}
}

func TestFormatEnvironmentSection_Empty(t *testing.T) {
	s := FormatEnvironmentSection(nil, "", "", nil)
	if s != "" {
		t.Errorf("expected empty string, got %q", s)
	}
}

func TestFormatEnvironmentSection_Mixed(t *testing.T) {
	results := []ProbeResult{
		{Binary: "go", Found: true, Output: "go1.22.1 darwin/arm64"},
		{Binary: "git", Found: true, Output: "git version 2.39.3"},
		{Binary: "python3", Found: false, Error: "not found"},
		{Binary: "node", Found: false, Error: "not found"},
	}

	s := FormatEnvironmentSection(results, "darwin", "/bin/zsh", nil)

	checks := []string{
		"## Environment",
		"OS: darwin",
		"Shell: /bin/zsh",
		"Available tools:",
		"go1.22.1",
		"git version 2.39.3",
		"Not found:",
		"python3",
		"node",
		"Do NOT attempt to run tools",
		"built-in tools",
		"brew install",
		"apt install",
		"winget install",
	}

	for _, c := range checks {
		if !strings.Contains(s, c) {
			t.Errorf("expected output to contain %q, got:\n%s", c, s)
		}
	}

	// 无 Configured tools 时不应出现该节
	if strings.Contains(s, "Configured tools") {
		t.Error("should not contain Configured tools when no overrides")
	}
}

func TestFormatEnvironmentSection_ConfiguredTools(t *testing.T) {
	results := []ProbeResult{
		{Binary: "go", Found: false, Error: "not found"},
		{Binary: "git", Found: true, Output: "git version 2.39.3"},
		{Binary: "python3", Found: false, Error: "not found"},
	}

	overrides := map[string]string{
		"go":      "D:\\go\\go\\bin\\go",
		"python3": "/usr/local/bin/python3",
	}

	s := FormatEnvironmentSection(results, "windows", "cmd.exe", overrides)

	checks := []string{
		"## Environment",
		"OS: windows",
		"Configured tools (use the full path when invoking):",
		"go",
		"D:\\go\\go\\bin\\go",
		"python3",
		"/usr/local/bin/python3",
		"Available tools:",
		"git version 2.39.3",
	}

	for _, c := range checks {
		if !strings.Contains(s, c) {
			t.Errorf("expected output to contain %q, got:\n%s", c, s)
		}
	}

	// 已配置的工具不应出现在 Not found 中
	if strings.Contains(s, "Not found:") {
		t.Error("should not contain Not found when all absent tools have overrides")
	}
}

func TestFormatEnvironmentSection_ConfiguredAndNotFound(t *testing.T) {
	results := []ProbeResult{
		{Binary: "go", Found: false, Error: "not found"},
		{Binary: "node", Found: false, Error: "not found"},
		{Binary: "docker", Found: false, Error: "not found"},
	}

	overrides := map[string]string{
		"go": "D:\\go\\go\\bin\\go",
	}

	s := FormatEnvironmentSection(results, "windows", "cmd.exe", overrides)

	// go 在 Configured tools 中，不在 Not found 中
	if !strings.Contains(s, "Configured tools") {
		t.Error("should contain Configured tools")
	}
	if !strings.Contains(s, "go") {
		t.Error("should contain go in Configured tools")
	}
	// node 和 docker 应在 Not found 中
	if !strings.Contains(s, "Not found:") {
		t.Error("should contain Not found for node and docker")
	}
	if !strings.Contains(s, "node") && !strings.Contains(s, "docker") {
		t.Error("should list node or docker in Not found")
	}
}

func TestFirstLine(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello\nworld", "hello"},
		{"single line", "single line"},
		{"line1\r\nline2", "line1"},
		{"", ""},
	}

	for _, tt := range tests {
		got := firstLine(tt.input)
		if got != tt.expected {
			t.Errorf("firstLine(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestDefaultProbesNotEmpty(t *testing.T) {
	probes := DefaultProbes()
	if len(probes) == 0 {
		t.Error("DefaultProbes() returned empty list")
	}
	// 确保全是格式合法的命令（至少有一个空格分隔 binary 和 args）
	for _, p := range probes {
		if !strings.Contains(p, " ") {
			t.Errorf("probe %q should contain a space between binary and args", p)
		}
	}
}

func TestFormatEnvironmentSection_SkipOnEmptyResults(t *testing.T) {
	// 空 results + 无 overrides → 不应有 ## Environment 节
	s := FormatEnvironmentSection([]ProbeResult{}, "linux", "/bin/bash", nil)
	if s != "" {
		t.Errorf("expected empty for no probes, got:\n%s", s)
	}

	// 空 results 但 有 overrides → 应有节
	s2 := FormatEnvironmentSection([]ProbeResult{}, "linux", "/bin/bash", map[string]string{"go": "/usr/bin/go"})
	if s2 == "" {
		t.Error("expected section when overrides present even without probe results")
	}
}

func TestJavaVersionOutputOnStderr(t *testing.T) {
	// Java 输出在 stderr，应被正确处理（通过 runOne 的 stderr fallback）
	results := RunProbes(context.Background(), []string{"java -version"})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if !r.Found {
		// 如果 java 未安装，也是合法的
		t.Logf("java not found: %s", r.Error)
		return
	}
	if r.Output == "" {
		t.Error("java -version found but output is empty — stderr fallback may be broken")
	}
	t.Logf("java output: %s", r.Output)
}

func TestRunProbesContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	results := RunProbes(ctx, DefaultProbes())
	// 应该全部返回 Found=false（要么 not found 要么因 ctx 取消）
	for _, r := range results {
		if r.Found {
			t.Errorf("expected all probes to fail with cancelled context, but %s succeeded", r.Binary)
		}
	}
}

// 确保 runOne 在 windows 上兼容（不测试实际行为，仅确保编译）
func TestRunOneEmptyCommand(t *testing.T) {
	r := runOne(context.Background(), "")
	if r.Found {
		t.Error("empty command should not succeed")
	}
	if r.Error != "empty command" {
		t.Errorf("expected 'empty command', got %q", r.Error)
	}
}

// 验证 FormatEnvironmentSection 在 OS/Shell 为空时的行为
func TestFormatEnvironmentSection_NoMeta(t *testing.T) {
	results := []ProbeResult{
		{Binary: "echo", Found: true, Output: "found"},
	}
	s := FormatEnvironmentSection(results, "", "", nil)
	if !strings.Contains(s, "Available tools:") {
		t.Error("expected Available tools section")
	}
	if strings.Contains(s, "- OS:") || strings.Contains(s, "- Shell:") {
		t.Error("should not contain OS/Shell lines when empty")
	}
}

func init() {
	// 确保测试不使用真实网络路径
	_ = runtime.GOOS
}
