// Package cache provides a small, bounded, concurrency-safe LRU. The always-on daemon must hold
// memory flat (AGENTS.md: <25MB RSS), so every in-process cache is capacity-bounded by design —
// it evicts the least-recently-used entry instead of growing without limit.
package cache

import (
	"container/list"
	"sync"
)

// LRU is a fixed-capacity least-recently-used cache safe for concurrent use.
type LRU[K comparable, V any] struct {
	mu       sync.Mutex
	capacity int
	ll       *list.List // front = most recently used
	items    map[K]*list.Element
}

type entry[K comparable, V any] struct {
	key K
	val V
}

// New returns an LRU holding at most capacity entries. A capacity <= 0 is treated as 1.
func New[K comparable, V any](capacity int) *LRU[K, V] {
	if capacity <= 0 {
		capacity = 1
	}
	return &LRU[K, V]{
		capacity: capacity,
		ll:       list.New(),
		items:    make(map[K]*list.Element, capacity),
	}
}

// Get returns the value for key and marks it most-recently-used.
func (c *LRU[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*entry[K, V]).val, true
	}
	var zero V
	return zero, false
}

// Contains reports membership without changing recency.
func (c *LRU[K, V]) Contains(key K) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.items[key]
	return ok
}

// Add inserts or updates key, marking it most-recently-used and evicting the LRU entry if full.
func (c *LRU[K, V]) Add(key K, val V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		el.Value.(*entry[K, V]).val = val
		return
	}
	el := c.ll.PushFront(&entry[K, V]{key: key, val: val})
	c.items[key] = el
	if c.ll.Len() > c.capacity {
		c.evictOldest()
	}
}

// Len returns the current number of entries.
func (c *LRU[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

func (c *LRU[K, V]) evictOldest() {
	el := c.ll.Back()
	if el == nil {
		return
	}
	c.ll.Remove(el)
	delete(c.items, el.Value.(*entry[K, V]).key)
}
