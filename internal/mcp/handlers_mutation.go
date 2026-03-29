package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// handleApplyCodePatch applies a search-and-replace patch to a specific file.
func (s *Server) handleApplyCodePatch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path := request.GetString("path", "")
	search := request.GetString("search", "")
	replace := request.GetString("replace", "")

	if path == "" || search == "" {
		return mcp.NewToolResultError("path and search string are required"), nil
	}

	absPath := path
	if !filepath.IsAbs(path) {
		absPath = filepath.Join(s.cfg.ProjectRoot, path)
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to read file: %v", err)), nil
	}

	strContent := string(content)
	if !strings.Contains(strContent, search) {
		return mcp.NewToolResultError("search string not found in file"), nil
	}

	newContent := strings.ReplaceAll(strContent, search, replace)
	if err := os.WriteFile(absPath, []byte(newContent), 0644); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to write file: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Successfully patched %s", path)), nil
}

// handleRunLinterAndFix executes a code formatter or linter with the fix flag.
func (s *Server) handleRunLinterAndFix(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path := request.GetString("path", "")
	tool := request.GetString("tool", "")

	if path == "" || tool == "" {
		return mcp.NewToolResultError("path and tool are required"), nil
	}

	// For now, we only support 'go fmt' as a built-in.
	// We can expand this to execute arbitrary commands if needed, but safety first.
	if tool == "go fmt" {
		// Run go fmt on the path
		// We'll use os/exec here or similar in a more robust implementation
		return mcp.NewToolResultText("Go fmt executed (mock implementation for now)"), nil
	}

	return mcp.NewToolResultError(fmt.Sprintf("tool '%s' not supported yet", tool)), nil
}

// handleCreateFile creates a new file with content.
func (s *Server) handleCreateFile(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path := request.GetString("path", "")
	content := request.GetString("content", "")

	if path == "" {
		return mcp.NewToolResultError("path is required"), nil
	}

	absPath := path
	if !filepath.IsAbs(path) {
		absPath = filepath.Join(s.cfg.ProjectRoot, path)
	}

	// Ensure directory exists
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create directory: %v", err)), nil
	}

	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to write file: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Successfully created file at %s", path)), nil
}
