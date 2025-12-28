package lru

import (
	"container/list"
)

// Cache is a simple LRU (Least Recently Used) cache implementation.
type Cache[K comparable, V any] struct {
	capacity  int
	items     map[K]*list.Element
	evictList *list.List
}

type entry[K comparable, V any] struct {
	key   K
	value V
}

// NewCache creates a new LRU cache with the given capacity.
func NewCache[K comparable, V any](capacity int) *Cache[K, V] {
	return &Cache[K, V]{
		capacity:  capacity,
		items:     make(map[K]*list.Element),
		evictList: list.New(),
	}
}

// Add adds a value to the cache. If the key already exists, it updates
// the value and moves it to the front. If the cache is at capacity,
// it evicts the least recently used item.
func (c *Cache[K, V]) Add(key K, value V) {
	// Check if the key already exists.
	if elem, ok := c.items[key]; ok {
		c.evictList.MoveToFront(elem)
		elem.Value.(*entry[K, V]).value = value
		return
	}

	// Add new item.
	c.items[key] = c.evictList.PushFront(&entry[K, V]{key: key, value: value})

	// Evict oldest if we exceeded capacity.
	if c.evictList.Len() > c.capacity {
		if elem := c.evictList.Back(); elem != nil {
			c.evictList.Remove(elem)
			ent := elem.Value.(*entry[K, V])
			delete(c.items, ent.key)
		}
	}
}

// Get retrieves a value from the cache. If found, it moves the item
// to the front (marking it as recently used) and returns (value, true).
// If not found, returns (zero value, false).
func (c *Cache[K, V]) Get(key K) (V, bool) {
	if elem, ok := c.items[key]; ok {
		c.evictList.MoveToFront(elem)
		return elem.Value.(*entry[K, V]).value, true
	}

	var zero V
	return zero, false
}
