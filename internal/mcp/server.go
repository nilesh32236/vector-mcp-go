package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
)

// Server is the core MCP server that manages tools and handles requests.
type Server struct {
	cfg              *config.Config
	logger           *slog.Logger
	mcpServer        *server.MCPServer
	storeGetter      func(ctx context.Context) (*db.Store, error)
	freshStoreGetter func(ctx context.Context) (*db.Store, error)
	embedder         indexer.Embedder
	indexQueue       chan string
	progressMap      *sync.Map
	watcherResetChan chan string
	monorepoResolver *indexer.WorkspaceResolver
}

// NewServer creates a new Server instance and registers its tools.
func NewServer(cfg *config.Config, logger *slog.Logger, storeGetter func(ctx context.Context) (*db.Store, error), freshStoreGetter func(ctx context.Context) (*db.Store, error), embedder indexer.Embedder, queue chan string, progress *sync.Map, resetChan chan string, resolver *indexer.WorkspaceResolver) *Server {
	s := server.NewMCPServer("vector-mcp-go", "1.0.0", server.WithLogging())
	srv := &Server{
		cfg:              cfg,
		logger:           logger,
		mcpServer:        s,
		storeGetter:      storeGetter,
		freshStoreGetter: freshStoreGetter,
		embedder:         embedder,
		indexQueue:       queue,
		progressMap:      progress,
		watcherResetChan: resetChan,
		monorepoResolver: resolver,
	}
	srv.registerTools()
	return srv
}

// Serve starts the MCP server on stdio.
func (s *Server) Serve() error {
	s.logger.Info("MCP Server listening on stdio...")
	return server.ServeStdio(s.mcpServer)
}

func (s *Server) registerTools() {
	// 0. ping
	s.mcpServer.AddTool(mcp.NewTool("ping", mcp.WithDescription("Check server connectivity")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("pong"), nil
		})

	// 1. trigger_project_index
	s.mcpServer.AddTool(mcp.NewTool("trigger_project_index",
		mcp.WithDescription("Trigger a full background index of a project. Use this when you first open a project or after major changes to ensure the vector index is up to date."),
		mcp.WithString("project_path", mcp.Description("Absolute path to the project root")),
	), s.handleTriggerProjectIndex)

	// 1.5 set_project_root
	s.mcpServer.AddTool(mcp.NewTool("set_project_root",
		mcp.WithDescription("Dynamically switch the active project root and update the file watcher. Use this when moving between different codebases or monorepo packages."),
		mcp.WithString("project_path", mcp.Description("Absolute path to the new project root")),
	), s.handleSetProjectRoot)

	// 2. store_context
	s.mcpServer.AddTool(mcp.NewTool("store_context",
		mcp.WithDescription("Store general project rules, architectural decisions, or shared context for other agents to read. This helps maintain consistency across different AI sessions."),
		mcp.WithString("text", mcp.Description("The text context to store.")),
		mcp.WithString("project_id", mcp.Description("The project this context belongs to. Defaults to the current project.")),
	), s.handleStoreContext)

	// 3. get_related_context
	s.mcpServer.AddTool(mcp.NewTool("get_related_context",
		mcp.WithDescription("Retrieve context for a file and its local dependencies, optionally cross-referencing other projects. Use this to understand how a specific file fits into the larger codebase."),
		mcp.WithString("filePath", mcp.Description("The relative path of the file to analyze")),
	), s.handleGetRelatedContext)

	// 3. find_duplicate_code
	s.mcpServer.AddTool(mcp.NewTool("find_duplicate_code",
		mcp.WithDescription("Scans a specific path to find duplicated logic across namespaces. Use this during refactoring to identify consolidation opportunities."),
		mcp.WithString("target_path", mcp.Description("The relative path to check")),
	), s.handleFindDuplicateCode)

	// 4. delete_context
	s.mcpServer.AddTool(mcp.NewTool("delete_context",
		mcp.WithDescription("Delete specific shared memory context, or completely wipe a project's vector index."),
		mcp.WithString("target_path", mcp.Description("The exact file path, context ID, or 'ALL' to clear the whole project.")),
		mcp.WithString("project_id", mcp.Description("The project ID to target. Defaults to the current project.")),
	), s.handleDeleteContext)

	// 5. index_status
	s.mcpServer.AddTool(mcp.NewTool("index_status", mcp.WithDescription("Check index status and background progress. Use this to verify if the server is still indexing or if it's ready for queries.")),
		s.handleIndexStatus)

	// 5.5 retrieve_context
	s.mcpServer.AddTool(mcp.NewTool("retrieve_context",
		mcp.WithDescription("Semantic search across the indexed codebase using natural language. Returns the most relevant code chunks."),
		mcp.WithString("query", mcp.Description("The natural language search query")),
		mcp.WithNumber("topK", mcp.Description("Number of results to return (default 5)")),
		// Simple description for the array parameter as the SDK might have different WithArray signature
		mcp.WithArray("cross_reference_projects", mcp.Description("Optional list of project IDs to search across")),
	), s.handleRetrieveContext)

	// 6. get_codebase_skeleton
	s.mcpServer.AddTool(mcp.NewTool("get_codebase_skeleton",
		mcp.WithDescription("Returns a topological tree map of the directory structure. Use this to progressively explore large codebases by specifying sub-directories and depths."),
		mcp.WithString("target_path", mcp.Description("Relative or absolute path to the directory to map (optional, defaults to project root).")),
		mcp.WithNumber("max_depth", mcp.Description("Maximum depth of the tree to generate (optional, defaults to 3).")),
	), s.handleGetCodebaseSkeleton)
}

