package lru_test

import (
	"testing"

	"github.com/go-delve/delve/pkg/internal/lru"
)

func TestCache_ZeroCapacity(t *testing.T) {
	cache := lru.NewCache[int, string](0)
	cache.Add(1, "one")

	// With zero capacity, nothing should be stored
	if val, ok := cache.Get(1); ok {
		t.Errorf("Get(1) = %v, %v; want '', false (zero capacity)", val, ok)
	}
}

func TestCacheNoEviction(t *testing.T) {
	cache := lru.NewCache[int, string](2)

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

func TestCacheEviction(t *testing.T) {
	cache := lru.NewCache[int, string](2)

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

func TestCacheUpdate(t *testing.T) {
	cache := lru.NewCache[int, string](2)

	cache.Add(1, "one")
	cache.Add(1, "ONE") // Update existing key

	if val, ok := cache.Get(1); !ok || val != "ONE" {
		t.Errorf("Get(1) = %v, %v; want 'ONE', true", val, ok)
	}
}

func TestCacheOrder(t *testing.T) {
	cache := lru.NewCache[int, string](2)

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
