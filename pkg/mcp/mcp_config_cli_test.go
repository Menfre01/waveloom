package mcp

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 测试辅助
// ---------------------------------------------------------------------------

// captureOutput 在 fn 执行期间捕获 stdout 与 stderr。
func captureOutput(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stderr pipe: %v", err)
	}
	os.Stdout, os.Stderr = outW, errW
	defer func() {
		os.Stdout, os.Stderr = oldOut, oldErr
	}()

	fn()

	_ = outW.Close()
	_ = errW.Close()
	outBytes, _ := io.ReadAll(outR)
	errBytes, _ := io.ReadAll(errR)
	return string(outBytes), string(errBytes)
}

// writeJSON 创建父目录并写入 JSON 内容。
func writeJSON(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// isolateClaudeDesktop 在 Windows 上将 %APPDATA% 重定向到临时目录,
// 防止测试读写 CI 机器上真实的 Claude Desktop 配置
// (claudeDesktopConfigPath 在 Windows 按 APPDATA 定位)。
func isolateClaudeDesktop(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", filepath.Join(t.TempDir(), "AppData", "Roaming"))
	}
}

// ============================================================================
// config.go — LoadConfigs 多来源优先级
// ============================================================================

func TestLoadConfigs_FullSourcePriority(t *testing.T) {
	isolateClaudeDesktop(t)
	homeDir := t.TempDir()
	projectDir := t.TempDir()

	// 最低优先级:Claude 桌面版配置
	writeJSON(t, claudeDesktopConfigPath(homeDir),
		`{"mcpServers":{"shared":{"type":"http","url":"http://desktop"}}}`)

	// Claude Code 用户级 + 本地级
	claudeData, _ := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"shared": map[string]any{"type": "http", "url": "http://claude-user"},
		},
		"projects": map[string]any{
			projectDir: map[string]any{
				"mcpServers": map[string]any{
					"shared": map[string]any{"type": "http", "url": "http://claude-local"},
				},
			},
		},
	})
	writeJSON(t, filepath.Join(homeDir, ".claude.json"), string(claudeData))

	// Waveloom 用户级 + 本地级
	waveloomData, _ := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"shared": map[string]any{"type": "http", "url": "http://waveloom-user"},
		},
		"projects": map[string]any{
			projectDir: map[string]any{
				"mcpServers": map[string]any{
					"shared": map[string]any{"type": "http", "url": "http://waveloom-local"},
				},
			},
		},
	})
	writeJSON(t, filepath.Join(homeDir, ".waveloom.json"), string(waveloomData))

	// 最高优先级:.mcp.json
	writeJSON(t, filepath.Join(projectDir, ".mcp.json"),
		`{"mcpServers":{"shared":{"type":"http","url":"http://project"}}}`)

	configs := LoadConfigs(projectDir, homeDir)
	if len(configs) != 1 {
		t.Fatalf("len = %d, want 1", len(configs))
	}
	cfg := configs["shared"]
	if cfg.URL != "http://project" {
		t.Errorf("URL = %q, want http://project (最高优先级)", cfg.URL)
	}
	if cfg.Name != "shared" {
		t.Errorf("Name = %q, want shared", cfg.Name)
	}
}

func TestLoadConfigs_NoSources(t *testing.T) {
	isolateClaudeDesktop(t)
	configs := LoadConfigs(t.TempDir(), t.TempDir())
	if len(configs) != 0 {
		t.Errorf("len = %d, want 0", len(configs))
	}
}

// ============================================================================
// config.go — 单个来源加载
// ============================================================================

func TestLoadMCPJSON_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, ".mcp.json"), `{not valid json`)
	if servers := loadMCPJSON(dir); len(servers) != 0 {
		t.Errorf("len = %d, want 0 for invalid JSON", len(servers))
	}
}

func TestLoadWaveloomJSON_InvalidJSON(t *testing.T) {
	home := t.TempDir()
	writeJSON(t, filepath.Join(home, ".waveloom.json"), `{bad`)
	if servers := loadWaveloomJSON(home, ""); len(servers) != 0 {
		t.Errorf("user scope: len = %d, want 0", len(servers))
	}
	if servers := loadWaveloomJSON(home, "/proj"); len(servers) != 0 {
		t.Errorf("local scope: len = %d, want 0", len(servers))
	}
}

