package tool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Menfre01/waveloom/pkg/pathutil"
)

func TestResolvePathSuccess(t *testing.T) {
	dir := t.TempDir()
	path, err := pathutil.ResolvePath(dir)
	if err != nil {
		t.Fatalf("ResolvePath() error = %v", err)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("ResolvePath() = %q, want absolute path", path)
	}
}

func TestResolvePathCleans(t *testing.T) {
	dir := t.TempDir()
	result, err := pathutil.ResolvePath(filepath.Join(dir, "..", filepath.Base(dir), "sub"))
	if err != nil {
		t.Fatalf("ResolvePath() error = %v", err)
	}
	want := filepath.Join(dir, "sub")
	if result != want {
		t.Errorf("ResolvePath() = %q, want %q", result, want)
	}
}

func TestIsWithinDirInside(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "sub")
	_ = os.Mkdir(subDir, 0o755)

	if !IsWithinDir(subDir, dir) {
		t.Errorf("IsWithinDir(%q, %q) = false, want true", subDir, dir)
	}
}

func TestIsWithinDirOutside(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "..", "outside")

	if IsWithinDir(other, dir) {
		t.Errorf("IsWithinDir(%q, %q) = true, want false", other, dir)
	}
}

func TestIsWithinDirDifferentBranch(t *testing.T) {
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, "a"), 0o755)
	_ = os.Mkdir(filepath.Join(dir, "b"), 0o755)

	if IsWithinDir(filepath.Join(dir, "a"), filepath.Join(dir, "b")) {
		t.Errorf("IsWithinDir(sibling) = true, want false")
	}
}

func TestIsWithinDirSelf(t *testing.T) {
	dir := t.TempDir()
	// self — rel would be ".", so should be false per IsWithinDir logic
	if IsWithinDir(dir, dir) {
		t.Errorf("IsWithinDir(self) = true, want false (same dir is not 'within')")
	}
}

func TestIsWithinDirSymlinkFallback(t *testing.T) {
	dir := t.TempDir()
	// A nonexistent file under dir should still be considered "within" —
	// it does not exist yet but is logically inside the allowed directory.
	nonexistent := filepath.Join(dir, "nonexistent")

	if !IsWithinDir(nonexistent, dir) {
		t.Errorf("IsWithinDir(nonexistent, dir) = false, want true")
	}
}

func TestIsBinaryFileText(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "text.txt")
	_ = os.WriteFile(f, []byte("hello world\n"), 0o644)

	isBin, err := IsBinaryFile(f)
	if err != nil {
		t.Fatalf("IsBinaryFile() error = %v", err)
	}
	if isBin {
		t.Error("IsBinaryFile() = true for text file, want false")
	}
}

func TestIsBinaryFileBinary(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "binary.bin")
	data := make([]byte, 512)
	for i := range data {
		if i%3 == 0 {
			data[i] = 0 // ~33% null
		} else {
			data[i] = 'A'
		}
	}
	_ = os.WriteFile(f, data, 0o644)

	isBin, err := IsBinaryFile(f)
	if err != nil {
		t.Fatalf("IsBinaryFile() error = %v", err)
	}
	if !isBin {
		t.Error("IsBinaryFile() = false for binary file, want true")
	}
}

func TestIsBinaryFileEmpty(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "empty.txt") // 用 .txt 绕过扩展名检查，走内容检测
	_ = os.WriteFile(f, []byte{}, 0o644)

	isBin, err := IsBinaryFile(f)
	if err != nil {
		t.Fatalf("IsBinaryFile() error = %v", err)
	}
	if isBin {
		t.Error("IsBinaryFile() = true for empty file, want false")
	}
}

func TestIsBinaryFileNotFound(t *testing.T) {
	_, err := IsBinaryFile("/nonexistent/path/file.txt") // .txt 走内容检测路径
	if err == nil {
		t.Error("IsBinaryFile() error = nil for nonexistent file, want error")
	}
}

func TestShouldSkipDirKnown(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{".git", true},
		{".svn", true},
		{"node_modules", true},
		{".claude", true},
		{"__pycache__", true},
		{".DS_Store", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldSkipDir(tt.name); got != tt.want {
				t.Errorf("ShouldSkipDir(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestShouldSkipDirDotPrefix(t *testing.T) {
	if !ShouldSkipDir(".hidden") {
		t.Error("ShouldSkipDir(.hidden) = false, want true")
	}
	if !ShouldSkipDir(".env") {
		t.Error("ShouldSkipDir(.env) = false, want true")
	}
}

func TestShouldSkipDirNormal(t *testing.T) {
	if ShouldSkipDir("src") {
		t.Error("ShouldSkipDir(src) = true, want false")
	}
	if ShouldSkipDir("pkg") {
		t.Error("ShouldSkipDir(pkg) = true, want false")
	}
}
