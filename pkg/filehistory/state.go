package filehistory

import (
	"sync"
	"time"
)

// FileHistoryState manages file-level backups for rewind support.
// Each session has one FileHistoryState, tracking all files modified by the agent.
type FileHistoryState struct {
	Snapshots    []FileHistorySnapshot // all checkpoints, sorted by time
	TrackedFiles map[string]bool       // all files agent has touched (only grows)
	SnapshotSeq  int                   // monotonically increasing snapshot number

	// currentBackups accumulates backup records within the current turn.
	// TrackEdit writes here; MakeSnapshot consumes and clears.
	currentBackups map[string]FileHistoryBackup
	mu             sync.RWMutex
}

// FileHistorySnapshot represents a checkpoint at a specific user message.
type FileHistorySnapshot struct {
	MessageID          string                       // associated user message UUID
	TrackedFileBackups map[string]FileHistoryBackup // file → backup version
	Timestamp          time.Time
}

// FileHistoryBackup describes a single file backup version.
type FileHistoryBackup struct {
	BackupFileName string    // empty means the file did not exist at this version
	Version        int       // version number, starting from 1
	BackupTime     time.Time
}

// NewState creates a new FileHistoryState.
func NewState() *FileHistoryState {
	return &FileHistoryState{
		TrackedFiles:   make(map[string]bool),
		currentBackups: make(map[string]FileHistoryBackup),
	}
}

// SnapshotCount returns the number of snapshots taken.
func (s *FileHistoryState) SnapshotCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.Snapshots)
}

// GetSnapshots returns a copy of all snapshots (thread-safe).
func (s *FileHistoryState) GetSnapshots() []FileHistorySnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]FileHistorySnapshot, len(s.Snapshots))
	copy(result, s.Snapshots)
	return result
}
