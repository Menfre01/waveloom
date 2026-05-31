package permission

import "sync"

// ---------------------------------------------------------------------------
// DenialTracker — 拒绝跟踪
// ---------------------------------------------------------------------------

// DenialTracker 跟踪连续和累计拒绝次数。
// 当连续拒绝达到上限时，应回退到更强制的策略（如终止循环）。
type DenialTracker struct {
	mu             sync.Mutex
	consecutive    int
	total          int
	maxConsecutive int
	maxTotal       int
}

// NewDenialTracker 创建一个拒绝跟踪器。
// 默认 maxConsecutive=3, maxTotal=10。
func NewDenialTracker() *DenialTracker {
	return &DenialTracker{
		maxConsecutive: 3,
		maxTotal:       10,
	}
}

// RecordDenial 记录一次拒绝。
// 返回 true 表示已达上限（连续或累计），应采取更强制的策略。
func (d *DenialTracker) RecordDenial() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.consecutive++
	d.total++
	return d.consecutive >= d.maxConsecutive || d.total >= d.maxTotal
}

// RecordAllow 记录一次允许，重置连续拒绝计数。
func (d *DenialTracker) RecordAllow() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.consecutive = 0
}

// Consecutive 返回当前连续拒绝次数。
func (d *DenialTracker) Consecutive() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.consecutive
}

// Total 返回总拒绝次数。
func (d *DenialTracker) Total() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.total
}

// Reset 重置所有计数器。
func (d *DenialTracker) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.consecutive = 0
	d.total = 0
}

// AtLimit 检查是否已达上限，不修改状态。
func (d *DenialTracker) AtLimit() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.consecutive >= d.maxConsecutive || d.total >= d.maxTotal
}