func (s *Server) handleTriggerProjectIndex(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path := request.GetString("project_path", "")
	if path == "" {
		return mcp.NewToolResultError("project_path is required"), nil
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid path: %v", err)), nil
	}
	s.indexQueue <- absPath
	return mcp.NewToolResultText(fmt.Sprintf("Initial indexing triggered in the background for %s.", absPath)), nil
}

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
	store, err := s.storeGetter(ctx)
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

func (s *Server) handleGetRelatedContext(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	filePath := request.GetString("filePath", "")
	store, err := s.storeGetter(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	pids := []string{s.cfg.ProjectRoot}
	crossProjs := request.GetStringSlice("cross_reference_projects", nil)
	if len(crossProjs) > 0 {
		pids = append(pids, crossProjs...)
	}
	records, err := store.GetByPath(ctx, filePath, s.cfg.ProjectRoot)
	if err != nil || len(records) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No context found for file: %s", filePath)), nil
	}
	uniqueDeps := make(map[string]string)
	allImportStrings := make(map[string]bool)
	allSymbols := make(map[string]bool)
	for _, r := range records {
		var deps []string
		if relStr := r.Metadata["relationships"]; relStr != "" {
			if err := json.Unmarshal([]byte(relStr), &deps); err == nil {
				for _, d := range deps {
					allImportStrings[d] = true
					if strings.HasPrefix(d, "./") || strings.HasPrefix(d, "../") {
						uniqueDeps[d] = filepath.Join(filepath.Dir(filePath), d)
					} else if physPath, ok := s.monorepoResolver.Resolve(d); ok {
						uniqueDeps[d] = physPath
					}
				}
			}
		}
		var symbols []string
		if symStr := r.Metadata["symbols"]; symStr != "" {
			if err := json.Unmarshal([]byte(symStr), &symbols); err == nil {
				for _, s := range symbols {
					allSymbols[s] = true
				}
			}
		}
	}
	var out strings.Builder
	out.WriteString("<context>\n")
	currentTokenCount := 0
	var omittedFiles []string
	out.WriteString(fmt.Sprintf("  <file path=\"%s\">\n    <metadata>\n", filePath))
	var depList []string
	for d := range allImportStrings { depList = append(depList, d) }
	depListJSON, _ := json.Marshal(depList)
	out.WriteString(fmt.Sprintf("      <dependencies>%s</dependencies>\n", string(depListJSON)))
	var symList []string
	for s := range allSymbols { symList = append(symList, s) }
	symListJSON, _ := json.Marshal(symList)
	out.WriteString(fmt.Sprintf("      <symbols>%s</symbols>\n", string(symListJSON)))
	out.WriteString("    </metadata>\n")
	for _, r := range records {
		tokens := indexer.EstimateTokens(r.Content)
		if currentTokenCount+tokens > indexer.MaxContextTokens {
			omittedFiles = append(omittedFiles, filePath)
			continue
		}
		out.WriteString("    <code_chunk>\n" + r.Content + "\n    </code_chunk>\n")
		currentTokenCount += tokens
	}
	out.WriteString("  </file>\n")
	if len(uniqueDeps) > 0 {
		allRecords, _ := store.GetAllRecords(ctx)
		for importPath, physPath := range uniqueDeps {
			matchPath := strings.TrimSuffix(physPath, filepath.Ext(physPath))
			out.WriteString(fmt.Sprintf("  <file path=\"%s\" resolved_from=\"%s\">\n", physPath, importPath))
			foundAny := false
			fileDeps := make(map[string]bool)
			fileSymbols := make(map[string]bool)
			var fileChunks []db.Record
			for _, dr := range allRecords {
				projMatch := false
				for _, pid := range pids {
					if dr.Metadata["project_id"] == pid {
						projMatch = true
						break
					}
				}
				if projMatch && (dr.Metadata["path"] == physPath || strings.Contains(dr.Metadata["path"], matchPath)) {
					fileChunks = append(fileChunks, dr)
					var dps []string
					if err := json.Unmarshal([]byte(dr.Metadata["relationships"]), &dps); err == nil {
						for _, d := range dps { fileDeps[d] = true }
					}
					var sys []string
					if err := json.Unmarshal([]byte(dr.Metadata["symbols"]), &sys); err == nil {
						for _, s := range sys { fileSymbols[s] = true }
					}
				}
			}
			if len(fileChunks) > 0 {
				out.WriteString("    <metadata>\n")
				var fdList []string
				for d := range fileDeps { fdList = append(fdList, d) }
				fdJSON, _ := json.Marshal(fdList)
				out.WriteString(fmt.Sprintf("      <dependencies>%s</dependencies>\n", string(fdJSON)))
				var fsList []string
				for s := range fileSymbols { fsList = append(fsList, s) }
				fsJSON, _ := json.Marshal(fsList)
				out.WriteString(fmt.Sprintf("      <symbols>%s</symbols>\n", string(fsJSON)))
				out.WriteString("    </metadata>\n")
				for _, dr := range fileChunks {
					tokens := indexer.EstimateTokens(dr.Content)
					if currentTokenCount+tokens > indexer.MaxContextTokens {
						omittedFiles = append(omittedFiles, dr.Metadata["path"])
						continue
					}
					out.WriteString("    <code_chunk>\n" + dr.Content + "\n    </code_chunk>\n")
					currentTokenCount += tokens
					foundAny = true
				}
			}
			if !foundAny && currentTokenCount < indexer.MaxContextTokens {
				out.WriteString("    <error>No indexed chunks found.</error>\n")
			}
			out.WriteString("  </file>\n")
		}
	}
	if len(omittedFiles) > 0 {
		out.WriteString("  <omitted_matches>\n    <files>")
		omittedJSON, _ := json.Marshal(omittedFiles)
		out.WriteString(string(omittedJSON) + "</files>\n  </omitted_matches>\n")
	}
	out.WriteString("</context>")
	return mcp.NewToolResultText(out.String()), nil
}

