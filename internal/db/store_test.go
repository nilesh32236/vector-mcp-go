package db

import (
	"context"
	"os"
	"testing"
)

func TestStoreStatuses(t *testing.T) {
	ctx := context.Background()
	dbPath := "./test_db"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	store, err := Connect(ctx, dbPath, "test_collection", 384)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	project1 := "/abs/path/to/project1"
	status1 := "Indexing: 50.0% (5/10) - Current: file1.go"
	project2 := "/abs/path/to/project2"
	status2 := "Completed: 10 indexed, 0 skipped"

	err = store.SetStatus(ctx, project1, status1)
	if err != nil {
		t.Errorf("failed to set status1: %v", err)
	}
	err = store.SetStatus(ctx, project2, status2)
	if err != nil {
		t.Errorf("failed to set status2: %v", err)
	}

	// Test GetStatus
	gotStatus1, err := store.GetStatus(ctx, project1)
	if err != nil {
		t.Errorf("failed to get status1: %v", err)
	}
	if gotStatus1 != status1 {
		t.Errorf("expected %s, got %s", status1, gotStatus1)
	}

	// Test GetAllStatuses
	statuses, err := store.GetAllStatuses(ctx)
	if err != nil {
		t.Errorf("failed to get all statuses: %v", err)
	}

	if len(statuses) != 2 {
		t.Errorf("expected 2 statuses, got %d", len(statuses))
	}
	if statuses[project1] != status1 {
		t.Errorf("expected %s for %s, got %s", status1, project1, statuses[project1])
	}
	if statuses[project2] != status2 {
		t.Errorf("expected %s for %s, got %s", status2, project2, statuses[project2])
	}
}

func TestLexicalSearchRebuildsIndexOnReconnect(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir()

	store, err := Connect(ctx, dbPath, "test_collection", 3)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	err = store.Insert(ctx, []Record{{
		ID:        "doc1",
		Content:   "func handleSearchQuery() {}",
		Embedding: []float32{0.1, 0.2, 0.3},
		Metadata: map[string]string{
			"name":       "handleSearchQuery",
			"path":       "internal/search.go",
			"project_id": "project-1",
		},
	}})
	if err != nil {
		t.Fatalf("failed to insert record: %v", err)
	}

	reconnected, err := Connect(ctx, dbPath, "test_collection", 3)
	if err != nil {
		t.Fatalf("failed to reconnect: %v", err)
	}

	results, err := reconnected.LexicalSearch(ctx, "search", 5, nil, "")
	if err != nil {
		t.Fatalf("lexical search failed after reconnect: %v", err)
	}
	if len(results) != 1 || results[0].ID != "doc1" {
		t.Fatalf("expected rehydrated BM25 index to find doc1, got %+v", results)
	}
}

func TestClearProjectRemovesLexicalEntries(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir()

	store, err := Connect(ctx, dbPath, "test_collection", 3)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	err = store.Insert(ctx, []Record{{
		ID:        "doc1",
		Content:   "func handleSearchQuery() {}",
		Embedding: []float32{0.1, 0.2, 0.3},
		Metadata: map[string]string{
			"project_id": "project-1",
		},
	}})
	if err != nil {
		t.Fatalf("failed to insert record: %v", err)
	}

	if err := store.ClearProject(ctx, "project-1"); err != nil {
		t.Fatalf("failed to clear project: %v", err)
	}

	results, err := store.LexicalSearch(ctx, "search", 5, nil, "")
	if err != nil {
		t.Fatalf("lexical search failed after project clear: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected lexical index to be cleared, got %+v", results)
	}
}
