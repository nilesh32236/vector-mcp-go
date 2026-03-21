package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/daemon"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
)

// IndexerStore defines the interface for database operations,
// allowing both local and remote implementations.
type IndexerStore interface {
	Search(ctx context.Context, embedding []float32, topK int, pids []string, category string) ([]db.Record, error)
	HybridSearch(ctx context.Context, query string, embedding []float32, topK int, pids []string, category string) ([]db.Record, error)
	Insert(ctx context.Context, records []db.Record) error
	DeleteByPrefix(ctx context.Context, prefix string, projectID string) error
	ClearProject(ctx context.Context, projectID string) error
	GetStatus(ctx context.Context, projectID string) (string, error)
	GetAllStatuses(ctx context.Context) (map[string]string, error)
	GetPathHashMapping(ctx context.Context, projectID string) (map[string]string, error)
	GetAllRecords(ctx context.Context) ([]db.Record, error)
	GetByPath(ctx context.Context, path string, projectID string) ([]db.Record, error)
	GetByPrefix(ctx context.Context, prefix string, projectID string) ([]db.Record, error)
	LexicalSearch(ctx context.Context, query string, topK int, projectIDs []string, category string) ([]db.Record, error)
	Count() int64
}

// Server is the core MCP server that manages tools and handles requests.
type Server struct {
	cfg              *config.Config
	logger           *slog.Logger
	MCPServer        *server.MCPServer
	storeGetter      func(ctx context.Context) (*db.Store, error)
	remoteStore      IndexerStore
	embedder         indexer.Embedder
	indexQueue       chan string
	daemonClient     *daemon.Client
	progressMap      *sync.Map
	watcherResetChan chan string
	monorepoResolver *indexer.WorkspaceResolver
	toolHandlers     map[string]func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

// NewServer creates a new Server instance and registers its tools.
func NewServer(cfg *config.Config, logger *slog.Logger, storeGetter func(ctx context.Context) (*db.Store, error), _ func(ctx context.Context) (*db.Store, error), embedder indexer.Embedder, queue chan string, daemonClient *daemon.Client, progress *sync.Map, resetChan chan string, resolver *indexer.WorkspaceResolver) *Server {
	s := server.NewMCPServer("vector-mcp-go", "1.0.0", server.WithLogging())
	srv := &Server{
		cfg:              cfg,
		logger:           logger,
		MCPServer:        s,
		storeGetter:      storeGetter,
		embedder:         embedder,
		indexQueue:       queue,
		daemonClient:     daemonClient,
		progressMap:      progress,
		watcherResetChan: resetChan,
		monorepoResolver: resolver,
		toolHandlers:     make(map[string]func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)),
	}
	srv.registerTools()
	return srv
}

func (s *Server) WithRemoteStore(rs IndexerStore) {
	s.remoteStore = rs
}

func (s *Server) getStore(ctx context.Context) (IndexerStore, error) {
	if s.remoteStore != nil {
		return s.remoteStore, nil
	}
	return s.storeGetter(ctx)
}

// Serve starts the MCP server on stdio.
func (s *Server) Serve() error {
	s.logger.Info("MCP Server listening on stdio...")
	return server.ServeStdio(s.MCPServer)
}

