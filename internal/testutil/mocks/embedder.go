package mocks

import (
	"context"
	"sync"
)

// MockEmbedder implements the indexer.Embedder interface for testing.
type MockEmbedder struct {
	mu            sync.RWMutex
	dimension     int
	embedErr      error
	embedBatchErr error
	rerankErr     error
	embedCalls    int
	batchCalls    int
	rerankCalls   int
	embeddings    map[string][]float32
}

// NewMockEmbedder creates a new MockEmbedder with the specified dimension.
func NewMockEmbedder(dimension int) *MockEmbedder {
	return &MockEmbedder{
		dimension: dimension,
		embeddings: make(map[string][]float32),
	}
}

// WithEmbedError sets an error to return on Embed.
func (m *MockEmbedder) WithEmbedError(err error) *MockEmbedder {
	m.embedErr = err
	return m
}

// WithEmbedBatchError sets an error to return on EmbedBatch.
func (m *MockEmbedder) WithEmbedBatchError(err error) *MockEmbedder {
	m.embedBatchErr = err
	return m
}

// WithRerankError sets an error to return on RerankBatch.
func (m *MockEmbedder) WithRerankError(err error) *MockEmbedder {
	m.rerankErr = err
	return m
}

// WithEmbedding sets a specific embedding for a text.
func (m *MockEmbedder) WithEmbedding(text string, embedding []float32) *MockEmbedder {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.embeddings[text] = embedding
	return m
}

// GetCallCounts returns the number of times each method was called.
func (m *MockEmbedder) GetCallCounts() (embedCalls, batchCalls, rerankCalls int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.embedCalls, m.batchCalls, m.rerankCalls
}

// Embed generates a mock embedding for the given text.
func (m *MockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return m.EmbedQuery(ctx, text) // Default to EmbedQuery for mock consistency
}

// EmbedQuery generates a mock embedding for the given query text.
func (m *MockEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	m.mu.Lock()
	m.embedCalls++
	m.mu.Unlock()

	if m.embedErr != nil {
		return nil, m.embedErr
	}

	// Check if we have a specific embedding for this text
	m.mu.RLock()
	if emb, ok := m.embeddings[text]; ok {
		m.mu.RUnlock()
		return emb, nil
	}
	m.mu.RUnlock()

	// Generate a deterministic embedding based on text hash
	return m.generateEmbedding(text), nil
}

// EmbedBatch generates mock embeddings for multiple texts.
func (m *MockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	m.mu.Lock()
	m.batchCalls++
	m.mu.Unlock()

	if m.embedBatchErr != nil {
		return nil, m.embedBatchErr
	}

	embeddings := make([][]float32, len(texts))
	for i, text := range texts {
		embeddings[i] = m.generateEmbedding(text)
	}
	return embeddings, nil
}

// RerankBatch returns mock relevance scores for reranking.
func (m *MockEmbedder) RerankBatch(ctx context.Context, query string, texts []string) ([]float32, error) {
	m.mu.Lock()
	m.rerankCalls++
	m.mu.Unlock()

	if m.rerankErr != nil {
		return nil, m.rerankErr
	}

	// Generate mock scores (higher for earlier items)
	scores := make([]float32, len(texts))
	for i := range texts {
		scores[i] = 1.0 - float32(i)*0.1
		if scores[i] < 0 {
			scores[i] = 0
		}
	}
	return scores, nil
}

// generateEmbedding creates a deterministic mock embedding.
func (m *MockEmbedder) generateEmbedding(text string) []float32 {
	embedding := make([]float32, m.dimension)
	
	// Simple deterministic generation based on text
	// Use character values to create variation
	for i := 0; i < m.dimension; i++ {
		if i < len(text) {
			embedding[i] = float32(text[i]) / 255.0
		} else {
			// Fill remaining with normalized values
			embedding[i] = 0.01 * float32(i%100)
		}
	}

	// Normalize to unit length
	var sum float32
	for _, v := range embedding {
		sum += v * v
	}
	if sum > 0 {
		// Simple normalization approximation
		for i := range embedding {
			embedding[i] /= (sum + 0.01) // Avoid division issues
		}
	}

	return embedding
}

// GetDimension returns the configured dimension.
func (m *MockEmbedder) GetDimension() int {
	return m.dimension
}
