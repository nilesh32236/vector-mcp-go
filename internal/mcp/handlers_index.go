package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
)

// handleTriggerProjectIndex triggers a full indexing of the specified project path.
// If running as a slave, it delegates the task to the master daemon.
func (s *Server) handleTriggerProjectIndex(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path := request.GetString("project_path", "")
	if path == "" {
		return mcp.NewToolResultError("project_path is required"), nil
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid path: %v", err)), nil
	}

	if s.daemonClient != nil {
		err := s.daemonClient.TriggerIndex(absPath)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to delegate indexing to master daemon: %v", err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Indexing task successfully delegated to the master daemon for %s.", absPath)), nil
	}

	// For MCP clients that support progress notifications
	if request.Params.Meta.ProgressToken != "" {
		go func() {
			store, err := s.getStore(ctx)
			if err != nil {
				s.logger.Error("Failed to get store for tool progress", "error", err)
				return
			}

			localStore, ok := store.(*db.Store)
			if !ok {
				s.logger.Error("Tool progress requires a local store")
				return
			}

			progressCh := make(chan indexer.Progress, 10)
			opts := indexer.IndexerOptions{
				Config: &config.Config{
					ProjectRoot: absPath,
					DbPath:      s.cfg.DbPath,
					ModelsDir:   s.cfg.ModelsDir,
					Logger:      s.logger,
				},
				Store:      localStore,
				Embedder:   s.embedder,
				Logger:     s.logger,
				ProgressCh: progressCh,
			}

			// Background goroutine to forward progress to MCP
			go func() {
				// Capture client session from context if possible
				session := server.ClientSessionFromContext(ctx)
				sessionID := ""
				if session != nil {
					sessionID = session.SessionID()
				}

				for p := range progressCh {
					params := map[string]any{
						"progressToken": request.Params.Meta.ProgressToken,
						"progress":      float64(p.Current),
						"total":         float64(p.Total),
					}
					if sessionID != "" {
						_ = s.MCPServer.SendNotificationToSpecificClient(sessionID, "notifications/progress", params)
					} else {
						s.MCPServer.SendNotificationToAllClients("notifications/progress", params)
					}
				}
			}()

			_, _ = indexer.IndexFullCodebase(ctx, opts)
			close(progressCh)
		}()
		return mcp.NewToolResultText(fmt.Sprintf("Full project indexing started with progress tracking for %s.", absPath)), nil
	}

	s.indexQueue <- absPath
	return mcp.NewToolResultText(fmt.Sprintf("Initial indexing triggered in the background for %s.", absPath)), nil
}

// handleDeleteContext removes specific context records or wipes an entire project index.
func (s *Server) handleDeleteContext(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	targetPath := request.GetString("target_path", "")
	if targetPath == "" {
		return mcp.NewToolResultError("target_path is required"), nil
	}
	projectID := request.GetString("project_id", s.cfg.ProjectRoot)

	dryRun := false
	if args, ok := request.Params.Arguments.(map[string]interface{}); ok {
		dryRun, _ = args["dry_run"].(bool)
	}

	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if dryRun {
		var toDelete []string
		if targetPath == "ALL" {
			toDelete = append(toDelete, "ALL RECORDS for project: "+projectID)
		} else {
			records, _ := store.GetByPrefix(ctx, targetPath, projectID)
			uniquePaths := make(map[string]bool)
			for _, r := range records {
				uniquePaths[r.Metadata["path"]] = true
			}
			for p := range uniquePaths {
				toDelete = append(toDelete, p)
			}
			sort.Strings(toDelete)
		}

		if len(toDelete) == 0 {
			return mcp.NewToolResultText("Dry Run: No records found to delete."), nil
		}
		return mcp.NewToolResultText("Dry Run: The following paths/records would be deleted:\n- " + strings.Join(toDelete, "\n- ")), nil
	}

	if targetPath == "ALL" {
		err = store.ClearProject(ctx, projectID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to clear project: %v", err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Successfully wiped all vectors for project: %s", projectID)), nil
	}
	err = store.DeleteByPrefix(ctx, targetPath, projectID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to delete context: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Deleted context/file: %s from project: %s", targetPath, projectID)), nil
}