func (s *Server) registerTools() {
	// Helper to add tool and track handler
	addTool := func(tool mcp.Tool, handler func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)) {
		s.MCPServer.AddTool(tool, handler)
		s.toolHandlers[tool.Name] = handler
	}

	// 0. ping
	addTool(mcp.NewTool("ping", mcp.WithDescription("Check server connectivity")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("pong"), nil
		})

	// 1. trigger_project_index
	addTool(mcp.NewTool("trigger_project_index",
		mcp.WithDescription("Trigger a full background index of a project. Use this when you first open a project or after major changes to ensure the vector index is up to date."),
		mcp.WithString("project_path", mcp.Description("Absolute path to the project root")),
	), s.handleTriggerProjectIndex)

	// 1.5 set_project_root
	addTool(mcp.NewTool("set_project_root",
		mcp.WithDescription("Dynamically switch the active project root and update the file watcher. Use this when moving between different codebases or monorepo packages."),
		mcp.WithString("project_path", mcp.Description("Absolute path to the new project root")),
	), s.handleSetProjectRoot)

	// 2. store_context
	addTool(mcp.NewTool("store_context",
		mcp.WithDescription("Store general project rules, architectural decisions, or shared context for other agents to read. This helps maintain consistency across different AI sessions."),
		mcp.WithString("text", mcp.Description("The text context to store.")),
		mcp.WithString("project_id", mcp.Description("The project this context belongs to. Defaults to the current project.")),
	), s.handleStoreContext)

	// 3. get_related_context
	addTool(mcp.NewTool("get_related_context",
		mcp.WithDescription("Retrieve context for a file and its local dependencies, optionally cross-referencing other projects. Use this to understand how a specific file fits into the larger codebase."),
		mcp.WithString("filePath", mcp.Description("The relative path of the file to analyze")),
		mcp.WithNumber("max_tokens", mcp.Description("Optional: Maximum total tokens to include in the context (default 10,000)")),
	), s.handleGetRelatedContext)

	// 3. find_duplicate_code
	addTool(mcp.NewTool("find_duplicate_code",
		mcp.WithDescription("Scans a specific path to find duplicated logic across namespaces. Use this during refactoring to identify consolidation opportunities."),
		mcp.WithString("target_path", mcp.Description("The relative path to check")),
	), s.handleFindDuplicateCode)

	// 4. delete_context
	addTool(mcp.NewTool("delete_context",
		mcp.WithDescription("Delete specific shared memory context, or completely wipe a project's vector index."),
		mcp.WithString("target_path", mcp.Description("The exact file path, context ID, or 'ALL' to clear the whole project.")),
		mcp.WithString("project_id", mcp.Description("The project ID to target. Defaults to the current project.")),
		mcp.WithBoolean("dry_run", mcp.Description("Optional: If true, returns the list of files that would be deleted without actually deleting them.")),
	), s.handleDeleteContext)

	// 5. index_status
	addTool(mcp.NewTool("index_status", mcp.WithDescription("Check index status and background progress. Use this to verify if the server is still indexing or if it's ready for queries.")),
		s.handleIndexStatus)

	// 5.5 retrieve_context
	addTool(mcp.NewTool("retrieve_context",
		mcp.WithDescription("Semantic search across the indexed codebase using natural language. Returns the most relevant code chunks."),
		mcp.WithString("query", mcp.Description("The natural language search query")),
		mcp.WithNumber("topK", mcp.Description("Number of results to return (default 5)")),
		mcp.WithArray("cross_reference_projects",
			mcp.Description("Optional list of project IDs to search across"),
			mcp.WithStringItems(),
		),
	), s.handleRetrieveContext)

	// 5.6 retrieve_docs
	addTool(mcp.NewTool("retrieve_docs",
		mcp.WithDescription("Semantic search across the indexed documentation (PDFs, MDs, TXTs) using natural language."),
		mcp.WithString("query", mcp.Description("The natural language search query")),
		mcp.WithNumber("topK", mcp.Description("Number of results to return (default 5)")),
		mcp.WithArray("cross_reference_projects",
			mcp.Description("Optional list of project IDs to search across"),
			mcp.WithStringItems(),
		),
	), s.handleRetrieveDocs)

	// 6. get_codebase_skeleton
	addTool(mcp.NewTool("get_codebase_skeleton",
		mcp.WithDescription("Returns a topological tree map of the directory structure. Use this to progressively explore large codebases by specifying sub-directories and depths."),
		mcp.WithString("target_path", mcp.Description("Relative or absolute path to the directory to map (optional, defaults to project root).")),
		mcp.WithNumber("max_depth", mcp.Description("Maximum depth of the tree to generate (optional, defaults to 3).")),
		mcp.WithString("include_pattern", mcp.Description("Optional: Only include files matching this glob pattern")),
		mcp.WithString("exclude_pattern", mcp.Description("Optional: Exclude files matching this glob pattern")),
		mcp.WithNumber("max_items", mcp.Description("Optional: Maximum number of items to return (default 1000)")),
	), s.handleGetCodebaseSkeleton)

	// 7. check_dependency_health
	addTool(mcp.NewTool("check_dependency_health",
		mcp.WithDescription("Analyzes a directory's package.json against its indexed imports to identify missing dependencies in the manifest."),
		mcp.WithString("directory_path", mcp.Description("The path to the directory containing package.json and source files")),
	), s.handleCheckDependencyHealth)

	// 8. generate_jsdoc_prompt
	addTool(mcp.NewTool("generate_jsdoc_prompt",
		mcp.WithDescription("Generates a highly contextual prompt for an LLM to write professional JSDoc for a specific entity."),
		mcp.WithString("file_path", mcp.Description("The relative path of the file")),
		mcp.WithString("entity_name", mcp.Description("The name of the function or class to document")),
	), s.handleGenerateJSDocPrompt)

	// 9. analyze_architecture
	addTool(mcp.NewTool("analyze_architecture",
		mcp.WithDescription("Generates a Mermaid.js dependency graph between packages in a monorepo."),
		mcp.WithString("monorepo_prefix", mcp.Description("Optional prefix for monorepo packages (e.g., '@herexa/')")),
	), s.handleAnalyzeArchitecture)

	// 10. find_dead_code
	addTool(mcp.NewTool("find_dead_code",
		mcp.WithDescription("Identifies potentially dead code by finding exported symbols that are never imported or called."),
	), s.handleFindDeadCode)

	// 11. filesystem_grep
	addTool(mcp.NewTool("filesystem_grep",
		mcp.WithDescription("Exact string or regex search across the project files."),
		mcp.WithString("query", mcp.Description("The search query (string or regex)")),
		mcp.WithString("include_pattern", mcp.Description("Optional: Glob pattern to filter files (e.g. '*.go')")),
		mcp.WithBoolean("is_regex", mcp.Description("Whether the query is a regular expression")),
	), s.handleFilesystemGrep)

	// 12. search_codebase
	addTool(mcp.NewTool("search_codebase",
		mcp.WithDescription("Unified semantic and lexical search across the codebase with advanced filtering."),
		mcp.WithString("query", mcp.Description("The natural language search query")),
		mcp.WithString("category", mcp.Description("Optional: 'code' or 'document'. Defaults to searching both.")),
		mcp.WithNumber("topK", mcp.Description("Number of results to return (default 10)")),
		mcp.WithString("path_filter", mcp.Description("Optional: Only search files whose path contains this string")),
		mcp.WithNumber("min_score", mcp.Description("Optional: Minimum similarity score (0.0 to 1.0) to include a result")),
		mcp.WithNumber("max_tokens", mcp.Description("Optional: Maximum total tokens to include in the context (default 10,000)")),
		mcp.WithArray("cross_reference_projects",
			mcp.Description("Optional list of project IDs to search across"),
			mcp.WithStringItems(),
		),
	), s.handleSearchCodebase)

	// 13. get_indexing_diagnostics
	addTool(mcp.NewTool("get_indexing_diagnostics",
		mcp.WithDescription("Provides detailed diagnostics on the indexing process, including recent errors and queue status."),
	), s.handleGetIndexingDiagnostics)
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

	if s.daemonClient != nil {
		err := s.daemonClient.TriggerIndex(absPath)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to delegate indexing to master daemon: %v", err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Indexing task successfully delegated to the master daemon for %s.", absPath)), nil
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

