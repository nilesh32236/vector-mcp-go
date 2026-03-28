/*
 * Package mcp provides the core implementation of the Model Context Protocol (MCP) server.
 * It manages tool registration, request handling, and integrates with the vector database
 * for semantic search and project context management.
 */
package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/daemon"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
)

// Searcher defines the interface for searching the vector database.
type Searcher interface {
	Search(ctx context.Context, embedding []float32, topK int, projectIDs []string, category string) ([]db.Record, error)
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
	cfg              *config.Config             // Server configuration
	logger           *slog.Logger               // Structured logger
	MCPServer        *server.MCPServer          // Underlying MCP server instance
	storeGetter      func(ctx context.Context) (*db.Store, error) // Function to get local store
	remoteStore      IndexerStore               // Optional remote store implementation
	embedder         indexer.Embedder           // Embedding engine for semantic operations
	indexQueue       chan string                // Queue for background indexing tasks
	daemonClient     *daemon.Client             // Client for master daemon communication
	progressMap      *sync.Map                  // Thread-safe map for tracking indexing progress
	watcherResetChan chan string                // Channel to signal file watcher resets
	monorepoResolver *indexer.WorkspaceResolver // Resolver for monorepo package structures
	toolHandlers     map[string]func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) // Map of tool names to handlers
}

// NewServer initializes and returns a new Server instance.
// It sets up the internal MCP server and registers all supported tools.
func NewServer(cfg *config.Config, logger *slog.Logger, storeGetter func(ctx context.Context) (*db.Store, error), embedder indexer.Embedder, queue chan string, daemonClient *daemon.Client, progress *sync.Map, resetChan chan string, resolver *indexer.WorkspaceResolver) *Server {
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
	return s.storeGetter(ctx)
}

// Serve starts the MCP server on stdio.
func (s *Server) Serve() error {
	s.logger.Info("MCP Server listening on stdio...")
	return server.ServeStdio(s.MCPServer)
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

	// 8. generate_docstring_prompt
	addTool(mcp.NewTool("generate_docstring_prompt",
		mcp.WithDescription("Generates a highly contextual prompt for an LLM to write professional documentation for a specific entity."),
		mcp.WithString("file_path", mcp.Description("The relative path of the file")),
		mcp.WithString("entity_name", mcp.Description("The name of the function or class to document")),
		mcp.WithString("language", mcp.Description("Optional: The language of the file (e.g., 'Go', 'TypeScript', 'Python'). Extracted from file extension if omitted.")),
	), s.handleGenerateDocstringPrompt)

	// 9. analyze_architecture
	addTool(mcp.NewTool("analyze_architecture",
		mcp.WithDescription("Generates a Mermaid.js dependency graph between packages in a monorepo."),
		mcp.WithString("monorepo_prefix", mcp.Description("Optional prefix for monorepo packages (e.g., '@herexa/')")),
	), s.handleAnalyzeArchitecture)

	// 10. find_dead_code
	addTool(mcp.NewTool("find_dead_code",
		mcp.WithDescription("Identifies potentially dead code by finding exported symbols that are never imported or called."),
		mcp.WithArray("exclude_paths", mcp.Description("Optional list of file paths or patterns to exclude from dead code analysis."), mcp.WithStringItems()),
		mcp.WithBoolean("is_library", mcp.Description("Optional: Set to true if analyzing a library where public exports are expected. Only flags unused symbols inside internal/ or marked as private.")),
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
		mcp.WithDescription("Unified semantic and lexical search across the codebase. Replaces retrieve_context and retrieve_docs."),
		mcp.WithString("query", mcp.Description("The natural language search query")),
		mcp.WithString("category", mcp.Description("Optional: 'code' or 'document'. Handles both 'code' and 'document' retrieval. Defaults to searching both.")),
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
