package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/util"
)

// parseAndClampLSPPosition reads and clamps LSP line/character request arguments once.
func parseAndClampLSPPosition(request mcp.CallToolRequest) (int, int) {
	line := util.ClampInt(int(request.GetFloat("line", 0)), 0, 1_000_000)
	character := util.ClampInt(int(request.GetFloat("character", 0)), 0, 10_000)
	return line, character
}

// handleGetPreciseDefinition uses the LSP to find exact symbol definitions.
func (s *Server) handleGetPreciseDefinition(ctx context.Context, path string, line, character int) (*mcp.CallToolResult, error) {

	if path == "" {
		return mcp.NewToolResultError("path is required"), nil
	}

	lspManager, _, err := s.getLSPManagerForFile(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get LSP session: %v", err)), nil
	}

	params := map[string]any{
		"textDocument": map[string]any{
			"uri": fmt.Sprintf("file://%s", path),
		},
		"position": map[string]any{
			"line":      line,
			"character": character,
		},
	}

	var result []any
	err = lspManager.Call(ctx, "textDocument/definition", params, &result)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("LSP call failed: %v", err)), nil
	}

	if len(result) == 0 {
		return mcp.NewToolResultText("No definition found"), nil
	}

	// Simplistic parsing of LSP Location or LocationLink
	return mcp.NewToolResultText(fmt.Sprintf("Definition found: %+v", result[0])), nil
}

// handleFindReferencesPrecise uses the LSP to find all usages of a symbol.
func (s *Server) handleFindReferencesPrecise(ctx context.Context, path string, line, character int) (*mcp.CallToolResult, error) {
	if path == "" {
		return mcp.NewToolResultError("path is required"), nil
	}

	lspManager, _, err := s.getLSPManagerForFile(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get LSP session: %v", err)), nil
	}

	params := map[string]any{
		"textDocument": map[string]any{
			"uri": fmt.Sprintf("file://%s", path),
		},
		"position": map[string]any{
			"line":      line,
			"character": character,
		},
		"context": map[string]any{
			"includeDeclaration": true,
		},
	}

	var result []any
	err = lspManager.Call(ctx, "textDocument/references", params, &result)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("LSP call failed: %v", err)), nil
	}

	if len(result) == 0 {
		return mcp.NewToolResultText("No references found"), nil
	}

	var builders []string
	for _, ref := range result {
		builders = append(builders, fmt.Sprintf("%+v", ref))
	}

	return mcp.NewToolResultText(fmt.Sprintf("Found %d references:\n%s", len(result), strings.Join(builders, "\n"))), nil
}

// handleGetTypeHierarchy uses the LSP to map out type structures.
func (s *Server) handleGetTypeHierarchy(ctx context.Context, path string, line, character int) (*mcp.CallToolResult, error) {
	if path == "" {
		return mcp.NewToolResultError("path is required"), nil
	}

	lspManager, _, err := s.getLSPManagerForFile(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get LSP session: %v", err)), nil
	}

	params := map[string]any{
		"textDocument": map[string]any{
			"uri": fmt.Sprintf("file://%s", path),
		},
		"position": map[string]any{
			"line":      line,
			"character": character,
		},
	}

	var result any
	// First call textDocument/prepareTypeHierarchy
	err = lspManager.Call(ctx, "textDocument/prepareTypeHierarchy", params, &result)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("LSP call failed: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Type Hierarchy Root: %+v", result)), nil
}

// handleLspQuery unifies precise LSP capabilities into a single "Fat Tool".
func (s *Server) handleLspQuery(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	action := request.GetString("action", "")
	path := request.GetString("path", "")
	line, character := parseAndClampLSPPosition(request)

	switch action {
	case "definition":
		return s.handleGetPreciseDefinition(ctx, path, line, character)
	case "references":
		return s.handleFindReferencesPrecise(ctx, path, line, character)
	case "type_hierarchy":
		return s.handleGetTypeHierarchy(ctx, path, line, character)
	case "impact_analysis":
		return s.handleGetImpactAnalysis(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]any{
					"path":      path,
					"line":      float64(line),
					"character": float64(character),
				},
			},
		})
	default:
		return mcp.NewToolResultError(fmt.Sprintf("Invalid action: %s. Must be 'definition', 'references', 'type_hierarchy', or 'impact_analysis'", action)), nil
	}
}