func TestLoadWaveloomJSON_ProjectNotPresent(t *testing.T) {
	home := t.TempDir()
	writeJSON(t, filepath.Join(home, ".waveloom.json"),
		`{"mcpServers":{},"projects":{}}`)
	if servers := loadWaveloomJSON(home, "/missing-project"); len(servers) != 0 {
		t.Errorf("len = %d, want 0", len(servers))
	}
}

func TestLoadClaudeJSON_LocalScope(t *testing.T) {
	home := t.TempDir()
	data, _ := json.Marshal(map[string]any{
		"projects": map[string]any{
			"/proj/z": map[string]any{
				"mcpServers": map[string]any{
					"z": map[string]any{"type": "http", "url": "http://z.example"},
				},
			},
		},
	})
	writeJSON(t, filepath.Join(home, ".claude.json"), string(data))

	servers := loadClaudeJSON(home, "/proj/z")
	if len(servers) != 1 {
		t.Fatalf("len = %d, want 1", len(servers))
	}
	if servers["z"].URL != "http://z.example" {
		t.Errorf("URL = %q", servers["z"].URL)
	}
	if servers := loadClaudeJSON(home, "/proj/missing"); len(servers) != 0 {
		t.Errorf("missing project: len = %d, want 0", len(servers))
	}
}

func TestLoadClaudeJSON_InvalidJSON(t *testing.T) {
	home := t.TempDir()
	writeJSON(t, filepath.Join(home, ".claude.json"), `{bad`)
	if servers := loadClaudeJSON(home, ""); len(servers) != 0 {
		t.Errorf("len = %d, want 0", len(servers))
	}
}

func TestLoadClaudeDesktopConfig(t *testing.T) {
	isolateClaudeDesktop(t)
	home := t.TempDir()
	// 无配置文件
	if servers := loadClaudeDesktopConfig(home); len(servers) != 0 {
		t.Errorf("missing file: len = %d, want 0", len(servers))
	}

	writeJSON(t, claudeDesktopConfigPath(home),
		`{"mcpServers":{"desk-srv":{"type":"stdio","command":"desk-cmd"}}}`)
	servers := loadClaudeDesktopConfig(home)
	if len(servers) != 1 {
		t.Fatalf("len = %d, want 1", len(servers))
	}
	if servers["desk-srv"].Command != "desk-cmd" {
		t.Errorf("Command = %q, want desk-cmd", servers["desk-srv"].Command)
	}
}

func TestLoadFlatMCPConfig_Errors(t *testing.T) {
	// 文件不存在
	if servers := loadFlatMCPConfig(filepath.Join(t.TempDir(), "nope.json")); len(servers) != 0 {
		t.Errorf("missing file: len = %d, want 0", len(servers))
	}
	// 坏 JSON
	badPath := filepath.Join(t.TempDir(), "bad.json")
	writeJSON(t, badPath, `{bad`)
	if servers := loadFlatMCPConfig(badPath); len(servers) != 0 {
		t.Errorf("invalid JSON: len = %d, want 0", len(servers))
	}
}

// ============================================================================
// config.go — 添加 server
// ============================================================================

func TestAddServerToWaveloomJSON_UpdateAndLocal(t *testing.T) {
	home := t.TempDir()
	cwd := "/proj/one"

	// 新建 user 级
	err := AddServerToWaveloomJSON(home, cwd, "user", "srv",
		ServerConfig{Type: ServerTypeHTTP, URL: "https://v1.example"})
	if err != nil {
		t.Fatalf("add user: %v", err)
	}
	// 同名覆盖更新
	err = AddServerToWaveloomJSON(home, cwd, "user", "srv",
		ServerConfig{Type: ServerTypeHTTP, URL: "https://v2.example"})
	if err != nil {
		t.Fatalf("update user: %v", err)
	}
	// local 级同名互不影响
	err = AddServerToWaveloomJSON(home, cwd, "local", "srv",
		ServerConfig{Type: ServerTypeHTTP, URL: "https://local.example"})
	if err != nil {
		t.Fatalf("add local: %v", err)
	}

	userServers := loadWaveloomJSON(home, "")
	if userServers["srv"].URL != "https://v2.example" {
		t.Errorf("user URL = %q, want updated value", userServers["srv"].URL)
	}
	localServers := loadWaveloomJSON(home, cwd)
	if localServers["srv"].URL != "https://local.example" {
		t.Errorf("local URL = %q", localServers["srv"].URL)
	}
}

