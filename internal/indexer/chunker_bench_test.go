package indexer

import (
	"strings"
	"testing"
)

func BenchmarkSplitIfNeeded(b *testing.B) {
	content := strings.Repeat("a\nb\nc\nd\ne\n", 10000)
	c := Chunk{
		Content:   content,
		StartLine: 1,
		EndLine:   10000 * 5,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		splitIfNeeded(c)
	}
}

func BenchmarkFastChunk(b *testing.B) {
	content := strings.Repeat("a\nb\nc\nd\ne\n", 10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fastChunk(content)
	}
}