// handleIndexStatus returns the current status of the indexing process.
func (s *Server) handleIndexStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	res, _ := s.runStatus(ctx, store)

	// Slaves fetch progress from master via RPC; master reads its own map.
	progressData := make(map[string]string)
	if s.daemonClient != nil {
		if p, err := s.daemonClient.GetProgress(); err == nil {
			progressData = p
		}
	} else {
		s.progressMap.Range(func(k, v interface{}) bool {
			progressData[k.(string)] = v.(string)
			return true
		})
	}

	bgStatus := "\n🚀 Background Indexing Tasks:\n"
	if len(progressData) == 0 {
		bgStatus += "- No active background indexing.\n"
	} else {
		for path, status := range progressData {
			bgStatus += fmt.Sprintf("- %s: %s\n", path, status)
		}
	}

	return mcp.NewToolResultText(res + bgStatus), nil
}

// handleGetIndexingDiagnostics returns detailed diagnostic information about the indexing process and system health.
func (s *Server) handleGetIndexingDiagnostics(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	progressData := make(map[string]string)
	s.progressMap.Range(func(k, v interface{}) bool {
		progressData[k.(string)] = v.(string)
		return true
	})

	status, _ := store.GetStatus(ctx, s.cfg.ProjectRoot)

	var out strings.Builder
	out.WriteString("## 🛠️ Indexing Diagnostics\n\n")
	out.WriteString(fmt.Sprintf("**Active Project Root**: `%s`\n", s.cfg.ProjectRoot))
	out.WriteString(fmt.Sprintf("**Global Index Status**: %s\n\n", status))

	out.WriteString("### 🚀 Active Background Tasks\n")
	if len(progressData) == 0 {
		out.WriteString("- No active background indexing tasks.\n")
	} else {
		for path, prog := range progressData {
			out.WriteString(fmt.Sprintf("- **%s**: %s\n", path, prog))
		}
	}

	out.WriteString("\n### 📊 Database Statistics\n")
	count := store.Count()
	out.WriteString(fmt.Sprintf("- **Total Chunks Indexed**: %d\n", count))

	// In a real implementation, we'd fetch actual error logs from the worker.
	// For now, we'll provide guidance on how to check logs.
	out.WriteString("\n### 🔍 Troubleshooting\n")
	out.WriteString("- If indexing is stuck, check the master daemon logs.\n")
	out.WriteString("- Ensure the file watcher is enabled if real-time updates are missing.\n")

	return mcp.NewToolResultText(out.String()), nil
}

// runStatus is a helper that compares disk files with the database to determine indexing health.
func (s *Server) runStatus(ctx context.Context, store IndexerStore) (string, error) {
	diskFiles, _ := indexer.ScanFiles(s.cfg.ProjectRoot)
	dbMapping, _ := store.GetPathHashMapping(ctx, s.cfg.ProjectRoot)
	var indexed, updated, missing []string
	diskPaths := make(map[string]bool)
	for _, absPath := range diskFiles {
		relPath := config.GetRelativePath(absPath, s.cfg.ProjectRoot)
		diskPaths[relPath] = true
		currentHash, _ := indexer.GetHash(absPath)
		if dbHash, exists := dbMapping[relPath]; exists {
			if dbHash == currentHash {
				indexed = append(indexed, relPath)
			} else {
				updated = append(updated, relPath)
			}
		} else {
			missing = append(missing, relPath)
		}
	}
	var deleted []string
	for dbPath := range dbMapping {
		if !diskPaths[dbPath] {
			deleted = append(deleted, dbPath)
		}
	}
	var out strings.Builder
	out.WriteString(fmt.Sprintf("🔍 Index Status for %s:\n", s.cfg.ProjectRoot))
	out.WriteString(fmt.Sprintf("✅ Fully Indexed: %d\n🔄 Modified: %d\n📂 Missing: %d\n🗑️ Deleted: %d\n", len(indexed), len(updated), len(missing), len(deleted)))
	if len(missing) > 0 {
		out.WriteString("\n📂 Missing Files (Next to index):\n")
		for i, f := range missing {
			if i >= 10 {
				out.WriteString("  ... and more\n")
				break
			}
			out.WriteString(fmt.Sprintf("  - %s\n", f))
		}
	}
	if len(updated) > 0 {
		out.WriteString("\n🔄 Modified Files (Need update):\n")
		for i, f := range updated {
			if i >= 10 {
				out.WriteString("  ... and more\n")
				break
			}
			out.WriteString(fmt.Sprintf("  - %s\n", f))
		}
	}
	status, _ := store.GetStatus(ctx, s.cfg.ProjectRoot)
	if status != "" {
		out.WriteString(fmt.Sprintf("\n🛰️ Background Status (from DB): %s\n", status))
	}
	return out.String(), nil
}
