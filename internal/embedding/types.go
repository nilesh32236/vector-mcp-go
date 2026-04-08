// Package embedding provides tools for generating and managing vector embeddings.
package embedding

import (
	"context"
	"fmt"
)

// ContentType identifies the type of content being embedded.
type ContentType string

const (
	// ContentTypeCode indicates that the associated content is source code.
	ContentTypeCode    ContentType = "code"
	ContentTypeDoc     ContentType = "documentation"
	ContentTypeConfig  ContentType = "config"
	ContentTypeGeneral ContentType = "general"
	ContentTypeQuery   ContentType = "query"
)

// ModelConfig defines the configuration for an embedding or reranker model.
type ModelConfig struct {
	OnnxURL       string // URL to the ONNX model file
	TokenizerURL  string // URL to the tokenizer.json file
	Filename      string // Local filename for the model
	Dimension     int    // Embedding dimension
	IsReranker    bool   // Whether the model is a reranker
	MatryoshkaDim int    // optional: truncate embeddings to this dim (MRL models only)
}

// EffectiveDimension returns the vector width emitted by the embedder after any
// optional Matryoshka truncation is applied.
func (mc ModelConfig) EffectiveDimension() int {
	if mc.IsReranker {
		return mc.Dimension
	}
	if mc.MatryoshkaDim > 0 && mc.MatryoshkaDim < mc.Dimension {
		return mc.MatryoshkaDim
	}
	return mc.Dimension
}

// WithMatryoshkaDimension applies a runtime truncation target when valid.
func (mc ModelConfig) WithMatryoshkaDimension(dim int) ModelConfig {
	if mc.IsReranker || dim <= 0 || dim >= mc.Dimension {
		return mc
	}
	mc.MatryoshkaDim = dim
	return mc
}

// Embedder handles the process of converting text into vector embeddings.
type Embedder struct {
	embSess    *SessionData
	rerankSess *SessionData
	Logger     func(string, ...any)
}

// Embed generates an embedding for a single text input.
func (e *Embedder) Embed(_ context.Context, text string) ([]float32, error) {
	return e.embSess.embedSingle(text)
}

// EmbedBatch generates embeddings for a batch of text inputs.
func (e *Embedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		emb, err := e.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		results[i] = emb
	}
	return results, nil
}

// RerankBatch re-scores a batch of text inputs against a query using a cross-encoder.
func (e *Embedder) RerankBatch(_ context.Context, query string, texts []string) ([]float32, error) {
	if e.rerankSess == nil {
		return nil, fmt.Errorf("reranker model not loaded in this embedder")
	}

	scores := make([]float32, len(texts))
	for i, text := range texts {
		score, err := e.rerankSess.rerankSingle(query, text)
		if err != nil {
			return nil, err
		}
		scores[i] = score
	}
	return scores, nil
}

// Close releases resources associated with the embedder.
func (e *Embedder) Close() {
	if e.embSess != nil {
		e.embSess.Close()
	}
	if e.rerankSess != nil {
		e.rerankSess.Close()
	}
}

// EmbedderPool manages a pool of reusable Embedder instances.
type EmbedderPool struct {
	pool chan *Embedder
}

// Get retrieves an embedder from the pool.
func (p *EmbedderPool) Get(ctx context.Context) (*Embedder, error) {
	select {
	case e := <-p.pool:
		return e, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Put returns an embedder to the pool.
func (p *EmbedderPool) Put(e *Embedder) {
	p.pool <- e
}

// Close shuts down the pool and closes all embedders.
func (p *EmbedderPool) Close() {
	close(p.pool)
	for e := range p.pool {
		e.Close()
	}
}
