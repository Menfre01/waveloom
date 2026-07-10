package filehistory

// TrackEdit records a backup before the agent modifies a file.
// Called before each edit_file / write_file execution.
// sessionDir is the session directory path (for storing backups).
// If the file has already been tracked in the current turn, this is a no-op.
func (s *FileHistoryState) TrackEdit(filePath, messageID, sessionDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Already tracked in current turn → skip
	if _, ok := s.currentBackups[filePath]; ok {
		return
	}

	// Determine version: if file already tracked globally, use next version
	version := 1
	// Find the latest version for this file across all snapshots
	for i := len(s.Snapshots) - 1; i >= 0; i-- {
		if b, ok := s.Snapshots[i].TrackedFileBackups[filePath]; ok {
			version = b.Version + 1
			break
		}
	}

	backup, err := createBackup(sessionDir, filePath, version)
	if err != nil {
		// Log but don't block tool execution
		return
	}

	s.currentBackups[filePath] = backup
	s.TrackedFiles[filePath] = true
}
