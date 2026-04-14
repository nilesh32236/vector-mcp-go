package mcp

import (
	"context"
	"strings"
	"sync"
	"testing"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
)

func TestHandleSearchWorkspaceFatTool(t *testing.T) {
	ctx := context.Background()
	dbPath := "./test_fat_tool_db"
	_ = os.RemoveAll(dbPath)
	defer func() { _ = os.RemoveAll(dbPath) }()

	cfg := &config.Config{
		ProjectRoot: ".",
		DbPath:      dbPath,
	}
	store, _ := db.Connect(ctx, dbPath, "test_collection", 1024)

	srv := &Server{
		cfg:              cfg,
		localStoreGetter: func(_ context.Context) (*db.Store, error) { return store, nil },
		embedder:         &mockEmbedder{dim: 1024},
		graph:            db.NewKnowledgeGraph(),
		progressMap:      &sync.Map{},
	}

	tests := []struct {
		name          string
		action        string
		args          map[string]any
		expectError   bool
		errorContains string
	}{
		{
			name:          "invalid action",
			action:        "unknown",
			args:          map[string]any{},
			expectError:   true,
			errorContains: "Invalid action",
		},
		{
			name:          "vector search missing query",
			action:        "vector",
			args:          map[string]any{},
			expectError:   true,
			errorContains: "query is required",
		},
		{
			name:          "vector search valid",
			action:        "vector",
			args:          map[string]any{"query": "test"},
			expectError:   false,
			errorContains: "",
		},
		{
			name:          "regex search missing query",
			action:        "regex",
			args:          map[string]any{},
			expectError:   true,
			errorContains: "query is required",
		},
		{
			name:          "regex search valid",
			action:        "regex",
			args:          map[string]any{"query": "test_regex_query_not_found"},
			expectError:   false,
			errorContains: "",
		},
		{
			name:          "graph missing interface",
			action:        "graph",
			args:          map[string]any{},
			expectError:   false,
			errorContains: "No implementations found",
		},
		{
			name:          "index_status valid",
			action:        "index_status",
			args:          map[string]any{},
			expectError:   false,
			errorContains: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := mcp.CallToolRequest{}
			tt.args["action"] = tt.action
			req.Params.Arguments = tt.args

			res, err := srv.handleSearchWorkspace(ctx, req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Some handlers return mcp.NewToolResultError which sets IsError=true
			if tt.expectError && !res.IsError {
				t.Errorf("expected error result but got success")
			}

			if tt.errorContains != "" {
				content := res.Content[0].(mcp.TextContent).Text
				if !strings.Contains(content, tt.errorContains) {
					t.Errorf("expected output to contain %q, got %q", tt.errorContains, content)
				}
			}
		})
	}
}

func TestSearchWorkspaceLimitsAndBounds(t *testing.T) {
	ctx := context.Background()
	dbPath := "./test_fat_tool_db2"
	_ = os.RemoveAll(dbPath)
	defer func() { _ = os.RemoveAll(dbPath) }()

	cfg := &config.Config{
		ProjectRoot: ".",
		DbPath:      dbPath,
	}
	store, _ := db.Connect(ctx, dbPath, "test_collection", 1024)

	srv := &Server{
		cfg:              cfg,
		localStoreGetter: func(_ context.Context) (*db.Store, error) { return store, nil },
		embedder:         &mockEmbedder{dim: 1024},
		graph:            db.NewKnowledgeGraph(),
		progressMap:      &sync.Map{},
	}

	tests := []struct {
		name        string
		limit       any
		expectPanic bool
	}{
		{"negative limit", -10, false},
		{"zero limit", 0, false},
		{"huge limit", 999999999, false},
		{"string limit", "10", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil && !tt.expectPanic {
					t.Errorf("unexpected panic: %v", r)
				}
			}()

			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]any{
				"action": "vector",
				"query":  "test",
				"limit":  tt.limit,
			}

			_, _ = srv.handleSearchWorkspace(ctx, req)
		})
	}
}
