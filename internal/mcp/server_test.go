package mcp

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
)

type mockEmbedder struct{}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	emb := make([]float32, 1024)
	emb[0] = 1.0 // Use a non-zero embedding
	return emb, nil
}

func TestIndexStatusTool(t *testing.T) {
	ctx := context.Background()
	dbPath := "./test_mcp_db"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	cfg := &config.Config{
		ProjectRoot: "/test/project",
		DbPath:      dbPath,
	}

	store, _ := db.Connect(ctx, dbPath, "test_collection", 1024)
	// Set status for local project and another project in DB
	store.SetStatus(ctx, "/test/project", "Indexing: 73.2% (73/100) - Current: file.go")
	store.SetStatus(ctx, "/other/project", "Indexing: 10.0% (1/10) - Current: remote.go")

	progressMap := &sync.Map{}
	progressMap.Store("/test/project", "Indexing: 73.2% (73/100) - Current: file.go")

	storeGetter := func(ctx context.Context) (*db.Store, error) { return store, nil }
	
	srv := &Server{
		cfg:              cfg,
		storeGetter:      storeGetter,
		freshStoreGetter: storeGetter,
		progressMap:      progressMap,
	}

	// Test handleIndexStatus directly
	res, err := srv.handleIndexStatus(ctx, mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleIndexStatus failed: %v", err)
	}

	content := res.Content[0].(mcp.TextContent).Text
	
	// Check if local tasks are present
	if !strings.Contains(content, "Local/In-Memory Tasks") {
		t.Error("expected 'Local/In-Memory Tasks' in output")
	}
	if !strings.Contains(content, "/test/project: Indexing: 73.2%") {
		t.Error("expected local task status in output")
	}

	// Check if global tasks (remote) are present
	if !strings.Contains(content, "Global/DB Tasks") {
		t.Error("expected 'Global/DB Tasks' in output")
	}
	if !strings.Contains(content, "/other/project: Indexing: 10.0% (1/10) - Current: remote.go (Remote Process)") {
		t.Error("expected remote task status in output")
	}
}

func TestRetrieveContextTool(t *testing.T) {
	ctx := context.Background()
	dbPath := "./test_retrieve_db"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	cfg := &config.Config{
		ProjectRoot: "/test/project",
		DbPath:      dbPath,
	}

	store, _ := db.Connect(ctx, dbPath, "test_collection", 1024)
	
	// Use the same embedding as the mock embedder
	emb := make([]float32, 1024)
	emb[0] = 1.0

	// Insert dummy record
	err := store.Insert(ctx, []db.Record{
		{
			ID: "test-id-1",
			Content: "func HelloWorld() { fmt.Println(\"Hello\") }",
			Embedding: emb,
			Metadata: map[string]string{
				"path": "hello.go",
				"project_id": "/test/project",
			},
		},
	})
	if err != nil {
		t.Fatalf("failed to insert: %v", err)
	}

	// Verify it's in DB
	results, err := store.Search(ctx, emb, 1, []string{"/test/project"})
	if err != nil {
		t.Fatalf("direct search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("direct search returned 0 results")
	}

	srv := &Server{
		cfg:         cfg,
		storeGetter: func(ctx context.Context) (*db.Store, error) { return store, nil },
		embedder:    &mockEmbedder{},
	}

	// Test with query
	req := mcp.CallToolRequest{}
	req.Params.Name = "retrieve_context"
	req.Params.Arguments = map[string]interface{}{
		"query": "hello world",
	}

	res, err := srv.handleRetrieveContext(ctx, req)
	if err != nil {
		t.Fatalf("handleRetrieveContext failed: %v", err)
	}

	content := res.Content[0].(mcp.TextContent).Text
	if !strings.Contains(content, "HelloWorld") {
		t.Errorf("expected search result in output, got: %s", content)
	}
	if !strings.Contains(content, "hello.go") {
		t.Errorf("expected file path in output, got: %s", content)
	}
}