func (s *Server) handleFindDuplicateCode(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	targetPath := request.GetString("target_path", "")
	pids := []string{s.cfg.ProjectRoot}
	crossProjs := request.GetStringSlice("cross_reference_projects", nil)
	if len(crossProjs) > 0 {
		pids = append(pids, crossProjs...)
	}
	store, _ := s.storeGetter(ctx)
	allRecords, _ := store.GetAllRecords(ctx)
	var targetChunks []db.Record
	for _, r := range allRecords {
		if r.Metadata["project_id"] == s.cfg.ProjectRoot && strings.HasPrefix(r.Metadata["path"], targetPath) {
			targetChunks = append(targetChunks, r)
		}
	}
	var out strings.Builder
	out.WriteString(fmt.Sprintf("<duplicate_analysis target=\"%s\">\n", targetPath))
	found := false
	for _, tc := range targetChunks {
		emb, _ := s.embedder.Embed(ctx, tc.Content)
		matches, _, _ := store.SearchWithScore(ctx, emb, 5, pids)
		for _, m := range matches {
			if m.Similarity > 0.93 && (m.Metadata["path"] != tc.Metadata["path"] || m.Metadata["project_id"] != tc.Metadata["project_id"]) {
				out.WriteString(fmt.Sprintf("  <finding>\n    <original file=\"%s\">%s</original>\n", tc.Metadata["path"], tc.Metadata["path"]))
				out.WriteString(fmt.Sprintf("    <match file=\"%s\" project=\"%s\" score=\"%f\">%s</match>\n  </finding>\n", m.Metadata["path"], m.Metadata["project_id"], m.Similarity, m.Metadata["path"]))
				found = true
			}
		}
	}
	if !found {
		out.WriteString("  <summary>No duplicates found.</summary>\n")
	}
	out.WriteString("</duplicate_analysis>")
	return mcp.NewToolResultText(out.String()), nil
}

