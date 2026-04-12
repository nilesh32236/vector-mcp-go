package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/analysis"
)

// handleDistillPackagePurpose invokes the Distiller to generate a high-level manual summary of a package.
// This summary is saved in the vector DB with a high priority (2.0) for future semantic searches.
func (s *Server) handleDistillPackagePurpose(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pkgPath := request.GetString("path", "")
	if pkgPath == "" {
		return mcp.NewToolResultError("path (package directory) is required"), nil
	}

	absPath, err := s.validatePath(pkgPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid path: %v", err)), nil
	}

	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get store: %v", err)), nil
	}

	distiller := analysis.NewDistiller(store, s.embedder, s.logger)
	summary, err := distiller.DistillPackagePurpose(ctx, s.projectRoot(), absPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Distillation failed: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("### Distillation Successful\n\n**Package**: %s\n**Summary**:\n%s\n\n*This distillation has been re-indexed with a 2.0x priority multiplier for semantic retrieval.*", pkgPath, summary)), nil
}
