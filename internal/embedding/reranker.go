package embedding

import (
	"context"
	"sort"
	"sync"
)

// RerankStrategy defines how reranking should be performed.
type RerankStrategy string

const (
	RerankStrategyNone     RerankStrategy = "none"     // No reranking
	RerankStrategyAll      RerankStrategy = "all"      // Rerank all results
	RerankStrategyTopK     RerankStrategy = "topk"     // Only rerank top K results
	RerankStrategyAdaptive RerankStrategy = "adaptive" // Adaptive based on score gaps
	RerankStrategyCohere   RerankStrategy = "cohere"   // Cohere-style reranking
)

// RerankConfig configures the reranking behavior.
type RerankConfig struct {
	Strategy        RerankStrategy
	TopK            int     // For topk strategy
	ScoreThreshold  float32 // Minimum score to consider
	MaxBatchSize    int     // Max items to rerank at once
	FallbackToCross bool    // Fall back to cross-encoder on error
}

// DefaultRerankConfig returns sensible defaults.
func DefaultRerankConfig() RerankConfig {
	return RerankConfig{
		Strategy:        RerankStrategyTopK,
		TopK:            20,
		ScoreThreshold:  0.3,
		MaxBatchSize:    32,
		FallbackToCross: true,
	}
}

// RerankResult represents a single reranked item.
type RerankResult struct {
	Index    int     // Original index
	Score    float32 // Reranker score
	Content  string
	Metadata map[string]string
}

// AdvancedReranker provides sophisticated reranking capabilities.
type AdvancedReranker struct {
	router *ModelRouter
	config RerankConfig
	mu     sync.RWMutex

	// Metrics
	rerankCalls    int64
	totalLatencyMs int64
}

// NewAdvancedReranker creates a new advanced reranker.
func NewAdvancedReranker(router *ModelRouter, config RerankConfig) *AdvancedReranker {
	return &AdvancedReranker{
		router: router,
		config: config,
	}
}

// Rerank performs reranking based on the configured strategy.
func (r *AdvancedReranker) Rerank(ctx context.Context, query string, results []RerankResult) ([]RerankResult, error) {
	if r.config.Strategy == RerankStrategyNone || len(results) == 0 {
		return results, nil
	}

	// Select items to rerank based on strategy
	toRerank := r.selectForReranking(results)

	if len(toRerank) == 0 {
		return results, nil
	}

	// Get reranker
	embedder, err := r.router.GetReranker()
	if err != nil {
		// Try to use cross-encoder fallback from any embedder with reranker
		if r.config.FallbackToCross {
			return r.crossEncoderFallback(query, results)
		}
		return nil, err
	}
	defer r.router.PutReranker(embedder)

	// Perform reranking
	scores, err := embedder.RerankBatch(ctx, query, r.extractContent(toRerank))
	if err != nil {
		if r.config.FallbackToCross {
			return r.crossEncoderFallback(query, results)
		}
		return nil, err
	}

	// Update scores
	for i, result := range toRerank {
		if i < len(scores) {
			result.Score = scores[i]
		}
	}

	// Sort by new scores
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	r.mu.Lock()
	r.rerankCalls++
	r.mu.Unlock()

	return results, nil
}

// selectForReranking chooses which items to rerank based on strategy.
func (r *AdvancedReranker) selectForReranking(results []RerankResult) []RerankResult {
	switch r.config.Strategy {
	case RerankStrategyAll:
		return results

	case RerankStrategyTopK:
		if len(results) <= r.config.TopK {
			return results
		}
		return results[:r.config.TopK]

	case RerankStrategyAdaptive:
		return r.adaptiveSelection(results)

	default:
		return results
	}
}

// adaptiveSelection selects items where score gaps are small.
func (r *AdvancedReranker) adaptiveSelection(results []RerankResult) []RerankResult {
	if len(results) <= 5 {
		return results
	}

	// Find where score gaps become significant
	selected := make([]RerankResult, 0, len(results))
	selected = append(selected, results[0])

	for i := 1; i < len(results) && i < r.config.MaxBatchSize; i++ {
		gap := selected[len(selected)-1].Score - results[i].Score

		// If gap is small, include for reranking
		if gap < 0.1 {
			selected = append(selected, results[i])
		} else {
			break
		}
	}

	return selected
}

// extractContent extracts content strings from results.
func (r *AdvancedReranker) extractContent(results []RerankResult) []string {
	contents := make([]string, len(results))
	for i, result := range results {
		contents[i] = result.Content
	}
	return contents
}

// crossEncoderFallback performs simple cross-encoder scoring.
func (r *AdvancedReranker) crossEncoderFallback(query string, results []RerankResult) ([]RerankResult, error) {
	// Simple word overlap scoring as fallback
	queryWords := make(map[string]bool)
	for _, word := range tokenize(query) {
		queryWords[word] = true
	}

	for i := range results {
		words := tokenize(results[i].Content)
		overlap := 0
		for _, word := range words {
			if queryWords[word] {
				overlap++
			}
		}
		// Normalize score
		if len(words) > 0 {
			results[i].Score = float32(overlap) / float32(len(queryWords))
		}
	}

	// Sort by scores
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}

// tokenize splits text into words.
func tokenize(text string) []string {
	// Simple whitespace tokenization
	words := make([]string, 0)
	start := 0

	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			if i > start {
				words = append(words, text[start:i])
			}
			start = i + 1
		}
	}

	if start < len(text) {
		words = append(words, text[start:])
	}

	return words
}

// HybridReranker combines multiple reranking signals.
type HybridReranker struct {
	reranker *AdvancedReranker
	weights  RerankWeights
}

// RerankWeights defines weights for different signals.
type RerankWeights struct {
	SemanticScore float32 // Weight for semantic search score
	RerankerScore float32 // Weight for reranker score
	RecencyScore  float32 // Weight for recency (if available)
	FileScore     float32 // Weight for file path relevance
}

// DefaultRerankWeights returns balanced weights.
func DefaultRerankWeights() RerankWeights {
	return RerankWeights{
		SemanticScore: 0.4,
		RerankerScore: 0.5,
		RecencyScore:  0.05,
		FileScore:     0.05,
	}
}

// NewHybridReranker creates a new hybrid reranker.
func NewHybridReranker(reranker *AdvancedReranker, weights RerankWeights) *HybridReranker {
	return &HybridReranker{
		reranker: reranker,
		weights:  weights,
	}
}

// Rerank performs hybrid reranking combining multiple signals.
func (h *HybridReranker) Rerank(ctx context.Context, query string, results []RerankResult, semanticScores []float32) ([]RerankResult, error) {
	if len(results) == 0 {
		return results, nil
	}

	// Get reranker scores
	reranked, err := h.reranker.Rerank(ctx, query, results)
	if err != nil {
		return nil, err
	}

	// Combine scores
	for i := range reranked {
		semanticScore := float32(0)
		if i < len(semanticScores) {
			semanticScore = semanticScores[i]
		}

		// Weighted combination
		reranked[i].Score = h.weights.SemanticScore*semanticScore +
			h.weights.RerankerScore*reranked[i].Score
	}

	// Sort by combined scores
	sort.Slice(reranked, func(i, j int) bool {
		return reranked[i].Score > reranked[j].Score
	})

	return reranked, nil
}

// GetStats returns reranker statistics.
func (r *AdvancedReranker) GetStats() map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return map[string]interface{}{
		"rerank_calls":     r.rerankCalls,
		"total_latency_ms": r.totalLatencyMs,
		"strategy":         string(r.config.Strategy),
		"top_k":            r.config.TopK,
	}
}