func TestAddServerToWaveloomJSON_UnknownScope(t *testing.T) {
	err := AddServerToWaveloomJSON(t.TempDir(), "/p", "bogus", "srv", ServerConfig{})
	if err == nil || !strings.Contains(err.Error(), "unknown scope") {
		t.Errorf("err = %v, want containing 'unknown scope'", err)
	}
}

func TestAddServerToWaveloomJSON_ParseError(t *testing.T) {
	home := t.TempDir()
	writeJSON(t, filepath.Join(home, ".waveloom.json"), `{invalid`)
	err := AddServerToWaveloomJSON(home, "/p", "user", "srv", ServerConfig{})
	if err == nil || !strings.Contains(err.Error(), "parse") {
		t.Errorf("err = %v, want containing 'parse'", err)
	}
}

func TestAddServerToMCPJSON_Update(t *testing.T) {
	dir := t.TempDir()
	if err := AddServerToMCPJSON(dir, "a",
		ServerConfig{Type: ServerTypeHTTP, URL: "https://a.example"}); err != nil {
		t.Fatalf("add a: %v", err)
	}
	if err := AddServerToMCPJSON(dir, "b",
		ServerConfig{Type: ServerTypeStdio, Command: "cmd-b"}); err != nil {
		t.Fatalf("add b: %v", err)
	}
	// 覆盖 a
	if err := AddServerToMCPJSON(dir, "a",
		ServerConfig{Type: ServerTypeHTTP, URL: "https://a2.example"}); err != nil {
		t.Fatalf("update a: %v", err)
	}

	servers := loadMCPJSON(dir)
	if len(servers) != 2 {
		t.Fatalf("len = %d, want 2", len(servers))
	}
	if servers["a"].URL != "https://a2.example" {
		t.Errorf("a.URL = %q, want updated", servers["a"].URL)
	}
	if servers["b"].Command != "cmd-b" {
		t.Errorf("b.Command = %q", servers["b"].Command)
	}
}

func TestAddServerToMCPJSON_ParseError(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, ".mcp.json"), `{bad`)
	err := AddServerToMCPJSON(dir, "srv", ServerConfig{})
	if err == nil || !strings.Contains(err.Error(), "parse") {
		t.Errorf("err = %v, want containing 'parse'", err)
	}
}

// ============================================================================
// config.go — 删除 server
// ============================================================================

func TestRemoveServer_NotFound(t *testing.T) {
	err := RemoveServer(t.TempDir(), t.TempDir(), "ghost")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want containing 'not found'", err)
	}
}

func TestRemoveServer_FromMCPJSON(t *testing.T) {
	dir := t.TempDir()
	if err := AddServerToMCPJSON(dir, "keep", ServerConfig{Type: ServerTypeHTTP, URL: "https://keep"}); err != nil {
		t.Fatal(err)
	}
	if err := AddServerToMCPJSON(dir, "gone", ServerConfig{Type: ServerTypeHTTP, URL: "https://gone"}); err != nil {
		t.Fatal(err)
	}

	if err := RemoveServer(t.TempDir(), dir, "gone"); err != nil {
		t.Fatalf("RemoveServer: %v", err)
	}
	servers := loadMCPJSON(dir)
	if _, ok := servers["gone"]; ok {
		t.Error("gone 未被删除")
	}
	if _, ok := servers["keep"]; !ok {
		t.Error("keep 不应被删除")
	}
}

func TestRemoveServer_FromWaveloomUserAndLocal(t *testing.T) {
	home := t.TempDir()
	cwd := "/proj/x"

	if err := AddServerToWaveloomJSON(home, cwd, "user", "gone-user",
		ServerConfig{Type: ServerTypeHTTP, URL: "https://u"}); err != nil {
		t.Fatal(err)
	}
	if err := AddServerToWaveloomJSON(home, cwd, "local", "gone-local",
		ServerConfig{Type: ServerTypeHTTP, URL: "https://l"}); err != nil {
		t.Fatal(err)
	}

	if err := RemoveServer(home, cwd, "gone-user"); err != nil {
		t.Fatalf("remove user: %v", err)
	}
	if err := RemoveServer(home, cwd, "gone-local"); err != nil {
		t.Fatalf("remove local: %v", err)
	}
	if servers := loadWaveloomJSON(home, ""); len(servers) != 0 {
		t.Errorf("user scope 未清空: %v", servers)
	}
	if servers := loadWaveloomJSON(home, cwd); len(servers) != 0 {
		t.Errorf("local scope 未清空: %v", servers)
	}
}

