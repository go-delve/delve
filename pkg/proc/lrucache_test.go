package proc_test

import (
	"sync"
	"testing"

	"github.com/go-delve/delve/pkg/proc"
)

func TestLRUCache_ZeroCapacity(t *testing.T) {
	cache := proc.NewLRUCache[int, string](0)

	cache.Add(1, "one")

	// With zero capacity, nothing should be stored
	if val, ok := cache.Get(1); ok {
		t.Errorf("Get(1) = %v, %v; want '', false (zero capacity)", val, ok)
	}
}

func TestLRUCacheNoEviction(t *testing.T) {
	cache := proc.NewLRUCache[int, string](2)

	// Test adding items
	cache.Add(1, "one")
	cache.Add(2, "two")

	// Test getting existing items
	if val, ok := cache.Get(1); !ok || val != "one" {
		t.Errorf("Get(1) = %v, %v; want 'one', true", val, ok)
	}
	if val, ok := cache.Get(2); !ok || val != "two" {
		t.Errorf("Get(2) = %v, %v; want 'two', true", val, ok)
	}

	// Test getting non-existent item
	if val, ok := cache.Get(3); ok {
		t.Errorf("Get(3) = %v, %v; want '', false", val, ok)
	}
}

func TestLRUCacheEviction(t *testing.T) {
	cache := proc.NewLRUCache[int, string](2)

	// Add items up to capacity
	cache.Add(1, "one")
	cache.Add(2, "two")

	// Add third item, should evict the least recently used (1)
	cache.Add(3, "three")

	// Item 1 should be evicted
	if val, ok := cache.Get(1); ok {
		t.Errorf("Get(1) = %v, %v; want '', false (should be evicted)", val, ok)
	}

	// Items 2 and 3 should still exist
	if val, ok := cache.Get(2); !ok || val != "two" {
		t.Errorf("Get(2) = %v, %v; want 'two', true", val, ok)
	}
	if val, ok := cache.Get(3); !ok || val != "three" {
		t.Errorf("Get(3) = %v, %v; want 'three', true", val, ok)
	}
}

func TestLRUCacheUpdate(t *testing.T) {
	cache := proc.NewLRUCache[int, string](2)

	cache.Add(1, "one")
	cache.Add(1, "ONE") // Update existing key

	if val, ok := cache.Get(1); !ok || val != "ONE" {
		t.Errorf("Get(1) = %v, %v; want 'ONE', true", val, ok)
	}
}

func TestLRUCacheOrder(t *testing.T) {
	cache := proc.NewLRUCache[int, string](2)

	// Add two items
	cache.Add(1, "one")
	cache.Add(2, "two")

	// Access item 1, making it more recently used
	cache.Get(1)

	// Add third item, should evict item 2 (least recently used)
	cache.Add(3, "three")

	// Item 2 should be evicted
	if val, ok := cache.Get(2); ok {
		t.Errorf("Get(2) = %v, %v; want '', false (should be evicted)", val, ok)
	}

	// Items 1 and 3 should still exist
	if val, ok := cache.Get(1); !ok || val != "one" {
		t.Errorf("Get(1) = %v, %v; want 'one', true", val, ok)
	}
	if val, ok := cache.Get(3); !ok || val != "three" {
		t.Errorf("Get(3) = %v, %v; want 'three', true", val, ok)
	}
}

func TestLRUCacheConcurrent(t *testing.T) {
	// Test passes if no race conditions occur.
	cache := proc.NewLRUCache[int, int](100)

	var wg sync.WaitGroup

	// Concurrent writes
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				cache.Add(i*100+j, i)
			}
		}()
	}

	// Concurrent reads
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 100 {
				cache.Get(i*100 + j)
			}
		}()
	}

	wg.Wait()
}
