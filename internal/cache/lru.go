// Package cache provides caching utilities for query results and embeddings.
package cache

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// Cache is a generic cache interface.
type Cache interface {
	Get(ctx context.Context, key string) (interface{}, bool)
	Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
	Clear() error
	Size() int
}

// LRUCache implements an LRU (Least Recently Used) cache with TTL support.
type LRUCache struct {
	mu         sync.RWMutex
	maxSize    int
	items      map[string]*list.Element
	evictList  *list.List
	ttls       map[string]time.Time
	defaultTTL time.Duration
	onEvict    func(key string, value interface{})
}

// entry represents a cache entry.
type entry struct {
	key   string
	value interface{}
}

// LRUCacheOption configures the LRU cache.
type LRUCacheOption func(*LRUCache)

// WithMaxSize sets the maximum cache size.
func WithMaxSize(size int) LRUCacheOption {
	return func(c *LRUCache) {
		c.maxSize = size
	}
}

// WithDefaultTTL sets the default TTL for cache entries.
func WithDefaultTTL(ttl time.Duration) LRUCacheOption {
	return func(c *LRUCache) {
		c.defaultTTL = ttl
	}
}

// WithOnEvict sets a callback for when entries are evicted.
func WithOnEvict(fn func(key string, value interface{})) LRUCacheOption {
	return func(c *LRUCache) {
		c.onEvict = fn
	}
}

// NewLRUCache creates a new LRU cache.
func NewLRUCache(opts ...LRUCacheOption) *LRUCache {
	c := &LRUCache{
		maxSize:    1000,
		items:      make(map[string]*list.Element),
		evictList:  list.New(),
		ttls:       make(map[string]time.Time),
		defaultTTL: 5 * time.Minute,
	}

	for _, opt := range opts {
		opt(c)
	}

	// Start background cleanup goroutine
	go c.cleanupExpired()

	return c
}

// Get retrieves a value from the cache.
func (c *LRUCache) Get(ctx context.Context, key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if expired
	if exp, exists := c.ttls[key]; exists && time.Now().After(exp) {
		c.removeElement(key)
		return nil, false
	}

	if elem, exists := c.items[key]; exists {
		c.evictList.MoveToFront(elem)
		return elem.Value.(*entry).value, true
	}
	return nil, false
}

// Set stores a value in the cache.
func (c *LRUCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Use default TTL if not specified
	if ttl == 0 {
		ttl = c.defaultTTL
	}

	// Check if key already exists
	if elem, exists := c.items[key]; exists {
		c.evictList.MoveToFront(elem)
		elem.Value.(*entry).value = value
		c.ttls[key] = time.Now().Add(ttl)
		return nil
	}

	// Add new entry
	elem := c.evictList.PushFront(&entry{key: key, value: value})
	c.items[key] = elem
	c.ttls[key] = time.Now().Add(ttl)

	// Evict if over capacity
	for len(c.items) > c.maxSize {
		c.evictOldest()
	}

	return nil
}

// Delete removes a key from the cache.
func (c *LRUCache) Delete(ctx context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.removeElement(key)
	return nil
}

// Clear removes all entries from the cache.
func (c *LRUCache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key := range c.items {
		c.removeElementLocked(key)
	}
	return nil
}

// Size returns the number of entries in the cache.
func (c *LRUCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// evictOldest removes the oldest entry.
func (c *LRUCache) evictOldest() {
	elem := c.evictList.Back()
	if elem != nil {
		entry := elem.Value.(*entry)
		c.removeElementLocked(entry.key)
	}
}

// removeElement removes an element without locking.
func (c *LRUCache) removeElement(key string) {
	if elem, exists := c.items[key]; exists {
		c.evictList.Remove(elem)
		delete(c.items, key)
		delete(c.ttls, key)
		if c.onEvict != nil {
			c.onEvict(key, elem.Value.(*entry).value)
		}
	}
}

// removeElementLocked removes an element (called when already locked).
func (c *LRUCache) removeElementLocked(key string) {
	if elem, exists := c.items[key]; exists {
		c.evictList.Remove(elem)
		delete(c.items, key)
		delete(c.ttls, key)
		if c.onEvict != nil {
			c.onEvict(key, elem.Value.(*entry).value)
		}
	}
}

// cleanupExpired periodically removes expired entries.
func (c *LRUCache) cleanupExpired() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for key, exp := range c.ttls {
			if now.After(exp) {
				c.removeElementLocked(key)
			}
		}
		c.mu.Unlock()
	}
}