func TestRemoveServer_ClaudeJSONUntouched(t *testing.T) {
	home := t.TempDir()
	writeJSON(t, filepath.Join(home, ".claude.json"),
		`{"mcpServers":{"c-srv":{"type":"stdio","command":"echo"}}}`)

	// .claude.json 不在 RemoveServer 管辖范围内
	err := RemoveServer(home, "", "c-srv")
	if err == nil {
		t.Fatal("expected not-found error")
	}
	servers := loadClaudeJSON(home, "")
	if len(servers) != 1 {
		t.Errorf(".claude.json 不应被修改, len = %d, want 1", len(servers))
	}
}

func TestRemoveServerFromFile_Errors(t *testing.T) {
	// 文件不存在
	if err := removeServerFromFile(filepath.Join(t.TempDir(), "nope.json"), "x", false, ""); err == nil {
		t.Error("missing file: want error, got nil")
	}
	// 坏 JSON
	badPath := filepath.Join(t.TempDir(), "bad.json")
	writeJSON(t, badPath, `{bad`)
	if err := removeServerFromFile(badPath, "x", false, ""); err == nil {
		t.Error("invalid JSON: want error, got nil")
	}
	// 文件有效但无此 server
	path := filepath.Join(t.TempDir(), ".mcp.json")
	writeJSON(t, path, `{"mcpServers":{}}`)
	if err := removeServerFromFile(path, "x", false, ""); err == nil {
		t.Error("not found: want error, got nil")
	}
	// hasProjects 且项目条目中无此 server
	wpath := filepath.Join(t.TempDir(), ".waveloom.json")
	writeJSON(t, wpath, `{"mcpServers":{},"projects":{}}`)
	if err := removeServerFromFile(wpath, "x", true, "/proj"); err == nil {
		t.Error("projects not found: want error, got nil")
	}
}

// ============================================================================
// config.go — 列表与 env 展开
// ============================================================================

func TestListServerConfigs_MultiSourceWithExpansion(t *testing.T) {
	t.Setenv("MCP_LIST_URL", "https://expanded.example")
	home := t.TempDir()
	cwd := t.TempDir()

	writeJSON(t, filepath.Join(cwd, ".mcp.json"),
		`{"mcpServers":{"srv":{"type":"http","url":"${MCP_LIST_URL}"}}}`)
	writeJSON(t, filepath.Join(home, ".waveloom.json"),
		`{"mcpServers":{"srv":{"type":"http","url":"http://wav-user"}},"projects":{}}`)
	writeJSON(t, filepath.Join(home, ".claude.json"),
		`{"mcpServers":{"srv":{"type":"http","url":"http://claude-user"}},"projects":{}}`)

	all := ListServerConfigs(home, cwd)
	sources := all["srv"]
	if len(sources) != 3 {
		t.Fatalf("sources len = %d, want 3", len(sources))
	}

	var projectCfg ServerConfig
	for _, src := range sources {
		switch src.Source {
		case ".mcp.json":
			projectCfg = src.Config
		case "~/.waveloom.json (user)", "~/.claude.json (user)":
		default:
			t.Errorf("unexpected source %q", src.Source)
		}
	}
	// env 展开已应用
	if projectCfg.URL != "https://expanded.example" {
		t.Errorf("project URL = %q, want expanded", projectCfg.URL)
	}
}

func TestListServerConfigs_Empty(t *testing.T) {
	if all := ListServerConfigs(t.TempDir(), t.TempDir()); len(all) != 0 {
		t.Errorf("len = %d, want 0", len(all))
	}
}

