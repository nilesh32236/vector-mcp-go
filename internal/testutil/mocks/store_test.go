package mocks

import (
	"context"
	"errors"
	"testing"

	"github.com/nilesh32236/vector-mcp-go/internal/db"
)

// Test error
var ErrTestError = errors.New("test error")

func TestMockStore_Search(t *testing.T) {
	store := NewMockStore()

	// Add some test records
	records := []db.Record{
		{ID: "1", Content: "test content 1", Metadata: map[string]string{"path": "file1.go"}},
		{ID: "2", Content: "test content 2", Metadata: map[string]string{"path": "file2.go"}},
		{ID: "3", Content: "test content 3", Metadata: map[string]string{"path": "file3.go"}},
	}
	store.WithRecords(records)

	// Test search
	ctx := context.Background()
	results, err := store.Search(ctx, []float32{0.1, 0.2, 0.3}, 2, nil, "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	// Test search with error
	store.WithSearchError(ErrTestError)
	_, err = store.Search(ctx, []float32{0.1}, 1, nil, "")
	if err != ErrTestError {
		t.Errorf("expected test error, got %v", err)
	}
}

func TestMockStore_Insert(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	records := []db.Record{
		{ID: "1", Content: "test"},
	}

	err := store.Insert(ctx, records)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify count
	if store.Count() != 1 {
		t.Errorf("expected count 1, got %d", store.Count())
	}

	// Test insert with error
	store.WithInsertError(ErrTestError)
	err = store.Insert(ctx, records)
	if err != ErrTestError {
		t.Errorf("expected test error, got %v", err)
	}
}

func TestMockStore_DeleteByPrefix(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	records := []db.Record{
		{ID: "1", Metadata: map[string]string{"path": "pkg/file1.go"}},
		{ID: "2", Metadata: map[string]string{"path": "pkg/file2.go"}},
		{ID: "3", Metadata: map[string]string{"path": "cmd/main.go"}},
	}
	store.WithRecords(records)

	err := store.DeleteByPrefix(ctx, "pkg/file1.go", "test-project")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Should have 2 records left
	if store.Count() != 2 {
		t.Errorf("expected count 2, got %d", store.Count())
	}
}

func TestMockStore_Status(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	store.SetStatus("project-1", "completed")
	store.SetStatus("project-2", "indexing")

	status, err := store.GetStatus(ctx, "project-1")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if status != "completed" {
		t.Errorf("expected status 'completed', got %s", status)
	}

	statuses, err := store.GetAllStatuses(ctx)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(statuses) != 2 {
		t.Errorf("expected 2 statuses, got %d", len(statuses))
	}
}

func TestMockStore_CallCounts(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	// Make some calls
	_, _ = store.Search(ctx, []float32{}, 10, nil, "")
	_, _ = store.Search(ctx, []float32{}, 10, nil, "")
	_ = store.Insert(ctx, []db.Record{})

	searchCalls, insertCalls := store.GetCallCounts()
	if searchCalls != 2 {
		t.Errorf("expected 2 search calls, got %d", searchCalls)
	}
	if insertCalls != 1 {
		t.Errorf("expected 1 insert call, got %d", insertCalls)
	}
}

func TestMockStore_HybridSearch(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	records := []db.Record{
		{ID: "1", Content: "test query match"},
		{ID: "2", Content: "another test"},
	}
	store.WithRecords(records)

	results, err := store.HybridSearch(ctx, "test query", []float32{0.1}, 10, nil, "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestMockStore_LexicalSearch(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	records := []db.Record{
		{ID: "1", Content: "function Search() {}"},
		{ID: "2", Content: "func test() {}"},
	}
	store.WithRecords(records)

	results, err := store.LexicalSearch(ctx, "Search", 10, nil, "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}
