package tool
import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"
)
type readStateKey struct{}
// WithReadState injects a ReadStateStore into ctx.
func WithReadState(ctx context.Context, store *ReadStateStore) context.Context {
	return context.WithValue(ctx, readStateKey{}, store)
}
// ReadStateFromContext extracts the ReadStateStore from ctx.
func ReadStateFromContext(ctx context.Context) *ReadStateStore {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(readStateKey{}).(*ReadStateStore)
	return s
}

// FileReadState records when a file was last read and its content at that time.
// Used by the edit tool to detect external modifications between read and edit.
type FileReadState struct {
	Content string
	MTime   time.Time
}

// ReadStateStore tracks read state per file, shared across turns within a session.
type ReadStateStore struct {
	mu   sync.RWMutex
	data map[string]*FileReadState
}

// NewReadStateStore creates an empty read state store.
func NewReadStateStore() *ReadStateStore {
	return &ReadStateStore{data: make(map[string]*FileReadState)}
}

// Record stores the read state for a file.
func (s *ReadStateStore) Record(path, content string) {
	s.mu.Lock()
	// key 统一 Clean 归一化,防止相对/绝对、./、尾斜杠等写法差异导致
	// edit 校验 miss(REGRESSION: parsePatchFiles 曾产出双重嵌套路径)。
	s.data[filepath.Clean(path)] = &FileReadState{Content: content, MTime: time.Now()}
	s.mu.Unlock()
}

// Get returns the stored state for a file, or nil if not recorded.
func (s *ReadStateStore) Get(path string) *FileReadState {
	s.mu.RLock()
	state := s.data[filepath.Clean(path)]
	s.mu.RUnlock()
	return state
}

// Validate checks whether a file is safe to edit based on its read state.
// Returns ok=true if the file has been read and hasn't been modified externally.
// Returns a reason string when not ok.
func (s *ReadStateStore) Validate(path string) (ok bool, reason string) {
	s.mu.RLock()
	cleanPath := filepath.Clean(path)
	state := s.data[cleanPath]
	s.mu.RUnlock()

	if state == nil {
		// 归因明确:主因是 edit 前未 read(路径双重嵌套已容错)。
		// 展示解析后的路径,排除 header 路径写法的干扰。
		return false, "file has not been read yet — call read on this file BEFORE edit (resolved: " + cleanPath +
			"; already read? check the '*** Update File:' header — omit it for single-file edits)"
	}

	info, err := os.Stat(path)
	if err != nil {
		return false, "cannot stat file: " + err.Error()
	}

	if !info.ModTime().After(state.MTime) {
		return true, ""
	}

	// mtime is newer — check if content actually changed (Windows time drift)
	data, err := os.ReadFile(path)
	if err != nil {
		return false, "cannot read file: " + err.Error()
	}
	if string(data) == state.Content {
		return true, "" // time drift, content unchanged
	}

	return false, "file has been modified since read — use read tool to get current content"
}

// Update refreshes the read state after a successful edit.
func (s *ReadStateStore) Update(path, content string) {
	s.mu.Lock()
	s.data[filepath.Clean(path)] = &FileReadState{Content: content, MTime: time.Now()}
	s.mu.Unlock()
}
