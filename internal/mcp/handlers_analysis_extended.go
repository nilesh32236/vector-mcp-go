package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/util"
)

// handleGetImpactAnalysis uses the LSP to identify the "blast radius" of a change to a symbol.
// It finds all references across the project and summarizes the potential impact.
func (s *Server) handleGetImpactAnalysis(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path := request.GetString("path", "")
	line := util.ClampInt(int(request.GetFloat("line", 0)), 0, 1_000_000)
	character := util.ClampInt(int(request.GetFloat("character", 0)), 0, 10_000)

	if path == "" {
		return mcp.NewToolResultError("path is required"), nil
	}

	lspManager, _, err := s.getLSPManagerForFile(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get LSP session: %v", err)), nil
	}

	// 1. Get references via LSP
	params := map[string]interface{}{
		"textDocument": map[string]string{"uri": fmt.Sprintf("file://%s", path)},
		"position":     map[string]int{"line": line, "character": character},
		"context":      map[string]bool{"includeDeclaration": false},
	}

	var res interface{}
	err = lspManager.Call(ctx, "textDocument/references", params, &res)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("LSP references call failed: %v", err)), nil
	}

	refs, ok := res.([]interface{})
	if !ok || len(refs) == 0 {
		return mcp.NewToolResultText("No downstream impact detected. This symbol appears to be unused or internal to this scope."), nil
	}

	// 2. Analyze "Blast Radius"
	uniqueFiles := make(map[string]bool)
	for _, r := range refs {
		if m, ok := r.(map[string]interface{}); ok {
			if uri, ok := m["uri"].(string); ok {
				uniqueFiles[uri] = true
			}
		}
	}

	risk := "Low"
	if len(uniqueFiles) > 3 {
		risk = "Medium"
	}
	if len(uniqueFiles) > 10 {
		risk = "High"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### Impact Analysis results:\n"))
	sb.WriteString(fmt.Sprintf("- **Risk Level**: %s\n", risk))
	sb.WriteString(fmt.Sprintf("- **Total References**: %d\n", len(refs)))
	sb.WriteString(fmt.Sprintf("- **Impacted Files**: %d\n\n", len(uniqueFiles)))
	sb.WriteString("#### Details:\n")

	count := 0
	for f := range uniqueFiles {
		if count >= 10 {
			sb.WriteString("- ... and more\n")
			break
		}
		sb.WriteString(fmt.Sprintf("- %s\n", strings.TrimPrefix(f, "file://")))
		count++
	}

	return mcp.NewToolResultText(sb.String()), nil
}
