package db

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func BenchmarkLexicalSearch(b *testing.B) {
	ctx := context.Background()
	dbPath := b.TempDir() + "/bench_db"

	store, err := Connect(ctx, dbPath, "bench_collection", 3)
	if err != nil {
		b.Fatalf("failed to connect: %v", err)
	}

	// Insert dummy data
	var records []Record
	for i := 0; i < 1000; i++ {
		syms := []string{fmt.Sprintf("symA%d", i), fmt.Sprintf("symB%d", i), "targetSymbol"}
		symsJSON, _ := json.Marshal(syms)

		calls := []string{fmt.Sprintf("callA%d", i), fmt.Sprintf("callB%d", i)}
		callsJSON, _ := json.Marshal(calls)

		records = append(records, Record{
			ID:      fmt.Sprintf("doc%d", i),
			Content: fmt.Sprintf("content of document %d", i),
			Metadata: map[string]string{
				"symbols": string(symsJSON),
				"calls":   string(callsJSON),
				"name":    fmt.Sprintf("docName%d", i),
			},
			Embedding: []float32{0.1, 0.2, 0.3},
		})
	}
	err = store.Insert(ctx, records)
	if err != nil {
		b.Fatalf("failed to insert: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := store.LexicalSearch(ctx, "targetSymbol", 10, nil, "")
		if err != nil {
			b.Fatalf("search failed: %v", err)
		}
	}
}
