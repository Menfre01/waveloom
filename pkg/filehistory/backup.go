package filehistory

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// backupDir returns the backup directory for a session.
func backupDir(sessionDir string) string {
	return filepath.Join(sessionDir, "file-history")
}

// backupPath returns the full path for a backup file.
// Format: <backupDir>/<sha256(absPath)>@v<N>
func backupPath(sessionDir, absPath string, version int) string {
	hash := sha256.Sum256([]byte(absPath))
	return filepath.Join(backupDir(sessionDir), fmt.Sprintf("%x@v%d", hash, version))
}

// createBackup copies the current file content to a backup.
// Returns the backup info. If file does not exist, returns BackupFileName="" (null semantic).
func createBackup(sessionDir, absPath string, version int) (FileHistoryBackup, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return FileHistoryBackup{BackupFileName: "", Version: version, BackupTime: time.Now()}, nil
		}
		return FileHistoryBackup{}, fmt.Errorf("read file for backup: %w", err)
	}

	bp := backupPath(sessionDir, absPath, version)
	if err := os.MkdirAll(filepath.Dir(bp), 0o755); err != nil {
		return FileHistoryBackup{}, fmt.Errorf("create backup dir: %w", err)
	}
	if err := os.WriteFile(bp, data, 0o644); err != nil {
		return FileHistoryBackup{}, fmt.Errorf("write backup: %w", err)
	}

	return FileHistoryBackup{
		BackupFileName: filepath.Base(bp),
		Version:        version,
		BackupTime:     time.Now(),
	}, nil
}

// restoreBackup copies backup content back to the original file path.
func restoreBackup(sessionDir, absPath string, backup FileHistoryBackup) error {
	if backup.BackupFileName == "" {
		// File did not exist at this version → delete it
		if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove file during rewind: %w", err)
		}
		return nil
	}

	bp := filepath.Join(backupDir(sessionDir), backup.BackupFileName)
	data, err := os.ReadFile(bp)
	if err != nil {
		return fmt.Errorf("read backup for restore: %w", err)
	}

	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create parent dir during restore: %w", err)
	}

	if err := os.WriteFile(absPath, data, 0o644); err != nil {
		return fmt.Errorf("write file during restore: %w", err)
	}
	return nil
}

// checkFileChanged checks if a file has been modified since the last backup
// by comparing mtime.
func checkFileChanged(absPath string, lastBackup FileHistoryBackup) bool {
	info, err := os.Stat(absPath)
	if err != nil {
		return true // file disappeared or error → treat as changed
	}
	return !info.ModTime().Equal(lastBackup.BackupTime)
}
