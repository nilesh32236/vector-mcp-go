package indexer

import (
	"strings"
	"testing"
)

var chunkSink []Chunk

func BenchmarkFastChunk(b *testing.B) {
	text := strings.Repeat("This is a line of text.\n", 10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		chunkSink = fastChunk(text)
	}
}
