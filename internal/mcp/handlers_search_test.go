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
	"github.com/nilesh32236/vector-mcp-go/internal/security/pathguard"
)

func TestHandleSearchWorkspace(t *testing.T) {
	ctx := context.Background()
	tempDir, err := os.MkdirTemp("", "test_search_workspace_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := tempDir + "/db"
	cfg := &config.Config{
		ProjectRoot: tempDir,
		DbPath:      dbPath,
	}

	store, err := db.Connect(ctx, dbPath, "test_collection", 1024)
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}

	validator, err := pathguard.NewValidator(tempDir, pathguard.DefaultOptions())
	if err != nil {
		t.Fatalf("pathguard.NewValidator: %v", err)
	}

	srv := &Server{
		cfg:              cfg,
		localStoreGetter: func(_ context.Context) (*db.Store, error) { return store, nil },
		embedder:         &mockEmbedder{dim: 1024},
		pathValidator:    validator,
		graph:            db.NewKnowledgeGraph(),
		progressMap:      &sync.Map{},
	}

	tests := []struct {
		name         string
		action       string
		query        string
		limit        float64
		expectedErr  bool
		expectedText string
	}{
		{
			name:         "invalid action",
			action:       "unknown_action",
			expectedErr:  false, // returns mcp.NewToolResultError
			expectedText: "Invalid action",
		},
		{
			name:         "vector action",
			action:       "vector",
			query:        "test query",
			limit:        10,
			expectedText: "No matches found",
		},
		{
			name:         "regex action",
			action:       "regex",
			query:        "test query",
			limit:        -5, // Test clamp limit
			expectedText: "No matches found",
		},
		{
			name:         "graph action",
			action:       "graph",
			query:        "MyInterface",
			expectedText: "No implementations found",
		},
		{
			name:         "index_status action",
			action:       "index_status",
			expectedText: "Background Indexing Tasks:",
		},
		{
			name:         "panic prevention extreme limit",
			action:       "vector",
			query:        "test limit",
			limit:        999999999, // very high limit
			expectedText: "No matches found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]any{
				"action": tt.action,
				"query":  tt.query,
				"limit":  tt.limit,
			}

			res, err := srv.handleSearchWorkspace(ctx, req)
			if (err != nil) != tt.expectedErr {
				t.Fatalf("expected error %v, got %v", tt.expectedErr, err)
			}

			if res == nil {
				t.Fatalf("expected result, got nil")
			}

			content := ""
			if len(res.Content) > 0 {
				if tc, ok := res.Content[0].(mcp.TextContent); ok {
					content = tc.Text
				}
			}

			if res.IsError {
				content = res.Content[0].(mcp.TextContent).Text
			}

			if !strings.Contains(content, tt.expectedText) {
				t.Errorf("expected text %q to be in %q", tt.expectedText, content)
			}
		})
	}
}
