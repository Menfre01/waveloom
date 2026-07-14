package pathutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// NormalizeShellCommand
// ---------------------------------------------------------------------------

func TestNormalizeShellCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		wantCmd string
		wantDir string
	}{
		{
			name:    "no cd prefix",
			command: "go test ./...",
			wantCmd: "go test ./...",
			wantDir: "",
		},
		{
			name:    "cd with &&",
			command: "cd /tmp && ls",
			wantCmd: "ls",
			wantDir: "/tmp",
		},
		{
			name:    "cd with semicolon",
			command: "cd /tmp; ls",
			wantCmd: "ls",
			wantDir: "/tmp",
		},
		{
			name:    "cd with spaces around &&",
			command: "cd /app  &&  go test ./...",
			wantCmd: "go test ./...",
			wantDir: "/app",
		},
		{
			name:    "cd with double-quoted path",
			command: `cd "/path with spaces" && ls`,
			wantCmd: "ls",
			wantDir: "/path with spaces",
		},
		{
			name:    "cd with single-quoted path",
			command: `cd '/path with spaces' && ls`,
			wantCmd: "ls",
			wantDir: "/path with spaces",
		},
		{
			name:    "cd . and command",
			command: "cd . && pwd",
			wantCmd: "pwd",
			wantDir: ".",
		},
		{
			name:    "cd with chained commands",
			command: "cd /app && go build && go test",
			wantCmd: "go build && go test",
			wantDir: "/app",
		},
		{
			name:    "just cd (no command after separator)",
			command: "cd /tmp",
			wantCmd: "cd /tmp",
			wantDir: "",
		},
		{
			name:    "empty command",
			command: "",
			wantCmd: "",
			wantDir: "",
		},
		{
			name:    "cd appears but not at beginning",
			command: "echo cd /tmp && ls",
			wantCmd: "echo cd /tmp && ls",
			wantDir: "",
		},
		{
			name:    "cd && with no space before &&",
			command: "cd /tmp&&ls",
			wantCmd: "ls",
			wantDir: "/tmp",
		},
		{
			name:    "cd ; with no space before ;",
			command: "cd /app;go test",
			wantCmd: "go test",
			wantDir: "/app",
		},
		{
			name:    "cd ; with spaces around ;",
			command: "cd /app  ;  go test",
			wantCmd: "go test",
			wantDir: "/app",
		},
		{
			name:    "cd with trailing space after command",
			command: "cd /tmp && ",
			wantCmd: "",
			wantDir: "/tmp",
		},
		{
			name:    "cd with tilde path",
			command: "cd ~/project && make",
			wantCmd: "make",
			wantDir: "~/project",
		},
		{
			name:    "cd with env var path",
			command: "cd $HOME && ls",
			wantCmd: "ls",
			wantDir: "$HOME",
		},
		{
			name:    "cd with escaped path",
			command: `cd \$HOME && ls`,
			wantCmd: "ls",
			wantDir: `\$HOME`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCmd, gotDir := NormalizeShellCommand(tt.command)
			if gotCmd != tt.wantCmd {
				t.Errorf("command = %q, want %q", gotCmd, tt.wantCmd)
			}
			if gotDir != tt.wantDir {
				t.Errorf("dir = %q, want %q", gotDir, tt.wantDir)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ResolvePath
// ---------------------------------------------------------------------------

func TestResolvePathSuccess(t *testing.T) {
	dir := t.TempDir()
	path, err := ResolvePath(dir)
	if err != nil {
		t.Fatalf("ResolvePath() error = %v", err)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("ResolvePath() = %q, want absolute path", path)
	}
}

func TestResolvePathCleans(t *testing.T) {
	dir := t.TempDir()
	result, err := ResolvePath(filepath.Join(dir, "..", filepath.Base(dir), "sub"))
	if err != nil {
		t.Fatalf("ResolvePath() error = %v", err)
	}
	want := filepath.Join(dir, "sub")
	if result != want {
		t.Errorf("ResolvePath() = %q, want %q", result, want)
	}
}

func TestResolvePathWithDir(t *testing.T) {
	tmp := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmp, "src"), 0o755)

	t.Run("relative path", func(t *testing.T) {
		got, err := ResolvePathWithDir("src", tmp)
		if err != nil {
			t.Fatalf("ResolvePathWithDir() error = %v", err)
		}
		want := filepath.Join(tmp, "src")
		if got != want {
			t.Errorf("ResolvePathWithDir() = %q, want %q", got, want)
		}
	})

	t.Run("absolute path", func(t *testing.T) {
		abs := filepath.Join(tmp, "src")
		got, err := ResolvePathWithDir(abs, "/other")
		if err != nil {
			t.Fatalf("ResolvePathWithDir() error = %v", err)
		}
		if got != abs {
			t.Errorf("ResolvePathWithDir() = %q, want %q", got, abs)
		}
	})

	t.Run("empty working dir falls back to cwd", func(t *testing.T) {
		got, err := ResolvePathWithDir("test", "")
		if err != nil {
			t.Fatalf("ResolvePathWithDir() error = %v", err)
		}
		if !filepath.IsAbs(got) {
			t.Errorf("ResolvePathWithDir() = %q, want absolute path", got)
		}
	})
}

// ---------------------------------------------------------------------------
// TempDir
// ---------------------------------------------------------------------------

func TestTempDir_IsAbsolute(t *testing.T) {
	dir := TempDir()
	if !filepath.IsAbs(dir) {
		t.Errorf("TempDir() = %q, want absolute path", dir)
	}
}

func TestTempDir_IsConsistent(t *testing.T) {
	dir1 := TempDir()
	dir2 := TempDir()
	if dir1 != dir2 {
		t.Errorf("TempDir() returned different values across calls: %q vs %q", dir1, dir2)
	}
}

// TestTempDir_ResolvesSymlinks 验证 TempDir 解析了符号链接。
// macOS 上 /var 是 /private/var 的符号链接，os.TempDir() 常返回 /var/...，
// 而 filepath.EvalSymlinks 会解析为 /private/var/...。
// TempDir 应始终返回解析后的路径。
func TestTempDir_ResolvesSymlinks(t *testing.T) {
	dir := TempDir()
	// 如果 os.TempDir() 以 /var/ 开头，TempDir 应在 macOS 上解析为 /private/var/...
	raw := os.TempDir()
	resolved, err := filepath.EvalSymlinks(raw)
	if err != nil {
		t.Skipf("EvalSymlinks failed on os.TempDir(): %v", err)
	}
	if filepath.Clean(resolved) != dir {
		t.Errorf("TempDir() = %q, want %q (EvalSymlinks of os.TempDir())", dir, filepath.Clean(resolved))
	}
}

// REGRESSION: TMPDIR 路径不一致 —— os.TempDir() 返回未解析路径，
// 导致 bash 工具返回的 log path 与实际文件系统路径不匹配。
// 修复：TempDir() 始终通过 filepath.EvalSymlinks 解析。
func TestRegression_TempDir_SymlinkResolution(t *testing.T) {
	raw := os.TempDir()
	dir := TempDir()
	// TempDir 不应以 /var（未解析）开头——应已解析为 /private/var
	if strings.HasPrefix(dir, "/var/") {
		resolved, err := filepath.EvalSymlinks("/var")
		if err == nil && resolved != "/var" {
			t.Errorf("TempDir() returned unresolved path %q, expected resolved path (not starting with /var/ when /var is a symlink)", dir)
		}
	}
	// 两个路径应指向同一文件
	fi1, err1 := os.Stat(raw)
	fi2, err2 := os.Stat(dir)
	if err1 != nil || err2 != nil {
		t.Skipf("cannot stat temp dirs: raw=%v resolved=%v", err1, err2)
	}
	if !os.SameFile(fi1, fi2) {
		t.Errorf("TempDir() = %q does not point to same file as os.TempDir() = %q", dir, raw)
	}
}