func (s *Server) handleGetRelatedContext(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	filePath := request.GetString("filePath", "")
	maxTokens := int(request.GetFloat("max_tokens", float64(indexer.MaxContextTokens)))
	if maxTokens <= 0 {
		maxTokens = indexer.MaxContextTokens
	}

	store, err := s.getStore(ctx)
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
	for d := range allImportStrings {
		depList = append(depList, d)
	}
	depListJSON, _ := json.Marshal(depList)
	out.WriteString(fmt.Sprintf("      <dependencies>%s</dependencies>\n", string(depListJSON)))
	var symList []string
	for s := range allSymbols {
		symList = append(symList, s)
	}
	symListJSON, _ := json.Marshal(symList)
	out.WriteString(fmt.Sprintf("      <symbols>%s</symbols>\n", string(symListJSON)))
	out.WriteString("    </metadata>\n")
	for _, r := range records {
		tokens := indexer.EstimateTokens(r.Content)
		if currentTokenCount+tokens > maxTokens {
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
						for _, d := range dps {
							fileDeps[d] = true
						}
					}
					var sys []string
					if err := json.Unmarshal([]byte(dr.Metadata["symbols"]), &sys); err == nil {
						for _, s := range sys {
							fileSymbols[s] = true
						}
					}
				}
			}
			if len(fileChunks) > 0 {
				out.WriteString("    <metadata>\n")
				var fdList []string
				for d := range fileDeps {
					fdList = append(fdList, d)
				}
				fdJSON, _ := json.Marshal(fdList)
				out.WriteString(fmt.Sprintf("      <dependencies>%s</dependencies>\n", string(fdJSON)))
				var fsList []string
				for s := range fileSymbols {
					fsList = append(fsList, s)
				}
				fsJSON, _ := json.Marshal(fsList)
				out.WriteString(fmt.Sprintf("      <symbols>%s</symbols>\n", string(fsJSON)))
				out.WriteString("    </metadata>\n")
				for _, dr := range fileChunks {
					tokens := indexer.EstimateTokens(dr.Content)
					if currentTokenCount+tokens > maxTokens {
						omittedFiles = append(omittedFiles, dr.Metadata["path"])
						continue
					}
					out.WriteString("    <code_chunk>\n" + dr.Content + "\n    </code_chunk>\n")
					currentTokenCount += tokens
					foundAny = true
				}
			}
			if !foundAny && currentTokenCount < maxTokens {
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

	// Usage Samples: Optimized cross-file symbol search
	if len(allSymbols) > 0 {
		out.WriteString("  <usage_samples>\n")
		foundUsage := false
		for s := range allSymbols {
			// Find usage of symbol 's' across projects
			usages, err := store.LexicalSearch(ctx, s, 5, pids, "")
			if err != nil {
				continue
			}

			for _, dr := range usages {
				if dr.Metadata["path"] == filePath {
					continue
				}
				
				tokens := indexer.EstimateTokens(dr.Content)
				if currentTokenCount+tokens > maxTokens {
					continue
				}

				out.WriteString(fmt.Sprintf("    <sample symbol=\"%s\" used_in=\"%s\">\n", s, dr.Metadata["path"]))
				out.WriteString(dr.Content + "\n")
				out.WriteString("    </sample>\n")
				currentTokenCount += tokens
				foundUsage = true
			}
		}
		if !foundUsage {
			out.WriteString("    <info>No external usage samples found.</info>\n")
		}
		out.WriteString("  </usage_samples>\n")
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
	store, _ := s.getStore(ctx)
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

		var matches []db.Record
		if ds, ok := store.(*db.Store); ok {
			ms, _, _ := ds.SearchWithScore(ctx, emb, 5, pids, "")
			matches = ms
		} else {
			ms, _ := store.Search(ctx, emb, 5, pids, "")
			matches = ms
		}

		for _, m := range matches {
			if m.Metadata["path"] != tc.Metadata["path"] || m.Metadata["project_id"] != tc.Metadata["project_id"] {
				out.WriteString(fmt.Sprintf("  <finding>\n    <original file=\"%s\">%s</original>\n", tc.Metadata["path"], tc.Metadata["path"]))
				out.WriteString(fmt.Sprintf("    <match file=\"%s\" project=\"%s\">%s</match>\n  </finding>\n", m.Metadata["path"], m.Metadata["project_id"], m.Metadata["path"]))
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

	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get store: %v", err)), nil
	}

	results, err := store.HybridSearch(ctx, query, emb, topK, pids, "code")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to search database: %v", err)), nil
	}

	if len(results) == 0 {
		return mcp.NewToolResultText("No relevant context found."), nil
	}

	var out strings.Builder
	out.WriteString("### Hybrid Search Results (Lexical + Semantic):\n\n")
	currentTokenCount := 0

	for i, r := range results {
		tokens := indexer.EstimateTokens(r.Content)
		if currentTokenCount+tokens > indexer.MaxContextTokens {
			out.WriteString("... (truncating further results to stay within context window)")
			break
		}

		lineRange := ""
		if start, ok := r.Metadata["start_line"]; ok {
			if end, ok := r.Metadata["end_line"]; ok {
				lineRange = fmt.Sprintf(" (Lines %s-%s)", start, end)
			}
		}

		out.WriteString(fmt.Sprintf("#### Result %d: %s%s\n", i+1, r.Metadata["path"], lineRange))
		if syms := r.Metadata["symbols"]; syms != "" {
			out.WriteString(fmt.Sprintf("- **Entities**: %s\n", syms))
		}
		if rels := r.Metadata["relationships"]; rels != "" {
			out.WriteString(fmt.Sprintf("- **Relationships**: %s\n", rels))
		}
		out.WriteString(fmt.Sprintf("```\n%s\n```\n\n", r.Content))
		currentTokenCount += tokens
	}
	return mcp.NewToolResultText(out.String()), nil
}

func (s *Server) handleRetrieveDocs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get store: %v", err)), nil
	}

	results, err := store.HybridSearch(ctx, query, emb, topK, pids, "document")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to search database: %v", err)), nil
	}

	if len(results) == 0 {
		return mcp.NewToolResultText("No relevant context found."), nil
	}

	var out strings.Builder
	out.WriteString("### Document Search Results:\n\n")
	currentTokenCount := 0

	for i, r := range results {
		tokens := indexer.EstimateTokens(r.Content)
		if currentTokenCount+tokens > indexer.MaxContextTokens {
			out.WriteString("... (truncating further results to stay within context window)")
			break
		}

		lineRange := ""
		if start, ok := r.Metadata["start_line"]; ok {
			if end, ok := r.Metadata["end_line"]; ok {
				lineRange = fmt.Sprintf(" (Lines %s-%s)", start, end)
			}
		}

		out.WriteString(fmt.Sprintf("#### Result %d: %s%s\n", i+1, r.Metadata["path"], lineRange))
		out.WriteString(fmt.Sprintf("```\n%s\n```\n\n", r.Content))
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
	maxDepth := int(request.GetFloat("max_depth", 3))
	includePattern := request.GetString("include_pattern", "")
	excludePattern := request.GetString("exclude_pattern", "")
	maxItems := int(request.GetFloat("max_items", 1000))

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
	truncated := false
	err = filepath.WalkDir(targetPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == targetPath {
			return nil
		}
		relPath, err := filepath.Rel(targetPath, path)
		if err != nil {
			return nil
		}
		depth := strings.Count(relPath, string(os.PathSeparator)) + 1
		if d.IsDir() {
			if indexer.IsIgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			if depth > maxDepth {
				return filepath.SkipDir
			}
		} else {
			if indexer.IsIgnoredFile(d.Name()) {
				return nil
			}
			if depth > maxDepth {
				return nil
			}

			// Pattern filtering
			if includePattern != "" {
				matched, _ := filepath.Match(includePattern, d.Name())
				if !matched {
					return nil
				}
			}
			if excludePattern != "" {
				matched, _ := filepath.Match(excludePattern, d.Name())
				if matched {
					return nil
				}
			}
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
		out.WriteString(fmt.Sprintf("... (tree truncated, reached %d item limit)\n", maxItems))
	}
	return mcp.NewToolResultText(fmt.Sprintf("<codebase_skeleton>\n%s</codebase_skeleton>", out.String())), nil
}

func (s *Server) handleCheckDependencyHealth(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	dirPath := request.GetString("directory_path", "")
	if dirPath == "" {
		return mcp.NewToolResultError("directory_path is required"), nil
	}

	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid path: %v", err)), nil
	}

	depSet := make(map[string]bool)
	projectType := "unknown"

	// 1. Detect project type and load dependencies
	if _, err := os.Stat(filepath.Join(absPath, "package.json")); err == nil {
		projectType = "npm"
		pkgData, _ := os.ReadFile(filepath.Join(absPath, "package.json"))
		var pkg struct {
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if err := json.Unmarshal(pkgData, &pkg); err == nil {
			for d := range pkg.Dependencies {
				depSet[d] = true
			}
			for d := range pkg.DevDependencies {
				depSet[d] = true
			}
		}
	} else if _, err := os.Stat(filepath.Join(absPath, "go.mod")); err == nil {
		projectType = "go"
		modData, _ := os.ReadFile(filepath.Join(absPath, "go.mod"))
		lines := strings.Split(string(modData), "\n")
		// Very simple go.mod parser
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "require ") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					depSet[parts[1]] = true
				}
			}
		}
	} else if _, err := os.Stat(filepath.Join(absPath, "requirements.txt")); err == nil {
		projectType = "python"
		reqData, _ := os.ReadFile(filepath.Join(absPath, "requirements.txt"))
		lines := strings.Split(string(reqData), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				// extract pkg name before == or >=
				pkgName := regexp.MustCompile(`^([a-zA-Z0-9_\-]+)`).FindString(line)
				if pkgName != "" {
					depSet[pkgName] = true
				}
			}
		}
	}

	if projectType == "unknown" {
		return mcp.NewToolResultError("Could not identify project type (no package.json, go.mod, or requirements.txt found)"), nil
	}

	// 2. Fetch Chunks
	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	relDirPath := config.GetRelativePath(absPath, s.cfg.ProjectRoot)
	records, err := store.GetByPrefix(ctx, relDirPath, s.cfg.ProjectRoot)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to fetch records: %v", err)), nil
	}

	// 3. Analyze Relationships
	missingDeps := make(map[string][]string) // dep -> files

	for _, r := range records {
		var rels []string
		if relStr := r.Metadata["relationships"]; relStr != "" {
			if err := json.Unmarshal([]byte(relStr), &rels); err == nil {
				for _, dep := range rels {
					if projectType == "npm" {
						// Skip local imports and monorepo prefix
						if strings.HasPrefix(dep, ".") || strings.HasPrefix(dep, "/") || strings.HasPrefix(dep, "@herexa/") {
							continue
						}
						pkgName := dep
						parts := strings.Split(dep, "/")
						if strings.HasPrefix(dep, "@") && len(parts) > 1 {
							pkgName = parts[0] + "/" + parts[1]
						} else {
							pkgName = parts[0]
						}
						if !depSet[pkgName] {
							missingDeps[pkgName] = append(missingDeps[pkgName], r.Metadata["path"])
						}
					} else if projectType == "go" {
						// Standard library check (simplified: no dots usually)
						if !strings.Contains(dep, ".") || strings.HasPrefix(dep, s.cfg.ProjectRoot) {
							continue
						}
						if !depSet[dep] {
							missingDeps[dep] = append(missingDeps[dep], r.Metadata["path"])
						}
					} else if projectType == "python" {
						if strings.HasPrefix(dep, ".") {
							continue
						}
						if !depSet[dep] {
							missingDeps[dep] = append(missingDeps[dep], r.Metadata["path"])
						}
					}
				}
			}
		}
	}

	// 4. Output Report
	if len(missingDeps) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("✅ Dependency Health Check (%s): All external imports are correctly declared.", projectType)), nil
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("## ⚠️ Dependency Health Report (%s)\n\n", projectType))
	out.WriteString("The following external dependencies are imported but missing from your manifest:\n\n")

	var deps []string
	for d := range missingDeps {
		deps = append(deps, d)
	}
	sort.Strings(deps)

	for _, dep := range deps {
		files := missingDeps[dep]
		uniqueFiles := make(map[string]bool)
		for _, f := range files {
			uniqueFiles[f] = true
		}
		var sortedFiles []string
		for f := range uniqueFiles {
			sortedFiles = append(sortedFiles, f)
		}
		sort.Strings(sortedFiles)

		out.WriteString(fmt.Sprintf("### `%s`\n", dep))
		out.WriteString("Imported in:\n")
		for _, f := range sortedFiles {
			out.WriteString(fmt.Sprintf("- %s\n", f))
		}
		out.WriteString("\n")
	}

	return mcp.NewToolResultText(out.String()), nil
}

