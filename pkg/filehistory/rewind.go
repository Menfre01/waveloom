package filehistory

import "fmt"

// Rewind restores all tracked files to their state at the given snapshot.
// Returns the list of modified files, or an error.
func (s *FileHistoryState) Rewind(targetMessageID, sessionDir string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Find the target snapshot
	targetIdx := -1
	for i, snap := range s.Snapshots {
		if snap.MessageID == targetMessageID {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		return nil, fmt.Errorf("snapshot not found for message %s", targetMessageID)
	}

	targetSnapshot := s.Snapshots[targetIdx]
	var restored []string

	for filePath := range s.TrackedFiles {
		targetBackup, ok := targetSnapshot.TrackedFileBackups[filePath]
		if ok {
			// Found target backup → restore
			if err := restoreBackup(sessionDir, filePath, targetBackup); err != nil {
				return restored, fmt.Errorf("restore %s: %w", filePath, err)
			}
			restored = append(restored, filePath)
		} else {
			// File not in target snapshot → find v1 backup (original state)
			v1Backup := s.findV1BackupLocked(filePath)
			if v1Backup.Version > 0 {
				if err := restoreBackup(sessionDir, filePath, v1Backup); err != nil {
					return restored, fmt.Errorf("restore %s to v1: %w", filePath, err)
				}
				restored = append(restored, filePath)
			}
		}
	}

	return restored, nil
}

// findV1BackupLocked finds the version 1 backup for a file (its original state).
// Caller must hold s.mu.
func (s *FileHistoryState) findV1BackupLocked(filePath string) FileHistoryBackup {
	for _, snap := range s.Snapshots {
		if b, ok := snap.TrackedFileBackups[filePath]; ok && b.Version == 1 {
			return b
		}
	}
	return FileHistoryBackup{}
}

// SnapshotForMessage returns the snapshot index for a given message ID.
// Returns -1 if not found.
func (s *FileHistoryState) SnapshotForMessage(messageID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i, snap := range s.Snapshots {
		if snap.MessageID == messageID {
			return i
		}
	}
	return -1
}
