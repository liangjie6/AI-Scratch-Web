package prompt

import (
	"sync"
	"time"
)

// CacheEntry is one cached prompt layer with its expiry.
type CacheEntry struct {
	Value     any
	ExpiresAt float64
}

// Cache is a small process-local cache for the stable prompt layers.
type Cache struct {
	clock   func() float64
	entries map[string]CacheEntry
	lock    sync.Mutex
}

// NewCache builds an empty prompt cache.
func NewCache() *Cache {
	return &Cache{clock: func() float64 { return float64(time.Now().UnixNano()) / 1e9 }, entries: map[string]CacheEntry{}}
}

// NewCacheWithClock builds a cache with an injectable clock (used by tests).
func NewCacheWithClock(clock func() float64) *Cache {
	return &Cache{clock: clock, entries: map[string]CacheEntry{}}
}

// Get returns a cached value when it has not expired.
func (c *Cache) Get(key string) (any, bool) {
	c.lock.Lock()
	defer c.lock.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if entry.ExpiresAt <= c.clock() {
		delete(c.entries, key)
		return nil, false
	}
	return entry.Value, true
}

// GetOrBuild returns a cached value or builds and stores a new one. The second
// result reports whether the value was a cache hit.
func (c *Cache) GetOrBuild(key string, builder func() any, ttl float64) (any, bool) {
	c.lock.Lock()
	defer c.lock.Unlock()
	entry, ok := c.entries[key]
	now := c.clock()
	if ok && entry.ExpiresAt > now {
		return entry.Value, true
	}
	if ok {
		delete(c.entries, key)
	}
	if ttl < 0 {
		ttl = 0
	}
	value := builder()
	c.entries[key] = CacheEntry{Value: value, ExpiresAt: now + ttl}
	return value, false
}

// Set stores a value with a TTL.
func (c *Cache) Set(key string, value any, ttl float64) any {
	c.lock.Lock()
	defer c.lock.Unlock()
	if ttl < 0 {
		ttl = 0
	}
	c.entries[key] = CacheEntry{Value: value, ExpiresAt: c.clock() + ttl}
	return value
}

// Invalidate drops entries with the prefix, or everything when the prefix is empty.
func (c *Cache) Invalidate(prefix string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if prefix == "" {
		c.entries = map[string]CacheEntry{}
		return
	}
	for key := range c.entries {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(c.entries, key)
		}
	}
}

// Clear drops every entry.
func (c *Cache) Clear() { c.Invalidate("") }

// Len reports the number of cached entries.
func (c *Cache) Len() int {
	c.lock.Lock()
	defer c.lock.Unlock()
	return len(c.entries)
}
