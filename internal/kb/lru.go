// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"container/list"
	"sync"
	"time"
)

// lru is a byte-capped LRU with optional per-entry expiry. Sizes are caller
// estimates (key + value + bookkeeping), so the cap bounds real memory use.
type lru[V any] struct {
	mu    sync.Mutex
	max   int
	size  int
	ll    *list.List
	items map[string]*list.Element
	now   func() time.Time
}

type lruEntry[V any] struct {
	key     string
	val     V
	size    int
	expires time.Time
}

const lruOverhead = 96 // list element + map slot + entry header, roughly

func newLRU[V any](maxBytes int) *lru[V] {
	return &lru[V]{max: maxBytes, ll: list.New(), items: map[string]*list.Element{}, now: time.Now}
}

// Get returns the value when present and not expired.
func (c *lru[V]) Get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var zero V
	el, ok := c.items[key]
	if !ok {
		return zero, false
	}
	e := el.Value.(*lruEntry[V])
	if !e.expires.IsZero() && c.now().After(e.expires) {
		c.remove(el)
		return zero, false
	}
	c.ll.MoveToFront(el)
	return e.val, true
}

// Put stores val with an estimated size of size bytes; ttl 0 = no expiry.
func (c *lru[V]) Put(key string, val V, size int, ttl time.Duration) {
	size += len(key) + lruOverhead
	if size > c.max {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.remove(el)
	}
	e := &lruEntry[V]{key: key, val: val, size: size}
	if ttl > 0 {
		e.expires = c.now().Add(ttl)
	}
	c.items[key] = c.ll.PushFront(e)
	c.size += size
	for c.size > c.max {
		c.remove(c.ll.Back())
	}
}

// Add stores key only if it is absent (or expired) and reports whether it did.
func (c *lru[V]) Add(key string, val V, size int, ttl time.Duration) bool {
	c.mu.Lock()
	if el, ok := c.items[key]; ok {
		e := el.Value.(*lruEntry[V])
		if e.expires.IsZero() || !c.now().After(e.expires) {
			c.mu.Unlock()
			return false
		}
	}
	c.mu.Unlock()
	c.Put(key, val, size, ttl)
	return true
}

// Purge drops everything.
func (c *lru[V]) Purge() {
	c.mu.Lock()
	c.ll.Init()
	c.items = map[string]*list.Element{}
	c.size = 0
	c.mu.Unlock()
}

// Bytes is the current estimated size.
func (c *lru[V]) Bytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.size
}

// Len is the number of entries.
func (c *lru[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

func (c *lru[V]) remove(el *list.Element) {
	e := el.Value.(*lruEntry[V])
	c.ll.Remove(el)
	delete(c.items, e.key)
	c.size -= e.size
}
