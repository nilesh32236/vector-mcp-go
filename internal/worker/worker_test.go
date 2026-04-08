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

func (m *mockEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	return make([]float32, 1024), nil
}

func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		emb, err := m.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		results[i] = emb
	}
	return results, nil
}

func (m *mockEmbedder) RerankBatch(_ context.Context, query string, texts []string) ([]float32, error) {
	return make([]float32, len(texts)), nil
}

func TestWorkerStatusUpdate(t *testing.T) {
	ctx := context.Background()
	dbPath := "./test_worker_db"
	_ = os.RemoveAll(dbPath)
	defer func() { _ = os.RemoveAll(dbPath) }()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg := &config.Config{
		ProjectRoot: "/non/existent/path",
		DbPath:      dbPath,
		Logger:      logger,
	}

	store, _ := db.Connect(ctx, dbPath, "test_collection", 1024)
	progressMap := &sync.Map{}

	w := &IndexWorker{
		cfg:         cfg,
		logger:      logger,
		progressMap: progressMap,
		storeGetter: func(_ context.Context) (*db.Store, error) { return store, nil },
		embedder:    &mockEmbedder{},
	}

	// Create a temporary directory for the project root.
	root := "./test_project_root"
	_ = os.MkdirAll(root, 0755)
	defer func() { _ = os.RemoveAll(root) }()
	_ = os.WriteFile(root+"/test.go", []byte("package main\nfunc main() {}"), 0644)

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
