package titler

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "titles.json")
	t.Setenv("KIOKU_TITLE_CACHE", path)

	c := LoadCache()
	if _, ok := c.Get("id1", 100, 200); ok {
		t.Fatal("unexpected hit on empty cache")
	}

	c.Set("id1", "テストタイトル", 100, 200)
	if err := c.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	c2 := LoadCache()
	got, ok := c2.Get("id1", 100, 200)
	if !ok || got != "テストタイトル" {
		t.Fatalf("reload mismatch: got %q ok=%v", got, ok)
	}
	if _, ok := c2.Get("id1", 101, 200); ok {
		t.Error("expected miss after size change (stale)")
	}
	if _, ok := c2.Get("id1", 100, 201); ok {
		t.Error("expected miss after mtime change (stale)")
	}
	if _, ok := c2.Get("missing", 1, 1); ok {
		t.Error("expected miss for unknown id")
	}
}

// TestCacheConcurrent exercises Set/Save/Get from many goroutines at once,
// mirroring the background generator writing while the UI reads. Run with -race.
func TestCacheConcurrent(t *testing.T) {
	t.Setenv("KIOKU_TITLE_CACHE", filepath.Join(t.TempDir(), "titles.json"))
	c := LoadCache()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("id%d", i)
			c.Set(id, fmt.Sprintf("タイトル%d", i), int64(i), int64(i))
			if err := c.Save(); err != nil {
				t.Errorf("save: %v", err)
			}
			c.Get(id, int64(i), int64(i))
		}(i)
	}
	wg.Wait()

	if got, ok := c.Get("id7", 7, 7); !ok || got != "タイトル7" {
		t.Errorf("id7 = %q ok=%v", got, ok)
	}
}
