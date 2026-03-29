/*
 * Package mcp provides the core implementation of the Model Context Protocol (MCP) server.
 * It manages tool registration, request handling, and integrates with the vector database
 * for semantic search and project context management.
 */
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/daemon"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
	"github.com/nilesh32236/vector-mcp-go/internal/lsp"
	"github.com/nilesh32236/vector-mcp-go/internal/mutation"
)

// Searcher defines the interface for searching the vector database.
type Searcher interface {
	Search(ctx context.Context, embedding []float32, topK int, projectIDs []string, category string) ([]db.Record, error)
	SearchWithScore(ctx context.Context, queryEmbedding []float32, topK int, projectIDs []string, category string) ([]db.Record, []float32, error)
	HybridSearch(ctx context.Context, query string, queryEmbedding []float32, topK int, projectIDs []string, category string) ([]db.Record, error)
	LexicalSearch(ctx context.Context, query string, topK int, projectIDs []string, category string) ([]db.Record, error)
}

// StatusProvider defines the interface for project status monitoring.
type StatusProvider interface {
	GetStatus(ctx context.Context, projectID string) (string, error)
	GetAllStatuses(ctx context.Context) (map[string]string, error)
}

// StoreManager defines the interface for managing project data and record lifecycle.
type StoreManager interface {
	Insert(ctx context.Context, records []db.Record) error
	DeleteByPrefix(ctx context.Context, prefix string, projectID string) error
	ClearProject(ctx context.Context, projectID string) error
	GetPathHashMapping(ctx context.Context, projectID string) (map[string]string, error)
	GetAllRecords(ctx context.Context) ([]db.Record, error)
	GetAllMetadata(ctx context.Context) ([]db.Record, error)
	GetByPath(ctx context.Context, path string, projectID string) ([]db.Record, error)
	GetByPrefix(ctx context.Context, prefix string, projectID string) ([]db.Record, error)
	Count() int64
}

// IndexerStore defines the composite interface for database operations,
// allowing both local and remote implementations to be used interchangeably.
type IndexerStore interface {
	Searcher
	StatusProvider
	StoreManager
}

// Server is the core MCP server implementation. It manages the lifecycle of
// the MCP server, registers available tools, and routes incoming tool calls
// to their respective handlers.
type Server struct {
	cfg              *config.Config                                                                                 // Server configuration
	logger           *slog.Logger                                                                                   // Structured logger
	MCPServer        *server.MCPServer                                                                              // Underlying MCP server instance
	localStoreGetter func(ctx context.Context) (*db.Store, error)                                                   // Function to get local store
	remoteStore      IndexerStore                                                                                   // Optional remote store implementation
	embedder         indexer.Embedder                                                                               // Embedding engine for semantic operations
	indexQueue       chan string                                                                                    // Queue for background indexing tasks
	daemonClient     *daemon.Client                                                                                 // Client for master daemon communication
	progressMap      *sync.Map                                                                                      // Thread-safe map for tracking indexing progress
	watcherResetChan chan string                                                                                    // Channel to signal file watcher resets
	monorepoResolver *indexer.WorkspaceResolver                                                                     // Resolver for monorepo package structures
	lspManager       *lsp.LSPManager                                                                                // High-precision language server manager
	safety           *mutation.SafetyChecker                                                                        // Safety checker for mutation integrity
	graph            *db.KnowledgeGraph                                                                             // Code relationship graph for reasoning
	toolHandlers     map[string]func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) // Map of tool names to handlers
}

// NewServer initializes and returns a new Server instance.
// It sets up the internal MCP server and registers all supported tools, resources, and prompts.
func NewServer(cfg *config.Config, logger *slog.Logger, storeGetter func(ctx context.Context) (*db.Store, error), embedder indexer.Embedder, queue chan string, daemonClient *daemon.Client, progress *sync.Map, resetChan chan string, resolver *indexer.WorkspaceResolver, lspManager *lsp.LSPManager, safety *mutation.SafetyChecker) *Server {
	s := server.NewMCPServer("vector-mcp-go", "1.0.0", server.WithLogging())
	srv := &Server{
		cfg:              cfg,
		logger:           logger,
		MCPServer:        s,
		localStoreGetter: storeGetter,
		embedder:         embedder,
		indexQueue:       queue,
		daemonClient:     daemonClient,
		progressMap:      progress,
		watcherResetChan: resetChan,
		monorepoResolver: resolver,
		lspManager:       lspManager,
		safety:           safety,
		graph:            db.NewKnowledgeGraph(),
		toolHandlers:     make(map[string]func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)),
	}
	srv.registerResources()
	srv.registerPrompts()
	srv.registerTools()
	return srv
}

