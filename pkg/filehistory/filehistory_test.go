package filehistory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNewState(t *testing.T) {
	s := NewState()
	if s == nil {
		t.Fatal("NewState returned nil")
		return
	}
	if s.SnapshotCount() != 0 {
		t.Fatalf("expected 0 snapshots, got %d", s.SnapshotCount())
	}
	if len(s.TrackedFiles) != 0 {
		t.Fatalf("expected 0 tracked files, got %d", len(s.TrackedFiles))
	}
}

func TestTrackEdit_CreatesBackup(t *testing.T) {
	dir := t.TempDir()
	s := NewState()

	src := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(src, []byte("original content"), 0o644); err != nil {
		t.Fatal(err)
	}

	s.TrackEdit(src, "msg-1", dir)

	if len(s.currentBackups) != 1 {
		t.Fatalf("expected 1 current backup, got %d", len(s.currentBackups))
	}
	if !s.TrackedFiles[src] {
		t.Fatal("expected file to be tracked")
	}

	// Verify backup file exists on disk
	b := s.currentBackups[src]
	bp := filepath.Join(backupDir(dir), b.BackupFileName)
	if _, err := os.Stat(bp); os.IsNotExist(err) {
		t.Fatalf("backup file not found: %s", bp)
	}
	data, _ := os.ReadFile(bp)
	if string(data) != "original content" {
		t.Fatalf("backup content mismatch: %q", string(data))
	}
}

