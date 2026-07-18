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

// SnapshotData 是 FileHistoryState 的可序列化快照。
type SnapshotData struct {
	Snapshots    []SnapshotEntry            `json:"snapshots"`
	TrackedFiles []string                   `json:"tracked_files"`
	SnapshotSeq  int                        `json:"snapshot_seq"`
}

// SnapshotEntry 是单个快照的序列化形式。
type SnapshotEntry struct {
	MessageID          string                 `json:"message_id"`
	TrackedFileBackups map[string]BackupEntry `json:"tracked_file_backups"`
}

// BackupEntry 是单个文件备份的序列化形式。
type BackupEntry struct {
	BackupFileName string `json:"backup_file_name"`
	Version        int    `json:"version"`
}

// ExportSnapshot 导出当前状态用于持久化。
func (s *FileHistoryState) ExportSnapshot() *SnapshotData {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshots := make([]SnapshotEntry, len(s.Snapshots))
	for i, snap := range s.Snapshots {
		backups := make(map[string]BackupEntry, len(snap.TrackedFileBackups))
		for fp, b := range snap.TrackedFileBackups {
			backups[fp] = BackupEntry{
				BackupFileName: b.BackupFileName,
				Version:        b.Version,
			}
		}
		snapshots[i] = SnapshotEntry{
			MessageID:          snap.MessageID,
			TrackedFileBackups: backups,
		}
	}

	tracked := make([]string, 0, len(s.TrackedFiles))
	for fp := range s.TrackedFiles {
		tracked = append(tracked, fp)
	}

	return &SnapshotData{
		Snapshots:    snapshots,
		TrackedFiles: tracked,
		SnapshotSeq:  s.SnapshotSeq,
	}
}

// ImportSnapshot 从持久化数据恢复状态。
func (s *FileHistoryState) ImportSnapshot(data *SnapshotData) {
	if data == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	snapshots := make([]FileHistorySnapshot, len(data.Snapshots))
	for i, entry := range data.Snapshots {
		backups := make(map[string]FileHistoryBackup, len(entry.TrackedFileBackups))
		for fp, b := range entry.TrackedFileBackups {
			backups[fp] = FileHistoryBackup{
				BackupFileName: b.BackupFileName,
				Version:        b.Version,
			}
		}
		snapshots[i] = FileHistorySnapshot{
			MessageID:          entry.MessageID,
			TrackedFileBackups: backups,
		}
	}

	s.Snapshots = snapshots
	s.SnapshotSeq = data.SnapshotSeq

	s.TrackedFiles = make(map[string]bool, len(data.TrackedFiles))
	for _, fp := range data.TrackedFiles {
		s.TrackedFiles[fp] = true
	}
}