// WithRemoteStore sets a remote store for the server, enabling it to act as a slave
// and delegate database operations to a master instance.
func (s *Server) WithRemoteStore(rs IndexerStore) {
	s.remoteStore = rs
}

// getStore returns the active store implementation. It prefers the remote store
// if configured; otherwise, it uses the local store getter.
func (s *Server) getStore(ctx context.Context) (IndexerStore, error) {
	if s.remoteStore != nil {
		return s.remoteStore, nil
	}
	return s.localStoreGetter(ctx)
}

// PopulateGraph builds the structural knowledge graph from all records in the store.
func (s *Server) PopulateGraph(ctx context.Context) error {
	s.logger.Info("Populating structural knowledge graph")

	store, err := s.getStore(ctx)
	if err != nil {
		return fmt.Errorf("failed to get store for graph population: %w", err)
	}

	records, err := store.GetAllRecords(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch records for graph: %w", err)
	}

	s.graph.Populate(records)
	s.logger.Info("Knowledge graph populated", "node_count", len(records))
	return nil
}

// Serve starts the MCP server on stdio.
func (s *Server) Serve() error {
	s.logger.Info("MCP Server listening on stdio...")
	return server.ServeStdio(s.MCPServer)
}

// registerResources defines and registers all available MCP resources.
func (s *Server) registerResources() {
	// 1. index://status
	s.MCPServer.AddResource(mcp.NewResource("index://status", "Indexing Status",
		mcp.WithResourceDescription("Current indexing status and background progress diagnostics."),
		mcp.WithMIMEType("application/json"),
	), func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		status := "Idle"
		if s.progressMap != nil {
			if val, ok := s.progressMap.Load(s.cfg.ProjectRoot); ok {
				status = val.(string)
			}
		}

		store, _ := s.getStore(ctx)
		count := int64(0)
		if store != nil {
			count = store.Count()
		}

		data := map[string]interface{}{
			"project_root": s.cfg.ProjectRoot,
			"status":       status,
			"record_count": count,
			"is_master":    s.remoteStore == nil,
			"model":        s.cfg.ModelName,
		}
		jsonBytes, _ := json.MarshalIndent(data, "", "  ")

		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "index://status",
				MIMEType: "application/json",
				Text:     string(jsonBytes),
			},
		}, nil
	})

	// 2. config://project
	s.MCPServer.AddResource(mcp.NewResource("config://project", "Project Configuration",
		mcp.WithResourceDescription("Active configuration for the vector-mcp-go server."),
		mcp.WithMIMEType("application/json"),
	), func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		jsonBytes, _ := json.MarshalIndent(s.cfg, "", "  ")
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "config://project",
				MIMEType: "application/json",
				Text:     string(jsonBytes),
			},
		}, nil
	})

	// 3. docs://guide
	s.MCPServer.AddResource(mcp.NewResource("docs://guide", "Usage Guide",
		mcp.WithResourceDescription("Quick guide on how to use vector-mcp-go effectively."),
		mcp.WithMIMEType("text/markdown"),
	), func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		guide := `
# Vector MCP Go Usage Guide

This server provides semantic search and code analysis for your project.

## Core Resources
- **index://status**: Check if indexing is complete.
- **config://project**: View active server settings.

## Recommended Prompts
- **generate-docstring**: Use this to write high-quality documentation for functions or classes.
- **analyze-architecture**: Get a visual overview of your project structure.

## Key Tools
- **search_codebase**: Your primary tool for semantic search.
- **get_related_context**: Best for understanding a specific file's dependencies.
- **trigger_project_index**: Run this if you've made major changes to ensure the index is fresh.
`
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "docs://guide",
				MIMEType: "text/markdown",
				Text:     guide,
			},
		}, nil
	})
}