func (s *Server) handleGenerateJSDocPrompt(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filePath := request.GetString("file_path", "")
	entityName := request.GetString("entity_name", "")
	if filePath == "" || entityName == "" {
		return mcp.NewToolResultError("file_path and entity_name are required"), nil
	}

	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Use LexicalSearch to find the entity in the file
	records, err := store.GetByPath(ctx, filePath, s.cfg.ProjectRoot)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to fetch records for file: %v", err)), nil
	}

	var match *db.Record
	for _, r := range records {
		var syms []string
		if symStr := r.Metadata["symbols"]; symStr != "" {
			if err := json.Unmarshal([]byte(symStr), &syms); err == nil {
				for _, s := range syms {
					if s == entityName {
						match = &r
						break
					}
				}
			}
		}
		if match != nil {
			break
		}
	}

	if match == nil {
		return mcp.NewToolResultError(fmt.Sprintf("Entity '%s' not found in file '%s'", entityName, filePath)), nil
	}

	// Construct Prompt
	content := match.Content
	calls := match.Metadata["calls"]
	symbols := match.Metadata["symbols"]
	relationships := match.Metadata["relationships"]

	prompt := fmt.Sprintf(`Please write a professional JSDoc comment for the following code. 
Architecture Context:
- Entity: %s
- Internal Calls made: %s
- File Imports: %s

Code:
%s`, symbols, calls, relationships, content)

	return mcp.NewToolResultText(prompt), nil
}

