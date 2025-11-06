package proc

import (
	"container/list"
	"sync"
)

// lruCache is a simple LRU (Least Recently Used) cache implementation.
type lruCache[K comparable, V any] struct {
	mu        sync.Mutex
	capacity  int
	items     map[K]*list.Element
	evictList *list.List
}

type lruEntry[K comparable, V any] struct {
	key   K
	value V
}

// newLRUCache creates a new LRU cache with the given capacity.
func newLRUCache[K comparable, V any](capacity int) *lruCache[K, V] {
	return &lruCache[K, V]{
		capacity:  capacity,
		items:     make(map[K]*list.Element),
		evictList: list.New(),
	}
}

// Add adds a value to the cache. If the key already exists, it updates
// the value and moves it to the front. If the cache is at capacity,
// it evicts the least recently used item.
func (c *lruCache[K, V]) Add(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if the key already exists.
	if elem, ok := c.items[key]; ok {
		c.evictList.MoveToFront(elem)
		elem.Value.(*lruEntry[K, V]).value = value
		return
	}

	// Add new item.
	c.items[key] = c.evictList.PushFront(&lruEntry[K, V]{key: key, value: value})

	// Evict oldest if we exceeded capacity.
	if c.evictList.Len() > c.capacity {
		if elem := c.evictList.Back(); elem != nil {
			c.evictList.Remove(elem)
			ent := elem.Value.(*lruEntry[K, V])
			delete(c.items, ent.key)
		}
	}
}

// Get retrieves a value from the cache. If found, it moves the item
// to the front (marking it as recently used) and returns (value, true).
// If not found, returns (zero value, false).
func (c *lruCache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		c.evictList.MoveToFront(elem)
		return elem.Value.(*lruEntry[K, V]).value, true
	}

	var zero V
	return zero, false
}