func TestExpandServerConfig_AllFields(t *testing.T) {
	t.Setenv("MCP_EXP_A", "val-a")
	t.Setenv("MCP_EXP_B", "val-b")

	cfg := &ServerConfig{
		Command: "${MCP_EXP_A}",
		URL:     "${MCP_EXP_B}",
		Args:    []string{"x", "${MCP_EXP_A}"},
		Env:     map[string]string{"E": "${MCP_EXP_B}"},
		Headers: map[string]string{"H": "${MCP_EXP_A}"},
	}
	expandServerConfig(cfg)

	if cfg.Command != "val-a" {
		t.Errorf("Command = %q, want val-a", cfg.Command)
	}
	if cfg.URL != "val-b" {
		t.Errorf("URL = %q, want val-b", cfg.URL)
	}
	if cfg.Args[1] != "val-a" {
		t.Errorf("Args[1] = %q, want val-a", cfg.Args[1])
	}
	if cfg.Env["E"] != "val-b" {
		t.Errorf("Env[E] = %q, want val-b", cfg.Env["E"])
	}
	if cfg.Headers["H"] != "val-a" {
		t.Errorf("Headers[H] = %q, want val-a", cfg.Headers["H"])
	}
}

// ============================================================================
// cli.go — RunMCPCommand
// ============================================================================

func TestRunMCPCommand_NoArgs(t *testing.T) {
	_, stderr := captureOutput(t, func() {
		if RunMCPCommand(nil) {
			t.Error("RunMCPCommand(nil) = true, want false")
		}
	})
	if !strings.Contains(stderr, "Usage: waveloom mcp") {
		t.Errorf("stderr = %q, want usage text", stderr)
	}
}

func TestRunMCPCommand_UnknownSubcommand(t *testing.T) {
	_, stderr := captureOutput(t, func() {
		if RunMCPCommand([]string{"bogus"}) {
			t.Error("RunMCPCommand(bogus) = true, want false")
		}
	})
	if !strings.Contains(stderr, "Unknown mcp subcommand: bogus") {
		t.Errorf("stderr = %q, want unknown subcommand message", stderr)
	}
}

// ============================================================================
// cli.go — runAdd
// ============================================================================

func TestRunAdd_HTTP_Success(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()

	var ok bool
	stdout, _ := captureOutput(t, func() {
		ok = runAdd([]string{
			"--transport", "http",
			"--scope", "user",
			"--header", "Authorization: Bearer tok",
			"h-srv", "https://example.com/mcp",
		}, home, cwd)
	})
	if !ok {
		t.Fatal("runAdd = false, want true")
	}
	if !strings.Contains(stdout, `Added MCP server "h-srv" (scope: user)`) {
		t.Errorf("stdout = %q", stdout)
	}

	servers := loadWaveloomJSON(home, "")
	cfg := servers["h-srv"]
	if cfg.Type != ServerTypeHTTP {
		t.Errorf("Type = %q, want http", cfg.Type)
	}
	if cfg.URL != "https://example.com/mcp" {
		t.Errorf("URL = %q", cfg.URL)
	}
	if cfg.Headers["Authorization"] != "Bearer tok" {
		t.Errorf("Headers = %v", cfg.Headers)
	}
}

func TestRunAdd_Stdio_Success(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()

	var ok bool
	_, stderr := captureOutput(t, func() {
		ok = runAdd([]string{
			"--transport", "stdio",
			"--env", "K1=v1",
			"--env", "K2=v2",
			"s-srv", "--", "cmd", "a1", "a2",
		}, home, cwd)
	})
	if !ok {
		t.Fatalf("runAdd = false, stderr = %q", stderr)
	}

	// 默认 scope 为 local
	servers := loadWaveloomJSON(home, cwd)
	cfg := servers["s-srv"]
	if cfg.Type != ServerTypeStdio {
		t.Errorf("Type = %q, want stdio", cfg.Type)
	}
	if cfg.Command != "cmd" {
		t.Errorf("Command = %q, want cmd", cfg.Command)
	}
	if len(cfg.Args) != 2 || cfg.Args[0] != "a1" || cfg.Args[1] != "a2" {
		t.Errorf("Args = %v, want [a1 a2]", cfg.Args)
	}
	if cfg.Env["K1"] != "v1" || cfg.Env["K2"] != "v2" {
		t.Errorf("Env = %v", cfg.Env)
	}
}

