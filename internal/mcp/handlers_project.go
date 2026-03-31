package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
	"github.com/nilesh32236/vector-mcp-go/internal/util"
)

// handleGetCodebaseSkeleton returns a topological tree map of the codebase.
func (s *Server) handleGetCodebaseSkeleton(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	targetPath := request.GetString("target_path", "")
	maxDepth := util.ClampInt(int(request.GetFloat("max_depth", 3)), 0, 20)
	includePattern := request.GetString("include_pattern", "")
	excludePattern := request.GetString("exclude_pattern", "")
	maxItems := util.ClampInt(int(request.GetFloat("max_items", 1000)), 1, 10000)

	root := s.cfg.ProjectRoot
	if targetPath != "" {
		if filepath.IsAbs(targetPath) {
			root = targetPath
		} else {
			root = filepath.Join(s.cfg.ProjectRoot, targetPath)
		}
	}

	type Node struct {
		Name     string
		Children []*Node
		IsFile   bool
	}

	itemCount := 0
	var buildTree func(string, int) (*Node, error)
	buildTree = func(path string, depth int) (*Node, error) {
		if depth > maxDepth || itemCount >= maxItems {
			return nil, nil
		}

		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}

		name := filepath.Base(path)
		node := &Node{Name: name, IsFile: !info.IsDir()}

		if !info.IsDir() {
			itemCount++
			return node, nil
		}

		// Apply filters to directories too if needed, but usually we filter files
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			if itemCount >= maxItems {
				break
			}

			entryName := entry.Name()
			if indexer.IsIgnoredDir(entryName) || indexer.IsIgnoredFile(entryName) {
				continue
			}

			// Apply patterns
			if includePattern != "" && !strings.Contains(entryName, includePattern) && !entry.IsDir() {
				continue
			}
			if excludePattern != "" && strings.Contains(entryName, excludePattern) {
				continue
			}

			childPath := filepath.Join(path, entryName)
			childNode, err := buildTree(childPath, depth+1)
			if err != nil {
				continue
			}
			if childNode != nil {
				node.Children = append(node.Children, childNode)
			}
		}

		return node, nil
	}

	tree, err := buildTree(root, 0)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to build tree: %v", err)), nil
	}

	if tree == nil {
		return mcp.NewToolResultText("No items found matching the criteria."), nil
	}

	var formatTree func(*Node, string) string
	formatTree = func(n *Node, indent string) string {
		var res strings.Builder
		res.WriteString(indent)
		if n.IsFile {
			res.WriteString("📄 ")
		} else {
			res.WriteString("📁 ")
		}
		res.WriteString(n.Name)
		res.WriteString("\n")

		sort.Slice(n.Children, func(i, j int) bool {
			if n.Children[i].IsFile != n.Children[j].IsFile {
				return !n.Children[i].IsFile // Dirs first
			}
			return n.Children[i].Name < n.Children[j].Name
		})

		for _, child := range n.Children {
			res.WriteString(formatTree(child, indent+"  "))
		}
		return res.String()
	}

	skeleton := formatTree(tree, "")
	return mcp.NewToolResultText(fmt.Sprintf("Codebase Skeleton for %s (Items: %d):\n\n%s", root, itemCount, skeleton)), nil
}

// handleWorkspaceManager unifies project configuration and index management tasks into a single "Fat Tool".
func (s *Server) handleWorkspaceManager(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	action := request.GetString("action", "")
	path := request.GetString("path", "")

	switch action {
	case "set_project_root":
		return s.handleSetProjectRoot(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]interface{}{
					"project_path": path,
				},
			},
		})
	case "trigger_index":
		return s.handleTriggerProjectIndex(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Arguments: map[string]interface{}{
					"project_path": path,
				},
			},
		})
	case "get_indexing_diagnostics":
		return s.handleGetIndexingDiagnostics(ctx, mcp.CallToolRequest{})
	default:
		return mcp.NewToolResultError(fmt.Sprintf("Invalid action: %s. Must be 'set_project_root', 'trigger_index', or 'get_indexing_diagnostics'", action)), nil
	}
}
