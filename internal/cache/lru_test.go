package cache

import (
	"context"
	"testing"
	"time"
)

func TestLRUCache_GetSet(t *testing.T) {
	cache := NewLRUCache(WithMaxSize(10))
	ctx := context.Background()

	// Set a value
	err := cache.Set(ctx, "key1", "value1", 0)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Get the value
	val, found := cache.Get(ctx, "key1")
	if !found {
		t.Error("expected to find key1")
	}
	if val != "value1" {
		t.Errorf("expected 'value1', got %v", val)
	}
}

func TestLRUCache_Miss(t *testing.T) {
	cache := NewLRUCache()
	ctx := context.Background()

	val, found := cache.Get(ctx, "nonexistent")
	if found {
		t.Error("expected not to find key")
	}
	if val != nil {
		t.Errorf("expected nil, got %v", val)
	}
}

func TestLRUCache_Eviction(t *testing.T) {
	cache := NewLRUCache(WithMaxSize(3))
	ctx := context.Background()

	// Add 3 items
	_ = cache.Set(ctx, "key1", "value1", 0)
	_ = cache.Set(ctx, "key2", "value2", 0)
	_ = cache.Set(ctx, "key3", "value3", 0)

	if cache.Size() != 3 {
		t.Errorf("expected size 3, got %d", cache.Size())
	}

	// Add a 4th item - should evict key1 (oldest)
	_ = cache.Set(ctx, "key4", "value4", 0)

	if cache.Size() != 3 {
		t.Errorf("expected size 3, got %d", cache.Size())
	}

	// key1 should be evicted
	_, found := cache.Get(ctx, "key1")
	if found {
		t.Error("expected key1 to be evicted")
	}

	// key4 should exist
	_, found = cache.Get(ctx, "key4")
	if !found {
		t.Error("expected key4 to exist")
	}
}

func TestLRUCache_Update(t *testing.T) {
	cache := NewLRUCache(WithMaxSize(10))
	ctx := context.Background()

	// Set initial value
	_ = cache.Set(ctx, "key1", "value1", 0)

	// Update value
	_ = cache.Set(ctx, "key1", "value2", 0)

	// Size should still be 1
	if cache.Size() != 1 {
		t.Errorf("expected size 1, got %d", cache.Size())
	}

	// Get updated value
	val, found := cache.Get(ctx, "key1")
	if !found {
		t.Error("expected to find key1")
	}
	if val != "value2" {
		t.Errorf("expected 'value2', got %v", val)
	}
}

func TestLRUCache_Delete(t *testing.T) {
	cache := NewLRUCache()
	ctx := context.Background()

	_ = cache.Set(ctx, "key1", "value1", 0)
	_ = cache.Delete(ctx, "key1")

	_, found := cache.Get(ctx, "key1")
	if found {
		t.Error("expected key1 to be deleted")
	}
}

func TestLRUCache_Clear(t *testing.T) {
	cache := NewLRUCache()
	ctx := context.Background()

	_ = cache.Set(ctx, "key1", "value1", 0)
	_ = cache.Set(ctx, "key2", "value2", 0)
	_ = cache.Set(ctx, "key3", "value3", 0)

	_ = cache.Clear()

	if cache.Size() != 0 {
		t.Errorf("expected size 0, got %d", cache.Size())
	}
}

func TestLRUCache_TTL(t *testing.T) {
	cache := NewLRUCache()
	ctx := context.Background()

	// Set with short TTL
	_ = cache.Set(ctx, "key1", "value1", 100*time.Millisecond)

	// Should exist immediately
	_, found := cache.Get(ctx, "key1")
	if !found {
		t.Error("expected to find key1")
	}

	// Wait for TTL to expire
	time.Sleep(150 * time.Millisecond)

	// Should be expired
	_, found = cache.Get(ctx, "key1")
	if found {
		t.Error("expected key1 to be expired")
	}
}

func TestLRUCache_OnEvict(t *testing.T) {
	evicted := make(map[string]any)
	cache := NewLRUCache(
		WithMaxSize(2),
		WithOnEvict(func(key string, value any) {
			evicted[key] = value
		}),
	)
	ctx := context.Background()

	_ = cache.Set(ctx, "key1", "value1", 0)
	_ = cache.Set(ctx, "key2", "value2", 0)
	_ = cache.Set(ctx, "key3", "value3", 0) // Should evict key1

	if evicted["key1"] != "value1" {
		t.Errorf("expected key1 to be evicted, got evicted: %v", evicted)
	}
}

func TestEmbeddingCache(t *testing.T) {
	cache := NewEmbeddingCache(100)
	ctx := context.Background()

	embedding := []float32{0.1, 0.2, 0.3, 0.4, 0.5}

	// Cache miss
	_, found := cache.GetEmbedding(ctx, "test text")
	if found {
		t.Error("expected cache miss")
	}

	// Set embedding
	err := cache.SetEmbedding(ctx, "test text", embedding)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Cache hit
	cached, found := cache.GetEmbedding(ctx, "test text")
	if !found {
		t.Error("expected cache hit")
	}

	// Verify values
	if len(cached) != len(embedding) {
		t.Errorf("expected length %d, got %d", len(embedding), len(cached))
	}

	// Same text should hit
	_, found = cache.GetEmbedding(ctx, "test text")
	if !found {
		t.Error("expected cache hit for same text")
	}

	// Different text should miss
	_, found = cache.GetEmbedding(ctx, "different text")
	if found {
		t.Error("expected cache miss for different text")
	}
}

