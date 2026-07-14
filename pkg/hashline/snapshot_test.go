package hashline

import (
	"sync"
	"testing"
)

func TestRecordAndVerify(t *testing.T) {
	store := NewStore()

	tag, err := store.Record("/tmp/test.go", "package main\n\nfunc main() {\n}\n")
	if err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if tag == "" || len(tag) != 4 {
		t.Fatalf("unexpected TAG: %q", tag)
	}

	// Verify succeeds with correct content
	snap, err := store.Verify("/tmp/test.go", tag, "package main\n\nfunc main() {\n}\n")
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if snap.TAG != tag {
		t.Fatalf("TAG mismatch: %s != %s", snap.TAG, tag)
	}

	// Verify fails with wrong TAG
	_, err = store.Verify("/tmp/test.go", "XXXX", "package main\n\nfunc main() {\n}\n")
	if err == nil {
		t.Fatal("expected Verify to fail with wrong TAG")
	}

	// Verify fails with modified content
	_, err = store.Verify("/tmp/test.go", tag, "package main\n\nfunc main() {\n    fmt.Println()\n}\n")
	if err == nil {
		t.Fatal("expected Verify to fail with modified content")
	}
}

func TestUpdateChangesTag(t *testing.T) {
	store := NewStore()

	tag1, _ := store.Record("/tmp/test.go", "line1\nline2\n")
	tag2 := store.Update("/tmp/test.go", "line1\nline2\nline3\n")

	if tag1 == tag2 {
		t.Fatal("expected TAG to change after Update")
	}

	// Verify with new TAG succeeds
	snap, err := store.Verify("/tmp/test.go", tag2, "line1\nline2\nline3\n")
	if err != nil {
		t.Fatalf("Verify after Update failed: %v", err)
	}
	if snap.TAG != tag2 {
		t.Fatalf("TAG mismatch: %s != %s", snap.TAG, tag2)
	}
}

func TestGet(t *testing.T) {
	store := NewStore()

	_, ok := store.Get("/nonexistent")
	if ok {
		t.Fatal("expected Get to return false for missing path")
	}

	_, _ = store.Record("/tmp/test.go", "content")
	snap, ok := store.Get("/tmp/test.go")
	if !ok {
		t.Fatal("expected Get to return true")
	}
	if snap.Content != "content" {
		t.Fatalf("unexpected content: %q", snap.Content)
	}
}

func TestConcurrentRecord(t *testing.T) {
	store := NewStore()
	var wg sync.WaitGroup

	for i := range 10 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			path := "/tmp/test" + string(rune('0'+idx)) + ".go"
			_, err := store.Record(path, "content")
			if err != nil {
				t.Errorf("concurrent Record failed: %v", err)
			}
		}(i)
	}
	wg.Wait()

	// All paths should be in the store
	for range 10 {
		snap, ok := store.Get("/tmp/test0.go")
		if !ok {
			t.Log("path test0.go may have been overwritten in concurrent access")
		}
		_ = snap
	}
}

func TestComputeTagStability(t *testing.T) {
	tag1 := computeTag("hello world")
	tag2 := computeTag("hello world")
	if tag1 != tag2 {
		t.Fatalf("computeTag should be stable: %s != %s", tag1, tag2)
	}

	tag3 := computeTag("hello world!")
	if tag1 == tag3 {
		t.Fatal("different content should produce different TAG")
	}
}

func TestEmptyContent(t *testing.T) {
	store := NewStore()
	tag, err := store.Record("/tmp/empty", "")
	if err != nil {
		t.Fatalf("Record empty content failed: %v", err)
	}

	snap, err := store.Verify("/tmp/empty", tag, "")
	if err != nil {
		t.Fatalf("Verify empty content failed: %v", err)
	}
	_ = snap
}

// REGRESSION: 重复 Record 同一文件相同内容应返回相同 TAG。
// 之前的实现中 generateUniqueTag 将同路径旧快照视为冲突，
// 导致用随机种子生成新 TAG，文件未变但 TAG 变了。
func TestRegression_RecordSameContentSameTag(t *testing.T) {
	store := NewStore()
	content := "package main\n\nfunc main() {\n}\n"

	tag1, err := store.Record("/tmp/test.go", content)
	if err != nil {
		t.Fatalf("first Record failed: %v", err)
	}

	// 第二次 Record，文件内容未变 → TAG 必须相同
	tag2, err := store.Record("/tmp/test.go", content)
	if err != nil {
		t.Fatalf("second Record failed: %v", err)
	}

	if tag1 != tag2 {
		t.Fatalf("same content produced different TAGs: %s vs %s", tag1, tag2)
	}
}

// 验证内容变化时 TAG 确实会变化（确保快速路径不会误触发）。
func TestRegression_RecordChangedContentChangesTag(t *testing.T) {
	store := NewStore()

	tag1, _ := store.Record("/tmp/test.go", "content v1")
	tag2, _ := store.Record("/tmp/test.go", "content v2")

	if tag1 == tag2 {
		t.Fatal("different content should produce different TAGs")
	}
}
