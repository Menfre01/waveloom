package hashline

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	xxhash "github.com/cespare/xxhash/v2"
)

// Snapshot 表示一个文件内容快照。
type Snapshot struct {
	TAG     string    // 4 位十六进制，如 "A1B2"
	Content string    // 完整文件内容（TAG 计算时的版本）
	At      time.Time // 快照创建时间
}

// SnapshotStore 维护文件路径 → 快照的映射。
// key = file path (canonical), value = (4-hex TAG, full file content).
type SnapshotStore struct {
	mu   sync.RWMutex
	data map[string]*Snapshot
}

// NewStore 创建一个空的 SnapshotStore。
func NewStore() *SnapshotStore {
	return &SnapshotStore{
		data: make(map[string]*Snapshot),
	}
}

// Record 为给定文件内容生成 TAG 并存入快照。
// 返回 4-hex TAG。若生成的 TAG 与已有快照碰撞（同一 Store 内不同路径不同内容产生相同 TAG），
// 自动重新哈希（追加随机种子）直到唯一，最多重试 3 次；超过返回错误。
func (s *SnapshotStore) Record(path string, content string) (string, error) {
	tag, err := s.generateUniqueTag(content)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	s.data[path] = &Snapshot{
		TAG:     tag,
		Content: content,
		At:      time.Now(),
	}
	s.mu.Unlock()

	return tag, nil
}

// Verify 验证给定文件的当前内容是否与 TAG 对应快照匹配。
// 匹配 → 返回快照内容；不匹配 → 返回错误。
func (s *SnapshotStore) Verify(path string, tag string, currentContent string) (*Snapshot, error) {
	s.mu.RLock()
	snap, ok := s.data[path]
	s.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no snapshot for path %q", path)
	}
	if snap.TAG != tag {
		return nil, fmt.Errorf("TAG mismatch for %q: expected %s, got %s", path, snap.TAG, tag)
	}
	if snap.Content != currentContent {
		return nil, fmt.Errorf("content mismatch for %q (TAG %s): file has been modified since snapshot", path, tag)
	}
	return snap, nil
}

// Update 编辑成功后更新快照（新内容 + 新 TAG）。
func (s *SnapshotStore) Update(path string, content string) string {
	tag := computeTag(content)
	// 确保 TAG 唯一性（简化版，不重试 — 调用方保证 Update 时内容不同）
	s.mu.Lock()
	existing := s.data[path]
	if existing != nil && existing.TAG == tag && existing.Content != content {
		tag = computeTag(content + "\x00")
	}
	s.data[path] = &Snapshot{
		TAG:     tag,
		Content: content,
		At:      time.Now(),
	}
	s.mu.Unlock()
	return tag
}

// Get 返回路径对应的快照（用于恢复模式）。
func (s *SnapshotStore) Get(path string) (*Snapshot, bool) {
	s.mu.RLock()
	snap, ok := s.data[path]
	s.mu.RUnlock()
	return snap, ok
}

// generateUniqueTag 生成唯一 TAG，最多重试 3 次。
func (s *SnapshotStore) generateUniqueTag(content string) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		seed := content
		if attempt > 0 {
			seed = content + string(rune(rand.Intn(256)))
		}
		tag := computeTag(seed)
		if !s.tagExists(tag) {
			return tag, nil
		}
	}
	return "", fmt.Errorf("failed to generate unique TAG after 3 attempts")
}

// tagExists 检查 TAG 是否已在 Store 中存在（不同路径）。
func (s *SnapshotStore) tagExists(tag string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, snap := range s.data {
		if snap.TAG == tag {
			return true
		}
	}
	return false
}

// computeTag 使用 XXH64 低 16 bit 生成 4 位十六进制 TAG。
func computeTag(content string) string {
	h := xxhash.New()
	_, _ = h.Write([]byte(content))
	hash := h.Sum64()
	low16 := uint16(hash & 0xFFFF)
	return fmt.Sprintf("%04X", low16)
}