// registerPrompts defines and registers all available MCP prompts.
func (s *Server) registerPrompts() {
	// 1. generate-docstring
	s.MCPServer.AddPrompt(mcp.NewPrompt("generate-docstring",
		mcp.WithPromptDescription("Generates a highly contextual prompt to write professional documentation."),
		mcp.WithArgument("file_path", mcp.ArgumentDescription("The relative path of the file"), mcp.RequiredArgument()),
		mcp.WithArgument("entity_name", mcp.ArgumentDescription("The name of the function or class to document"), mcp.RequiredArgument()),
	), func(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		filePath := request.Params.Arguments["file_path"]
		entityName := request.Params.Arguments["entity_name"]

		prompt := fmt.Sprintf("Please generate a professional docstring for the entity '%s' in file '%s'. "+
			"Include parameter descriptions, return values, and any relevant implementation details based on the context.",
			entityName, filePath)

		return &mcp.GetPromptResult{
			Description: "Prompt for generating documentation",
			Messages: []mcp.PromptMessage{
				{
					Role: mcp.RoleUser,
					Content: mcp.TextContent{
						Type: "text",
						Text: prompt,
					},
				},
			},
		}, nil
	})

	// 2. analyze-architecture
	s.MCPServer.AddPrompt(mcp.NewPrompt("analyze-architecture",
		mcp.WithPromptDescription("Analyzes the project architecture and generates a summary prompt."),
	), func(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		prompt := "Analyze the current project's architecture. Focus on package boundaries, dependency flow, and key design patterns used. If this is a monorepo, identify the core packages and their interactions."

		return &mcp.GetPromptResult{
			Description: "Architectural analysis prompt",
			Messages: []mcp.PromptMessage{
				{
					Role: mcp.RoleUser,
					Content: mcp.TextContent{
						Type: "text",
						Text: prompt,
					},
				},
			},
		}, nil
	})
}

