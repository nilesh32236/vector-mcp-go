package mcp

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
)

func TestHandleSearchWorkspace(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/test_search_workspace_db"

	cfg := &config.Config{
		ProjectRoot: t.TempDir(), // Empty project root
		DbPath:      dbPath,
	}

	store, err := db.Connect(ctx, dbPath, "test_collection", 1024)
	if err != nil {
		t.Fatalf("Failed to connect to DB: %v", err)
	}

	srv := &Server{
		cfg:              cfg,
		localStoreGetter: func(_ context.Context) (*db.Store, error) { return store, nil },
		embedder:         &mockEmbedder{dim: 1024},
		graph:            db.NewKnowledgeGraph(), // Need this to avoid nil pointer panic on graph action
		progressMap:      &sync.Map{},            // Need this to avoid nil pointer panic on index_status action
	}

	tests := []struct {
		name          string
		action        string
		query         string
		limit         float64
		pathFilter    string
		expectError   bool
		expectedMsg   string
	}{
		{
			name:        "invalid action",
			action:      "invalid_action",
			query:       "foo",
			limit:       10,
			expectError: true,
			expectedMsg: "Invalid action",
		},
		{
			name:        "vector search - valid limit",
			action:      "vector",
			query:       "foo",
			limit:       10,
			expectError: false,
			expectedMsg: "No matches found",
		},
		{
			name:        "vector search - large limit clamped",
			action:      "vector",
			query:       "foo",
			limit:       999999999, // Should be clamped to 100, won't panic
			expectError: false,
			expectedMsg: "No matches found",
		},
		{
			name:        "vector search - negative limit clamped",
			action:      "vector",
			query:       "foo",
			limit:       -500, // Should be adjusted to 10 then clamped
			expectError: false,
			expectedMsg: "No matches found",
		},
		{
			name:        "regex search",
			action:      "regex",
			query:       "foo",
			limit:       10,
			expectError: false,
			expectedMsg: "No matches found",
		},
		{
			name:        "graph search",
			action:      "graph",
			query:       "someInterface",
			limit:       10,
			expectError: false,
			expectedMsg: "No implementations found for interface",
		},
		{
			name:        "index_status",
			action:      "index_status",
			query:       "",
			limit:       10,
			expectError: false,
			expectedMsg: "Background Indexing Tasks",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]any{
				"action": tt.action,
				"query":  tt.query,
				"limit":  tt.limit,
				"path":   tt.pathFilter,
			}

			res, err := srv.handleSearchWorkspace(ctx, req)
			if err != nil {
				t.Fatalf("Unexpected error from handleSearchWorkspace: %v", err)
			}

			if res.IsError != tt.expectError {
				t.Errorf("expected IsError=%v, got %v", tt.expectError, res.IsError)
			}

			if len(res.Content) > 0 {
				if textContent, ok := res.Content[0].(mcp.TextContent); ok {
					if !strings.Contains(textContent.Text, tt.expectedMsg) {
						t.Errorf("expected output to contain %q, got: %q", tt.expectedMsg, textContent.Text)
					}
				} else {
					t.Errorf("expected mcp.TextContent, got %T", res.Content[0])
				}
			} else {
				t.Errorf("expected content in result, got none")
			}
		})
	}
}