func TestRunAdd_Errors(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()

	// http 缺参数
	captureOutput(t, func() {
		if runAdd([]string{"--transport", "http", "name"}, home, cwd) {
			t.Error("http 缺参数: want false")
		}
	})
	// 只有 flags,无剩余参数
	captureOutput(t, func() {
		if runAdd([]string{"--transport", "http"}, home, cwd) {
			t.Error("只有 flags: want false")
		}
	})
	// stdio 缺少 --
	_, stderr := captureOutput(t, func() {
		if runAdd([]string{"--transport", "stdio", "name", "not-dash", "cmd"}, home, cwd) {
			t.Error("stdio 缺 --: want false")
		}
	})
	if !strings.Contains(stderr, "requires -- before the command") {
		t.Errorf("stderr = %q", stderr)
	}
	// stdio 参数不足
	captureOutput(t, func() {
		if runAdd([]string{"--transport", "stdio", "name", "--"}, home, cwd) {
			t.Error("stdio 参数不足: want false")
		}
	})
	// 未知 transport
	captureOutput(t, func() {
		if runAdd([]string{"--transport", "grpc", "name", "url"}, home, cwd) {
			t.Error("未知 transport: want false")
		}
	})
}

// ============================================================================
// cli.go — addServer 与 runAddJSON
// ============================================================================

func TestAddServer_ProjectScope(t *testing.T) {
	cwd := t.TempDir()
	var ok bool
	stdout, _ := captureOutput(t, func() {
		ok = addServer("p-srv", ServerConfig{Type: ServerTypeHTTP, URL: "https://p.example"},
			"project", t.TempDir(), cwd)
	})
	if !ok {
		t.Fatal("addServer = false, want true")
	}
	if !strings.Contains(stdout, `Added MCP server "p-srv" (scope: project)`) {
		t.Errorf("stdout = %q", stdout)
	}
	servers := loadMCPJSON(cwd)
	if servers["p-srv"].URL != "https://p.example" {
		t.Errorf("URL = %q", servers["p-srv"].URL)
	}
}

