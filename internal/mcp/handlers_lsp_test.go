package mcp

import (
	"context"
	"strings"
	"testing"
	"sync"
	"log/slog"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/system"
)

func TestHandleLspQueryFatToolLimits(t *testing.T) {
	srv := &Server{
		lspSessions: make(map[string]lspManagerInterface),
		lspMu:       sync.Mutex{},
		cfg:         &config.Config{ProjectRoot: "."},
		logger:      slog.New(slog.NewTextHandler(os.Stdout, nil)),
		throttler:   system.NewMemThrottler(1024, 80),
	}

	tests := []struct {
		name          string
		action        string
		args          map[string]any
		expectPanic   bool
		errorContains string
	}{
		{
			name:          "invalid action",
			action:        "unknown",
			args:          map[string]any{},
			expectPanic:   false,
			errorContains: "Invalid action",
		},
		{
			name:          "definition missing path",
			action:        "definition",
			args:          map[string]any{},
			expectPanic:   false,
			errorContains: "path is required",
		},
		{
			name:          "negative bounds",
			action:        "definition",
			args:          map[string]any{"path": "foo.go", "line": -10, "character": -5},
			expectPanic:   false,
			errorContains: "LSP call failed",
		},
		{
			name:          "huge bounds",
			action:        "references",
			args:          map[string]any{"path": "foo.go", "line": 99999999, "character": 99999999},
			expectPanic:   false,
			errorContains: "LSP call failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil && !tt.expectPanic {
					t.Errorf("unexpected panic: %v", r)
				}
			}()

			req := mcp.CallToolRequest{}
			tt.args["action"] = tt.action
			req.Params.Arguments = tt.args

			res, err := srv.handleLspQuery(context.Background(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.errorContains != "" {
				content := res.Content[0].(mcp.TextContent).Text
				if !strings.Contains(content, tt.errorContains) {
					t.Errorf("expected error to contain %q, got %q", tt.errorContains, content)
				}
			}
		})
	}
}
