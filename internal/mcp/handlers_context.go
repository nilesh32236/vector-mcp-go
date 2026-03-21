package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
)

// handleSetProjectRoot updates the active project root directory and resets the file watcher.
func (s *Server) handleSetProjectRoot(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path := request.GetString("project_path", "")
	if path == "" {
		return mcp.NewToolResultError("project_path is required"), nil
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid path: %v", err)), nil
	}
	s.cfg.ProjectRoot = absPath
	s.monorepoResolver = indexer.InitResolver(s.cfg.ProjectRoot)
	select {
	case s.watcherResetChan <- absPath:
		return mcp.NewToolResultText(fmt.Sprintf("Project root updated to %s. File watcher is resetting.", absPath)), nil
	default:
		return mcp.NewToolResultText(fmt.Sprintf("Project root updated to %s, but watcher reset signal was blocked.", absPath)), nil
	}
}

// handleStoreContext saves manual context, rules, or architectural decisions into the vector database.
func (s *Server) handleStoreContext(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	text := request.GetString("text", "")
	if text == "" {
		return mcp.NewToolResultError("text is required"), nil
	}
	projectID := request.GetString("project_id", s.cfg.ProjectRoot)
	emb, err := s.embedder.Embed(ctx, text)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to generate embedding: %v", err)), nil
	}
	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	err = store.Insert(ctx, []db.Record{{
		ID:        fmt.Sprintf("context-%d", time.Now().UnixNano()),
		Content:   fmt.Sprintf("// Shared Context\n%s", text),
		Embedding: emb,
		Metadata: map[string]string{
			"project_id": projectID,
			"type":       "shared_knowledge",
		},
	}})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to store context: %v", err)), nil
	}
	return mcp.NewToolResultText("Context successfully stored in the global brain."), nil
}
