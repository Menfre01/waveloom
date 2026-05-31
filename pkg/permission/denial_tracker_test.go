package permission

import (
	"sync"
	"testing"
)

func TestDenialTracker_Initial(t *testing.T) {
	d := NewDenialTracker()
	if d.Consecutive() != 0 {
		t.Errorf("initial consecutive = %d, want 0", d.Consecutive())
	}
	if d.Total() != 0 {
		t.Errorf("initial total = %d, want 0", d.Total())
	}
	if d.AtLimit() {
		t.Error("initial AtLimit = true, want false")
	}
}

func TestDenialTracker_ConsecutiveDenial(t *testing.T) {
	d := NewDenialTracker()

	// 连续拒绝 2 次，未达上限
	if d.RecordDenial() {
		t.Error("1st RecordDenial should not be at limit")
	}
	if d.RecordDenial() {
		t.Error("2nd RecordDenial should not be at limit")
	}
	if d.Consecutive() != 2 {
		t.Errorf("consecutive = %d, want 2", d.Consecutive())
	}

	// 第 3 次拒绝达到上限
	if !d.RecordDenial() {
		t.Error("3rd RecordDenial should be at limit")
	}
	if d.Consecutive() != 3 {
		t.Errorf("consecutive = %d, want 3", d.Consecutive())
	}
}

func TestDenialTracker_AllowResetsConsecutive(t *testing.T) {
	d := NewDenialTracker()

	d.RecordDenial()
	d.RecordDenial()
	if d.Consecutive() != 2 {
		t.Errorf("consecutive = %d, want 2", d.Consecutive())
	}

	d.RecordAllow()
	if d.Consecutive() != 0 {
		t.Errorf("after allow, consecutive = %d, want 0", d.Consecutive())
	}
	if d.Total() != 2 {
		t.Errorf("after allow, total = %d, want 2", d.Total())
	}
}

func TestDenialTracker_TotalLimit(t *testing.T) {
	d := NewDenialTracker()

	// 连续拒绝 + allow 交替，累计到 total 上限
	for i := 0; i < 9; i++ {
		d.RecordDenial()
		d.RecordAllow()
	}
	// total = 9, consecutive = 0
	if d.AtLimit() {
		t.Error("total=9 should not be at limit (maxTotal=10)")
	}

	// 第 10 次拒绝达到 total 上限
	d.RecordDenial()
	if d.Total() != 10 {
		t.Errorf("total = %d, want 10", d.Total())
	}
	if !d.AtLimit() {
		t.Error("total=10 should be at limit")
	}
}

func TestDenialTracker_Reset(t *testing.T) {
	d := NewDenialTracker()

	d.RecordDenial()
	d.RecordDenial()
	d.RecordDenial()

	d.Reset()
	if d.Consecutive() != 0 {
		t.Errorf("after reset, consecutive = %d, want 0", d.Consecutive())
	}
	if d.Total() != 0 {
		t.Errorf("after reset, total = %d, want 0", d.Total())
	}
	if d.AtLimit() {
		t.Error("after reset, AtLimit should be false")
	}
}

func TestDenialTracker_ConcurrentSafety(t *testing.T) {
	d := NewDenialTracker()
	var wg sync.WaitGroup

	// 50 个 goroutine 同时 RecordDenial
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.RecordDenial()
		}()
	}

	// 50 个 goroutine 同时 RecordAllow
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.RecordAllow()
		}()
	}

	wg.Wait()

	// 不检查具体值，只验证不 panic 且 total 在合理范围内
	total := d.Total()
	if total < 0 || total > 50 {
		t.Errorf("total = %d, want between 0 and 50", total)
	}
}