func (s *Server) handleAnalyzeArchitecture(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	monorepoPrefix := request.GetString("monorepo_prefix", "@herexa/")
	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	records, err := store.GetAllRecords(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to fetch all records: %v", err)), nil
	}

	// adjacency list: source_package -> target_package -> exists
	adj := make(map[string]map[string]bool)

	for _, r := range records {
		path := r.Metadata["path"]
		if path == "" {
			continue
		}

		// Source package detection (e.g., apps/api/src/main.ts -> apps/api)
		parts := strings.Split(path, string(os.PathSeparator))
		if len(parts) < 2 {
			continue
		}
		srcPkg := parts[0]
		if len(parts) > 2 && (parts[0] == "apps" || parts[0] == "packages") {
			srcPkg = parts[0] + "/" + parts[1]
		}

		// Relationships
		var rels []string
		if relStr := r.Metadata["relationships"]; relStr != "" {
			if err := json.Unmarshal([]byte(relStr), &rels); err == nil {
				for _, rel := range rels {
					if strings.HasPrefix(rel, monorepoPrefix) {
						targetPkg := rel
						if adj[srcPkg] == nil {
							adj[srcPkg] = make(map[string]bool)
						}
						adj[srcPkg][targetPkg] = true
					}
				}
			}
		}
	}

	// Build Mermaid graph
	var sb strings.Builder
	sb.WriteString("graph TD\n")

	// Sort for deterministic output
	var sources []string
	for s := range adj {
		sources = append(sources, s)
	}
	sort.Strings(sources)

	for _, src := range sources {
		var targets []string
		for t := range adj[src] {
			targets = append(targets, t)
		}
		sort.Strings(targets)
		for _, target := range targets {
			sb.WriteString(fmt.Sprintf("    \"%s\" --> \"%s\"\n", src, target))
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func (s *Server) handleFindDeadCode(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	records, err := store.GetAllRecords(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to fetch all records: %v", err)), nil
	}

	type exportedSymbol struct {
		name string
		path string
	}
	setA := make(map[string]exportedSymbol) // name -> info
	setB := make(map[string]bool)           // name -> used

	for _, r := range records {
		// Set A: Exports (structural types only)
		t := r.Metadata["type"]
		if t == "function" || t == "class" || t == "variable" || t == "arrow_function" {
			var syms []string
			if err := json.Unmarshal([]byte(r.Metadata["symbols"]), &syms); err == nil {
				for _, sym := range syms {
					if sym != "" {
						setA[sym] = exportedSymbol{name: sym, path: r.Metadata["path"]}
					}
				}
			}
		}

		// Set B: Usage
		// 1. Calls
		var calls []string
		if err := json.Unmarshal([]byte(r.Metadata["calls"]), &calls); err == nil {
			for _, call := range calls {
				setB[call] = true
			}
		}
		// 2. Relationships (Imports)
		var rels []string
		if err := json.Unmarshal([]byte(r.Metadata["relationships"]), &rels); err == nil {
			for _, rel := range rels {
				setB[rel] = true
			}
		}
	}

	// Set Difference
	var dead []exportedSymbol
	for name, info := range setA {
		if !setB[name] {
			dead = append(dead, info)
		}
	}

	if len(dead) == 0 {
		return mcp.NewToolResultText("✅ Dead Code Check: No unused exported symbols found."), nil
	}

	// Sort for deterministic output
	sort.Slice(dead, func(i, j int) bool {
		if dead[i].path == dead[j].path {
			return dead[i].name < dead[j].name
		}
		return dead[i].path < dead[j].path
	})

	var out strings.Builder
	out.WriteString("## 🔎 Potential Dead Code Report\n\n")
	out.WriteString("The following exported symbols are not explicitly used (imported or called) in the indexed codebase:\n\n")
	for _, d := range dead {
		out.WriteString(fmt.Sprintf("- **`%s`** in `%s`\n", d.name, d.path))
	}

	return mcp.NewToolResultText(out.String()), nil
}

func (s *Server) handleFilesystemGrep(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := request.GetString("query", "")
	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}
	includePattern := request.GetString("include_pattern", "")

	isRegex := false
	if args, ok := request.Params.Arguments.(map[string]interface{}); ok {
		isRegex, _ = args["is_regex"].(bool)
	}

	var re *regexp.Regexp
	if isRegex {
		var err error
		re, err = regexp.Compile(query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Invalid regex: %v", err)), nil
		}
	}

	var results []string
	maxMatches := 100
	matchCount := 0

	err := filepath.WalkDir(s.cfg.ProjectRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if matchCount >= maxMatches {
			return filepath.SkipDir
		}

		relPath, _ := filepath.Rel(s.cfg.ProjectRoot, path)
		if indexer.IsIgnoredDir(filepath.Base(filepath.Dir(path))) || indexer.IsIgnoredFile(d.Name()) {
			return nil
		}

		if includePattern != "" {
			matched, _ := filepath.Match(includePattern, d.Name())
			if !matched {
				return nil
			}
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			matched := false
			if isRegex {
				matched = re.MatchString(line)
			} else {
				matched = strings.Contains(strings.ToLower(line), strings.ToLower(query))
			}

			if matched {
				results = append(results, fmt.Sprintf("%s:%d: %s", relPath, i+1, strings.TrimSpace(line)))
				matchCount++
				if matchCount >= maxMatches {
					break
				}
			}
		}

		return nil
	})

	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error during grep: %v", err)), nil
	}

	if len(results) == 0 {
		return mcp.NewToolResultText("No matches found."), nil
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("### Grep Results for '%s' (%d matches):\n\n", query, len(results)))
	for _, res := range results {
		out.WriteString(fmt.Sprintf("%s\n", res))
	}

	if matchCount >= maxMatches {
		out.WriteString("\n... (limit reached, more matches may exist)")
	}

	return mcp.NewToolResultText(out.String()), nil
}