func TestAddServer_UnknownScope(t *testing.T) {
	_, stderr := captureOutput(t, func() {
		if addServer("x", ServerConfig{}, "bogus", t.TempDir(), t.TempDir()) {
			t.Error("unknown scope: want false")
		}
	})
	if !strings.Contains(stderr, "Unknown scope") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRunAddJSON_Success(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()

	var ok bool
	stdout, stderr := captureOutput(t, func() {
		ok = runAddJSON([]string{"--scope", "user", "j-srv",
			`{"type":"http","url":"https://j.example"}`}, home, cwd)
	})
	if !ok {
		t.Fatalf("runAddJSON = false, stderr = %q", stderr)
	}
	if !strings.Contains(stdout, `Added MCP server "j-srv" (scope: user)`) {
		t.Errorf("stdout = %q", stdout)
	}
	servers := loadWaveloomJSON(home, "")
	if servers["j-srv"].URL != "https://j.example" {
		t.Errorf("URL = %q", servers["j-srv"].URL)
	}
}

func TestRunAddJSON_Errors(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()

	// 缺参数
	captureOutput(t, func() {
		if runAddJSON([]string{"only-name"}, home, cwd) {
			t.Error("缺参数: want false")
		}
	})
	// 坏 JSON
	_, stderr := captureOutput(t, func() {
		if runAddJSON([]string{"n", "{bad"}, home, cwd) {
			t.Error("坏 JSON: want false")
		}
	})
	if !strings.Contains(stderr, "Error parsing JSON") {
		t.Errorf("stderr = %q", stderr)
	}
}

// ============================================================================
// cli.go — runList / runGet / runRemove
// ============================================================================

func TestRunList_Empty(t *testing.T) {
	stdout, _ := captureOutput(t, func() {
		if !runList(t.TempDir(), t.TempDir()) {
			t.Error("runList = false, want true")
		}
	})
	if !strings.Contains(stdout, "No MCP servers configured.") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestRunList_WithConfigs(t *testing.T) {
	cwd := t.TempDir()
	writeJSON(t, filepath.Join(cwd, ".mcp.json"), `{"mcpServers":{
		"stdio-srv":{"type":"stdio","command":"cmd","args":["a","b"],"env":{"K":"v"}},
		"http-srv":{"type":"http","url":"https://h.example"}
	}}`)

	stdout, _ := captureOutput(t, func() {
		if !runList(t.TempDir(), cwd) {
			t.Error("runList = false, want true")
		}
	})
	for _, want := range []string{
		"stdio-srv:",
		"[.mcp.json] stdio: cmd a b",
		"env: map[K:v]",
		"http-srv:",
		"[.mcp.json] http: https://h.example",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout 缺少 %q\nstdout = %q", want, stdout)
		}
	}
}

func TestRunGet_NoArgs(t *testing.T) {
	_, stderr := captureOutput(t, func() {
		if runGet(nil, t.TempDir(), t.TempDir()) {
			t.Error("no args: want false")
		}
	})
	if !strings.Contains(stderr, "Usage: waveloom mcp get") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRunGet_NotFound(t *testing.T) {
	stdout, _ := captureOutput(t, func() {
		if runGet([]string{"ghost"}, t.TempDir(), t.TempDir()) {
			t.Error("not found: want false")
		}
	})
	if !strings.Contains(stdout, `Server "ghost" not found.`) {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestRunGet_Found(t *testing.T) {
	cwd := t.TempDir()
	writeJSON(t, filepath.Join(cwd, ".mcp.json"), `{"mcpServers":{
		"http-srv":{"type":"http","url":"https://g.example","headers":{"X-K":"v"},"timeout":5000},
		"stdio-srv":{"type":"stdio","command":"cmd","args":["a"]}
	}}`)

	stdout, _ := captureOutput(t, func() {
		if !runGet([]string{"http-srv"}, t.TempDir(), cwd) {
			t.Error("http-srv: want true")
		}
	})
	for _, want := range []string{
		"http-srv:",
		"URL: https://g.example",
		"Headers: map[X-K:v]",
		"Timeout: 5000ms",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("http stdout 缺少 %q\nstdout = %q", want, stdout)
		}
	}

	stdout, _ = captureOutput(t, func() {
		if !runGet([]string{"stdio-srv"}, t.TempDir(), cwd) {
			t.Error("stdio-srv: want true")
		}
	})
	for _, want := range []string{
		"stdio-srv:",
		"Command: cmd",
		"Args: [a]",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdio stdout 缺少 %q\nstdout = %q", want, stdout)
		}
	}
}

func TestRunRemove_NoArgs(t *testing.T) {
	_, stderr := captureOutput(t, func() {
		if runRemove(nil, t.TempDir(), t.TempDir()) {
			t.Error("no args: want false")
		}
	})
	if !strings.Contains(stderr, "Usage: waveloom mcp remove") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRunRemove_Success(t *testing.T) {
	cwd := t.TempDir()
	if err := AddServerToMCPJSON(cwd, "r-srv", ServerConfig{Type: ServerTypeHTTP, URL: "https://r"}); err != nil {
		t.Fatal(err)
	}

	var ok bool
	stdout, _ := captureOutput(t, func() {
		ok = runRemove([]string{"r-srv"}, t.TempDir(), cwd)
	})
	if !ok {
		t.Fatal("runRemove = false, want true")
	}
	if !strings.Contains(stdout, `Removed MCP server "r-srv"`) {
		t.Errorf("stdout = %q", stdout)
	}
	if servers := loadMCPJSON(cwd); len(servers) != 0 {
		t.Errorf("servers 未清空: %v", servers)
	}
}

func TestRunRemove_NotFound(t *testing.T) {
	_, stderr := captureOutput(t, func() {
		if runRemove([]string{"ghost"}, t.TempDir(), t.TempDir()) {
			t.Error("not found: want false")
		}
	})
	if !strings.Contains(stderr, "Error:") {
		t.Errorf("stderr = %q", stderr)
	}
}

// ============================================================================
// cli.go — flag 解析边界
// ============================================================================

func TestParseHeaderFlags_Malformed(t *testing.T) {
	result := parseHeaderFlags([]string{"NoColon", "A:B:C", "Key:Val"})
	if _, ok := result["NoColon"]; ok {
		t.Error("无冒号条目不应被解析")
	}
	if result["A"] != "B:C" {
		t.Errorf("A = %q, want B:C", result["A"])
	}
	if result["Key"] != "Val" {
		t.Errorf("Key = %q, want Val", result["Key"])
	}
}

func TestParseHeaderFlags_Empty(t *testing.T) {
	if len(parseHeaderFlags(nil)) != 0 {
		t.Error("nil input: want empty map")
	}
}

func TestParseEnvFlags_Malformed(t *testing.T) {
	result := parseEnvFlags([]string{"NOEQ", "A=1=2"})
	if _, ok := result["NOEQ"]; ok {
		t.Error("无等号条目不应被解析")
	}
	if result["A"] != "1=2" {
		t.Errorf("A = %q, want 1=2", result["A"])
	}
}