func TestSearchResultCache(t *testing.T) {
	cache := NewSearchResultCache(100, 5*time.Minute)
	ctx := context.Background()

	results := []string{"result1", "result2", "result3"}
	projectIDs := []string{"project1", "project2"}

	// Cache miss
	_, found := cache.Get(ctx, "test query", projectIDs)
	if found {
		t.Error("expected cache miss")
	}

	// Set results
	err := cache.Set(ctx, "test query", projectIDs, results)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Cache hit
	cached, found := cache.Get(ctx, "test query", projectIDs)
	if !found {
		t.Error("expected cache hit")
	}

	cachedResults, ok := cached.([]string)
	if !ok {
		t.Error("expected []string type")
	}
	if len(cachedResults) != len(results) {
		t.Errorf("expected %d results, got %d", len(results), len(cachedResults))
	}
}

func TestSearchResultCache_InvalidateByProject(t *testing.T) {
	cache := NewSearchResultCache(100, 5*time.Minute)
	ctx := context.Background()

	results := []string{"result1"}
	projectIDs := []string{"project1"}

	_ = cache.Set(ctx, "query", projectIDs, results)

	// Should exist
	_, found := cache.Get(ctx, "query", projectIDs)
	if !found {
		t.Error("expected cache hit")
	}

	// Invalidate
	cache.InvalidateByProject("project1")

	// Should be cleared
	if cache.cache.Size() != 0 {
		t.Errorf("expected empty cache, got size %d", cache.cache.Size())
	}
}

func TestStatsCache(t *testing.T) {
	innerCache := NewLRUCache()
	cache := NewStatsCache(innerCache)
	ctx := context.Background()

	// Miss
	cache.Get(ctx, "key1")

	stats := cache.GetStats()
	if stats.MissRate != 1.0 {
		t.Errorf("expected miss rate 1.0, got %f", stats.MissRate)
	}

	// Set and hit
	_ = cache.Set(ctx, "key1", "value1", 0)
	cache.Get(ctx, "key1")

	stats = cache.GetStats()
	if stats.HitRate != 0.5 {
		t.Errorf("expected hit rate 0.5, got %f", stats.HitRate)
	}
}

func TestLRUCache_LRUOrder(t *testing.T) {
	cache := NewLRUCache(WithMaxSize(3))
	ctx := context.Background()

	// Add items
	_ = cache.Set(ctx, "key1", "value1", 0)
	_ = cache.Set(ctx, "key2", "value2", 0)
	_ = cache.Set(ctx, "key3", "value3", 0)

	// Access key1 to make it recently used
	cache.Get(ctx, "key1")

	// Add new item - should evict key2 (now oldest)
	_ = cache.Set(ctx, "key4", "value4", 0)

	// key1 should still exist (was accessed)
	_, found := cache.Get(ctx, "key1")
	if !found {
		t.Error("expected key1 to exist")
	}

	// key2 should be evicted
	_, found = cache.Get(ctx, "key2")
	if found {
		t.Error("expected key2 to be evicted")
	}

	// key3 should still exist
	_, found = cache.Get(ctx, "key3")
	if !found {
		t.Error("expected key3 to exist")
	}
}

func TestHashText(t *testing.T) {
	hash1 := hashText("test text")
	hash2 := hashText("test text")
	hash3 := hashText("different text")

	// Same text should produce same hash
	if hash1 != hash2 {
		t.Error("expected same hashes for same text")
	}

	// Different text should produce different hash
	if hash1 == hash3 {
		t.Error("expected different hashes for different text")
	}

	// Hash should be consistent length
	if len(hash1) != 64 { // SHA256 produces 64 hex characters
		t.Errorf("expected hash length 64, got %d", len(hash1))
	}
}

func TestHashQuery(t *testing.T) {
	query := "search query"
	projects := []string{"p1", "p2"}

	hash1 := hashQuery(query, projects)
	hash2 := hashQuery(query, projects)
	hash3 := hashQuery(query, []string{"p3"})

	// Same query + projects should produce same hash
	if hash1 != hash2 {
		t.Error("expected same hashes for same query + projects")
	}

	// Different projects should produce different hash
	if hash1 == hash3 {
		t.Error("expected different hashes for different projects")
	}
}

func BenchmarkLRUCache_Set(b *testing.B) {
	cache := NewLRUCache(WithMaxSize(10000))
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cache.Set(ctx, string(rune(i)), i, 0)
	}
}

func BenchmarkLRUCache_Get(b *testing.B) {
	cache := NewLRUCache(WithMaxSize(10000))
	ctx := context.Background()

	// Pre-populate
	for i := 0; i < 10000; i++ {
		_ = cache.Set(ctx, string(rune(i)), i, 0)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get(ctx, string(rune(i%10000)))
	}
}

func BenchmarkEmbeddingCache(b *testing.B) {
	cache := NewEmbeddingCache(10000)
	ctx := context.Background()
	embedding := make([]float32, 384)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cache.SetEmbedding(ctx, string(rune(i)), embedding)
		_, _ = cache.GetEmbedding(ctx, string(rune(i)))
	}
}
