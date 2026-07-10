package filehistory

import "time"

// MakeSnapshot creates a checkpoint for the given user message.
// It captures the current state of all tracked files.
// Called after each user turn completes (after CompleteRun).
func (s *FileHistoryState) MakeSnapshot(messageID, sessionDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	trackedFileBackups := make(map[string]FileHistoryBackup, len(s.TrackedFiles))

	for filePath := range s.TrackedFiles {
		// Check if we have a current backup for this file
		if currentBackup, ok := s.currentBackups[filePath]; ok {
			trackedFileBackups[filePath] = currentBackup
		} else {
			// File is tracked but wasn't modified this turn → check mtime
			// Find the latest backup for this file
			latestBackup := s.latestBackupForFileLocked(filePath)
			if checkFileChanged(filePath, latestBackup) {
				// File changed outside agent (e.g., user manual edit) → create new backup
				version := latestBackup.Version + 1
				backup, err := createBackup(sessionDir, filePath, version)
				if err == nil {
					trackedFileBackups[filePath] = backup
				}
			} else if latestBackup.Version > 0 {
				// Unchanged → reuse latest backup
				trackedFileBackups[filePath] = latestBackup
			}
		}
	}

	snapshot := FileHistorySnapshot{
		MessageID:          messageID,
		TrackedFileBackups: trackedFileBackups,
		Timestamp:          time.Now(),
	}

	s.Snapshots = append(s.Snapshots, snapshot)
	s.SnapshotSeq++

	// Clear current turn backups
	s.currentBackups = make(map[string]FileHistoryBackup)
}

// latestBackupForFileLocked finds the most recent backup for a file.
// Caller must hold s.mu.
func (s *FileHistoryState) latestBackupForFileLocked(filePath string) FileHistoryBackup {
	for i := len(s.Snapshots) - 1; i >= 0; i-- {
		if b, ok := s.Snapshots[i].TrackedFileBackups[filePath]; ok {
			return b
		}
	}
	return FileHistoryBackup{}
}