func (s *Server) handleSearchCodebase(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := request.GetString("query", "")
	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}
	topK := int(request.GetFloat("topK", 10))
	category := request.GetString("category", "") // code, document, or empty
	pathFilter := request.GetString("path_filter", "")
	maxTokens := int(request.GetFloat("max_tokens", float64(indexer.MaxContextTokens)))
	if maxTokens <= 0 {
		maxTokens = indexer.MaxContextTokens
	}

	pids := request.GetStringSlice("cross_reference_projects", nil)
	if len(pids) == 0 {
		pids = []string{s.cfg.ProjectRoot}
	}

	emb, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to embed query: %v", err)), nil
	}

	store, err := s.getStore(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get store: %v", err)), nil
	}

	// For hybrid search, we fetch more to allow filtering
	results, err := store.HybridSearch(ctx, query, emb, topK*3, pids, category)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to search database: %v", err)), nil
	}

	var filtered []db.Record
	for _, r := range results {
		// 1. Path Filter
		if pathFilter != "" && !strings.Contains(r.Metadata["path"], pathFilter) {
			continue
		}

		// 2. Score Filter (Wait, HybridSearch score is RRF, not direct similarity)
		// But Record has Similarity if it came from SearchWithScore inside HybridSearch.
		// RRF scores are usually small. Let's skip minScore for RRF for now OR use direct vector search if minScore is set.
		// Actually, let's just use it as is, or if minScore > 0, we can prioritize vector results.

		filtered = append(filtered, r)
		if len(filtered) >= topK {
			break
		}
	}

	if len(filtered) == 0 {
		return mcp.NewToolResultText("No matches found matching the criteria."), nil
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("### Search Results for '%s':\n\n", query))
	currentTokenCount := 0

	for i, r := range filtered {
		tokens := indexer.EstimateTokens(r.Content)
		if currentTokenCount+tokens > maxTokens {
			out.WriteString("... (truncating further results to stay within context window)")
			break
		}

		lineRange := ""
		if start, ok := r.Metadata["start_line"]; ok {
			if end, ok := r.Metadata["end_line"]; ok {
				lineRange = fmt.Sprintf(" (Lines %s-%s)", start, end)
			}
		}

		out.WriteString(fmt.Sprintf("#### Result %d: %s%s\n", i+1, r.Metadata["path"], lineRange))
		if cat := r.Metadata["category"]; cat != "" {
			out.WriteString(fmt.Sprintf("- **Category**: %s\n", cat))
		}
		if syms := r.Metadata["symbols"]; syms != "" {
			out.WriteString(fmt.Sprintf("- **Entities**: %s\n", syms))
		}
		out.WriteString(fmt.Sprintf("```\n%s\n```\n\n", r.Content))
		currentTokenCount += tokens
	}

	return mcp.NewToolResultText(out.String()), nil
}

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

func (s *Server) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	handler, ok := s.toolHandlers[name]
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", name)
	}

	// Create a CallToolRequest from the args
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	return handler(ctx, req)
}

func (s *Server) ListTools() []mcp.Tool {
	var tools []mcp.Tool
	for _, t := range s.MCPServer.ListTools() {
		tools = append(tools, t.Tool)
	}
	return tools
}
