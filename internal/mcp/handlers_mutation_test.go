package mcp

import (
	"context"
	"strings"
	"testing"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/security/pathguard"
)

func TestHandleModifyWorkspaceFatToolLimits(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "test_mutation_fat_tool_*")
	defer os.RemoveAll(tempDir)

	validator, _ := pathguard.NewValidator(tempDir, pathguard.DefaultOptions())

	srv := &Server{
		cfg:           &config.Config{ProjectRoot: tempDir},
		pathValidator: validator,
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
			name:          "apply_patch missing args",
			action:        "apply_patch",
			args:          map[string]any{},
			expectPanic:   false,
			errorContains: "path and search string are required",
		},
		{
			name:          "create_file missing args",
			action:        "create_file",
			args:          map[string]any{},
			expectPanic:   false,
			errorContains: "path is required",
		},
		{
			name:          "run_linter missing args",
			action:        "run_linter",
			args:          map[string]any{},
			expectPanic:   false,
			errorContains: "path and tool are required",
		},
		{
			name:          "verify_patch missing args",
			action:        "verify_patch",
			args:          map[string]any{},
			expectPanic:   false,
			errorContains: "path and search are required",
		},
		{
			name:          "auto_fix missing args",
			action:        "auto_fix",
			args:          map[string]any{},
			expectPanic:   false,
			errorContains: "diagnostic_json is required",
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

			res, err := srv.handleModifyWorkspace(context.Background(), req)
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