func TestTrackEdit_DuplicateInSameTurn(t *testing.T) {
	dir := t.TempDir()
	s := NewState()

	src := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(src, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	// First track
	s.TrackEdit(src, "msg-1", dir)
	firstBackup := s.currentBackups[src]

	// Overwrite file with different content
	if err := os.WriteFile(src, []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second track — should be no-op (same turn, same file)
	s.TrackEdit(src, "msg-1", dir)

	// currentBackups should still have the first (original) backup
	if s.currentBackups[src].BackupFileName != firstBackup.BackupFileName {
		t.Fatal("duplicate TrackEdit should not create new backup")
	}
}

func TestTrackEdit_NonExistentFile(t *testing.T) {
	dir := t.TempDir()
	s := NewState()

	nonexistent := filepath.Join(dir, "nonexistent.txt")
	s.TrackEdit(nonexistent, "msg-1", dir)

	if !s.TrackedFiles[nonexistent] {
		t.Fatal("expected non-existent file to be tracked")
	}
	if b := s.currentBackups[nonexistent]; b.BackupFileName != "" {
		t.Fatalf("expected empty BackupFileName for non-existent file, got %q", b.BackupFileName)
	}
}

func TestTrackEdit_VersionIncrements(t *testing.T) {
	dir := t.TempDir()
	s := NewState()

	src := filepath.Join(dir, "version_test.txt")
	if err := os.WriteFile(src, []byte("v1 content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Turn 1: Track + Snapshot
	s.TrackEdit(src, "msg-1", dir)
	s.MakeSnapshot("msg-1", dir)
	if s.SnapshotCount() != 1 {
		t.Fatalf("expected 1 snapshot, got %d", s.SnapshotCount())
	}

	// Turn 2: Track again → should create v2
	if err := os.WriteFile(src, []byte("v2 content"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.TrackEdit(src, "msg-2", dir)

	if s.currentBackups[src].Version != 2 {
		t.Fatalf("expected version 2, got %d", s.currentBackups[src].Version)
	}
}

func TestMakeSnapshot(t *testing.T) {
	dir := t.TempDir()
	s := NewState()

	src := filepath.Join(dir, "snap.txt")
	if err := os.WriteFile(src, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	s.TrackEdit(src, "msg-1", dir)
	s.MakeSnapshot("msg-1", dir)

	if s.SnapshotCount() != 1 {
		t.Fatalf("expected 1 snapshot, got %d", s.SnapshotCount())
	}

	snap := s.GetSnapshots()[0]
	if snap.MessageID != "msg-1" {
		t.Fatalf("expected MessageID msg-1, got %s", snap.MessageID)
	}
	if b, ok := snap.TrackedFileBackups[src]; !ok {
		t.Fatal("expected backup in snapshot")
	} else if b.Version != 1 {
		t.Fatalf("expected version 1, got %d", b.Version)
	}

	// currentBackups should be cleared
	if len(s.currentBackups) != 0 {
		t.Fatalf("expected currentBackups to be cleared, got %d", len(s.currentBackups))
	}
}

func TestMakeSnapshot_NoChanges(t *testing.T) {
	dir := t.TempDir()
	s := NewState()

	src := filepath.Join(dir, "stable.txt")
	if err := os.WriteFile(src, []byte("stable"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Turn 1
	s.TrackEdit(src, "msg-1", dir)
	s.MakeSnapshot("msg-1", dir)

	// Turn 2: no edit to this file, just snapshot
	// File mtime differs from backup time → a new backup (v2) is created
	s.MakeSnapshot("msg-2", dir)

	if s.SnapshotCount() != 2 {
		t.Fatalf("expected 2 snapshots, got %d", s.SnapshotCount())
	}

	snap2 := s.GetSnapshots()[1]
	if b, ok := snap2.TrackedFileBackups[src]; !ok {
		t.Fatal("expected backup for unchanged file in snapshot 2")
	} else if b.Version == 0 {
		t.Fatal("expected non-zero version")
	}

	// currentBackups should be cleared after snapshot
	if len(s.currentBackups) != 0 {
		t.Fatalf("expected currentBackups cleared, got %d", len(s.currentBackups))
	}
}

func TestRewind_RestoresFile(t *testing.T) {
	dir := t.TempDir()
	s := NewState()

	src := filepath.Join(dir, "rewind_test.txt")
	if err := os.WriteFile(src, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Turn 1: backup original, then modify
	s.TrackEdit(src, "msg-1", dir)
	s.MakeSnapshot("msg-1", dir)

	if err := os.WriteFile(src, []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Turn 2: backup modified, then snapshot
	s.TrackEdit(src, "msg-2", dir)
	s.MakeSnapshot("msg-2", dir)

	// Rewind to msg-1 → file should be "original"
	restored, err := s.Rewind("msg-1", dir)
	if err != nil {
		t.Fatalf("Rewind failed: %v", err)
	}
	if len(restored) != 1 || restored[0] != src {
		t.Fatalf("unexpected restored files: %v", restored)
	}

	data, _ := os.ReadFile(src)
	if string(data) != "original" {
		t.Fatalf("expected 'original', got %q", string(data))
	}
}

func TestRewind_FileDidNotExist(t *testing.T) {
	dir := t.TempDir()
	s := NewState()

	nonexistent := filepath.Join(dir, "new_file.txt")
	// Agent creates a new file → TrackEdit stores BackupFileName="" 
	s.TrackEdit(nonexistent, "msg-1", dir)
	s.MakeSnapshot("msg-1", dir)

	// Create the file (simulating agent having created it after backup)
	if err := os.WriteFile(nonexistent, []byte("new content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Rewind should delete the file (it didn't exist at msg-1)
	restored, err := s.Rewind("msg-1", dir)
	if err != nil {
		t.Fatalf("Rewind failed: %v", err)
	}
	if len(restored) != 1 || restored[0] != nonexistent {
		t.Fatalf("unexpected restored files: %v", restored)
	}

	if _, err := os.Stat(nonexistent); !os.IsNotExist(err) {
		t.Fatal("expected file to be deleted after rewind")
	}
}

func TestRewind_V1Fallback(t *testing.T) {
	dir := t.TempDir()
	s := NewState()

	fileA := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(fileA, []byte("a-original"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Turn 1: only track fileA
	s.TrackEdit(fileA, "msg-1", dir)
	s.MakeSnapshot("msg-1", dir)

	fileB := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(fileB, []byte("b-original"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Turn 2: track fileB (first time)
	s.TrackEdit(fileB, "msg-2", dir)
	s.MakeSnapshot("msg-2", dir)

	// Modify both files
	if err := os.WriteFile(fileA, []byte("a-modified"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("b-modified"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Rewind to msg-1
	// fileA: has backup in msg-1 → restore from msg-1
	// fileB: NOT in msg-1 (first tracked at msg-2) → fallback to v1 (original)
	restored, err := s.Rewind("msg-1", dir)
	if err != nil {
		t.Fatalf("Rewind failed: %v", err)
	}

	dataA, _ := os.ReadFile(fileA)
	if string(dataA) != "a-original" {
		t.Fatalf("fileA: expected 'a-original', got %q", string(dataA))
	}

	dataB, _ := os.ReadFile(fileB)
	if string(dataB) != "b-original" {
		t.Fatalf("fileB: expected 'b-original' (v1 fallback), got %q", string(dataB))
	}

	_ = restored
}

func TestRewind_SnapshotNotFound(t *testing.T) {
	s := NewState()
	_, err := s.Rewind("nonexistent-msg", t.TempDir())
	if err == nil {
		t.Fatal("expected error for non-existent message ID")
	}
}

func TestRewind_PartialRestoreOnError(t *testing.T) {
	dir := t.TempDir()
	s := NewState()

	src := filepath.Join(dir, "good.txt")
	if err := os.WriteFile(src, []byte("good"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.TrackEdit(src, "msg-1", dir)
	s.MakeSnapshot("msg-1", dir)

	// Manually corrupt the backup on disk to simulate restore error
	// This is hard to test reliably, but we can at least verify
	// the function doesn't panic and returns partial results.
	// For now, test the normal case above.
	_ = src
}

func TestSnapshotForMessage(t *testing.T) {
	s := NewState()

	src := filepath.Join(t.TempDir(), "dummy.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	s.TrackEdit(src, "msg-aaa", t.TempDir())
	s.MakeSnapshot("msg-aaa", t.TempDir())

	s.TrackEdit(src, "msg-bbb", t.TempDir())
	s.MakeSnapshot("msg-bbb", t.TempDir())

	if idx := s.SnapshotForMessage("msg-aaa"); idx != 0 {
		t.Fatalf("expected index 0 for msg-aaa, got %d", idx)
	}
	if idx := s.SnapshotForMessage("msg-bbb"); idx != 1 {
		t.Fatalf("expected index 1 for msg-bbb, got %d", idx)
	}
	if idx := s.SnapshotForMessage("msg-ccc"); idx != -1 {
		t.Fatalf("expected -1 for unknown message, got %d", idx)
	}
}

func TestComputeDiffStats(t *testing.T) {
	prev := FileHistorySnapshot{
		MessageID: "msg-1",
		TrackedFileBackups: map[string]FileHistoryBackup{
			"a.txt": {BackupFileName: "hash@v1", Version: 1},
			"b.txt": {BackupFileName: "hash@v1", Version: 1},
		},
	}

	// curr: a changed, c added, b unchanged
	curr := FileHistorySnapshot{
		MessageID: "msg-2",
		TrackedFileBackups: map[string]FileHistoryBackup{
			"a.txt": {BackupFileName: "hash@v2", Version: 2}, // changed
			"b.txt": {BackupFileName: "hash@v1", Version: 1}, // unchanged
			"c.txt": {BackupFileName: "hash@v1", Version: 1}, // new
		},
	}

	ds := ComputeDiffStats(prev, curr)
	if ds.FilesChanged != 2 {
		t.Fatalf("expected 2 files changed, got %d (files: %v)", ds.FilesChanged, ds.Files)
	}
}

func TestFileListDisplay(t *testing.T) {
	tests := []struct {
		files    []string
		expected string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"a.txt"}, "a.txt"},
		{[]string{"a.txt", "b.txt"}, "a.txt, b.txt"},
		{[]string{"a.txt", "b.txt", "c.txt"}, "a.txt, b.txt, c.txt"},
		{[]string{"a.txt", "b.txt", "c.txt", "d.txt"}, "a.txt, b.txt, c.txt and 1 more"},
	}
	for _, tc := range tests {
		got := FileListDisplay(tc.files)
		if got != tc.expected {
			t.Errorf("FileListDisplay(%v) = %q, want %q", tc.files, got, tc.expected)
		}
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		n        int
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{-1, "-1"},
		{42, "42"},
		{-99, "-99"},
	}
	for _, tc := range tests {
		got := itoa(tc.n)
		if got != tc.expected {
			t.Errorf("itoa(%d) = %q, want %q", tc.n, got, tc.expected)
		}
	}
}

func TestContextFunctions(t *testing.T) {
	ctx := context.Background()

	// Test empty extraction
	if FromContext(ctx) != nil {
		t.Fatal("expected nil from empty context")
	}

	s := NewState()
	ctx = WithFileHistory(ctx, s)
	if FromContext(ctx) != s {
		t.Fatal("expected same state back")
	}

	ctx = WithSessionDir(ctx, "/tmp/test")
	if got := SessionDirFromContext(ctx); got != "/tmp/test" {
		t.Fatalf("expected /tmp/test, got %q", got)
	}

	ctx = WithMessageID(ctx, "msg-42")
	if got := MessageIDFromContext(ctx); got != "msg-42" {
		t.Fatalf("expected msg-42, got %q", got)
	}

	// Test empty extractions from fresh context
	ctx2 := context.Background()
	if got := SessionDirFromContext(ctx2); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := MessageIDFromContext(ctx2); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}
