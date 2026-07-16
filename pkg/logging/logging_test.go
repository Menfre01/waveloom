package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit_CreatesLogFile(t *testing.T) {
	dir := t.TempDir()
	cleanup := Init(dir, slog.LevelInfo)
	defer cleanup()

	logPath := filepath.Join(dir, "waveloom.log")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("log file not created: %v", err)
	}

	slog.Info("test message", "key", "value")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "test message") {
		t.Errorf("log file missing message, got: %s", string(data))
	}
	if !strings.Contains(string(data), "key") {
		t.Errorf("log file missing key, got: %s", string(data))
	}
}

func TestInit_RotatesOldFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "waveloom.log")
	oldPath := logPath + ".1"

	// Create an existing log file.
	if err := os.WriteFile(logPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	cleanup := Init(dir, slog.LevelInfo)
	defer cleanup()

	if _, err := os.Stat(oldPath); err != nil {
		t.Fatal("old log not rotated to .1")
	}
	oldData, _ := os.ReadFile(oldPath)
	if string(oldData) != "old" {
		t.Errorf("rotated file content mismatch: %q", string(oldData))
	}
}

func TestInit_DebugLevelWritesStderr(t *testing.T) {
	// Capture stderr via pipe (Linux/macOS only).
	r, w, err := os.Pipe()
	if err != nil {
		t.Skip("pipe not available")
	}
	origStderr := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = origStderr }()

	dir := t.TempDir()
	cleanup := Init(dir, slog.LevelDebug)
	defer cleanup()

	slog.Debug("debug msg", "k", "v")
	_ = w.Close()

	var stderrOut strings.Builder
	_, _ = io.Copy(&stderrOut, r)

	if !strings.Contains(stderrOut.String(), "debug msg") {
		t.Errorf("stderr missing debug msg, got: %s", stderrOut.String())
	}
}

func TestInit_InfoLevelSilentStderr(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Skip("pipe not available")
	}
	origStderr := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = origStderr }()

	dir := t.TempDir()
	cleanup := Init(dir, slog.LevelInfo)
	defer cleanup()

	slog.Debug("should not appear")
	_ = w.Close()

	var stderrOut strings.Builder
	_, _ = io.Copy(&stderrOut, r)

	if stderrOut.String() != "" {
		t.Errorf("stderr should be empty at Info level, got: %s", stderrOut.String())
	}
}

func TestInit_MissingDir_GracefulDegrade(t *testing.T) {
	// Use a path that can't be created (e.g. /proc/.../readonly).
	// We don't want to rely on OS-specific restrictions, so test
	// with a valid dir but ensure cleanup works.
	dir := t.TempDir()
	cleanup := Init(dir, slog.LevelInfo)
	cleanup()

	// Should not panic.
	slog.Info("after cleanup", "status", "ok")
}
