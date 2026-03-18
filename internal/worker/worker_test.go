package worker

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
)

type mockEmbedder struct{}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return make([]float32, 1024), nil
}

func TestWorkerStatusUpdate(t *testing.T) {
	ctx := context.Background()
	dbPath := "./test_worker_db"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg := &config.Config{
		ProjectRoot: "/non/existent/path",
		DbPath:      dbPath,
		Logger:      logger,
	}

	store, _ := db.Connect(ctx, dbPath, "test_collection")
	progressMap := &sync.Map{}

	w := &IndexWorker{
		cfg:         cfg,
		logger:      logger,
		progressMap: progressMap,
		storeGetter: func(ctx context.Context) (*db.Store, error) { return store, nil },
		embedder:    &mockEmbedder{},
	}

	// Create a temporary directory for the project root.
	root := "./test_project_root"
	os.MkdirAll(root, 0755)
	defer os.RemoveAll(root)
	os.WriteFile(root+"/test.go", []byte("package main\nfunc main() {}"), 0644)

	w.processPath(ctx, root)

	// Check if progressMap was updated
	status, ok := progressMap.Load(root)
	if !ok {
		t.Fatal("expected status in progressMap")
	}
	if !strings.Contains(status.(string), "Completed") {
		t.Errorf("expected 'Completed' status, got %s", status)
	}

	// Check if DB status was updated
	dbStatus, _ := store.GetStatus(ctx, root)
	if !strings.Contains(dbStatus, "Completed") {
		t.Errorf("expected 'Completed' status in DB, got %s", dbStatus)
	}
}
