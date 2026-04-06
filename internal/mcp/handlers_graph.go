package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func (s *Server) handleGetInterfaceImplementations(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments"), nil
	}
	name, ok := args["interface_name"].(string)
	if !ok {
		return mcp.NewToolResultError("interface_name is required"), nil
	}

	impls := s.graph.GetImplementations(name)
	if len(impls) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No implementations found for interface '%s'.", name)), nil
	}

	var res strings.Builder
	res.WriteString(fmt.Sprintf("Found %d implementations for '%s':\n", len(impls), name))
	for _, node := range impls {
		res.WriteString(fmt.Sprintf("- %s (Type: %s, Path: %s)\n", node.Name, node.Type, node.Path))
	}

	return mcp.NewToolResultText(res.String()), nil
}

func (s *Server) handleTraceDataFlow(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments"), nil
	}
	name, ok := args["field_name"].(string)
	if !ok {
		return mcp.NewToolResultError("field_name is required"), nil
	}

	nodes := s.graph.FindUsage(name)
	if len(nodes) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No entities found using symbol '%s'.", name)), nil
	}

	var res strings.Builder
	res.WriteString(fmt.Sprintf("Entities using/containing symbol '%s':\n", name))
	for _, node := range nodes {
		res.WriteString(fmt.Sprintf("- %s (Type: %s, Path: %s)\n", node.Name, node.Type, node.Path))
		if node.Docstring != "" {
			res.WriteString(fmt.Sprintf("  Doc: %s\n", node.Docstring))
		}
	}

	return mcp.NewToolResultText(res.String()), nil
}

func (s *Server) handleGetImpactRadiusPrecise(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments"), nil
	}
	name, ok := args["symbol_name"].(string)
	if !ok {
		return mcp.NewToolResultError("symbol_name is required"), nil
	}

	nodes := s.graph.SearchByName(name)
	if len(nodes) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No entities found matching '%s'.", name)), nil
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Precise Impact Analysis for '%s':\n", name))
	for _, node := range nodes {
		output.WriteString(fmt.Sprintf("\nEntity: %s (%s) in %s\n", node.Name, node.Type, node.Path))

		// 1. Structural Dependents (Implementations)
		if node.Type == "interface" || node.Type == "interface_type" {
			impls := s.graph.GetImplementations(node.Name)
			if len(impls) > 0 {
				output.WriteString("  - 🔄 Implemented by:\n")
				for _, impl := range impls {
					output.WriteString(fmt.Sprintf("    * %s (%s)\n", impl.Name, impl.Path))
				}
			}
		}

		// 2. Data Flow Dependents
		// (Future: follow CallTree edges)
	}

	return mcp.NewToolResultText(output.String()), nil
}