// EmbeddingCache caches embedding vectors for queries.
type EmbeddingCache struct {
	cache *LRUCache
}

// NewEmbeddingCache creates a new embedding cache.
func NewEmbeddingCache(maxSize int) *EmbeddingCache {
	return &EmbeddingCache{
		cache: NewLRUCache(WithMaxSize(maxSize), WithDefaultTTL(10*time.Minute)),
	}
}

// GetEmbedding retrieves a cached embedding.
func (c *EmbeddingCache) GetEmbedding(ctx context.Context, text string) ([]float32, bool) {
	key := hashText(text)
	val, found := c.cache.Get(ctx, key)
	if !found {
		return nil, false
	}
	embedding, ok := val.([]float32)
	if !ok {
		return nil, false
	}
	return embedding, true
}

// SetEmbedding stores an embedding in the cache.
func (c *EmbeddingCache) SetEmbedding(ctx context.Context, text string, embedding []float32) error {
	key := hashText(text)
	return c.cache.Set(ctx, key, embedding, 0)
}

// SearchResultCache caches search results.
type SearchResultCache struct {
	cache *LRUCache
}

// NewSearchResultCache creates a new search result cache.
func NewSearchResultCache(maxSize int, defaultTTL time.Duration) *SearchResultCache {
	return &SearchResultCache{
		cache: NewLRUCache(WithMaxSize(maxSize), WithDefaultTTL(defaultTTL)),
	}
}

// CachedResult represents a cached search result.
type CachedResult struct {
	QueryHash string
	Results   interface{}
	CreatedAt time.Time
}

// Get retrieves cached search results.
func (c *SearchResultCache) Get(ctx context.Context, query string, projectIDs []string) (interface{}, bool) {
	key := hashQuery(query, projectIDs)
	val, found := c.cache.Get(ctx, key)
	if !found {
		return nil, false
	}
	result, ok := val.(*CachedResult)
	if !ok {
		return nil, false
	}
	return result.Results, true
}

// Set stores search results in the cache.
func (c *SearchResultCache) Set(ctx context.Context, query string, projectIDs []string, results interface{}) error {
	key := hashQuery(query, projectIDs)
	cached := &CachedResult{
		QueryHash: key,
		Results:   results,
		CreatedAt: time.Now(),
	}
	return c.cache.Set(ctx, key, cached, 0)
}

// InvalidateByProject removes all cached results for a project.
func (c *SearchResultCache) InvalidateByProject(projectID string) {
	// For simplicity, clear the entire cache when a project is modified
	// A more sophisticated implementation would track which keys belong to which project
	c.cache.Clear()
}

// hashText creates a hash key for text.
func hashText(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

// hashQuery creates a hash key for a query with project IDs.
func hashQuery(query string, projectIDs []string) string {
	h := sha256.New()
	h.Write([]byte(query))
	for _, pid := range projectIDs {
		h.Write([]byte(pid))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Stats returns cache statistics.
type Stats struct {
	Size     int
	HitRate  float64
	MissRate float64
}

// StatsCache wraps a cache to track hit/miss statistics.
type StatsCache struct {
	cache  Cache
	hits   int64
	misses int64
	mu     sync.Mutex
}

// NewStatsCache creates a cache wrapper that tracks statistics.
func NewStatsCache(cache Cache) *StatsCache {
	return &StatsCache{cache: cache}
}

// Get retrieves a value and tracks the hit/miss.
func (c *StatsCache) Get(ctx context.Context, key string) (interface{}, bool) {
	val, found := c.cache.Get(ctx, key)
	c.mu.Lock()
	defer c.mu.Unlock()
	if found {
		c.hits++
	} else {
		c.misses++
	}
	return val, found
}

// Set stores a value.
func (c *StatsCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	return c.cache.Set(ctx, key, value, ttl)
}

// Delete removes a key.
func (c *StatsCache) Delete(ctx context.Context, key string) error {
	return c.cache.Delete(ctx, key)
}

// Clear removes all entries.
func (c *StatsCache) Clear() error {
	return c.cache.Clear()
}

// Size returns the cache size.
func (c *StatsCache) Size() int {
	return c.cache.Size()
}

// GetStats returns the cache statistics.
func (c *StatsCache) GetStats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()

	total := c.hits + c.misses
	hitRate := float64(0)
	if total > 0 {
		hitRate = float64(c.hits) / float64(total)
	}

	return Stats{
		Size:     c.cache.Size(),
		HitRate:  hitRate,
		MissRate: 1 - hitRate,
	}
}