// registerTools defines and registers all available MCP tools and their handlers.
func (s *Server) registerTools() {
	// Helper to add tool and track handler
	addTool := func(tool mcp.Tool, handler func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)) {
		s.MCPServer.AddTool(tool, handler)
		s.toolHandlers[tool.Name] = handler
	}

	// Tool registration (Note: numbering below is for logical grouping, not strict sequence)

	// 0. ping
	addTool(mcp.NewTool("ping", mcp.WithDescription("Check server connectivity")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("pong"), nil
		})

	// 1. trigger_project_index

	// Unified search_workspace tool

	// Unified lsp_query tool
	addTool(mcp.NewTool("lsp_query",
		mcp.WithDescription("Unified tool for Language Server Protocol (LSP) intelligence. Use this for precise code navigation and impact analysis."),
		mcp.WithString("action", mcp.Description("Required. Type of query: 'definition' (find exact source), 'references' (find all usages), 'type_hierarchy' (interface implementations/struct embedding), 'impact_analysis' (calculate downstream blast radius).")),
		mcp.WithString("path", mcp.Description("Required. The absolute or relative path to the file.")),
		mcp.WithNumber("line", mcp.Description("Required. The line number (0-indexed).")),
		mcp.WithNumber("character", mcp.Description("Required. The character/column number (0-indexed).")),
	), s.handleLspQuery)

	// Unified analyze_code tool
	addTool(mcp.NewTool("analyze_code",
		mcp.WithDescription("Unified tool for codebase analysis and diagnostics. Use this for structural mapping and health checks."),
		mcp.WithString("action", mcp.Description("Required. Type of analysis: 'ast_skeleton' (topological directory map), 'dependencies' (check package.json health), 'duplicate_code' (find structural duplication), 'dead_code' (find unused symbols).")),
		mcp.WithString("path", mcp.Description("Required. The path to the file or directory to analyze.")),
	), s.handleAnalyzeCode)
	addTool(mcp.NewTool("search_workspace",
		mcp.WithDescription("Unified tool for code discovery and search. Use this for all searching and structural mapping."),
		mcp.WithString("action", mcp.Description("Required. Type of search: 'vector' (semantic AI search), 'regex' (exact match/regex), 'graph' (code relationships/interfaces), 'index_status' (check background indexing).")),
		mcp.WithString("query", mcp.Description("Required. The search term, regex pattern, or graph query (e.g., interface name).")),
		mcp.WithNumber("limit", mcp.Description("Optional. Maximum results to return (default 10).")),
		mcp.WithString("path", mcp.Description("Optional. A specific directory or file path to restrict the search.")),
	), s.handleSearchWorkspace)

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

	// 4. delete_context
	addTool(mcp.NewTool("delete_context",
		mcp.WithDescription("Delete specific shared memory context, or completely wipe a project's vector index."),
		mcp.WithString("target_path", mcp.Description("The exact file path, context ID, or 'ALL' to clear the whole project.")),
		mcp.WithString("project_id", mcp.Description("The project ID to target. Defaults to the current project.")),
		mcp.WithBoolean("dry_run", mcp.Description("Optional: If true, returns the list of files that would be deleted without actually deleting them.")),
	), s.handleDeleteContext)

	// 8. generate_docstring_prompt
	addTool(mcp.NewTool("generate_docstring_prompt",
		mcp.WithDescription("Generates a highly contextual prompt to write professional documentation for a specific entity."),
		mcp.WithString("file_path", mcp.Description("The relative path of the file")),
		mcp.WithString("entity_name", mcp.Description("The name of the function or class to document")),
		mcp.WithString("language", mcp.Description("Optional: The language of the file (e.g., 'Go', 'TypeScript', 'Python'). Extracted from file extension if omitted.")),
	), s.handleGenerateDocstringPrompt)

	// 9. analyze_architecture
	addTool(mcp.NewTool("analyze_architecture",
		mcp.WithDescription("Generates a Mermaid.js dependency graph between packages in a monorepo."),
		mcp.WithString("monorepo_prefix", mcp.Description("Optional prefix for monorepo packages (e.g., '@herexa/')")),
	), s.handleAnalyzeArchitecture)

	// 13. get_indexing_diagnostics
	addTool(mcp.NewTool("get_indexing_diagnostics",
		mcp.WithDescription("Provides detailed diagnostics on the indexing process, including recent errors and queue status."),
	), s.handleGetIndexingDiagnostics)

	// 14. verify_implementation_gap
	addTool(mcp.NewTool("verify_implementation_gap",
		mcp.WithDescription("Verifies if things from docs and client feedback are actually implemented in the code."),
		mcp.WithString("query", mcp.Description("The requirement or feedback query to verify (e.g. 'user authentication')")),
	), s.handleVerifyImplementationGap)

	// 15. find_missing_tests
	addTool(mcp.NewTool("find_missing_tests",
		mcp.WithDescription("Identifies exported symbols that lack corresponding test coverage by mapping source to tests."),
	), s.handleFindMissingTests)

	// 16. list_api_endpoints
	addTool(mcp.NewTool("list_api_endpoints",
		mcp.WithDescription("Identifies potential API route definitions in the codebase across various frameworks."),
	), s.handleListAPIEndpoints)

	// 17. get_code_history
	addTool(mcp.NewTool("get_code_history",
		mcp.WithDescription("Retrieves recent git history (last 10 commits) for a specific file to understand its evolution."),
		mcp.WithString("file_path", mcp.Description("The relative path of the file to check history for")),
	), s.handleGetCodeHistory)

	// 18. get_summarized_context
	addTool(mcp.NewTool("get_summarized_context",
		mcp.WithDescription("Retrieves context for a query and returns chunks programmatically."),
		mcp.WithString("query", mcp.Description("The search query to summarize")),
		mcp.WithNumber("topK", mcp.Description("Optional: Number of chunks to include in summary (default 5)")),
	), s.handleGetSummarizedContext)

	// 19. verify_proposed_change
	addTool(mcp.NewTool("verify_proposed_change",
		mcp.WithDescription("Checks a proposed code change or task against stored Knowledge Items and Architectural Decisions to ensure pattern compliance."),
		mcp.WithString("proposed_change", mcp.Description("The description or diff of the proposed change")),
		mcp.WithArray("cross_reference_projects", mcp.Description("Optional list of project IDs to search across"), mcp.WithStringItems()),
	), s.handleVerifyProposedChange)

	// 21. distill_knowledge
	addTool(mcp.NewTool("distill_knowledge",
		mcp.WithDescription("Analyzes a directory and automatically generates a Knowledge Item summary."),
		mcp.WithString("path", mcp.Description("The relative path to analyze")),
	), s.handleDistillKnowledge)

	// 22. apply_code_patch
	addTool(mcp.NewTool("apply_code_patch",
		mcp.WithDescription("Atomically applies a search-and-replace patch to a file. Use this for targeted code modifications after verifying the search string exists and is unique. Always verify the patch integrity afterward."),
		mcp.WithString("path", mcp.Description("Relative path to the target file")),
		mcp.WithString("search", mcp.Description("The EXACT block of code or string to locate. Must be unique.")),
		mcp.WithString("replace", mcp.Description("The new code or string to insert instead.")),
	), s.handleApplyCodePatch)

	// 23. run_linter_and_fix
	addTool(mcp.NewTool("run_linter_and_fix",
		mcp.WithDescription("Executes standard Go formatters (go fmt) or linters with the fix flag. Best for cleaning up code style or resolving trivial linting errors automatically."),
		mcp.WithString("path", mcp.Description("Path to the file or directory to process")),
		mcp.WithString("tool", mcp.Description("Target tool (strictly 'go fmt' for now)")),
	), s.handleRunLinterAndFix)

	// 24. create_file
	addTool(mcp.NewTool("create_file",
		mcp.WithDescription("Scaffolds a new file with specified content. Automatically creates parent directories if they don't exist. Use this for adding new features or splitting code into separate modules."),
		mcp.WithString("path", mcp.Description("Relative path for the new file")),
		mcp.WithString("content", mcp.Description("The text content to write into the file.")),
	), s.handleCreateFile)

	// 28. verify_patch_integrity
	addTool(mcp.NewTool("verify_patch_integrity",
		mcp.WithDescription("Crucial pre-commit safety guard: Checks if an in-memory application of a patch would introduce compiler errors by invoking LSP diagnostics. Always use this before finalizing a mutation."),
		mcp.WithString("path", mcp.Description("Path to the file being edited")),
		mcp.WithString("search", mcp.Description("The exact code segment to replace")),
		mcp.WithString("replace", mcp.Description("The proposed new code segment")),
	), s.handleVerifyPatchIntegrity)

	// 29. auto_fix_mutation
	addTool(mcp.NewTool("auto_fix_mutation",
		mcp.WithDescription("Autonomous repair tool: Suggests a fix for a failed code patch based on LSP diagnostics. Use this when verify_patch_integrity returns errors to get a corrected strategy."),
		mcp.WithString("diagnostic_json", mcp.Description("The JSON-encoded diagnostic object from a failed verification.")),
	), s.handleAutoFixMutation)

	// 31. distill_package_purpose
	addTool(mcp.NewTool("distill_package_purpose",
		mcp.WithDescription("Generates a high-level semantic summary of a package's primary purpose and key entities. This distillation is re-indexed with a 2.0x priority boost to prime the agent's architectural context."),
		mcp.WithString("path", mcp.Description("Relative or absolute path of the package directory to distill.")),
	), s.handleDistillPackagePurpose)

	// 33. trace_data_flow
	addTool(mcp.NewTool("trace_data_flow",
		mcp.WithDescription("Traces the usage of a specific field or symbol across the project to understand data dependencies. High-precision structural analysis."),
		mcp.WithString("field_name", mcp.Description("The name of the field or symbol to trace")),
	), s.handleTraceDataFlow)
}

// SendNotification sends a logging message notification to all connected clients.
func (s *Server) SendNotification(level mcp.LoggingLevel, data any, logger string) {
	params := map[string]any{
		"level": level,
		"data":  data,
	}
	if logger != "" {
		params["logger"] = logger
	}
	s.MCPServer.SendNotificationToAllClients("notifications/message", params)
}

// Notify is a helper to send info-level notifications.
func (s *Server) Notify(message string) {
	s.SendNotification(mcp.LoggingLevelInfo, message, "vector-mcp")
}

// Log is a helper to send debug/log-level notifications.
func (s *Server) Log(level mcp.LoggingLevel, message string) {
	s.SendNotification(level, message, "vector-mcp")
}

// CallTool invokes a registered tool handler by name with the provided arguments.
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

// ListTools returns a list of all tools registered with the MCP server.
func (s *Server) ListTools() []mcp.Tool {
	var tools []mcp.Tool
	for _, t := range s.MCPServer.ListTools() {
		tools = append(tools, t.Tool)
	}
	return tools
}

// GetEmbedder returns the embedding engine used by the server.
func (s *Server) GetEmbedder() indexer.Embedder {
	return s.embedder
}
