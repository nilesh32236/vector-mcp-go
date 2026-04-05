package mocks

import (
	"context"
	"errors"
	"testing"
)

func TestMockEmbedder_Embed(t *testing.T) {
	embedder := NewMockEmbedder(384)
	ctx := context.Background()

	embedding, err := embedder.Embed(ctx, "test text")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(embedding) != 384 {
		t.Errorf("expected embedding length 384, got %d", len(embedding))
	}

	// Test with error
	testErr := errors.New("embed error")
	embedder.WithEmbedError(testErr)
	_, err = embedder.Embed(ctx, "test")
	if err != testErr {
		t.Errorf("expected embed error, got %v", err)
	}
}

func TestMockEmbedder_EmbedBatch(t *testing.T) {
	embedder := NewMockEmbedder(768)
	ctx := context.Background()

	texts := []string{"text1", "text2", "text3"}
	embeddings, err := embedder.EmbedBatch(ctx, texts)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(embeddings) != 3 {
		t.Errorf("expected 3 embeddings, got %d", len(embeddings))
	}
	for i, emb := range embeddings {
		if len(emb) != 768 {
			t.Errorf("embedding %d: expected length 768, got %d", i, len(emb))
		}
	}

	// Test with error
	testErr := errors.New("batch error")
	embedder.WithEmbedBatchError(testErr)
	_, err = embedder.EmbedBatch(ctx, texts)
	if err != testErr {
		t.Errorf("expected batch error, got %v", err)
	}
}

func TestMockEmbedder_RerankBatch(t *testing.T) {
	embedder := NewMockEmbedder(384)
	ctx := context.Background()

	texts := []string{"doc1", "doc2", "doc3"}
	scores, err := embedder.RerankBatch(ctx, "query", texts)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(scores) != 3 {
		t.Errorf("expected 3 scores, got %d", len(scores))
	}

	// First score should be highest
	if scores[0] < scores[1] {
		t.Error("expected first score to be highest")
	}

	// Test with error
	testErr := errors.New("rerank error")
	embedder.WithRerankError(testErr)
	_, err = embedder.RerankBatch(ctx, "query", texts)
	if err != testErr {
		t.Errorf("expected rerank error, got %v", err)
	}
}

func TestMockEmbedder_WithEmbedding(t *testing.T) {
	embedder := NewMockEmbedder(384)
	ctx := context.Background()

	// Set specific embedding for a text
	customEmbedding := []float32{0.1, 0.2, 0.3}
	for i := 3; i < 384; i++ {
		customEmbedding = append(customEmbedding, 0.0)
	}
	embedder.WithEmbedding("custom text", customEmbedding)

	embedding, err := embedder.Embed(ctx, "custom text")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if embedding[0] != 0.1 || embedding[1] != 0.2 || embedding[2] != 0.3 {
		t.Errorf("expected custom embedding values, got %v", embedding[:3])
	}
}

func TestMockEmbedder_CallCounts(t *testing.T) {
	embedder := NewMockEmbedder(384)
	ctx := context.Background()

	embedder.Embed(ctx, "text1")
	embedder.Embed(ctx, "text2")
	embedder.EmbedBatch(ctx, []string{"a", "b"})
	embedder.RerankBatch(ctx, "q", []string{"d"})

	embedCalls, batchCalls, rerankCalls := embedder.GetCallCounts()
	if embedCalls != 2 {
		t.Errorf("expected 2 embed calls, got %d", embedCalls)
	}
	if batchCalls != 1 {
		t.Errorf("expected 1 batch call, got %d", batchCalls)
	}
	if rerankCalls != 1 {
		t.Errorf("expected 1 rerank call, got %d", rerankCalls)
	}
}

func TestMockEmbedder_Deterministic(t *testing.T) {
	embedder := NewMockEmbedder(384)
	ctx := context.Background()

	// Same text should produce same embedding
	emb1, _ := embedder.Embed(ctx, "test")
	emb2, _ := embedder.Embed(ctx, "test")

	for i := range emb1 {
		if emb1[i] != emb2[i] {
			t.Errorf("embeddings should be deterministic, differ at index %d", i)
			break
		}
	}

	// Different text should produce different embedding
	emb3, _ := embedder.Embed(ctx, "different")
	allSame := true
	for i := range emb1 {
		if emb1[i] != emb3[i] {
			allSame = false
			break
		}
	}
	if allSame {
		t.Error("different texts should produce different embeddings")
	}
}