func (s *Server) handleDeleteContext(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	targetPath := request.GetString("target_path", "")
	if targetPath == "" {
		return mcp.NewToolResultError("target_path is required"), nil
	}
	projectID := request.GetString("project_id", s.cfg.ProjectRoot)
	store, err := s.storeGetter(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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

func (s *Server) handleIndexStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	store, _ := s.freshStoreGetter(ctx)
	res, _ := s.runStatus(ctx, store)

	bgStatus := "\n🚀 Local/In-Memory Tasks (This Instance):\n"
	localTasks := make(map[string]bool)
	hasLocal := false
	s.progressMap.Range(func(key, value interface{}) bool {
		bgStatus += fmt.Sprintf("- %s: %s\n", key, value)
		localTasks[key.(string)] = true
		hasLocal = true
		return true
	})
	if !hasLocal {
		bgStatus += "- No active background indexing on this instance.\n"
	}

	globalStatus := "\n🛰️ Global/DB Tasks (All Instances):\n"
	allStatuses, _ := store.GetAllStatuses(ctx)
	hasGlobal := false
	for pid, status := range allStatuses {
		// If it's not in our local tasks but seems active in DB, report it
		isIndexing := strings.Contains(status, "Indexing") || strings.Contains(status, "Initializing")
		if !localTasks[pid] && isIndexing {
			globalStatus += fmt.Sprintf("- %s: %s (Remote Process)\n", pid, status)
			hasGlobal = true
		}
	}
	if !hasGlobal {
		globalStatus += "- No active background indexing from other instances.\n"
	}

	return mcp.NewToolResultText(res + bgStatus + globalStatus), nil
}

func (s *Server) handleRetrieveContext(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := request.GetString("query", "")
	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}
	topK := int(request.GetFloat("topK", 5))


	pids := request.GetStringSlice("cross_reference_projects", nil)
	if len(pids) == 0 {
		pids = []string{s.cfg.ProjectRoot}
	}

	emb, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to embed query: %v", err)), nil
	}

	store, err := s.storeGetter(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get store: %v", err)), nil
	}

	results, err := store.Search(ctx, emb, topK, pids)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to search database: %v", err)), nil
	}

	if len(results) == 0 {

		return mcp.NewToolResultText("No relevant context found."), nil
	}

	var out strings.Builder
	out.WriteString("### Semantic Search Results:\n\n")
	currentTokenCount := 0

	for i, r := range results {
		tokens := indexer.EstimateTokens(r.Content)
		if currentTokenCount+tokens > indexer.MaxContextTokens {
			out.WriteString("... (truncating further results to stay within context window)")
			break
		}
		out.WriteString(fmt.Sprintf("#### Result %d: %s\n```\n%s\n```\n\n", i+1, r.Metadata["path"], r.Content))
		currentTokenCount += tokens
	}
	return mcp.NewToolResultText(out.String()), nil
}

func (s *Server) handleGetCodebaseSkeleton(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	targetPath := request.GetString("target_path", s.cfg.ProjectRoot)
	if targetPath == "" {
		targetPath = s.cfg.ProjectRoot
	}
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(s.cfg.ProjectRoot, targetPath)
	}
	maxDepth := request.GetInt("max_depth", 3)
	info, err := os.Stat(targetPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid target_path: %v", err)), nil
	}
	if !info.IsDir() {
		targetPath = filepath.Dir(targetPath)
	}
	var out strings.Builder
	out.WriteString(fmt.Sprintf("Directory Tree: %s (Depth Limit: %d)\n", targetPath, maxDepth))
	itemCount := 0
	maxItems := 1500
	truncated := false
	err = filepath.WalkDir(targetPath, func(path string, d os.DirEntry, err error) error {
		if err != nil { return nil }
		if path == targetPath { return nil }
		relPath, err := filepath.Rel(targetPath, path)
		if err != nil { return nil }
		depth := strings.Count(relPath, string(os.PathSeparator)) + 1
		if d.IsDir() {
			if indexer.IsIgnoredDir(d.Name()) { return filepath.SkipDir }
			if depth > maxDepth { return filepath.SkipDir }
		} else {
			if indexer.IsIgnoredFile(d.Name()) { return nil }
			if depth > maxDepth { return nil }
		}
		if itemCount >= maxItems {
			truncated = true
			return filepath.SkipDir
		}
		itemCount++
		indent := strings.Repeat("│   ", depth-1)
		out.WriteString(fmt.Sprintf("%s├── %s\n", indent, d.Name()))
		return nil
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error walking directory: %v", err)), nil
	}
	if truncated {
		out.WriteString(fmt.Sprintf("... (tree truncated, reached %d item limit. Please target a deeper sub-directory)\n", maxItems))
	}
	return mcp.NewToolResultText(fmt.Sprintf("<codebase_skeleton>\n%s</codebase_skeleton>", out.String())), nil
}

func (s *Server) runStatus(ctx context.Context, store *db.Store) (string, error) {
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
