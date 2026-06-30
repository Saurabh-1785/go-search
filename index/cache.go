package index

import (
	"container/list"
	"fmt"
	"sync"
)

// ScoredDoc is a document with its BM25 score.
// This is what the cache stores — scoring results only, no presentation.
type ScoredDoc struct {
	DocID uint32
	Score float64
}

// cacheEntry stores the cached scoring result for a query.
type cacheEntry struct {
	key       string
	docs      []ScoredDoc
	totalHits int
}

// SearchCache is a thread-safe LRU cache for search results.
type SearchCache struct {
	mu       sync.Mutex
	capacity int
	items    map[string]*list.Element
	order    *list.List // front = most recently used, back = least recently used
	hits     int64
	misses   int64
}

// CacheStats holds cache performance metrics.
type CacheStats struct {
	Size     int     `json:"size"`
	Capacity int     `json:"capacity"`
	Hits     int64   `json:"hits"`
	Misses   int64   `json:"misses"`
	HitRate  float64 `json:"hit_rate"` // hits / (hits + misses)
}

// NewSearchCache creates an LRU cache with the given capacity.
func NewSearchCache(capacity int) *SearchCache {
	if capacity <= 0 {
		capacity = 128
	}
	return &SearchCache{
		capacity: capacity,
		items:    make(map[string]*list.Element, capacity),
		order:    list.New(),
	}
}

// CacheKey builds a cache key from query parameters.
func CacheKey(query string, topK int, mode string) string {
	return fmt.Sprintf("%s|%d|%s", query, topK, mode)
}

// Get retrieves a cached result by key.
func (c *SearchCache) Get(key string) ([]ScoredDoc, int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, exists := c.items[key]
	if !exists {
		c.misses++
		return nil, 0, false
	}

	c.order.MoveToFront(elem)
	entry := elem.Value.(*cacheEntry)
	c.hits++

	docs := make([]ScoredDoc, len(entry.docs))
	copy(docs, entry.docs)

	return docs, entry.totalHits, true
}

// Put inserts or updates a cached result.
func (c *SearchCache) Put(key string, docs []ScoredDoc, totalHits int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, exists := c.items[key]; exists {
		c.order.MoveToFront(elem)
		entry := elem.Value.(*cacheEntry)
		entry.docs = docs
		entry.totalHits = totalHits
		return
	}

	if c.order.Len() >= c.capacity {
		tail := c.order.Back()
		if tail != nil {
			evicted := c.order.Remove(tail).(*cacheEntry)
			delete(c.items, evicted.key)
		}
	}

	entry := &cacheEntry{
		key:       key,
		docs:      docs,
		totalHits: totalHits,
	}
	elem := c.order.PushFront(entry)
	c.items[key] = elem
}

// Invalidate clears the cache.
func (c *SearchCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*list.Element, c.capacity)
	c.order.Init()
}

// Stats returns current cache performance metrics.
func (c *SearchCache) Stats() CacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()

	hitRate := 0.0
	total := c.hits + c.misses
	if total > 0 {
		hitRate = float64(c.hits) / float64(total)
	}

	return CacheStats{
		Size:     c.order.Len(),
		Capacity: c.capacity,
		Hits:     c.hits,
		Misses:   c.misses,
		HitRate:  hitRate,
	}
}
