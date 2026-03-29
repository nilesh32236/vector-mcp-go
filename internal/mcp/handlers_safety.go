package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/mutation"
)

// handleVerifyPatchIntegrity uses the LSP to check if a proposed patch introduces compiler errors.
func (s *Server) handleVerifyPatchIntegrity(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path := request.GetString("path", "")
	search := request.GetString("search", "")
	replace := request.GetString("replace", "")

	if path == "" || search == "" {
		return mcp.NewToolResultError("path and search are required"), nil
	}

	diags, err := s.safety.VerifyPatchIntegrity(ctx, path, search, replace)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("verification failed: %v", err)), nil
	}

	if len(diags) == 0 {
		return mcp.NewToolResultText("Patch verified. No compiler errors introduced."), nil
	}

	var builders []string
	for _, d := range diags {
		severity := "Error"
		if d.Severity == 2 {
			severity = "Warning"
		}
		builders = append(builders, fmt.Sprintf("[%s] line %d: %s", severity, d.Range.Start.Line, d.Message))
	}

	return mcp.NewToolResultText(fmt.Sprintf("Patch verification found issues:\n%s", strings.Join(builders, "\n"))), nil
}

// handleAutoFixMutation takes a diagnostic and returns a fix suggestion.
func (s *Server) handleAutoFixMutation(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	diagJSON := request.GetString("diagnostic_json", "")
	if diagJSON == "" {
		return mcp.NewToolResultError("diagnostic_json is required"), nil
	}

	var diag mutation.Diagnostic
	if err := json.Unmarshal([]byte(diagJSON), &diag); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid diagnostic JSON: %v", err)), nil
	}

	suggestion := s.safety.AutoFixMutation(diag)
	return mcp.NewToolResultText(suggestion), nil
}
