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
