package lexical

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBM25BasicSearch(t *testing.T) {
	idx := NewIndex()
	idx.Add("doc1", "func handleSearch query vector embedding")
	idx.Add("doc2", "func handleIndex project files scanner")
	idx.Add("doc3", "vector embedding search semantic similarity")

	results := idx.Search("vector search", 10)
	require.NotEmpty(t, results)
	// doc3 and doc1 should rank highest (both contain "vector")
	assert.Equal(t, "doc3", results[0].DocID)
}

func TestBM25Remove(t *testing.T) {
	idx := NewIndex()
	idx.Add("doc1", "vector search embedding")
	idx.Add("doc2", "vector search embedding")
	idx.Remove("doc1")

	results := idx.Search("vector", 10)
	require.Len(t, results, 1)
	assert.Equal(t, "doc2", results[0].DocID)
}

func TestBM25Empty(t *testing.T) {
	idx := NewIndex()
	results := idx.Search("anything", 10)
	assert.Empty(t, results)
}

func TestBM25TopK(t *testing.T) {
	idx := NewIndex()
	for i := 0; i < 20; i++ {
		idx.Add(string(rune('a'+i)), "search query term")
	}
	results := idx.Search("search", 5)
	assert.Len(t, results, 5)
}

func TestBM25TokenizesCodeIdentifiers(t *testing.T) {
	idx := NewIndex()
	idx.Add("doc1", "func handleSearchQuery target_symbol HTTPServer2")

	assert.NotEmpty(t, idx.Search("search", 5))
	assert.NotEmpty(t, idx.Search("target", 5))
	assert.NotEmpty(t, idx.Search("symbol", 5))
	assert.NotEmpty(t, idx.Search("server", 5))
}

func TestBM25NonPositiveTopK(t *testing.T) {
	idx := NewIndex()
	idx.Add("doc1", "vector search embedding")

	assert.Empty(t, idx.Search("vector", 0))
	assert.Empty(t, idx.Search("vector", -1))
}

func BenchmarkBM25Search(b *testing.B) {
	idx := NewIndex()
	for i := 0; i < 10000; i++ {
		idx.Add(string(rune(i)), "func handleSearch vector embedding query project files scanner")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.Search("vector search", 10)
	}
}
