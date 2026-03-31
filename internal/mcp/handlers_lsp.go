package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/util"
)

// handleGetPreciseDefinition uses the LSP to find exact symbol definitions.
func (s *Server) handleGetPreciseDefinition(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path := request.GetString("path", "")
	line := util.ClampInt(int(request.GetFloat("line", 0)), 0, 1_000_000)
	char := util.ClampInt(int(request.GetFloat("character", 0)), 0, 10_000)

	if path == "" {
		return mcp.NewToolResultError("path is required"), nil
	}

	lspManager, _, err := s.getLSPManagerForFile(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get LSP session: %v", err)), nil
	}

	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": fmt.Sprintf("file://%s", path),
		},
		"position": map[string]interface{}{
			"line":      line,
			"character": char,
		},
	}

	var result []interface{}
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
func (s *Server) handleFindReferencesPrecise(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path := request.GetString("path", "")
	line := util.ClampInt(int(request.GetFloat("line", 0)), 0, 1_000_000)
	char := util.ClampInt(int(request.GetFloat("character", 0)), 0, 10_000)

	if path == "" {
		return mcp.NewToolResultError("path is required"), nil
	}

	lspManager, _, err := s.getLSPManagerForFile(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get LSP session: %v", err)), nil
	}

	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": fmt.Sprintf("file://%s", path),
		},
		"position": map[string]interface{}{
			"line":      line,
			"character": char,
		},
		"context": map[string]interface{}{
			"includeDeclaration": true,
		},
	}

	var result []interface{}
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
func (s *Server) handleGetTypeHierarchy(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path := request.GetString("path", "")
	line := util.ClampInt(int(request.GetFloat("line", 0)), 0, 1_000_000)
	char := util.ClampInt(int(request.GetFloat("character", 0)), 0, 10_000)

	if path == "" {
		return mcp.NewToolResultError("path is required"), nil
	}

	lspManager, _, err := s.getLSPManagerForFile(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get LSP session: %v", err)), nil
	}

	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": fmt.Sprintf("file://%s", path),
		},
		"position": map[string]interface{}{
			"line":      line,
			"character": char,
		},
	}

	var result interface{}
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
	line := util.ClampInt(int(request.GetFloat("line", 0)), 0, 1_000_000)
	character := util.ClampInt(int(request.GetFloat("character", 0)), 0, 10_000)

	switch action {
	case "definition":
		return s.handleGetPreciseDefinition(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]interface{}{
					"path":      path,
					"line":      float64(line),
					"character": float64(character),
				},
			},
		})
	case "references":
		return s.handleFindReferencesPrecise(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]interface{}{
					"path":      path,
					"line":      float64(line),
					"character": float64(character),
				},
			},
		})
	case "type_hierarchy":
		return s.handleGetTypeHierarchy(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]interface{}{
					"path":      path,
					"line":      float64(line),
					"character": float64(character),
				},
			},
		})
	case "impact_analysis":
		return s.handleGetImpactAnalysis(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]interface{}{
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
